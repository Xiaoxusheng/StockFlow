-- 000008 sales 域单据表：sales_orders/sales_order_items/outbound_orders/outbound_items/
--       allocation_records/pick_tasks/check_tasks/packing_records/packing_items/shipments
-- 依据：backend-m2-plan.md §5（000008 冻结 DDL）、§6.4–§6.5（状态机）、business-flow.md §6–§8/§13.4、
--       inventory-rules.md §4（预占锁定）、api.md §4。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：customer/sku/warehouse/bin/batch 为跨域逻辑引用，
--     so_no/outbound_no 为跨单据逻辑单号引用 + 索引，一致性由 Service 层校验。
--   * 库存预占在销售订单审核、正式扣减在发货完成（business-flow §7.2）——allocation_records
--     记录分配结果与 lock_id 回指，发货经 Deduct 核销锁（plan §7）。
--   * 作业类从表 pick_tasks/check_tasks/packing_records/shipments 创建时自主出库单冗余
--     warehouse_id 落列（plan §10.5：列表过滤免 join，可独立建 (warehouse_id, status) 索引）。
--   * 事件型表 packing_records/shipments 挂 idempotency_key 部分唯一索引（architecture §3.2）；
--     任务类 pick/check 以状态机守卫与原子抢占天然幂等，不加键（plan §5 幂等口径）。
--   * 包裹与订单明细多对多落 packing_items（business-flow §8.4 拆包场景）。
--   * 单据不做软删除：作废/取消/关闭走状态机（business-flow §13.3）。

CREATE TABLE sales_orders (
    id               bigserial      PRIMARY KEY,
    so_no            varchar(64)    NOT NULL,
    customer_id      bigint         NOT NULL,
    warehouse_id     bigint         NOT NULL,
    shipping_address varchar(512)   NOT NULL DEFAULT '',
    delivery_method  varchar(64)    NOT NULL DEFAULT '',
    total_amount     numeric(18, 4) NOT NULL DEFAULT 0,
    status           varchar(32)    NOT NULL DEFAULT 'DRAFT',
    approved_by      bigint         NOT NULL DEFAULT 0,
    approved_at      timestamptz,
    shipped_at       timestamptz,
    completed_at     timestamptz,
    cancelled_at     timestamptz,
    remark           text           NOT NULL DEFAULT '',
    created_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at       timestamptz    NOT NULL DEFAULT now(),
    created_by       bigint         NOT NULL DEFAULT 0,
    updated_by       bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_sales_orders_status CHECK (status IN ('DRAFT', 'PENDING_APPROVAL', 'APPROVED', 'REJECTED', 'PARTIAL_SHIPPED', 'SHIPPED_ALL', 'COMPLETED', 'CANCELLED'))
);

COMMENT ON TABLE sales_orders IS '销售订单（business-flow §6：审核即预占，预占失败订单停留 PENDING_APPROVAL——plan §6.4）';
COMMENT ON COLUMN sales_orders.customer_id IS '客户（masterdata 逻辑引用，跨域不建 FK；CustomerChecker 校验启用）';
COMMENT ON COLUMN sales_orders.warehouse_id IS '出货仓（business-flow §6.1；数据权限过滤与预占/分配范围依据）';
COMMENT ON COLUMN sales_orders.shipping_address IS '收货地址（business-flow §6.1）';
COMMENT ON COLUMN sales_orders.delivery_method IS '配送方式（business-flow §6.1）';
COMMENT ON COLUMN sales_orders.total_amount IS '订单金额（business-flow §6.1 金额；明细行金额汇总，由 Service 层计算）';
COMMENT ON COLUMN sales_orders.approved_at IS '审核时间（business-flow §13.4；approved_by 冗余供列表展示，完整审批记录落 document_approvals）';
COMMENT ON COLUMN sales_orders.shipped_at IS '发货时间（business-flow §13.4）';

CREATE UNIQUE INDEX uk_sales_orders_no ON sales_orders (so_no);
CREATE INDEX idx_sales_orders_customer ON sales_orders (customer_id);
CREATE INDEX idx_sales_orders_warehouse_status ON sales_orders (warehouse_id, status);

