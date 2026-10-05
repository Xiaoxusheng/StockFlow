package reports

// 聚合分析端点批·业务域十一端点（2026-10-05 分析卡片轮，docs/api.md §9 契约先行）：
//
//	GET /api/inbounds/status-composition   入库单状态构成
//	GET /api/inbounds/supplier-rank        供应商入库排行
//	GET /api/outbounds/completion-rate     出库订单完成率
//	GET /api/outbounds/product-rank        商品出库排行（估值口径随响应披露）
//	GET /api/warehouses/workload           仓库作业量
//	GET /api/purchases/analytics/trend     采购订单金额趋势
//	GET /api/purchases/supplier-rank       供应商采购排行
//	GET /api/purchases/status-composition  采购单状态构成
//	GET /api/sales/analytics/trend         销售订单金额趋势
//	GET /api/sales/product-rank            商品销售排行
//	GET /api/sales/status-composition      销售单状态构成
//
// 平台批（2026-10-05，docs/api.md §9 契约先行——task.ts 前端先行契约承接）：
//
//	GET /api/workbench/summary            工作台四块入口计数（dashboardTodayRepo 同源聚合）
//	GET /api/tasks                        我的任务列表（UNION ALL 三任务表明细化）
//
// 权限：inbounds/outbounds 四端点挂 PermReportRead（所在分析页既有趋势端点同码，
// 页面同权限面"菜单可见⟺数据可达"）；warehouses/workload 挂 auth.PermInventoryList
// （仓库分析页 warehouse-stock 同码）；purchases/sales 六端点挂域列表读权限
// auth.PermPurchaseList / auth.PermSalesList（新分析页独立权限面，current.md 挂账口径）；
// workbench/summary 与 /api/tasks 挂 auth.PermInventoryList（Dashboard/tasks 计数版
// 已在同码暴露同一信息面，明细化不抬高敏感面；不新增 task:* 域权限码——避免 seed
// 扩容与 DOMAIN_BOUND_RESOURCES 限定 fail-closed 恒隐身复发）。
// 跨域零 import：SQL 直连单据/任务表（本包为平台包，禁 import 各域包——repository.go:18 同口径）。
// 软删一致性：purchase_orders/inbound_orders/inbound_items 域模型内嵌 gorm.DeletedAt
// （迁移 000016），聚合 SQL 显式 deleted_at IS NULL 与列表页可见集对齐；sales/stockops
// 域单据任务表为显式字段无软删（000016 注），不加过滤。
// 数据范围：一律 Scope 会话仓库快照（scopeOf），禁收前端范围参数。
// 只读约束：全部为参数化 SELECT，零写语句（guard-readonly 红线，同 repository.go）。
// 免分页直出：TopN 行数≤limit≤50、趋势行数≤窗口天数≤366、状态构成≤CHECK 值域行数；
// /api/tasks 例外走统一分页（ParsePage/OKPage，明细化列表契约）。

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// ---------- DTO（snake_case 契约；SKUID/SupplierID 类字段显式 gorm column tag） ----------

// statusCountRow 状态构成行。
type statusCountRow struct {
	Status string `json:"status"`
	Count  int64  `gorm:"column:count" json:"count"`
}

// statusCompositionDTO 状态构成响应（items 仅含 count>0 态——GROUP BY 天然形态；
// total=全量单据数，Go 侧求和）。
type statusCompositionDTO struct {
	Total int64            `json:"total"`
	Items []statusCountRow `json:"items"`
}

// inboundSupplierRankRow GET /api/inbounds/supplier-rank 行（OTHER 来源入库无供应商
// 不入榜——口径披露见 docs/api.md §9）。
type inboundSupplierRankRow struct {
	SupplierID   int64   `gorm:"column:supplier_id" json:"supplier_id"`
	SupplierCode string  `json:"supplier_code"`
	SupplierName string  `json:"supplier_name"`
	InboundCount int64   `json:"inbound_count"`
	ReceivedQty  float64 `json:"received_qty"`
}

// completionRateDTO GET /api/outbounds/completion-rate 响应（口径：分母=非取消出库单；
// 分子=SHIPPED_ALL+CLOSED——CLOSED 差额关闭视为完成出库流程，迁移 000008:100 注释）。
type completionRateDTO struct {
	OrderTotal  int64 `gorm:"column:order_total" json:"order_total"`
	Cancelled   int64 `gorm:"column:cancelled" json:"cancelled"`
	Denominator int64 `gorm:"column:denominator" json:"denominator"`
	ShippedAll  int64 `gorm:"column:shipped_all" json:"shipped_all"`
	Closed      int64 `gorm:"column:closed" json:"closed"`
	InProgress  int64 `json:"in_progress"`
	// CompletionRate 完成率 = 分子/分母×100（0~100；分母 0 记 0——后端计算禁前端拼算）。
	CompletionRate float64 `json:"completion_rate"`
}

