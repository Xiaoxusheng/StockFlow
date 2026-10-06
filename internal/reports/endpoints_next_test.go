package reports

// 效率层一期 B3 端点测试（2026-10-06，docs/plans/2026-10-06-efficiency-layer-phase1.md §7.1 T7）：
//
//	GET /api/tasks/next                  自动下一条——候选池（B7）/排序层（真实 SQL ORDER BY）/
//	                                     current_task_id 排除 / has_next=false / task_type=400
//	GET /api/workbench/recent-operations 最近操作尾 N 条
//	GET /api/workbench/summary           三增量计数（mine_count/timeout_count/today_completed_count）
//
// 测试形态与 fakedb_test.go 同约定：假驱动按 SQL 文本子串路由回放，口径断言经 queryLog
// 抓取 SQL/参数（B7 池含未领取 PENDING、ORDER BY 层序、超时截止线注入等均为文本/参数断言）。

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// nextEnv /api/tasks/next 与 recent-operations 的独立测试环境（handler 直挂，
// 权限中间件形态由 routes_test 冻结，此处经 ctx 键直注用户快照——rptUserCtxKey 同源）。
type nextEnv struct {
	t    *testing.T
	svc  *Service
	r    *gin.Engine
	user auth.UserContext
}

func newNextEnv(t *testing.T, uc auth.UserContext, opts ...Option) *nextEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	env := &nextEnv{t: t, svc: NewService(db, opts...), user: uc}
	h := &handler{svc: env.svc}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(rptUserCtxKey, env.user); c.Next() })
	g := r.Group("/api")
	g.GET("/tasks/next", h.nextTask)
	g.GET("/workbench/recent-operations", h.recentOperations)
	g.GET("/workbench/summary", h.workbenchSummary)
	env.r = r
	return env
}

func (e *nextEnv) get(path string) envEnvelope {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	var out envEnvelope
	decodeJSON(e.t, rec.Body.Bytes(), &out)
	out.httpStatus = rec.Code
	return out
}

func (e *nextEnv) mustOK(path string) envEnvelope {
	e.t.Helper()
	out := e.get(path)
	if !out.ok() || out.httpStatus != http.StatusOK {
		e.t.Fatalf("GET %s 应成功（200/code=0），实际 http=%d code=%s message=%s", path, out.httpStatus, out.Code, out.Message)
	}
	return out
}

func (e *nextEnv) mustErr(path string, wantCode string) envEnvelope {
	e.t.Helper()
	out := e.get(path)
	if out.ok() {
		e.t.Fatalf("GET %s 应失败（%s），实际成功: %s", path, wantCode, out.Data)
	}
	if string(out.Code) != "\""+wantCode+"\"" {
		e.t.Fatalf("GET %s 应为 %s，实际 http=%d code=%s", path, wantCode, out.httpStatus, out.Code)
	}
	return out
}

// normSQL 空白归一（口径文本断言，migrations_test normalizeSQL 同思路）。
func normSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

// branchQuery 取 log 中匹配子串的首条分支查询及参数（next 先读超时阈值配置两查，
// 分支查询按下标定位不可靠——按表名子串定位）。
func branchQuery(log *queryLog, substr string) (string, []driver.NamedValue) {
	for i, q := range log.queries {
		if strings.Contains(q, substr) {
			return normSQL(q), log.args[i]
		}
	}
	return "", nil
}

// nextTaskRow 自动下一条回放行（myTaskItemDTO 列序）。
func nextTaskRow(id int64, taskNo string) []any {
	created := time.Date(2026, 10, 6, 8, 0, 0, 0, time.Local)
	return []any{id, taskNo, "putaway", "IN-2026-1", "一号仓", 5.0, 0.0,
		"in_progress", "IN_PROGRESS", "张三", created, nil}
}

