-- 000002 审计日志表：operation_logs / login_logs
-- 依据：backend-m1-plan.md §4.4/§6.2、database.md §7（审计数据保护——不可破坏）、
--       permission.md §6（敏感操作强制审计）、architecture.md §8。
-- 约定：
--   * 纯日志表按 database.md §3 但书：无 updated_by/deleted_at，只增不改（append-only）。
--   * 应用层（internal/middleware 审计 helper）只 INSERT；DB 账号分层由 db/grants/app_grants.sql
--     强制（业务运行账号仅 SELECT+INSERT，无 UPDATE/DELETE）。
--   * 快照列 jsonb：关键更新必填 before/after（architecture.md §8.1），密码/Token 等敏感字段
--     由写入方脱敏（architecture.md §6）。

CREATE TABLE operation_logs (
    id                bigserial    PRIMARY KEY,
    request_id        varchar(64)  NOT NULL DEFAULT '',
    user_id           bigint       NOT NULL DEFAULT 0,
    username          varchar(64)  NOT NULL DEFAULT '',
    ip                varchar(64)  NOT NULL DEFAULT '',
    user_agent        varchar(512) NOT NULL DEFAULT '',
    module            varchar(64)  NOT NULL,
    object_type       varchar(64)  NOT NULL DEFAULT '',
    object_id         bigint       NOT NULL DEFAULT 0,
    action            varchar(64)  NOT NULL,
    method            varchar(16)  NOT NULL DEFAULT '',
    path              varchar(512) NOT NULL DEFAULT '',
    success           boolean      NOT NULL DEFAULT TRUE,
    error_code        varchar(64)  NOT NULL DEFAULT '',
    request_snapshot  jsonb,
    before_snapshot   jsonb,
    after_snapshot    jsonb,
    created_at        timestamptz  NOT NULL DEFAULT now()
);

COMMENT ON TABLE operation_logs IS '关键操作日志（database.md §7 审计数据：可查询可追溯不可篡改；M1 必写清单见 backend-m1-plan §4.4）';
COMMENT ON COLUMN operation_logs.request_id IS '请求链路 ID（RequestID 中间件），贯穿访问日志与业务日志';

CREATE TABLE login_logs (
    id           bigserial    PRIMARY KEY,
    user_id      bigint       NOT NULL DEFAULT 0,
    username     varchar(64)  NOT NULL,
    ip           varchar(64)  NOT NULL DEFAULT '',
    user_agent   varchar(512) NOT NULL DEFAULT '',
    success      boolean      NOT NULL DEFAULT FALSE,
    fail_reason  varchar(255) NOT NULL DEFAULT '',
    created_at   timestamptz  NOT NULL DEFAULT now()
);

COMMENT ON TABLE login_logs IS '登录日志（permission.md §3.3：每次登录成功/失败均记录；在线会话视图以 Redis 为准，登录记录以本表落库为准）';

-- 索引（backend-m1-plan §6.2）
CREATE INDEX idx_operation_logs_user_created ON operation_logs (user_id, created_at);
CREATE INDEX idx_operation_logs_module_created ON operation_logs (module, created_at);
CREATE INDEX idx_operation_logs_object ON operation_logs (object_type, object_id);
CREATE INDEX idx_operation_logs_created_at ON operation_logs (created_at);
CREATE INDEX idx_login_logs_username_created ON login_logs (username, created_at);
CREATE INDEX idx_login_logs_created_at ON login_logs (created_at);
