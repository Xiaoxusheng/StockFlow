-- 000025 回滚：drop 补齐的报表/追溯/扫描日志索引。
DROP INDEX IF EXISTS idx_scan_logs_device_code_trgm;
DROP INDEX IF EXISTS idx_scan_logs_page_trgm;
DROP INDEX IF EXISTS idx_scan_logs_raw_code_trgm;
DROP INDEX IF EXISTS idx_quality_orders_qc_no_trgm;
DROP INDEX IF EXISTS idx_quality_orders_source_no;
DROP INDEX IF EXISTS idx_quality_items_batch_no;
DROP INDEX IF EXISTS idx_inventory_ledgers_serial_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_batch_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_bin_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_wh_sku_type_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_type_created;
-- pg_trgm 扩展由 000022 引入，此处不回滚扩展本身。
