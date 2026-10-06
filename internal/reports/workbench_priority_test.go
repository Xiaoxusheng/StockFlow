package reports

// 工作台优先处理端点测试（GET /api/workbench/priorities——工作台『我现在该做什么』改版，
// web/src/views/workbench/WorkbenchPage.tsx 消费契约）。测试形态与 endpoints_next_test.go
// 同约定：假驱动按 SQL 文本子串路由回放（fakedb_test.go），口径断言经 queryLog 抓取
// SQL/参数（四组 WHERE/排序/截止线/临期窗口/仓库范围均为文本或参数断言）。

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// priorityEnv /api/workbench/priorities 独立测试环境（handler 直挂；权限中间件形态由
// routes_test 冻结，此处经 ctx 键直注用户快照——rptUserCtxKey 同源）。
func priorityEnv(t *testing.T, uc auth.UserContext, opts ...Option) (*testEnv, *gin.Engine) {
	t.Helper()
	env := newTestEnv(t, uc, opts...)
	g := env.r.Group("/api")
	h := &handler{svc: env.svc}
	g.GET("/workbench/priorities", h.workbenchPriorities)
	return env, env.r
}

// TestWorkbenchPrioritiesEndpoint 四组计数+明细回放与 DTO 映射（limit=2、超管 ALL 范围、
// 收货阈值缺省路径、临期窗口注入 30）。
func TestWorkbenchPrioritiesEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env, _ := priorityEnv(t, newUser(true, auth.DataScopeAll), thresholdsOption(),
		WithClock(func() time.Time { return now }))
	log := &queryLog{}
	created := now.Add(-6 * time.Hour)
	useFixture(logAndRoute(log,
		// system_configs 读空 → receive_hours 缺省 4h。
		rroute("FROM system_configs", "", []string{"value"}),
		// 超时收货：计数 + 明细。
		rrouteCount("FROM inbound_orders io", "SELECT COUNT(*)", 3),
		rroute("FROM inbound_orders io", "",
			[]string{"id", "inbound_no", "warehouse_name", "source_no", "created_at"},
			[]any{int64(21), "IN-2026-9", "一号仓", "PO-1", created},
			[]any{int64(22), "IN-2026-10", "二号仓", "", now.Add(-7 * time.Hour)},
		),
		// 库位异常：计数 + 明细。
		rrouteCount("FROM exceptions e", "SELECT COUNT(*)", 2),
		rroute("FROM exceptions e", "",
			[]string{"id", "exception_no", "type", "status", "source_no", "created_at"},
			[]any{int64(31), "EXC-2026-1", "上架异常", "OPEN", "IN-2026-9", created},
		),
		// 临期库存：计数 + 明细。
		rrouteCount("FROM inventory i", "SELECT COUNT(*)", 4),
		rroute("FROM inventory i", "",
			[]string{"warehouse_id", "sku_code", "sku_name", "warehouse_name", "batch_no", "days_left", "qty"},
			[]any{int64(1), "SKU001", "矿泉水", "一号仓", "BN2026-1", int64(3), 120.0},
		),
		// 待复核订单：计数 + 明细。
		rrouteCount("FROM check_tasks ck", "COUNT(DISTINCT ck.outbound_no)", 5),
		rroute("FROM check_tasks ck", "",
			[]string{"id", "outbound_no", "warehouse_name", "task_count", "first_created_at"},
			[]any{int64(41), "OUT-2026-1", "一号仓", int64(2), created},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/workbench/priorities?limit=2")
	var dto workbenchPrioritiesDTO
	env.decode(out, &dto)

	// 计数回放：count=全量、items=TopN 截断（3 条只回 2 行）。
	if dto.OverdueReceipts.Count != 3 || len(dto.OverdueReceipts.Items) != 2 {
		t.Fatalf("超时收货应 count=3 items=2: %+v", dto.OverdueReceipts)
	}
	first := dto.OverdueReceipts.Items[0]
	if first.Title != "IN-2026-9" || first.Subtitle != "一号仓" || first.Detail != "来源 PO-1" {
		t.Fatalf("超时收货行映射不符: %+v", first)
	}
	assertTime(t, "overdue_receipts.time", first.Time.Time, created)

	if dto.BinExceptions.Count != 2 || len(dto.BinExceptions.Items) != 1 {
		t.Fatalf("库位异常应 count=2 items=1: %+v", dto.BinExceptions)
	}
	be := dto.BinExceptions.Items[0]
	if be.Title != "EXC-2026-1" || be.Subtitle != "上架异常" || be.Status != "OPEN" {
		t.Fatalf("库位异常行映射不符: %+v", be)
	}

	if dto.NearExpiryStock.Count != 4 || len(dto.NearExpiryStock.Items) != 1 {
		t.Fatalf("临期库存应 count=4 items=1: %+v", dto.NearExpiryStock)
	}
	ne := dto.NearExpiryStock.Items[0]
	if ne.Title != "SKU001" || ne.Detail != "批次 BN2026-1 · 剩余 3 天" {
		t.Fatalf("临期库存行映射不符: %+v", ne)
	}
	if ne.Qty == nil || *ne.Qty != 120 {
		t.Fatalf("临期现存量应为 120: %+v", ne)
	}
	if ne.Time != nil {
		t.Fatalf("临期行无时间锚点，Time 应缺省: %+v", ne)
	}

	if dto.PendingChecks.Count != 5 || len(dto.PendingChecks.Items) != 1 {
		t.Fatalf("待复核订单应 count=5 items=1: %+v", dto.PendingChecks)
	}
	pc := dto.PendingChecks.Items[0]
	if pc.Title != "OUT-2026-1" || pc.Detail != "2 条待复核任务" {
		t.Fatalf("待复核订单行映射不符: %+v", pc)
	}

	// 口径断言（SQL 文本 + 参数；branchQuery 按明细特有子串定位——计数/明细两组同表）。
	// 超时收货：RECEIVING + 软删过滤 + created_at ASC + 截止线=now−4h（缺省路径）。
	receiptSQL, receiptArgs := branchQuery(log, "JOIN warehouses w ON w.id = io.warehouse_id")
	if receiptSQL == "" {
		t.Fatalf("未捕获超时收货明细查询: %v", log.queries)
	}
	for _, frag := range []string{"io.status = 'RECEIVING'", "io.deleted_at IS NULL", "ORDER BY io.created_at, io.id"} {
		if !strings.Contains(receiptSQL, frag) {
			t.Fatalf("超时收货 SQL 缺片段 %q: %s", frag, receiptSQL)
		}
	}
	cutoff := now.Add(-4 * time.Hour)
	foundCutoff := false
	for _, a := range receiptArgs {
		if tv, ok := a.Value.(time.Time); ok && tv.Sub(cutoff).Abs() < time.Minute {
			foundCutoff = true
		}
	}
	if !foundCutoff {
		t.Fatalf("应含收货超时截止线 now−4h（缺省阈值）: %v", receiptArgs)
	}
	// 库位异常：bin_id>0 + 未闭环；无仓库范围片段（exceptions 无仓库列）。
	excSQL, _ := branchQuery(log, "FROM exceptions e")
	for _, frag := range []string{"e.bin_id > 0", "e.status NOT IN ('RESOLVED', 'CLOSED')"} {
		if !strings.Contains(excSQL, frag) {
			t.Fatalf("库位异常 SQL 缺片段 %q: %s", frag, excSQL)
		}
	}
	if strings.Contains(excSQL, "warehouse_id IN") {
		t.Fatalf("库位异常不应受仓库范围过滤: %s", excSQL)
	}
	// 临期库存：窗口参数=阈值最大档 30 天（thresholdsOption 注入降序首位）；仅临期不含已过期。
	nearSQL, nearArgs := branchQuery(log, "JOIN skus s ON s.id = t.sku_id")
	if nearSQL == "" {
		t.Fatalf("未捕获临期库存明细查询: %v", log.queries)
	}
	for _, frag := range []string{"CURRENT_DATE + ?::interval", "b.expiry_date > CURRENT_DATE"} {
		if !strings.Contains(nearSQL, frag) {
			t.Fatalf("临期库存 SQL 缺片段 %q: %s", frag, nearSQL)
		}
	}
	foundWindow := false
	for _, a := range nearArgs {
		if a.Value == "30 days" {
			foundWindow = true
		}
	}
	if !foundWindow {
		t.Fatalf("应含临期窗口参数 30 days: %v", nearArgs)
	}
	// 待复核订单：按出库单去重计数 + PENDING。
	checkSQL, _ := branchQuery(log, "COUNT(DISTINCT ck.outbound_no)")
	for _, frag := range []string{"COUNT(DISTINCT ck.outbound_no)", "ck.status = 'PENDING'"} {
		if !strings.Contains(checkSQL, frag) {
			t.Fatalf("待复核订单 SQL 缺片段 %q: %s", frag, checkSQL)
		}
	}
	// limit 透传：明细查询应携带 2。
	if !log.anyContains("LIMIT ?") {
		t.Fatalf("明细查询应带 LIMIT: %v", log.queries)
	}
}

// TestWorkbenchPrioritiesScope 仓库范围收窄：SPECIFIED_WAREHOUSE 范围用户（仓 3/4），
// scope 片段应进入 inbound/check/inventory 三组 SQL；exceptions 组无仓库过滤（同上）。
func TestWorkbenchPrioritiesScope(t *testing.T) {
	env, _ := priorityEnv(t, newUser(false, auth.DataScopeSpecifiedWh, 3, 4), thresholdsOption())
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM system_configs", "", []string{"value"}),
		rrouteCount("FROM inbound_orders io", "SELECT COUNT(*)", 0),
		rroute("FROM inbound_orders io", "",
			[]string{"id", "inbound_no", "warehouse_name", "source_no", "created_at"}),
		rrouteCount("FROM exceptions e", "SELECT COUNT(*)", 0),
		rroute("FROM exceptions e", "",
			[]string{"id", "exception_no", "type", "status", "source_no", "created_at"}),
		rrouteCount("FROM inventory i", "SELECT COUNT(*)", 0),
		rroute("FROM inventory i", "",
			[]string{"warehouse_id", "sku_code", "sku_name", "warehouse_name", "batch_no", "days_left", "qty"}),
		rrouteCount("FROM check_tasks ck", "COUNT(DISTINCT ck.outbound_no)", 0),
		rroute("FROM check_tasks ck", "",
			[]string{"id", "outbound_no", "warehouse_name", "task_count", "first_created_at"}),
	))
	defer useFixture(nil)

	env.mustOK("/api/workbench/priorities")
	for _, sub := range []string{"FROM inbound_orders io", "FROM inventory i", "FROM check_tasks ck"} {
		sql := normSQL(log.queries[indexOfQuery(log, sub)])
		if !strings.Contains(sql, "IN (?,?)") {
			t.Fatalf("%s 应携仓库范围 IN 片段: %s", sub, sql)
		}
	}
	// 范围参数 = 仓 3/4（IN ? 展开为两占位符，计数查询另携截止线）。
	args := log.argsOf("FROM inbound_orders io")
	if len(args) < 2 || args[0].Value != int64(3) || args[1].Value != int64(4) {
		t.Fatalf("仓库范围参数应以 [3 4] 开头: %v", args)
	}
	excSQL := normSQL(log.queries[indexOfQuery(log, "FROM exceptions e")])
	if strings.Contains(excSQL, "IN (?,?)") {
		t.Fatalf("库位异常不受仓库范围约束: %s", excSQL)
	}
}

// TestWorkbenchPrioritiesInvalidParams limit 白名单 1–20，越界/非法 400。
func TestWorkbenchPrioritiesInvalidParams(t *testing.T) {
	env, _ := priorityEnv(t, newUser(true, auth.DataScopeAll))
	env.mustErr("/api/workbench/priorities?limit=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/workbench/priorities?limit=21", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/workbench/priorities?limit=abc", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// indexOfQuery 返回 log 中首个含 sub 的查询下标（queryLog.argsOf 的下标版）。
func indexOfQuery(log *queryLog, sub string) int {
	log.mu.Lock()
	defer log.mu.Unlock()
	for i, s := range log.queries {
		if strings.Contains(s, sub) {
			return i
		}
	}
	return -1
}
