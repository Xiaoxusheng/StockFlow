package printing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/batchresult"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/sysops"
)

// 打印任务业务（printing.md §1.2/§3，plan §7.2）：
//   - 创建即同步装配（ContentReader 窄接口按 object_type 分派），print_tasks 落库
//     （print_no=PT 前缀、template_snapshot 冻结）+ print_task_rows 渲染数据包；
//   - 入队 printing:task:render（仅异步预生成条码 PNG 至进程内缓存，降低大批量
//     首次预览延迟；不重复装配，PNG 不落 files 表——plan §4.2/§7.2）；
//   - 状态机 QUEUED→PROCESSING→SUCCESS/FAILED（render 队列态；打印执行本身由前端
//     渲染层完成，执行确认经 ExecuteTask 回填 result 一次——plan §13.2）。

// TaskCreateInput 任务创建入参（前端 PrintTaskCreatePayload 同构；data_ids 为
// 打印对象业务 ID/码值原文，每条生成一行内容）。
type TaskCreateInput struct {
	TemplateID int64    `json:"template_id"`
	DataIDs    []string `json:"data_ids"`
	Copies     int      `json:"copies"`
}

// ExecuteInput 执行确认入参（plan §7.2：{result: SUCCESS|FAILED, message?}）。
type ExecuteInput struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// renderPayload render 任务 payload（TaskID=print_no；payload 为业务语义真相源，
// asynqx.Task.TaskID 兼作 asynq 去重键——plan §4.1）。
type renderPayload struct {
	PrintNo string `json:"print_no"`
}