// outboundRankRow GET /api/outbounds/product-rank 行（OUTBOUND 流水估值口径）。
type outboundRankRow struct {
	SKUID       int64   `gorm:"column:sku_id" json:"sku_id"`
	SKUCode     string  `json:"sku_code"`
	SKUName     string  `json:"sku_name"`
	OutboundQty float64 `json:"outbound_qty"`
	Amount      float64 `json:"amount"`
}

// outboundProductRankDTO 商品出库排行响应（valuation.basis=sale_price 显式披露——
// 含采购退货出库等一切 OUTBOUND 扣减，与 outbound-stats 同口径可对账）。
type outboundProductRankDTO struct {
	Valuation flowValuationDTO  `json:"valuation"`
	Items     []outboundRankRow `json:"items"`
}

// warehouseWorkloadRow GET /api/warehouses/workload 行（零作业仓返回零行）。
type warehouseWorkloadRow struct {
	WarehouseID        int64   `gorm:"column:warehouse_id" json:"warehouse_id"`
	WarehouseCode      string  `json:"warehouse_code"`
	WarehouseName      string  `json:"warehouse_name"`
	InboundOrderCount  int64   `json:"inbound_order_count"`
	OutboundOrderCount int64   `json:"outbound_order_count"`
	InboundQty         float64 `json:"inbound_qty"`
	OutboundQty        float64 `json:"outbound_qty"`
}

// orderTrendRow 订单金额趋势行（date YYYY-MM-DD 字符串——dashboardTrendRow 先例）。
type orderTrendRow struct {
	Date       string  `json:"date"`
	OrderCount int64   `json:"order_count"`
	Amount     float64 `json:"amount"`
}

// orderTrendDTO 采购/销售订单金额趋势响应（metric=order_amount 显式披露——订单金额
// 口径，≠ inbound/outbound-stats 的流水估值口径）。
type orderTrendDTO struct {
	Metric string          `json:"metric"`
	Items  []orderTrendRow `json:"items"`
}

// purchaseSupplierRankRow GET /api/purchases/supplier-rank 行。
type purchaseSupplierRankRow struct {
	SupplierID   int64   `gorm:"column:supplier_id" json:"supplier_id"`
	SupplierCode string  `json:"supplier_code"`
	SupplierName string  `json:"supplier_name"`
	OrderCount   int64   `json:"order_count"`
	TotalAmount  float64 `json:"total_amount"`
}

// salesProductRankRow GET /api/sales/product-rank 行（qty=下单量、amount=行金额——
// 下单金额口径，非估值）。
type salesProductRankRow struct {
	SKUID      int64   `gorm:"column:sku_id" json:"sku_id"`
	SKUCode    string  `json:"sku_code"`
	SKUName    string  `json:"sku_name"`
	OrderCount int64   `json:"order_count"`
	Qty        float64 `json:"qty"`
	Amount     float64 `json:"amount"`
}

// ---------- Repository（只读参数化 SELECT） ----------

// statusComposition 单据状态构成（table/whCol/extra 均为代码常量，无用户输入拼接面；
// 排序 count DESC 图表友好，契约未冻结顺序）。
func (r *repository) statusComposition(ctx context.Context, table, whCol, extra string, sc Scope) (*statusCompositionDTO, error) {
	scCond, scArgs := sc.cond(whCol)
	where := scCond
	if extra != "" {
		where += " AND " + extra
	}
	sql := "SELECT status, COUNT(*)::bigint AS count FROM " + table +
		" WHERE " + where + " GROUP BY status ORDER BY count DESC, status"
	rows := make([]statusCountRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, scArgs...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 状态构成查询失败: %w", err)
	}
	out := &statusCompositionDTO{Items: rows}
	for _, it := range rows {
		out.Total += it.Count
	}
	return out, nil
}

// inboundSupplierRankBase 供应商入库排行：仅 PURCHASE 来源（ON 条件含 source_type
// 判定，OTHER 入库内联出局）经 source_no=po_no 关联 PO→供应商（printing_content.go:85
// 同构 JOIN）；po_no 全库唯一（uk_purchase_orders_no），JOIN 无行放大；窗口按
// inbound_orders.created_at；received_qty=Σ inbound_items.qty_received。
const inboundSupplierRankBase = `
SELECT po.supplier_id,
       COALESCE(sp.code, '') AS supplier_code,
       COALESCE(sp.name, '') AS supplier_name,
       COUNT(DISTINCT io.id)::bigint AS inbound_count,
       COALESCE(SUM(ii.qty_received), 0)::float8 AS received_qty
FROM inbound_orders io
JOIN purchase_orders po ON io.source_type = 'PURCHASE' AND po.po_no = io.source_no AND po.deleted_at IS NULL
LEFT JOIN suppliers sp ON sp.id = po.supplier_id
LEFT JOIN inbound_items ii ON ii.inbound_id = io.id AND ii.deleted_at IS NULL
WHERE io.deleted_at IS NULL AND io.created_at >= ? AND io.created_at < ? AND %s
GROUP BY po.supplier_id, sp.code, sp.name
ORDER BY received_qty DESC, po.supplier_id
LIMIT ?`

