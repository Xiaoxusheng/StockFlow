package purchase

// Service 层单测（内存替身，不依赖 PostgreSQL/Redis——ask 约束）。
// 覆盖：状态机迁移矩阵、超量收货拒绝（§2.3）、收货幂等重放（§3.2/architecture §3.2）、
// 任务原子抢占互斥（architecture §5.2）、部分/异常收货、质检→InspectResult 映射、
// 免检直通、序列号/批次采集、审计同事务落库。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// qty 十进制数量便捷构造。
func qty(t *testing.T, s string) stock.Qty {
	t.Helper()
	q, err := stock.ParseQty(s)
	require.NoError(t, err)
	return q
}

// codeOf 业务错误码提取（response.Error 的 Error() 渲染为 "CODE: message"；
// code 字段未导出，测试经字符串前缀比对——与 handler 输出信封同源）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var be *response.Error
	require.True(t, errors.As(err, &be), "必须为 *response.Error: %v", err)
	return strings.SplitN(be.Error(), ":", 2)[0]
}

func testActor() Actor {
	return Actor{UserID: 9, Username: "测试员", IsSuper: false, RequestID: "req-test", IP: "127.0.0.1"}
}

func dateOf(t *testing.T, s string) database.JSONTime {
	t.Helper()
	var d database.JSONTime
	require.NoError(t, d.UnmarshalJSON([]byte(`"`+s+`"`)))
	return d
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

type testEnv struct {
	svc   *Service
	repo  *fakeRepo
	stock *fakeStock
	chk   *fakeCheckers
	rec   *fakeRecommender
	exc   *fakeExceptions
	spy   *auditSpy
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	spy := &auditSpy{}
	repo := newFakeRepo(spy)
	fs := newFakeStock()
	chk := newFakeCheckers()
	chk.seedSKU(100, true, false, false, false) // 普通品
	chk.seedSKU(101, true, true, true, false)   // 批次+效期管理
	chk.seedSKU(102, true, false, false, true)  // 序列号管理
	rec := newFakeRecommender()
	exc := &fakeExceptions{}
	svc := NewService(repo,
		WithStock(fs),
		WithSupplierChecker(supplierCheckerAdapter{chk}),
		WithWarehouseChecker(warehouseCheckerAdapter{chk}),
		WithSKUAttrReader(skuAttrAdapter{chk}),
		WithBinChecker(binCheckerAdapter{chk}),
		WithBinRecommender(rec),
		WithExceptionCreator(exc),
	)
	return &testEnv{svc: svc, repo: repo, stock: fs, chk: chk, rec: rec, exc: exc, spy: spy}
}

// seedApprovedChain 建立已审核采购订单 + 采购入库单（经真实 Service 路径创建）。
func (e *testEnv) seedApprovedChain(t *testing.T, sourceType, sourceNo string, lines map[int64]stock.Qty) (*PurchaseOrder, *InboundOrder) {
	t.Helper()
	ctx := context.Background()
	actor := testActor()
	var items []POItemInput
	for sku, q := range lines {
		items = append(items, POItemInput{SKUID: sku, Qty: q, Price: 10000})
	}
	po, err := e.svc.CreatePO(ctx, actor, POCreateInput{SupplierID: 11, WarehouseID: 1, Items: items})
	require.NoError(t, err)
	_, err = e.svc.SubmitPO(ctx, actor, po.ID.Int64())
	require.NoError(t, err)
	po, err = e.svc.ApprovePO(ctx, actor, po.ID.Int64(), POApproveInput{Approved: true, Opinion: "同意"})
	require.NoError(t, err)
	require.Equal(t, POStatusApproved, po.Status)

	var inItems []InboundItemInput
	for sku, q := range lines {
		inItems = append(inItems, InboundItemInput{SKUID: sku, Qty: q})
	}
	if sourceType == SourceTypePurchase {
		sourceNo = po.PONo // 采购入库必须回填采购订单号（api.md §4 业务关系）
	}
	in, err := e.svc.CreateInbound(ctx, actor, InboundCreateInput{
		SourceType: sourceType, SourceNo: sourceNo, WarehouseID: 1, Items: inItems,
	})
	require.NoError(t, err)
	return e.repo.po(po.ID.Int64()), e.repo.inbounds[in.ID.Int64()]
}

// ---- 1. 采购订单状态机迁移矩阵（business-flow §2.2、plan §6.1）----

func TestPOStateMachineTransitionMatrix(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()

	// 每行：起始状态 → 动作 → 期望目标状态（合法迁移；收货驱动迁移另有专项测试）。
	newPO := func(status string) *PurchaseOrder {
		po := e.repo.seedPO(status, 1, map[int64]stock.Qty{100: qty(t, "10")})
		if status == POStatusPartialReceived || status == POStatusReceivedAll {
			items, _ := e.repo.ListPOItems(ctx, po.ID.Int64())
			recv := qty(t, "4")
			if status == POStatusReceivedAll {
				recv = qty(t, "10")
			}
			_, _ = e.repo.AddPOItemReceived(ctx, nil, items[0].ID.Int64(), recv, 0, 9)
		}
		return po
	}

	legal := []struct {
		name   string
		status string
		act    func(poID int64) error
		want   string
	}{
		{"草稿提交审核", POStatusDraft, func(id int64) error {
			_, err := e.svc.SubmitPO(ctx, actor, id)
			return err
		}, POStatusPendingApproval},
		{"草稿取消", POStatusDraft, func(id int64) error {
			_, err := e.svc.CancelPO(ctx, actor, id)
			return err
		}, POStatusCancelled},
		{"待审核通过", POStatusPendingApproval, func(id int64) error {
			_, err := e.svc.ApprovePO(ctx, actor, id, POApproveInput{Approved: true})
			return err
		}, POStatusApproved},
		{"待审核驳回回草稿", POStatusPendingApproval, func(id int64) error {
			_, err := e.svc.ApprovePO(ctx, actor, id, POApproveInput{Approved: false, Opinion: "单价不符"})
			return err
		}, POStatusDraft},
		{"待审核取消", POStatusPendingApproval, func(id int64) error {
			_, err := e.svc.CancelPO(ctx, actor, id)
			return err
		}, POStatusCancelled},
		{"已审核取消（无收货）", POStatusApproved, func(id int64) error {
			_, err := e.svc.CancelPO(ctx, actor, id)
			return err
		}, POStatusCancelled},
		{"部分到货差额关闭", POStatusPartialReceived, func(id int64) error {
			_, err := e.svc.ClosePO(ctx, actor, id, POCloseInput{Reason: "余量取消采购"})
			return err
		}, POStatusCompleted},
		{"到货完成差额关闭", POStatusReceivedAll, func(id int64) error {
			_, err := e.svc.ClosePO(ctx, actor, id, POCloseInput{Reason: "差额关闭"})
			return err
		}, POStatusCompleted},
	}
	for _, tc := range legal {
		t.Run(tc.name, func(t *testing.T) {
			po := newPO(tc.status)
			require.NoError(t, tc.act(po.ID.Int64()))
			fresh, err := e.svc.GetPO(ctx, po.ID.Int64(), WarehouseScope{All: true})
			require.NoError(t, err)
			require.Equal(t, tc.want, fresh.Order.Status)
		})
	}

	// 非法迁移：当前状态不允许该动作 → ErrPOStatusNotAllowed（409，§13.2 状态机守卫）。
	illegal := []struct {
		name   string
		status string
		act    func(poID int64) error
	}{
		{"草稿不能审核", POStatusDraft, func(id int64) error {
			_, err := e.svc.ApprovePO(ctx, actor, id, POApproveInput{Approved: true})
			return err
		}},
		{"草稿不能关闭", POStatusDraft, func(id int64) error {
			_, err := e.svc.ClosePO(ctx, actor, id, POCloseInput{Reason: "x"})
			return err
		}},
		{"已审核不能重复提交", POStatusApproved, func(id int64) error {
			_, err := e.svc.SubmitPO(ctx, actor, id)
			return err
		}},
		{"部分到货不能取消", POStatusPartialReceived, func(id int64) error {
			_, err := e.svc.CancelPO(ctx, actor, id)
			return err
		}},
		{"已完成不能取消", POStatusCompleted, func(id int64) error {
			_, err := e.svc.CancelPO(ctx, actor, id)
			return err
		}},
		{"已完成不能关闭", POStatusCompleted, func(id int64) error {
			_, err := e.svc.ClosePO(ctx, actor, id, POCloseInput{Reason: "x"})
			return err
		}},
		{"已取消不能提交", POStatusCancelled, func(id int64) error {
			_, err := e.svc.SubmitPO(ctx, actor, id)
			return err
		}},
	}
	for _, tc := range illegal {
		t.Run(tc.name, func(t *testing.T) {
			po := newPO(tc.status)
			err := tc.act(po.ID.Int64())
			require.Equal(t, ErrPOStatusNotAllowed.Code, codeOf(t, err))
		})
	}
}

func TestPOCancelWithReceiptsRejected(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	po, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "4")}},
	}, "op-cancel-rc")
	require.NoError(t, err)
	_, err = e.svc.CancelPO(ctx, actor, po.ID.Int64())
	require.Error(t, err, "有收货的订单禁止取消（§13.3 反向冲正）")
	got := codeOf(t, err)
	require.True(t, got == ErrPOStatusNotAllowed.Code || got == ErrPOHasReceipts.Code, "实际: %s", got)
}

