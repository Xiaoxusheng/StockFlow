package datax

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/storage"
)

// GORM 数据访问实现（迁移 000011 列结构；写 SQL 仅命中白名单四表——plan §2.3 判据 3，
// guard-datax 守卫核查）。守卫更新统一形态：WHERE id=? AND status IN from，影响行数由
// Service 判定（0 = 状态冲突，plan §13.2）。
type gormRepository struct {
	db *gorm.DB
}

// NewGormRepository 构建数据访问实现。
func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) DB() *gorm.DB { return r.db }

// —— 导入任务 ——

func (r *gormRepository) InsertImportTask(ctx context.Context, tx *gorm.DB, t *ImportTask) error {
	// 单号发放与建行同事务（docnum §4.1 冻结机制；IMP 前缀 plan §12.3）。
	rule, ok := docnum.RuleFor("IMP")
	if !ok {
		return fmt.Errorf("datax: IMP 前缀不在 docnum 冻结注册表（编程错误）")
	}
	no, err := docnum.NextWithRetry(ctx, tx, rule, func(tx *gorm.DB, no string) error {
		t.ImportNo = no
		return tx.Create(t).Error
	})
	if err != nil {
		return err
	}
	t.ImportNo = no
	return nil
}

func (r *gormRepository) FindImportTask(ctx context.Context, id int64) (*ImportTask, error) {
	var t ImportTask
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, responseNotFound()
		}
		return nil, fmt.Errorf("datax: 查询导入任务 %d 失败: %w", id, err)
	}
	return &t, nil
}

func (r *gormRepository) FindImportTaskByNo(ctx context.Context, no string) (*ImportTask, error) {
	var t ImportTask
	err := r.db.WithContext(ctx).Where("import_no = ?", no).Take(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, responseNotFound()
		}
		return nil, fmt.Errorf("datax: 查询导入任务 %s 失败: %w", no, err)
	}
	return &t, nil
}

func (r *gormRepository) ListImportTasks(ctx context.Context, f TaskListFilter) ([]*ImportTask, int64, error) {
	q := r.db.WithContext(ctx).Model(&ImportTask{})
	if f.Module != "" {
		q = q.Where("import_type = ?", f.Module)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("datax: 统计导入任务失败: %w", err)
	}
	var rows []*ImportTask
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("datax: 查询导入任务列表失败: %w", err)
	}
	return rows, total, nil
}

func (r *gormRepository) GuardUpdateImportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error) {
	q := tx.Model(&ImportTask{}).Where("id = ?", id)
	if len(from) > 0 {
		q = q.Where("status IN ?", from)
	}
	res := q.Updates(sanitizeTaskSet(set))
	return res.RowsAffected, res.Error
}

// —— 导入任务行 ——

func (r *gormRepository) InsertImportRows(tx *gorm.DB, rows []*ImportTaskRow) error {
	if len(rows) == 0 {
		return nil
	}
	// 分片插入（行数上限 5000，单语句参数上限防御；200/片与导入批大小同量级）。
	const chunk = 200
	for i := 0; i < len(rows); i += chunk {
		end := i + chunk
		if end > len(rows) {
			end = len(rows)
		}
		if err := tx.Create(rows[i:end]).Error; err != nil {
			return fmt.Errorf("datax: 写入导入任务行失败: %w", err)
		}
	}
	return nil
}

func (r *gormRepository) ListImportRows(ctx context.Context, taskID int64, statuses []string, limit int) ([]*ImportTaskRow, error) {
	q := r.db.WithContext(ctx).Where("task_id = ?", taskID)
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []*ImportTaskRow
	if err := q.Order("row_no ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("datax: 查询导入任务行失败: %w", err)
	}
	return rows, nil
}

func (r *gormRepository) UpdateImportRow(tx *gorm.DB, rowID int64, set map[string]any) error {
	return tx.Model(&ImportTaskRow{}).Where("id = ?", rowID).Updates(sanitizeTaskSet(set)).Error
}

func (r *gormRepository) CountImportRowsByStatus(ctx context.Context, taskID int64) (map[string]int64, error) {
	var counts []struct {
		Status string
		N      int64
	}
	err := r.db.WithContext(ctx).Model(&ImportTaskRow{}).
		Select("status, COUNT(*) AS n").
		Where("task_id = ?", taskID).
		Group("status").Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("datax: 统计导入行状态失败: %w", err)
	}
	out := make(map[string]int64, len(counts))
	for _, c := range counts {
		out[c.Status] = c.N
	}
	return out, nil
}

// —— 导出任务 ——

func (r *gormRepository) InsertExportTask(ctx context.Context, tx *gorm.DB, t *ExportTask) error {
	rule, ok := docnum.RuleFor("EXP")
	if !ok {
		return fmt.Errorf("datax: EXP 前缀不在 docnum 冻结注册表（编程错误）")
	}
	no, err := docnum.NextWithRetry(ctx, tx, rule, func(tx *gorm.DB, no string) error {
		t.ExportNo = no
		return tx.Create(t).Error
	})
	if err != nil {
		return err
	}
	t.ExportNo = no
	return nil
}

