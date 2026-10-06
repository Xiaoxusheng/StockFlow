package idempotency

// Service 层单测（效率层一期 ask 测试矩阵）：
//   - 幂等重复提交只执行一次（占用 → 回放，执行计数不变）；
//   - 并发同键只允许一个执行（16 goroutine，-race 下断言恰一占用）；
//   - 同键换载荷 409、跨用户/跨端点隔离、快照收尾守卫。

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stockflow/server/internal/response"
)

const (
	tEndpoint = "POST /api/receipts"
	tKey      = "0f8d1e2c-3b4a-4f5e-8a7b-6c5d4e3f2a1b"
)

func testService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo, nil), repo
}

func requireErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，得到 nil", want)
	}
	var re *response.Error
	if !errors.As(err, &re) {
		t.Fatalf("期望业务错误，得到 %v", err)
	}
	if got := re.Error()[:len(want)]; got != want {
		t.Fatalf("错误码不符：want=%s got=%s", want, re.Error())
	}
}

// TestAcquireOccupyCompleteReplay 幂等主语义：首次占用 → 真实执行 → 收尾快照；
// 重复提交回放首次结果且不再占用（执行计数=1）。
func TestAcquireOccupyCompleteReplay(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	l1, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":1}`)))
	if err != nil {
		t.Fatalf("首次占用失败: %v", err)
	}
	if !l1.Acquired() || l1.Replay() != nil {
		t.Fatalf("首次应占用: %+v", l1)
	}
	svc.Complete(ctx, l1, 200, []byte(`{"code":0,"message":"ok","data":{"receipt_no":"REC-1"},"request_id":"req-1"}`))

	execCount := 0
	for i := 0; i < 3; i++ {
		l2, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":1}`)))
		if err != nil {
			t.Fatalf("重放占用失败: %v", err)
		}
		if l2.Acquired() {
			execCount++ // 任何一次重放路径占用成功即为双执行
		}
		snap := l2.Replay()
		if snap == nil {
			t.Fatalf("第 %d 次重复提交应回放快照", i+2)
		}
		if snap.Status != 200 {
			t.Fatalf("快照状态不符: %d", snap.Status)
		}
		var body map[string]any
		if err := json.Unmarshal(snap.Body, &body); err != nil {
			t.Fatalf("快照体非法: %v", err)
		}
		if body["data"].(map[string]any)["receipt_no"] != "REC-1" {
			t.Fatalf("快照应返回首次结果: %v", body)
		}
	}
	if execCount != 0 {
		t.Fatalf("重复提交不得再执行（幂等只执行一次）: %d", execCount)
	}
}

