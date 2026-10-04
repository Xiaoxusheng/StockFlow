package datax

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/sysops"
)

// 导入执行器（asynqx 任务类型 datax:import:commit 的 handler 主体，plan §4.2/§6.2）：
//
//   - 进入即以任务行状态守卫确认执行权（status=EXECUTING 之外静默丢弃重投递）；
//   - 断点续跑（excel §6.3）：仅取 QUEUED/FAILED 行，SUCCESS 行跳过；
//   - 按 datax.batch_size 分批调用 Writer.Commit（写路径经域内既有 Service 通路，plan
//     §2.2 规则①/判据 10），单批失败不影响已完成批次（excel §6.2）；
//   - Writer 返回 error = 基础设施失败 → 交由 asynq 重试（行状态未动，重入续跑）；
//   - 终态：全部成功 SUCCESS / 部分失败 PARTIAL_SUCCESS / 全部失败 FAILED，
//     结果回写与执行均记 operation_logs（module=datax，plan §6.2）。

// importCommitPayload 入队载荷（actor 随载荷透传：执行归因于确认导入的用户）。
type importCommitPayload struct {
	ImportNo string `json:"import_no"`
	Actor    Actor  `json:"actor"`
}

// RunImportCommit 任务执行入口（注册为 asynqx handler，见 service_queue.go）。
func (s *Service) RunImportCommit(ctx context.Context, t asynqx.Task) error {
	var p importCommitPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ImportNo == "" {
		return response.NewError(ErrPayloadInvalid, map[string]any{"reason": "导入执行载荷非法"})
	}
	task, err := s.repo.FindImportTaskByNo(ctx, p.ImportNo)
	if err != nil {
		return err // 任务行缺失属数据异常：返回 error 交由队列归档暴露
	}
	// 状态守卫（plan §4.2）：非 EXECUTING = 已被处理/未确认，静默丢弃重投递。
	if task.Status != TaskStatusExecuting {
		s.log.Info("datax: 导入任务重投递被状态守卫丢弃",
			zap.String("import_no", p.ImportNo), zap.String("status", task.Status))
		return nil
	}
	w, err := s.writerFor(task.ImportType)
	if err != nil {
		// Writer 运行期缺位 = 装配错误，重试无意义 → 终态 FAILED 披露。
		return s.finalizeImport(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, 0, 0,
			"导入写入器未装配: "+task.ImportType)
	}
	spec := w.Template()

	// 断点续跑：仅 QUEUED/FAILED 行（SUCCESS 行跳过，excel §6.3）。
	rows, err := s.repo.ListImportRows(ctx, task.ID.Int64(), []string{RowStatusQueued, RowStatusFailed}, 0)
	if err != nil {
		return err
	}
	// 计数以行状态为真相源收敛（重试把 FAILED 行改写为 SUCCESS 后计数必须收敛到
	// 行面事实——progress 由行列推导，plan §13.3）：successRows 取执行前已 SUCCESS 行数
	// （本轮跳过不再处理），failedRows 必须从 0 起算——本轮重试清单包含全部 FAILED 行，
	// 每行本轮必然获得最终状态（成功/失败二选一）；若以执行前 FAILED 计数为种子，
	// 重试行再失败会双倍计入（如 50→100）、改成功则 success+failed 超出 total_rows。
	counts, err := s.repo.CountImportRowsByStatus(ctx, task.ID.Int64())
	if err != nil {
		return err
	}
	successRows, failedRows := counts[RowStatusSuccess], int64(0)
	for start := 0; start < len(rows); start += s.cfg.BatchSize {
		end := start + s.cfg.BatchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		inRows := make([]ImportRow, 0, len(batch))
		for _, r := range batch {
			raw := map[string]string{}
			for k, v := range r.Raw {
				if sv, ok := v.(string); ok {
					raw[k] = sv
				}
			}
			inRows = append(inRows, ImportRow{
				RowNo: r.RowNo,
				Cells: rowCellsFromJSON(spec, r.Parsed, raw),
				Raw:   raw,
			})
		}
		res, cerr := w.Commit(ctx, p.Actor, TaskRef{
			TaskID: task.ID.Int64(), ImportNo: task.ImportNo, ImportType: task.ImportType,
		}, inRows)
		if cerr != nil {
			// 基础设施失败：行状态未动，asynq 重试后从本批断点续跑（plan §4.2）。
			return fmt.Errorf("datax: 导入批次执行失败（import_no=%s batch_start_row=%d）: %w",
				task.ImportNo, batch[0].RowNo, cerr)
		}
		// 逐行结果落库（行级失败进 errors——断点续跑与结果展示依据，plan §6.2）。
		failed := make(map[int]RowError, len(res.Errors))
		for _, e := range res.Errors {
			failed[e.Row] = e
		}
		batchOK, batchFail := 0, 0
		err = s.inTx(ctx, func(tx *gorm.DB) error {
			for _, r := range batch {
				status := RowStatusSuccess
				set := map[string]any{"updated_by": p.Actor.UserID}
				if fe, bad := failed[r.RowNo]; bad {
					status = RowStatusFailed
					set["errors"] = []RowError{fe}
					batchFail++
				} else {
					set["errors"] = nil
					batchOK++
				}
				set["status"] = status
				if err := s.repo.UpdateImportRow(tx, r.ID.Int64(), set); err != nil {
					return err
				}
			}
			successRows += int64(batchOK)
			failedRows += int64(batchFail)
			// 计数回填在行更新同事务（失败整体回滚本批结果，重试重算——批间独立）。
			_, err := s.repo.GuardUpdateImportTask(tx, task.ID.Int64(), nil, map[string]any{
				"success_rows": successRows, "failed_rows": failedRows, "updated_by": p.Actor.UserID,
			})
			return err
		})
		if err != nil {
			return err
		}
	}

	// 终态汇总（excel §6.2：全部成功 SUCCESS / 部分失败 PARTIAL_SUCCESS / 全败 FAILED）。
	final := TaskStatusSuccess
	message := ""
	switch {
	case failedRows > 0 && successRows > 0:
		final = TaskStatusPartial
		message = fmt.Sprintf("部分行导入失败：成功 %d 行，失败 %d 行", successRows, failedRows)
	case failedRows > 0 && successRows == 0:
		final = TaskStatusFailed
		message = fmt.Sprintf("导入失败：全部 %d 行未成功", failedRows)
	}
	return s.finalizeImport(ctx, p.Actor, task.ID.Int64(), final, successRows, failedRows, message)
}