func (r *repository) inboundSupplierRank(ctx context.Context, sc Scope, from, to time.Time, limit int) ([]inboundSupplierRankRow, error) {
	scCond, scArgs := sc.cond("io.warehouse_id")
	sql := fmt.Sprintf(inboundSupplierRankBase, scCond)
	args := append(append([]any{from, to}, scArgs...), limit)
	rows := make([]inboundSupplierRankRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 供应商入库排行查询失败: %w", err)
	}
	return rows, nil
}

// outboundCompletion 出库完成率计数集（FILTER 条件聚合，一次扫描出全部分母/分子切片；
// outbound_orders 无软删列——000016 注）。
const outboundCompletionBase = `
SELECT COUNT(*)::bigint                                        AS order_total,
       COUNT(*) FILTER (WHERE status = 'CANCELLED')::bigint    AS cancelled,
       COUNT(*) FILTER (WHERE status <> 'CANCELLED')::bigint   AS denominator,
       COUNT(*) FILTER (WHERE status = 'SHIPPED_ALL')::bigint  AS shipped_all,
       COUNT(*) FILTER (WHERE status = 'CLOSED')::bigint       AS closed
FROM outbound_orders
WHERE created_at >= ? AND created_at < ? AND %s`

func (r *repository) outboundCompletion(ctx context.Context, sc Scope, from, to time.Time) (*completionRateDTO, error) {
	scCond, scArgs := sc.cond("warehouse_id")
	sql := fmt.Sprintf(outboundCompletionBase, scCond)
	row := &completionRateDTO{}
	if err := r.db.WithContext(ctx).Raw(sql, append([]any{from, to}, scArgs...)...).Scan(row).Error; err != nil {
		return nil, fmt.Errorf("reports: 出库完成率查询失败: %w", err)
	}
	row.InProgress = row.Denominator - row.ShippedAll - row.Closed
	if row.Denominator > 0 {
		row.CompletionRate = roundQty(float64(row.ShippedAll+row.Closed) / float64(row.Denominator) * 100)
	}
	return row, nil
}

// outboundProductRankBase 商品出库排行（OUTBOUND 流水估值：amount=Σ|qty_change|×
// sale_price，与 outbound-stats 同一估值面；含采购退货出库等一切 OUTBOUND 扣减）。
const outboundProductRankBase = `
SELECT l.sku_id, s.code AS sku_code, p.name AS sku_name,
       SUM(ABS(l.qty_change))::float8 AS outbound_qty,
       SUM(ABS(l.qty_change) * s.sale_price)::float8 AS amount
FROM inventory_ledgers l
JOIN skus s ON s.id = l.sku_id
JOIN products p ON p.id = s.product_id
WHERE l.change_type = 'OUTBOUND' AND l.created_at >= ? AND l.created_at < ? AND %s
GROUP BY l.sku_id, s.code, p.name
ORDER BY outbound_qty DESC, l.sku_id
LIMIT ?`

func (r *repository) outboundProductRank(ctx context.Context, sc Scope, from, to time.Time, limit int) ([]outboundRankRow, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	sql := fmt.Sprintf(outboundProductRankBase, scCond)
	args := append(append([]any{from, to}, scArgs...), limit)
	rows := make([]outboundRankRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 商品出库排行查询失败: %w", err)
	}
	return rows, nil
}

// warehouseWorkloadBase 仓库作业量（flowStats 同一数据面加仓库维度列）：
// 可见仓全集 LEFT JOIN（零作业仓返回零行，dashboardWarehouseStockRepo 全仓形态同构；
// 仓库集合同样受 Scope 约束——ALL 全量、SPECIFIED 仅范围内仓、空集 fail-closed 零行），
// 排序两 qty 之和 DESC。
const warehouseWorkloadBase = `
SELECT w.id AS warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       COALESCE(f.inbound_order_count, 0)::bigint AS inbound_order_count,
       COALESCE(f.outbound_order_count, 0)::bigint AS outbound_order_count,
       COALESCE(f.inbound_qty, 0)::float8 AS inbound_qty,
       COALESCE(f.outbound_qty, 0)::float8 AS outbound_qty
FROM warehouses w
LEFT JOIN (
    SELECT l.warehouse_id,
           COUNT(DISTINCT l.business_no) FILTER (WHERE l.change_type = 'INBOUND')::bigint AS inbound_order_count,
           COUNT(DISTINCT l.business_no) FILTER (WHERE l.change_type = 'OUTBOUND')::bigint AS outbound_order_count,
           COALESCE(SUM(ABS(l.qty_change)) FILTER (WHERE l.change_type = 'INBOUND'), 0)::float8 AS inbound_qty,
           COALESCE(SUM(ABS(l.qty_change)) FILTER (WHERE l.change_type = 'OUTBOUND'), 0)::float8 AS outbound_qty
    FROM inventory_ledgers l
    WHERE l.created_at >= ? AND l.created_at < ? AND %s
    GROUP BY 1
) f ON f.warehouse_id = w.id
WHERE w.deleted_at IS NULL AND %s
ORDER BY (COALESCE(f.inbound_qty, 0) + COALESCE(f.outbound_qty, 0)) DESC, w.id`

