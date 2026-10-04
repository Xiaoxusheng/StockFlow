package returns

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/stock"
)

// returns 域 GORM 模型（迁移 000010_create_returns_tables.up.sql 列结构）。
// 不做软删除：作废/取消/关闭走状态机（business-flow §13.3，迁移头注释同口径）。
// 数量列统一用 stock.Qty（numeric(18,4) 精确十进制载体；本域已因库存原语网关
// 引用 inventory 值类型，不再另造第二套数量实现——见 ports.go 落位说明）。

// ---- 值域（与迁移 CHECK 约束同源）----

// 退货单类型（chk_return_orders_type）。
const (
	ReturnTypeSales    = "SALES"    // 销售退货（business-flow §9.1）
	ReturnTypePurchase = "PURCHASE" // 采购退货（business-flow §9.2）
)

// 退货单状态（chk_return_orders_status；plan §6.9 状态机）。
const (
	ReturnStatusDraft           = "DRAFT"
	ReturnStatusPendingApproval = "PENDING_APPROVAL"
	ReturnStatusApproved        = "APPROVED"
	ReturnStatusReceiving       = "RECEIVING"
	ReturnStatusInQC            = "IN_QC"
	ReturnStatusShipped         = "SHIPPED" // 采购退货出库完成态
	ReturnStatusCompleted       = "COMPLETED"
	ReturnStatusCancelled       = "CANCELLED"
)

// 异常单状态（chk_exceptions_status；business-flow §11.2 / plan §6.10 生命周期）。
const (
	ExceptionStatusOpen          = "OPEN"
	ExceptionStatusAssigned      = "ASSIGNED"
	ExceptionStatusProcessing    = "PROCESSING"
	ExceptionStatusPendingReview = "PENDING_REVIEW"
	ExceptionStatusResolved      = "RESOLVED"
	ExceptionStatusClosed        = "CLOSED"
)

// exceptionTypes 九类异常（business-flow §11.2 值域；迁移 chk_exceptions_type 中文值域）。
var exceptionTypes = map[string]bool{
	"收货异常": true, "质检异常": true, "上架异常": true, "库存异常": true,
	"拣货异常": true, "复核异常": true, "物流异常": true, "盘点异常": true, "系统异常": true,
}

// ExceptionTypeValid 异常类型值域判定（供 Service 与跨域调用方共用）。
func ExceptionTypeValid(t string) bool { return exceptionTypes[t] }

// statusTimeCols 状态迁移时间列白名单（原生 SQL 列名只取本白名单，go-dev-standard 规则 8：
// 不拼接任何外部输入）。
var statusTimeCols = map[string]bool{
	"approved_at": true, "received_at": true, "qc_at": true,
	"completed_at": true, "cancelled_at": true,
	"assigned_at": true, "resolved_at": true, "closed_at": true,
	"": true, // 纯状态迁移不落业务时间列
}

// ---- jsonb 载体（列 handle_records/image_refs；形态对齐 middleware 包 jsonRaw 约定）----

type jsonb json.RawMessage

func (j jsonb) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

func (j *jsonb) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(jsonb(nil), v...)
	case string:
		*j = jsonb(v)
	default:
		return fmt.Errorf("returns.jsonb.Scan 不支持类型 %T", src)
	}
	return nil
}

func (j jsonb) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *jsonb) UnmarshalJSON(b []byte) error {
	*j = append(jsonb(nil), b...)
	return nil
}

func marshalJSONB(v any) jsonb {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return jsonb(`{"error":"snapshot_marshal_failed"}`)
	}
	return jsonb(b)
}

// HandleRecord 异常处理记录（business-flow §11.2：追加式，不覆盖历史）。
// Action 值域：assign 分派 / start 开始处理 / review 提交复核 / resolve 解决 / close 关闭 /
// freeze 异常冻结（携带 lock_id 与冻结量，供解冻释放取量——迁移 000010 freeze 相关列说明）/
// release 解除冻结。
type HandleRecord struct {
	At     database.JSONTime `json:"at"`
	Action string            `json:"action"`
	ByID   int64             `json:"by_id"`
	ByName string            `json:"by_name"`
	Note   string            `json:"note,omitempty"`
	LockID int64             `json:"lock_id,omitempty"`
	Qty    string            `json:"qty,omitempty"`
}

// ---- 单据表 ----

// ReturnOrder 退货单（迁移 000010 return_orders）。
type ReturnOrder struct {
	ID          database.ID       `gorm:"primaryKey;autoIncrement"`
	ReturnNo    string            `gorm:"column:return_no"`
	Type        string            `gorm:"column:type"`
	SourceNo    string            `gorm:"column:source_no"`
	CustomerID  int64             `gorm:"column:customer_id"`
	SupplierID  int64             `gorm:"column:supplier_id"`
	WarehouseID int64             `gorm:"column:warehouse_id"`
	Status      string            `gorm:"column:status"`
	ApprovedBy  int64             `gorm:"column:approved_by"`
	ApprovedAt  database.JSONTime `gorm:"column:approved_at"`
	ReceivedAt  database.JSONTime `gorm:"column:received_at"`
	QcAt        database.JSONTime `gorm:"column:qc_at"`
	CompletedAt database.JSONTime `gorm:"column:completed_at"`
	CancelledAt database.JSONTime `gorm:"column:cancelled_at"`
	Remark      string            `gorm:"column:remark"`
	CreatedAt   database.JSONTime `gorm:"column:created_at"`
	UpdatedAt   database.JSONTime `gorm:"column:updated_at"`
	CreatedBy   int64             `gorm:"column:created_by"`
	UpdatedBy   int64             `gorm:"column:updated_by"`
}

