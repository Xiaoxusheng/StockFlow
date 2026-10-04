-- 000009 stockops 域单据表：transfer_orders/transfer_items/count_orders/count_items/count_differences
-- 依据：backend-m2-plan.md §5（000009 冻结 DDL）、§6.6–§6.7（状态机）、business-flow.md §10/§13.4、
--       inventory-rules.md §2/§8（在途口径、盘点序列号逐件）。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：sku/batch 为跨域逻辑引用（batch_id 0=非批次 SKU，
--     000005 同款哨兵约定）；from/to 仓库与库位为 warehouse/bins 逻辑引用 + Service 层校验。
--   * 调拨在途不落在 inventory 列（六列恒等式 CHECK 已于 000005 冻结，在途不入恒等式）：
--     在途数量 = transfer_items 中 TRANSFERRING 单据的 qty_out - qty_in 聚合
--     （plan §5 000009 注，§10.1"在途库存体现"的 M2 落地口径）。
--   * 调拨两端流水齐备（business-flow §10.1 源减目标增），TRANSFER_OUT/TRANSFER_IN 已在
--     000005 change_type 值域内，零迁移改动（plan §8.1）。
--   * 盘点序列号 SKU 逐件登记一行 qty=1（inventory-rules §8.2"盘点必须逐个序列号操作"）：
--     count_items.serial_no 非序列号 SKU 为空串，唯一键含 serial_no（plan §5）。
--   * 状态列全部挂 CHECK 约束（与 plan §6.6/§6.7 状态机同源）；各环节时间字段按 §13.4 落列。

CREATE TABLE transfer_orders (
    id                bigserial   PRIMARY KEY,
    transfer_no       varchar(64) NOT NULL,
    type              varchar(16) NOT NULL,
    from_warehouse_id bigint      NOT NULL,
    to_warehouse_id   bigint      NOT NULL,
    status            varchar(32) NOT NULL DEFAULT 'DRAFT',
    approved_by       bigint      NOT NULL DEFAULT 0,
    approved_at       timestamptz,
    outbound_at       timestamptz,
    received_at       timestamptz,
    cancelled_at      timestamptz,
    remark            text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    created_by        bigint      NOT NULL DEFAULT 0,
    updated_by        bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_transfer_orders_type CHECK (type IN ('WAREHOUSE', 'BIN')),
    CONSTRAINT chk_transfer_orders_status CHECK (status IN ('DRAFT', 'PENDING_APPROVAL', 'APPROVED', 'TRANSFERRING', 'AWAITING_RECEIPT', 'COMPLETED', 'CANCELLED'))
);

COMMENT ON TABLE transfer_orders IS '调拨单（business-flow §10.1：仓库→仓库/库位→库位；六态逐一对应——APPROVED=待出库、AWAITING_RECEIPT=待入库，plan §6.6）';
COMMENT ON COLUMN transfer_orders.type IS '调拨维度（WAREHOUSE 跨仓/BIN 库位间，business-flow §10.1）';
COMMENT ON COLUMN transfer_orders.from_warehouse_id IS '源仓（database.md §6.1：两端仓库均为列表检索条件，各建 (warehouse_id, status) 索引）';
COMMENT ON COLUMN transfer_orders.to_warehouse_id IS '目标仓（同上；库位间调拨两端同仓）';
COMMENT ON COLUMN transfer_orders.approved_at IS '审核时间（business-flow §13.4；完整审批记录落 document_approvals）';
COMMENT ON COLUMN transfer_orders.outbound_at IS '调拨出库时间（business-flow §13.4；TransferOut 落账时点）';
COMMENT ON COLUMN transfer_orders.received_at IS '到货入库时间（business-flow §13.4；TransferIn 落账时点）';
COMMENT ON COLUMN transfer_orders.cancelled_at IS '取消时间（TRANSFERRING/AWAITING_RECEIPT 禁止直接取消——只能反向调拨冲正，business-flow §13.3/plan §6.6）';

CREATE UNIQUE INDEX uk_transfer_orders_no ON transfer_orders (transfer_no);
CREATE INDEX idx_transfer_orders_from_wh_status ON transfer_orders (from_warehouse_id, status);
CREATE INDEX idx_transfer_orders_to_wh_status ON transfer_orders (to_warehouse_id, status);

