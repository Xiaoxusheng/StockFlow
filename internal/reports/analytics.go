package reports

// 聚合分析端点批·库存域三端点（2026-10-05 分析卡片轮，docs/api.md §9 契约先行）：
//
//	GET /api/inventory/sku-top          SKU 库存 TOP N（metric=qty|value；limit 1–50）
//	GET /api/inventory/turnover-trend   库存周转趋势（days 1–366，日粒度连续序列）
//	GET /api/reports/flow-trend         出入库流水趋势（type 必填；flowStats 去分页版）
//
// 权限：sku-top/turnover-trend 挂 auth.PermInventoryList（消费方库存分析页——
// /api/inventory/analytics 同码先例）；flow-trend 挂 PermReportRead（ReportFlowStats
// 页既有 inbound/outbound-stats 同码）。
// 数据范围：一律 Scope 会话仓库快照（scopeOf），禁收前端范围参数。
// 只读约束：全部为参数化 SELECT，零写语句（guard-readonly 红线，同 repository.go）。
// 免分页直出：TopN 行数≤limit≤50、趋势行数≤366——response.OK 直出，不走 ParsePage
// （MaxPageSize=100 下长窗口趋势必须免分页，api.md §9 report-flowstats-trend-cap 销项）。

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/response"
)

// ---------- DTO（snake_case 契约；SKUID 类字段显式 gorm column tag——sk_uid 映射缺陷教训） ----------

// skuTopRow GET /api/inventory/sku-top 行。
type skuTopRow struct {
	SKUID   int64  `gorm:"column:sku_id" json:"sku_id"`
	SKUCode string `json:"sku_code"`
	SKUName string `json:"sku_name"`
	// TotalQty 现存量合计（HAVING SUM(total_qty)>0——只看在库 SKU）。
	TotalQty float64 `json:"total_qty"`
	// StockValue 库存金额（成本价口径：批次成本价优先，非批次 SKU 用 SKU 成本价——
	// 与 inventory-summary 同源 repository.go:93）。
	StockValue float64 `json:"stock_value"`
}

// turnoverTrendRow GET /api/inventory/turnover-trend 行（date YYYY-MM-DD 字符串——
// dashboardTrendRepo/analyticsTrendRow 先例）。
type turnoverTrendRow struct {
	Date string `json:"date"`
	// OutboundQty 当日 Σ|OUTBOUND 流水量|。
	OutboundQty float64 `json:"outbound_qty"`
	// EndQty 日末现存量（锚点回推：锚点 − 该日之后 stockLevelTypes 净变化）。
	EndQty float64 `json:"end_qty"`
	// AvgInventory 平均库存 =（日初 + 日末）/2；日初 = 日末 − 当日净变化。
	AvgInventory float64 `json:"avg_inventory"`
	// TurnoverRate 周转率 = outbound_qty/avg_inventory（avg≤0 记 0——不造假分母）。
	TurnoverRate float64 `json:"turnover_rate"`
}

// flowValuationDTO 估值口径披露块（与 flowStats handler 同形：basis 随响应显式下发）。
type flowValuationDTO struct {
	Basis string `json:"basis"`
}

// flowTrendDTO GET /api/reports/flow-trend 响应（items 复用 FlowStatRow——与既有
// inbound/outbound-stats 行形逐字段一致，stat_date JSONTime 收敛口径不变）。
type flowTrendDTO struct {
	Valuation flowValuationDTO `json:"valuation"`
	Items     []FlowStatRow    `json:"items"`
}

// ---------- Repository（只读参数化 SELECT） ----------

// skuTopBase 金额口径与 inventorySummaryBase 同源（批次成本价优先，repository.go:93）。
// ORDER BY 段按 metric 二选一（qty→total_qty / value→stock_value，均为代码常量白名单，
// 无注入面）；LIMIT 兜底 TopN 扫描面。
const skuTopBase = `
SELECT i.sku_id, s.code AS sku_code, p.name AS sku_name,
       SUM(i.total_qty)::float8 AS total_qty,
       SUM(i.total_qty * COALESCE(b.cost_price, s.cost_price, 0))::float8 AS stock_value
FROM inventory i
JOIN skus s ON s.id = i.sku_id
JOIN products p ON p.id = s.product_id
LEFT JOIN batches b ON b.id = i.batch_id AND i.batch_id > 0
WHERE %s AND %s
GROUP BY i.sku_id, s.code, p.name
HAVING SUM(i.total_qty) > 0
ORDER BY %s, i.sku_id
LIMIT ?`

