package returns

// Service 层单测（ask 约束：退货入库数量守恒、异常冻结/解冻、追溯链完整性、幂等重放——
// 内存替身；另覆盖状态机守卫与来源单校验）。集成测试（真实 PG 的 SQL 守卫/并发/唯一索引
// 裁决）在 integration_test.go（//go:build integration）。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

var ctx = context.Background()

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *response.Error
	if !errors.As(err, &e) {
		t.Fatalf("期望业务错误，得到: %v", err)
	}
	// response.Error 的机器可读码经 Error() 文本前缀承载（"CODE: message"）。
	return strings.SplitN(e.Error(), ":", 2)[0]
}

func expectCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，得到 nil", want)
	}
	if got := codeOf(t, err); got != want {
		t.Fatalf("期望错误码 %s，得到 %s（%v）", want, got, err)
	}
}

// seedSalesReturn 创建并审批一张 10 件的销售退货单（行 1，SKU 11）。
func seedSalesReturn(t *testing.T, env *testEnv, soNo string, wh, sku int64, qty int64) (*ReturnOrderView, *testEnv) {
	t.Helper()
	env.sales.seed(soNo, wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: qty})
	view, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: soNo, CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(qty), Reason: "质量问题"}},
	})
	if err != nil {
		t.Fatalf("创建销售退货失败: %v", err)
	}
	if _, err := env.svc.SubmitReturn(ctx, actorA(), view.ID.Int64()); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	view, err = env.svc.ApproveReturn(ctx, actorB(), view.ID.Int64(), ApproveInput{Approved: true, Opinion: "同意"})
	if err != nil {
		t.Fatalf("审批失败: %v", err)
	}
	if view.Status != ReturnStatusApproved {
		t.Fatalf("期望 APPROVED，得到 %s", view.Status)
	}
	return view, env
}

func actorA() Actor { return Actor{UserID: 1, Username: "张三", RequestID: "req-a"} }
func actorB() Actor { return Actor{UserID: 2, Username: "李四", RequestID: "req-b"} }

func qtyText(n int64) string {
	return stock.Qty(n * 10000).String()
}

func mustQty(n int64) stock.Qty { return stock.Qty(n * 10000) }

// ---- 1. 销售退货全链路 + 数量守恒 ----

func TestSalesReturnLifecycleAndQuantityConservation(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 1, 11, 101
	order, _ := seedSalesReturn(t, env, "SO-20261003-000001", wh, sku, 10)

	// 部分收货 4（入待检）
	v, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(4)}},
	})
	if err != nil {
		t.Fatalf("首次收货失败: %v", err)
	}
	if v.Status != ReturnStatusReceiving {
		t.Fatalf("首次收货后期望 RECEIVING，得到 %s", v.Status)
	}
	row := env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.pending != mustQty(4) || row.total != mustQty(4) || row.avail != 0 {
		t.Fatalf("首次收货后库存不符: %+v", row)
	}

	// 余量收货 6
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(6)}},
	}); err != nil {
		t.Fatalf("二次收货失败: %v", err)
	}
	row = env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.pending != mustQty(10) {
		t.Fatalf("二次收货后期望待检 10，得到 %s", row.pending)
	}
	if env.stock.totalOf() != mustQty(10) {
		t.Fatalf("数量守恒破坏：系统总库存=%s，期望 10", env.stock.totalOf())
	}

	// 提交质检（全部收齐才可提交）
	subView, subQCNo, err := env.svc.SubmitSalesQC(ctx, actorA(), order.ID.Int64(), SubmitQCInput{})
	if err != nil {
		t.Fatalf("提交质检失败: %v", err)
	}
	if subView.Status != ReturnStatusInQC || subQCNo == "" {
		t.Fatalf("提交质检后状态/质检单号不符: %s / %q", subView.Status, subQCNo)
	}

	// 质检结果：7 合格 → available，3 不良 → defective（§9.1 质检决定去向）
	v, err = env.svc.ApplySalesQCResult(ctx, actorB(), order.ID.Int64(), QCResultInput{
		QCNo:  subQCNo,
		Lines: []QCResultLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, QtyQualified: qtyText(7), QtyDefective: qtyText(3)}},
	})
	if err != nil {
		t.Fatalf("质检结果应用失败: %v", err)
	}
	if v.Status != ReturnStatusCompleted {
		t.Fatalf("全部检完期望 COMPLETED，得到 %s", v.Status)
	}
	// 质检单回写（问题 5 修复回归）：全量检完必须把质检模块的 QC 单收尾一次且仅一次，
	// 行结果为「合格 7 / 不良 3」——原先只建单不回写，QC 单会永远停在 PENDING。
	if got := env.qc.completionCount(); got != 1 {
		t.Fatalf("全量检完应恰回写质检单 1 次，实际 %d 次", got)
	}
	done := env.qc.completed[0]
	if done.QCNo != subQCNo {
		t.Fatalf("回写质检单号不符：%q，期望 %q", done.QCNo, subQCNo)
	}
	if len(done.Lines) != 1 || done.Lines[0].LineNo != 1 ||
		done.Lines[0].QtyQualified != qtyText(7) || done.Lines[0].QtyDefective != qtyText(3) {
		t.Fatalf("回写质检结果行不符: %+v", done.Lines)
	}
	row = env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.avail != mustQty(7) || row.defect != mustQty(3) || row.pending != 0 {
		t.Fatalf("质检后库存去向不符: %+v", row)
	}
	if env.stock.totalOf() != mustQty(10) {
		t.Fatalf("数量守恒破坏：质检后总库存=%s，期望 10（可用+不良=10）", env.stock.totalOf())
	}

	// 审计与审批记录（business-flow §12.2/§13.2）
	actions := env.spy.actions()
	for _, want := range []string{"create", "submit", "approve", "receive", "submit-qc", "apply-qc"} {
		found := false
		for _, a := range actions {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("审计缺动作 %s: %v", want, actions)
		}
	}
	if len(env.repo.approvals) < 2 { // SUBMIT + APPROVE
		t.Fatalf("审批记录缺失: %d", len(env.repo.approvals))
	}
}

