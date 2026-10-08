package sales

// Service 层单测（ask 指定矩阵）：状态机矩阵、预占→核销全链、并发抢占互斥、
// 超卖拒绝、幂等重放——全部经内存替身（fakes_test.go），不依赖 PostgreSQL/Redis。
// 真实 PostgreSQL 下的 SQL/事务/并发行为见 integration_test.go（//go:build integration）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// harness 单测装配（替身 + Service）。
type harness struct {
	repo  *fakeRepo
	stock *fakeStock
	skus  *fakeSKUs
	exc   *fakeExceptions
	audit *spyAudit
	nums  *fakeNumbers
	svc   *Service
	ctx   context.Context

	// realDB 非空 = 集成模式（服务走 GORM 仓储 + 真库存表）。单测恒为 nil。
	// seedStock 据此额外落真 inventory 行——否则分配阶段 ReadBinStock（真 SQL）
	// 找不到候选库位 → APPROVE 报 INVENTORY_NOT_ENOUGH。
	realDB *gorm.DB
	// itRepo 真仓储（集成模式）：集成用例断言读真库（服务写入的目标）。
	// 恒以 h.realDB 作 tx 传入（真仓储读方法直接 tx.Raw，传 nil 会 panic）。
	itRepo Repo
	t      *testing.T
}

const (
	wh1       = 11
	bin1      = 101
	bin2      = 102
	skuPlain  = 501 // 非批次
	skuBatch  = 502 // 批次 + 效期（FEFO）
	skuFIFO   = 503 // 批次非效期（FIFO）
	skuSerial = 504 // 批次 + 效期 + 序列号
)

func qtyOf(v int64) Qty { return Qty(v * 10000) } // 1 单位 = 0.0001

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		repo:  newFakeRepo(),
		stock: newFakeStock(),
		skus: newFakeSKUs(map[int64]SKUFlags{
			skuPlain:  {Enabled: true},
			skuBatch:  {Enabled: true, BatchManaged: true, ExpiryManaged: true},
			skuFIFO:   {Enabled: true, BatchManaged: true},
			skuSerial: {Enabled: true, BatchManaged: true, ExpiryManaged: true, SerialManaged: true},
		}),
		exc:   &fakeExceptions{},
		audit: &spyAudit{},
		nums:  newFakeNumbers(),
		ctx:   context.Background(),
	}
	h.svc = NewService(h.repo,
		WithStock(h.stock),
		WithSKUAttr(h.skus),
		WithCustomerChecker(fakeCustomers{ok: true}),
		WithBinChecker(fakeBins{ok: true}),
		WithExceptions(h.exc),
		WithAudit(h.audit.audit),
		WithNumbers(h.nums.next),
	)
	return h
}

// seedStock 同步塞库存：fakeStock（守卫语义）+ fakeRepo.binStock（候选/库位定位读）。
// 集成模式（realDB 非空）额外 upsert 真 inventory 行——集成 harness 的服务走 GORM
// 仓储，分配阶段的 ReadBinStock/ReadBinLocation 读真表，不落真行必 INVENTORY_NOT_ENOUGH。
// upsert 用绝对值改写（非累加）：同一 DB 重复运行可复位到种子量，断言绝对数不随轮次漂移。
func (h *harness) seedStock(wh, bin, sku, batch int64, avail Qty, zone, shelf int64) {
	h.stock.Seed(wh, bin, sku, batch, avail)
	key := binKey(wh, sku, batch)
	h.repo.binStock[key] = append(h.repo.binStock[key], BinStock{
		WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku, BatchID: batch, AvailableQty: avail,
	})
	if h.realDB == nil {
		return
	}
	require.NoError(h.t, h.realDB.Exec(`
		INSERT INTO inventory (warehouse_id, zone_id, shelf_id, bin_id, sku_id, batch_id,
			total_qty, available_qty, locked_qty, frozen_qty, pending_inspect_qty, defective_qty,
			created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, now(), now(), 0, 0)
		ON CONFLICT (warehouse_id, bin_id, sku_id, batch_id) DO UPDATE
		SET total_qty = EXCLUDED.total_qty, available_qty = EXCLUDED.available_qty,
		    locked_qty = 0, frozen_qty = 0, pending_inspect_qty = 0, defective_qty = 0,
		    zone_id = EXCLUDED.zone_id, shelf_id = EXCLUDED.shelf_id, updated_at = now()`,
		wh, zone, shelf, bin, sku, batch, avail.String(), avail.String()).Error)
}

// seedBatches 批次候选（FEFO/FIFO 纯函数输入）。
func (h *harness) seedBatches(wh, sku int64, cands []BatchCandidate) {
	h.repo.batchCands[fmt.Sprintf("%d:%d", wh, sku)] = cands
}

func (h *harness) createOrder(t *testing.T, skuID int64, qty Qty) *SalesOrder {
	t.Helper()
	o, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9, Name: "creator"}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{{SKUID: skuID, Qty: qty, Price: qtyOf(2)}},
	})
	if err != nil {
		t.Fatalf("创建订单失败: %v", err)
	}
	return o
}

func (h *harness) submitAndApprove(t *testing.T, skuID int64, qty Qty) (*SalesOrder, *ApproveResult) {
	t.Helper()
	o := h.createOrder(t, skuID, qty)
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, o.ID.Int64()); err != nil {
		t.Fatalf("提交订单失败: %v", err)
	}
	res, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8, Name: "approver"}, o.ID.Int64(), ApproveInput{Action: "APPROVE"})
	if err != nil {
		t.Fatalf("审核订单失败: %v", err)
	}
	return res.Order, res
}

