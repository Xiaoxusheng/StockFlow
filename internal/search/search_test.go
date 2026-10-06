package search

// 测试（efficiency-layer-phase1 §7.1：T1 权限过滤 / T2 数据权限 / T3 短路 + 路由冻结
// T14 口径）。零外部依赖，fakedb 项目约定（internal/reports/fakedb_test.go 同款）：
// database/sql 假驱动 + gorm 方言器，驱动层按「SQL 文本子串」路由回放行数据；
// 漏配夹具 fail-loud 报错而非静默空结果。同包测试串行执行（全局夹具），禁 t.Parallel。
//
// 权限中间件不挂（RequirePermission 属 auth 职责）：数据用例经 ctxUserKey 直注
// UserContext 快照驱动 CurrentUser/WarehouseScope，权限判定经 handler.perm 注入点
// 替换（包内私有字段，语义与 auth.HasPermission 的调用面一致）。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const sfFakeDriverName = "sfake-search-test"

var drvOnce sync.Once

func registerFakeDriver() {
	drvOnce.Do(func() {
		sql.Register(sfFakeDriverName, fakeDriver{})
	})
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct{}

func (*fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("search 测试假驱动不支持 Prepare（Raw 查询应走 QueryerContext）")
}

func (*fakeConn) Close() error { return nil }

func (*fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("search 测试假驱动不支持事务（本域只读，不应触达）")
}

// QueryContext 夹具路由入口（gorm Raw 经 ConnPool.QueryContext 直达此处）。
func (*fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if f := currentFixture(); f != nil {
		cols, rows := f(query, args)
		if cols != nil {
			return &fakeRows{cols: cols, rows: rows}, nil
		}
		// fail-loud：漏配夹具必须响亮失败，禁止以空结果伪装搜索数据。
		return nil, fmt.Errorf("search 测试假驱动: SQL 未命中夹具: %.200s", query)
	}
	return nil, errors.New("search 测试假驱动: 未装配查询夹具（短路用例不应触达 SQL）")
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
		return fmt.Errorf("search 测试假驱动: 夹具行值数 %d 与列数 %d 不一致", len(r.rows[r.i]), len(dest))
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// ---- gorm 方言器（形态对齐 reports fakedb_test.go 同款） ----

type fakeDialector struct{}

func (fakeDialector) Name() string { return "sfake-search" }

func (fakeDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{
		CreateClauses:        []string{"INSERT", "VALUES", "ON CONFLICT", "RETURNING"},
		UpdateClauses:        []string{"UPDATE", "SET", "WHERE", "RETURNING"},
		DeleteClauses:        []string{"DELETE", "FROM", "WHERE", "RETURNING"},
		LastInsertIDReversed: true,
	})
	registerFakeDriver()
	sqlDB, err := sql.Open(sfFakeDriverName, "")
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

// toDriverValue 夹具值归一为合法 driver.Value。
func toDriverValue(v any) driver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		return int64(x)
	case int64, float64, bool, string, []byte, time.Time:
		return x
	default:
		panic(fmt.Sprintf("search 测试夹具: 不支持的值类型 %T", v))
	}
}

// fixtureRows 行构造：列名与各行值数必须一致。
func fixtureRows(cols []string, rows ...[]any) ([]string, [][]driver.Value) {
	out := make([][]driver.Value, 0, len(rows))
	for _, r := range rows {
		if len(r) != len(cols) {
			panic(fmt.Sprintf("search 测试夹具: 行值数 %d 与列数 %d 不一致", len(r), len(cols)))
		}
		vals := make([]driver.Value, len(r))
		for i, v := range r {
			vals[i] = toDriverValue(v)
		}
		out = append(out, vals)
	}
	return cols, out
}

// searchRowCols 搜索单元行统一投影列（repository.go row）。
func searchRowCols() []string {
	return []string{"id", "title", "code", "status", "summary", "updated_at", "match_rank", "match_total"}
}