// systemConfigFixture 超时阈值注入（按 key 参数分流——pick/putaway 两读同文异参）。
func systemConfigFixture(pickHours, putawayHours string) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	return func(q string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		if strings.Contains(q, "FROM system_configs") && len(args) > 0 {
			switch args[0].Value {
			case "task.timeout.pick_hours":
				return fixtureRows([]string{"value"}, []any{pickHours})
			case "task.timeout.putaway_hours":
				return fixtureRows([]string{"value"}, []any{putawayHours})
			}
		}
		return nil, nil
	}
}

// TestNextTaskPutawayPoolAndOrdering T7·putaway 分支：B7 候选池（本人进行中 ∪ 未领取
// PENDING——池内含他人未领取任务的 SQL 证据）+ 四层排序（mine DESC > priority DESC >
// 超时 > created_at ASC）+ current_task_id 排除 + 超时阈值注入（pick=6/putaway=2）。
func TestNextTaskPutawayPoolAndOrdering(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newNextEnv(t, newUser(true, auth.DataScopeAll), WithClock(func() time.Time { return now }))
	log := &queryLog{}
	cfg := systemConfigFixture("6", "2")
	row := nextTaskRow(9, "PW-2026-1")
	useFixture(func(q string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		log.record(q, args)
		if c, r := cfg(q, args); c != nil {
			return c, r
		}
		if strings.Contains(q, "FROM putaway_tasks pt") && strings.Contains(q, "LIMIT 1") {
			return fixtureRows(
				[]string{"id", "task_no", "task_type", "source_no", "warehouse_name",
					"total_qty", "completed_qty", "status", "raw_status", "assignee_name",
					"created_at", "completed_at"},
				row,
			)
		}
		return nil, nil // fail-loud
	})
	defer useFixture(nil)

	out := env.mustOK("/api/tasks/next?task_type=putaway&current_task_id=7&warehouse_id=3")
	var res struct {
		HasNext bool           `json:"has_next"`
		Task    *myTaskItemDTO `json:"task"`
	}
	decodeJSON(t, out.Data, &res)
	if !res.HasNext || res.Task == nil || res.Task.TaskNo != "PW-2026-1" {
		t.Fatalf("应命中候选任务: %+v", res)
	}

	sql, args := branchQuery(log, "FROM putaway_tasks pt")
	if sql == "" {
		t.Fatalf("未捕获 putaway 分支查询: %v", log.queries)
	}
	// B7 候选池：本人进行中 OR 未领取 PENDING（他人未领取 PENDING 可达的 SQL 证据）。
	for _, frag := range []string{
		"(pt.claimed_by = ? AND pt.status IN ('IN_PROGRESS', 'PAUSED')) OR (pt.claimed_by = 0 AND pt.status = 'PENDING')",
	} {
		if !strings.Contains(sql, frag) {
			t.Fatalf("候选池 WHERE 缺片段 %q: %s", frag, sql)
		}
	}
	// 四层排序：mine DESC > priority DESC > 超时 > created_at ASC（真实 SQL ORDER BY）。
	orderAt := strings.Index(sql, "ORDER BY")
	for _, frag := range []string{
		"CASE WHEN pt.claimed_by = ? AND pt.status IN ('IN_PROGRESS', 'PAUSED') THEN 0 ELSE 1 END",
		"pt.priority DESC",
		"CASE WHEN pt.created_at <= ? THEN 0 ELSE 1 END",
		"pt.created_at, pt.id LIMIT 1",
	} {
		if !strings.Contains(sql[orderAt:], frag) {
			t.Fatalf("ORDER BY 缺排序层 %q: %s", frag, sql[orderAt:])
		}
	}
	// 参数：归因=当前用户(42)、current_task_id 排除(7)、warehouse 收窄(3)、超时截止线=now−2h。
	if len(args) == 0 || args[0].Value != int64(42) {
		t.Fatalf("首参应为归因用户 42: %v", args)
	}
	cutoff := now.Add(-2 * time.Hour)
	foundCutoff := false
	for _, a := range args {
		if tv, ok := a.Value.(time.Time); ok && tv.Sub(cutoff).Abs() < time.Minute {
			foundCutoff = true
		}
	}
	if !foundCutoff {
		t.Fatalf("应含上架超时截止线 now−2h（阈值注入 putaway=2）: %v", args)
	}
	foundEx, foundWh := false, false
	for _, a := range args {
		if a.Value == int64(7) {
			foundEx = true
		}
		if a.Value == int64(3) {
			foundWh = true
		}
	}
	if !foundEx || !foundWh {
		t.Fatalf("应含 current_task_id 排除(7)与 warehouse 收窄(3)参数: %v", args)
	}
}

