-- 000010 returns 域单据表：return_orders/return_items/exceptions
-- 依据：backend-m2-plan.md §5（000010 冻结 DDL）、§6.9–§6.10（状态机）、business-flow.md §9/§11.2/§13.4、
--       inventory-rules.md §4/§10（异常冻结、追溯不建新表）。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：source_no 为销售/采购单号逻辑单号引用；
--     customer/supplier/sku/bin 为跨域逻辑引用 + Service 层校验。
--   * 采购退货不建 outbound_order（plan §6.9：退货出库在 returns 域内闭环，追溯经库存流水
--     business_no；出库状态 SHIPPED 落在 return_orders 状态机内）。
--   * 追溯（场景 7）不建新表（plan §5 000010 注）：追溯 = 库存流水 + 业务单据 + 操作日志
--     的查询编排（GET /api/inventory/trace），任何"追溯中间表"都是第二真相来源，禁止。
--   * 异常冻结联动：freeze_lock_id 可空回指 inventory_locks（EXCEPTION_FREEZE），
--     RESOLVED/CLOSED 必须释放（inventory-rules §4.2）。
--   * 单据不做软删除：作废/取消/关闭走状态机（business-flow §13.3）。

CREATE TABLE return_orders (
    id           bigserial   PRIMARY KEY,
    return_no    varchar(64) NOT NULL,
    type         varchar(16) NOT NULL,
    source_no    varchar(64) NOT NULL DEFAULT '',
    customer_id  bigint      NOT NULL DEFAULT 0,
    supplier_id  bigint      NOT NULL DEFAULT 0,
    warehouse_id bigint      NOT NULL,
    status       varchar(32) NOT NULL DEFAULT 'DRAFT',
    approved_by  bigint      NOT NULL DEFAULT 0,
    approved_at  timestamptz,
    received_at  timestamptz,
    qc_at        timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    remark       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint      NOT NULL DEFAULT 0,
    updated_by   bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_return_orders_type CHECK (type IN ('SALES', 'PURCHASE')),
    CONSTRAINT chk_return_orders_status CHECK (status IN ('DRAFT', 'PENDING_APPROVAL', 'APPROVED', 'RECEIVING', 'IN_QC', 'SHIPPED', 'COMPLETED', 'CANCELLED'))
);

COMMENT ON TABLE return_orders IS '退货单（business-flow §9：销售退货 收货→质检→正常/不良；采购退货 审核→退货出库，不建 outbound_order——plan §6.9）';
COMMENT ON COLUMN return_orders.type IS '退货类型（SALES 销售退货/PURCHASE 采购退货，plan §5）';
COMMENT ON COLUMN return_orders.source_no IS '来源单号（销售/采购单号逻辑引用，idx 支持按来源单追溯与可退数量校验——plan §3.1 Reader 窄接口）';
COMMENT ON COLUMN return_orders.customer_id IS '客户（masterdata 逻辑引用；销售退货必填，采购退货为 0）';
COMMENT ON COLUMN return_orders.supplier_id IS '供应商（masterdata 逻辑引用；采购退货必填，销售退货为 0）';
COMMENT ON COLUMN return_orders.warehouse_id IS '退货仓（收货/退货出库作业仓，数据权限过滤）';
COMMENT ON COLUMN return_orders.status IS '状态机（plan §6.9：销售退货 DRAFT→PENDING_APPROVAL→APPROVED→RECEIVING→IN_QC→COMPLETED；采购退货 APPROVED→SHIPPED→COMPLETED；未收货/未出库前可 CANCELLED）';
COMMENT ON COLUMN return_orders.qc_at IS '质检时间（business-flow §13.4；销售退货质检经 QCCreator 窄接口落 quality_orders）';

CREATE UNIQUE INDEX uk_return_orders_no ON return_orders (return_no);
CREATE INDEX idx_return_orders_type_status ON return_orders (type, status);
CREATE INDEX idx_return_orders_source_no ON return_orders (source_no);

CREATE TABLE return_items (
    id            bigserial      PRIMARY KEY,
    return_id     bigint         NOT NULL,
    line_no       integer        NOT NULL,
    sku_id        bigint         NOT NULL,
    qty_return    numeric(18, 4) NOT NULL,
    qty_received  numeric(18, 4) NOT NULL DEFAULT 0,
    qty_inspected numeric(18, 4) NOT NULL DEFAULT 0,
    qty_defective numeric(18, 4) NOT NULL DEFAULT 0,
    reason        text           NOT NULL DEFAULT '',
    remark        text           NOT NULL DEFAULT '',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_return_items_qty_return_positive CHECK (qty_return > 0)
);

