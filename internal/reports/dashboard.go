package reports

// Dashboard 五端点 + 库存分析（GET /api/reports/dashboard/*、GET /api/inventory/analytics）。
//
// 兑现前端先行契约（web/src/api/dashboard.ts、web/src/api/inventory.ts InventoryAnalytics，
// 2026-10-04 立项挂账"遗留清单"——联调轮 2026-10-05 补齐，docs/api.md §9）：
//   - GET /api/reports/dashboard/today           Dashboard 第一层今日指标（camelCase JSON tag——
//                                                与前端 DashboardTodayMetrics 契约逐字段回对）
//   - GET /api/reports/dashboard/trend           第二层趋势（inbound/outbound/stockQty，camelCase）
//   - GET /api/reports/dashboard/tasks           第三层任务概览（label/link 后端下发）
//   - GET /api/reports/dashboard/alerts          第三层预警条目（snake_case，同 repository_alerts 口径）
//   - GET /api/reports/dashboard/warehouse-stock 第四层仓库库存分布 + 库位利用率
//   - GET /api/inventory/analytics               库存分析（金额/周转/ABC/趋势；reports 实现、
//                                                inventory 前缀挂载——summary/alerts 同口径先例）
//
// 权限：全部挂 auth.PermInventoryList（消费方 Dashboard 页无独立权限码、/inventory/analytics
// 页挂库存域入口——api.md §9"数据域读权限承载 reports 实现"先例同口径）。
// 数据范围：一律 Scope 会话仓库快照（scopeOf），禁止接受前端范围参数。
// 只读约束：全部为参数化 SELECT，零写语句（plan §2.3 判据 3 同本包 repository.go）。

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// ---------- DTO（与前端契约逐字段回对） ----------

// todayMetricsDTO GET /api/reports/dashboard/today 响应（web/src/api/dashboard.ts
// DashboardTodayMetrics；字段名 camelCase 为前端先行契约的冻结口径）。
type todayMetricsDTO struct {
	// —— 两视图公共 ——
	TodayInboundCount  int64 `json:"todayInboundCount"`
	TodayOutboundCount int64 `json:"todayOutboundCount"`
	PendingTaskCount   int64 `json:"pendingTaskCount"`
	StockAlertCount    int64 `json:"stockAlertCount"`
	// —— 管理层视图 ——
	WarehouseCount        *int64   `json:"warehouseCount,omitempty"`
	SKUCount              *int64   `json:"skuCount,omitempty"`
	TotalQty              *float64 `json:"totalQty,omitempty"`
	StockValue            *float64 `json:"stockValue,omitempty"`
	OrderCount            *int64   `json:"orderCount,omitempty"`
	NearExpiryQty         *float64 `json:"nearExpiryQty,omitempty"`
	SlowMovingQty         *float64 `json:"slowMovingQty,omitempty"`
	PendingApprovalCount  *int64   `json:"pendingApprovalCount,omitempty"`
	PendingExceptionCount *int64   `json:"pendingExceptionCount,omitempty"`
	// —— 仓库人员视图 ——
	PendingReceiveCount  *int64 `json:"pendingReceiveCount,omitempty"`
	PendingPutawayCount  *int64 `json:"pendingPutawayCount,omitempty"`
	PendingPickCount     *int64 `json:"pendingPickCount,omitempty"`
	PendingCheckCount    *int64 `json:"pendingCheckCount,omitempty"`
	PendingPackCount     *int64 `json:"pendingPackCount,omitempty"`
	PendingShipmentCount *int64 `json:"pendingShipmentCount,omitempty"`
	PendingCountCount    *int64 `json:"pendingCountCount,omitempty"`
}

// taskItemDTO GET /api/reports/dashboard/tasks 行（type/label/count/link，link 为前端
// 真实路由——web/src/config/menu.tsx 注册路径，与 DashboardPage buildOperatorMetrics 同源）。
type taskItemDTO struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Count int64  `json:"count"`
	Link  string `json:"link"`
}

// trendPointDTO GET /api/reports/dashboard/trend 行（date/inbound/outbound/stockQty——
// camelCase 为前端 TrendPoint 契约冻结口径；date 为 YYYY-MM-DD）。
type trendPointDTO struct {
	Date     string  `json:"date"`
	Inbound  float64 `json:"inbound"`
	Outbound float64 `json:"outbound"`
	StockQty float64 `json:"stockQty"`
}

// warehouseStockDTO GET /api/reports/dashboard/warehouse-stock 行（snake_case，前端
// DashboardWarehouseStock 契约；bin_utilization 为 0~100 百分数，前端 Progress percent 直用）。
// warehouse_id 供前端图表/列表下钻预筛（frontend.md §33.4：/inventory/stock?warehouse_id=、
// /bins?warehouseId=，2026-10-07 联动批次二）。
type warehouseStockDTO struct {
	WarehouseID    int64   `json:"warehouse_id"`
	WarehouseCode  string  `json:"warehouse_code"`
	WarehouseName  string  `json:"warehouse_name"`
	SKUCount       int64   `json:"sku_count"`
	TotalQty       float64 `json:"total_qty"`
	BinUtilization float64 `json:"bin_utilization"`
}

