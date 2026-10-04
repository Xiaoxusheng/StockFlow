-- 000007 purchase 域单据表：purchase_orders/purchase_order_items/inbound_orders/inbound_items/
--       receipts/receipt_items/putaway_tasks/quality_orders/quality_items
-- 依据：backend-m2-plan.md §5（000007 冻结 DDL）、§6.1–§6.3（状态机）、business-flow.md §2–§5/§13.4、
--       inventory-rules.md §6/§7（批次/序列号采集）、api.md §4。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：supplier/sku/warehouse/bin 为跨域逻辑引用（裸 ID + Service
--     层校验），inbound/receipt/putaway 链路内引用走 *_no 逻辑单号或裸 ID + 索引。
--   * 数量/金额一律 numeric(18,4)（禁 float）；采购四量约束（business-flow §2.3）落列于
--     purchase_order_items，超量收货由收货服务强校验并回滚。
--   * 状态列全部挂 CHECK 约束（与 plan §6 状态机同源，迁移是状态机第二道防线）；
--     各环节时间字段按 business-flow §13.4 落列，不能只存 created_at。
--   * receipts 为事件型表（plan §6.3：一次性生效、幂等键防重，无状态机）；
--     idempotency_key 部分唯一索引承接"用户连续点击两次不能执行两次"（architecture §3.2）。
--   * 明细表 SKU 索引为 database.md §6.1 列表条件字段（追溯/按 SKU 查询）的统一落列；
--     其余索引严格按 plan §5 冻结清单。
--   * 单据不做软删除（database.md §5.1 清单外）：作废/取消/关闭走状态机（business-flow §13.3）。

CREATE TABLE purchase_orders (
    id           bigserial      PRIMARY KEY,
    po_no        varchar(64)    NOT NULL,
    supplier_id  bigint         NOT NULL,
    warehouse_id bigint         NOT NULL,
    total_amount numeric(18, 4) NOT NULL DEFAULT 0,
    status       varchar(32)    NOT NULL DEFAULT 'DRAFT',
    approved_by  bigint         NOT NULL DEFAULT 0,
    approved_at  timestamptz,
    received_at  timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    remark       text           NOT NULL DEFAULT '',
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    created_by   bigint         NOT NULL DEFAULT 0,
    updated_by   bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_purchase_orders_status CHECK (status IN ('DRAFT', 'PENDING_APPROVAL', 'APPROVED', 'PARTIAL_RECEIVED', 'RECEIVED_ALL', 'COMPLETED', 'CANCELLED'))
);

COMMENT ON TABLE purchase_orders IS '采购订单（business-flow §2：M2 采购链路源头，采购申请不做——plan §12）';
COMMENT ON COLUMN purchase_orders.supplier_id IS '供应商（masterdata 逻辑引用，跨域不建 FK；SupplierChecker 校验启用）';
COMMENT ON COLUMN purchase_orders.warehouse_id IS '收货仓（plan §5：必填——数据权限过滤与到货校验依据）';
COMMENT ON COLUMN purchase_orders.total_amount IS '原始金额（business-flow §2.1；明细行金额汇总，由 Service 层计算）';
COMMENT ON COLUMN purchase_orders.status IS '状态机（plan §6.1 与 business-flow §2.2 同源，无 CLOSED——差额关闭走 COMPLETED+审计原因）';
COMMENT ON COLUMN purchase_orders.approved_at IS '审核时间（business-flow §13.4；approved_by 冗余供列表展示，完整审批记录落 document_approvals）';
COMMENT ON COLUMN purchase_orders.received_at IS '收货时间（business-flow §13.4）';
COMMENT ON COLUMN purchase_orders.completed_at IS '完成时间（business-flow §13.4）';
COMMENT ON COLUMN purchase_orders.cancelled_at IS '取消时间（business-flow §13.3 作废/取消留痕）';

