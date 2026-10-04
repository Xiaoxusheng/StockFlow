package datax

import (
	"context"
	"mime/multipart"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
)

// jsonTimePtr *time.Time → database.JSONTime（nil → 零值，JSON 序列化为 null；
// 非零值格式化为 api.md §2 统一时间形态）。
func jsonTimePtr(t *time.Time) database.JSONTime {
	if t == nil {
		return database.JSONTime{}
	}
	return database.JSONTime{Time: *t}
}

// 文件中心（excel §7、api.md §5；plan §6.4）：
//   - 上传：multipart（module 必填、business_no 选填）→ storage.Save 全量安全校验
//     （扩展名白名单 + 嗅探 MIME 交叉 + 大小上限 + 服务端重命名/路径生成防路径穿越）→
//     files 登记（module/business_no/uploader/保留期）；
//   - 下载：权限点 datax:file:read + 过期/软删拒绝 + 流式 ServeContent + operation_logs
//     审计（module=file, action=download——excel §3 下载记录落位，plan §6.4）；
//   - 预览：仅图片原样流式返回（不做缩略图——plan §6.4）；
//   - 列表：module/business_no/keyword 过滤 + 分页（文件名/类型/大小/上传人/时间/模块/单号）；
//   - 删除：软删（deleted_at）+ 审计；物理删除由 sysops file_cleanup 定时任务承担。

// FileUploadInput 上传入参（multipart 字段：module / business_no / file）。
type FileUploadInput struct {
	Module     string
	BusinessNo string
	File       *multipart.FileHeader
}

// FileItem 文件列表项（excel §7 记录字段 + 访问地址；前端 file.ts FileItem 契约回对为
// snake_case——plan §12.4 条 2，后端为冻结契约）。时间字段统一 database.JSONTime
// （api.md §2 YYYY-MM-DD HH:mm:ss；裸 time.Time 会序列化为 RFC3339——2026-10-04 统一）。
type FileItem struct {
	ID           int64             `json:"id"`
	FileName     string            `json:"file_name"`
	FileType     string            `json:"file_type"` // 扩展名白名单值（.xlsx 等）
	MimeType     string            `json:"mime_type"`
	SizeBytes    int64             `json:"size_bytes"`
	Module       string            `json:"module"`
	ModuleName   string            `json:"module_name,omitempty"`
	BusinessNo   string            `json:"business_no,omitempty"`
	UploaderID   int64             `json:"uploader_id,omitempty"`
	UploaderName string            `json:"uploader_name"`
	DownloadURL  string            `json:"download_url"`
	ExpiresAt    database.JSONTime `json:"expires_at,omitempty"` // NULL=不过期 → 零值序列化 null
	CreatedAt    database.JSONTime `json:"created_at"`
}

// FileModulePattern module/business_no 参数字符集（files 列 varchar(64) 防御 +
// 防 LOC 注入式脏值入库；业务模块为编码值不含路径/空白字符）。
func validModuleParam(v string, maxLen int) bool {
	if v == "" || len(v) > maxLen {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// UploadFile 文件上传（api.md §5 全量校验经 storage.Save；登记与审计同事务）。
func (s *Service) UploadFile(ctx context.Context, actor Actor, in FileUploadInput) (*FileItem, error) {
	if !validModuleParam(in.Module, 64) {
		return nil, response.NewError(ErrModuleParamInvalid, map[string]any{"field": "module"})
	}
	if in.BusinessNo != "" && !validModuleParam(in.BusinessNo, 64) {
		return nil, response.NewError(ErrModuleParamInvalid, map[string]any{"field": "business_no"})
	}
	if in.File == nil || in.File.Size == 0 {
		return nil, response.NewError(ErrImportFileRequired, nil)
	}
	src, err := in.File.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()

	// 物理保存（扩展名/MIME 交叉/大小上限/服务端重命名与路径生成——api.md §5 全清单）。
	stored, err := s.store.Save(src, in.File.Filename, s.cfg.UploadMaxBytes)
	if err != nil {
		return nil, err
	}

	file := &storage.File{
		FileName:     in.File.Filename,
		StoredName:   stored.StoredName,
		StoragePath:  stored.StoragePath,
		MimeType:     stored.MimeType,
		FileType:     stored.FileType,
		SizeBytes:    stored.Size,
		Module:       in.Module,
		BusinessNo:   in.BusinessNo,
		UploaderID:   actor.UserID,
		UploaderName: actor.Username,
		ExpiresAt:    s.expiry(),
		CreatedBy:    actor.UserID,
	}
	err = s.inTx(ctx, func(tx *gorm.DB) error {
		if err := s.repo.InsertFile(tx, file); err != nil {
			return err
		}
		e := actor.auditEntry("file", file.ID, "upload")
		e.Success = true
		e.Request = map[string]any{"module": in.Module, "business_no": in.BusinessNo,
			"file_name": in.File.Filename, "size_bytes": stored.Size}
		e.After = map[string]any{"file_id": file.ID, "stored_name": stored.StoredName}
		return s.audit(tx, e)
	})
	if err != nil {
		_ = s.store.Remove(stored.StoragePath) // 登记失败：清理无主物理文件
		return nil, err
	}
	return fileItemOf(file), nil
}

// fileItemOf 登记行 → 列表项（时间统一 JSONTime；ExpiresAt 指针 → 零值=null）。
func fileItemOf(f *storage.File) *FileItem {
	return &FileItem{
		ID: f.ID, FileName: f.FileName, FileType: f.FileType,
		MimeType: f.MimeType, SizeBytes: f.SizeBytes,
		Module: f.Module, ModuleName: ModuleLabel(f.Module), BusinessNo: f.BusinessNo,
		UploaderID: f.UploaderID, UploaderName: f.UploaderName,
		DownloadURL: "/api/files/" + strconv.FormatInt(f.ID, 10) + "/download",
		ExpiresAt:   jsonTimePtr(f.ExpiresAt),
		CreatedAt:   database.JSONTime{Time: f.CreatedAt},
	}
}

// ListFiles 文件列表（excel §7：按业务模块/关联单据过滤可见 + 分页）。
// 数据权限（plan §6.4"数据权限沿用仓库范围，file 列表 Service 层过滤"）：范围规则见
// FileScope/repository.go 注释——SQL 侧过滤保证分页 total 与行面一致（repo_gorm.go）。
func (s *Service) ListFiles(ctx context.Context, f FileListFilter) ([]FileItem, int64, error) {
	f.Page, f.PageSize = normalizePage(f.Page, f.PageSize)
	rows, total, err := s.repo.ListFiles(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]FileItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, *fileItemOf(r))
	}
	return items, total, nil
}