// dashboardAlertRowDTO GET /api/reports/dashboard/alerts 行（前端 DashboardAlertItem 契约）。
// 预警为现场计算无自然主键/创建时间：ID 用序号（前端仅作 key），created_at 携 slow_moving
// 的末次移动时间（其余级别为空）。
type dashboardAlertRowDTO struct {
	ID          int64              `json:"id"`
	Type        string             `json:"type"`
	Level       string             `json:"level"`
	SKUCode     string             `json:"sku_code"`
	ProductName string             `json:"product_name"`
	Message     string             `json:"message"`
	CreatedAt   *database.JSONTime `json:"created_at"`
}

// analyticsDTO GET /api/inventory/analytics 响应（web/src/api/inventory.ts InventoryAnalytics
// 契约；ABC 分档由后端计算——前端不得自行分档）。
type analyticsDTO struct {
	TotalStockValue float64             `json:"total_stock_value"`
	TotalSKUCount   int64               `json:"total_sku_count"`
	TotalQty        float64             `json:"total_qty"`
	TurnoverRate    float64             `json:"turnover_rate"`
	TurnoverDays    float64             `json:"turnover_days"`
	ABC             []analyticsABCRow   `json:"abc"`
	Trend           []analyticsTrendRow `json:"trend"`
}

// analyticsABCRow ABC 分类项（按库存金额 80/15/5 阈值分档：累计占比 ≤80% A、≤95% B、其余 C）。
type analyticsABCRow struct {
	Grade        string  `json:"grade"` // A/B/C
	SKUCount     int64   `json:"sku_count"`
	ValueAmount  float64 `json:"value_amount"`
	ValuePercent float64 `json:"value_percent"` // 0~100
}

// analyticsTrendRow 库存趋势点（date YYYY-MM-DD；total_qty/stock_value 为日末口径）。
type analyticsTrendRow struct {
	Date       string  `json:"date"`
	TotalQty   float64 `json:"total_qty"`
	StockValue float64 `json:"stock_value"`
}

// ---------- Repository（只读参数化 SELECT） ----------

// scalarInt64 单标量整型查询（查询带仓库范围片段时 args 前置）。
func (r *repository) scalarInt64(ctx context.Context, sql string, args ...any) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("reports: dashboard 标量查询失败: %w", err)
	}
	return n, nil
}

// dashboardTodayRepo 作业链路计数集（today 与 tasks 两端点共享，一次查询组装）。
type dashboardTodayRepo struct {
	receive, putaway, pick, check, pack, ship, count_ int64
	approval, exception                               int64
}

func (r *repository) dashboardTodayRepo(ctx context.Context, sc Scope) (*dashboardTodayRepo, error) {
	// 各单据表统一形态：状态计数 + 仓库范围（表间仓列不同：putaway_tasks 为
	// target_warehouse_id，其余为 warehouse_id；均 NOT NULL 无软删，直连 COUNT）。
	count := func(table, whCol, statusIn string) (int64, error) {
		if !sc.AllWarehouses && len(sc.WarehouseIDs) == 0 {
			return 0, nil // fail-closed：空集不可见任何行
		}
		sql := "SELECT COUNT(*) FROM " + table + " WHERE status IN (" + statusIn + ")"
		var args []any
		if !sc.AllWarehouses {
			sql += " AND " + whCol + " IN ?"
			args = append(args, sc.WarehouseIDs)
		}
		return r.scalarInt64(ctx, sql, args...)
	}
	out := &dashboardTodayRepo{}
	var err error
	// 待收货：采购单已批未收完（000007 状态机）。
	if out.receive, err = count("purchase_orders", "warehouse_id", "'APPROVED','PARTIAL_RECEIVED'"); err != nil {
		return nil, err
	}
	// 待上架：上架任务活动态（含 000017 PAUSED——暂停仍是未完成作业）。
	if out.putaway, err = count("putaway_tasks", "target_warehouse_id", "'PENDING','IN_PROGRESS','PAUSED'"); err != nil {
		return nil, err
	}
	// 拣/复核/打包/发货：出库单状态机切片（000008 plan §6.5）。
	if out.pick, err = count("outbound_orders", "warehouse_id", "'ALLOCATED','PICKING'"); err != nil {
		return nil, err
	}
	if out.check, err = count("outbound_orders", "warehouse_id", "'PICKED'"); err != nil {
		return nil, err
	}
	if out.pack, err = count("outbound_orders", "warehouse_id", "'CHECKED'"); err != nil {
		return nil, err
	}
	if out.ship, err = count("outbound_orders", "warehouse_id", "'PACKED'"); err != nil {
		return nil, err
	}
	// 待盘点：盘点单进行中/待复核（000009）。
	if out.count_, err = count("count_orders", "warehouse_id", "'COUNTING','PENDING_REVIEW'"); err != nil {
		return nil, err
	}
	// 待审核：采购/销售/调拨/库存调整 PENDING_APPROVAL + 盘点单 PENDING_REVIEW。
	approval := int64(0)
	for _, spec := range [][3]string{
		{"purchase_orders", "warehouse_id", "'PENDING_APPROVAL'"},
		{"sales_orders", "warehouse_id", "'PENDING_APPROVAL'"},
		{"transfer_orders", "from_warehouse_id", "'PENDING_APPROVAL'"},
		{"inventory_adjustments", "warehouse_id", "'PENDING_APPROVAL'"},
		{"count_orders", "warehouse_id", "'PENDING_REVIEW'"},
	} {
		n, err := count(spec[0], spec[1], spec[2])
		if err != nil {
			return nil, err
		}
		approval += n
	}
	out.approval = approval
	// 待处理异常：未闭环（OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW；000010 状态机）。
	// exceptions 无 warehouse 列（挂 SKU/库位），按"异常全量未闭环"口径计数（超管/ALL 同权；
	// SPECIFIED 范围仍可见——异常中心页同口径）。
	if out.exception, err = r.scalarInt64(ctx,
		"SELECT COUNT(*) FROM exceptions WHERE status NOT IN ('RESOLVED','CLOSED')"); err != nil {
		return nil, err
	}
	return out, nil
}