func (r *repository) warehouseWorkload(ctx context.Context, sc Scope, from, to time.Time) ([]warehouseWorkloadRow, error) {
	lCond, lArgs := sc.cond("l.warehouse_id")
	wCond, wArgs := sc.cond("w.id")
	sql := fmt.Sprintf(warehouseWorkloadBase, lCond, wCond)
	args := append(append([]any{from, to}, lArgs...), wArgs...)
	rows := make([]warehouseWorkloadRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 仓库作业量查询失败: %w", err)
	}
	return rows, nil
}

// orderTrendBase 订单金额趋势（表/排除态/附加过滤均为代码常量；date 用 to_char 落
// YYYY-MM-DD 字符串——裸 ::date 扫进 string 字段会被 database/sql 转 RFC3339Nano，
// 契约要求 'YYYY-MM-DD'，api.md §9 时间格式收敛口径）；amount=Σ total_amount 订单
// 金额口径，metric 显式披露≠流水估值。
const orderTrendBase = `
SELECT to_char(o.created_at::date, 'YYYY-MM-DD') AS date,
       COUNT(*)::bigint AS order_count,
       COALESCE(SUM(o.total_amount), 0)::float8 AS amount
FROM %s o
WHERE o.status NOT IN (%s) AND o.created_at >= ? AND o.created_at < ? AND %s AND %s
GROUP BY 1
ORDER BY 1`

func (r *repository) orderTrend(ctx context.Context, table, excludeStatus, extra string, sc Scope, from, to time.Time) ([]orderTrendRow, error) {
	scCond, scArgs := sc.cond("o.warehouse_id")
	sql := fmt.Sprintf(orderTrendBase, table, excludeStatus, scCond, extra)
	args := append(append([]any{}, from, to), scArgs...)
	rows := make([]orderTrendRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 订单金额趋势查询失败: %w", err)
	}
	return rows, nil
}

// purchaseSupplierRankBase 供应商采购排行（DRAFT 未提交/CANCELLED 已取消不计业绩；
// 排序 total_amount DESC）。
const purchaseSupplierRankBase = `
SELECT o.supplier_id,
       COALESCE(sp.code, '') AS supplier_code,
       COALESCE(sp.name, '') AS supplier_name,
       COUNT(*)::bigint AS order_count,
       COALESCE(SUM(o.total_amount), 0)::float8 AS total_amount
FROM purchase_orders o
LEFT JOIN suppliers sp ON sp.id = o.supplier_id
WHERE o.status NOT IN ('DRAFT', 'CANCELLED') AND o.deleted_at IS NULL
  AND o.created_at >= ? AND o.created_at < ? AND %s
GROUP BY o.supplier_id, sp.code, sp.name
ORDER BY total_amount DESC, o.supplier_id
LIMIT ?`

func (r *repository) purchaseSupplierRank(ctx context.Context, sc Scope, from, to time.Time, limit int) ([]purchaseSupplierRankRow, error) {
	scCond, scArgs := sc.cond("o.warehouse_id")
	sql := fmt.Sprintf(purchaseSupplierRankBase, scCond)
	args := append(append([]any{from, to}, scArgs...), limit)
	rows := make([]purchaseSupplierRankRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 供应商采购排行查询失败: %w", err)
	}
	return rows, nil
}

// salesProductRankBase 商品销售排行（sales_order_items.so_id 关联；qty=Σ items.qty
// 下单量、amount=Σ items.amount 行金额——下单金额非估值；ORDER BY 段按 sort 白名单
// 二选一，无注入面）。
const salesProductRankBase = `
SELECT i.sku_id, s.code AS sku_code, p.name AS sku_name,
       COUNT(DISTINCT o.id)::bigint AS order_count,
       COALESCE(SUM(i.qty), 0)::float8 AS qty,
       COALESCE(SUM(i.amount), 0)::float8 AS amount
FROM sales_order_items i
JOIN sales_orders o ON o.id = i.so_id
JOIN skus s ON s.id = i.sku_id
JOIN products p ON p.id = s.product_id
WHERE o.status NOT IN ('DRAFT', 'REJECTED', 'CANCELLED') AND o.created_at >= ? AND o.created_at < ? AND %s
GROUP BY i.sku_id, s.code, p.name
ORDER BY %s DESC, i.sku_id
LIMIT ?`

