package reports

// 测试替身：可按「SQL 文本子串 + 可选二重子串」路由回放行数据的假 gorm 方言器
// （database/sql 假驱动支撑）。目的：31 条聚合端点（handler→service→repository 的
// 只读 Raw SELECT 链路）在无 PostgreSQL 环境下可测——驱动层按测试夹具回放聚合行，
// 断言落在 handler 参数绑定/校验、统一信封与分页形态、Service 层加工上。
//
// 与 internal/masterdata/fakedb_test.go 的 fakeQueryFixture 同一项目约定（Raw SELECT
// 装配路径经钩子按 SQL 文本路由，非第二套机制），差异仅两点：
//  1. 夹具可感知参数（driver.NamedValue）——todayFlowCount 同文异参（INBOUND/OUTBOUND，
//     dashboard.go todayFlowCount）必须按参数分流；
//  2. 未命中夹具 fail-loud 报错而非静默空结果——漏配夹具应得到响亮的失败而非 0 值假通过。
//
// 同包测试串行执行（全局夹具，masterdata 同注），禁用 t.Parallel。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/stockflow/server/internal/auth"
)

// ---- 全局查询夹具（同包测试串行复用） ----

var (
	fixtureMu   sync.Mutex
	fixtureFunc func(query string, args []driver.NamedValue) (cols []string, rows [][]driver.Value)
)

// useFixture 装配查询夹具（nil=卸载）；用例以 defer useFixture(nil) 收尾。
func useFixture(f func(query string, args []driver.NamedValue) (cols []string, rows [][]driver.Value)) {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	fixtureFunc = f
}

func currentFixture() func(query string, args []driver.NamedValue) (cols []string, rows [][]driver.Value) {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	return fixtureFunc
}

// ---- database/sql 假驱动 ----

const rptFakeDriverName = "sfake-reports-test"

var drvOnce sync.Once

func registerFakeDriver() {
	drvOnce.Do(func() {
		sql.Register(rptFakeDriverName, fakeDriver{})
	})
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("reports 测试假驱动不支持 Prepare（Raw 查询应走 QueryerContext）")
}

func (*fakeConn) Close() error { return nil }

func (*fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("reports 测试假驱动不支持事务（本域为只读聚合，不应触达）")
}

// QueryContext 夹具路由入口：gorm Raw 经 clause.Expr.Build 展开占位符后
// （chainable_api.go Raw → Expr.Build）经 ConnPool.QueryContext 直达此处。
func (*fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if f := currentFixture(); f != nil {
		cols, rows := f(query, args)
		if cols != nil {
			return &fakeRows{cols: cols, rows: rows}, nil
		}
		// fail-loud：漏配夹具必须响亮失败，禁止以空结果伪装聚合数据。
		return nil, fmt.Errorf("reports 测试假驱动: SQL 未命中夹具: %.200s", query)
	}
	return nil, errors.New("reports 测试假驱动: 未装配查询夹具（用例不应触达 SQL）")
}

var _ driver.Conn = (*fakeConn)(nil)
var _ driver.QueryerContext = (*fakeConn)(nil)

// fakeRows 多列配置行（夹具投影承载）。
type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	if len(r.rows[r.i]) != len(dest) {
		return fmt.Errorf("reports 测试假驱动: 夹具行值数 %d 与列数 %d 不一致", len(r.rows[r.i]), len(dest))
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// ---- gorm 方言器（形态对齐 gorm.io/gorm/utils/tests 的 DummyDialector + 可用 ConnPool）----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake-reports" }

func (fakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	registerFakeDriver()
	sqlDB, err := sql.Open(rptFakeDriverName, "")
	if err != nil {
		return err
	}
	db.ConnPool = sqlDB
	return nil
}

func (fakeDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (fakeDialector) Migrator(*gorm.DB) gorm.Migrator { return nil }

func (fakeDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	writer.WriteByte('?')
}

func (fakeDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteByte('`')
	writer.WriteString(str)
	writer.WriteByte('`')
}

func (fakeDialector) Explain(sqlStr string, vars ...any) string {
	return logger.ExplainSQL(sqlStr, nil, `"`, vars...)
}

func (fakeDialector) DataTypeOf(*schema.Field) string { return "text" }

// openFakeGorm 打开假 gorm 句柄（不落任何真实存储）。
func openFakeGorm() (*gorm.DB, error) {
	registerFakeDriver()
	return gorm.Open(fakeDialector{}, &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
}

// ---- 夹具构造助手 ----

// toDriverValue 夹具值归一为合法 driver.Value（行数据不经 database/sql 转换，必须自备合法类型）。
func toDriverValue(v any) driver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		return int64(x)
	case int64, float64, bool, string, []byte, time.Time:
		return x
	default:
		panic(fmt.Sprintf("reports 测试夹具: 不支持的值类型 %T", v))
	}
}

// fixtureRows 行构造：列名与各行值数必须一致（fakeRows.Next 再校验一遍）。
func fixtureRows(cols []string, rows ...[]any) ([]string, [][]driver.Value) {
	out := make([][]driver.Value, 0, len(rows))
	for _, r := range rows {
		if len(r) != len(cols) {
			panic(fmt.Sprintf("reports 测试夹具: 行值数 %d 与列数 %d 不一致", len(r), len(cols)))
		}
		vals := make([]driver.Value, len(r))
		for i, v := range r {
			vals[i] = toDriverValue(v)
		}
		out = append(out, vals)
	}
	return cols, out
}

// rptRoute 单条路由：SQL 含 match（必选）且含 count（可选二重子串）即命中。
// count 二重子串用于区分 paged 的「SELECT COUNT(*) FROM (...) agg」计数与带
// LIMIT/OFFSET 的明细查询（二者共享同一 base SQL 文本，repository.go paged）。
type rptRoute struct {
	match string
	count string
	cols  []string
	rows  [][]driver.Value
}

// rroute 行路由构造。
func rroute(match, count string, cols []string, rows ...[]any) rptRoute {
	c, r := fixtureRows(cols, rows...)
	return rptRoute{match: match, count: count, cols: c, rows: r}
}

// rrouteCount 计数路由（paged COUNT / 标量计数查询）。
func rrouteCount(match, marker string, n int64) rptRoute {
	return rroute(match, marker, []string{"count"}, []any{n})
}

// rrouteScalar 标量值路由（float64/int64 聚合值）。
func rrouteScalar(match string, v any) rptRoute {
	return rroute(match, "", []string{"value"}, []any{v})
}

// fixtureRoutes 子串路由夹具（先命中先返回；未命中由 QueryContext fail-loud）。
func fixtureRoutes(routes ...rptRoute) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	return func(query string, _ []driver.NamedValue) ([]string, [][]driver.Value) {
		for _, rt := range routes {
			if strings.Contains(query, rt.match) && (rt.count == "" || strings.Contains(query, rt.count)) {
				return rt.cols, rt.rows
			}
		}
		return nil, nil
	}
}