// todayInboundOutbound 当日进出（DISTINCT business_no，scope 过滤）。
func (r *repository) todayFlowCount(ctx context.Context, sc Scope, changeType string, dayStart time.Time) (int64, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	sql := `SELECT COUNT(DISTINCT l.business_no) FROM inventory_ledgers l
WHERE l.change_type = ? AND l.created_at >= ? AND ` + scCond
	return r.scalarInt64(ctx, sql, append([]any{changeType, dayStart}, scArgs...)...)
}

// managementStock 管理层库存聚合：仓库数 / SKU 数 / 总量 / 金额 / 销售订单数。
// 金额口径与 inventory-summary 同源（批次成本价优先，非批次 SKU 用 SKU 成本价）。
func (r *repository) managementStock(ctx context.Context, sc Scope) (whCnt, skuCnt, orderCnt int64, totalQty, stockValue float64, err error) {
	if !sc.AllWarehouses && len(sc.WarehouseIDs) == 0 {
		return 0, 0, 0, 0, 0, nil // fail-closed：空集不可见任何行
	}
	// 仓库数（scope 过滤；ALL 全量）。
	whSQL := "SELECT COUNT(*) FROM warehouses WHERE deleted_at IS NULL"
	whArgs := []any{}
	if !sc.AllWarehouses {
		whSQL += " AND id IN ?"
		whArgs = append(whArgs, sc.WarehouseIDs)
	}
	if whCnt, err = r.scalarInt64(ctx, whSQL, whArgs...); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	// SKU 总数（主数据维度，不随仓库范围变化）。
	if skuCnt, err = r.scalarInt64(ctx, "SELECT COUNT(*) FROM skus WHERE deleted_at IS NULL"); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	// 库存总量与金额（scope 过滤）。
	scCond, scArgs := sc.cond("i.warehouse_id")
	stockSQL := `SELECT COALESCE(SUM(i.total_qty), 0)::float8 AS total_qty,
       COALESCE(SUM(i.total_qty * COALESCE(b.cost_price, s.cost_price, 0)), 0)::float8 AS stock_value
FROM inventory i
JOIN skus s ON s.id = i.sku_id
LEFT JOIN batches b ON b.id = i.batch_id AND i.batch_id > 0
WHERE ` + scCond
	row := struct {
		TotalQty   float64 `gorm:"column:total_qty"`
		StockValue float64 `gorm:"column:stock_value"`
	}{}
	if err = r.db.WithContext(ctx).Raw(stockSQL, scArgs...).Scan(&row).Error; err != nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("reports: dashboard 库存聚合失败: %w", err)
	}
	totalQty, stockValue = row.TotalQty, row.StockValue
	// 销售订单数（非取消；scope 过滤）。
	orderSQL := "SELECT COUNT(*) FROM sales_orders WHERE status <> 'CANCELLED'"
	orderArgs := []any{}
	if !sc.AllWarehouses {
		orderSQL += " AND warehouse_id IN ?"
		orderArgs = append(orderArgs, sc.WarehouseIDs)
	}
	if orderCnt, err = r.scalarInt64(ctx, orderSQL, orderArgs...); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return whCnt, skuCnt, orderCnt, totalQty, stockValue, nil
}

// stockLevelTypes 改变仓库总量（total_qty）的流水类型（000005 六列恒等式：total =
// available+locked+frozen+pending_inspect+defective；LOCK/RELEASE 仅状态转移、
// INSPECT_PASS/INSPECT_DEFECTIVE 为质检态迁移、MOVE 为仓内移库——均不改变总量，
// 库存重构时排除，否则 Σ(qty_change) 与 Σ(total_qty) 出现恒等偏差）。
const stockLevelTypes = "'INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','ADJUST'"