// codeOf 提取注册错误码字符串（response.Error.Error() 形如 "CODE: message"）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *response.Error
	if errors.As(err, &e) {
		s := e.Error()
		if i := strings.Index(s, ":"); i > 0 {
			return s[:i]
		}
	}
	return ""
}

// ---- 1. 状态机矩阵（business-flow §13.2：合法迁移通过、非法迁移 409） ----

func TestStateMachineMatrix(t *testing.T) {
	for from, tos := range soTransitions {
		for _, to := range tos {
			if !canTransition(soTransitions, from, to) {
				t.Errorf("销售订单合法迁移 %s→%s 被拒", from, to)
			}
		}
	}
	for from, tos := range obTransitions {
		for _, to := range tos {
			if !canTransition(obTransitions, from, to) {
				t.Errorf("出库单合法迁移 %s→%s 被拒", from, to)
			}
		}
	}
	for from, tos := range pickTransitions {
		for _, to := range tos {
			if !canTransition(pickTransitions, from, to) {
				t.Errorf("拣货任务合法迁移 %s→%s 被拒", from, to)
			}
		}
	}
	for from, tos := range shipTransitions {
		for _, to := range tos {
			if !canTransition(shipTransitions, from, to) {
				t.Errorf("发货单合法迁移 %s→%s 被拒", from, to)
			}
		}
	}
	// 非法迁移抽样：跳状态、终态出边。
	illegal := [][3]string{
		{SOStatusDraft, SOStatusApproved, "so"},
		{SOStatusApproved, SOStatusPendingApproval, "so"},
		{SOStatusShippedAll, SOStatusCompleted, "so"},
		{SOStatusCancelled, SOStatusDraft, "so"},
		{OBStatusAllocated, OBStatusPicked, "ob"},
		{OBStatusPicked, OBStatusPacked, "ob"},
		{OBStatusShippedAll, OBStatusCancelled, "ob"},
		{OBStatusClosed, OBStatusPacked, "ob"},
		{PickStatusPicked, PickStatusClaimed, "pick"},
		{ShipStatusPending, ShipStatusSigned, "ship"},
		{ShipStatusSigned, ShipStatusInTransit, "ship"},
	}
	for _, tc := range illegal {
		var table map[string][]string
		switch tc[2] {
		case "so":
			table = soTransitions
		case "ob":
			table = obTransitions
		case "pick":
			table = pickTransitions
		case "ship":
			table = shipTransitions
		}
		if canTransition(table, tc[0], tc[1]) {
			t.Errorf("非法迁移 %s→%s（%s）被放行", tc[0], tc[1], tc[2])
		}
	}
}

// 服务级状态守卫：重复审核同一订单（第二次已非 PENDING_APPROVAL）→ 409。
func TestApproveTwiceRejected(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(5), 1, 11)
	o := h.createOrder(t, skuPlain, qtyOf(5))
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, o.ID.Int64()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "APPROVE"}); err != nil {
		t.Fatalf("首次审核失败: %v", err)
	}
	_, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "APPROVE"})
	if codeOf(t, err) != "SALES_STATE_CONFLICT" {
		t.Fatalf("重复审核应 409 SALES_STATE_CONFLICT，got %v", err)
	}
}

// 非法动作入参。
func TestApproveInvalidAction(t *testing.T) {
	h := newHarness(t)
	o := h.createOrder(t, skuPlain, qtyOf(5))
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, o.ID.Int64()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	_, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "MAYBE"})
	if err == nil {
		t.Fatal("非法 action 应被拒绝")
	}
}

// ---- 2. 预占→核销全链（审核预占 → 拣 → 复 → 包 → 发 → Deduct 核销） ----

