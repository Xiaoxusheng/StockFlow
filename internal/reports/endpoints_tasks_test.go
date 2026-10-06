package reports

// 数据驱动端点测试·我的任务（GET /api/tasks，UNION ALL 三任务表明细化 + 统一信封分页）。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// myTasksFixture 装配计数/明细两查询（UNION 分支集由 taskBranchSpecs 决定，
// 无 task_type 过滤为全三分支——分支文本恒含 putaway/pick/check 三段）。
func myTasksFixture(log *queryLog, total int64, rows ...[]any) {
	useFixture(logAndRoute(log,
		rrouteCount("FROM putaway_tasks pt", "SELECT COUNT(*) FROM (", total),
		rroute("FROM putaway_tasks pt", "SELECT * FROM (",
			[]string{"id", "task_no", "task_type", "source_no", "warehouse_name", "total_qty",
				"completed_qty", "status", "raw_status", "assignee_name", "created_at", "completed_at"},
			rows...),
	))
}

func pickingOnlyFixture(log *queryLog, total int64, rows ...[]any) {
	useFixture(logAndRoute(log,
		rrouteCount("FROM pick_tasks pk", "SELECT COUNT(*) FROM (", total),
		rroute("FROM pick_tasks pk", "SELECT * FROM (",
			[]string{"id", "task_no", "task_type", "source_no", "warehouse_name", "total_qty",
				"completed_qty", "status", "raw_status", "assignee_name", "created_at", "completed_at"},
			rows...),
	))
}

func TestMyTasksEndpoint(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	myTasksFixture(log, 2,
		[]any{int64(11), "PA-001", "putaway", "IB-001", "一号仓", 50.0, 0.0,
			"in_progress", "IN_PROGRESS", "张三", now.AddDate(0, 0, -1), nil},
		[]any{int64(12), "PK-001", "picking", "OB-001", "一号仓", 10.0, 10.0,
			"completed", "PICKED", "李四", now.AddDate(0, 0, -2), now.AddDate(0, 0, -1)},
	)
	defer useFixture(nil)

	out := env.mustOK("/api/tasks?page=1&pageSize=10")
	var page pageData
	env.decode(out, &page)
	if page.Page != 1 || page.PageSize != 10 || page.Total != 2 {
		t.Fatalf("分页信封应为 1/10/total=2，实际 %+v", page)
	}
	var rows []myTaskItemDTO
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 2 {
		t.Fatalf("应 2 行，实际 %d", len(rows))
	}
	if rows[0].ID != 11 || rows[0].TaskNo != "PA-001" || rows[0].TaskType != "putaway" ||
		rows[0].SourceNo != "IB-001" || rows[0].WarehouseName != "一号仓" {
		t.Fatalf("任务行字段应回放: %+v", rows[0])
	}
	assertFloat(t, "total_qty", rows[0].TotalQty, 50)
	assertFloat(t, "completed_qty", rows[0].CompletedQty, 0)
	if rows[0].Status != "in_progress" || rows[0].RawStatus != "IN_PROGRESS" || rows[0].AssigneeName != "张三" {
		t.Fatalf("统一状态/原态/负责人应回放: %+v", rows[0])
	}
	// 完成态行：completed_qty=total、CompletedAt 非空。
	if rows[1].Status != "completed" || rows[1].CompletedAt == nil {
		t.Fatalf("完成态行应携完成时间: %+v", rows[1])
	}
	assertTime(t, "completed_at", rows[1].CompletedAt.Time, now.AddDate(0, 0, -1))
}

func TestMyTasksAssigneeAndFilters(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	log := &queryLog{}
	pickingOnlyFixture(log, 1,
		[]any{int64(12), "PK-001", "picking", "OB-001", "一号仓", 10.0, 4.0,
			"in_progress", "PICKING", "李四", now.AddDate(0, 0, -2), nil},
	)
	defer useFixture(nil)

	out := env.mustOK("/api/tasks?task_type=picking&status=in_progress&warehouse_code=WH01")
	var page pageData
	env.decode(out, &page)
	var rows []myTaskItemDTO
	decodeJSON(t, page.Items, &rows)
	if len(rows) != 1 || rows[0].TaskType != "picking" || rows[0].CompletedQty != 4 {
		t.Fatalf("picking 行应回放: %+v", rows)
	}
	// task_type=picking：恰取单分支（bug 回归：不得误带他类分支）。
	if !log.anyContains("FROM pick_tasks pk") {
		t.Fatalf("picking 应命中 pick_tasks 分支: %v", log.queries)
	}
	if !log.noneContains("FROM putaway_tasks pt") || !log.noneContains("FROM check_tasks ck") {
		t.Fatalf("picking 分支不得携带他类任务 SQL: %v", log.queries)
	}
	// assignee 恒=当前用户（UserID=42）+ 仓库编码/统一状态过滤（参数按分支拼接顺序：
	// userID、仓库范围、warehouse_code、status）。
	countArgs := log.argsOf("SELECT COUNT(*) FROM (")
	if len(countArgs) < 3 || countArgs[0].Value != int64(42) || countArgs[1].Value != "WH01" ||
		countArgs[2].Value != "in_progress" {
		t.Fatalf("计数参数应为 用户/仓库/状态: %v", countArgs)
	}
	if !log.anyContains("t.status = ?") || !log.anyContains("w.code = ?") {
		t.Fatalf("状态与仓库过滤应进入 SQL: %v", log.queries)
	}
}

func TestMyTasksInvalidParams(t *testing.T) {
	env := newTestEnv(t, newUser(true, auth.DataScopeAll))
	// packing/moving/counting 无独立任务表承载 → 400（rulings 裁决）。
	env.mustErr("/api/tasks?task_type=packing", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/tasks?task_type=bogus", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/tasks?status=bogus", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/tasks?page=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")
	env.mustErr("/api/tasks?pageSize=101", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// TestMyTasksUnauthorized CurrentUser 缺失（认证上下文未建立）→ 401 fail-closed。
func TestMyTasksUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	h := &handler{svc: NewService(db)}
	r := gin.New() // 无用户注入中间件：CurrentUser 取不到
	r.GET("/api/tasks", h.myTasks)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无认证上下文应 401，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"COMMON_UNAUTHORIZED"`) {
		t.Fatalf("应返回统一信封错误码 COMMON_UNAUTHORIZED: %s", rec.Body.String())
	}
}