// currentStockAnchor 现存量事实锚点（Σ total_qty，scope 过滤）。
func (r *repository) currentStockAnchor(ctx context.Context, sc Scope) (float64, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	var n float64
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(SUM(i.total_qty), 0)::float8 FROM inventory i WHERE `+scCond, scArgs...).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("reports: 库存锚点查询失败: %w", err)
	}
	return n, nil
}

// netAfterByDay 逐日"该日之后至现在的总量级净变化"（锚点回推法的核心查询）。
// f7 修复：原实现为 generate_series × 流水非等值 LEFT JOIN（l.created_at >= d.day+1day，
// 无上界连接无法走 hash join，单请求代价 ≈ 流水总行数 × 天数，366 天 custom 档分钟级）。
// 改写为后缀和——数学上与原查询逐日求和完全等价（同一条流水行集，仅求和次序不同）：
//
//	netAfter(to) = Σ{t ≥ to+1day}（有界单查询）
//	netAfter(d)  = netAfter(d+1) + dayNet(d+1)（日序回推，Go 侧纯计算）
//
// dayNet 为窗口内逐日净变化聚合（created_at 索引范围扫描一遍，代价 ≈ 窗口内行数），
// 代价从"全表 × 天数"降为"窗口行数"，随数据量的超线性劣化消除。
func (r *repository) netAfterByDay(ctx context.Context, sc Scope, from, to time.Time) (map[string]float64, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	// 尾段净变化 [to+1day, now)：有界（created_at 索引范围扫描）。
	tailSQL := `SELECT COALESCE(SUM(l.qty_change), 0)::float8
FROM inventory_ledgers l
WHERE l.created_at >= (?::date + interval '1 day') AND l.change_type IN (` + stockLevelTypes + `) AND ` + scCond
	var tail float64
	if err := r.db.WithContext(ctx).Raw(tailSQL, append([]any{to}, scArgs...)...).Scan(&tail).Error; err != nil {
		return nil, fmt.Errorf("reports: 回推尾段净变化查询失败: %w", err)
	}
	// 窗口内逐日净变化 [from+1day, to+1day)：范围扫描一遍。
	daySQL := `SELECT l.created_at::date AS day, COALESCE(SUM(l.qty_change), 0)::float8 AS net
FROM inventory_ledgers l
WHERE l.created_at >= (?::date + interval '1 day') AND l.created_at < (?::date + interval '1 day')
  AND l.change_type IN (` + stockLevelTypes + `) AND ` + scCond + `
GROUP BY 1`
	type dayRow struct {
		Day time.Time `gorm:"column:day"`
		Net float64   `gorm:"column:net"`
	}
	var dayRows []dayRow
	if err := r.db.WithContext(ctx).Raw(daySQL, append([]any{from, to}, scArgs...)...).Scan(&dayRows).Error; err != nil {
		return nil, fmt.Errorf("reports: 回推逐日净变化查询失败: %w", err)
	}
	dayNet := make(map[string]float64, len(dayRows))
	for _, d := range dayRows {
		dayNet[d.Day.Format("2006-01-02")] = d.Net
	}
	// 日序回推（原 generate_series 每日必有行；缺日 = 零净变化，回推跳过等价）。
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	out := make(map[string]float64, int(to.Sub(from).Hours()/24)+2)
	net := tail
	for d := to; ; d = d.AddDate(0, 0, -1) {
		out[d.Format("2006-01-02")] = net
		if d.AddDate(0, 0, -1).Before(fromDay) {
			break
		}
		// netAfter(d-1) = netAfter(d) + dayNet(d)
		net += dayNet[d.Format("2006-01-02")]
	}
	return out, nil
}

// netSince 期间净变化（自 from 起至现在的总量级净和；周转期初回推用）。
func (r *repository) netSince(ctx context.Context, sc Scope, from time.Time) (float64, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	var n float64
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(SUM(l.qty_change), 0)::float8 FROM inventory_ledgers l
WHERE l.created_at >= ?::date AND l.change_type IN (`+stockLevelTypes+`) AND `+scCond,
		append([]any{from}, scArgs...)...).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("reports: 期间净变化查询失败: %w", err)
	}
	return n, nil
}

// dashboardTrendRepo 趋势行集：每日入库/出库量 + 日末库存。
// 日末库存采用"锚点回推"（inventory-rules §3 流水可重构口径的稳定实现）：
//
//	日末库存(day) = 当前现存量锚点 − (day, now] 的总量级净变化
//
// 相比"窗口前基线 + 逐日累计"：锚点是事实值，回推只依赖净变化类型集，
// 不受多库位行级 qty_after 聚合歧义与状态迁移类流水影响。
func (r *repository) dashboardTrendRepo(ctx context.Context, sc Scope, from, to time.Time) ([]trendPointDTO, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	dayFlowSQL := `SELECT l.created_at::date AS day,
       COALESCE(SUM(CASE WHEN l.change_type = 'INBOUND' THEN l.qty_change ELSE 0 END), 0)::float8 AS inbound,
       COALESCE(SUM(CASE WHEN l.change_type = 'OUTBOUND' THEN -l.qty_change ELSE 0 END), 0)::float8 AS outbound
FROM inventory_ledgers l
WHERE l.change_type IN ('INBOUND','OUTBOUND') AND l.created_at >= ?::date AND l.created_at < (?::date + 1) AND ` + scCond + `
GROUP BY 1`

	// 1) 现存量锚点（执行一次）
	anchor, err := r.currentStockAnchor(ctx, sc)
	if err != nil {
		return nil, err
	}
	// 2) 每日"之后净变化"（执行一次，锚点回推）
	netAfter, err := r.netAfterByDay(ctx, sc, from, to)
	if err != nil {
		return nil, err
	}
	// 3) 每日进出展示量（执行一次）
	type flowRow struct {
		Day      time.Time `gorm:"column:day"`
		Inbound  float64   `gorm:"column:inbound"`
		Outbound float64   `gorm:"column:outbound"`
	}
	var flows []flowRow
	flowArgs := append([]any{from, to}, scArgs...)
	if err := r.db.WithContext(ctx).Raw(dayFlowSQL, flowArgs...).Scan(&flows).Error; err != nil {
		return nil, fmt.Errorf("reports: trend 进出查询失败: %w", err)
	}
	flowMap := map[string][2]float64{}
	for _, f := range flows {
		flowMap[f.Day.Format("2006-01-02")] = [2]float64{f.Inbound, f.Outbound}
	}
	// 4) 连续日期序列组装：日末库存 = 锚点 − 该日之后净变化（Go 侧补零，避免图表断档）
	out := make([]trendPointDTO, 0, 32)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		f := flowMap[key]
		out = append(out, trendPointDTO{
			Date:     key,
			Inbound:  f[0],
			Outbound: f[1],
			StockQty: anchor - netAfter[key],
		})
	}
	return out, nil
}