// queryLog SQL/参数捕获（口径断言：范围条件、软删过滤、ORDER BY 段、估值列、参数）。
type queryLog struct {
	mu      sync.Mutex
	queries []string
	args    [][]driver.NamedValue
}

func (q *queryLog) record(query string, args []driver.NamedValue) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queries = append(q.queries, query)
	q.args = append(q.args, args)
}

// anyContains 是否存在捕获 SQL 含子串。
func (q *queryLog) anyContains(sub string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range q.queries {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// noneContains 是否所有捕获 SQL 均不含子串。
func (q *queryLog) noneContains(sub string) bool {
	return !q.anyContains(sub)
}

// argsOf 返回第一条含 sub 的 SQL 参数（未命中返回 nil）。
func (q *queryLog) argsOf(sub string) []driver.NamedValue {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, s := range q.queries {
		if strings.Contains(s, sub) {
			return q.args[i]
		}
	}
	return nil
}

// logAndRoute 组合「捕获 + 子串路由」的夹具。
func logAndRoute(log *queryLog, routes ...rptRoute) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	route := fixtureRoutes(routes...)
	return func(query string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		log.record(query, args)
		return route(query, args)
	}
}

// ---- HTTP 测试环境（handler 直挂；权限中间件 fail-closed 形态由 routes_test.go 冻结） ----

// rptUserCtxKey auth.middleware.go ctxUserKey（包私有常量，字符串为冻结契约）；
// 权限点校验（RequirePermission）属 auth 中间件职责，routes_test.go 已冻结未认证
// 401 fail-closed 形态——数据用例经此键直注 UserContext 快照以驱动 scopeOf/CurrentUser。
const rptUserCtxKey = "sf_auth_user"

type testEnv struct {
	t    *testing.T
	db   *gorm.DB
	svc  *Service
	r    *gin.Engine
	user auth.UserContext
}

// newUser 构造用户上下文快照（UserID 恒 42 供 /api/tasks assignee 断言）。
func newUser(isSuper bool, dataScope string, warehouseIDs ...int64) auth.UserContext {
	return auth.UserContext{
		UserID:       42,
		Username:     "tester",
		IsSuper:      isSuper,
		DataScope:    dataScope,
		WarehouseIDs: warehouseIDs,
	}
}

// newTestEnv 建假 gorm + Service + 直挂全部 31 条 handler 路径的 gin 引擎
// （路径集与 routes.go 一致；权限中间件不挂，原因见 rptUserCtxKey 注）。
func newTestEnv(t *testing.T, uc auth.UserContext, opts ...Option) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	env := &testEnv{t: t, db: db, svc: NewService(db, opts...), user: uc}
	h := &handler{svc: env.svc}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(rptUserCtxKey, env.user)
		c.Next()
	})
	g := r.Group("/api")
	g.GET("/reports", h.catalog)
	g.GET("/reports/inventory-summary", h.inventorySummary)
	g.GET("/reports/inbound-stats", h.inboundStats)
	g.GET("/reports/outbound-stats", h.outboundStats)
	g.GET("/reports/inventory-turnover", h.turnover)
	g.GET("/reports/stagnant-stock", h.stagnantStock)
	g.GET("/reports/replenishment-suggestions", h.replenishment)
	g.GET("/reports/dashboard/today", h.dashboardToday)
	g.GET("/reports/dashboard/trend", h.dashboardTrend)
	g.GET("/reports/dashboard/tasks", h.dashboardTasks)
	g.GET("/reports/dashboard/alerts", h.dashboardAlertFeed)
	g.GET("/reports/dashboard/warehouse-stock", h.dashboardWarehouseStock)
	g.GET("/inventory/summary", h.dashboardSummary)
	g.GET("/inventory/alerts", h.dashboardAlerts)
	g.GET("/inventory/analytics", h.inventoryAnalytics)
	g.GET("/inventory/sku-top", h.skuTop)
	g.GET("/inventory/turnover-trend", h.turnoverTrend)
	g.GET("/reports/flow-trend", h.flowTrend)
	g.GET("/inbounds/status-composition", h.inboundStatusComposition)
	g.GET("/inbounds/supplier-rank", h.inboundSupplierRank)
	g.GET("/outbounds/completion-rate", h.outboundCompletionRate)
	g.GET("/outbounds/product-rank", h.outboundProductRank)
	g.GET("/warehouses/workload", h.warehouseWorkload)
	g.GET("/purchases/analytics/trend", h.purchaseTrend)
	g.GET("/purchases/supplier-rank", h.purchaseSupplierRank)
	g.GET("/purchases/status-composition", h.purchaseStatusComposition)
	g.GET("/sales/analytics/trend", h.salesTrend)
	g.GET("/sales/product-rank", h.salesProductRank)
	g.GET("/sales/status-composition", h.salesStatusComposition)
	g.GET("/workbench/summary", h.workbenchSummary)
	g.GET("/tasks", h.myTasks)
	env.r = r
	return env
}

