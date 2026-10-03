-- 000005 inventory 域：batches/serial_numbers/inventory/inventory_locks/inventory_ledgers/inventory_adjustments
-- 依据：backend-m1-plan.md §6.2/§8、database.md §3/§4/§6、inventory-rules.md §2/§3/§4/§5/§8/§9。
-- 约定：
--   * 五维库存定位（inventory-rules §3）：sku + warehouse + zone + shelf + bin (+ batch + serial)。
--     serial 维度经 serial_numbers 表定位（一物一行），inventory 行不内嵌 serial_id；
--     batch_id 统一 NOT NULL DEFAULT 0（0=非批次 SKU）——不用可空列，避免 PG 唯一索引把
--     NULL 视为互异导致同一库位同 SKU 出现多行；batches.id 从 1 起不冲突（plan §6.2）。
--   * 数量一律 numeric(18,4)（plan §1 禁 float）；inventory 六列恒等式与非负由 CHECK 约束
--     在数据库层强制（inventory-rules §2：total = available + locked + frozen + pending_inspect + defective）。
--   * 本域无任何跨域外键（sku/warehouse/bin 引用 masterdata/warehouse 域；supplier_id 同理），
--     一致性由 Service 层校验（plan §6.4）；域内 batch_id 亦不建 FK（0 哨兵值无法过 FK）。
--   * inventory_ledgers 为 append-only 审计数据（inventory-rules §5、database.md §7）：
--     仅 created_at，无 updated_at/updated_by/deleted_at；DB 账号分层见 db/grants/app_grants.sql。
--   * GORM 模型仅可定义/导出于 internal/inventory（plan §4.2 判据 3）。

CREATE TABLE batches (
    id              bigserial      PRIMARY KEY,
    sku_id          bigint         NOT NULL,
    batch_no        varchar(64)    NOT NULL,
    supplier_id     bigint         NOT NULL DEFAULT 0,
    production_date date,
    inbound_date    date,
    expiry_date     date,
    cost_price      numeric(18, 4) NOT NULL DEFAULT 0,
    remark          text           NOT NULL DEFAULT '',
    created_at      timestamptz    NOT NULL DEFAULT now(),
    updated_at      timestamptz    NOT NULL DEFAULT now(),
    created_by      bigint         NOT NULL DEFAULT 0,
    updated_by      bigint         NOT NULL DEFAULT 0
);

COMMENT ON TABLE batches IS '批次（inventory-rules §6：启用批次管理的 SKU 按批次记库存；FIFO/FEFO 出库策略命中维度）';
COMMENT ON COLUMN batches.supplier_id IS '供应商（masterdata 逻辑引用，跨域不建 FK；0=未指定）';
COMMENT ON COLUMN batches.expiry_date IS '有效期（效期预警阈值扫描与 FEFO 出库的热点索引列）';

CREATE UNIQUE INDEX uk_batches_sku_batch_no ON batches (sku_id, batch_no);
CREATE INDEX idx_batches_expiry_date ON batches (expiry_date);

CREATE TABLE serial_numbers (
    id               bigserial    PRIMARY KEY,
    serial_no        varchar(128) NOT NULL,
    sku_id           bigint       NOT NULL,
    batch_id         bigint       NOT NULL DEFAULT 0,
    warehouse_id     bigint       NOT NULL DEFAULT 0,
    bin_id           bigint       NOT NULL DEFAULT 0,
    status           varchar(32)  NOT NULL DEFAULT 'IN_STOCK',
    last_source_type varchar(64)  NOT NULL DEFAULT '',
    last_source_no   varchar(64)  NOT NULL DEFAULT '',
    last_event_at    timestamptz,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    updated_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint       NOT NULL DEFAULT 0,
    updated_by       bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_serial_numbers_status CHECK (status IN ('IN_STOCK', 'LOCKED', 'OUTBOUND', 'RETURNED', 'FROZEN'))
);

COMMENT ON TABLE serial_numbers IS '序列号（inventory-rules §8：全局唯一、一物一行、记录完整生命周期；仓库/库位 0=不在库）';
COMMENT ON COLUMN serial_numbers.last_source_no IS '最近一次状态变化的来源单据号（与 last_source_type 配对构成追溯指针）';

CREATE UNIQUE INDEX uk_serial_numbers_serial_no ON serial_numbers (serial_no);
CREATE INDEX idx_serial_numbers_sku_status ON serial_numbers (sku_id, status);
CREATE INDEX idx_serial_numbers_warehouse_bin ON serial_numbers (warehouse_id, bin_id);