// ---- 2. 超量收货拒绝（business-flow §2.3 / plan §6.1：累计收货 ≤ 原始数量）----

func TestConfirmReceiptOverReceiptRejected(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	po, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})

	// 一次超量：11 > 10。
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "11")}},
	}, "op-over-1")
	require.Error(t, err)
	require.Equal(t, ErrOverReceipt.Code, codeOf(t, err))
	// 整体回滚：无收货落库、状态未动、无任务生成。
	require.Empty(t, e.repo.receipts)
	require.Equal(t, POStatusApproved, e.repo.po(po.ID.Int64()).Status)
	require.Equal(t, InboundStatusDraft, e.repo.inbounds[in.ID.Int64()].Status)
	require.Empty(t, e.stock.putaways)

	// 分次收货合计超量（§3.3 部分收货）：6 合法 → 余 4；再收 5 拒绝；再收 4 收齐。
	first, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "6")}},
	}, "op-part-1")
	require.NoError(t, err)
	require.Equal(t, POStatusPartialReceived, first.POStatus)

	_, err = e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "5")}},
	}, "op-part-2")
	require.Error(t, err)
	require.Equal(t, ErrOverReceipt.Code, codeOf(t, err))

	second, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "4"), RequireInspect: boolPtr(false)}},
	}, "op-part-3")
	require.NoError(t, err)
	require.Equal(t, POStatusReceivedAll, second.POStatus)
	require.Equal(t, InboundStatusAwaitingQC, second.InboundStatus,
		"免检直通 4 件已计入处理量，但 6 件待检未质检——入库单停留 AWAITING_QC")

	items, _ := e.repo.ListPOItems(ctx, po.ID.Int64())
	require.Equal(t, "10.0000", items[0].QtyReceived.String(), "累计收货恰为订单量")
}

