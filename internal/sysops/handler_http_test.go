package sysops

// HTTP handler 层数据驱动表测试（ask：/api/logs /api/system /api/notifications 17 条
// 端点逐一覆盖参数绑定/校验失败/成功路径/可测错误与权限分支；不依赖 PostgreSQL/Redis，
// 数据访问走 fakegorm_test.go 的 SQL 子串路由夹具——与 internal/reports/fakedb_test.go
// 同一项目约定，非第二套机制）。
//
// 测试通路：gin.TestMode 引擎经真实 RegisterRoutes 挂载（路由集合与 RequirePermission
// 权限链路真实走通）。权限分支口径：
//   - 未认证（未注入用户上下文）→ RequirePerm 401 fail-closed（routes.go:94-120 挂载点）；
//     个人收件箱 handler 内 auth.CurrentUser 401（routes.go:528-531）；
//   - 非超管 + auth 包未装配（本测试二进制恒未装配）→ RequirePermission fail-closed 500
//     （internal/auth/middleware.go RequirePermission 的 snapshotWired !wired 分支）；
//   - 超管直通（auth.go RequirePermission IsSuper 分支，permission.md §1）→ 驱动数据用例。
//
// 用户上下文注入：auth.CurrentUser 只认 gin 键 "sf_auth_user"（internal/auth/middleware.go
// ctxUserKey，包内私有常量，plan §5.1 冻结"跨包只经 CurrentUser 取值"）；本文件以同字面量
// 镜像注入。若 auth 改键名，401/归因断言将响亮失败而非静默放过。
//
// 包级全局状态（schedRef 调度器引用 / runtimeCfgValue 运行时参数）在 newSysEnv 的
// t.Cleanup 中复位；任务执行体注册按 cron_test.go 先例以 allowReplace 写入，残留仅可被
// 调度器 execute 调用，不影响其他用例。

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// ctxUserKeyMirror 镜像 internal/auth/middleware.go ctxUserKey（"sf_auth_user"）。
const ctxUserKeyMirror = "sf_auth_user"

const testRequestID = "req-sysops-http"

// ---- 测试环境 ----

type sysEnv struct {
	t    *testing.T
	user *auth.UserContext
	r    *gin.Engine
}

// sysSuperUser 超管上下文（RequirePermission 直通，驱动数据用例；UserID 恒 42 供审计归因断言）。
func sysSuperUser() *auth.UserContext {
	return &auth.UserContext{UserID: 42, Username: "tester", IsSuper: true}
}

// newSysEnv 建假 gorm + 真实 RegisterRoutes 装配的 gin 引擎；
// t.Cleanup 复位包级全局状态（调度器引用/运行时参数）与 SQL 夹具。
func newSysEnv(t *testing.T, uc *auth.UserContext, opts ...ServiceOption) *sysEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openSysFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	e := &sysEnv{t: t, user: uc, r: gin.New()}
	e.r.Use(func(c *gin.Context) {
		c.Set(response.RequestIDKey, testRequestID)
		if e.user != nil {
			c.Set(ctxUserKeyMirror, *e.user)
		}
		c.Next()
	})
	RegisterRoutes(e.r.Group("/api"), db, nil, opts...)

	t.Cleanup(func() {
		useSQLFixture(nil)
		attachScheduler(nil)       // schedRef 复位（SetJobStatus/listJobs 调度器分支）
		Configure(runtimeConfig{}) // runtimeCfgValue 复位（备份下载/模板全局参数）
		sysCapture.reset()
	})
	return e
}

// doRaw 发送请求返回原始 recorder（下载/响应头断言用）。
func (e *sysEnv) doRaw(method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "unit-http")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

// do 发送请求并解析统一信封（api.md §2.2）。
func (e *sysEnv) do(method, path, body string) (int, map[string]any) {
	e.t.Helper()
	w := e.doRaw(method, path, body)
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		e.t.Fatalf("响应不是合法 JSON 信封: %v; http=%d body=%s", err, w.Code, w.Body.String())
	}
	return w.Code, env
}

// sysCase 单用例：wantCode 成功=0（信封数字）、失败=错误码字符串；fixture 为用例级夹具
// （nil=不更换）；setup 在请求前执行（切调度器引用等全局态）。
type sysCase struct {
	name       string
	method     string
	path       string
	body       string
	wantStatus int
	wantCode   any
	fixture    *sysFixture
	setup      func(t *testing.T)
	check      func(t *testing.T, env map[string]any)
}

// run 顺序执行用例（全局夹具，串行；不用 t.Parallel）。
func (e *sysEnv) run(cases []sysCase) {
	e.t.Helper()
	for _, tc := range cases {
		tc := tc
		e.t.Run(tc.name, func(t *testing.T) {
			if tc.fixture != nil {
				useSQLFixture(tc.fixture)
			}
			defer useSQLFixture(nil)
			if tc.setup != nil {
				tc.setup(t)
			}
			status, env := e.do(tc.method, tc.path, tc.body)
			require.Equal(t, tc.wantStatus, status, "HTTP 状态不符: %v", env)
			require.EqualValues(t, tc.wantCode, env["code"], "信封 code 不符: %v", env)
			require.Equal(t, testRequestID, env["request_id"], "信封 request_id 必须回显（api.md §2.2）")
			require.NotEmpty(t, env["message"], "信封 message 必须存在")
			if tc.check != nil {
				tc.check(t, env)
			}
		})
	}
}

// ---- 信封取值 helper（JSON unmarshal 后数字一律 float64）----

func dataOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	require.IsType(t, map[string]any{}, env["data"], "data 应为对象: %v", env["data"])
	return env["data"].(map[string]any)
}

func itemsOf(t *testing.T, env map[string]any) []any {
	t.Helper()
	data := dataOf(t, env)
	require.IsType(t, []any{}, data["items"], "items 应为数组（api.md §2.1 分页信封，空列表禁 null）")
	return data["items"].([]any)
}

func detailsOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	require.IsType(t, map[string]any{}, env["details"], "失败必须带 details（api.md §4）")
	return env["details"].(map[string]any)
}

func numOf(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	require.Contains(t, m, key, "缺少字段 %s: %v", key, m)
	require.IsType(t, float64(0), m[key], "%s 应为数字: %v", key, m[key])
	return m[key].(float64)
}

func strOf(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	require.Contains(t, m, key, "缺少字段 %s: %v", key, m)
	require.IsType(t, "", m[key], "%s 应为字符串: %v", key, m[key])
	return m[key].(string)
}

// ---- 夹具列布局（与各 DTO 的 gorm 默认列名映射一致）----

var opLogCols = []string{
	"id", "request_id", "user_id", "username", "ip", "user_agent", "module",
	"object_type", "object_id", "action", "method", "path", "success", "error_code",
	"request_snapshot", "before_snapshot", "after_snapshot", "created_at",
}

var loginLogCols = []string{
	"id", "user_id", "username", "ip", "user_agent", "success", "fail_reason", "created_at",
}

var configCols = []string{
	"key", "value", "name", "group", "type", "options_raw", "remark", "readonly", "updated_at",
}

var jobCols = []string{
	"id", "code", "name", "cron_expr", "enabled", "last_run_at",
	"last_run_status", "last_run_duration_ms", "next_run_at", "remark",
}

var runLogCols = []string{
	"id", "job_code", "trigger", "start_at", "end_at", "success", "duration_ms", "message",
}

var backupCols = []string{
	"id", "file_name", "file_path", "size_bytes", "trigger", "status", "message",
	"started_at", "finished_at", "created_at", "created_by",
}

var inboxCols = []string{"id", "type", "title", "content", "read", "created_at"}

