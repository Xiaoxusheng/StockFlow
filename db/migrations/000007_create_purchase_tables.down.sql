-- 000007 purchase 域回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS uk_quality_items_qc_line;
DROP TABLE IF EXISTS quality_items;

DROP INDEX IF EXISTS idx_quality_orders_warehouse_status;
DROP INDEX IF EXISTS idx_quality_orders_source;
DROP INDEX IF EXISTS uk_quality_orders_no;
DROP TABLE IF EXISTS quality_orders;

DROP INDEX IF EXISTS idx_putaway_tasks_inbound_no;
DROP INDEX IF EXISTS idx_putaway_tasks_status;
DROP INDEX IF EXISTS uk_putaway_tasks_no;
DROP TABLE IF EXISTS putaway_tasks;

DROP INDEX IF EXISTS uk_receipt_items_receipt_line;
DROP TABLE IF EXISTS receipt_items;

DROP INDEX IF EXISTS idx_receipts_inbound_no;
DROP INDEX IF EXISTS uk_receipts_idempotency;
DROP INDEX IF EXISTS uk_receipts_no;
DROP TABLE IF EXISTS receipts;

DROP INDEX IF EXISTS idx_inbound_items_sku;
DROP INDEX IF EXISTS uk_inbound_items_inbound_line;
DROP TABLE IF EXISTS inbound_items;

DROP INDEX IF EXISTS idx_inbound_orders_source;
DROP INDEX IF EXISTS idx_inbound_orders_warehouse_status;
DROP INDEX IF EXISTS uk_inbound_orders_no;
DROP TABLE IF EXISTS inbound_orders;

DROP INDEX IF EXISTS idx_purchase_order_items_sku;
DROP INDEX IF EXISTS uk_purchase_order_items_po_line;
DROP TABLE IF EXISTS purchase_order_items;

DROP INDEX IF EXISTS idx_purchase_orders_warehouse_status;
DROP INDEX IF EXISTS idx_purchase_orders_supplier;
DROP INDEX IF EXISTS uk_purchase_orders_no;
DROP TABLE IF EXISTS purchase_orders;
