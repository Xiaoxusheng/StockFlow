package warehouse

// warehouse 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// WAREHOUSE / LOCATION 两源）。只读本域表（§2.2 规则①）；keyset 游标 = 主键 id 升序
// （plan §6.3 禁止 offset 深翻页）；筛选键白名单（plan §18.5：未知键忽略）。

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
)

// exporter 本域行源共用查询底座（GORM 只读；软删行由模型作用域自动排除）。
type exporter struct {
	db *gorm.DB
}

// NewDataxExporter 构建行源查询底座（router 装配）。
func NewDataxExporter(db *gorm.DB) *exporter { return &exporter{db: db} }

// applyTimeRange TIME_RANGE：created_at 区间（含边界）。
func applyTimeRange(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if f.TimeFrom != nil {
		q = q.Where("created_at >= ?", *f.TimeFrom)
	}
	if f.TimeTo != nil {
		q = q.Where("created_at <= ?", *f.TimeTo)
	}
	return q
}

// applyIDs SELECTED：id IN 集合。
func applyIDs(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if len(f.IDs) > 0 {
		q = q.Where("id IN ?", f.IDs)
	}
	return q
}

// applyPage CURRENT_PAGE：单页窗口（数据量 ≤ 100，无深翻页问题）。
func applyPage(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if f.Page > 0 && f.PageSize > 0 {
		q = q.Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize)
	}
	return q
}

// ---- 仓库行源（WAREHOUSE）----

// WarehouseExportSource 仓库行源。
type WarehouseExportSource struct{ ex *exporter }

// NewWarehouseExportSource 构建仓库行源（router 装配）。
func NewWarehouseExportSource(db *gorm.DB) datax.ExportSource {
	return &WarehouseExportSource{ex: NewDataxExporter(db)}
}

// Columns 导出列。
func (s *WarehouseExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "code", Title: "仓库编码", Width: 14},
		{Key: "name", Title: "仓库名称", Width: 20},
		{Key: "wh_type", Title: "仓库类型", Width: 12},
		{Key: "address", Title: "地址", Width: 26},
		{Key: "contact", Title: "联系人", Width: 12},
		{Key: "phone", Title: "电话", Width: 14},
		{Key: "area", Title: "面积(㎡)", Type: datax.CellNumber},
		{Key: "capacity", Title: "容量", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 10},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
	}
}

// Count 满足筛选的总行数。
func (s *WarehouseExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q, err := s.query(ctx, f)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// query 构造筛选查询（CURSOR 之外的通用条件；CURRENT_PAGE 的窗口在 Batch 内附加）。
func (s *WarehouseExportSource) query(ctx context.Context, f datax.ExportFilter) (*gorm.DB, error) {
	q := s.ex.db.WithContext(ctx).Model(&Warehouse{})
	if kw := f.Filters["keyword"]; kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	q = applyTimeRange(q, f)
	q = applyIDs(q, f)
	return q, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *WarehouseExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q, err := s.query(ctx, f)
	if err != nil {
		return nil, c, err
	}
	if f.Scope == "CURRENT_PAGE" {
		q = applyPage(q, f)
	} else if c.Value != "" {
		id, perr := strconv.ParseInt(c.Value, 10, 64)
		if perr != nil {
			return nil, c, errCursor(c.Value, perr)
		}
		q = q.Where("id > ?", id)
	}
	var rows []*Warehouse
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.Code), textCell(r.Name), textCell(r.Type), textCell(r.Address),
			textCell(r.Contact), textCell(r.Phone), numCell(r.Area), numCell(r.Capacity),
			textCell(r.Status), dateCell(r.CreatedAt.Time),
		}})
	}
	return out, nextCursor(rows, f), nil
}

// Summary 合计声明（无数值合计需求，nil）。
func (s *WarehouseExportSource) Summary() []datax.SummaryRow { return nil }

// ---- 库位行源（LOCATION）----

// BinExportSource 库位行源。
type BinExportSource struct{ ex *exporter }

// NewBinExportSource 构建库位行源（router 装配）。
func NewBinExportSource(db *gorm.DB) datax.ExportSource {
	return &BinExportSource{ex: NewDataxExporter(db)}
}

// Columns 导出列。
func (s *BinExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "zone_id", Title: "库区ID", Type: datax.CellNumber},
		{Key: "shelf_id", Title: "货架ID", Type: datax.CellNumber},
		{Key: "code", Title: "库位编码", Width: 18},
		{Key: "bin_type", Title: "库位类型", Width: 10},
		{Key: "layer", Title: "层", Type: datax.CellNumber},
		{Key: "column_no", Title: "列", Type: datax.CellNumber},
		{Key: "max_capacity", Title: "最大容量", Type: datax.CellNumber},
		{Key: "current_capacity", Title: "当前占用", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 10},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
	}
}

// Count 满足筛选的总行数。
func (s *BinExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q, err := s.query(ctx, f)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// query 构造筛选查询（数据权限：仓库范围过滤——plan §13.6）。
func (s *BinExportSource) query(ctx context.Context, f datax.ExportFilter) (*gorm.DB, error) {
	q := s.ex.db.WithContext(ctx).Model(&Bin{})
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			q = q.Where("1 = 0") // fail-closed：无可见仓库
		} else {
			q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
		}
	}
	if wid := f.Filters["warehouse_id"]; wid != "" {
		q = q.Where("warehouse_id = ?", wid)
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if kw := f.Filters["keyword"]; kw != "" {
		q = q.Where("code ILIKE ?", "%"+kw+"%")
	}
	q = applyTimeRange(q, f)
	q = applyIDs(q, f)
	return q, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *BinExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q, err := s.query(ctx, f)
	if err != nil {
		return nil, c, err
	}
	if f.Scope == "CURRENT_PAGE" {
		q = applyPage(q, f)
	} else if c.Value != "" {
		id, perr := strconv.ParseInt(c.Value, 10, 64)
		if perr != nil {
			return nil, c, errCursor(c.Value, perr)
		}
		q = q.Where("id > ?", id)
	}
	var rows []*Bin
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			numCell(float64(r.WarehouseID.Int64())), numCell(float64(r.ZoneID.Int64())),
			numCell(float64(r.ShelfID.Int64())), textCell(r.Code), textCell(r.BinType),
			numCell(float64(r.Layer)), numCell(float64(r.ColumnNo)),
			numCell(r.MaxCapacity), numCell(r.CurrentCapacity),
			textCell(r.Status), dateCell(r.CreatedAt.Time),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}

// Summary 合计声明（无）。
func (s *BinExportSource) Summary() []datax.SummaryRow { return nil }

// ---- 行源共用工具（本文件两个行源共享）----

// batchOf 每批行数（执行器注入 datax.export_batch_size；0 取缺省 500）。
func batchOf(f datax.ExportFilter) int {
	if f.BatchSize > 0 {
		return f.BatchSize
	}
	return 500
}

// nextCursor 生成续读游标（不足一批即结束；keyset = 最后行主键 id）。
func nextCursor(rows []*Warehouse, f datax.ExportFilter) datax.Cursor {
	if len(rows) < batchOf(f) {
		return datax.Cursor{Done: true}
	}
	return datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
}

func textCell(v string) datax.CellValue { return datax.CellValue{T: datax.CellText, S: v} }

func numCell(v float64) datax.CellValue { return datax.CellValue{T: datax.CellNumber, N: v} }

func dateCell(t time.Time) datax.CellValue {
	if t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
}

func errCursor(v string, err error) error {
	return &cursorError{msg: "导出游标非法: " + v, cause: err}
}

// cursorError 游标解析错误（导出执行器终止任务并落 error_message）。
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
