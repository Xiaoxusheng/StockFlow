-- 000004 warehouse 域回滚：DROP 顺序与 up 相反（库位 → 货架 → 库区 → 仓库）

DROP INDEX IF EXISTS idx_bins_status;
DROP INDEX IF EXISTS idx_bins_shelf_id;
DROP INDEX IF EXISTS idx_bins_zone_id;
DROP INDEX IF EXISTS uk_bins_warehouse_code;
DROP TABLE IF EXISTS bins;

DROP INDEX IF EXISTS idx_shelves_zone_id;
DROP INDEX IF EXISTS idx_shelves_warehouse_id;
DROP INDEX IF EXISTS uk_shelves_zone_code;
DROP TABLE IF EXISTS shelves;

DROP INDEX IF EXISTS idx_zones_warehouse_id;
DROP INDEX IF EXISTS uk_zones_warehouse_code;
DROP TABLE IF EXISTS zones;

DROP INDEX IF EXISTS uk_warehouses_code;
DROP TABLE IF EXISTS warehouses;
