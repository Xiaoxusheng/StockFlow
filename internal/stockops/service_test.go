package stockops

// Service 层单测（内存替身，零 PostgreSQL/Redis 依赖）：
//   - 调拨两侧恒等式与两端流水齐备（business-flow §10.1、inventory-rules §2/§5）；
//   - 状态机守卫与幂等重放（business-flow §13.2、architecture §3.2）；
//   - 盘点范围锁定/释放/互斥、差异调整正确性、序列号逐件联动（plan §6.6/§6.7/§10.3）。

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/response"
)

// codeOf 业务错误码提取（response.Error 的机器可读码在 Error() 文本首段）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *response.Error
	require.ErrorAs(t, err, &e)
	return strings.SplitN(e.Error(), ":", 2)[0]
}

const (
	whSrc = int64(10)
	whDst = int64(20)

	zoneSrc, shelfSrc, binSrc    = int64(1), int64(11), int64(111)
	zoneSrc2, shelfSrc2, binSrc2 = int64(1), int64(12), int64(112)
	zoneDst, shelfDst, binDst    = int64(2), int64(21), int64(211)

	skuPlain  = int64(100)
	skuBatch  = int64(101)
	skuSerial = int64(102)
	batchA    = int64(900)
)

// fixture 固定世界：
//
//	whSrc/bin111: skuPlain 10；skuSerial 3 件（S1/S2/S3）；whSrc/bin112: skuBatch(批次A) 50
//	whDst/bin211: 空（收货行由 TransferIn 创建）
type fixture struct {
	svc *Service
	w   *fakeWorld
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	w := newFakeWorld()
	w.seedRow(whSrc, zoneSrc, shelfSrc, binSrc, skuPlain, 0, q(10))
	w.seedRow(whSrc, zoneSrc, shelfSrc, binSrc, skuSerial, 0, q(3))
	for _, s := range []string{"S1", "S2", "S3"} {
		w.seedSerial(s, skuSerial, 0, whSrc, binSrc)
	}
	w.seedRow(whSrc, zoneSrc2, shelfSrc2, binSrc2, skuBatch, batchA, q(50))

	flags := &fakeFlags{m: map[int64]SKUFlags{
		skuPlain:  {Enabled: true},
		skuBatch:  {Enabled: true, BatchManaged: true},
		skuSerial: {Enabled: true, SerialManaged: true},
	}}
	svc := NewService(nil,
		WithStore(&memStore{w: w}),
		WithGateway(&fakeGateway{w: w}),
		WithBinChecker(&fakeBins{ok: map[[2]int64]bool{
			{whSrc, binSrc}: true, {whSrc, binSrc2}: true, {whDst, binDst}: true,
		}}),
		WithSKUChecker(&fakeSKUs{ok: map[int64]bool{skuPlain: true, skuBatch: true, skuSerial: true}}),
		WithSKUFlagReader(flags),
		WithNumberIssuer(&fakeIssuer{w: w}),
	)
	return &fixture{svc: svc, w: w}
}

func transferInput(lineSKU int64, qty inventory.Qty, batch int64) TransferInput {
	return TransferInput{
		Type: TransferTypeWarehouse, FromWarehouseID: whSrc, ToWarehouseID: whDst,
		Lines: []TransferLineInput{{
			SKUID: lineSKU, BatchID: batch, Qty: qty,
			From: TransferLoc{WarehouseID: whSrc, ZoneID: zoneSrc, ShelfID: shelfSrc, BinID: binSrc},
			To:   TransferLoc{WarehouseID: whDst, ZoneID: zoneDst, ShelfID: shelfDst, BinID: binDst},
		}},
	}
}

// runTransferLifecycle 走完 创建→提交→审核→出库→到货→收货，返回每步结果。
func (f *fixture) runTransferLifecycle(t *testing.T, in TransferInput) *TransferDetail {
	t.Helper()
	actor := testActor("stockop")
	d, err := f.svc.CreateTransfer(ctxBG(), actor, in)
	require.NoError(t, err)
	_, _, err = f.svc.SubmitTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, d.Order.ID.Int64(), ApproveTransferInput{Action: "approve"})
	require.NoError(t, err)
	_, _, err = f.svc.OutboundTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ArriveTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ReceiveTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	fresh, err := f.svc.GetTransferDetail(ctxBG(), d.Order.ID.Int64(), Scope{AllWarehouses: true})
	require.NoError(t, err)
	return fresh
}

