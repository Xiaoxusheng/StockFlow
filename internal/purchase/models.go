package purchase

import (
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/stock"
)

// purchase 域 GORM 模型——与 db/migrations/000007_create_purchase_tables.up.sql 冻结列结构
// 一一对应（backend-m2-plan §5；判据：本域表模型仅定义/导出于本包）。
// 数量/金额列 numeric(18,4) 用 stock.Qty 承载（方案 §3 契约包统一载体，禁 float）；
// 可空业务时间列用 database.JSONTime（零值写 NULL，database/model.go:79）。
// 单据表不做软删除（迁移文件头：作废/取消/关闭走状态机，business-flow §13.3）。

// 单据状态值域（与迁移 CHECK 约束同源，plan §6.1–§6.3）。
const (
	// 采购订单（business-flow §2.2：草稿→待审核→已审核→部分到货→到货完成→已完成/已取消）
	POStatusDraft           = "DRAFT"
	POStatusPendingApproval = "PENDING_APPROVAL"
	POStatusApproved        = "APPROVED"
	POStatusPartialReceived = "PARTIAL_RECEIVED"
	POStatusReceivedAll     = "RECEIVED_ALL"
	POStatusCompleted       = "COMPLETED"
	POStatusCancelled       = "CANCELLED"

	// 入库单（plan §6.2：DRAFT→RECEIVING→AWAITING_QC→AWAITING_PUTAWAY→COMPLETED，
	// 差额关闭 CLOSED，取消 CANCELLED）
	InboundStatusDraft           = "DRAFT"
	InboundStatusReceiving       = "RECEIVING"
	InboundStatusAwaitingQC      = "AWAITING_QC"
	InboundStatusAwaitingPutaway = "AWAITING_PUTAWAY"
	InboundStatusCompleted       = "COMPLETED"
	InboundStatusCancelled       = "CANCELLED"
	InboundStatusClosed          = "CLOSED"

	// 质检单（plan §6.3：PENDING→INSPECTING→COMPLETED，COMPLETED 即处理结果落定并触发库存映射）
	QCStatusPending    = "PENDING"
	QCStatusInspecting = "INSPECTING"
	QCStatusCompleted  = "COMPLETED"

	// 上架任务（business-flow §5.1：待上架→上架中→已完成；取消随入库单联动，plan §6.3；
	// PAUSED 暂停/恢复为作业过程态——迁移 000017，PadPutawayPage 暂停占位接线前置，
	// 仅领取人可暂停/恢复，PAUSED 视为活动态参与入库单推进/关闭守卫）
	TaskStatusPending    = "PENDING"
	TaskStatusInProgress = "IN_PROGRESS"
	TaskStatusPaused     = "PAUSED"
	TaskStatusCompleted  = "COMPLETED"
	TaskStatusCancelled  = "CANCELLED"
)

// 来源/类型值域（迁移 CHECK 同源）。
const (
	SourceTypePurchase = "PURCHASE" // 采购入库
	SourceTypeOther    = "OTHER"    // 其他入库

	QCSourceInbound = "INBOUND" // 入库质检（RETURN 退货质检由 returns 域经 QCCreator 复用）
	QCSourceReturn  = "RETURN"

	// 检验方式（business-flow §4.1：免检/抽检/全检）
	InspectionExempt = "免检"
	InspectionSample = "抽检"
	InspectionFull   = "全检"

	// 上架任务 from_state（迁移 chk_putaway_tasks_from_state；plan §5：
	// available 免检直通 / pending_inspect 经检待检——决定 PutawayOp.RequireInspect）
	FromStateAvailable      = "available"
	FromStatePendingInspect = "pending_inspect"

	// 序列号收货采集状态（inventory-rules §8：入库采集即在库，库位随上架事件更新）
	SerialStatusInStock = "IN_STOCK"
)

