package reports

// 数据驱动端点测试·Dashboard 五端点 + 库存分析/TopN/趋势六端点：
//
//	GET /api/reports/dashboard/today            dashboardToday
//	GET /api/reports/dashboard/trend            dashboardTrend
//	GET /api/reports/dashboard/tasks            dashboardTasks
//	GET /api/reports/dashboard/alerts           dashboardAlertFeed
//	GET /api/reports/dashboard/warehouse-stock  dashboardWarehouseStock
//	GET /api/inventory/summary                  dashboardSummary
//	GET /api/inventory/alerts                   dashboardAlerts
//	GET /api/inventory/analytics                inventoryAnalytics
//	GET /api/inventory/sku-top                  skuTop
//	GET /api/inventory/turnover-trend           turnoverTrend

import (
	"context"
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stockflow/server/internal/auth"
)

// ---- dashboardTodayBundle 夹具（today/tasks/workbench 三端点共享同源聚合） ----

// todayWants dashboardTodayRepo 各计数查询的回放值（dashboard.go dashboardTodayRepo）。
type todayWants struct {
	receive, putaway, pick, check, pack, ship, count_, exception int64
	approvalPO, approvalSO, approvalTO, approvalAdj, approvalCnt int64
	inboundToday, outboundToday                                  int64
	alertTotal, slowMovingTotal                                  int64
	summarySku                                                   int64
	summaryTotal, summaryAvailable, summaryLocked                float64
	summaryFrozen, summaryAbnormal, summaryNear                  float64
	whCount, skuCount, salesOrders                               int64
	mgmtTotal, mgmtValue                                         float64
}

func (w todayWants) approvalTotal() int64 {
	return w.approvalPO + w.approvalSO + w.approvalTO + w.approvalAdj + w.approvalCnt
}

// todayFixture 装配 dashboardTodayBundle 全部查询的夹具（含同文异参的
// todayFlowCount INBOUND/OUTBOUND 按参数分流）。
func todayFixture(t *testing.T, log *queryLog, w todayWants) {
	t.Helper()
	routes := []rptRoute{
		rrouteCount("FROM purchase_orders WHERE status IN ('APPROVED','PARTIAL_RECEIVED')", "", w.receive),
		rrouteCount("FROM putaway_tasks WHERE status IN ('PENDING','IN_PROGRESS','PAUSED')", "", w.putaway),
		rrouteCount("FROM outbound_orders WHERE status IN ('ALLOCATED','PICKING')", "", w.pick),
		rrouteCount("FROM outbound_orders WHERE status IN ('PICKED')", "", w.check),
		rrouteCount("FROM outbound_orders WHERE status IN ('CHECKED')", "", w.pack),
		rrouteCount("FROM outbound_orders WHERE status IN ('PACKED')", "", w.ship),
		rrouteCount("FROM count_orders WHERE status IN ('COUNTING','PENDING_REVIEW')", "", w.count_),
		rrouteCount("FROM purchase_orders WHERE status IN ('PENDING_APPROVAL')", "", w.approvalPO),
		rrouteCount("FROM sales_orders WHERE status IN ('PENDING_APPROVAL')", "", w.approvalSO),
		rrouteCount("FROM transfer_orders WHERE status IN ('PENDING_APPROVAL')", "", w.approvalTO),
		rrouteCount("FROM inventory_adjustments WHERE status IN ('PENDING_APPROVAL')", "", w.approvalAdj),
		rrouteCount("FROM count_orders WHERE status IN ('PENDING_REVIEW')", "", w.approvalCnt),
		rrouteCount("FROM exceptions WHERE status NOT IN", "", w.exception),
		// 预警四分支合并（level=""）：count 先于 list（二重子串区分，均含四分支文本）。
		rrouteCount("'low_stock' AS level", "SELECT COUNT(*) FROM (", w.alertTotal),
		rroute("'low_stock' AS level", "SELECT * FROM (",
			[]string{"level", "warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
				"sku_code", "sku_name", "current_qty", "threshold", "batch_no", "last_moved_at"}),
		// slow_moving 单分支（无 UNION ALL）。
		rrouteCount("'slow_moving' AS level", "SELECT COUNT(*) FROM (", w.slowMovingTotal),
		rroute("'slow_moving' AS level", "SELECT * FROM (",
			[]string{"level", "warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
				"sku_code", "sku_name", "current_qty", "threshold", "batch_no", "last_moved_at"}),
		rroute("COUNT(DISTINCT i.sku_id)::bigint", "",
			[]string{"sku_count", "total_qty", "available_qty", "locked_qty", "frozen_qty", "abnormal_qty"},
			[]any{w.summarySku, w.summaryTotal, w.summaryAvailable, w.summaryLocked,
				w.summaryFrozen, w.summaryAbnormal},
		),
		rrouteScalar("b.expiry_date <= (CURRENT_DATE + ?::interval)", w.summaryNear),
		rrouteCount("SELECT COUNT(*) FROM warehouses WHERE deleted_at IS NULL", "", w.whCount),
		rrouteCount("SELECT COUNT(*) FROM skus WHERE deleted_at IS NULL", "", w.skuCount),
		rroute("COALESCE(SUM(i.total_qty * COALESCE(b.cost_price, s.cost_price, 0)), 0)::float8", "",
			[]string{"total_qty", "stock_value"},
			[]any{w.mgmtTotal, w.mgmtValue},
		),
		rrouteCount("FROM sales_orders WHERE status <> 'CANCELLED'", "", w.salesOrders),
	}
	useFixture(func(q string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		log.record(q, args)
		// todayFlowCount：同文异参（change_type 为首参）。
		if strings.Contains(q, "COUNT(DISTINCT l.business_no) FROM inventory_ledgers") {
			if len(args) > 0 && args[0].Value == "OUTBOUND" {
				return countRow(w.outboundToday)
			}
			return countRow(w.inboundToday)
		}
		// 三增量计数（2026-10-06 效率层一期 B3 additive）：summary 端点在四块计数外
		// 增发 myWorkbenchCounts 单查询，此处回放零值（增量断言归 endpoints_next_test.go）。
		if strings.Contains(q, "AS today_completed_count") {
			return fixtureRows([]string{"mine_count", "timeout_count", "today_completed_count"},
				[]any{int64(0), int64(0), int64(0)})
		}
		// 超时阈值读（task.timeout.pick_hours/putaway_hours，数值文本回放）。
		if strings.Contains(q, "FROM system_configs") && len(args) > 0 {
			return fixtureRows([]string{"value"}, []any{"4"})
		}
		for _, rt := range routes {
			if strings.Contains(q, rt.match) && (rt.count == "" || strings.Contains(q, rt.count)) {
				return rt.cols, rt.rows
			}
		}
		return nil, nil
	})
}

