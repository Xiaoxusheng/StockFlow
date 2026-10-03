package inventory

import (
	"github.com/stockflow/server/internal/database"
)

// 库存族六表 GORM 模型（迁移 000005 列结构）。
//
// 违约判据（backend-m1-plan §4.2 判据 3）：库存族模型仅可定义/导出于本包——
// 域外拿不到模型类型即无法构造 GORM 链式写（db.Model(...).Update 等），
// 与字符串守卫（§8.6 第一道）共同构成两道防线。
//
// 说明：
//   - 六表均无 deleted_at（迁移 000005 与 database.md §5.1 软删除清单一致——
//     库存/流水/批次/序列号/锁定/调整单不软删；inventory 行归零由业务调整完成）；
//   - 批次/序列号等需要行级写操作一律经本包 Service/Repository（原生 SQL），
//     模型仅承载扫描与查询视图。
type (
	// Inventory 实时库存（五维唯一：同仓同位同批同 SKU 恰一行，迁移 uk_inventory_location）。
	Inventory struct {
		ID                database.ID       `gorm:"primaryKey;autoIncrement"`
		WarehouseID       int64             `gorm:"column:warehouse_id"`
		ZoneID            int64             `gorm:"column:zone_id"`
		ShelfID           int64             `gorm:"column:shelf_id"`
		BinID             int64             `gorm:"column:bin_id"`
		SKUID             int64             `gorm:"column:sku_id"`
		BatchID           int64             `gorm:"column:batch_id"` // 0=非批次 SKU（plan §6.2）
		TotalQty          Qty               `gorm:"column:total_qty"`
		AvailableQty      Qty               `gorm:"column:available_qty"`
		LockedQty         Qty               `gorm:"column:locked_qty"`
		FrozenQty         Qty               `gorm:"column:frozen_qty"`
		PendingInspectQty Qty               `gorm:"column:pending_inspect_qty"`
		DefectiveQty      Qty               `gorm:"column:defective_qty"`
		CreatedAt         database.JSONTime `gorm:"column:created_at"`
		UpdatedAt         database.JSONTime `gorm:"column:updated_at"`
		CreatedBy         int64             `gorm:"column:created_by"`
		UpdatedBy         int64             `gorm:"column:updated_by"`
	}

	// InventoryLock 库存锁定（inventory-rules §4：记录来源单据/类型/数量/操作人/时间）。
	InventoryLock struct {
		ID          database.ID       `gorm:"primaryKey;autoIncrement"`
		WarehouseID int64             `gorm:"column:warehouse_id"`
		BinID       int64             `gorm:"column:bin_id"`
		SKUID       int64             `gorm:"column:sku_id"`
		BatchID     int64             `gorm:"column:batch_id"`
		LockType    string            `gorm:"column:lock_type"`
		SourceType  string            `gorm:"column:source_type"`
		SourceNo    string            `gorm:"column:source_no"`
		Qty         Qty               `gorm:"column:qty"`
		Status      string            `gorm:"column:status"` // ACTIVE/RELEASED/CONSUMED
		ReleasedAt  database.JSONTime `gorm:"column:released_at"`
		ReleasedBy  int64             `gorm:"column:released_by"`
		Remark      string            `gorm:"column:remark"`
		CreatedAt   database.JSONTime `gorm:"column:created_at"`
		UpdatedAt   database.JSONTime `gorm:"column:updated_at"`
		CreatedBy   int64             `gorm:"column:created_by"`
		UpdatedBy   int64             `gorm:"column:updated_by"`
	}

	// InventoryLedger 库存流水（inventory-rules §5：只增不改不删 append-only；
	// 与库存变更同事务提交；应用层不提供任何 UPDATE/DELETE 通路，database.md §7）。
	InventoryLedger struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement"`
		LedgerNo       string            `gorm:"column:ledger_no"`
		SKUID          int64             `gorm:"column:sku_id"`
		WarehouseID    int64             `gorm:"column:warehouse_id"`
		ZoneID         *int64            `gorm:"column:zone_id"`
		ShelfID        *int64            `gorm:"column:shelf_id"`
		BinID          int64             `gorm:"column:bin_id"`
		BatchID        int64             `gorm:"column:batch_id"`
		SerialNo       string            `gorm:"column:serial_no"`
		ChangeType     string            `gorm:"column:change_type"`
		BusinessType   string            `gorm:"column:business_type"`
		BusinessNo     string            `gorm:"column:business_no"`
		StatusFrom     string            `gorm:"column:status_from"`
		StatusTo       string            `gorm:"column:status_to"`
		QtyBefore      Qty               `gorm:"column:qty_before"`
		QtyChange      Qty               `gorm:"column:qty_change"`
		QtyAfter       Qty               `gorm:"column:qty_after"`
		IdempotencyKey *string           `gorm:"column:idempotency_key"`
		OperatorID     int64             `gorm:"column:operator_id"`
		OperatorName   string            `gorm:"column:operator_name"`
		RequestID      string            `gorm:"column:request_id"`
		Remark         string            `gorm:"column:remark"`
		CreatedAt      database.JSONTime `gorm:"column:created_at"`
	}

	// InventoryAdjustment 库存调整单（business-flow §11.1；M1 只落 schema 与 Service 执行入口）。
	InventoryAdjustment struct {
		ID           database.ID       `gorm:"primaryKey;autoIncrement"`
		AdjustmentNo string            `gorm:"column:adjustment_no"`
		WarehouseID  int64             `gorm:"column:warehouse_id"`
		SKUID        int64             `gorm:"column:sku_id"`
		BinID        int64             `gorm:"column:bin_id"`
		BatchID      int64             `gorm:"column:batch_id"`
		AdjustType   string            `gorm:"column:adjust_type"` // 盘盈/盘亏/损耗/报废/其他
		Qty          Qty               `gorm:"column:qty"`
		Reason       string            `gorm:"column:reason"`
		Status       string            `gorm:"column:status"`
		ApprovedBy   int64             `gorm:"column:approved_by"`
		ApprovedAt   database.JSONTime `gorm:"column:approved_at"`
		ExecutedBy   int64             `gorm:"column:executed_by"`
		ExecutedAt   database.JSONTime `gorm:"column:executed_at"`
		CreatedAt    database.JSONTime `gorm:"column:created_at"`
		UpdatedAt    database.JSONTime `gorm:"column:updated_at"`
		CreatedBy    int64             `gorm:"column:created_by"`
		UpdatedBy    int64             `gorm:"column:updated_by"`
	}

	// Batch 批次台账（inventory-rules §6：启用批次管理的 SKU 按批次记库存）。
	Batch struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement"`
		SKUID          int64             `gorm:"column:sku_id"`
		BatchNo        string            `gorm:"column:batch_no"`
		SupplierID     int64             `gorm:"column:supplier_id"`
		ProductionDate database.JSONTime `gorm:"column:production_date"`
		InboundDate    database.JSONTime `gorm:"column:inbound_date"`
		ExpiryDate     database.JSONTime `gorm:"column:expiry_date"`
		CostPrice      Qty               `gorm:"column:cost_price"`
		Remark         string            `gorm:"column:remark"`
		CreatedAt      database.JSONTime `gorm:"column:created_at"`
		UpdatedAt      database.JSONTime `gorm:"column:updated_at"`
		CreatedBy      int64             `gorm:"column:created_by"`
		UpdatedBy      int64             `gorm:"column:updated_by"`
	}

	// SerialNumber 序列号（inventory-rules §8：全局唯一、一物一行；
	// last_source_* 为最近一次状态变化的追溯指针，完整历史经 operation_logs 与业务单据）。
	SerialNumber struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement"`
		SerialNo       string            `gorm:"column:serial_no"`
		SKUID          int64             `gorm:"column:sku_id"`
		BatchID        int64             `gorm:"column:batch_id"`
		WarehouseID    int64             `gorm:"column:warehouse_id"`
		BinID          int64             `gorm:"column:bin_id"`
		Status         string            `gorm:"column:status"`
		LastSourceType string            `gorm:"column:last_source_type"`
		LastSourceNo   string            `gorm:"column:last_source_no"`
		LastEventAt    database.JSONTime `gorm:"column:last_event_at"`
		CreatedAt      database.JSONTime `gorm:"column:created_at"`
		UpdatedAt      database.JSONTime `gorm:"column:updated_at"`
		CreatedBy      int64             `gorm:"column:created_by"`
		UpdatedBy      int64             `gorm:"column:updated_by"`
	}
)

// TableName 显式表名（GORM 默认复数命名与迁移一致，此处显式固化防漂移）。
func (Inventory) TableName() string           { return "inventory" }
func (InventoryLock) TableName() string       { return "inventory_locks" }
func (InventoryLedger) TableName() string     { return "inventory_ledgers" }
func (InventoryAdjustment) TableName() string { return "inventory_adjustments" }
func (Batch) TableName() string               { return "batches" }
func (SerialNumber) TableName() string        { return "serial_numbers" }

// State 取库存行六状态快照（恒等式校验与流水三态计算的数据源）。
func (m *Inventory) State() StockState {
	return StockState{
		Total:          m.TotalQty,
		Available:      m.AvailableQty,
		Locked:         m.LockedQty,
		Frozen:         m.FrozenQty,
		PendingInspect: m.PendingInspectQty,
		Defective:      m.DefectiveQty,
	}
}