// ---- 3. 收货确认幂等重放（architecture §3.2 部分唯一索引 + plan §8.5）----

func TestConfirmReceiptIdempotentReplay(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})

	in1 := ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "10")}},
	}
	first, err := e.svc.ConfirmReceipt(ctx, actor, in1, "idem-op-1")
	require.NoError(t, err)
	require.False(t, first.Replay)
	require.NotEmpty(t, first.ReceiptNo)
	require.Len(t, first.PutawayTasks, 1)

	replay, err := e.svc.ConfirmReceipt(ctx, actor, in1, "idem-op-1")
	require.NoError(t, err)
	require.True(t, replay.Replay, "同幂等键重放必须返回既有结果")
	require.Equal(t, first.ReceiptNo, replay.ReceiptNo)
	require.Equal(t, first.PutawayTasks, replay.PutawayTasks)

	items, _ := e.repo.ListInboundItems(ctx, in.ID.Int64())
	require.Equal(t, "10.0000", items[0].QtyReceived.String(), "重放不得重复累计")

	// 幂等键唯一索引兜底建模：fakeRepo 对同键二次插入返回 23505（uk_receipts_idempotency）。
	err = e.repo.InsertReceipt(ctx, nil, &Receipt{
		ReceiptNo: "RC-DUP", IdempotencyKey: strPtr("idem-op-1"),
	}, nil)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23505", pgErr.Code)
}

// ---- 4. 上架任务原子抢占互斥（architecture §5.2：并发领取恰一成功）----

func TestClaimPutawayTaskConcurrentMutex(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})
	actor := testActor()
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "10")}},
	}, "op-claim")
	require.NoError(t, err)
	tasks, _, err := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	taskID := tasks[0].ID.Int64()

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			a := testActor()
			a.UserID = int64(100 + idx)
			_, err := e.svc.ClaimPutawayTask(ctx, a, taskID)
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	wins := 0
	var winnerID int64
	for i, err := range errs {
		if err == nil {
			wins++
			winnerID = int64(100 + i)
			continue
		}
		require.Equal(t, ErrPutawayClaimConflict.Code, codeOf(t, err), "失败方必须为业务冲突而非内部错误")
	}
	require.Equal(t, 1, wins, "并发领取必须恰一路成功（architecture §5.2）")
	task, err := e.svc.GetTask(ctx, taskID, WarehouseScope{All: true})
	require.NoError(t, err)
	require.Equal(t, TaskStatusInProgress, task.Status)
	require.Equal(t, winnerID, task.ClaimedBy)
}

