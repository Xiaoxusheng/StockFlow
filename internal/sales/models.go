package sales

import (
	"github.com/stockflow/server/internal/database"
)

// 销售域十表 GORM 模型（迁移 000008 列结构；backend-m2-plan §5 000008 冻结 DDL）。
// 模型仅承载扫描与查询视图 + GORM 查询构造器的列表路径；写路径（INSERT RETURNING、
// 状态守卫 UPDATE）走 repository 原生 SQL——本域表不在库存族判据范围（plan §2.3 判据 3
// 只约束库存族六表），但写形态与全项目一致（状态迁移必须有 WHERE status 守卫）。
type (
	// SalesOrder 销售订单（business-flow §6：审核即预占，预占失败订单停留
	// PENDING_APPROVAL——plan §6.4）。
	SalesOrder struct {
		ID              database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		SoNo            string            `gorm:"column:so_no" json:"so_no"`
		CustomerID      int64             `gorm:"column:customer_id" json:"customer_id"`
		WarehouseID     int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		ShippingAddress string            `gorm:"column:shipping_address" json:"shipping_address"`
		DeliveryMethod  string            `gorm:"column:delivery_method" json:"delivery_method"`
		TotalAmount     Qty               `gorm:"column:total_amount" json:"total_amount"`
		Status          string            `gorm:"column:status" json:"status"`
		ApprovedBy      int64             `gorm:"column:approved_by" json:"approved_by"`
		ApprovedAt      database.JSONTime `gorm:"column:approved_at" json:"approved_at"`
		ShippedAt       database.JSONTime `gorm:"column:shipped_at" json:"shipped_at"`
		CompletedAt     database.JSONTime `gorm:"column:completed_at" json:"completed_at"`
		CancelledAt     database.JSONTime `gorm:"column:cancelled_at" json:"cancelled_at"`
		Remark          string            `gorm:"column:remark" json:"remark"`
		CreatedAt       database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt       database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy       int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy       int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// SalesOrderItem 销售订单明细（qty_allocated/qty_shipped 承载预占与发货进度）。
	SalesOrderItem struct {
		ID           database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		SoID         int64             `gorm:"column:so_id" json:"so_id"`
		LineNo       int64             `gorm:"column:line_no" json:"line_no"`
		SKUID        int64             `gorm:"column:sku_id" json:"sku_id"`
		Qty          Qty               `gorm:"column:qty" json:"qty"`
		Price        Qty               `gorm:"column:price" json:"price"`
		Amount       Qty               `gorm:"column:amount" json:"amount"`
		QtyAllocated Qty               `gorm:"column:qty_allocated" json:"qty_allocated"`
		QtyShipped   Qty               `gorm:"column:qty_shipped" json:"qty_shipped"`
		Remark       string            `gorm:"column:remark" json:"remark"`
		CreatedAt    database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt    database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy    int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy    int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// OutboundOrder 出库单（business-flow §7.2 分配→拣货→复核→打包→发货执行链）。
	OutboundOrder struct {
		ID          database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		OutboundNo  string            `gorm:"column:outbound_no" json:"outbound_no"`
		SoNo        string            `gorm:"column:so_no" json:"so_no"`
		Type        string            `gorm:"column:type" json:"type"`
		WarehouseID int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		Status      string            `gorm:"column:status" json:"status"`
		PickedAt    database.JSONTime `gorm:"column:picked_at" json:"picked_at"`
		CheckedAt   database.JSONTime `gorm:"column:checked_at" json:"checked_at"`
		PackedAt    database.JSONTime `gorm:"column:packed_at" json:"packed_at"`
		ShippedAt   database.JSONTime `gorm:"column:shipped_at" json:"shipped_at"`
		CancelledAt database.JSONTime `gorm:"column:cancelled_at" json:"cancelled_at"`
		Remark      string            `gorm:"column:remark" json:"remark"`
		CreatedAt   database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt   database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy   int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy   int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// OutboundItem 出库单明细（qty_picked/checked/packed/shipped 各环节累计，
	// 支持部分拣货/部分发货）。
	OutboundItem struct {
		ID         database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		OutboundID int64             `gorm:"column:outbound_id" json:"outbound_id"`
		LineNo     int64             `gorm:"column:line_no" json:"line_no"`
		SKUID      int64             `gorm:"column:sku_id" json:"sku_id"`
		Qty        Qty               `gorm:"column:qty" json:"qty"`
		QtyPicked  Qty               `gorm:"column:qty_picked" json:"qty_picked"`
		QtyChecked Qty               `gorm:"column:qty_checked" json:"qty_checked"`
		QtyPacked  Qty               `gorm:"column:qty_packed" json:"qty_packed"`
		QtyShipped Qty               `gorm:"column:qty_shipped" json:"qty_shipped"`
		Remark     string            `gorm:"column:remark" json:"remark"`
		CreatedAt  database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt  database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy  int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy  int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// AllocationRecord 库存分配记录（business-flow §8.1 五策略；逐 bin 选行结果落行，
	// lock_id 回指 inventory_locks——发货 Deduct 核销依据）。
	AllocationRecord struct {
		ID          database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		OutboundNo  string            `gorm:"column:outbound_no" json:"outbound_no"`
		LineNo      int64             `gorm:"column:line_no" json:"line_no"`
		SKUID       int64             `gorm:"column:sku_id" json:"sku_id"`
		BatchID     int64             `gorm:"column:batch_id" json:"batch_id"`
		WarehouseID int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		BinID       int64             `gorm:"column:bin_id" json:"bin_id"`
		Qty         Qty               `gorm:"column:qty" json:"qty"`
		Strategy    string            `gorm:"column:strategy" json:"strategy"`
		Reason      map[string]any    `gorm:"column:reason;serializer:json" json:"reason"`
		LockID      int64             `gorm:"column:lock_id" json:"lock_id"`
		CreatedAt   database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt   database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy   int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy   int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// PickTask 拣货任务（business-flow §8.2：SKU→来源库位→数量→操作人→完成时间）。
	PickTask struct {
		ID                database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		PickNo            string            `gorm:"column:pick_no" json:"pick_no"`
		OutboundNo        string            `gorm:"column:outbound_no" json:"outbound_no"`
		OutboundLineNo    int64             `gorm:"column:outbound_line_no" json:"outbound_line_no"`
		SKUID             int64             `gorm:"column:sku_id" json:"sku_id"`
		BatchID           int64             `gorm:"column:batch_id" json:"batch_id"`
		SourceWarehouseID int64             `gorm:"column:source_warehouse_id" json:"source_warehouse_id"`
		SourceZoneID      int64             `gorm:"column:source_zone_id" json:"source_zone_id"`
		SourceShelfID     int64             `gorm:"column:source_shelf_id" json:"source_shelf_id"`
		SourceBinID       int64             `gorm:"column:source_bin_id" json:"source_bin_id"`
		Qty               Qty               `gorm:"column:qty" json:"qty"`
		PickedQty         Qty               `gorm:"column:picked_qty" json:"picked_qty"`
		Status            string            `gorm:"column:status" json:"status"`
		// Priority 任务优先级（效率层一期 B3，迁移 000023；0–9，默认 0）——
		// /api/tasks/next 排序层（mine > priority > 超时 > created_at）的数据来源，
		// 列表下发供前端「优先级」列与行内设置入口回显（PUT /api/picks/{id}/priority）。
		Priority          int16             `gorm:"column:priority" json:"priority"`
		AssigneeID        int64             `gorm:"column:assignee_id" json:"assignee_id"`
		AssigneeName      string            `gorm:"column:assignee_name" json:"assignee_name"`
		ClaimedAt         database.JSONTime `gorm:"column:claimed_at" json:"claimed_at"`
		PickedAt          database.JSONTime `gorm:"column:picked_at" json:"picked_at"`
		ScannedCode       string            `gorm:"column:scanned_code" json:"scanned_code"`
		ScanMatched       bool              `gorm:"column:scan_matched" json:"scan_matched"`
		WarehouseID       int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		Remark            string            `gorm:"column:remark" json:"remark"`
		CreatedAt         database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt         database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy         int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy         int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// CheckTask 复核任务（business-flow §8.3：重新确认 SKU/条码/数量/批次/序列号/订单；
	// 五类复核异常落 result；序列号 SKU 逐件一行一件）。
	CheckTask struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		CheckNo        string            `gorm:"column:check_no" json:"check_no"`
		OutboundNo     string            `gorm:"column:outbound_no" json:"outbound_no"`
		OutboundLineNo int64             `gorm:"column:outbound_line_no" json:"outbound_line_no"`
		SKUID          int64             `gorm:"column:sku_id" json:"sku_id"`
		BatchID        int64             `gorm:"column:batch_id" json:"batch_id"`
		SerialNo       string            `gorm:"column:serial_no" json:"serial_no"`
		Qty            Qty               `gorm:"column:qty" json:"qty"`
		Status         string            `gorm:"column:status" json:"status"`
		// Priority 复核任务优先级（效率层一期 B3，迁移 000023；0–9）——/api/tasks/next
		// checking 分支排序层（mine > priority > created_at）数据来源；列表下发供前端回显。
		Priority       int16             `gorm:"column:priority" json:"priority"`
		Result         string            `gorm:"column:result" json:"result"`
		AssigneeID     int64             `gorm:"column:assignee_id" json:"assignee_id"`
		AssigneeName   string            `gorm:"column:assignee_name" json:"assignee_name"`
		ClaimedAt      database.JSONTime `gorm:"column:claimed_at" json:"claimed_at"`
		DoneAt         database.JSONTime `gorm:"column:done_at" json:"done_at"`
		WarehouseID    int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		Remark         string            `gorm:"column:remark" json:"remark"`
		CreatedAt      database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt      database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy      int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy      int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// PackingRecord 打包记录（business-flow §8.4 全列；一个订单允许多个包裹）。
	PackingRecord struct {
		ID              database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		PackageNo       string            `gorm:"column:package_no" json:"package_no"`
		OutboundNo      string            `gorm:"column:outbound_no" json:"outbound_no"`
		PackingMaterial string            `gorm:"column:packing_material" json:"packing_material"`
		Length          Qty               `gorm:"column:length" json:"length"`
		Width           Qty               `gorm:"column:width" json:"width"`
		Height          Qty               `gorm:"column:height" json:"height"`
		Weight          Qty               `gorm:"column:weight" json:"weight"`
		Volume          Qty               `gorm:"column:volume" json:"volume"`
		Carrier         string            `gorm:"column:carrier" json:"carrier"`
		TrackingNo      string            `gorm:"column:tracking_no" json:"tracking_no"`
		WarehouseID     int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		IdempotencyKey  *string           `gorm:"column:idempotency_key" json:"idempotency_key"`
		Remark          string            `gorm:"column:remark" json:"remark"`
		CreatedAt       database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt       database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy       int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy       int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// PackingItem 包裹×明细多对多（business-flow §8.4 拆包场景）。
	PackingItem struct {
		ID         database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		PackageID  int64             `gorm:"column:package_id" json:"package_id"`
		OutboundID int64             `gorm:"column:outbound_id" json:"outbound_id"`
		LineNo     int64             `gorm:"column:line_no" json:"line_no"`
		Qty        Qty               `gorm:"column:qty" json:"qty"`
		Remark     string            `gorm:"column:remark" json:"remark"`
		CreatedAt  database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt  database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy  int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy  int64             `gorm:"column:updated_by" json:"updated_by"`
	}

	// Shipment 发货单（business-flow §8.5；PENDING→SHIPPED 触发正式扣减，
	// 后续物流态为纯记录流转）。
	Shipment struct {
		ID             database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
		ShipmentNo     string            `gorm:"column:shipment_no" json:"shipment_no"`
		OutboundNo     string            `gorm:"column:outbound_no" json:"outbound_no"`
		Carrier        string            `gorm:"column:carrier" json:"carrier"`
		TrackingNo     string            `gorm:"column:tracking_no" json:"tracking_no"`
		WarehouseID    int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
		ShipperID      int64             `gorm:"column:shipper_id" json:"shipper_id"`
		ShipperName    string            `gorm:"column:shipper_name" json:"shipper_name"`
		PackageCount   int64             `gorm:"column:package_count" json:"package_count"`
		Status         string            `gorm:"column:status" json:"status"`
		ShippedAt      database.JSONTime `gorm:"column:shipped_at" json:"shipped_at"`
		IdempotencyKey *string           `gorm:"column:idempotency_key" json:"idempotency_key"`
		Remark         string            `gorm:"column:remark" json:"remark"`
		CreatedAt      database.JSONTime `gorm:"column:created_at" json:"created_at"`
		UpdatedAt      database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
		CreatedBy      int64             `gorm:"column:created_by" json:"created_by"`
		UpdatedBy      int64             `gorm:"column:updated_by" json:"updated_by"`
	}
)

// TableName 显式表名（GORM 默认复数命名与迁移一致，此处显式固化防漂移）。
func (SalesOrder) TableName() string       { return "sales_orders" }
func (SalesOrderItem) TableName() string   { return "sales_order_items" }
func (OutboundOrder) TableName() string    { return "outbound_orders" }
func (OutboundItem) TableName() string     { return "outbound_items" }
func (AllocationRecord) TableName() string { return "allocation_records" }
func (PickTask) TableName() string         { return "pick_tasks" }
func (CheckTask) TableName() string        { return "check_tasks" }
func (PackingRecord) TableName() string    { return "packing_records" }
func (PackingItem) TableName() string      { return "packing_items" }
func (Shipment) TableName() string         { return "shipments" }

// ---- 状态值域（与迁移 CHECK 约束同源；plan §6.4–§6.5 状态机） ----

// 销售订单状态。
const (
	SOStatusDraft           = "DRAFT"
	SOStatusPendingApproval = "PENDING_APPROVAL"
	SOStatusApproved        = "APPROVED"
	SOStatusRejected        = "REJECTED"
	SOStatusPartialShipped  = "PARTIAL_SHIPPED"
	SOStatusShippedAll      = "SHIPPED_ALL"
	SOStatusCompleted       = "COMPLETED"
	SOStatusCancelled       = "CANCELLED"
)

// soTransitions 销售订单合法迁移表（business-flow §13.2：状态机守卫的唯一依据；
// 每次迁移均须可落一个业务方法，禁止散落 SET status）。
var soTransitions = map[string][]string{
	SOStatusDraft:           {SOStatusPendingApproval, SOStatusCancelled},
	SOStatusPendingApproval: {SOStatusApproved, SOStatusRejected, SOStatusCancelled},
	SOStatusApproved:        {SOStatusPartialShipped, SOStatusShippedAll, SOStatusCancelled},
	SOStatusRejected:        {},
	SOStatusPartialShipped:  {SOStatusShippedAll, SOStatusCompleted},
	SOStatusShippedAll:      {},
	SOStatusCompleted:       {},
	SOStatusCancelled:       {},
}

// 出库单状态。
const (
	OBStatusPendingAllocate = "PENDING_ALLOCATE"
	OBStatusAllocated       = "ALLOCATED"
	OBStatusPicking         = "PICKING"
	OBStatusPicked          = "PICKED"
	OBStatusChecked         = "CHECKED"
	OBStatusPacked          = "PACKED"
	OBStatusPartialShipped  = "PARTIAL_SHIPPED"
	OBStatusShippedAll      = "SHIPPED_ALL"
	OBStatusCancelled       = "CANCELLED"
	OBStatusClosed          = "CLOSED"
)

// obTransitions 出库单合法迁移表（plan §6.5）。
var obTransitions = map[string][]string{
	OBStatusPendingAllocate: {OBStatusAllocated, OBStatusCancelled},
	OBStatusAllocated:       {OBStatusPicking, OBStatusCancelled},
	OBStatusPicking:         {OBStatusPicked, OBStatusCancelled},
	OBStatusPicked:          {OBStatusChecked, OBStatusCancelled},
	OBStatusChecked:         {OBStatusPacked, OBStatusCancelled},
	OBStatusPacked:          {OBStatusPartialShipped, OBStatusShippedAll, OBStatusCancelled},
	OBStatusPartialShipped:  {OBStatusShippedAll, OBStatusClosed},
	OBStatusShippedAll:      {},
	OBStatusCancelled:       {},
	OBStatusClosed:          {},
}

// 拣货任务状态（迁移 chk_pick_tasks_status 值域；plan §6.5 领取原子抢占；
// PICKING 值域保留、本域状态机不使用——CLAIMED 后直接确认 PICKED）。
const (
	PickStatusPending   = "PENDING"
	PickStatusClaimed   = "CLAIMED"
	PickStatusPicking   = "PICKING"
	PickStatusPicked    = "PICKED"
	PickStatusException = "EXCEPTION"
	PickStatusCancelled = "CANCELLED"
)

// pickTransitions 拣货任务合法迁移表。
var pickTransitions = map[string][]string{
	PickStatusPending:   {PickStatusClaimed, PickStatusCancelled},
	PickStatusClaimed:   {PickStatusPicked, PickStatusException, PickStatusCancelled},
	PickStatusPicking:   {PickStatusPicked, PickStatusException, PickStatusCancelled},
	PickStatusPicked:    {},
	PickStatusException: {},
	PickStatusCancelled: {},
}

// 复核任务状态（迁移 chk_check_tasks_status 值域：无 CLAIMED——领取为原子指派，
// 不迁移状态；result 为空串 = 复核通过）。
const (
	CheckStatusPending   = "PENDING"
	CheckStatusDone      = "DONE"
	CheckStatusException = "EXCEPTION"
)

// 复核异常类型（business-flow §8.3 五类中文值域，迁移 chk_check_tasks_result）。
var checkResults = map[string]bool{"错货": true, "少货": true, "多货": true, "批次错误": true, "序列号错误": true}

// 发货单状态（business-flow §8.5；库存动作仅发生在 PENDING→SHIPPED，plan §6.5）。
const (
	ShipStatusPending   = "PENDING"
	ShipStatusShipped   = "SHIPPED"
	ShipStatusInTransit = "IN_TRANSIT"
	ShipStatusSigned    = "SIGNED"
	ShipStatusAbnormal  = "ABNORMAL"
)

// shipTransitions 发货单合法迁移表（后续物流态为人工流转记录，不接物流 API）。
var shipTransitions = map[string][]string{
	ShipStatusPending:   {ShipStatusShipped},
	ShipStatusShipped:   {ShipStatusInTransit, ShipStatusAbnormal},
	ShipStatusInTransit: {ShipStatusSigned, ShipStatusAbnormal},
	ShipStatusSigned:    {},
	ShipStatusAbnormal:  {},
}

// 出库类型（迁移 chk_outbound_orders_type 值域，business-flow §7.1 全量保留；
// M2 实际入口为销售出库，其余值域随 api 入参校验白名单开放）。
var outboundTypes = map[string]bool{
	"销售出库": true, "生产领料": true, "调拨出库": true, "其他出库": true, "报损出库": true,
}

// 分配策略（迁移 chk_allocation_records_strategy 值域，business-flow §8.1）。
const (
	AllocStrategyFIFO      = "FIFO"
	AllocStrategyFEFO      = "FEFO"
	AllocStrategyBatch     = "指定批次"
	AllocStrategyWarehouse = "指定仓库"
	AllocStrategyBin       = "指定库位"
)

// canTransition 状态机守卫纯函数：from→to 是否合法迁移（business-flow §13.2）。
func canTransition(table map[string][]string, from, to string) bool {
	for _, t := range table[from] {
		if t == to {
			return true
		}
	}
	return false
}