func srcRow(f *fixture) *fakeInvRow { return f.w.rowByKey(whSrc, binSrc, skuPlain, 0) }
func dstRow(f *fixture) *fakeInvRow { return f.w.rowByKey(whDst, binDst, skuPlain, 0) }

// ---- 调拨 ----

func TestTransferLifecycleIdentityAndLedgers(t *testing.T) {
	f := newFixture(t)
	d := f.runTransferLifecycle(t, transferInput(skuPlain, q(6), 0))

	// 两端恒等式（inventory-rules §2：所有时刻所有代码路径）。
	checkAllIdentity(t, f.w)
	require.Equal(t, q(4), srcRow(f).total)
	require.Equal(t, q(4), srcRow(f).avail)
	require.True(t, srcRow(f).locked.IsZero())
	require.Equal(t, q(6), dstRow(f).total)
	require.Equal(t, q(6), dstRow(f).avail)

	// 明细进度与在途清零（在途 = qty_out - qty_in）。
	require.Len(t, d.Items, 1)
	require.Equal(t, q(6), d.Items[0].QtyOut)
	require.Equal(t, q(6), d.Items[0].QtyIn)
	require.True(t, d.Items[0].QtyOut.Sub(d.Items[0].QtyIn).IsZero())
	require.Equal(t, TransferCompleted, d.Order.Status)

	// 两端流水齐备（§10.1 源减目标增）：LOCK/TRANSFER_OUT 在源仓、TRANSFER_IN 在目标仓。
	var lockN, outN, inN int
	for _, l := range f.w.ledgers {
		switch l.changeType {
		case "LOCK":
			require.Equal(t, whSrc, l.wh)
			lockN++
		case "TRANSFER_OUT":
			require.Equal(t, whSrc, l.wh)
			require.Equal(t, d.Order.TransferNo, l.bizNo)
			outN++
		case "TRANSFER_IN":
			require.Equal(t, whDst, l.wh)
			require.Equal(t, d.Order.TransferNo, l.bizNo)
			inN++
		}
	}
	require.Equal(t, 1, lockN)
	require.Equal(t, 1, outN)
	require.Equal(t, 1, inN)

	// 审批链与审计（business-flow §12/§13.2）。
	var submits, approves int
	for _, a := range f.w.approvals {
		if a.TargetNo == d.Order.TransferNo {
			switch a.Action {
			case "SUBMIT":
				submits++
			case "APPROVE":
				approves++
			}
		}
	}
	require.Equal(t, 1, submits)
	require.Equal(t, 1, approves)
	require.NotEmpty(t, f.w.audits)
}

func TestTransferInTransitAggregation(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	d, err := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(6), 0))
	require.NoError(t, err)
	id := d.Order.ID.Int64()
	_, _, err = f.svc.SubmitTransfer(ctxBG(), actor, id)
	require.NoError(t, err)
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, id, ApproveTransferInput{Action: "approve"})
	require.NoError(t, err)

	// 待出库：无在途（尚未离仓）。
	rows, total, err := f.svc.ListInTransit(ctxBG(), InTransitFilter{AllWarehouses: true}, 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)

	_, _, err = f.svc.OutboundTransfer(ctxBG(), actor, id)
	require.NoError(t, err)

	// 调拨中：在途 = qty_out - qty_in = 6（inventory-rules §2 在途口径）。
	rows, total, err = f.svc.ListInTransit(ctxBG(), InTransitFilter{AllWarehouses: true}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	require.Equal(t, q(6), rows[0].OutTransit)
	require.Equal(t, q(6), rows[0].InTransit)

	// 目标仓视角：只看 in_transit。
	rows, _, err = f.svc.ListInTransit(ctxBG(), InTransitFilter{AllWarehouses: true, WarehouseID: whDst}, 1, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].OutTransit.IsZero())
	require.Equal(t, q(6), rows[0].InTransit)
}

