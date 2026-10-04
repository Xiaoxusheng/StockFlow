package reports

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 只读聚合 SQL（现场聚合，backend-m3-plan §9.2）：全部为参数化 SELECT，零写语句
// （plan §2.3 判据 3：本包 INSERT/UPDATE/DELETE 零命中）；聚合命中既有索引
//（idx_inventory_ledgers_sku_created / idx_inventory_ledgers_warehouse_created /
// uk_inventory_location 等），时间范围上限 + 强制分页兜住扫描面。

// Scope 数据权限仓库范围快照（形状与 auth.WarehouseScope 快照一致；本包为平台包，
// 禁止 import internal/inventory——plan §2.3 判据 2，故本地定义同形值类型）。
type Scope struct {
	AllWarehouses bool
	WarehouseIDs  []int64
}

// cond 返回仓库范围过滤片段与参数（all=恒真片段；空集=fail-closed 不可见任何行；
// permission.md §4 数据权限由 Service 层自 gin 上下文注入，禁止接受前端范围参数）。
// all 分支返回 "1 = 1" 而非空串：调用方以 `WHERE %s` 或 `... AND %s` 两种形态拼接
// （真库回归 2026-10-04：空串在 ALL 范围下产生 "WHERE "/"AND " 悬空尾巴，PG 42601
// syntax error，/api/reports/* 与 /api/inventory/summary|alerts 对超管/ALL 范围全量
// 500），恒真片段对两种拼接形态均合法。
func (s Scope) cond(column string) (string, []any) {
	if s.AllWarehouses {
		return "1 = 1", nil
	}
	if len(s.WarehouseIDs) == 0 {
		return "1 = 0", nil
	}
	return column + " IN ?", []any{s.WarehouseIDs}
}

// repository 报表只读查询（Handler→Service→Repository 分层，architecture §1）。
type repository struct {
	db *gorm.DB
}

// 移动类流水口径（plan §9.3，见下方各 SQL 的 IN 清单）：INBOUND/OUTBOUND/TRANSFER/MOVE/ADJUST
// 为"移动"；LOCK/RELEASE 仅状态转移、INSPECT_* 为质检态迁移，均不进末次移动与净变化聚合。

// paged 通用分页包装：countSQL 对子查询计数，listSQL 追加排序与 LIMIT/OFFSET。
func paged(ctx context.Context, db *gorm.DB, base string, args []any, order string, page, pageSize int, dst any) (int64, error) {
	var total int64
	if err := db.WithContext(ctx).Raw(
		fmt.Sprintf("SELECT COUNT(*) FROM (%s) agg", base), args...).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("reports: 聚合计数失败: %w", err)
	}
	list := fmt.Sprintf("%s ORDER BY %s LIMIT ? OFFSET ?", base, order)
	args = append(args, pageSize, (page-1)*pageSize)
	if err := db.WithContext(ctx).Raw(list, args...).Scan(dst).Error; err != nil {
		return 0, fmt.Errorf("reports: 聚合查询失败: %w", err)
	}
	return total, nil
}

// ---- 库存汇总（/api/reports/inventory-summary）----

// InventorySummaryRow 库存汇总行（按仓 + SKU 分组）。
type InventorySummaryRow struct {
	WarehouseID       int64   `json:"warehouse_id"`
	WarehouseCode     string  `json:"warehouse_code"`
	WarehouseName     string  `json:"warehouse_name"`
	SKUID             int64   `json:"sku_id"`
	SKUCode           string  `json:"sku_code"`
	SKUName           string  `json:"sku_name"`
	TotalQty          float64 `json:"total_qty"`
	AvailableQty      float64 `json:"available_qty"`
	LockedQty         float64 `json:"locked_qty"`
	FrozenQty         float64 `json:"frozen_qty"`
	PendingInspectQty float64 `json:"pending_inspect_qty"`
	DefectiveQty      float64 `json:"defective_qty"`
	// StockValue 库存金额 = Σ 数量 × 成本价（批次成本价优先，非批次 SKU 用 SKU 成本价）。
	StockValue float64 `json:"stock_value"`
}

