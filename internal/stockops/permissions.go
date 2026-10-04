package stockops

import "github.com/stockflow/server/internal/auth"

// stockops 域权限点常量（backend-m2-plan §9.2 冻结清单逐字命名，域:资源:动作）。
//
// 收编落位（MT5，plan §9.2）：常量唯一来源已并入 internal/auth/permissions.go（M2 段，
// seed 种子同源）；本文件保留同名包内别名，路由挂载（routes.go）零改动。
// 动作词全部来自 §9.1 冻结枚举，清单外动作词未发明（§2.3 判据 9）。

// —— stockops:transfer（调拨，plan §9.2）——
const (
	PermTransferList    = auth.PermTransferList
	PermTransferRead    = auth.PermTransferRead
	PermTransferCreate  = auth.PermTransferCreate
	PermTransferUpdate  = auth.PermTransferUpdate
	PermTransferSubmit  = auth.PermTransferSubmit
	PermTransferApprove = auth.PermTransferApprove
	PermTransferExecute = auth.PermTransferExecute // 调拨两端：出库/到货登记/收货
	PermTransferCancel  = auth.PermTransferCancel
	PermTransferClose   = auth.PermTransferClose // 清单内动作词；M2 调拨无差额关闭态，无路由挂载
)

// —— stockops:count（盘点，plan §9.2）——
const (
	PermCountList    = auth.PermCountList
	PermCountRead    = auth.PermCountRead
	PermCountCreate  = auth.PermCountCreate
	PermCountExecute = auth.PermCountExecute // 冻结开始盘点/实盘登记/完成实盘
	PermCountApprove = auth.PermCountApprove // 差异审核（通过=complete/驳回=reject 共用资源点）
	PermCountCancel  = auth.PermCountCancel
	PermCountClose   = auth.PermCountClose // 清单内动作词；M2 盘点无差额关闭态，无路由挂载
)

// —— stockops:move（仓内移库 POST /api/inventory/moves，plan §8.3 条 5 / §9.2）——
const (
	PermMoveList    = auth.PermMoveList
	PermMoveExecute = auth.PermMoveExecute
)