// dashboardWarehouseStockRepo 仓库库存分布 + 库位利用率（一次连接查询）。
func (r *repository) dashboardWarehouseStockRepo(ctx context.Context, sc Scope) ([]warehouseStockDTO, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	binCond, binArgs := sc.cond("b.warehouse_id")
	sql := `SELECT w.id AS warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       COUNT(DISTINCT i.sku_id) AS sku_count,
       COALESCE(SUM(i.total_qty), 0)::float8 AS total_qty,
       COALESCE(bin.util, 0)::float8 AS bin_utilization
FROM warehouses w
LEFT JOIN inventory i ON i.warehouse_id = w.id AND ` + scCond + `
LEFT JOIN (
  SELECT b.warehouse_id,
         CASE WHEN SUM(b.max_capacity) > 0 THEN SUM(b.current_capacity)::float8 / SUM(b.max_capacity) * 100 ELSE 0 END AS util
  FROM bins b
  WHERE b.status = 'ENABLED' AND b.deleted_at IS NULL AND ` + binCond + `
  GROUP BY 1
) bin ON bin.warehouse_id = w.id
WHERE w.deleted_at IS NULL
GROUP BY w.id, w.code, w.name, bin.util
ORDER BY w.code`
	rows := make([]warehouseStockDTO, 0)
	args := append(append([]any{}, scArgs...), binArgs...)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("reports: dashboard 仓库分布查询失败: %w", err)
	}
	return rows, nil
}

// analyticsRepo 库存分析聚合（金额/周转/ABC/趋势）。
func (r *repository) analyticsRepo(ctx context.Context, sc Scope, from, to time.Time) (*analyticsDTO, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	out := &analyticsDTO{ABC: make([]analyticsABCRow, 0), Trend: make([]analyticsTrendRow, 0)}

	// 1) 现存量总览 + ABC 基础行（SKU 级金额，成本价口径与 inventory-summary 披露一致）
	var skus []analyticsSkuValue
	abcSQL := `SELECT i.sku_id, SUM(i.total_qty)::float8 AS qty,
       SUM(i.total_qty * COALESCE(s.cost_price, 0))::float8 AS value
FROM inventory i
JOIN skus s ON s.id = i.sku_id
WHERE ` + scCond + `
GROUP BY 1`
	if err := r.db.WithContext(ctx).Raw(abcSQL, scArgs...).Scan(&skus).Error; err != nil {
		return nil, fmt.Errorf("reports: analytics 库存查询失败: %w", err)
	}
	for _, s := range skus {
		out.TotalQty += s.Qty
		out.TotalStockValue += s.Value
		if s.Qty > 0 {
			out.TotalSKUCount++
		}
	}
	out.ABC = abcGrade(skus)

	// 2) 周转（窗口内出库量 / 平均库存；期初 = 锚点 − 自窗口起的总量级净变化，
	//    期末 = 锚点当前值——与 trend 同一锚点回推口径）
	scLCond, scLArgs := sc.cond("l.warehouse_id")
	var outboundQty float64
	if err := r.db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(ABS(l.qty_change)), 0)::float8
FROM inventory_ledgers l
WHERE l.change_type = 'OUTBOUND' AND l.created_at >= ?::date AND l.created_at < (?::date + 1) AND `+scLCond,
		append([]any{from, to}, scLArgs...)...).Scan(&outboundQty).Error; err != nil {
		return nil, fmt.Errorf("reports: analytics 出库查询失败: %w", err)
	}
	anchorQty, err := r.currentStockAnchor(ctx, sc)
	if err != nil {
		return nil, err
	}
	sinceNet, err := r.netSince(ctx, sc, from)
	if err != nil {
		return nil, err
	}
	windowDays := int(to.Sub(from).Hours()/24) + 1
	startQty := anchorQty - sinceNet
	avgInventory := (startQty + anchorQty) / 2
	if avgInventory > 0 {
		out.TurnoverRate = outboundQty / avgInventory
		if out.TurnoverRate > 0 {
			out.TurnoverDays = float64(windowDays) / out.TurnoverRate
		}
	}

	// 3) 趋势（日末总量 + 日末金额；金额锚点 = Σ(total_qty × SKU 成本价)，
	//    回推净变化同口径乘 SKU 成本价——金额线为成本价口径，与分析页披露一致）
	anchorValue, err := r.currentStockValueAnchor(ctx, sc)
	if err != nil {
		return nil, err
	}
	netAfterValue, err := r.netAfterValueByDay(ctx, sc, from, to)
	if err != nil {
		return nil, err
	}
	netAfterQty, err := r.netAfterByDay(ctx, sc, from, to)
	if err != nil {
		return nil, err
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		out.Trend = append(out.Trend, analyticsTrendRow{
			Date:       key,
			TotalQty:   anchorQty - netAfterQty[key],
			StockValue: anchorValue - netAfterValue[key],
		})
	}
	return out, nil
}

// currentStockValueAnchor 金额锚点（Σ total_qty × SKU 成本价；与分析页金额口径一致）。
func (r *repository) currentStockValueAnchor(ctx context.Context, sc Scope) (float64, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	var n float64
	if err := r.db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(i.total_qty * COALESCE(s.cost_price, 0)), 0)::float8
FROM inventory i
JOIN skus s ON s.id = i.sku_id
WHERE `+scCond, scArgs...).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("reports: 金额锚点查询失败: %w", err)
	}
	return n, nil
}

