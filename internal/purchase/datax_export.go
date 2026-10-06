package purchase

// purchase 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// PURCHASE_ORDER / PURCHASE_INBOUND / QUALITY 三源）。只读本域表（§2.2 规则①）；
// keyset 游标 = 主键 id 升序（plan §6.3 禁止 offset 深翻页）；筛选键白名单（plan §18.5）。
// 跨域名称列（供应商/仓库/SKU）以 ID 呈现——编码对照经对应模块导出（只读本域表红线）。

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/stock"
)

// exportDB 行源共用查询句柄（GORM 只读；软删行由模型作用域自动排除）。
type exportDB struct {
	db *gorm.DB
}

// NewDataxExporter 构建行源查询底座（router 装配）。
func NewDataxExporter(db *gorm.DB) *exportDB { return &exportDB{db: db} }

// batchOf 每批行数（执行器注入 datax.export_batch_size；0 取缺省 500）。
func batchOf(f datax.ExportFilter) int {
	if f.BatchSize > 0 {
		return f.BatchSize
	}
	return 500
}

// applyCommon 通用筛选：TIME_RANGE / SELECTED。
func applyCommon(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
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

// pageOrKeyset CURRENT_PAGE 取窗口，其余按 keyset id>cursor 升序。
func pageOrKeyset(q *gorm.DB, f datax.ExportFilter, c datax.Cursor) (*gorm.DB, error) {
	if f.Scope == "CURRENT_PAGE" {
		if f.Page > 0 && f.PageSize > 0 {
			q = q.Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize)
		}
		return q, nil
	}
	if c.Value != "" {
		id, err := strconv.ParseInt(c.Value, 10, 64)
		if err != nil {
			return nil, &cursorError{msg: "导出游标非法: " + c.Value, cause: err}
		}
		q = q.Where("id > ?", id)
	}
	return q, nil
}

// keysetNext 游标推进（不足一批即结束）。
func keysetNext(rowsID int64, n, batch int) datax.Cursor {
	if n < batch {
		return datax.Cursor{Done: true}
	}
	return datax.Cursor{Value: strconv.FormatInt(rowsID, 10)}
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

// qtyFloat numeric(18,4) 文本 → 数值单元格（excel §2.3 禁全字符串导出；显示精度
// 4 位内无损，ParseFloat 取最接近表示——既有 binOccupancyBridge 同口径）。
func qtyFloat(q stock.Qty) float64 {
	f, _ := strconv.ParseFloat(q.String(), 64)
	return f
}

func textCell(v string) datax.CellValue { return datax.CellValue{T: datax.CellText, S: v} }

func numCell(v float64) datax.CellValue { return datax.CellValue{T: datax.CellNumber, N: v} }

func dateCell(t time.Time) datax.CellValue {
	if t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
}

// applyWarehouseScope 仓库数据权限（fail-closed：指定范围空集 = 不可见任何行；
// 与 inventory/stockops/sales/warehouse 行源同口径，plan §13.6）。此前 purchase 三行源
// 均未按 ExportFilter 仓库范围过滤（contract.go:181-183 契约要求），受限仓库数据权限的
// 用户导出范围会宽于其列表可见范围（列表经 applyScope 过滤）——2026-10-06 集成轮补齐。
func applyWarehouseScope(q *gorm.DB, f datax.ExportFilter, column string) *gorm.DB {
	if f.AllWarehouses {
		return q
	}
	if len(f.WarehouseIDs) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where(column+" IN ?", f.WarehouseIDs)
}

// ---- 采购订单行源（PURCHASE_ORDER）----

// POExportSource 采购订单行源。
type POExportSource struct{ ex *exportDB }

// NewPOExportSource 构建采购订单行源（router 装配）。
func NewPOExportSource(db *gorm.DB) datax.ExportSource {
	return &POExportSource{ex: NewDataxExporter(db)}
}

// Columns 导出列。
func (s *POExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "po_no", Title: "采购单号", Width: 20},
		{Key: "supplier_id", Title: "供应商ID", Type: datax.CellNumber},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "total_amount", Title: "订单总额", Type: datax.CellMoney},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "remark", Title: "备注", Width: 24},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 数值列合计（订单总额）。