func (r *repository) salesProductRank(ctx context.Context, sc Scope, sort string, from, to time.Time, limit int) ([]salesProductRankRow, error) {
	scCond, scArgs := sc.cond("o.warehouse_id")
	sql := fmt.Sprintf(salesProductRankBase, scCond, sort)
	args := append(append([]any{from, to}, scArgs...), limit)
	rows := make([]salesProductRankRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 商品销售排行查询失败: %w", err)
	}
	return rows, nil
}

// ---------- Service ----------

// InboundStatusComposition 入库单状态构成（全量现状分布，无时间窗口）。
func (s *Service) InboundStatusComposition(ctx context.Context, sc Scope) (*statusCompositionDTO, error) {
	return s.repo.statusComposition(ctx, "inbound_orders", "warehouse_id", "deleted_at IS NULL", sc)
}

// InboundSupplierRank 供应商入库排行（窗口按入库单 created_at；OTHER 来源不入榜）。
func (s *Service) InboundSupplierRank(ctx context.Context, sc Scope, from, to time.Time, limit int) ([]inboundSupplierRankRow, error) {
	return s.repo.inboundSupplierRank(ctx, sc, from, to, limit)
}

// OutboundCompletionRate 出库订单完成率（口径与比率均在后端计算）。
func (s *Service) OutboundCompletionRate(ctx context.Context, sc Scope, from, to time.Time) (*completionRateDTO, error) {
	return s.repo.outboundCompletion(ctx, sc, from, to)
}

// OutboundProductRank 商品出库排行（估值口径 sale_price 随响应披露）。
func (s *Service) OutboundProductRank(ctx context.Context, sc Scope, from, to time.Time, limit int) (*outboundProductRankDTO, error) {
	rows, err := s.repo.outboundProductRank(ctx, sc, from, to, limit)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].OutboundQty = roundQty(rows[i].OutboundQty)
		rows[i].Amount = roundQty(rows[i].Amount)
	}
	return &outboundProductRankDTO{
		Valuation: flowValuationDTO{Basis: flowValuationBasis(false)},
		Items:     rows,
	}, nil
}

// WarehouseWorkload 仓库作业量（可见仓全集，零作业仓返回零行）。
func (s *Service) WarehouseWorkload(ctx context.Context, sc Scope, from, to time.Time) ([]warehouseWorkloadRow, error) {
	rows, err := s.repo.warehouseWorkload(ctx, sc, from, to)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].InboundQty = roundQty(rows[i].InboundQty)
		rows[i].OutboundQty = roundQty(rows[i].OutboundQty)
	}
	return rows, nil
}

// PurchaseTrend 采购订单金额趋势（status NOT IN ('DRAFT','CANCELLED')）。
func (s *Service) PurchaseTrend(ctx context.Context, sc Scope, from, to time.Time) (*orderTrendDTO, error) {
	rows, err := s.repo.orderTrend(ctx, "purchase_orders", "'DRAFT', 'CANCELLED'", "o.deleted_at IS NULL", sc, from, to)
	if err != nil {
		return nil, err
	}
	return &orderTrendDTO{Metric: "order_amount", Items: rows}, nil
}

// PurchaseSupplierRank 供应商采购排行（状态过滤同 PurchaseTrend）。
func (s *Service) PurchaseSupplierRank(ctx context.Context, sc Scope, from, to time.Time, limit int) ([]purchaseSupplierRankRow, error) {
	return s.repo.purchaseSupplierRank(ctx, sc, from, to, limit)
}

// PurchaseStatusComposition 采购单状态构成（七态 CHECK 值域，迁移 000007:35）。
func (s *Service) PurchaseStatusComposition(ctx context.Context, sc Scope) (*statusCompositionDTO, error) {
	return s.repo.statusComposition(ctx, "purchase_orders", "warehouse_id", "deleted_at IS NULL", sc)
}

// SalesTrend 销售订单金额趋势（status NOT IN ('DRAFT','REJECTED','CANCELLED')；
// 订单金额口径——页面须标注"订单金额口径，非流水估值"）。
func (s *Service) SalesTrend(ctx context.Context, sc Scope, from, to time.Time) (*orderTrendDTO, error) {
	rows, err := s.repo.orderTrend(ctx, "sales_orders", "'DRAFT', 'REJECTED', 'CANCELLED'", "1 = 1", sc, from, to)
	if err != nil {
		return nil, err
	}
	return &orderTrendDTO{Metric: "order_amount", Items: rows}, nil
}