// netAfterValueByDay 逐日"该日之后至现在的金额净变化"（净变化 × SKU 成本价）。
// f7 修复：与 netAfterByDay 同款后缀和改写——尾段单查询 + 窗口内逐日金额净变化
// 聚合（索引范围扫描一遍）+ Go 侧日序回推，代价从"全表 × 天数"降为"窗口行数"。
func (r *repository) netAfterValueByDay(ctx context.Context, sc Scope, from, to time.Time) (map[string]float64, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	tailSQL := `SELECT COALESCE(SUM(l.qty_change * COALESCE(s.cost_price, 0)), 0)::float8
FROM inventory_ledgers l
LEFT JOIN skus s ON s.id = l.sku_id
WHERE l.created_at >= (?::date + interval '1 day') AND l.change_type IN (` + stockLevelTypes + `) AND ` + scCond
	var tail float64
	if err := r.db.WithContext(ctx).Raw(tailSQL, append([]any{to}, scArgs...)...).Scan(&tail).Error; err != nil {
		return nil, fmt.Errorf("reports: 回推尾段金额净变化查询失败: %w", err)
	}
	daySQL := `SELECT l.created_at::date AS day,
       COALESCE(SUM(l.qty_change * COALESCE(s.cost_price, 0)), 0)::float8 AS net
FROM inventory_ledgers l
LEFT JOIN skus s ON s.id = l.sku_id
WHERE l.created_at >= (?::date + interval '1 day') AND l.created_at < (?::date + interval '1 day')
  AND l.change_type IN (` + stockLevelTypes + `) AND ` + scCond + `
GROUP BY 1`
	type dayRow struct {
		Day time.Time `gorm:"column:day"`
		Net float64   `gorm:"column:net"`
	}
	var dayRows []dayRow
	if err := r.db.WithContext(ctx).Raw(daySQL, append([]any{from, to}, scArgs...)...).Scan(&dayRows).Error; err != nil {
		return nil, fmt.Errorf("reports: 回推逐日金额净变化查询失败: %w", err)
	}
	dayNet := make(map[string]float64, len(dayRows))
	for _, d := range dayRows {
		dayNet[d.Day.Format("2006-01-02")] = d.Net
	}
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	out := make(map[string]float64, int(to.Sub(from).Hours()/24)+2)
	net := tail
	for d := to; ; d = d.AddDate(0, 0, -1) {
		out[d.Format("2006-01-02")] = net
		if d.AddDate(0, 0, -1).Before(fromDay) {
			break
		}
		net += dayNet[d.Format("2006-01-02")]
	}
	return out, nil
}

// analyticsSkuValue ABC 分档输入行（SKU 级现存量与库存金额）。
type analyticsSkuValue struct {
	SKUID int64   `gorm:"column:sku_id"`
	Qty   float64 `gorm:"column:qty"`
	Value float64 `gorm:"column:value"`
}

// abcGrade ABC 分档（后端冻结口径：按库存金额降序累计，累计占比 ≤80% A、≤95% B、
// 其余 C；零金额 SKU 不入档。前端不得自行分档——inventory.ts AnalyticsAbcItem 契约）。
func abcGrade(rows []analyticsSkuValue) []analyticsABCRow {
	valued := make([]analyticsSkuValue, 0, len(rows))
	var total float64
	for _, r := range rows {
		if r.Value > 0 {
			valued = append(valued, r)
			total += r.Value
		}
	}
	if total <= 0 || len(valued) == 0 {
		return make([]analyticsABCRow, 0)
	}
	sort.Slice(valued, func(i, j int) bool { return valued[i].Value > valued[j].Value })
	out := make([]analyticsABCRow, 0, 3)
	acc := 0.0
	for _, r := range valued {
		acc += r.Value / total * 100
		grade := "C"
		switch {
		case acc <= 80.0:
			grade = "A"
		case acc <= 95.0:
			grade = "B"
		}
		// 累计占比单调不减，分档序列天然有序（A…B…C），同档续行合并。
		if n := len(out); n > 0 && out[n-1].Grade == grade {
			out[n-1].SKUCount++
			out[n-1].ValueAmount += r.Value
		} else {
			out = append(out, analyticsABCRow{Grade: grade, SKUCount: 1, ValueAmount: r.Value})
		}
	}
	for i := range out {
		out[i].ValuePercent = out[i].ValueAmount / total * 100
	}
	return out
}