// ---- 2. 超量收货拒绝 + 回滚守恒 ----

func TestSalesReturnOverReceiveRejectedAndRolledBack(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 1, 11, 101
	order, _ := seedSalesReturn(t, env, "SO-20261003-000002", wh, sku, 5)
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(4)}},
	}); err != nil {
		t.Fatalf("首次收货失败: %v", err)
	}
	// 4 + 7 > 5 → 拒绝；Putaway 已在事务内发生，必须整体回滚（守恒）。
	expectCode(t, func() error {
		_, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
			Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(7)}},
		})
		return err
	}(), "RETURNS_QTY_EXCEEDED")
	row := env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.pending != mustQty(4) || row.total != mustQty(4) {
		t.Fatalf("回滚后库存应为首次收货的 4：total=%s pending=%s", row.total, row.pending)
	}
	if env.stock.totalOf() != mustQty(4) {
		t.Fatalf("回滚后系统总库存=%s，期望 4", env.stock.totalOf())
	}
}

// ---- 3. 收货幂等重放（plan §8.5：重放不重复变更）----

func TestReceiveIdempotentReplay(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 1, 11, 101
	order, _ := seedSalesReturn(t, env, "SO-20261003-000003", wh, sku, 10)
	in := ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(5)}}}
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), in); err != nil {
		t.Fatalf("首次收货失败: %v", err)
	}
	// 完全相同请求重试：命中 ledger 幂等键 → 重放，不重复累计。
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), in); err != nil {
		t.Fatalf("幂等重放失败: %v", err)
	}
	row := env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.pending != mustQty(5) {
		t.Fatalf("重放后期望待检仍为 5，得到 %s", row.pending)
	}
	items, _ := env.repo.ListReturnItemsForUpdate(nil, order.ID.Int64())
	if items[0].QtyReceived != mustQty(5) {
		t.Fatalf("重放后期望已收 5，得到 %s", items[0].QtyReceived)
	}
}

// ---- 4. 部分重放 = 矛盾请求 → 整体回滚 ----

func TestReceivePartialReplayRejected(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku = 1, 11
	env.sales.seed("SO-20261003-000004", wh,
		ReturnableLine{LineNo: 1, SKUID: sku, Qty: 5},
		ReturnableLine{LineNo: 2, SKUID: 12, Qty: 3})
	order, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-20261003-000004", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{
			{LineNo: 1, SKUID: sku, QtyReturn: qtyText(5), Reason: "x"},
			{LineNo: 2, SKUID: 12, QtyReturn: qtyText(3), Reason: "x"},
		},
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	env.svc.SubmitReturn(ctx, actorA(), order.ID.Int64())
	env.svc.ApproveReturn(ctx, actorB(), order.ID.Int64(), ApproveInput{Approved: true})
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: qtyText(5)}},
	}); err != nil {
		t.Fatalf("首次收货失败: %v", err)
	}
	// 行 1 同键重放 + 行 2 新收货 → 矛盾，整体回滚。
	expectCode(t, func() error {
		_, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
			Lines: []ReceiveLineInput{
				{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: qtyText(5)},
				{LineNo: 2, ZoneID: 2, ShelfID: 3, BinID: 102, Qty: qtyText(3)},
			},
		})
		return err
	}(), "RETURNS_PARTIAL_REPLAY")
	if env.stock.totalOf() != mustQty(5) {
		t.Fatalf("回滚后总库存=%s，期望 5", env.stock.totalOf())
	}
}

