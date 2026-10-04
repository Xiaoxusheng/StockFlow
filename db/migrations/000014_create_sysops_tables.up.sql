-- 000014 平台运维表：scheduled_jobs / scheduled_job_runs / system_configs / notifications / backup_records
-- 依据：backend-m3-plan.md §5（000014 冻结 DDL）、§4.3（定时任务框架）、§10（日志/系统/备份/通知）、
--       architecture.md §9–§10、deployment.md §4–§5（Orchestrator 裁决②：备份=登记+外部执行混合模式）。
-- 约定：
--   * 不建任何外键（plan §5 通用规则）：job_code/user_id/updated_by/created_by 一律裸值/裸 ID。
--   * scheduled_jobs 为 cron 注册表的 DB 覆盖层：代码注册表为缺省值源，启动时 upsert 注册表行，
--     DB 行 enabled 为管理员启停覆盖（plan §4.3 管理要求；cron 表达式 M3 冻结在代码，不开放修改）。
--   * scheduled_job_runs 为执行日志（非审计数据，plan §5 000014 注）：允许 UPDATE 回填结束状态，
--     不入 grants 严格清单；任务执行日志与预警写入不同事务（执行日志不回滚业务结果——plan §4.3）。
--   * notifications.dedup_key 部分唯一索引 = 预警扫描幂等防线：同一对象同一收件人同日只通知一次；
--     dedup_key 落库值必须复合收件人（对象键:{user_id}，plan §4.3——否则第二个收件人 INSERT 撞唯一索引）。
--   * backup_records 混合模式状态机（plan §10.5）：REQUESTED=应用侧已登记待部署侧执行器拾取；
--     在途部分唯一索引保证并发手动触发 409 拒绝；备份文件不入 files 表（运维资产与业务文件分域）。
--   * "group"/"trigger" 为 PostgreSQL 保留字，DDL 一律带引号书写。

CREATE TABLE scheduled_jobs (
    id                   bigserial   PRIMARY KEY,
    code                 varchar(64) NOT NULL,
    name                 varchar(128) NOT NULL,
    cron_expr            varchar(64) NOT NULL,
    enabled              boolean     NOT NULL DEFAULT TRUE,
    last_run_at          timestamptz,
    last_run_status      varchar(16) NOT NULL DEFAULT 'never',
    last_run_duration_ms bigint      NOT NULL DEFAULT 0,
    next_run_at          timestamptz,
    remark               text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint      NOT NULL DEFAULT 0,
    updated_by           bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_scheduled_jobs_last_run_status CHECK (last_run_status IN ('success', 'failed', 'running', 'never'))
);

COMMENT ON TABLE scheduled_jobs IS '定时任务注册（backend-m3-plan §4.3：代码注册表五任务为缺省，DB 行 enabled 为启停覆盖；PUT 状态后进程内 cron 热更新（RemoveEntry/AddFunc 单实例语义））';
COMMENT ON COLUMN scheduled_jobs.code IS '任务编码（注册表冻结：inventory_low_stock_scan/inventory_expiry_scan/inventory_stagnant_scan/task_timeout_scan/file_cleanup）';
COMMENT ON COLUMN scheduled_jobs.cron_expr IS 'cron 表达式（M3 冻结在代码注册表，运行时不开放修改——plan §10.3 防任意 cron 注入）';
COMMENT ON COLUMN scheduled_jobs.last_run_status IS '最近执行状态（小写值域：success/failed/running/never——plan §5 000014 冻结）';

CREATE UNIQUE INDEX uk_scheduled_jobs_code ON scheduled_jobs (code);

CREATE TABLE scheduled_job_runs (
    id          bigserial   PRIMARY KEY,
    job_code    varchar(64) NOT NULL,
    "trigger"   varchar(16) NOT NULL,
    start_at    timestamptz NOT NULL,
    end_at      timestamptz,
    success     boolean     NOT NULL DEFAULT FALSE,
    duration_ms bigint      NOT NULL DEFAULT 0,
    message     text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    created_by  bigint      NOT NULL DEFAULT 0,
    updated_by  bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_scheduled_job_runs_trigger CHECK ("trigger" IN ('SCHEDULED', 'MANUAL', 'SKIPPED'))
);

COMMENT ON TABLE scheduled_job_runs IS '定时任务执行日志（plan §4.3：开始/结束/结果/耗时/触发方式；SKIPPED=防重入未获执行权——advisory lock 未获取或同进程仍在执行）';
COMMENT ON COLUMN scheduled_job_runs.job_code IS '任务编码（scheduled_jobs.code 逻辑引用，不建 FK）';
COMMENT ON COLUMN scheduled_job_runs."trigger" IS '触发方式（SCHEDULED=调度触发/MANUAL=手动触发/SKIPPED=防重入跳过；trigger 为 PG 保留字带引号）';
COMMENT ON COLUMN scheduled_job_runs.message IS '执行消息（成功摘要或失败原因；失败同时记站内告警，下一周期自然重试——plan §4.3 不做同周期内重试）';

