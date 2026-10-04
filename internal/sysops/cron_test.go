package sysops

// cron 基座单测——不依赖 PostgreSQL/网络（backend-m3-plan §14：注册表契约、防重入路径、
// 执行日志回填以内存替身覆盖；advisory lock 与 robfig 真实调度行为属集成测试面）。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// ---- 测试替身 ----

type fakeRun struct {
	id       int64
	code     string
	trigger  RunTrigger
	success  bool
	message  string
	finished bool
}

type fakeJobState struct {
	status    RunStatus
	duration  time.Duration
	nextRunAt time.Time
}

type fakeStore struct {
	mu        sync.Mutex
	upsertErr error
	enabled   map[string]bool
	runs      []fakeRun
	states    map[string]fakeJobState
	nextID    int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{enabled: map[string]bool{}, states: map[string]fakeJobState{}}
}

func (f *fakeStore) UpsertRegistry(_ context.Context, jobs []JobDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	for _, j := range jobs {
		if _, ok := f.enabled[j.Code]; !ok {
			f.enabled[j.Code] = true // 缺省启用（缺省值源语义）
		}
	}
	return nil
}

func (f *fakeStore) EnabledCodes(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var codes []string
	for c, on := range f.enabled {
		if on {
			codes = append(codes, c)
		}
	}
	return codes, nil
}

func (f *fakeStore) BeginRun(_ context.Context, code string, trigger RunTrigger, _ time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := f.nextID
	f.runs = append(f.runs, fakeRun{id: id, code: code, trigger: trigger})
	return id, nil
}

func (f *fakeStore) FinishRun(_ context.Context, runID int64, _ time.Time, success bool, _ time.Duration, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.runs {
		if f.runs[i].id == runID {
			f.runs[i].finished = true
			f.runs[i].success = success
			f.runs[i].message = message
		}
	}
	return nil
}

func (f *fakeStore) RecordSkipped(_ context.Context, code string, trigger RunTrigger, _ time.Time, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.runs = append(f.runs, fakeRun{id: f.nextID, code: code, trigger: trigger, message: message, finished: true})
	return nil
}

func (f *fakeStore) UpdateJobState(_ context.Context, code string, status RunStatus, _ time.Time, duration time.Duration, nextRunAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[code] = fakeJobState{status: status, duration: duration, nextRunAt: nextRunAt}
	return nil
}

func (f *fakeStore) SetJobEnabled(_ context.Context, code string, enabled bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.enabled[code]; !ok {
		return 0, nil
	}
	f.enabled[code] = enabled
	return 1, nil
}

func (f *fakeStore) runsFor(code string) []fakeRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeRun
	for _, r := range f.runs {
		if r.code == code {
			out = append(out, r)
		}
	}
	return out
}

type fakeLocker struct {
	mu     sync.Mutex
	busy   map[string]bool  // key → 被持有（TryLock 返回 ok=false）
	errFor map[string]error // key → 强制锁检查失败
}

func newFakeLocker() *fakeLocker {
	return &fakeLocker{busy: map[string]bool{}, errFor: map[string]error{}}
}

func (l *fakeLocker) TryLock(_ context.Context, key string) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.errFor[key]; err != nil {
		return nil, false, err
	}
	if l.busy[key] {
		return nil, false, nil
	}
	l.busy[key] = true
	return func() { l.mu.Lock(); delete(l.busy, key); l.mu.Unlock() }, true, nil
}

// newTestScheduler 装配被测调度器（不触达 PostgreSQL）。
func newTestScheduler(store *fakeStore, locker *fakeLocker) *Scheduler {
	return newScheduler(store, locker, nil)
}

// ---- 注册表契约 ----

func TestFrozenJobRegistry(t *testing.T) {
	// M3 冻结五任务（plan §4.3）；log_cleanup/backup_daily 有意不进应用内注册表（plan §4.3/§15）。
	want := map[string]string{
		"inventory_low_stock_scan": "*/10 * * * *",
		"inventory_expiry_scan":    "0 6 * * *",
		"inventory_stagnant_scan":  "0 6 * * *",
		"task_timeout_scan":        "0 * * * *",
		"file_cleanup":             "30 3 * * *",
	}
	if len(frozenJobs) != len(want) {
		t.Fatalf("冻结注册表应含 %d 任务（plan §4.3），实际 %d", len(want), len(frozenJobs))
	}
	for code, expr := range want {
		def, ok := jobByCode(code)
		if !ok {
			t.Fatalf("注册表缺少任务 %s", code)
		}
		if def.CronExpr != expr {
			t.Fatalf("任务 %s cron 表达式应为 %q，实际 %q", code, expr, def.CronExpr)
		}
		if def.Name == "" {
			t.Fatalf("任务 %s 缺少名称", code)
		}
		if _, err := cron.ParseStandard(def.CronExpr); err != nil {
			t.Fatalf("任务 %s cron 表达式非法: %v", code, err)
		}
	}
	for _, excluded := range []string{"log_cleanup", "backup_daily"} {
		if _, ok := jobByCode(excluded); ok {
			t.Fatalf("任务 %s 不应进应用内注册表（plan §4.3/§15：审计清理属部署侧脚本、备份属登记+外部执行）", excluded)
		}
	}
}