// finalizeImport 导入任务终态回填（守卫 EXECUTING → 终态 + finished_at + 审计）。
// 幂等：守卫 0 行（已终态，重投递/并发回写）直接返回 nil。
func (s *Service) finalizeImport(ctx context.Context, actor Actor, taskID int64, status string, successRows, failedRows int64, message string) error {
	now := time.Now()
	return s.inTx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.GuardUpdateImportTask(tx, taskID, []string{TaskStatusExecuting}, map[string]any{
			"status": status, "success_rows": successRows, "failed_rows": failedRows,
			"error_message": message, "finished_at": now, "updated_at": database.Now(),
		})
		if err != nil || n == 0 {
			return err // 0 行 = 已终态（幂等重入），静默返回
		}
		e := actor.auditEntry("import_task", taskID, "commit")
		e.Success = status != TaskStatusFailed
		if !e.Success {
			e.ErrorCode = "DATAX_IMPORT_FAILED"
		}
		e.After = map[string]any{"status": status, "success_rows": successRows, "failed_rows": failedRows, "message": message}
		return s.audit(tx, e)
	})
}

// FinalizeImportFailure 失败终态回调（asynqx.FailureFinalizer，service_queue.go 注册）：
// 基础设施失败重试耗尽（asynq）/inline 无重试环境时，任务行落终态 FAILED + error_message
// 并发站内告警（plan §4.2"重试耗尽 → 终态 FAILED + error_message + 站内告警"）——否则
// 任务永久卡 EXECUTING，用户侧永远显示"处理中"（INITIAL_INVENTORY 等高危导入尤其会让
// 操作员误判灌数仍在进行）。幂等：finalizeImport 守卫 EXECUTING，已终态静默。
func (s *Service) FinalizeImportFailure(ctx context.Context, t asynqx.Task, cause error) error {
	var p importCommitPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ImportNo == "" {
		// 载荷非法无法定位任务行（该路径 handler 亦未消费任务行）：如实上抛由基座日志披露。
		return fmt.Errorf("datax: 导入失败终态回调载荷非法（task_id=%s）: %w", t.TaskID, err)
	}
	task, err := s.repo.FindImportTaskByNo(ctx, p.ImportNo)
	if err != nil {
		return fmt.Errorf("datax: 导入失败终态回调装载任务失败（import_no=%s）: %w", p.ImportNo, err)
	}
	message := fmt.Sprintf("导入执行失败（重试耗尽或无重试环境，已终止）：%s", truncateMessage(cause.Error(), 500))
	if err := s.finalizeImport(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed,
		int64(task.SuccessRows), int64(task.FailedRows), message); err != nil {
		return err
	}
	// 站内告警（super_admin/sys_admin——plan §10.6 失败告警类收件人；去重防重放）。
	if err := sysops.NotifyTaskFailure(ctx, s.repo.DB(), asynqx.TaskTypeImportCommit, p.ImportNo, message); err != nil {
		s.log.Error("datax: 导入失败告警写入失败", zap.String("import_no", p.ImportNo), zap.Error(err))
	}
	return nil
}