func (s *POExportSource) Summary() []datax.SummaryRow {
	return []datax.SummaryRow{{ColumnKey: "total_amount", Label: "合计"}}
}

// Count 满足筛选的总行数。
func (s *POExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.ex.db.WithContext(ctx).Model(&PurchaseOrder{})
	q = s.applyFilters(q, f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// applyFilters 筛选（白名单键 + 数据权限）。键清单与 PurchaseListPage SfExportButton
// scopeParams 对齐（keyword/status/supplier_id/warehouse_id）——keyword/warehouse_id
// 此前按"未知键忽略"静默丢弃，导出范围会宽于当前视图（2026-10-06 集成轮补齐；
// 语义与列表 ListPOs 一致：keyword 按 po_no ILIKE 模糊，repository.go:294-309）。
func (s *POExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	q = applyWarehouseScope(q, f, "warehouse_id")
	if kw := likeEscape(f.Filters["keyword"]); kw != "" {
		q = q.Where("po_no ILIKE ?", "%"+kw+"%")
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if sup := f.Filters["supplier_id"]; sup != "" {
		q = q.Where("supplier_id = ?", sup)
	}
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	return applyCommon(q, f)
}

// Batch 下一批。
func (s *POExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q, err := pageOrKeyset(s.applyFilters(s.ex.db.WithContext(ctx).Model(&PurchaseOrder{}), f), f, c)
	if err != nil {
		return nil, c, err
	}
	var rows []*PurchaseOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.PONo), numCell(float64(r.SupplierID)), numCell(float64(r.WarehouseID)),
			numCell(qtyFloat(r.TotalAmount)), textCell(r.Status), textCell(r.Remark),
			dateCell(r.CreatedAt.Time),
		}})
	}
	var next datax.Cursor
	if len(rows) > 0 {
		next = keysetNext(rows[len(rows)-1].ID.Int64(), len(rows), batchOf(f))
	} else {
		next = datax.Cursor{Done: true}
	}
	return out, next, nil
}

// ---- 入库单行源（PURCHASE_INBOUND）----

// InboundExportSource 入库单行源。
type InboundExportSource struct{ ex *exportDB }

// NewInboundExportSource 构建入库单行源（router 装配）。
func NewInboundExportSource(db *gorm.DB) datax.ExportSource {
	return &InboundExportSource{ex: NewDataxExporter(db)}
}

// Columns 导出列。
func (s *InboundExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "inbound_no", Title: "入库单号", Width: 20},
		{Key: "source_type", Title: "来源类型", Width: 12},
		{Key: "source_no", Title: "来源单号", Width: 20},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
		{Key: "completed_at", Title: "完成时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 合计声明（无）。
func (s *InboundExportSource) Summary() []datax.SummaryRow { return nil }

// Count 满足筛选的总行数。
func (s *InboundExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.ex.db.WithContext(ctx).Model(&InboundOrder{})
	q = s.applyFilters(q, f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// applyFilters 筛选（白名单键 + 数据权限）。键清单与 InboundPage SfExportButton
// scopeParams 对齐（keyword/status/source_type/source_no/warehouse_id）——
// keyword/source_type/source_no 此前按"未知键忽略"静默丢弃，导出范围会宽于当前视图
// （2026-10-06 集成轮补齐；语义与列表 ListInbounds 一致：keyword 按 inbound_no/source_no
// ILIKE 模糊、source_no 精确匹配，repository.go:389-407）。
func (s *InboundExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	q = applyWarehouseScope(q, f, "warehouse_id")
	if kw := likeEscape(f.Filters["keyword"]); kw != "" {
		q = q.Where("inbound_no ILIKE ? OR source_no ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if stp := f.Filters["source_type"]; stp != "" {
		q = q.Where("source_type = ?", stp)
	}
	if sn := f.Filters["source_no"]; sn != "" {
		q = q.Where("source_no = ?", sn)
	}
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	return applyCommon(q, f)
}

// Batch 下一批。
func (s *InboundExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q, err := pageOrKeyset(s.applyFilters(s.ex.db.WithContext(ctx).Model(&InboundOrder{}), f), f, c)
	if err != nil {
		return nil, c, err
	}
	var rows []*InboundOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.InboundNo), textCell(r.SourceType), textCell(r.SourceNo),
			numCell(float64(r.WarehouseID)), textCell(r.Status),
			dateCell(r.CreatedAt.Time), dateCell(r.CompletedAt.Time),
		}})
	}
	var next datax.Cursor
	if len(rows) > 0 {
		next = keysetNext(rows[len(rows)-1].ID.Int64(), len(rows), batchOf(f))
	} else {
		next = datax.Cursor{Done: true}
	}
	return out, next, nil
}

