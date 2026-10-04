-- 000011 datax 平台支撑表：import_tasks / import_task_rows / export_tasks / files
-- 依据：backend-m3-plan.md §5（000011 冻结 DDL）、§6（Excel 导入导出与文件中心）、excel.md §1–§7、api.md §5。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：source_file_id/error_file_id/file_id 为 files 裸 ID 引用，
--     created_by 为 users 裸 ID 引用，一致性由 Service 层校验。
--   * 任务状态列挂 CHECK，与状态机同源（plan §13.2 状态守卫幂等的 DDL 防线）：
--     import_tasks 六态（向导 PARSED/VALIDATED + 执行 EXECUTING + 终态）；
--     import_task_rows 六态（断点续跑依据，plan §6.2）；export_tasks 五态。
--   * 导入/导出任务单号唯一（uk_*_no），经 internal/docnum IMP/EXP 前缀发放（plan §12.3）。
--   * files 为文件中心统一登记表：存储名/路径一律服务端生成（api.md §5 防路径穿越）；
--     下载记录复用 operation_logs（module=file, action=download），不建独立下载记录表
--     （plan §5 000011 注：避免第二真相来源）；软删除 deleted_at（plan §5：deleted_at NULL）。

CREATE TABLE import_tasks (
    id            bigserial   PRIMARY KEY,
    import_no     varchar(64) NOT NULL,
    import_type   varchar(32) NOT NULL,
    status        varchar(32) NOT NULL DEFAULT 'PARSED',
    source_file_id bigint     NOT NULL DEFAULT 0,
    total_rows    integer     NOT NULL DEFAULT 0,
    valid_rows    integer     NOT NULL DEFAULT 0,
    error_rows    integer     NOT NULL DEFAULT 0,
    success_rows  integer     NOT NULL DEFAULT 0,
    failed_rows   integer     NOT NULL DEFAULT 0,
    error_file_id bigint      NOT NULL DEFAULT 0,
    error_message text        NOT NULL DEFAULT '',
    started_at    timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint      NOT NULL DEFAULT 0,
    updated_by    bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_import_tasks_type CHECK (import_type IN ('PRODUCT', 'SKU', 'SUPPLIER', 'CUSTOMER', 'WAREHOUSE', 'LOCATION', 'PURCHASE_ORDER', 'SALES_ORDER', 'INITIAL_INVENTORY')),
    CONSTRAINT chk_import_tasks_status CHECK (status IN ('PARSED', 'VALIDATED', 'EXECUTING', 'SUCCESS', 'PARTIAL_SUCCESS', 'FAILED')),
    CONSTRAINT chk_import_tasks_rows CHECK (total_rows >= 0 AND valid_rows >= 0 AND error_rows >= 0 AND success_rows >= 0 AND failed_rows >= 0)
);

COMMENT ON TABLE import_tasks IS 'Excel 导入任务（backend-m3-plan §6.2 向导状态机：上传解析 PARSED → 校验 VALIDATED → 确认 EXECUTING → SUCCESS/PARTIAL_SUCCESS/FAILED；confirm 状态守卫防重放，plan §13.2）';
COMMENT ON COLUMN import_tasks.import_no IS '导入任务单号（docnum IMP 前缀，plan §12.3）';
COMMENT ON COLUMN import_tasks.import_type IS '导入类型九值（excel §1.1 全量：PRODUCT/SKU/SUPPLIER/CUSTOMER/WAREHOUSE/LOCATION/PURCHASE_ORDER/SALES_ORDER/INITIAL_INVENTORY）';
COMMENT ON COLUMN import_tasks.source_file_id IS '上传源文件（files 裸 ID 引用，不建 FK）';
COMMENT ON COLUMN import_tasks.total_rows IS '解析总行数（数据行，1 起；plan §6.2：超 datax.import_max_rows 上传即拒）';
COMMENT ON COLUMN import_tasks.valid_rows IS '校验通过行数';
COMMENT ON COLUMN import_tasks.error_rows IS '校验失败行数（>0 生成错误 Excel，error_file_id 登记于 files）';
COMMENT ON COLUMN import_tasks.success_rows IS '已成功写入行数（断点续跑与进度推导来源，plan §13.3）';
COMMENT ON COLUMN import_tasks.failed_rows IS '执行失败行数';
COMMENT ON COLUMN import_tasks.error_file_id IS '错误 Excel 文件（files 裸 ID 引用；plan §6.2 errorFileUrl）';

