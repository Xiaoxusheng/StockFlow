//go:build integration

// 真实 PostgreSQL 依赖的采购入库链路集成测试（plan §11.2 场景 1 / §11.3 并发 / §11.4 幂等
// 的本域子集；默认 go test 不编译本文件——单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/purchase/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip；Redis 非本域正确性依赖）。
//
// 装配说明：库存原语经 real inventory.Service 承载（§3 契约包 stock 值类型 → inventory
// 值类型逐字段桥接——生产环境的桥接位于 router 装配，集成工程师 MT5 交付；本适配器仅在
// integration 构建标签内编译，域包非测试代码不出现对 internal/inventory 的引用，
// backend-m2-plan §2.3 判据 2）。SKU/库位存在性校验复用本包测试替身（fakeports_test.go，
// 与 internal/inventory/integration_test.go 的 fakeSKUChecker/fakeBinChecker 同款口径——
// 集成构建无需 masterdata/warehouse 数据）。
package purchase

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/stock"
)

// integrationEnv 真库环境：连接 + 迁移到最新（inventory/integration_test.go 同款）。
func integrationEnv(t *testing.T) (*gorm.DB, *Service) {
	t.Helper()
	if os.Getenv("SF_TEST_PG_NAME") == "" {
		t.Skip("未设置 SF_TEST_PG_* 环境变量，跳过集成测试")
	}
	port, err := strconv.Atoi(envOr("SF_TEST_PG_PORT", "5432"))
	require.NoError(t, err)
	dbCfg := config.DatabaseConfig{
		Host: envOr("SF_TEST_PG_HOST", "127.0.0.1"), Port: port,
		User: envOr("SF_TEST_PG_USER", "postgres"), Password: os.Getenv("SF_TEST_PG_PASSWORD"),
		Name: os.Getenv("SF_TEST_PG_NAME"), SSLMode: "disable",
		MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 10 * time.Minute,
	}
	db, err := database.Connect(dbCfg)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	require.NoError(t, database.MigrateUp(dbCfg.DSN(), repoRootMigrations(t)))

	repo := NewRepository(db)
	chk := newFakeCheckers()
	chk.seedSKU(100, true, false, false, false)
	chk.seedSKU(101, true, true, true, false)

	// 库存原语网关：real inventory.Service + stock→inventory 桥接（见文件头说明）。
	inv := inventory.NewService(db, nil,
		inventory.WithSKUChecker(invSKUChecker{chk}),
		inventory.WithBinChecker(invBinChecker{chk}),
	)

	svc := NewService(repo,
		WithStock(&inventoryGateway{svc: inv}),
		WithSupplierChecker(supplierCheckerAdapter{chk}),
		WithWarehouseChecker(warehouseCheckerAdapter{chk}),
		WithSKUAttrReader(skuAttrAdapter{chk}),
		WithBinChecker(binCheckerAdapter{chk}),
		WithBinRecommender(newFakeRecommender()),
	)
	return db, svc
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func repoRootMigrations(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for range 8 {
		cand := filepath.Join(dir, "db", "migrations")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("未找到 db/migrations（须在仓库内运行）")
	return ""
}

// ---- 校验器适配（结构化满足 inventory.SKUChecker/BinChecker）----

type invSKUChecker struct{ c *fakeCheckers }

func (a invSKUChecker) ExistsActive(ctx context.Context, skuID int64) (bool, error) {
	return a.c.existsActiveSKU(ctx, skuID)
}

type invBinChecker struct{ c *fakeCheckers }

func (a invBinChecker) ExistsActive(ctx context.Context, whID, binID int64) (bool, error) {
	return a.c.existsActiveBin(ctx, whID, binID)
}

// ---- StockGateway 桥接（stock 值类型 → inventory 值类型，逐字段转换；
// inventory 侧 aliases.go 落地后即为恒等类型，可直接传参）----

type inventoryGateway struct{ svc *inventory.Service }

func (g *inventoryGateway) Putaway(ctx context.Context, tx *gorm.DB, op stock.PutawayOp) (stock.MutationResult, error) {
	res, err := g.svc.Putaway(ctx, tx, inventory.PutawayOp{
		Key:            inventory.RowKey(op.Key),
		Qty:            inventory.Qty(op.Qty),
		RequireInspect: op.RequireInspect,
		Source:         inventory.Source(op.Source),
		Actor:          inventory.Actor(op.Actor),
		IdempotencyKey: op.IdempotencyKey,
		Remark:         op.Remark,
	})
	return mutationOf(res), err
}

func (g *inventoryGateway) InspectResult(ctx context.Context, tx *gorm.DB, op stock.InspectResultOp) (stock.MutationResult, error) {
	res, err := g.svc.InspectResult(ctx, tx, inventory.InspectResultOp{
		Key:            inventory.RowKey(op.Key),
		Qty:            inventory.Qty(op.Qty),
		Pass:           op.Pass,
		Source:         inventory.Source(op.Source),
		Actor:          inventory.Actor(op.Actor),
		IdempotencyKey: op.IdempotencyKey,
		Remark:         op.Remark,
	})
	return mutationOf(res), err
}

func (g *inventoryGateway) EnsureBatch(ctx context.Context, tx *gorm.DB, op stock.BatchOp) (int64, bool, error) {
	return g.svc.EnsureBatch(ctx, tx, inventory.BatchOp{
		SKUID:          op.SKUID,
		BatchNo:        op.BatchNo,
		SupplierID:     op.SupplierID,
		ProductionDate: op.ProductionDate,
		InboundDate:    op.InboundDate,
		ExpiryDate:     op.ExpiryDate,
		CostPrice:      inventory.Qty(op.CostPrice),
		Actor:          inventory.Actor(op.Actor),
		Remark:         op.Remark,
	})
}

func (g *inventoryGateway) SerialEvent(ctx context.Context, tx *gorm.DB, op stock.SerialOp) (int64, bool, error) {
	return g.svc.SerialEvent(ctx, tx, inventory.SerialOp{
		SerialNo:       op.SerialNo,
		SKUID:          op.SKUID,
		BatchID:        op.BatchID,
		WarehouseID:    op.WarehouseID,
		BinID:          op.BinID,
		Status:         op.Status,
		Source:         inventory.Source(op.Source),
		Actor:          inventory.Actor(op.Actor),
		IdempotencyKey: op.IdempotencyKey,
		Remark:         op.Remark,
	})
}

func mutationOf(res inventory.MutationResult) stock.MutationResult {
	return stock.MutationResult{
		Replay: res.Replay,
		Ledger: stock.LedgerRef{ID: res.Ledger.ID, LedgerNo: res.Ledger.LedgerNo},
		LockID: res.LockID,
	}
}

// ---- 测试 ----

// chainEnv 已审核采购订单 + 入库单（真实落库）。
type chainEnv struct {
	svc *Service
	po  *PurchaseOrder
	in  *InboundOrder
}

func newChain(t *testing.T, svc *Service, skuID int64, poQty string) *chainEnv {
	t.Helper()
	ctx := context.Background()
	actor := testActor()
	po, err := svc.CreatePO(ctx, actor, POCreateInput{
		SupplierID: 11, WarehouseID: 1,
		Items: []POItemInput{{SKUID: skuID, Qty: mustQty(t, poQty)}},
	})
	require.NoError(t, err)
	_, err = svc.SubmitPO(ctx, actor, po.ID.Int64())
	require.NoError(t, err)
	_, err = svc.ApprovePO(ctx, actor, po.ID.Int64(), POApproveInput{Approved: true})
	require.NoError(t, err)
	in, err := svc.CreateInbound(ctx, actor, InboundCreateInput{
		SourceType: SourceTypePurchase, SourceNo: po.PONo, WarehouseID: 1,
		Items: []InboundItemInput{{SKUID: skuID, Qty: mustQty(t, poQty)}},
	})
	require.NoError(t, err)
	return &chainEnv{svc: svc, po: po, in: in}
}

func mustQty(t *testing.T, s string) stock.Qty {
	t.Helper()
	q, err := stock.ParseQty(s)
	require.NoError(t, err)
	return q
}

// TestIntegrationReceiptIdempotencyUniqueIndex 并发同幂等键收货：部分唯一索引裁决，
// 恰一条收货、累计一次、重放返回同一单号（plan §11.4）。
func TestIntegrationReceiptIdempotencyUniqueIndex(t *testing.T) {
	_, svc := integrationEnv(t)
	ch := newChain(t, svc, 100, "10")
	actor := testActor()

	const workers = 6
	var wg sync.WaitGroup
	results := make([]*ReceiptResult, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := svc.ConfirmReceipt(context.Background(), actor, ReceiptInput{
				InboundNo: ch.in.InboundNo,
				Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: mustQty(t, "10")}},
			}, "idem-integration-1")
			results[idx], errs[idx] = res, err
		}(i)
	}
	wg.Wait()
	okCount, replayCount := 0, 0
	var receiptNo string
	for i, err := range errs {
		require.NoError(t, err, "并发同键全部成功（含重放路径），失败者: %v", err)
		require.NotNil(t, results[i])
		if receiptNo == "" {
			receiptNo = results[i].ReceiptNo
		}
		require.Equal(t, receiptNo, results[i].ReceiptNo, "同键返回同一收货单")
		if results[i].Replay {
			replayCount++
		} else {
			okCount++
		}
	}
	require.Equal(t, 1, okCount, "恰一路非重放（真实变更一次）")
	require.Equal(t, workers-1, replayCount)

	items, err := svc.repo.ListInboundItems(context.Background(), ch.in.ID.Int64())
	require.NoError(t, err)
	require.Equal(t, "10.0000", items[0].QtyReceived.String(), "库存与单据只变化一次")
}