// countRow 标量计数行（列名不影响 Scan）。
func countRow(n int64) ([]string, [][]driver.Value) {
	return fixtureRows([]string{"count"}, []any{n})
}

func sampleTodayWants() todayWants {
	return todayWants{
		receive: 3, putaway: 2, pick: 5, check: 4, pack: 2, ship: 1, count_: 1, exception: 4,
		approvalPO: 1, approvalSO: 2, approvalTO: 1, approvalAdj: 1, approvalCnt: 2,
		inboundToday: 9, outboundToday: 4,
		alertTotal: 6, slowMovingTotal: 2,
		summarySku: 5, summaryTotal: 500.5, summaryAvailable: 400, summaryLocked: 30,
		summaryFrozen: 20, summaryAbnormal: 25, summaryNear: 12,
		whCount: 2, skuCount: 12, salesOrders: 9, mgmtTotal: 500.5, mgmtValue: 6250,
	}
}

// ---- GET /api/reports/dashboard/today ----

func TestDashboardTodayEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	w := sampleTodayWants()
	todayFixture(t, log, w)
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/today")
	var dto todayMetricsDTO
	env.decode(out, &dto)
	if dto.TodayInboundCount != 9 || dto.TodayOutboundCount != 4 {
		t.Fatalf("当日进出应 9/4: %+v", dto)
	}
	// pendingTask = receive+putaway+pick+check+pack+ship+count+exception（不含 approval）。
	if want := int64(3 + 2 + 5 + 4 + 2 + 1 + 1 + 4); dto.PendingTaskCount != want {
		t.Fatalf("PendingTaskCount 应为 %d，实际 %d", want, dto.PendingTaskCount)
	}
	if dto.StockAlertCount != 6 || dto.SlowMovingQty == nil || *dto.SlowMovingQty != 2 {
		t.Fatalf("预警计数应 6/2: %+v", dto)
	}
	if want := w.approvalTotal(); dto.PendingApprovalCount == nil || *dto.PendingApprovalCount != want {
		t.Fatalf("PendingApprovalCount 应为 %d: %+v", want, dto)
	}
	if dto.PendingExceptionCount == nil || *dto.PendingExceptionCount != 4 {
		t.Fatalf("PendingExceptionCount 应为 4: %+v", dto)
	}
	if dto.PendingReceiveCount == nil || *dto.PendingReceiveCount != 3 ||
		dto.PendingPutawayCount == nil || *dto.PendingPutawayCount != 2 ||
		dto.PendingPickCount == nil || *dto.PendingPickCount != 5 ||
		dto.PendingCheckCount == nil || *dto.PendingCheckCount != 4 ||
		dto.PendingPackCount == nil || *dto.PendingPackCount != 2 ||
		dto.PendingShipmentCount == nil || *dto.PendingShipmentCount != 1 ||
		dto.PendingCountCount == nil || *dto.PendingCountCount != 1 {
		t.Fatalf("六作业块计数应逐块回放: %+v", dto)
	}
	if dto.WarehouseCount == nil || *dto.WarehouseCount != 2 ||
		dto.SKUCount == nil || *dto.SKUCount != 12 ||
		dto.OrderCount == nil || *dto.OrderCount != 9 {
		t.Fatalf("管理层视图计数应回放: %+v", dto)
	}
	assertFloat(t, "TotalQty", *dto.TotalQty, 500.5)
	assertFloat(t, "StockValue", *dto.StockValue, 6250)
	assertFloat(t, "NearExpiryQty", *dto.NearExpiryQty, 12)
}