// ---- 5. 完整链路：质检 → InspectResult（pending_inspect → available/defective）----

func TestQualityExecuteMapsInspectResult(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{101: qty(t, "5")})

	rc, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines: []ReceiptLineInput{{
			SKUID: 101, QtyGood: qty(t, "5"),
			BatchNo: "B20261001", ExpiryDate: dateOf(t, "2027-10-01"),
		}},
	}, "op-qc-rc")
	require.NoError(t, err)
	require.Equal(t, InboundStatusAwaitingQC, rc.InboundStatus)
	require.Len(t, e.stock.batches, 1, "批次管理 SKU 收货必须采集批次（inventory-rules §6）")

	// 上架任务领取 + 确认 → Putaway(RequireInspect=true) 落账进待检。
	tasks, _, err := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, FromStatePendingInspect, tasks[0].FromState)
	require.Positive(t, tasks[0].BatchID, "任务携带批次维度（PutawayOp 构造所需）")
	_, err = e.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
	require.NoError(t, err)
	_, err = e.svc.ExecutePutawayTask(ctx, actor, tasks[0].ID.Int64(), PutawayExecuteInput{})
	require.NoError(t, err)
	require.Len(t, e.stock.putaways, 1)
	require.True(t, e.stock.putaways[0].RequireInspect)
	// 待检量未质检处理，入库单停留 AWAITING_QC。
	got, err := e.svc.GetInbound(ctx, in.ID.Int64(), WarehouseScope{All: true})
	require.NoError(t, err)
	require.Equal(t, InboundStatusAwaitingQC, got.Order.Status)

	qc, err := e.svc.CreateQC(ctx, actor, QCCreateInput{
		SourceNo: in.InboundNo, InspectionType: InspectionSample,
		Lines: []QCLineInput{{SKUID: 101, BatchNo: "B20261001", QtyInspected: qty(t, "5")}},
	})
	require.NoError(t, err)
	// 未开始检验不可直接完成（状态机 PENDING→INSPECTING→COMPLETED）。
	_, err = e.svc.ExecuteQC(ctx, actor, qc.ID.Int64(), QCExecuteInput{
		Lines:  []QCExecuteLine{{LineNo: 1, QtyQualified: qty(t, "3"), QtyDefective: qty(t, "2")}},
		Result: "部分合格",
	})
	require.Error(t, err)
	require.Equal(t, ErrQCStatusNotAllowed.Code, codeOf(t, err))
	// 正确路径：start → execute。
	qc, err = e.svc.StartQC(ctx, actor, qc.ID.Int64())
	require.NoError(t, err)
	require.Equal(t, QCStatusInspecting, qc.Status)
	qc, err = e.svc.ExecuteQC(ctx, actor, qc.ID.Int64(), QCExecuteInput{
		Lines:  []QCExecuteLine{{LineNo: 1, QtyQualified: qty(t, "3"), QtyDefective: qty(t, "2")}},
		Result: "部分合格",
	})
	require.NoError(t, err)
	require.Equal(t, QCStatusCompleted, qc.Status)

	// 库存映射断言：合格 3 → available（pass），不良 2 → defective；键构成符合 plan §7。
	prefix := fmt.Sprintf("inspect:%s:%d:", qc.QCNo, 1)
	passOps := e.stock.inspectOpsByKey(prefix + "pass:")
	defOps := e.stock.inspectOpsByKey(prefix + "defect:")
	require.Len(t, passOps, 1)
	require.Len(t, defOps, 1)
	require.Equal(t, "3.0000", passOps[0].Qty.String())
	require.True(t, passOps[0].Pass)
	require.False(t, defOps[0].Pass)
	require.Equal(t, "2.0000", defOps[0].Qty.String())
	require.Equal(t, "quality_order", passOps[0].Source.Type)
	require.Equal(t, qc.QCNo, passOps[0].Source.No)
	require.Contains(t, passOps[0].IdempotencyKey,
		fmt.Sprintf(":%d:%d:%d", tasks[0].TargetBinID, 101, tasks[0].BatchID),
		"键尾缀含 bin:sku:batch 五维尾缀（plan §7 构成律）")

	// 全部明细处理完成 + 任务全部完成 → 入库单 COMPLETED。
	got, err = e.svc.GetInbound(ctx, in.ID.Int64(), WarehouseScope{All: true})
	require.NoError(t, err)
	require.Equal(t, InboundStatusCompleted, got.Order.Status)

	// 采购订单上架量累计 → 可无原因关闭（上架全部完成，plan §6.1）。
	poRow, err := e.repo.FindPOByNo(ctx, in.SourceNo)
	require.NoError(t, err)
	require.NotNil(t, poRow)
	poGot, err := e.svc.GetPO(ctx, poRow.ID.Int64(), WarehouseScope{All: true})
	require.NoError(t, err)
	require.Equal(t, "5.0000", poGot.Items[0].QtyPutaway.String())
	closed, err := e.svc.ClosePO(ctx, actor, poRow.ID.Int64(), POCloseInput{})
	require.NoError(t, err)
	require.Equal(t, POStatusCompleted, closed.Status)
}

