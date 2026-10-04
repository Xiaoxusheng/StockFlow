-- 000016 补齐 purchase 域业务表的 deleted_at 列
-- 依据：database.md §3 通用字段规范（业务表统一包含 deleted_at）；
--       服务器真库回归实测：purchase 域模型内嵌 database.BaseModel（internal/database/model.go
--       含 gorm.DeletedAt），GORM INSERT/SELECT 显式携带 deleted_at，而 000007 未建该列，
--       导致部署后采购下单/收货/质检全链路写入必然失败
--       （SQLSTATE 42703: column "deleted_at" of relation "purchase_orders" does not exist）。
-- 约定：
--   * 仅补 purchase 域内嵌 BaseModel 的 9 张表；sales/returns/stockops 等域模型为
--     显式字段（无 DeletedAt），不涉及（database.md §5.1 软删除清单不含单据表，
--     本列为可空死列、无行为影响，模型侧重构另案清理）。
--   * 可空、无默认值，镜像 000001 users 的 deleted_at 风格；纯加列，不动任何数据。

ALTER TABLE purchase_orders   ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE purchase_order_items ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE inbound_orders    ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE inbound_items     ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE receipts          ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE receipt_items     ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE putaway_tasks     ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE quality_orders    ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE quality_items     ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
