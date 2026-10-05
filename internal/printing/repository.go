package printing

import (
	"context"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// 数据访问层（architecture.md §1：Service → Repository → Database；handler 禁止
// 直连数据库，plan §2.3 判据 6）。Repository 为接口：Service 依赖接口，单元测试
// 以内存替身替换（ask 约束：单测不依赖 PostgreSQL/Redis/网络，参照
// internal/returns、internal/sales 既有替身模式）。

// TemplateFilter 模板列表过滤（列表强制分页，architecture.md §7）。
type TemplateFilter struct {
	Keyword    string // 名称/备注 ILIKE
	ObjectType string
	Status     string
	Page       int
	PageSize   int
}

// TaskFilter 打印任务列表过滤（前端 PrintTaskQuery：objectType/status/id；
// ConfirmedOnly=true 供打印历史（print_tasks 已确认子集，plan §7.2），
// Result 为历史执行结果筛选 SUCCESS/FAILED，TemplateID 为历史按模板筛选
// （qr-code.md 闭环——GET /api/prints/history 只加查询参数不加端点）。
type TaskFilter struct {
	ID            int64  // 精确过滤（前端预览页回对取单条）
	TemplateID    int64  // 模板精确过滤（历史页；0=全部）
	Keyword       string // 单号 ILIKE
	ObjectType    string
	Status        string
	ConfirmedOnly bool   // result IS NOT NULL
	Result        string // result 精确筛选（历史页；空=全部）
	Page          int
	PageSize      int
}

// Repository 打印域数据访问接口。
type Repository interface {
	// DB 返回句柄（Service 开启外层事务用，returns/masterdata 同款约定）。
	DB() *gorm.DB

	// —— 模板 ——
	InsertTemplate(tx *gorm.DB, t *PrintTemplate) error
	UpdateTemplate(tx *gorm.DB, t *PrintTemplate) error
	FindTemplate(ctx context.Context, id int64) (*PrintTemplate, error)
	ListTemplates(ctx context.Context, f TemplateFilter) ([]*PrintTemplate, int64, error)
	// UpdateTemplateStatus 启停守卫 UPDATE：WHERE id=? AND status=from，影响行数 0 即
	// 状态冲突（plan §13.2 状态守卫统一形态）。
	UpdateTemplateStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error)

	// —— 任务 ——
	InsertTask(tx *gorm.DB, t *PrintTask) error
	InsertTaskRows(tx *gorm.DB, rows []*PrintTaskRow) error
	FindTask(ctx context.Context, id int64) (*PrintTask, error)
	FindTaskByNo(ctx context.Context, printNo string) (*PrintTask, error)
	ListTasks(ctx context.Context, f TaskFilter) ([]*PrintTask, int64, error)
	ListTaskRows(ctx context.Context, taskID int64) ([]*PrintTaskRow, error)
	// UpdateTaskStatus render 状态机守卫 UPDATE：WHERE print_no=? AND status IN from，
	// 影响行数 0 即状态冲突/已被处理（plan §4.2 handler 状态守卫、§13.2 统一形态）。
	// to=FAILED 时 errMsg 落 error_message。
	UpdateTaskStatus(tx *gorm.DB, printNo string, from []string, to, errMsg string) (int64, error)
	// UpdateExecuteResult 执行确认回填（plan §13.2：SUCCESS/FAILED 回填一次）：
	// WHERE id=? AND result IS NULL，0 行=已确认（PRINT_ALREADY_CONFIRMED）；
	// 命中时回填 result/printed_by/printed_at=now()，message 非空且 result=FAILED
	// 时写 error_message（执行失败原因；不覆盖 render 侧错误以外的语义）。
	UpdateExecuteResult(tx *gorm.DB, id int64, result, message string, by int64) (int64, error)
}

// gormRepository GORM 实现（迁移 000012 列结构）。
type gormRepository struct {
	db *gorm.DB
}

// NewGormRepository 构造 GORM 数据访问实现。
func NewGormRepository(db *gorm.DB) Repository {
	return &gormRepository{db: db}
}

func (r *gormRepository) DB() *gorm.DB { return r.db }

// ---- 模板 ----

func (r *gormRepository) InsertTemplate(tx *gorm.DB, t *PrintTemplate) error {
	return tx.Create(t).Error
}