// TestNextTaskCheckingNoTimeoutLayer T7·checking 分支：mine > priority > created_at，
// 无超时层（一期不新增 task.timeout.check_hours）；候选=PENDING 且 assignee ∈ (本人, 未指派)。
func TestNextTaskCheckingNoTimeoutLayer(t *testing.T) {
	env := newNextEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM system_configs", "", []string{"value"}, []any{"4"}),
		rroute("FROM check_tasks ck", "",
			[]string{"id", "task_no", "task_type", "source_no", "warehouse_name",
				"total_qty", "completed_qty", "status", "raw_status", "assignee_name",
				"created_at", "completed_at"},
			[]any{int64(11), "CH-1", "checking", "OUT-1", "一号仓", 3.0, 0.0,
				"pending", "PENDING", "", parseT(t, "2026-10-06 08:00:00"), nil},
		),
	))
	defer useFixture(nil)

	env.mustOK("/api/tasks/next?task_type=checking")
	sql, args := branchQuery(log, "FROM check_tasks ck")
	if sql == "" {
		t.Fatalf("未捕获 checking 分支查询: %v", log.queries)
	}
	if !strings.Contains(sql, "ck.status = 'PENDING' AND ck.assignee_id IN (?, 0)") {
		t.Fatalf("checking 候选池不符: %s", sql)
	}
	orderAt := strings.Index(sql, "ORDER BY")
	order := sql[orderAt:]
	for _, frag := range []string{
		"CASE WHEN ck.assignee_id = ? THEN 0 ELSE 1 END", "ck.priority DESC", "ck.created_at, ck.id LIMIT 1",
	} {
		if !strings.Contains(order, frag) {
			t.Fatalf("checking ORDER BY 缺层 %q: %s", frag, order)
		}
	}
	if strings.Contains(order, "created_at <= ?") {
		t.Fatalf("checking 不应有超时层（一期裁决）: %s", order)
	}
	// 参数无截止线（仅归因+scope）。
	for _, a := range args {
		if _, ok := a.Value.(time.Time); ok {
			t.Fatalf("checking 分支不应携带超时截止线参数: %v", args)
		}
	}
}