// TableName 显式表名。
func (ReturnOrder) TableName() string { return "return_orders" }

// ReturnItem 退货明细（迁移 000010 return_items；部分退货——各环节累计列逐次累加）。
type ReturnItem struct {
	ID           database.ID       `gorm:"primaryKey;autoIncrement"`
	ReturnID     int64             `gorm:"column:return_id"`
	LineNo       int64             `gorm:"column:line_no"`
	SKUID        int64             `gorm:"column:sku_id"`
	QtyReturn    stock.Qty         `gorm:"column:qty_return"`
	QtyReceived  stock.Qty         `gorm:"column:qty_received"`
	QtyInspected stock.Qty         `gorm:"column:qty_inspected"`
	QtyDefective stock.Qty         `gorm:"column:qty_defective"`
	Reason       string            `gorm:"column:reason"`
	Remark       string            `gorm:"column:remark"`
	CreatedAt    database.JSONTime `gorm:"column:created_at"`
	UpdatedAt    database.JSONTime `gorm:"column:updated_at"`
	CreatedBy    int64             `gorm:"column:created_by"`
	UpdatedBy    int64             `gorm:"column:updated_by"`
}

// TableName 显式表名。
func (ReturnItem) TableName() string { return "return_items" }

// Exception 异常单（迁移 000010 exceptions；由各域经 ExceptionCreator 窄接口创建，
// 异常中心 CRUD 归本域——plan §6.10）。
type Exception struct {
	ID            database.ID       `gorm:"primaryKey;autoIncrement"`
	ExceptionNo   string            `gorm:"column:exception_no"`
	Type          string            `gorm:"column:type"`
	SourceType    string            `gorm:"column:source_type"`
	SourceNo      string            `gorm:"column:source_no"`
	SKUID         int64             `gorm:"column:sku_id"`
	BinID         int64             `gorm:"column:bin_id"`
	SerialNo      string            `gorm:"column:serial_no"`
	Status        string            `gorm:"column:status"`
	Detail        string            `gorm:"column:detail"`
	AssigneeID    int64             `gorm:"column:assignee_id"`
	AssigneeName  string            `gorm:"column:assignee_name"`
	OwnerID       int64             `gorm:"column:owner_id"`
	OwnerName     string            `gorm:"column:owner_name"`
	HandleRecords jsonb             `gorm:"column:handle_records;type:jsonb"`
	ImageRefs     jsonb             `gorm:"column:image_refs;type:jsonb"`
	FreezeLockID  *int64            `gorm:"column:freeze_lock_id"`
	AssignedAt    database.JSONTime `gorm:"column:assigned_at"`
	ResolvedAt    database.JSONTime `gorm:"column:resolved_at"`
	ClosedAt      database.JSONTime `gorm:"column:closed_at"`
	Remark        string            `gorm:"column:remark"`
	CreatedAt     database.JSONTime `gorm:"column:created_at"`
	UpdatedAt     database.JSONTime `gorm:"column:updated_at"`
	CreatedBy     int64             `gorm:"column:created_by"`
	UpdatedBy     int64             `gorm:"column:updated_by"`
}

// TableName 显式表名。
func (Exception) TableName() string { return "exceptions" }

// HandleRecordList 反序列化处理记录数组（损坏 JSON 返回空数组，不静默丢数据——原样保留
// 由审计行承载，此处只影响读视图）。
func (e *Exception) HandleRecordList() []HandleRecord {
	out := []HandleRecord{}
	if len(e.HandleRecords) == 0 {
		return out
	}
	_ = json.Unmarshal([]byte(e.HandleRecords), &out)
	return out
}

// ImageRefList 反序列化图片引用数组。
func (e *Exception) ImageRefList() []string {
	out := []string{}
	if len(e.ImageRefs) == 0 {
		return out
	}
	_ = json.Unmarshal([]byte(e.ImageRefs), &out)
	return out
}

// DocumentApproval 审批记录（迁移 000006 document_approvals；append-only 纯审计，
// 应用层无 UPDATE/DELETE 通路——database.md §7，DB 账号仅 SELECT+INSERT）。
// 退货单 submit/approve/reject/cancel 与业务同事务落本表（business-flow §12.2）。
type DocumentApproval struct {
	ID           database.ID       `gorm:"primaryKey;autoIncrement"`
	TargetType   string            `gorm:"column:target_type"`
	TargetNo     string            `gorm:"column:target_no"`
	Action       string            `gorm:"column:action"` // SUBMIT/APPROVE/REJECT/CANCEL
	Result       string            `gorm:"column:result"`
	Opinion      string            `gorm:"column:opinion"`
	OperatorID   int64             `gorm:"column:operator_id"`
	OperatorName string            `gorm:"column:operator_name"`
	RequestID    string            `gorm:"column:request_id"`
	CreatedAt    database.JSONTime `gorm:"column:created_at"`
}

// TableName 显式表名。
func (DocumentApproval) TableName() string { return "document_approvals" }

// 审批动作值域（chk_document_approvals_action）。
const (
	ApprovalActionSubmit  = "SUBMIT"
	ApprovalActionApprove = "APPROVE"
	ApprovalActionReject  = "REJECT"
	ApprovalActionCancel  = "CANCEL"
)
