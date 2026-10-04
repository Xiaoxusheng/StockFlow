-- 000015 回滚：恢复 inventory_ledgers.zone_id/shelf_id 可空，删除 000015 新增索引，
-- 并清除 000015 新增的两条列注释——up→down 循环后与 000005 原态完全一致
--（本迁移无建表/删表，回滚为纯反向 ALTER + DROP INDEX + 注释清除）

DROP INDEX IF EXISTS idx_count_orders_creator_created;
DROP INDEX IF EXISTS idx_outbound_orders_creator_created;
DROP INDEX IF EXISTS idx_transfer_orders_creator_created;
DROP INDEX IF EXISTS idx_sales_orders_creator_created;
DROP INDEX IF EXISTS idx_quality_orders_creator_created;
DROP INDEX IF EXISTS idx_inbound_orders_creator_created;
DROP INDEX IF EXISTS idx_purchase_orders_creator_created;
DROP INDEX IF EXISTS idx_inventory_adjustments_creator_created;
DROP INDEX IF EXISTS idx_inventory_ledgers_creator_created;

-- 移除 000015 新增的两条列注释（000005 本无 zone_id/shelf_id 列注释；
-- COMMENT .. IS NULL 为 PostgreSQL 移除注释的规范写法）——up→down 循环后与 000005 原态一致
COMMENT ON COLUMN inventory_ledgers.zone_id IS NULL;
COMMENT ON COLUMN inventory_ledgers.shelf_id IS NULL;

ALTER TABLE inventory_ledgers ALTER COLUMN shelf_id DROP NOT NULL;
ALTER TABLE inventory_ledgers ALTER COLUMN zone_id DROP NOT NULL;
