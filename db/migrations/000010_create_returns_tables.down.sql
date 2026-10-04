-- 000010 returns 域回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS idx_exceptions_source;
DROP INDEX IF EXISTS idx_exceptions_type_status;
DROP INDEX IF EXISTS uk_exceptions_no;
DROP TABLE IF EXISTS exceptions;

DROP INDEX IF EXISTS idx_return_items_sku;
DROP INDEX IF EXISTS uk_return_items_return_line;
DROP TABLE IF EXISTS return_items;

DROP INDEX IF EXISTS idx_return_orders_source_no;
DROP INDEX IF EXISTS idx_return_orders_type_status;
DROP INDEX IF EXISTS uk_return_orders_no;
DROP TABLE IF EXISTS return_orders;
