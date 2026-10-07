package sales

// 批量领取 + 任务优先级服务层测试（2026-10-06 效率层一期 B3，计划 §7.1 T8/T9/T10）：
//
//	T8  批量领取结果对账——mixed success/failed/skipped 逐条对账 + 契约计数
//	T9  并发双领取恰一 success 一 failed（fake 原子抢占，TestConcurrentClaimExactlyOneWinner 同型）
//	T10 priority 守卫——完成态 409 / 值域外 400 / 审计写入
//
// 装配复用 service_test.go harness（替身 + Service），不另起炉灶。

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stockflow/server/internal/response"
)

// genPick 生成拣货任务夹具（走真实链路：下单→审核→生成任务；TestConcurrentClaimExactlyOneWinner 同型）。
func genPick(t *testing.T, h *harness, qty Qty) (outboundNo string, pickID int64) {
	t.Helper()
	_, res := h.submitAndApprove(t, skuPlain, qty)
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, Actor{ID: 7}, res.OutboundNo)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	return res.OutboundNo, picks[0].ID.Int64()
}

// seedCheck 复核任务夹具（InsertCheckTasks 直插——fake 自动派 ID）。
func seedCheck(t *testing.T, h *harness, status string) int64 {
	t.Helper()
	tasks := []CheckTask{{CheckNo: "CH-T", OutboundNo: "OB-T", SKUID: skuPlain, Qty: qtyOf(1), Status: status}}
	if err := h.repo.InsertCheckTasks(nil, tasks); err != nil {
		t.Fatalf("插入复核任务失败: %v", err)
	}
	return tasks[0].ID.Int64()
}

// ---- T8 批量领取结果对账 ----

func TestBatchClaimPicksReconcile(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(20), 1, 11)
	_, idA := genPick(t, h, qtyOf(3))
	_, idB := genPick(t, h, qtyOf(4))

	// mixed：两条 PENDING→success；不存在→failed(reason=SALES_TASK_NOT_FOUND)。
	res, err := h.svc.BatchClaimPicks(h.ctx, Actor{ID: 7, Name: "picker"}, BatchClaimInput{IDs: []int64{idA, idB, 99999}})
	if err != nil {
		t.Fatalf("批量领取失败: %v", err)
	}
	if res.Total != 3 || res.SuccessCount != 2 || res.FailedCount != 1 || res.SkippedCount != 0 {
		t.Fatalf("计数对账不符: %+v", res)
	}
	// results[].id 为 JSON 字符串形态（api.md §2 冻结口径，service_batch.go strconv.FormatInt）。
	if len(res.Results) != 3 || res.Results[0].ID != strconv.FormatInt(idA, 10) || res.Results[0].Status != batchStatusSuccess ||
		res.Results[2].ID != "99999" || res.Results[2].Status != batchStatusFailed ||
		res.Results[2].Reason != "SALES_TASK_NOT_FOUND" {
		t.Fatalf("逐条结果不符: %+v", res.Results)
	}
	// 幂等命中：已被本人领取→skipped（不触发重复抢占）。
	res, err = h.svc.BatchClaimPicks(h.ctx, Actor{ID: 7, Name: "picker"}, BatchClaimInput{IDs: []int64{idA}})
	if err != nil {
		t.Fatalf("重复批量领取失败: %v", err)
	}
	if res.SkippedCount != 1 || res.Results[0].Status != batchStatusSkipped {
		t.Fatalf("本人重复领取应 skipped: %+v", res)
	}
	// 被他人领取→failed(reason=既有冲突码 SALES_CLAIM_CONFLICT)。
	res, err = h.svc.BatchClaimPicks(h.ctx, Actor{ID: 8, Name: "other"}, BatchClaimInput{IDs: []int64{idA}})
	if err != nil {
		t.Fatalf("他人批量领取失败: %v", err)
	}
	if res.FailedCount != 1 || res.Results[0].Reason != "SALES_CLAIM_CONFLICT" {
		t.Fatalf("被他人领取应 failed=既有冲突码: %+v", res)
	}
}

func TestBatchClaimChecksReconcile(t *testing.T) {
	h := newHarness(t)
	idA := seedCheck(t, h, CheckStatusPending)
	idB := seedCheck(t, h, CheckStatusPending)

	// mixed：两条 PENDING→success（领取=原子指派不迁移状态）；不存在→failed。
	res, err := h.svc.BatchClaimChecks(h.ctx, Actor{ID: 6, Name: "checker"}, BatchClaimInput{IDs: []int64{idA, idB, 99999}})
	if err != nil {
		t.Fatalf("批量领取复核失败: %v", err)
	}
	if res.Total != 3 || res.SuccessCount != 2 || res.FailedCount != 1 || res.SkippedCount != 0 {
		t.Fatalf("计数对账不符: %+v", res)
	}
	// 幂等命中：PENDING 且 assignee=本人→skipped（复核值域无 CLAIMED 态）。
	res, err = h.svc.BatchClaimChecks(h.ctx, Actor{ID: 6, Name: "checker"}, BatchClaimInput{IDs: []int64{idA}})
	if err != nil {
		t.Fatalf("重复批量领取复核失败: %v", err)
	}
	if res.SkippedCount != 1 || res.Results[0].Status != batchStatusSkipped {
		t.Fatalf("本人重复领取应 skipped: %+v", res)
	}
	// 非法状态（DONE）→failed。
	idC := seedCheck(t, h, CheckStatusDone)
	res, err = h.svc.BatchClaimChecks(h.ctx, Actor{ID: 8, Name: "other"}, BatchClaimInput{IDs: []int64{idC}})
	if err != nil {
		t.Fatalf("终态批量领取复核失败: %v", err)
	}
	if res.FailedCount != 1 {
		t.Fatalf("终态任务应 failed: %+v", res)
	}
}

