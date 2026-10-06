// Package search 全局业务搜索（Ctrl/Cmd+K 命令面板后端，efficiency-layer-phase1 §2.1）。
//
//	GET /api/search?q=&types=&warehouse_id=&limit=
//
// 平台包口径（同 internal/reports/workbench.go 头注）：禁 import 各业务域包，
// 只读 SQL 直连单据/基础资料表——全部为参数化 SELECT，零写语句（guard-readonly 红线）。
// 跳转 path 由前端映射（web/src/config/searchTargets.ts），后端不编码前端路由。
//
// 端点级只挂认证（router protected 组的 auth.AuthRequired），组内按用户权限集
// 逐 type 过滤（auth.HasPermission，无对应 list 码的 type 不查询不出组）；
// 数据范围 scopeOf 会话仓库快照（auth.WarehouseScope 同口径），warehouse_id
// 为收窄过滤器与 scope 求交，越界仓库按该 type 无结果处理（不 403，防范围探测）。
package search
