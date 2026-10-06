package reports

// 数据驱动端点测试·报表核心七端点 + flow-trend + 数据范围（Scope）SQL 语义：
//
//	GET /api/reports                            catalog
//	GET /api/reports/inventory-summary          inventorySummary
//	GET /api/reports/inbound-stats              inboundStats
//	GET /api/reports/outbound-stats             outboundStats
//	GET /api/reports/inventory-turnover         turnover
//	GET /api/reports/stagnant-stock             stagnantStock
//	GET /api/reports/replenishment-suggestions  replenishment
//	GET /api/reports/flow-trend                 flowTrend
//
// 聚合 SQL 的真库行为属 integration 面（service_test.go 头注同口径）；本文件以
// fakedb_test.go 夹具回放聚合行，断言 handler 参数绑定/校验、统一信封与分页形态、
// Service 层加工（roundQty/turnoverRate/积压分档过滤/补货公式/估值口径披露）。

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stockflow/server/internal/auth"
)

// ---- GET /api/reports（catalog，无 DB 依赖）----

func TestCatalogEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	out := env.mustOK("/api/reports")
	var items []reportCatalogItem
	env.decode(out, &items)
	want := []string{"inventory-summary", "inbound-stats", "outbound-stats",
		"inventory-turnover", "stagnant-stock", "replenishment-suggestions"}
	if len(items) != len(want) {
		t.Fatalf("目录应含 %d 项，实际 %d", len(want), len(items))
	}
	for i, key := range want {
		if items[i].Key != key {
			t.Fatalf("目录第 %d 项 key 应为 %s，实际 %s", i, key, items[i].Key)
		}
		if items[i].Name == "" || items[i].Category == "" {
			t.Fatalf("目录项 %s 缺少名称/分类: %+v", key, items[i])
		}
	}
}

// ---- GET /api/reports/inventory-summary ----

func TestInventorySummaryEndpoint(t *testing.T) {
	// SPECIFIED_WAREHOUSE 快照 [2,5]：范围条件、warehouse_id/sku_id 过滤、分页与
	// roundQty 加工一并断言（行值 100.123456 → 100.1235）。
	env := newTestEnv(t, newUser(false, auth.DataScopeSpecifiedWh, 2, 5))
	log := &queryLog{}
	summaryCols := []string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "total_qty", "available_qty", "locked_qty", "frozen_qty",
		"pending_inspect_qty", "defective_qty", "stock_value"}
	useFixture(logAndRoute(log,
		rrouteCount("GROUP BY i.warehouse_id, w.code", "SELECT COUNT(*) FROM (", 2),
		rroute("GROUP BY i.warehouse_id, w.code", "LIMIT ? OFFSET ?", summaryCols,
			[]any{int64(2), "WH01", "一号仓", int64(7), "A001", "凤爪 500g",
				100.123456, 60.5, 10.25, 5.125, 15.0625, 9.2, 1251.54321},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/inventory-summary?page=2&pageSize=1&warehouse_id=2&sku_id=7")
	var page pageData
	env.decode(out, &page)
	if page.Page != 2 || page.PageSize != 1 || page.Total != 2 {
		t.Fatalf("分页信封应为 2/1/total=2，实际 %+v", page)
	}
	var rows []InventorySummaryRow
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 1 {
		t.Fatalf("第 2 页应恰 1 行，实际 %d", len(rows))
	}
	r := rows[0]
	if r.SKUID != 7 || r.SKUCode != "A001" || r.WarehouseCode != "WH01" {
		t.Fatalf("行标识字段应原样回放: %+v", r)
	}
	assertFloat(t, "total_qty（roundQty 4 位小数）", r.TotalQty, 100.1235)
	assertFloat(t, "available_qty", r.AvailableQty, 60.5)
	assertFloat(t, "locked_qty", r.LockedQty, 10.25)
	assertFloat(t, "frozen_qty", r.FrozenQty, 5.125)
	assertFloat(t, "pending_inspect_qty", r.PendingInspectQty, 15.0625)
	assertFloat(t, "defective_qty", r.DefectiveQty, 9.2)
	assertFloat(t, "stock_value", r.StockValue, 1251.5432)

	// 明细查询参数顺序（repository.go inventorySummary）：范围[2,5] + 过滤[2,7] + LIMIT/OFFSET。
	args := log.argsOf("LIMIT ? OFFSET ?")
	if len(args) != 6 {
		t.Fatalf("明细查询应 6 参数（范围2+过滤2+分页2），实际 %v", args)
	}
	for i, want := range []int64{2, 5, 2, 7, 1, 1} { // page=2/pageSize=1 → LIMIT 1 OFFSET 1
		if args[i].Value != want {
			t.Fatalf("明细参数[%d] 应为 %d，实际 %v", i, want, args[i].Value)
		}
	}
	if !log.anyContains("i.warehouse_id IN (?,?)") || !log.noneContains("1 = 0") {
		t.Fatalf("SPECIFIED 范围应产生 IN 条件且不产生 1 = 0: %v", log.queries)
	}
}

func TestInventorySummaryEmptyItemsNotNull(t *testing.T) {
	// 空结果 items 必须为 [] 而非 null（api.md §2.1，nil slice 教训）。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	useFixture(fixtureRoutes(
		rrouteCount("GROUP BY i.warehouse_id, w.code", "SELECT COUNT(*) FROM (", 0),
		rroute("GROUP BY i.warehouse_id, w.code", "LIMIT ? OFFSET ?",
			[]string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
				"sku_code", "sku_name", "total_qty", "available_qty", "locked_qty",
				"frozen_qty", "pending_inspect_qty", "defective_qty", "stock_value"},
		),
	))
	defer useFixture(nil)
	out := env.mustOK("/api/reports/inventory-summary")
	if !strings.Contains(string(out.Data), `"items":[]`) {
		t.Fatalf("空结果 items 应为 []，实际 %s", out.Data)
	}
}

