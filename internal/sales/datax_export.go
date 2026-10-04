package sales

// sales 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// SALES_OUTBOUND 一源）。只读本域表（§2.2 规则①）；keyset 游标 = 主键 id 升序；
// 筛选键白名单（plan §18.5：未知键忽略）；数据权限按仓库范围过滤（plan §13.6）。

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

// OutboundExportSource 出库单行源。
type OutboundExportSource struct {
	db *gorm.DB
}

// NewOutboundExportSource 构建出库单行源（router 装配）。
func NewOutboundExportSource(db *gorm.DB) datax.ExportSource {
	return &OutboundExportSource{db: db}
}

// Columns 导出列。
func (s *OutboundExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "outbound_no", Title: "出库单号", Width: 20},
		{Key: "so_no", Title: "销售单号", Width: 20},
		{Key: "ob_type", Title: "出库类型", Width: 12},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "remark", Title: "备注", Width: 24},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
		{Key: "shipped_at", Title: "发货时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 合计声明（无）。
func (s *OutboundExportSource) Summary() []datax.SummaryRow { return nil }

// applyFilters 通用筛选（白名单键 + TIME_RANGE + SELECTED + 仓库数据权限）。
func (s *OutboundExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	if !f.AllWarehouses {
		if len(f.WarehouseIDs) == 0 {
			q = q.Where("1 = 0") // fail-closed
		} else {
			q = q.Where("warehouse_id IN ?", f.WarehouseIDs)
		}
	}
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if so := f.Filters["so_no"]; so != "" {
		q = q.Where("so_no = ?", so)
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
func (s *OutboundExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&OutboundOrder{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *OutboundExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&OutboundOrder{}), f)
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
	var rows []*OutboundOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.OutboundNo), textCell(r.SoNo), textCell(r.Type),
			numCell(float64(r.WarehouseID)), textCell(r.Status), textCell(r.Remark),
			dateCell(r.CreatedAt.Time), dateCell(r.ShippedAt.Time),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}

// ---- 单元格值构造（本文件行源共享）----

func textCell(v string) datax.CellValue { return datax.CellValue{T: datax.CellText, S: v} }

func numCell(v float64) datax.CellValue { return datax.CellValue{T: datax.CellNumber, N: v} }

func dateCell(t interface {
	IsZero() bool
	Format(string) string
}) datax.CellValue {
	if t == nil || t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
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
