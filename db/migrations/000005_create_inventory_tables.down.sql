-- 000005 inventory 域回滚：DROP 顺序与 up 相反

DROP INDEX IF EXISTS idx_inventory_adjustments_warehouse_status;
DROP INDEX IF EXISTS uk_inventory_adjustments_no;
DROP TABLE IF EXISTS inventory_adjustments;

DROP INDEX IF EXISTS idx_inventory_ledgers_business_no;
DROP INDEX IF EXISTS idx_inventory_ledgers_warehouse_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_sku_created;
DROP INDEX IF EXISTS uk_inventory_ledgers_idempotency_key;
DROP INDEX IF EXISTS uk_inventory_ledgers_ledger_no;
DROP TABLE IF EXISTS inventory_ledgers;

DROP INDEX IF EXISTS idx_inventory_locks_source;
DROP INDEX IF EXISTS idx_inventory_locks_sku_wh_status;
DROP TABLE IF EXISTS inventory_locks;

DROP INDEX IF EXISTS idx_inventory_warehouse_sku;
DROP INDEX IF EXISTS idx_inventory_sku_id;
DROP INDEX IF EXISTS uk_inventory_location;
DROP TABLE IF EXISTS inventory;

DROP INDEX IF EXISTS idx_serial_numbers_warehouse_bin;
DROP INDEX IF EXISTS idx_serial_numbers_sku_status;
DROP INDEX IF EXISTS uk_serial_numbers_serial_no;
DROP TABLE IF EXISTS serial_numbers;

DROP INDEX IF EXISTS idx_batches_expiry_date;
DROP INDEX IF EXISTS uk_batches_sku_batch_no;
DROP TABLE IF EXISTS batches;
