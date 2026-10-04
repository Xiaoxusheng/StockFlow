-- 000013 设备与扫码表：devices / device_configs / scan_logs / device_logs / app_versions
-- 依据：backend-m3-plan.md §5（000013 冻结 DDL）、§8（设备管理与统一扫码解析）、
--       devices.md §6–§7/§13、scanner.md §5–§7。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：warehouse_id/bound_user_id 为跨域裸 ID 引用 +
--     Service 层校验（plan §5 000013 注）；device_id 为 devices 裸 ID 引用。
--   * scan_logs/device_logs 为 append-only 审计表（devices §13.2"谁、在哪台设备、什么时候、
--     扫了什么、执行了什么"）：纯日志表按 database.md §3 但书无 updated_by/deleted_at，
--     仅 created_at；应用层无 UPDATE/DELETE 通路，db/grants/app_grants.sql 分层（仅 SELECT+INSERT）。
--   * scan_logs.device_id/device_code 可空（NULL）：Web HID 场景以用户 JWT 调用、无设备行，
--     审计以 user_id/username/ip 归因（plan §8.3、devices §13.2 注）。
--   * 设备令牌撤销语义：token_version 停用/解绑重置/重新生成激活码即 +1 全量失效
--     （plan §8.2，设备令牌 TTL 365 天无刷新端点，失效由 token_version 承担）。
--   * app_versions 为架构预留表（devices §7.4）：M3 建表 + latest 查询，无写入端点
--     （APK 上传/OTA 随 scan 域立项——plan §15）。

CREATE TABLE devices (
    id                    bigserial    PRIMARY KEY,
    code                  varchar(64)  NOT NULL,
    name                  varchar(128) NOT NULL,
    type                  varchar(16)  NOT NULL,
    brand                 varchar(64)  NOT NULL DEFAULT '',
    model                 varchar(64)  NOT NULL DEFAULT '',
    os                    varchar(32)  NOT NULL DEFAULT '',
    warehouse_id          bigint       NOT NULL DEFAULT 0,
    bound_user_id         bigint       NOT NULL DEFAULT 0,
    status                varchar(16)  NOT NULL DEFAULT 'ENABLED',
    activation_status     varchar(16)  NOT NULL DEFAULT 'PENDING',
    activation_token_hash varchar(128) NOT NULL DEFAULT '',
    activation_expires_at timestamptz,
    activated_at          timestamptz,
    activated_by          bigint       NOT NULL DEFAULT 0,
    app_version           varchar(32)  NOT NULL DEFAULT '',
    last_online_at        timestamptz,
    last_scan_at          timestamptz,
    battery_level         integer,
    ip                    varchar(64)  NOT NULL DEFAULT '',
    token_version         integer      NOT NULL DEFAULT 1,
    remark                text         NOT NULL DEFAULT '',
    created_at            timestamptz  NOT NULL DEFAULT now(),
    updated_at            timestamptz  NOT NULL DEFAULT now(),
    created_by            bigint       NOT NULL DEFAULT 0,
    updated_by            bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_devices_type CHECK (type IN ('pc', 'pad', 'pda', 'scanner', 'printer')),
    CONSTRAINT chk_devices_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT chk_devices_activation_status CHECK (activation_status IN ('PENDING', 'ACTIVATED')),
    CONSTRAINT chk_devices_battery_level CHECK (battery_level IS NULL OR (battery_level >= 0 AND battery_level <= 100)),
    CONSTRAINT chk_devices_token_version CHECK (token_version >= 1)
);