// TestDashboardTodayEmptyScopeFailClosed 空仓库范围：表计数零查询直返 0（fail-closed
// 短路），可见 SQL 一律带 1 = 0 恒假片段（permission.md §4）。
func TestDashboardTodayEmptyScopeFailClosed(t *testing.T) {
	env := newTestEnv(t, newUser(false, auth.DataScopeDepartment))
	log := &queryLog{}
	todayFixture(t, log, todayWants{})
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/today")
	var dto todayMetricsDTO
	env.decode(out, &dto)
	if dto.PendingTaskCount != 0 || dto.StockAlertCount != 0 ||
		dto.PendingApprovalCount == nil || *dto.PendingApprovalCount != 0 {
		t.Fatalf("空范围四计数应全 0: %+v", dto)
	}
	if !log.noneContains("FROM putaway_tasks") ||
		!log.noneContains("FROM purchase_orders WHERE status IN ('APPROVED'") {
		t.Fatalf("空范围不得触发表计数 SQL: %v", log.queries)
	}
	if !log.anyContains("1 = 0") {
		t.Fatalf("流水/预警类可见 SQL 应带 1 = 0 恒假片段: %v", log.queries)
	}
}

// ---- GET /api/reports/dashboard/tasks ----

func TestDashboardTasksEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	w := sampleTodayWants()
	todayFixture(t, log, w)
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/tasks")
	var rows []taskItemDTO
	env.decode(out, &rows)
	want := []struct {
		typ, label, link string
		count            int64
	}{
		{"receive", "待收货", "/purchases/receipts", 3},
		{"putaway", "待上架", "/inbound", 2},
		{"pick", "待拣货", "/picking", 5},
		{"check", "待复核", "/checking", 4},
		{"pack", "待打包", "/packing", 2},
		{"ship", "待发货", "/shipment", 1},
		{"count", "待盘点", "/counts", 1},
		{"approval", "待审核单据", "/tasks", 7},
		{"exception", "待处理异常", "/exceptions", 4},
	}
	if len(rows) != len(want) {
		t.Fatalf("任务概览应 %d 项，实际 %d", len(want), len(rows))
	}
	for i, tc := range want {
		if rows[i].Type != tc.typ || rows[i].Label != tc.label || rows[i].Link != tc.link || rows[i].Count != tc.count {
			t.Fatalf("任务项[%d] 应为 %+v，实际 %+v", i, tc, rows[i])
		}
	}
}

// ---- GET /api/workbench/summary（计数复用 dashboardTodayRepo 同源聚合） ----

func TestWorkbenchSummaryEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	w := sampleTodayWants()
	todayFixture(t, log, w)
	defer useFixture(nil)

	out := env.mustOK("/api/workbench/summary")
	var dto workbenchSummaryDTO
	env.decode(out, &dto)
	if dto.TodoCount != 3 {
		t.Fatalf("todo_count 应为待收货 3，实际 %d", dto.TodoCount)
	}
	if want := w.approvalTotal(); dto.ApprovalCount != want {
		t.Fatalf("approval_count 应为 %d，实际 %d", want, dto.ApprovalCount)
	}
	// task_count = putaway+pick+check+pack+ship+count_（不含 receive/approval/exception）。
	if want := int64(2 + 5 + 4 + 2 + 1 + 1); dto.TaskCount != want {
		t.Fatalf("task_count 应为 %d，实际 %d", want, dto.TaskCount)
	}
	if dto.ExceptionCount != 4 {
		t.Fatalf("exception_count 应为 4，实际 %d", dto.ExceptionCount)
	}
}

