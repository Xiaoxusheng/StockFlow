package returns

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/stock"
)

// 跨域窄接口与装配端口（backend-m2-plan §3.1 唯一跨域机制：消费方在包内定义最小接口，
// 实现由被消费域提供、router 装配经 Option 注入；域包之间禁止 import——
// 合法 import 面仅 internal/stock + internal/auth）。
//
// StockGateway 签名引用 internal/stock 值类型（plan §3 冻结机制，purchase/sales/stockops
// 同款）。inventory.Service 的别名承接（plan §8.3 条 4：type PutawayOp = stock.PutawayOp 等）
// 尚未落地，收编前 router 以闭包适配器逐字段桥接（purchase/ports.go StockGateway 注释
// 同口径）；别名落地后类型恒等，*inventory.Service 直接结构化满足本接口。

// StockGateway 库存原语消费接口（本域所需原语子集，plan §3.1 StockGateway 行）。
// 实现由 inventory.NewService(db, rdb) 承接（别名收编前经 router 闭包逐字段桥接）；
// router 装配注入。SerialStates 为 M2 修复轮新增：采购退货出库逐件校验台账的数据源
// （inventory-rules §8.2；plan §2.3 判据 7——跨域读走窄接口，不建旁路 SELECT）。
type StockGateway interface {
	Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error)
	Deduct(ctx context.Context, tx *gorm.DB, op stock.DeductOp) (stock.MutationResult, error)
	Lock(ctx context.Context, tx *gorm.DB, op stock.LockOp) (stock.MutationResult, error)
	ReleaseLock(ctx context.Context, tx *gorm.DB, op stock.ReleaseLockOp) (stock.MutationResult, error)
	InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error)
	EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (batchID int64, created bool, err error)
	SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (serialID int64, created bool, err error)
	// SerialStates 按序列号集合读台账当前状态（必须在业务事务句柄 tx 上执行——
	// 同事务"读台账→校验→SerialEvent"原子；不存在/SKU 不匹配的序列号不在结果中，
	// 调用方以缺失判定 fail-closed）。
	SerialStates(ctx context.Context, tx *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error)
}

// SKUFlags SKU 三开关 + 启用位（business-flow §1.2；stockops.SKUFlags 同形，
// 消费方包内定义——判据 7 窄接口机制）。
type SKUFlags struct {
	Enabled       bool
	BatchManaged  bool
	ExpiryManaged bool
	SerialManaged bool
}

// SKUFlagReader SKU 开关读取（plan §3.1 SKUAttrReader 同形窄接口的 returns 子集：
// 采购退货出库按序列号管理 SKU 逐件核销台账——inventory-rules §8.2）。实现方
// masterdata（NewSKUFlagReader，router 桥接注入）；未注入时依赖该开关的业务动作
// fail-closed 拒绝（ErrFlagReaderMissing），不静默按非序列号 SKU 处理。
type SKUFlagReader interface {
	GetFlags(ctx context.Context, skuID int64) (SKUFlags, error)
}

// ReturnableLine 来源单可退行（plan §3.1 Reader 窄接口返回形态：逐行可退量由本域自己的
// 累计比对——SumReturnedBySource，实现方只报原始量）。
type ReturnableLine struct {
	LineNo int64
	SKUID  int64
	Qty    int64 // 销售单=QtyShipped 已发货量；采购单=QtyReceived 已收货量
}

// SalesOrderReader 销售单读取接口（plan §3.1 冻结：returns.SalesOrderReader，实现由 sales
// 域提供、router 装配注入——sales 实现读自己的表，无需 import 本包即可经 router 闭包桥接）。
type SalesOrderReader interface {
	// FindReturnable 按销售单号取可退明细：销售单必须存在且已发货（QtyShipped>0）。
	// found=false 表示单号不存在或不可退；warehouseID 供退货仓一致性校验。
	FindReturnable(ctx context.Context, soNo string) (soID, warehouseID int64, lines []ReturnableLine, found bool, err error)
}

// PurchaseOrderReader 采购单读取接口（plan §3.1 冻结：returns.PurchaseOrderReader，
// 实现由 purchase 域提供、同形装配）。
type PurchaseOrderReader interface {
	// FindReturnable 按采购单号取可退明细：采购单必须存在且已收货（QtyReceived>0，
	// 退量 ≤ 收量，plan §3.1）。
	FindReturnable(ctx context.Context, poNo string) (poID, warehouseID int64, lines []ReturnableLine, found bool, err error)
}

// QCLine 退货质检单行（QCCreator 入参；数量走 numeric(18,4) 文本，域间契约不携带域类型）。
type QCLine struct {
	LineNo       int64
	SKUID        int64
	QtyInspected string
}

// QCCreator 质检单创建接口（plan §3.1 冻结：returns.QCCreator，实现由 purchase 域提供——
// 质检单一套实现，禁止本域另造，plan §3.1 表注）。source_type 固定传 "RETURN"。
type QCCreator interface {
	// CreateQC 创建质检单并返回质检单号（qc_no 全局唯一，质检结果应用时以
	// inspect:{qc_no}:{line_no}:{pass|defect} 幂等键回引——plan §7）。
	CreateQC(ctx context.Context, sourceType, sourceNo, qcType string, warehouseID int64, lines []QCLine) (qcNo string, err error)
}