// TestIntegrationOverReceiptGuardRealDB 并发分次收货合计超量：数据层守卫裁决，
// 累计收货不得超过订单量（business-flow §2.3、plan §11.3）。
func TestIntegrationOverReceiptGuardRealDB(t *testing.T) {
	_, svc := integrationEnv(t)
	ch := newChain(t, svc, 100, "10")
	actor := testActor()

	const workers = 8 // 每路收 4，合计 32 > 10
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.ConfirmReceipt(context.Background(), actor, ReceiptInput{
				InboundNo: ch.in.InboundNo,
				Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: mustQty(t, "4")}},
			}, "idem-over-"+strconv.Itoa(idx))
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		require.Contains(t, []string{
			ErrOverReceipt.Code, ErrReceiptQtyMismatch.Code, ErrInboundStatusNotAllowed.Code,
		}, codeOf(t, err))
	}
	require.LessOrEqual(t, wins*4, 10, "累计收货不得超过订单量（§2.3）")
}

// TestIntegrationClaimAtomic 并发领取恰一成功（architecture §5.2，真实行锁串行化）。
func TestIntegrationClaimAtomic(t *testing.T) {
	_, svc := integrationEnv(t)
	ch := newChain(t, svc, 100, "4")
	actor := testActor()
	ctx := context.Background()
	res, err := svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: ch.in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: mustQty(t, "4")}},
	}, "idem-claim")
	require.NoError(t, err)
	require.Len(t, res.PutawayTasks, 1)

	task, err := svc.repo.FindTaskByNo(ctx, res.PutawayTasks[0])
	require.NoError(t, err)
	require.NotNil(t, task)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			a := testActor()
			a.UserID = int64(200 + idx)
			_, err := svc.ClaimPutawayTask(context.Background(), a, task.ID.Int64())
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			require.Equal(t, ErrPutawayClaimConflict.Code, codeOf(t, err))
		}
	}
	require.Equal(t, 1, wins)
}