// ---- GET /api/reports/dashboard/trend ----

// dashboardTrendFixture 装配 trend 三查询：锚点 / 逐日之后净变化 / 每日进出。
func dashboardTrendFixture(t *testing.T, log *queryLog, anchor float64, netAfter [][2]any, flows [][3]any) {
	t.Helper()
	netRows := make([][]any, 0, len(netAfter))
	for _, p := range netAfter {
		netRows = append(netRows, []any{p[0], p[1]})
	}
	flowRows := make([][]any, 0, len(flows))
	for _, f := range flows {
		flowRows = append(flowRows, []any{f[0], f[1], f[2]})
	}
	useFixture(logAndRoute(log,
		rrouteScalar("COALESCE(SUM(i.total_qty), 0)::float8 FROM inventory i", anchor),
		rroute("generate_series(?::date, ?::date, interval '1 day') AS d(day)", "",
			[]string{"day", "net_after"}, netRows...),
		rroute("SUM(CASE WHEN l.change_type = 'INBOUND'", "",
			[]string{"day", "inbound", "outbound"}, flowRows...),
	))
}

func TestDashboardTrendPresetRange(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll), WithClock(func() time.Time { return now }))
	log := &queryLog{}
	// range=7d → from=09-30，to=10-06；锚点 1000，末三日净变化 50/30/10；10-05 有进出。
	dashboardTrendFixture(t, log, 1000,
		[][2]any{
			{parseT(t, "2026-10-04 00:00:00"), 50.0},
			{parseT(t, "2026-10-05 00:00:00"), 30.0},
			{parseT(t, "2026-10-06 00:00:00"), 10.0},
		},
		[][3]any{{parseT(t, "2026-10-05 00:00:00"), 100.0, 40.0}},
	)
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/trend?range=7d")
	var rows []trendPointDTO
	env.decode(out, &rows)
	if len(rows) != 7 {
		t.Fatalf("7d 档应 7 行，实际 %d", len(rows))
	}
	if rows[0].Date != "2026-09-30" || rows[6].Date != "2026-10-06" {
		t.Fatalf("日期序列应为 09-30..10-06: %s..%s", rows[0].Date, rows[6].Date)
	}
	// 无流水日补零 + 锚点回推：StockQty = 锚点 − 之后净变化。
	if rows[0].StockQty != 1000 || rows[0].Inbound != 0 || rows[0].Outbound != 0 {
		t.Fatalf("首日应补零且 StockQty=锚点: %+v", rows[0])
	}
	if rows[4].StockQty != 950 { // 10-04：1000−50
		t.Fatalf("10-04 日末库存应 950: %+v", rows[4])
	}
	if rows[5].Inbound != 100 || rows[5].Outbound != 40 || rows[5].StockQty != 970 {
		t.Fatalf("10-05 进出/日末应 100/40/970: %+v", rows[5])
	}
	if rows[6].StockQty != 990 { // 10-06：1000−10
		t.Fatalf("10-06 日末库存应 990: %+v", rows[6])
	}
	// generate_series 窗口参数应为 from/to（预设档）。
	args := log.argsOf("generate_series")
	assertTime(t, "trend from", args[0].Value.(time.Time), parseT(t, "2026-09-30 00:00:00"))
	assertTime(t, "trend to", args[1].Value.(time.Time), parseT(t, "2026-10-06 00:00:00"))
}

func TestDashboardTrendCustomRange(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	dashboardTrendFixture(t, log, 1000, nil, nil)
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/trend?range=custom&time_from=2026-10-01&time_to=2026-10-03")
	var rows []trendPointDTO
	env.decode(out, &rows)
	if len(rows) != 3 || rows[0].Date != "2026-10-01" || rows[2].Date != "2026-10-03" {
		t.Fatalf("custom 档应 3 行 10-01..10-03: %+v", rows)
	}
	args := log.argsOf("generate_series")
	assertTime(t, "custom from", args[0].Value.(time.Time), parseT(t, "2026-10-01 00:00:00"))
	assertTime(t, "custom to", args[1].Value.(time.Time), parseT(t, "2026-10-03 00:00:00"))
}

func TestDashboardTrendInvalidRange(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/reports/dashboard/trend?range=7d30", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/reports/dashboard/trend", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/reports/dashboard/trend?range=custom&time_from=2026-10-01",
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/reports/dashboard/trend?range=custom&time_from=2026-10-05&time_to=2026-10-01",
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/reports/dashboard/trend?range=custom&time_from=2025-01-01&time_to=2026-10-01",
		http.StatusBadRequest, "REPORT_RANGE_TOO_LARGE")
}