func TestFullPipelineHoldToDeduct(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	h.seedBatches(wh1, skuPlain, nil)

	so, res := h.submitAndApprove(t, skuPlain, qtyOf(6))
	if res.OutboundNo == "" {
		t.Fatal("审核应联动创建出库单")
	}
	avail, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if avail != qtyOf(4) || locked != qtyOf(6) {
		t.Fatalf("预占后库存不符: avail=%s locked=%s", avail, locked)
	}
	// 分配记录锁定量 = 分配量（plan §6.4）。
	allocs, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)
	if len(allocs) != 1 || allocs[0].Qty != qtyOf(6) || allocs[0].LockID == 0 {
		t.Fatalf("分配记录不符: %+v", allocs)
	}
	if allocs[0].Strategy != AllocStrategyFIFO {
		t.Fatalf("非批次 SKU 策略应退化 FIFO，got %s", allocs[0].Strategy)
	}
	// 出库单已 ALLOCATED；订单行预占进度落列。
	items, _ := h.repo.ListSalesOrderItems(nil, so.ID.Int64())
	if items[0].QtyAllocated != qtyOf(6) {
		t.Fatalf("订单行 qty_allocated 不符: %s", items[0].QtyAllocated)
	}

	actor := Actor{ID: 7, Name: "picker"}
	// 生成拣货任务 → ALLOCATED→PICKING。
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	if len(picks) != 1 || picks[0].SourceBinID != bin1 {
		t.Fatalf("拣货任务不符: %+v", picks)
	}
	// 领取 → 确认。
	if _, err := h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64()); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if _, err := h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(6)}); err != nil {
		t.Fatalf("拣货确认失败: %v", err)
	}
	// 出库单 PICKED；复核任务已联动创建。
	if ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo); ob.Status != OBStatusPicked {
		t.Fatalf("拣齐后出库单应 PICKED，got %s", ob.Status)
	}
	checks, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	if len(checks) != 1 {
		t.Fatalf("应联动创建 1 张复核任务，got %d", len(checks))
	}
	// 复核领取（原子指派）→ 通过 → CHECKED。
	if _, err := h.svc.ClaimCheckTask(h.ctx, Actor{ID: 6, Name: "checker"}, checks[0].ID.Int64()); err != nil {
		t.Fatalf("复核领取失败: %v", err)
	}
	if _, err := h.svc.ConfirmCheck(h.ctx, Actor{ID: 6}, checks[0].ID.Int64(), CheckConfirmInput{Pass: true}); err != nil {
		t.Fatalf("复核确认失败: %v", err)
	}
	// 打包 → PACKED。
	if _, err := h.svc.Pack(h.ctx, Actor{ID: 5, Name: "packer"}, PackInput{
		OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(6)}},
	}); err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	// 发货确认 → Deduct 核销（locked↓ total↓，锁 CONSUMED）。
	shipRes, err := h.svc.Ship(h.ctx, Actor{ID: 4, Name: "shipper"}, ShipInput{
		OutboundNo: res.OutboundNo, Carrier: "SF", TrackingNo: "SF123",
	})
	if err != nil {
		t.Fatalf("发货失败: %v", err)
	}
	if shipRes.Replay {
		t.Fatal("首次发货不应是重放")
	}
	avail, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if avail != qtyOf(4) || locked != 0 || total != qtyOf(4) {
		t.Fatalf("发货后库存不符: avail=%s locked=%s total=%s", avail, locked, total)
	}
	if l := h.stock.LockByID(allocs[0].LockID); l == nil || l.Status != "CONSUMED" {
		t.Fatalf("锁应已核销 CONSUMED，got %+v", l)
	}
	// 出库单 SHIPPED_ALL、销售订单 SHIPPED_ALL、发货单 SHIPPED。
	ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo)
	if ob.Status != OBStatusShippedAll {
		t.Fatalf("出库单应 SHIPPED_ALL，got %s", ob.Status)
	}
	so2, _ := h.repo.GetSalesOrderByNo(nil, so.SoNo)
	if so2.Status != SOStatusShippedAll {
		t.Fatalf("销售订单应 SHIPPED_ALL，got %s", so2.Status)
	}
	if sh := shipRes.Shipment; sh.Status != ShipStatusShipped || sh.ShippedAt.IsZero() {
		t.Fatalf("发货单状态/时间不符: %+v", sh)
	}
	// 审计断言：审核与发货均同事务写 operation_logs（before/after 快照）。
	if h.audit.count("approve") == 0 || h.audit.count("ship") == 0 {
		t.Fatalf("审计缺位: approve=%d ship=%d", h.audit.count("approve"), h.audit.count("ship"))
	}
}

// ---- 3. 并发抢占互斥（两路并发领取同一拣货任务，恰一路成功） ----

func TestConcurrentClaimExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(4))
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, Actor{ID: 7}, res.OutboundNo)
	if err != nil {
		t.Fatalf("生成任务失败: %v", err)
	}

	const racers = 16
	var wg sync.WaitGroup
	winners := make(chan int, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			_, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: int64(100 + id), Name: "p"}, picks[0].ID.Int64())
			if err == nil {
				winners <- id
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
	if n != 1 {
		t.Fatalf("并发领取应恰一路成功，got %d", n)
	}
}

// ---- 4. 超卖拒绝（两单竞争不足库存：第二单审核整体回滚、停留 PENDING_APPROVAL） ----

func TestOversellRejected(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(15), 1, 11)

	// 第一单 10：成功预占。
	_, res1 := h.submitAndApprove(t, skuPlain, qtyOf(10))
	if res1.OutboundNo == "" {
		t.Fatal("第一单应审核成功")
	}
	avail, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != qtyOf(10) || avail != qtyOf(5) {
		t.Fatalf("第一单预占不符: avail=%s locked=%s", avail, locked)
	}
	// 第二单 10：可用仅 5 → INVENTORY_NOT_ENOUGH，整体回滚。
	so2 := h.createOrder(t, skuPlain, qtyOf(10))
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, so2.ID.Int64()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	_, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, so2.ID.Int64(), ApproveInput{Action: "APPROVE"})
	if codeOf(t, err) != "INVENTORY_NOT_ENOUGH" {
		t.Fatalf("超卖应返回 INVENTORY_NOT_ENOUGH，got %v", err)
	}
	// 回滚完整性：订单停留 PENDING_APPROVAL；无出库单、无分配记录、无锁增量。
	got2, _ := h.repo.GetSalesOrderByNo(nil, so2.SoNo)
	if got2.Status != SOStatusPendingApproval {
		t.Fatalf("预占失败订单应停留 PENDING_APPROVAL，got %s", got2.Status)
	}
	if obs, _ := h.repo.ListOutboundOrdersBySO(nil, so2.SoNo); len(obs) != 0 {
		t.Fatalf("回滚后不应残留出库单: %d", len(obs))
	}
	if allocs, _ := h.repo.ListAllocationsByOutbound(nil, res1.OutboundNo); len(allocs) == 0 {
		// 第一单的分配仍在（不受第二单回滚影响）。
		t.Fatal("第一单分配记录不应被回滚波及")
	}
	avail, locked, _ = h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != qtyOf(10) || avail != qtyOf(5) {
		t.Fatalf("回滚后库存应保持第一单预占态: avail=%s locked=%s", avail, locked)
	}
}