func (r *repository) skuTop(ctx context.Context, sc Scope, metric string, limit int, warehouseID int64) ([]skuTopRow, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	extra, eArgs := "1 = 1", []any{}
	if warehouseID > 0 {
		extra, eArgs = "i.warehouse_id = ?", []any{warehouseID}
	}
	order := "total_qty DESC"
	if metric == "value" {
		order = "stock_value DESC"
	}
	sql := fmt.Sprintf(skuTopBase, scCond, extra, order)
	args := append(append(append([]any{}, scArgs...), eArgs...), limit)
	// 预置非 nil 空 slice：nil slice 序列化为 items:null（api.md §9 教训）。
	rows := make([]skuTopRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: SKU TOP 查询失败: %w", err)
	}
	return rows, nil
}

// turnoverTrendRepo 周转趋势（日粒度连续序列，dashboardTrendRepo 同法）：
//
//	日末库存(day) = 现存量锚点 − (day, now] 净变化（netAfterByDay 复用）
//	日初库存(day) = 现存量锚点 − [day, now] 净变化 = 日末 − 当日净变化
//	→ 净变化序列从 from 前一日取起（generate_series 起点前移一天），单次查询同时
//	  覆盖"日初/日末"两个锚点差分键，无需第二遍扫描。
func (r *repository) turnoverTrendRepo(ctx context.Context, sc Scope, from, to time.Time) ([]turnoverTrendRow, error) {
	// 1) 现存量锚点（执行一次）。
	anchor, err := r.currentStockAnchor(ctx, sc)
	if err != nil {
		return nil, err
	}
	// 2) 逐日"该日之后净变化"（起点前移一天——from 的日初差分键）。
	netAfter, err := r.netAfterByDay(ctx, sc, from.AddDate(0, 0, -1), to)
	if err != nil {
		return nil, err
	}
	// 3) 每日 OUTBOUND 流水量（Σ|qty_change|，一次扫描）。
	scCond, scArgs := sc.cond("l.warehouse_id")
	outSQL := `SELECT l.created_at::date AS day,
       COALESCE(SUM(ABS(l.qty_change)), 0)::float8 AS outbound
	FROM inventory_ledgers l
	WHERE l.change_type = 'OUTBOUND' AND l.created_at >= ?::date AND l.created_at < (?::date + 1) AND ` + scCond + `
	GROUP BY 1`
	type outRow struct {
		Day      time.Time `gorm:"column:day"`
		Outbound float64   `gorm:"column:outbound"`
	}
	var outs []outRow
	if err := r.db.WithContext(ctx).Raw(outSQL, append([]any{from, to}, scArgs...)...).Scan(&outs).Error; err != nil {
		return nil, fmt.Errorf("reports: 周转趋势出库查询失败: %w", err)
	}
	outMap := make(map[string]float64, len(outs))
	for _, o := range outs {
		outMap[o.Day.Format("2006-01-02")] = o.Outbound
	}
	// 4) 连续日期序列组装（Go 侧补零，避免图表断档）。
	out := make([]turnoverTrendRow, 0, int(to.Sub(from).Hours()/24)+1)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		prevKey := d.AddDate(0, 0, -1).Format("2006-01-02")
		endQty := anchor - netAfter[key]       // 日末 = 锚点 − (day, now] 净变化
		startQty := anchor - netAfter[prevKey] // 日初 = 锚点 − [day, now] 净变化
		avg := (startQty + endQty) / 2
		rate := 0.0
		if avg > 0 {
			rate = outMap[key] / avg
		}
		out = append(out, turnoverTrendRow{
			Date:         key,
			OutboundQty:  roundQty(outMap[key]),
			EndQty:       roundQty(endQty),
			AvgInventory: roundQty(avg),
			TurnoverRate: roundQty(rate),
		})
	}
	return out, nil
}

// flowTrend flowStats 同一参数化 SQL 去 LIMIT/OFFSET（flowStatsBase 直接复用
// changeType/priceColumn 占位；ORDER BY 1 = stat_date，行数≤窗口天数≤366）。
func (r *repository) flowTrend(ctx context.Context, sc Scope, changeType, priceColumn string, from, to time.Time) ([]FlowStatRow, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	sql := fmt.Sprintf(flowStatsBase+" ORDER BY 1", priceColumn, scCond)
	args := append(append([]any{}, changeType, from, to), scArgs...)
	rows := make([]FlowStatRow, 0)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: 流水趋势查询失败: %w", err)
	}
	return rows, nil
}