// ---- 5. 状态机守卫 ----

func TestSalesReturnStateMachineGuards(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku = 1, 11
	order, _ := seedSalesReturn(t, env, "SO-20261003-000005", wh, sku, 5)

	// 未审批不可收货
	env.sales.seed("SO-20261003-000006", wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 1})
	v2, _ := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-20261003-000006", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(1), Reason: "x"}},
	})
	_, err := env.svc.ReceiveSalesReturn(ctx, actorA(), v2.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: qtyText(1)}},
	})
	expectCode(t, err, "RETURNS_STATUS_CONFLICT")

	// 重复审批（已 APPROVED 不可再 approve）
	_, err = env.svc.ApproveReturn(ctx, actorB(), order.ID.Int64(), ApproveInput{Approved: true})
	expectCode(t, err, "RETURNS_STATUS_CONFLICT")

	// 收货后不可取消（business-flow §13.3）
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: qtyText(5)}},
	}); err != nil {
		t.Fatalf("收货失败: %v", err)
	}
	_, err = env.svc.CancelReturn(ctx, actorA(), order.ID.Int64(), CancelInput{Reason: "不要了"})
	expectCode(t, err, "RETURNS_STATUS_CONFLICT")

	// 迁移表纯函数断言（含驳回回草稿）
	if !returnTransitionAllowed(ReturnTypeSales, ReturnStatusPendingApproval, ReturnStatusDraft) {
		t.Fatal("PENDING_APPROVAL→DRAFT 驳回应合法")
	}
	if returnTransitionAllowed(ReturnTypeSales, ReturnStatusReceiving, ReturnStatusCancelled) {
		t.Fatal("RECEIVING→CANCELLED 应非法")
	}
	if returnTransitionAllowed(ReturnTypePurchase, ReturnStatusShipped, ReturnStatusCancelled) {
		t.Fatal("SHIPPED→CANCELLED 应非法（已出库只能冲正）")
	}
}

// ---- 6. 序列号逐件（inventory-rules §8）----

func TestSalesReturnSerialUnits(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 1, 22, 201
	order, _ := seedSalesReturn(t, env, "SO-20261003-000007", wh, sku, 2)

	// 数量与序列号件数不一致 → 拒绝
	_, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2), Serials: []string{"SN-1"}}},
	})
	expectCode(t, err, "RETURNS_SERIAL_MISMATCH")

	// 正确逐件收货 → RETURNED
	if _, err := env.svc.ReceiveSalesReturn(ctx, actorA(), order.ID.Int64(), ReceiveInput{
		Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2), Serials: []string{"SN-1", "SN-2"}}},
	}); err != nil {
		t.Fatalf("序列号收货失败: %v", err)
	}
	for _, sn := range []string{"SN-1", "SN-2"} {
		s, ok := env.stock.serialOf(sn)
		if !ok || s.status != "RETURNED" || s.binID != bin {
			t.Fatalf("序列号 %s 台账不符: %+v ok=%v", sn, s, ok)
		}
	}

	// 质检合格逐件回 IN_STOCK
	_, subQCNo, err := env.svc.SubmitSalesQC(ctx, actorA(), order.ID.Int64(), SubmitQCInput{})
	if err != nil {
		t.Fatalf("提交质检失败: %v", err)
	}
	if _, err := env.svc.ApplySalesQCResult(ctx, actorB(), order.ID.Int64(), QCResultInput{
		QCNo:  subQCNo,
		Lines: []QCResultLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, QtyQualified: qtyText(2), SerialsPassed: []string{"SN-1", "SN-2"}}},
	}); err != nil {
		t.Fatalf("质检结果应用失败: %v", err)
	}
	for _, sn := range []string{"SN-1", "SN-2"} {
		s, _ := env.stock.serialOf(sn)
		if s.status != "IN_STOCK" {
			t.Fatalf("质检合格后 %s 应 IN_STOCK，得到 %s", sn, s.status)
		}
	}
}

// ---- 7. 来源单校验（plan §3.1 Reader + 累计防超退）----