// ---------- Service ----------

// DashboardToday 今日指标（today 与 tasks 共享一次仓储查询）。
func (s *Service) dashboardTodayBundle(ctx context.Context, sc Scope, dayStart time.Time) (*todayMetricsDTO, []taskItemDTO, error) {
	t, err := s.repo.dashboardTodayRepo(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	inbound, err := s.repo.todayFlowCount(ctx, sc, "INBOUND", dayStart)
	if err != nil {
		return nil, nil, err
	}
	outbound, err := s.repo.todayFlowCount(ctx, sc, "OUTBOUND", dayStart)
	if err != nil {
		return nil, nil, err
	}
	// 预警/临期/积压：复用 alerts 仓储（计数 only，pageSize=1）。
	expiryList, stagnantList, err := s.alertThresholds(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reports: 预警阈值读取失败: %w", err)
	}
	_, alertTotal, err := s.repo.alerts(ctx, sc, "", "", expiryList[0], stagnantList[len(stagnantList)-1], 1, 1)
	if err != nil {
		return nil, nil, err
	}
	_, slowMovingTotal, err := s.repo.alerts(ctx, sc, "slow_moving", "", expiryList[0], stagnantList[len(stagnantList)-1], 1, 1)
	if err != nil {
		return nil, nil, err
	}
	slowMovingQty := float64(slowMovingTotal)
	summary, err := s.DashboardSummary(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	whCnt, skuCnt, orderCnt, totalQty, stockValue, err := s.repo.managementStock(ctx, sc)
	if err != nil {
		return nil, nil, err
	}
	pendingTask := t.receive + t.putaway + t.pick + t.check + t.pack + t.ship + t.count_ + t.exception
	dto := &todayMetricsDTO{
		TodayInboundCount:     inbound,
		TodayOutboundCount:    outbound,
		PendingTaskCount:      pendingTask,
		StockAlertCount:       alertTotal,
		WarehouseCount:        &whCnt,
		SKUCount:              &skuCnt,
		TotalQty:              &totalQty,
		StockValue:            &stockValue,
		OrderCount:            &orderCnt,
		NearExpiryQty:         &summary.NearExpiryQty,
		SlowMovingQty:         &slowMovingQty,
		PendingApprovalCount:  &t.approval,
		PendingExceptionCount: &t.exception,
		PendingReceiveCount:   &t.receive,
		PendingPutawayCount:   &t.putaway,
		PendingPickCount:      &t.pick,
		PendingCheckCount:     &t.check,
		PendingPackCount:      &t.pack,
		PendingShipmentCount:  &t.ship,
		PendingCountCount:     &t.count_,
	}
	// Link 状态预筛（frontend.md §33.4，2026-10-07 联动批次二）：仅追加目标列表筛选
	// 白名单内、且与计数口径对得上的键值；收货列表无 status 参数、盘点/异常为多态计数
	// 单值筛不覆盖、审批无对应筛参数——四者保持裸路径（诚实不硬筛）。
	tasks := []taskItemDTO{
		{Type: "receive", Label: "待收货", Count: t.receive, Link: "/purchases/receipts"},
		{Type: "putaway", Label: "待上架", Count: t.putaway, Link: "/inbound?status=AWAITING_PUTAWAY"},
		{Type: "pick", Label: "待拣货", Count: t.pick, Link: "/picking?status=PENDING"},
		{Type: "check", Label: "待复核", Count: t.check, Link: "/checking?status=PENDING"},
		{Type: "pack", Label: "待打包", Count: t.pack, Link: "/packing"},
		{Type: "ship", Label: "待发货", Count: t.ship, Link: "/shipment?status=PENDING"},
		{Type: "count", Label: "待盘点", Count: t.count_, Link: "/counts"},
		{Type: "approval", Label: "待审核单据", Count: t.approval, Link: "/tasks"},
		{Type: "exception", Label: "待处理异常", Count: t.exception, Link: "/exceptions"},
	}
	return dto, tasks, nil
}

// DashboardToday 今日业务指标。
func (s *Service) DashboardToday(ctx context.Context, sc Scope) (*todayMetricsDTO, error) {
	dayStart := s.now().Truncate(24 * time.Hour)
	dto, _, err := s.dashboardTodayBundle(ctx, sc, dayStart)
	return dto, err
}

// DashboardTasks 任务概览条目。
func (s *Service) DashboardTasks(ctx context.Context, sc Scope) ([]taskItemDTO, error) {
	dayStart := s.now().Truncate(24 * time.Hour)
	_, tasks, err := s.dashboardTodayBundle(ctx, sc, dayStart)
	return tasks, err
}

// DashboardTrend 业务趋势（range 冻结档 7d/30d/90d/custom；custom 必须带 time_from/time_to）。
func (s *Service) DashboardTrend(ctx context.Context, sc Scope, rng, timeFrom, timeTo string) ([]trendPointDTO, error) {
	now := s.now()
	var from, to time.Time
	switch rng {
	case "7d", "30d", "90d":
		days := map[string]int{"7d": 7, "30d": 30, "90d": 90}[rng]
		to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		from = to.AddDate(0, 0, -(days - 1))
	case "custom":
		if timeFrom == "" || timeTo == "" {
			return nil, response.NewError(response.CodeInvalidParam, gin.H{"field": "time_from/time_to", "reason": "自定义档必须同时提供 time_from 与 time_to"})
		}
		var err error
		if from, err = parseDay(timeFrom); err != nil {
			return nil, response.NewError(response.CodeInvalidParam, gin.H{"field": "time_from", "reason": err.Error()})
		}
		if to, err = parseDay(timeTo); err != nil {
			return nil, response.NewError(response.CodeInvalidParam, gin.H{"field": "time_to", "reason": err.Error()})
		}
		if to.Before(from) {
			return nil, response.NewError(response.CodeInvalidParam, gin.H{"reason": "time_to 不得早于 time_from"})
		}
		// validateRange 规范化（from>to 与 366 天上限，calc.go:24；与报表域冻结口径一致）。
		from, to, err = validateRange(&from, &to, now)
		if err != nil {
			return nil, response.NewError(ErrRangeTooLarge, gin.H{"reason": err.Error(), "max_days": maxRangeDays})
		}
	default:
		return nil, response.NewError(response.CodeInvalidParam, gin.H{"field": "range", "reason": "必须为 7d/30d/90d/custom"})
	}
	return s.repo.dashboardTrendRepo(ctx, sc, from, to)
}

// DashboardWarehouseStock 仓库库存分布。
func (s *Service) DashboardWarehouseStock(ctx context.Context, sc Scope) ([]warehouseStockDTO, error) {
	return s.repo.dashboardWarehouseStockRepo(ctx, sc)
}

// DashboardAlertFeed Dashboard 预警条目（复用 alerts 仓储，取前 limit 条）。
func (s *Service) DashboardAlertFeed(ctx context.Context, sc Scope, limit int) ([]dashboardAlertRowDTO, error) {
	expiryList, stagnantList, err := s.alertThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("reports: 预警阈值读取失败: %w", err)
	}
	rows, _, err := s.repo.alerts(ctx, sc, "", "", expiryList[0], stagnantList[len(stagnantList)-1], 1, limit)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]dashboardAlertRowDTO, 0, len(rows))
	for i, a := range rows {
		// Message 由 Service 层模板组装（DashboardAlerts 同法，service.go:240）。
		row := dashboardAlertRowDTO{
			ID:          int64(i + 1),
			Type:        a.Level,
			Level:       a.Level,
			SKUCode:     a.SKUCode,
			ProductName: a.SKUName,
			Message:     alertMessage(a, now),
		}
		if a.LastMovedAt != nil {
			row.CreatedAt = a.LastMovedAt
		}
		out = append(out, row)
	}
	return out, nil
}