func TestBatchClaimInputValidation(t *testing.T) {
	h := newHarness(t)
	// ids 空 → 400 invalidParam。
	_, err := h.svc.BatchClaimPicks(h.ctx, Actor{ID: 7}, BatchClaimInput{})
	var be *response.Error
	if err == nil || !asErr(err, &be) || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("空 ids 应 400 invalidParam: %v", err)
	}
	// 负数 ID → 400。
	_, err = h.svc.BatchClaimPicks(h.ctx, Actor{ID: 7}, BatchClaimInput{IDs: []int64{-1}})
	if err == nil || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("负数 ID 应 400: %v", err)
	}
}

// ---- T9 并发双领取恰一 success 一 failed ----

func TestBatchClaimConcurrentExactlyOneSuccess(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	_, idA := genPick(t, h, qtyOf(4))

	const racers = 8
	var wg sync.WaitGroup
	successes := make(chan int, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			res, err := h.svc.BatchClaimPicks(h.ctx, Actor{ID: int64(200 + id), Name: "p"}, BatchClaimInput{IDs: []int64{idA}})
			if err == nil && res.SuccessCount == 1 {
				successes <- id
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(successes)
	n := 0
	for range successes {
		n++
	}
	if n != 1 {
		t.Fatalf("并发批量领取应恰一批 success，got %d", n)
	}
}

// ---- T10 priority 守卫 ----

func TestSetPickTaskPriorityGuards(t *testing.T) {
	h := newHarness(t)
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(10), 1, 11)
	_, idA := genPick(t, h, qtyOf(4))

	// 正常设置：PENDING 任务 priority=3。
	p := 3
	if err := h.svc.SetPickTaskPriority(h.ctx, Actor{ID: 7}, idA, TaskPriorityInput{Priority: &p}); err != nil {
		t.Fatalf("设置优先级失败: %v", err)
	}
	if got := h.repo.pickPriorities[idA]; got != 3 {
		t.Fatalf("优先级列应为 3，实际 %d", got)
	}
	// 值域外 → 400 invalidParam。
	p = 10
	err := h.svc.SetPickTaskPriority(h.ctx, Actor{ID: 7}, idA, TaskPriorityInput{Priority: &p})
	if err == nil || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority=10 应 400: %v", err)
	}
	p = -1
	if err := h.svc.SetPickTaskPriority(h.ctx, Actor{ID: 7}, idA, TaskPriorityInput{Priority: &p}); err == nil || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority=-1 应 400: %v", err)
	}
	// 缺省（nil）→ 400。
	if err := h.svc.SetPickTaskPriority(h.ctx, Actor{ID: 7}, idA, TaskPriorityInput{}); err == nil || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority 缺省应 400: %v", err)
	}
	// 完成态（PICKED）→ 409 状态守卫。
	row := h.repo.picks[idA]
	row.Status = PickStatusPicked
	p = 5
	err = h.svc.SetPickTaskPriority(h.ctx, Actor{ID: 7}, idA, TaskPriorityInput{Priority: &p})
	if err == nil || codeOfErr(err) != "SALES_STATE_CONFLICT" {
		t.Fatalf("完成态应 409 SALES_STATE_CONFLICT: %v", err)
	}
	// 审计写入（action=priority）。
	if h.audit.count("priority") == 0 {
		t.Fatalf("优先级调整应写审计: %d", h.audit.count("priority"))
	}
}

func TestSetCheckTaskPriorityGuards(t *testing.T) {
	h := newHarness(t)
	idA := seedCheck(t, h, CheckStatusPending)

	p := 7
	if err := h.svc.SetCheckTaskPriority(h.ctx, Actor{ID: 6}, idA, TaskPriorityInput{Priority: &p}); err != nil {
		t.Fatalf("设置复核优先级失败: %v", err)
	}
	if got := h.repo.checkPriorities[idA]; got != 7 {
		t.Fatalf("复核优先级列应为 7，实际 %d", got)
	}
	// 值域外 → 400。
	p = 99
	if err := h.svc.SetCheckTaskPriority(h.ctx, Actor{ID: 6}, idA, TaskPriorityInput{Priority: &p}); err == nil || codeOfErr(err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority=99 应 400: %v", err)
	}
	// 终态（DONE/EXCEPTION）→ 409。
	row := h.repo.checks[idA]
	row.Status = CheckStatusException
	p = 1
	err := h.svc.SetCheckTaskPriority(h.ctx, Actor{ID: 6}, idA, TaskPriorityInput{Priority: &p})
	if err == nil || codeOfErr(err) != "SALES_STATE_CONFLICT" {
		t.Fatalf("异常终态应 409: %v", err)
	}
	// 不存在 → 404。
	missing := int64(88888)
	p = 1
	err = h.svc.SetCheckTaskPriority(h.ctx, Actor{ID: 6}, missing, TaskPriorityInput{Priority: &p})
	if err == nil || codeOfErr(err) != "SALES_TASK_NOT_FOUND" {
		t.Fatalf("任务不存在应 404: %v", err)
	}
}

// ---- 小助手（本文件专用；purchase service_test codeOf 同思路——code 字段未导出，
// 经 Error() 文本前缀 "CODE: message" 提取）----

func asErr(err error, target **response.Error) bool {
	return errors.As(err, target)
}

func codeOfErr(err error) string {
	var be *response.Error
	if !asErr(err, &be) {
		return ""
	}
	return strings.SplitN(be.Error(), ":", 2)[0]
}
