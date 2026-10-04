package stockops

// stockops 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// TRANSFER / COUNT 两源）。只读本域表（§2.2 规则①）；keyset 游标 = 主键 id 升序；
// 筛选键白名单（plan §18.5）；数据权限按仓库范围过滤（plan §13.6）。

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/stock"
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

func dateCell(t dbTime) datax.CellValue {
	if t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
}

func qtyFloat(q stock.Qty) float64 {
	f, _ := strconv.ParseFloat(q.String(), 64)
	return f
}

// dbTime 数据库时间列的极小接口（database.JSONTime 满足）。
type dbTime interface {
	IsZero() bool
	Format(string) string
}

// applyCommon TIME_RANGE / SELECTED / 仓库范围。
func applyCommon(q *gorm.DB, f datax.ExportFilter, whCols ...string) *gorm.DB {
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

// ---- 调拨单行源（TRANSFER）----

// TransferExportSource 调拨单行源。
type TransferExportSource struct {
	db *gorm.DB
}

// NewTransferExportSource 构建调拨单行源（router 装配）。
func NewTransferExportSource(db *gorm.DB) datax.ExportSource {
	return &TransferExportSource{db: db}
}

// Columns 导出列。
func (s *TransferExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "transfer_no", Title: "调拨单号", Width: 20},
		{Key: "tr_type", Title: "调拨类型", Width: 12},
		{Key: "from_warehouse_id", Title: "调出仓ID", Type: datax.CellNumber},
		{Key: "to_warehouse_id", Title: "调入仓ID", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "remark", Title: "备注", Width: 24},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
		{Key: "received_at", Title: "收货时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 合计声明（无——行数在 items 维度）。
func (s *TransferExportSource) Summary() []datax.SummaryRow { return nil }

// applyFilters 筛选（白名单键 + 数据权限：调出仓或调入仓在范围内可见）。
func (s *TransferExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			q = q.Where("1 = 0")
		} else {
			q = q.Where("from_warehouse_id IN ? OR to_warehouse_id IN ?", f.WarehouseIDs, f.WarehouseIDs)
		}
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if tt := f.Filters["type"]; tt != "" {
		q = q.Where("type = ?", tt)
	}
	return applyCommon(q, f)
}

// Count 满足筛选的总行数。
func (s *TransferExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&TransferOrder{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *TransferExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&TransferOrder{}), f)
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
	var rows []*TransferOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.TransferNo), textCell(r.Type),
			numCell(float64(r.FromWarehouseID)), numCell(float64(r.ToWarehouseID)),
			textCell(r.Status), textCell(r.Remark),
			dateCell(r.CreatedAt), dateCell(r.ReceivedAt),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}

// ---- 盘点单行源（COUNT）----

// CountExportSource 盘点单行源。
type CountExportSource struct {
	db *gorm.DB
}

// NewCountExportSource 构建盘点单行源（router 装配）。
func NewCountExportSource(db *gorm.DB) datax.ExportSource {
	return &CountExportSource{db: db}
}

// Columns 导出列。
func (s *CountExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "count_no", Title: "盘点单号", Width: 20},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "remark", Title: "备注", Width: 24},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
		{Key: "completed_at", Title: "完成时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 合计声明（无）。
func (s *CountExportSource) Summary() []datax.SummaryRow { return nil }

// applyFilters 筛选（白名单键 + 数据权限）。
func (s *CountExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			q = q.Where("1 = 0")
		} else {
			q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
		}
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	return applyCommon(q, f)
}

// Count 满足筛选的总行数。
func (s *CountExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&CountOrder{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *CountExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&CountOrder{}), f)
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
	var rows []*CountOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.CountNo), numCell(float64(r.WarehouseID)),
			textCell(r.Status), textCell(r.Remark),
			dateCell(r.CreatedAt), dateCell(r.CompletedAt),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}