// ---- 5. 幂等重放 ----

// 5a. 发货幂等键重放：同键第二次 Ship 返回既有发货单，库存只扣一次。
func TestShipIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(8), 1, 11)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(8))
	actor := Actor{ID: 7}
	_, picks, _ := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64())
	h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(8)})
	checks, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true})
	h.svc.Pack(h.ctx, actor, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(8)}}})

	in := ShipInput{OutboundNo: res.OutboundNo, IdempotencyKey: "ship-idem-1"}
	r1, err := h.svc.Ship(h.ctx, actor, in)
	if err != nil {
		t.Fatalf("首次发货失败: %v", err)
	}
	r2, err := h.svc.Ship(h.ctx, actor, in)
	if err != nil {
		t.Fatalf("重放发货失败: %v", err)
	}
	if !r2.Replay || r2.Shipment.ShipmentNo != r1.Shipment.ShipmentNo {
		t.Fatalf("重放应返回既有发货单: %+v vs %+v", r2, r1)
	}
	_, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != 0 || total != qtyOf(0) {
		t.Fatalf("重放后库存不应再变: locked=%s total=%s", locked, total)
	}
	if total := h.nums.counts["SH"]; total != 1 {
		t.Fatalf("重放不应再次取号（SH=%d）", total)
	}
}

// 5b. 显式幂等键缺失时的防重：状态守卫兜底（发货后无可发明细 → 拒绝，不再扣减）。
func TestShipDoubleSubmitGuarded(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(5), 1, 11)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(5))
	actor := Actor{ID: 7}
	_, picks, _ := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64())
	h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(5)})
	checks, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true})
	h.svc.Pack(h.ctx, actor, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(5)}}})

	if _, err := h.svc.Ship(h.ctx, actor, ShipInput{OutboundNo: res.OutboundNo}); err != nil {
		t.Fatalf("发货失败: %v", err)
	}
	_, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != 0 || total != 0 {
		t.Fatalf("首次发货后库存异常: locked=%s total=%s", locked, total)
	}
	_, err := h.svc.Ship(h.ctx, actor, ShipInput{OutboundNo: res.OutboundNo})
	if codeOf(t, err) != "SALES_STATE_CONFLICT" {
		t.Fatalf("无幂等键的重复发货应被状态守卫拒绝（已 SHIPPED_ALL），got %v", err)
	}
	_, locked, total = h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != 0 || total != 0 {
		t.Fatalf("重复发货后库存不应变化: locked=%s total=%s", locked, total)
	}
}

// 5c. 锁原语层幂等：同键重 Lock 返回既有锁（Replay），库存不变。
func TestLockPrimitiveReplay(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(5), 1, 11)
	m1, err := h.stock.Lock(h.ctx, nil, LockOp{
		Key: RowKey{WarehouseID: wh1, BinID: bin1, SKUID: skuPlain},
		Qty: qtyOf(2), LockType: "ORDER_HOLD",
		Source: Source{Type: "sales_order", No: "SO-1"}, Actor: Actor{ID: 1},
		IdempotencyKey: "lock:SO-1:1:101:501:0",
	})
	if err != nil || m1.Replay {
		t.Fatalf("首次 Lock 失败: %+v %v", m1, err)
	}
	m2, err := h.stock.Lock(h.ctx, nil, LockOp{
		Key: RowKey{WarehouseID: wh1, BinID: bin1, SKUID: skuPlain},
		Qty: qtyOf(2), LockType: "ORDER_HOLD",
		Source: Source{Type: "sales_order", No: "SO-1"}, Actor: Actor{ID: 1},
		IdempotencyKey: "lock:SO-1:1:101:501:0",
	})
	if err != nil {
		t.Fatalf("重放 Lock 失败: %v", err)
	}
	if !m2.Replay || m2.LockID != m1.LockID {
		t.Fatalf("重放应返回既有锁: %+v vs %+v", m2, m1)
	}
	_, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if locked != qtyOf(2) {
		t.Fatalf("重放后锁量不应翻倍: %s", locked)
	}
}

// ---- FEFO/FIFO 策略与批次分配（business-flow §8.1） ----

