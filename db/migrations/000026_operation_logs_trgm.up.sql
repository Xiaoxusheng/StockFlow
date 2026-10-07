-- 000026 operation_logs 检索 pg_trgm 索引（安全渗透检查 S5/性能建议项）：
-- 系统管理-操作日志按 username/ip ILIKE '%kw%' 模糊检索（internal/sysops/logs.go:79-83），
-- operation_logs 为每次 API 操作写一条的审计 append-only 大表，既有 btree 索引
-- （created_at/module/user/object）对 %kw% 模式无效，检索退化为顺序扫描×2（COUNT+LIST）。
-- 比照 000022 口径：只建扩展与索引，零建表（internal/database/migrations_test.go 表集合
-- 冻结断言不受影响）；pg_trgm 扩展已由 000022 创建，IF NOT EXISTS 幂等兜底。

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_operation_logs_username_trgm ON operation_logs USING gin (username gin_trgm_ops);
CREATE INDEX idx_operation_logs_ip_trgm       ON operation_logs USING gin (ip gin_trgm_ops);
