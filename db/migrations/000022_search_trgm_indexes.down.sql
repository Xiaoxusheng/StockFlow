-- 000022 回滚：按 000022_search_trgm_indexes.up.sql 的逆序删索引 + 卸扩展
-- （efficiency-layer-phase1 §3.3；DROP EXTENSION 需安装权限，§8.5 风险同源）。

DROP INDEX IF EXISTS idx_shipments_no_trgm;
DROP INDEX IF EXISTS idx_exceptions_no_trgm;
DROP INDEX IF EXISTS idx_count_orders_no_trgm;
DROP INDEX IF EXISTS idx_transfer_orders_no_trgm;
DROP INDEX IF EXISTS idx_outbound_orders_no_trgm;
DROP INDEX IF EXISTS idx_sales_orders_no_trgm;
DROP INDEX IF EXISTS idx_inbound_orders_no_trgm;
DROP INDEX IF EXISTS idx_purchase_orders_no_trgm;
DROP INDEX IF EXISTS idx_suppliers_name_trgm;
DROP INDEX IF EXISTS idx_customers_name_trgm;
DROP INDEX IF EXISTS idx_warehouses_name_trgm;
DROP INDEX IF EXISTS idx_bins_code_trgm;
DROP INDEX IF EXISTS idx_serial_numbers_no_trgm;
DROP INDEX IF EXISTS idx_batches_batch_no_trgm;
DROP INDEX IF EXISTS idx_barcodes_barcode_trgm;
DROP INDEX IF EXISTS idx_skus_code_trgm;
DROP INDEX IF EXISTS idx_products_name_trgm;
DROP EXTENSION IF EXISTS pg_trgm;