func TestSalesReturnSourceValidation(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku = 1, 11
	// 来源单不存在
	_, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-NOPE", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(1), Reason: "x"}},
	})
	expectCode(t, err, "RETURNS_SOURCE_ORDER_NOT_FOUND")

	// 仓库不一致
	env.sales.seed("SO-1", 99, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 5})
	_, err = env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-1", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(1), Reason: "x"}},
	})
	expectCode(t, err, "RETURNS_SOURCE_MISMATCH")

	// 来源单没有该行（行号+SKU 必须一致）
	env.sales.seed("SO-2", wh, ReturnableLine{LineNo: 2, SKUID: sku, Qty: 5})
	_, err = env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-2", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(1), Reason: "x"}},
	})
	expectCode(t, err, "RETURNS_LINE_NOT_FOUND")

	// 累计防超退：两张退货单合计 ≤ 已发货量
	env.sales.seed("SO-3", wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 6})
	if _, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-3", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(4), Reason: "x"}},
	}); err != nil {
		t.Fatalf("第一张退货单创建失败: %v", err)
	}
	_, err = env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-3", CustomerID: 7, WarehouseID: wh,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(3), Reason: "x"}}, // 4+3 > 6
	})
	expectCode(t, err, "RETURNS_QTY_EXCEEDED")
}

// ---- 8. 采购退货：Lock→Deduct 出库 + 幂等重放 ----

func TestPurchaseReturnShipDeductAndReplay(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 2, 33, 301
	env.purchase.seed("PO-20261003-000001", wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 10})
	// 预置库存：available 10
	env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: 2, ShelfID: 3, BinID: bin, SKUID: sku},
		Qty: mustQty(10), Source: stock.Source{Type: "seed", No: "seed"},
	})
	order, err := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-20261003-000001", SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(10), Reason: "来料不良"}},
	})
	if err != nil {
		t.Fatalf("创建采购退货失败: %v", err)
	}
	if _, err := env.svc.SubmitReturn(ctx, actorA(), order.ID.Int64()); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if _, err := env.svc.ApproveReturn(ctx, actorB(), order.ID.Int64(), ApproveInput{Approved: true}); err != nil {
		t.Fatalf("审批失败: %v", err)
	}

	shipIn := ShipInput{Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(10)}}}
	if _, err := env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), shipIn); err != nil {
		t.Fatalf("退货出库失败: %v", err)
	}
	// Lock→Deduct 后：total -10，locked 0（核销），库存归零
	row := env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.total != 0 || row.locked != 0 || row.avail != 0 {
		t.Fatalf("出库后库存不符: %+v", row)
	}

	// 整单重试：幂等重放，不再扣减
	if _, err := env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), shipIn); err != nil {
		t.Fatalf("出库幂等重放失败: %v", err)
	}
	if env.stock.totalOf() != 0 {
		t.Fatalf("重放后总库存=%s，期望 0（不得重复扣减）", env.stock.totalOf())
	}

	// 完成
	v, err := env.svc.CompletePurchaseReturn(ctx, actorA(), order.ID.Int64())
	if err != nil {
		t.Fatalf("完成失败: %v", err)
	}
	if v.Status != ReturnStatusCompleted {
		t.Fatalf("期望 COMPLETED，得到 %s", v.Status)
	}
}

// ---- 9. 采购退货：出库数量/覆盖校验 ----

func TestPurchaseReturnShipValidation(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 2, 33, 301
	env.purchase.seed("PO-20261003-000002", wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 8})
	env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: 2, ShelfID: 3, BinID: bin, SKUID: sku},
		Qty: mustQty(8), Source: stock.Source{Type: "seed", No: "seed"},
	})
	order, _ := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-20261003-000002", SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(8), Reason: "x"}},
	})
	env.svc.SubmitReturn(ctx, actorA(), order.ID.Int64())
	env.svc.ApproveReturn(ctx, actorB(), order.ID.Int64(), ApproveInput{Approved: true})

	// 行数量 ≠ qty_return → 拒绝
	_, err := env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(5)}},
	})
	expectCode(t, err, "RETURNS_QTY_EXCEEDED")
	// 库存未被动过（事务回滚）
	if env.stock.totalOf() != mustQty(8) {
		t.Fatalf("失败出库后库存=%s，期望 8", env.stock.totalOf())
	}
	// 未覆盖全部行 → 拒绝（多行场景）
	env.purchase.seed("PO-20261003-000003", wh,
		ReturnableLine{LineNo: 1, SKUID: sku, Qty: 2},
		ReturnableLine{LineNo: 2, SKUID: 44, Qty: 2})
	order2, _ := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-20261003-000003", SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{
			{LineNo: 1, SKUID: sku, QtyReturn: qtyText(2), Reason: "x"},
			{LineNo: 2, SKUID: 44, QtyReturn: qtyText(2), Reason: "x"},
		},
	})
	env.svc.SubmitReturn(ctx, actorA(), order2.ID.Int64())
	env.svc.ApproveReturn(ctx, actorB(), order2.ID.Int64(), ApproveInput{Approved: true})
	_, err = env.svc.ShipPurchaseReturn(ctx, actorA(), order2.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2)}},
	})
	expectCode(t, err, "RETURNS_QTY_EXCEEDED")
}