// ---- 6. 免检直通（require_inspect=false → available）----

func TestExemptReceiptDirectAvailable(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "8")})
	rc, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "8"), RequireInspect: boolPtr(false)}},
	}, "op-exempt")
	require.NoError(t, err)
	require.Equal(t, InboundStatusAwaitingPutaway, rc.InboundStatus,
		"全免检收齐即通过质检判定（免检直通），等待上架任务完成")

	tasks, _, err := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, FromStateAvailable, tasks[0].FromState)
	_, err = e.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
	require.NoError(t, err)
	_, err = e.svc.ExecutePutawayTask(ctx, actor, tasks[0].ID.Int64(), PutawayExecuteInput{})
	require.NoError(t, err)
	require.Len(t, e.stock.putaways, 1)
	require.False(t, e.stock.putaways[0].RequireInspect, "免检任务 Putaway 直达 available")
	require.Equal(t, "8.0000", e.stock.putaways[0].Qty.String())
	require.Contains(t, e.stock.putaways[0].IdempotencyKey, "putaway:"+in.InboundNo+":", "幂等键 plan §7 构成")
	got, err := e.svc.GetInbound(ctx, in.ID.Int64(), WarehouseScope{All: true})
	require.NoError(t, err)
	require.Equal(t, InboundStatusCompleted, got.Order.Status, "全部任务完成后入库单完成")
}

// ---- 7. 异常收货登记异常中心（§3.4 → §11.2）----

func TestExceptionReceiptRegistersExceptionCenter(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})
	res, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines: []ReceiptLineInput{{
			SKUID: 100, QtyGood: qty(t, "8"), QtyRejected: qty(t, "2"),
			ExceptionType: "破损", ExceptionNote: "外箱破损 2 件",
		}},
	}, "op-exc")
	require.NoError(t, err)
	require.Len(t, e.exc.calls, 1)
	require.Equal(t, "收货异常", e.exc.calls[0].ExcType, "§11.2 收货异常类目")
	require.Equal(t, "inbound_order", e.exc.calls[0].SourceType)
	require.Equal(t, in.InboundNo, e.exc.calls[0].SourceNo)
	require.Contains(t, e.exc.calls[0].Detail, "破损")

	// 收货明细携带异常单号（exception_ref）与拒收单独记录（§2.3）。
	detail, err := e.svc.GetReceiptByNo(ctx, res.ReceiptNo, WarehouseScope{All: true})
	require.NoError(t, err)
	require.NotEmpty(t, detail.Items)
	require.Equal(t, "EX-TEST-0001", detail.Items[0].ExceptionRef)
	require.Equal(t, "2.0000", detail.Items[0].QtyRejected.String())
}

// ---- 8. 序列号 SKU 收货/上架（inventory-rules §8）----

