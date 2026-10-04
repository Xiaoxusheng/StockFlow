package purchase

import "github.com/stockflow/server/internal/auth"

// purchase 域权限点常量（backend-m2-plan §9.2 冻结清单，命名 = api.md §1 领域:资源:动作）。
//
// 收编落位（MT5，plan §9.2）：常量唯一来源已并入 internal/auth/permissions.go（M2 段，
// seed 种子同源）；本文件按域包交付时预留的收敛方式保留同名包内别名——路由挂载
// （routes.go）与各处引用零改动，字符串值与冻结清单逐字一致。
// 动作词域（plan §9.1）：submit 提交审核、approve 审核通过/驳回共用资源点、cancel 取消、
// close 关闭、execute 作业执行、claim 任务领取——清单外动作词禁止发明（plan §2.3 判据 9）。
const (
	// —— purchase:purchase（采购订单）——
	PermPurchaseList    = auth.PermPurchaseList
	PermPurchaseRead    = auth.PermPurchaseRead
	PermPurchaseCreate  = auth.PermPurchaseCreate
	PermPurchaseUpdate  = auth.PermPurchaseUpdate
	PermPurchaseSubmit  = auth.PermPurchaseSubmit
	PermPurchaseApprove = auth.PermPurchaseApprove
	PermPurchaseCancel  = auth.PermPurchaseCancel
	PermPurchaseClose   = auth.PermPurchaseClose

	// —— purchase:inbound（入库单）——
	PermInboundList   = auth.PermInboundList
	PermInboundRead   = auth.PermInboundRead
	PermInboundCreate = auth.PermInboundCreate
	PermInboundUpdate = auth.PermInboundUpdate
	PermInboundCancel = auth.PermInboundCancel
	PermInboundClose  = auth.PermInboundClose

	// —— purchase:receipt（收货，execute=收货确认）——
	PermReceiptList    = auth.PermReceiptList
	PermReceiptRead    = auth.PermReceiptRead
	PermReceiptExecute = auth.PermReceiptExecute

	// —— purchase:putaway（上架任务，claim=领取 / execute=上架确认）——
	PermPutawayList    = auth.PermPutawayList
	PermPutawayRead    = auth.PermPutawayRead
	PermPutawayClaim   = auth.PermPutawayClaim
	PermPutawayExecute = auth.PermPutawayExecute

	// —— purchase:quality（质检单，execute=质检结果提交）——
	PermQualityList    = auth.PermQualityList
	PermQualityRead    = auth.PermQualityRead
	PermQualityCreate  = auth.PermQualityCreate
	PermQualityExecute = auth.PermQualityExecute
)