func TestInventorySummaryInvalidParams(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	cases := []struct {
		name  string
		query string
		field string
	}{
		{"warehouse_id 非整数", "?warehouse_id=abc", "warehouse_id"},
		{"sku_id 小数", "?sku_id=1.5", "sku_id"},
		{"page=0", "?page=0", "page"},
		{"page 非整数", "?page=x", "page"},
		{"pageSize 超上限", "?pageSize=101", "pageSize"},
	}
	for _, c := range cases {
		out := env.mustErr("/api/reports/inventory-summary"+c.query, http.StatusBadRequest, "COMMON_INVALID_PARAM")
		var details map[string]any
		decodeJSON(t, out.Details, &details)
		if details["field"] != c.field {
			t.Fatalf("%s: details.field 应为 %s，实际 %s", c.name, c.field, details["field"])
		}
	}
}

// TestScopeCondSQL 数据范围快照 → SQL 条件（repository.go Scope.cond）：
// ALL 恒真 / SPECIFIED IN 清单 / 空集 fail-closed 1 = 0（permission.md §4）。
func TestScopeCondSQL(t *testing.T) {
	summaryCols := []string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "total_qty", "available_qty", "locked_qty", "frozen_qty",
		"pending_inspect_qty", "defective_qty", "stock_value"}
	cases := []struct {
		name     string
		sc       Scope
		wantHas  string
		wantNot  string  // 空=无否定断言（过滤缺省时 extra 恒真片段合法出现）
		wantArgs []int64 // SPECIFIED 范围 IN 参数
	}{
		{"ALL 恒真片段", Scope{AllWarehouses: true}, "1 = 1", "1 = 0", nil},
		{"SPECIFIED 双仓", Scope{WarehouseIDs: []int64{2, 5}}, "IN (?,?)", "1 = 0", []int64{2, 5}},
		{"空集 fail-closed", Scope{}, "WHERE (1 = 0)", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newTestEnv(t, newUser(true, auth.DataScopeAll))
			log := &queryLog{}
			useFixture(logAndRoute(log,
				rrouteCount("GROUP BY i.warehouse_id, w.code", "SELECT COUNT(*) FROM (", 0),
				rroute("GROUP BY i.warehouse_id, w.code", "LIMIT ? OFFSET ?", summaryCols),
			))
			defer useFixture(nil)
			if _, _, err := env.svc.InventorySummary(context.Background(), c.sc, 0, 0, 1, 20); err != nil {
				t.Fatalf("聚合不应报错: %v", err)
			}
			if !log.anyContains(c.wantHas) {
				t.Fatalf("范围条件应含 %s: %v", c.wantHas, log.queries)
			}
			if c.wantNot != "" && !log.noneContains(c.wantNot) {
				t.Fatalf("范围条件不得含 %s: %v", c.wantNot, log.queries)
			}
			if c.wantArgs != nil {
				args := log.argsOf("LIMIT ? OFFSET ?")
				if len(args) < len(c.wantArgs) {
					t.Fatalf("范围参数缺失: %v", args)
				}
				for i, want := range c.wantArgs {
					if args[i].Value != want {
						t.Fatalf("范围参数[%d] 应为 %d，实际 %v", i, want, args[i].Value)
					}
				}
			}
		})
	}
}

