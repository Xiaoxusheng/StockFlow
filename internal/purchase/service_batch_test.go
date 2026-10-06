package purchase

// 批量领取 + 任务优先级服务层测试（2026-10-06 效率层一期 B3，计划 §7.1 T8/T9/T10·putaway 域）：
//
//	T8  批量领取结果对账——mixed success/failed/skipped 逐条对账 + 契约计数
//	T9  并发批量领取恰一批 success（fake 原子抢占 + claimGuard 串行化，service_test
//	    TestClaimPutawayTaskConcurrentMutex 同型）
//	T10 priority 守卫——完成态 409 / 值域外 400 / 审计写入
//
// 装配复用 service_test.go testEnv（替身 + Service）。

import (
	"context"
	"sync"
	"testing"

	"github.com/stockflow/server/internal/stock"
)

// seedPutaway 上架任务夹具（seedTask 包装：PENDING/IN_PROGRESS/COMPLETED 直播状态）。
func seedPutaway(t *testing.T, e *testEnv, status string) int64 {
	t.Helper()
	task := e.repo.seedTask("IN-BATCH", 100, 0, stock.Qty(10000), "RECEIVED", status, 1, 101)
	if status == "" {
		t.Fatal("状态必填")
	}
	return task.ID.Int64()
}

// ---- T8 批量领取结果对账 ----

func TestBatchClaimPutawayReconcile(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	idA := seedPutaway(t, e, TaskStatusPending)
	idB := seedPutaway(t, e, TaskStatusPending)

	// mixed：两条 PENDING→success；不存在→failed(reason=既有错误码)。
	res, err := e.svc.BatchClaimPutawayTasks(ctx, actor, BatchClaimInput{IDs: []int64{idA, idB, 99999}})
	if err != nil {
		t.Fatalf("批量领取失败: %v", err)
	}
	if res.Total != 3 || res.SuccessCount != 2 || res.FailedCount != 1 || res.SkippedCount != 0 {
		t.Fatalf("计数对账不符: %+v", res)
	}
	if len(res.Results) != 3 || res.Results[0].ID != idA || res.Results[0].Status != batchStatusSuccess ||
		res.Results[2].ID != 99999 || res.Results[2].Status != batchStatusFailed ||
		res.Results[2].Reason != "PURCHASE_PUTAWAY_TASK_NOT_FOUND" {
		t.Fatalf("逐条结果不符: %+v", res.Results)
	}

	// 幂等命中：已被本人领取（IN_PROGRESS claimed_by=本人）→ skipped。
	res, err = e.svc.BatchClaimPutawayTasks(ctx, actor, BatchClaimInput{IDs: []int64{idA}})
	if err != nil {
		t.Fatalf("重复批量领取失败: %v", err)
	}
	if res.SkippedCount != 1 || res.Results[0].Status != batchStatusSkipped {
		t.Fatalf("本人重复领取应 skipped: %+v", res)
	}

	// 被他人领取→failed(reason=既有冲突码 PURCHASE_PUTAWAY_CLAIM_CONFLICT)。
	res, err = e.svc.BatchClaimPutawayTasks(ctx, Actor{UserID: 8, Username: "other"}, BatchClaimInput{IDs: []int64{idA}})
	if err != nil {
		t.Fatalf("他人批量领取失败: %v", err)
	}
	if res.FailedCount != 1 || res.Results[0].Reason != "PURCHASE_PUTAWAY_CLAIM_CONFLICT" {
		t.Fatalf("被他人领取应 failed=既有冲突码: %+v", res)
	}

	// 入参校验：ids 空 → 400。
	if _, err := e.svc.BatchClaimPutawayTasks(ctx, actor, BatchClaimInput{}); err == nil || codeOf(t, err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("空 ids 应 400 invalidParam: %v", err)
	}
}

// ---- T9 并发批量领取恰一批 success ----

func TestBatchClaimPutawayConcurrentExactlyOneSuccess(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	idA := seedPutaway(t, e, TaskStatusPending)

	const racers = 8
	var wg sync.WaitGroup
	successes := make(chan int, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			a := testActor()
			a.UserID = int64(300 + idx)
			res, err := e.svc.BatchClaimPutawayTasks(ctx, a, BatchClaimInput{IDs: []int64{idA}})
			if err == nil && res.SuccessCount == 1 {
				successes <- idx
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
	task, err := e.svc.GetTask(ctx, idA, WarehouseScope{All: true})
	if err != nil {
		t.Fatalf("回读任务失败: %v", err)
	}
	if task.Status != TaskStatusInProgress {
		t.Fatalf("胜者领取后任务应为 IN_PROGRESS，实际 %s", task.Status)
	}
}

// ---- T10 priority 守卫 ----

func TestSetPutawayTaskPriorityGuards(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	actor := testActor()
	idA := seedPutaway(t, e, TaskStatusPending)

	// 正常设置：PENDING 任务 priority=3。
	p := 3
	if err := e.svc.SetPutawayTaskPriority(ctx, actor, idA, TaskPriorityInput{Priority: &p}); err != nil {
		t.Fatalf("设置优先级失败: %v", err)
	}
	if got := e.repo.taskPriorities[idA]; got != 3 {
		t.Fatalf("优先级列应为 3，实际 %d", got)
	}
	// 值域外 → 400 invalidParam。
	p = 10
	if err := e.svc.SetPutawayTaskPriority(ctx, actor, idA, TaskPriorityInput{Priority: &p}); err == nil || codeOf(t, err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority=10 应 400: %v", err)
	}
	// 缺省（nil）→ 400。
	if err := e.svc.SetPutawayTaskPriority(ctx, actor, idA, TaskPriorityInput{}); err == nil || codeOf(t, err) != "COMMON_INVALID_PARAM" {
		t.Fatalf("priority 缺省应 400: %v", err)
	}
	// 完成态 → 409 状态守卫（既有 ErrPutawayStatusNotAllowed）。
	idC := seedPutaway(t, e, TaskStatusCompleted)
	p = 5
	if err := e.svc.SetPutawayTaskPriority(ctx, actor, idC, TaskPriorityInput{Priority: &p}); err == nil || codeOf(t, err) != "PURCHASE_PUTAWAY_STATUS_NOT_ALLOWED" {
		t.Fatalf("完成态应 409: %v", err)
	}
	// 不存在 → 404（既有 ErrPutawayTaskNotFound）。
	missing := int64(88888)
	p = 1
	if err := e.svc.SetPutawayTaskPriority(ctx, actor, missing, TaskPriorityInput{Priority: &p}); err == nil || codeOf(t, err) != "PURCHASE_PUTAWAY_TASK_NOT_FOUND" {
		t.Fatalf("任务不存在应 404: %v", err)
	}
	// 审计写入（module=purchase, action=priority）。
	if e.spy.countBy("purchase", "priority") == 0 {
		t.Fatalf("优先级调整应写审计: %d", e.spy.countBy("purchase", "priority"))
	}
}
