package returns

import "github.com/stockflow/server/internal/auth"

// 权限点常量（backend-m2-plan §9.2 冻结清单，域:资源:动作）。
//
// 收编落位（MT5，plan §9.2）：常量唯一来源已并入 internal/auth/permissions.go（M2 段，
// seed 种子同源）；本文件保留同名包内别名——handler.go 路由挂载引用零改动，
// 字符串值与冻结清单逐字一致。清单外动作词一律不发明（plan §2.3 判据 9）。
//
// 未开端点的冻结动作词：update/close 随 §9.2 清单导出，但 M2 状态机无对应迁移
// （退货单无 CLOSED 态，business-flow §13.3 已影响库存的单据只能冲正；草稿修订端点
// 随真实需求开放），不虚设路由。
const (
	// —— 销售退货 returns:salesreturn（plan §9.2）——
	PermSalesReturnList    = auth.PermSalesReturnList
	PermSalesReturnRead    = auth.PermSalesReturnRead
	PermSalesReturnCreate  = auth.PermSalesReturnCreate
	PermSalesReturnUpdate  = auth.PermSalesReturnUpdate
	PermSalesReturnSubmit  = auth.PermSalesReturnSubmit
	PermSalesReturnApprove = auth.PermSalesReturnApprove
	PermSalesReturnExecute = auth.PermSalesReturnExecute // 收货/质检结果应用（收货、提交质检、质检结果应用）
	PermSalesReturnCancel  = auth.PermSalesReturnCancel
	PermSalesReturnClose   = auth.PermSalesReturnClose

	// —— 采购退货 returns:purchasereturn（plan §9.2）——
	PermPurchaseReturnList    = auth.PermPurchaseReturnList
	PermPurchaseReturnRead    = auth.PermPurchaseReturnRead
	PermPurchaseReturnCreate  = auth.PermPurchaseReturnCreate
	PermPurchaseReturnUpdate  = auth.PermPurchaseReturnUpdate
	PermPurchaseReturnSubmit  = auth.PermPurchaseReturnSubmit
	PermPurchaseReturnApprove = auth.PermPurchaseReturnApprove
	PermPurchaseReturnExecute = auth.PermPurchaseReturnExecute // 退货出库（Lock→Deduct）/完成确认
	PermPurchaseReturnCancel  = auth.PermPurchaseReturnCancel
	PermPurchaseReturnClose   = auth.PermPurchaseReturnClose

	// —— 异常中心 returns:exception（plan §9.2）——
	PermExceptionList    = auth.PermExceptionList
	PermExceptionRead    = auth.PermExceptionRead
	PermExceptionCreate  = auth.PermExceptionCreate
	PermExceptionAssign  = auth.PermExceptionAssign // 分派（plan §9.1 动作词 assign）
	PermExceptionExecute = auth.PermExceptionExecute
	PermExceptionClose   = auth.PermExceptionClose

	// —— 库存追溯 returns:trace（plan §9.2；GET /api/inventory/trace）——
	PermTraceList = auth.PermTraceList
)
