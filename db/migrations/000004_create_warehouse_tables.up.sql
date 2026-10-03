-- 000004 warehouse 域：warehouses/zones/shelves/bins（仓库 → 库区 → 货架 → 库位 四级结构）
-- 依据：backend-m1-plan.md §6.1/§6.2、database.md §3/§4/§5.1、business-flow.md §1.6。
-- 约定：
--   * 软删除 deleted_at 仅 warehouses/bins（database.md §5.1）；zones/shelves 走 status 停用。
--   * 域内四级外键齐建（plan §6.4：域内 FK 表达）；跨域外键不建（如 bins 被库存/序列号引用）。
--   * 库位编码仓内唯一：unique(warehouse_id, code)（plan §6.2 契约，非部分索引——库位编码
--     含软删行在内仓内保留，避免历史库存/流水的库位编码被复用混淆追溯）。

CREATE TABLE warehouses (
    id              bigserial      PRIMARY KEY,
    code            varchar(64)    NOT NULL,
    name            varchar(255)   NOT NULL,
    address         varchar(512)   NOT NULL DEFAULT '',
    contact         varchar(64)    NOT NULL DEFAULT '',
    phone           varchar(32)    NOT NULL DEFAULT '',
    area            numeric(18, 4) NOT NULL DEFAULT 0,
    capacity        numeric(18, 4) NOT NULL DEFAULT 0,
    type            varchar(32)    NOT NULL DEFAULT 'NORMAL',
    status          varchar(16)    NOT NULL DEFAULT 'ENABLED',
    manager_user_id bigint         NOT NULL DEFAULT 0,
    created_at      timestamptz    NOT NULL DEFAULT now(),
    updated_at      timestamptz    NOT NULL DEFAULT now(),
    created_by      bigint         NOT NULL DEFAULT 0,
    updated_by      bigint         NOT NULL DEFAULT 0,
    deleted_at      timestamptz,
    CONSTRAINT chk_warehouses_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE warehouses IS '仓库（business-flow §1.6 第一级；manager_user_id 为用户表逻辑引用，跨域不建 FK）';
COMMENT ON COLUMN warehouses.type IS '仓库类型（默认 NORMAL 正常仓；值域随业务扩展由应用层校验）';

CREATE UNIQUE INDEX uk_warehouses_code ON warehouses (code) WHERE deleted_at IS NULL;

CREATE TABLE zones (
    id            bigserial      PRIMARY KEY,
    warehouse_id  bigint         NOT NULL,
    code          varchar(64)    NOT NULL,
    name          varchar(255)   NOT NULL,
    zone_type     varchar(32)    NOT NULL DEFAULT 'STORAGE',
    capacity      numeric(18, 4) NOT NULL DEFAULT 0,
    status        varchar(16)    NOT NULL DEFAULT 'ENABLED',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT fk_zones_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT chk_zones_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE zones IS '库区（business-flow §1.6 第二级；STORAGE 存储/PICKING 拣货/RECEIVING 收货暂存等，由应用层校验）';

CREATE UNIQUE INDEX uk_zones_warehouse_code ON zones (warehouse_id, code);
CREATE INDEX idx_zones_warehouse_id ON zones (warehouse_id);

CREATE TABLE shelves (
    id            bigserial      PRIMARY KEY,
    warehouse_id  bigint         NOT NULL,
    zone_id       bigint         NOT NULL,
    code          varchar(64)    NOT NULL,
    layers        integer        NOT NULL DEFAULT 1,
    columns       integer        NOT NULL DEFAULT 1,
    capacity      numeric(18, 4) NOT NULL DEFAULT 0,
    status        varchar(16)    NOT NULL DEFAULT 'ENABLED',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT fk_shelves_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT fk_shelves_zone FOREIGN KEY (zone_id) REFERENCES zones (id),
    CONSTRAINT chk_shelves_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE shelves IS '货架（business-flow §1.6 第三级；layers 层数 / columns 列数，均为非保留字可直接作列名）';
COMMENT ON COLUMN shelves.columns IS '货架列数（COLUMNS 为 PostgreSQL 非保留关键字，可作列名）';

CREATE UNIQUE INDEX uk_shelves_zone_code ON shelves (zone_id, code);
CREATE INDEX idx_shelves_warehouse_id ON shelves (warehouse_id);
CREATE INDEX idx_shelves_zone_id ON shelves (zone_id);

CREATE TABLE bins (
    id                bigserial      PRIMARY KEY,
    warehouse_id      bigint         NOT NULL,
    zone_id           bigint         NOT NULL,
    shelf_id          bigint         NOT NULL,
    layer             integer        NOT NULL DEFAULT 1,
    column_no         integer        NOT NULL DEFAULT 1,
    code              varchar(64)    NOT NULL,
    bin_type          varchar(32)    NOT NULL DEFAULT 'PICK',
    max_capacity      numeric(18, 4) NOT NULL DEFAULT 0,
    current_capacity  numeric(18, 4) NOT NULL DEFAULT 0,
    status            varchar(16)    NOT NULL DEFAULT 'ENABLED',
    created_at        timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    created_by        bigint         NOT NULL DEFAULT 0,
    updated_by        bigint         NOT NULL DEFAULT 0,
    deleted_at        timestamptz,
    CONSTRAINT fk_bins_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT fk_bins_zone FOREIGN KEY (zone_id) REFERENCES zones (id),
    CONSTRAINT fk_bins_shelf FOREIGN KEY (shelf_id) REFERENCES shelves (id),
    CONSTRAINT chk_bins_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE bins IS '库位（business-flow §1.6 第四级；库存五维定位 warehouse+bin(+sku+batch) 的锚点）';
COMMENT ON COLUMN bins.bin_type IS '库位类型（PICK 拣货位/STORAGE 存储位/RECEIVE 收货位等，由应用层校验）';
COMMENT ON COLUMN bins.current_capacity IS '当前占用容量（由上架/移库业务维护，禁止直改）';

CREATE UNIQUE INDEX uk_bins_warehouse_code ON bins (warehouse_id, code);
CREATE INDEX idx_bins_zone_id ON bins (zone_id);
CREATE INDEX idx_bins_shelf_id ON bins (shelf_id);
CREATE INDEX idx_bins_status ON bins (status);