// ---- 9.1 采购退货：序列号 SKU 逐件核销（inventory-rules §8.2，修复轮补齐）----

func TestPurchaseReturnShipSerials(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 2, 66, 301
	env.flags.set(sku, SKUFlags{Enabled: true, SerialManaged: true})
	env.purchase.seed("PO-20261003-000004", wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: 2})
	// 预置库存 + 序列号台账（SN-A/SN-B IN_STOCK 于 wh/bin）
	env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: 2, ShelfID: 3, BinID: bin, SKUID: sku},
		Qty: mustQty(2), Source: stock.Source{Type: "seed", No: "seed"},
	})
	for _, sn := range []string{"SN-A", "SN-B"} {
		env.stock.SerialEvent(ctx, nil, stock.SerialOp{
			SerialNo: sn, SKUID: sku, WarehouseID: wh, BinID: bin, Status: "IN_STOCK",
			Source: stock.Source{Type: "seed", No: "seed"},
		})
	}
	order, _ := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-20261003-000004", SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(2), Reason: "来料不良"}},
	})
	env.svc.SubmitReturn(ctx, actorA(), order.ID.Int64())
	env.svc.ApproveReturn(ctx, actorB(), order.ID.Int64(), ApproveInput{Approved: true})

	// 序列号 SKU 未采集序列号 → 拒绝（件数 ≠ 数量）
	_, err := env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2)}},
	})
	expectCode(t, err, "RETURNS_SERIAL_MISMATCH")
	// 采集了不存在的序列号 → 台账校验拒绝（fail-closed，不核销不存在的件）
	_, err = env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2), Serials: []string{"SN-A", "SN-NOPE"}}},
	})
	expectCode(t, err, "RETURNS_SERIAL_STATE_INVALID")
	if env.stock.totalOf() != mustQty(2) {
		t.Fatalf("失败出库后库存=%s，期望 2（事务回滚）", env.stock.totalOf())
	}

	// 正确逐件出库 → 台账 OUTBOUND 且脱离仓库/库位，库存归零
	if _, err := env.svc.ShipPurchaseReturn(ctx, actorA(), order.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(2), Serials: []string{"SN-A", "SN-B"}}},
	}); err != nil {
		t.Fatalf("序列号退货出库失败: %v", err)
	}
	for _, sn := range []string{"SN-A", "SN-B"} {
		s, ok := env.stock.serialOf(sn)
		if !ok || s.status != "OUTBOUND" || s.whID != 0 || s.binID != 0 {
			t.Fatalf("序列号 %s 出库后台账不符: %+v ok=%v", sn, s, ok)
		}
	}
	if env.stock.totalOf() != 0 {
		t.Fatalf("出库后总库存=%s，期望 0", env.stock.totalOf())
	}

	// 非序列号 SKU 带序列号 → 拒绝（独立单据；上单已 SHIPPED，重复请求为幂等重放）
	env.purchase.seed("PO-20261003-000005", wh, ReturnableLine{LineNo: 1, SKUID: 44, Qty: 1})
	env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: 2, ShelfID: 3, BinID: bin, SKUID: 44},
		Qty: mustQty(1), Source: stock.Source{Type: "seed", No: "seed"},
	})
	order2, _ := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-20261003-000005", SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 44, QtyReturn: qtyText(1), Reason: "x"}},
	})
	env.svc.SubmitReturn(ctx, actorA(), order2.ID.Int64())
	env.svc.ApproveReturn(ctx, actorB(), order2.ID.Int64(), ApproveInput{Approved: true})
	_, err = env.svc.ShipPurchaseReturn(ctx, actorA(), order2.ID.Int64(), ShipInput{
		Lines: []ShipLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: bin, Qty: qtyText(1), Serials: []string{"SN-A"}}},
	})
	expectCode(t, err, "COMMON_INVALID_PARAM")
}

// ---- 10. 异常冻结/解冻（inventory-rules §4.2）----