// fixtureRoutes 子串路由夹具（先命中先返回；未命中由 QueryContext fail-loud）。
// 条目形如 []any{match 子串, 行, 行...}。
func fixtureRoutes(pairs ...([]any)) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	type entry struct {
		m    string
		cols []string
		rows [][]driver.Value
	}
	entries := make([]entry, 0, len(pairs))
	for _, p := range pairs {
		m := p[0].(string)
		rowSpecs := make([][]any, 0, len(p)-1)
		for _, r := range p[1:] {
			rowSpecs = append(rowSpecs, r.([]any))
		}
		cols, rows := fixtureRows(searchRowCols(), rowSpecs...)
		entries = append(entries, entry{m, cols, rows})
	}
	return func(query string, _ []driver.NamedValue) ([]string, [][]driver.Value) {
		for _, e := range entries {
			if strings.Contains(query, e.m) {
				return e.cols, e.rows
			}
		}
		return nil, nil
	}
}

// queryLog SQL/参数捕获（计数与口径断言）。
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

// count 已捕获 SQL 条数（T3 短路断言：repo 调用计数=0）。
func (q *queryLog) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queries)
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

// lastArgsOf 返回最后一条含 sub 的 SQL 参数（同文多次查询取末次，未命中返回 nil）。
func (q *queryLog) lastArgsOf(sub string) []driver.NamedValue {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := len(q.queries) - 1; i >= 0; i-- {
		if strings.Contains(q.queries[i], sub) {
			return q.args[i]
		}
	}
	return nil
}

// logAndRoute 组合「捕获 + 子串路由」的夹具。
func logAndRoute(log *queryLog, route func(string, []driver.NamedValue) ([]string, [][]driver.Value)) func(string, []driver.NamedValue) ([]string, [][]driver.Value) {
	return func(query string, args []driver.NamedValue) ([]string, [][]driver.Value) {
		log.record(query, args)
		return route(query, args)
	}
}

// ---- HTTP 测试环境 ----

// ctxUserKey auth.middleware.go ctxUserKey（包私有常量，字符串为冻结契约）。
const ctxUserKey = "sf_auth_user"

// testUser 构造用户上下文快照。
func testUser(isSuper bool, dataScope string, warehouseIDs ...int64) auth.UserContext {
	return auth.UserContext{
		UserID:       42,
		Username:     "tester",
		IsSuper:      isSuper,
		DataScope:    dataScope,
		WarehouseIDs: warehouseIDs,
	}
}

// testEnv 假 gorm + handler 直挂 GET /api/search 的 gin 引擎。
type testEnv struct {
	t    *testing.T
	log  *queryLog
	r    *gin.Engine
	perm map[string]bool
}

// newTestEnv 建测试环境（permCodes=nil 表示超管直通语义外的全量放行由 perm 恒 true 承载；
// 传入集合则按集合判定，模拟 HasPermission 权限快照）。
func newTestEnv(t *testing.T, uc auth.UserContext, permCodes ...string) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	env := &testEnv{t: t, log: &queryLog{}, perm: make(map[string]bool, len(permCodes))}
	for _, code := range permCodes {
		env.perm[code] = true
	}
	h := &handler{svc: NewService(newRepository(db)), perm: func(_ *gin.Context, code string) bool {
		return env.perm[code] // 权限快照替身（与 auth.HasPermission 调用面同构）
	}}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(ctxUserKey, uc)
		c.Next()
	})
	g := r.Group("/api")
	g.GET("/search", h.search)
	env.r = r
	return env
}

// envEnvelope 统一信封解码（成功 code=0 数字、失败为字符串错误码）。
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

// mustErr 断言失败信封的 HTTP 状态与错误码字符串。
func (env *testEnv) mustErr(path string, wantStatus int, wantCode string) {
	env.t.Helper()
	out := env.get(path)
	if out.ok() {
		env.t.Fatalf("GET %s 应失败（%s），实际成功: %s", path, wantCode, out.Data)
	}
	if out.httpStatus != wantStatus || string(out.Code) != fmt.Sprintf("%q", wantCode) {
		env.t.Fatalf("GET %s 应为 %d/%s，实际 http=%d code=%s message=%s details=%s",
			path, wantStatus, wantCode, out.httpStatus, out.Code, out.Message, out.Details)
	}
}