COMMENT ON TABLE return_items IS '退货明细（business-flow §9：部分退货——qty_received/qty_inspected/qty_defective 各环节累计；逐行退量由 returns 域流水累计比对来源单可退量，plan §3.1）';
COMMENT ON COLUMN return_items.return_id IS '退货单（return_orders 裸 ID 引用，域内同事务创建，不建 FK）';
COMMENT ON COLUMN return_items.reason IS '退货原因（business-flow §9.1 退货申请必填原因口径，由应用层校验）';

CREATE UNIQUE INDEX uk_return_items_return_line ON return_items (return_id, line_no);
CREATE INDEX idx_return_items_sku ON return_items (sku_id);

CREATE TABLE exceptions (
    id             bigserial   PRIMARY KEY,
    exception_no   varchar(64) NOT NULL,
    type           varchar(16) NOT NULL,
    source_type    varchar(32) NOT NULL DEFAULT '',
    source_no      varchar(64) NOT NULL DEFAULT '',
    sku_id         bigint      NOT NULL DEFAULT 0,
    bin_id         bigint      NOT NULL DEFAULT 0,
    serial_no      varchar(128) NOT NULL DEFAULT '',
    status         varchar(16) NOT NULL DEFAULT 'OPEN',
    detail         text        NOT NULL DEFAULT '',
    assignee_id    bigint      NOT NULL DEFAULT 0,
    assignee_name  varchar(64) NOT NULL DEFAULT '',
    owner_id       bigint      NOT NULL DEFAULT 0,
    owner_name     varchar(64) NOT NULL DEFAULT '',
    handle_records jsonb       NOT NULL DEFAULT '[]'::jsonb,
    image_refs     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    freeze_lock_id bigint,
    assigned_at    timestamptz,
    resolved_at    timestamptz,
    closed_at      timestamptz,
    remark         text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint      NOT NULL DEFAULT 0,
    updated_by     bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_exceptions_type CHECK (type IN ('收货异常', '质检异常', '上架异常', '库存异常', '拣货异常', '复核异常', '物流异常', '盘点异常', '系统异常')),
    CONSTRAINT chk_exceptions_status CHECK (status IN ('OPEN', 'ASSIGNED', 'PROCESSING', 'PENDING_REVIEW', 'RESOLVED', 'CLOSED'))
);

COMMENT ON TABLE exceptions IS '异常单（business-flow §11.2 九类异常统一生命周期：发现→创建→分派→处理中→待复核→已解决→已关闭；由各域经 plan §3.1 ExceptionCreator 窄接口创建，异常中心 CRUD 归 returns 域）';
COMMENT ON COLUMN exceptions.type IS '异常类型（business-flow §11.2 九值中文值域，与 M1 中文 CHECK 值域同风格）';
COMMENT ON COLUMN exceptions.source_type IS '来源单据类型（收货/拣货/复核/盘点等产生环节的单据类型，与 source_no 配对定位）';
COMMENT ON COLUMN exceptions.source_no IS '来源单号（逻辑单号引用，idx 支持按来源单聚合异常）';
COMMENT ON COLUMN exceptions.sku_id IS 'SKU 定位（可空定位：0=未定位到 SKU）';
COMMENT ON COLUMN exceptions.bin_id IS '库位定位（可空定位：0=未定位到库位）';
COMMENT ON COLUMN exceptions.serial_no IS '序列号定位（可空定位：空串=非序列号问题）';
COMMENT ON COLUMN exceptions.handle_records IS '处理记录（business-flow §11.2 追加式 jsonb 数组，不覆盖历史；处理人/责任人/时间由应用层结构化落列）';
COMMENT ON COLUMN exceptions.image_refs IS '异常图片（business-flow §3.4/§11.2；文件中心阶段 14 前仅占位列，不提供上传接口——plan §5/§12）';
COMMENT ON COLUMN exceptions.freeze_lock_id IS '异常冻结锁（inventory_locks 逻辑引用回指 EXCEPTION_FREEZE 锁，跨域不建 FK；NULL=未冻结——创建时可选冻结，RESOLVED/CLOSED 必须释放，inventory-rules §4.2）';
COMMENT ON COLUMN exceptions.owner_name IS '责任人（business-flow §11.2 异常单支持责任人）';
COMMENT ON COLUMN exceptions.assigned_at IS '分派时间（plan §6.10 OPEN→ASSIGNED）';
COMMENT ON COLUMN exceptions.resolved_at IS '解决时间（plan §6.10 RESOLVED）';
COMMENT ON COLUMN exceptions.closed_at IS '关闭时间（plan §6.10 CLOSED）';

CREATE UNIQUE INDEX uk_exceptions_no ON exceptions (exception_no);
CREATE INDEX idx_exceptions_type_status ON exceptions (type, status);
CREATE INDEX idx_exceptions_source ON exceptions (source_type, source_no);