func TestRegisterJobHandlerGuards(t *testing.T) {
	wantPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("%s 应触发 panic（编程错误快速失败）", name)
			}
		}()
		fn()
	}
	wantPanic("清单外任务编码注册", func() {
		RegisterJobHandler("backup_daily", func(ctx context.Context) error { return nil })
	})
	wantPanic("nil 执行体注册", func() {
		registerJobHandler("file_cleanup", nil, true)
	})
	registerJobHandler("file_cleanup", func(ctx context.Context) error { return nil }, true)
	wantPanic("重复注册", func() {
		RegisterJobHandler("file_cleanup", func(ctx context.Context) error { return nil })
	})
	if fn, ok := jobHandlerFor("file_cleanup"); !ok || fn == nil {
		t.Fatal("注册后应可读取执行体")
	}
}

// ---- 执行与执行日志 ----

func TestSchedulerRunSuccess(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	s := newTestScheduler(store, locker)
	ran := false
	def, _ := jobByCode("inventory_low_stock_scan")
	s.execute(context.Background(), def, TriggerScheduled, func(ctx context.Context) error {
		ran = true
		return nil
	})
	if !ran {
		t.Fatal("获锁成功应执行 handler")
	}
	runs := store.runsFor(def.Code)
	if len(runs) != 1 {
		t.Fatalf("应恰落一条执行日志，实际 %d", len(runs))
	}
	r := runs[0]
	if r.trigger != TriggerScheduled || !r.finished || !r.success {
		t.Fatalf("执行日志应为 SCHEDULED/已完成/成功: %+v", r)
	}
	st := store.states[def.Code]
	if st.status != RunStatusSuccess {
		t.Fatalf("last_run_status 应为 success，实际 %s", st.status)
	}
}

func TestSchedulerRunFailureAndPanic(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	s := newTestScheduler(store, locker)
	def, _ := jobByCode("task_timeout_scan")

	s.execute(context.Background(), def, TriggerManual, func(ctx context.Context) error {
		return errors.New("reader 未装配")
	})
	st := store.states[def.Code]
	if st.status != RunStatusFailed {
		t.Fatalf("失败应回填 last_run_status=failed，实际 %s", st.status)
	}
	runs := store.runsFor(def.Code)
	if len(runs) != 1 || runs[0].success || !strings.Contains(runs[0].message, "reader 未装配") {
		t.Fatalf("失败执行日志应携带失败原因: %+v", runs)
	}

	// panic 视为执行失败（不击穿调度器 goroutine）。
	s.execute(context.Background(), def, TriggerManual, func(ctx context.Context) error {
		panic("boom")
	})
	st = store.states[def.Code]
	if st.status != RunStatusFailed {
		t.Fatalf("panic 应记 last_run_status=failed，实际 %s", st.status)
	}
	if got := store.runsFor(def.Code); len(got) != 2 {
		t.Fatalf("两次执行应恰落两条日志，实际 %d", len(got))
	}
}

// ---- 防重入（plan §4.3：进程内 single-flight + advisory lock，SKIPPED 落执行日志） ----

func TestSchedulerSkipWhenLockBusy(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	locker.busy[lockKey("inventory_expiry_scan")] = true
	s := newTestScheduler(store, locker)
	def, _ := jobByCode("inventory_expiry_scan")

	executed := false
	s.execute(context.Background(), def, TriggerScheduled, func(ctx context.Context) error {
		executed = true
		return nil
	})
	if executed {
		t.Fatal("advisory lock 未获取不得执行 handler")
	}
	runs := store.runsFor(def.Code)
	if len(runs) != 1 || runs[0].trigger != TriggerSkipped {
		t.Fatalf("应落一条 SKIPPED 执行日志: %+v", runs)
	}
	if _, ok := store.states[def.Code]; ok {
		t.Fatal("跳过不应回填 last_run_* 汇总（不覆盖最近真实执行状态）")
	}
}

func TestSchedulerFailClosedOnLockError(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	locker.errFor[lockKey("file_cleanup")] = errors.New("pg 不可达")
	s := newTestScheduler(store, locker)
	def, _ := jobByCode("file_cleanup")

	executed := false
	s.execute(context.Background(), def, TriggerScheduled, func(ctx context.Context) error {
		executed = true
		return nil
	})
	if executed {
		t.Fatal("锁检查失败必须 fail-closed 跳过（无锁执行破坏多副本至多一实例语义）")
	}
	runs := store.runsFor(def.Code)
	if len(runs) != 1 || runs[0].trigger != TriggerSkipped ||
		!strings.Contains(runs[0].message, "fail-closed") {
		t.Fatalf("锁检查失败应落 SKIPPED 日志并注明 fail-closed: %+v", runs)
	}
}