const inventorySummaryBase = `
SELECT i.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       i.sku_id, s.code AS sku_code, p.name AS sku_name,
       SUM(i.total_qty)::float8            AS total_qty,
       SUM(i.available_qty)::float8        AS available_qty,
       SUM(i.locked_qty)::float8           AS locked_qty,
       SUM(i.frozen_qty)::float8           AS frozen_qty,
       SUM(i.pending_inspect_qty)::float8  AS pending_inspect_qty,
       SUM(i.defective_qty)::float8        AS defective_qty,
       SUM(i.total_qty * COALESCE(b.cost_price, s.cost_price, 0))::float8 AS stock_value
FROM inventory i
JOIN skus s ON s.id = i.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = i.warehouse_id
LEFT JOIN batches b ON b.id = i.batch_id AND i.batch_id > 0
WHERE (%s) AND (%s)
GROUP BY i.warehouse_id, w.code, w.name, i.sku_id, s.code, p.name`

func (r *repository) inventorySummary(ctx context.Context, sc Scope, warehouseID, skUID int64, page, pageSize int) ([]InventorySummaryRow, int64, error) {
	extra, eArgs := "1 = 1", []any{}
	if warehouseID > 0 {
		extra, eArgs = extra+" AND i.warehouse_id = ?", append(eArgs, warehouseID)
	}
	if skUID > 0 {
		extra, eArgs = extra+" AND i.sku_id = ?", append(eArgs, skUID)
	}
	scCond, scArgs := sc.cond("i.warehouse_id")
	base := fmt.Sprintf(inventorySummaryBase, scCond, extra)
	args := append(append([]any{}, scArgs...), eArgs...)
	var rows []InventorySummaryRow
	total, err := paged(ctx, r.db, base, args, "i.warehouse_id, i.sku_id", page, pageSize, &rows)
	return rows, total, err
}

// ---- 出入库统计（/api/reports/inbound-stats、/api/reports/outbound-stats）----

// FlowStatRow 单据流统计行（按日）。
type FlowStatRow struct {
	// StatDate 统计日（YYYY-MM-DD；created_at::date，数据库会话时区）。
	StatDate time.Time `json:"stat_date"`
	// OrderCount 当日 DISTINCT 业务单号数（business_no）。
	OrderCount int64   `json:"order_count"`
	Qty        float64 `json:"qty"`
	// Amount 金额估值 = Σ |qty_change| × 估值单价（入库=成本价/出库=销售价，Service 层口径注明）。
	Amount float64 `json:"amount"`
}

const flowStatsBase = `
SELECT l.created_at::date                       AS stat_date,
       COUNT(DISTINCT l.business_no)::bigint    AS order_count,
       SUM(ABS(l.qty_change))::float8           AS qty,
       SUM(ABS(l.qty_change) * s.%s)::float8    AS amount
FROM inventory_ledgers l
JOIN skus s ON s.id = l.sku_id
WHERE l.change_type = ? AND l.created_at >= ? AND l.created_at < ? AND %s
GROUP BY 1`

func (r *repository) flowStats(ctx context.Context, sc Scope, changeType, priceColumn string, from, to time.Time, page, pageSize int) ([]FlowStatRow, int64, error) {
	scCond, scArgs := sc.cond("l.warehouse_id")
	base := fmt.Sprintf(flowStatsBase, priceColumn, scCond)
	// 占位顺序与 SQL 一致：change_type、created_at 下界/上界、仓库范围（flowStatsBase WHERE %s 在最后）。
	args := append(append([]any{}, changeType, from, to), scArgs...)
	var rows []FlowStatRow
	total, err := paged(ctx, r.db, base, args, "stat_date", page, pageSize, &rows)
	return rows, total, err
}

// ---- 库存周转（/api/reports/inventory-turnover）----

