package reports

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GET /api/inventory/alerts 五类预警查询（与前端 StockAlertItem 契约 level 值域对齐：
// low_stock/overstock/near_expiry/expired/slow_moving；reports 实现、inventory 前缀挂载——
// plan §9.1）。按 level 过滤时只拼对应分支，避免无谓扫描；仓库数据权限过滤逐分支注入，
// keyword（SKU 编码/商品名称 ILIKE）逐分支收敛；占位符参数按分支拼接顺序逐段组装。

// alertBranch 单分支查询段：sql 为 UNION 段文本，args 与其占位符顺序一一对应。
type alertBranch struct {
	sql  string
	args []any
}

// buildAlertBranch 构建单分支（level：low_stock/overstock/expiry/slow_moving）。
// scCond/scArgs 仓库范围（inventory/stock 聚合子查询），lmCond/lmArgs 流水子查询仓库范围，
// kw/kwArgs SKU 编码与商品名称 ILIKE（拼在各分支 WHERE 尾部，占位符位置随分支而异）。
func buildAlertBranch(level, scCond string, scArgs []any, lmCond string, lmArgs []any, kw string, kwArgs []any, expiryDays, minStagnantDays int) alertBranch {
	interval := func(days int) any { return fmt.Sprintf("%d days", days) }
	stockAgg := func(qtyExpr string) string {
		return `
    SELECT i.warehouse_id, i.sku_id, SUM(` + qtyExpr + `) AS qty
    FROM inventory i
    WHERE ` + scCond + `
    GROUP BY 1, 2`
	}
	branch := alertBranch{}
	switch level {
	case "low_stock":
		branch.sql = `
SELECT 'low_stock' AS level, t.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       t.sku_id, s.code AS sku_code, p.name AS sku_name,
       t.qty::float8 AS current_qty, s.safety_stock::float8 AS threshold,
       '' AS batch_no, NULL::timestamptz AS last_moved_at
FROM (` + stockAgg("i.available_qty") + `) t
JOIN skus s ON s.id = t.sku_id AND s.safety_stock > 0
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = t.warehouse_id
WHERE t.qty <= s.safety_stock` + kw
		branch.args = append(append([]any{}, scArgs...), kwArgs...)
	case "overstock":
		branch.sql = `
SELECT 'overstock' AS level, t.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       t.sku_id, s.code AS sku_code, p.name AS sku_name,
       t.qty::float8 AS current_qty, s.max_stock::float8 AS threshold,
       '' AS batch_no, NULL::timestamptz AS last_moved_at
FROM (` + stockAgg("i.total_qty") + `) t
JOIN skus s ON s.id = t.sku_id AND s.max_stock > 0
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = t.warehouse_id
WHERE t.qty > s.max_stock` + kw
		branch.args = append(append([]any{}, scArgs...), kwArgs...)
	case "expiry":
		// 批次台账 × 现存量（批次为 SKU 维度台账，warehouse 维度在 inventory 行上）；
		// threshold = 剩余效期天数（0=已过期），near_expiry/expired 由 CASE 判定。
		branch.sql = `
SELECT b.level, t.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       t.sku_id, s.code AS sku_code, p.name AS sku_name,
       t.qty::float8 AS current_qty, b.threshold::float8 AS threshold,
       b.batch_no, NULL::timestamptz AS last_moved_at
FROM (
    SELECT i.warehouse_id, i.sku_id, i.batch_id, SUM(i.total_qty) AS qty
    FROM inventory i
    WHERE ` + scCond + `
    GROUP BY 1, 2, 3
    HAVING SUM(i.total_qty) > 0
) t
JOIN (
    SELECT b.id, b.batch_no, b.sku_id,
           CASE WHEN b.expiry_date <= CURRENT_DATE THEN 0
                ELSE (b.expiry_date - CURRENT_DATE)::int END AS threshold,
           CASE WHEN b.expiry_date <= CURRENT_DATE THEN 'expired'
                ELSE 'near_expiry' END AS level
    FROM batches b
    WHERE b.expiry_date IS NOT NULL
      AND b.expiry_date <= (CURRENT_DATE + ?::interval)
) b ON b.id = t.batch_id AND b.sku_id = t.sku_id
JOIN skus s ON s.id = t.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = t.warehouse_id
WHERE b.level IN ('near_expiry','expired')` + kw
		branch.args = append(append([]any{}, scArgs...), interval(expiryDays))
		branch.args = append(branch.args, kwArgs...)
	case "slow_moving":
		// 与 /api/reports/stagnant-stock 同口径：末次移动（缺流水回退首次建账）超最小档位。
		branch.sql = `
SELECT 'slow_moving' AS level, st.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       st.sku_id, s.code AS sku_code, p.name AS sku_name,
       st.qty::float8 AS current_qty, ?::float8 AS threshold,
       '' AS batch_no, lm.last_moved_at
FROM (
    SELECT i.warehouse_id, i.sku_id, SUM(i.total_qty) AS qty, MIN(i.created_at) AS first_stock_at
    FROM inventory i
    WHERE ` + scCond + `
    GROUP BY 1, 2
    HAVING SUM(i.total_qty) > 0
) st
LEFT JOIN (
    SELECT l.warehouse_id, l.sku_id, MAX(l.created_at) AS last_moved_at
    FROM inventory_ledgers l
    WHERE l.change_type IN ('INBOUND','OUTBOUND','TRANSFER_IN','TRANSFER_OUT','MOVE','ADJUST') AND ` + lmCond + `
    GROUP BY 1, 2
) lm ON lm.warehouse_id = st.warehouse_id AND lm.sku_id = st.sku_id
JOIN skus s ON s.id = st.sku_id
JOIN products p ON p.id = s.product_id
JOIN warehouses w ON w.id = st.warehouse_id
WHERE COALESCE(lm.last_moved_at, st.first_stock_at) <= ?` + kw
		branch.args = append(append([]any{}, scArgs...), lmArgs...)
		// slow_moving 截止时间为 timestamptz 比较（`<= ?` 无显式 cast——参数必须携带
		// 时间类型；此前传 "N days" 文本被 PG 按 timestamptz 解析失败 22007，真库回归
		// 2026-10-04 修复。与 /api/reports/stagnant-stock 的 cutoff time.Time 同口径）。
		cutoff := time.Now().AddDate(0, 0, -minStagnantDays)
		branch.args = append(branch.args, minStagnantDays, cutoff)
		branch.args = append(branch.args, kwArgs...)
	}
	return branch
}

