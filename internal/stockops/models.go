package stockops

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// stockops 域单据表 GORM 模型（迁移 000009 列结构；database.md §3 通用字段）。
// 仅本域五表——库存族六表模型仍仅定义/导出于 internal/inventory（M1 §4.2 判据 3）。
type (
	// TransferOrder 调拨单（business-flow §10.1 六态；APPROVED=待出库、
	// AWAITING_RECEIPT=待入库，backend-m2-plan §6.6 命名对应）。
	TransferOrder struct {
		ID              database.ID       `gorm:"primaryKey;autoIncrement"`
		TransferNo      string            `gorm:"column:transfer_no"`
		Type            string            `gorm:"column:type"` // WAREHOUSE / BIN
		FromWarehouseID int64             `gorm:"column:from_warehouse_id"`
		ToWarehouseID   int64             `gorm:"column:to_warehouse_id"`
		Status          string            `gorm:"column:status"`
		ApprovedBy      int64             `gorm:"column:approved_by"`
		ApprovedAt      database.JSONTime `gorm:"column:approved_at"`
		OutboundAt      database.JSONTime `gorm:"column:outbound_at"`
		ReceivedAt      database.JSONTime `gorm:"column:received_at"`
		CancelledAt     database.JSONTime `gorm:"column:cancelled_at"`
		Remark          string            `gorm:"column:remark"`
		CreatedAt       database.JSONTime `gorm:"column:created_at"`
		UpdatedAt       database.JSONTime `gorm:"column:updated_at"`
		CreatedBy       int64             `gorm:"column:created_by"`
		UpdatedBy       int64             `gorm:"column:updated_by"`
	}

	// TransferItem 调拨明细（在途 = QtyOut - QtyIn，backend-m2-plan §5 000009）。
	TransferItem struct {
		ID              database.ID       `gorm:"primaryKey;autoIncrement"`
		TransferID      int64             `gorm:"column:transfer_id"`
		LineNo          int               `gorm:"column:line_no"`
		SKUID           int64             `gorm:"column:sku_id"`
		BatchID         int64             `gorm:"column:batch_id"` // 0=非批次 SKU
		FromWarehouseID int64             `gorm:"column:from_warehouse_id"`
		FromZoneID      int64             `gorm:"column:from_zone_id"`
		FromShelfID     int64             `gorm:"column:from_shelf_id"`
		FromBinID       int64             `gorm:"column:from_bin_id"`
		ToWarehouseID   int64             `gorm:"column:to_warehouse_id"`
		ToZoneID        int64             `gorm:"column:to_zone_id"`
		ToShelfID       int64             `gorm:"column:to_shelf_id"`
		ToBinID         int64             `gorm:"column:to_bin_id"`
		Qty             stock.Qty         `gorm:"column:qty"`
		QtyOut          stock.Qty         `gorm:"column:qty_out"`
		QtyIn           stock.Qty         `gorm:"column:qty_in"`
		Remark          string            `gorm:"column:remark"`
		CreatedAt       database.JSONTime `gorm:"column:created_at"`
		UpdatedAt       database.JSONTime `gorm:"column:updated_at"`
		CreatedBy       int64             `gorm:"column:created_by"`
		UpdatedBy       int64             `gorm:"column:updated_by"`
	}

	// CountOrder 盘点单（business-flow §10.2；scope 为范围声明快照 jsonb）。
	CountOrder struct {
		ID          database.ID       `gorm:"primaryKey;autoIncrement"`
		CountNo     string            `gorm:"column:count_no"`
		WarehouseID int64             `gorm:"column:warehouse_id"`
		Scope       CountScope        `gorm:"column:scope;type:jsonb"`
		Status      string            `gorm:"column:status"`
		FrozenAt    database.JSONTime `gorm:"column:frozen_at"`
		ReviewedAt  database.JSONTime `gorm:"column:reviewed_at"`
		CompletedAt database.JSONTime `gorm:"column:completed_at"`
		CancelledAt database.JSONTime `gorm:"column:cancelled_at"`
		Remark      string            `gorm:"column:remark"`
		CreatedAt   database.JSONTime `gorm:"column:created_at"`
		UpdatedAt   database.JSONTime `gorm:"column:updated_at"`
		CreatedBy   int64             `gorm:"column:created_by"`
		UpdatedBy   int64             `gorm:"column:updated_by"`
	}

	// CountItem 盘点明细（qty_system 冻结快照；qty_counted 实盘，NULL=未登记；
	// 序列号 SKU 逐件一行 qty=1，非序列号 SKU 单行 serial_no=''，inventory-rules §8.2）。
	CountItem struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement"`
		CountID        int64             `gorm:"column:count_id"`
		InventoryRowID int64             `gorm:"column:inventory_row_id"`
		SKUID          int64             `gorm:"column:sku_id"`
		WarehouseID    int64             `gorm:"column:warehouse_id"`
		ZoneID         int64             `gorm:"column:zone_id"`
		ShelfID        int64             `gorm:"column:shelf_id"`
		BinID          int64             `gorm:"column:bin_id"`
		QtySystem      stock.Qty         `gorm:"column:qty_system"`
		QtyCounted     NullQty           `gorm:"column:qty_counted"`
		CountedBy      int64             `gorm:"column:counted_by"`
		CountedAt      database.JSONTime `gorm:"column:counted_at"`
		SerialNo       string            `gorm:"column:serial_no"`
		CreatedAt      database.JSONTime `gorm:"column:created_at"`
		UpdatedAt      database.JSONTime `gorm:"column:updated_at"`
		CreatedBy      int64             `gorm:"column:created_by"`
		UpdatedBy      int64             `gorm:"column:updated_by"`
	}

	// CountDifference 盘点差异（diff_qty = qty_counted - qty_system，盘盈为正；
	// adjust_no 为差异批准后执行的调整单单号回写）。
	CountDifference struct {
		ID          database.ID       `gorm:"primaryKey;autoIncrement"`
		CountID     int64             `gorm:"column:count_id"`
		LineNo      int               `gorm:"column:line_no"`
		SKUID       int64             `gorm:"column:sku_id"`
		WarehouseID int64             `gorm:"column:warehouse_id"`
		BinID       int64             `gorm:"column:bin_id"`
		BatchID     int64             `gorm:"column:batch_id"` // 0=非批次 SKU
		QtySystem   stock.Qty         `gorm:"column:qty_system"`
		QtyCounted  stock.Qty         `gorm:"column:qty_counted"`
		DiffQty     stock.Qty         `gorm:"column:diff_qty"`
		AdjustNo    string            `gorm:"column:adjust_no"`
		Status      string            `gorm:"column:status"` // PENDING/APPROVED/REJECTED/EXECUTED
		Remark      string            `gorm:"column:remark"`
		CreatedAt   database.JSONTime `gorm:"column:created_at"`
		UpdatedAt   database.JSONTime `gorm:"column:updated_at"`
		CreatedBy   int64             `gorm:"column:created_by"`
		UpdatedBy   int64             `gorm:"column:updated_by"`
	}
)