COMMENT ON TABLE devices IS '设备档案（devices §6：注册/二维码激活/绑定仓库/心跳/配置下发；code 如 SF-SCAN-001 管理端命名，非 docnum 单号）';
COMMENT ON COLUMN devices.type IS '设备类型（devices §6：pc/pad/pda/scanner/printer 小写值域，与前端设备中心分组一致）';
COMMENT ON COLUMN devices.warehouse_id IS '绑定仓库（warehouses 裸 ID 引用 + Service 校验，不建 FK；数据权限维度）';
COMMENT ON COLUMN devices.bound_user_id IS '绑定操作者（users 裸 ID 引用；devices §6.4 共享设备硬性规则：业务操作记录真实操作者）';
COMMENT ON COLUMN devices.activation_token_hash IS '一次性激活 token 哈希（15 分钟有效、单次消费——plan §8.2；不存明文，architecture §6 日志红线同源）';
COMMENT ON COLUMN devices.activated_at IS '激活时间（PENDING→ACTIVATED）';
COMMENT ON COLUMN devices.last_online_at IS '最近心跳时间（在线判定 = 3 分钟内有心跳，plan §8.2；idx 支持离线判定扫描）';
COMMENT ON COLUMN devices.battery_level IS '电量百分比（0–100；NULL=未上报）';
COMMENT ON COLUMN devices.token_version IS '设备令牌版本（停用/解绑重置/重新生成激活码即 +1，旧设备令牌全失效——plan §8.2）';

CREATE UNIQUE INDEX uk_devices_code ON devices (code);
CREATE INDEX idx_devices_type_status ON devices (type, status);
CREATE INDEX idx_devices_warehouse ON devices (warehouse_id);
CREATE INDEX idx_devices_last_online ON devices (last_online_at);

CREATE TABLE device_configs (
    id         bigserial  PRIMARY KEY,
    device_id  bigint     NOT NULL,
    config     jsonb,
    version    integer    NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint     NOT NULL DEFAULT 0,
    updated_by bigint     NOT NULL DEFAULT 0,
    CONSTRAINT chk_device_configs_version CHECK (version >= 1),
    CONSTRAINT chk_device_logs_level CHECK (level IN ('INFO', 'WARN', 'ERROR'))
);

COMMENT ON TABLE device_configs IS '设备配置（devices §7.3 下发项：扫码模式/声音/震动/自动聚焦/连续扫码/扫码超时/默认仓库/任务刷新间隔/自动锁屏；device_id UNIQUE 一机一配置，version 单调递增 +1，心跳响应携带）';
COMMENT ON COLUMN device_configs.device_id IS '设备（devices 裸 ID 引用 + UNIQUE，不建 FK）';
COMMENT ON COLUMN device_configs.version IS '配置版本（单调递增，PUT 下发即 +1；设备端比对心跳响应的 config_version 决定拉取——plan §8.2）';

CREATE UNIQUE INDEX uk_device_configs_device ON device_configs (device_id);
CREATE INDEX idx_device_configs_version ON device_configs (version);

CREATE TABLE scan_logs (
    id           bigserial    PRIMARY KEY,
    device_id    bigint,
    device_code  varchar(64),
    user_id      bigint       NOT NULL DEFAULT 0,
    username     varchar(64)  NOT NULL DEFAULT '',
    ip           varchar(64)  NOT NULL DEFAULT '',
    warehouse_id bigint       NOT NULL DEFAULT 0,
    raw_code     varchar(255) NOT NULL,
    symbology    varchar(32)  NOT NULL DEFAULT '',
    resolve_type varchar(32)  NOT NULL DEFAULT '',
    resolve_id   bigint       NOT NULL DEFAULT 0,
    resolve_code varchar(128) NOT NULL DEFAULT '',
    page         varchar(128) NOT NULL DEFAULT '',
    business_no  varchar(64)  NOT NULL DEFAULT '',
    success      boolean      NOT NULL DEFAULT TRUE,
    error_code   varchar(64)  NOT NULL DEFAULT '',
    created_at   timestamptz  NOT NULL DEFAULT now()
);

