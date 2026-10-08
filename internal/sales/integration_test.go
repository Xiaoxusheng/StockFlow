//go:build integration

// 真实 PostgreSQL 依赖的集成测试（testing.md §3/§4 M2 销售子集；默认 go test 不编译
// 本文件——单元测试零外部依赖约束）。运行：go test -tags integration ./internal/sales/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip）。
//
// 覆盖口径：本域十表 SQL（INSERT RETURNING / 状态守卫 UPDATE / jsonb reason /
// 部分唯一索引 / ILIKE 列表）、docnum 计数表发放与单据同事务、原子抢占的真实行级
// 互斥、幂等键重放。库存原语侧（Lock/Deduct/ReleaseLock 的 SQL 守卫与并发）由
// internal/inventory 的集成测试覆盖——本文件以 fakeStock（语义替身）注入 StockGateway，
// 避免跨包实现适配器；跨域装配（真实 inventory 适配器）属 router MT5 职责。
package sales

import (
	"fmt"
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
	"github.com/stockflow/server/internal/response"
)

func integrationEnv(t *testing.T) *harness {
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

	h := newHarness(t)
	// 生产仓储（真实 PG SQL）+ 语义替身网关/校验器。
	// 集成模式：服务与断言读都走真仓储（seedStock 落真 inventory 行，分配候选读真表）。
	// 断言读必须用真仓储——原 `h.repo`（内存替身）看不到真库写入，Len(...,1) 类断言恒空。
	h.realDB = db
	h.itRepo = newGormRepository(db)
	h.svc = NewService(h.itRepo,
		WithStock(h.stock),
		WithSKUAttr(h.skus),
		WithCustomerChecker(fakeCustomers{ok: true}),
		WithBinChecker(fakeBins{ok: true}),
		WithExceptions(h.exc),
		WithAudit(h.audit.audit), // 审计仍经 middleware.Audit 同形签名；此处探针便于断言
		WithNumbers(h.nums.next),
	)
	return h
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// itRunToken 本轮唯一令牌：拼进集成测试的幂等键，使同一 DB 重复运行不命中上一轮
// 遗留的 packing_records/shipments 幂等键（命中会被服务判为「重放」，
// require.False(res.Replay) 随之失败）。
var itRunToken = strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond)%1_000_000*1000+int64(os.Getpid()%1000), 10)

// itKey 本轮唯一幂等键（保持原键名可读性）。
func itKey(base string) string { return base + "-" + itRunToken }

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