// fileVisible 单文件可见性判定（FileScope 冻结规则；与 repo_gorm.go ListFiles 的 SQL
// 条件同规则——列表走 SQL 保证分页一致，单文件读取走本函数）：
//   - All（超管/ALL 范围）：全部可见；
//   - 范围受限：① 本人上传；② datax 导出产物（business_no=EXP- 单号）归属导出任务的
//     仓库范围快照（params.all_warehouses / params.warehouse_ids，创建时点冻结）与
//     可见仓库集相交；③ datax 导入任务产物（business_no=IMP- 单号）归属导入任务创建人
//     （import_tasks 无仓库快照列——导入原始/错误明细仅创建人可见，fail-closed）；
//   - 其余（含无法归属任务的普通附件）：不可见。
func (s *Service) fileVisible(ctx context.Context, f *storage.File, scope *FileScope) bool {
	if scope == nil || scope.All {
		return true
	}
	if f.UploaderID == scope.UserID {
		return true
	}
	if f.Module == "datax" {
		if t, err := s.repo.FindExportTaskByNo(ctx, f.BusinessNo); err == nil && t != nil {
			all, ids := exportScopeSnapshot(t.Params)
			if all {
				return true
			}
			for _, id := range ids {
				for _, wid := range scope.WarehouseIDs {
					if id == wid {
						return true
					}
				}
			}
			return false
		}
		if t, err := s.repo.FindImportTaskByNo(ctx, f.BusinessNo); err == nil && t != nil {
			return int64(t.CreatedBy) == scope.UserID
		}
	}
	return false
}

// exportScopeSnapshot 导出任务数据权限快照解析（CreateExport 冻结键：all_warehouses/
// warehouse_ids；键缺失按 false/空集处理）。
func exportScopeSnapshot(params JSONMap) (all bool, ids []int64) {
	if v, ok := params["all_warehouses"].(bool); ok {
		all = v
	}
	if raw, ok := params["warehouse_ids"].([]any); ok {
		for _, item := range raw {
			if fv, ok := item.(float64); ok {
				ids = append(ids, int64(fv))
			}
		}
	}
	return all, ids
}

// ensureFileVisible 范围受限用户访问不可见文件按不存在处理（防 ID 枚举探测）。
func (s *Service) ensureFileVisible(ctx context.Context, f *storage.File, scope *FileScope) error {
	if !s.fileVisible(ctx, f, scope) {
		return response.NewError(ErrFileNotFound, nil)
	}
	return nil
}

// DeleteFile 软删除（plan §6.4：删除=软删 + 审计；物理删除由 file_cleanup 承担）。
// 幂等：0 行（不存在或已删）返回 404。scope 为数据权限范围（FileScope 规则）。
func (s *Service) DeleteFile(ctx context.Context, actor Actor, id int64, scope *FileScope) error {
	// 先过可见性闸（软删/过期行拒绝操作 + 数据权限范围校验）。
	f, err := s.loadFileRow(ctx, id)
	if err != nil {
		return err
	}
	if err := s.ensureFileVisible(ctx, f, scope); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.SoftDeleteFile(tx, id, actor.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrFileNotFound, nil)
		}
		e := actor.auditEntry("file", id, "delete")
		e.Success = true
		return s.audit(tx, e)
	})
}

// AuditFileDownload 下载审计（module=file, action=download——excel §3 下载记录，
// plan §6.4：下载记录复用 operation_logs 且以 file 归属；下载无业务事务，独立写入，
// 失败不阻断下载——读取侧降级口径）。
func (s *Service) AuditFileDownload(ctx context.Context, actor Actor, fileID int64, fileName string) {
	e := actor.auditEntry("file", fileID, "download")
	e.Module = "file"
	e.Success = true
	e.Request = map[string]any{"file_name": fileName}
	s.auditStandalone(ctx, e)
}