// ---- GET /api/reports/inbound-stats、/api/reports/outbound-stats ----

func TestFlowStatsEndpoints(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		wantType  string
		wantBasis string
	}{
		{"入库统计", "/api/reports/inbound-stats", "INBOUND", "cost_price"},
		{"出库统计", "/api/reports/outbound-stats", "OUTBOUND", "sale_price"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, newUser(true, auth.DataScopeAll))
			log := &queryLog{}
			useFixture(logAndRoute(log,
				rrouteCount("created_at::date", "SELECT COUNT(*) FROM (", 2),
				rroute("created_at::date", "LIMIT ? OFFSET ?",
					[]string{"stat_date", "order_count", "qty", "amount"},
					[]any{parseT(t, "2026-10-01 00:00:00"), int64(3), 120.5, 1446.0},
					[]any{parseT(t, "2026-10-02 00:00:00"), int64(5), 80.25, 963.0},
				),
			))
			defer useFixture(nil)

			out := env.mustOK(tc.path + "?time_from=2026-10-01&time_to=2026-10-03&page=2&pageSize=10")
			var page pageData
			env.decode(out, &page)
			if page.Total != 2 || page.Page != 2 || page.PageSize != 10 {
				t.Fatalf("分页信封应为 page=2/pageSize=10/total=2，实际 %+v", page)
			}
			var inner struct {
				Items []FlowStatRow `json:"items"`
				Total int64         `json:"total"`
				Val   struct {
					Basis string `json:"basis"`
				} `json:"valuation"`
			}
			decodeJSON(t, page.Items, &inner)
			if inner.Val.Basis != tc.wantBasis {
				t.Fatalf("估值口径应为 %s，实际 %s", tc.wantBasis, inner.Val.Basis)
			}
			if len(inner.Items) != 2 || inner.Items[0].OrderCount != 3 {
				t.Fatalf("聚合行应回放: %+v", inner.Items)
			}
			assertFloat(t, "qty", inner.Items[0].Qty, 120.5)
			assertFloat(t, "amount", inner.Items[0].Amount, 1446.0)
			assertTime(t, "stat_date", inner.Items[0].StatDate.Time, parseT(t, "2026-10-01 00:00:00"))

			// 参数顺序：change_type、from、to（纯日期含当天 → 上界=次日零点）、LIMIT、OFFSET。
			args := log.argsOf("LIMIT ? OFFSET ?")
			if len(args) != 5 {
				t.Fatalf("明细查询应 5 参数，实际 %v", args)
			}
			if args[0].Value != tc.wantType {
				t.Fatalf("change_type 应为 %s，实际 %v", tc.wantType, args[0].Value)
			}
			assertTime(t, "time_from", args[1].Value.(time.Time), parseT(t, "2026-10-01 00:00:00"))
			assertTime(t, "time_to（含当天 +1 天）", args[2].Value.(time.Time), parseT(t, "2026-10-04 00:00:00"))
			if args[3].Value != int64(10) || args[4].Value != int64(10) {
				t.Fatalf("LIMIT/OFFSET 应为 10/10，实际 %v/%v", args[3].Value, args[4].Value)
			}
		})
	}
}