COMMENT ON TABLE scan_logs IS '扫码审计（devices §13.2：谁、在哪台设备、什么时候、扫了什么、执行了什么——append-only，应用层仅 INSERT，grants 分层无 UPDATE/DELETE）';
COMMENT ON COLUMN scan_logs.device_id IS '设备（devices 裸 ID 引用；NULL=PC Web HID 场景无设备行，审计以 user_id/username/ip 归因——plan §8.3）';
COMMENT ON COLUMN scan_logs.device_code IS '设备编码冗余（NULL 同 device_id；免 join 供管理端列表直读）';
COMMENT ON COLUMN scan_logs.user_id IS '操作者（users 裸 ID 引用；共享设备记录真实操作者——devices §6.4）';
COMMENT ON COLUMN scan_logs.warehouse_id IS '作业仓（devices 上下文或操作者数据权限仓库；0=未定位）';
COMMENT ON COLUMN scan_logs.raw_code IS '原始码值（resolve 输入原样留存，审计可复现）';
COMMENT ON COLUMN scan_logs.symbology IS '码制（scanner 接入层识别，Web HID 可空）';
COMMENT ON COLUMN scan_logs.resolve_type IS '解析类型（doc/barcode/bin/serial/batch/unknown 等，/api/scanner/resolve 输出 type）';
COMMENT ON COLUMN scan_logs.resolve_id IS '命中对象 ID（0=未命中/多命中未定位）';
COMMENT ON COLUMN scan_logs.resolve_code IS '命中对象编码（多命中列表场景落首个命中或空）';
COMMENT ON COLUMN scan_logs.page IS '发起页面（前端上下文，便于还原操作场景）';
COMMENT ON COLUMN scan_logs.business_no IS '关联业务单号（跳转目标单据，逻辑引用不建 FK）';
COMMENT ON COLUMN scan_logs.success IS '解析是否成功（FALSE 时 error_code 记原因：UNKNOWN_BARCODE/*_NOT_FOUND 等——scanner §6.1）';

CREATE INDEX idx_scan_logs_device_created ON scan_logs (device_id, created_at);
CREATE INDEX idx_scan_logs_user_created ON scan_logs (user_id, created_at);
CREATE INDEX idx_scan_logs_created_at ON scan_logs (created_at);
CREATE INDEX idx_scan_logs_raw_code ON scan_logs (raw_code);

CREATE TABLE device_logs (
    id          bigserial   PRIMARY KEY,
    device_id   bigint      NOT NULL,
    level       varchar(8)  NOT NULL,
    event_type  varchar(64) NOT NULL DEFAULT '',
    message     text        NOT NULL DEFAULT '',
    context     jsonb,
    occurred_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE device_logs IS '设备运行日志（设备端批量上报 ≤100 条/次——plan §8.2；append-only，应用层仅 INSERT，grants 分层）';
COMMENT ON COLUMN device_logs.device_id IS '设备（devices 裸 ID 引用，不建 FK）';
COMMENT ON COLUMN device_logs.occurred_at IS '事件发生时间（设备端时钟；批量上报历史日志时区别于服务端 created_at）';

CREATE INDEX idx_device_logs_device_occurred ON device_logs (device_id, occurred_at);
CREATE INDEX idx_device_logs_level_occurred ON device_logs (level, occurred_at);

CREATE TABLE app_versions (
    id                bigserial    PRIMARY KEY,
    platform          varchar(16)  NOT NULL,
    version_code      integer      NOT NULL,
    version_name      varchar(32)  NOT NULL,
    release_notes     text         NOT NULL DEFAULT '',
    file_url          varchar(512) NOT NULL DEFAULT '',
    min_supported_code integer     NOT NULL DEFAULT 0,
    force_update      boolean      NOT NULL DEFAULT FALSE,
    status            varchar(16)  NOT NULL DEFAULT 'DRAFT',
    published_at      timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint       NOT NULL DEFAULT 0,
    updated_by        bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_app_versions_platform CHECK (platform IN ('android')),
    CONSTRAINT chk_app_versions_version_code CHECK (version_code >= 1),
    CONSTRAINT chk_app_versions_status CHECK (status IN ('DRAFT', 'PUBLISHED', 'DEPRECATED'))
);

COMMENT ON TABLE app_versions IS 'App 版本（devices §7.4 架构预留：M3 建表 + latest 查询，无上传/OTA 写入端点——plan §15，升级执行随 scan 域立项）';
COMMENT ON COLUMN app_versions.file_url IS '安装包地址（架构预留，M3 不做上传；latest 查询无已发布行返回 404——plan §15）';
COMMENT ON COLUMN app_versions.min_supported_code IS '最低支持 version_code（低于即提示升级，force_update 强制）';

CREATE UNIQUE INDEX uk_app_versions_platform_code ON app_versions (platform, version_code);