// groupsOf 解码 data.groups。
func (env *testEnv) groupsOf(e envEnvelope) []Group {
	env.t.Helper()
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := json.Unmarshal(e.Data, &out); err != nil {
		env.t.Fatalf("groups 解码失败: %v（%s）", err, e.Data)
	}
	return out.Groups
}

var skuFixtureAt = time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local)

// skuRowFixture 一行 sku 搜索单元行（id=7）。
func skuRowFixture(total int64) []any {
	return []any{int64(7), "测试SKU甲", "SKU001", "ENABLED", "", skuFixtureAt, int64(0), total}
}

// barcodeRowFixture 一行 barcode 搜索单元行（与 sku 同用 masterdata:sku:list，有码
// 用例会连带查询 barcodes 表；id=8）。
func barcodeRowFixture() []any {
	return []any{int64(8), "6901234567890", "6901234567890", "", "CODE128", skuFixtureAt, int64(0), int64(1)}
}

// ---- T1 权限过滤 ----

// TestSearchPermissionFilter 无 masterdata:sku:list 的用户查 "SKU001" → groups 不含
// sku 组且零 SQL；有码则含且 items 形状齐（T1）。
func TestSearchPermissionFilter(t *testing.T) {
	route := fixtureRoutes([]any{"FROM skus", skuRowFixture(1)}, []any{"FROM barcodes", barcodeRowFixture()})

	// 无权限：不查询不出组，且零 SQL（组内 type 过滤先于仓储调用）。
	env := newTestEnv(t, testUser(false, "ALL"))
	useFixture(logAndRoute(env.log, route))
	defer useFixture(nil)
	out := env.get("/api/search?q=SKU001")
	if got := env.log.count(); got != 0 {
		t.Fatalf("无权限用户不应触达 SQL，实际 %d 条", got)
	}
	if gs := env.groupsOf(out); len(gs) != 0 {
		t.Fatalf("无权限用户 groups 应为空，实际 %d 组", len(gs))
	}

	// 有码：含 sku 组，items 形状齐（业务 ID 字符串形态 + 统一时间格式）。
	env = newTestEnv(t, testUser(false, "ALL"), "masterdata:sku:list")
	useFixture(logAndRoute(env.log, route))
	defer useFixture(nil)
	out = env.get("/api/search?q=SKU001")
	if !out.ok() {
		t.Fatalf("搜索应成功，实际 code=%s message=%s", out.Code, out.Message)
	}
	gs := env.groupsOf(out)
	// sku 与 barcode 同用 masterdata:sku:list（§2.1 清单口径），两组合法。
	typed := map[string]Group{}
	for _, g := range gs {
		typed[g.Type] = g
	}
	sku, ok := typed["sku"]
	if !ok || sku.Title != "SKU" {
		t.Fatalf("应含 sku 组，实际 %v", gs)
	}
	if _, ok := typed["product"]; ok {
		t.Fatalf("无 masterdata:product:list 不应出 product 组，实际 %v", gs)
	}
	if sku.Count != 1 || len(sku.Items) != 1 {
		t.Fatalf("count/items 应为 1，实际 count=%d items=%d", sku.Count, len(sku.Items))
	}
	item := sku.Items[0]
	if item.ID != "7" || item.Type != "sku" || item.Code != "SKU001" || item.Status != "ENABLED" {
		t.Fatalf("item 形状不符: %+v", item)
	}
	if !item.UpdatedAt.Equal(skuFixtureAt) {
		t.Fatalf("updated_at 应为 %s，实际 %s", skuFixtureAt, item.UpdatedAt)
	}
	// navigation 跳转标识：普通 type 与 typ 同值、id 与条目同值。
	if item.Navigation.Kind != "sku" || item.Navigation.ID != "7" {
		t.Fatalf("navigation 应为 {sku,7}，实际 %+v", item.Navigation)
	}
	// JSON 形状冻结：八字段齐备（id/type/title/code/status/summary/updated_at/navigation）。
	raw := struct {
		Groups []map[string]any `json:"groups"`
	}{}
	if err := json.Unmarshal(out.Data, &raw); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	for _, key := range []string{"id", "type", "title", "code", "status", "summary", "updated_at", "navigation"} {
		itemMap, ok := raw.Groups[0]["items"].([]any)[0].(map[string]any)
		if !ok {
			t.Fatalf("items[0] 应为对象形态")
		}
		if _, ok := itemMap[key]; !ok {
			t.Fatalf("item JSON 缺字段 %s", key)
		}
	}
}