// TurnoverRow 库存周转行（按仓 + SKU）。
type TurnoverRow struct {
	WarehouseID   int64      `json:"warehouse_id"`
	WarehouseCode string     `json:"warehouse_code"`
	WarehouseName string     `json:"warehouse_name"`
	SKUID         int64      `json:"sku_id"`
	SKUCode       string     `json:"sku_code"`
	SKUName       string     `json:"sku_name"`
	OutboundQty   float64    `json:"outbound_qty"`
	StartQty      float64    `json:"start_qty"`
	EndQty        float64    `json:"end_qty"`
	AvgInventory  float64    `json:"avg_inventory"`
	LastMovedAt   *time.Time `json:"last_moved_at,omitempty"`
	// TurnoverRate/TurnoverDays Service 层纯函数计算（周转率 = 出库量/平均库存；天数 = 周期天数/周转率）。
	TurnoverRate float64 `json:"turnover_rate"`
	TurnoverDays float64 `json:"turnover_days"`
}

// turnoverBase 期初库存 = 期末库存 − 窗口净变化（流水可重构，inventory-rules §5
// append-only 流水是唯一真相来源）；平均库存 =（期初 + 期末）/ 2，比率在 Service 层纯函数计算。
const turnoverBase = `
WITH cur AS (
    SELECT i.warehouse_id, i.sku_id, SUM(i.total_qty) AS end_qty
    FROM inventory i
    WHERE %s
    GROUP BY 1, 2
), move AS (
    SELECT l.warehouse_id, l.sku_id,
           SUM(CASE WHEN l.change_type = 'OUTBOUND' THEN ABS(l.qty_change) ELSE 0 END) AS outbound_qty,
           SUM(CASE WHEN l.change_type IN ('INBOUND','TRANSFER_IN','OUTBOUND','TRANSFER_OUT','ADJUST')
                    THEN l.qty_change ELSE 0 END) AS net_change
    FROM inventory_ledgers l
    WHERE l.created_at >= ? AND l.created_at < ?
      AND l.change_type IN ('INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','ADJUST')
      AND %s
    GROUP BY 1, 2
), lastm AS (
    SELECT l.warehouse_id, l.sku_id, MAX(l.created_at) AS last_moved_at
    FROM inventory_ledgers l
    WHERE l.change_type IN ('INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','MOVE','ADJUST') AND %s
    GROUP BY 1, 2
)
SELECT c.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       c.sku_id, s.code AS sku_code, p.name AS sku_name,
       c.end_qty::float8                                          AS end_qty,
       COALESCE(m.outbound_qty, 0)::float8                        AS outbound_qty,
       (c.end_qty - COALESCE(m.net_change, 0))::float8            AS start_qty,
       lm.last_moved_at
FROM cur c
LEFT JOIN move m  ON m.warehouse_id = c.warehouse_id AND m.sku_id = c.sku_id
LEFT JOIN lastm lm ON lm.warehouse_id = c.warehouse_id AND lm.sku_id = c.sku_id
JOIN skus s ON s.id = c.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = c.warehouse_id
WHERE %s`

func (r *repository) turnover(ctx context.Context, sc Scope, from, to time.Time, page, pageSize int) ([]TurnoverRow, int64, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	lmCond, lmArgs := sc.cond("l.warehouse_id")
	base := fmt.Sprintf(turnoverBase, scCond, lmCond, lmCond, scCond)
	args := append(append([]any{}, scArgs...), from, to)
	args = append(args, lmArgs...)
	args = append(args, scArgs...)
	var rows []TurnoverRow
	total, err := paged(ctx, r.db, base, args, "c.warehouse_id, c.sku_id", page, pageSize, &rows)
	return rows, total, err
}

// ---- 积压识别（/api/reports/stagnant-stock，inventory-rules §11）----