// CreateTask 创建打印任务（批量结果演进——效率层一期计划 §2.7：响应为批量结果形态
// 200 + batchresult.Result（internal/batchresult 共享包，集成收口收敛，JSON 契约
// 零变化），整请求参数错误仍 400；部分对象不可打印（停用 SKU/数据缺失）
// 逐条 failed(reason=既有错误码)，其余对象正常建任务——409+details.disabled_ids
// 整体拒绝语义废止，验收场景 4（100→96/3/1）硬前提）。
//
// 单据打印 = 单 ID，批量打印 = 多 ID（printing.md §3）；按筛选打印不交付：BY_FILTER
// 属 datax 导出范围值，plan §7.2 冻结创建入参仅 data_ids，波次级全量单据打印由前端
// 按上限分片，plan §18.4。
func (s *Service) CreateTask(ctx context.Context, actor Actor, in TaskCreateInput) (*batchresult.Result, error) {
	if in.TemplateID <= 0 {
		return nil, paramError("template_id", "必须为正整数")
	}
	if len(in.DataIDs) == 0 {
		return nil, paramError("data_ids", "不能为空")
	}
	if len(in.DataIDs) > MaxDataIDs {
		return nil, response.NewError(ErrTooManyDataIDs, map[string]any{
			"limit": MaxDataIDs, "actual": len(in.DataIDs),
		})
	}
	copies := in.Copies
	if copies == 0 {
		copies = MinCopies
	}
	if copies < MinCopies || copies > MaxCopies {
		return nil, response.NewError(ErrCopiesInvalid, map[string]any{
			"min": MinCopies, "max": MaxCopies, "actual": in.Copies,
		})
	}
	for _, id := range in.DataIDs {
		if id == "" || len(id) > MaxDataIDLen {
			return nil, response.NewError(ErrDataIDsInvalid, map[string]any{
				"reason": "标识不能为空且长度不能超过 " + fmt.Sprint(MaxDataIDLen), "max_len": MaxDataIDLen,
			})
		}
	}

	tpl, err := s.loadTemplateOrError(ctx, in.TemplateID)
	if err != nil {
		return nil, err
	}
	if tpl.Status != StatusEnabled {
		// printing.md §2：停用即不可被新任务选用。
		return nil, response.NewError(ErrTemplateDisabled, map[string]any{
			"template_id": tpl.ID.Int64(), "status": tpl.Status,
		})
	}

	reader, ok := s.readerFor(tpl.ObjectType)
	if !ok {
		// RegisterRoutes 启动期已 fail-fast 全量 reader；运行期缺位=装配被绕过（编程错误）。
		return nil, response.NewError(ErrReaderRequired, map[string]any{
			"object_type": tpl.ObjectType, "reason": "装配 reader 未注入（plan §3.1 规则①）",
		})
	}
	// 装配在任务创建时同步完成（plan §7.2）：按模板 fields 绑定键装配（预设键序，
	// 确定性输入支撑装配纯函数幂等）。
	fields := orderedFieldList(tpl.ObjectType, tpl.FieldMap())
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.Key)
	}

	// 请求内去重：重复 data_id → skipped（同批重复对象已在任务中=已处目标态）。
	// firstIdx 记录首次出现下标——结果循环按「是否首次出现」区分 success/skipped。
	firstIdx := make(map[string]int, len(in.DataIDs))
	unique := make([]string, 0, len(in.DataIDs))
	for i, id := range in.DataIDs {
		if _, dup := firstIdx[id]; dup {
			continue
		}
		firstIdx[id] = i
		unique = append(unique, id)
	}

	// 逐对象装配：不可打印对象逐条 failed，其余对象继续建任务（§2.7）。
	rowsByID, failReason, err := s.assembleBatch(ctx, reader, unique, keys)
	if err != nil {
		return nil, err
	}

	items := make([]batchresult.Item, 0, len(in.DataIDs)) // 请求序逐条结果
	rows := make([]ContentRow, 0, len(rowsByID))          // 可打印行（请求序）
	for i, id := range in.DataIDs {
		item := batchresult.Item{ID: id}
		switch {
		case firstIdx[id] != i:
			// 非首次出现 → skipped（携 DUPLICATE_DATA_ID reason——api.md §9 披露形态；
			// Builder.Skipped 语义冻结 reason 恒空，故直接构造 Item）。
			item.Status, item.Reason = batchresult.StatusSkipped, ReasonDuplicateDataID
		default:
			if row, ok := rowsByID[id]; ok {
				item.Status = batchresult.StatusSuccess
				rows = append(rows, row)
			} else {
				item.Status, item.Reason = batchresult.StatusFailed, failReason[id]
			}
		}
		items = append(items, item)
	}
	// 计数收敛入共享包（total=success+failed+skipped 逐条对账，不手工维护计数）。
	res := batchresult.SummaryOf(items)
	if len(rows) == 0 {
		// 无可打印对象：不建任务不入队，逐条结果如实返回（全 failed，仍 200）。
		return &res, nil
	}

	rule, err := docRule()
	if err != nil {
		return nil, err
	}
	task := &PrintTask{}
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		// 单号发放与任务落库同事务（business-flow §13.1；uk_print_tasks_no 冲突
		// 自动取号重试——docnum.NextWithRetry）。
		_, err := docnum.NextWithRetry(ctx, tx, rule, func(tx *gorm.DB, no string) error {
			task = &PrintTask{
				PrintNo:          no,
				ObjectType:       tpl.ObjectType,
				TemplateID:       tpl.ID.Int64(),
				TemplateSnapshot: snapshotJSONB(SnapshotFromTemplate(tpl)),
				Paper:            tpl.Paper,
				Copies:           copies,
				TotalCount:       len(rows),
				Status:           TaskStatusQueued,
				CreatedBy:        actor.UserID,
				UpdatedBy:        actor.UserID,
			}
			if err := s.repo.InsertTask(tx, task); err != nil {
				return err
			}
			return s.repo.InsertTaskRows(tx, taskRows(task.ID.Int64(), rows, actor.UserID))
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	// 入队 render（事务提交后——inline 模式 handler 立即执行，须能读到已提交行）。
	if s.opt.queue == nil {
		return nil, response.NewError(ErrQueueRequired, map[string]any{
			"reason": "router 未注入 WithQueue（plan §3.1 规则①）",
		})
	}
	payload, err := json.Marshal(renderPayload{PrintNo: task.PrintNo})
	if err != nil {
		return nil, err
	}
	err = s.opt.queue.Enqueue(ctx, asynqx.Task{
		Type:    asynqx.TaskTypePrintRender,
		Payload: payload,
		TaskID:  task.PrintNo,
	})
	if err != nil {
		// 入队失败（如 Redis 故障）：任务行已提交，请求不回滚。降级为进程内同步
		// 执行同一 handler（状态守卫幂等，天然可重入）——杜绝任务悬挂 QUEUED；
		// 同步执行仍失败时 handler 自身已落 FAILED 终态。
		s.logWarn("打印任务入队失败，降级进程内同步 render",
			zap.String("print_no", task.PrintNo), zap.Error(err))
		_ = s.HandleRenderTask(ctx, asynqx.Task{Type: asynqx.TaskTypePrintRender, Payload: payload, TaskID: task.PrintNo})
	}

	return &res, nil
}

// assembleBatch 逐对象装配（§2.7 批量演进）。reader 契约为整体成功或整体报错——
// 报错时提取 details 中不可打印对象明细（PRINT_SKU_DISABLED.disabled_ids /
// PRINT_DATA_NOT_FOUND.missing_ids）标记逐条失败，剔除后对剩余对象重试装配，
// 直至成功或无进展；非对象级错误（reader 契约违约/基础设施）整体透传。
func (s *Service) assembleBatch(ctx context.Context, reader ContentReader, ids, keys []string) (map[string]ContentRow, map[string]string, error) {
	rowsByID := make(map[string]ContentRow, len(ids))
	failed := make(map[string]string)
	pending := append([]string(nil), ids...)
	for len(pending) > 0 {
		rows, err := reader.Assemble(ctx, pending, keys)
		if err == nil {
			for i, id := range pending {
				rowsByID[id] = rows[i] // reader 契约：行序与入参一致
			}
			return rowsByID, failed, nil
		}
		reason, bad, ok := batchFailureDetail(err)
		if !ok {
			return nil, nil, err
		}
		next := make([]string, 0, len(pending))
		progressed := false
		for _, id := range pending {
			if _, isBad := bad[id]; isBad {
				if _, marked := failed[id]; !marked {
					failed[id] = reason
					progressed = true
				}
				continue
			}
			next = append(next, id)
		}
		if !progressed {
			// reader 未点名任何对象（防御死循环），整体透传原始错误。
			return nil, nil, err
		}
		pending = next
	}
	return rowsByID, failed, nil
}

// batchFailureDetail 装配错误 → 逐条失败明细（reason 错误码 + 不可打印对象集）；
// 非对象级错误返回 ok=false（整体透传）。
func batchFailureDetail(err error) (string, map[string]bool, bool) {
	var re *response.Error
	if !errors.As(err, &re) {
		return "", nil, false
	}
	details, ok := re.Details.(map[string]any)
	if !ok {
		return "", nil, false
	}
	for _, kv := range []struct {
		key    string
		reason string
	}{
		{"disabled_ids", "PRINT_SKU_DISABLED"},
		{"missing_ids", "PRINT_DATA_NOT_FOUND"},
	} {
		if raw, ok := details[kv.key].([]string); ok && len(raw) > 0 {
			set := make(map[string]bool, len(raw))
			for _, id := range raw {
				set[id] = true
			}
			return kv.reason, set, true
		}
	}
	return "", nil, false
}

// taskRows 装配结果 → 渲染数据包行（seq 1 起与入参序一致；values/lines 快照冻结；
// DataID=行对象业务身份十进制文本（迁移 000019，qr-code.md §7.4）——重打唯一取数
// 依据，修复 ContentRow.ID 在落库时丢失的建模缺口；旧行 data_id 为 NULL → 空串）。
func taskRows(taskID int64, rows []ContentRow, by int64) []*PrintTaskRow {
	out := make([]*PrintTaskRow, 0, len(rows))
	for i, r := range rows {
		out = append(out, &PrintTaskRow{
			TaskID:    taskID,
			Seq:       i + 1,
			Code:      r.Code,
			Values:    marshalJSONB(r.Values),
			Lines:     marshalJSONB(r.Lines),
			DataID:    r.ID,
			CreatedBy: by,
			UpdatedBy: by,
		})
	}
	return out
}

// GetTask 任务详情（渲染数据包：详情含 rows + 模板快照，前端 react-to-print 消费；
// 渲染数据 = 占位符替换后的结构化数据——字段绑定键到真实业务取值的映射，不做
// 服务端 PDF，Orchestrator 裁决③）。
func (s *Service) GetTask(ctx context.Context, id int64) (*TaskView, error) {
	task, err := s.loadTaskOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.taskDetail(ctx, task)
}

// taskDetail 装配详情视图（列表/历史不携带 rows——前端先行契约 omitempty 口径）。
func (s *Service) taskDetail(ctx context.Context, task *PrintTask) (*TaskView, error) {
	rows, err := s.repo.ListTaskRows(ctx, task.ID.Int64())
	if err != nil {
		return nil, err
	}
	v := s.taskView(task, true)
	for _, r := range rows {
		v.Rows = append(v.Rows, rowView(r))
	}
	return &v, nil
}

// ListTasks 任务列表（强制分页；支持 id 精确过滤——前端预览页回对）。
func (s *Service) ListTasks(ctx context.Context, f TaskFilter) ([]TaskView, int64, error) {
	if f.Page <= 0 || f.PageSize <= 0 {
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "page", "reason": "分页参数缺失（列表接口强制分页）",
		})
	}
	if f.ObjectType != "" && !ObjectTypeValid(f.ObjectType) {
		return nil, 0, response.NewError(ErrObjectTypeInvalid, map[string]any{"object_type": f.ObjectType})
	}
	switch f.Status {
	case "", TaskStatusQueued, TaskStatusProcessing, TaskStatusSuccess, TaskStatusFailed:
	default:
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "status", "reason": "必须为 QUEUED/PROCESSING/SUCCESS/FAILED",
		})
	}
	switch f.Result {
	case "", ResultSuccess, ResultFailed:
	default:
		return nil, 0, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "result", "reason": "必须为 SUCCESS 或 FAILED",
		})
	}
	rows, total, err := s.repo.ListTasks(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]TaskView, 0, len(rows))
	for _, t := range rows {
		items = append(items, s.taskView(t, false))
	}
	return items, total, nil
}