func TestTransferApproveInsufficientRollsBack(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	d, err := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(99), 0))
	require.NoError(t, err)
	_, _, err = f.svc.SubmitTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, d.Order.ID.Int64(), ApproveTransferInput{Action: "approve"})
	require.Error(t, err) // 源仓不足

	// 整体回滚：单据停留待审核、无锁、无 LOCK 流水、库存未动（plan §6.6）。
	o, err := f.svc.GetTransferDetail(ctxBG(), d.Order.ID.Int64(), Scope{AllWarehouses: true})
	require.NoError(t, err)
	require.Equal(t, TransferPending, o.Order.Status)
	require.Equal(t, q(10), srcRow(f).total)
	require.Equal(t, q(10), srcRow(f).avail)
	for _, l := range f.w.ledgers {
		require.NotEqual(t, "LOCK", l.changeType)
	}
	require.Empty(t, f.w.locks)
}

func TestTransferStateGuardAndIdempotentReplay(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	d, err := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(6), 0))
	require.NoError(t, err)
	id := d.Order.ID.Int64()

	// 未提交直接出库 → 状态冲突。
	_, _, err = f.svc.OutboundTransfer(ctxBG(), actor, id)
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_STATUS_CONFLICT", codeOf(t, err))

	// 提交两次：第二次幂等重放，单据仍在待审核。
	_, replay, err := f.svc.SubmitTransfer(ctxBG(), actor, id)
	require.NoError(t, err)
	require.False(t, replay)
	_, replay, err = f.svc.SubmitTransfer(ctxBG(), actor, id)
	require.NoError(t, err)
	require.True(t, replay)

	// 审核两次：第二次重放且不重复预占（单条 ACTIVE 锁）。
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, id, ApproveTransferInput{Action: "approve"})
	require.NoError(t, err)
	_, replay, err = f.svc.ApproveTransfer(ctxBG(), actor, id, ApproveTransferInput{Action: "approve"})
	require.NoError(t, err)
	require.True(t, replay)
	activeLocks := 0
	for _, l := range f.w.locks {
		if l.status == "ACTIVE" {
			activeLocks++
		}
	}
	require.Equal(t, 1, activeLocks)
	require.Equal(t, q(4), srcRow(f).avail)

	// 出库两次：第二次重放，qty_out 不变（重复请求不重复扣减）。
	_, _, err = f.svc.OutboundTransfer(ctxBG(), actor, id)
	require.NoError(t, err)
	totalBefore := srcRow(f).total
	_, replay, err = f.svc.OutboundTransfer(ctxBG(), actor, id)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, totalBefore, srcRow(f).total)
	items, err := f.svc.GetTransferDetail(ctxBG(), id, Scope{AllWarehouses: true})
	require.NoError(t, err)
	require.Equal(t, q(6), items.Items[0].QtyOut)
}

func TestTransferCancelReleasesLock(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	d, _ := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(6), 0))
	id := d.Order.ID.Int64()
	_, _, _ = f.svc.SubmitTransfer(ctxBG(), actor, id)
	_, _, _ = f.svc.ApproveTransfer(ctxBG(), actor, id, ApproveTransferInput{Action: "approve"})
	require.Equal(t, q(4), srcRow(f).avail)
	require.Equal(t, q(6), srcRow(f).locked)

	_, replay, err := f.svc.CancelTransfer(ctxBG(), actor, id, "计划变更")
	require.NoError(t, err)
	require.False(t, replay)
	// 锁全部释放、可用恢复（inventory-rules §4.2）。
	require.Equal(t, q(10), srcRow(f).avail)
	require.True(t, srcRow(f).locked.IsZero())
	checkAllIdentity(t, f.w)
	var releases int
	for _, l := range f.w.ledgers {
		if l.changeType == "RELEASE" {
			releases++
		}
	}
	require.Equal(t, 1, releases)
	// 重复取消 → 幂等。
	_, replay, err = f.svc.CancelTransfer(ctxBG(), actor, id, "")
	require.NoError(t, err)
	require.True(t, replay)
}