// TestIntegrationPipeline 真实 PG 上的预占→核销全链（单据编号经 docnum 计数表、
// 状态守卫 UPDATE、jsonb 分配理由、打包幂等键唯一索引、发货 Deduct 核销）。
func TestIntegrationPipeline(t *testing.T) {
	h := integrationEnv(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	actor := Actor{ID: 9, Name: "it-user", RequestID: "it-req-1"}

	so, err := h.svc.CreateSalesOrder(h.ctx, actor, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1, DeliveryMethod: "快递",
		Items: []OrderItemInput{{LineNo: 1, SKUID: skuPlain, Qty: qtyOf(6), Price: qtyOf(2)}},
	})
	require.NoError(t, err)
	require.True(t, len(so.SoNo) > 0, "docnum 应发放 SO 单号")
	// 写端点响应体回读（2026-10-07 全流程实测缺陷回归）：GORM Raw().Scan(结构体) 会先
	// 把整个结构体置零，只回填 RETURNING 列——曾致创建响应 so_no/customer_id/status 全空。
	require.Equal(t, SOStatusDraft, so.Status, "创建响应 status 应为 DRAFT")
	require.Equal(t, int64(77), so.CustomerID, "创建响应 customer_id 应回读")
	require.Equal(t, int64(wh1), so.WarehouseID, "创建响应 warehouse_id 应回读")
	require.NotZero(t, so.ID.Int64(), "创建响应 id 应回填")
	_, err = h.svc.SubmitSalesOrder(h.ctx, actor, so.ID.Int64())
	require.NoError(t, err)
	res, err := h.svc.ApproveSalesOrder(h.ctx, actor, so.ID.Int64(), ApproveInput{Action: "APPROVE"})
	require.NoError(t, err)
	require.NotEmpty(t, res.OutboundNo, "docnum 应发放 OUT 单号")
	// 审核响应预占回执 + 订单行分配进度（分配记录 Qty/LockID 曾被 Scan 清零致双双失真）。
	require.Equal(t, 1, res.LockCount, "审核响应 lock_count 应等于预占锁行数")
	require.Equal(t, qtyOf(6), res.LockedQty, "审核响应 locked_qty 应为实际预占量")
	require.NotNil(t, res.Order)
	require.Equal(t, SOStatusApproved, res.Order.Status, "审核响应订单状态应为 APPROVED")
	soItems, err := h.itRepo.ListSalesOrderItems(h.realDB, so.ID.Int64())
	require.NoError(t, err)
	require.Len(t, soItems, 1)
	require.Equal(t, qtyOf(6), soItems[0].QtyAllocated, "审核后订单行 qty_allocated 应为预占量")

	// 分配记录 + jsonb 理由落库（真实 jsonb 列写入/回读）。
	allocs, err := h.itRepo.ListAllocationsByOutbound(h.realDB, res.OutboundNo)
	require.NoError(t, err)
	require.Len(t, allocs, 1)
	require.Equal(t, qtyOf(6), allocs[0].Qty)
	require.NotZero(t, allocs[0].LockID)
	require.Equal(t, AllocStrategyFIFO, allocs[0].Reason["strategy"])

	// 拣 → 复 → 包 → 发。
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	require.NoError(t, err)
	require.Len(t, picks, 1)
	// createPicks 响应体回读（同缺陷回归）：pick_no/status 曾被打包前的 Scan 清零。
	require.NotEmpty(t, picks[0].PickNo, "拣货任务响应 pick_no 应回读")
	require.Equal(t, PickStatusPending, picks[0].Status, "拣货任务响应 status 应为 PENDING")
	require.Equal(t, qtyOf(6), picks[0].Qty, "拣货任务响应 qty 应回读")
	require.NotZero(t, picks[0].ID.Int64(), "拣货任务响应 id 应回填")
	_, err = h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64())
	require.NoError(t, err)
	_, err = h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(6)})
	require.NoError(t, err)
	checks, err := h.itRepo.ListCheckTasksByOutbound(h.realDB, res.OutboundNo)
	require.NoError(t, err)
	_, err = h.svc.ClaimCheckTask(h.ctx, actor, checks[0].ID.Int64())
	require.NoError(t, err)
	_, err = h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true})
	require.NoError(t, err)
	packRes, err := h.svc.Pack(h.ctx, actor, PackInput{OutboundNo: res.OutboundNo,
		Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(6)}}, IdempotencyKey: itKey("it-bp-1")})
	require.NoError(t, err)
	// 打包响应体回读：package_no 曾为空串。
	require.NotEmpty(t, packRes.Package.PackageNo, "打包响应 package_no 应回读")
	require.Equal(t, res.OutboundNo, packRes.Package.OutboundNo, "打包响应 outbound_no 应回读")
	require.NotZero(t, packRes.Package.ID.Int64(), "打包响应 id 应回填")
	shipRes, err := h.svc.Ship(h.ctx, actor, ShipInput{OutboundNo: res.OutboundNo,
		Carrier: "SF", TrackingNo: "SF-IT-1", IdempotencyKey: itKey("it-ship-1")})
	require.NoError(t, err)
	require.False(t, shipRes.Replay)
	// 发货响应体回读：shipment_no 曾为空串。
	require.NotEmpty(t, shipRes.Shipment.ShipmentNo, "发货响应 shipment_no 应回读")
	require.Equal(t, res.OutboundNo, shipRes.Shipment.OutboundNo, "发货响应 outbound_no 应回读")

	// 库存核销（fakeStock 守卫）+ 单据终态（真实 PG 守卫 UPDATE）。
	_, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	require.Zero(t, int64(locked))
	require.Equal(t, int64(40000), int64(total))
	ob, err := h.itRepo.GetOutboundOrderByNo(h.realDB, res.OutboundNo)
	require.NoError(t, err)
	require.Equal(t, OBStatusShippedAll, ob.Status)
	require.False(t, ob.ShippedAt.IsZero(), "出库单 shipped_at 应落列")

	// 幂等重放（真实 PG 的 shipments.idempotency_key 唯一索引路径）。
	replay, err := h.svc.Ship(h.ctx, actor, ShipInput{OutboundNo: res.OutboundNo, IdempotencyKey: itKey("it-ship-1")})
	require.NoError(t, err)
	require.True(t, replay.Replay)
	require.Equal(t, shipRes.Shipment.ShipmentNo, replay.Shipment.ShipmentNo)

	// 发货单物流态流转（纯记录）。
	st, err := h.svc.UpdateShipmentStatus(h.ctx, actor, shipRes.Shipment.ID.Int64(), ShipStatusInput{Status: "IN_TRANSIT"})
	require.NoError(t, err)
	require.Equal(t, ShipStatusInTransit, st.Status)
}