// ExecuteTask 打印执行确认（plan §7.2 M3 端点：回填 printed_by/printed_at/result +
// operation_logs——printing.md §1.2/§6 打印日志：谁、何时、用什么模板、打了什么）。
// 回填一次守卫（plan §13.2）：result IS NULL → 命中；0 行=已确认 409。
func (s *Service) ExecuteTask(ctx context.Context, actor Actor, id int64, in ExecuteInput) (*TaskView, error) {
	if in.Result != ResultSuccess && in.Result != ResultFailed {
		return nil, response.NewError(ErrResultInvalid, map[string]any{
			"field": "result", "reason": "必须为 SUCCESS 或 FAILED",
		})
	}
	task, err := s.loadTaskOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	err = withTx(ctx, s.repo, func(tx *gorm.DB) error {
		n, err := s.repo.UpdateExecuteResult(tx, id, in.Result, in.Message, actor.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrAlreadyConfirmed, map[string]any{
				"task_id": id, "print_no": task.PrintNo, "reason": "打印结果已确认，不能重复回填",
			})
		}
		return middleware.Audit(tx, auditWithSnapshots(
			actor.auditEntry("print_task", id, "execute"), in, nil, map[string]any{
				"print_no": task.PrintNo, "object_type": task.ObjectType,
				"template_id": task.TemplateID, "total_count": task.TotalCount,
				"result": in.Result,
			}))
	})
	if err != nil {
		return nil, err
	}
	after, err := s.loadTaskOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	v := s.taskView(after, false)
	return &v, nil
}

