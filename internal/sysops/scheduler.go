// 定时任务调度器（robfig/cron 装配，backend-m3-plan §4.3、architecture §9.2）。
//
// 防重入双闸（plan §4.3 多副本防重 + 单进程语义）：
//  1. 进程内 single-flight：同一任务上一轮未结束，本轮记 SKIPPED 执行日志后跳过；
//  2. PG advisory lock（hashtext('sfjob:'||code)）：其他实例持有锁时记 SKIPPED 跳过；
//     锁检查失败 fail-closed 跳过（无锁执行会破坏多副本"至多一实例执行"的语义）。
//
// 失败语义（plan §4.3）：执行失败记 FAILED 执行日志 + last_run_status=failed，不做同周期
// 内重试——扫描类幂等由 dedup_key 保证，下一周期自然重试；与 asynq 重试分工明确。
// 执行日志与任务业务写入不同事务（执行日志不回滚业务结果）。
package sysops

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// stopWait 优雅停机等待在途任务完成的超时上限。
const stopWait = 30 * time.Second

// Scheduler 定时任务调度器：robfig/cron 装配 + 冻结注册表 + 执行日志。
// 由 cmd/server main 创建、Start、Stop（goroutine 生命周期归 main，go-dev-standard 规则 3）。
type Scheduler struct {
	cron   *cron.Cron
	store  jobRunStore
	locker lockAcquirer
	log    *zap.Logger
	now    func() time.Time
	// alertFn 执行失败站内告警钩子（nil=不告警；NewScheduler 注入 notifications 写入，
	// plan §4.3"失败记 FAILED + 站内告警"；单测经 newScheduler 保持 nil）。
	alertFn func(ctx context.Context, code, message string)

	mu        sync.Mutex
	flight    map[string]*atomic.Bool // 进程内 single-flight 标记
	entries   map[string]cron.EntryID // 已调度任务 entry
	schedules map[string]cron.Schedule
	started   bool
	stopped   chan struct{} // 关闭即向在途任务广播取消
}

// lockAcquirer 咨询锁获取接口（单测以内存替身注入）。
type lockAcquirer interface {
	// TryLock 尝试获取任务锁：ok=false 表示被他人持有；err 非 nil 表示锁检查失败（调用方 fail-closed）。
	TryLock(ctx context.Context, key string) (unlock func(), ok bool, err error)
}

// NewScheduler 装配调度器（生产构造：GORM 句柄 + 分类日志）。
// 装配期完成两件事：① 以 db 绑定闭包注册本包实现的任务执行体（预警扫描三任务 +
// file_cleanup，backend-m3-plan §4.3 接入点；allowReplace 幂等，重复构造安全）；
// ② 保存进程内调度器引用（管理端启停热更新读取）。
func NewScheduler(db *gorm.DB, log *zap.Logger) *Scheduler {
	wireJobHandlers(db, log)
	s := newScheduler(newGormJobStore(db), &sqlLockAcquirer{db: db}, log)
	if db != nil {
		s.alertFn = func(ctx context.Context, code, message string) {
			def, ok := jobByCode(code)
			name := code
			if ok {
				name = def.Name
			}
			// 失败告警独立超时上下文：run 的 ctx 可能已被 Stop 取消，告警尽力而为。
			alertCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := notifyJobFailure(alertCtx, db, code, name, message, s.now()); err != nil {
				s.log.Error("定时任务失败告警写入失败", zap.String("job", code), zap.Error(err))
			}
		}
	}
	attachScheduler(s)
	return s
}

// newScheduler 注入构造（单测替换 store/locker/时钟，不依赖 PostgreSQL）。
func newScheduler(store jobRunStore, locker lockAcquirer, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		cron:      cron.New(),
		store:     store,
		locker:    locker,
		log:       log,
		now:       time.Now,
		flight:    map[string]*atomic.Bool{},
		entries:   map[string]cron.EntryID{},
		schedules: map[string]cron.Schedule{},
	}
}