CREATE TABLE inventory (
    id                  bigserial      PRIMARY KEY,
    warehouse_id        bigint         NOT NULL,
    zone_id             bigint         NOT NULL,
    shelf_id            bigint         NOT NULL,
    bin_id              bigint         NOT NULL,
    sku_id              bigint         NOT NULL,
    batch_id            bigint         NOT NULL DEFAULT 0,
    total_qty           numeric(18, 4) NOT NULL DEFAULT 0,
    available_qty       numeric(18, 4) NOT NULL DEFAULT 0,
    locked_qty          numeric(18, 4) NOT NULL DEFAULT 0,
    frozen_qty          numeric(18, 4) NOT NULL DEFAULT 0,
    pending_inspect_qty numeric(18, 4) NOT NULL DEFAULT 0,
    defective_qty       numeric(18, 4) NOT NULL DEFAULT 0,
    created_at          timestamptz    NOT NULL DEFAULT now(),
    updated_at          timestamptz    NOT NULL DEFAULT now(),
    created_by          bigint         NOT NULL DEFAULT 0,
    updated_by          bigint         NOT NULL DEFAULT 0,
    -- 恒等式 + 非负：数据库层最后防线（backend-m1-plan §8.1；inventory-rules §2 一致性恒等式）
    CONSTRAINT chk_inventory_identity CHECK (
        total_qty >= 0 AND available_qty >= 0 AND locked_qty >= 0
        AND frozen_qty >= 0 AND pending_inspect_qty >= 0 AND defective_qty >= 0
        AND total_qty = available_qty + locked_qty + frozen_qty + pending_inspect_qty + defective_qty
    )
);

COMMENT ON TABLE inventory IS '实时库存（五维唯一：同仓同位同批同 SKU 恰一行，行锁粒度精确；唯一变更入口 internal/inventory.Service，inventory-rules §1）';
COMMENT ON COLUMN inventory.batch_id IS '批次（batches 逻辑引用，跨域不建 FK；0=非批次 SKU）';

-- 五维唯一索引：定位维度 warehouse+bin+sku+batch（zone/shelf 随 bin 冗余，便于按区/架聚合）
CREATE UNIQUE INDEX uk_inventory_location ON inventory (warehouse_id, bin_id, sku_id, batch_id);
CREATE INDEX idx_inventory_sku_id ON inventory (sku_id);
CREATE INDEX idx_inventory_warehouse_sku ON inventory (warehouse_id, sku_id);

