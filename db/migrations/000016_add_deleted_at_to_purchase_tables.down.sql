-- 000016 回滚：删除 000016 补充的 deleted_at 列（纯加列回滚，无数据变动）

ALTER TABLE purchase_orders   DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE purchase_order_items DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE inbound_orders    DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE inbound_items     DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE receipts          DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE receipt_items     DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE putaway_tasks     DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE quality_orders    DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE quality_items     DROP COLUMN IF EXISTS deleted_at;
