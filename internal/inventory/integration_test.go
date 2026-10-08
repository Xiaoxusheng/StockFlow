//go:build integration

// 真实 PostgreSQL 依赖的并发/恒等式/流水/幂等/锁定生命周期集成测试（plan §8.7 专项
// 1–5；默认 go test 不编译本文件——单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/inventory/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip；Redis 非本域正确性依赖，不需要）。
// 说明：专项 6（权限/越权 403）属 auth 域中间件与 router 装配职责，不在本域测试范围。
package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// fakeSKUChecker/fakeBinChecker 定义于 handler_test.go（默认测试构建），集成构建复用。

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

	// 迁移到最新（从仓库根解析 db/migrations——go test 的工作目录是包目录）。
	require.NoError(t, database.MigrateUp(dbCfg.DSN(), repoRootMigrations(t)))

	svc := NewService(db, nil, WithSKUChecker(fakeSKUChecker{}), WithBinChecker(fakeBinChecker{}))
	return db, svc
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// repoRootMigrations 向上查找仓库根的 db/migrations。
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

func testActor() Actor {
	return Actor{ID: 99, Name: "it-tester", RequestID: "it-req-1"}
}

func testKey() RowKey {
	return RowKey{WarehouseID: 1, ZoneID: 1, ShelfID: 1, BinID: itBin(101), SKUID: 5001, BatchID: 0}
}

// itRunID 本轮运行唯一标识（毫秒时间戳 + PID），用于派生集成用例的库位号。
//
// 为什么需要：原实现所有用例共用固定库位 101–108，同库重复运行会残留上一轮库存行
// （Putaway 累加）→ 断言绝对量（avail/total）与 `require.Equal(int64(1), total)` 类
// 计数断言随轮次失真；同包多用例亦会互相看见对方的行。按运行派生库位后，同一 DB
// 可无限次重复运行且用例彼此隔离（inventory 表无库位外键；库位存在性由
// fakeBinChecker 放行）。
var itRunID = int64(time.Now().UnixNano()/int64(time.Millisecond)%1_000_000)*1000 + int64(os.Getpid()%1000)

// itBin 派生本轮库位号（base 保留可读性：101 → 101000123）。
func itBin(base int64) int64 { return base*1_000_000 + itRunID }

// itToken 本轮唯一令牌（拼进业务单号/幂等键，供同库重跑时 count(*) 类断言保持确定）。
func itToken() string { return strconv.FormatInt(itRunID, 10) }

// seedRow 经 Putaway 建立指定可用量（免检直达）的库存行。
func seedRow(t *testing.T, svc *Service, key RowKey, avail int64) {
	t.Helper()
	_, err := svc.Putaway(context.Background(), nil, PutawayOp{
		Key: key, Qty: q(avail), Source: Source{Type: "it_seed", No: "SEED-1"}, Actor: testActor(),
	})
	require.NoError(t, err)
}