// TraceLedger 追溯流水行（LedgerReader 返回形态；全部内建类型——库存流水的跨域只读投影，
// 数量为 numeric(18,4) 文本）。
type TraceLedger struct {
	ID           int64     `json:"id"`
	LedgerNo     string    `json:"ledger_no"`
	SKUID        int64     `json:"sku_id"`
	WarehouseID  int64     `json:"warehouse_id"`
	BinID        int64     `json:"bin_id"`
	BatchID      int64     `json:"batch_id"`
	SerialNo     string    `json:"serial_no,omitempty"`
	ChangeType   string    `json:"change_type"`
	BusinessType string    `json:"business_type"`
	BusinessNo   string    `json:"business_no"`
	StatusFrom   string    `json:"status_from"`
	StatusTo     string    `json:"status_to"`
	QtyBefore    string    `json:"qty_before"`
	QtyChange    string    `json:"qty_change"`
	QtyAfter     string    `json:"qty_after"`
	OperatorName string    `json:"operator_name"`
	RequestID    string    `json:"request_id,omitempty"`
	Remark       string    `json:"remark,omitempty"`
	CreatedAt    time.Time `json:"-"`
	CreatedAtStr string    `json:"created_at"` // YYYY-MM-DD HH:mm:ss（api.md §2）
}

// LedgerReader 库存流水读取接口（追溯数据源之一：inventory-rules §10；实现由 inventory
// 域经 router 闭包桥接其只读查询——plan §2.3 判据 7：跨域读不走旁路 SELECT）。
type LedgerReader interface {
	// LedgersForTrace 按 SKU（必填）/序列号（可选）取追溯流水：最新 limit 条，
	// 返回按时间正序（ CreatedAt 升序）排列；warehouseID>0 时按仓过滤。
	LedgersForTrace(ctx context.Context, skuID int64, serialNo string, warehouseID int64, limit int) ([]TraceLedger, error)
}

// TraceStockRow 当前库存行（StockStateReader 返回形态；六状态数量为 numeric(18,4) 文本）。
type TraceStockRow struct {
	WarehouseID    int64     `json:"warehouse_id"`
	ZoneID         int64     `json:"zone_id"`
	ShelfID        int64     `json:"shelf_id"`
	BinID          int64     `json:"bin_id"`
	SKUID          int64     `json:"sku_id"`
	BatchID        int64     `json:"batch_id"`
	Total          string    `json:"total_qty"`
	Available      string    `json:"available_qty"`
	Locked         string    `json:"locked_qty"`
	Frozen         string    `json:"frozen_qty"`
	PendingInspect string    `json:"pending_inspect_qty"`
	Defective      string    `json:"defective_qty"`
	UpdatedAt      time.Time `json:"-"`
	UpdatedAtStr   string    `json:"updated_at"`
}

// TraceSerialRow 序列号当前台账（inventory-rules §8：last_source_* 为最近一次状态变化
// 的追溯指针）。
type TraceSerialRow struct {
	SerialNo       string    `json:"serial_no"`
	SKUID          int64     `json:"sku_id"`
	BatchID        int64     `json:"batch_id"`
	WarehouseID    int64     `json:"warehouse_id"`
	BinID          int64     `json:"bin_id"`
	Status         string    `json:"status"`
	LastSourceType string    `json:"last_source_type"`
	LastSourceNo   string    `json:"last_source_no"`
	LastEventAt    time.Time `json:"-"`
	LastEventAtStr string    `json:"last_event_at"`
}

// StockStateReader 库存当前状态读取接口（追溯数据源之一：当前状态→库位→批次起点；
// 实现由 inventory 域经 router 闭包桥接其只读查询）。
type StockStateReader interface {
	// StockRowsBySKU 按 SKU 取当前库存行（warehouseID>0 时按仓过滤），至多 limit 条。
	StockRowsBySKU(ctx context.Context, skuID, warehouseID int64, limit int) ([]TraceStockRow, error)
	// SerialByNo 按序列号取当前台账；found=false 表示不存在。
	SerialByNo(ctx context.Context, serialNo string) (TraceSerialRow, bool, error)
}

// ---- Option 装配（plan §3.1：仅用于跨域消费接口注入，不得携带业务配置）----

type options struct {
	stock          StockGateway
	salesOrders    SalesOrderReader
	purchaseOrders PurchaseOrderReader
	qc             QCCreator
	ledgers        LedgerReader
	stockState     StockStateReader
	skuFlags       SKUFlagReader
}

// Option RegisterRoutes 的可选注入项。
type Option func(*options)

// WithStock 注入库存原语网关（router 装配：inventory.NewService(db, rdb)）。
func WithStock(g StockGateway) Option { return func(o *options) { o.stock = g } }

// WithSKUFlags 注入 SKU 开关读取（router 装配：masterdata.NewSKUFlagReader 桥接）。
func WithSKUFlags(r SKUFlagReader) Option { return func(o *options) { o.skuFlags = r } }

// WithSalesOrders 注入销售单读取实现（router 装配：sales 域提供）。
func WithSalesOrders(r SalesOrderReader) Option { return func(o *options) { o.salesOrders = r } }

// WithPurchaseOrders 注入采购单读取实现（router 装配：purchase 域提供）。
func WithPurchaseOrders(r PurchaseOrderReader) Option {
	return func(o *options) { o.purchaseOrders = r }
}

// WithQCCreator 注入质检单创建实现（router 装配：purchase 域提供）。
func WithQCCreator(c QCCreator) Option { return func(o *options) { o.qc = c } }

// WithLedgers 注入库存流水读取实现（router 装配：inventory 只读查询闭包桥接）。
func WithLedgers(r LedgerReader) Option { return func(o *options) { o.ledgers = r } }

// WithStockState 注入库存当前状态读取实现（router 装配：inventory 只读查询闭包桥接）。
func WithStockState(r StockStateReader) Option { return func(o *options) { o.stockState = r } }
