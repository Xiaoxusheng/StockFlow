-- 000012 打印中心表：print_templates / print_tasks / print_task_rows
-- 依据：backend-m3-plan.md §5（000012 冻结 DDL）、§7（打印中心）、printing.md §1–§6。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：template_id 为 print_templates 裸 ID 引用
--     （同域同事务或独立校验），task_id 为 print_tasks 裸 ID 引用，created_by/printed_by 为 users 裸 ID。
--   * 模板不存 HTML（printing §2 禁止把打印 HTML 写死——渲染由前端 react-to-print 承担，
--     后端只管数据与快照）；fields 为字段绑定键值 jsonb（创建时校验为预设键子集，plan §7.1）。
--   * print_tasks.template_snapshot 任务创建时冻结模板快照（plan §7.2：后续模板修改不影响已建任务）。
--   * 服务端 PDF 不做（Orchestrator 裁决③，plan §7.2）：打印产物 = 渲染数据包（print_task_rows）
--     + /api/prints/barcode 条码端点；条码 PNG 不落 files 表（plan §4.2）。
--   * 单据不做软删除：模板停用走 status=DISABLED，任务失败走状态机。

CREATE TABLE print_templates (
    id                bigserial    PRIMARY KEY,
    name              varchar(128) NOT NULL,
    object_type       varchar(32)  NOT NULL,
    paper             varchar(32)  NOT NULL,
    barcode_symbology varchar(16),
    qrcode_enabled    boolean      NOT NULL DEFAULT FALSE,
    fields            jsonb        NOT NULL DEFAULT '{}'::jsonb,
    header_text       varchar(255) NOT NULL DEFAULT '',
    status            varchar(16)  NOT NULL DEFAULT 'ENABLED',
    remark            text         NOT NULL DEFAULT '',
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint       NOT NULL DEFAULT 0,
    updated_by        bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_print_templates_object_type CHECK (object_type IN ('SKU_LABEL', 'BIN_LABEL', 'CARTON_CODE', 'PALLET_CODE', 'INBOUND_ORDER', 'OUTBOUND_ORDER', 'PICK_ORDER', 'COUNT_ORDER', 'SHIPMENT_ORDER')),
    CONSTRAINT chk_print_templates_paper CHECK (paper IN ('A4', 'A5', 'THERMAL_40_30', 'THERMAL_60_40', 'THERMAL_100_50')),
    CONSTRAINT chk_print_templates_barcode_symbology CHECK (barcode_symbology IS NULL OR barcode_symbology IN ('CODE128', 'CODE39', 'EAN13', 'EAN8', 'UPC')),
    CONSTRAINT chk_print_templates_status CHECK (status IN ('ENABLED', 'DISABLED'))
);

COMMENT ON TABLE print_templates IS '打印模板（printing §1.1：F12 首批 9 类 object_type 与 web/src/api/printing.ts 先行枚举一致；模板 CRUD/复制/启停归 printing 域——plan §7.1）';
COMMENT ON COLUMN print_templates.object_type IS '模板对象类型九值（SKU 标签/库位标签/箱码/托盘码/入库单/出库单/拣货单/盘点单/发货单）';
COMMENT ON COLUMN print_templates.paper IS '纸张规格（A4/A5 + 三种热敏规格）';
COMMENT ON COLUMN print_templates.barcode_symbology IS '条码码制（标签类必填，plan §7.1；单据类可 NULL）';
COMMENT ON COLUMN print_templates.qrcode_enabled IS '是否启用二维码';
COMMENT ON COLUMN print_templates.fields IS '字段绑定键值（jsonb 对象；object_type 预设键子集，预设清单与 PRINT_TEMPLATE_FIELD_PRESETS 冻结同源——plan §7.1）';
COMMENT ON COLUMN print_templates.status IS '状态（DISABLED 停用后不可被新任务选用，printing §2）';

CREATE INDEX idx_print_templates_type_status ON print_templates (object_type, status);
CREATE INDEX idx_print_templates_creator ON print_templates (created_by);

