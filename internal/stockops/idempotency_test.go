package stockops

// 幂等键合成与头键重放测试（效率层一期计划 §2.10 / §7.1 T13；api.md §7「行级幂等键
// × 头键合成规则」；内存替身，零 PostgreSQL/Redis 依赖）：
//   - composeIdemKey/idempotencyKeyOf 单元断言（键合成唯一实现点 + 头键 ≤32 校验）；
//   - T13 同 Idempotency-Key 二次提交 → 返回首次结果且库存/流水/单据不变
//     （口径对齐 internal/inventory/integration_test.go TestIdempotencyReplayDoesNotDoubleApply，
//     该文件为只读锚点不动）；
//   - 头键缺省 → 行级键与存量通式形态完全一致（零行为变化）。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/response"
)

// ---- composeIdemKey / idempotencyKeyOf 单元测试 ----

func TestComposeIdemKey(t *testing.T) {
	const rowKey = "trout:TR-20261006-000001:1:111:100:0"

	// 头键缺省 → 原行级通式原样（零行为变化）。
	require.Equal(t, rowKey, composeIdemKey("", rowKey))

	// 头键存在且非空 → "{头键}:{原行级通式}"（新命名空间）。
	require.Equal(t, "ABC123:"+rowKey, composeIdemKey("ABC123", rowKey))
}

// idemKeyCtx 构造携带指定请求头的 gin 上下文。
func idemKeyCtx(t *testing.T, header string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if header != "" {
		req.Header.Set("Idempotency-Key", header)
	}
	c.Request = req
	return c, w
}

func TestIdempotencyKeyOf(t *testing.T) {
	// 缺省/空白 → 空串（不参与合成，键形态与存量一致）。
	for _, h := range []string{"", "   "} {
		c, _ := idemKeyCtx(t, h)
		key, err := idempotencyKeyOf(c)
		require.NoError(t, err)
		require.Equal(t, "", key)
	}

	// 恰好 32 字符 → 放行并 TrimSpace。
	c, _ := idemKeyCtx(t, "  "+strings.Repeat("K", maxIdemHeaderLen)+"  ")
	key, err := idempotencyKeyOf(c)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("K", maxIdemHeaderLen), key)

	// 超长（33 字符）→ 400 invalidParam。
	c, _ = idemKeyCtx(t, strings.Repeat("K", maxIdemHeaderLen+1))
	_, err = idempotencyKeyOf(c)
	require.Error(t, err)
	var re *response.Error
	require.ErrorAs(t, err, &re)
	require.Contains(t, re.Error(), "COMMON_INVALID_PARAM")
}

// ---- T13 同头键重放：不重复扣加库存/流水 ----

func TestIdempotencyReplay_SameHeaderKeyNoDoubleApply(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")
	header := "IDEM-MOVE-0001"
	in := MoveInput{
		From:     MoveKeyInput{WarehouseID: whSrc, ZoneID: zoneSrc, ShelfID: shelfSrc, BinID: binSrc, SKUID: skuPlain},
		To:       MoveKeyInput{WarehouseID: whSrc, ZoneID: zoneSrc, ShelfID: shelfSrc, BinID: binSrc2, SKUID: skuPlain},
		Qty:      "3",
		SourceNo: "WO-2026-001",
	}

	// 首次提交：正常执行（源行 -3，目标行 +3，一条 MOVE 流水）。
	first, err := f.svc.MoveBin(ctxBG(), actor, in, header)
	require.NoError(t, err)
	require.False(t, first.Replay)
	require.Equal(t, q(7), f.w.rowByKey(whSrc, binSrc, skuPlain, 0).avail)
	ledgersAfterFirst := len(f.w.ledgers)

	// 同 Idempotency-Key 二次提交：返回首次结果，库存/流水/单据不变（T13 口径）。
	second, err := f.svc.MoveBin(ctxBG(), actor, in, header)
	require.NoError(t, err)
	require.True(t, second.Replay)
	require.NotZero(t, second.Ledger.ID)
	require.Equal(t, ledgersAfterFirst, len(f.w.ledgers), "重放不产生新流水")
	require.Equal(t, q(7), f.w.rowByKey(whSrc, binSrc, skuPlain, 0).avail, "重放不重复扣减")
	require.Equal(t, q(3), f.w.rowByKey(whSrc, binSrc2, skuPlain, 0).avail, "重放不重复加成")

	// 不同头键 = 不同命名空间：正常再执行一次。
	third, err := f.svc.MoveBin(ctxBG(), actor, in, "IDEM-MOVE-0002")
	require.NoError(t, err)
	require.False(t, third.Replay)
	require.Equal(t, q(4), f.w.rowByKey(whSrc, binSrc, skuPlain, 0).avail)
}

