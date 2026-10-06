package reports

// 数据驱动端点测试·业务域聚合分析十一端点：
//
//	GET /api/inbounds/status-composition    inboundStatusComposition
//	GET /api/inbounds/supplier-rank         inboundSupplierRank
//	GET /api/outbounds/completion-rate      outboundCompletionRate
//	GET /api/outbounds/product-rank         outboundProductRank
//	GET /api/warehouses/workload            warehouseWorkload
//	GET /api/purchases/analytics/trend      purchaseTrend
//	GET /api/purchases/supplier-rank        purchaseSupplierRank
//	GET /api/purchases/status-composition   purchaseStatusComposition
//	GET /api/sales/analytics/trend          salesTrend
//	GET /api/sales/product-rank             salesProductRank
//	GET /api/sales/status-composition       salesStatusComposition

import (
	"net/http"
	"testing"

	"github.com/stockflow/server/internal/auth"
)

// ---- 状态构成三端点（共享形态） ----

// statusCompositionCase 状态构成端点通用断言：items 回放 + total 求和 + 软删口径。
func statusCompositionCase(t *testing.T, path, tableSub, extraMust, extraNot string) {
	t.Helper()
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("SELECT status, COUNT(*)::bigint AS count FROM "+tableSub, "",
			[]string{"status", "count"},
			[]any{"DRAFT", int64(2)},
			[]any{"APPROVED", int64(5)},
			[]any{"CLOSED", int64(1)},
		),
	))
	defer useFixture(nil)

	out := env.mustOK(path)
	var dto statusCompositionDTO
	env.decode(out, &dto)
	if dto.Total != 8 {
		t.Fatalf("total 应为各态求和 8，实际 %d", dto.Total)
	}
	if len(dto.Items) != 3 || dto.Items[0].Status != "DRAFT" || dto.Items[0].Count != 2 ||
		dto.Items[2].Status != "CLOSED" || dto.Items[2].Count != 1 {
		t.Fatalf("状态构成行应回放: %+v", dto.Items)
	}
	if !log.anyContains(extraMust) {
		t.Fatalf("SQL 应含 %s: %v", extraMust, log.queries)
	}
	if extraNot != "" && !log.noneContains(extraNot) {
		t.Fatalf("SQL 不得含 %s（无软删域不加过滤）: %v", extraNot, log.queries)
	}
}

func TestInboundStatusCompositionEndpoint(t *testing.T) {
	// 入库单内嵌软删（迁移 000016）：聚合 SQL 显式 deleted_at IS NULL。
	statusCompositionCase(t, "/api/inbounds/status-composition", "inbound_orders",
		"deleted_at IS NULL", "")
}

func TestPurchaseStatusCompositionEndpoint(t *testing.T) {
	statusCompositionCase(t, "/api/purchases/status-composition", "purchase_orders",
		"deleted_at IS NULL", "")
}

func TestSalesStatusCompositionEndpoint(t *testing.T) {
	// sales_orders 无软删列（000016 注）：不加 deleted_at 过滤。
	statusCompositionCase(t, "/api/sales/status-composition", "sales_orders",
		"AND 1 = 1", "deleted_at")
}

// TestStatusCompositionRepoError 仓储查询失败 → 500 统一信封（fail-loud 夹具）。
func TestStatusCompositionRepoError(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	useFixture(nil) // 未命中即报错
	defer useFixture(nil)
	env.mustErr("/api/inbounds/status-composition", http.StatusInternalServerError, "COMMON_INTERNAL_ERROR")
}

// ---- GET /api/inbounds/supplier-rank ----

func TestInboundSupplierRankEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM inbound_orders io", "LIMIT ?",
			[]string{"supplier_id", "supplier_code", "supplier_name", "inbound_count", "received_qty"},
			[]any{int64(11), "S001", "供应商一", int64(3), 120.5},
			[]any{int64(12), "S002", "供应商二", int64(1), 30.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inbounds/supplier-rank?time_from=2026-10-01&time_to=2026-10-03&limit=2")
	var rows []inboundSupplierRankRow
	env.decode(out, &rows)
	if len(rows) != 2 || rows[0].SupplierID != 11 || rows[0].SupplierCode != "S001" ||
		rows[0].InboundCount != 3 {
		t.Fatalf("排行行应回放: %+v", rows)
	}
	assertFloat(t, "received_qty", rows[0].ReceivedQty, 120.5)
	// 口径：仅 PURCHASE 来源入库经 po_no 关联供应商（OTHER 不入榜）。
	if !log.anyContains("io.source_type = 'PURCHASE'") || !log.anyContains("po.po_no = io.source_no") {
		t.Fatalf("SQL 应仅统计 PURCHASE 来源: %v", log.queries)
	}
	args := log.argsOf("LIMIT ?")
	if len(args) != 3 || args[2].Value != int64(2) {
		t.Fatalf("limit 参数应为 2: %v", args)
	}
	requireRangeInvalidCases(t, "/api/inbounds/supplier-rank")
}

func TestInboundSupplierRankInvalidLimit(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/inbounds/supplier-rank?limit=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inbounds/supplier-rank?limit=51", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inbounds/supplier-rank?limit=abc", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- GET /api/outbounds/completion-rate ----

func TestOutboundCompletionRateEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("COUNT(*) FILTER (WHERE status = 'CANCELLED')", "",
			[]string{"order_total", "cancelled", "denominator", "shipped_all", "closed"},
			[]any{int64(10), int64(2), int64(8), int64(5), int64(1)},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/outbounds/completion-rate?time_from=2026-10-01&time_to=2026-10-03")
	var dto completionRateDTO
	env.decode(out, &dto)
	if dto.OrderTotal != 10 || dto.Cancelled != 2 || dto.Denominator != 8 ||
		dto.ShippedAll != 5 || dto.Closed != 1 {
		t.Fatalf("计数应回放: %+v", dto)
	}
	// in_progress = 分母 − (SHIPPED_ALL+CLOSED)；完成率 = 6/8×100=75。
	if dto.InProgress != 2 {
		t.Fatalf("in_progress 应为 2，实际 %d", dto.InProgress)
	}
	assertFloat(t, "completion_rate", dto.CompletionRate, 75)
	if !log.anyContains("status = 'SHIPPED_ALL'") || !log.anyContains("status = 'CLOSED'") {
		t.Fatalf("分子应含 SHIPPED_ALL 与 CLOSED: %v", log.queries)
	}
	requireRangeInvalidCases(t, "/api/outbounds/completion-rate")
}

func TestOutboundCompletionRateZeroDenominator(t *testing.T) {
	// 窗口内无单据：分母 0 → 完成率记 0（不造假分母）。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	useFixture(fixtureRoutes(
		rroute("COUNT(*) FILTER (WHERE status = 'CANCELLED')", "",
			[]string{"order_total", "cancelled", "denominator", "shipped_all", "closed"},
			[]any{int64(0), int64(0), int64(0), int64(0), int64(0)},
		),
	))
	defer useFixture(nil)
	out := env.mustOK("/api/outbounds/completion-rate")
	var dto completionRateDTO
	env.decode(out, &dto)
	if dto.Denominator != 0 || dto.CompletionRate != 0 || dto.InProgress != 0 {
		t.Fatalf("空窗口应全零: %+v", dto)
	}
}

// ---- GET /api/outbounds/product-rank ----

func TestOutboundProductRankEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("SUM(ABS(l.qty_change) * s.sale_price)::float8 AS amount", "LIMIT ?",
			[]string{"sku_id", "sku_code", "sku_name", "outbound_qty", "amount"},
			[]any{int64(7), "A001", "凤爪 500g", 120.123456, 963.54321},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/outbounds/product-rank?time_from=2026-10-01&time_to=2026-10-03&limit=1")
	var dto outboundProductRankDTO
	env.decode(out, &dto)
	if dto.Valuation.Basis != "sale_price" {
		t.Fatalf("估值口径应随响应披露 sale_price，实际 %s", dto.Valuation.Basis)
	}
	if len(dto.Items) != 1 || dto.Items[0].SKUID != 7 {
		t.Fatalf("排行行应回放: %+v", dto.Items)
	}
	assertFloat(t, "outbound_qty（roundQty）", dto.Items[0].OutboundQty, 120.1235)
	assertFloat(t, "amount（roundQty）", dto.Items[0].Amount, 963.5432)
	args := log.argsOf("LIMIT ?")
	if len(args) != 3 || args[2].Value != int64(1) {
		t.Fatalf("limit 参数应为 1: %v", args)
	}
	requireRangeInvalidCases(t, "/api/outbounds/product-rank")
	env.mustErr("/api/outbounds/product-rank?limit=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/outbounds/product-rank?limit=51", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- GET /api/warehouses/workload ----

func TestWarehouseWorkloadEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FILTER (WHERE l.change_type = 'INBOUND')", "",
			[]string{"warehouse_id", "warehouse_code", "warehouse_name",
				"inbound_order_count", "outbound_order_count", "inbound_qty", "outbound_qty"},
			[]any{int64(1), "WH01", "一号仓", int64(3), int64(2), 100.5, 80.25},
			[]any{int64(2), "WH02", "二号仓", int64(0), int64(0), 0.0, 0.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/warehouses/workload?time_from=2026-10-01&time_to=2026-10-03")
	var rows []warehouseWorkloadRow
	env.decode(out, &rows)
	if len(rows) != 2 {
		t.Fatalf("应 2 仓（零作业仓返回零行），实际 %d", len(rows))
	}
	if rows[0].WarehouseCode != "WH01" || rows[0].InboundOrderCount != 3 || rows[0].OutboundOrderCount != 2 {
		t.Fatalf("作业量应回放: %+v", rows[0])
	}
	assertFloat(t, "inbound_qty", rows[0].InboundQty, 100.5)
	assertFloat(t, "outbound_qty", rows[0].OutboundQty, 80.25)
	if rows[1].InboundQty != 0 || rows[1].OutboundOrderCount != 0 {
		t.Fatalf("零作业仓应返回零行: %+v", rows[1])
	}
	if !log.anyContains("w.deleted_at IS NULL") {
		t.Fatalf("仓库集应排除软删: %v", log.queries)
	}
	requireRangeInvalidCases(t, "/api/warehouses/workload")
}

// ---- GET /api/purchases/analytics/trend ----

func TestPurchaseTrendEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM purchase_orders o", "",
			[]string{"date", "order_count", "amount"},
			[]any{"2026-10-01", int64(2), 1000.5},
			[]any{"2026-10-02", int64(1), 300.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/purchases/analytics/trend?time_from=2026-10-01&time_to=2026-10-03")
	var dto orderTrendDTO
	env.decode(out, &dto)
	if dto.Metric != "order_amount" {
		t.Fatalf("metric 应显式披露 order_amount，实际 %s", dto.Metric)
	}
	if len(dto.Items) != 2 || dto.Items[0].Date != "2026-10-01" || dto.Items[0].OrderCount != 2 {
		t.Fatalf("趋势行应回放: %+v", dto.Items)
	}
	assertFloat(t, "amount", dto.Items[0].Amount, 1000.5)
	// 口径：DRAFT/CANCELLED 不计业绩 + 软删过滤。
	if !log.anyContains("status NOT IN ('DRAFT', 'CANCELLED')") || !log.anyContains("o.deleted_at IS NULL") {
		t.Fatalf("SQL 应排除草稿/取消并过滤软删: %v", log.queries)
	}
	requireRangeInvalidCases(t, "/api/purchases/analytics/trend")
}

// ---- GET /api/purchases/supplier-rank ----

func TestPurchaseSupplierRankEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("LEFT JOIN suppliers sp ON sp.id = o.supplier_id", "LIMIT ?",
			[]string{"supplier_id", "supplier_code", "supplier_name", "order_count", "total_amount"},
			[]any{int64(11), "S001", "供应商一", int64(3), 3000.5},
			[]any{int64(12), "S002", "供应商二", int64(1), 500.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/purchases/supplier-rank?limit=2")
	var rows []purchaseSupplierRankRow
	env.decode(out, &rows)
	if len(rows) != 2 || rows[0].SupplierName != "供应商一" || rows[0].OrderCount != 3 {
		t.Fatalf("排行行应回放: %+v", rows)
	}
	assertFloat(t, "total_amount", rows[0].TotalAmount, 3000.5)
	if !log.anyContains("status NOT IN ('DRAFT', 'CANCELLED')") || !log.anyContains("o.deleted_at IS NULL") {
		t.Fatalf("SQL 应排除草稿/取消并过滤软删: %v", log.queries)
	}
	env.mustErr("/api/purchases/supplier-rank?limit=51", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	requireRangeInvalidCases(t, "/api/purchases/supplier-rank")
}

// ---- GET /api/sales/analytics/trend ----

func TestSalesTrendEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM sales_orders o", "",
			[]string{"date", "order_count", "amount"},
			[]any{"2026-10-01", int64(4), 2200.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/sales/analytics/trend?time_from=2026-10-01&time_to=2026-10-03")
	var dto orderTrendDTO
	env.decode(out, &dto)
	if dto.Metric != "order_amount" || len(dto.Items) != 1 || dto.Items[0].OrderCount != 4 {
		t.Fatalf("趋势行应回放（订单金额口径）: %+v", dto)
	}
	assertFloat(t, "amount", dto.Items[0].Amount, 2200.0)
	// 口径：三态排除（多 REJECTED）+ 无软删过滤。
	if !log.anyContains("status NOT IN ('DRAFT', 'REJECTED', 'CANCELLED')") {
		t.Fatalf("SQL 应排除草稿/驳回/取消: %v", log.queries)
	}
	if !log.noneContains("deleted_at") {
		t.Fatalf("sales_orders 无软删列，不得加过滤: %v", log.queries)
	}
	requireRangeInvalidCases(t, "/api/sales/analytics/trend")
}

// ---- GET /api/sales/product-rank ----

func TestSalesProductRankEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM sales_order_items i", "LIMIT ?",
			[]string{"sku_id", "sku_code", "sku_name", "order_count", "qty", "amount"},
			[]any{int64(7), "A001", "凤爪 500g", int64(3), 30.0, 297.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/sales/product-rank?time_from=2026-10-01&time_to=2026-10-03&limit=1")
	var rows []salesProductRankRow
	env.decode(out, &rows)
	if len(rows) != 1 || rows[0].SKUID != 7 || rows[0].OrderCount != 3 {
		t.Fatalf("排行行应回放: %+v", rows)
	}
	assertFloat(t, "qty（下单量）", rows[0].Qty, 30)
	assertFloat(t, "amount（行金额）", rows[0].Amount, 297)
	// 缺省 sort=qty。
	if !log.anyContains("ORDER BY qty DESC") {
		t.Fatalf("缺省应按 qty 排序: %v", log.queries)
	}
	if !log.anyContains("status NOT IN ('DRAFT', 'REJECTED', 'CANCELLED')") {
		t.Fatalf("SQL 应排除草稿/驳回/取消: %v", log.queries)
	}
	args := log.argsOf("LIMIT ?")
	if len(args) != 3 || args[2].Value != int64(1) {
		t.Fatalf("limit 参数应为 1: %v", args)
	}

	// sort=amount 切换排序段。
	env.mustOK("/api/sales/product-rank?sort=amount")
	if !log.anyContains("ORDER BY amount DESC") {
		t.Fatalf("sort=amount 应按金额排序: %v", log.queries)
	}
	requireRangeInvalidCases(t, "/api/sales/product-rank")
	env.mustErr("/api/sales/product-rank?sort=price", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/sales/product-rank?limit=51", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}