func (r *gormRepository) FindExportTask(ctx context.Context, id int64) (*ExportTask, error) {
	var t ExportTask
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, responseNotFound()
		}
		return nil, fmt.Errorf("datax: 查询导出任务 %d 失败: %w", id, err)
	}
	return &t, nil
}

func (r *gormRepository) FindExportTaskByNo(ctx context.Context, no string) (*ExportTask, error) {
	var t ExportTask
	err := r.db.WithContext(ctx).Where("export_no = ?", no).Take(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, responseNotFound()
		}
		return nil, fmt.Errorf("datax: 查询导出任务 %s 失败: %w", no, err)
	}
	return &t, nil
}

func (r *gormRepository) ListExportTasks(ctx context.Context, f TaskListFilter) ([]*ExportTask, int64, error) {
	q := r.db.WithContext(ctx).Model(&ExportTask{})
	if f.Module != "" {
		q = q.Where("module = ?", f.Module)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("datax: 统计导出任务失败: %w", err)
	}
	var rows []*ExportTask
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("datax: 查询导出任务列表失败: %w", err)
	}
	return rows, total, nil
}

func (r *gormRepository) GuardUpdateExportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error) {
	q := tx.Model(&ExportTask{}).Where("id = ?", id)
	if len(from) > 0 {
		q = q.Where("status IN ?", from)
	}
	res := q.Updates(sanitizeTaskSet(set))
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateExportTask(ctx context.Context, id int64, set map[string]any) error {
	return r.db.WithContext(ctx).Model(&ExportTask{}).Where("id = ?", id).Updates(sanitizeTaskSet(set)).Error
}

// —— 文件中心 ——

func (r *gormRepository) InsertFile(tx *gorm.DB, f *storage.File) error {
	return tx.Create(f).Error
}

func (r *gormRepository) FindFile(ctx context.Context, id int64) (*storage.File, error) {
	var f storage.File
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&f).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, responseNotFound()
		}
		return nil, fmt.Errorf("datax: 查询文件 %d 失败: %w", id, err)
	}
	return &f, nil
}

func (r *gormRepository) ListFiles(ctx context.Context, f FileListFilter) ([]*storage.File, int64, error) {
	q := r.db.WithContext(ctx).Model(&storage.File{})
	if f.Module != "" {
		q = q.Where("module = ?", f.Module)
	}
	if f.BusinessNo != "" {
		q = q.Where("business_no = ?", f.BusinessNo)
	}
	if f.Keyword != "" {
		q = q.Where("file_name ILIKE ?", "%"+f.Keyword+"%")
	}
	// 数据权限（plan §6.4"数据权限沿用仓库范围，file 列表 Service 层过滤"）：SQL 侧过滤
	// 保证分页 total 与行面一致；规则与 service_file.go fileVisible 同一冻结口径——
	// 范围受限用户可见：本人上传 ∪ datax 导出产物（归属导出任务仓库范围快照与可见仓库
	// 集相交）∪ datax 导入任务产物（归属导入任务创建人，import_tasks 无仓库快照列）。
	if f.Scope != nil && !f.Scope.All {
		q = q.Where(`(
			uploader_id = ?
			OR EXISTS (SELECT 1 FROM export_tasks et WHERE et.export_no = files.business_no
				AND ((et.params->>'all_warehouses')::boolean IS TRUE
					OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(et.params->'warehouse_ids', '[]'::jsonb)) AS w
						WHERE w::text::bigint IN ?)))
			OR EXISTS (SELECT 1 FROM import_tasks it WHERE it.import_no = files.business_no AND it.created_by = ?)
		)`, f.Scope.UserID, f.Scope.WarehouseIDs, f.Scope.UserID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("datax: 统计文件失败: %w", err)
	}
	var rows []*storage.File
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("datax: 查询文件列表失败: %w", err)
	}
	return rows, total, nil
}

func (r *gormRepository) SoftDeleteFile(tx *gorm.DB, id int64, by int64) (int64, error) {
	res := tx.Model(&storage.File{}).Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{
			"deleted_at": database.Now(),
			"updated_by": by,
		})
	return res.RowsAffected, res.Error
}

// taskSetWhitelist 守卫/普通更新的列名白名单（set 键必须命中，防列名注入——go-dev-standard
// 规则 8 同族防线；本包 set 均为代码内字面量，白名单为纵深防御）。
var taskSetWhitelist = map[string]bool{
	"status": true, "total_rows": true, "valid_rows": true, "error_rows": true,
	"success_rows": true, "failed_rows": true, "error_file_id": true, "error_message": true,
	"started_at": true, "finished_at": true, "progress": true, "file_id": true,
	"updated_by": true, "updated_at": true,
	// import_task_rows 专用列：
	"row_no": true, "raw": true, "parsed": true, "errors": true, "batch_no": true, "created_by": true,
}

func sanitizeTaskSet(set map[string]any) map[string]any {
	out := make(map[string]any, len(set))
	for k, v := range set {
		if taskSetWhitelist[k] {
			out[k] = v
		}
	}
	return out
}