// SalesProductRank 商品销售排行（sort=qty|amount，缺省 qty）。
func (s *Service) SalesProductRank(ctx context.Context, sc Scope, sort string, from, to time.Time, limit int) ([]salesProductRankRow, error) {
	return s.repo.salesProductRank(ctx, sc, sort, from, to, limit)
}

// SalesStatusComposition 销售单状态构成（八态 CHECK 值域，迁移 000008:36）。
func (s *Service) SalesStatusComposition(ctx context.Context, sc Scope) (*statusCompositionDTO, error) {
	return s.repo.statusComposition(ctx, "sales_orders", "warehouse_id", "1 = 1", sc)
}

// ---------- Handler ----------

// parseLimitQuery TopN/排行 limit 参数（缺省 10，1–50）；非法写 400 响应并返回 ok=false
// （调用方必须终止）。
func parseLimitQuery(c *gin.Context) (int, bool) {
	const (
		defLimit = 10
		minLimit = 1
		maxLimit = 50
	)
	if c.Query("limit") == "" {
		return defLimit, true
	}
	n, ok := parseInt64Query(c, "limit")
	if !ok {
		return 0, false
	}
	if n < minLimit || n > maxLimit {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "limit", "reason": fmt.Sprintf("必须为 %d-%d 的整数", minLimit, maxLimit),
		}))
		return 0, false
	}
	return int(n), true
}