func TestTransferCancelForbiddenAfterOutbound(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	d, _ := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(6), 0))
	id := d.Order.ID.Int64()
	_, _, _ = f.svc.SubmitTransfer(ctxBG(), actor, id)
	_, _, _ = f.svc.ApproveTransfer(ctxBG(), actor, id, ApproveTransferInput{Action: "approve"})
	_, _, _ = f.svc.OutboundTransfer(ctxBG(), actor, id)

	_, _, err := f.svc.CancelTransfer(ctxBG(), actor, id, "")
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_TRANSFER_CANCEL_FORBIDDEN", codeOf(t, err))
}

func TestTransferSerialPiecesRoundTrip(t *testing.T) {
	f := newFixture(t)
	in := transferInput(skuSerial, q(2), 0) // 序列号 SKU：2 件 = S1/S2
	d := f.runTransferLifecycle(t, in)

	// 出库后两件转在途（wh=0），收货后回位目标库位 IN_STOCK。
	for _, no := range []string{"S1", "S2"} {
		s := f.w.serials[no]
		require.Equal(t, "IN_STOCK", s.status)
		require.Equal(t, whDst, s.wh)
		require.Equal(t, binDst, s.bin)
		require.Equal(t, d.Order.TransferNo, s.lastSrcNo)
	}
	// 留源仓的 S3 不受影响。
	require.Equal(t, "IN_STOCK", f.w.serials["S3"].status)
	require.Equal(t, whSrc, f.w.serials["S3"].wh)
	checkAllIdentity(t, f.w)
}

func TestTransferSerialQtyMustBeInteger(t *testing.T) {
	f := newFixture(t)
	fraction, err := inventory.ParseQty("1.5")
	require.NoError(t, err)
	_, err = f.svc.CreateTransfer(ctxBG(), testActor("x"), transferInput(skuSerial, fraction, 0))
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_SERIAL_QTY_INVALID", codeOf(t, err))
}

func TestTransferBatchCarriedAcrossWarehouses(t *testing.T) {
	f := newFixture(t)
	in := TransferInput{
		Type: TransferTypeWarehouse, FromWarehouseID: whSrc, ToWarehouseID: whDst,
		Lines: []TransferLineInput{{
			SKUID: skuBatch, BatchID: batchA, Qty: q(20),
			From: TransferLoc{WarehouseID: whSrc, ZoneID: zoneSrc2, ShelfID: shelfSrc2, BinID: binSrc2},
			To:   TransferLoc{WarehouseID: whDst, ZoneID: zoneDst, ShelfID: shelfDst, BinID: binDst},
		}},
	}
	d := f.runTransferLifecycle(t, in)
	checkAllIdentity(t, f.w)
	require.Equal(t, q(30), f.w.rowByKey(whSrc, binSrc2, skuBatch, batchA).total)
	dst := f.w.rowByKey(whDst, binDst, skuBatch, batchA)
	require.NotNil(t, dst)
	require.Equal(t, q(20), dst.total)
	require.Equal(t, batchA, dst.batch)
	require.Equal(t, d.Items[0].QtyOut, q(20))
}

// ---- 盘点 ----

