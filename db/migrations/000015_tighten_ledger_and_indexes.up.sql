-- 000015 库存流水约束收紧 + 关键业务表 created_by 索引
-- 依据：database.md §6.1 条 1（列表查询条件字段——含创建人——建立索引）、
--       docs/tasks/current.md 清偿项 F12/F15（核对员 2026-10-04 复核记录）。
-- 约定：
--   * 不建任何外键、不建任何表（plan §5 通用规则：本迁移只做 ALTER 收紧与索引补充）。
--   * inventory_ledgers.zone_id/shelf_id 收紧为 NOT NULL：M1 起写入侧 insertLedger
--     （internal/inventory/repository.go）恒填两列，与 inventory 主表（000005 NOT NULL）对齐；
--     生产首启前执行本迁移是安全的——若历史库存在 NULL 残留，下面的断言块会整体失败回滚，
--     需先按 business_no 追溯补数据后再执行（不允许静默置 0 掩盖脏数据）。

-- == 1 == inventory_ledgers.zone_id / shelf_id 收紧 NOT NULL ====
-- 断言内建于 PostgreSQL 语义：ALTER ... SET NOT NULL 执行时全表扫描校验，
-- 存在 NULL 行则整个迁移事务失败回滚并报 "column ... contains null values"——
-- 显式失败优于隐式覆盖，且保持 golang-migrate 单文件单事务约定（不写 PL/pgSQL DO 块）。

ALTER TABLE inventory_ledgers ALTER COLUMN zone_id SET NOT NULL;
ALTER TABLE inventory_ledgers ALTER COLUMN shelf_id SET NOT NULL;

COMMENT ON COLUMN inventory_ledgers.zone_id IS '库区 ID（写入恒填，000015 收紧 NOT NULL；database.md §6.1）';
COMMENT ON COLUMN inventory_ledgers.shelf_id IS '货架 ID（写入恒填，000015 收紧 NOT NULL；database.md §6.1）';

-- == 2 == 关键业务表 created_by 索引（database.md §6.1「创建人」筛选面；只补主单据表，不滥用） ====
-- 注：inventory_ledgers 无 created_by 列（操作者列为 operator_id），且流水列表查询
-- （internal/inventory/query.go LedgerQuery）无创建人筛选，不建该索引。

CREATE INDEX idx_inventory_adjustments_creator_created ON inventory_adjustments (created_by, created_at);
CREATE INDEX idx_purchase_orders_creator_created ON purchase_orders (created_by, created_at);
CREATE INDEX idx_inbound_orders_creator_created ON inbound_orders (created_by, created_at);
CREATE INDEX idx_quality_orders_creator_created ON quality_orders (created_by, created_at);
CREATE INDEX idx_sales_orders_creator_created ON sales_orders (created_by, created_at);
CREATE INDEX idx_outbound_orders_creator_created ON outbound_orders (created_by, created_at);
CREATE INDEX idx_transfer_orders_creator_created ON transfer_orders (created_by, created_at);
CREATE INDEX idx_count_orders_creator_created ON count_orders (created_by, created_at);