// Analytics 库存分析（GET /api/inventory/analytics；days 仅影响趋势与周转窗口，默认 30）。
func (s *Service) Analytics(ctx context.Context, sc Scope, days int) (*analyticsDTO, error) {
	if days <= 0 {
		days = 30
	}
	if days > maxRangeDays {
		return nil, response.NewError(ErrRangeTooLarge, gin.H{"reason": fmt.Sprintf("days 上限 %d", maxRangeDays), "max_days": maxRangeDays})
	}
	now := s.now()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := to.AddDate(0, 0, -(days - 1))
	return s.repo.analyticsRepo(ctx, sc, from, to)
}

// ---------- Handler ----------

// @Summary GET /api/reports/dashboard/today
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/reports/dashboard/today [get]
func (h *handler) dashboardToday(c *gin.Context) {
	dto, err := h.svc.DashboardToday(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}

// @Summary GET /api/reports/dashboard/trend
// @Tags 报表
// @Produce json
// @Param range query string true "时间档 7d/30d/90d/custom"
// @Param time_from query string false "自定义档起（YYYY-MM-DD）"
// @Param time_to query string false "自定义档止（YYYY-MM-DD）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/reports/dashboard/trend [get]
func (h *handler) dashboardTrend(c *gin.Context) {
	rows, err := h.svc.DashboardTrend(c.Request.Context(), scopeOf(c),
		c.Query("range"), c.Query("time_from"), c.Query("time_to"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/reports/dashboard/tasks
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/reports/dashboard/tasks [get]
func (h *handler) dashboardTasks(c *gin.Context) {
	rows, err := h.svc.DashboardTasks(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/reports/dashboard/alerts
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/reports/dashboard/alerts [get]
func (h *handler) dashboardAlertFeed(c *gin.Context) {
	rows, err := h.svc.DashboardAlertFeed(c.Request.Context(), scopeOf(c), 8)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/reports/dashboard/warehouse-stock
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/reports/dashboard/warehouse-stock [get]
func (h *handler) dashboardWarehouseStock(c *gin.Context) {
	rows, err := h.svc.DashboardWarehouseStock(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, rows)
}

// @Summary GET /api/inventory/analytics
// @Tags 报表
// @Produce json
// @Param days query int false "趋势窗口天数（默认 30，上限 366）"
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/inventory/analytics [get]
func (h *handler) inventoryAnalytics(c *gin.Context) {
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
	dto, err := h.svc.Analytics(c.Request.Context(), scopeOf(c), days)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}