// TableName 显式表名（与迁移 000009 一致，防漂移）。
func (TransferOrder) TableName() string   { return "transfer_orders" }
func (TransferItem) TableName() string    { return "transfer_items" }
func (CountOrder) TableName() string      { return "count_orders" }
func (CountItem) TableName() string       { return "count_items" }
func (CountDifference) TableName() string { return "count_differences" }

// —— 状态值域（与迁移 000009 CHECK 约束同源，方案 §6.6/§6.7）——

const (
	TransferDraft     = "DRAFT"
	TransferPending   = "PENDING_APPROVAL"
	TransferApproved  = "APPROVED" // 待出库
	TransferMoving    = "TRANSFERRING"
	TransferAwaiting  = "AWAITING_RECEIPT" // 待入库
	TransferCompleted = "COMPLETED"
	TransferCancelled = "CANCELLED"

	CountDraft     = "DRAFT"
	CountCounting  = "COUNTING"
	CountReview    = "PENDING_REVIEW"
	CountCompleted = "COMPLETED"
	CountCancelled = "CANCELLED"

	DiffPending  = "PENDING"
	DiffRejected = "REJECTED"
	DiffExecuted = "EXECUTED"

	TransferTypeWarehouse = "WAREHOUSE"
	TransferTypeBin       = "BIN"
)

