package datax

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
	"go.uber.org/zap"
)

// 导出中心（excel §2/§3；plan §6.3）：创建异步导出任务（QUEUED）→ asynqx 执行
// （service_export_run.go：Count → meta 区 → StreamWriter keyset 游标分批 → 合计行 →
// files 登记）→ 终态下载。导出属敏感操作（permission §6）：创建与下载均记审计。

// ExportCreateInput 创建导出任务入参（scope 五值 excel §2.2；params 冻结键：
// ids/page/page_size/filters/time_from/time_to——TIME_RANGE 标准键 plan §6.3）。
type ExportCreateInput struct {
	Module   string            `json:"module"`
	Scope    string            `json:"scope"`
	IDs      []string          `json:"ids,omitempty"`
	Page     int               `json:"page,omitempty"`
	PageSize int               `json:"page_size,omitempty"`
	Filters  map[string]string `json:"filters,omitempty"`
	TimeFrom string            `json:"time_from,omitempty"`
	TimeTo   string            `json:"time_to,omitempty"`
}

// SELECTED 导出选中 ID 上限（plan §6.3：ids≤1000，防失控任务）。
const maxSelectedIDs = 1000

// CURRENT_PAGE 单页导出行数上限（与列表 pageSize 同量级，防变形全量导出绕过分页）。
const maxCurrentPageRows = 100

// exportScopes 冻结范围值集（000011 CHECK 同源）。
var exportScopes = map[string]bool{
	"CURRENT_PAGE": true, "SELECTED": true, "ALL": true, "BY_FILTER": true, "TIME_RANGE": true,
}