// runCountStart 创建并开始盘点（范围：bin111 + bin112）。
func (f *fixture) runCountStart(t *testing.T, scope CountScope) *CountDetail {
	t.Helper()
	actor := testActor("stocktaker")
	d, err := f.svc.CreateCount(ctxBG(), actor, CountInput{WarehouseID: whSrc, Scope: scope})
	require.NoError(t, err)
	started, replay, err := f.svc.StartCount(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	require.False(t, replay)
	return started
}

func allBinScope() CountScope {
	return CountScope{Mode: "BIN", BinIDs: []int64{binSrc, binSrc2}}
}

func findRowID(t *testing.T, f *fixture, wh, bin, sku, batch int64) int64 {
	t.Helper()
	for _, r := range f.w.rows {
		if r.wh == wh && r.bin == bin && r.sku == sku && r.batch == batch {
			return r.id
		}
	}
	t.Fatalf("行不存在: wh=%d bin=%d sku=%d", wh, bin, sku)
	return 0
}

func TestCountFreezeSnapshotAndMutex(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())

	// 范围内行冻结：available→frozen（inventory-rules §4 盘点锁定）。
	plain := f.w.rowByKey(whSrc, binSrc, skuPlain, 0)
	require.True(t, plain.avail.IsZero())
	require.Equal(t, q(10), plain.frozen)
	batch := f.w.rowByKey(whSrc, binSrc2, skuBatch, batchA)
	require.True(t, batch.avail.IsZero())
	require.Equal(t, q(50), batch.frozen)
	serialRow := f.w.rowByKey(whSrc, binSrc, skuSerial, 0)
	require.Equal(t, q(3), serialRow.frozen)

	// 明细双层：行级汇总行 + 序列号逐件行（qty_system=1）。
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)
	rowSerial := findRowID(t, f, whSrc, binSrc, skuSerial, 0)
	rowBatch := findRowID(t, f, whSrc, binSrc2, skuBatch, batchA)
	byRow := map[int64]int{}
	for _, it := range d.Items {
		byRow[it.InventoryRowID]++
		if it.SerialNo != "" {
			require.Equal(t, qtyUnit, it.QtySystem)
		} else {
			require.False(t, it.QtyCounted.Valid)
		}
	}
	require.Equal(t, 1, byRow[rowPlain])
	require.Equal(t, 1, byRow[rowBatch])
	require.Equal(t, 4, byRow[rowSerial]) // 汇总行 + S1/S2/S3

	// 序列号逐件冻结（inventory-rules §8.2）。
	for _, s := range []string{"S1", "S2", "S3"} {
		require.Equal(t, "FROZEN", f.w.serials[s].status)
	}

	// 多人同范围互斥（architecture §5）：第二张同范围盘点单冻结被拒。
	second, err := f.svc.CreateCount(ctxBG(), testActor("rival"), CountInput{WarehouseID: whSrc, Scope: allBinScope()})
	require.NoError(t, err)
	_, _, err = f.svc.StartCount(ctxBG(), testActor("rival"), second.Order.ID.Int64())
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_SCOPE_FROZEN", codeOf(t, err))
	checkAllIdentity(t, f.w)
}

func TestCountScopeEmpty(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stocktaker")
	d, err := f.svc.CreateCount(ctxBG(), actor, CountInput{
		WarehouseID: whSrc, Scope: CountScope{Mode: "BIN", BinIDs: []int64{999}},
	})
	require.NoError(t, err)
	_, _, err = f.svc.StartCount(ctxBG(), actor, d.Order.ID.Int64())
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_SCOPE_EMPTY", codeOf(t, err))
}

func TestCountRegisterIdempotentPUTAndSurplus(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	actor := testActor("stocktaker")
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)

	// 登记两次同值：PUT 幂等覆盖，无新增行（architecture §3.2）。
	reg := CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(7)},
	}}
	for range 2 {
		_, err := f.svc.RegisterCountings(ctxBG(), actor, d.Order.ID.Int64(), reg)
		require.NoError(t, err)
	}
	items, _ := f.latestCountItems(t, d.Order.ID.Int64())
	n := 0
	for _, it := range items {
		if it.InventoryRowID == rowPlain && it.SerialNo == "" {
			n++
			require.True(t, it.QtyCounted.Valid)
			require.Equal(t, q(7), it.QtyCounted.Qty)
		}
	}
	require.Equal(t, 1, n)

	// 盘盈多出件：未知序列号建档行 qty_system=0。
	_, err := f.svc.RegisterCountings(ctxBG(), actor, d.Order.ID.Int64(), CountRegistrationInput{
		Registrations: []CountRegistration{{InventoryRowID: rowPlain, SerialNo: "SX9", Qty: qtyUnit}},
	})
	require.NoError(t, err)
	items, _ = f.latestCountItems(t, d.Order.ID.Int64())
	var surplus *CountItem
	for i := range items {
		if items[i].SerialNo == "SX9" {
			surplus = &items[i]
		}
	}
	require.NotNil(t, surplus)
	require.True(t, surplus.QtySystem.IsZero())
	require.Equal(t, qtyUnit, surplus.QtyCounted.Qty)

	// 序列号行数量仅 0/1。
	_, err = f.svc.RegisterCountings(ctxBG(), actor, d.Order.ID.Int64(), CountRegistrationInput{
		Registrations: []CountRegistration{{InventoryRowID: rowPlain, SerialNo: "SX8", Qty: q(2)}},
	})
	require.Error(t, err)
	// 越范围行拒绝。
	_, err = f.svc.RegisterCountings(ctxBG(), actor, d.Order.ID.Int64(), CountRegistrationInput{
		Registrations: []CountRegistration{{InventoryRowID: 424242, Qty: q(1)}},
	})
	require.Error(t, err)
}