// TestIntegrationPutawayAndInspectChain 全链路：收货→上架（真实库存行 pending_inspect）
// →质检执行→ available/defective（plan §11.2 质检库存映射、inventory-rules §2 恒等式）。
func TestIntegrationPutawayAndInspectChain(t *testing.T) {
	db, svc := integrationEnv(t)
	ch := newChain(t, svc, 101, "5")
	actor := testActor()
	ctx := context.Background()

	rc, err := svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: ch.in.InboundNo,
		Lines: []ReceiptLineInput{{
			SKUID: 101, QtyGood: mustQty(t, "5"),
			BatchNo: "B-INT-01", ExpiryDate: dateOf(t, "2027-12-31"),
		}},
	}, "idem-chain")
	require.NoError(t, err)
	task, err := svc.repo.FindTaskByNo(ctx, rc.PutawayTasks[0])
	require.NoError(t, err)
	_, err = svc.ClaimPutawayTask(ctx, actor, task.ID.Int64())
	require.NoError(t, err)
	_, err = svc.ExecutePutawayTask(ctx, actor, task.ID.Int64(), PutawayExecuteInput{})
	require.NoError(t, err)

	// 真实库存行：total=5、pending_inspect=5、available=0（Putaway RequireInspect=true）。
	var row struct {
		Total          string
		Available      string
		PendingInspect string
		Defective      string
	}
	require.NoError(t, db.Raw(
		`SELECT total_qty::text AS total, available_qty::text AS available,
		        pending_inspect_qty::text AS pending_inspect, defective_qty::text AS defective
		 FROM inventory WHERE sku_id = ? AND warehouse_id = 1 LIMIT 1`, 101).Scan(&row).Error)
	require.Equal(t, "5.0000", row.Total)
	require.Equal(t, "5.0000", row.PendingInspect)
	require.Equal(t, "0.0000", row.Available)

	// 质检：3 合格 / 2 不良。
	qc, err := svc.CreateQC(ctx, actor, QCCreateInput{
		SourceNo: ch.in.InboundNo, InspectionType: InspectionFull,
		Lines: []QCLineInput{{SKUID: 101, BatchNo: "B-INT-01", QtyInspected: mustQty(t, "5")}},
	})
	require.NoError(t, err)
	_, err = svc.StartQC(ctx, actor, qc.ID.Int64())
	require.NoError(t, err)
	_, err = svc.ExecuteQC(ctx, actor, qc.ID.Int64(), QCExecuteInput{
		Lines:  []QCExecuteLine{{LineNo: 1, QtyQualified: mustQty(t, "3"), QtyDefective: mustQty(t, "2")}},
		Result: "部分合格",
	})
	require.NoError(t, err)

	require.NoError(t, db.Raw(
		`SELECT total_qty::text AS total, available_qty::text AS available,
		        pending_inspect_qty::text AS pending_inspect, defective_qty::text AS defective
		 FROM inventory WHERE sku_id = ? AND warehouse_id = 1 LIMIT 1`, 101).Scan(&row).Error)
	require.Equal(t, "5.0000", row.Total)
	require.Equal(t, "3.0000", row.Available, "质检合格转可用")
	require.Equal(t, "0.0000", row.PendingInspect)
	require.Equal(t, "2.0000", row.Defective, "质检不良转不良品库存状态")

	// 流水成对：上架 INBOUND(5) + 质检两向（INSPECT_PASS 3 / INSPECT_DEFECTIVE 2）。
	var ledgerCount int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*) FROM inventory_ledgers
		WHERE change_type IN ('INBOUND', 'INSPECT_PASS', 'INSPECT_DEFECTIVE')
		  AND (business_no = ? OR business_no = ?)`,
		ch.in.InboundNo, ch.in.InboundNo).Scan(&ledgerCount).Error)
	require.GreaterOrEqual(t, ledgerCount, int64(3), "上架与质检两向流水齐备（inventory-rules §5）")
}

// TestIntegrationDraftPOEditReplaceItems 草稿编辑整单替换明细（回归：2026-10-07
// 编辑采购订单报"系统内部错误"——ReplacePOItems 曾用 GORM 软删，软删行物理保留占用
// uk_purchase_order_items_po_line (po_id, line_no)，重插同行号即 23505；
// Unscoped 硬删后必须可重复编辑，且旧明细物理清除、新明细行号连续）。
func TestIntegrationDraftPOEditReplaceItems(t *testing.T) {
	db, svc := integrationEnv(t)
	actor := testActor()
	ctx := context.Background()

	po, err := svc.CreatePO(ctx, actor, POCreateInput{
		SupplierID: 11, WarehouseID: 1,
		Items: []POItemInput{{SKUID: 100, Qty: mustQty(t, "10"), Price: mustQty(t, "2")}},
	})
	require.NoError(t, err)
	poID := po.ID.Int64()

	// 第一次替换：同 SKU 改量改价（用户截图场景：编辑保存）
	_, err = svc.UpdatePO(ctx, actor, poID, POUpdateInput{
		Items: []POItemInput{{SKUID: 100, Qty: mustQty(t, "2000"), Price: mustQty(t, "1")}},
	})
	require.NoError(t, err, "草稿编辑替换明细不得因唯一索引冲突失败")

	// 第二次替换：换 SKU + 行数变化（1 行 → 2 行），覆盖删旧插新的完整路径
	_, err = svc.UpdatePO(ctx, actor, poID, POUpdateInput{
		Items: []POItemInput{
			{SKUID: 100, Qty: mustQty(t, "5"), Price: mustQty(t, "3")},
			{SKUID: 101, Qty: mustQty(t, "7"), Price: mustQty(t, "4")},
		},
	})
	require.NoError(t, err, "行数变化的重替换同样不得冲突")

	items, err := svc.repo.ListPOItems(ctx, poID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, 1, items[0].LineNo)
	require.Equal(t, int64(100), items[0].SKUID)
	require.Equal(t, "5.0000", items[0].QtyOrdered.String())
	require.Equal(t, 2, items[1].LineNo)
	require.Equal(t, int64(101), items[1].SKUID)

	// 旧行必须物理清除（Unscoped 硬删）：任何 deleted_at 非空行残留都会再次顶爆唯一索引
	var stale int64
	require.NoError(t, db.Raw(
		`SELECT COUNT(*) FROM purchase_order_items WHERE po_id = ? AND deleted_at IS NOT NULL`, poID).
		Scan(&stale).Error)
	require.Zero(t, stale, "替换后不得残留软删行（唯一索引 (po_id, line_no) 会被其占用）")
}

// TestIntegrationDraftInboundEditReplaceItems 入库单草稿编辑同型回归（与 PO 同一
// 软删冲突面：uk_inbound_items_inbound_line）。
func TestIntegrationDraftInboundEditReplaceItems(t *testing.T) {
	db, svc := integrationEnv(t)
	actor := testActor()
	ctx := context.Background()

	in, err := svc.CreateInbound(ctx, actor, InboundCreateInput{
		SourceType: SourceTypeOther, WarehouseID: 1,
		Items: []InboundItemInput{{SKUID: 100, Qty: mustQty(t, "10")}},
	})
	require.NoError(t, err)
	inID := in.ID.Int64()

	_, err = svc.UpdateInbound(ctx, actor, inID, InboundUpdateInput{
		Items: []InboundItemInput{{SKUID: 100, Qty: mustQty(t, "20")}},
	})
	require.NoError(t, err, "入库单草稿编辑替换明细不得因唯一索引冲突失败")

	_, err = svc.UpdateInbound(ctx, actor, inID, InboundUpdateInput{
		Items: []InboundItemInput{
			{SKUID: 100, Qty: mustQty(t, "3")},
			{SKUID: 101, Qty: mustQty(t, "4")},
		},
	})
	require.NoError(t, err)

	items, err := svc.repo.ListInboundItems(ctx, inID)
	require.NoError(t, err)
	require.Len(t, items, 2)

	var stale int64
	require.NoError(t, db.Raw(
		`SELECT COUNT(*) FROM inbound_items WHERE inbound_id = ? AND deleted_at IS NOT NULL`, inID).
		Scan(&stale).Error)
	require.Zero(t, stale, "替换后不得残留软删行")
}
