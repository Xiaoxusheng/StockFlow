-- 000008 sales 域回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS idx_shipments_warehouse_status;
DROP INDEX IF EXISTS idx_shipments_outbound_no;
DROP INDEX IF EXISTS uk_shipments_idempotency;
DROP INDEX IF EXISTS uk_shipments_no;
DROP TABLE IF EXISTS shipments;

DROP INDEX IF EXISTS uk_packing_items_package_outbound_line;
DROP TABLE IF EXISTS packing_items;

DROP INDEX IF EXISTS idx_packing_records_outbound_no;
DROP INDEX IF EXISTS uk_packing_records_idempotency;
DROP INDEX IF EXISTS uk_packing_records_no;
DROP TABLE IF EXISTS packing_records;

DROP INDEX IF EXISTS idx_check_tasks_warehouse_status;
DROP INDEX IF EXISTS idx_check_tasks_outbound_no;
DROP INDEX IF EXISTS uk_check_tasks_no;
DROP TABLE IF EXISTS check_tasks;

DROP INDEX IF EXISTS idx_pick_tasks_warehouse_status;
DROP INDEX IF EXISTS idx_pick_tasks_assignee_status;
DROP INDEX IF EXISTS idx_pick_tasks_outbound_no;
DROP INDEX IF EXISTS uk_pick_tasks_no;
DROP TABLE IF EXISTS pick_tasks;

DROP INDEX IF EXISTS idx_allocation_records_sku_batch;
DROP INDEX IF EXISTS idx_allocation_records_outbound_no;
DROP TABLE IF EXISTS allocation_records;

DROP INDEX IF EXISTS idx_outbound_items_sku;
DROP INDEX IF EXISTS uk_outbound_items_outbound_line;
DROP TABLE IF EXISTS outbound_items;

DROP INDEX IF EXISTS idx_outbound_orders_warehouse_status;
DROP INDEX IF EXISTS idx_outbound_orders_so_no;
DROP INDEX IF EXISTS uk_outbound_orders_no;
DROP TABLE IF EXISTS outbound_orders;

DROP INDEX IF EXISTS idx_sales_order_items_sku;
DROP INDEX IF EXISTS uk_sales_order_items_so_line;
DROP TABLE IF EXISTS sales_order_items;

DROP INDEX IF EXISTS idx_sales_orders_warehouse_status;
DROP INDEX IF EXISTS idx_sales_orders_customer;
DROP INDEX IF EXISTS uk_sales_orders_no;
DROP TABLE IF EXISTS sales_orders;