var (
	tf0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)
	tf1 = time.Date(2026, 10, 5, 2, 30, 0, 0, time.Local)
	tf2 = time.Date(2026, 10, 5, 2, 35, 10, 0, time.Local)
	tf3 = time.Date(2026, 10, 6, 0, 0, 0, 0, time.Local)
)

// ---- 审计日志：GET /api/logs/operations、GET /api/logs/operations/:id ----

func TestLogsOperationsEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	listFixture := &sysFixture{
		queries: sysQRoutes(
			sysQRoute("FROM operation_logs WHERE id = ?", "", opLogCols,
				[]any{int64(7), "req-detail", int64(42), "tester", "10.0.0.8", "unit-http",
					"system", "system_config", int64(0), "update", "PUT", "/api/system/configs",
					true, "", []byte(`{"key":"k"}`), []byte(`{"value":"a"}`), []byte(`{"value":"b"}`), tf1}),
			sysQRoute("FROM operation_logs WHERE 1 = 1", "ORDER BY created_at DESC", opLogCols,
				[]any{int64(102), "req-1", int64(42), "tester", "10.0.0.8", "unit-http",
					"inventory", "stocktake", int64(9), "update", "PUT", "/api/inventory/adjust",
					true, "", []byte(`{"reason":"盘点"}`), []byte(`{"qty":5}`), []byte(`{"qty":8}`), tf1},
				[]any{int64(101), "req-2", int64(43), "op", "10.0.0.9", "pad",
					"inventory", "stocktake", int64(9), "update", "PUT", "/api/inventory/adjust",
					false, "INVENTORY_INSUFFICIENT", nil, nil, nil, tf0}),
			sysQRoute("FROM operation_logs", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
		),
	}

	e.run([]sysCase{
		{
			name: "成功_默认分页与三快照", method: http.MethodGet, path: "/api/logs/operations",
			wantStatus: http.StatusOK, wantCode: 0, fixture: listFixture,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 1, data["page"])
				require.EqualValues(t, 20, data["pageSize"])
				require.EqualValues(t, 2, data["total"])
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				row := items[0].(map[string]any)
				require.EqualValues(t, 102, row["id"])
				require.Equal(t, "inventory", row["module"])
				require.Equal(t, "update", row["action"])
				require.Equal(t, true, row["success"])
				// 三快照原样返回（jsonb 往返为 JSON 对象，非字符串包装）。
				require.IsType(t, map[string]any{}, row["request_snapshot"])
				require.Equal(t, "盘点", row["request_snapshot"].(map[string]any)["reason"])
				require.IsType(t, map[string]any{}, row["after_snapshot"])
				// 失败行错误码透传、空快照省略。
				fail := items[1].(map[string]any)
				require.Equal(t, false, fail["success"])
				require.Equal(t, "INVENTORY_INSUFFICIENT", fail["error_code"])
				require.NotContains(t, fail, "before_snapshot")
			},
		},
		{
			name:       "筛选_关键词模块动作_success时间范围",
			method:     http.MethodGet,
			path:       "/api/logs/operations?keyword=admin&module=inventory&action=update&success=false&time_from=2026-10-01&time_to=2026-10-05%2012:00:00",
			wantStatus: http.StatusOK, wantCode: 0, fixture: listFixture,
			check: func(t *testing.T, env map[string]any) {
				args := sysCapture.argsOf("SELECT COUNT(*) FROM operation_logs")
				require.NotNil(t, args, "计数 SQL 应已下发")
				require.Contains(t, argString(args), "%admin%", "keyword 应以 ILIKE 模糊参数下发")
				require.True(t, argContains(args, "inventory"), "module 筛选参数缺失")
				require.True(t, argContains(args, "update"), "action 筛选参数缺失")
				require.True(t, argContains(args, false), "success 筛选参数缺失")
				require.Equal(t, 7, len(args), "应含 keyword(2)+module+action+success+time_from+time_to")
				q := sysCapture.queryOf("SELECT COUNT(*) FROM operation_logs")
				require.Contains(t, q, "created_at >=", "time_from 应落 WHERE")
				require.Contains(t, q, "created_at <", "time_to 应落 WHERE（开区间）")
			},
		},
		{
			name: "success 非法值_400", method: http.MethodGet, path: "/api/logs/operations?success=yes",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "success", detailsOf(t, env)["field"])
			},
		},
		{
			name: "time_from 非法格式_400", method: http.MethodGet, path: "/api/logs/operations?time_from=2026/10/01",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "time_from", detailsOf(t, env)["field"])
			},
		},
		{
			name: "page 非法_400", method: http.MethodGet, path: "/api/logs/operations?page=0",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
			check: func(t *testing.T, env map[string]any) { require.Equal(t, "page", detailsOf(t, env)["field"]) },
		},
		{
			name: "pageSize 超上限_400", method: http.MethodGet, path: "/api/logs/operations?pageSize=101",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
			check: func(t *testing.T, env map[string]any) { require.Equal(t, "pageSize", detailsOf(t, env)["field"]) },
		},
		{
			name: "数据库故障_500", method: http.MethodGet, path: "/api/logs/operations",
			wantStatus: http.StatusInternalServerError, wantCode: "COMMON_INTERNAL_ERROR",
			fixture: &sysFixture{}, // 无查询路由 → fail-loud
		},
		{
			name: "详情成功_三快照完整", method: http.MethodGet, path: "/api/logs/operations/7",
			wantStatus: http.StatusOK, wantCode: 0, fixture: listFixture,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 7, data["id"])
				require.Equal(t, "system", data["module"])
				require.IsType(t, map[string]any{}, data["before_snapshot"])
				require.IsType(t, map[string]any{}, data["after_snapshot"])
			},
		},
		{
			name: "详情_id 非整数_400", method: http.MethodGet, path: "/api/logs/operations/abc",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
			check: func(t *testing.T, env map[string]any) { require.Equal(t, "id", detailsOf(t, env)["field"]) },
		},
		{
			name: "详情_id 非正数_400", method: http.MethodGet, path: "/api/logs/operations/0",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: listFixture,
		},
		{
			name: "详情_不存在_404", method: http.MethodGet, path: "/api/logs/operations/999",
			wantStatus: http.StatusNotFound, wantCode: "SYSTEM_LOG_NOT_FOUND",
			fixture: &sysFixture{
				queries: sysQRoutes(
					sysQRoute("FROM operation_logs WHERE id = ?", "", opLogCols), // 零行
					sysQRoute("FROM operation_logs", "SELECT COUNT(*)", []string{"count"}, []any{int64(0)}),
				),
			},
		},
	})
}

// ---- 登录日志：GET /api/logs/logins ----

func TestLogsLoginsEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	fixture := &sysFixture{
		queries: sysQRoutes(
			sysQRoute("FROM login_logs WHERE 1 = 1", "ORDER BY created_at DESC", loginLogCols,
				[]any{int64(31), int64(42), "tester", "10.0.0.8", "chrome", true, "", tf1},
				[]any{int64(30), int64(0), "ghost", "10.0.0.9", "curl", false, "COMMON_UNAUTHORIZED", tf0}),
			sysQRoute("FROM login_logs", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
		),
	}

	e.run([]sysCase{
		{
			name: "成功_失败原因透传", method: http.MethodGet, path: "/api/logs/logins",
			wantStatus: http.StatusOK, wantCode: 0, fixture: fixture,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				require.EqualValues(t, 2, dataOf(t, env)["total"])
				fail := items[1].(map[string]any)
				require.Equal(t, false, fail["success"])
				require.Equal(t, "COMMON_UNAUTHORIZED", fail["fail_reason"], "登录失败原因应透传")
				require.Equal(t, "tester", items[0].(map[string]any)["username"])
			},
		},
		{
			name: "筛选_success_false_参数下发", method: http.MethodGet,
			path:       "/api/logs/logins?success=false&keyword=ghost",
			wantStatus: http.StatusOK, wantCode: 0, fixture: fixture,
			check: func(t *testing.T, env map[string]any) {
				args := sysCapture.argsOf("SELECT COUNT(*) FROM login_logs")
				require.True(t, argContains(args, false), "success 筛选参数缺失")
				require.Contains(t, argString(args), "%ghost%", "keyword 应以模糊参数下发")
				require.Contains(t, sysCapture.queryOf("SELECT COUNT(*) FROM login_logs"), "username ILIKE",
					"login_logs 应复用 logWhere 的 keyword 维度")
			},
		},
		{
			name: "success 非法值_400", method: http.MethodGet, path: "/api/logs/logins?success=1x",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: fixture,
		},
	})

	// 已知 bug 复现位：listLoginLogs 以 `var rows []loginLogItem` 承接（logs.go:130），零行时
	// 保持 nil → items:null，违反 api.md §2.1（2026-10-05 收口轮已把 items:null 定为契约断裂，
	// notifications/reports 均已预置空 slice，logs/jobs/run-logs/backups 分页查询漏改）。
	t.Run("已知bug_空列表 items null", func(t *testing.T) {
		useSQLFixture(&sysFixture{
			queries: sysQRoutes(
				sysQRoute("FROM login_logs WHERE 1 = 1", "ORDER BY created_at DESC", loginLogCols),
				sysQRoute("FROM login_logs", "SELECT COUNT(*)", []string{"count"}, []any{int64(0)}),
			),
		})
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/logs/logins", "")
		require.Equal(t, http.StatusOK, status)
		require.EqualValues(t, 0, dataOf(t, env)["total"])
		require.Nil(t, dataOf(t, env)["items"], "现状：零行时 items 序列化为 null")
		t.Skip("已知bug：/api/logs 空列表 items:null 违反统一分页契约（var rows []T 未预置空 slice，logs.go:130）")
	})
}

// ---- 系统配置：GET /api/system/configs ----

func TestSystemConfigsListEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())

	e.run([]sysCase{
		{
			name: "列表成功_options 三态解析", method: http.MethodGet, path: "/api/system/configs",
			wantStatus: http.StatusOK, wantCode: 0,
			fixture: &sysFixture{
				queries: sysQRoutes(
					// 注：SQL 为 "FROM system_configs\n ORDER BY ..."（跨行），子串路由不可跨换行拼空格。
					sysQRoute("FROM system_configs", "ORDER BY", configCols,
						[]any{"inventory.alert.expiry_days", "30,15,7,3", "效期预警阈值（天）", "库存预警", "text",
							`["30","15"]`, "逗号分隔档位", false, tf1},
						[]any{"inventory.alert.low_stock_enabled", "true", "库存预警扫描开关", "库存预警", "boolean",
							"null", "false 时空转", false, tf0},
						[]any{"sysops.legacy_key", "1", "历史键", "其他", "text",
							"{broken", "解析失败的候选值", true, tf0},
					),
				),
			},
			check: func(t *testing.T, env map[string]any) {
				rows := env["data"].([]any)
				require.Len(t, rows, 3)
				first := rows[0].(map[string]any)
				require.Equal(t, "inventory.alert.expiry_days", first["key"])
				require.IsType(t, []any{}, first["options"], "合法 options jsonb 应解析为数组")
				require.Equal(t, []any{"30", "15"}, first["options"])
				second := rows[1].(map[string]any)
				require.NotContains(t, second, "options", "options=null 应省略（不造假候选值）")
				third := rows[2].(map[string]any)
				require.NotContains(t, third, "options", "options 解析失败应省略而非报错")
				require.Equal(t, true, third["readonly"], "readonly 位透传")
			},
		},
	})
}

// ---- 系统配置：PUT /api/system/configs ----

// configSaveFixture 构造 saveConfigs 夹具：FOR UPDATE 行按 key 参数分流（同文异参，
// reports fakedb 同约定）；审计 INSERT 回放 id；UPDATE 放行。
func configSaveFixture(keysForUpdate map[string][2]any) *sysFixture {
	return &sysFixture{
		queries: func(q string, args []driver.NamedValue) ([]string, [][]driver.Value) {
			switch {
			case strings.Contains(q, "FOR UPDATE"):
				key, _ := args[0].Value.(string)
				row, ok := keysForUpdate[key]
				if !ok {
					return []string{"value", "readonly"}, nil // 行不存在（零行）
				}
				return []string{"value", "readonly"}, [][]driver.Value{{row[0], row[1]}}
			case strings.Contains(q, "INSERT INTO `operation_logs`"):
				return []string{"id"}, [][]driver.Value{{int64(501)}}
			}
			return nil, nil
		},
		execs: sysERoutes(sysExecRoute{match: "UPDATE system_configs", affected: 1}),
	}
}

func TestSystemConfigsSaveEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())

	t.Run("批量保存_变更项更新未变项跳过_审计同事务", func(t *testing.T) {
		useSQLFixture(configSaveFixture(map[string][2]any{
			"inventory.alert.expiry_days": {"30,15,7,3", false},
			"task.timeout.pick_hours":     {"4", false},
		}))
		defer useSQLFixture(nil)

		status, env := e.do(http.MethodPut, "/api/system/configs",
			`{"items":[{"key":"inventory.alert.expiry_days","value":"30,15,7"},{"key":"task.timeout.pick_hours","value":"4"}]}`)
		require.Equal(t, http.StatusOK, status, "%v", env)
		require.EqualValues(t, 0, env["code"], "%v", env)
		require.EqualValues(t, 2, dataOf(t, env)["saved"])
		require.Equal(t, 1, sysCapture.execCountOf("UPDATE system_configs"),
			"值未变更的项应跳过 UPDATE（configs.go:168 未变更项跳过，不刷审计）")
		args := sysCapture.execArgsOf("UPDATE system_configs")
		require.Contains(t, argString(args), "30,15,7", "新值应下发")
		require.Contains(t, argString(args), "inventory.alert.expiry_days", "按 key 定位更新")
		auditSQL := sysCapture.queryOf("INSERT INTO `operation_logs`")
		require.NotEmpty(t, auditSQL, "配置变更必须逐项写审计（configs.go:176）")
		auditArgs := sysCapture.argsOf("INSERT INTO `operation_logs`")
		require.Contains(t, argString(auditArgs), "system_config", "审计 ObjectType 应为 system_config")
		require.Contains(t, argString(auditArgs), "30,15,7,3", "before 快照应含旧值")
		require.Equal(t, 1, sysCapture.txCountOf("begin"), "批量保存应单事务")
		require.Equal(t, 1, sysCapture.txCountOf("commit"))
		require.Equal(t, 0, sysCapture.txCountOf("rollback"), "成功路径不应回滚")
	})

	t.Run("空 items_400", func(t *testing.T) {
		useSQLFixture(configSaveFixture(nil))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{"items":[]}`)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "SYSTEM_CONFIG_EMPTY", env["code"])
	})

	t.Run("key 空白_400", func(t *testing.T) {
		useSQLFixture(configSaveFixture(nil))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{"items":[{"key":"   ","value":"x"}]}`)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "COMMON_INVALID_PARAM", env["code"])
		require.Equal(t, "items.key", detailsOf(t, env)["field"])
	})

	t.Run("请求体非法_400", func(t *testing.T) {
		useSQLFixture(configSaveFixture(nil))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{items}`)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "COMMON_INVALID_PARAM", env["code"])
		require.Equal(t, "请求体非法", detailsOf(t, env)["reason"])
	})

	t.Run("键不存在_404_事务回滚", func(t *testing.T) {
		useSQLFixture(configSaveFixture(nil))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{"items":[{"key":"nosuch.key","value":"1"}]}`)
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "SYSTEM_CONFIG_NOT_FOUND", env["code"], "fail-closed：禁止经 API 发明清单外配置键")
		require.Equal(t, 1, sysCapture.txCountOf("rollback"), "fail-closed 应整体回滚（批内原子）")
		require.Equal(t, 0, sysCapture.execCountOf("UPDATE system_configs"), "不存在的键禁止写入")
	})

	t.Run("只读项_400_事务回滚", func(t *testing.T) {
		useSQLFixture(configSaveFixture(map[string][2]any{
			"sysops.locked_key": {"1", true},
		}))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{"items":[{"key":"sysops.locked_key","value":"2"}]}`)
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, "SYSTEM_CONFIG_READONLY", env["code"])
		require.Equal(t, 1, sysCapture.txCountOf("rollback"))
		require.Equal(t, 0, sysCapture.execCountOf("UPDATE system_configs"), "只读项禁止写入")
	})

	t.Run("空值行_视同缺失_404", func(t *testing.T) {
		// configs.go:160：row.Value=="" 即 ErrConfigNotFound——值为空串的存量行同样不可保存。
		useSQLFixture(configSaveFixture(map[string][2]any{
			"blank.value.key": {"", false},
		}))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodPut, "/api/system/configs", `{"items":[{"key":"blank.value.key","value":"1"}]}`)
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "SYSTEM_CONFIG_NOT_FOUND", env["code"])
	})
}

// ---- 定时任务：GET /api/system/jobs ----

func TestSystemJobsListEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	rowsFixture := &sysFixture{
		queries: sysQRoutes(
			sysQRoute("FROM scheduled_jobs WHERE 1 = 1", "ORDER BY id", jobCols,
				[]any{int64(1), "inventory_low_stock_scan", "库存预警扫描", "*/10 * * * *", true,
					tf2, "success", int64(120), tf3, "available ≤ safety_stock"},
				[]any{int64(2), "file_cleanup", "过期文件清理", "30 3 * * *", false,
					nil, "never", int64(0), nil, "过期文件+备份核对"},
			),
			sysQRoute("FROM scheduled_jobs", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
		),
	}

	e.run([]sysCase{
		{
			name: "成功_无调度器时 scheduled 恒 false", method: http.MethodGet, path: "/api/system/jobs",
			wantStatus: http.StatusOK, wantCode: 0, fixture: rowsFixture,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				row := items[0].(map[string]any)
				require.Equal(t, "inventory_low_stock_scan", row["code"])
				require.Equal(t, true, row["enabled"])
				require.Equal(t, "success", row["last_run_status"])
				require.EqualValues(t, 120, row["last_run_duration_ms"])
				require.Equal(t, false, row["scheduled"],
					"调度器未装配（schedRef nil）时 scheduled 恒 false（jobsapi.go:80）")
				disabled := items[1].(map[string]any)
				require.Equal(t, "never", disabled["last_run_status"])
				require.Nil(t, disabled["last_run_at"], "NULL last_run_at 应序列化为 null")
				require.Nil(t, disabled["next_run_at"], "NULL next_run_at 应序列化为 null")
				require.Equal(t, false, disabled["scheduled"])
			},
		},
		{
			name: "筛选_keyword_参数下发", method: http.MethodGet,
			path:       "/api/system/jobs?keyword=scan",
			wantStatus: http.StatusOK, wantCode: 0, fixture: rowsFixture,
			check: func(t *testing.T, env map[string]any) {
				args := sysCapture.argsOf("SELECT COUNT(*) FROM scheduled_jobs")
				require.Contains(t, argString(args), "%scan%", "keyword 应双列模糊（code/name）")
				require.Contains(t, sysCapture.queryOf("SELECT COUNT(*) FROM scheduled_jobs"), "code ILIKE")
			},
		},
		{
			name: "page 非法_400", method: http.MethodGet, path: "/api/system/jobs?page=-1",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: rowsFixture,
		},
	})

	// enabled 筛选回归（原「已知 bug 复现位①」修复后改写为真实断言）：listJobs 按契约键名
	// enabled 三态取参——非法值 400，合法值进入 WHERE（前端 SystemJobQuery 发 enabled）。
	t.Run("enabled 筛选生效", func(t *testing.T) {
		useSQLFixture(rowsFixture)
		defer useSQLFixture(nil)
		status, _ := e.do(http.MethodGet, "/api/system/jobs?enabled=maybe", "")
		require.Equal(t, http.StatusBadRequest, status, "enabled 非法值应 400（不再静默 200）")
		status, _ = e.do(http.MethodGet, "/api/system/jobs?enabled=true", "")
		require.Equal(t, http.StatusOK, status)
		args := sysCapture.argsOf("SELECT COUNT(*) FROM scheduled_jobs")
		require.True(t, argContains(args, true), "enabled=true 应进入 WHERE 参数")
	})

	// 已知 bug 复现位②：SQL 别名 cron_expr 无法映射到 DTO 字段 Cron（无 gorm column tag，
	// jobsapi.go:19）——列表响应 cron 恒为空串，前端任务列表展示断裂。
	t.Run("已知bug_cron_expr 列映射断裂恒空", func(t *testing.T) {
		useSQLFixture(rowsFixture)
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/jobs", "")
		require.Equal(t, http.StatusOK, status)
		row := itemsOf(t, env)[0].(map[string]any)
		require.Equal(t, "", row["cron"], "现状：cron_expr 列无法映射，cron 恒空")
		t.Skip("已知bug：GET /api/system/jobs 响应 cron 恒为空（SELECT 别名 cron_expr 与 jobItem.Cron 默认列名 cron 不匹配，jobsapi.go:19）")
	})
}

func TestSystemJobsListScheduledFlag(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	// 装配调度器 + 注册 inventory_low_stock_scan 执行体（allowReplace 幂等，cron_test 同款）。
	attachScheduler(newTestScheduler(newFakeStore(), newFakeLocker()))
	registerJobHandler("inventory_low_stock_scan", func(context.Context) error { return nil }, true)
	t.Cleanup(func() { attachScheduler(nil) })

	useSQLFixture(&sysFixture{
		queries: sysQRoutes(
			sysQRoute("FROM scheduled_jobs WHERE 1 = 1", "ORDER BY id", jobCols,
				[]any{int64(1), "inventory_low_stock_scan", "库存预警扫描", "*/10 * * * *", true,
					nil, "never", int64(0), nil, ""},
				[]any{int64(2), "inventory_expiry_scan", "效期预警扫描", "0 6 * * *", true,
					nil, "never", int64(0), nil, ""},
			),
			sysQRoute("FROM scheduled_jobs", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
		),
	})
	defer useSQLFixture(nil)

	status, env := e.do(http.MethodGet, "/api/system/jobs", "")
	require.Equal(t, http.StatusOK, status, "%v", env)
	items := itemsOf(t, env)
	require.Len(t, items, 2)
	row1 := items[0].(map[string]any)
	require.Equal(t, true, row1["scheduled"], "调度器已装配且执行体已注册 → scheduled=true")
	row2 := items[1].(map[string]any)
	_, expiryRegistered := jobHandlerFor("inventory_expiry_scan")
	require.Equal(t, expiryRegistered, row2["scheduled"],
		"scheduled 应精确等于该任务执行体注册状态（空实现位不假装已调度）")
}

// ---- 定时任务：PUT /api/system/jobs/:id/status ----

func TestSystemJobStatusEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	// 按 id 取任务行夹具（getJobByID："FROM scheduled_jobs WHERE id = ?"）。
	jobRowFixture := func(id int64, code string, enabled bool) *sysFixture {
		return &sysFixture{
			queries: sysQRoutes(
				sysQRoute("FROM scheduled_jobs WHERE id = ?", "", jobCols,
					[]any{id, code, "任务", "0 6 * * *", enabled, nil, "never", int64(0), nil, ""}),
			),
			execs: sysERoutes(sysExecRoute{match: "UPDATE scheduled_jobs SET enabled", affected: 1}),
		}
	}
	var hotStore *fakeStore

	e.run([]sysCase{
		{
			name: "无调度器_仅落库持久化", method: http.MethodPut, path: "/api/system/jobs/2/status",
			body:       `{"enabled":false}`,
			setup:      func(t *testing.T) { attachScheduler(nil) },
			wantStatus: http.StatusOK, wantCode: 0, fixture: jobRowFixture(2, "file_cleanup", true),
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 2, data["id"])
				require.Equal(t, "file_cleanup", data["code"])
				require.Equal(t, false, data["enabled"])
				args := sysCapture.execArgsOf("UPDATE scheduled_jobs SET enabled")
				require.NotNil(t, args, "启停必须持久化到 scheduled_jobs 行（service.go:96）")
				require.Contains(t, argString(args), "file_cleanup", "应按 code 而非 id 更新行")
				require.True(t, argContains(args, false), "enabled=false 应下发")
			},
		},
		{
			name: "调度器在_执行体未注册_409", method: http.MethodPut, path: "/api/system/jobs/3/status",
			body: `{"enabled":true}`,
			setup: func(t *testing.T) {
				attachScheduler(newTestScheduler(newFakeStore(), newFakeLocker()))
			},
			wantStatus: http.StatusConflict, wantCode: "SYSTEM_JOB_NOT_SCHEDULED",
			fixture: jobRowFixture(3, "inventory_expiry_scan", false),
		},
		{
			name: "调度器在_执行体已注册_热更成功", method: http.MethodPut, path: "/api/system/jobs/4/status",
			body: `{"enabled":false}`,
			setup: func(t *testing.T) {
				hotStore = newFakeStore()
				hotStore.enabled["inventory_stagnant_scan"] = true // SetJobEnabled 需注册表行存在
				attachScheduler(newTestScheduler(hotStore, newFakeLocker()))
				registerJobHandler("inventory_stagnant_scan", func(context.Context) error { return nil }, true)
			},
			wantStatus: http.StatusOK, wantCode: 0, fixture: jobRowFixture(4, "inventory_stagnant_scan", true),
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, false, dataOf(t, env)["enabled"])
				require.Equal(t, false, hotStore.enabled["inventory_stagnant_scan"],
					"启停应经调度器 store 持久化（scheduler.go:165 持久化先行）")
			},
		},
		{
			name: "enabled 缺失_400", method: http.MethodPut, path: "/api/system/jobs/2/status",
			body:       `{}`,
			setup:      func(t *testing.T) { attachScheduler(nil) },
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM",
			fixture: jobRowFixture(2, "file_cleanup", true),
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "enabled 必须为布尔值", detailsOf(t, env)["reason"])
			},
		},
		{
			name: "请求体非法_400", method: http.MethodPut, path: "/api/system/jobs/2/status",
			body:       `{"enabled":"yes"}`,
			setup:      func(t *testing.T) { attachScheduler(nil) },
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM",
			fixture: jobRowFixture(2, "file_cleanup", true),
		},
		{
			name: "id 非整数_400", method: http.MethodPut, path: "/api/system/jobs/x/status",
			body:       `{"enabled":true}`,
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM",
			check: func(t *testing.T, env map[string]any) { require.Equal(t, "id", detailsOf(t, env)["field"]) },
		},
		{
			name: "任务不存在_404", method: http.MethodPut, path: "/api/system/jobs/999/status",
			body:       `{"enabled":true}`,
			setup:      func(t *testing.T) { attachScheduler(nil) },
			wantStatus: http.StatusNotFound, wantCode: "SYSTEM_JOB_NOT_FOUND",
			fixture: &sysFixture{
				queries: sysQRoutes(sysQRoute("FROM scheduled_jobs WHERE id = ?", "", jobCols)),
			},
		},
	})
}

// ---- 定时任务：GET /api/system/jobs/:id/run-logs ----

func TestSystemJobRunLogsEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	fixture := &sysFixture{
		queries: sysQRoutes(
			// run-logs 前置：按 id 解析任务编码（service.go:106 getJobByID）。
			sysQRoute("FROM scheduled_jobs WHERE id = ?", "", jobCols,
				[]any{int64(3), "inventory_expiry_scan", "效期预警扫描", "0 6 * * *", true, nil, "never", int64(0), nil, ""}),
			sysQRoute("FROM scheduled_job_runs WHERE job_code", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
			sysQRoute("FROM scheduled_job_runs WHERE job_code", "ORDER BY start_at", runLogCols,
				[]any{int64(9), "inventory_expiry_scan", "SCHEDULED", tf1, tf2, true, int64(310000), ""},
				[]any{int64(8), "inventory_expiry_scan", "SKIPPED", tf0, tf0, false, int64(0),
					"advisory lock 未获取（其他实例执行中）"}),
		),
	}

	e.run([]sysCase{
		{
			name: "成功_触发方式与跳过原因透传", method: http.MethodGet, path: "/api/system/jobs/3/run-logs",
			wantStatus: http.StatusOK, wantCode: 0, fixture: fixture,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				require.EqualValues(t, 2, dataOf(t, env)["total"])
				first := items[0].(map[string]any)
				require.Equal(t, "SCHEDULED", first["trigger"])
				require.Equal(t, true, first["success"])
				require.EqualValues(t, 310000, first["duration_ms"])
				require.Contains(t, first, "end_at", "已完成执行应带 end_at")
				require.NotContains(t, first, "message", "空 message 省略")
				skip := items[1].(map[string]any)
				require.Equal(t, "SKIPPED", skip["trigger"])
				require.Equal(t, false, skip["success"])
				require.Contains(t, skip["message"], "advisory lock", "跳过原因应透传")
			},
		},
		{
			name: "任务不存在_404", method: http.MethodGet, path: "/api/system/jobs/999/run-logs",
			wantStatus: http.StatusNotFound, wantCode: "SYSTEM_JOB_NOT_FOUND",
			fixture: &sysFixture{
				queries: sysQRoutes(sysQRoute("FROM scheduled_jobs WHERE id = ?", "", jobCols)),
			},
		},
		{
			name: "id 非整数_400", method: http.MethodGet, path: "/api/system/jobs/-1/run-logs",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: fixture,
		},
		{
			name: "pageSize 超上限_400", method: http.MethodGet, path: "/api/system/jobs/3/run-logs?pageSize=1000",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: fixture,
		},
	})
}

// ---- 系统监控：GET /api/system/monitor ----

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

type stubQueues struct {
	stats []QueueStat
	err   error
}

func (q stubQueues) QueueStats(context.Context, ...string) ([]QueueStat, error) {
	return q.stats, q.err
}

func TestMonitorEndpoint(t *testing.T) {
	jobsAggregate := sysQRoute("COUNT(*)::bigint", "", []string{"total", "enabled", "failed_last_run"},
		[]any{int64(5), int64(4), int64(1)})

	t.Run("全量指标", func(t *testing.T) {
		useSQLFixture(&sysFixture{queries: sysQRoutes(jobsAggregate), pingOK: true})
		defer useSQLFixture(nil)
		e := newSysEnv(t, sysSuperUser(),
			WithVersion("v-unit-1"),
			WithRedisPinger(stubPinger{}),
			WithQueueStats(stubQueues{stats: []QueueStat{{Queue: "default", Pending: 3, Active: 1, Scheduled: 2, Retry: 4}}}),
		)
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusOK, status, "%v", env)
		data := dataOf(t, env)
		db := data["database"].(map[string]any)
		require.Equal(t, true, db["healthy"], "假驱动 Ping 成功 → healthy")
		require.Contains(t, db, "max_open_connections")
		redisStat := data["redis"].(map[string]any)
		require.Equal(t, true, redisStat["enabled"])
		require.Equal(t, true, redisStat["healthy"])
		require.Equal(t, "ok", redisStat["status"])
		api := data["api"].(map[string]any)
		require.EqualValues(t, 0, api["request_count_1h"], "测试进程内采样器无请求计数")
		require.EqualValues(t, 0, api["error_rate_percent"], "零请求不产生 NaN 错误率")
		require.IsType(t, []any{}, data["api_trend"], "24h 趋势应为数组")
		jobs := data["jobs"].(map[string]any)
		require.EqualValues(t, 5, jobs["total"])
		require.EqualValues(t, 4, jobs["enabled"])
		require.EqualValues(t, 1, jobs["failed_last_run"])
		queued := data["queued_tasks"].([]any)
		require.Len(t, queued, 1)
		require.Equal(t, "default", queued[0].(map[string]any)["queue"])
		require.EqualValues(t, 3, queued[0].(map[string]any)["pending"])
		require.Equal(t, "v-unit-1", data["version"], "WithVersion 注入应覆盖 dev")
		require.GreaterOrEqual(t, numOf(t, data, "uptime_seconds"), float64(0))
		require.Contains(t, strOf(t, data, "go_version"), "go1.")
		remarks := data["remarks"].([]any)
		require.Len(t, remarks, 2, "指标口径注记（进程内采样局限/系统资源豁免）必须随响应")
	})

	t.Run("redis 不可达_仍200 unhealthy 透出", func(t *testing.T) {
		useSQLFixture(&sysFixture{queries: sysQRoutes(jobsAggregate)})
		defer useSQLFixture(nil)
		e := newSysEnv(t, sysSuperUser(), WithRedisPinger(stubPinger{err: errors.New("connection refused")}))
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusOK, status, "监控为指标读取面，依赖异常降级为 unhealthy 而非 5xx")
		redisStat := dataOf(t, env)["redis"].(map[string]any)
		require.Equal(t, false, redisStat["healthy"])
		require.Equal(t, "unreachable", redisStat["status"])
	})

	t.Run("队列读取失败_省略 queued_tasks", func(t *testing.T) {
		useSQLFixture(&sysFixture{queries: sysQRoutes(jobsAggregate)})
		defer useSQLFixture(nil)
		e := newSysEnv(t, sysSuperUser(), WithQueueStats(stubQueues{err: errors.New("redis down")}))
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusOK, status)
		require.NotContains(t, dataOf(t, env), "queued_tasks", "读取失败应省略而非造假积压数据")
	})

	t.Run("未注入 redis_队列_降级口径", func(t *testing.T) {
		useSQLFixture(&sysFixture{queries: sysQRoutes(jobsAggregate)})
		defer useSQLFixture(nil)
		e := newSysEnv(t, sysSuperUser())
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusOK, status)
		data := dataOf(t, env)
		redisStat := data["redis"].(map[string]any)
		require.Equal(t, false, redisStat["enabled"])
		require.Equal(t, true, redisStat["healthy"], "未启用 Redis 不视为故障")
		require.Equal(t, "disabled", redisStat["status"])
		require.NotContains(t, data, "queued_tasks")
	})

	t.Run("数据库不健康_healthy false 透出", func(t *testing.T) {
		useSQLFixture(&sysFixture{queries: sysQRoutes(jobsAggregate)}) // pingOK=false
		defer useSQLFixture(nil)
		e := newSysEnv(t, sysSuperUser())
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, false, dataOf(t, env)["database"].(map[string]any)["healthy"])
	})
}

// ---- 备份：GET /api/system/backups/pg-dump-template ----

func TestBackupPgDumpTemplateEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())

	t.Run("注入连接参数", func(t *testing.T) {
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{DBHost: "db.local", DBPort: 5433, DBUser: "svc", DBName: "stockflow"})
		status, env := e.do(http.MethodGet, "/api/system/backups/pg-dump-template", "")
		require.Equal(t, http.StatusOK, status, "%v", env)
		data := dataOf(t, env)
		cmd := strOf(t, data, "command")
		require.Contains(t, cmd, "--host=db.local --port=5433 --username=svc")
		require.Contains(t, cmd, "stockflow", "库名应入命令尾参")
		require.Contains(t, cmd, `PGPASSWORD="$SF_DATABASE_PASSWORD"`, "密码经环境变量注入")
		require.NotContains(t, cmd, "--password", "密码绝不进入命令行参数（backups.go:172）")
		require.Equal(t, "30 2 * * * /opt/stockflow/bin/backup-executor.sh >> /var/log/stockflow/backup.log 2>&1",
			strOf(t, data, "crontab_line"))
		require.Len(t, data["notes"].([]any), 5)
	})

	t.Run("未注入_占位符回退", func(t *testing.T) {
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{})
		status, env := e.do(http.MethodGet, "/api/system/backups/pg-dump-template", "")
		require.Equal(t, http.StatusOK, status)
		cmd := strOf(t, dataOf(t, env), "command")
		require.Contains(t, cmd, "--host=<db_host>")
		require.Contains(t, cmd, "--port=5432", "port 零值回退默认 5432")
		require.Contains(t, cmd, "--username=<db_user>")
		require.Contains(t, cmd, "<db_name>")
	})
}

// ---- 备份：GET /api/system/backups ----

func TestBackupListEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	rowsFixture := &sysFixture{
		queries: sysQRoutes(
			sysQRoute("FROM backup_records WHERE", "ORDER BY created_at DESC", backupCols,
				[]any{int64(3), "stockflow_20261005_023000.dump", "2026/10/05/stockflow_20261005_023000.dump",
					int64(10485760), "AUTO", "SUCCESS", "", tf1, tf2, tf2, int64(0)},
				[]any{int64(2), "", "", int64(0), "MANUAL", "FAILED", "执行器超时未拾取", nil, tf1, tf0, int64(42)}),
			sysQRoute("FROM backup_records", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
		),
	}

	e.run([]sysCase{
		{
			name: "成功_状态与时间列透传", method: http.MethodGet, path: "/api/system/backups",
			wantStatus: http.StatusOK, wantCode: 0, fixture: rowsFixture,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				ok := items[0].(map[string]any)
				require.Equal(t, "SUCCESS", ok["status"])
				require.Equal(t, "AUTO", ok["trigger"])
				require.EqualValues(t, 10485760, ok["size_bytes"])
				require.NotContains(t, ok, "message", "空 message 省略")
				fail := items[1].(map[string]any)
				require.Equal(t, "FAILED", fail["status"])
				require.Equal(t, "执行器超时未拾取", fail["message"])
				require.Nil(t, fail["started_at"], "NULL started_at 应序列化为 null（*time.Time 无值）")
			},
		},
		{
			name: "status 筛选_参数下发", method: http.MethodGet, path: "/api/system/backups?status=SUCCESS",
			wantStatus: http.StatusOK, wantCode: 0, fixture: rowsFixture,
			check: func(t *testing.T, env map[string]any) {
				args := sysCapture.argsOf("SELECT COUNT(*) FROM backup_records")
				require.True(t, argContains(args, "SUCCESS"), "status 筛选应参数化下发")
				require.Contains(t, sysCapture.queryOf("SELECT COUNT(*) FROM backup_records"), "status = ?")
			},
		},
	})

	// 已知 bug 复现位：status 白名单外的值 service 层以裸 error 拒绝（service_backups.go:20），
	// 经 response.Err 归一为 500 而非 400 参数错误（api.md §4：校验失败应 4xx + details）。
	t.Run("status 非法值_当前返回500", func(t *testing.T) {
		useSQLFixture(&sysFixture{})
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups?status=BOGUS", "")
		require.Equal(t, http.StatusInternalServerError, status, "现状：非法 status 走内部错误分支")
		require.Equal(t, "COMMON_INTERNAL_ERROR", env["code"])
		t.Skip("已知bug：备份状态筛选非法值返回 500 内部错误而非 400 参数错误（service_backups.go:20 裸 error 非 *Error）")
	})
}

// ---- 备份：POST /api/system/backups ----

func TestBackupRegisterEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	registerRow := []any{int64(3), "", "", int64(0), "MANUAL", "REQUESTED",
		"已登记，等待部署侧执行器拾取", nil, nil, tf3, int64(42)}

	t.Run("成功登记_REQUESTED_同事务审计", func(t *testing.T) {
		useSQLFixture(&sysFixture{
			queries: sysQRoutes(
				sysQRoute("WITH ins AS", "", []string{"id"}, []any{int64(3)}),
				sysQRoute("FROM backup_records WHERE id = ?", "", backupCols, registerRow),
				sysQRoute("INSERT INTO `operation_logs`", "", []string{"id"}, []any{int64(601)}),
			),
		})
		defer useSQLFixture(nil)

		status, env := e.do(http.MethodPost, "/api/system/backups", "")
		require.Equal(t, http.StatusOK, status, "%v", env)
		data := dataOf(t, env)
		backup := data["backup"].(map[string]any)
		require.EqualValues(t, 3, backup["id"])
		require.Equal(t, "REQUESTED", backup["status"], "登记即 REQUESTED，等待外部执行器拾取")
		require.Equal(t, "MANUAL", backup["trigger"])
		require.EqualValues(t, 42, backup["created_by"], "登记人归因=当前用户")
		require.Contains(t, strOf(t, data, "message"), "部署侧执行器", "响应说明真实能力（裁决②混合模式）")
		require.NotEmpty(t, sysCapture.queryOf("WITH ins AS"), "登记 INSERT 应经 CTE RETURNING 原子取回 ID")
		require.NotEmpty(t, sysCapture.queryOf("INSERT INTO `operation_logs`"), "登记必须同事务审计（backups.go:80）")
		require.Equal(t, 1, sysCapture.txCountOf("begin"))
		require.Equal(t, 1, sysCapture.txCountOf("commit"))
		require.Equal(t, 0, sysCapture.txCountOf("rollback"))
	})

	t.Run("在途冲突_409_回滚", func(t *testing.T) {
		useSQLFixture(&sysFixture{
			queryErr: func(q string, _ []driver.NamedValue) error {
				if strings.Contains(q, "WITH ins AS") {
					// driver 层回放在途唯一索引冲突（translateBackupDup 判定源，backups.go:90）。
					return &pgconn.PgError{Code: "23505", Message: "duplicate key value",
						ConstraintName: "uk_backup_records_inflight"}
				}
				return nil
			},
		})
		defer useSQLFixture(nil)

		status, env := e.do(http.MethodPost, "/api/system/backups", "")
		require.Equal(t, http.StatusConflict, status, "%v", env)
		require.Equal(t, "SYSTEM_BACKUP_INFLIGHT", env["code"], "并发手动触发应 409（plan §10.5）")
		require.Equal(t, 1, sysCapture.txCountOf("rollback"))
	})
}

// ---- 备份：GET /api/system/backups/:id/download ----

func TestBackupDownloadEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	backupRow := func(id int64, status, filePath, fileName string) *sysFixture {
		return &sysFixture{
			queries: sysQRoutes(
				sysQRoute("FROM backup_records WHERE id = ?", "", backupCols,
					[]any{id, fileName, filePath, int64(2048), "MANUAL", status, "", tf1, tf2, tf2, int64(42)}),
				sysQRoute("INSERT INTO `operation_logs`", "", []string{"id"}, []any{int64(602)}),
			),
		}
	}

	t.Run("成功下载_内容与附件头_下载审计", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "backups"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "backups", "sf-20261006.dump"),
			[]byte("PGDMP-bytes-0123456789"), 0o644))
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{StorageRoot: root})
		useSQLFixture(backupRow(5, "SUCCESS", "sf-20261006.dump", "stockflow_20261006.dump"))
		defer useSQLFixture(nil)

		w := e.doRaw(http.MethodGet, "/api/system/backups/5/download", "")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		require.Equal(t, `attachment; filename="stockflow_20261006.dump"`,
			w.Header().Get("Content-Disposition"), "下载文件名应取记录 file_name（防注入白名单化）")
		require.Contains(t, w.Body.String(), "PGDMP-bytes", "文件内容应流式回放")
		require.NotEmpty(t, sysCapture.queryOf("INSERT INTO `operation_logs`"),
			"下载为敏感操作必须审计（service_backups.go:44）")
		require.Equal(t, 1, sysCapture.txCountOf("begin"), "下载审计独立事务")
		require.Equal(t, 1, sysCapture.txCountOf("commit"))
	})

	t.Run("记录不存在_404", func(t *testing.T) {
		useSQLFixture(&sysFixture{
			queries: sysQRoutes(sysQRoute("FROM backup_records WHERE id = ?", "", backupCols)),
		})
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups/999/download", "")
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "SYSTEM_BACKUP_NOT_FOUND", env["code"])
	})

	t.Run("记录未完成_409", func(t *testing.T) {
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{StorageRoot: t.TempDir()}) // 根校验先于状态校验（backups.go:141-145）
		useSQLFixture(backupRow(6, "REQUESTED", "sf.dump", "sf.dump"))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups/6/download", "")
		require.Equal(t, http.StatusConflict, status, "%v", env)
		require.Equal(t, "SYSTEM_BACKUP_NOT_DOWNLOADABLE", env["code"])
	})

	t.Run("物理文件缺失_404", func(t *testing.T) {
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{StorageRoot: t.TempDir()}) // 根内无 backups/sf.dump
		useSQLFixture(backupRow(7, "SUCCESS", "sf.dump", "sf.dump"))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups/7/download", "")
		require.Equal(t, http.StatusNotFound, status, "%v", env)
		require.Equal(t, "SYSTEM_BACKUP_FILE_MISSING", env["code"])
	})

	t.Run("路径穿越尝试_不逃逸不泄漏", func(t *testing.T) {
		root := t.TempDir()
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{StorageRoot: root})
		useSQLFixture(backupRow(8, "SUCCESS", "../../escape.dump", "escape.dump"))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups/8/download", "")
		require.Equal(t, http.StatusNotFound, status, "%v", env)
		require.Equal(t, "SYSTEM_BACKUP_FILE_MISSING", env["code"],
			"穿越路径经归一落在 backups 根内且文件缺失（backups.go:146 防穿越）")
	})

	t.Run("存储根未配置_503", func(t *testing.T) {
		t.Cleanup(func() { Configure(runtimeConfig{}) })
		Configure(runtimeConfig{})
		useSQLFixture(backupRow(9, "SUCCESS", "sf.dump", "sf.dump"))
		defer useSQLFixture(nil)
		status, env := e.do(http.MethodGet, "/api/system/backups/9/download", "")
		require.Equal(t, http.StatusServiceUnavailable, status, "%v", env)
		require.Equal(t, "SYSTEM_STORAGE_ROOT_NOT_CONFIGURED", env["code"])
	})
}

// ---- 通知个人收件箱 ----

func TestNotificationsEndpoint(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	inboxFixture := &sysFixture{
		queries: sysQRoutes(
			// 未读数：SELECT COUNT(*) ... AND "read" = FALSE。
			sysQRoute("FROM notifications", `"read" = FALSE`, []string{"count"}, []any{int64(7)}),
			// 收件箱计数：SELECT COUNT(*) FROM notifications WHERE user_id = ? AND ...
			sysQRoute("FROM notifications", "SELECT COUNT(*)", []string{"count"}, []any{int64(2)}),
			// 收件箱明细（ORDER BY 守卫与计数查询区分）。
			sysQRoute(`"read", created_at FROM notifications`, "ORDER BY created_at", inboxCols,
				[]any{int64(41), "STOCK_ALERT", "低库存预警", "SKU-1 available 3 ≤ safety 5", false, tf1},
				[]any{int64(40), "SYSTEM", "备份完成", "stockflow_20261005.dump", true, tf0}),
		),
		execs: sysERoutes(sysExecRoute{match: "UPDATE notifications SET", affected: 1}),
	}

	e.run([]sysCase{
		{
			name: "未读数", method: http.MethodGet, path: "/api/notifications/unread-count",
			wantStatus: http.StatusOK, wantCode: 0, fixture: inboxFixture,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 7, env["data"], "未读数为裸数值 data")
			},
		},
		{
			name: "收件箱_ID 字符串形态与时间格式", method: http.MethodGet, path: "/api/notifications",
			wantStatus: http.StatusOK, wantCode: 0, fixture: inboxFixture,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 2, data["total"])
				items := itemsOf(t, env)
				require.Len(t, items, 2)
				row := items[0].(map[string]any)
				// database.ID JSON 字符串形态（api.md 2026-10-05 收口轮；notifications.go:245）。
				require.Equal(t, "41", row["id"], "个人收件箱行 id 统一字符串形态")
				// JSONTime 统一 YYYY-MM-DD HH:mm:ss（api.md §2）。
				require.Equal(t, "2026-10-05 02:30:00", row["created_at"])
				require.Equal(t, "STOCK_ALERT", row["type"])
				require.Equal(t, false, row["read"])
				require.NotContains(t, row, "dedup_key", "收件箱不回显 dedup 内部键")
			},
		},
		{
			name: "收件箱_read 筛选_参数下发", method: http.MethodGet, path: "/api/notifications?read=true",
			wantStatus: http.StatusOK, wantCode: 0, fixture: inboxFixture,
			check: func(t *testing.T, env map[string]any) {
				args := sysCapture.argsOf("SELECT COUNT(*) FROM notifications WHERE user_id")
				require.True(t, argContains(args, true), "read 筛选参数缺失")
				require.True(t, argContains(args, int64(42)), "user_id 应取当前用户（个人数据自见）")
			},
		},
		{
			name: "read 非法值_400", method: http.MethodGet, path: "/api/notifications?read=maybe",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: inboxFixture,
			check: func(t *testing.T, env map[string]any) { require.Equal(t, "read", detailsOf(t, env)["field"]) },
		},
		{
			name: "收件箱空列表_items 数组非 null", method: http.MethodGet, path: "/api/notifications?read=false",
			wantStatus: http.StatusOK, wantCode: 0,
			fixture: &sysFixture{
				queries: sysQRoutes(
					sysQRoute("FROM notifications", `"read" = FALSE`, []string{"count"}, []any{int64(0)}),
					sysQRoute("FROM notifications", "SELECT COUNT(*)", []string{"count"}, []any{int64(0)}),
					sysQRoute(`"read", created_at FROM notifications`, "ORDER BY created_at", inboxCols),
				),
			},
			check: func(t *testing.T, env map[string]any) {
				require.NotNil(t, itemsOf(t, env), "items:null 违反 api.md §2.1（listInbox 预置空 slice）")
				require.Empty(t, itemsOf(t, env))
			},
		},
		{
			name: "标记已读_成功", method: http.MethodPost, path: "/api/notifications/41/read",
			wantStatus: http.StatusOK, wantCode: 0, fixture: inboxFixture,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["marked"])
				args := sysCapture.execArgsOf("UPDATE notifications SET")
				require.NotNil(t, args, "已读必须真实 UPDATE")
				require.True(t, argContains(args, int64(41)) && argContains(args, int64(42)),
					"UPDATE 应同时限定 id 与 user_id（仅本人行，notifications.go:296）")
			},
		},
		{
			name: "标记已读_非本人或不存在_404", method: http.MethodPost, path: "/api/notifications/999/read",
			wantStatus: http.StatusNotFound, wantCode: "SYSTEM_NOTIFICATION_NOT_FOUND",
			fixture: &sysFixture{
				execs: sysERoutes(sysExecRoute{match: "UPDATE notifications SET", affected: 0}),
			},
		},
		{
			name: "标记已读_id 非法_400", method: http.MethodPost, path: "/api/notifications/0/read",
			wantStatus: http.StatusBadRequest, wantCode: "COMMON_INVALID_PARAM", fixture: inboxFixture,
		},
	})
}

func TestNotificationsMarkAllRead(t *testing.T) {
	e := newSysEnv(t, sysSuperUser())
	useSQLFixture(&sysFixture{
		execs: sysERoutes(sysExecRoute{match: "UPDATE notifications SET", affected: 3}),
	})
	defer useSQLFixture(nil)

	status, env := e.do(http.MethodPost, "/api/notifications/read-all", "")
	require.Equal(t, http.StatusOK, status, "%v", env)
	require.EqualValues(t, 3, dataOf(t, env)["marked"], "全部已读应回显受影响行数")
}

// ---- 权限与认证分支（真实中间件链路）----

func TestSysopsAuthFailClosed(t *testing.T) {
	t.Run("未认证_权限面401", func(t *testing.T) {
		e := newSysEnv(t, nil) // 不注入用户上下文
		for _, path := range []string{
			"/api/logs/operations", "/api/logs/logins", "/api/system/configs",
			"/api/system/jobs", "/api/system/monitor", "/api/system/backups",
			"/api/system/backups/pg-dump-template",
		} {
			status, env := e.do(http.MethodGet, path, "")
			require.Equal(t, http.StatusUnauthorized, status, "%s 应 401: %v", path, env)
			require.Equal(t, "COMMON_UNAUTHORIZED", env["code"], "%s 应 fail-closed（routes.go RequirePerm）", path)
		}
	})

	t.Run("未认证_收件箱401", func(t *testing.T) {
		e := newSysEnv(t, nil)
		status, env := e.do(http.MethodGet, "/api/notifications/unread-count", "")
		require.Equal(t, http.StatusUnauthorized, status, "%v", env)
		require.Equal(t, "COMMON_UNAUTHORIZED", env["code"], "认证即可用 ≠ 匿名可用（routes.go:528）")
		status, env = e.do(http.MethodPost, "/api/notifications/read-all", "")
		require.Equal(t, http.StatusUnauthorized, status)
		require.Equal(t, "COMMON_UNAUTHORIZED", env["code"])
	})

	t.Run("非超管_auth未装配_fail-closed 500", func(t *testing.T) {
		// RequirePermission：非超管走 snapshotWired——本测试二进制 auth 恒未装配，
		// 按中间件语义 fail-closed 500（internal/auth/middleware.go），不放行不静默。
		e := newSysEnv(t, &auth.UserContext{UserID: 7, Username: "op", IsSuper: false})
		status, env := e.do(http.MethodGet, "/api/system/monitor", "")
		require.Equal(t, http.StatusInternalServerError, status, "%v", env)
		require.Equal(t, "COMMON_INTERNAL_ERROR", env["code"])
		require.Equal(t, "auth 中间件未装配", detailsOf(t, env)["reason"])
	})
}
