// Package stock 库存原语值类型契约包（backend-m2-plan §3 冻结决策）。
//
// 本包为 M2 四业务域（purchase/sales/stockops/returns）消费 internal/inventory
// Service 原语时，接口签名可引用的值类型唯一载体——域包之间禁止互相 import
// （backend-m2-plan §2.3 判据 2），故把 M1 已冻结的值类型形状"原样搬迁"到本叶子包，
// inventory 侧后续以类型别名承接（type Qty = stock.Qty 等），零逻辑改动。
//
// 文件与形状（backend-m2-plan §3 冻结清单）：
//
//	qty.go      Qty（numeric(18,4) 精确十进制载体，自 internal/inventory/qty.go 原样搬迁）
//	actor.go    Actor / Source / RowKey（自 internal/inventory/service.go 原样搬迁）
//	state.go    StockState / StateColumn / 锁定类型（自 internal/inventory/identity.go、service.go 原样搬迁）
//	ops.go      九个原语入参 Op 结构（自 internal/inventory/service.go 原样搬迁）
//	result.go   MutationResult / LedgerRef（自 internal/inventory/service.go 原样搬迁）
//
// 包约束：
//   - 本包禁止 import 任何业务域包（auth/masterdata/warehouse/inventory/purchase/...）；
//     唯一例外见 ops.go：BatchOp 的三个时间字段沿用平台值类型 internal/database.JSONTime，
//     以保证 inventory 侧别名承接后类型恒等（M1 既有代码与测试零改动，§3 决策本意）；
//   - 本包无任何 SQL/IO/状态（§3 规则③），只有值类型与其纯函数方法；
//   - inventory 内部使用的未导出辅助（RowKey.details、Source.validate、StateColumn.dbColumn/
//     getOf 等 SQL/错误拼装管道）不属于冻结值形状，不随迁——各域在自己的校验层实现等价检查；
//   - 创建说明：本包由采购入库工程师（MT1）经 Orchestrator 裁决（2026-10-03，选项 A）创建，
//     集成工程师收编时以本版本为唯一基线：inventory 侧新增 aliases.go 类型别名承接后，
//     删除 inventory 内的重复定义（plan §3/§8.3 条 4）。
package stock
