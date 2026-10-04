// 执行日志与注册表落库（scheduled_jobs / scheduled_job_runs，backend-m3-plan §5 000014、§4.3）。
//
// 写通路仅命中 sysops 白名单自有表（plan §2.3 判据 3 guard-readonly）；执行日志与任务
// 业务写入分属不同事务（执行日志不回滚业务结果——plan §4.3），本层各语句独立事务。
package sysops

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// RunTrigger 执行触发方式（chk_scheduled_job_runs_trigger 值域）。
type RunTrigger string

const (
	// TriggerScheduled 调度触发。
	TriggerScheduled RunTrigger = "SCHEDULED"
	// TriggerManual 手动触发（管理端/运维）。
	TriggerManual RunTrigger = "MANUAL"
	// TriggerSkipped 防重入跳过（advisory lock 未获取/同进程仍在执行/handler 未注册）。
	// 跳过行的 success=false、message 记原因，不参与 last_run_* 汇总。
	TriggerSkipped RunTrigger = "SKIPPED"
)

// RunStatus 最近执行状态（chk_scheduled_jobs_last_run_status 小写值域）。
type RunStatus string

const (
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
	RunStatusRunning RunStatus = "running"
	RunStatusNever   RunStatus = "never"
)

// jobRunStore 执行日志/注册表存储接口（单测以内存替身注入，不依赖 PostgreSQL）。
type jobRunStore interface {
	// UpsertRegistry 启动时同步冻结注册表行：缺行补默认值，已存在行更新 name/cron_expr/remark、
	// 保留 enabled（DB 行启停为管理员覆盖——plan §4.3"代码注册表为缺省，DB 行覆盖"）。
	UpsertRegistry(ctx context.Context, jobs []JobDef) error
	// EnabledCodes 返回 DB 中 enabled=true 的任务编码（决定本次启动调度范围）。
	EnabledCodes(ctx context.Context) ([]string, error)
	// BeginRun 记录执行开始（trigger 为 SCHEDULED/MANUAL）：插入 scheduled_job_runs 行并回填
	// scheduled_jobs.last_run_status=running，返回 run 行 ID。
	BeginRun(ctx context.Context, code string, trigger RunTrigger, startAt time.Time) (int64, error)
	// FinishRun 回填执行结束状态（end_at/success/duration_ms/message）。
	FinishRun(ctx context.Context, runID int64, finishedAt time.Time, success bool, duration time.Duration, message string) error
	// RecordSkipped 记录防重入跳过（trigger=SKIPPED，不回填 last_run_*——跳过不覆盖最近成功/失败状态）。
	RecordSkipped(ctx context.Context, code string, trigger RunTrigger, startAt time.Time, message string) error
	// UpdateJobState 回填 scheduled_jobs 最近执行汇总（last_run_at/status/duration + next_run_at）。
	UpdateJobState(ctx context.Context, code string, status RunStatus, finishedAt time.Time, duration time.Duration, nextRunAt time.Time) error
	// SetJobEnabled 更新启停覆盖（热更新持久化），返回受影响行数（0=注册表行缺失）。
	SetJobEnabled(ctx context.Context, code string, enabled bool) (int64, error)
}

// gormJobStore jobRunStore 的 GORM 实现（表无 GORM 模型，走原生参数化 SQL——
// 计数器表同款口径；表名/列名与 000014 DDL 一致）。
type gormJobStore struct {
	db *gorm.DB
}

func newGormJobStore(db *gorm.DB) *gormJobStore {
	return &gormJobStore{db: db}
}

func (s *gormJobStore) UpsertRegistry(ctx context.Context, jobs []JobDef) error {
	for _, j := range jobs {
		res := s.db.WithContext(ctx).Exec(`
			INSERT INTO scheduled_jobs (code, name, cron_expr, enabled, remark, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, TRUE, ?, now(), now(), 0, 0)
			ON CONFLICT (code) DO UPDATE
			SET name = EXCLUDED.name, cron_expr = EXCLUDED.cron_expr, remark = EXCLUDED.remark, updated_at = now()`,
			j.Code, j.Name, j.CronExpr, j.Remark)
		if res.Error != nil {
			return fmt.Errorf("sysops: 注册表行 upsert 失败（code=%s）: %w", j.Code, res.Error)
		}
	}
	return nil
}