// purchaseTransitions 采购订单状态机迁移表（business-flow §2.2 与 plan §6.1 同源；
// 状态变化必须经业务方法守卫——§13.2 硬性规则，数据层第二道防线见
// repository.updateStatusGuarded 的 WHERE status=<前置态>）。
var purchaseTransitions = map[string][]string{
	POStatusDraft:           {POStatusPendingApproval, POStatusCancelled},
	POStatusPendingApproval: {POStatusApproved, POStatusDraft, POStatusCancelled},
	POStatusApproved:        {POStatusPartialReceived, POStatusReceivedAll, POStatusCancelled},
	POStatusPartialReceived: {POStatusReceivedAll, POStatusCompleted},
	POStatusReceivedAll:     {POStatusCompleted},
	POStatusCompleted:       {},
	POStatusCancelled:       {},
}

// inboundTransitions 入库单状态机迁移表（plan §6.2）。
var inboundTransitions = map[string][]string{
	InboundStatusDraft:           {InboundStatusReceiving, InboundStatusCancelled},
	InboundStatusReceiving:       {InboundStatusAwaitingQC, InboundStatusClosed},
	InboundStatusAwaitingQC:      {InboundStatusAwaitingPutaway},
	InboundStatusAwaitingPutaway: {InboundStatusCompleted},
	InboundStatusCompleted:       {},
	InboundStatusCancelled:       {},
	InboundStatusClosed:          {},
}

// qcTransitions 质检单状态机迁移表（plan §6.3）。
var qcTransitions = map[string][]string{
	QCStatusPending:    {QCStatusInspecting},
	QCStatusInspecting: {QCStatusCompleted},
	QCStatusCompleted:  {},
}

// putawayTransitions 上架任务状态机迁移表（business-flow §5.1、plan §6.3；
// PAUSED 暂停/恢复为作业过程态——迁移 000017）。
var putawayTransitions = map[string][]string{
	TaskStatusPending:    {TaskStatusInProgress, TaskStatusCancelled},
	TaskStatusInProgress: {TaskStatusCompleted, TaskStatusPaused},
	TaskStatusPaused:     {TaskStatusInProgress},
	TaskStatusCompleted:  {},
	TaskStatusCancelled:  {},
}

// canTransition 状态机守卫纯函数（service 层第一道校验；数据层 WHERE status 为第二道）。
func canTransition(table map[string][]string, from, to string) bool {
	for _, t := range table[from] {
		if t == to {
			return true
		}
	}
	return false
}

