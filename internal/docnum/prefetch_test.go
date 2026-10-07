package docnum

// 号段预取分配器单测——不依赖 PostgreSQL/Redis/网络（AGENTS 硬性规则，与 docnum_test.go
// 同口径）。生产补段实现 refillFromDB（根连接池短事务）属数据库路径，由 testcontainers
// 集成测试覆盖（本环境无 PostgreSQL，见 docnum_test.go 头注）；本文件以注入式 refillFn
// 验证分配器并发语义。
//
// 并发测试口径：本机缺 gcc 无法启用 -race（任务约束，已注明），以普通并发测试覆盖——
// 分配器内部 preallocator.mu 保证互斥，注入 refillFn 以 mutex 模拟 DB 原子预留，
// 断言"多 goroutine 取号不重、不漏、跨块续接"。生产环境 race 检测由 CI（有 gcc/PG）
// 的 go test -race 兜底。

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// preallocReset 重置包级分配器状态（cache/refillFn），t.Cleanup 恢复——防用例间串扰
// （同包测试串行执行，Go 默认无 t.Parallel，与 inventory fakedb 系列同一约定）。
func preallocReset(t *testing.T) {
	t.Helper()
	oldCache, oldRefill := prealloc.cache, prealloc.refillFn
	t.Cleanup(func() {
		prealloc.mu.Lock()
		defer prealloc.mu.Unlock()
		prealloc.cache = oldCache
		prealloc.refillFn = oldRefill
	})
	prealloc.mu.Lock()
	defer prealloc.mu.Unlock()
	prealloc.cache = map[string]*numberSegment{}
	prealloc.refillFn = nil
}

// TestPreallocateRegistry LED 恒定单行计数（ResetAll→period='ALL'）故唯一允许预取；
// 其余规则语义不变（同事务逐号发放）。
func TestPreallocateRegistry(t *testing.T) {
	rule, ok := RuleFor("LED")
	if !ok || !rule.Preallocate {
		t.Fatal("LED 规则应标记号段预取（Preallocate=true）")
	}
	if rule.Reset != ResetAll {
		t.Fatalf("LED 预取要求恒定单行计数（ResetAll），实际 %s", rule.Reset)
	}
	for p, r := range frozenRules {
		if p == "LED" {
			continue
		}
		if r.Preallocate {
			t.Fatalf("前缀 %s 不应标记预取（仅 LED 允许，避免改变其余取号规则语义）", p)
		}
	}
}

// TestTakeCrossBlockContinuation 连续取号跨多个号段：严格递增、块间无缺口无重叠。
func TestTakeCrossBlockContinuation(t *testing.T) {
	preallocReset(t)
	var counter int64
	prealloc.refillFn = func(ctx context.Context, prefix, period string, count int64) (int64, error) {
		if prefix != "LED" || period != "ALL" {
			t.Errorf("补段参数应 LED/ALL，实际 %s/%s", prefix, period)
		}
		if count != preallocBatch {
			t.Errorf("补段数量应为 %d，实际 %d", preallocBatch, count)
		}
		counter += count // 模拟 DB 原子 UPDATE next_no = next_no + N RETURNING
		return counter, nil
	}
	// 250 号跨 3 个号段（100+100+50）：1..250 无缺口无重复
	for want := int64(1); want <= 250; want++ {
		got, err := prealloc.take("LED", "ALL", nil)
		if err != nil {
			t.Fatalf("take #%d: %v", want, err)
		}
		if got != want {
			t.Fatalf("跨块续接断裂：第 %d 号应 %d，实际 %d", want, want, got)
		}
	}
}

// TestTakeConcurrentNoDupNoGap 多 goroutine 并发取号：不重、不漏、补段次数精确。
func TestTakeConcurrentNoDupNoGap(t *testing.T) {
	preallocReset(t)
	var (
		mu      sync.Mutex
		counter int64
		calls   int32
	)
	prealloc.refillFn = func(ctx context.Context, prefix, period string, count int64) (int64, error) {
		atomic.AddInt32(&calls, 1)
		mu.Lock() // 模拟 DB 原子预留：互斥递增（同 UPDATE ... RETURNING 串行化语义）
		defer mu.Unlock()
		counter += count
		return counter, nil
	}
	const (
		workers   = 16
		perWorker = 250
	)
	results := make([]int64, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				seq, err := prealloc.take("LED", "ALL", nil)
				if err != nil {
					t.Errorf("worker %d take: %v", w, err)
					return
				}
				results[w*perWorker+j] = seq
			}
		}(w)
	}
	wg.Wait()
	seen := make(map[int64]bool, len(results))
	for _, seq := range results {
		if seen[seq] {
			t.Fatalf("并发取号重复：%d（不重被破坏）", seq)
		}
		seen[seq] = true
	}
	for i := int64(1); i <= int64(len(results)); i++ {
		if !seen[i] {
			t.Fatalf("并发取号缺号：%d（不漏被破坏）", i)
		}
	}
	// 补段次数 = 4000/100 = 40（整除：最后一块恰好发完，无浪费性多补）
	if got := atomic.LoadInt32(&calls); got != 40 {
		t.Fatalf("补段次数应 40，实际 %d", got)
	}
}