// templateEditableCols 模板可编辑列白名单（UpdateTemplate 全量按列更新；
// status 走专用守卫端点，不在其中）。
func (r *gormRepository) UpdateTemplate(tx *gorm.DB, t *PrintTemplate) error {
	return tx.Model(&PrintTemplate{}).
		Where("id = ?", t.ID.Int64()).
		Updates(map[string]any{
			"name":              t.Name,
			"object_type":       t.ObjectType,
			"paper":             t.Paper,
			"barcode_symbology": t.BarcodeSymbology,
			"qrcode_enabled":    t.QRCodeEnabled,
			"fields":            t.Fields,
			"header_text":       t.HeaderText,
			"remark":            t.Remark,
			"updated_at":        gorm.Expr("now()"),
			"updated_by":        t.UpdatedBy,
		}).Error
}

func (r *gormRepository) FindTemplate(ctx context.Context, id int64) (*PrintTemplate, error) {
	var t PrintTemplate
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *gormRepository) ListTemplates(ctx context.Context, f TemplateFilter) ([]*PrintTemplate, int64, error) {
	q := r.db.WithContext(ctx).Model(&PrintTemplate{})
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("name ILIKE ? OR remark ILIKE ?", kw, kw)
	}
	if f.ObjectType != "" {
		q = q.Where("object_type = ?", f.ObjectType)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*PrintTemplate
	err := q.Order("id DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) UpdateTemplateStatus(tx *gorm.DB, id int64, from, to string, by int64) (int64, error) {
	res := tx.Model(&PrintTemplate{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{
			"status":     to,
			"updated_at": gorm.Expr("now()"),
			"updated_by": by,
		})
	return res.RowsAffected, res.Error
}

// ---- 任务 ----

func (r *gormRepository) InsertTask(tx *gorm.DB, t *PrintTask) error {
	return tx.Create(t).Error
}

func (r *gormRepository) InsertTaskRows(tx *gorm.DB, rows []*PrintTaskRow) error {
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func (r *gormRepository) FindTask(ctx context.Context, id int64) (*PrintTask, error) {
	var t PrintTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *gormRepository) FindTaskByNo(ctx context.Context, printNo string) (*PrintTask, error) {
	var t PrintTask
	err := r.db.WithContext(ctx).Where("print_no = ?", printNo).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *gormRepository) ListTasks(ctx context.Context, f TaskFilter) ([]*PrintTask, int64, error) {
	q := r.db.WithContext(ctx).Model(&PrintTask{})
	if f.ID > 0 {
		q = q.Where("id = ?", f.ID)
	}
	if f.TemplateID > 0 {
		q = q.Where("template_id = ?", f.TemplateID)
	}
	if f.Keyword != "" {
		q = q.Where("print_no ILIKE ?", "%"+f.Keyword+"%")
	}
	if f.ObjectType != "" {
		if !ObjectTypeValid(f.ObjectType) {
			return nil, 0, response.NewError(ErrObjectTypeInvalid, map[string]any{"object_type": f.ObjectType})
		}
		q = q.Where("object_type = ?", f.ObjectType)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.ConfirmedOnly {
		q = q.Where("result IS NOT NULL")
	}
	if f.Result != "" {
		q = q.Where("result = ?", f.Result)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*PrintTask
	err := q.Order("id DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *gormRepository) ListTaskRows(ctx context.Context, taskID int64) ([]*PrintTaskRow, error) {
	var rows []*PrintTaskRow
	err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).Order("seq").
		Find(&rows).Error
	return rows, err
}

func (r *gormRepository) UpdateTaskStatus(tx *gorm.DB, printNo string, from []string, to, errMsg string) (int64, error) {
	sets := map[string]any{
		"status":     to,
		"updated_at": gorm.Expr("now()"),
	}
	if to == TaskStatusFailed {
		sets["error_message"] = errMsg
	}
	res := tx.Model(&PrintTask{}).
		Where("print_no = ? AND status IN ?", printNo, from).
		Updates(sets)
	return res.RowsAffected, res.Error
}

func (r *gormRepository) UpdateExecuteResult(tx *gorm.DB, id int64, result, message string, by int64) (int64, error) {
	sets := map[string]any{
		"result":     result,
		"printed_by": by,
		"printed_at": gorm.Expr("now()"),
		"updated_at": gorm.Expr("now()"),
		"updated_by": by,
	}
	if result == ResultFailed && message != "" {
		sets["error_message"] = message
	}
	res := tx.Model(&PrintTask{}).
		Where("id = ? AND result IS NULL", id).
		Updates(sets)
	return res.RowsAffected, res.Error
}