CREATE TABLE transfer_items (
    id                bigserial      PRIMARY KEY,
    transfer_id       bigint         NOT NULL,
    line_no           integer        NOT NULL,
    sku_id            bigint         NOT NULL,
    batch_id          bigint         NOT NULL DEFAULT 0,
    from_warehouse_id bigint         NOT NULL,
    from_zone_id      bigint         NOT NULL DEFAULT 0,
    from_shelf_id     bigint         NOT NULL DEFAULT 0,
    from_bin_id       bigint         NOT NULL,
    to_warehouse_id   bigint         NOT NULL,
    to_zone_id        bigint         NOT NULL DEFAULT 0,
    to_shelf_id       bigint         NOT NULL DEFAULT 0,
    to_bin_id         bigint         NOT NULL,
    qty               numeric(18, 4) NOT NULL,
    qty_out           numeric(18, 4) NOT NULL DEFAULT 0,
    qty_in            numeric(18, 4) NOT NULL DEFAULT 0,
    remark            text           NOT NULL DEFAULT '',
    created_at        timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    created_by        bigint         NOT NULL DEFAULT 0,
    updated_by        bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_transfer_items_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE transfer_items IS '调拨明细（business-flow §10.1：源仓减少、目标仓增加，两端流水；qty_out/qty_in 落行——在途=qty_out-qty_in，plan §5）';
COMMENT ON COLUMN transfer_items.transfer_id IS '调拨单（transfer_orders 裸 ID 引用，域内同事务创建，不建 FK）';
COMMENT ON COLUMN transfer_items.batch_id IS '批次（batches 逻辑引用；0=非批次 SKU——TransferOut/TransferIn 的 RowKey 维度，plan §8.1）';
COMMENT ON COLUMN transfer_items.from_bin_id IS '源库位（四维冗余源仓/区/架定位，TransferOut RowKey）';
COMMENT ON COLUMN transfer_items.to_bin_id IS '目标库位（四维冗余目标仓/区/架定位，TransferIn RowKey 需 zone/shelf——plan §8.1 needZoneShelf）';

CREATE UNIQUE INDEX uk_transfer_items_transfer_line ON transfer_items (transfer_id, line_no);
CREATE INDEX idx_transfer_items_sku ON transfer_items (sku_id);

CREATE TABLE count_orders (
    id           bigserial   PRIMARY KEY,
    count_no     varchar(64) NOT NULL,
    warehouse_id bigint      NOT NULL,
    scope        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       varchar(32) NOT NULL DEFAULT 'DRAFT',
    frozen_at    timestamptz,
    reviewed_at  timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    remark       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint      NOT NULL DEFAULT 0,
    updated_by   bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_count_orders_status CHECK (status IN ('DRAFT', 'COUNTING', 'PENDING_REVIEW', 'COMPLETED', 'CANCELLED'))
);

COMMENT ON TABLE count_orders IS '盘点单（business-flow §10.2：创建→冻结→实盘→差异→审核→调整；硬性规则——差异必须走调整单+审批链路，禁止直接改库存）';
COMMENT ON COLUMN count_orders.warehouse_id IS '盘点仓（数据权限过滤；全盘/按仓/区/架/位/SKU 范围快照落 scope）';
COMMENT ON COLUMN count_orders.scope IS '盘点范围快照（business-flow §10.2 全盘/按仓/区/架/位/SKU 的 jsonb 范围声明，plan §6.7 DRAFT→COUNTING 时冻结快照）';
COMMENT ON COLUMN count_orders.frozen_at IS '冻结时间（plan §6.7：Lock(COUNT_FREEZE) 与 qty_system 快照时点）';
COMMENT ON COLUMN count_orders.reviewed_at IS '差异审核时间（business-flow §13.4）';

CREATE UNIQUE INDEX uk_count_orders_no ON count_orders (count_no);
CREATE INDEX idx_count_orders_warehouse_status ON count_orders (warehouse_id, status);

CREATE TABLE count_items (
    id               bigserial      PRIMARY KEY,
    count_id         bigint         NOT NULL,
    inventory_row_id bigint         NOT NULL,
    sku_id           bigint         NOT NULL,
    warehouse_id     bigint         NOT NULL,
    zone_id          bigint         NOT NULL DEFAULT 0,
    shelf_id         bigint         NOT NULL DEFAULT 0,
    bin_id           bigint         NOT NULL,
    qty_system       numeric(18, 4) NOT NULL DEFAULT 0,
    qty_counted      numeric(18, 4),
    counted_by       bigint         NOT NULL DEFAULT 0,
    counted_at       timestamptz,
    serial_no        varchar(128)   NOT NULL DEFAULT '',
    created_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at       timestamptz    NOT NULL DEFAULT now(),
    created_by       bigint         NOT NULL DEFAULT 0,
    updated_by       bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_count_items_qty_counted CHECK (qty_counted IS NULL OR qty_counted >= 0)
);

COMMENT ON TABLE count_items IS '盘点明细（plan §5：qty_system 冻结时快照/qty_counted 实盘登记；实盘登记为按 row+serial 幂等 PUT 覆盖——plan §6.7/§10.2）';
COMMENT ON COLUMN count_items.inventory_row_id IS '库存行（inventory 裸 ID 引用，冻结锁与解冻的定位键）';
COMMENT ON COLUMN count_items.bin_id IS '库位（四维冗余仓/区/架——盘点范围与差异定位维度）';
COMMENT ON COLUMN count_items.qty_counted IS '实盘数量（NULL=尚未登记；实盘后 ≥0，PUT 幂等覆盖）';
COMMENT ON COLUMN count_items.counted_at IS '实盘登记时间（business-flow §13.4）';
COMMENT ON COLUMN count_items.serial_no IS '序列号（序列号 SKU 逐件登记一行 qty=1——inventory-rules §8.2；非序列号 SKU 为空串，plan §5）';

CREATE UNIQUE INDEX uk_count_items_row_serial ON count_items (count_id, inventory_row_id, serial_no);
CREATE INDEX idx_count_items_count_row ON count_items (count_id, inventory_row_id);

CREATE TABLE count_differences (
    id           bigserial      PRIMARY KEY,
    count_id     bigint         NOT NULL,
    line_no      integer        NOT NULL,
    sku_id       bigint         NOT NULL,
    warehouse_id bigint         NOT NULL,
    bin_id       bigint         NOT NULL,
    batch_id     bigint         NOT NULL DEFAULT 0,
    qty_system   numeric(18, 4) NOT NULL DEFAULT 0,
    qty_counted  numeric(18, 4) NOT NULL DEFAULT 0,
    diff_qty     numeric(18, 4) NOT NULL DEFAULT 0,
    adjust_no    varchar(64)    NOT NULL DEFAULT '',
    status       varchar(16)    NOT NULL DEFAULT 'PENDING',
    remark       text           NOT NULL DEFAULT '',
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    created_by   bigint         NOT NULL DEFAULT 0,
    updated_by   bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_count_differences_qty_non_negative CHECK (qty_system >= 0 AND qty_counted >= 0),
    CONSTRAINT chk_count_differences_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXECUTED'))
);

COMMENT ON TABLE count_differences IS '盘点差异（business-flow §10.2：系统对比生成，差异审核通过后生成调整单并回写 adjust_no——plan §6.7 解冻与调整同事务原子）';
COMMENT ON COLUMN count_differences.bin_id IS '库位（差异定位维度；batch_id 0=非批次 SKU——Adjust RowKey 五维）';
COMMENT ON COLUMN count_differences.diff_qty IS '差异数量（qty_counted - qty_system，盘盈为正——plan §5）';
COMMENT ON COLUMN count_differences.adjust_no IS '差异批准后生成的调整单单号（inventory_adjustments.adjustment_no 逻辑引用，空串=未生成）';

CREATE UNIQUE INDEX uk_count_differences_count_line ON count_differences (count_id, line_no);
CREATE INDEX idx_count_differences_count_status ON count_differences (count_id, status);
