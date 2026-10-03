// Package warehouse 仓库空间域（backend-m1-plan §2，scope E）。
//
// 职责：仓库/库区/货架/库位四级结构 CRUD 与级联校验（business-flow §1.6；
// 路由清单 plan §5.4：/api/warehouses、/api/zones、/api/shelves、/api/bins、
// GET /api/warehouses/{id}/map 库位地图）。
//
// 分层（architecture.md §1）：handler（参数/校验/响应）→ service（事务边界与业务
// 判断、数据权限过滤、操作日志）→ repository（纯数据访问）→ GORM（000004 迁移表）。
//
// 跨域契约（plan §4.3/§5.1，导出签名冻结）：
//   - NewChecker / NewBinChecker：被消费方导出的存在性校验实现，供 auth（用户绑定
//     仓库校验）与 inventory（库位校验）的结构化接口消费；
//   - BinOccupancyReader + WithBinOccupancy：本域作为消费方定义的库位占用接口，
//     实现由 inventory 提供，router 装配注入；未注入时库位地图不带库存占用字段。
//
// 冻结签名（实现交付不得变更，plan §5.2）：RegisterRoutes(rg, db, rdb, opts...)、
// EnsureDefaultWarehouse(db)。
//
// 软删除边界（database.md §5.1）：warehouses/bins 有 deleted_at（软删除）；
// zones/shelves 无 deleted_at 列，只有启用/停用（status），无删除接口
// （plan §5.4.1 权限点清单同源）。
package warehouse