// TestNextTaskReceiptAndException T7·receipt/exception 分支：无 priority 列（B8），
// 仅 created_at ASC；exception 另加 assignee=me 处理中 > OPEN 前置层，且不受
// warehouse_id 过滤（exceptions 无仓库列——SQL 无 warehouses JOIN 证据）。
func TestNextTaskReceiptAndException(t *testing.T) {
	env := newNextEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM system_configs", "", []string{"value"}, []any{"4"}),
		rroute("FROM inbound_orders io", "",
			[]string{"id", "task_no", "task_type", "source_no", "warehouse_name",
				"total_qty", "completed_qty", "status", "raw_status", "assignee_name",
				"created_at", "completed_at"},
			[]any{int64(21), "IN-2026-9", "receipt", "PO-1", "一号仓", 0.0, 0.0,
				"in_progress", "RECEIVING", "", parseT(t, "2026-10-06 07:00:00"), nil},
		),
		rroute("FROM exceptions e", "",
			[]string{"id", "task_no", "task_type", "source_no", "warehouse_name",
				"total_qty", "completed_qty", "status", "raw_status", "assignee_name",
				"created_at", "completed_at"},
			[]any{int64(31), "EXC-1", "exception", "PK-1", "", 0.0, 0.0,
				"pending", "OPEN", "", parseT(t, "2026-10-06 06:00:00"), nil},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/tasks/next?task_type=receipt&warehouse_id=3")
	var res struct {
		HasNext bool           `json:"has_next"`
		Task    *myTaskItemDTO `json:"task"`
	}
	decodeJSON(t, out.Data, &res)
	if !res.HasNext || res.Task.TaskNo != "IN-2026-9" {
		t.Fatalf("receipt 应命中: %+v", res)
	}
	sql, _ := branchQuery(log, "FROM inbound_orders io")
	if sql == "" {
		t.Fatalf("未捕获 receipt 分支查询: %v", log.queries)
	}
	order := sql[strings.Index(sql, "ORDER BY"):]
	if order != "ORDER BY io.created_at, io.id LIMIT 1" {
		t.Fatalf("receipt 应仅 created_at 排序（B8 无 priority 层）: %s", order)
	}
	if !strings.Contains(sql, "io.status = 'RECEIVING' AND io.deleted_at IS NULL") {
		t.Fatalf("receipt 候选池应为 RECEIVING 且含软删过滤: %s", sql)
	}

	env.mustOK("/api/tasks/next?task_type=exception&warehouse_id=3")
	sql, _ = branchQuery(log, "FROM exceptions e")
	if sql == "" {
		t.Fatalf("未捕获 exception 分支查询: %v", log.queries)
	}
	if !strings.Contains(sql, "e.status = 'OPEN' OR (e.status IN ('ASSIGNED', 'PROCESSING') AND e.assignee_id = ?)") {
		t.Fatalf("exception 候选池不符: %s", sql)
	}
	order = sql[strings.Index(sql, "ORDER BY"):]
	for _, frag := range []string{
		"CASE WHEN e.assignee_id = ? AND e.status IN ('ASSIGNED', 'PROCESSING') THEN 0 ELSE 1 END",
		"e.created_at, e.id LIMIT 1",
	} {
		if !strings.Contains(order, frag) {
			t.Fatalf("exception ORDER BY 缺层 %q: %s", frag, order)
		}
	}
	if strings.Contains(sql, "priority") || strings.Contains(sql, "warehouses") {
		t.Fatalf("exception 无 priority 列/无仓库列，不应出现排序层或 JOIN: %s", sql)
	}
}

// TestNextTaskEmptyAndInvalid T7：无候选 has_next=false（task=null）；
// task_type 白名单外（packing）→ 400 invalidParam。
func TestNextTaskEmptyAndInvalid(t *testing.T) {
	env := newNextEnv(t, newUser(true, auth.DataScopeAll))
	useFixture(logAndRoute(&queryLog{},
		rroute("FROM system_configs", "", []string{"value"}, []any{"4"}),
		rroute("FROM putaway_tasks pt", "", []string{"id", "task_no", "task_type", "source_no",
			"warehouse_name", "total_qty", "completed_qty", "status", "raw_status",
			"assignee_name", "created_at", "completed_at"}), // 零行
	))
	defer useFixture(nil)

	out := env.mustOK("/api/tasks/next?task_type=putaway")
	var res struct {
		HasNext bool           `json:"has_next"`
		Task    *myTaskItemDTO `json:"task"`
	}
	decodeJSON(t, out.Data, &res)
	if res.HasNext || res.Task != nil {
		t.Fatalf("无候选应 has_next=false 且 task=null: %+v", res)
	}
	env.mustErr("/api/tasks/next?task_type=packing", "COMMON_INVALID_PARAM")
	env.mustErr("/api/tasks/next", "COMMON_INVALID_PARAM") // 缺 task_type 同 400
}

// TestRecentOperationsEndpoint 最近操作：本人尾 N 条（WHERE user_id=当前 ORDER BY id
// DESC LIMIT n），缺省 limit=10，响应 {items:[...]}。
func TestRecentOperationsEndpoint(t *testing.T) {
	env := newNextEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	useFixture(logAndRoute(log,
		rroute("FROM operation_logs", "",
			[]string{"created_at", "action", "module", "object_type", "object_id",
				"success", "error_code", "request_id"},
			[]any{parseT(t, "2026-10-06 11:00:00"), "claim", "purchase", "putaway_task",
				int64(9), true, "", "req-1"},
		),
	))
	defer useFixture(nil)

	out := env.mustOK("/api/workbench/recent-operations")
	var res struct {
		Items []recentOperationDTO `json:"items"`
	}
	decodeJSON(t, out.Data, &res)
	if len(res.Items) != 1 || res.Items[0].Action != "claim" ||
		res.Items[0].Module != "purchase" || !res.Items[0].Success {
		t.Fatalf("最近操作行不符: %+v", res.Items)
	}
	sql := normSQL(log.queries[0])
	if !strings.Contains(sql, "WHERE user_id = ?") || !strings.Contains(sql, "ORDER BY id DESC") {
		t.Fatalf("最近操作 SQL 口径不符: %s", sql)
	}
	if got := log.args[0]; len(got) != 2 || got[0].Value != int64(42) || got[1].Value != int64(10) {
		t.Fatalf("参数应为 (用户 42, 缺省 limit 10): %v", got)
	}
}

// TestWorkbenchSummaryIncrements summary 三增量计数（additive）：键名 mine_count/
// timeout_count/today_completed_count，归因=当前用户，超时截止线=配置阈值。
func TestWorkbenchSummaryIncrements(t *testing.T) {
	env := newNextEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	// 既有四块计数复用 dashboardTodayRepo 同源夹具（endpoints_dashboard_test.go），
	// 再叠加本批三计数与配置读取两条路由（fail-loud 驱动要求全量命中）。
	todayFixture(t, log, sampleTodayWants())
	prev := currentFixture()
	useFixture(func(q string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		log.record(q, args)
		if strings.Contains(q, "AS today_completed_count") {
			return fixtureRows([]string{"mine_count", "timeout_count", "today_completed_count"},
				[]any{int64(2), int64(1), int64(3)})
		}
		if c, r := systemConfigFixture("4", "4")(q, args); c != nil {
			return c, r
		}
		return prev(q, args)
	})
	defer useFixture(nil)

	out := env.mustOK("/api/workbench/summary")
	var dto workbenchSummaryDTO
	decodeJSON(t, out.Data, &dto)
	if dto.TodoCount != 3 || dto.TaskCount != 15 || dto.ExceptionCount != 4 {
		t.Fatalf("既有四计数应不受增量影响: %+v", dto)
	}
	if dto.MineCount != 2 || dto.TimeoutCount != 1 || dto.TodayCompletedCount != 3 {
		t.Fatalf("三增量计数不符: %+v", dto)
	}
	// 口径断言：三计数为本人归因（claimed_by/assignee_id=?），超时层带截止线参数。
	var countsSQL string
	for _, q := range log.queries {
		if strings.Contains(q, "AS today_completed_count") {
			countsSQL = normSQL(q)
			break
		}
	}
	for _, frag := range []string{
		"pt.claimed_by = ? AND pt.status IN ('IN_PROGRESS','PAUSED')",
		"pk.assignee_id = ? AND pk.status = 'PICKED' AND pk.picked_at >= ?",
		"ck.assignee_id = ? AND ck.status = 'DONE' AND ck.done_at >= ?",
		"pt.created_at <= ?",
	} {
		if !strings.Contains(countsSQL, frag) {
			t.Fatalf("三计数 SQL 缺口径片段 %q: %s", frag, countsSQL)
		}
	}
}
