package sysops

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// 作业任务超时检测（task_timeout_scan，backend-m3-plan §4.3 注册表 0 * * * *）：
// 拣货（pick_tasks）/上架（putaway_tasks）任务创建时间 + 阈值（system_configs
// task.timeout.pick_hours / task.timeout.putaway_hours，缺省 4 小时——§10.2 冻结键）
// 仍未完成 → 站内通知（预警类收件人 super_admin/sys_admin/warehouse_manager，§10.6）。
//
// 幂等：dedup_key = tasktimeout:{任务号}（+收件人后缀）——同一任务号至多通知一次，
// 任务完成前每小时扫描不重复打扰，完成后状态离开在途集合自然不再命中。
// 数据面：任务表只读 SELECT（零写），通知写 notifications（白名单自有表）。
//
// 任务表无跨域外键（M3 迁移通用规则），超时判定所需列（单号/状态/创建时间）为
// 000007/000008 冻结 DDL 既有列，只读访问与预警扫描同口径。

// 任务超时阈值缺省值（plan §10.2 seed 缺省：4 小时）。
const defTaskTimeoutHours = 4

// timeoutTaskRow 在途超时任务行（pick/putaway 共用人读字段投影）。
type timeoutTaskRow struct {
	TaskNo      string // pick_no / putaway_no
	WarehouseID int64
	CreatedAt   time.Time
}

// pendingTimeoutTaskRows 只读聚合：创建超过阈值仍未完成的两类任务行。
func (s scanRunner) pendingTimeoutTaskRows(ctx context.Context, pickCutoff, putawayCutoff time.Time) ([]timeoutTaskRow, error) {
	var rows []timeoutTaskRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT pick_no AS task_no, warehouse_id, created_at
		FROM pick_tasks
		WHERE status IN ('PENDING','CLAIMED','PICKING') AND created_at <= ?
		UNION ALL
		SELECT putaway_no AS task_no, target_warehouse_id AS warehouse_id, created_at
		FROM putaway_tasks
		WHERE status IN ('PENDING','IN_PROGRESS') AND created_at <= ?
		ORDER BY created_at`, pickCutoff, putawayCutoff).Scan(&rows).Error
	return rows, err
}

// taskTimeoutScan 拣货/上架任务超时扫描（wiring.go 注册为 task_timeout_scan 执行体）。
func (s scanRunner) taskTimeoutScan(ctx context.Context) error {
	pickHours, err := s.configValueInt(ctx, cfgKeyTaskTimeoutPickHours, defTaskTimeoutHours)
	if err != nil {
		return fmt.Errorf("读取拣货超时阈值失败: %w", err)
	}
	putawayHours, err := s.configValueInt(ctx, cfgKeyTaskTimeoutPutawayHours, defTaskTimeoutHours)
	if err != nil {
		return fmt.Errorf("读取上架超时阈值失败: %w", err)
	}
	now := s.now()
	rows, err := s.pendingTimeoutTaskRows(ctx, now.Add(-time.Duration(pickHours)*time.Hour), now.Add(-time.Duration(putawayHours)*time.Hour))
	if err != nil {
		return fmt.Errorf("超时任务行聚合失败: %w", err)
	}
	// 收件人集合每轮扫描恒定：循环外解析一次（防 N+1）。
	recipients, err := resolveRecipients(ctx, s.db, alertRecipientRoles)
	if err != nil {
		return fmt.Errorf("超时收件人解析失败: %w", err)
	}
	var notified int64
	for _, r := range rows {
		idle := now.Sub(r.CreatedAt).Truncate(time.Minute)
		n, err := notifyRecipients(ctx, gormNotifyStore{db: s.db}, notifyInput{
			Type:  NotifyTypeTask,
			Title: fmt.Sprintf("作业任务超时：%s", r.TaskNo),
			Content: fmt.Sprintf(
				"任务 %s 已创建 %s 仍未完成（超过超时阈值），请检查任务进度或上报异常。",
				r.TaskNo, idle),
			// dedup = 任务号（+收件人）——同一任务至多通知一次（plan §4.3 注册表）。
			DedupKey: fmt.Sprintf("tasktimeout:%s", r.TaskNo),
		}, recipients)
		if err != nil {
			return fmt.Errorf("任务超时通知写入失败（task=%s）: %w", r.TaskNo, err)
		}
		notified += n
	}
	s.log.Info("任务超时扫描完成",
		zap.Int("timeout_rows", len(rows)),
		zap.Int64("notified", notified))
	return nil
}

// configValueInt scanRunner 数值配置读取（行缺失回退 def；非法值 fail-closed 上抛）。
func (s scanRunner) configValueInt(ctx context.Context, key string, def int) (int, error) {
	raw, err := s.configValue(ctx, key, fmt.Sprintf("%d", def))
	if err != nil {
		return def, err
	}
	n, err := parseIntConfig(raw, key)
	if err != nil || n < 1 {
		if err != nil {
			return def, err
		}
		return def, fmt.Errorf("配置 %s 数值非法: %d（必须为正整数）", key, n)
	}
	return n, nil
}

// 超时阈值配置键（plan §10.2 冻结 seed 键；configs.go configSeeds 同源）。
const (
	cfgKeyTaskTimeoutPickHours    = "task.timeout.pick_hours"
	cfgKeyTaskTimeoutPutawayHours = "task.timeout.putaway_hours"
)