// ListHistory 打印历史（printing.md §1.2：打印人/打印时间/模板/数据量/打印结果；
// = print_tasks 已确认子集，plan §7.2；权限复用 printing:task:list）。
func (s *Service) ListHistory(ctx context.Context, f TaskFilter) ([]HistoryItem, int64, error) {
	f.ConfirmedOnly = true
	rows, total, err := s.ListTasks(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]HistoryItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, HistoryItem{
			ID:           t.ID,
			PrintNo:      t.PrintNo,
			ObjectType:   t.ObjectType,
			TemplateID:   t.TemplateID,
			TemplateName: t.TemplateName,
			PrintedBy:    t.PrintedBy,
			PrintedAt:    t.PrintedAt,
			TotalCount:   t.TotalCount,
			Result:       t.Result,
			ErrorMessage: t.ErrorMessage,
		})
	}
	return items, total, nil
}

// loadTaskOrError 装载任务或返回 404。
func (s *Service) loadTaskOrError(ctx context.Context, id int64) (*PrintTask, error) {
	t, err := s.repo.FindTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrTaskNotFound, map[string]any{"task_id": id})
	}
	return t, nil
}

// logWarn 域内 warn 日志（logger 未注入时降级 nop——单测场景）。
func (s *Service) logWarn(msg string, fields ...zap.Field) {
	if s.opt.log != nil {
		s.opt.log.Warn(msg, fields...)
	}
}