func TestBatchAllocationFEFOAndFIFO(t *testing.T) {
	h := newHarness(t)
	// 批次 B2 效期早（先出），B3 无效期（末位）；库存分两 bin。
	h.seedStock(wh1, bin2, skuBatch, 3002, qtyOf(3), 1, 12)
	h.seedStock(wh1, bin1, skuBatch, 3002, qtyOf(3), 1, 11)
	h.seedStock(wh1, bin1, skuBatch, 3003, qtyOf(10), 1, 11)
	h.seedBatches(wh1, skuBatch, []BatchCandidate{
		{BatchID: 3003, BatchNo: "B3", AvailableQty: qtyOf(10)},
		{BatchID: 3002, BatchNo: "B2", AvailableQty: qtyOf(6)},
	})
	so, res := h.submitAndApprove(t, skuBatch, qtyOf(8))
	_ = so
	allocs, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)
	if len(allocs) != 3 {
		t.Fatalf("FEFO 应产出三条分配记录（B2×2bin + B3×1bin），got %d", len(allocs))
	}
	// 先耗先到期批次 B2（批次内按库位升序 bin1→bin2），余量落 B3。
	if allocs[0].BatchID != 3002 || allocs[0].BinID != bin1 || allocs[0].Qty != qtyOf(3) {
		t.Fatalf("FEFO 第 1 步应为 B2@bin1×3: %+v", allocs[0])
	}
	if allocs[1].BatchID != 3002 || allocs[1].BinID != bin2 || allocs[1].Qty != qtyOf(3) {
		t.Fatalf("FEFO 第 2 步应为 B2@bin2×3: %+v", allocs[1])
	}
	if allocs[2].BatchID != 3003 || allocs[2].Qty != qtyOf(2) {
		t.Fatalf("FEFO 余量应落在 B3×2: %+v", allocs[2])
	}
	// 先到期批次内部：bin_id 升序消耗（bin1 < bin2）。
	if allocs[0].BinID != bin1 {
		t.Fatalf("同批次应按库位升序消耗，got bin %d", allocs[0].BinID)
	}
	if allocs[0].Strategy != AllocStrategyFEFO {
		t.Fatalf("策略应为 FEFO，got %s", allocs[0].Strategy)
	}
	// 分配理由（§8.1 展示义务）：策略命中原因 + 可用量快照。
	if allocs[0].Reason["strategy"] != AllocStrategyFEFO || allocs[0].Reason["batch_reason"] == "" {
		t.Fatalf("分配理由缺失: %+v", allocs[0].Reason)
	}
}

func TestBatchAllocationFIFO(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuFIFO, 4001, qtyOf(2), 1, 11)
	h.seedStock(wh1, bin1, skuFIFO, 4002, qtyOf(9), 1, 11)
	h.seedBatches(wh1, skuFIFO, []BatchCandidate{
		{BatchID: 4001, BatchNo: "F1", AvailableQty: qtyOf(2)},
		{BatchID: 4002, BatchNo: "F2", AvailableQty: qtyOf(9)},
	})
	_, res := h.submitAndApprove(t, skuFIFO, qtyOf(5))
	allocs, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)
	if len(allocs) != 2 || allocs[0].BatchID != 4001 || allocs[0].Qty != qtyOf(2) || allocs[1].Qty != qtyOf(3) {
		t.Fatalf("FIFO 应先耗 F1 再耗 F2: %+v", allocs)
	}
	if allocs[0].Strategy != AllocStrategyFIFO {
		t.Fatalf("非效期批次 SKU 策略应为 FIFO，got %s", allocs[0].Strategy)
	}
}

// ---- 取消释放（ReleaseLock，inventory-rules §4.2） ----

func TestCancelOrderReleasesLocks(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	so, res := h.submitAndApprove(t, skuPlain, qtyOf(6))
	allocs, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)

	if _, err := h.svc.CancelSalesOrder(h.ctx, Actor{ID: 9}, so.ID.Int64(), CancelInput{Reason: "客户取消"}); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	avail, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0)
	if avail != qtyOf(10) || locked != 0 {
		t.Fatalf("取消后锁应全部释放: avail=%s locked=%s", avail, locked)
	}
	if l := h.stock.LockByID(allocs[0].LockID); l == nil || l.Status != "RELEASED" {
		t.Fatalf("锁记录应 RELEASED，got %+v", l)
	}
	ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo)
	if ob.Status != OBStatusCancelled {
		t.Fatalf("联动出库单应 CANCELLED，got %s", ob.Status)
	}
	// 已发货后（APPROVED→发过货）不可取消走关闭：直接断言 PARTIAL_SHIPPED 的 SO 不能取消——
	// 用状态机矩阵覆盖，这里补一个 APPROVED+有锁重分配场景见 Reallocate 测试。
}

// ---- 重新分配（释放旧锁 + relock 新锁 + 记录替换，同事务） ----

func TestReallocate(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
	h.seedStock(wh1, bin2, skuPlain, 0, qtyOf(6), 1, 12)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(5))
	allocs, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)
	if len(allocs) != 2 { // bin1(4) + bin2(1)
		t.Fatalf("初次分配应跨两库位: %+v", allocs)
	}
	oldLockIDs := []int64{allocs[0].LockID, allocs[1].LockID}

	// bin1 库存"消失"（模拟物理缺货），重分配后全部落在 bin2。
	h.repo.binStock[binKey(wh1, skuPlain, 0)] = []BinStock{{
		WarehouseID: wh1, ZoneID: 1, ShelfID: 12, BinID: bin2, SKUID: skuPlain, AvailableQty: qtyOf(6),
	}}
	recs, err := h.svc.Reallocate(h.ctx, Actor{ID: 7}, ReallocateInput{OutboundNo: res.OutboundNo})
	if err != nil {
		t.Fatalf("重新分配失败: %v", err)
	}
	if len(recs) != 1 || recs[0].BinID != bin2 || recs[0].Qty != qtyOf(5) {
		t.Fatalf("重分配应全部落 bin2（5 件）: %+v", recs)
	}
	// 旧锁释放、新锁产生且锁量守恒。
	for _, id := range oldLockIDs {
		if l := h.stock.LockByID(id); l == nil || l.Status == "ACTIVE" {
			t.Fatalf("旧锁 %d 应已释放: %+v", id, l)
		}
	}
	_, locked, _ := h.stock.Snapshot(wh1, bin2, skuPlain, 0)
	if locked != qtyOf(5) {
		t.Fatalf("重分配后锁量不符: %s", locked)
	}
	// 记录整组替换。
	after, _ := h.repo.ListAllocationsByOutbound(nil, res.OutboundNo)
	if len(after) != 1 {
		t.Fatalf("allocation_records 应整组替换，got %d 条", len(after))
	}
}

