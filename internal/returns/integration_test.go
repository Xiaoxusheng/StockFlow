//go:build integration

// 真实 PostgreSQL 依赖的退货域集成测试（backend-m2-plan §11.2 退货子集、§11.3 并发收货；
// 默认 go test 不编译本文件——单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/returns/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip）。
//
// 覆盖（本域职责面）：
//   - SQL 守卫真路径：状态机 UPDATE 影响行数 0 → 域内冲突码；AddItemReceived 数量守卫
//     （WHERE qty_received + n <= qty_return）超量拒绝 + 真实事务回滚（库存无残留）；
//   - 退货收货→质检：真实 inventory.Service（Putaway RequireInspect → InspectResult），
//     合格→available / 不良→defective，恒等式由 CHECK 约束兜底（inventory-rules §2）；
//   - 采购退货出库：真实 Lock(ORDER_HOLD)→Deduct（total/locked 同减）+ 幂等重放；
//   - 异常冻结/解冻：真实 EXCEPTION_FREEZE 锁与 RELEASE 流水（inventory-rules §4.2）；
//   - 并发收货不超量（§11.3）：行锁 + 数量守卫由真实 PG 裁决；
//   - 追溯：真实 inventory_ledgers → LedgerReader/StockStateReader 桥接（router 装配
//     将采用同款闭包形态）→ 链完整。
//
// inventory→stock 桥接适配器（inventoryStockBridge）：inventory.Service 类型别名承接
// （plan §8.3 条 4）落地前，router 需以逐字段闭包桥接收编——本适配器即该桥接的可行性
// 验证（字段 1:1，stock 值类型为 inventory 类型原样搬迁）。
//
// sales_orders/purchase_orders 来源单数据由本测试直接 INSERT 迁移 000007/000008 表（测试
// 夹具），经测试内只读 Reader 注入——生产实现由 sales/purchase 域提供（plan §3.1）；
// QCCreator 为测试替身（记录调用并返回合成单号；质检单一套实现归 purchase 域，本测试
// 不落 quality_orders，不构成第二套质检实现）。
package returns

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("SF_TEST_PG_NAME") == "" {
		t.Skip("未设置 SF_TEST_PG_* 环境变量，跳过集成测试")
	}
	port, err := strconv.Atoi(envOrReturns("SF_TEST_PG_PORT", "5432"))
	require.NoError(t, err)
	cfg := config.DatabaseConfig{
		Host: envOrReturns("SF_TEST_PG_HOST", "127.0.0.1"), Port: port,
		User: envOrReturns("SF_TEST_PG_USER", "postgres"), Password: os.Getenv("SF_TEST_PG_PASSWORD"),
		Name: os.Getenv("SF_TEST_PG_NAME"), SSLMode: "disable",
		MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 10 * time.Minute,
	}
	db, err := database.Connect(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "db", "migrations"))
	require.NoError(t, err)
	require.NoError(t, database.MigrateUp(cfg.DSN(), migrationsDir))
	return db
}