// StagnantRow 积压识别行。
type StagnantRow struct {
	WarehouseID   int64   `json:"warehouse_id"`
	WarehouseCode string  `json:"warehouse_code"`
	WarehouseName string  `json:"warehouse_name"`
	SKUID         int64   `json:"sku_id"`
	SKUCode       string  `json:"sku_code"`
	SKUName       string  `json:"sku_name"`
	TotalQty      float64 `json:"total_qty"`
	// LastMovedAt 末次移动时间（INBOUND/OUTBOUND/TRANSFER/MOVE/ADJUST 的最后落账；
	// 无任何流水时回退首次入库建账时间）。
	LastMovedAt time.Time `json:"last_moved_at"`
	// IdleDays 未动天数（截至查询时点，按自然 24h 折算）。
	IdleDays int `json:"idle_days"`
	// Tier 分档（"30"/"60"/"90"…，Service 层按阈值清单分档）。
	Tier string `json:"tier"`
}

// stagnantBase 仅取 last_moved <= cutoff（未动天数 ≥ 最小档位）的行，避免全量拉取。
const stagnantBase = `
WITH lastm AS (
    SELECT l.warehouse_id, l.sku_id, MAX(l.created_at) AS last_moved_at
    FROM inventory_ledgers l
    WHERE l.change_type IN ('INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','MOVE','ADJUST') AND %s
    GROUP BY 1, 2
), stock AS (
    SELECT i.warehouse_id, i.sku_id, SUM(i.total_qty) AS total_qty, MIN(i.created_at) AS first_stock_at
    FROM inventory i
    WHERE %s
    GROUP BY 1, 2
    HAVING SUM(i.total_qty) > 0
)
SELECT st.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       st.sku_id, s.code AS sku_code, p.name AS sku_name,
       st.total_qty::float8 AS total_qty,
       COALESCE(lm.last_moved_at, st.first_stock_at) AS last_moved_at
FROM stock st
LEFT JOIN lastm lm ON lm.warehouse_id = st.warehouse_id AND lm.sku_id = st.sku_id
JOIN skus s ON s.id = st.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = st.warehouse_id
WHERE COALESCE(lm.last_moved_at, st.first_stock_at) <= ? AND %s`

func (r *repository) stagnant(ctx context.Context, sc Scope, cutoff time.Time, page, pageSize int) ([]StagnantRow, int64, error) {
	lmCond, lmArgs := sc.cond("l.warehouse_id")
	stCond, stArgs := sc.cond("i.warehouse_id")
	outCond, outArgs := sc.cond("st.warehouse_id")
	base := fmt.Sprintf(stagnantBase, lmCond, stCond, outCond)
	args := append(append([]any{}, lmArgs...), stArgs...)
	args = append(args, cutoff)
	args = append(args, outArgs...)
	var rows []StagnantRow
	total, err := paged(ctx, r.db, base, args, "last_moved_at, st.warehouse_id, st.sku_id", page, pageSize, &rows)
	return rows, total, err
}

// ---- 智能补货建议（/api/reports/replenishment-suggestions，plan §9.3）----

// ReplenishmentRow 补货建议行（计算依据字段与建议值同落，inventory-rules §11 可追溯）。
type ReplenishmentRow struct {
	WarehouseID      int64   `json:"warehouse_id"`
	WarehouseCode    string  `json:"warehouse_code"`
	WarehouseName    string  `json:"warehouse_name"`
	SKUID            int64   `json:"sku_id"`
	SKUCode          string  `json:"sku_code"`
	SKUName          string  `json:"sku_name"`
	DailyAvgSales    float64 `json:"daily_avg_sales"`
	SafetyStock      float64 `json:"safety_stock"`
	IncomingQty      float64 `json:"incoming_qty"`
	CurrentAvailable float64 `json:"current_available"`
	TargetStock      float64 `json:"target_stock"`
	SuggestedQty     float64 `json:"suggested_qty"`
}