// ---- asynqx render handler（plan §4.2：printing:task:render）----

// HandleRenderTask 渲染 handler：条码 PNG 预生成与进程内缓存（plan §4.2/§7.2）。
// 装配已在任务创建时同步完成，本 handler 不重复装配，仅按模板快照逐行预生成
// 主码/二维码 PNG。
//
// 幂等与状态机（plan §4.2 handler 状态守卫、§13.2）：
//   - 进入即守卫 QUEUED→PROCESSING（0 行 = SUCCESS/FAILED 终态或并发，静默丢弃重投递）；
//   - 预生成成功 → PROCESSING→SUCCESS；0 行 = 已被并发处理，静默；
//   - 失败路径：装配读取/落库等基础设施错误返回 error（asynq 从 QUEUED 断点重试）；
//     预生成阶段错误为纯函数失败（重试无增益）→ fail-fast 落 FAILED + error_message
//     终态并返回 nil（对 §4.2"重试耗尽终态"的本域适配——终态即刻达成，不空耗重试；
//     单行内容与码制不符仅跳过该行预生成，前端渲染层按 row.code 客户端降级渲染，
//     不影响任务终态）。
func (s *Service) HandleRenderTask(ctx context.Context, t asynqx.Task) error {
	if t.Type != asynqx.TaskTypePrintRender {
		return fmt.Errorf("printing: render handler 收到非本域任务类型 %q（编程错误）", t.Type)
	}
	var p renderPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.PrintNo == "" {
		return fmt.Errorf("printing: render payload 非法（print_no 缺失）: %w", err)
	}

	// 1. 状态守卫（plan §4.2：进入即以任务行状态守卫确认执行权）。
	db := s.repo.DB()
	n, err := s.repo.UpdateTaskStatus(db, p.PrintNo, []string{TaskStatusQueued}, TaskStatusProcessing, "")
	if err != nil {
		return err // 基础设施错误：状态未变，asynq 重试可从 QUEUED 重入
	}
	if n == 0 {
		return nil // 已被处理（终态）——静默丢弃重投递
	}

	// 2. 装载任务与渲染数据包。
	task, err := s.repo.FindTaskByNo(ctx, p.PrintNo)
	if err != nil {
		return s.renderFail(db, p.PrintNo, err)
	}
	if task == nil {
		return s.renderFail(db, p.PrintNo, fmt.Errorf("print task %s 不存在", p.PrintNo))
	}
	rows, err := s.repo.ListTaskRows(ctx, task.ID.Int64())
	if err != nil {
		return s.renderFail(db, p.PrintNo, err)
	}

	// 3. 逐行预生成（快照冻结的 symbology/qrcode_enabled；单行失败跳过——
	//    PNG 缓存为预览延迟优化，缺行由前端客户端渲染兜底）。
	snap := task.Snapshot()
	for _, r := range rows {
		if snap.BarcodeSymbology != "" {
			if _, err := s.RenderBarcodePNG(r.Code, snap.BarcodeSymbology, DefaultBarcodeWidth, DefaultBarcodeHeight); err != nil {
				s.logWarn("打印条码预生成跳过（内容与码制不符或尺寸越界）",
					zap.String("print_no", p.PrintNo), zap.Int("seq", r.Seq), zap.Error(err))
			}
		}
		if snap.QRCodeEnabled {
			if _, err := s.RenderBarcodePNG(r.Code, SymbologyQR, DefaultQRSize, DefaultQRSize); err != nil {
				s.logWarn("打印二维码预生成跳过",
					zap.String("print_no", p.PrintNo), zap.Int("seq", r.Seq), zap.Error(err))
			}
		}
	}

	// 4. 终态 SUCCESS（0 行 = 已被并发处理，静默）。
	if _, err := s.repo.UpdateTaskStatus(db, p.PrintNo, []string{TaskStatusProcessing}, TaskStatusSuccess, ""); err != nil {
		return err
	}
	return nil
}