// Start 启动调度：同步注册表行（缺省补行、保留 enabled 覆盖）→ 按 DB enabled +
// handler 注册情况调度 → cron.Start。任一注册表操作失败返回错误（main fail-fast）。
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.store.UpsertRegistry(ctx, frozenJobs); err != nil {
		return fmt.Errorf("sysops: 定时任务注册表同步失败: %w", err)
	}
	enabled, err := s.store.EnabledCodes(ctx)
	if err != nil {
		return fmt.Errorf("sysops: 定时任务启停状态读取失败: %w", err)
	}
	enabledSet := make(map[string]bool, len(enabled))
	for _, c := range enabled {
		enabledSet[c] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, def := range frozenJobs {
		fn, ok := jobHandlerFor(def.Code)
		if !ok {
			// 空实现位：handler 未注册不调度（plan §4.3 各域后续注册；避免空跑刷执行日志）。
			s.log.Warn("定时任务 handler 未注册，本次启动不调度（空实现位）",
				zap.String("job", def.Code), zap.String("name", def.Name))
			continue
		}
		if !enabledSet[def.Code] {
			s.log.Info("定时任务已被管理端停用，本次启动不调度",
				zap.String("job", def.Code), zap.String("name", def.Name))
			continue
		}
		if err := s.scheduleLocked(def, fn); err != nil {
			return fmt.Errorf("sysops: 定时任务 %s 调度注册失败: %w", def.Code, err)
		}
	}
	s.stopped = make(chan struct{})
	s.cron.Start()
	s.started = true
	s.log.Info("定时任务调度器已启动",
		zap.Int("scheduled", len(s.entries)),
		zap.Int("registry", len(frozenJobs)))
	return nil
}

// scheduleLocked 注册单个任务的 cron 条目（须持 s.mu）。
func (s *Scheduler) scheduleLocked(def JobDef, fn JobFunc) error {
	schedule, err := cron.ParseStandard(def.CronExpr)
	if err != nil {
		// 冻结注册表表达式非法属编程错误（启动期暴露，禁止带病调度）。
		return fmt.Errorf("cron 表达式 %q 非法: %w", def.CronExpr, err)
	}
	fnCode := def.Code
	entryID := s.cron.Schedule(schedule, cron.FuncJob(func() {
		s.execute(context.Background(), def, TriggerScheduled, fn)
	}))
	s.entries[fnCode] = entryID
	s.schedules[fnCode] = schedule
	return nil
}

// SetEnabled 启停热更新（管理端 PUT /api/system/jobs/{id}/status 接入点，plan §4.3/§10.3）：
// 更新 DB 行 enabled 并对进程内 cron 执行 RemoveEntry/AddFunc（单实例语义，多副本属 M4 检查项）。
// cron 表达式 M3 不开放修改（冻结在注册表，防任意 cron 注入——plan §10.3）。
func (s *Scheduler) SetEnabled(ctx context.Context, code string, enabled bool) error {
	def, ok := jobByCode(code)
	if !ok {
		return fmt.Errorf("sysops: 任务编码 %q 不在冻结注册表", code)
	}
	// 持久化先行：进程内热更失败可重试，DB 行是启停真相源。
	if err := s.setEnabledRow(ctx, code, enabled); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil // 未启动时仅落库，Start 按 DB 行决定调度
	}
	entryID, scheduled := s.entries[code]
	if enabled && !scheduled {
		fn, registered := jobHandlerFor(code)
		if !registered {
			// handler 未注册（空实现位）：不调度，仍视为成功（状态已持久化）。
			return nil
		}
		if err := s.scheduleLocked(def, fn); err != nil {
			return err
		}
		s.log.Info("定时任务已启用并热更新调度", zap.String("job", code))
		return nil
	}
	if !enabled && scheduled {
		s.cron.Remove(entryID)
		delete(s.entries, code)
		delete(s.schedules, code)
		s.log.Info("定时任务已停用并热更新调度", zap.String("job", code))
	}
	return nil
}

// setEnabledRow 更新 scheduled_jobs.enabled（enabled 列为启停唯一覆盖项；
// 注册表行已由 Start upsert 保证存在，行缺失视为部署异常上抛）。
func (s *Scheduler) setEnabledRow(ctx context.Context, code string, enabled bool) error {
	affected, err := s.store.SetJobEnabled(ctx, code, enabled)
	if err != nil {
		return fmt.Errorf("sysops: 任务 %s 启停状态更新失败: %w", code, err)
	}
	if affected == 0 {
		return fmt.Errorf("sysops: 任务 %s 注册表行不存在（须先 Start 同步注册表）", code)
	}
	return nil
}

// Stop 优雅停机：广播取消信号给在途任务 → 停止调度 → 等待在途任务退出（上限 stopWait）。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	wasStarted := s.started
	s.started = false
	s.mu.Unlock()
	if !wasStarted {
		return
	}
	close(s.stopped) // 在途任务经 jobContext 感知取消，尽早退出
	running := s.cron.Stop()
	timer := time.NewTimer(stopWait)
	defer timer.Stop()
	select {
	case <-running.Done():
	case <-timer.C:
		s.log.Warn("定时任务优雅停机超时，存在未退出任务", zap.Duration("waited", stopWait))
	}
}

// RunNow 手动触发一次执行（trigger=MANUAL；管理端/联调/单测入口）。
// 同步执行（调用方 goroutine 内），与调度触发共用防重入与执行日志路径。
func (s *Scheduler) RunNow(ctx context.Context, code string) error {
	def, ok := jobByCode(code)
	if !ok {
		return fmt.Errorf("sysops: 任务编码 %q 不在冻结注册表", code)
	}
	fn, registered := jobHandlerFor(code)
	if !registered {
		return fmt.Errorf("sysops: 任务 %q 的 handler 未注册", code)
	}
	s.execute(ctx, def, TriggerManual, fn)
	return nil
}

