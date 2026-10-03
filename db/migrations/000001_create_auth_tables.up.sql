-- 000001 auth 域：users/roles/permissions/departments + user_roles/role_permissions/user_warehouses
-- 依据：backend-m1-plan.md §6.1/§6.2（冻结 DDL 契约）、database.md §3（通用字段）/§5.1（软删除仅 users）、
--       permission.md §2/§4（RBAC + 数据权限五范围）。
-- 约定：
--   * 通用字段 id/created_at/updated_at/created_by/updated_by（created_by/updated_by=0 表示系统），
--     软删除 deleted_at 仅 users（database.md §5.1 清单）；join 表仅 created_at/created_by。
--   * 时间一律 timestamptz；金额/数量数值列一律 numeric（本域无）。
--   * 跨域外键不建（user_warehouses.warehouse_id → warehouses 归 000004，见 plan §6.4）；
--     域内外键（departments/permissions 自引用、users→departments、join 表）在域内表达。
--   * golang-migrate 每个迁移文件在单事务内执行，文件内不写 BEGIN/COMMIT。

CREATE TABLE departments (
    id          bigserial    PRIMARY KEY,
    parent_id   bigint,
    code        varchar(64)  NOT NULL,
    name        varchar(64)  NOT NULL,
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT fk_departments_parent FOREIGN KEY (parent_id) REFERENCES departments (id),
    CONSTRAINT chk_departments_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE departments IS '部门（permission.md §4 部门维度数据权限；parent_id 自引用构成部门树）';

-- code 唯一（down 引用 + seed ON CONFLICT (code) 依赖；departments 无软删除，全量唯一）。
-- golang-migrate 每迁移单事务，本文件补索引与建表同事务执行，历史库未在真库初始化过（changelog 披露），无存量风险。
CREATE UNIQUE INDEX uk_departments_code ON departments (code);
CREATE INDEX idx_departments_parent_id ON departments (parent_id);

CREATE TABLE roles (
    id          bigserial    PRIMARY KEY,
    code        varchar(64)  NOT NULL,
    name        varchar(64)  NOT NULL,
    is_system   boolean      NOT NULL DEFAULT FALSE,
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_roles_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE roles IS '角色（permission.md §1：16 个内置角色 is_system=TRUE 禁删）';
COMMENT ON COLUMN roles.is_system IS '内置角色标记：TRUE 禁止删除';

-- code 唯一（down 引用 + seed ON CONFLICT (code) 依赖；roles 无软删除，全量唯一）。
CREATE UNIQUE INDEX uk_roles_code ON roles (code);

CREATE TABLE permissions (
    id          bigserial    PRIMARY KEY,
    code        varchar(128) NOT NULL,
    name        varchar(64)  NOT NULL,
    type        varchar(16)  NOT NULL,
    parent_id   bigint,
    sort        integer      NOT NULL DEFAULT 0,
    status      varchar(16)  NOT NULL DEFAULT 'ENABLED',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT fk_permissions_parent FOREIGN KEY (parent_id) REFERENCES permissions (id),
    CONSTRAINT chk_permissions_type CHECK (type IN ('MENU', 'BUTTON', 'API')),
    CONSTRAINT chk_permissions_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE permissions IS '权限点（permission.md §2：覆盖 MENU/BUTTON/API 三级；编码按 域:资源[:动作]，backend-m1-plan §5.4.1 全量冻结）';

-- code 唯一 + parent_id 反查（down 引用 + seed ON CONFLICT (code) 依赖；permissions 无软删除）。
CREATE UNIQUE INDEX uk_permissions_code ON permissions (code);
CREATE INDEX idx_permissions_parent_id ON permissions (parent_id);

CREATE TABLE users (
    id                   bigserial    PRIMARY KEY,
    username             varchar(64)  NOT NULL,
    password_hash        varchar(100) NOT NULL, -- bcrypt（cost 12，architecture.md §6）
    real_name            varchar(64)  NOT NULL DEFAULT '',
    phone                varchar(32)  NOT NULL DEFAULT '',
    email                varchar(128) NOT NULL DEFAULT '',
    department_id        bigint,
    data_scope           varchar(32)  NOT NULL DEFAULT 'ALL',
    status               varchar(16)  NOT NULL DEFAULT 'ACTIVE',
    must_change_password boolean      NOT NULL DEFAULT FALSE,
    locked_until         timestamptz,
    last_login_at        timestamptz,
    last_login_ip        varchar(64)  NOT NULL DEFAULT '',
    created_at           timestamptz  NOT NULL DEFAULT now(),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint       NOT NULL DEFAULT 0,
    updated_by           bigint       NOT NULL DEFAULT 0,
    deleted_at           timestamptz,
    CONSTRAINT fk_users_department FOREIGN KEY (department_id) REFERENCES departments (id),
    CONSTRAINT chk_users_data_scope CHECK (data_scope IN ('ALL', 'SPECIFIED_WAREHOUSE', 'DEPARTMENT', 'SELF', 'SELF_IN_CHARGE')),
    CONSTRAINT chk_users_status CHECK (status IN ('ACTIVE', 'DISABLED'))
);

COMMENT ON TABLE users IS '用户（permission.md §3/§4；登录失败锁定落库 locked_until，失败计数在 Redis）';
COMMENT ON COLUMN users.data_scope IS '数据权限范围：ALL/SPECIFIED_WAREHOUSE/DEPARTMENT/SELF/SELF_IN_CHARGE（permission.md §4）';
COMMENT ON COLUMN users.must_change_password IS '初始密码强制修改标记（database.md §8.1）';
COMMENT ON COLUMN users.locked_until IS '连续登录失败锁定截止时间（permission.md §3.2）';

-- username 唯一：部分唯一索引实现“软删后编码可复用”（plan §6.2）
CREATE UNIQUE INDEX uk_users_username ON users (username) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_department_id ON users (department_id);

CREATE TABLE user_roles (
    user_id     bigint      NOT NULL,
    role_id     bigint      NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, role_id)
);

COMMENT ON TABLE user_roles IS '用户-角色关联（RBAC：用户 → 角色 → 权限点）';

CREATE TABLE role_permissions (
    role_id        bigint      NOT NULL,
    permission_id  bigint      NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (role_id, permission_id)
);

COMMENT ON TABLE role_permissions IS '角色-权限点关联';

CREATE TABLE user_warehouses (
    user_id       bigint      NOT NULL,
    warehouse_id  bigint      NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, warehouse_id)
);

COMMENT ON TABLE user_warehouses IS '用户-仓库绑定（permission.md §4 SPECIFIED_WAREHOUSE 数据权限的仓库集；warehouse_id 外键归 000004，跨文件不建 FK，plan §6.4）';

-- 域内关联表外键 + 反向查询索引（database.md §6.1：列表查询条件字段建索引）
ALTER TABLE user_roles ADD CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id) REFERENCES users (id);
ALTER TABLE user_roles ADD CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id) REFERENCES roles (id);
ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_role FOREIGN KEY (role_id) REFERENCES roles (id);
ALTER TABLE role_permissions ADD CONSTRAINT fk_role_permissions_permission FOREIGN KEY (permission_id) REFERENCES permissions (id);
ALTER TABLE user_warehouses ADD CONSTRAINT fk_user_warehouses_user FOREIGN KEY (user_id) REFERENCES users (id);

CREATE INDEX idx_user_roles_role_id ON user_roles (role_id);
CREATE INDEX idx_role_permissions_permission_id ON role_permissions (permission_id);
CREATE INDEX idx_user_warehouses_warehouse_id ON user_warehouses (warehouse_id);
