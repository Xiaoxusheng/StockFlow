package datax

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
	"github.com/stockflow/server/internal/sysops"
)

// 导出执行器（asynqx 任务类型 datax:export:run 的 handler 主体，plan §4.2/§6.3）：
//
//   - 状态守卫：QUEUED → PROCESSING（首次执行）/ PROCESSING（重试续跑，整文件重生成
//     覆盖——纯函数幂等，plan §4.2）；终态静默丢弃重投递；
//   - Count 定总数 → meta 区（报表名/导出时间/导出人/查询条件——excel §2.3）→
//     StreamWriter 按 keyset 游标逐批写（每批 datax.export_batch_size，内存不驻留整表）→
//     合计行（Summary 声明，excel §2.3）→ storage 落盘 → files 登记 → SUCCESS；
//   - 进度 = 已写行数/total 每批 UPDATE progress（批外更新，plan §13.3）；
//   - 契约违约（0 行且未结束/游标不推进）终止任务防死循环；基础设施错误返回 error
//     交 asynq 重试。

// exportRunPayload 入队载荷。
type exportRunPayload struct {
	ExportNo string `json:"export_no"`
	Actor    Actor  `json:"actor"`
}

// RunExportRun 任务执行入口（注册为 asynqx handler，见 service_queue.go）。
func (s *Service) RunExportRun(ctx context.Context, t asynqx.Task) error {
	var p exportRunPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ExportNo == "" {
		return response.NewError(ErrPayloadInvalid, map[string]any{"reason": "导出执行载荷非法"})
	}
	task, err := s.repo.FindExportTaskByNo(ctx, p.ExportNo)
	if err != nil {
		return err
	}
	switch task.Status {
	case TaskStatusQueued:
		n, err := s.repo.GuardUpdateExportTask(s.repo.DB(), task.ID.Int64(), []string{TaskStatusQueued}, map[string]any{
			"status": TaskStatusProcessing, "started_at": time.Now(), "updated_by": p.Actor.UserID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil // 并发抢占失败：另一 worker 已开始，静默退出
		}
	case TaskStatusProcessing:
		// 重试续跑：整文件重生成覆盖（plan §4.2 纯函数幂等口径）。
	default:
		s.log.Info("datax: 导出任务重投递被状态守卫丢弃",
			zap.String("export_no", p.ExportNo), zap.String("status", task.Status))
		return nil
	}

	src, err := s.sourceFor(task.Module)
	if err != nil {
		return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, 0, 0, 0,
			"导出行源未装配: "+task.Module)
	}
	f, err := s.filterFromParams(task)
	if err != nil {
		return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, 0, 0, 0,
			"导出参数解析失败: "+err.Error())
	}

	total, err := src.Count(ctx, f)
	if err != nil {
		return fmt.Errorf("datax: 导出统计失败（export_no=%s module=%s）: %w", task.ExportNo, task.Module, err)
	}

	// meta 区（excel §2.3：标题、导出时间、导出人、查询条件）。
	condition := describeExportCondition(task.Scope, f)
	wb, err := newExportWorkbook(ModuleLabel(task.Module), []string{
		"导出时间：" + time.Now().Format(dateLayout),
		"导出人：" + p.Actor.Username,
		"查询条件：" + condition,
	}, src.Columns(), src.Summary())
	if err != nil {
		return err
	}

	// keyset 游标分批（plan §6.3：禁止 offset 深翻页；游标=排序键最后值）。
	cursor := Cursor{}
	written := int64(0)
	batchNo := 0
	for {
		rows, next, err := src.Batch(ctx, f, cursor)
		if err != nil {
			wb.Abort()
			return fmt.Errorf("datax: 导出读取批次失败（export_no=%s cursor=%s）: %w", task.ExportNo, cursor.Value, err)
		}
		if len(rows) == 0 && !next.Done {
			wb.Abort()
			// 行源契约违约：重试无法恢复（确定性），终态失败披露（防死循环——go-dev-standard
			// 规则 10/15：资源与循环必须有界）。
			return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, total, written, 0,
				"行源返回空批次但游标未结束（契约违约）")
		}
		for _, r := range rows {
			if err := wb.AppendRow(r); err != nil {
				wb.Abort()
				return err
			}
		}
		written += int64(len(rows))
		// 进度每批回写（批外更新，失败不回滚业务批次——plan §13.3）。
		progress := deriveProgress(written, total)
		if uerr := s.repo.UpdateExportTask(ctx, task.ID.Int64(), map[string]any{
			"progress": progress, "total_rows": total, "updated_by": p.Actor.UserID,
		}); uerr != nil {
			s.log.Warn("datax: 导出进度回写失败（不阻断导出）",
				zap.String("export_no", task.ExportNo), zap.Error(uerr))
		}
		if next.Done {
			break
		}
		if next.Value == cursor.Value {
			wb.Abort()
			return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, total, written, 0,
				"行源游标未推进（契约违约）")
		}
		cursor = next
		batchNo++
		if batchNo > 1_000_000 { // 理论不可达：游标推进守卫之外的双保险
			wb.Abort()
			return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, total, written, 0,
				"导出批次超上限，已终止")
		}
	}

	// 序列化流式落盘（excel §3：不整簿驻留内存——xlsx 经 WriteTo 逐部件写入临时文件，
	// 大小上限按落盘文件 Stat 校验；先物化 200MB 内存再校验的路径已废除，并发导出
	// 内存不随簿大小成倍放大）。
	tmp, err := os.CreateTemp("", "sf-export-*.xlsx")
	if err != nil {
		return fmt.Errorf("datax: 创建导出临时文件失败（export_no=%s）: %w", task.ExportNo, err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := wb.FinalizeTo(tmp); err != nil {
		cleanupTmp()
		return fmt.Errorf("datax: 导出文件生成失败（export_no=%s）: %w", task.ExportNo, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("datax: 关闭导出临时文件失败（export_no=%s）: %w", task.ExportNo, err)
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("datax: 导出临时文件读取失败（export_no=%s）: %w", task.ExportNo, err)
	}
	if info.Size() > ExportMaxFileBytes {
		_ = os.Remove(tmpPath)
		return s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed, total, written, 0,
			fmt.Sprintf("导出文件超出大小上限（%d 字节）", ExportMaxFileBytes))
	}

	fileName := ModuleLabel(task.Module) + "导出-" + task.ExportNo + ".xlsx"
	fileSrc, err := os.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("datax: 导出临时文件打开失败（export_no=%s）: %w", task.ExportNo, err)
	}
	stored, err := s.store.Save(fileSrc, fileName, ExportMaxFileBytes)
	_ = fileSrc.Close()
	_ = os.Remove(tmpPath) // 转存完成/失败均清理临时文件（store.Save 已原子落盘自有副本）
	if err != nil {
		return err
	}
	return s.completeExport(ctx, p.Actor, task, stored, fileName, total, written)
}