CREATE UNIQUE INDEX uk_purchase_orders_no ON purchase_orders (po_no);
CREATE INDEX idx_purchase_orders_supplier ON purchase_orders (supplier_id);
CREATE INDEX idx_purchase_orders_warehouse_status ON purchase_orders (warehouse_id, status);

CREATE TABLE purchase_order_items (
    id           bigserial      PRIMARY KEY,
    po_id        bigint         NOT NULL,
    line_no      integer        NOT NULL,
    sku_id       bigint         NOT NULL,
    qty_ordered  numeric(18, 4) NOT NULL,
    qty_received numeric(18, 4) NOT NULL DEFAULT 0,
    qty_rejected numeric(18, 4) NOT NULL DEFAULT 0,
    qty_putaway  numeric(18, 4) NOT NULL DEFAULT 0,
    price        numeric(18, 4) NOT NULL DEFAULT 0,
    amount       numeric(18, 4) NOT NULL DEFAULT 0,
    remark       text           NOT NULL DEFAULT '',
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    created_by   bigint         NOT NULL DEFAULT 0,
    updated_by   bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_purchase_order_items_qty_ordered_positive CHECK (qty_ordered > 0)
);

COMMENT ON TABLE purchase_order_items IS '采购订单明细（business-flow §2.3 四量约束落列：原始/已到货/拒收/已上架，收货服务强校验防超量收货）';
COMMENT ON COLUMN purchase_order_items.po_id IS '采购订单（purchase_orders 裸 ID 引用，域内同事务创建，不建 FK）';
COMMENT ON COLUMN purchase_order_items.qty_putaway IS '已上架数量（对应 plan §6.1 RECEIVED_ALL→COMPLETED 的上架任务完成判定）';

CREATE UNIQUE INDEX uk_purchase_order_items_po_line ON purchase_order_items (po_id, line_no);
CREATE INDEX idx_purchase_order_items_sku ON purchase_order_items (sku_id);

CREATE TABLE inbound_orders (
    id           bigserial   PRIMARY KEY,
    inbound_no   varchar(64) NOT NULL,
    source_type  varchar(16) NOT NULL,
    source_no    varchar(64) NOT NULL DEFAULT '',
    warehouse_id bigint      NOT NULL,
    status       varchar(32) NOT NULL DEFAULT 'DRAFT',
    received_at  timestamptz,
    inspected_at timestamptz,
    putaway_at   timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    remark       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint      NOT NULL DEFAULT 0,
    updated_by   bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_inbound_orders_source_type CHECK (source_type IN ('PURCHASE', 'OTHER')),
    CONSTRAINT chk_inbound_orders_status CHECK (status IN ('DRAFT', 'RECEIVING', 'AWAITING_QC', 'AWAITING_PUTAWAY', 'COMPLETED', 'CANCELLED', 'CLOSED'))
);

COMMENT ON TABLE inbound_orders IS '入库单（business-flow §3.2 收货→质检→上架骨架；M2 实际入口=采购入库/其他入库，退货/调拨入库由对应域单据承载——plan §6.2）';
COMMENT ON COLUMN inbound_orders.source_type IS '来源类型（PURCHASE 采购入库/OTHER 其他入库，plan §5 冻结值域）';
COMMENT ON COLUMN inbound_orders.source_no IS '来源单号（PO 单号逻辑引用，idx(source_type, source_no) 支持按来源单追溯）';
COMMENT ON COLUMN inbound_orders.status IS '状态机（plan §6.2：DRAFT→RECEIVING→AWAITING_QC→AWAITING_PUTAWAY→COMPLETED，差额关闭 CLOSED，取消 CANCELLED）';
COMMENT ON COLUMN inbound_orders.inspected_at IS '质检时间（business-flow §13.4）';
COMMENT ON COLUMN inbound_orders.putaway_at IS '上架时间（business-flow §13.4）';