CREATE UNIQUE INDEX uk_import_tasks_no ON import_tasks (import_no);
CREATE INDEX idx_import_tasks_type_created ON import_tasks (import_type, created_at);
CREATE INDEX idx_import_tasks_creator_created ON import_tasks (created_by, created_at);
CREATE INDEX idx_import_tasks_status ON import_tasks (status);

CREATE TABLE import_task_rows (
    id         bigserial   PRIMARY KEY,
    task_id    bigint      NOT NULL,
    row_no     integer     NOT NULL,
    raw        jsonb,
    parsed     jsonb,
    status     varchar(16) NOT NULL DEFAULT 'RAW',
    errors     jsonb,
    batch_no   integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint      NOT NULL DEFAULT 0,
    updated_by bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_import_task_rows_row_no CHECK (row_no >= 1),
    CONSTRAINT chk_import_task_rows_batch_no CHECK (batch_no >= 0),
    CONSTRAINT chk_import_task_rows_status CHECK (status IN ('RAW', 'VALID', 'INVALID', 'QUEUED', 'SUCCESS', 'FAILED'))
);

COMMENT ON TABLE import_task_rows IS '导入任务行（plan §5 000011：raw=原行单元格，parsed=类型化解析结果；errors=[{row,column,message}] 定位错误列——excel §1.4 逐行错误定位）';
COMMENT ON COLUMN import_task_rows.task_id IS '导入任务（import_tasks 裸 ID 引用，域内同事务创建，不建 FK）';
COMMENT ON COLUMN import_task_rows.row_no IS '数据行号（1 起，不含表头行）';
COMMENT ON COLUMN import_task_rows.status IS '行状态（plan §6.2 断点续跑依据：仅重试 QUEUED/FAILED 行，SUCCESS 行跳过；excel §6.3 幂等）';
COMMENT ON COLUMN import_task_rows.batch_no IS '分批事务批号（datax.batch_size 分批，单批失败不影响已完成批次——excel §6.2）';
COMMENT ON COLUMN import_task_rows.errors IS '行错误（jsonb 数组 [{row,column,message}]，校验/执行两阶段共用）';

CREATE UNIQUE INDEX uk_import_task_rows_task_row ON import_task_rows (task_id, row_no);
CREATE INDEX idx_import_task_rows_task_status ON import_task_rows (task_id, status);
CREATE INDEX idx_import_task_rows_task_batch ON import_task_rows (task_id, batch_no);

CREATE TABLE export_tasks (
    id            bigserial   PRIMARY KEY,
    export_no     varchar(64) NOT NULL,
    module        varchar(32) NOT NULL,
    scope         varchar(16) NOT NULL,
    params        jsonb,
    status        varchar(32) NOT NULL DEFAULT 'QUEUED',
    progress      integer     NOT NULL DEFAULT 0,
    total_rows    bigint      NOT NULL DEFAULT 0,
    file_id       bigint      NOT NULL DEFAULT 0,
    error_message text        NOT NULL DEFAULT '',
    started_at    timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint      NOT NULL DEFAULT 0,
    updated_by    bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_export_tasks_module CHECK (module IN ('PRODUCT', 'SKU', 'SUPPLIER', 'CUSTOMER', 'WAREHOUSE', 'LOCATION', 'PURCHASE_ORDER', 'PURCHASE_INBOUND', 'QUALITY', 'SALES_OUTBOUND', 'INVENTORY', 'INVENTORY_LEDGER', 'TRANSFER', 'COUNT', 'EXCEPTION', 'REPORT')),
    CONSTRAINT chk_export_tasks_scope CHECK (scope IN ('CURRENT_PAGE', 'SELECTED', 'ALL', 'BY_FILTER', 'TIME_RANGE')),
    CONSTRAINT chk_export_tasks_status CHECK (status IN ('QUEUED', 'PROCESSING', 'SUCCESS', 'PARTIAL_SUCCESS', 'FAILED')),
    CONSTRAINT chk_export_tasks_progress CHECK (progress >= 0 AND progress <= 100)
);