func loadRow(t *testing.T, db *gorm.DB, key RowKey) *Inventory {
	t.Helper()
	var row Inventory
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

func assertIdentity(t *testing.T, row *Inventory) {
	t.Helper()
	require.NoError(t, row.State().ValidateIdentity(),
		"恒等式被破坏: %+v（inventory-rules §2）", row.State())
}

func ledgerCount(t *testing.T, db *gorm.DB, businessNo string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM inventory_ledgers WHERE business_no = ?`, businessNo).Scan(&n).Error)
	return n
}

// ---- 专项 1：并发扣减/预占（inventory-rules §9.1：总量 15，两路各 10，恰一路成功）----

func TestConcurrentLockNoOversell(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	seedRow(t, svc, key, 15)

	const goroutines = 8
	var wg sync.WaitGroup
	var success atomic.Int64
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Lock(ctx, nil, LockOp{
				Key: key, Qty: q(10), LockType: "ORDER_HOLD",
				Source: Source{Type: "it_order", No: fmt.Sprintf("SO-CONC-%d", idx)}, Actor: testActor(),
			})
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err == nil {
			success.Add(1)
		} else {
			var bizErr *response.Error
			require.True(t, errors.As(err, &bizErr) && strings.Contains(bizErr.Error(), "INVENTORY_NOT_ENOUGH"),
				"并发失败必须是库存不足，得到: %v", err)
		}
	}
	require.Equal(t, int64(1), success.Load(), "15 库存两路各 10 预占：恰一路成功（inventory-rules §9.1）")

	row := loadRow(t, db, key)
	require.Equal(t, q(5), row.AvailableQty)
	require.Equal(t, q(10), row.LockedQty)
	require.Equal(t, q(15), row.TotalQty)
	assertIdentity(t, row)
}

func TestConcurrentDeductNoNegativeStock(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	key.BinID = itBin(102)
	seedRow(t, svc, key, 15)
	// 先锁定 10（为并发核销做准备：两路各核销 10，仅一路可成功）。
	res, err := svc.Lock(ctx, nil, LockOp{
		Key: key, Qty: q(10), LockType: "ORDER_HOLD",
		Source: Source{Type: "it_order", No: "SO-CONC-DEDUCT"}, Actor: testActor(),
	})
	require.NoError(t, err)

	const goroutines = 2
	var wg sync.WaitGroup
	var success atomic.Int64
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Deduct(ctx, nil, DeductOp{
				Key: key, Qty: q(10), LockID: res.LockID,
				Source: Source{Type: "it_shipment", No: fmt.Sprintf("SH-CONC-%d", idx)}, Actor: testActor(),
			})
			if err == nil {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int64(1), success.Load(), "锁定 10 两路各核销 10：恰一路成功")

	// 胜出一路的效果：核销 = 锁定转已出（total 与 locked 同减 10），available 不变。
	// 原断言（total 15 / locked 10，注释「锁定 10 未出库，total 不变」）与 Deduct 语义
	// 及 success==1 自相矛盾——同文件 TestLockLifecycle 已断言「核销后 total 同减」
	// （Deduct 经 applyDelta(totalDelta=-qty) + ColLocked 同减），此处对齐。
	row := loadRow(t, db, key)
	require.Equal(t, q(5), row.TotalQty, "核销一路成功：total 同减 10（15→5）")
	require.Equal(t, Qty(0), row.LockedQty, "锁定被核销清零（失败方不生效）")
	require.Equal(t, q(5), row.AvailableQty)
	assertIdentity(t, row)
}

// ---- 专项 2：恒等式（每个变更操作后显式断言六列恒等式）----

func TestIdentityAfterEveryMutation(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	key.BinID = itBin(103)

	// 免检入库 → 锁定 → 部分释放 → 核销 → 待检入库 → 质检合格/不良 → 盘盈/盘亏 → 移库。
	_, err := svc.Putaway(ctx, nil, PutawayOp{Key: key, Qty: q(50), Source: Source{Type: "it_in", No: "IN-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	lk, err := svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(20), LockType: "ORDER_HOLD", Source: Source{Type: "it_order", No: "SO-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	_, err = svc.ReleaseLock(ctx, nil, ReleaseLockOp{LockID: lk.LockID, Qty: q(5), Source: Source{Type: "it_cancel", No: "SO-1-C1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	_, err = svc.Deduct(ctx, nil, DeductOp{Key: key, Qty: q(15), LockID: lk.LockID, Source: Source{Type: "it_ship", No: "SH-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	_, err = svc.Putaway(ctx, nil, PutawayOp{Key: key, Qty: q(12), RequireInspect: true, Source: Source{Type: "it_in", No: "IN-2"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	_, err = svc.InspectResult(ctx, nil, InspectResultOp{Key: key, Qty: q(7), Pass: true, Source: Source{Type: "it_qc", No: "QC-1"}, Actor: testActor()})
	require.NoError(t, err)
	_, err = svc.InspectResult(ctx, nil, InspectResultOp{Key: key, Qty: q(5), Pass: false, Source: Source{Type: "it_qc", No: "QC-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	_, err = svc.Adjust(ctx, nil, AdjustOp{Key: key, AdjustType: "盘盈", Qty: q(3), Reason: "盘点多出", Source: Source{Type: "it_count", No: "CK-1"}, Actor: testActor()})
	require.NoError(t, err)
	_, err = svc.Adjust(ctx, nil, AdjustOp{Key: key, AdjustType: "盘亏", Qty: q(2), Reason: "盘点短缺", Source: Source{Type: "it_count", No: "CK-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))

	dest := key
	dest.BinID = itBin(104)
	_, err = svc.MoveBin(ctx, nil, MoveBinOp{From: key, To: dest, Qty: q(10), Source: Source{Type: "it_move", No: "MV-1"}, Actor: testActor()})
	require.NoError(t, err)
	assertIdentity(t, loadRow(t, db, key))
	assertIdentity(t, loadRow(t, db, dest))
}

// ---- 专项 3：流水一一对应（plan §8.4 冻结口径）----

func TestLedgerPairing(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	key.BinID = itBin(105)
	businessNo := "SO-LEDGER-1-" + itToken() // 本轮唯一：同库重跑时 count(*) 断言不被上一轮流水污染

	// total 变化型（INBOUND）：total 差 = qty_change；status_from/to=受影响列。
	_, err := svc.Putaway(ctx, nil, PutawayOp{Key: key, Qty: q(30), Source: Source{Type: "it_in", No: businessNo}, Actor: testActor()})
	require.NoError(t, err)
	require.Equal(t, int64(1), ledgerCount(t, db, businessNo))
	var led InventoryLedger
	require.NoError(t, db.Raw(`SELECT * FROM inventory_ledgers WHERE business_no = ?`, businessNo).Scan(&led).Error)
	require.Equal(t, "INBOUND", led.ChangeType)
	require.Equal(t, "available", led.StatusFrom)
	require.Equal(t, "available", led.StatusTo)
	require.Equal(t, Qty(0), led.QtyBefore)
	require.Equal(t, q(30), led.QtyChange)
	require.Equal(t, q(30), led.QtyAfter)
	require.NotEmpty(t, led.LedgerNo)

	// 状态迁移型（LOCK）：total 不变，status_from=available→locked，qty 三态为 available 列。
	_, err = svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(10), LockType: "ORDER_HOLD", Source: Source{Type: "it_order", No: businessNo}, Actor: testActor()})
	require.NoError(t, err)
	require.Equal(t, int64(2), ledgerCount(t, db, businessNo))
	var lockLed InventoryLedger
	require.NoError(t, db.Raw(`SELECT * FROM inventory_ledgers WHERE business_no = ? AND change_type = 'LOCK'`, businessNo).Scan(&lockLed).Error)
	require.Equal(t, "available", lockLed.StatusFrom)
	require.Equal(t, "locked", lockLed.StatusTo)
	require.Equal(t, q(30), lockLed.QtyBefore) // available 列三态
	require.Equal(t, q(-10), lockLed.QtyChange)
	require.Equal(t, q(20), lockLed.QtyAfter)

	// 移库：源/目标各一条 MOVE 流水，幂等键仅在首条（部分唯一索引）。
	// 移库幂等键由调用方传入（api.md §7 通式表无 move 前缀，stockops 以头键合成后透传），
	// 故此处显式给键——不给键时两条流水均无键，无法验证「仅首条带键」的配对规则。
	dest := key
	dest.BinID = itBin(106)
	moveIdem := "move:" + businessNo + ":1"
	_, err = svc.MoveBin(ctx, nil, MoveBinOp{From: key, To: dest, Qty: q(4), Source: Source{Type: "it_move", No: businessNo}, Actor: testActor(), IdempotencyKey: moveIdem})
	require.NoError(t, err)
	var moves []InventoryLedger
	require.NoError(t, db.Raw(`SELECT * FROM inventory_ledgers WHERE business_no = ? AND change_type = 'MOVE' ORDER BY id`, businessNo).Scan(&moves).Error)
	require.Len(t, moves, 2)
	require.NotNil(t, moves[0].IdempotencyKey, "首条 MOVE 流水应带幂等键")
	require.Nil(t, moves[1].IdempotencyKey, "次条 MOVE 流水不带幂等键（部分唯一索引约束）")
	require.Equal(t, q(-4), moves[0].QtyChange)
	require.Equal(t, q(4), moves[1].QtyChange)
}

// ---- 专项 4：幂等（同 idempotency_key 重放，库存仅变化一次）----

func TestIdempotencyReplayDoesNotDoubleApply(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	key.BinID = itBin(107)
	seedRow(t, svc, key, 100)

	idem := "IDEM-LOCK-" + itToken()
	src := Source{Type: "it_order", No: "SO-IDEM-1"}
	first, err := svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(30), LockType: "ORDER_HOLD", Source: src, Actor: testActor(), IdempotencyKey: idem})
	require.NoError(t, err)
	require.False(t, first.Replay)
	require.NotZero(t, first.LockID)

	// 同键重放：不重复预占。
	second, err := svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(30), LockType: "ORDER_HOLD", Source: src, Actor: testActor(), IdempotencyKey: idem})
	require.NoError(t, err)
	require.True(t, second.Replay)
	require.Equal(t, first.LockID, second.LockID)
	require.Equal(t, first.Ledger.LedgerNo, second.Ledger.LedgerNo)

	row := loadRow(t, db, key)
	require.Equal(t, q(30), row.LockedQty, "重放不重复扣减（plan §8.5）")
	require.Equal(t, q(70), row.AvailableQty)
	assertIdentity(t, row)

	// 出库扣减幂等。
	idemDeduct := "IDEM-DEDUCT-" + itToken()
	d1, err := svc.Deduct(ctx, nil, DeductOp{Key: key, Qty: q(10), Source: Source{Type: "it_ship", No: "SH-IDEM-1"}, Actor: testActor(), IdempotencyKey: idemDeduct})
	require.NoError(t, err)
	d2, err := svc.Deduct(ctx, nil, DeductOp{Key: key, Qty: q(10), Source: Source{Type: "it_ship", No: "SH-IDEM-1"}, Actor: testActor(), IdempotencyKey: idemDeduct})
	require.NoError(t, err)
	require.True(t, d2.Replay)
	require.Equal(t, d1.Ledger.ID, d2.Ledger.ID)
	row = loadRow(t, db, key)
	require.Equal(t, q(20), row.LockedQty)
	require.Equal(t, q(90), row.TotalQty, "total 仅扣一次")
	assertIdentity(t, row)
}

// ---- 专项 5：锁定生命周期（Lock→ReleaseLock / Deduct 核销，锁记录与流水配套）----

func TestLockLifecycle(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()
	key := testKey()
	key.BinID = itBin(108)
	seedRow(t, svc, key, 50)

	// QC 冻结类锁定 → frozen 列（inventory-rules §2 冻结状态）。
	fz, err := svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(10), LockType: "QC_FREEZE", Source: Source{Type: "it_qc", No: "QC-LC-1"}, Actor: testActor()})
	require.NoError(t, err)
	row := loadRow(t, db, key)
	require.Equal(t, q(10), row.FrozenQty)
	require.Equal(t, q(40), row.AvailableQty)
	assertIdentity(t, row)

	// 部分解冻：frozen→available，锁记录保持 ACTIVE 且 qty 递减。
	_, err = svc.ReleaseLock(ctx, nil, ReleaseLockOp{LockID: fz.LockID, Qty: q(4), Source: Source{Type: "it_qc", No: "QC-LC-1-R1"}, Actor: testActor()})
	require.NoError(t, err)
	var lockRow struct {
		Qty    Qty
		Status string
	}
	require.NoError(t, db.Raw(`SELECT qty, status FROM inventory_locks WHERE id = ?`, fz.LockID).Scan(&lockRow).Error)
	require.Equal(t, "ACTIVE", lockRow.Status)
	require.Equal(t, q(6), lockRow.Qty)
	row = loadRow(t, db, key)
	require.Equal(t, q(6), row.FrozenQty)
	require.Equal(t, q(44), row.AvailableQty)
	assertIdentity(t, row)

	// 全量解冻 → RELEASED（qty 保持终态前值——迁移 chk_inventory_locks_qty_positive
	// 要求 qty>0，终态由 status 表达）。
	_, err = svc.ReleaseLock(ctx, nil, ReleaseLockOp{LockID: fz.LockID, Qty: q(6), Source: Source{Type: "it_qc", No: "QC-LC-1-R2"}, Actor: testActor()})
	require.NoError(t, err)
	require.NoError(t, db.Raw(`SELECT qty, status FROM inventory_locks WHERE id = ?`, fz.LockID).Scan(&lockRow).Error)
	require.Equal(t, "RELEASED", lockRow.Status)
	require.Equal(t, q(6), lockRow.Qty)
	row = loadRow(t, db, key)
	require.Equal(t, Qty(0), row.FrozenQty)
	assertIdentity(t, row)

	// 订单占用 → 发货核销 → CONSUMED。
	od, err := svc.Lock(ctx, nil, LockOp{Key: key, Qty: q(12), LockType: "ORDER_HOLD", Source: Source{Type: "it_order", No: "SO-LC-1"}, Actor: testActor()})
	require.NoError(t, err)
	_, err = svc.Deduct(ctx, nil, DeductOp{Key: key, Qty: q(12), LockID: od.LockID, Source: Source{Type: "it_ship", No: "SH-LC-1"}, Actor: testActor()})
	require.NoError(t, err)
	require.NoError(t, db.Raw(`SELECT qty, status FROM inventory_locks WHERE id = ?`, od.LockID).Scan(&lockRow).Error)
	require.Equal(t, "CONSUMED", lockRow.Status)
	require.Equal(t, q(12), lockRow.Qty, "终态 qty 保持正数（CHECK 约束），核销量以流水为准")
	row = loadRow(t, db, key)
	require.Equal(t, q(38), row.TotalQty, "核销后 total 同减")
	require.Equal(t, Qty(0), row.LockedQty)
	assertIdentity(t, row)

	// 超额释放被拒绝（锁记录不存在/已完结语义）。
	_, err = svc.ReleaseLock(ctx, nil, ReleaseLockOp{LockID: od.LockID, Qty: q(1), Source: Source{Type: "it_cancel", No: "SO-LC-1-X"}, Actor: testActor()})
	require.Error(t, err)
}

// ---- 专项 7：序列号并发核销（2026-10 渗透修复回归：一物两卖窗口封闭）----

// TestConcurrentSerialWriteOffSingleWinner 两个出库事务并发核销同一序列号，各自
// 复刻消费方最小流程："SerialStates（FOR UPDATE 读）→ 校验 IN_STOCK →
// SerialEvent(OUTBOUND)"。
// 修复前：读侧普通 SELECT 无锁，两事务可同时读到 IN_STOCK 并双双核销（一物两卖）；
// 修复后：后到事务在 SerialStates 处阻塞至先到提交，READ COMMITTED 重读见
// OUTBOUND，校验 fail-closed 拒绝——恰一方成功；写侧 updateSerial 的
// WHERE status=? CAS 守卫（repository.go）为第二重防线。
func TestConcurrentSerialWriteOffSingleWinner(t *testing.T) {
	db, svc := integrationEnv(t)
	ctx := context.Background()

	// 序列号建档 IN_STOCK（入库采集形态，inventory-rules §8.2）。
	const sn = "SN-CONC-WRITEOFF-1"
	_, _, err := svc.SerialEvent(ctx, nil, SerialOp{
		SerialNo: sn, SKUID: testKey().SKUID, WarehouseID: testKey().WarehouseID, BinID: testKey().BinID,
		Status: "IN_STOCK", Source: Source{Type: "it_inbound", No: "IN-SN-1"}, Actor: testActor(),
	})
	require.NoError(t, err)

	key := testKey()
	writeOff := func(order string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			states, err := svc.SerialStates(ctx, tx, key.SKUID, []string{sn})
			if err != nil {
				return err
			}
			inStock := false
			for _, st := range states {
				if st.SerialNo == sn && st.Status == "IN_STOCK" {
					inStock = true
				}
			}
			if !inStock {
				return response.NewError(response.CodeConflict, map[string]any{
					"reason": "序列号不在库（IN_STOCK 校验不通过，fail-closed）",
				})
			}
			_, _, err = svc.SerialEvent(ctx, tx, SerialOp{
				SerialNo: sn, SKUID: key.SKUID, WarehouseID: key.WarehouseID, BinID: key.BinID,
				Status: "OUTBOUND", Source: Source{Type: "it_ship", No: order}, Actor: testActor(),
			})
			return err
		})
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = writeOff(fmt.Sprintf("SH-SN-CONC-%d", idx))
		}(i)
	}
	wg.Wait()
	var success int
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		// 失败方必须是业务拒绝（校验不通过或 CAS 冲突），不允许基础设施崩溃。
		var bizErr *response.Error
		require.True(t, errors.As(err, &bizErr), "失败方应返回业务错误，实际: %v", err)
	}
	require.Equal(t, 1, success, "并发核销同一序列号恰一方成功（一物两卖窗口已封闭）")

	var status string
	require.NoError(t, db.Raw(`SELECT status FROM serial_numbers WHERE serial_no = ?`, sn).Scan(&status).Error)
	require.Equal(t, "OUTBOUND", status)
}