CREATE UNIQUE INDEX uk_inbound_orders_no ON inbound_orders (inbound_no);
CREATE INDEX idx_inbound_orders_warehouse_status ON inbound_orders (warehouse_id, status);
CREATE INDEX idx_inbound_orders_source ON inbound_orders (source_type, source_no);

CREATE TABLE inbound_items (
    id            bigserial      PRIMARY KEY,
    inbound_id    bigint         NOT NULL,
    line_no       integer        NOT NULL,
    sku_id        bigint         NOT NULL,
    qty           numeric(18, 4) NOT NULL,
    qty_received  numeric(18, 4) NOT NULL DEFAULT 0,
    qty_inspected numeric(18, 4) NOT NULL DEFAULT 0,
    qty_putaway   numeric(18, 4) NOT NULL DEFAULT 0,
    remark        text           NOT NULL DEFAULT '',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_inbound_items_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE inbound_items IS '入库单明细（business-flow §3.3 部分收货：每次收货累计 qty_received，剩余数量继续跟踪直到收齐或关闭）';
COMMENT ON COLUMN inbound_items.inbound_id IS '入库单（inbound_orders 裸 ID 引用，域内同事务创建，不建 FK）';

CREATE UNIQUE INDEX uk_inbound_items_inbound_line ON inbound_items (inbound_id, line_no);
CREATE INDEX idx_inbound_items_sku ON inbound_items (sku_id);

CREATE TABLE receipts (
    id              bigserial   PRIMARY KEY,
    receipt_no      varchar(64) NOT NULL,
    inbound_no      varchar(64) NOT NULL,
    warehouse_id    bigint      NOT NULL,
    batch_no        varchar(64) NOT NULL DEFAULT '',
    expiry_date     date,
    production_date date,
    idempotency_key varchar(128),
    operator_id     bigint      NOT NULL DEFAULT 0,
    operator_name   varchar(64) NOT NULL DEFAULT '',
    remark          text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    created_by      bigint      NOT NULL DEFAULT 0,
    updated_by      bigint      NOT NULL DEFAULT 0
);

COMMENT ON TABLE receipts IS '收货记录（business-flow §3.2 收货环节；事件型一次性生效，plan §6.3 无状态机——状态语义由入库单承载）';
COMMENT ON COLUMN receipts.inbound_no IS '入库单号（逻辑单号引用，idx 支持 mixed 部分收货多次回查）';
COMMENT ON COLUMN receipts.batch_no IS '批次采集（inventory-rules §6：批次管理 SKU 收货时采集批次号，空串=非批次 SKU）';
COMMENT ON COLUMN receipts.expiry_date IS '效期采集（inventory-rules §6：效期管理 SKU 收货时采集，NULL=非效期 SKU）';
COMMENT ON COLUMN receipts.production_date IS '生产日期采集（批次属性，NULL=未采集）';
COMMENT ON COLUMN receipts.idempotency_key IS '收货幂等键（architecture §3.2：部分唯一索引防重复提交，键构成见 plan §7）';
COMMENT ON COLUMN receipts.operator_id IS '收货人（middleware.Audit 与 operation_logs 之外的业务落列，列表展示用）';

CREATE UNIQUE INDEX uk_receipts_no ON receipts (receipt_no);
CREATE UNIQUE INDEX uk_receipts_idempotency ON receipts (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_receipts_inbound_no ON receipts (inbound_no);

CREATE TABLE receipt_items (
    id           bigserial      PRIMARY KEY,
    receipt_id   bigint         NOT NULL,
    line_no      integer        NOT NULL,
    sku_id       bigint         NOT NULL,
    qty_good     numeric(18, 4) NOT NULL DEFAULT 0,
    qty_rejected numeric(18, 4) NOT NULL DEFAULT 0,
    exception_ref varchar(64)   NOT NULL DEFAULT '',
    remark       text           NOT NULL DEFAULT '',
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    created_by   bigint         NOT NULL DEFAULT 0,
    updated_by   bigint         NOT NULL DEFAULT 0
);

COMMENT ON TABLE receipt_items IS '收货明细（business-flow §3.4：合格/拒收分列；异常收货经 exception_ref 关联异常中心单号，plan §3.1 异常创建窄接口）';
COMMENT ON COLUMN receipt_items.receipt_id IS '收货记录（receipts 裸 ID 引用，同事务创建，不建 FK）';
COMMENT ON COLUMN receipt_items.exception_ref IS '关联异常单号（exceptions.exception_no 逻辑引用，空串=无异常）';

CREATE UNIQUE INDEX uk_receipt_items_receipt_line ON receipt_items (receipt_id, line_no);

CREATE TABLE putaway_tasks (
    id                  bigserial      PRIMARY KEY,
    putaway_no          varchar(64)    NOT NULL,
    inbound_no          varchar(64)    NOT NULL,
    receipt_no          varchar(64)    NOT NULL DEFAULT '',
    sku_id              bigint         NOT NULL,
    batch_id            bigint         NOT NULL DEFAULT 0,
    serial_no           varchar(128)   NOT NULL DEFAULT '',
    qty                 numeric(18, 4) NOT NULL,
    from_state          varchar(32)    NOT NULL DEFAULT 'available',
    target_warehouse_id bigint         NOT NULL,
    target_zone_id      bigint         NOT NULL DEFAULT 0,
    target_shelf_id     bigint         NOT NULL DEFAULT 0,
    target_bin_id       bigint         NOT NULL,
    status              varchar(16)    NOT NULL DEFAULT 'PENDING',
    claimed_by          bigint         NOT NULL DEFAULT 0,
    claimed_at          timestamptz,
    completed_at        timestamptz,
    remark              text           NOT NULL DEFAULT '',
    created_at          timestamptz    NOT NULL DEFAULT now(),
    updated_at          timestamptz    NOT NULL DEFAULT now(),
    created_by          bigint         NOT NULL DEFAULT 0,
    updated_by          bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_putaway_tasks_from_state CHECK (from_state IN ('available', 'pending_inspect')),
    CONSTRAINT chk_putaway_tasks_status CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED')),
    CONSTRAINT chk_putaway_tasks_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE putaway_tasks IS '上架任务（business-flow §5：待上架→上架中→已完成；plan §6.3 领取为原子抢占，完成触发 Putaway 落账）';
COMMENT ON COLUMN putaway_tasks.inbound_no IS '入库单号（逻辑单号引用，idx 支持按入库单聚合任务）';
COMMENT ON COLUMN putaway_tasks.receipt_no IS '来源收货单号（plan §5 source receipt_no，逻辑引用）';
COMMENT ON COLUMN putaway_tasks.batch_id IS '批次（batches 逻辑引用；0=非批次 SKU——PutawayOp 构造所需的批次维度，plan §7）';
COMMENT ON COLUMN putaway_tasks.serial_no IS '序列号（序列号 SKU 单件任务 qty=1 逐件上架，inventory-rules §8.2；空串=非序列号 SKU，plan §8.3 PutawayOp.SerialNo 数据来源）';
COMMENT ON COLUMN putaway_tasks.from_state IS '质检结果去向（plan §5：available 免检直通 / pending_inspect 经检待检——决定 PutawayOp.RequireInspect）';
COMMENT ON COLUMN putaway_tasks.target_bin_id IS '目标库位（推荐库位或人工指定，四维冗余目标仓/区/架定位）';
COMMENT ON COLUMN putaway_tasks.claimed_at IS '领取时间（plan §6.3 原子抢占落列）';

CREATE UNIQUE INDEX uk_putaway_tasks_no ON putaway_tasks (putaway_no);
CREATE INDEX idx_putaway_tasks_status ON putaway_tasks (status);
CREATE INDEX idx_putaway_tasks_inbound_no ON putaway_tasks (inbound_no);

CREATE TABLE quality_orders (
    id              bigserial   PRIMARY KEY,
    qc_no           varchar(64) NOT NULL,
    source_type     varchar(16) NOT NULL,
    source_no       varchar(64) NOT NULL,
    warehouse_id    bigint      NOT NULL,
    inspection_type varchar(16) NOT NULL DEFAULT '免检',
    status          varchar(16) NOT NULL DEFAULT 'PENDING',
    qty_inspected   numeric(18, 4) NOT NULL DEFAULT 0,
    qty_qualified   numeric(18, 4) NOT NULL DEFAULT 0,
    qty_defective   numeric(18, 4) NOT NULL DEFAULT 0,
    result          varchar(16) NOT NULL DEFAULT '',
    inspector_id    bigint      NOT NULL DEFAULT 0,
    inspector_name  varchar(64) NOT NULL DEFAULT '',
    inspected_at    timestamptz,
    image_refs      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    remark          text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    created_by      bigint      NOT NULL DEFAULT 0,
    updated_by      bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_quality_orders_source_type CHECK (source_type IN ('INBOUND', 'RETURN')),
    CONSTRAINT chk_quality_orders_inspection_type CHECK (inspection_type IN ('免检', '抽检', '全检')),
    CONSTRAINT chk_quality_orders_result CHECK (result IN ('合格', '部分合格', '不合格', '退供应商', '报废', '返工', '降级', '转不良品仓', '特批放行')),
    CONSTRAINT chk_quality_orders_status CHECK (status IN ('PENDING', 'INSPECTING', 'COMPLETED'))
);

COMMENT ON TABLE quality_orders IS '质检单（business-flow §4：检验方式免检/抽检/全检，九类处理结果；COMPLETED 即处理结果落定并触发库存映射——plan §6.3）';
COMMENT ON COLUMN quality_orders.source_type IS '来源类型（INBOUND 入库质检/RETURN 退货质检——退货质检经 plan §3.1 QCCreator 窄接口复用本表，禁止 returns 另造）';
COMMENT ON COLUMN quality_orders.source_no IS '来源单号（入库单号/退货单号，逻辑引用）';
COMMENT ON COLUMN quality_orders.warehouse_id IS '质检作业仓（plan §5：数据权限过滤）';
COMMENT ON COLUMN quality_orders.result IS '处理结果（business-flow §4.3 九类中文值域，与 M1 adjust_type 中文值域同风格）';
COMMENT ON COLUMN quality_orders.image_refs IS '质检图片（文件中心阶段 14 前仅占位列，不提供上传接口——plan §5/§12）';
COMMENT ON COLUMN quality_orders.inspected_at IS '检验时间（business-flow §4.2/§13.4）';

CREATE UNIQUE INDEX uk_quality_orders_no ON quality_orders (qc_no);
CREATE INDEX idx_quality_orders_source ON quality_orders (source_type, source_no);
CREATE INDEX idx_quality_orders_warehouse_status ON quality_orders (warehouse_id, status);

CREATE TABLE quality_items (
    id            bigserial      PRIMARY KEY,
    qc_id         bigint         NOT NULL,
    line_no       integer        NOT NULL,
    sku_id        bigint         NOT NULL,
    batch_no      varchar(64)    NOT NULL DEFAULT '',
    qty_inspected numeric(18, 4) NOT NULL DEFAULT 0,
    qty_qualified numeric(18, 4) NOT NULL DEFAULT 0,
    qty_defective numeric(18, 4) NOT NULL DEFAULT 0,
    remark        text           NOT NULL DEFAULT '',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0
);

COMMENT ON TABLE quality_items IS '质检明细（business-flow §4.2：检验/合格/不合格数量逐行记录）';
COMMENT ON COLUMN quality_items.qc_id IS '质检单（quality_orders 裸 ID 引用，同事务创建，不建 FK）';
COMMENT ON COLUMN quality_items.batch_no IS '批次号（质检对象批次，空串=非批次 SKU）';

CREATE UNIQUE INDEX uk_quality_items_qc_line ON quality_items (qc_id, line_no);