// replenishmentBase 在途 = 采购在途（PO 已审核未收齐：qty_ordered − qty_received）
// + 调拨在途（WAREHOUSE 调拨明细 qty_out − qty_in，plan §9.3）；
// 目标库存/建议量在 SQL 计算（分页正确性需要先算后滤后分页），Service 层以同公式复算展示依据。
const replenishmentBase = `
WITH stock AS (
    SELECT i.warehouse_id, i.sku_id, SUM(i.available_qty) AS available_qty
    FROM inventory i
    WHERE %s
    GROUP BY 1, 2
), sales30 AS (
    SELECT l.warehouse_id, l.sku_id, SUM(ABS(l.qty_change)) / 30.0 AS daily_avg
    FROM inventory_ledgers l
    WHERE l.change_type = 'OUTBOUND' AND l.created_at >= ? AND %s
    GROUP BY 1, 2
), po_transit AS (
    SELECT poi.sku_id, po.warehouse_id, SUM(poi.qty_ordered - poi.qty_received) AS qty
    FROM purchase_order_items poi
    JOIN purchase_orders po ON po.id = poi.po_id
    WHERE po.status IN ('APPROVED', 'PARTIAL_RECEIVED')
    GROUP BY 1, 2
), tr_transit AS (
    SELECT ti.sku_id, ti.to_warehouse_id AS warehouse_id, SUM(GREATEST(ti.qty_out - ti.qty_in, 0)) AS qty
    FROM transfer_items ti
    JOIN transfer_orders t ON t.id = ti.transfer_id
    WHERE t.type = 'WAREHOUSE' AND t.status <> 'CANCELLED'
    GROUP BY 1, 2
), base_rows AS (
    SELECT st.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
           st.sku_id, s.code AS sku_code, p.name AS sku_name,
           st.available_qty::float8            AS current_available,
           s.safety_stock::float8              AS safety_stock,
           COALESCE(sv.daily_avg, 0)::float8   AS daily_avg_sales,
           (COALESCE(pt.qty, 0) + COALESCE(tt.qty, 0))::float8 AS incoming_qty,
           GREATEST(s.safety_stock::float8,
                    COALESCE(sv.daily_avg, 0)::float8 * ?) AS target_stock,
           GREATEST(0, GREATEST(s.safety_stock::float8,
                    COALESCE(sv.daily_avg, 0)::float8 * ?)
                    - st.available_qty::float8
                    - (COALESCE(pt.qty, 0) + COALESCE(tt.qty, 0))::float8) AS suggested_qty
    FROM stock st
    LEFT JOIN sales30 sv   ON sv.warehouse_id = st.warehouse_id AND sv.sku_id = st.sku_id
    LEFT JOIN po_transit pt ON pt.warehouse_id = st.warehouse_id AND pt.sku_id = st.sku_id
    LEFT JOIN tr_transit tt ON tt.warehouse_id = st.warehouse_id AND tt.sku_id = st.sku_id
    JOIN skus s ON s.id = st.sku_id
    JOIN products p ON p.id = s.product_id
    JOIN warehouses w ON w.id = st.warehouse_id
    WHERE %s
)
SELECT * FROM base_rows b WHERE %s`

func (r *repository) replenishment(ctx context.Context, sc Scope, salesFrom time.Time, leadPlusBuffer int, onlyShortage bool, page, pageSize int) ([]ReplenishmentRow, int64, error) {
	stCond, stArgs := sc.cond("i.warehouse_id")
	lCond, lArgs := sc.cond("l.warehouse_id")
	bCond, bArgs := sc.cond("st.warehouse_id")
	// 仅缺货行：suggested_qty > 0（默认）；onlyShortage=false 时返回全部参与计算的行。
	tail := "1 = 1"
	if onlyShortage {
		tail = "b.suggested_qty > 0"
	}
	base := fmt.Sprintf(replenishmentBase, stCond, lCond, bCond, tail)
	args := append(append([]any{}, stArgs...), salesFrom)
	args = append(args, lArgs...)
	args = append(args, float64(leadPlusBuffer), float64(leadPlusBuffer))
	args = append(args, bArgs...)
	var rows []ReplenishmentRow
	total, err := paged(ctx, r.db, base, args, "b.warehouse_id, b.sku_id", page, pageSize, &rows)
	return rows, total, err
}