// TestTakeRefillError 补段失败：错误上抛且不污染缓存（号段要么整体预留成功要么整体作废）。
func TestTakeRefillError(t *testing.T) {
	preallocReset(t)
	boom := errors.New("boom")
	prealloc.refillFn = func(ctx context.Context, prefix, period string, count int64) (int64, error) {
		return 0, boom
	}
	if _, err := prealloc.take("LED", "ALL", nil); !errors.Is(err, boom) {
		t.Fatalf("补段错误应上抛，实际 %v", err)
	}
	var called int32
	prealloc.refillFn = func(ctx context.Context, prefix, period string, count int64) (int64, error) {
		atomic.AddInt32(&called, 1)
		return preallocBatch, nil
	}
	seq, err := prealloc.take("LED", "ALL", nil)
	if err != nil || seq != 1 {
		t.Fatalf("失败不应污染缓存：seq=%d err=%v", seq, err)
	}
	if got := atomic.LoadInt32(&called); got != 1 {
		t.Fatalf("失败后重取应重新补段一次，实际 %d", got)
	}
}

// TestTakeUnavailableFallbackSignal 无根连接池且无注入实现（如单测假事务/非标准池）：
// 返回回退信号，由 nextPreallocated 回退 nextInTx 同事务发放。
func TestTakeUnavailableFallbackSignal(t *testing.T) {
	preallocReset(t)
	if _, err := prealloc.take("LED", "ALL", nil); !errors.Is(err, errPreallocUnavailable) {
		t.Fatalf("应返回回退信号 errPreallocUnavailable，实际 %v", err)
	}
}

// TestNextPreallocatedDispatch Next 分派：Preallocate 规则走进程内号段（成功路径
// 不触达事务句柄），格式与既有 LED 承接口径一致（ResetAll 仍带日期段）。
func TestNextPreallocatedDispatch(t *testing.T) {
	preallocReset(t)
	var counter int64
	prealloc.refillFn = func(ctx context.Context, prefix, period string, count int64) (int64, error) {
		counter += count
		return counter, nil
	}
	// tx 非 nil 即可通过前置校验（预取成功路径不使用 tx；nil tx 由 TestNextRequiresTx 覆盖）。
	tx := &gorm.DB{}
	no, err := Next(context.Background(), tx, mustRule(t, "LED"))
	if err != nil {
		t.Fatalf("预取取号失败: %v", err)
	}
	// LED 格式：LED-{当日 YYYYMMDD}-{6位流水}（ResetAll 仍带日期段，M1 承接口径；
	// 日期取发放时刻，无法注入固定时间，故用模式断言）。
	want := regexp.MustCompile(`^LED-\d{8}-000001$`)
	if !want.MatchString(no) {
		t.Fatalf("LED 单号格式不符（LED-{YYYYMMDD}-{6位流水}）: %s", no)
	}
	no2, err := Next(context.Background(), tx, mustRule(t, "LED"))
	if err != nil {
		t.Fatalf("预取取号失败: %v", err)
	}
	if !regexp.MustCompile(`^LED-\d{8}-000002$`).MatchString(no2) {
		t.Fatalf("LED 单号格式不符: %s", no2)
	}
	if no == no2 {
		t.Fatal("同号段取号应递增，实际重复")
	}
}

// TestRecoverRootPoolGuards 根连接池恢复守卫：nil 事务、事务形态（TxCommitter）、
// 非标准池均拒绝（回退同事务发放，正确性兜底）。
func TestRecoverRootPoolGuards(t *testing.T) {
	if got := recoverRootPool(nil); got != nil {
		t.Fatal("nil 事务应不可恢复")
	}
	if got := recoverRootPool(&gorm.DB{}); got != nil {
		t.Fatal("nil Config 应不可恢复")
	}
	// TxCommitter 形态 = 调用方事务本身：必须拒绝（防止补段短事务退化回长事务行锁）。
	if got := recoverRootPool(&gorm.DB{Config: &gorm.Config{ConnPool: fakeTxCommitter{}}}); got != nil {
		t.Fatal("TxCommitter 形态（调用方事务）应拒绝恢复")
	}
	// 非标准连接池包装（非 *sql.DB）：拒绝。
	if got := recoverRootPool(&gorm.DB{Config: &gorm.Config{ConnPool: notADB{}}}); got != nil {
		t.Fatal("非 *sql.DB 连接池应拒绝恢复")
	}
}

// fakePoolBase 满足 gorm.ConnPool 接口的最小占位（守卫测试不会真正执行语句；
// gorm.ConnPool 方法签名为 ...interface{} 变参，非 driver.NamedValue）。
type fakePoolBase struct{}

func (fakePoolBase) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unused")
}

func (fakePoolBase) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("unused")
}

func (fakePoolBase) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("unused")
}

func (fakePoolBase) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

// fakeTxCommitter 模拟 *sql.Tx 的事务形态（gorm.ConnPool + gorm.TxCommitter）。
type fakeTxCommitter struct{ fakePoolBase }

func (fakeTxCommitter) Commit() error   { return nil }
func (fakeTxCommitter) Rollback() error { return nil }

// notADB 非事务也非标准 *sql.DB 的连接池形态（PrepareStmt/dbresolver 等包装代表）。
type notADB struct{ fakePoolBase }