// @Summary GET /api/inbounds/status-composition
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/inbounds/status-composition [get]
func (h *handler) inboundStatusComposition(c *gin.Context) {
	dto, err := h.svc.InboundStatusComposition(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/inbounds/supplier-rank
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Param limit query int false "返回条数（缺省 10，1–50）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inbounds/supplier-rank [get]
func (h *handler) inboundSupplierRank(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	limit, ok := parseLimitQuery(c)
	if !ok {
		return
	}
	rows, err := h.svc.InboundSupplierRank(c.Request.Context(), scopeOf(c), from, to, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/outbounds/completion-rate
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/completion-rate [get]
func (h *handler) outboundCompletionRate(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	dto, err := h.svc.OutboundCompletionRate(c.Request.Context(), scopeOf(c), from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/outbounds/product-rank
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Param limit query int false "返回条数（缺省 10，1–50）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/outbounds/product-rank [get]
func (h *handler) outboundProductRank(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	limit, ok := parseLimitQuery(c)
	if !ok {
		return
	}
	dto, err := h.svc.OutboundProductRank(c.Request.Context(), scopeOf(c), from, to, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/warehouses/workload
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/warehouses/workload [get]
func (h *handler) warehouseWorkload(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	rows, err := h.svc.WarehouseWorkload(c.Request.Context(), scopeOf(c), from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/purchases/analytics/trend
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/analytics/trend [get]
func (h *handler) purchaseTrend(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	dto, err := h.svc.PurchaseTrend(c.Request.Context(), scopeOf(c), from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/purchases/supplier-rank
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Param limit query int false "返回条数（缺省 10，1–50）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/purchases/supplier-rank [get]
func (h *handler) purchaseSupplierRank(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	limit, ok := parseLimitQuery(c)
	if !ok {
		return
	}
	rows, err := h.svc.PurchaseSupplierRank(c.Request.Context(), scopeOf(c), from, to, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/purchases/status-composition
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/purchases/status-composition [get]
func (h *handler) purchaseStatusComposition(c *gin.Context) {
	dto, err := h.svc.PurchaseStatusComposition(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/sales/analytics/trend
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/analytics/trend [get]
func (h *handler) salesTrend(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	dto, err := h.svc.SalesTrend(c.Request.Context(), scopeOf(c), from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/sales/product-rank
// @Tags 报表
// @Produce json
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Param sort query string false "排序键 qty|amount（缺省 qty）"
// @Param limit query int false "返回条数（缺省 10，1–50）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/sales/product-rank [get]
func (h *handler) salesProductRank(c *gin.Context) {
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	sort := c.Query("sort")
	if sort == "" {
		sort = "qty"
	}
	if sort != "qty" && sort != "amount" {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "sort", "reason": "必须为 qty|amount"}))
		return
	}
	limit, ok := parseLimitQuery(c)
	if !ok {
		return
	}
	rows, err := h.svc.SalesProductRank(c.Request.Context(), scopeOf(c), sort, from, to, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/sales/status-composition
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/sales/status-composition [get]
func (h *handler) salesStatusComposition(c *gin.Context) {
	dto, err := h.svc.SalesStatusComposition(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// myTaskItemDTO GET /api/tasks 行（web/src/api/task.ts TaskItem 契约 + raw_status 原表态）。
type myTaskItemDTO struct {
	ID            int64   `gorm:"column:id" json:"id"`
	TaskNo        string  `json:"task_no"`
	TaskType      string  `json:"task_type"`
	SourceNo      string  `json:"source_no"`
	WarehouseName string  `json:"warehouse_name"`
	TotalQty      float64 `json:"total_qty"`
	CompletedQty  float64 `json:"completed_qty"`
	// Status 统一五值：pending/in_progress/completed/cancelled/exception。
	Status string `json:"status"`
	// RawStatus 原表态（putaway_tasks/pick_tasks/check_tasks 的 status 原值）。
	RawStatus string `gorm:"column:raw_status" json:"raw_status"`
	// AssigneeName 负责人（我的任务=指派给当前用户的子集；putaway 经 JOIN users
	// 取 real_name，pick/check 表内冗余直取）。
	AssigneeName string            `json:"assignee_name"`
	CreatedAt    database.JSONTime `json:"created_at"`
	// CompletedAt 完成时间（putaway→completed_at、picking→picked_at、checking→done_at；
	// 未完成为 null）。
	CompletedAt *database.JSONTime `json:"completed_at"`
}

// 我的三类任务分支（UNION ALL；统一 status 五值映射在分支内完成，raw_status 留原态；
// %1=仓库范围片段 %2=仓库编码片段——均为 Scope/代码常量拼接面，assignee 占位符为字面 ?）。
const (
	myTasksPutawayBranch = `
SELECT pt.id, pt.putaway_no AS task_no, 'putaway' AS task_type,
       pt.inbound_no AS source_no, w.name AS warehouse_name,
       pt.qty::float8 AS total_qty,
       CASE WHEN pt.status = 'COMPLETED' THEN pt.qty::float8 ELSE 0 END AS completed_qty,
       CASE pt.status WHEN 'PENDING' THEN 'pending' WHEN 'IN_PROGRESS' THEN 'in_progress'
                      WHEN 'PAUSED' THEN 'in_progress' WHEN 'COMPLETED' THEN 'completed'
                      WHEN 'CANCELLED' THEN 'cancelled' END AS status,
       pt.status AS raw_status, COALESCE(u.real_name, '') AS assignee_name,
       pt.created_at, pt.completed_at
FROM putaway_tasks pt
JOIN warehouses w ON w.id = pt.target_warehouse_id
LEFT JOIN users u ON u.id = pt.claimed_by
WHERE pt.claimed_by = ? AND %s AND %s`
	myTasksPickBranch = `
SELECT pk.id, pk.pick_no AS task_no, 'picking' AS task_type,
       pk.outbound_no AS source_no, w.name AS warehouse_name,
       pk.qty::float8 AS total_qty, pk.picked_qty::float8 AS completed_qty,
       CASE pk.status WHEN 'PENDING' THEN 'pending' WHEN 'CLAIMED' THEN 'in_progress'
                      WHEN 'PICKING' THEN 'in_progress' WHEN 'PICKED' THEN 'completed'
                      WHEN 'EXCEPTION' THEN 'exception' WHEN 'CANCELLED' THEN 'cancelled' END AS status,
       pk.status AS raw_status, pk.assignee_name, pk.created_at, pk.picked_at AS completed_at
FROM pick_tasks pk
JOIN warehouses w ON w.id = pk.warehouse_id
WHERE pk.assignee_id = ? AND %s AND %s`
	myTasksCheckBranch = `
SELECT ck.id, ck.check_no AS task_no, 'checking' AS task_type,
       ck.outbound_no AS source_no, w.name AS warehouse_name,
       ck.qty::float8 AS total_qty,
       CASE WHEN ck.status = 'DONE' THEN ck.qty::float8 ELSE 0 END AS completed_qty,
       CASE ck.status WHEN 'PENDING' THEN 'pending' WHEN 'DONE' THEN 'completed'
                      WHEN 'EXCEPTION' THEN 'exception' END AS status,
       ck.status AS raw_status, ck.assignee_name, ck.created_at, ck.done_at AS completed_at
FROM check_tasks ck
JOIN warehouses w ON w.id = ck.warehouse_id
WHERE ck.assignee_id = ? AND %s AND %s`
)

// taskBranchSpec 三类任务分支的仓储 SQL 与仓库范围列（type 过滤经 taskBranchSpecs 选取）。
type taskBranchSpec struct {
	sql      string
	scopeCol string
}

// taskBranchIndex task_type → 分支下标（handler 白名单已保证取值；-1 不可达防御）。
func taskBranchIndex(taskType string) int {
	switch taskType {
	case "putaway":
		return 0
	case "picking":
		return 1
	case "checking":
		return 2
	}
	return -1
}

// taskBranchSpecs task_type 过滤的分支选取：空=全部三分支 UNION；指定类型=恰一分支
// （bug 修复 2026-10-05 独立复核发现①：此前 [idx:] 截头不截尾，putaway 误带 picking/
// checking 两分支——单测 taskBranchSpecs 断言冻结）。
func taskBranchSpecs(taskType string) []taskBranchSpec {
	all := []taskBranchSpec{
		{myTasksPutawayBranch, "pt.target_warehouse_id"},
		{myTasksPickBranch, "pk.warehouse_id"},
		{myTasksCheckBranch, "ck.warehouse_id"},
	}
	if taskType == "" {
		return all
	}
	idx := taskBranchIndex(taskType)
	return []taskBranchSpec{all[idx]}
}

// myTasks 我的任务列表（UNION ALL 三表 + 统一状态过滤 + ORDER BY created_at DESC 分页；
// assignee 恒=当前用户——未领取任务不入『我的任务』，无放宽参数）。
func (r *repository) myTasks(ctx context.Context, sc Scope, userID int64, taskType, status, warehouseCode string, page, pageSize int) ([]myTaskItemDTO, int64, error) {
	branches := taskBranchSpecs(taskType)
	branchArgs := []any{}
	sqls := make([]string, 0, len(branches))
	for _, b := range branches {
		scCond, scArgs := sc.cond(b.scopeCol)
		whCond, whArgs := "1 = 1", []any{}
		if warehouseCode != "" {
			whCond, whArgs = "w.code = ?", []any{warehouseCode}
		}
		sqls = append(sqls, fmt.Sprintf(b.sql, scCond, whCond))
		branchArgs = append(branchArgs, userID)
		branchArgs = append(branchArgs, scArgs...)
		branchArgs = append(branchArgs, whArgs...)
	}
	union := strings.Join(sqls, " UNION ALL ")
	// 统一状态过滤在外层（status 为分支内映射产物）
	statusCond := "1 = 1"
	statusArgs := []any{}
	if status != "" {
		statusCond = "t.status = ?"
		statusArgs = []any{status}
	}
	var total int64
	countSQL := "SELECT COUNT(*) FROM (" + union + ") t WHERE " + statusCond
	if err := r.db.WithContext(ctx).Raw(countSQL, append(append([]any{}, branchArgs...), statusArgs...)...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("reports: 我的任务计数失败: %w", err)
	}
	listSQL := "SELECT * FROM (" + union + ") t WHERE " + statusCond + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := append(append([]any{}, branchArgs...), statusArgs...)
	args = append(args, pageSize, (page-1)*pageSize)
	items := make([]myTaskItemDTO, 0)
	if err := r.db.WithContext(ctx).Raw(listSQL, args...).Scan(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("reports: 我的任务查询失败: %w", err)
	}
	return items, total, nil
}

// MyTasks 我的任务列表（服务层透传；分页/过滤在仓储完成）。
func (s *Service) MyTasks(ctx context.Context, sc Scope, userID int64, taskType, status, warehouseCode string, page, pageSize int) ([]myTaskItemDTO, int64, error) {
	return s.repo.myTasks(ctx, sc, userID, taskType, status, warehouseCode, page, pageSize)
}

// validTaskType /api/tasks 的 task_type 白名单（packing/moving/counting 不映射——
// 无独立任务表承载，传值 400 invalidParam，rulings 裁决）。
func validTaskType(t string) bool {
	return t == "putaway" || t == "picking" || t == "checking"
}

// validUnifiedTaskStatus /api/tasks 的统一五值状态白名单。
func validUnifiedTaskStatus(s string) bool {
	switch s {
	case "pending", "in_progress", "completed", "cancelled", "exception":
		return true
	}
	return false
}

// myTasks GET /api/tasks（assignee 恒=当前用户；RequirePerm 已保证认证上下文，
// CurrentUser 缺失为编程错误防御分支 fail-closed）。
//
// @Summary GET /api/tasks
// @Tags 报表
// @Produce json
// @Param page query int false "页码（缺省 1）"
// @Param pageSize query int false "页大小（缺省 20，1-100）"
// @Param task_type query string false "任务类型 putaway|picking|checking（可选）"
// @Param status query string false "统一状态 pending|in_progress|completed|cancelled|exception（可选）"
// @Param warehouse_code query string false "仓库编码（可选）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/tasks [get]
func (h *handler) myTasks(c *gin.Context) {
	page, pageSize, err := response.ParsePage(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	taskType := c.Query("task_type")
	if taskType != "" && !validTaskType(taskType) {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "task_type", "reason": "必须为 putaway|picking|checking（packing/moving/counting 暂不映射）",
		}))
		return
	}
	status := c.Query("status")
	if status != "" && !validUnifiedTaskStatus(status) {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{
			"field": "status", "reason": "必须为 pending|in_progress|completed|cancelled|exception",
		}))
		return
	}
	uc, ok := auth.CurrentUser(c)
	if !ok {
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	items, total, err := h.svc.MyTasks(c.Request.Context(), scopeOf(c), uc.UserID,
		taskType, status, c.Query("warehouse_code"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	listOut(c, items, page, pageSize, total)
}