// ---- 部分发货（PARTIAL_SHIPPED 中间态 + 差额关闭释放剩余锁，business-flow §14/§13.3） ----

func TestPartialShipAndClose(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
	h.seedStock(wh1, bin2, skuFIFO, 4001, qtyOf(4), 1, 12)
	h.seedBatches(wh1, skuFIFO, []BatchCandidate{{BatchID: 4001, BatchNo: "F1", AvailableQty: qtyOf(4)}})
	o, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{
			{LineNo: 1, SKUID: skuPlain, Qty: qtyOf(2)},
			{LineNo: 2, SKUID: skuFIFO, Qty: qtyOf(3)},
		},
	})
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, o.ID.Int64()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	res, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "APPROVE"})
	if err != nil {
		t.Fatalf("审核失败: %v", err)
	}
	a := Actor{ID: 7}
	_, p, err := h.svc.GeneratePickTasks(h.ctx, a, res.OutboundNo)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	for _, task := range p {
		h.svc.ClaimPickTask(h.ctx, a, task.ID.Int64())
		h.svc.ConfirmPick(h.ctx, a, task.ID.Int64(), PickConfirmInput{PickedQty: task.Qty})
	}
	checks, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	for _, ck := range checks {
		h.svc.ConfirmCheck(h.ctx, a, ck.ID.Int64(), CheckConfirmInput{Pass: true})
	}
	h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{
		{LineNo: 1, Qty: qtyOf(2)}, {LineNo: 2, Qty: qtyOf(3)},
	}})
	// 只发行 1 → 出库单与销售订单 PARTIAL_SHIPPED；行 2 锁仍 ACTIVE。
	r1, err := h.svc.Ship(h.ctx, a, ShipInput{OutboundNo: res.OutboundNo, Lines: []ShipLineInput{{LineNo: 1}}})
	if err != nil {
		t.Fatalf("部分发货失败: %v", err)
	}
	if ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo); ob.Status != OBStatusPartialShipped {
		t.Fatalf("部分发货后出库单应 PARTIAL_SHIPPED，got %s", ob.Status)
	}
	so, _ := h.repo.GetSalesOrderByNo(nil, o.SoNo)
	if so.Status != SOStatusPartialShipped {
		t.Fatalf("部分发货后销售订单应 PARTIAL_SHIPPED，got %s", so.Status)
	}
	_, locked2, _ := h.stock.Snapshot(wh1, bin2, skuFIFO, 4001)
	if locked2 != qtyOf(3) {
		t.Fatalf("未发货行锁应保留: %s", locked2)
	}
	// 已发行重发货 → 拒绝。
	if _, err := h.svc.Ship(h.ctx, a, ShipInput{OutboundNo: res.OutboundNo, Lines: []ShipLineInput{{LineNo: 1}}}); codeOf(t, err) != "SALES_SHIP_QTY_INVALID" {
		t.Fatalf("已发行重发货应拒绝，got %v", err)
	}
	// 差额关闭出库单（PARTIAL_SHIPPED→CLOSED）：剩余锁释放。
	if _, err := h.svc.CloseOutbound(h.ctx, a, res.OutboundNo, CloseInput{Reason: "余量客户不要了"}); err != nil {
		t.Fatalf("关闭出库单失败: %v", err)
	}
	_, locked2, _ = h.stock.Snapshot(wh1, bin2, skuFIFO, 4001)
	if locked2 != 0 {
		t.Fatalf("关闭后剩余锁应释放: %s", locked2)
	}
	if ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo); ob.Status != OBStatusClosed {
		t.Fatalf("出库单应 CLOSED，got %s", ob.Status)
	}
	// 销售订单差额关闭（PARTIAL_SHIPPED→COMPLETED）。
	if _, err := h.svc.CloseSalesOrder(h.ctx, a, o.ID.Int64(), CloseInput{Reason: "差额关闭"}); err != nil {
		t.Fatalf("关闭销售订单失败: %v", err)
	}
	if so, _ = h.repo.GetSalesOrderByNo(nil, o.SoNo); so.Status != SOStatusCompleted {
		t.Fatalf("销售订单应 COMPLETED，got %s", so.Status)
	}
	_ = r1
}

// ---- 序列号 SKU：拣货逐件校验 + 复核逐件 + 发货逐件核销 ----