func (f *fixture) latestCountItems(t *testing.T, countID int64) ([]CountItem, error) {
	t.Helper()
	d, err := f.svc.GetCountDetail(ctxBG(), countID, Scope{AllWarehouses: true})
	require.NoError(t, err)
	return d.Items, nil
}

func TestCountFinishGeneratesDifferences(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	actor := testActor("stocktaker")
	cid := d.Order.ID.Int64()
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)
	rowSerial := findRowID(t, f, whSrc, binSrc, skuSerial, 0)
	rowBatch := findRowID(t, f, whSrc, binSrc2, skuBatch, batchA)

	// 未登记完成实盘 → 拒绝（漏盘不得当差异）。
	_, _, err := f.svc.FinishCount(ctxBG(), actor, cid)
	require.Error(t, err)

	// 登记：普通行盘亏 3（10→7）；序列号行盘亏 1 件（3→2，S3 缺失）；批次行盘盈 0 差异。
	_, err = f.svc.RegisterCountings(ctxBG(), actor, cid, CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(7)},
		{InventoryRowID: rowSerial, Qty: q(2)}, // 行级汇总（实盘 2 件）
		{InventoryRowID: rowSerial, SerialNo: "S1", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S2", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S3", Qty: q(0)}, // 缺失显式登记 0
		{InventoryRowID: rowBatch, Qty: q(50)},
	}})
	require.NoError(t, err)
	fresh, replay, err := f.svc.FinishCount(ctxBG(), actor, cid)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, CountReview, fresh.Order.Status)

	// 差异：普通行 -3（盘亏）、序列号行 -1（盘亏）；批次行无差异不生成（plan §6.7）。
	require.Len(t, fresh.Differences, 2)
	diffs := map[int64]CountDifference{}
	for _, df := range fresh.Differences {
		diffs[df.SKUID] = df
		require.Equal(t, DiffPending, df.Status)
	}
	require.Equal(t, q(-3), diffs[skuPlain].DiffQty)
	require.Equal(t, q(10), diffs[skuPlain].QtySystem)
	require.Equal(t, q(7), diffs[skuPlain].QtyCounted)
	require.Equal(t, q(-1), diffs[skuSerial].DiffQty)
	require.Empty(t, diffs[skuBatch].AdjustNo)
}