func TestExceptionFreezeAndRelease(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku, bin = 3, 55, 401
	env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: 2, ShelfID: 3, BinID: bin, SKUID: sku},
		Qty: mustQty(20), Source: stock.Source{Type: "seed", No: "seed"},
	})

	// 创建异常并冻结 5（EXCEPTION_FREEZE→frozen，plan §6.10）
	no, err := env.svc.CreateException(ctx, nil, CreateExceptionOp{
		Type: "库存异常", SourceType: "count_order", SourceNo: "CK-20261003-000001",
		Detail: "库存行差异", SKUID: sku, BinID: bin,
		Freeze: true, FreezeWarehouseID: wh, FreezeQty: qtyText(5), Actor: actorA(),
	})
	if err != nil {
		t.Fatalf("创建异常失败: %v", err)
	}
	if no == "" {
		t.Fatal("异常单号为空")
	}
	row := env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.frozen != mustQty(5) || row.avail != mustQty(15) {
		t.Fatalf("冻结后库存不符: %+v", row)
	}
	exs, _, _ := env.svc.ListExceptions(ctx, ExceptionFilter{Status: ExceptionStatusOpen, Page: 1, PageSize: 10})
	if len(exs) != 1 || exs[0].ExceptionNo != no || exs[0].FreezeLockID == 0 {
		t.Fatalf("异常单列表/冻结锁回指不符: %+v", exs)
	}
	exID := exs[0].IDInt.Int64()

	// 生命周期推进到 RESOLVED → 必须释放冻结（同事务 + RELEASE 流水）
	for _, step := range []struct {
		fn   func() error
		want string
	}{
		{func() error {
			_, err := env.svc.AssignException(ctx, actorB(), exID, ExceptionAssignInput{AssigneeID: 5, AssigneeName: "王五"})
			return err
		}, ExceptionStatusAssigned},
		{func() error { _, err := env.svc.StartException(ctx, actorA(), exID, ExceptionNoteInput{}); return err }, ExceptionStatusProcessing},
		{func() error { _, err := env.svc.ReviewException(ctx, actorA(), exID, ExceptionNoteInput{}); return err }, ExceptionStatusPendingReview},
	} {
		if err := step.fn(); err != nil {
			t.Fatalf("生命周期推进失败: %v", err)
		}
	}
	v, err := env.svc.ResolveException(ctx, actorB(), exID, ExceptionNoteInput{Note: "已调整"})
	if err != nil {
		t.Fatalf("解决失败: %v", err)
	}
	if v.Status != ExceptionStatusResolved || v.FreezeLockID != 0 {
		t.Fatalf("解决后状态/冻结锁不符: %s lock=%d", v.Status, v.FreezeLockID)
	}
	row = env.stock.stateOf(t, wh, 2, 3, bin, sku, 0)
	if row.frozen != 0 || row.avail != mustQty(20) {
		t.Fatalf("解冻后库存不符: %+v", row)
	}

	// 关闭
	v, err = env.svc.CloseException(ctx, actorB(), exID, ExceptionNoteInput{Note: "归档"})
	if err != nil || v.Status != ExceptionStatusClosed {
		t.Fatalf("关闭失败: %v %s", err, v.Status)
	}
	// 处理记录追加式（freeze/assign/start/review/resolve/release/close，不覆盖历史）
	fresh, _ := env.svc.GetException(ctx, exID)
	if len(fresh.HandleRecords) < 6 {
		t.Fatalf("处理记录数不足: %d", len(fresh.HandleRecords))
	}
}

// ---- 11. 异常状态机与创建校验 ----

func TestExceptionGuardsAndValidation(t *testing.T) {
	env := newTestEnv(t)
	// 非法类型
	_, err := env.svc.CreateException(ctx, nil, CreateExceptionOp{Type: "外星异常", Actor: actorA()})
	expectCode(t, err, "RETURNS_EXCEPTION_TYPE_INVALID")
	// 冻结缺定位
	_, err = env.svc.CreateException(ctx, nil, CreateExceptionOp{Type: "库存异常", Freeze: true, SKUID: 1, Actor: actorA()})
	expectCode(t, err, "RETURNS_FREEZE_TARGET_REQUIRED")
	// 冻结缺量
	_, err = env.svc.CreateException(ctx, nil, CreateExceptionOp{
		Type: "库存异常", Freeze: true, FreezeWarehouseID: 1, BinID: 2, SKUID: 3, Actor: actorA(),
	})
	expectCode(t, err, "COMMON_INVALID_PARAM")

	if _, err := env.svc.CreateException(ctx, nil, CreateExceptionOp{Type: "拣货异常", SourceType: "pick_task", SourceNo: "PK-1", Actor: actorA()}); err != nil {
		t.Fatalf("创建拣货异常失败: %v", err)
	}
	exs, _, _ := env.svc.ListExceptions(ctx, ExceptionFilter{SourceNo: "PK-1", Page: 1, PageSize: 10})
	if len(exs) != 1 {
		t.Fatalf("异常列表数=%d", len(exs))
	}
	id := exs[0].IDInt.Int64()

	// 跳状态：OPEN 直接 resolve → 冲突
	_, err = env.svc.ResolveException(ctx, actorA(), id, ExceptionNoteInput{})
	expectCode(t, err, "RETURNS_EXCEPTION_STATUS_CONFLICT")
	// 重复分派 → 冲突
	env.svc.AssignException(ctx, actorA(), id, ExceptionAssignInput{AssigneeID: 3, AssigneeName: "x"})
	_, err = env.svc.AssignException(ctx, actorA(), id, ExceptionAssignInput{AssigneeID: 4, AssigneeName: "y"})
	expectCode(t, err, "RETURNS_EXCEPTION_STATUS_CONFLICT")
}

