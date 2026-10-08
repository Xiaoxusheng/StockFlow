//go:build integration

// 真实 PostgreSQL 依赖的调拨/盘点集成测试（backend-m2-plan §11.2 调拨/盘点子集；
// 默认 go test 不编译本文件——单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/stockops/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip）。
// 覆盖：gormStore 全部 SQL 路径（状态守卫迁移/审批/审计/在途聚合/跨域只读编排）、
// 调拨两端流水与恒等式（business-flow §10.1）、盘点冻结—登记—差异—调整—解冻
// （plan §6.7）、幂等键唯一冲突兜底（inventory_ledgers 部分唯一索引）。
package stockops

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/response"
)

// fakeSKUChecker2/fakeBinChecker2/fakeFlags2 集成装配替身（inventory 域同名单独定义，
// 域间测试替身不可跨包复用）。

type fakeSKUChecker2 struct{}

func (fakeSKUChecker2) ExistsActive(_ context.Context, skuID int64) (bool, error) {
	return skuID > 0, nil
}

type fakeBinChecker2 struct{}

func (fakeBinChecker2) ExistsActive(_ context.Context, _, _ int64) (bool, error) {
	return true, nil
}

type fakeFlags2 struct{}

func (fakeFlags2) GetFlags(_ context.Context, _ int64) (SKUFlags, error) {
	return SKUFlags{Enabled: true}, nil // 集成种子均为非序列号 SKU
}

func integrationEnv(t *testing.T) (*gorm.DB, *Service, *inventory.Service) {
	t.Helper()
	if os.Getenv("SF_TEST_PG_NAME") == "" {
		t.Skip("未设置 SF_TEST_PG_* 环境变量，跳过集成测试")
	}
	port, err := strconv.Atoi(getenvOr("SF_TEST_PG_PORT", "5432"))
	require.NoError(t, err)
	dbCfg := config.DatabaseConfig{
		Host: getenvOr("SF_TEST_PG_HOST", "127.0.0.1"), Port: port,
		User: getenvOr("SF_TEST_PG_USER", "postgres"), Password: os.Getenv("SF_TEST_PG_PASSWORD"),
		Name: os.Getenv("SF_TEST_PG_NAME"), SSLMode: "disable",
		MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 10 * time.Minute,
	}
	db, err := database.Connect(dbCfg)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	require.NoError(t, database.MigrateUp(dbCfg.DSN(), repoRootMigrations(t)))

	gw := inventory.NewService(db, nil,
		inventory.WithSKUChecker(fakeSKUChecker2{}), inventory.WithBinChecker(fakeBinChecker2{}))
	svc := NewService(db,
		WithGateway(gw),
		WithBinChecker(fakeBinChecker2{}),
		WithSKUChecker(fakeSKUChecker2{}),
		WithSKUFlagReader(fakeFlags2{}),
	)
	return db, svc, gw
}

func getenvOr(key, def string) string {
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
	t.Fatal("未找到 db/migrations（请从仓库内运行）")
	return ""
}

const (
	itWhSrc = int64(1)
	itWhDst = int64(2)
)

// itRunID 本轮运行的唯一标识（毫秒时间戳 + PID），用于派生集成用例的库位号。
//
// 为什么需要：同包多个用例原先共用 bin 111（调拨用例种 SKU7001、盘点用例种 SKU7002），
// 盘点按 `Scope{BIN:[111]}` 冻结会同时捞到两行 → `require.Len(detail.Items, 1)` 恒失败；
// 且同一 DB 上重复运行会残留旧行（Putaway 累加），断言绝对量（qty 10）随之失真。
// 按运行派生库位后，用例彼此独立、同一 DB 可重复运行，且不需要清库（不破坏演示数据）。
// inventory 表无库位外键，任意 bin_id 合法；库位存在性由 fakeBinChecker2 放行。
var itRunID = int64(time.Now().UnixNano()/int64(time.Millisecond)%1_000_000)*1000 + int64(os.Getpid()%1000)

// itLoc 派生本轮库位号（base 保留可读性：111 → 111000123）。
func itLoc(base int64) int64 { return base*1_000_000 + itRunID }

// itSeedRow 经库存原语 Putaway 播种源行（免检直达 available）。
func itSeedRow(t *testing.T, gw *inventory.Service, wh, zone, shelf, bin, sku int64, qty int64) inventory.RowKey {
	t.Helper()
	key := inventory.RowKey{WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku}
	_, err := gw.Putaway(context.Background(), nil, inventory.PutawayOp{
		Key: key, Qty: inventory.Qty(qty * 10000),
		Source: inventory.Source{Type: "it_seed", No: "SEED-IT"}, Actor: testActor("it"),
	})
	require.NoError(t, err)
	return key
}