func TestFlowStatsTimeToWithClock(t *testing.T) {
	// 带时刻的 time_to 维持精确边界不 +1 天（handler.go requireRange 语义）。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rrouteCount("created_at::date", "SELECT COUNT(*) FROM (", 0),
		rroute("created_at::date", "LIMIT ? OFFSET ?", []string{"stat_date", "order_count", "qty", "amount"}),
	))
	defer useFixture(nil)
	env.mustOK("/api/reports/inbound-stats?time_from=2026-10-01%2000:00:00&time_to=2026-10-03%2008:30:00")
	args := log.argsOf("LIMIT ? OFFSET ?")
	assertTime(t, "带时刻 time_to 不应前进一天", args[2].Value.(time.Time), parseT(t, "2026-10-03 08:30:00"))
}

// requireRangeInvalidCases 时间范围非法参数组（requireRange 共用校验路径）。
func requireRangeInvalidCases(t *testing.T, path string) {
	t.Helper()
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr(path+"?time_from=2026/10/01", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr(path+"?time_from=2026-10-05&time_to=2026-10-01", http.StatusBadRequest, "REPORT_RANGE_TOO_LARGE")
	env.mustErr(path+"?time_from=2020-01-01&time_to=2026-10-01", http.StatusBadRequest, "REPORT_RANGE_TOO_LARGE")
}

func TestFlowStatsInvalidRange(t *testing.T) {
	requireRangeInvalidCases(t, "/api/reports/inbound-stats")
	requireRangeInvalidCases(t, "/api/reports/outbound-stats")
}

// ---- GET /api/reports/inventory-turnover ----

func TestTurnoverEndpoint(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rrouteCount("WITH cur AS (", "SELECT COUNT(*) FROM (", 3),
		rroute("WITH cur AS (", "LIMIT ? OFFSET ?",
			[]string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id", "sku_code",
				"sku_name", "end_qty", "outbound_qty", "start_qty", "last_moved_at"},
			[]any{int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪", 150.0, 200.0, 50.0, nil},
			[]any{int64(1), "WH01", "一号仓", int64(8), "B002", "薯片", 0.0, 100.0, 0.0, nil},
			[]any{int64(1), "WH01", "一号仓", int64(9), "C003", "可乐", 10.05555, 0.0, 10.05555, nil},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/inventory-turnover?time_from=2026-09-01&time_to=2026-10-01")
	var page pageData
	env.decode(out, &page)
	var rows []TurnoverRow
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 3 || page.Total != 3 {
		t.Fatalf("应 3 行 total=3，实际 %d/%d", len(rows), page.Total)
	}
	// 行 1：平均库存 (50+150)/2=100，周转率 200/100=2；周期 = 纯日期 time_to 含当天
	// （+1 天）→ 09-01..10-02 共 31 天 → 天数 31/2=15.5。
	assertFloat(t, "avg_inventory", rows[0].AvgInventory, 100)
	assertFloat(t, "turnover_rate", rows[0].TurnoverRate, 2)
	assertFloat(t, "turnover_days", rows[0].TurnoverDays, 15.5)
	// 行 2：平均库存 0 → 0/0（不造假分母，calc.go turnoverRate）。
	assertFloat(t, "空仓周转率", rows[1].TurnoverRate, 0)
	assertFloat(t, "空仓周转天数", rows[1].TurnoverDays, 0)
	// 行 3：平均库存 roundQty 到 4 位小数；无出库 → 0。
	assertFloat(t, "avg_inventory 取整", rows[2].AvgInventory, 10.0556)
	assertFloat(t, "无出库周转率", rows[2].TurnoverRate, 0)
	if rows[0].LastMovedAt != nil {
		t.Fatalf("NULL last_moved_at 应保持 nil: %+v", rows[0].LastMovedAt)
	}
	requireRangeInvalidCases(t, "/api/reports/inventory-turnover")
}

// ---- GET /api/reports/stagnant-stock ----

func TestStagnantStockEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll),
		WithClock(func() time.Time { return now }),
		thresholdsOption(90, 60, 30),
	)
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rrouteCount("WITH lastm AS (", "SELECT COUNT(*) FROM (", 3),
		rroute("WITH lastm AS (", "LIMIT ? OFFSET ?",
			[]string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
				"sku_code", "sku_name", "total_qty", "last_moved_at"},
			[]any{int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪", 55.0, now.AddDate(0, 0, -100)},
			[]any{int64(1), "WH01", "一号仓", int64(8), "B002", "薯片", 22.0, now.AddDate(0, 0, -45)},
			// 最小档位（30 天）边界内：Service 防御性过滤丢弃（service.go stagnant 过滤）。
			[]any{int64(1), "WH01", "一号仓", int64(9), "C003", "可乐", 8.0, now.AddDate(0, 0, -10)},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/stagnant-stock")
	var page pageData
	env.decode(out, &page)
	var rows []StagnantRow
	decodeJSON(t, page.Items, &rows)
	if page.Total != 3 {
		t.Fatalf("total 应为计数 SQL 的 3，实际 %d", page.Total)
	}
	if len(rows) != 2 {
		t.Fatalf("档位边界内行应被防御性过滤，实际保留 %d 行: %+v", len(rows), rows)
	}
	if rows[0].IdleDays != 100 || rows[0].Tier != "90" {
		t.Fatalf("100 天未动应 idle=100/tier=90，实际 %d/%s", rows[0].IdleDays, rows[0].Tier)
	}
	if rows[1].IdleDays != 45 || rows[1].Tier != "30" {
		t.Fatalf("45 天未动应 idle=45/tier=30，实际 %d/%s", rows[1].IdleDays, rows[1].Tier)
	}
	assertFloat(t, "total_qty", rows[0].TotalQty, 55)
}