// FinalizeExportFailure 失败终态回调（asynqx.FailureFinalizer，service_queue.go 注册）：
// 基础设施失败重试耗尽（asynq）/inline 无重试环境时，任务行落终态 FAILED + error_message
// 并发站内告警（plan §4.2"重试耗尽 → 终态 FAILED + error_message + 站内告警"）——否则
// 任务永久卡 PROCESSING/QUEUED，用户侧永远显示"处理中"。幂等：finalizeExportStatus
// 守卫 QUEUED/PROCESSING，已终态静默。
func (s *Service) FinalizeExportFailure(ctx context.Context, t asynqx.Task, cause error) error {
	var p exportRunPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ExportNo == "" {
		return fmt.Errorf("datax: 导出失败终态回调载荷非法（task_id=%s）: %w", t.TaskID, err)
	}
	task, err := s.repo.FindExportTaskByNo(ctx, p.ExportNo)
	if err != nil {
		return fmt.Errorf("datax: 导出失败终态回调装载任务失败（export_no=%s）: %w", p.ExportNo, err)
	}
	message := fmt.Sprintf("导出执行失败（重试耗尽或无重试环境，已终止）：%s", truncateMessage(cause.Error(), 500))
	if err := s.finalizeExportStatus(ctx, p.Actor, task.ID.Int64(), TaskStatusFailed,
		task.TotalRows, 0, 0, message); err != nil {
		return err
	}
	if err := sysops.NotifyTaskFailure(ctx, s.repo.DB(), asynqx.TaskTypeExportRun, p.ExportNo, message); err != nil {
		s.log.Error("datax: 导出失败告警写入失败", zap.String("export_no", p.ExportNo), zap.Error(err))
	}
	return nil
}

