-- 000002 审计日志表回滚

DROP INDEX IF EXISTS idx_login_logs_created_at;
DROP INDEX IF EXISTS idx_login_logs_username_created;
DROP INDEX IF EXISTS idx_operation_logs_created_at;
DROP INDEX IF EXISTS idx_operation_logs_object;
DROP INDEX IF EXISTS idx_operation_logs_module_created;
DROP INDEX IF EXISTS idx_operation_logs_user_created;

DROP TABLE IF EXISTS login_logs;
DROP TABLE IF EXISTS operation_logs;
