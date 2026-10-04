package inventory

// inventory 域 datax 导出行源（backend-m3-plan §2.2 冻结新增文件；§6.1 导出 16 模块中
// INVENTORY / INVENTORY_LEDGER 两源）。只读本域表（§2.2 规则①；本包无任何写通路新增，
// plan §2.3 判据 3/判据 10）；keyset 游标 = 主键 id 升序；筛选键白名单（plan §18.5）；
// 仓库范围数据权限过滤（plan §13.6）。跨域名称列以 ID 呈现（只读本域表红线，
// 编码对照经 WAREHOUSE/LOCATION/SKU 模块导出）。

import (
	"context"
	"strconv"
	"time"

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

// applyWarehouseScope 仓库数据权限（fail-closed：指定范围空集 = 不可见任何行）。
func applyWarehouseScope(q *gorm.DB, f datax.ExportFilter, column string) *gorm.DB {
	if f.AllWarehouses {
		return q
	}
	if len(f.WarehouseIDs) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where(column+" IN ?", f.WarehouseIDs)
}

// applyCommon TIME_RANGE / SELECTED。
func applyCommon(q *gorm.DB, f datax.ExportFilter, timeColumn string) *gorm.DB {
	if f.TimeFrom != nil {
		q = q.Where(timeColumn+" >= ?", *f.TimeFrom)
	}
	if f.TimeTo != nil {
		q = q.Where(timeColumn+" <= ?", *f.TimeTo)
	}
	if len(f.IDs) > 0 {
		q = q.Where("id IN ?", f.IDs)
	}
	return q
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

func dateCell(t time.Time) datax.CellValue {
	if t.IsZero() {
		return datax.CellValue{T: datax.CellDate}
	}
	return datax.CellValue{T: datax.CellDate, D: t.Format("2006-01-02 15:04:05")}
}

// qtyFloat numeric(18,4) 文本 → 数值单元格（excel §2.3 禁全字符串导出）。
func qtyFloat(q stock.Qty) float64 {
	f, _ := strconv.ParseFloat(q.String(), 64)
	return f
}

// ---- 库存行源（INVENTORY）----

// InventoryExportSource 库存行源。
type InventoryExportSource struct {
	db *gorm.DB
}

// NewInventoryExportSource 构建库存行源（router 装配）。
func NewInventoryExportSource(db *gorm.DB) datax.ExportSource {
	return &InventoryExportSource{db: db}
}

// Columns 导出列。
func (s *InventoryExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "zone_id", Title: "库区ID", Type: datax.CellNumber},
		{Key: "shelf_id", Title: "货架ID", Type: datax.CellNumber},
		{Key: "bin_id", Title: "库位ID", Type: datax.CellNumber},
		{Key: "sku_id", Title: "SKU ID", Type: datax.CellNumber},
		{Key: "batch_id", Title: "批次ID", Type: datax.CellNumber},
		{Key: "total_qty", Title: "总量", Type: datax.CellNumber},
		{Key: "available_qty", Title: "可用", Type: datax.CellNumber},
		{Key: "locked_qty", Title: "锁定", Type: datax.CellNumber},
		{Key: "frozen_qty", Title: "冻结", Type: datax.CellNumber},
		{Key: "pending_inspect_qty", Title: "待检", Type: datax.CellNumber},
		{Key: "defective_qty", Title: "不良", Type: datax.CellNumber},
		{Key: "updated_at", Title: "更新时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 六状态数量合计（excel §2.3 总计/合计落位）。
func (s *InventoryExportSource) Summary() []datax.SummaryRow {
	return []datax.SummaryRow{
		{ColumnKey: "total_qty", Label: "合计"},
		{ColumnKey: "available_qty", Label: "合计"},
		{ColumnKey: "locked_qty", Label: "合计"},
		{ColumnKey: "frozen_qty", Label: "合计"},
		{ColumnKey: "pending_inspect_qty", Label: "合计"},
		{ColumnKey: "defective_qty", Label: "合计"},
	}
}

// applyFilters 筛选（白名单键 + 数据权限）。键清单与 StockListPage SfExportButton
// scopeParams 对齐（warehouse_id/zone_id/shelf_id/bin_id/sku_id/batch_id）——此前
// zone_id/shelf_id/batch_id 按"未知键忽略"静默丢弃，列表所见与导出所得不一致
// （2026-10-04 补齐）。
func (s *InventoryExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	q = applyWarehouseScope(q, f, "warehouse_id")
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	if zone := f.Filters["zone_id"]; zone != "" {
		q = q.Where("zone_id = ?", zone)
	}
	if shelf := f.Filters["shelf_id"]; shelf != "" {
		q = q.Where("shelf_id = ?", shelf)
	}
	if sku := f.Filters["sku_id"]; sku != "" {
		q = q.Where("sku_id = ?", sku)
	}
	if bin := f.Filters["bin_id"]; bin != "" {
		q = q.Where("bin_id = ?", bin)
	}
	if batch := f.Filters["batch_id"]; batch != "" {
		q = q.Where("batch_id = ?", batch)
	}
	return applyCommon(q, f, "updated_at")
}

// Count 满足筛选的总行数。
func (s *InventoryExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&Inventory{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *InventoryExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&Inventory{}), f)
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
	var rows []*Inventory
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			numCell(float64(r.WarehouseID)), numCell(float64(r.ZoneID)),
			numCell(float64(r.ShelfID)), numCell(float64(r.BinID)),
			numCell(float64(r.SKUID)), numCell(float64(r.BatchID)),
			numCell(qtyFloat(r.TotalQty)), numCell(qtyFloat(r.AvailableQty)), numCell(qtyFloat(r.LockedQty)),
			numCell(qtyFloat(r.FrozenQty)), numCell(qtyFloat(r.PendingInspectQty)), numCell(qtyFloat(r.DefectiveQty)),
			dateCell(r.UpdatedAt.Time),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}

// ---- 库存流水行源（INVENTORY_LEDGER）----

// LedgerExportSource 库存流水行源（append-only 只读，database.md §7）。
type LedgerExportSource struct {
	db *gorm.DB
}

// NewLedgerExportSource 构建流水行源（router 装配）。
func NewLedgerExportSource(db *gorm.DB) datax.ExportSource {
	return &LedgerExportSource{db: db}
}

// Columns 导出列。
func (s *LedgerExportSource) Columns() []datax.Column {
	return []datax.Column{
		{Key: "ledger_no", Title: "流水号", Width: 22},
		{Key: "sku_id", Title: "SKU ID", Type: datax.CellNumber},
		{Key: "warehouse_id", Title: "仓库ID", Type: datax.CellNumber},
		{Key: "bin_id", Title: "库位ID", Type: datax.CellNumber},
		{Key: "batch_id", Title: "批次ID", Type: datax.CellNumber},
		{Key: "serial_no", Title: "序列号", Width: 18},
		{Key: "change_type", Title: "变动类型", Width: 12},
		{Key: "business_type", Title: "业务类型", Width: 16},
		{Key: "business_no", Title: "业务单号", Width: 22},
		{Key: "status_from", Title: "前状态", Width: 16},
		{Key: "status_to", Title: "后状态", Width: 16},
		{Key: "qty_before", Title: "变动前", Type: datax.CellNumber},
		{Key: "qty_change", Title: "变动量", Type: datax.CellNumber},
		{Key: "qty_after", Title: "变动后", Type: datax.CellNumber},
		{Key: "operator_name", Title: "操作人", Width: 12},
		{Key: "created_at", Title: "时间", Type: datax.CellDate, Width: 20},
	}
}

// Summary 变动量合计。
func (s *LedgerExportSource) Summary() []datax.SummaryRow {
	return []datax.SummaryRow{{ColumnKey: "qty_change", Label: "合计"}}
}

// applyFilters 筛选（白名单键 + 数据权限）。
func (s *LedgerExportSource) applyFilters(q *gorm.DB, f datax.ExportFilter) *gorm.DB {
	q = applyWarehouseScope(q, f, "warehouse_id")
	if wh := f.Filters["warehouse_id"]; wh != "" {
		q = q.Where("warehouse_id = ?", wh)
	}
	if sku := f.Filters["sku_id"]; sku != "" {
		q = q.Where("sku_id = ?", sku)
	}
	if ct := f.Filters["change_type"]; ct != "" {
		q = q.Where("change_type = ?", ct)
	}
	if bn := f.Filters["business_no"]; bn != "" {
		q = q.Where("business_no = ?", bn)
	}
	return applyCommon(q, f, "created_at")
}

// Count 满足筛选的总行数。
func (s *LedgerExportSource) Count(ctx context.Context, f datax.ExportFilter) (int64, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&InventoryLedger{}), f)
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Batch 下一批（keyset：id > cursor 升序）。
func (s *LedgerExportSource) Batch(ctx context.Context, f datax.ExportFilter, c datax.Cursor) ([]datax.Row, datax.Cursor, error) {
	q := s.applyFilters(s.db.WithContext(ctx).Model(&InventoryLedger{}), f)
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
	var rows []*InventoryLedger
	if err := q.Order("id ASC").Limit(batchOf(f)).Find(&rows).Error; err != nil {
		return nil, c, err
	}
	out := make([]datax.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, datax.Row{Cells: []datax.CellValue{
			textCell(r.LedgerNo), numCell(float64(r.SKUID)), numCell(float64(r.WarehouseID)),
			numCell(float64(r.BinID)), numCell(float64(r.BatchID)), textCell(r.SerialNo),
			textCell(r.ChangeType), textCell(r.BusinessType), textCell(r.BusinessNo),
			textCell(r.StatusFrom), textCell(r.StatusTo),
			numCell(qtyFloat(r.QtyBefore)), numCell(qtyFloat(r.QtyChange)), numCell(qtyFloat(r.QtyAfter)),
			textCell(r.OperatorName), dateCell(r.CreatedAt.Time),
		}})
	}
	next := datax.Cursor{Done: true}
	if len(rows) > 0 && len(rows) == batchOf(f) {
		next = datax.Cursor{Value: strconv.FormatInt(rows[len(rows)-1].ID.Int64(), 10)}
	}
	return out, next, nil
}