// execute 单次执行全流程：进程内 single-flight → advisory lock → 执行 → 执行日志/状态回填。
func (s *Scheduler) execute(ctx context.Context, def JobDef, trigger RunTrigger, fn JobFunc) {
	// 进程内 single-flight：上一轮未结束本轮直接跳过（robfig 同一 schedule 的重叠触发防线）。
	flag := s.flightFor(def.Code)
	if !flag.CompareAndSwap(false, true) {
		s.skip(ctx, def.Code, "上一轮仍在执行（进程内防重入）")
		return
	}
	defer flag.Store(false)

	// advisory lock：其他实例执行中跳过；锁检查失败 fail-closed（plan §4.3 多副本语义前提）。
	unlock, ok, err := s.locker.TryLock(ctx, lockKey(def.Code))
	if err != nil {
		s.skip(ctx, def.Code, fmt.Sprintf("advisory lock 检查失败，fail-closed 跳过: %v", err))
		return
	}
	if !ok {
		s.skip(ctx, def.Code, "advisory lock 未获取（其他实例执行中）")
		return
	}
	defer unlock()

	s.run(ctx, def, trigger, fn)
}

// run 执行 handler 并回填执行日志与 last_run_* 汇总（观测失败仅记日志，不影响业务结果）。
func (s *Scheduler) run(ctx context.Context, def JobDef, trigger RunTrigger, fn JobFunc) {
	start := s.now()
	runCtx, cancel := context.WithCancel(s.jobContext(ctx))
	defer cancel()

	runID, err := s.store.BeginRun(ctx, def.Code, trigger, start)
	if err != nil {
		s.log.Error("定时任务执行日志写入失败（继续执行）", zap.String("job", def.Code), zap.Error(err))
		runID = 0
	}

	// handler 兜底 recover：panic 视为执行失败（记 FAILED + 告警语义交给上层日志），
	// 不允许单任务 panic 击穿调度器 goroutine（robfig FuncJob 无内建恢复）。
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("handler panic: %v", r)
			}
		}()
		runErr = fn(runCtx)
	}()
	duration := s.now().Sub(start)
	success := runErr == nil
	message := ""
	if !success {
		message = runErr.Error()
	}

	if runID > 0 {
		if err := s.store.FinishRun(ctx, runID, s.now(), success, duration, message); err != nil {
			s.log.Error("定时任务执行日志回填失败", zap.String("job", def.Code), zap.Int64("run_id", runID), zap.Error(err))
		}
	}
	status := RunStatusSuccess
	if !success {
		status = RunStatusFailed
		s.log.Error("定时任务执行失败（不做同周期重试，下一周期自然重试）",
			zap.String("job", def.Code), zap.Duration("duration", duration), zap.Error(runErr))
		if s.alertFn != nil {
			s.alertFn(ctx, def.Code, message)
		}
	}
	next := s.nextRunAfter(def.Code, s.now())
	if err := s.store.UpdateJobState(ctx, def.Code, status, s.now(), duration, next); err != nil {
		s.log.Error("定时任务最近状态回填失败", zap.String("job", def.Code), zap.Error(err))
	}
}

// skip 记录防重入跳过（trigger=SKIPPED；不回填 last_run_*——跳过不覆盖最近真实执行状态）。
func (s *Scheduler) skip(ctx context.Context, code, message string) {
	if err := s.store.RecordSkipped(ctx, code, TriggerSkipped, s.now(), message); err != nil {
		s.log.Error("定时任务跳过记录写入失败", zap.String("job", code), zap.Error(err))
	}
	s.log.Info("定时任务本轮跳过", zap.String("job", code), zap.String("reason", message))
}

// flightFor 取任务级 single-flight 标记（惰性建）。
func (s *Scheduler) flightFor(code string) *atomic.Bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.flight[code]; ok {
		return f
	}
	f := &atomic.Bool{}
	s.flight[code] = f
	return f
}

// nextRunAfter 计算任务的下次调度时间（无已注册 schedule 时返回零值）。
func (s *Scheduler) nextRunAfter(code string, after time.Time) time.Time {
	s.mu.Lock()
	schedule, ok := s.schedules[code]
	s.mu.Unlock()
	if !ok {
		return time.Time{}
	}
	return schedule.Next(after)
}

// jobContext 任务执行上下文：Stop 关闭信号即取消，长任务可感知停机尽早退出。
func (s *Scheduler) jobContext(ctx context.Context) context.Context {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	c, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-stopped:
			cancel()
		case <-c.Done():
		}
	}()
	return c
}