// ---- GET /api/reports/dashboard/alerts ----

func TestDashboardAlertFeedEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll), WithClock(func() time.Time { return now }))
	log := &queryLog{}
	alertCols := []string{"level", "warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "current_qty", "threshold", "batch_no", "last_moved_at"}
	useFixture(logAndRoute(log,
		rrouteCount("'low_stock' AS level", "SELECT COUNT(*) FROM (", 2),
		rroute("'low_stock' AS level", "SELECT * FROM (", alertCols,
			[]any{"low_stock", int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪 500g",
				5.0, 10.0, "", nil},
			[]any{"slow_moving", int64(1), "WH01", "一号仓", int64(8), "B002", "薯片",
				8.0, 30.0, "", now.AddDate(0, 0, -40)},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/alerts")
	var rows []dashboardAlertRowDTO
	env.decode(out, &rows)
	if len(rows) != 2 {
		t.Fatalf("应 2 条预警，实际 %d", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Type != "low_stock" || rows[0].Level != "low_stock" ||
		rows[0].SKUCode != "A001" || rows[0].ProductName != "凤爪 500g" {
		t.Fatalf("预警行字段应回放: %+v", rows[0])
	}
	if rows[0].Message != "可用库存 5.0000 已低于等于安全库存 10.0000" {
		t.Fatalf("low_stock 文案应携计算依据: %s", rows[0].Message)
	}
	if rows[1].ID != 2 || rows[1].Type != "slow_moving" {
		t.Fatalf("slow_moving 行应回放: %+v", rows[1])
	}
	if !strings.Contains(rows[1].Message, "已 40 天未发生移动") ||
		!strings.Contains(rows[1].Message, "阈值 30 天") {
		t.Fatalf("slow_moving 文案应携未动天数与阈值: %s", rows[1].Message)
	}
	if rows[1].CreatedAt == nil {
		t.Fatalf("slow_moving 应携末次移动时间")
	}
	assertTime(t, "created_at=last_moved_at", rows[1].CreatedAt.Time, now.AddDate(0, 0, -40))
	// handler 固定取前 8 条：明细查询参数 = 四分支参数（效期窗口 30 days、积压档 30、
	// 截止时间）+ LIMIT 8 + OFFSET 0（ORDER BY a.level 唯一标识明细 SQL）。
	args := log.argsOf("ORDER BY a.level")
	if len(args) != 5 || args[0].Value != "30 days" || args[1].Value != int64(30) ||
		args[3].Value != int64(8) || args[4].Value != int64(0) {
		t.Fatalf("明细参数应为 效期窗/积压档/截止时间/LIMIT 8/OFFSET 0: %v", args)
	}
	if _, ok := args[2].Value.(time.Time); !ok {
		t.Fatalf("积压截止时间应为时间类型: %v", args[2])
	}
}

// ---- GET /api/inventory/summary ----

func TestDashboardSummaryEndpoint(t *testing.T) {
	// 注入阈值 [90,30]：临期窗口取降序首位 90 → interval 参数应为 "90 days"。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll),
		WithAlertThresholds(func(context.Context) ([]int, []int, error) {
			return []int{90, 30}, []int{30, 60, 90}, nil
		}),
	)
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("COUNT(DISTINCT i.sku_id)::bigint", "",
			[]string{"sku_count", "total_qty", "available_qty", "locked_qty", "frozen_qty", "abnormal_qty"},
			[]any{int64(5), 500.123456, 400.0, 30.0, 20.0, 25.0},
		),
		rrouteScalar("b.expiry_date <= (CURRENT_DATE + ?::interval)", 12.5),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/summary")
	var dto DashboardSummary
	env.decode(out, &dto)
	assertFloat(t, "total_qty（roundQty）", dto.TotalQty, 500.1235)
	assertFloat(t, "available_qty", dto.AvailableQty, 400)
	assertFloat(t, "abnormal_qty（冻结+残次）", dto.AbnormalQty, 25)
	assertFloat(t, "near_expiry_qty", dto.NearExpiryQty, 12.5)
	args := log.argsOf("b.expiry_date")
	if len(args) < 1 || args[len(args)-1].Value != "90 days" {
		t.Fatalf("临期窗口应取阈值最大档 90 days，实际 %v", args)
	}
}

// ---- GET /api/inventory/alerts ----

func TestDashboardAlertsEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	alertCols := []string{"level", "warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "current_qty", "threshold", "batch_no", "last_moved_at"}
	useFixture(logAndRoute(log,
		rrouteCount("WHERE t.qty <= s.safety_stock", "SELECT COUNT(*) FROM (", 1),
		rroute("WHERE t.qty <= s.safety_stock", "SELECT * FROM (", alertCols,
			[]any{"low_stock", int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪 500g",
				5.0, 10.0, "", nil},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/alerts?level=low_stock&keyword=%E5%87%A4%E7%88%AA&page=1&pageSize=10")
	var page pageData
	env.decode(out, &page)
	var rows []AlertItem
	decodeJSON(t, page.Items, &rows)
	if page.Total != 1 || len(rows) != 1 || rows[0].Level != "low_stock" || rows[0].BatchNo != "" {
		t.Fatalf("low_stock 行应回放: total=%d rows=%+v", page.Total, rows)
	}
	if rows[0].Message != "可用库存 5.0000 已低于等于安全库存 10.0000" {
		t.Fatalf("预警文案应携计算依据: %s", rows[0].Message)
	}
	// keyword 收敛：ILIKE 双参数 + LIMIT/OFFSET。
	if !log.anyContains("ILIKE") {
		t.Fatalf("keyword 应经 ILIKE 收敛: %v", log.queries)
	}
	args := log.argsOf("ORDER BY a.level")
	if len(args) != 4 || args[0].Value != "%凤爪%" || args[1].Value != "%凤爪%" ||
		args[2].Value != int64(10) || args[3].Value != int64(0) {
		t.Fatalf("keyword/分页参数应回放: %v", args)
	}
}

func TestDashboardAlertsNearExpiryLevelFilter(t *testing.T) {
	// near_expiry 与 expired 共用效期分支 + b.level 精确过滤。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	alertCols := []string{"level", "warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "current_qty", "threshold", "batch_no", "last_moved_at"}
	useFixture(logAndRoute(log,
		rrouteCount("b.level IN ('near_expiry','expired')", "SELECT COUNT(*) FROM (", 1),
		rroute("b.level IN ('near_expiry','expired')", "SELECT * FROM (", alertCols,
			[]any{"near_expiry", int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪",
				6.0, 12.0, "B20260101", nil},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/alerts?level=near_expiry")
	var page pageData
	env.decode(out, &page)
	var rows []AlertItem
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 1 || rows[0].BatchNo != "B20260101" {
		t.Fatalf("near_expiry 行应携批次: %+v", rows)
	}
	if rows[0].Message != "批次 B20260101 距到期还有 12 天，现存量 6.0000" {
		t.Fatalf("临期文案应携批次与剩余天数: %s", rows[0].Message)
	}
	if !log.anyContains("AND b.level = 'near_expiry'") {
		t.Fatalf("效期分支合并后应追加 level 精确过滤: %v", log.queries)
	}
	// 效期窗口参数：缺省阈值 [30,15,7,3] 降序首位 30。
	args := log.argsOf("ORDER BY a.level")
	if len(args) < 1 || args[0].Value != "30 days" {
		t.Fatalf("效期窗口参数应为 30 days: %v", args)
	}
}

func TestDashboardAlertsInvalidLevel(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/inventory/alerts?level=bogus", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- GET /api/inventory/analytics ----

func TestInventoryAnalyticsEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll), WithClock(func() time.Time { return now }))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		// ABC 基础行（第 4 行零金额：计入总量/计数但不入档）。
		rroute("SUM(i.total_qty * COALESCE(s.cost_price, 0))::float8 AS value", "GROUP BY 1",
			[]string{"sku_id", "qty", "value"},
			[]any{int64(1), 80.0, 8000.0},
			[]any{int64(2), 15.0, 1500.0},
			[]any{int64(3), 5.0, 500.0},
			[]any{int64(4), 10.0, 0.0},
		),
		rrouteScalar("l.change_type = 'OUTBOUND' AND l.created_at >= ?::date", 200.0),
		rrouteScalar("COALESCE(SUM(i.total_qty), 0)::float8 FROM inventory i", 400.0),
		rrouteScalar("SELECT COALESCE(SUM(l.qty_change), 0)::float8 FROM inventory_ledgers", 100.0),
		rrouteScalar("SUM(i.total_qty * COALESCE(s.cost_price, 0)), 0)::float8", 10000.0),
		// 金额净变化（与数量净变化同 generate_series，按金额特征子串分流）。
		rroute("l.qty_change * COALESCE(s.cost_price, 0)", "",
			[]string{"day", "net_after"},
			[]any{parseT(t, "2026-10-04 00:00:00"), 1500.0},
			[]any{parseT(t, "2026-10-05 00:00:00"), 1000.0},
			[]any{parseT(t, "2026-10-06 00:00:00"), 500.0},
		),
		rroute("generate_series(?::date, ?::date, interval '1 day') AS d(day)", "",
			[]string{"day", "net_after"},
			[]any{parseT(t, "2026-10-04 00:00:00"), 150.0},
			[]any{parseT(t, "2026-10-05 00:00:00"), 100.0},
			[]any{parseT(t, "2026-10-06 00:00:00"), 50.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/analytics?days=3")
	var dto analyticsDTO
	env.decode(out, &dto)
	assertFloat(t, "total_stock_value", dto.TotalStockValue, 10000)
	assertFloat(t, "total_qty（含零金额 SKU）", dto.TotalQty, 110)
	if dto.TotalSKUCount != 4 {
		t.Fatalf("total_sku_count 应为 4，实际 %d", dto.TotalSKUCount)
	}
	// 周转：出库 200 / 平均库存 (期初 300 + 期末 400)/2=350；窗口 3 天 → 3/(200/350)=5.25。
	assertFloat(t, "turnover_rate", dto.TurnoverRate, 200.0/350.0)
	assertFloat(t, "turnover_days", dto.TurnoverDays, 5.25)
	// ABC 80/15/5 分档（零金额 SKU 不入档，前端不得自行分档）。
	if len(dto.ABC) != 3 {
		t.Fatalf("ABC 应 3 档，实际 %d: %+v", len(dto.ABC), dto.ABC)
	}
	wantGrades := []struct {
		grade   string
		count   int64
		amount  float64
		percent float64
	}{{"A", 1, 8000, 80}, {"B", 1, 1500, 15}, {"C", 1, 500, 5}}
	for i, wg := range wantGrades {
		if dto.ABC[i].Grade != wg.grade || dto.ABC[i].SKUCount != wg.count {
			t.Fatalf("ABC[%d] 应为 %s/%d，实际 %+v", i, wg.grade, wg.count, dto.ABC[i])
		}
		assertFloat(t, "value_amount", dto.ABC[i].ValueAmount, wg.amount)
		assertFloat(t, "value_percent", dto.ABC[i].ValuePercent, wg.percent)
	}
	// 趋势：3 行日末口径（锚点 − 之后净变化）。
	if len(dto.Trend) != 3 || dto.Trend[0].Date != "2026-10-04" || dto.Trend[2].Date != "2026-10-06" {
		t.Fatalf("趋势应 3 行 10-04..10-06: %+v", dto.Trend)
	}
	assertFloat(t, "10-04 日末量", dto.Trend[0].TotalQty, 250)
	assertFloat(t, "10-06 日末量", dto.Trend[2].TotalQty, 350)
	assertFloat(t, "10-06 日末金额", dto.Trend[2].StockValue, 9500)
}

func TestInventoryAnalyticsInvalidDays(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/inventory/analytics?days=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/analytics?days=abc", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/analytics?days=367", http.StatusBadRequest, "REPORT_RANGE_TOO_LARGE")
}

// ---- GET /api/inventory/sku-top ----

func TestSkuTopEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("HAVING SUM(i.total_qty) > 0", "",
			[]string{"sku_id", "sku_code", "sku_name", "total_qty", "stock_value"},
			[]any{int64(7), "A001", "凤爪 500g", 100.123456, 1251.54321},
			[]any{int64(8), "B002", "薯片", 60.0, 720.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/sku-top?limit=2&warehouse_id=3")
	var rows []skuTopRow
	env.decode(out, &rows)
	if len(rows) != 2 || rows[0].SKUCode != "A001" {
		t.Fatalf("TopN 行应回放: %+v", rows)
	}
	assertFloat(t, "total_qty（roundQty）", rows[0].TotalQty, 100.1235)
	assertFloat(t, "stock_value（roundQty）", rows[0].StockValue, 1251.5432)
	// 缺省 metric=qty：ORDER BY 段与 warehouse 过滤/limit 参数。
	if !log.anyContains("ORDER BY total_qty DESC") {
		t.Fatalf("缺省应按现存量排序: %v", log.queries)
	}
	if !log.anyContains("i.warehouse_id = ?") {
		t.Fatalf("warehouse_id 过滤应进入 SQL: %v", log.queries)
	}
	args := log.argsOf("HAVING")
	if len(args) != 2 || args[0].Value != int64(3) || args[1].Value != int64(2) {
		t.Fatalf("参数应为 warehouse=3、limit=2: %v", args)
	}

	// metric=value 切换排序段。
	env.mustOK("/api/inventory/sku-top?metric=value")
	if !log.anyContains("ORDER BY stock_value DESC") {
		t.Fatalf("metric=value 应按金额排序: %v", log.queries)
	}
}

func TestSkuTopInvalidParams(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/inventory/sku-top?metric=amount", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/sku-top?limit=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/sku-top?limit=51", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/sku-top?limit=abc", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- GET /api/inventory/turnover-trend ----

func TestTurnoverTrendEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll), WithClock(func() time.Time { return now }))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rrouteScalar("COALESCE(SUM(i.total_qty), 0)::float8 FROM inventory i", 100.0),
		// 净变化窗口自 from 前一日取起（turnoverTrendRepo 日初差分键）。
		rroute("generate_series(?::date, ?::date, interval '1 day') AS d(day)", "",
			[]string{"day", "net_after"},
			[]any{parseT(t, "2026-10-03 00:00:00"), 0.0},
			[]any{parseT(t, "2026-10-04 00:00:00"), 10.0},
			[]any{parseT(t, "2026-10-05 00:00:00"), 20.0},
			[]any{parseT(t, "2026-10-06 00:00:00"), 30.0},
		),
		rroute("l.change_type = 'OUTBOUND' AND l.created_at >= ?::date", "",
			[]string{"day", "outbound"},
			[]any{parseT(t, "2026-10-05 00:00:00"), 50.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/inventory/turnover-trend?days=3")
	var rows []turnoverTrendRow
	env.decode(out, &rows)
	if len(rows) != 3 || rows[0].Date != "2026-10-04" || rows[2].Date != "2026-10-06" {
		t.Fatalf("days=3 应 3 行 10-04..10-06: %+v", rows)
	}
	// 10-04：日初=锚点−0=100、日末=100−10=90、均值 95、无出库 → 0。
	assertFloat(t, "10-04 日末", rows[0].EndQty, 90)
	assertFloat(t, "10-04 均值", rows[0].AvgInventory, 95)
	assertFloat(t, "10-04 周转率", rows[0].TurnoverRate, 0)
	// 10-05：日初 90、日末 80、均值 85、出库 50 → 50/85=0.5882。
	assertFloat(t, "10-05 出库", rows[1].OutboundQty, 50)
	assertFloat(t, "10-05 均值", rows[1].AvgInventory, 85)
	assertFloat(t, "10-05 周转率", rows[1].TurnoverRate, 0.5882)
	// 10-06：日末 70、均值 75、无出库 → 0（avg>0 但 outbound=0 记 0）。
	assertFloat(t, "10-06 日末", rows[2].EndQty, 70)
	assertFloat(t, "10-06 周转率", rows[2].TurnoverRate, 0)

	// 缺省 30 天：连续 30 行。
	out2 := env.mustOK("/api/inventory/turnover-trend")
	var rows30 []turnoverTrendRow
	env.decode(out2, &rows30)
	if len(rows30) != 30 || rows30[0].Date != "2026-09-07" {
		t.Fatalf("缺省应 30 行自 %s 起，实际 %d 行首 %s", "2026-09-07", len(rows30), rows30[0].Date)
	}
}

func TestTurnoverTrendInvalidDays(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/inventory/turnover-trend?days=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/turnover-trend?days=abc", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/inventory/turnover-trend?days=367", http.StatusBadRequest, "REPORT_RANGE_TOO_LARGE")
}

// ---- GET /api/reports/dashboard/warehouse-stock ----

func TestDashboardWarehouseStockEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("LEFT JOIN inventory i ON i.warehouse_id = w.id", "",
			[]string{"warehouse_code", "warehouse_name", "sku_count", "total_qty", "bin_utilization"},
			[]any{"WH01", "一号仓", int64(5), 500.5, 62.5},
			[]any{"WH02", "二号仓", int64(0), 0.0, 0.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/dashboard/warehouse-stock")
	var rows []warehouseStockDTO
	env.decode(out, &rows)
	if len(rows) != 2 {
		t.Fatalf("应 2 仓，实际 %d", len(rows))
	}
	if rows[0].WarehouseCode != "WH01" || rows[0].SKUCount != 5 || rows[0].BinUtilization != 62.5 {
		t.Fatalf("仓库分布行应回放: %+v", rows[0])
	}
	// 零作业/零库存仓返回零行（可见仓全集 LEFT JOIN 形态）。
	if rows[1].WarehouseCode != "WH02" || rows[1].TotalQty != 0 || rows[1].BinUtilization != 0 {
		t.Fatalf("零库存仓应返回零行: %+v", rows[1])
	}
	if !log.anyContains("b.status = 'ENABLED'") {
		t.Fatalf("库位利用率应仅统计启用库位: %v", log.queries)
	}
}