// ---- T2 数据权限（scope 交集） ----

// TestSearchDataScope warehouse_id 与 scopeOf 求交：越界仓库该 type 零 SQL 零结果
// （不 403）；范围内仓库过滤生效（T2）。
func TestSearchDataScope(t *testing.T) {
	route := fixtureRoutes([]any{"FROM serial_numbers", []any{
		int64(9), "SN20261006001", "SN20261006001", "IN_STOCK", "", skuFixtureAt, int64(0), int64(1),
	}})
	uc := testUser(false, "SPECIFIED_WAREHOUSE", 1, 2)

	// 越界仓库（3 ∉ {1,2}）：零结果且不执行 SQL（防范围探测）。
	env := newTestEnv(t, uc, "inventory:serial:list")
	useFixture(logAndRoute(env.log, route))
	defer useFixture(nil)
	out := env.get("/api/search?q=SN20261006001&types=serial&warehouse_id=3")
	if !out.ok() {
		t.Fatalf("越界仓库应 200 零结果而非 403，实际 code=%s", out.Code)
	}
	if gs := env.groupsOf(out); len(gs) != 0 {
		t.Fatalf("越界仓库 groups 应为空，实际 %+v", gs)
	}
	if got := env.log.count(); got != 0 {
		t.Fatalf("越界仓库不应触达 SQL，实际 %d 条", got)
	}

	// 范围内仓库（2 ∈ {1,2}）：SQL 执行且过滤参数为收窄值 2。
	env = newTestEnv(t, uc, "inventory:serial:list")
	useFixture(logAndRoute(env.log, route))
	defer useFixture(nil)
	out = env.get("/api/search?q=SN20261006001&types=serial&warehouse_id=2")
	if !out.ok() {
		t.Fatalf("范围内仓库应成功，实际 code=%s", out.Code)
	}
	gs := env.groupsOf(out)
	if len(gs) != 1 || gs[0].Type != "serial" || len(gs[0].Items) != 1 {
		t.Fatalf("应恰含 serial 组一条，实际 %+v", gs)
	}
	args := env.log.argsOf("FROM serial_numbers")
	if args == nil {
		t.Fatalf("应捕获 serial 查询 SQL")
	}
	found := false
	for _, a := range args {
		if v, ok := a.Value.(int64); ok && v == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("SQL 参数应含收窄仓库 2，实际 %v", args)
	}
}

// ---- T3 短路 ----