// TestIdemKeyShape_HeaderComposesRowKeys 头键缺省/存在两态的行级键形态断言：
// 缺省 → 与存量通式逐字一致（零行为变化）；存在 → "{头键}:{原行级通式}"。
func TestIdemKeyShape_HeaderComposesRowKeys(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stockop")

	// —— 头键缺省：调拨链路（lock/trout/trin）与移库键形态与存量完全一致 ——
	d, err := f.svc.CreateTransfer(ctxBG(), actor, transferInput(skuPlain, q(6), 0))
	require.NoError(t, err)
	no := d.Order.TransferNo
	_, _, err = f.svc.SubmitTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, d.Order.ID.Int64(), ApproveTransferInput{Action: "approve"}, "")
	require.NoError(t, err)
	_, _, err = f.svc.OutboundTransfer(ctxBG(), actor, d.Order.ID.Int64(), "")
	require.NoError(t, err)
	_, _, err = f.svc.ArriveTransfer(ctxBG(), actor, d.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ReceiveTransfer(ctxBG(), actor, d.Order.ID.Int64(), "")
	require.NoError(t, err)

	wantLock, ok := f.w.ledgerByKey("lock:" + no + ":1:111:100:0")
	require.True(t, ok, "头键缺省 lock 键应与存量通式逐字一致")
	require.Equal(t, "lock:"+no+":1:111:100:0", wantLock.idemKey)
	_, ok = f.w.ledgerByKey("trout:" + no + ":1:111:100:0")
	require.True(t, ok)
	_, ok = f.w.ledgerByKey("trin:" + no + ":1:211:100:0")
	require.True(t, ok)
	_, ok = f.w.ledgerByKey("move:WO-X:111:100:0")
	require.False(t, ok)

	// —— 头键存在：行级键 = "{头键}:{原行级通式}" ——
	const header = "IDEM-TR-0001"
	in2 := TransferInput{
		Type: TransferTypeWarehouse, FromWarehouseID: whSrc, ToWarehouseID: whDst,
		Lines: []TransferLineInput{{
			SKUID: skuBatch, BatchID: batchA, Qty: q(5),
			From: TransferLoc{WarehouseID: whSrc, ZoneID: zoneSrc2, ShelfID: shelfSrc2, BinID: binSrc2},
			To:   TransferLoc{WarehouseID: whDst, ZoneID: zoneDst, ShelfID: shelfDst, BinID: binDst},
		}},
	}
	d2, err := f.svc.CreateTransfer(ctxBG(), actor, in2)
	require.NoError(t, err)
	no2 := d2.Order.TransferNo
	_, _, err = f.svc.SubmitTransfer(ctxBG(), actor, d2.Order.ID.Int64())
	require.NoError(t, err)
	_, _, err = f.svc.ApproveTransfer(ctxBG(), actor, d2.Order.ID.Int64(), ApproveTransferInput{Action: "approve"}, header)
	require.NoError(t, err)
	led, ok := f.w.ledgerByKey(header + ":lock:" + no2 + ":1:112:101:900")
	require.True(t, ok, "头键应前缀合成到行级键")
	require.Equal(t, header+":lock:"+no2+":1:112:101:900", led.idemKey)
}

// TestIdemKeyShape_CountCompleteAdjustComposed 盘点完成差异调整（count 完成产生的
// 调整——§2.10 适用面）：adjust 行级键同样经头键前缀合成。
func TestIdemKeyShape_CountCompleteAdjustComposed(t *testing.T) {
	f := newFixture(t)
	actor := testActor("stocktaker")
	const header = "IDEM-CK-0001"

	// 范围仅 binSrc2（skuBatch/batchA 行）：盘盈 +5 → 完成时恰一条差异调整。
	cd, err := f.svc.CreateCount(ctxBG(), actor, CountInput{WarehouseID: whSrc, Scope: CountScope{Mode: "BIN", BinIDs: []int64{binSrc2}}})
	require.NoError(t, err)
	cid := cd.Order.ID.Int64()
	_, _, err = f.svc.StartCount(ctxBG(), actor, cid)
	require.NoError(t, err)
	rowBatch := findRowID(t, f, whSrc, binSrc2, skuBatch, batchA)
	_, err = f.svc.RegisterCountings(ctxBG(), actor, cid, CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowBatch, Qty: q(55)},
	}})
	require.NoError(t, err)
	_, _, err = f.svc.FinishCount(ctxBG(), actor, cid)
	require.NoError(t, err)
	done, replay, err := f.svc.CompleteCount(ctxBG(), testActor("manager"), cid, "差异属实", header)
	require.NoError(t, err)
	require.False(t, replay)

	// adjust 键 = "{头键}:adjust:{盘点单号}:{差异行号}"。
	require.Len(t, done.Differences, 1)
	wantKey := header + ":adjust:" + cd.Order.CountNo + ":" + itoa(int64(done.Differences[0].LineNo))
	_, ok := f.w.ledgerByKey(wantKey)
	require.True(t, ok, "盘点差异调整键应经头键前缀合成: %s", wantKey)

	// 同头键重试完成：状态守卫重放，不重复调整（T13 单据不变维度）。
	totals := map[int64]inventory.Qty{}
	for _, r := range f.w.rows {
		totals[r.id] = r.total
	}
	_, replay, err = f.svc.CompleteCount(ctxBG(), testActor("manager"), cid, "", header)
	require.NoError(t, err)
	require.True(t, replay)
	for _, r := range f.w.rows {
		require.Equal(t, totals[r.id], r.total, "重放不得二次调整 row=%d", r.id)
	}
}
