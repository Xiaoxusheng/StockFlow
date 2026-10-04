-- 000009 stockops 域回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS idx_count_differences_count_status;
DROP INDEX IF EXISTS uk_count_differences_count_line;
DROP TABLE IF EXISTS count_differences;

DROP INDEX IF EXISTS idx_count_items_count_row;
DROP INDEX IF EXISTS uk_count_items_row_serial;
DROP TABLE IF EXISTS count_items;

DROP INDEX IF EXISTS idx_count_orders_warehouse_status;
DROP INDEX IF EXISTS uk_count_orders_no;
DROP TABLE IF EXISTS count_orders;

DROP INDEX IF EXISTS idx_transfer_items_sku;
DROP INDEX IF EXISTS uk_transfer_items_transfer_line;
DROP TABLE IF EXISTS transfer_items;

DROP INDEX IF EXISTS idx_transfer_orders_to_wh_status;
DROP INDEX IF EXISTS idx_transfer_orders_from_wh_status;
DROP INDEX IF EXISTS uk_transfer_orders_no;
DROP TABLE IF EXISTS transfer_orders;