// TestSearchShortCircuit q<2/空查询零 SQL；q 超长 400；limit 缺省 5 上限 20（T3）。
func TestSearchShortCircuit(t *testing.T) {
	route := fixtureRoutes([]any{"FROM skus", skuRowFixture(1)}, []any{"FROM barcodes", barcodeRowFixture()})
	env := newTestEnv(t, testUser(false, "ALL"), "masterdata:sku:list")
	useFixture(logAndRoute(env.log, route))
	defer useFixture(nil)

	// 空查询 / 单字符：直接空 groups，不执行任何 SQL。
	for _, q := range []string{"", "a", "%20a%20"} {
		out := env.get("/api/search?q=" + q)
		if !out.ok() {
			t.Fatalf("q=%q 应 200 空结果，实际 code=%s", q, out.Code)
		}
		if gs := env.groupsOf(out); gs == nil || len(gs) != 0 {
			t.Fatalf("q=%q groups 应为空切片，实际 %+v", q, gs)
		}
	}
	if got := env.log.count(); got != 0 {
		t.Fatalf("短路请求不应触达 SQL，实际 %d 条", got)
	}

	// q 超长（65 字符）：400 invalidParam，零 SQL。
	long := strings.Repeat("超", 65)
	env.mustErr("/api/search?q="+long, http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if got := env.log.count(); got != 0 {
		t.Fatalf("超长 q 不应触达 SQL，实际 %d 条", got)
	}

	// limit 缺省 5：尾参=LIMIT 5。
	env.get("/api/search?q=SKU001")
	if args := env.log.lastArgsOf("FROM skus"); args == nil || args[len(args)-1].Value != int64(5) {
		t.Fatalf("limit 缺省应为 5，实际 %v", args)
	}

	// limit=50 上限收口 20；limit=0 非法 400。
	env.get("/api/search?q=SKU001&limit=50")
	if args := env.log.lastArgsOf("FROM skus"); args == nil || args[len(args)-1].Value != int64(20) {
		t.Fatalf("limit 上限应收口 20，实际 %v", args)
	}
	env.mustErr("/api/search?q=SKU001&limit=0", http.StatusBadRequest, "COMMON_INVALID_PARAM")

	// types 白名单外 400。
	env.mustErr("/api/search?q=SKU001&types=bogus", http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- navigation.kind 注册表（doc 七分支权威区分，ask 硬性要求） ----

// TestNavigationKinds navKind 注册表冻结：普通 type 回落 typ，唯 logistics 显式覆盖为
// 实体名 shipment（"logistics" 是检索分组标签而非实体）；doc 七分支逐一持有独立实体名
// （purchase_order/inbound_order/sales_order/outbound_order/transfer_order/count_order/
// exception）——前端 searchTargets.ts 据此映射路由。
func TestNavigationKinds(t *testing.T) {
	// 非 doc type 的显式覆盖白名单（其余必须回落 typ）。
	overrides := map[string]string{"logistics": "shipment"}
	wantDoc := map[string]bool{
		"purchase_order": false, "inbound_order": false, "sales_order": false,
		"outbound_order": false, "transfer_order": false, "count_order": false,
		"exception": false,
	}
	docUnits := 0
	for _, u := range units {
		k := u.navKindOrType()
		if k == "" {
			t.Fatalf("unit %s(%s) navigation.kind 为空", u.typ, u.branch)
		}
		if u.typ != "doc" {
			if want, ok := overrides[u.typ]; ok {
				if u.navKind != want {
					t.Fatalf("type %s 显式 navKind 应为 %q，实际 %q", u.typ, want, u.navKind)
				}
			} else if u.navKind != "" {
				t.Fatalf("普通 type %s 不应显式声明 navKind（回落 typ）", u.typ)
			}
			if k != u.typ && k != overrides[u.typ] {
				t.Fatalf("type %s 的 navigation.kind 应为 %s，实际 %s", u.typ, u.typ, k)
			}
			continue
		}
		docUnits++
		if _, ok := wantDoc[k]; !ok {
			t.Fatalf("doc 分支 %s 的 kind %q 不在冻结清单: %v", u.branch, k, wantDoc)
		}
		wantDoc[k] = true
	}
	if docUnits != 7 {
		t.Fatalf("doc 应恰 7 分支，实际 %d", docUnits)
	}
	for k, seen := range wantDoc {
		if !seen {
			t.Fatalf("doc 分支缺实体 kind %q", k)
		}
	}
}

// ---- 路由冻结（T14 口径） ----

// TestSearchRoutesFrozen 端点集冻结：RegisterRoutes 恰注册 GET /api/search 一条
// （漏注册/多注册即测试红；挂载建议见 RegisterRoutes 头注——router protected 组）。
func TestSearchRoutesFrozen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	r := gin.New()
	RegisterRoutes(r.Group("/api"), db, nil)
	got := make([]string, 0)
	for _, rt := range r.Routes() {
		if strings.HasPrefix(rt.Path, "/api/search") {
			got = append(got, rt.Method+" "+rt.Path)
		}
	}
	if len(got) != 1 || got[0] != "GET /api/search" {
		t.Fatalf("search 包应恰注册 GET /api/search 一条路由，实际 %v", got)
	}
}