func TestSerialReceiptAndPutaway(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{102: qty(t, "2")})
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines: []ReceiptLineInput{{
			SKUID: 102, QtyGood: qty(t, "2"),
			Serials: []string{"SN-0001", "SN-0002"}, RequireInspect: boolPtr(false),
		}},
	}, "op-serial")
	require.NoError(t, err)
	// 收货采集：逐件 SerialEvent（在库、库位待上架）。
	require.Len(t, e.stock.serials, 2)
	require.Equal(t, "IN_STOCK", e.stock.serials[0].Status)
	require.Equal(t, int64(0), e.stock.serials[0].BinID)
	// 序列号件数与合格数量不一致 → 拒绝。
	_, err = e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 102, QtyGood: qty(t, "1"), Serials: []string{"SN-0003", "SN-0004"}}},
	}, "op-serial-bad")
	require.Error(t, err)

	// 逐件任务（qty=1/件）：领取 + 确认 → 库位定位事件。
	tasks, _, err := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	for _, tk := range tasks {
		require.Equal(t, "1.0000", tk.Qty.String())
		require.NotEmpty(t, tk.SerialNo)
		_, err := e.svc.ClaimPutawayTask(ctx, actor, tk.ID.Int64())
		require.NoError(t, err)
		_, err = e.svc.ExecutePutawayTask(ctx, actor, tk.ID.Int64(), PutawayExecuteInput{})
		require.NoError(t, err)
	}
	require.Len(t, e.stock.serials, 4, "收货 2 件 + 上架定位 2 件")
	require.Equal(t, 101, int(e.stock.serials[2].BinID), "上架事件更新序列号库位")
}

// ---- 9. 审计与业务同事务（architecture §4/§8.1、§13.2）----

func TestAuditEntriesWrittenWithBusiness(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	po, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "6")})
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "6"), RequireInspect: boolPtr(false)}},
	}, "op-audit")
	require.NoError(t, err)
	tasks, _, _ := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	_, err = e.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
	require.NoError(t, err)
	_, err = e.svc.ExecutePutawayTask(ctx, actor, tasks[0].ID.Int64(), PutawayExecuteInput{})
	require.NoError(t, err)

	for _, action := range []string{"create", "submit", "APPROVE", "receive", "claim", "putaway", "status"} {
		require.Positive(t, e.spy.countBy("purchase", action), "操作日志必须包含 %s 动作（module=purchase）", action)
	}
	_ = po
}

// ---- 10. 入库单差额关闭联动取消任务（plan §6.2/§6.3）----

func TestInboundCloseCancelsPendingTasks(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	_, in := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "10")})
	_, err := e.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
		InboundNo: in.InboundNo,
		Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "6")}},
	}, "op-close-1")
	require.NoError(t, err)
	// 进行中任务阻断关闭。
	tasks, _, _ := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Status: TaskStatusPending, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NotEmpty(t, tasks)
	_, err = e.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
	require.NoError(t, err)
	_, err = e.svc.CloseInbound(ctx, actor, in.ID.Int64(), InboundCloseInput{Reason: "余量不再到货"})
	require.Error(t, err, "存在上架中任务时禁止差额关闭")
	_, err = e.svc.ExecutePutawayTask(ctx, actor, tasks[0].ID.Int64(), PutawayExecuteInput{})
	require.NoError(t, err)
	closed, err := e.svc.CloseInbound(ctx, actor, in.ID.Int64(), InboundCloseInput{Reason: "余量不再到货"})
	require.NoError(t, err)
	require.Equal(t, InboundStatusClosed, closed.Status)
	remaining, _, err := e.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Status: TaskStatusPending, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
	require.NoError(t, err)
	require.Empty(t, remaining, "关闭联动取消待领取任务")
}

// ---- 11. 缺失必填（api.md §4 校验样例）----

func TestApproveRejectRequiresOpinion(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	po, _ := e.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "1")})
	_, err := e.svc.SubmitPO(ctx, actor, po.ID.Int64())
	require.Error(t, err, "已审核订单不能重复提交")
	// 新建一单走到待审核，再驳回（无意见）必须被拒。
	po2, err := e.svc.CreatePO(ctx, actor, POCreateInput{
		SupplierID: 11, WarehouseID: 1,
		Items: []POItemInput{{SKUID: 100, Qty: qty(t, "2")}},
	})
	require.NoError(t, err)
	_, err = e.svc.SubmitPO(ctx, actor, po2.ID.Int64())
	require.NoError(t, err)
	_, err = e.svc.ApprovePO(ctx, actor, po2.ID.Int64(), POApproveInput{Approved: false})
	require.Error(t, err)
	require.Equal(t, response.CodeInvalidParam.Code, codeOf(t, err), "驳回必须填写审批意见")
	_ = po
}

func TestMulAmount(t *testing.T) {
	a, ok := mulAmount(qty(t, "3.5"), qty(t, "2.0000"))
	require.True(t, ok)
	require.Equal(t, "7.0000", a.String())
	_, ok = mulAmount(qty(t, "99999999999999.9999"), qty(t, "99999999999999.9999"))
	require.False(t, ok, "溢出必须拒绝")
}