// CreateExport 创建导出任务（plan §6.3）：模块/范围/参数校验 → export_no（EXP 前缀）→
// status=QUEUED → 入队 datax:export:run。仓库范围数据权限随任务快照入 params
// （执行器原样还原 ExportFilter——permission.md §4 数据权限在创建时点冻结）。
func (s *Service) CreateExport(ctx context.Context, actor Actor, in ExportCreateInput, allWarehouses bool, warehouseIDs []int64) (*ListTaskItem, error) {
	if !IsValidExportModule(in.Module) {
		return nil, response.NewError(ErrModuleInvalid, map[string]any{"module": in.Module})
	}
	// 行源缺位（REPORT 归 MT4）fail-closed 拒绝——不出假任务。
	if _, err := s.sourceFor(in.Module); err != nil {
		return nil, err
	}
	if !exportScopes[in.Scope] {
		return nil, response.NewError(ErrScopeInvalid, map[string]any{"scope": in.Scope})
	}

	params := map[string]any{}
	switch in.Scope {
	case "SELECTED":
		if len(in.IDs) == 0 {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{"field": "ids", "reason": "SELECTED 必须携带选中记录 ID"})
		}
		if len(in.IDs) > maxSelectedIDs {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{"field": "ids", "reason": "选中记录超过 " + strconv.Itoa(maxSelectedIDs) + " 条上限"})
		}
		ids := make([]int64, 0, len(in.IDs))
		for _, raw := range in.IDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return nil, response.NewError(ErrScopeInvalid, map[string]any{"field": "ids", "reason": "ID 必须为正整数字符串"})
			}
			ids = append(ids, id)
		}
		params["ids"] = ids
	case "CURRENT_PAGE":
		if in.Page < 1 {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{"field": "page", "reason": "必须 >= 1"})
		}
		if in.PageSize < 1 || in.PageSize > maxCurrentPageRows {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{
				"field": "page_size", "reason": "必须为 1-" + strconv.Itoa(maxCurrentPageRows),
			})
		}
		params["page"] = in.Page
		params["page_size"] = in.PageSize
	case "TIME_RANGE":
		tf, ok1 := parseTimeParam(in.TimeFrom)
		tt, ok2 := parseTimeParam(in.TimeTo)
		if !ok1 || !ok2 {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{
				"field": "time_from/time_to", "reason": "TIME_RANGE 必须携带 YYYY-MM-DD HH:mm:ss 时间区间",
			})
		}
		if tf.After(tt) {
			return nil, response.NewError(ErrScopeInvalid, map[string]any{"reason": "开始时间不能晚于结束时间"})
		}
		params["time_from"] = in.TimeFrom
		params["time_to"] = in.TimeTo
	case "BY_FILTER":
		// filters 键白名单在行源实现内执行（plan §18.5：未知键忽略，防任意键注入）。
		params["filters"] = in.Filters
	}
	// 数据权限快照（auth.WarehouseScope 由 handler 注入；plan §13.6）。
	params["all_warehouses"] = allWarehouses
	if len(warehouseIDs) > 0 {
		params["warehouse_ids"] = warehouseIDs
	}

	task := &ExportTask{
		Module:    in.Module,
		Scope:     in.Scope,
		Status:    TaskStatusQueued,
		Params:    JSONMap(params),
		CreatedBy: actor.UserID,
		UpdatedBy: actor.UserID,
	}
	err := s.inTx(ctx, func(tx *gorm.DB) error {
		if err := s.repo.InsertExportTask(ctx, tx, task); err != nil {
			return err
		}
		e := actor.auditEntry("export_task", task.ID.Int64(), "create")
		e.Success = true
		e.Request = map[string]any{"module": in.Module, "scope": in.Scope, "params": params}
		e.After = map[string]any{"export_no": task.ExportNo, "status": task.Status}
		return s.audit(tx, e)
	})
	if err != nil {
		return nil, err
	}

	// 入队（TaskID=EXP- 单号；inline 模式同步执行——创建响应即含终态，plan §4.1）。
	payload, _ := json.Marshal(exportRunPayload{ExportNo: task.ExportNo, Actor: actor})
	if err := s.queue.Enqueue(ctx, asynqx.Task{
		Type:    asynqx.TaskTypeExportRun,
		Payload: payload,
		TaskID:  task.ExportNo,
	}); err != nil {
		// 补偿：QUEUED → FAILED + error_message（导出尚未开始执行，无半成品可保留）。
		if _, uerr := s.repo.GuardUpdateExportTask(s.repo.DB(), task.ID.Int64(), []string{TaskStatusQueued}, map[string]any{
			"status": TaskStatusFailed, "error_message": "任务入队失败", "updated_by": actor.UserID,
		}); uerr != nil {
			s.log.Error("datax: 导出任务入队失败且补偿失败", zap.String("export_no", task.ExportNo), zap.Error(uerr))
		}
		return nil, response.NewError(ErrEnqueueFailed, nil)
	}

	final, err := s.repo.FindExportTask(ctx, task.ID.Int64())
	if err != nil {
		return nil, err
	}
	item := s.exportTaskItem(ctx, final, actorFileScope(actor))
	return &item, nil
}

// parseTimeParam 时间参数解析（api.md §2 统一格式，兼容纯日期）。
func parseTimeParam(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{dateLayout, dateLayoutOnly} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ListExports 导出任务列表（excel §3/§4；筛选 status/module）。scope 为数据权限范围：
// 任务行元数据（状态/行数/创建人）按任务权限点可见，产物联动字段（file_url/file_name/
// 过期时间）按 FileScope 收口——跨仓不可见任务的产物链接不下发（与下载 404 同口径）。
func (s *Service) ListExports(ctx context.Context, f TaskListFilter, scope *FileScope) ([]ListTaskItem, int64, error) {
	f.Page, f.PageSize = normalizePage(f.Page, f.PageSize)
	rows, total, err := s.repo.ListExportTasks(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ListTaskItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, s.exportTaskItem(ctx, t, scope))
	}
	return items, total, nil
}