func (s *gormJobStore) EnabledCodes(ctx context.Context) ([]string, error) {
	var codes []string
	err := s.db.WithContext(ctx).Raw(
		`SELECT code FROM scheduled_jobs WHERE enabled = TRUE`).Scan(&codes).Error
	return codes, err
}

func (s *gormJobStore) BeginRun(ctx context.Context, code string, trigger RunTrigger, startAt time.Time) (int64, error) {
	var runID int64
	// CTE RETURNING 原子取回插入 ID（插入与取回同语句，规避并发/时序定位问题）。
	err := s.db.WithContext(ctx).Raw(`
		WITH ins AS (
			INSERT INTO scheduled_job_runs (job_code, "trigger", start_at, success, duration_ms, message, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, FALSE, 0, '', now(), now(), 0, 0)
			RETURNING id
		)
		SELECT id FROM ins`,
		code, string(trigger), startAt).Scan(&runID).Error
	return runID, err
}

func (s *gormJobStore) FinishRun(ctx context.Context, runID int64, finishedAt time.Time, success bool, duration time.Duration, message string) error {
	return s.db.WithContext(ctx).Exec(`
		UPDATE scheduled_job_runs
		SET end_at = ?, success = ?, duration_ms = ?, message = ?, updated_at = now()
		WHERE id = ?`,
		finishedAt, success, duration.Milliseconds(), message, runID).Error
}

func (s *gormJobStore) RecordSkipped(ctx context.Context, code string, trigger RunTrigger, startAt time.Time, message string) error {
	return s.db.WithContext(ctx).Exec(`
		INSERT INTO scheduled_job_runs (job_code, "trigger", start_at, end_at, success, duration_ms, message, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, FALSE, 0, ?, now(), now(), 0, 0)`,
		code, string(trigger), startAt, startAt, message).Error
}

func (s *gormJobStore) UpdateJobState(ctx context.Context, code string, status RunStatus, finishedAt time.Time, duration time.Duration, nextRunAt time.Time) error {
	// next_run_at 零值写 NULL（手动触发无已注册 schedule 时不产生伪下次执行时间）。
	var nextRun any
	if !nextRunAt.IsZero() {
		nextRun = nextRunAt
	}
	return s.db.WithContext(ctx).Exec(`
		UPDATE scheduled_jobs
		SET last_run_at = ?, last_run_status = ?, last_run_duration_ms = ?, next_run_at = ?, updated_at = now()
		WHERE code = ?`,
		finishedAt, string(status), duration.Milliseconds(), nextRun, code).Error
}

func (s *gormJobStore) SetJobEnabled(ctx context.Context, code string, enabled bool) (int64, error) {
	res := s.db.WithContext(ctx).Exec(
		`UPDATE scheduled_jobs SET enabled = ?, updated_at = now() WHERE code = ?`, enabled, code)
	return res.RowsAffected, res.Error
}

// sqlLockAcquirer advisory lock 获取器（plan §4.3 多副本防重：pg_try_advisory_lock）。
// 锁为会话级：从连接池取专用连接持锁，解锁必须复用同一连接（连接池直用会漂移连接）。
type sqlLockAcquirer struct {
	db *gorm.DB
}

// lockTimeout 单次锁操作超时（拿锁/解锁均为快速单语句）。
const lockTimeout = 5 * time.Second

// TryLock 尝试获取任务级咨询锁，成功返回解锁函数。
// err 非 nil 表示锁检查失败（PG 不可达等）——调用方必须 fail-closed 跳过本轮，
// 不得在无锁状态下执行（plan §4.3 多副本语义的正确性前提）。
func (l *sqlLockAcquirer) TryLock(ctx context.Context, key string) (unlock func(), ok bool, err error) {
	sqlDB, err := l.db.DB()
	if err != nil {
		return nil, false, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	conn, err := sqlDB.Conn(lockCtx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRowContext(lockCtx,
		`SELECT pg_try_advisory_lock(hashtext(?))`, key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	unlock = func() {
		defer conn.Close()
		// 解锁失败不阻断主流程：连接 Close 即会话终止，会话级 advisory lock 自动释放兜底。
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), lockTimeout)
		defer unlockCancel()
		var released bool
		_ = conn.QueryRowContext(unlockCtx,
			`SELECT pg_advisory_unlock(hashtext(?))`, key).Scan(&released)
	}
	return unlock, true, nil
}

// lockKey 任务级咨询锁键（plan §4.3：hashtext('sfjob:'||code)）。
func lockKey(code string) string {
	return "sfjob:" + code
}
