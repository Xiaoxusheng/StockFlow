package returns

// returns 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// EXCEPTION 一源）。只读本域表（§2.2 规则①）；keyset 游标 = 主键 id 升序；
// 筛选键白名单（plan §18.5）。异常单为跨域工单（迁移 000010 无仓库列），不按仓库
// 范围过滤——与 returns 域列表数据权限口径一致（handler.go 数据权限注记）。

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
)

// batchOf 每批行数（执行器注入 datax.export_batch_size；0 取缺省 500）。
func batchOf(f datax.ExportFilter) int {
	if f.BatchSize > 0 {
		return f.BatchSize
	}
	return 500
}

// cursorError 游标解析错误。
type cursorError struct {
	msg   string
	cause error
}

func (e *cursorError) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

func textCell(v string) datax.CellValue { return datax.CellValue{T: datax.CellText, S: v} }

func numCell(v float64) datax.CellValue { return datax.CellValue{T: datax.CellNumber, N: v} }

// dbTime 数据库时间列的极小接口（database.JSONTime 满足）。
type dbTime interface {
	IsZero() bool
	Format(string) string
}

func dateCell(t dbTime) datax.CellValue {
	if t == nil || t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
}

// ExceptionExportSource 异常单行源。
type ExceptionExportSource struct {
	db *gorm.DB
}

// NewExceptionExportSource 构建异常单行源（router 装配）。
func NewExceptionExportSource(db *gorm.DB) datax.ExportSource {
	return &ExceptionExportSource{db: db}
}

// Columns 导出列。
func (s *ExceptionExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "exception_no", Title: "异常单号", Width: 20},
		{Key: "exc_type", Title: "异常类型", Width: 14},
		{Key: "source_type", Title: "来源类型", Width: 14},
		{Key: "source_no", Title: "来源单号", Width: 22},
		{Key: "sku_id", Title: "SKU ID", Type: datax.CellNumber},
		{Key: "bin_id", Title: "库位ID", Type: datax.CellNumber},
		{Key: "serial_no", Title: "序列号", Width: 18},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "assignee_name", Title: "处理人", Width: 12},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
		{Key: "resolved_at", Title: "解决时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 合计声明（无）。
func (s *ExceptionExportSource) Summary() []datax.SummaryRow { return nil }

// applyFilters 筛选（白名单键 + TIME_RANGE + SELECTED）。
func (s *ExceptionExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if et := f.Filters["type"]; et != "" {
		q = q.Where("type = ?", et)
	}
	if an := f.Filters["assignee_id"]; an != "" {
		q = q.Where("assignee_id = ?", an)
	}
	if f.TimeFrom != nil {
		q = q.Where("created_at >= ?", *f.TimeFrom)
	}
	if f.TimeTo != nil {
		q = q.Where("created_at <= ?", *f.TimeTo)
	}
	if len(f.IDs) > 0 {
		q = q.Where("id IN ?", f.IDs)
	}
	return q
}

// Count 满足筛选的总行数。
func (s *ExceptionExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&Exception{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *ExceptionExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&Exception{}), f)
	if f.Scope == "CURRENT_PAGE" {
		if f.Page > 0 && f.PageSize > 0 {
			q = q.Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize)
		}
	} else if c.Value != "" {
		id, err := strconv.ParseInt(c.Value, 10, 64)
		if err != nil {
			return nil, c, &cursorError{msg: "导出游标非法: " + c.Value, cause: err}
		}
		q = q.Where("id > ?", id)
	}
	var rows []*Exception
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.ExceptionNo), textCell(r.Type), textCell(r.SourceType),
			textCell(r.SourceNo), numCell(float64(r.SKUID)), numCell(float64(r.BinID)),
			textCell(r.SerialNo), textCell(r.Status), textCell(r.AssigneeName),
			dateCell(r.CreatedAt), dateCell(r.ResolvedAt),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}