// renderFail 预生成阶段失败：fail-fast 落 FAILED 终态（纯函数失败重试无增益）。
func (s *Service) renderFail(db *gorm.DB, printNo string, cause error) error {
	msg := truncateMessage(cause.Error(), 512)
	if _, err := s.repo.UpdateTaskStatus(db, printNo, []string{TaskStatusProcessing}, TaskStatusFailed, msg); err != nil {
		return err
	}
	return nil
}

// FinalizeRenderFailure 失败终态回调（asynqx.FailureFinalizer，handler.go 注册）：
// 基础设施失败重试耗尽（asynq）/inline 无重试环境时，print_tasks 行落 FAILED 终态
// （plan §4.2"重试耗尽 → 终态 FAILED + error_message + 站内告警"——否则任务永久卡
// PROCESSING/QUEUED）。幂等：守卫 PROCESSING/QUEUED 前置态，已终态 0 行静默。
// 渲染数据装配已在创建时同步完成，PNG 缓存仅预览优化——终态 FAILED 不影响前端
// 客户端降级渲染。
func (s *Service) FinalizeRenderFailure(ctx context.Context, t asynqx.Task, cause error) error {
	if t.Type != asynqx.TaskTypePrintRender {
		return fmt.Errorf("printing: 失败终态回调收到非本域任务类型 %q（编程错误）", t.Type)
	}
	var p renderPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.PrintNo == "" {
		return fmt.Errorf("printing: 失败终态回调载荷非法（task_id=%s）: %w", t.TaskID, err)
	}
	db := s.repo.DB()
	msg := truncateMessage(cause.Error(), 512)
	for _, from := range []string{TaskStatusProcessing, TaskStatusQueued} {
		if _, err := s.repo.UpdateTaskStatus(db, p.PrintNo, []string{from}, TaskStatusFailed, msg); err != nil {
			return err
		}
	}
	if err := sysops.NotifyTaskFailure(ctx, db, asynqx.TaskTypePrintRender, p.PrintNo, msg); err != nil {
		s.logWarn("打印任务失败告警写入失败", zap.String("print_no", p.PrintNo), zap.Error(err))
	}
	return nil
}

// truncateMessage 消息截断（error_message 列宽对齐；按字节限长但回退到完整 UTF-8
// rune 边界，避免切断多字节字符产生非法序列）。
func truncateMessage(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s[:max])
	for len(b) > 0 && !utf8.RuneStart(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	if len(b) > 0 {
		if r, size := utf8.DecodeLastRune(b); r == utf8.RuneError && size == 1 {
			b = b[:len(b)-1]
		}
	}
	return string(b) + "…"
}