func TestCountCompleteAdjustsReleasesAndSerials(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	actor := testActor("stocktaker")
	cid := d.Order.ID.Int64()
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)
	rowSerial := findRowID(t, f, whSrc, binSrc, skuSerial, 0)
	rowBatch := findRowID(t, f, whSrc, binSrc2, skuBatch, batchA)

	_, err := f.svc.RegisterCountings(ctxBG(), actor, cid, CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(12)},                    // 盘盈 +2
		{InventoryRowID: rowSerial, Qty: q(2)},                    // 盘亏 -1（S3 缺失）
		{InventoryRowID: rowSerial, SerialNo: "S1", Qty: qtyUnit}, // 在库
		{InventoryRowID: rowSerial, SerialNo: "S2", Qty: qtyUnit}, // 在库
		{InventoryRowID: rowSerial, SerialNo: "S3", Qty: q(0)},    // 缺失
		{InventoryRowID: rowBatch, Qty: q(50)},                    // 无差异
	}})
	require.NoError(t, err)
	_, _, err = f.svc.FinishCount(ctxBG(), actor, cid)
	require.NoError(t, err)

	done, replay, err := f.svc.CompleteCount(ctxBG(), testActor("manager"), cid, "差异属实")
	require.NoError(t, err)
	require.False(t, replay)

	// 差异调整正确性（解冻后 Adjust 落账）：盘盈 +2、盘亏 -1、无差异行只解冻。
	checkAllIdentity(t, f.w)
	plain := f.w.rowByKey(whSrc, binSrc, skuPlain, 0)
	require.Equal(t, q(12), plain.total)
	require.Equal(t, q(12), plain.avail)
	serialRow := f.w.rowByKey(whSrc, binSrc, skuSerial, 0)
	require.Equal(t, q(2), serialRow.total)
	require.Equal(t, q(2), serialRow.avail)
	batch := f.w.rowByKey(whSrc, binSrc2, skuBatch, batchA)
	require.Equal(t, q(50), batch.total)
	require.Equal(t, q(50), batch.avail)

	// 序列号联动：S3 缺失核销 OUTBOUND（source=调整单）；S1/S2 回正常 IN_STOCK。
	require.Equal(t, "OUTBOUND", f.w.serials["S3"].status)
	require.Equal(t, adjustSourceType, f.w.serials["S3"].lastSourceType)
	require.NotEmpty(t, f.w.serials["S3"].lastSrcNo)
	require.Equal(t, "IN_STOCK", f.w.serials["S1"].status)
	require.Equal(t, "IN_STOCK", f.w.serials["S2"].status)

	// 差异行 EXECUTED + adjust_no 回写（plan §6.7）。
	for _, df := range done.Differences {
		require.Equal(t, DiffExecuted, df.Status)
		require.NotEmpty(t, df.AdjustNo)
	}

	// 冻结全部释放：无残留 ACTIVE COUNT_FREEZE。
	for _, l := range f.w.locks {
		if l.status == "ACTIVE" {
			t.Fatalf("残留活跃冻结锁: %+v", l)
		}
	}

	// 重复完成 → 幂等重放，不重复调整。
	totals := map[int64]inventory.Qty{}
	for _, r := range f.w.rows {
		totals[r.id] = r.total
	}
	_, replay, err = f.svc.CompleteCount(ctxBG(), testActor("manager"), cid, "")
	require.NoError(t, err)
	require.True(t, replay)
	for _, r := range f.w.rows {
		require.Equal(t, totals[r.id], r.total, "重复完成不得二次调整 row=%d", r.id)
	}
	checkAllIdentity(t, f.w)
}

func TestCountRejectUnfreezesWithoutAdjust(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	actor := testActor("stocktaker")
	cid := d.Order.ID.Int64()
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)
	rowSerial := findRowID(t, f, whSrc, binSrc, skuSerial, 0)
	rowBatch := findRowID(t, f, whSrc, binSrc2, skuBatch, batchA)

	_, err := f.svc.RegisterCountings(ctxBG(), actor, cid, CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(7)},
		{InventoryRowID: rowSerial, Qty: q(3)},
		{InventoryRowID: rowSerial, SerialNo: "S1", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S2", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S3", Qty: qtyUnit},
		{InventoryRowID: rowBatch, Qty: q(50)},
	}})
	require.NoError(t, err)
	_, _, err = f.svc.FinishCount(ctxBG(), actor, cid)
	require.NoError(t, err)

	rejected, _, err := f.svc.RejectCount(ctxBG(), testActor("manager"), cid, "重盘")
	require.NoError(t, err)

	// 解冻恢复、库存未调整、差异 REJECTED（plan §6.7 驳回路径）。
	plain := f.w.rowByKey(whSrc, binSrc, skuPlain, 0)
	require.Equal(t, q(10), plain.total)
	require.Equal(t, q(10), plain.avail)
	for _, df := range rejected.Differences {
		require.Equal(t, DiffRejected, df.Status)
		require.Empty(t, df.AdjustNo)
	}
	for _, s := range []string{"S1", "S2", "S3"} {
		require.Equal(t, "IN_STOCK", f.w.serials[s].status)
	}
	checkAllIdentity(t, f.w)
}