func itLoadRow(t *testing.T, db *gorm.DB, key inventory.RowKey) *inventory.Inventory {
	t.Helper()
	var row inventory.Inventory
	err := db.Raw(`
		SELECT id, warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
		       total_qty, available_qty, locked_qty, frozen_qty,
		       pending_inspect_qty, defective_qty
		FROM inventory
		WHERE warehouse_id = ? AND bin_id = ? AND sku_id = ? AND batch_id = ?`,
		key.WarehouseID, key.BinID, key.SKUID, key.BatchID).Scan(&row).Error
	require.NoError(t, err)
	require.NotZero(t, row.ID, "库存行应存在")
	return &row
}

func TestIntegrationTransferLifecycle(t *testing.T) {
	db, svc, gw := integrationEnv(t)
	ctx := context.Background()
	actor := testActor("it")

	src := itSeedRow(t, gw, itWhSrc, 1, 11, itLoc(111), 7001, 10)

	in := TransferInput{
		Type: TransferTypeWarehouse, FromWarehouseID: itWhSrc, ToWarehouseID: itWhDst,
		Lines: []TransferLineInput{{
			SKUID: 7001, Qty: q(6),
			From: TransferLoc{WarehouseID: itWhSrc, ZoneID: 1, ShelfID: 11, BinID: itLoc(111)},
			To:   TransferLoc{WarehouseID: itWhDst, ZoneID: 2, ShelfID: 21, BinID: itLoc(211)},
		}},
	}
	d, err := svc.CreateTransfer(ctx, actor, in)
	require.NoError(t, err)
	id := d.Order.ID.Int64()
	require.Regexp(t, `^TR-\d{8}-\d{6}$`, d.Order.TransferNo) // docnum 编号（business-flow §13.1）

	_, _, err = svc.SubmitTransfer(ctx, actor, id)
	require.NoError(t, err)
	_, _, err = svc.ApproveTransfer(ctx, actor, id, ApproveTransferInput{Action: "approve"}, "")
	require.NoError(t, err)

	// 预占后：available 4 / locked 6 / total 10。
	srcRow := itLoadRow(t, db, src)
	require.Equal(t, q(4), srcRow.AvailableQty)
	require.Equal(t, q(6), srcRow.LockedQty)
	require.NoError(t, srcRow.State().ValidateIdentity())

	_, _, err = svc.OutboundTransfer(ctx, actor, id, "")
	require.NoError(t, err)
	srcRow = itLoadRow(t, db, src)
	require.Equal(t, q(4), srcRow.TotalQty)
	require.True(t, srcRow.LockedQty.IsZero())
	require.NoError(t, srcRow.State().ValidateIdentity())

	// 在途聚合 = 6（按本用例 SKU 收窄：全局口径会被同 DB 其他 TRANSFERRING 单干扰，
	// 集成测试需在共用库上可重复运行）。
	rows, total, err := svc.ListInTransit(ctx, InTransitFilter{AllWarehouses: true, SKUID: 7001}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, q(6), rows[0].OutTransit)

	_, _, err = svc.ArriveTransfer(ctx, actor, id)
	require.NoError(t, err)
	_, _, err = svc.ReceiveTransfer(ctx, actor, id, "")
	require.NoError(t, err)

	dst := inventory.RowKey{WarehouseID: itWhDst, ZoneID: 2, ShelfID: 21, BinID: itLoc(211), SKUID: 7001}
	dstRow := itLoadRow(t, db, dst)
	require.Equal(t, q(6), dstRow.TotalQty)
	require.Equal(t, q(6), dstRow.AvailableQty)
	require.NoError(t, dstRow.State().ValidateIdentity())

	// 两端流水齐备（§10.1）。
	var outN, inN int
	err = db.Raw(`SELECT COUNT(*) FROM inventory_ledgers WHERE change_type = 'TRANSFER_OUT'
		AND business_type = ? AND business_no = ?`, sourceTransfer, d.Order.TransferNo).Scan(&outN).Error
	require.NoError(t, err)
	err = db.Raw(`SELECT COUNT(*) FROM inventory_ledgers WHERE change_type = 'TRANSFER_IN'
		AND business_type = ? AND business_no = ?`, sourceTransfer, d.Order.TransferNo).Scan(&inN).Error
	require.NoError(t, err)
	require.Equal(t, 1, outN)
	require.Equal(t, 1, inN)

	// 幂等重放：重复出库调用（状态守卫拦截）不产生第二条 TRANSFER_OUT。
	_, _, err = svc.OutboundTransfer(ctx, actor, id, "")
	require.Error(t, err) // 状态已 COMPLETED → 冲突
	err = db.Raw(`SELECT COUNT(*) FROM inventory_ledgers WHERE change_type = 'TRANSFER_OUT'
		AND business_no = ?`, d.Order.TransferNo).Scan(&outN).Error
	require.NoError(t, err)
	require.Equal(t, 1, outN)
}

