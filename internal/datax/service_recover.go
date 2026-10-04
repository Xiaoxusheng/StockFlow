package datax

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/sysops"
)

// 非终态任务启动清扫（backend-m3-plan §4.2 悬挂窗口收口，2026-10-04 评审修复轮新增）：
//
//	Confirm/CreateExport 的事务提交与 queue.Enqueue 之间进程崩溃 → 任务行永久停在
//	EXECUTING/QUEUED 且无 asynq 在途投递；FailureFinalizer 只覆盖"有投递且失败"路径。
//	本清扫在进程启动期一次性回收（单实例语义，plan §4.3 同口径）：updated_at 超过
//	staleAfter 仍未终态的任务行标记 FAILED + error_message + 审计 + 站内告警
//	（sysops.NotifyTaskFailure，dedup=taskfail:{type}:{单号} 防重启重复告警），
//	用户重新发起导入/导出（导入断点行面已提交部分经幂等键不重复入账）。
//
//	误判防护：staleAfter=30 分钟 ≫ asynq 缺省指数退避的三次重试总间隔（15s–120s 量级，
//	server.go DefaultRetryDelayFunc）——在途重试期间任务行 updated_at 不变也不会被误清；
//	清扫后若仍有迟到的重投递，handler 状态守卫（非 EXECUTING/QUEUED 静默丢弃）兜底。
//	数据面：import_tasks/export_tasks 守卫更新（白名单四表内，guard-datax）+ 审计 + 通知。

// StaleTaskAfter 非终态任务判定阈值（updated_at 早于 now-30min 仍未终态 = 悬挂）。
const StaleTaskAfter = 30 * time.Minute

// recoverMessage 清扫回填的 error_message（用户可读，指引重新发起）。
const recoverMessage = "任务执行中断（进程重启或投递丢失，启动清扫回收），请重新发起导入/导出"

// RecoverStaleTasks 启动期非终态任务清扫（cmd/server main 装配期调用一次；
// 独立于冻结五任务注册表——plan §4.3 注册表不扩项，悬挂回收属任务域自身收口）。
// 返回回收任务数；行级失败不中断整体回收（逐行记日志后继续），仅汇总错误上抛。
func RecoverStaleTasks(ctx context.Context, db *gorm.DB, log *zap.Logger, staleAfter time.Duration) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("datax: 非终态任务清扫失败: db 为 nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	cutoff := time.Now().Add(-staleAfter)
	recovered := 0

	// 1) 导入任务：EXECUTING 悬挂（Confirm 提交后入队前崩溃）。
	var imps []ImportTask
	if err := db.WithContext(ctx).Raw(`
		SELECT id, import_no FROM import_tasks
		WHERE status = ? AND updated_at < ?`, TaskStatusExecuting, cutoff).Scan(&imps).Error; err != nil {
		return recovered, fmt.Errorf("datax: 悬挂导入任务读取失败: %w", err)
	}
	for _, t := range imps {
		res := db.WithContext(ctx).Exec(`
			UPDATE import_tasks SET status = ?, error_message = ?, finished_at = now(), updated_at = now()
			WHERE id = ? AND status = ?`, TaskStatusFailed, recoverMessage, t.ID.Int64(), TaskStatusExecuting)
		if res.Error != nil {
			log.Warn("datax: 悬挂导入任务清扫失败", zap.String("import_no", t.ImportNo), zap.Error(res.Error))
			continue
		}
		if res.RowsAffected == 0 {
			continue // 并发回收/已终态（幂等）
		}
		recovered++
		sweepAudit(db, log, "import_task", t.ID.Int64(), t.ImportNo)
		sweepNotify(ctx, db, log, asynqx.TaskTypeImportCommit, t.ImportNo)
	}

	// 2) 导出任务：QUEUED（入队前崩溃）/PROCESSING（执行中崩溃）悬挂。
	var exps []ExportTask
	if err := db.WithContext(ctx).Raw(`
		SELECT id, export_no FROM export_tasks
		WHERE status IN (?, ?) AND updated_at < ?`, TaskStatusQueued, TaskStatusProcessing, cutoff).Scan(&exps).Error; err != nil {
		return recovered, fmt.Errorf("datax: 悬挂导出任务读取失败: %w", err)
	}
	for _, t := range exps {
		res := db.WithContext(ctx).Exec(`
			UPDATE export_tasks SET status = ?, error_message = ?, finished_at = now(), updated_at = now()
			WHERE id = ? AND status IN (?, ?)`, TaskStatusFailed, recoverMessage, t.ID.Int64(), TaskStatusQueued, TaskStatusProcessing)
		if res.Error != nil {
			log.Warn("datax: 悬挂导出任务清扫失败", zap.String("export_no", t.ExportNo), zap.Error(res.Error))
			continue
		}
		if res.RowsAffected == 0 {
			continue
		}
		recovered++
		sweepAudit(db, log, "export_task", t.ID.Int64(), t.ExportNo)
		sweepNotify(ctx, db, log, asynqx.TaskTypeExportRun, t.ExportNo)
	}
	return recovered, nil
}

// sweepAudit 清扫审计（无业务 actor——系统归因 OperatorID=0，与通知 created_by=0 同款；
// 独立写入，失败仅记日志不阻断回收）。
func sweepAudit(db *gorm.DB, log *zap.Logger, objectType string, objectID int64, taskNo string) {
	err := middleware.Audit(db.WithContext(context.Background()), middleware.AuditEntry{
		Module: "datax", ObjectType: objectType, ObjectID: objectID, Action: "recover",
		Success: true, OperatorID: 0, OperatorName: "system",
		After: map[string]any{"status": TaskStatusFailed, "message": recoverMessage},
	})
	if err != nil {
		log.Warn("datax: 悬挂任务清扫审计写入失败", zap.String("task_no", taskNo), zap.Error(err))
	}
}

// sweepNotify 清扫站内告警（super_admin/sys_admin；dedup 防重启重复）。
func sweepNotify(ctx context.Context, db *gorm.DB, log *zap.Logger, taskType, taskNo string) {
	if err := sysops.NotifyTaskFailure(ctx, db, taskType, taskNo, recoverMessage); err != nil {
		log.Warn("datax: 悬挂任务清扫告警写入失败", zap.String("task_no", taskNo), zap.Error(err))
	}
}