CREATE INDEX idx_scheduled_job_runs_job_start ON scheduled_job_runs (job_code, start_at);
CREATE INDEX idx_scheduled_job_runs_start ON scheduled_job_runs (start_at);

CREATE TABLE system_configs (
    key        varchar(128) PRIMARY KEY,
    value      text        NOT NULL DEFAULT '',
    name       varchar(128) NOT NULL DEFAULT '',
    "group"    varchar(64) NOT NULL DEFAULT '',
    type       varchar(16) NOT NULL DEFAULT 'text',
    options    jsonb,
    remark     text        NOT NULL DEFAULT '',
    readonly   boolean     NOT NULL DEFAULT FALSE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    created_by bigint      NOT NULL DEFAULT 0,
    updated_by bigint      NOT NULL DEFAULT 0,
    CONSTRAINT chk_system_configs_type CHECK (type IN ('text', 'number', 'boolean', 'enum'))
);

COMMENT ON TABLE system_configs IS '系统配置（键值统一字符串存储；type 驱动前端渲染；readonly 项服务端拒绝写入——plan §10.2；修改逐项 middleware.Audit，permission §6）';
COMMENT ON TABLE system_configs IS '"group" 为 PG 保留字带引号；M3 seed 键清单见 backend-m3-plan §10.2（inventory.alert.* / task.timeout.* / insight.replenishment.* 等）';

CREATE TABLE notifications (
    id         bigserial    PRIMARY KEY,
    user_id    bigint,
    type       varchar(16)  NOT NULL,
    title      varchar(255) NOT NULL,
    content    text         NOT NULL DEFAULT '',
    dedup_key  varchar(128),
    read       boolean      NOT NULL DEFAULT FALSE,
    read_at    timestamptz,
    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    created_by bigint       NOT NULL DEFAULT 0,
    updated_by bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_notifications_type CHECK (type IN ('SYSTEM', 'APPROVAL', 'STOCK_ALERT', 'EXPIRY_ALERT', 'EXCEPTION', 'TASK'))
);

COMMENT ON TABLE notifications IS '站内通知（architecture §10；M3 预警/告警均解析为具体收件人逐行落库，user_id 恒非空——NULL 仅为后续广播演进保留，plan §10.6）';
COMMENT ON COLUMN notifications.type IS '通知类型（architecture §10 六值）';
COMMENT ON COLUMN notifications.dedup_key IS '去重键（=对象键:{user_id} 复合收件人，如 lowstock:20261003:SKU-001:WH-A:42——plan §4.3；uk_notifications_dedup 保证同对象同收件人同日只通知一次）';
COMMENT ON COLUMN notifications.read IS '已读标记（read 为 PG 非保留关键字；置位同事务回填 read_at）';

CREATE UNIQUE INDEX uk_notifications_dedup ON notifications (dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX idx_notifications_user_read_created ON notifications (user_id, read, created_at);

CREATE TABLE backup_records (
    id          bigserial    PRIMARY KEY,
    file_name   varchar(255) NOT NULL DEFAULT '',
    file_path   varchar(512) NOT NULL DEFAULT '',
    size_bytes  bigint       NOT NULL DEFAULT 0,
    "trigger"   varchar(16)  NOT NULL,
    status      varchar(16)  NOT NULL DEFAULT 'REQUESTED',
    message     text         NOT NULL DEFAULT '',
    started_at  timestamptz,
    finished_at timestamptz,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint       NOT NULL DEFAULT 0,
    updated_by  bigint       NOT NULL DEFAULT 0,
    CONSTRAINT chk_backup_records_trigger CHECK ("trigger" IN ('AUTO', 'MANUAL')),
    CONSTRAINT chk_backup_records_status CHECK (status IN ('REQUESTED', 'RUNNING', 'SUCCESS', 'FAILED')),
    CONSTRAINT chk_backup_records_size_bytes CHECK (size_bytes >= 0)
);

COMMENT ON TABLE backup_records IS '备份登记（Orchestrator 裁决②混合模式——plan §10.5：应用侧只登记/核对，pg_dump 由部署侧执行器轮询 REQUESTED 记录 FOR UPDATE SKIP LOCKED 拾取执行并回写）';
COMMENT ON COLUMN backup_records.file_path IS '备份文件相对 storage.root 路径（storage.root/backups/ 内；执行器回写；下载走 /api/system/backups/{id}/download）';
COMMENT ON COLUMN backup_records."trigger" IS '触发方式（AUTO=部署侧执行器调度/MANUAL=管理端登记；trigger 为 PG 保留字带引号）';
COMMENT ON COLUMN backup_records.status IS '状态机（REQUESTED=已登记待拾取/RUNNING=执行中/SUCCESS/FAILED；RUNNING 超 30 分钟由 file_cleanup 标记 FAILED + 告警——plan §4.3/§10.5）';

CREATE INDEX idx_backup_records_status_created ON backup_records (status, created_at);
CREATE UNIQUE INDEX uk_backup_records_inflight ON backup_records ("trigger") WHERE status IN ('REQUESTED', 'RUNNING');