// ---- Dashboard 聚合（GET /api/inventory/summary、GET /api/inventory/alerts；
// reports 实现、inventory 前缀挂载——plan §9.1/M2 §15.2 前端先行契约承接）----

// DashboardSummary 库存总览（StockSummary 契约，snake_case 输出）。
type DashboardSummary struct {
	SKUCount     int64   `json:"sku_count"`
	TotalQty     float64 `json:"total_qty"`
	AvailableQty float64 `json:"available_qty"`
	LockedQty    float64 `json:"locked_qty"`
	FrozenQty    float64 `json:"frozen_qty"`
	// NearExpiryQty 临期/已过期库存量（效期 ≤ 阈值天数的批次现存量，含已过期）。
	NearExpiryQty float64 `json:"near_expiry_qty"`
	// AbnormalQty 异常库存量 = 冻结 + 残次（待检属正常流转态，不计入异常）。
	AbnormalQty float64 `json:"abnormal_qty"`
}

func (r *repository) dashboardSummary(ctx context.Context, sc Scope, expiryDays int) (*DashboardSummary, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	var row DashboardSummary
	err := r.db.WithContext(ctx).Raw(`
		SELECT COUNT(DISTINCT i.sku_id)::bigint          AS sku_count,
		       COALESCE(SUM(i.total_qty), 0)::float8     AS total_qty,
		       COALESCE(SUM(i.available_qty), 0)::float8 AS available_qty,
		       COALESCE(SUM(i.locked_qty), 0)::float8    AS locked_qty,
		       COALESCE(SUM(i.frozen_qty), 0)::float8    AS frozen_qty,
		       COALESCE(SUM(i.frozen_qty + i.defective_qty), 0)::float8 AS abnormal_qty
		FROM inventory i
		WHERE `+scCond, scArgs...).Scan(&row).Error
	if err != nil {
		return nil, fmt.Errorf("reports: 库存总览聚合失败: %w", err)
	}
	bCond, bArgs := sc.cond("i.warehouse_id")
	err = r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(i.total_qty), 0)::float8 AS near_expiry_qty
		FROM inventory i
		JOIN batches b ON b.id = i.batch_id AND i.batch_id > 0
		WHERE `+bCond+`
		  AND b.expiry_date IS NOT NULL
		  AND b.expiry_date <= (CURRENT_DATE + ?::interval)`,
		append(append([]any{}, bArgs...), fmt.Sprintf("%d days", expiryDays))...).Scan(&row.NearExpiryQty).Error
	if err != nil {
		return nil, fmt.Errorf("reports: 临期库存聚合失败: %w", err)
	}
	return &row, nil
}

// AlertItem 库存预警行（StockAlertItem 契约，snake_case 输出）。
type AlertItem struct {
	Level         string  `json:"level"` // low_stock/overstock/near_expiry/expired/slow_moving
	WarehouseID   int64   `json:"warehouse_id"`
	WarehouseCode string  `json:"warehouse_code"`
	WarehouseName string  `json:"warehouse_name"`
	SKUID         int64   `json:"sku_id"`
	SKUCode       string  `json:"sku_code"`
	SKUName       string  `json:"sku_name"`
	CurrentQty    float64 `json:"current_qty"`
	Threshold     float64 `json:"threshold"`
	// BatchNo 批次号（效期类预警携带；其余为空）。
	BatchNo string `json:"batch_no,omitempty"`
	// LastMovedAt 末次移动时间（slow_moving 携带；其余为空）。
	LastMovedAt *time.Time `json:"last_moved_at,omitempty"`
	// Message 预警说明（Service 层按级别模板组装，携计算依据）。
	Message string `json:"message"`
}