CREATE TABLE print_tasks (
    id                bigserial   PRIMARY KEY,
    print_no          varchar(64) NOT NULL,
    object_type       varchar(32) NOT NULL,
    template_id       bigint      NOT NULL,
    template_snapshot jsonb,
    paper             varchar(32) NOT NULL,
    copies            integer     NOT NULL DEFAULT 1,
    total_count       integer     NOT NULL DEFAULT 0,
    status            varchar(16) NOT NULL DEFAULT 'QUEUED',
    result            varchar(16),
    printed_by        bigint      NOT NULL DEFAULT 0,
    printed_at        timestamptz,
    error_message     text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    created_by        bigint      NOT NULL DEFAULT 0,
    updated_by        bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_print_tasks_object_type CHECK (object_type IN ('SKU_LABEL', 'BIN_LABEL', 'CARTON_CODE', 'PALLET_CODE', 'INBOUND_ORDER', 'OUTBOUND_ORDER', 'PICK_ORDER', 'COUNT_ORDER', 'SHIPMENT_ORDER')),
    CONSTRAINT chk_print_tasks_paper CHECK (paper IN ('A4', 'A5', 'THERMAL_40_30', 'THERMAL_60_40', 'THERMAL_100_50')),
    CONSTRAINT chk_print_tasks_copies CHECK (copies >= 1),
    CONSTRAINT chk_print_tasks_total_count CHECK (total_count >= 0),
    CONSTRAINT chk_print_tasks_status CHECK (status IN ('QUEUED', 'PROCESSING', 'SUCCESS', 'FAILED')),
    CONSTRAINT chk_print_tasks_result CHECK (result IS NULL OR result IN ('SUCCESS', 'FAILED'))
);

COMMENT ON TABLE print_tasks IS '打印任务（printing §1.2：创建时同步装配（ContentReader 窄接口），render 任务仅异步预生成条码 PNG；打印执行确认回填 result/printed_by/printed_at——plan §7.2）';
COMMENT ON COLUMN print_tasks.print_no IS '打印任务单号（docnum PT 前缀，plan §12.3；有意避开 PRT——api §7 prt: 为采购退货幂等键动作前缀）';
COMMENT ON COLUMN print_tasks.template_snapshot IS '模板快照（任务创建时冻结，后续模板修改不影响已建任务——plan §7.2）';
COMMENT ON COLUMN print_tasks.copies IS '打印份数';
COMMENT ON COLUMN print_tasks.total_count IS '打印对象总数（data_ids 数，≤500 防失控批量——Orchestrator 裁决④）';
COMMENT ON COLUMN print_tasks.status IS '任务状态（render 队列态；打印执行本身由前端渲染层完成）';
COMMENT ON COLUMN print_tasks.result IS '执行确认结果（SUCCESS/FAILED；NULL=尚未确认——/api/prints/tasks/{id}/execute 回填一次，plan §13.2 状态守卫）';
COMMENT ON COLUMN print_tasks.printed_by IS '执行确认人（打印历史五字段来源，printing §1.2/§6 打印日志：谁、何时、用什么模板、打了什么）';

CREATE UNIQUE INDEX uk_print_tasks_no ON print_tasks (print_no);
CREATE INDEX idx_print_tasks_type_created ON print_tasks (object_type, created_at);
CREATE INDEX idx_print_tasks_status ON print_tasks (status);
CREATE INDEX idx_print_tasks_creator_created ON print_tasks (created_by, created_at);

CREATE TABLE print_task_rows (
    id         bigserial    PRIMARY KEY,
    task_id    bigint       NOT NULL,
    seq        integer      NOT NULL,
    code       varchar(255) NOT NULL,
    values     jsonb,
    lines      jsonb,
    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    created_by bigint       NOT NULL DEFAULT 0,
    updated_by bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_print_task_rows_seq CHECK (seq >= 1)
);

COMMENT ON TABLE print_task_rows IS '打印任务行（渲染数据包：code=主码内容（SKU 条码/库位编码/箱码/托盘码/单据号），values=字段绑定取值快照，lines=单据明细行——plan §7.2；重投递整体重写覆盖，装配为纯函数幂等）';
COMMENT ON COLUMN print_task_rows.task_id IS '打印任务（print_tasks 裸 ID 引用，域内同事务创建，不建 FK）';
COMMENT ON COLUMN print_task_rows.seq IS '行序（1 起）';

CREATE UNIQUE INDEX uk_print_task_rows_task_seq ON print_task_rows (task_id, seq);