// ---- 质检单行源（QUALITY）----

// QualityExportSource 质检单行源。
type QualityExportSource struct{ ex *exportDB }

// NewQualityExportSource 构建质检单行源（router 装配）。
func NewQualityExportSource(db *gorm.DB) datax.ExportSource {
	return &QualityExportSource{ex: NewDataxExporter(db)}
}

// Columns 导出列。
func (s *QualityExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "qc_no", Title: "质检单号", Width: 20},
		{Key: "source_type", Title: "来源类型", Width: 12},
		{Key: "source_no", Title: "来源单号", Width: 20},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "status", Title: "状态", Width: 18},
		{Key: "qty_inspected", Title: "已检数量", Type: datax.CellNumber},
		{Key: "qty_qualified", Title: "合格数量", Type: datax.CellNumber},
		{Key: "qty_defective", Title: "不良数量", Type: datax.CellNumber},
		{Key: "result", Title: "结论", Width: 10},
		{Key: "created_at", Title: "创建时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 数值列合计。
func (s *QualityExportSource) Summary() []datax.SummaryRow {
	return []datax.SummaryRow{
		{ColumnKey: "qty_inspected", Label: "合计"},
		{ColumnKey: "qty_qualified", Label: "合计"},
		{ColumnKey: "qty_defective", Label: "合计"},
	}
}

// Count 满足筛选的总行数。
func (s *QualityExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.ex.db.WithContext(ctx).Model(&QualityOrder{})
	q = s.applyFilters(q, f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// applyFilters 筛选（白名单键 + 数据权限）。键清单与 QualityInspectionListPage
// SfExportButton scopeParams 对齐（keyword/status/source_type/source_no/warehouse_id）——
// keyword/source_type/source_no 此前按"未知键忽略"静默丢弃，导出范围会宽于当前视图
// （2026-10-06 集成轮补齐；语义与列表 ListQCs 一致：keyword 按 qc_no/source_no ILIKE
// 模糊、source_no 精确匹配，repository.go:544-562）。
func (s *QualityExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	q = applyWarehouseScope(q, f, "warehouse_id")
	if kw := likeEscape(f.Filters["keyword"]); kw != "" {
		q = q.Where("qc_no ILIKE ? OR source_no ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if st := f.Filters["status"]; st != "" {
		q = q.Where("status = ?", st)
	}
	if stp := f.Filters["source_type"]; stp != "" {
		q = q.Where("source_type = ?", stp)
	}
	if sn := f.Filters["source_no"]; sn != "" {
		q = q.Where("source_no = ?", sn)
	}
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	return applyCommon(q, f)
}

// Batch 下一批。
func (s *QualityExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q, err := pageOrKeyset(s.applyFilters(s.ex.db.WithContext(ctx).Model(&QualityOrder{}), f), f, c)
	if err != nil {
		return nil, c, err
	}
	var rows []*QualityOrder
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.QCNo), textCell(r.SourceType), textCell(r.SourceNo),
			numCell(float64(r.WarehouseID)), textCell(r.Status),
			numCell(qtyFloat(r.QtyInspected)), numCell(qtyFloat(r.QtyQualified)),
			numCell(qtyFloat(r.QtyDefective)), textCell(r.Result), dateCell(r.CreatedAt.Time),
		}})
	}
	var next datax.Cursor
	if len(rows) > 0 {
		next = keysetNext(rows[len(rows)-1].ID.Int64(), len(rows), batchOf(f))
	} else {
		next = datax.Cursor{Done: true}
	}
	return out, next, nil
}
