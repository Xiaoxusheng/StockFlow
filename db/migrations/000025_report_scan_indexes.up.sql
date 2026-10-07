-- 000025 报表/追溯/扫描日志热路径索引补齐（2026-10-07 安全与性能修复轮 f12/f19/f23/f28）：
-- 只建扩展与索引，零建表（internal/database/migrations_test.go 表集合冻结断言不受影响）。
-- 全部列名均已对照既有 DDL 复核（000005 inventory_ledgers / 000007 quality_* /
-- 000013 scan_logs），命名沿用 idx_{table}_{cols} 惯例。

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---- inventory_ledgers（f12 流水报表/列表 + f28 末次移动聚合）----
-- flowStats（reports/repository.go flowStatsBase）：change_type=? AND created_at 范围，
-- ALL 仓库范围下此前无可用索引（000005 仅 (sku_id,created_at)/(warehouse_id,created_at)/(business_no)）。
CREATE INDEX idx_inventory_ledgers_type_created ON inventory_ledgers (change_type, created_at);
-- lastm/slow_moving/stagnant 末次移动聚合：MAX(created_at) GROUP BY warehouse_id, sku_id
-- （repository.go lastm CTE / repository_alerts.go slow_moving 分支），宽覆盖列序支撑
-- index-only scan，替代 append-only 宽表全表顺序扫描。
CREATE INDEX idx_inventory_ledgers_wh_sku_type_created ON inventory_ledgers (warehouse_id, sku_id, change_type, created_at);
-- 流水列表过滤（inventory/repository.go listLedgers：bin_id/batch_id 等值 + 时间排序）。
CREATE INDEX idx_inventory_ledgers_bin_created ON inventory_ledgers (bin_id, created_at);
CREATE INDEX idx_inventory_ledgers_batch_created ON inventory_ledgers (batch_id, created_at);
CREATE INDEX idx_inventory_ledgers_serial_created ON inventory_ledgers (serial_no, created_at);

-- ---- 质量追溯（f19：trace/不合格品列表三大检索入口）----
-- quality_items.batch_no 等值检索（service_quality_trace.go:229），000007 未建。
CREATE INDEX idx_quality_items_batch_no ON quality_items (batch_no);
-- quality_orders.source_no 单列等值（:233 按 source_no 查询用不上 (source_type,source_no) 复合左前缀）。
CREATE INDEX idx_quality_orders_source_no ON quality_orders (source_no);
-- qc_no ILIKE 模糊（:182），000022 trgm 未覆盖 quality_orders。
CREATE INDEX idx_quality_orders_qc_no_trgm ON quality_orders USING gin (qc_no gin_trgm_ops);

-- ---- PDA 扫描日志（f23：keyword 三列 ILIKE '%kw%'，btree 对 %kw% 无效）----
CREATE INDEX idx_scan_logs_raw_code_trgm ON scan_logs USING gin (raw_code gin_trgm_ops);
CREATE INDEX idx_scan_logs_page_trgm ON scan_logs USING gin (page gin_trgm_ops);
CREATE INDEX idx_scan_logs_device_code_trgm ON scan_logs USING gin (device_code gin_trgm_ops);
