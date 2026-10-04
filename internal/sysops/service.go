package sysops

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Service 平台运维查询/管理面（预警扫描执行体在 scans.go/watch.go，经 cron 注册表
// 触发；本 Service 承载 /api/logs /api/system /api/notifications 的查询与管理动作）。
type Service struct {
	repo *runtimeRepo
	now  func() time.Time
}

// Option Service 装配项。
type Option func(*Service)

// WithClock 注入时钟（单测）。
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// NewService 构造（db 为 nil 由 RegisterRoutes fail-fast）。
func NewService(db *gorm.DB, opts ...Option) *Service {
	s := &Service{repo: newRuntimeRepo(db), now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ---- 审计日志查询（只读）----

// ListOperationLogs 操作日志分页（architecture §8.2 筛选维度）。
func (s *Service) ListOperationLogs(ctx context.Context, f logFilter) ([]operationLogItem, int64, error) {
	return s.repo.listOperationLogs(ctx, f)
}

// GetOperationLog 操作日志详情（三快照原样；越权无关——审计为平台只读面）。
func (s *Service) GetOperationLog(ctx context.Context, id int64) (*operationLogItem, error) {
	row, ok, err := s.repo.getOperationLog(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLogNotFound
	}
	return row, nil
}

// ListLoginLogs 登录日志分页。
func (s *Service) ListLoginLogs(ctx context.Context, f logFilter) ([]loginLogItem, int64, error) {
	return s.repo.listLoginLogs(ctx, f)
}

// ---- 系统配置 ----

// ListConfigs 全量配置（量级有限不分页——plan §10.2）。
func (s *Service) ListConfigs(ctx context.Context) ([]systemConfigItem, error) {
	return s.repo.listConfigs(ctx)
}

// SaveConfigs 批量保存（逐项 UPDATE + 同事务逐项审计；readonly/不存在 fail-closed）。
func (s *Service) SaveConfigs(ctx context.Context, actor auditActor, items []saveConfigItem) (int, error) {
	return s.repo.saveConfigs(ctx, actor, items)
}

// ---- 定时任务管理 ----

// ListJobs 任务列表。
func (s *Service) ListJobs(ctx context.Context, f jobFilter) ([]jobItem, int64, error) {
	return s.repo.listJobs(ctx, f)
}

// SetJobStatus 启停（DB 行持久化 + 进程内 cron 热更新——plan §4.3/§10.3）。
// 调度器未创建（路由已装配而 main 未到 scheduler.Start 的窗口）：仅落库，
// 下次 Start 按 DB 行决定调度（不报错、不假装热更）。
func (s *Service) SetJobStatus(ctx context.Context, id int64, enabled bool) (*jobItem, error) {
	job, ok, err := s.repo.getJobByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrJobNotFound
	}
	sched := attachedScheduler()
	if sched != nil {
		if _, registered := jobHandlerFor(job.Code); !registered {
			return nil, ErrJobNotScheduled
		}
		if err := sched.SetEnabled(ctx, job.Code, enabled); err != nil {
			return nil, err
		}
	} else {
		// 调度器未创建：仅持久化启停覆盖（Start 语义按 DB 行调度，状态最终一致）。
		if _, err := (&gormJobStore{db: s.repo.db}).SetJobEnabled(ctx, job.Code, enabled); err != nil {
			return nil, err
		}
	}
	job.Enabled = enabled
	return job, nil
}

// ListJobRunLogs 执行日志分页。
func (s *Service) ListJobRunLogs(ctx context.Context, id int64, page, pageSize int) ([]jobRunLogItem, int64, error) {
	job, ok, err := s.repo.getJobByID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, ErrJobNotFound
	}
	return s.repo.listJobRunLogs(ctx, job.Code, page, pageSize)
}

// ---- 系统监控 ----

// MonitorSnapshot 监控指标（deployment §5 指标矩阵；裁决①口径见响应 remarks）。
func (s *Service) MonitorSnapshot(ctx context.Context, rdb redisPinger, queues QueueStatsReader, version string) (*monitorResponse, error) {
	m := &monitorService{
		db:           s.repo.db,
		rdb:          rdb,
		queues:       queues,
		version:      version,
		processStart: processStart,
	}
	return m.snapshot(ctx)
}

// processStart 进程启动时间（uptime 口径；包初始化即进程装配早期）。
var processStart = time.Now()
