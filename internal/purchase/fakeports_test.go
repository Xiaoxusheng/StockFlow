package purchase

// 测试替身：跨域消费接口内存实现（StockGateway / 校验器 / 推荐库位 / 异常中心）。
// fakeStock 记录全部原语调用并按 inventory 幂等语义建模（InspectResult 幂等键重放、
// EnsureBatch 唯一键天然幂等），供测试断言"库存动作恰好发生一次、键构成正确"。

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/stock"
)

// fakeStock 库存原语替身（plan §3 StockGateway 消费接口）。
type fakeStock struct {
	mu sync.Mutex

	putaways  []stock.PutawayOp
	inspects  []stock.InspectResultOp
	batches   []stock.BatchOp
	serials   []stock.SerialOp
	batchIDs  map[string]int64 // "sku:batch" → batchID
	serialIDs map[string]int64 // serial_no → serialID
	inspKeys  map[string]bool  // 已消费的 InspectResult 幂等键
	nextID    int64
}

func newFakeStock() *fakeStock {
	return &fakeStock{
		batchIDs:  map[string]int64{},
		serialIDs: map[string]int64{},
		inspKeys:  map[string]bool{},
	}
}

func (f *fakeStock) Putaway(_ context.Context, _ *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putaways = append(f.putaways, op)
	f.nextID++
	return stock.MutationResult{Ledger: stock.LedgerRef{ID: f.nextID, LedgerNo: fmt.Sprintf("LED-%06d", f.nextID)}}, nil
}

// InspectResult 幂等键重放建模：同键第二次调用返回 Replay=true，不重复记录。
func (f *fakeStock) InspectResult(_ context.Context, _ *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if op.IdempotencyKey != "" {
		if f.inspKeys[op.IdempotencyKey] {
			return stock.MutationResult{Replay: true, Ledger: stock.LedgerRef{ID: 1, LedgerNo: "LED-REPLAY"}}, nil
		}
		f.inspKeys[op.IdempotencyKey] = true
	}
	f.inspects = append(f.inspects, op)
	f.nextID++
	return stock.MutationResult{Ledger: stock.LedgerRef{ID: f.nextID, LedgerNo: fmt.Sprintf("LED-%06d", f.nextID)}}, nil
}

// EnsureBatch 唯一键 sku+batch 天然幂等（inventory.Service 同语义）。
func (f *fakeStock) EnsureBatch(_ context.Context, _ *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%d:%s", op.SKUID, op.BatchNo)
	if id, ok := f.batchIDs[key]; ok {
		return id, false, nil
	}
	f.nextID++
	f.batchIDs[key] = f.nextID
	f.batches = append(f.batches, op)
	return f.nextID, true, nil
}

func (f *fakeStock) SerialEvent(_ context.Context, _ *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serials = append(f.serials, op)
	if id, ok := f.serialIDs[op.SerialNo]; ok {
		return id, false, nil
	}
	f.nextID++
	f.serialIDs[op.SerialNo] = f.nextID
	return f.nextID, true, nil
}

func (f *fakeStock) inspectOpsByKey(prefix string) []stock.InspectResultOp {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []stock.InspectResultOp
	for _, op := range f.inspects {
		if strings.HasPrefix(op.IdempotencyKey, prefix) {
			out = append(out, op)
		}
	}
	return out
}

// fakeCheckers 供应商/仓库/SKU 校验替身。
type fakeCheckers struct {
	mu         sync.Mutex
	suppliers  map[int64]bool
	warehouses map[int64]bool
	skus       map[int64]SKUFlags
	bins       map[string]bool // "wh:bin"
}

func newFakeCheckers() *fakeCheckers {
	return &fakeCheckers{
		suppliers:  map[int64]bool{11: true},
		warehouses: map[int64]bool{1: true, 2: true},
		skus:       map[int64]SKUFlags{},
		bins:       map[string]bool{"1:101": true, "1:102": true, "2:201": true},
	}
}

func (f *fakeCheckers) seedSKU(id int64, enabled, batch, expiry, serial bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skus[id] = SKUFlags{Enabled: enabled, BatchManaged: batch, ExpiryManaged: expiry, SerialManaged: serial}
}

func (f *fakeCheckers) ExistsActiveSupplier(_ context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suppliers[id], nil
}

func (f *fakeCheckers) ExistsActiveWarehouse(_ context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.warehouses[id], nil
}

func (f *fakeCheckers) GetFlags(_ context.Context, id int64) (SKUFlags, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.skus[id]
	return fl, ok, nil
}

func (f *fakeCheckers) ExistsActiveBin(_ context.Context, whID, binID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bins[fmt.Sprintf("%d:%d", whID, binID)], nil
}

// existsActiveSKU / existsActiveBin 集成构建的库存域校验器适配（inventory.SKUChecker/
// inventory.BinChecker 签名，integration_test.go 专用）。
func (f *fakeCheckers) existsActiveSKU(_ context.Context, skuID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skus[skuID].Enabled, nil
}

func (f *fakeCheckers) existsActiveBin(_ context.Context, whID, binID int64) (bool, error) {
	return f.ExistsActiveBin(context.Background(), whID, binID)
}

// 适配方法集：把同一替身拆成各消费接口（结构化满足）。
type supplierCheckerAdapter struct{ c *fakeCheckers }

func (a supplierCheckerAdapter) ExistsActive(ctx context.Context, id int64) (bool, error) {
	return a.c.ExistsActiveSupplier(ctx, id)
}

type warehouseCheckerAdapter struct{ c *fakeCheckers }

func (a warehouseCheckerAdapter) ExistsActive(ctx context.Context, id int64) (bool, error) {
	return a.c.ExistsActiveWarehouse(ctx, id)
}

type skuAttrAdapter struct{ c *fakeCheckers }

func (a skuAttrAdapter) GetFlags(ctx context.Context, id int64) (SKUFlags, bool, error) {
	return a.c.GetFlags(ctx, id)
}

type binCheckerAdapter struct{ c *fakeCheckers }

func (a binCheckerAdapter) ExistsActive(ctx context.Context, whID, binID int64) (bool, error) {
	return a.c.ExistsActiveBin(ctx, whID, binID)
}

// fakeRecommender 推荐库位替身（同 SKU 集中+容量基础规则的测试桩）。
type fakeRecommender struct {
	mu    sync.Mutex
	calls []string // "wh:sku:qty" 记录
	byWH  map[int64][]BinSuggestion
}

func newFakeRecommender() *fakeRecommender {
	return &fakeRecommender{
		byWH: map[int64][]BinSuggestion{
			1: {{BinID: 101, ZoneID: 11, ShelfID: 21, Score: 0.9, Reason: "同 SKU 集中"}},
		},
	}
}

func (f *fakeRecommender) Recommend(_ context.Context, whID, skuID int64, qty float64) ([]BinSuggestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%d:%d:%g", whID, skuID, qty))
	return f.byWH[whID], nil
}

// exceptionCall 异常中心调用记录（结构化，供断言）。
type exceptionCall struct {
	ExcType, SourceType, SourceNo, Detail string
}

// fakeExceptions 异常中心替身（记录调用，返回 EX-TEST-0001 式单号）。
type fakeExceptions struct {
	mu    sync.Mutex
	calls []exceptionCall
}

func (f *fakeExceptions) Create(_ context.Context, _ *gorm.DB, excType, sourceType, sourceNo, detail string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, exceptionCall{ExcType: excType, SourceType: sourceType, SourceNo: sourceNo, Detail: detail})
	return fmt.Sprintf("EX-TEST-%04d", len(f.calls)), nil
}