CREATE TABLE inventory_locks (
    id            bigserial      PRIMARY KEY,
    warehouse_id  bigint         NOT NULL,
    bin_id        bigint         NOT NULL,
    sku_id        bigint         NOT NULL,
    batch_id      bigint         NOT NULL DEFAULT 0,
    lock_type     varchar(32)    NOT NULL,
    source_type   varchar(64)    NOT NULL,
    source_no     varchar(64)    NOT NULL,
    qty           numeric(18, 4) NOT NULL,
    status        varchar(16)    NOT NULL DEFAULT 'ACTIVE',
    released_at   timestamptz,
    released_by   bigint         NOT NULL DEFAULT 0,
    remark        text           NOT NULL DEFAULT '',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_inventory_locks_lock_type CHECK (lock_type IN ('ORDER_HOLD', 'COUNT_FREEZE', 'QC_FREEZE', 'MANUAL_FREEZE', 'EXCEPTION_FREEZE')),
    CONSTRAINT chk_inventory_locks_status CHECK (status IN ('ACTIVE', 'RELEASED', 'CONSUMED')),
    CONSTRAINT chk_inventory_locks_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE inventory_locks IS '库存锁定（inventory-rules §4：记录来源单据/类型/数量，释放须由明确业务动作触发并生成流水）';
COMMENT ON COLUMN inventory_locks.source_no IS '来源单据号（与 source_type 配对；按来源幂等查重的索引键）';

CREATE INDEX idx_inventory_locks_sku_wh_status ON inventory_locks (sku_id, warehouse_id, status);
CREATE INDEX idx_inventory_locks_source ON inventory_locks (source_type, source_no);

CREATE TABLE inventory_ledgers (
    id              bigserial      PRIMARY KEY,
    ledger_no       varchar(64)    NOT NULL,
    sku_id          bigint         NOT NULL,
    warehouse_id    bigint         NOT NULL,
    zone_id         bigint,
    shelf_id        bigint,
    bin_id          bigint         NOT NULL,
    batch_id        bigint         NOT NULL DEFAULT 0,
    serial_no       varchar(128)   NOT NULL DEFAULT '',
    change_type     varchar(32)    NOT NULL,
    business_type   varchar(64)    NOT NULL,
    business_no     varchar(64)    NOT NULL,
    status_from     varchar(32)    NOT NULL,
    status_to       varchar(32)    NOT NULL,
    qty_before      numeric(18, 4) NOT NULL DEFAULT 0,
    qty_change      numeric(18, 4) NOT NULL DEFAULT 0,
    qty_after       numeric(18, 4) NOT NULL DEFAULT 0,
    idempotency_key varchar(128),
    operator_id     bigint         NOT NULL DEFAULT 0,
    operator_name   varchar(64)    NOT NULL DEFAULT '',
    request_id      varchar(64)    NOT NULL DEFAULT '',
    remark          text           NOT NULL DEFAULT '',
    created_at      timestamptz    NOT NULL DEFAULT now(),
    CONSTRAINT chk_inventory_ledgers_change_type CHECK (change_type IN ('INBOUND', 'OUTBOUND', 'TRANSFER_OUT', 'TRANSFER_IN', 'LOCK', 'RELEASE', 'MOVE', 'INSPECT_PASS', 'INSPECT_DEFECTIVE', 'ADJUST')),
    CONSTRAINT chk_inventory_ledgers_status_from CHECK (status_from IN ('available', 'locked', 'frozen', 'pending_inspect', 'defective')),
    CONSTRAINT chk_inventory_ledgers_status_to CHECK (status_to IN ('available', 'locked', 'frozen', 'pending_inspect', 'defective'))
);

COMMENT ON TABLE inventory_ledgers IS '库存流水（inventory-rules §5：只增不改不删 append-only；与库存变更同事务提交；审计数据，DB 账号无 UPDATE/DELETE 权限）';
COMMENT ON COLUMN inventory_ledgers.qty_before IS '受影响状态列（status_from 所指列）变化前数量；total 变化型操作 total 同步 ±qty_change（plan §8.4 冻结口径）';
COMMENT ON COLUMN inventory_ledgers.idempotency_key IS '幂等键（重复变更返回既有结果，不重复扣减；NULL=无幂等要求）';

-- 流水号唯一；幂等键部分唯一索引（NULL 不参与，plan §8.5）
CREATE UNIQUE INDEX uk_inventory_ledgers_ledger_no ON inventory_ledgers (ledger_no);
CREATE UNIQUE INDEX uk_inventory_ledgers_idempotency_key ON inventory_ledgers (idempotency_key) WHERE idempotency_key IS NOT NULL;
-- 主查询路径（plan §6.3：按时间分区/归档策略预留，M1 不提前分区）
CREATE INDEX idx_inventory_ledgers_sku_created ON inventory_ledgers (sku_id, created_at);
CREATE INDEX idx_inventory_ledgers_warehouse_created ON inventory_ledgers (warehouse_id, created_at);
CREATE INDEX idx_inventory_ledgers_business_no ON inventory_ledgers (business_no);

CREATE TABLE inventory_adjustments (
    id             bigserial      PRIMARY KEY,
    adjustment_no  varchar(64)    NOT NULL,
    warehouse_id   bigint         NOT NULL,
    sku_id         bigint         NOT NULL,
    bin_id         bigint         NOT NULL,
    batch_id       bigint         NOT NULL DEFAULT 0,
    adjust_type    varchar(16)    NOT NULL,
    qty            numeric(18, 4) NOT NULL,
    reason         text           NOT NULL,
    status         varchar(32)    NOT NULL DEFAULT 'DRAFT',
    approved_by    bigint         NOT NULL DEFAULT 0,
    approved_at    timestamptz,
    executed_by    bigint         NOT NULL DEFAULT 0,
    executed_at    timestamptz,
    created_at     timestamptz    NOT NULL DEFAULT now(),
    updated_at     timestamptz    NOT NULL DEFAULT now(),
    created_by     bigint         NOT NULL DEFAULT 0,
    updated_by     bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_inventory_adjustments_adjust_type CHECK (adjust_type IN ('盘盈', '盘亏', '损耗', '报废', '其他')),
    CONSTRAINT chk_inventory_adjustments_status CHECK (status IN ('DRAFT', 'PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'EXECUTED', 'CANCELLED'))
);

COMMENT ON TABLE inventory_adjustments IS '库存调整单（business-flow §11.1：申请必填原因 → 审核 → 执行 → 生成流水；M1 只落 schema 与 Service 执行入口，审批流 UI/接口随 M2）';
COMMENT ON COLUMN inventory_adjustments.adjust_type IS '调整类型（business-flow §11.1 中文值域：盘盈/盘亏/损耗/报废/其他）';
COMMENT ON COLUMN inventory_adjustments.status IS '状态机（business-flow §13.2：DRAFT→PENDING_APPROVAL→APPROVED→EXECUTED，驳回 REJECTED，作废 CANCELLED）';

CREATE UNIQUE INDEX uk_inventory_adjustments_no ON inventory_adjustments (adjustment_no);
CREATE INDEX idx_inventory_adjustments_warehouse_status ON inventory_adjustments (warehouse_id, status);