func TestSerialSKUPipeline(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuSerial, 5001, qtyOf(3), 1, 11)
	h.seedBatches(wh1, skuSerial, []BatchCandidate{{BatchID: 5001, BatchNo: "S1", AvailableQty: qtyOf(3)}})
	// 序列号台账（在库，位于 bin1）。
	for _, sn := range []string{"SN-A", "SN-B"} {
		h.repo.serials[sn] = SerialState{SerialNo: sn, SKUID: skuSerial, BatchID: 5001, WarehouseID: wh1, BinID: bin1, Status: "IN_STOCK"}
	}
	_, res := h.submitAndApprove(t, skuSerial, qtyOf(2))
	actor := Actor{ID: 7}
	_, picks, _ := h.svc.GeneratePickTasks(h.ctx, actor, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, actor, picks[0].ID.Int64())

	// 序列号 SKU 不带序列号确认 → 拒绝。
	if _, err := h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(2)}); codeOf(t, err) != "SALES_SERIALS_MISSING" {
		t.Fatalf("序列号 SKU 缺序列号应拒绝，got %v", err)
	}
	// 序列号不在来源库位 → 拒绝。
	{
		st := h.repo.serials["SN-A"]
		st.Status = "LOCKED"
		h.repo.serials["SN-A"] = st
	}
	if _, err := h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(2), Serials: []string{"SN-A", "SN-B"}}); codeOf(t, err) != "SALES_SERIAL_STATE_INVALID" {
		t.Fatalf("非在库序列号应拒绝，got %v", err)
	}
	{
		st := h.repo.serials["SN-A"]
		st.Status = "IN_STOCK"
		h.repo.serials["SN-A"] = st
	}
	if _, err := h.svc.ConfirmPick(h.ctx, actor, picks[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(2), Serials: []string{"SN-A", "SN-B"}}); err != nil {
		t.Fatalf("带序列号确认失败: %v", err)
	}
	// 序列号复核任务逐件一行。
	checks, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	if len(checks) != 2 || checks[0].SerialNo == "" || checks[1].SerialNo == "" {
		t.Fatalf("序列号复核任务应逐件一行: %+v", checks)
	}
	// 复核：通过必须携带与任务一致的序列号。
	if _, err := h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true, Serial: "SN-X"}); err == nil {
		t.Fatal("序列号不一致的复核通过应被拒绝")
	}
	if _, err := h.svc.ConfirmCheck(h.ctx, actor, checks[0].ID.Int64(), CheckConfirmInput{Pass: true, Serial: "SN-A"}); err != nil {
		t.Fatalf("复核确认失败: %v", err)
	}
	if _, err := h.svc.ConfirmCheck(h.ctx, actor, checks[1].ID.Int64(), CheckConfirmInput{Pass: true, Serial: "SN-B"}); err != nil {
		t.Fatalf("复核确认失败: %v", err)
	}
	h.svc.Pack(h.ctx, actor, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(2)}}})
	// 发货：逐件核销（shipSerials 校验已复核序列号件数）。
	shipRes, err := h.svc.Ship(h.ctx, actor, ShipInput{OutboundNo: res.OutboundNo})
	if err != nil {
		t.Fatalf("发货失败: %v", err)
	}
	_ = shipRes
	// 复核异常路径 → 异常中心登记。
	h3 := newHarness(t)
	h3.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
	_, r3 := h3.submitAndApprove(t, skuPlain, qtyOf(2))
	a3 := Actor{ID: 7}
	_, _, _ = h3.svc.GeneratePickTasks(h3.ctx, a3, r3.OutboundNo)
	t3, _ := h3.repo.ListPickTasksByOutbound(nil, r3.OutboundNo)
	h3.svc.ClaimPickTask(h3.ctx, a3, t3[0].ID.Int64())
	h3.svc.ConfirmPick(h3.ctx, a3, t3[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(2)})
	c3, _ := h3.repo.ListCheckTasksByOutbound(nil, r3.OutboundNo)
	if _, err := h3.svc.ConfirmCheck(h3.ctx, a3, c3[0].ID.Int64(), CheckConfirmInput{Pass: false, Result: "少货"}); err != nil {
		t.Fatalf("复核异常上报失败: %v", err)
	}
	if len(h3.exc.rows) == 0 {
		t.Fatal("复核异常应登记异常中心")
	}
}

// ---- 拣货异常上报联动异常中心 ----

func TestPickExceptionReports(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(4))
	a := Actor{ID: 7}
	_, p, _ := h.svc.GeneratePickTasks(h.ctx, a, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, a, p[0].ID.Int64())
	if _, err := h.svc.ReportPickException(h.ctx, a, p[0].ID.Int64(), "库位实际无货"); err != nil {
		t.Fatalf("异常上报失败: %v", err)
	}
	if len(h.exc.rows) != 1 || !strings.HasPrefix(h.exc.rows[0], "EX-TEST-拣货") {
		t.Fatalf("异常中心未登记: %+v", h.exc.rows)
	}
	// EXCEPTION 任务阻塞 PICKED：出库单停留 PICKING。
	ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo)
	if ob.Status != OBStatusPicking {
		t.Fatalf("存在异常任务时出库单应停留 PICKING，got %s", ob.Status)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if len(v) >= len(s) && v[:len(s)] == s {
			return true
		}
	}
	return false
}

// ---- 打包：数量守卫 + 多包裹 + 幂等 ----