CREATE TABLE sales_order_items (
    id            bigserial      PRIMARY KEY,
    so_id         bigint         NOT NULL,
    line_no       integer        NOT NULL,
    sku_id        bigint         NOT NULL,
    qty           numeric(18, 4) NOT NULL,
    price         numeric(18, 4) NOT NULL DEFAULT 0,
    amount        numeric(18, 4) NOT NULL DEFAULT 0,
    qty_allocated numeric(18, 4) NOT NULL DEFAULT 0,
    qty_shipped   numeric(18, 4) NOT NULL DEFAULT 0,
    remark        text           NOT NULL DEFAULT '',
    created_at    timestamptz    NOT NULL DEFAULT now(),
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    created_by    bigint         NOT NULL DEFAULT 0,
    updated_by    bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_sales_order_items_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE sales_order_items IS '销售订单明细（business-flow §6.1：数量/单价/金额；qty_allocated/qty_shipped 承载预占与发货进度——plan §5）';
COMMENT ON COLUMN sales_order_items.so_id IS '销售订单（sales_orders 裸 ID 引用，域内同事务创建，不建 FK）';

CREATE UNIQUE INDEX uk_sales_order_items_so_line ON sales_order_items (so_id, line_no);
CREATE INDEX idx_sales_order_items_sku ON sales_order_items (sku_id);

CREATE TABLE outbound_orders (
    id           bigserial   PRIMARY KEY,
    outbound_no  varchar(64) NOT NULL,
    so_no        varchar(64) NOT NULL DEFAULT '',
    type         varchar(16) NOT NULL,
    warehouse_id bigint      NOT NULL,
    status       varchar(32) NOT NULL DEFAULT 'PENDING_ALLOCATE',
    picked_at    timestamptz,
    checked_at   timestamptz,
    packed_at    timestamptz,
    shipped_at   timestamptz,
    cancelled_at timestamptz,
    remark       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint      NOT NULL DEFAULT 0,
    updated_by   bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_outbound_orders_type CHECK (type IN ('销售出库', '生产领料', '调拨出库', '其他出库', '报损出库')),
    CONSTRAINT chk_outbound_orders_status CHECK (status IN ('PENDING_ALLOCATE', 'ALLOCATED', 'PICKING', 'PICKED', 'CHECKED', 'PACKED', 'PARTIAL_SHIPPED', 'SHIPPED_ALL', 'CANCELLED', 'CLOSED'))
);

COMMENT ON TABLE outbound_orders IS '出库单（business-flow §7.2 分配→拣货→复核→打包→发货执行链；正式扣减发生在发货完成）';
COMMENT ON COLUMN outbound_orders.so_no IS '销售订单号（逻辑单号引用；销售出库必填，其他出库类型为空串）';
COMMENT ON COLUMN outbound_orders.type IS '出库类型（business-flow §7.1 五值中文值域；M2 实际入口为销售出库/其他出库，值域按 §7.1 全量保留）';
COMMENT ON COLUMN outbound_orders.status IS '状态机（plan §6.5：PENDING_ALLOCATE→ALLOCATED→PICKING→PICKED→CHECKED→PACKED→SHIPPED_ALL，差额关闭 CLOSED，取消 CANCELLED——与 §6.5 迁移行同源）';
COMMENT ON COLUMN outbound_orders.picked_at IS '拣货完成时间（business-flow §13.4）';
COMMENT ON COLUMN outbound_orders.checked_at IS '复核完成时间（business-flow §13.4）';
COMMENT ON COLUMN outbound_orders.packed_at IS '打包完成时间（business-flow §13.4）';
COMMENT ON COLUMN outbound_orders.shipped_at IS '发货时间（business-flow §13.4）';

CREATE UNIQUE INDEX uk_outbound_orders_no ON outbound_orders (outbound_no);
CREATE INDEX idx_outbound_orders_so_no ON outbound_orders (so_no);
CREATE INDEX idx_outbound_orders_warehouse_status ON outbound_orders (warehouse_id, status);

CREATE TABLE outbound_items (
    id          bigserial      PRIMARY KEY,
    outbound_id bigint         NOT NULL,
    line_no     integer        NOT NULL,
    sku_id      bigint         NOT NULL,
    qty         numeric(18, 4) NOT NULL,
    qty_picked  numeric(18, 4) NOT NULL DEFAULT 0,
    qty_checked numeric(18, 4) NOT NULL DEFAULT 0,
    qty_packed  numeric(18, 4) NOT NULL DEFAULT 0,
    qty_shipped numeric(18, 4) NOT NULL DEFAULT 0,
    remark      text           NOT NULL DEFAULT '',
    created_at  timestamptz    NOT NULL DEFAULT now(),
    updated_at  timestamptz    NOT NULL DEFAULT now(),
    created_by  bigint         NOT NULL DEFAULT 0,
    updated_by  bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_outbound_items_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE outbound_items IS '出库单明细（plan §5：qty_picked/qty_checked/qty_packed/qty_shipped 各环节累计，支持部分拣货/部分发货）';
COMMENT ON COLUMN outbound_items.outbound_id IS '出库单（outbound_orders 裸 ID 引用，域内同事务创建，不建 FK）';

CREATE UNIQUE INDEX uk_outbound_items_outbound_line ON outbound_items (outbound_id, line_no);
CREATE INDEX idx_outbound_items_sku ON outbound_items (sku_id);

CREATE TABLE allocation_records (
    id           bigserial      PRIMARY KEY,
    outbound_no  varchar(64)    NOT NULL,
    line_no      integer        NOT NULL,
    sku_id       bigint         NOT NULL,
    batch_id     bigint         NOT NULL DEFAULT 0,
    warehouse_id bigint         NOT NULL,
    bin_id       bigint         NOT NULL,
    qty          numeric(18, 4) NOT NULL,
    strategy     varchar(16)    NOT NULL,
    reason       jsonb          NOT NULL DEFAULT '{}'::jsonb,
    lock_id      bigint         NOT NULL DEFAULT 0,
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    created_by   bigint         NOT NULL DEFAULT 0,
    updated_by   bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_allocation_records_strategy CHECK (strategy IN ('FIFO', 'FEFO', '指定批次', '指定仓库', '指定库位')),
    CONSTRAINT chk_allocation_records_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE allocation_records IS '库存分配记录（business-flow §8.1：FIFO/FEFO/指定批次/指定仓库/指定库位；逐 bin 选行结果落行，lock_id 回指 inventory_locks）';
COMMENT ON COLUMN allocation_records.outbound_no IS '出库单号（逻辑单号引用，重新分配按单号+行号整组替换——plan §6.5）';
COMMENT ON COLUMN allocation_records.batch_id IS '批次（batches 逻辑引用；0=非批次 SKU）';
COMMENT ON COLUMN allocation_records.strategy IS '分配策略（business-flow §8.1 五值；指定仓库落订单级选仓或本表人工指定行）';
COMMENT ON COLUMN allocation_records.reason IS '分配理由（business-flow §8.1 展示义务：命中原因+可用量快照，jsonb 结构由应用层定义）';
COMMENT ON COLUMN allocation_records.lock_id IS '预占锁（inventory_locks 逻辑引用，发货 Deduct 核销依据——plan §7）';

CREATE INDEX idx_allocation_records_outbound_no ON allocation_records (outbound_no);
CREATE INDEX idx_allocation_records_sku_batch ON allocation_records (sku_id, batch_id);

CREATE TABLE pick_tasks (
    id                 bigserial      PRIMARY KEY,
    pick_no            varchar(64)    NOT NULL,
    outbound_no        varchar(64)    NOT NULL,
    outbound_line_no   integer        NOT NULL,
    sku_id             bigint         NOT NULL,
    batch_id           bigint         NOT NULL DEFAULT 0,
    source_warehouse_id bigint        NOT NULL,
    source_zone_id     bigint         NOT NULL DEFAULT 0,
    source_shelf_id    bigint         NOT NULL DEFAULT 0,
    source_bin_id      bigint         NOT NULL,
    qty                numeric(18, 4) NOT NULL,
    picked_qty         numeric(18, 4) NOT NULL DEFAULT 0,
    status             varchar(16)    NOT NULL DEFAULT 'PENDING',
    assignee_id        bigint         NOT NULL DEFAULT 0,
    assignee_name      varchar(64)    NOT NULL DEFAULT '',
    claimed_at         timestamptz,
    picked_at          timestamptz,
    scanned_code       varchar(128)   NOT NULL DEFAULT '',
    scan_matched       boolean        NOT NULL DEFAULT FALSE,
    warehouse_id       bigint         NOT NULL,
    remark             text           NOT NULL DEFAULT '',
    created_at         timestamptz    NOT NULL DEFAULT now(),
    updated_at         timestamptz    NOT NULL DEFAULT now(),
    created_by         bigint         NOT NULL DEFAULT 0,
    updated_by         bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_pick_tasks_status CHECK (status IN ('PENDING', 'CLAIMED', 'PICKING', 'PICKED', 'EXCEPTION', 'CANCELLED')),
    CONSTRAINT chk_pick_tasks_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE pick_tasks IS '拣货任务（business-flow §8.2：SKU→来源库位→数量→操作人→完成时间；plan §6.5 领取原子抢占，缺货/少货上报异常中心后转 EXCEPTION）';
COMMENT ON COLUMN pick_tasks.outbound_line_no IS '出库单行号（逻辑引用 outbound_items.line_no，整单一张或多行任务共用）';
COMMENT ON COLUMN pick_tasks.batch_id IS '批次（batches 逻辑引用；0=非批次 SKU）';
COMMENT ON COLUMN pick_tasks.source_bin_id IS '来源库位（business-flow §8.2 任务内容；四维冗余仓/区/架定位）';
COMMENT ON COLUMN pick_tasks.scanned_code IS '扫码校验录入值（business-flow §8.2 扫码校验；空串=未扫码）';
COMMENT ON COLUMN pick_tasks.scan_matched IS '扫码是否匹配（库位/SKU/批次校验结果，由作业服务落列）';
COMMENT ON COLUMN pick_tasks.warehouse_id IS '所属仓库（创建时自主出库单冗余——plan §10.5 数据权限过滤免 join）';

CREATE UNIQUE INDEX uk_pick_tasks_no ON pick_tasks (pick_no);
CREATE INDEX idx_pick_tasks_outbound_no ON pick_tasks (outbound_no);
CREATE INDEX idx_pick_tasks_assignee_status ON pick_tasks (assignee_id, status);
CREATE INDEX idx_pick_tasks_warehouse_status ON pick_tasks (warehouse_id, status);

CREATE TABLE check_tasks (
    id               bigserial      PRIMARY KEY,
    check_no         varchar(64)    NOT NULL,
    outbound_no      varchar(64)    NOT NULL,
    outbound_line_no integer        NOT NULL,
    sku_id           bigint         NOT NULL,
    batch_id         bigint         NOT NULL DEFAULT 0,
    serial_no        varchar(128)   NOT NULL DEFAULT '',
    qty              numeric(18, 4) NOT NULL,
    status           varchar(16)    NOT NULL DEFAULT 'PENDING',
    result           varchar(16)    NOT NULL DEFAULT '',
    assignee_id      bigint         NOT NULL DEFAULT 0,
    assignee_name    varchar(64)    NOT NULL DEFAULT '',
    claimed_at       timestamptz,
    done_at          timestamptz,
    warehouse_id     bigint         NOT NULL,
    remark           text           NOT NULL DEFAULT '',
    created_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at       timestamptz    NOT NULL DEFAULT now(),
    created_by       bigint         NOT NULL DEFAULT 0,
    updated_by       bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_check_tasks_result CHECK (result IN ('', '错货', '少货', '多货', '批次错误', '序列号错误')),
    CONSTRAINT chk_check_tasks_status CHECK (status IN ('PENDING', 'DONE', 'EXCEPTION'))
);

COMMENT ON TABLE check_tasks IS '复核任务（business-flow §8.3：重新确认 SKU/条码/数量/批次/序列号/订单；五类复核异常落 result 并联动异常中心——plan §6.5）';
COMMENT ON COLUMN check_tasks.serial_no IS '序列号核验记录（序列号 SKU 逐件确认一行一件，与 count_items 同口径；空串=非序列号 SKU）';
COMMENT ON COLUMN check_tasks.result IS '复核异常类型（business-flow §8.3 五类中文值域；空串=复核通过）';
COMMENT ON COLUMN check_tasks.warehouse_id IS '所属仓库（创建时自主出库单冗余——plan §10.5）';
COMMENT ON COLUMN check_tasks.done_at IS '复核完成时间（business-flow §13.4 复核时间）';

CREATE UNIQUE INDEX uk_check_tasks_no ON check_tasks (check_no);
CREATE INDEX idx_check_tasks_outbound_no ON check_tasks (outbound_no);
CREATE INDEX idx_check_tasks_warehouse_status ON check_tasks (warehouse_id, status);

CREATE TABLE packing_records (
    id               bigserial      PRIMARY KEY,
    package_no       varchar(64)    NOT NULL,
    outbound_no      varchar(64)    NOT NULL,
    packing_material varchar(128)   NOT NULL DEFAULT '',
    length           numeric(18, 4) NOT NULL DEFAULT 0,
    width            numeric(18, 4) NOT NULL DEFAULT 0,
    height           numeric(18, 4) NOT NULL DEFAULT 0,
    weight           numeric(18, 4) NOT NULL DEFAULT 0,
    volume           numeric(18, 4) NOT NULL DEFAULT 0,
    carrier          varchar(64)    NOT NULL DEFAULT '',
    tracking_no      varchar(128)   NOT NULL DEFAULT '',
    warehouse_id     bigint         NOT NULL,
    idempotency_key  varchar(128),
    remark           text           NOT NULL DEFAULT '',
    created_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at       timestamptz    NOT NULL DEFAULT now(),
    created_by       bigint         NOT NULL DEFAULT 0,
    updated_by       bigint         NOT NULL DEFAULT 0
);

COMMENT ON TABLE packing_records IS '打包记录（business-flow §8.4 全列：包装材料/长宽高/重/体积/快递公司/快递单号；一个订单允许多个包裹）';
COMMENT ON COLUMN packing_records.outbound_no IS '出库单号（逻辑单号引用，包裹×明细多对多落 packing_items）';
COMMENT ON COLUMN packing_records.idempotency_key IS '打包幂等键（architecture §3.2：重复提交返回既有包裹——plan §6.5 CHECKED→PACKED）';

CREATE UNIQUE INDEX uk_packing_records_no ON packing_records (package_no);
CREATE UNIQUE INDEX uk_packing_records_idempotency ON packing_records (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_packing_records_outbound_no ON packing_records (outbound_no);

CREATE TABLE packing_items (
    id          bigserial      PRIMARY KEY,
    package_id  bigint         NOT NULL,
    outbound_id bigint         NOT NULL,
    line_no     integer        NOT NULL,
    qty         numeric(18, 4) NOT NULL,
    remark      text           NOT NULL DEFAULT '',
    created_at  timestamptz    NOT NULL DEFAULT now(),
    updated_at  timestamptz    NOT NULL DEFAULT now(),
    created_by  bigint         NOT NULL DEFAULT 0,
    updated_by  bigint         NOT NULL DEFAULT 0,
    CONSTRAINT chk_packing_items_qty_positive CHECK (qty > 0)
);

COMMENT ON TABLE packing_items IS '包裹×明细多对多（business-flow §8.4 拆包场景：一行明细可拆入多个包裹，一个包裹可含多行明细）';
COMMENT ON COLUMN packing_items.package_id IS '包裹（packing_records 裸 ID 引用，同事务创建，不建 FK）';
COMMENT ON COLUMN packing_items.outbound_id IS '出库单（outbound_orders 裸 ID 引用，line_no 对应 outbound_items.line_no）';

CREATE UNIQUE INDEX uk_packing_items_package_outbound_line ON packing_items (package_id, outbound_id, line_no);

CREATE TABLE shipments (
    id              bigserial   PRIMARY KEY,
    shipment_no     varchar(64) NOT NULL,
    outbound_no     varchar(64) NOT NULL,
    carrier         varchar(64) NOT NULL DEFAULT '',
    tracking_no     varchar(128) NOT NULL DEFAULT '',
    warehouse_id    bigint      NOT NULL,
    shipper_id      bigint      NOT NULL DEFAULT 0,
    shipper_name    varchar(64) NOT NULL DEFAULT '',
    package_count   integer     NOT NULL DEFAULT 0,
    status          varchar(16) NOT NULL DEFAULT 'PENDING',
    shipped_at      timestamptz,
    idempotency_key varchar(128),
    remark          text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    created_by      bigint      NOT NULL DEFAULT 0,
    updated_by      bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_shipments_status CHECK (status IN ('PENDING', 'SHIPPED', 'IN_TRANSIT', 'SIGNED', 'ABNORMAL'))
);

COMMENT ON TABLE shipments IS '发货单（business-flow §8.5：发货仓落列；PENDING→SHIPPED 触发正式扣减——plan §6.5，后续物流态为纯记录流转不接物流公司 API）';
COMMENT ON COLUMN shipments.outbound_no IS '出库单号（逻辑单号引用）';
COMMENT ON COLUMN shipments.carrier IS '物流公司（business-flow §8.5）';
COMMENT ON COLUMN shipments.tracking_no IS '物流单号（business-flow §8.5）';
COMMENT ON COLUMN shipments.warehouse_id IS '发货仓（business-flow §8.5 发货仓落列；数据权限过滤）';
COMMENT ON COLUMN shipments.package_count IS '包裹数量（business-flow §8.5）';
COMMENT ON COLUMN shipments.shipped_at IS '发货时间（business-flow §13.4；库存 Deduct 与本状态迁移同事务）';
COMMENT ON COLUMN shipments.idempotency_key IS '发货幂等键（architecture §3.2：幂等重放返回既有结果，不重复扣减——plan §6.5/§11.4）';

CREATE UNIQUE INDEX uk_shipments_no ON shipments (shipment_no);
CREATE UNIQUE INDEX uk_shipments_idempotency ON shipments (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_shipments_outbound_no ON shipments (outbound_no);
CREATE INDEX idx_shipments_warehouse_status ON shipments (warehouse_id, status);