func TestCountCancelDuringCountingUnfreezes(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	plain := f.w.rowByKey(whSrc, binSrc, skuPlain, 0)
	require.Equal(t, q(10), plain.frozen)

	_, _, err := f.svc.CancelCount(ctxBG(), testActor("stocktaker"), d.Order.ID.Int64(), "范围有误")
	require.NoError(t, err)
	require.Equal(t, q(10), plain.avail)
	require.True(t, plain.frozen.IsZero())
	for _, s := range []string{"S1", "S2", "S3"} {
		require.Equal(t, "IN_STOCK", f.w.serials[s].status)
	}
	checkAllIdentity(t, f.w)
}

func TestCountDifferenceAdjustUsesInventoryPrimitive(t *testing.T) {
	// 差异调整必须经 inventory.Adjust 原语落账（ADJUST 流水 + inventory_adjustments
	// 执行单，business-flow §11.1）——禁止旁路直改库存。
	f := newFixture(t)
	d := f.runCountStart(t, CountScope{Mode: "BIN", BinIDs: []int64{binSrc}})
	actor := testActor("stocktaker")
	rowPlain := findRowID(t, f, whSrc, binSrc, skuPlain, 0)
	rowSerial := findRowID(t, f, whSrc, binSrc, skuSerial, 0)
	_, err := f.svc.RegisterCountings(ctxBG(), actor, d.Order.ID.Int64(), CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(8)},
		{InventoryRowID: rowSerial, Qty: q(3)},
		{InventoryRowID: rowSerial, SerialNo: "S1", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S2", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S3", Qty: qtyUnit},
	}})
	require.NoError(t, err)
	_, _, err = f.svc.FinishCount(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.CompleteCount(ctxBG(), testActor("manager"), d.Order.ID.Int64(), "")
	require.NoError(t, err)

	var adjustLedgers int
	for _, l := range f.w.ledgers {
		if l.changeType == "ADJUST" {
			adjustLedgers++
			require.Equal(t, sourceCount, l.businessType)
		}
	}
	require.Equal(t, 1, adjustLedgers)
	require.Len(t, f.w.adjustments, 1)
	require.Equal(t, "盘亏", f.w.adjustments[0].adjustType)
	require.Equal(t, q(2), f.w.adjustments[0].qty)
}

func TestCountDataPermissionFailClosed(t *testing.T) {
	f := newFixture(t)
	d := f.runCountStart(t, allBinScope())
	// 无范围（DEPARTMENT/SELF 等未落行级的范围）→ 不可见任何行（permission.md §4）。
	_, err := f.svc.GetCountDetail(ctxBG(), d.Order.ID.Int64(), Scope{})
	require.Error(t, err)
	require.Equal(t, "STOCKOPS_COUNT_NOT_FOUND", codeOf(t, err))
	// 范围内仓库可见。
	_, err = f.svc.GetCountDetail(ctxBG(), d.Order.ID.Int64(), Scope{WarehouseIDs: []int64{whSrc}})
	require.NoError(t, err)
}

func TestTransferDataPermissionEitherEnd(t *testing.T) {
	f := newFixture(t)
	d := f.runTransferLifecycle(t, transferInput(skuPlain, q(6), 0))
	// 源仓范围可见（发出的调拨）。
	_, err := f.svc.GetTransferDetail(ctxBG(), d.Order.ID.Int64(), Scope{WarehouseIDs: []int64{whSrc}})
	require.NoError(t, err)
	// 目标仓范围可见（收到的调拨）。
	_, err = f.svc.GetTransferDetail(ctxBG(), d.Order.ID.Int64(), Scope{WarehouseIDs: []int64{whDst}})
	require.NoError(t, err)
	// 无关仓库不可见（fail-closed 404）。
	_, err = f.svc.GetTransferDetail(ctxBG(), d.Order.ID.Int64(), Scope{WarehouseIDs: []int64{99}})
	require.Error(t, err)
}