// TestIntegrationConcurrentClaim 真实 PG 行级 UPDATE 原子性：16 路并发领取恰 1 成功
// （testing.md §4；单元替身只验证服务级契约，真实互斥在此验证）。
func TestIntegrationConcurrentClaim(t *testing.T) {
	h := integrationEnv(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
	actor := Actor{ID: 9, Name: "it-claim"}
	_, res := h.mustApprove(t, actor, skuPlain, qtyOf(4))
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	require.NoError(t, err)

	const racers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	winners := make(chan struct{}, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			if _, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: int64(200 + id), Name: fmt.Sprintf("p%d", id)},
				picks[0].ID.Int64()); err == nil {
				winners <- struct{}{}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(winners)
	n := 0
	for range winners {
		n++
	}
	require.Equal(t, 1, n, "真实 PG 行级守卫下应恰一路领取成功")
}

// TestIntegrationPackingIdempotencyUniqueIndex 打包幂等键：重放返回既有包裹且
// packing_records 不产生第二行（部分唯一索引兜底）。
func TestIntegrationPackingIdempotencyUniqueIndex(t *testing.T) {
	h := integrationEnv(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
	actor := Actor{ID: 9, Name: "it-pack"}
	_, res := h.mustApprove(t, actor, skuPlain, qtyOf(2))
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	require.NoError(t, err)
	_, err = h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64())
	require.NoError(t, err)
	_, err = h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(2)})
	require.NoError(t, err)
	checks, err := h.itRepo.ListCheckTasksByOutbound(h.realDB, res.OutboundNo)
	require.NoError(t, err)
	_, err = h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true})
	require.NoError(t, err)

	in := PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(2)}}, IdempotencyKey: itKey("it-pack-key")}
	r1, err := h.svc.Pack(h.ctx, actor, in)
	require.NoError(t, err)
	r2, err := h.svc.Pack(h.ctx, actor, in)
	require.NoError(t, err)
	require.True(t, r2.Replay)
	require.Equal(t, r1.Package.PackageNo, r2.Package.PackageNo)
	pkgs, err := h.itRepo.ListPackagesByOutbound(h.realDB, res.OutboundNo)
	require.NoError(t, err)
	require.Len(t, pkgs, 1, "幂等键下不产生第二个包裹行")
}

// mustApprove 提交+审核一步到位（集成测试用）。
func (h *harness) mustApprove(t *testing.T, actor Actor, skuID int64, qty Qty) (*SalesOrder, *ApproveResult) {
	t.Helper()
	so, err := h.svc.CreateSalesOrder(h.ctx, actor, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{{LineNo: 1, SKUID: skuID, Qty: qty}},
	})
	require.NoError(t, err)
	_, err = h.svc.SubmitSalesOrder(h.ctx, actor, so.ID.Int64())
	require.NoError(t, err)
	res, err := h.svc.ApproveSalesOrder(h.ctx, actor, so.ID.Int64(), ApproveInput{Action: "APPROVE"})
	require.NoError(t, err)
	return res.Order, res
}

// 集成构建下保留对 response 包的显式依赖说明（域错误码注册于 errors.go）。
var _ = response.CodeConflict
var _ = gorm.ErrRecordNotFound