// ---- 12. 追溯链完整性（inventory-rules §10）----

func TestTraceChainCompleteness(t *testing.T) {
	env := newTestEnv(t)
	const wh, sku = 1, 11
	base := time.Date(2026, 10, 3, 8, 0, 0, 0, time.Local)
	led := func(i int, changeType, bizType, bizNo, from, to, serial string, qty int64) TraceLedger {
		return TraceLedger{
			ID: int64(i), LedgerNo: "LED-" + string(rune('A'+i)), SKUID: sku, WarehouseID: wh,
			BinID: 101, ChangeType: changeType, BusinessType: bizType, BusinessNo: bizNo,
			StatusFrom: from, StatusTo: to, SerialNo: serial,
			QtyBefore: qtyText(0), QtyChange: qtyText(qty), QtyAfter: qtyText(qty),
			OperatorName: "张三", RequestID: "req-" + string(rune('A'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		}
	}
	env.ledgers.seed(
		led(1, "INBOUND", "purchase_order", "PO-1", "available", "available", "", 10),          // 入库来源
		led(2, "TRANSFER_OUT", "return_order", "TR-1", "locked", "locked", "", -10),            // 调拨出
		led(3, "TRANSFER_IN", "return_order", "TR-1", "available", "available", "", 10),        // 调拨入
		led(4, "OUTBOUND", "sales_order", "SO-1", "locked", "locked", "", -10),                 // 出库
		led(5, "INBOUND", "return_order", "RT-1", "pending_inspect", "pending_inspect", "", 3), // 退货
	)
	env.state.seedRows(TraceStockRow{
		WarehouseID: wh, BinID: 101, SKUID: sku, Total: qtyText(3), Available: qtyText(3),
	})
	env.state.seedSerial("SN-9", TraceSerialRow{SerialNo: "SN-9", SKUID: sku, Status: "RETURNED", WarehouseID: wh, BinID: 101})
	env.repo.seedOperationLog(middleware.OperationLog{ID: 9001, RequestID: "req-B", Module: "inventory", ObjectType: "inventory", Action: "lock", Success: true})
	env.repo.seedOperationLog(middleware.OperationLog{ID: 9002, RequestID: "req-E", Module: "inventory", ObjectType: "inventory", Action: "putaway", Success: true})

	// 按 SKU 追溯：链完整（5 条、时间正序）、单据富化、操作日志关联
	res, err := env.svc.Trace(ctx, TraceQuery{SKUID: sku, Limit: 100}, TraceScope{AllWarehouses: true})
	if err != nil {
		t.Fatalf("追溯失败: %v", err)
	}
	if len(res.Chain) != 5 {
		t.Fatalf("追溯链长度=%d，期望 5（入库→调拨→出库→退货 全链）", len(res.Chain))
	}
	for i := 1; i < len(res.Chain); i++ {
		if res.Chain[i].CreatedAt.Before(res.Chain[i-1].CreatedAt) {
			t.Fatal("追溯链非时间正序")
		}
	}
	if len(res.StockRows) != 1 || res.StockRows[0].BinID != 101 {
		t.Fatalf("当前状态缺失: %+v", res.StockRows)
	}
	foundSO, foundPO := false, false
	for _, d := range res.Documents {
		if d.Type == "sales_order" && d.No == "SO-1" {
			foundSO = true
		}
		if d.Type == "purchase_order" && d.No == "PO-1" {
			foundPO = true
		}
	}
	if !foundSO || !foundPO {
		t.Fatalf("单据富化缺失: SO-1=%v PO-1=%v", foundSO, foundPO)
	}
	if len(res.Operations) != 2 {
		t.Fatalf("操作日志关联数=%d，期望 2", len(res.Operations))
	}

	// 按序列号追溯：定位 SKU + 序列号台账 + 生命周期链
	env.ledgers.seed(led(6, "INBOUND", "return_order", "RT-2", "pending_inspect", "pending_inspect", "SN-9", 1))
	res, err = env.svc.Trace(ctx, TraceQuery{SerialNo: "SN-9"}, TraceScope{AllWarehouses: true})
	if err != nil {
		t.Fatalf("序列号追溯失败: %v", err)
	}
	if res.SKUID != sku || res.Serial == nil || res.Serial.SerialNo != "SN-9" {
		t.Fatalf("序列号追溯定位不符: %+v", res)
	}
	if len(res.Chain) != 1 || res.Chain[0].SerialNo != "SN-9" {
		t.Fatalf("序列号链过滤不符: %+v", res.Chain)
	}

	// 参数缺失 / 序列号不存在
	expectCode(t, func() error {
		_, err := env.svc.Trace(ctx, TraceQuery{}, TraceScope{AllWarehouses: true})
		return err
	}(), "RETURNS_TRACE_PARAM_REQUIRED")
	expectCode(t, func() error {
		_, err := env.svc.Trace(ctx, TraceQuery{SerialNo: "SN-404"}, TraceScope{AllWarehouses: true})
		return err
	}(), "RETURNS_SERIAL_NOT_FOUND")
}

// ---- 13. 追溯数据权限（permission.md §4）----

func TestTraceScopeEnforcement(t *testing.T) {
	env := newTestEnv(t)
	env.ledgers.seed(TraceLedger{ID: 1, SKUID: 11, WarehouseID: 2, ChangeType: "INBOUND", CreatedAt: time.Now()})
	env.state.seedRows(TraceStockRow{WarehouseID: 2, SKUID: 11})
	// 请求仓库越界（范围未含仓 2）→ 403
	_, err := env.svc.Trace(ctx, TraceQuery{SKUID: 11, WarehouseID: 2}, TraceScope{WarehouseIDs: []int64{1}})
	expectCode(t, err, "COMMON_PERMISSION_DENIED")
	// 多仓范围未指定仓库 → 403（fail-closed）
	_, err = env.svc.Trace(ctx, TraceQuery{SKUID: 11}, TraceScope{WarehouseIDs: []int64{1, 3}})
	expectCode(t, err, "COMMON_PERMISSION_DENIED")
	// 空范围 → 403
	_, err = env.svc.Trace(ctx, TraceQuery{SKUID: 11}, TraceScope{})
	expectCode(t, err, "COMMON_PERMISSION_DENIED")
	// 范围内仓库 → 通过，且只能看到范围内数据（仓 2 的流水/库存不泄漏）
	res, err := env.svc.Trace(ctx, TraceQuery{SKUID: 11, WarehouseID: 1}, TraceScope{WarehouseIDs: []int64{1}})
	if err != nil {
		t.Fatalf("范围内追溯失败: %v", err)
	}
	if len(res.Chain) != 0 || len(res.StockRows) != 0 {
		t.Fatalf("范围外数据泄漏: %+v", res)
	}
}

// ---- 14. 权限点常量契约（plan §9.2 逐字冻结）----

func TestPermissionConstants(t *testing.T) {
	cases := map[string]string{
		PermSalesReturnList: "returns:salesreturn:list", PermSalesReturnRead: "returns:salesreturn:read",
		PermSalesReturnCreate: "returns:salesreturn:create", PermSalesReturnUpdate: "returns:salesreturn:update",
		PermSalesReturnSubmit: "returns:salesreturn:submit", PermSalesReturnApprove: "returns:salesreturn:approve",
		PermSalesReturnExecute: "returns:salesreturn:execute", PermSalesReturnCancel: "returns:salesreturn:cancel",
		PermSalesReturnClose:   "returns:salesreturn:close",
		PermPurchaseReturnList: "returns:purchasereturn:list", PermPurchaseReturnRead: "returns:purchasereturn:read",
		PermPurchaseReturnCreate: "returns:purchasereturn:create", PermPurchaseReturnUpdate: "returns:purchasereturn:update",
		PermPurchaseReturnSubmit: "returns:purchasereturn:submit", PermPurchaseReturnApprove: "returns:purchasereturn:approve",
		PermPurchaseReturnExecute: "returns:purchasereturn:execute", PermPurchaseReturnCancel: "returns:purchasereturn:cancel",
		PermPurchaseReturnClose: "returns:purchasereturn:close",
		PermExceptionList:       "returns:exception:list", PermExceptionRead: "returns:exception:read",
		PermExceptionCreate: "returns:exception:create", PermExceptionAssign: "returns:exception:assign",
		PermExceptionExecute: "returns:exception:execute", PermExceptionClose: "returns:exception:close",
		PermTraceList: "returns:trace:list",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("权限点与 plan §9.2 不一致: %s != %s", got, want)
		}
	}
}