COMMENT ON TABLE export_tasks IS 'Excel 导出任务（backend-m3-plan §6.3 流式导出：QUEUED → PROCESSING → SUCCESS/PARTIAL_SUCCESS/FAILED；导出属敏感操作，创建与下载均记 operation_logs——permission §6）';
COMMENT ON COLUMN export_tasks.export_no IS '导出任务单号（docnum EXP 前缀，plan §12.3）';
COMMENT ON COLUMN export_tasks.module IS '导出模块十六值（plan §6.1 全量：基础资料 4 + 仓库 2 + 采购 3 + 销售 1 + 库存 2 + 作业 2 + 异常 1 + 报表 1）';
COMMENT ON COLUMN export_tasks.scope IS '导出范围五值（excel §2.2；TIME_RANGE=按创建时间区间，params 标准键 time_from/time_to；SELECTED 必带 ids≤1000）';
COMMENT ON COLUMN export_tasks.params IS '导出参数（ids/page/filters/时间范围；BY_FILTER 的 filters 键为各域列表筛选白名单，plan §18.5）';
COMMENT ON COLUMN export_tasks.progress IS '进度 0–100（每 batch 更新一次，批外更新失败不回滚业务批次——plan §13.3；仅本表有进度列）';
COMMENT ON COLUMN export_tasks.file_id IS '导出产物文件（files 裸 ID 引用，expires_at=retention_days）';

CREATE UNIQUE INDEX uk_export_tasks_no ON export_tasks (export_no);
CREATE INDEX idx_export_tasks_module_created ON export_tasks (module, created_at);
CREATE INDEX idx_export_tasks_creator_created ON export_tasks (created_by, created_at);
CREATE INDEX idx_export_tasks_status ON export_tasks (status);

CREATE TABLE files (
    id            bigserial    PRIMARY KEY,
    file_name     varchar(255) NOT NULL,
    stored_name   varchar(128) NOT NULL,
    storage_path  varchar(512) NOT NULL,
    mime_type     varchar(128) NOT NULL DEFAULT '',
    file_type     varchar(16)  NOT NULL,
    size_bytes    bigint       NOT NULL DEFAULT 0,
    module        varchar(64)  NOT NULL,
    business_no   varchar(64)  NOT NULL DEFAULT '',
    uploader_id   bigint       NOT NULL DEFAULT 0,
    uploader_name varchar(64)  NOT NULL DEFAULT '',
    expires_at    timestamptz,
    deleted_at    timestamptz,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    created_by    bigint       NOT NULL DEFAULT 0,
    updated_by    bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_files_size_bytes CHECK (size_bytes >= 0)
);

COMMENT ON TABLE files IS '文件中心统一登记表（backend-m3-plan §6.4、excel §7、api §5；database.md §2 注：attachments 由本表承载，不建第二套附件表）';
COMMENT ON COLUMN files.file_name IS '原始文件名（仅展示用途，绝不参与存储路径拼装——api §5 服务端重命名红线）';
COMMENT ON COLUMN files.stored_name IS '服务端生成存储名（uuid + 白名单扩展名，api §5：禁止使用用户原始文件名作存储路径）';
COMMENT ON COLUMN files.storage_path IS '相对 storage.root 的存储路径（服务端生成 yyyyMM/stored_name，防路径穿越；物理落点 = storage.root/storage_path）';
COMMENT ON COLUMN files.mime_type IS 'MIME 类型（http.DetectContentType 嗅探值，与扩展名白名单交叉校验——api §5）';
COMMENT ON COLUMN files.file_type IS '文件类型（扩展名白名单值，internal/storage 白名单注册表冻结）';
COMMENT ON COLUMN files.module IS '归属模块（datax/printing/devices/...；列表按 module 过滤可见——plan §6.4 数据权限）';
COMMENT ON COLUMN files.business_no IS '业务单号逻辑引用（如 IMP-/EXP- 任务号；跨域不建 FK）';
COMMENT ON COLUMN files.uploader_id IS '上传人（users 裸 ID 引用；与 created_by 同源冗余，业务侧用 uploader 语义列）';
COMMENT ON COLUMN files.expires_at IS '过期时间（datax.file_retention_days 推导；NULL=不过期；file_cleanup 定时清理——plan §4.3）';
COMMENT ON COLUMN files.deleted_at IS '软删除时间（plan §6.4 删除=软删 + 审计；物理删除由 file_cleanup 承担）';

CREATE INDEX idx_files_module_business ON files (module, business_no);
CREATE INDEX idx_files_expires_at ON files (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_files_uploader_created ON files (uploader_id, created_at);