// PurchaseOrder 采购订单（business-flow §2；warehouse_id=收货仓必填，
// 数据权限过滤与到货校验依据——plan §5）。
type PurchaseOrder struct {
	database.BaseModel
	PONo        string            `gorm:"column:po_no;size:64" json:"po_no"`
	SupplierID  int64             `gorm:"column:supplier_id" json:"supplier_id"`
	WarehouseID int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	TotalAmount stock.Qty         `gorm:"column:total_amount" json:"total_amount"`
	Status      string            `gorm:"column:status;size:32" json:"status"`
	ApprovedBy  int64             `gorm:"column:approved_by" json:"approved_by"`
	ApprovedAt  database.JSONTime `gorm:"column:approved_at" json:"approved_at"`
	ReceivedAt  database.JSONTime `gorm:"column:received_at" json:"received_at"`
	CompletedAt database.JSONTime `gorm:"column:completed_at" json:"completed_at"`
	CancelledAt database.JSONTime `gorm:"column:cancelled_at" json:"cancelled_at"`
	Remark      string            `gorm:"column:remark" json:"remark"`
	CreatedBy   database.ID       `gorm:"column:created_by" json:"created_by"`
	UpdatedBy   database.ID       `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (PurchaseOrder) TableName() string { return "purchase_orders" }

// PurchaseOrderItem 采购订单明细（business-flow §2.3 四量约束落列：
// qty_ordered 原始 / qty_received 累计收货（含合格+不合格待定）/ qty_rejected 拒收 /
// qty_putaway 已上架——超量收货由收货服务强校验 + 数据层条件更新双防线）。
type PurchaseOrderItem struct {
	database.BaseModel
	POID        int64       `gorm:"column:po_id" json:"po_id"`
	LineNo      int         `gorm:"column:line_no" json:"line_no"`
	SKUID       int64       `gorm:"column:sku_id" json:"sku_id"`
	QtyOrdered  stock.Qty   `gorm:"column:qty_ordered" json:"qty_ordered"`
	QtyReceived stock.Qty   `gorm:"column:qty_received" json:"qty_received"`
	QtyRejected stock.Qty   `gorm:"column:qty_rejected" json:"qty_rejected"`
	QtyPutaway  stock.Qty   `gorm:"column:qty_putaway" json:"qty_putaway"`
	Price       stock.Qty   `gorm:"column:price" json:"price"`
	Amount      stock.Qty   `gorm:"column:amount" json:"amount"`
	Remark      string      `gorm:"column:remark" json:"remark"`
	CreatedBy   database.ID `gorm:"column:created_by" json:"created_by"`
	UpdatedBy   database.ID `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (PurchaseOrderItem) TableName() string { return "purchase_order_items" }

// InboundOrder 入库单（business-flow §3.2 收货→质检→上架骨架；
// M2 实际入口=采购入库/其他入库，plan §6.2）。
type InboundOrder struct {
	database.BaseModel
	InboundNo   string            `gorm:"column:inbound_no;size:64" json:"inbound_no"`
	SourceType  string            `gorm:"column:source_type;size:16" json:"source_type"`
	SourceNo    string            `gorm:"column:source_no;size:64" json:"source_no"`
	WarehouseID int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	Status      string            `gorm:"column:status;size:32" json:"status"`
	ReceivedAt  database.JSONTime `gorm:"column:received_at" json:"received_at"`
	InspectedAt database.JSONTime `gorm:"column:inspected_at" json:"inspected_at"`
	PutawayAt   database.JSONTime `gorm:"column:putaway_at" json:"putaway_at"`
	CompletedAt database.JSONTime `gorm:"column:completed_at" json:"completed_at"`
	CancelledAt database.JSONTime `gorm:"column:cancelled_at" json:"cancelled_at"`
	Remark      string            `gorm:"column:remark" json:"remark"`
	CreatedBy   database.ID       `gorm:"column:created_by" json:"created_by"`
	UpdatedBy   database.ID       `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (InboundOrder) TableName() string { return "inbound_orders" }

// InboundItem 入库单明细（business-flow §3.3 部分收货：每次收货累计 qty_received，
// 剩余数量继续跟踪直到收齐或关闭；qty_inspected 承载质检/免检直通的处理量，
// 用于 AWAITING_QC→AWAITING_PUTAWAY 的"全部明细质检完成或免检"判定）。
type InboundItem struct {
	database.BaseModel
	InboundID    int64       `gorm:"column:inbound_id" json:"inbound_id"`
	LineNo       int         `gorm:"column:line_no" json:"line_no"`
	SKUID        int64       `gorm:"column:sku_id" json:"sku_id"`
	Qty          stock.Qty   `gorm:"column:qty" json:"qty"`
	QtyReceived  stock.Qty   `gorm:"column:qty_received" json:"qty_received"`
	QtyInspected stock.Qty   `gorm:"column:qty_inspected" json:"qty_inspected"`
	QtyPutaway   stock.Qty   `gorm:"column:qty_putaway" json:"qty_putaway"`
	Remark       string      `gorm:"column:remark" json:"remark"`
	CreatedBy    database.ID `gorm:"column:created_by" json:"created_by"`
	UpdatedBy    database.ID `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (InboundItem) TableName() string { return "inbound_items" }

// Receipt 收货记录（business-flow §3.2；事件型一次性生效、幂等键防重，plan §6.3
// 无状态机——状态语义由入库单承载；idempotency_key 部分唯一索引
// uk_receipts_idempotency（WHERE idempotency_key IS NOT NULL）为数据库兜底防线）。
type Receipt struct {
	database.BaseModel
	ReceiptNo      string            `gorm:"column:receipt_no;size:64" json:"receipt_no"`
	InboundNo      string            `gorm:"column:inbound_no;size:64" json:"inbound_no"`
	WarehouseID    int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	BatchNo        string            `gorm:"column:batch_no;size:64" json:"batch_no"`
	ExpiryDate     database.JSONTime `gorm:"column:expiry_date" json:"expiry_date"`
	ProductionDate database.JSONTime `gorm:"column:production_date" json:"production_date"`
	IdempotencyKey *string           `gorm:"column:idempotency_key;size:128" json:"idempotency_key,omitempty"`
	OperatorID     int64             `gorm:"column:operator_id" json:"operator_id"`
	OperatorName   string            `gorm:"column:operator_name;size:64" json:"operator_name"`
	Remark         string            `gorm:"column:remark" json:"remark"`
	CreatedBy      database.ID       `gorm:"column:created_by" json:"created_by"`
	UpdatedBy      database.ID       `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (Receipt) TableName() string { return "receipts" }

// ReceiptItem 收货明细（business-flow §3.4：合格/拒收分列；异常收货经 exception_ref
// 关联异常中心单号）。
type ReceiptItem struct {
	database.BaseModel
	ReceiptID    int64       `gorm:"column:receipt_id" json:"receipt_id"`
	LineNo       int         `gorm:"column:line_no" json:"line_no"`
	SKUID        int64       `gorm:"column:sku_id" json:"sku_id"`
	QtyGood      stock.Qty   `gorm:"column:qty_good" json:"qty_good"`
	QtyRejected  stock.Qty   `gorm:"column:qty_rejected" json:"qty_rejected"`
	ExceptionRef string      `gorm:"column:exception_ref;size:64" json:"exception_ref"`
	Remark       string      `gorm:"column:remark" json:"remark"`
	CreatedBy    database.ID `gorm:"column:created_by" json:"created_by"`
	UpdatedBy    database.ID `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (ReceiptItem) TableName() string { return "receipt_items" }

// PutawayTask 上架任务（business-flow §5；领取为原子抢占，完成触发 Putaway 落账；
// serial_no 序列号 SKU 单件任务 qty=1 逐件上架，inventory-rules §8.2）。
type PutawayTask struct {
	database.BaseModel
	PutawayNo         string            `gorm:"column:putaway_no;size:64" json:"putaway_no"`
	InboundNo         string            `gorm:"column:inbound_no;size:64" json:"inbound_no"`
	ReceiptNo         string            `gorm:"column:receipt_no;size:64" json:"receipt_no"`
	SKUID             int64             `gorm:"column:sku_id" json:"sku_id"`
	BatchID           int64             `gorm:"column:batch_id" json:"batch_id"`
	SerialNo          string            `gorm:"column:serial_no;size:128" json:"serial_no"`
	Qty               stock.Qty         `gorm:"column:qty" json:"qty"`
	FromState         string            `gorm:"column:from_state;size:32" json:"from_state"`
	TargetWarehouseID int64             `gorm:"column:target_warehouse_id" json:"target_warehouse_id"`
	TargetZoneID      int64             `gorm:"column:target_zone_id" json:"target_zone_id"`
	TargetShelfID     int64             `gorm:"column:target_shelf_id" json:"target_shelf_id"`
	TargetBinID       int64             `gorm:"column:target_bin_id" json:"target_bin_id"`
	Status            string            `gorm:"column:status;size:16" json:"status"`
	ClaimedBy         int64             `gorm:"column:claimed_by" json:"claimed_by"`
	ClaimedAt         database.JSONTime `gorm:"column:claimed_at" json:"claimed_at"`
	CompletedAt       database.JSONTime `gorm:"column:completed_at" json:"completed_at"`
	Remark            string            `gorm:"column:remark" json:"remark"`
	CreatedBy         database.ID       `gorm:"column:created_by" json:"created_by"`
	UpdatedBy         database.ID       `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (PutawayTask) TableName() string { return "putaway_tasks" }

// QualityOrder 质检单（business-flow §4：检验方式免检/抽检/全检，九类处理结果；
// COMPLETED 即处理结果落定并触发库存映射——plan §6.3）。
type QualityOrder struct {
	database.BaseModel
	QCNo           string            `gorm:"column:qc_no;size:64" json:"qc_no"`
	SourceType     string            `gorm:"column:source_type;size:16" json:"source_type"`
	SourceNo       string            `gorm:"column:source_no;size:64" json:"source_no"`
	WarehouseID    int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	InspectionType string            `gorm:"column:inspection_type;size:16" json:"inspection_type"`
	Status         string            `gorm:"column:status;size:16" json:"status"`
	QtyInspected   stock.Qty         `gorm:"column:qty_inspected" json:"qty_inspected"`
	QtyQualified   stock.Qty         `gorm:"column:qty_qualified" json:"qty_qualified"`
	QtyDefective   stock.Qty         `gorm:"column:qty_defective" json:"qty_defective"`
	Result         string            `gorm:"column:result;size:16" json:"result"`
	InspectorID    int64             `gorm:"column:inspector_id" json:"inspector_id"`
	InspectorName  string            `gorm:"column:inspector_name;size:64" json:"inspector_name"`
	InspectedAt    database.JSONTime `gorm:"column:inspected_at" json:"inspected_at"`
	ImageRefs      StringList        `gorm:"column:image_refs;type:jsonb" json:"image_refs"`
	Remark         string            `gorm:"column:remark" json:"remark"`
	CreatedBy      database.ID       `gorm:"column:created_by" json:"created_by"`
	UpdatedBy      database.ID       `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (QualityOrder) TableName() string { return "quality_orders" }

// QualityItem 质检明细（business-flow §4.2：检验/合格/不合格数量逐行记录）。
type QualityItem struct {
	database.BaseModel
	QCID         int64       `gorm:"column:qc_id" json:"qc_id"`
	LineNo       int         `gorm:"column:line_no" json:"line_no"`
	SKUID        int64       `gorm:"column:sku_id" json:"sku_id"`
	BatchNo      string      `gorm:"column:batch_no;size:64" json:"batch_no"`
	QtyInspected stock.Qty   `gorm:"column:qty_inspected" json:"qty_inspected"`
	QtyQualified stock.Qty   `gorm:"column:qty_qualified" json:"qty_qualified"`
	QtyDefective stock.Qty   `gorm:"column:qty_defective" json:"qty_defective"`
	Remark       string      `gorm:"column:remark" json:"remark"`
	CreatedBy    database.ID `gorm:"column:created_by" json:"created_by"`
	UpdatedBy    database.ID `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (QualityItem) TableName() string { return "quality_items" }

// DocumentApproval 审批记录（000006 共享表，business-flow §12.2：审批人/时间/意见/结果
// 不可修改删除——append-only，本包只 INSERT，无任何 UPDATE/DELETE 通路）。
// 迁移 chk_document_approvals_action 值域：SUBMIT/APPROVE/REJECT/CANCEL——
// 差额关闭（close）不落本表（CHECK 值域限制），经 operation_logs 审计承载。
type DocumentApproval struct {
	ID           database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	TargetType   string            `gorm:"column:target_type;size:32" json:"target_type"`
	TargetNo     string            `gorm:"column:target_no;size:64" json:"target_no"`
	Action       string            `gorm:"column:action;size:16" json:"action"`
	Result       string            `gorm:"column:result;size:16" json:"result"`
	Opinion      string            `gorm:"column:opinion" json:"opinion"`
	OperatorID   int64             `gorm:"column:operator_id" json:"operator_id"`
	OperatorName string            `gorm:"column:operator_name;size:64" json:"operator_name"`
	RequestID    string            `gorm:"column:request_id;size:64" json:"request_id"`
	CreatedAt    database.JSONTime `json:"created_at"`
}

// TableName 显式指定表名。
func (DocumentApproval) TableName() string { return "document_approvals" }

// 审批动作值域（迁移 chk_document_approvals_action 同源）。
const (
	ApprovalActionSubmit  = "SUBMIT"
	ApprovalActionApprove = "APPROVE"
	ApprovalActionReject  = "REJECT"
	ApprovalActionCancel  = "CANCEL"
)