// envEnvelope 统一信封解码（response.go：成功 code=0 数字、失败为字符串错误码）。
type envEnvelope struct {
	httpStatus int
	Code       json.RawMessage `json:"code"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
	Details    json.RawMessage `json:"details"`
}

func (e envEnvelope) ok() bool { return string(e.Code) == "0" }

// get 发起 GET 并解码统一信封。
func (env *testEnv) get(path string) envEnvelope {
	env.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	var out envEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		env.t.Fatalf("信封 JSON 解析失败（响应: %s）: %v", rec.Body.String(), err)
	}
	out.httpStatus = rec.Code
	return out
}

// mustOK 断言 200 + code=0，返回信封。
func (env *testEnv) mustOK(path string) envEnvelope {
	env.t.Helper()
	out := env.get(path)
	if !out.ok() || out.httpStatus != http.StatusOK {
		env.t.Fatalf("GET %s 应成功（200/code=0），实际 http=%d code=%s message=%s details=%s",
			path, out.httpStatus, out.Code, out.Message, out.Details)
	}
	return out
}

// mustErr 断言失败信封的 HTTP 状态与错误码字符串。
func (env *testEnv) mustErr(path string, wantStatus int, wantCode string) envEnvelope {
	env.t.Helper()
	out := env.get(path)
	if out.ok() {
		env.t.Fatalf("GET %s 应失败（%s），实际成功: %s", path, wantCode, out.Data)
	}
	if out.httpStatus != wantStatus || string(out.Code) != fmt.Sprintf("%q", wantCode) {
		env.t.Fatalf("GET %s 应为 %d/%s，实际 http=%d code=%s message=%s details=%s",
			path, wantStatus, wantCode, out.httpStatus, out.Code, out.Message, out.Details)
	}
	return out
}

// decode 将 data 段解码为 dst（同包测试直用业务 DTO，不另造影子结构）。
func (env *testEnv) decode(envp envEnvelope, dst any) {
	env.t.Helper()
	decodeJSON(env.t, envp.Data, dst)
}

// pageData 统一分页信封 data 形态（api.md §2.1：{page,pageSize,total,items}）。
type pageData struct {
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int64           `json:"total"`
	Items    json.RawMessage `json:"items"`
}

// decodeJSON JSON 解码助手。
func decodeJSON(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("JSON 解码失败: %v（%s）", err, raw)
	}
}

// assertFloat 浮点断言（聚合数值 1e-9 容差）。
func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s 应为 %v，实际 %v", name, want, got)
	}
}

// parseT 统一时间文本解析（api.md §2 YYYY-MM-DD HH:mm:ss）。
func parseT(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		t.Fatalf("时间解析失败 %q: %v", s, err)
	}
	return v
}

// assertTime 时间断言（同刻等值，容忍单调时钟差异）。
func assertTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s 应为 %s，实际 %s", name, want.Format(time.RFC3339), got.Format(time.RFC3339))
	}
}

// thresholdsOption 注入预警阈值替身（expiry 固定 30/15/7/3，stagnant 由变参给定，
// 与 parsePositiveIntList 降序输出同形——service.go 注入点形态）。
func thresholdsOption(stagnantDays ...int) Option {
	return WithAlertThresholds(func(context.Context) ([]int, []int, error) {
		return []int{30, 15, 7, 3}, stagnantDays, nil
	})
}