func TestIntegrationCountFlow(t *testing.T) {
	db, svc, gw := integrationEnv(t)
	ctx := context.Background()
	actor := testActor("it")

	src := itSeedRow(t, gw, itWhSrc, 1, 11, itLoc(112), 7002, 10)

	d, err := svc.CreateCount(ctx, actor, CountInput{
		WarehouseID: itWhSrc, Scope: CountScope{Mode: "BIN", BinIDs: []int64{itLoc(112)}},
	})
	require.NoError(t, err)
	require.Regexp(t, `^CK-\d{8}-\d{6}$`, d.Order.CountNo)
	cid := d.Order.ID.Int64()

	_, _, err = svc.StartCount(ctx, actor, cid)
	require.NoError(t, err)

	// 冻结：available→frozen。
	srcRow := itLoadRow(t, db, src)
	require.True(t, srcRow.AvailableQty.IsZero())
	require.Equal(t, q(10), srcRow.FrozenQty)
	require.NoError(t, srcRow.State().ValidateIdentity())

	// 冻结期间并发销售预占被 available 守卫拒绝（plan §11.3）。
	_, err = gw.Lock(ctx, nil, inventory.LockOp{
		Key: src, Qty: q(1), LockType: "ORDER_HOLD",
		Source: inventory.Source{Type: "sales_order", No: "SO-IT"}, Actor: actor,
	})
	require.Error(t, err)
	var bizErr *response.Error
	require.ErrorAs(t, err, &bizErr)
	require.Equal(t, "INVENTORY_NOT_ENOUGH", bizErr.Error()[:len("INVENTORY_NOT_ENOUGH")])

	detail, err := svc.GetCountDetail(ctx, cid, Scope{AllWarehouses: true})
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	rowID := detail.Items[0].InventoryRowID

	// 登记（PUT 幂等覆盖 ×2）。
	reg := CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowID, Qty: q(8)}, // 盘亏 2
	}}
	for range 2 {
		_, err = svc.RegisterCountings(ctx, actor, cid, reg)
		require.NoError(t, err)
	}
	_, _, err = svc.FinishCount(ctx, actor, cid)
	require.NoError(t, err)
	detail, err = svc.GetCountDetail(ctx, cid, Scope{AllWarehouses: true})
	require.NoError(t, err)
	require.Len(t, detail.Differences, 1)
	require.Equal(t, q(-2), detail.Differences[0].DiffQty)

	// 完成：解冻 + 调整原子（plan §6.7）。
	_, _, err = svc.CompleteCount(ctx, actor, cid, "差异属实", "")
	require.NoError(t, err)

	srcRow = itLoadRow(t, db, src)
	require.Equal(t, q(8), srcRow.TotalQty)
	require.Equal(t, q(8), srcRow.AvailableQty)
	require.True(t, srcRow.FrozenQty.IsZero())
	require.NoError(t, srcRow.State().ValidateIdentity())

	detail, err = svc.GetCountDetail(ctx, cid, Scope{AllWarehouses: true})
	require.NoError(t, err)
	require.Equal(t, DiffExecuted, detail.Differences[0].Status)
	require.NotEmpty(t, detail.Differences[0].AdjustNo) // adjust_no 回写

	// 调整单 + ADJUST 流水落库（business-flow §11.1）。
	var adjN int
	err = db.Raw(`SELECT COUNT(*) FROM inventory_adjustments WHERE adjustment_no = ?`,
		detail.Differences[0].AdjustNo).Scan(&adjN).Error
	require.NoError(t, err)
	require.Equal(t, 1, adjN)
	var ledN int
	err = db.Raw(`SELECT COUNT(*) FROM inventory_ledgers WHERE change_type = 'ADJUST'
		AND business_type = ? AND business_no = ?`, sourceCount, detail.Order.CountNo).Scan(&ledN).Error
	require.NoError(t, err)
	require.Equal(t, 1, ledN)

	// 无残留活跃冻结锁。
	var lockN int
	err = db.Raw(`SELECT COUNT(*) FROM inventory_locks WHERE source_type = ? AND source_no = ?
		AND lock_type = 'COUNT_FREEZE' AND status = 'ACTIVE'`, sourceCount, detail.Order.CountNo).Scan(&lockN).Error
	require.NoError(t, err)
	require.Zero(t, lockN)
}