func envOrReturns(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// inventoryStockBridge inventory.Service → returns.StockGateway 的逐字段桥接
// （inventory 别名收编前的形态；字段 1:1，别名落地后可整体替换为直接注入）。
// RowKey/Source/Actor 为独立命名类型（stock 与 inventory 未别名承接前不等价），逐字段转换。
type inventoryStockBridge struct{ svc *inventory.Service }

func toInvKey(k stock.RowKey) inventory.RowKey {
	return inventory.RowKey{
		WarehouseID: k.WarehouseID, ZoneID: k.ZoneID, ShelfID: k.ShelfID,
		BinID: k.BinID, SKUID: k.SKUID, BatchID: k.BatchID,
	}
}

func toInvSource(s stock.Source) inventory.Source { return inventory.Source{Type: s.Type, No: s.No} }

func toInvActor(a stock.Actor) inventory.Actor {
	return inventory.Actor{
		ID: a.ID, Name: a.Name, RequestID: a.RequestID,
		IP: a.IP, UserAgent: a.UserAgent, Method: a.Method, Path: a.Path,
	}
}

// toStockResult MutationResult 逐字段转换（Replay/Ledger/LockID）。
func toStockResult(res inventory.MutationResult) stock.MutationResult {
	return stock.MutationResult{
		Replay: res.Replay,
		Ledger: stock.LedgerRef{ID: res.Ledger.ID, LedgerNo: res.Ledger.LedgerNo},
		LockID: res.LockID,
	}
}

func (b inventoryStockBridge) Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	res, err := b.svc.Putaway(ctx, tx, inventory.PutawayOp{
		Key: toInvKey(op.Key), Qty: inventory.Qty(op.Qty), RequireInspect: op.RequireInspect,
		Source: toInvSource(op.Source), Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
	return toStockResult(res), err
}

func (b inventoryStockBridge) Deduct(ctx context.Context, tx *gorm.DB, op stock.DeductOp) (stock.MutationResult, error) {
	res, err := b.svc.Deduct(ctx, tx, inventory.DeductOp{
		Key: toInvKey(op.Key), Qty: inventory.Qty(op.Qty), LockID: op.LockID,
		Source: toInvSource(op.Source), Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
	return toStockResult(res), err
}

func (b inventoryStockBridge) Lock(ctx context.Context, tx *gorm.DB, op stock.LockOp) (stock.MutationResult, error) {
	res, err := b.svc.Lock(ctx, tx, inventory.LockOp{
		Key: toInvKey(op.Key), Qty: inventory.Qty(op.Qty), LockType: op.LockType,
		Source: toInvSource(op.Source), Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
	return toStockResult(res), err
}

func (b inventoryStockBridge) ReleaseLock(ctx context.Context, tx *gorm.DB, op stock.ReleaseLockOp) (stock.MutationResult, error) {
	res, err := b.svc.ReleaseLock(ctx, tx, inventory.ReleaseLockOp{
		LockID: op.LockID, Qty: inventory.Qty(op.Qty), Source: toInvSource(op.Source),
		Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
	return toStockResult(res), err
}

func (b inventoryStockBridge) InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	res, err := b.svc.InspectResult(ctx, tx, inventory.InspectResultOp{
		Key: toInvKey(op.Key), Qty: inventory.Qty(op.Qty), Pass: op.Pass,
		Source: toInvSource(op.Source), Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
	return toStockResult(res), err
}

func (b inventoryStockBridge) EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	return b.svc.EnsureBatch(ctx, tx, inventory.BatchOp{
		SKUID: op.SKUID, BatchNo: op.BatchNo, SupplierID: op.SupplierID,
		ProductionDate: op.ProductionDate, InboundDate: op.InboundDate, ExpiryDate: op.ExpiryDate,
		CostPrice: inventory.Qty(op.CostPrice), Actor: toInvActor(op.Actor), Remark: op.Remark,
	})
}

func (b inventoryStockBridge) SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	return b.svc.SerialEvent(ctx, tx, inventory.SerialOp{
		SerialNo: op.SerialNo, SKUID: op.SKUID, BatchID: op.BatchID,
		WarehouseID: op.WarehouseID, BinID: op.BinID, Status: op.Status,
		Source: toInvSource(op.Source), Actor: toInvActor(op.Actor), IdempotencyKey: op.IdempotencyKey, Remark: op.Remark,
	})
}

// SerialStates 桥接 M2 修复轮新增的采购退货序列号逐件校验（inventory.Service.SerialStates）。
func (b inventoryStockBridge) SerialStates(ctx context.Context, tx *gorm.DB, skuID int64, serials []string) ([]stock.SerialState, error) {
	return b.svc.SerialStates(ctx, tx, skuID, serials)
}

// ---- 测试内只读 Reader/替身（生产实现归 sales/purchase 域，plan §3.1）----

type integrationQCCreator struct {
	t           *testing.T
	seq         int
	mu          sync.Mutex
	completions []integrationQCCompletion
}

// integrationQCCompletion 记录一次 CompleteQC 调用（断言退货全量检完时质检单被回写收尾）。
type integrationQCCompletion struct {
	QCNo  string
	Lines []QCResultLine
}

func (c *integrationQCCreator) CreateQC(_ context.Context, sourceType, sourceNo, qcType string, warehouseID int64, lines []QCLine) (string, error) {
	require.NotEmpty(c.t, sourceType)
	require.NotEmpty(c.t, sourceNo)
	require.NotEmpty(c.t, lines)
	c.seq++
	// 单号必须跨轮次唯一：inspect:{qc_no}:{line}:{pass} 是 ledger 幂等键，QC 号复用会让
	// 质检结果应用命中原语重放（不落账）→ 库存断言恒 0（2026-10-08 实测定位）。
	return fmt.Sprintf("QC-TEST-%d-%05d", itRunID, c.seq), nil
}

// CompleteQC 质检单回写收尾（QCCreator 契约，2026-10-08 问题 5 修复新增）：真库不落
// quality_orders（质检单一套实现归 purchase 域，本包不构成第二套），只记录调用供断言。
func (c *integrationQCCreator) CompleteQC(_ context.Context, _ int64, _ string, qcNo string, lines []QCResultLine) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.completions = append(c.completions, integrationQCCompletion{QCNo: qcNo, Lines: append([]QCResultLine(nil), lines...)})
	return nil
}

// completionCount 已回写收尾次数。
func (c *integrationQCCreator) completionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.completions)
}

// integrationSKUFlags SKU 开关桩：集成用例种子 SKU（intSkuID）为普通品（非批次/非序列号）。
// 采购退货出库前置校验需要该 reader，缺则 RETURNS_READER_MISSING（夹具装配缺口）。
type integrationSKUFlags struct{}

func (integrationSKUFlags) GetFlags(_ context.Context, _ int64) (SKUFlags, error) {
	return SKUFlags{Enabled: true}, nil
}

type integrationSOReader struct{ db *gorm.DB }

func (r integrationSOReader) FindReturnable(_ context.Context, soNo string) (int64, int64, []ReturnableLine, bool, error) {
	var soID, wh int64
	err := r.db.Raw(`SELECT id, warehouse_id FROM sales_orders WHERE so_no = ? AND status IN ('PARTIAL_SHIPPED','SHIPPED_ALL','COMPLETED')`, soNo).
		Row().Scan(&soID, &wh)
	if err != nil {
		return 0, 0, nil, false, nil // 未命中 → found=false（业务关系校验失败语义）
	}
	var rows []struct {
		LineNo     int64  `gorm:"column:line_no"`
		// SKUID 必须显式 tag：GORM 命名策略把 SKUID 推导成 sk_uid（≠表列 sku_id），
		// 扫描静默落 0 → 来源单明细 sku 全 0 → 退货创建报 RETURNS_LINE_NOT_FOUND
		// （2026-10-08 实测定位；与生产侧 stockops/sales 同类缺陷）。
		SKUID      int64  `gorm:"column:sku_id"`
		QtyShipped string `gorm:"column:qty_shipped"`
	}
	if err := r.db.Raw(`SELECT line_no, sku_id, qty_shipped FROM sales_order_items WHERE so_id = ? AND qty_shipped > 0`, soID).
		Scan(&rows).Error; err != nil {
		return 0, 0, nil, false, err
	}
	lines := make([]ReturnableLine, 0, len(rows))
	for _, l := range rows {
		lines = append(lines, ReturnableLine{LineNo: l.LineNo, SKUID: l.SKUID, Qty: wholeUnits(l.QtyShipped)})
	}
	return soID, wh, lines, true, nil
}

type integrationPOReader struct{ db *gorm.DB }

func (r integrationPOReader) FindReturnable(_ context.Context, poNo string) (int64, int64, []ReturnableLine, bool, error) {
	var poID, wh int64
	err := r.db.Raw(`SELECT id, warehouse_id FROM purchase_orders WHERE po_no = ? AND status IN ('PARTIAL_RECEIVED','RECEIVED_ALL','COMPLETED')`, poNo).
		Row().Scan(&poID, &wh)
	if err != nil {
		return 0, 0, nil, false, nil
	}
	var rows []struct {
		LineNo      int64  `gorm:"column:line_no"`
		SKUID       int64  `gorm:"column:sku_id"` // 显式 tag，理由同上（否则 sku 恒 0）
		QtyReceived string `gorm:"column:qty_received"`
	}
	if err := r.db.Raw(`SELECT line_no, sku_id, qty_received FROM purchase_order_items WHERE po_id = ? AND qty_received > 0`, poID).
		Scan(&rows).Error; err != nil {
		return 0, 0, nil, false, err
	}
	lines := make([]ReturnableLine, 0, len(rows))
	for _, l := range rows {
		lines = append(lines, ReturnableLine{LineNo: l.LineNo, SKUID: l.SKUID, Qty: wholeUnits(l.QtyReceived)})
	}
	return poID, wh, lines, true, nil
}

// wholeUnits numeric(18,4) 文本 → 整数件（集成夹具全部用整数件）。
func wholeUnits(q string) int64 {
	v, err := stock.ParseQty(q)
	if err != nil {
		return 0
	}
	return int64(v) / 10000
}

type integrationSKUChecker struct{ db *gorm.DB }

func (c integrationSKUChecker) ExistsActive(_ context.Context, skuID int64) (bool, error) {
	var n int64
	err := c.db.Raw(`SELECT COUNT(*) FROM skus WHERE id = ? AND is_enabled = true`, skuID).Row().Scan(&n)
	return n > 0, err
}

type integrationBinChecker struct{ db *gorm.DB }

func (c integrationBinChecker) ExistsActive(_ context.Context, warehouseID, binID int64) (bool, error) {
	var n int64
	err := c.db.Raw(`SELECT COUNT(*) FROM bins WHERE id = ? AND warehouse_id = ? AND status = 'ENABLED' AND deleted_at IS NULL`, binID, warehouseID).Row().Scan(&n)
	return n > 0, err
}

// ledgerReaderBridge / stockStateBridge：inventory.Service 只读查询 → 本域追溯契约
// （router 装配同款闭包桥接形态，plan §3.1/§8.3 条 5）。
type ledgerReaderBridge struct{ svc *inventory.Service }

func (b ledgerReaderBridge) LedgersForTrace(ctx context.Context, skuID int64, serialNo string, warehouseID int64, limit int) ([]TraceLedger, error) {
	q := inventory.LedgerQuery{SKUID: skuID, SerialNo: serialNo, Page: 1, PageSize: limit, Scope: inventory.Scope{AllWarehouses: true}}
	if warehouseID > 0 {
		q.WarehouseID = warehouseID
	}
	rows, _, err := b.svc.QueryLedgers(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]TraceLedger, 0, len(rows))
	for _, l := range rows {
		out = append(out, TraceLedger{
			ID: l.ID.Int64(), LedgerNo: l.LedgerNo, SKUID: l.SKUID, WarehouseID: l.WarehouseID,
			BinID: l.BinID, BatchID: l.BatchID, SerialNo: l.SerialNo,
			ChangeType: l.ChangeType, BusinessType: l.BusinessType, BusinessNo: l.BusinessNo,
			StatusFrom: l.StatusFrom, StatusTo: l.StatusTo,
			QtyBefore: l.QtyBefore.String(), QtyChange: l.QtyChange.String(), QtyAfter: l.QtyAfter.String(),
			OperatorName: l.OperatorName, RequestID: l.RequestID, Remark: l.Remark,
			CreatedAt: l.CreatedAt.Time,
		})
	}
	return out, nil
}

type stockStateBridge struct{ svc *inventory.Service }

func (b stockStateBridge) StockRowsBySKU(ctx context.Context, skuID, warehouseID int64, limit int) ([]TraceStockRow, error) {
	q := inventory.InventoryQuery{SKUID: skuID, Page: 1, PageSize: limit, Scope: inventory.Scope{AllWarehouses: warehouseID == 0}}
	if warehouseID > 0 {
		q.WarehouseID = warehouseID
	}
	rows, _, err := b.svc.QueryInventory(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]TraceStockRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, TraceStockRow{
			WarehouseID: r.WarehouseID, ZoneID: r.ZoneID, ShelfID: r.ShelfID, BinID: r.BinID,
			SKUID: r.SKUID, BatchID: r.BatchID,
			Total: r.TotalQty.String(), Available: r.AvailableQty.String(), Locked: r.LockedQty.String(),
			Frozen: r.FrozenQty.String(), PendingInspect: r.PendingInspectQty.String(), Defective: r.DefectiveQty.String(),
			UpdatedAt: r.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (b stockStateBridge) SerialByNo(ctx context.Context, serialNo string) (TraceSerialRow, bool, error) {
	rows, _, err := b.svc.QuerySerials(ctx, inventory.SerialQuery{SerialNo: serialNo, Page: 1, PageSize: 1, Scope: inventory.Scope{AllWarehouses: true}})
	if err != nil {
		return TraceSerialRow{}, false, err
	}
	if len(rows) == 0 {
		return TraceSerialRow{}, false, nil
	}
	r := rows[0]
	return TraceSerialRow{
		SerialNo: r.SerialNo, SKUID: r.SKUID, BatchID: r.BatchID,
		WarehouseID: r.WarehouseID, BinID: r.BinID, Status: r.Status,
		LastSourceType: r.LastSourceType, LastSourceNo: r.LastSourceNo, LastEventAt: r.LastEventAt.Time,
	}, true, nil
}

// ---- 组装与夹具 ----

type integrationFixture struct {
	t    *testing.T
	db   *gorm.DB
	repo Repository
	gw   StockGateway
	svc  *Service
	qc   *integrationQCCreator
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	db := integrationDB(t)
	repo := NewGormRepository(db)
	// 真实 inventory.Service + stock 桥接（别名收编前形态，见 inventoryStockBridge 注释）。
	inv := inventory.NewService(db, nil,
		inventory.WithSKUChecker(integrationSKUChecker{db: db}),
		inventory.WithBinChecker(integrationBinChecker{db: db}))
	gw := inventoryStockBridge{svc: inv}
	qc := &integrationQCCreator{t: t}
	return &integrationFixture{
		t: t, db: db, repo: repo, gw: gw, qc: qc,
		svc: NewService(repo,
			WithStock(gw),
			WithSalesOrders(integrationSOReader{db: db}),
			WithPurchaseOrders(integrationPOReader{db: db}),
			WithQCCreator(qc),
			WithSKUFlags(integrationSKUFlags{}),
			WithLedgers(ledgerReaderBridge{svc: inv}),
			WithStockState(stockStateBridge{svc: inv}),
		),
	}
}

const (
	intSkuID = 990001
)

// seedMaster 种子基础资料（products/skus，迁移 000003）。
func (f *integrationFixture) seedMaster() {
	f.t.Helper()
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO products (id, code, name, status, created_at, updated_at, created_by, updated_by)
		VALUES (990000, 'P-INT', '集成测试品', 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`).Error)
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO skus (id, code, product_id, is_enabled, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'SKU-INT', 990000, true, now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`, intSkuID).Error)
}

// itRunID/itBin/itNo 本轮唯一派生：同库重复运行不累加库存、不撞种子单号
// （returns 集成用例按固定 (wh,bin,sku) 读绝对库存量，固定库位会跨轮次累加失真）。
var itRunID = int64(time.Now().UnixNano()/int64(time.Millisecond)%1_000_000)*1000 + int64(os.Getpid()%1000)

// itSeq 进程内自增：同一轮运行内多次派生也互不相同（不同用例可能用同一 base）。
var itSeq atomic.Int64

func itBin(base int64) int64 { return base*1_000_000 + itRunID%1000*1000 + itSeq.Add(1)%1000 }

func itNo(base string) string {
	return fmt.Sprintf("%s-%d-%d", base, itRunID, itSeq.Add(1))
}

// seedBin 种子仓库/库位（迁移 000004；唯一编码冲突容忍——多次运行复用）。
func (f *integrationFixture) seedBin(wh, zone, shelf, bin int64) {
	f.t.Helper()
	f.seedMaster()
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO warehouses (id, code, name, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, '集成测试仓', 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`, wh, fmt.Sprintf("WH-INT-%d", wh)).Error)
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO zones (id, warehouse_id, code, name, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, '集成区', 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`, zone, wh, fmt.Sprintf("Z-INT-%d", zone)).Error)
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO shelves (id, warehouse_id, zone_id, code, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`, shelf, wh, zone, fmt.Sprintf("S-INT-%d", shelf)).Error)
	require.NoError(f.t, f.db.Exec(`
		INSERT INTO bins (id, warehouse_id, zone_id, shelf_id, code, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (id) DO NOTHING`, bin, wh, zone, shelf, fmt.Sprintf("B-INT-%d", bin)).Error)
}

// seedSalesOrder 种子销售单（已全部发货；迁移 000008）。
func (f *integrationFixture) seedSalesOrder(no string, wh int64, lines ...[3]int64) {
	f.t.Helper()
	var soID int64
	require.NoError(f.t, f.db.Raw(`
		INSERT INTO sales_orders (so_no, customer_id, warehouse_id, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, 7, ?, 'SHIPPED_ALL', now(), now(), 0, 0) RETURNING id`, no, wh).Row().Scan(&soID))
	for _, l := range lines {
		require.NoError(f.t, f.db.Exec(`
			INSERT INTO sales_order_items (so_id, line_no, sku_id, qty, qty_allocated, qty_shipped, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, now(), now(), 0, 0)`,
			soID, l[0], l[1], qtyText(l[2]), qtyText(l[2]), qtyText(l[2])).Error)
	}
}

// seedPurchaseOrder 种子采购单（已收齐；迁移 000007）。
func (f *integrationFixture) seedPurchaseOrder(no string, wh int64, lines ...[3]int64) {
	f.t.Helper()
	var poID int64
	require.NoError(f.t, f.db.Raw(`
		INSERT INTO purchase_orders (po_no, supplier_id, warehouse_id, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, 9, ?, 'RECEIVED_ALL', now(), now(), 0, 0) RETURNING id`, no, wh).Row().Scan(&poID))
	for _, l := range lines {
		require.NoError(f.t, f.db.Exec(`
			INSERT INTO purchase_order_items (po_id, line_no, sku_id, qty_ordered, qty_received, qty_rejected, qty_putaway, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, 0, ?, now(), now(), 0, 0)`,
			poID, l[0], l[1], qtyText(l[2]), qtyText(l[2]), qtyText(l[2])).Error)
	}
}

func (f *integrationFixture) seedStock(wh, zone, shelf, bin, sku int64, qty string) {
	f.t.Helper()
	_, err := f.gw.Putaway(context.Background(), nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku},
		Qty: mustParseQty(f.t, qty), Source: stock.Source{Type: "seed", No: "seed"},
		Actor: stock.Actor{ID: 0},
	})
	require.NoError(f.t, err)
}

func mustParseQty(t *testing.T, s string) stock.Qty {
	t.Helper()
	q, err := stock.ParseQty(s)
	require.NoError(t, err)
	return q
}

// ---- 用例 ----

// TestIntegrationSalesReturnReceiveQCFullChain 销售退货全链：收货入待检 → 质检
// 合格回 available / 不良入 defective；幂等重放不重复累计；超量收货拒绝（真实回滚）。
func TestIntegrationSalesReturnReceiveQCFullChain(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	const wh, zone, shelf, sku = 901, 9011, 9012, intSkuID
	bin := itBin(9013)
	soNo := itNo("SO-INT")
	f.seedBin(wh, zone, shelf, bin)
	f.seedSalesOrder(soNo, wh, [3]int64{1, sku, 10})

	actor1 := Actor{UserID: 1, Username: "tester", RequestID: "int-1"}
	order, err := f.svc.CreateSalesReturn(ctx, actor1,
		SalesReturnCreateInput{SONo: soNo, CustomerID: 7, WarehouseID: wh,
			Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(10), Reason: "质量问题"}}})
	require.NoError(t, err)
	_, err = f.svc.SubmitReturn(ctx, actor1, order.ID.Int64())
	require.NoError(t, err)
	_, err = f.svc.ApproveReturn(ctx, Actor{UserID: 2}, order.ID.Int64(), ApproveInput{Approved: true})
	require.NoError(t, err)

	rcv := ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, Qty: qtyText(10)}}}
	_, err = f.svc.ReceiveSalesReturn(ctx, Actor{UserID: 3, RequestID: "int-rcv"}, order.ID.Int64(), rcv)
	require.NoError(t, err)

	// 幂等重放：同请求重试（不同 request_id，同幂等键），库存与累计不变
	_, err = f.svc.ReceiveSalesReturn(ctx, Actor{UserID: 3, RequestID: "int-rcv-retry"}, order.ID.Int64(), rcv)
	require.NoError(t, err)
	view, err := f.svc.GetReturn(ctx, order.ID.Int64())
	require.NoError(t, err)
	require.Equal(t, qtyText(10), view.Items[0].QtyReceived, "幂等重放不得重复累计")

	// 已收满再收 → 数量守卫拒绝（真实 SQL 守卫 + 事务回滚，库存无残留）
	_, err = f.svc.ReceiveSalesReturn(ctx, Actor{UserID: 3}, order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, Qty: qtyText(2)}}})
	var re *response.Error
	require.ErrorAs(t, err, &re)
	require.True(t, len(re.Error()) >= len("RETURNS_QTY_EXCEEDED") && re.Error()[:len("RETURNS_QTY_EXCEEDED")] == "RETURNS_QTY_EXCEEDED",
		"期望 RETURNS_QTY_EXCEEDED，得到 %v", err)

	// 质检：7 合格 3 不良（§9.1 质检决定去向）
	_, qcNo, err := f.svc.SubmitSalesQC(ctx, Actor{UserID: 3}, order.ID.Int64(), SubmitQCInput{})
	require.NoError(t, err)
	_, err = f.svc.ApplySalesQCResult(ctx, Actor{UserID: 3}, order.ID.Int64(), QCResultInput{
		QCNo:  qcNo,
		Lines: []QCResultLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, QtyQualified: qtyText(7), QtyDefective: qtyText(3)}}})
	require.NoError(t, err)

	var total, avail, defect string
	require.NoError(t, f.db.Raw(`SELECT total_qty, available_qty, defective_qty FROM inventory WHERE warehouse_id=? AND bin_id=? AND sku_id=?`, wh, bin, sku).
		Row().Scan(&total, &avail, &defect))
	require.Equal(t, qtyText(10), total)
	require.Equal(t, qtyText(7), avail)
	require.Equal(t, qtyText(3), defect)

	// 追溯链：收货 INBOUND（return_order）→ INSPECT_PASS / INSPECT_DEFECTIVE
	res, err := f.svc.Trace(ctx, TraceQuery{SKUID: sku}, TraceScope{AllWarehouses: true})
	require.NoError(t, err)
	changeTypes := map[string]bool{}
	for _, l := range res.Chain {
		changeTypes[l.ChangeType] = true
	}
	require.True(t, changeTypes["INBOUND"], "追溯链应含收货 INBOUND")
	require.True(t, changeTypes["INSPECT_PASS"] && changeTypes["INSPECT_DEFECTIVE"], "追溯链应含质检两段")
}

// TestIntegrationPurchaseReturnShipLockDeduct 采购退货出库：真实 Lock→Deduct + 幂等重放。
func TestIntegrationPurchaseReturnShipLockDeduct(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	const wh, zone, shelf, sku = 902, 9021, 9022, intSkuID
	bin := itBin(9023)
	poNo := itNo("PO-INT")
	f.seedBin(wh, zone, shelf, bin)
	f.seedStock(wh, zone, shelf, bin, sku, qtyText(5))
	f.seedPurchaseOrder(poNo, wh, [3]int64{1, sku, 5})

	order, err := f.svc.CreatePurchaseReturn(ctx, Actor{UserID: 1}, PurchaseReturnCreateInput{
		PONo: poNo, SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(5), Reason: "来料不良"}}})
	require.NoError(t, err)
	_, err = f.svc.SubmitReturn(ctx, Actor{UserID: 1}, order.ID.Int64())
	require.NoError(t, err)
	_, err = f.svc.ApproveReturn(ctx, Actor{UserID: 2}, order.ID.Int64(), ApproveInput{Approved: true})
	require.NoError(t, err)

	shipIn := ShipInput{Lines: []ShipLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, Qty: qtyText(5)}}}
	_, err = f.svc.ShipPurchaseReturn(ctx, Actor{UserID: 3, RequestID: "int-ship"}, order.ID.Int64(), shipIn)
	require.NoError(t, err)
	var total, locked string
	require.NoError(t, f.db.Raw(`SELECT total_qty, locked_qty FROM inventory WHERE warehouse_id=? AND bin_id=? AND sku_id=?`, wh, bin, sku).
		Row().Scan(&total, &locked))
	require.Equal(t, "0.0000", total)
	require.Equal(t, "0.0000", locked)

	// 整单重试：幂等重放，不再扣减
	_, err = f.svc.ShipPurchaseReturn(ctx, Actor{UserID: 3, RequestID: "int-ship-retry"}, order.ID.Int64(), shipIn)
	require.NoError(t, err)
	require.NoError(t, f.db.Raw(`SELECT total_qty FROM inventory WHERE warehouse_id=? AND bin_id=? AND sku_id=?`, wh, bin, sku).
		Row().Scan(&total))
	require.Equal(t, "0.0000", total)
}

// TestIntegrationExceptionFreezeRelease 异常冻结/解冻：真实 EXCEPTION_FREEZE 锁与 RELEASE 流水。
func TestIntegrationExceptionFreezeRelease(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	const wh, zone, shelf, sku = 903, 9031, 9032, intSkuID
	bin := itBin(9033)
	srcNo := itNo("MANUAL-INT") // 本轮唯一：按来源单号过滤的断言不被上一轮异常单污染
	f.seedBin(wh, zone, shelf, bin)
	f.seedStock(wh, zone, shelf, bin, sku, qtyText(20))

	_, err := f.svc.CreateException(ctx, nil, CreateExceptionOp{
		Type: "库存异常", SourceType: "manual", SourceNo: srcNo, Detail: "差异",
		SKUID: sku, BinID: bin, Freeze: true, FreezeWarehouseID: wh, FreezeQty: qtyText(5),
		Actor: Actor{UserID: 1},
	})
	require.NoError(t, err)
	var frozen string
	require.NoError(t, f.db.Raw(`SELECT frozen_qty FROM inventory WHERE warehouse_id=? AND bin_id=? AND sku_id=?`, wh, bin, sku).
		Row().Scan(&frozen))
	require.Equal(t, qtyText(5), frozen)

	exs, _, err := f.svc.ListExceptions(ctx, ExceptionFilter{SourceNo: srcNo, Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, exs, 1)
	id := exs[0].IDInt.Int64()
	for _, step := range []func() error{
		func() error {
			_, err := f.svc.AssignException(ctx, Actor{UserID: 2}, id, ExceptionAssignInput{AssigneeID: 5, AssigneeName: "王五"})
			return err
		},
		func() error {
			_, err := f.svc.StartException(ctx, Actor{UserID: 2}, id, ExceptionNoteInput{})
			return err
		},
		func() error {
			_, err := f.svc.ReviewException(ctx, Actor{UserID: 2}, id, ExceptionNoteInput{})
			return err
		},
	} {
		require.NoError(t, step())
	}
	_, err = f.svc.ResolveException(ctx, Actor{UserID: 2}, id, ExceptionNoteInput{Note: "ok"})
	require.NoError(t, err)
	require.NoError(t, f.db.Raw(`SELECT frozen_qty FROM inventory WHERE warehouse_id=? AND bin_id=? AND sku_id=?`, wh, bin, sku).
		Row().Scan(&frozen))
	require.Equal(t, "0.0000", frozen, "RESOLVED 必须释放异常冻结（inventory-rules §4.2）")
}

// TestIntegrationConcurrentReceiveNoOverReceive 并发收货不超量（plan §11.3）：
// 6 路并发各收 3（每路不同幂等键），qty_return=10 → 恰 3 路成功、其余因数量守卫被拒，
// 库存=9=累计收货（恒等式由 CHECK 兜底）。
func TestIntegrationConcurrentReceiveNoOverReceive(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	const wh, zone, shelf, sku = 904, 9041, 9042, intSkuID
	bin := itBin(9043)
	soNo := itNo("SO-INT")
	f.seedBin(wh, zone, shelf, bin)
	f.seedSalesOrder(soNo, wh, [3]int64{1, sku, 10})
	order, err := f.svc.CreateSalesReturn(ctx, Actor{UserID: 1},
		SalesReturnCreateInput{SONo: soNo, CustomerID: 7, WarehouseID: wh,
			Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(10), Reason: "x"}}})
	require.NoError(t, err)
	_, err = f.svc.SubmitReturn(ctx, Actor{UserID: 1}, order.ID.Int64())
	require.NoError(t, err)
	_, err = f.svc.ApproveReturn(ctx, Actor{UserID: 2}, order.ID.Int64(), ApproveInput{Approved: true})
	require.NoError(t, err)

	var wg sync.WaitGroup
	okc := make(chan int, 8)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// 每路显式给不同幂等键：无键时服务按「单号+行+库位+sku+批次+数量」确定性
			// 合成行级键，6 路同载荷会退化为「1 路真实变更 + 5 路幂等重放」（重放不报错、
			// 不落账），并发数量守卫根本不会被触发——那不是本用例要验的东西。
			_, err := f.svc.ReceiveSalesReturn(ctx, Actor{UserID: int64(n + 10), RequestID: fmt.Sprintf("int-cc-%d", n)},
				order.ID.Int64(), ReceiveInput{
					IdempotencyKey: itNo(fmt.Sprintf("cc-%d", n)),
					Lines:          []ReceiveLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, Qty: qtyText(3)}}})
			if err == nil {
				okc <- 1
			}
		}(i)
	}
	wg.Wait()
	close(okc)
	success := 0
	for range okc {
		success++
	}
	require.Equal(t, 3, success, "并发收货应由行锁+数量守卫裁决为恰 3 路成功（10//3）")
	var received, total string
	require.NoError(t, f.db.Raw(`
		SELECT i.qty_received, inv.total_qty FROM return_items i, inventory inv
		WHERE i.return_id = ? AND inv.warehouse_id=? AND inv.bin_id=? AND inv.sku_id=?`,
		order.ID.Int64(), wh, bin, sku).Row().Scan(&received, &total))
	require.Equal(t, qtyText(9), received)
	require.Equal(t, qtyText(9), total)
}