// validTransferTypes / validCountStatuses 供入参与迁移守卫校验。
var validTransferTypes = map[string]bool{TransferTypeWarehouse: true, TransferTypeBin: true}

// CountScope 盘点范围声明（business-flow §10.2：全盘/按仓/按库区/按货架/按库位/按 SKU；
// 按仓 = 单据 warehouse_id 本身，快照落 scope jsonb）。
type CountScope struct {
	Mode     string  `json:"mode"` // ALL/ZONE/SHELF/BIN/SKU
	ZoneIDs  []int64 `json:"zone_ids,omitempty"`
	ShelfIDs []int64 `json:"shelf_ids,omitempty"`
	BinIDs   []int64 `json:"bin_ids,omitempty"`
	SKUIDs   []int64 `json:"sku_ids,omitempty"`
}

// scanScopeModes mode 值域与各 mode 允许携带的维度。
var scanScopeModes = map[string][]string{
	"ALL":   {},
	"ZONE":  {"zone_ids"},
	"SHELF": {"shelf_ids"},
	"BIN":   {"bin_ids"},
	"SKU":   {"sku_ids"},
}

// validate 范围声明校验（api.md §4：后端完整校验）。
func (s CountScope) validate() error {
	dims, ok := scanScopeModes[s.Mode]
	if !ok {
		return response.NewError(ErrScopeInvalid, map[string]any{"mode": s.Mode})
	}
	allowed := map[string]bool{}
	for _, d := range dims {
		allowed[d] = true
	}
	for field, ids := range map[string][]int64{
		"zone_ids": s.ZoneIDs, "shelf_ids": s.ShelfIDs, "bin_ids": s.BinIDs, "sku_ids": s.SKUIDs,
	} {
		seen := map[int64]bool{}
		for _, id := range ids {
			if id <= 0 {
				return response.NewError(ErrScopeInvalid, map[string]any{"field": field, "value": id})
			}
			if seen[id] {
				return response.NewError(ErrScopeInvalid, map[string]any{"field": field, "value": id, "reason": "重复"})
			}
			seen[id] = true
		}
		if len(ids) > 0 && !allowed[field] {
			return response.NewError(ErrScopeInvalid, map[string]any{"field": field, "reason": "mode " + s.Mode + " 不允许携带该维度"})
		}
	}
	return nil
}

// Value 实现 driver.Valuer（jsonb 列）。
func (s CountScope) Value() (driver.Value, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("序列化盘点范围失败: %w", err)
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner（jsonb 列）。
func (s *CountScope) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = CountScope{Mode: "ALL"}
	case []byte:
		return json.Unmarshal(v, s)
	case string:
		return json.Unmarshal([]byte(v), s)
	default:
		return fmt.Errorf("CountScope.Scan 不支持类型 %T", src)
	}
	return nil
}

// NullQty qty_counted 可空载体：区分"未登记"（NULL）与"登记为 0"
// （inventory-rules §9：实盘为 0 必须显式登记，二者语义不同）。
type NullQty struct {
	Qty   stock.Qty
	Valid bool
}

// Value 实现 driver.Valuer：NULL 或十进制文本。
func (n NullQty) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Qty.String(), nil
}

// Scan 实现 sql.Scanner。
func (n *NullQty) Scan(src any) error {
	if src == nil {
		n.Qty, n.Valid = 0, false
		return nil
	}
	if err := n.Qty.Scan(src); err != nil {
		return err
	}
	n.Valid = true
	return nil
}

// ApprovalRecord 审批记录（document_approvals，append-only，business-flow §12.2：
// 审批人/时间/意见/结果不可修改删除——本表应用层无 UPDATE/DELETE 通路）。
type ApprovalRecord struct {
	TargetType   string // transfer_order / count_order
	TargetNo     string
	Action       string // SUBMIT/APPROVE/REJECT/CANCEL（迁移 chk_document_approvals_action）
	Result       string // APPROVED/REJECTED/CANCELLED；SUBMIT 为空串
	Opinion      string
	OperatorID   int64
	OperatorName string
	RequestID    string
}
