package sales

import "github.com/stockflow/server/internal/auth"

// 权限点常量（backend-m2-plan §9.2 冻结清单，域:资源:动作 命名）。
//
// 收编落位（MT5，plan §9.2）：常量唯一来源已并入 internal/auth/permissions.go（M2 段，
// seed 种子同源）——本文件按交付时预留的收敛方式 1/2 混合形态保留同名包内别名：
// 字符串值由 auth 单点定义，本包引用零改动（routes.go 不动），无第二套权限语义。
// 清单外动作词禁止发明（plan §2.3 判据 9）。
const (
	// sales:sales 销售订单（list/read/create/update/submit/approve/cancel/close）。
	PermSalesList    = auth.PermSalesList
	PermSalesRead    = auth.PermSalesRead
	PermSalesCreate  = auth.PermSalesCreate
	PermSalesUpdate  = auth.PermSalesUpdate
	PermSalesSubmit  = auth.PermSalesSubmit
	PermSalesApprove = auth.PermSalesApprove
	PermSalesCancel  = auth.PermSalesCancel
	PermSalesClose   = auth.PermSalesClose

	// sales:outbound 出库单（list/read/create/cancel/close）。
	PermOutboundList   = auth.PermOutboundList
	PermOutboundRead   = auth.PermOutboundRead
	PermOutboundCreate = auth.PermOutboundCreate
	PermOutboundCancel = auth.PermOutboundCancel
	PermOutboundClose  = auth.PermOutboundClose

	// sales:allocation 库存分配（list/read/create/execute 重新分配）。
	PermAllocationList    = auth.PermAllocationList
	PermAllocationRead    = auth.PermAllocationRead
	PermAllocationCreate  = auth.PermAllocationCreate
	PermAllocationExecute = auth.PermAllocationExecute

	// sales:pick 拣货任务（list/read/claim/execute）。
	PermPickList    = auth.PermPickList
	PermPickRead    = auth.PermPickRead
	PermPickClaim   = auth.PermPickClaim
	PermPickAssign  = auth.PermPickAssign
	PermPickExecute = auth.PermPickExecute

	// sales:check 复核任务（list/read/claim/execute）。
	PermCheckList    = auth.PermCheckList
	PermCheckRead    = auth.PermCheckRead
	PermCheckClaim   = auth.PermCheckClaim
	PermCheckAssign  = auth.PermCheckAssign
	PermCheckExecute = auth.PermCheckExecute

	// sales:packing 打包（list/read/execute）。
	PermPackingList    = auth.PermPackingList
	PermPackingRead    = auth.PermPackingRead
	PermPackingExecute = auth.PermPackingExecute

	// sales:shipment 发货（list/read/execute）。
	PermShipmentList    = auth.PermShipmentList
	PermShipmentRead    = auth.PermShipmentRead
	PermShipmentExecute = auth.PermShipmentExecute
)