// exportTaskItem 导出任务 → 列表 DTO（file_url 终态后下发，前端不拼 URL）。产物联动
// 字段按 FileScope 收口（service_file.go fileVisible 冻结规则）：范围受限用户对跨仓
// 不可见产物不下发链接/文件名/过期时间——任务行其余元数据仍按任务权限点可见。
func (s *Service) exportTaskItem(ctx context.Context, t *ExportTask, scope *FileScope) ListTaskItem {
	item := ListTaskItem{
		ID: t.ID, TaskNo: t.ExportNo, TaskType: "EXPORT",
		Module: t.Module, ModuleName: ExportModuleLabel(t.Module), Scope: t.Scope,
		Status: t.Status, Progress: t.Progress,
		TotalRows: t.TotalRows, CreatorID: t.CreatedBy,
		StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
		CreatedAt:    t.CreatedAt.Time,
		CreatedAtStr: t.CreatedAt.Format(dateLayout),
		ErrorMessage: t.ErrorMessage,
	}
	if t.FileID > 0 && isTerminalExport(t.Status) {
		if f, ferr := s.loadFileRow(ctx, t.FileID); ferr == nil && s.fileVisible(ctx, f, scope) {
			item.FileURL = "/api/exports/" + strconv.FormatInt(t.ID.Int64(), 10) + "/file"
			item.FileName = f.FileName
			item.FileExpiredAt = f.ExpiresAt
		}
	}
	return item
}

func isTerminalExport(status string) bool {
	return status == TaskStatusSuccess || status == TaskStatusPartial
}

// GetTask 导入/导出任务详情（数据中心任务卡刷新用；kind=IMPORT/EXPORT）。scope 为
// 数据权限范围：任务产物联动字段按 FileScope 收口（同 exportTaskItem/ListImports 口径）。
func (s *Service) GetTask(ctx context.Context, kind string, id int64, scope *FileScope) (*ListTaskItem, error) {
	switch kind {
	case "IMPORT":
		t, err := s.repo.FindImportTask(ctx, id)
		if err != nil {
			return nil, err
		}
		item := ListTaskItem{
			ID: t.ID, TaskNo: t.ImportNo, TaskType: "IMPORT",
			Module: t.ImportType, ModuleName: ImportTypeLabel(t.ImportType),
			Status:      t.Status,
			Progress:    deriveProgress(int64(t.SuccessRows+t.FailedRows), int64(t.TotalRows)),
			TotalRows:   int64(t.TotalRows),
			SuccessRows: int64(t.SuccessRows), FailedRows: int64(t.FailedRows),
			CreatorID: t.CreatedBy, StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
			CreatedAt: t.CreatedAt.Time, CreatedAtStr: t.CreatedAt.Format(dateLayout),
			ErrorMessage: t.ErrorMessage,
		}
		if t.ErrorFileID > 0 {
			if ef, ferr := s.loadFileRow(ctx, t.ErrorFileID); ferr == nil && s.fileVisible(ctx, ef, scope) {
				item.FileURL = "/api/imports/" + strconv.FormatInt(id, 10) + "/error-file"
				item.FileName = ef.FileName
				item.FileExpiredAt = ef.ExpiresAt
			}
		}
		return &item, nil
	case "EXPORT":
		t, err := s.repo.FindExportTask(ctx, id)
		if err != nil {
			return nil, err
		}
		item := s.exportTaskItem(ctx, t, scope)
		return &item, nil
	default:
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "kind"})
	}
}

// —— 下载/预览的读取准备（handler 以 http.ServeContent 流式响应，不整读内存）——

// serveFile 下载/预览响应集合（登记行 + 打开的物理文件；Body 为 *os.File，天然满足
// ReadSeeker + Closer——http.ServeContent 流式响应，调用方负责 Close）。
type serveFile struct {
	ID       int64
	FileName string
	MimeType string
	ModTime  time.Time
	Body     readSeekCloser
}

// readSeekCloser 下载响应体接口（os.File 满足）。
type readSeekCloser interface {
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
	Close() error
}

