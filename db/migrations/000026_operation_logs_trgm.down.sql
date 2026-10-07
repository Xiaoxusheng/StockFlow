-- 000026 回滚：删除 operation_logs 检索 trgm 索引（扩展 pg_trgm 由 000022 引入，不回滚）。
DROP INDEX IF EXISTS idx_operation_logs_ip_trgm;
DROP INDEX IF EXISTS idx_operation_logs_username_trgm;