// levelBranches level 过滤 → 参与合并的分支键（near_expiry/expired 共用效期分支，
// 合并后追加 b.level 精确过滤；空 level = 全部四段合并）。
func levelBranches(level string) ([]string, bool) {
	switch level {
	case "":
		return []string{"low_stock", "overstock", "expiry", "slow_moving"}, false
	case "low_stock", "overstock", "slow_moving":
		return []string{level}, false
	case "near_expiry", "expired":
		return []string{"expiry"}, true
	default:
		return nil, false
	}
}

func (r *repository) alerts(ctx context.Context, sc Scope, level, keyword string, expiryDays, minStagnantDays int, page, pageSize int) ([]AlertItem, int64, error) {
	scCond, scArgs := sc.cond("i.warehouse_id")
	lmCond, lmArgs := sc.cond("l.warehouse_id")
	kw, kwArgs := "", []any{}
	if keyword != "" {
		kw = " AND (s.code ILIKE ? OR p.name ILIKE ?)"
		kwArgs = []any{"%" + keyword + "%", "%" + keyword + "%"}
	}
	keys, filterLevel := levelBranches(level)
	if keys == nil {
		return nil, 0, fmt.Errorf("未知预警级别: %q", level)
	}
	parts := make([]string, 0, len(keys))
	var args []any
	for _, k := range keys {
		br := buildAlertBranch(k, scCond, scArgs, lmCond, lmArgs, kw, kwArgs, expiryDays, minStagnantDays)
		if filterLevel {
			br.sql += ` AND b.level = '` + level + `'`
		}
		parts = append(parts, br.sql)
		args = append(args, br.args...)
	}
	base := "SELECT * FROM (" + strings.Join(parts, "\nUNION ALL\n") + ") a"

	var rows []AlertItem
	total, err := paged(ctx, r.db, base, args, "a.level, a.warehouse_id, a.sku_id, a.batch_no", page, pageSize, &rows)
	return rows, total, err
}