// GetExportFile 导出产物下载准备（plan §11.1：datax:export:read；终态前拒绝下载；
// 数据权限 FileScope 规则——跨仓不可见任务产物按不存在处理）。
func (s *Service) GetExportFile(ctx context.Context, id int64, scope *FileScope) (*serveFile, error) {
	task, err := s.repo.FindExportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isTerminalExport(task.Status) || task.FileID <= 0 {
		return nil, response.NewError(ErrExportNotFinished, map[string]any{"status": task.Status})
	}
	f, err := s.loadFileRow(ctx, task.FileID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFileVisible(ctx, f, scope); err != nil {
		return nil, err
	}
	return s.openServe(ctx, task.FileID)
}

// GetImportErrorFile 错误 Excel 下载准备（excel §1.4：修复后重新导入；数据权限
// FileScope 规则——导入任务产物仅创建人及全量范围用户可见）。
func (s *Service) GetImportErrorFile(ctx context.Context, id int64, scope *FileScope) (*serveFile, error) {
	task, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.ErrorFileID <= 0 {
		return nil, response.NewError(ErrFileNotFound, map[string]any{"reason": "该任务无错误明细文件"})
	}
	f, err := s.loadFileRow(ctx, task.ErrorFileID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFileVisible(ctx, f, scope); err != nil {
		return nil, err
	}
	return s.openServe(ctx, task.ErrorFileID)
}

// GetFileDownload 文件中心下载准备（权限点 datax:file:read；过期/已删拒绝，excel §5；
// 数据权限 FileScope 规则，service_file.go fileVisible）。
func (s *Service) GetFileDownload(ctx context.Context, id int64, scope *FileScope) (*serveFile, error) {
	f, err := s.loadFileRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFileVisible(ctx, f, scope); err != nil {
		return nil, err
	}
	return s.openServe(ctx, f.ID)
}

// GetFilePreview 文件预览准备（plan §6.4：仅图片原样流式返回，不做缩略图；数据权限
// FileScope 规则，service_file.go fileVisible）。
func (s *Service) GetFilePreview(ctx context.Context, id int64, scope *FileScope) (*serveFile, error) {
	f, err := s.loadFileRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFileVisible(ctx, f, scope); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(f.MimeType, "image/") {
		return nil, response.NewError(ErrPreviewUnsupported, map[string]any{"mime_type": f.MimeType})
	}
	return s.openServe(ctx, f.ID)
}

// loadFileRow 读取登记行（软删行由 GORM 作用域排除 → NOT FOUND；过期行拒绝访问——
// excel §5：过期自动标记，清理前即不可下载）。
func (s *Service) loadFileRow(ctx context.Context, id int64) (*storage.File, error) {
	f, err := s.repo.FindFile(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.DeletedAt.Valid {
		return nil, response.NewError(ErrFileDeleted, nil)
	}
	if f.ExpiresAt != nil && f.ExpiresAt.Before(time.Now()) {
		return nil, response.NewError(ErrFileExpired, map[string]any{"expires_at": f.ExpiresAt.Format(dateLayout)})
	}
	return f, nil
}

// fileByID 登记行快捷读取（列表 DTO 装配；读不到返回 nil 不阻断列表）。
func (s *Service) fileByID(ctx context.Context, id int64) *fileRecord {
	f, err := s.loadFileRow(ctx, id)
	if err != nil {
		return nil
	}
	return &fileRecord{
		ID: f.ID, FileName: f.FileName, MimeType: f.MimeType, SizeBytes: f.SizeBytes,
		Module: f.Module, BusinessNo: f.BusinessNo, UploaderName: f.UploaderName,
		ExpiresAt: f.ExpiresAt, CreatedAt: f.CreatedAt,
		CreatedAtStr: f.CreatedAt.Format(dateLayout),
	}
}

// fileRecord files 登记行投影（列表 DTO 用；模型留在 storage 层）。
type fileRecord struct {
	ID           int64
	FileName     string
	MimeType     string
	SizeBytes    int64
	Module       string
	BusinessNo   string
	UploaderName string
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	CreatedAtStr string
}

// openServe 按登记行打开物理文件（storage.Open 内含路径收敛防御；流式句柄）。
func (s *Service) openServe(ctx context.Context, fileID int64) (*serveFile, error) {
	f, err := s.loadFileRow(ctx, fileID)
	if err != nil {
		return nil, err
	}
	h, info, err := s.store.Open(f.StoragePath)
	if err != nil {
		return nil, err
	}
	return &serveFile{
		ID: f.ID, FileName: f.FileName, MimeType: f.MimeType,
		ModTime: info.ModTime(), Body: h,
	}, nil
}
