package sysops

import (
	"context"
	"fmt"
	"time"
)

// 定时任务管理（/api/system/jobs，backend-m3-plan §10.3、architecture §9.2）：
// 列表（scheduled_jobs 行）/ 启停（DB 行持久化 + 进程内 cron 热更新）/ 执行日志分页。
// cron 表达式 M3 不开放修改（冻结在代码注册表，防任意 cron 注入——plan §10.3）。

// jobItem 任务行（web/src/api/system.ts SystemJobItem 契约，snake_case tag；
// id 用 scheduled_jobs.id，启停按 id 解析任务编码）。
type jobItem struct {
	ID                int64      `json:"id"`
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	Cron              string     `json:"cron"`
	Enabled           bool       `json:"enabled"`
	LastRunAt         *time.Time `json:"last_run_at"`
	LastRunStatus     string     `json:"last_run_status"`
	LastRunDurationMS int64      `json:"last_run_duration_ms"`
	NextRunAt         *time.Time `json:"next_run_at"`
	Remark            string     `json:"remark,omitempty"`
	// Scheduled 进程内是否已装配调度（handler 未注册的空实现位任务为 false——
	// 管理端启停仅落库，下次启动按 DB 行决定调度）。
	Scheduled bool `json:"scheduled"`
}

// jobRunLogItem 执行日志行（SystemJobRunLogItem 契约）。
type jobRunLogItem struct {
	ID         int64      `json:"id"`
	JobCode    string     `json:"job_code"`
	Trigger    string     `json:"trigger"`
	StartAt    time.Time  `json:"start_at"`
	EndAt      *time.Time `json:"end_at"`
	Success    bool       `json:"success"`
	DurationMS int64      `json:"duration_ms"`
	Message    string     `json:"message,omitempty"`
}

// jobFilter 任务列表筛选（keyword=编码/名称模糊；enabled 三态）。
type jobFilter struct {
	Keyword  string
	Enabled  *bool
	Page     int
	PageSize int
}

func (r *runtimeRepo) listJobs(ctx context.Context, f jobFilter) ([]jobItem, int64, error) {
	conds := []string{"1 = 1"}
	var args []any
	if f.Keyword != "" {
		conds = append(conds, "(code ILIKE ? OR name ILIKE ?)")
		like := "%" + f.Keyword + "%"
		args = append(args, like, like)
	}
	if f.Enabled != nil {
		conds = append(conds, "enabled = ?")
		args = append(args, *f.Enabled)
	}
	where := conds[0]
	for _, c := range conds[1:] {
		where += " AND " + c
	}
	base := "FROM scheduled_jobs WHERE " + where
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) "+base, args...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 任务计数失败: %w", err)
	}
	var rows []jobItem
	list := `SELECT id, code, name, cron_expr, enabled, last_run_at, last_run_status,
	                last_run_duration_ms, next_run_at, remark
	         ` + base + ` ORDER BY id LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	if err := r.db.WithContext(ctx).Raw(list, args...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 任务查询失败: %w", err)
	}
	attached := schedRef.Load() != nil
	if attached {
		for i := range rows {
			_, ok := jobHandlerFor(rows[i].Code)
			rows[i].Scheduled = ok
		}
	}
	return rows, total, nil
}

// getJobByID 按 id 取任务行（启停/执行日志路径解析编码）。
func (r *runtimeRepo) getJobByID(ctx context.Context, id int64) (*jobItem, bool, error) {
	var row jobItem
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, code, name, cron_expr, enabled, last_run_at, last_run_status,
		       last_run_duration_ms, next_run_at, remark
		FROM scheduled_jobs WHERE id = ?`, id).Scan(&row).Error
	if err != nil {
		return nil, false, err
	}
	if row.ID == 0 {
		return nil, false, nil
	}
	return &row, true, nil
}

// listJobRunLogs 执行日志分页（按任务编码；start_at 倒序）。
func (r *runtimeRepo) listJobRunLogs(ctx context.Context, jobCode string, page, pageSize int) ([]jobRunLogItem, int64, error) {
	base := `FROM scheduled_job_runs WHERE job_code = ?`
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) "+base, jobCode).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 执行日志计数失败: %w", err)
	}
	var rows []jobRunLogItem
	list := `SELECT id, job_code, "trigger", start_at, end_at, success, duration_ms, message
	         ` + base + ` ORDER BY start_at DESC, id DESC LIMIT ? OFFSET ?`
	if err := r.db.WithContext(ctx).Raw(list, jobCode, pageSize, (page-1)*pageSize).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("sysops: 执行日志查询失败: %w", err)
	}
	return rows, total, nil
}