func TestSchedulerInProcessSingleFlight(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	s := newTestScheduler(store, locker)
	def, _ := jobByCode("inventory_low_stock_scan")

	// 预占 single-flight 标记（模拟上一轮仍在执行）。
	s.flightFor(def.Code).Store(true)
	executed := false
	s.execute(context.Background(), def, TriggerScheduled, func(ctx context.Context) error {
		executed = true
		return nil
	})
	if executed {
		t.Fatal("上一轮未结束本轮不得执行（进程内防重入）")
	}
	runs := store.runsFor(def.Code)
	if len(runs) != 1 || runs[0].trigger != TriggerSkipped {
		t.Fatalf("进程内重叠应落 SKIPPED 执行日志: %+v", runs)
	}
}

// ---- 入口与生命周期 ----

func TestSchedulerRunNowGuards(t *testing.T) {
	s := newTestScheduler(newFakeStore(), newFakeLocker())
	if err := s.RunNow(context.Background(), "backup_daily"); err == nil {
		t.Fatal("清单外任务编码 RunNow 应报错")
	}
	if err := s.RunNow(context.Background(), "task_timeout_scan"); err == nil {
		t.Fatal("handler 未注册 RunNow 应报错（空实现位不静默空跑）")
	}
	registerJobHandler("task_timeout_scan", func(ctx context.Context) error { return nil }, true)
	if err := s.RunNow(context.Background(), "task_timeout_scan"); err != nil {
		t.Fatalf("已注册任务 RunNow 应成功，实际 %v", err)
	}
	runs := s.store.(*fakeStore).runsFor("task_timeout_scan")
	if len(runs) != 1 || runs[0].trigger != TriggerManual {
		t.Fatalf("手动触发应落 MANUAL 执行日志: %+v", runs)
	}
}

func TestSchedulerStartStopLifecycle(t *testing.T) {
	store, locker := newFakeStore(), newFakeLocker()
	s := newTestScheduler(store, locker)
	registerJobHandler("inventory_low_stock_scan", func(ctx context.Context) error { return nil }, true)
	store.enabled["inventory_expiry_scan"] = false // 管理端停用覆盖
	// 包级 handler 注册表可能残留其他用例的注册——以启停覆盖关闭它们，构造确定性场景：
	// 仅 inventory_low_stock_scan 满足"已注册 + enabled"。
	for _, j := range frozenJobs {
		if j.Code == "inventory_low_stock_scan" {
			continue
		}
		if _, registered := jobHandlerFor(j.Code); registered {
			store.enabled[j.Code] = false
		}
	}

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start 不应失败: %v", err)
	}
	s.mu.Lock()
	scheduled := len(s.entries)
	s.mu.Unlock()
	if scheduled != 1 {
		t.Fatalf("应恰调度 1 个任务（1 个已注册 handler 且 enabled；停用/未注册不调度），实际 %d", scheduled)
	}

	// 启停热更新：停用已调度任务 → cron 条目移除；启用未注册任务 → 不调度不报错。
	if err := s.SetEnabled(ctx, "inventory_low_stock_scan", false); err != nil {
		t.Fatalf("停用热更新失败: %v", err)
	}
	s.mu.Lock()
	scheduled = len(s.entries)
	s.mu.Unlock()
	if scheduled != 0 {
		t.Fatalf("停用后应无调度条目，实际 %d", scheduled)
	}
	if store.enabled["inventory_low_stock_scan"] {
		t.Fatal("启停状态应持久化到 DB 行覆盖")
	}
	if err := s.SetEnabled(ctx, "inventory_expiry_scan", true); err != nil {
		t.Fatalf("启用未注册任务不应报错（不调度）: %v", err)
	}
	if err := s.SetEnabled(ctx, "backup_daily", true); err == nil {
		t.Fatal("清单外任务编码启停应报错")
	}

	s.Stop()
	s.Stop() // 幂等
}

func TestSchedulerStartFailsWhenRegistryUnreachable(t *testing.T) {
	store, _ := newFakeStore(), newFakeLocker()
	store.upsertErr = errors.New("pg 不可达")
	s := newTestScheduler(store, newFakeLocker())
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("注册表同步失败应上抛（main fail-fast，禁止带病启动）")
	}
}

func TestLockKey(t *testing.T) {
	// plan §4.3：hashtext('sfjob:'||code)。
	if got := lockKey("file_cleanup"); got != "sfjob:file_cleanup" {
		t.Fatalf("锁键应为 sfjob:{code}，实际 %s", got)
	}
}