// completeExport 产物登记 + 终态回填（files 行与任务守卫更新同事务；plan §6.3）。
func (s *Service) completeExport(ctx context.Context, actor Actor, task *ExportTask, stored storage.StoredFile, fileName string, total, written int64) error {
	now := time.Now()
	err := s.inTx(ctx, func(tx *gorm.DB) error {
		file := &storage.File{
			FileName:     fileName,
			StoredName:   stored.StoredName,
			StoragePath:  stored.StoragePath,
			MimeType:     stored.MimeType,
			FileType:     stored.FileType,
			SizeBytes:    stored.Size,
			Module:       "datax",
			BusinessNo:   task.ExportNo,
			UploaderID:   actor.UserID,
			UploaderName: actor.Username,
			ExpiresAt:    s.expiry(),
			CreatedBy:    actor.UserID,
		}
		if err := s.repo.InsertFile(tx, file); err != nil {
			return err
		}
		n, err := s.repo.GuardUpdateExportTask(tx, task.ID.Int64(), []string{TaskStatusQueued, TaskStatusProcessing}, map[string]any{
			"status": TaskStatusSuccess, "progress": 100, "total_rows": total,
			"file_id": file.ID, "finished_at": now, "updated_at": database.Now(), "updated_by": actor.UserID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil // 已终态（重投递竞态）：物理文件暂留，由 file_cleanup 按过期回收
		}
		e := actor.auditEntry("export_task", task.ID.Int64(), "finish")
		e.Success = true
		e.After = map[string]any{"export_no": task.ExportNo, "total_rows": total, "file_id": file.ID}
		return s.audit(tx, e)
	})
	if err != nil {
		_ = s.store.Remove(stored.StoragePath)
	}
	return err
}

// finalizeExportStatus 导出任务终态回填（守卫 QUEUED/PROCESSING → status + finished_at + 审计；
// 幂等：0 行 = 已终态，静默返回）。
func (s *Service) finalizeExportStatus(ctx context.Context, actor Actor, taskID int64, status string, total, written, fileID int64, message string) error {
	now := time.Now()
	return s.inTx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.GuardUpdateExportTask(tx, taskID, []string{TaskStatusQueued, TaskStatusProcessing}, map[string]any{
			"status": status, "total_rows": total, "file_id": fileID,
			"error_message": message, "finished_at": now, "updated_at": database.Now(), "updated_by": actor.UserID,
		})
		if err != nil || n == 0 {
			return err
		}
		e := actor.auditEntry("export_task", taskID, "finish")
		e.Success = status == TaskStatusSuccess
		if !e.Success {
			e.ErrorCode = "DATAX_EXPORT_FAILED"
		}
		e.After = map[string]any{"status": status, "total_rows": total, "written": written, "message": message}
		return s.audit(tx, e)
	})
}

// filterFromParams 任务参数 → ExportFilter（创建时点冻结的数据权限快照原样还原）。
func (s *Service) filterFromParams(t *ExportTask) (ExportFilter, error) {
	f := ExportFilter{Scope: t.Scope}
	var params struct {
		IDs           []int64           `json:"ids"`
		Page          int               `json:"page"`
		PageSize      int               `json:"page_size"`
		Filters       map[string]string `json:"filters"`
		TimeFrom      string            `json:"time_from"`
		TimeTo        string            `json:"time_to"`
		AllWarehouses bool              `json:"all_warehouses"`
		WarehouseIDs  []int64           `json:"warehouse_ids"`
	}
	raw, err := json.Marshal(map[string]any(t.Params))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return f, err
	}
	f.IDs = params.IDs
	f.Page = params.Page
	f.PageSize = params.PageSize
	f.Filters = params.Filters
	f.AllWarehouses = params.AllWarehouses
	f.WarehouseIDs = params.WarehouseIDs
	f.BatchSize = s.cfg.ExportBatchSize
	if params.TimeFrom != "" {
		if tf, ok := parseTimeParam(params.TimeFrom); ok {
			f.TimeFrom = &tf
		}
	}
	if params.TimeTo != "" {
		if tt, ok := parseTimeParam(params.TimeTo); ok {
			// 区间含边界：纯日期结束值补足到当日 23:59:59（TIME_RANGE 语义）。
			if len(params.TimeTo) <= len(dateLayoutOnly) {
				tt = tt.AddDate(0, 0, 1).Add(-time.Second)
			}
			f.TimeTo = &tt
		}
	}
	return f, nil
}

// describeExportCondition 查询条件人读描述（excel §2.3 meta 区落位）。
func describeExportCondition(scope string, f ExportFilter) string {
	switch scope {
	case "SELECTED":
		return fmt.Sprintf("选中 %d 条记录", len(f.IDs))
	case "CURRENT_PAGE":
		return fmt.Sprintf("第 %d 页，每页 %d 行", f.Page, f.PageSize)
	case "TIME_RANGE":
		from, to := "", ""
		if f.TimeFrom != nil {
			from = f.TimeFrom.Format(dateLayout)
		}
		if f.TimeTo != nil {
			to = f.TimeTo.Format(dateLayout)
		}
		return "创建时间 " + from + " 至 " + to
	case "BY_FILTER":
		keys := make([]string, 0, len(f.Filters))
		for k, v := range f.Filters {
			keys = append(keys, k+"="+v)
		}
		if len(keys) == 0 {
			return "筛选条件（无）"
		}
		return "筛选：" + strings.Join(keys, ", ")
	default:
		return "全部数据"
	}
}