func TestPackGuardsAndMultiPackage(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	_, res := h.submitAndApprove(t, skuPlain, qtyOf(10))
	a := Actor{ID: 7}
	_, p, _ := h.svc.GeneratePickTasks(h.ctx, a, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, a, p[0].ID.Int64())
	h.svc.ConfirmPick(h.ctx, a, p[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(10)})
	c, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	h.svc.ConfirmCheck(h.ctx, a, c[0].ID.Int64(), CheckConfirmInput{Pass: true})

	// 超量打包 → 拒绝。
	_, err := h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(11)}}})
	if codeOf(t, err) != "SALES_PACK_EXCEED" {
		t.Fatalf("超量打包应拒绝，got %v", err)
	}
	// 拆包：两个包裹各 5。
	r1, err := h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(5)}}, IdempotencyKey: "bp-1"})
	if err != nil {
		t.Fatalf("包裹1失败: %v", err)
	}
	if ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo); ob.Status != OBStatusChecked {
		t.Fatalf("未打满前应停留 CHECKED，got %s", ob.Status)
	}
	r2, err := h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(5)}}, IdempotencyKey: "bp-2"})
	if err != nil {
		t.Fatalf("包裹2失败: %v", err)
	}
	if ob, _ := h.repo.GetOutboundOrderByNo(nil, res.OutboundNo); ob.Status != OBStatusPacked {
		t.Fatalf("打满后应 PACKED，got %s", ob.Status)
	}
	// 幂等重放：同键返回既有包裹。
	r1replay, err := h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(5)}}, IdempotencyKey: "bp-1"})
	if err != nil || !r1replay.Replay || r1replay.Package.PackageNo != r1.Package.PackageNo {
		t.Fatalf("同键重放应返回既有包裹: %+v %v", r1replay, err)
	}
	_ = r2
}

// ---- 差额关闭（PARTIAL_SHIPPED→CLOSED 释放剩余锁；SO PARTIAL_SHIPPED→COMPLETED） ----

func TestCloseReleasesRemainingLocks(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(6), 1, 11)
	so, res := h.submitAndApprove(t, skuPlain, qtyOf(6))
	a := Actor{ID: 7}
	_, p, _ := h.svc.GeneratePickTasks(h.ctx, a, res.OutboundNo)
	h.svc.ClaimPickTask(h.ctx, a, p[0].ID.Int64())
	h.svc.ConfirmPick(h.ctx, a, p[0].ID.Int64(), PickConfirmInput{PickedQty: qtyOf(6)})
	c, _ := h.repo.ListCheckTasksByOutbound(nil, res.OutboundNo)
	h.svc.ConfirmCheck(h.ctx, a, c[0].ID.Int64(), CheckConfirmInput{Pass: true})
	h.svc.Pack(h.ctx, a, PackInput{OutboundNo: res.OutboundNo, Lines: []PackLineInput{{LineNo: 1, Qty: qtyOf(6)}}})
	// 发货（正常完整），再把库存补回来制造"部分发货"场景太绕——直接测
	// PACKED→CANCELLED 不可（有锁释放语义在 cancel 测试覆盖），此处验证
	// CLOSE 仅允许 PARTIAL_SHIPPED。
	if _, err := h.svc.CloseOutbound(h.ctx, a, res.OutboundNo, CloseInput{Reason: "x"}); codeOf(t, err) != "SALES_STATE_CONFLICT" {
		t.Fatalf("非 PARTIAL_SHIPPED 关闭应 409，got %v", err)
	}
	_ = so
}

// ---- 数据权限（Scope 行级过滤，permission.md §4） ----

func TestScopeFiltering(t *testing.T) {
	h := newHarness(t)
	o := h.createOrder(t, skuPlain, qtyOf(1)) // wh1
	q := SalesOrderQuery{Scope: Scope{All: false, WarehouseIDs: []int64{999}}, Page: 1, PageSize: 20}
	rows, total, err := h.svc.ListSalesOrders(h.ctx, q)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Fatalf("越仓范围应不可见任何行，got total=%d", total)
	}
	q.Scope = Scope{All: false, WarehouseIDs: []int64{wh1}}
	if _, total, _ = h.svc.ListSalesOrders(h.ctx, q); total != 1 {
		t.Fatalf("绑定仓范围应可见 1 行，got %d", total)
	}
	// 详情行级校验。
	if _, _, err := h.svc.GetSalesOrderDetail(h.ctx, o.ID.Int64(), Scope{All: false, WarehouseIDs: []int64{999}}); codeOf(t, err) != "SALES_ORDER_NOT_FOUND" {
		t.Fatalf("越仓详情应 404，got %v", err)
	}
}

// ---- 事务回滚完整性（Tx 快照语义）：提交校验失败时草稿不落库 ----

func TestCreateValidationNoSideEffect(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{{SKUID: skuPlain, Qty: 0}}, // 非法数量
	})
	if err == nil {
		t.Fatal("非法数量应被拒绝")
	}
	if len(h.repo.soRows) != 0 {
		t.Fatalf("校验失败不应产生副作用: %d", len(h.repo.soRows))
	}
	// 客户停用 → 拒绝。
	h2 := newHarness(t)
	h2.svc.customers = fakeCustomers{ok: false}
	_, err = h2.svc.CreateSalesOrder(h2.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1, Items: []OrderItemInput{{SKUID: skuPlain, Qty: qtyOf(1)}},
	})
	if codeOf(t, err) != "SALES_CUSTOMER_NOT_FOUND" {
		t.Fatalf("停用客户应拒绝，got %v", err)
	}
}
