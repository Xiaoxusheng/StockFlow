-- 000022 B2 全局业务搜索 pg_trgm 索引（efficiency-layer-phase1 §3.3）：
-- 名称/编码类列建 GIN trigram 索引，加速 GET /api/search 的 ILIKE '%q%' 模糊匹配。
-- 只建扩展与索引，零建表（internal/database/migrations_test.go 表集合冻结断言不受影响）。
--
-- DDL 逐列复核结论（000003/000004/000005/000008，efficiency-layer-phase1 §3.3 要求）：
-- 计划原文含 idx_skus_name_trgm ON skus (name)——000003 DDL 复核确认 skus 表无
-- name 列（SKU 名称在 products.name，经 skus.product_id 关联），按真实 DDL 修正剔除；
-- 搜索 SQL 对 sku 类型改匹配 skus.code + products.name（products.name 索引由
-- idx_products_name_trgm 覆盖）。其余各列均与 DDL 一致。

CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- 名称/编码类
CREATE INDEX idx_products_name_trgm        ON products        USING gin (name gin_trgm_ops);
CREATE INDEX idx_skus_code_trgm            ON skus            USING gin (code gin_trgm_ops);
CREATE INDEX idx_barcodes_barcode_trgm     ON barcodes        USING gin (barcode gin_trgm_ops);
CREATE INDEX idx_batches_batch_no_trgm     ON batches         USING gin (batch_no gin_trgm_ops);
CREATE INDEX idx_serial_numbers_no_trgm    ON serial_numbers  USING gin (serial_no gin_trgm_ops);
CREATE INDEX idx_bins_code_trgm            ON bins            USING gin (code gin_trgm_ops);
CREATE INDEX idx_warehouses_name_trgm      ON warehouses      USING gin (name gin_trgm_ops);
CREATE INDEX idx_customers_name_trgm       ON customers       USING gin (name gin_trgm_ops);
CREATE INDEX idx_suppliers_name_trgm       ON suppliers       USING gin (name gin_trgm_ops);
-- 单据号类
CREATE INDEX idx_purchase_orders_no_trgm   ON purchase_orders USING gin (po_no gin_trgm_ops);
CREATE INDEX idx_inbound_orders_no_trgm    ON inbound_orders  USING gin (inbound_no gin_trgm_ops);
CREATE INDEX idx_sales_orders_no_trgm      ON sales_orders    USING gin (so_no gin_trgm_ops);
CREATE INDEX idx_outbound_orders_no_trgm   ON outbound_orders USING gin (outbound_no gin_trgm_ops);
CREATE INDEX idx_transfer_orders_no_trgm   ON transfer_orders USING gin (transfer_no gin_trgm_ops);
CREATE INDEX idx_count_orders_no_trgm      ON count_orders    USING gin (count_no gin_trgm_ops);
CREATE INDEX idx_exceptions_no_trgm        ON exceptions      USING gin (exception_no gin_trgm_ops);
CREATE INDEX idx_shipments_no_trgm         ON shipments       USING gin (shipment_no gin_trgm_ops);