// ---------- Service ----------

// SkuTop SKU 库存 TOP N（metric：qty=按现存量 / value=按库存金额）。
func (s *Service) SkuTop(ctx context.Context, sc Scope, metric string, limit int, warehouseID int64) ([]skuTopRow, error) {
	rows, err := s.repo.skuTop(ctx, sc, metric, limit, warehouseID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].TotalQty = roundQty(rows[i].TotalQty)
		rows[i].StockValue = roundQty(rows[i].StockValue)
	}
	return rows, nil
}

// TurnoverTrend 库存周转趋势（days 仅影响窗口；缺省 30，上限 maxRangeDays）。
func (s *Service) TurnoverTrend(ctx context.Context, sc Scope, days int) ([]turnoverTrendRow, error) {
	if days <= 0 {
		days = 30
	}
	if days > maxRangeDays {
		return nil, response.NewError(ErrRangeTooLarge, gin.H{"reason": fmt.Sprintf("days 上限 %d", maxRangeDays), "max_days": maxRangeDays})
	}
	now := s.now()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := to.AddDate(0, 0, -(days - 1))
	return s.repo.turnoverTrendRepo(ctx, sc, from, to)
}

// FlowTrend 出入库流水趋势（inbound→INBOUND/成本价估值、outbound→OUTBOUND/销售价
// 估值——与既有 inbound/outbound-stats 同口径同 SQL 面，仅去分页）。
func (s *Service) FlowTrend(ctx context.Context, sc Scope, inbound bool, from, to time.Time) (*flowTrendDTO, error) {
	changeType, priceColumn := "INBOUND", "cost_price"
	if !inbound {
		changeType, priceColumn = "OUTBOUND", "sale_price"
	}
	rows, err := s.repo.flowTrend(ctx, sc, changeType, priceColumn, from, to)
	if err != nil {
		return nil, err
	}
	return &flowTrendDTO{
		Valuation: flowValuationDTO{Basis: flowValuationBasis(inbound)},
		Items:     rows,
	}, nil
}

// ---------- Handler ----------

// @Summary GET /api/inventory/sku-top
// @Tags 报表
// @Produce json
// @Param metric query string false "排序指标 qty|value（缺省 qty）"
// @Param limit query int false "返回条数（缺省 10，1–50）"
// @Param warehouse_id query int false "仓库 ID（缺省不过滤）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/sku-top [get]
func (h *handler) skuTop(c *gin.Context) {
	metric := c.Query("metric")
	if metric == "" {
		metric = "qty"
	}
	if metric != "qty" && metric != "value" {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "metric", "reason": "必须为 qty|value"}))
		return
	}
	limit, ok := parseLimitQuery(c)
	if !ok {
		return
	}
	warehouseID, ok := parseInt64Query(c, "warehouse_id")
	if !ok {
		return
	}
	rows, err := h.svc.SkuTop(c.Request.Context(), scopeOf(c), metric, limit, warehouseID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/inventory/turnover-trend
// @Tags 报表
// @Produce json
// @Param days query int false "窗口天数（缺省 30，1–366）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/inventory/turnover-trend [get]
func (h *handler) turnoverTrend(c *gin.Context) {
	days := 30
	if raw := c.Query("days"); raw != "" {
		// parseInt64Query 返回 (int64, bool)：解析失败已写响应（handler.go:275）。
		n, ok := parseInt64Query(c, "days")
		if !ok {
			return
		}
		if n <= 0 {
			response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "days", "reason": "必须为正整数"}))
			return
		}
		days = int(n)
	}
	rows, err := h.svc.TurnoverTrend(c.Request.Context(), scopeOf(c), days)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/reports/flow-trend
// @Tags 报表
// @Produce json
// @Param type query string true "流水类型 inbound|outbound"
// @Param time_from query string false "起（YYYY-MM-DD，缺省近 30 天）"
// @Param time_to query string false "止（YYYY-MM-DD，上限 366 天）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 400 {object} response.Envelope "请求参数错误"
// @Router /api/reports/flow-trend [get]
func (h *handler) flowTrend(c *gin.Context) {
	typ := c.Query("type")
	if typ != "inbound" && typ != "outbound" {
		response.Err(c, response.NewError(response.CodeInvalidParam, gin.H{"field": "type", "reason": "必须为 inbound|outbound"}))
		return
	}
	from, to, ok := h.requireRange(c)
	if !ok {
		return
	}
	dto, err := h.svc.FlowTrend(c.Request.Context(), scopeOf(c), typ == "inbound", from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}