// ---- GET /api/reports/replenishment-suggestions ----

func TestReplenishmentEndpoint(t *testing.T) {
	// 注入 7/3 补货参数（router.go WithReplenishmentParams 注入点同形态）。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll),
		WithReplenishmentParams(func(context.Context) (int, int, error) { return 7, 3, nil }),
	)
	log := &queryLog{}
	repCols := []string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "daily_avg_sales", "safety_stock", "incoming_qty",
		"current_available", "target_stock", "suggested_qty"}
	useFixture(logAndRoute(log,
		rrouteCount("WITH stock AS (", "SELECT COUNT(*) FROM (", 2),
		// target/suggested 占位错误值：Service 以同公式复算覆盖（service.go ReplenishmentSuggestions）。
		rroute("WITH stock AS (", "LIMIT ? OFFSET ?", repCols,
			[]any{int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪", 10.0, 40.0, 10.0, 20.0, -1.0, -1.0},
			[]any{int64(1), "WH01", "一号仓", int64(8), "B002", "薯片", 0.0, 10.0, 0.0, 3.0, -1.0, -1.0},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/reports/replenishment-suggestions")
	var page pageData
	env.decode(out, &page)
	var rows []ReplenishmentSuggestion
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 2 {
		t.Fatalf("应 2 行，实际 %d", len(rows))
	}
	// 行 1：目标 = max(40, 10×10)=100；建议 = 100−20−10=70。
	if rows[0].TargetStock != 100 || rows[0].SuggestedQty != 70 {
		t.Fatalf("建议应为 目标100/建议70，实际 %+v", rows[0])
	}
	if rows[0].LeadTimeDays != 7 || rows[0].BufferDays != 3 {
		t.Fatalf("依据参数应 7/3: %+v", rows[0])
	}
	if !strings.Contains(rows[0].BasisText, "建议补货 70.0000") ||
		!strings.Contains(rows[0].BasisText, "采购周期 7 天 + 缓冲 3 天") {
		t.Fatalf("计算依据应携公式与输入值: %s", rows[0].BasisText)
	}
	// 行 2：日均 0 → 目标=安全库存 10；建议 = 10−3=7。
	if rows[1].TargetStock != 10 || rows[1].SuggestedQty != 7 {
		t.Fatalf("安全库存主导建议应 10/7，实际 %+v", rows[1])
	}
	// 参数顺序（repository.go replenishment）：销量窗口起点、lead+buffer=10×2、LIMIT 20、OFFSET 0。
	args := log.argsOf("LIMIT ? OFFSET ?")
	if len(args) != 5 {
		t.Fatalf("明细查询应 5 参数，实际 %v", args)
	}
	if _, ok := args[0].Value.(time.Time); !ok {
		t.Fatalf("首个参数应为近 30 天销量窗口起点时间，实际 %v", args[0].Value)
	}
	if args[1].Value != float64(10) || args[2].Value != float64(10) {
		t.Fatalf("lead+buffer 参数应 10×2，实际 %v/%v", args[1].Value, args[2].Value)
	}
	if args[3].Value != int64(20) || args[4].Value != int64(0) {
		t.Fatalf("LIMIT/OFFSET 应为 20/0，实际 %v/%v", args[3].Value, args[4].Value)
	}
}

func TestReplenishmentIncludeAll(t *testing.T) {
	// only_shortage=false：充足库存行（建议 0）一并返回。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll),
		WithReplenishmentParams(func(context.Context) (int, int, error) { return 7, 3, nil }),
	)
	repCols := []string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "daily_avg_sales", "safety_stock", "incoming_qty",
		"current_available", "target_stock", "suggested_qty"}
	useFixture(fixtureRoutes(
		rrouteCount("WITH stock AS (", "SELECT COUNT(*) FROM (", 2),
		rroute("WITH stock AS (", "LIMIT ? OFFSET ?", repCols,
			[]any{int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪", 10.0, 40.0, 10.0, 20.0, 100.0, 70.0},
			[]any{int64(1), "WH01", "一号仓", int64(8), "B002", "薯片", 10.0, 40.0, 10.0, 120.0, 100.0, 0.0},
		),
	))
	defer useFixture(nil)
	out := env.mustOK("/api/reports/replenishment-suggestions?only_shortage=false")
	var page pageData
	env.decode(out, &page)
	var rows []ReplenishmentSuggestion
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 2 || rows[1].SuggestedQty != 0 {
		t.Fatalf("only_shortage=false 应含充足行（建议 0）: %+v", rows)
	}
}

func TestReplenishmentInvalidParamsFailClosed(t *testing.T) {
	// 补货参数非法（lead<0）→ 非业务错误 → 500（fail-closed，不静默用缺省值）。
	env := newTestEnv(t, newUser(true, auth.DataScopeAll),
		WithReplenishmentParams(func(context.Context) (int, int, error) { return -1, 3, nil }),
	)
	repCols := []string{"warehouse_id", "warehouse_code", "warehouse_name", "sku_id",
		"sku_code", "sku_name", "daily_avg_sales", "safety_stock", "incoming_qty",
		"current_available", "target_stock", "suggested_qty"}
	useFixture(fixtureRoutes(
		rrouteCount("WITH stock AS (", "SELECT COUNT(*) FROM (", 1),
		rroute("WITH stock AS (", "LIMIT ? OFFSET ?", repCols,
			[]any{int64(1), "WH01", "一号仓", int64(7), "A001", "凤爪", 10.0, 40.0, 10.0, 20.0, 100.0, 70.0},
		),
	))
	defer useFixture(nil)
	env.mustErr("/api/reports/replenishment-suggestions", http.StatusInternalServerError, "COMMON_INTERNAL_ERROR")
}

// ---- GET /api/reports/flow-trend ----

func TestFlowTrendEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		typ       string
		wantBasis string
		wantType  string
	}{
		{"入库趋势", "inbound", "cost_price", "INBOUND"},
		{"出库趋势", "outbound", "sale_price", "OUTBOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, newUser(true, auth.DataScopeAll))
			log := &queryLog{}
			useFixture(logAndRoute(log,
				rroute("created_at::date", "ORDER BY 1",
					[]string{"stat_date", "order_count", "qty", "amount"},
					[]any{parseT(t, "2026-10-01 00:00:00"), int64(3), 120.5, 1446.0},
				),
			))
			defer useFixture(nil)
			out := env.mustOK("/api/reports/flow-trend?type=" + tc.typ +
				"&time_from=2026-10-01&time_to=2026-10-03")
			var dto flowTrendDTO
			env.decode(out, &dto)
			if dto.Valuation.Basis != tc.wantBasis {
				t.Fatalf("估值口径应为 %s，实际 %s", tc.wantBasis, dto.Valuation.Basis)
			}
			if len(dto.Items) != 1 || dto.Items[0].OrderCount != 3 {
				t.Fatalf("趋势行应回放: %+v", dto.Items)
			}
			args := log.argsOf("ORDER BY 1")
			if len(args) < 3 || args[0].Value != tc.wantType {
				t.Fatalf("change_type 应为 %s，实际 %v", tc.wantType, args)
			}
		})
	}
}

func TestFlowTrendInvalidType(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/reports/flow-trend", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/reports/flow-trend?type=both", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}