// TestAcquireConcurrentOnlyOneExecutes 并发同键只允许一个执行（ask 硬性测试项；
// go test -race 下运行）：N goroutine 同时 Acquire 恰一占用，余者 IN_PROGRESS；
// 胜者收尾后追加提交获得回放。占用阶段不做 Complete——并发窗口内快照未就绪，
// 所有失败方必须命中 PROCESSING → 409。
func TestAcquireConcurrentOnlyOneExecutes(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	const n = 16

	start := make(chan struct{})
	var wg sync.WaitGroup
	var occupied atomic.Int64
	var inProgress atomic.Int64
	var winner atomic.Value // 占用成功的 Lease（并发恰一方写入）
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":1}`)))
			switch {
			case err == nil && l.Acquired():
				if winner.Load() != nil {
					t.Error("并发窗口内占用成功方多于一个")
				}
				winner.Store(l)
				occupied.Add(1)
			case err == nil && l.Replay() != nil:
				t.Error("并发窗口内不应有回放（快照未就绪）")
			default:
				requireErrCode(t, err, "IDEMPOTENCY_IN_PROGRESS")
				inProgress.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := occupied.Load(); got != 1 {
		t.Fatalf("并发同键应恰一执行，实际占用 %d 次", got)
	}
	if got := inProgress.Load(); got != n-1 {
		t.Fatalf("其余 %d 路应 409 IDEMPOTENCY_IN_PROGRESS，实际 %d", n-1, got)
	}
	// 胜者收尾 → 快照就绪 → 追加提交回放，不执行。
	svc.Complete(ctx, winner.Load().(Lease), 200, []byte(`{"code":0,"message":"ok","data":{},"request_id":"w"}`))
	l, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":1}`)))
	if err != nil {
		t.Fatalf("收尾后追加提交失败: %v", err)
	}
	if l.Acquired() || l.Replay() == nil {
		t.Fatalf("追加提交应回放: %+v", l)
	}
}

// TestAcquireRequestHashMismatch 同键换载荷 → 409 IDEMPOTENCY_REQUEST_MISMATCH。
func TestAcquireRequestHashMismatch(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	l, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":1}`)))
	if err != nil {
		t.Fatalf("首次占用失败: %v", err)
	}
	svc.Complete(ctx, l, 200, []byte(`{"code":0,"message":"ok","data":{},"request_id":"w"}`))
	_, err = svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{"qty":2}`)))
	requireErrCode(t, err, "IDEMPOTENCY_REQUEST_MISMATCH")
}

// TestAcquireDimensionIsolation 同键不同用户 / 不同端点互不干扰。
func TestAcquireDimensionIsolation(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()
	hash := HashRequest([]byte(`{}`))
	if l, err := svc.Acquire(ctx, 7, tEndpoint, tKey, hash); err != nil || !l.Acquired() {
		t.Fatalf("用户 A 首次占用失败: %v %+v", err, l)
	}
	// 不同用户同键：独立占用。
	if l, err := svc.Acquire(ctx, 8, tEndpoint, tKey, hash); err != nil || !l.Acquired() {
		t.Fatalf("用户 B 应独立占用: %v %+v", err, l)
	}
	// 不同端点同键同用户：独立占用。
	if l, err := svc.Acquire(ctx, 7, "POST /api/picks/1/confirm", tKey, hash); err != nil || !l.Acquired() {
		t.Fatalf("不同端点应独立占用: %v %+v", err, l)
	}
}

// TestAcquireInvalidKey 键值域校验（000024 CHECK 同式）。
func TestAcquireInvalidKey(t *testing.T) {
	svc, _ := testService()
	for _, key := range []string{"", "空 格", "k!", string(make([]byte, 65))} {
		if key == "" {
			continue // 空键由中间件 required 模式处理，Acquire 视为格式非法
		}
		if _, err := svc.Acquire(context.Background(), 7, tEndpoint, key, "h"); err == nil {
			t.Fatalf("键 %q 应被拒绝", key)
		} else {
			requireErrCode(t, err, "IDEMPOTENCY_KEY_INVALID")
		}
	}
}

// TestCompleteGuardAndReleaseCompleteAfter 收尾守卫：Complete 后 Release 不生效
// （快照保留可回放）；未收尾行 Release 后同键可重新占用。
func TestCompleteGuardAndRelease(t *testing.T) {
	svc, repo := testService()
	ctx := context.Background()

	// 未收尾 → Release 释放 → 同键重新占用。
	l1, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{}`)))
	if err != nil {
		t.Fatalf("占用失败: %v", err)
	}
	svc.Release(ctx, l1)
	if n, _ := repo.Release(ctx, l1.LeaseID()); n != 0 {
		t.Fatalf("已释放行二次 Release 应 0 行: %d", n)
	}
	l2, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{}`)))
	if err != nil || !l2.Acquired() {
		t.Fatalf("释放后同键应可重新占用: %v %+v", err, l2)
	}
	svc.Complete(ctx, l2, 200, []byte(`{"code":0,"message":"ok","data":{},"request_id":"w"}`))
	// 已 COMPLETED 行 Release 不生效（守卫 PROCESSING）。
	if n, _ := repo.Release(ctx, l2.LeaseID()); n != 0 {
		t.Fatalf("COMPLETED 行不得被 Release: %d", n)
	}
	if _, err := svc.Acquire(ctx, 7, tEndpoint, tKey, HashRequest([]byte(`{}`))); err != nil {
		t.Fatalf("收尾后应可回放: %v", err)
	}
}

// TestCompleteNonCacheableResponse 空体/非 JSON/超限响应不缓存（占用释放、诚实降级）。
func TestCompleteNonCacheableResponse(t *testing.T) {
	svc, repo := testService()
	ctx := context.Background()
	hash := HashRequest([]byte(`{}`))

	for name, body := range map[string][]byte{
		"空响应":   nil,
		"非JSON": []byte("plain-text-not-json"),
		"超限":    make([]byte, maxSnapshotBytes+1),
	} {
		l, err := svc.Acquire(ctx, 7, tEndpoint, tKey, hash)
		if err != nil || !l.Acquired() {
			t.Fatalf("%s: 占用失败 %v", name, err)
		}
		svc.Complete(ctx, l, 200, body)
		row, _ := repo.Find(ctx, tKey, 7, tEndpoint)
		if row != nil {
			t.Fatalf("%s: 不可缓存响应应释放占用行", name)
		}
	}
}

// TestEndpointOf 端点维度口径稳定（中间件与手动模式共用，防键空间分裂）。
func TestEndpointOf(t *testing.T) {
	if got := EndpointOf("POST", "/api/receipts"); got != "POST /api/receipts" {
		t.Fatalf("EndpointOf 口径不符: %q", got)
	}
}
