package returns

// HTTP 层数据驱动表测试（ask 约束：单测零 PostgreSQL/Redis 依赖）：
//   - 引擎经 registerTestRoutes 逐条镜像 RegisterRoutes（handler.go:114-150）的路由与
//     auth.RequirePermission 权限中间件，handler 直接装配 fakes_test.go 内存替身背书的
//     Service（RegisterRoutes 固定装配 NewGormRepository，假驱动无法承载真 SQL 扫描，
//     故仅替换 service 来源；路由面一致性由 TestHTTPRouteSurfaceMirrorsRegisterRoutes
//     对 RegisterRoutes 的 (方法,路径) 全集守卫——镜像漂移即测试失败）；
//   - 认证上下文以中间件注入 auth.UserContext（键为 auth 中间件私有键 "sf_auth_user"，
//     internal/auth/middleware.go:26；auth 侧漂移则 CurrentUser 落空 → 全部 401，
//     本文件测试响亮失败而非静默误报）；
//   - 覆盖参数绑定、校验失败、统一信封与 HTTP 状态映射、数据权限（permission.md §4）、
//     成功路径与可测错误分支；service 层深行为（数量守恒/幂等重放/状态机全矩阵/追溯链
//     完整性）已由 service_test.go 覆盖，此处只补 HTTP 面。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/stock"
)

// ---- HTTP 基建 ----

// authUserCtxKey auth 中间件的 gin 用户上下文键（internal/auth/middleware.go:26，
// 未导出——同 module 测试以同值注入；漂移时 CurrentUser 落空 → 全部 401 响亮失败）。
const authUserCtxKey = "sf_auth_user"

func userInject(uc auth.UserContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(authUserCtxKey, uc)
		c.Next()
	}
}

// fakeImageFiles ImageFileChecker 内存替身（异常图片挂接两段式的文件中心侧）。
type fakeImageFiles struct{ valid map[int64]bool }

func (f fakeImageFiles) IsImageFile(_ context.Context, id int64) (bool, error) {
	return f.valid[id], nil
}

// superUser 超级管理员（RequirePermission 直通，permission.md §1）。
func superUser() auth.UserContext {
	return auth.UserContext{UserID: 1, Username: "张三", IsSuper: true}
}

// whScopeUser 指定仓库数据权限用户（permission.md §4 数据范围快照）。
func whScopeUser(ids ...int64) auth.UserContext {
	return auth.UserContext{UserID: 2, Username: "李四", DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids}
}

// registerTestRoutes 逐条镜像 RegisterRoutes 的路由表（handler.go:114-150），
// handler 换为替身 service 装配；一致性由 TestHTTPRouteSurfaceMirrorsRegisterRoutes 守卫。
func registerTestRoutes(api *gin.RouterGroup, h *handler) {
	registerTestRoutesOpts(api, h, true)
}

// registerTestRoutesOpts 同一路由表；withPerm=false 的变体专用于数据权限用例：
// 非超管用户的 RequirePermission 授权判定需 auth 包全局装配（internal/auth/middleware.go:127
// snapshotWired → PermissionCodes），单测环境（无 PG/Redis）不可达，故数据范围过滤
// （handler returnScope / getReturn 越界 / TraceScope）以"仅注入认证上下文"的引擎验证；
// 权限中间件本身的挂载已由 TestAllEndpointsRequireAuthentication 的 401 路径全覆盖。
func registerTestRoutesOpts(api *gin.RouterGroup, h *handler, withPerm bool) {
	perm := func(p string, hd gin.HandlerFunc) []gin.HandlerFunc {
		if !withPerm {
			return []gin.HandlerFunc{hd}
		}
		return []gin.HandlerFunc{auth.RequirePermission(p), hd}
	}
	ret := api.Group("/returns")
	ret.GET("", perm(PermSalesReturnList, h.listSalesReturns)...)
	ret.POST("", perm(PermSalesReturnCreate, h.createSalesReturn)...)
	ret.GET("/:id", perm(PermSalesReturnRead, h.getReturn)...)
	ret.POST("/:id/submit", perm(PermSalesReturnSubmit, h.submitReturn)...)
	ret.POST("/:id/approve", perm(PermSalesReturnApprove, h.approveReturn)...)
	ret.POST("/:id/receive", perm(PermSalesReturnExecute, h.receiveSalesReturn)...)
	ret.POST("/:id/submit-qc", perm(PermSalesReturnExecute, h.submitSalesQC)...)
	ret.POST("/:id/quality", perm(PermSalesReturnExecute, h.applySalesQC)...)
	ret.POST("/:id/cancel", perm(PermSalesReturnCancel, h.cancelReturn)...)

	pr := api.Group("/purchase-returns")
	pr.GET("", perm(PermPurchaseReturnList, h.listPurchaseReturns)...)
	pr.POST("", perm(PermPurchaseReturnCreate, h.createPurchaseReturn)...)
	pr.GET("/:id", perm(PermPurchaseReturnRead, h.getReturn)...)
	pr.POST("/:id/submit", perm(PermPurchaseReturnSubmit, h.submitReturn)...)
	pr.POST("/:id/approve", perm(PermPurchaseReturnApprove, h.approveReturn)...)
	pr.POST("/:id/ship", perm(PermPurchaseReturnExecute, h.shipPurchaseReturn)...)
	pr.POST("/:id/complete", perm(PermPurchaseReturnExecute, h.completePurchaseReturn)...)
	pr.POST("/:id/cancel", perm(PermPurchaseReturnCancel, h.cancelReturn)...)

	ex := api.Group("/exceptions")
	ex.GET("", perm(PermExceptionList, h.listExceptions)...)
	ex.POST("", perm(PermExceptionCreate, h.createException)...)
	ex.GET("/:id", perm(PermExceptionRead, h.getException)...)
	ex.POST("/:id/assign", perm(PermExceptionAssign, h.assignException)...)
	ex.POST("/:id/images", perm(PermExceptionExecute, h.attachExceptionImages)...)
	ex.POST("/:id/start", perm(PermExceptionExecute, h.startException)...)
	ex.POST("/:id/review", perm(PermExceptionExecute, h.reviewException)...)
	ex.POST("/:id/resolve", perm(PermExceptionExecute, h.resolveException)...)
	ex.POST("/:id/close", perm(PermExceptionClose, h.closeException)...)

	api.GET("/inventory/trace", perm(PermTraceList, h.trace)...)
}

// newReturnsEngine 装配镜像路由引擎（含权限中间件；uc 非 nil 时注入认证上下文中间件）。
func newReturnsEngine(t *testing.T, svc *Service, uc *auth.UserContext) *gin.Engine {
	t.Helper()
	return newEngineOpt(t, svc, uc, true)
}

// newScopeEngine 数据权限用例引擎（无权限中间件，仅注入认证上下文——见
// registerTestRoutesOpts 的 withPerm 说明）。
func newScopeEngine(t *testing.T, svc *Service, uc *auth.UserContext) *gin.Engine {
	t.Helper()
	return newEngineOpt(t, svc, uc, false)
}

func newEngineOpt(t *testing.T, svc *Service, uc *auth.UserContext, withPerm bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	if uc != nil {
		api.Use(userInject(*uc))
	}
	registerTestRoutesOpts(api, &handler{svc: svc}, withPerm)
	return r
}

type httpEnv struct {
	*testEnv
	img    fakeImageFiles
	engine *gin.Engine
}

// newHTTPEnv 组装带图片校验器与认证上下文的引擎（超级管理员直通）。
func newHTTPEnv(t *testing.T, uc auth.UserContext) *httpEnv {
	t.Helper()
	env := newTestEnv(t)
	img := fakeImageFiles{valid: map[int64]bool{}}
	svc := NewService(env.repo, WithStock(env.stock), WithSKUFlags(env.flags),
		WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase), WithQCCreator(env.qc),
		WithLedgers(env.ledgers), WithStockState(env.state), WithImageFileChecker(img))
	return &httpEnv{
		testEnv: env,
		img:     img,
		engine:  newReturnsEngine(t, svc, &uc),
	}
}

// ---- 请求与信封助手 ----

func doReq(t *testing.T, r *gin.Engine, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// doRaw 原始请求体（坏 JSON 绑定失败用例）。
func doRaw(t *testing.T, r *gin.Engine, method, path, rawBody string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func qPath(base string, kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	if enc := v.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

type httpEnvelope struct {
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Details json.RawMessage `json:"details"`
}

func decodeEnv(t *testing.T, w *httptest.ResponseRecorder) httpEnvelope {
	t.Helper()
	var e httpEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应非统一信封 JSON（api.md §2）: %v（%s）", err, w.Body.String())
	}
	return e
}

// wantOK 断言 200 + code=0（数字）并返回 data 对象。
func wantOK(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", w.Code, w.Body.String())
	}
	e := decodeEnv(t, w)
	if n, ok := e.Code.(float64); !ok || n != 0 {
		t.Fatalf("期望 code=0（数字），得到 %v: %s", e.Code, w.Body.String())
	}
	if e.Message != "ok" {
		t.Fatalf("成功信封 message 期望 ok，得到 %q", e.Message)
	}
	var m map[string]any
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &m); err != nil {
			t.Fatalf("data 非对象: %v（%s）", err, string(e.Data))
		}
	}
	return m
}

// wantErr 断言 HTTP 状态与错误码字符串，返回 details（可 nil）。
func wantErr(t *testing.T, w *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("期望 %d，得到 %d: %s", status, w.Code, w.Body.String())
	}
	e := decodeEnv(t, w)
	if s, ok := e.Code.(string); !ok || s != code {
		t.Fatalf("期望错误码 %s，得到 %v: %s", code, e.Code, w.Body.String())
	}
	if len(e.Details) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(e.Details, &m)
	return m
}

func itemsOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	arr, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items 缺失或非数组: %v", m)
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		out = append(out, it.(map[string]any))
	}
	return out
}

func num(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("期望数字字段，得到 %T（%v）", v, v)
	}
	return f
}

func str(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("期望字符串字段，得到 %T（%v）", v, v)
	}
	return s
}

// ---- 种子助手（复用 service_test.go 的 seedSalesReturn/actorA/qtyText/mustQty/ctx）----

// seedPurchaseReturnForHTTP 经 service 预置一张 DRAFT 采购退货单。
func seedPurchaseReturnForHTTP(t *testing.T, env *testEnv, poNo string, wh, sku, qty int64) *ReturnOrderView {
	t.Helper()
	env.purchase.seed(poNo, wh, ReturnableLine{LineNo: 1, SKUID: sku, Qty: qty})
	view, err := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: poNo, SupplierID: 9, WarehouseID: wh,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: sku, QtyReturn: qtyText(qty), Reason: "来料不良"}},
	})
	if err != nil {
		t.Fatalf("预置采购退货失败: %v", err)
	}
	return view
}

// approvePurchaseForHTTP 预置提交+审批（APPROVED）。
func approvePurchaseForHTTP(t *testing.T, env *testEnv, id int64) {
	t.Helper()
	if _, err := env.svc.SubmitReturn(ctx, actorA(), id); err != nil {
		t.Fatalf("预置提交失败: %v", err)
	}
	if _, err := env.svc.ApproveReturn(ctx, actorB(), id, ApproveInput{Approved: true}); err != nil {
		t.Fatalf("预置审批失败: %v", err)
	}
}

// seedStockForHTTP 预置库存行（fakeStock 六状态模拟器）。
func seedStockForHTTP(env *testEnv, wh, zone, shelf, bin, sku, qty int64) {
	_, _ = env.stock.Putaway(ctx, nil, stock.PutawayOp{
		Key: stock.RowKey{WarehouseID: wh, ZoneID: zone, ShelfID: shelf, BinID: bin, SKUID: sku},
		Qty: mustQty(qty), Source: stock.Source{Type: "seed", No: "seed"},
	})
}

// seedExceptionID 经 service 预置异常单并按来源单号取回 ID。
func seedExceptionID(t *testing.T, env *testEnv, excType, sourceNo string) int64 {
	t.Helper()
	_, err := env.svc.CreateException(ctx, nil, CreateExceptionOp{
		Type: excType, SourceType: "manual", SourceNo: sourceNo, Detail: "测试异常", Actor: actorA(),
	})
	if err != nil {
		t.Fatalf("预置异常单失败: %v", err)
	}
	exs, _, err := env.svc.ListExceptions(ctx, ExceptionFilter{SourceNo: sourceNo, Page: 1, PageSize: 10})
	if err != nil || len(exs) != 1 {
		t.Fatalf("预置异常单查询失败: err=%v n=%d", err, len(exs))
	}
	return exs[0].IDInt.Int64()
}

// ---- 路由面与权限中间件 ----

// TestHTTPRouteSurfaceMirrorsRegisterRoutes 镜像路由表与真实 RegisterRoutes 的
// (方法,路径) 全集一致（27 条）——registerTestRoutes 漂移即失败。
func TestHTTPRouteSurfaceMirrorsRegisterRoutes(t *testing.T) {
	env := newTestEnv(t)
	gin.SetMode(gin.TestMode)
	real := gin.New()
	RegisterRoutes(real.Group("/api"), env.repo.DB(), nil,
		WithStock(env.stock), WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase),
		WithQCCreator(env.qc), WithLedgers(env.ledgers), WithStockState(env.state))
	mirrored := gin.New()
	registerTestRoutes(mirrored.Group("/api"), &handler{svc: env.svc})

	set := func(r *gin.Engine) map[string]bool {
		m := map[string]bool{}
		for _, ri := range r.Routes() {
			m[ri.Method+" "+ri.Path] = true
		}
		return m
	}
	want, got := set(real), set(mirrored)
	if len(want) != 27 {
		t.Fatalf("RegisterRoutes 路由数=%d，期望 27（9 销退 + 8 采退 + 9 异常 + 1 追溯）", len(want))
	}
	for p := range want {
		if !got[p] {
			t.Fatalf("镜像路由缺失: %s", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Fatalf("镜像路由多余: %s", p)
		}
	}
}

// TestAllEndpointsRequireAuthentication 27 条路由全部挂 auth.RequirePermission：
// 无认证上下文 → 401 COMMON_UNAUTHORIZED（permission.md §2）。
func TestAllEndpointsRequireAuthentication(t *testing.T) {
	env := newTestEnv(t)
	r := newReturnsEngine(t, env.svc, nil)
	probes := []struct{ method, path string }{
		{http.MethodGet, "/api/returns"},
		{http.MethodPost, "/api/returns"},
		{http.MethodGet, "/api/returns/1"},
		{http.MethodPost, "/api/returns/1/submit"},
		{http.MethodPost, "/api/returns/1/approve"},
		{http.MethodPost, "/api/returns/1/receive"},
		{http.MethodPost, "/api/returns/1/submit-qc"},
		{http.MethodPost, "/api/returns/1/quality"},
		{http.MethodPost, "/api/returns/1/cancel"},
		{http.MethodGet, "/api/purchase-returns"},
		{http.MethodPost, "/api/purchase-returns"},
		{http.MethodGet, "/api/purchase-returns/1"},
		{http.MethodPost, "/api/purchase-returns/1/submit"},
		{http.MethodPost, "/api/purchase-returns/1/approve"},
		{http.MethodPost, "/api/purchase-returns/1/ship"},
		{http.MethodPost, "/api/purchase-returns/1/complete"},
		{http.MethodPost, "/api/purchase-returns/1/cancel"},
		{http.MethodGet, "/api/exceptions"},
		{http.MethodPost, "/api/exceptions"},
		{http.MethodGet, "/api/exceptions/1"},
		{http.MethodPost, "/api/exceptions/1/assign"},
		{http.MethodPost, "/api/exceptions/1/images"},
		{http.MethodPost, "/api/exceptions/1/start"},
		{http.MethodPost, "/api/exceptions/1/review"},
		{http.MethodPost, "/api/exceptions/1/resolve"},
		{http.MethodPost, "/api/exceptions/1/close"},
		{http.MethodGet, "/api/inventory/trace"},
	}
	for _, p := range probes {
		w := doReq(t, r, p.method, p.path, nil, nil)
		wantErr(t, w, http.StatusUnauthorized, "COMMON_UNAUTHORIZED")
	}
}

// ---- 销售退货 ----

// TestSalesReturnListHTTP GET /api/returns：类型隔离、筛选、分页、参数校验、数据权限。
func TestSalesReturnListHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	seedSalesReturn(t, env.testEnv, "SO-HTTP-L1", 1, 11, 5) // APPROVED
	env.sales.seed("SO-HTTP-L2", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 3})
	if _, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-HTTP-L2", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: qtyText(3), Reason: "x"}},
	}); err != nil {
		t.Fatalf("预置 DRAFT 退货单失败: %v", err)
	}
	seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-L1", 1, 33, 2) // 干扰项：采购退货

	cases := []struct {
		name       string
		kv         []string
		wantTotal  int
		wantStatus string
	}{
		{"全量（不混入采购退货）", nil, 2, ""},
		{"状态过滤", []string{"status", "APPROVED"}, 1, "APPROVED"},
		{"来源单过滤", []string{"source_no", "SO-HTTP-L2"}, 1, "DRAFT"},
		{"仓库过滤", []string{"warehouse_id", "1"}, 2, ""},
		{"无匹配", []string{"status", "COMPLETED"}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wantOK(t, doReq(t, env.engine, http.MethodGet, qPath("/api/returns", tc.kv...), nil, nil))
			if got := int(num(t, m["total"])); got != tc.wantTotal {
				t.Fatalf("total=%d，期望 %d", got, tc.wantTotal)
			}
			if int(num(t, m["page"])) != 1 || int(num(t, m["pageSize"])) != 20 {
				t.Fatalf("分页回显不符: %v/%v（api.md §2.1）", m["page"], m["pageSize"])
			}
			for _, it := range itemsOf(t, m) {
				if got := str(t, it["type"]); got != "SALES" {
					t.Fatalf("列表混入非销售退货: %s", got)
				}
				if tc.wantStatus != "" && str(t, it["status"]) != tc.wantStatus {
					t.Fatalf("状态过滤失效: %s", it["status"])
				}
			}
		})
	}

	// 参数校验（api.md §4：校验失败 400 + details）。
	d := wantErr(t, doReq(t, env.engine, http.MethodGet, qPath("/api/returns", "warehouse_id", "abc"), nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["field"]) != "warehouse_id" {
		t.Fatalf("details.field=%v，期望 warehouse_id", d["field"])
	}
	if d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/returns?page=0", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "page" {
		t.Fatalf("details.field=%v，期望 page", d["field"])
	}
	if d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/returns?pageSize=1000", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "pageSize" {
		t.Fatalf("details.field=%v，期望 pageSize", d["field"])
	}

	// 数据权限：指定仓库范围用户只见绑定仓数据（permission.md §4）。
	env.sales.seed("SO-HTTP-L3", 2, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 1})
	if _, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-HTTP-L3", CustomerID: 7, WarehouseID: 2,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: qtyText(1), Reason: "x"}},
	}); err != nil {
		t.Fatalf("预置仓 2 退货单失败: %v", err)
	}
	uc := whScopeUser(1)
	scoped := newScopeEngine(t, env.svc, &uc)
	m := wantOK(t, doReq(t, scoped, http.MethodGet, "/api/returns", nil, nil))
	if got := int(num(t, m["total"])); got != 2 {
		t.Fatalf("范围外数据泄漏：total=%d，期望 2（仅仓 1 的两张）", got)
	}
	for _, it := range itemsOf(t, m) {
		if got := num(t, it["warehouse_id"]); got != 1 {
			t.Fatalf("越界仓库可见: %v", got)
		}
	}
}

// TestSalesReturnCreateHTTP POST /api/returns：绑定失败、校验失败表、成功路径。
func TestSalesReturnCreateHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	env.sales.seed("SO-HTTP-C1", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 3})

	// 成功路径：真实业务数据落单。
	m := wantOK(t, doReq(t, env.engine, http.MethodPost, "/api/returns", SalesReturnCreateInput{
		SONo: "SO-HTTP-C1", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "2.0000", Reason: "质量问题"}},
	}, nil))
	if no := str(t, m["return_no"]); !strings.HasPrefix(no, "RT-") {
		t.Fatalf("单号前缀不符（business-flow §13.1）: %s", no)
	}
	if str(t, m["type"]) != "SALES" || str(t, m["status"]) != "DRAFT" {
		t.Fatalf("类型/状态不符: %v/%v", m["type"], m["status"])
	}
	if num(t, m["customer_id"]) != 7 || num(t, m["warehouse_id"]) != 1 {
		t.Fatalf("客户/仓库回显不符: %v/%v", m["customer_id"], m["warehouse_id"])
	}
	if items := itemsOf(t, m); len(items) != 1 || str(t, items[0]["qty_return"]) != "2.0000" {
		t.Fatalf("明细回显不符: %v", m["items"])
	}

	// 绑定失败：坏 JSON / 空请求体 → 400 + 固定文案（BindErrorReason）。
	d := wantErr(t, doRaw(t, env.engine, http.MethodPost, "/api/returns", "{bad"), http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["reason"]) != "请求体格式错误" {
		t.Fatalf("绑定失败 details.reason=%v，期望固定文案", d["reason"])
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns", nil, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM")

	// 校验失败表（handler → service 校验链，api.md §4 带 details.field）。
	env.sales.seed("SO-HTTP-C2", 9, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 5})
	cases := []struct {
		name   string
		in     SalesReturnCreateInput
		code   string
		status int
		field  string
	}{
		{"缺来源单号", SalesReturnCreateInput{WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}}}, "COMMON_INVALID_PARAM", 400, "source_no"},
		{"缺仓库", SalesReturnCreateInput{SONo: "SO-HTTP-C1", Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}}}, "COMMON_INVALID_PARAM", 400, "warehouse_id"},
		{"明细为空", SalesReturnCreateInput{SONo: "SO-HTTP-C1", WarehouseID: 1}, "COMMON_INVALID_PARAM", 400, "lines"},
		{"数量为零", SalesReturnCreateInput{SONo: "SO-HTTP-C1", WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "0", Reason: "x"}}}, "COMMON_INVALID_PARAM", 400, "qty_return"},
		{"缺原因", SalesReturnCreateInput{SONo: "SO-HTTP-C1", WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000"}}}, "COMMON_INVALID_PARAM", 400, "reason"},
		{"行号重复", SalesReturnCreateInput{SONo: "SO-HTTP-C1", WarehouseID: 1, Lines: []SalesReturnLineInput{
			{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}, {LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"},
		}}, "COMMON_INVALID_PARAM", 400, "line_no"},
		{"来源单不存在", SalesReturnCreateInput{SONo: "SO-NOPE", CustomerID: 7, WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_SOURCE_ORDER_NOT_FOUND", 400, ""},
		{"仓库与来源单不一致", SalesReturnCreateInput{SONo: "SO-HTTP-C2", CustomerID: 7, WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_SOURCE_MISMATCH", 400, ""},
		{"行不存在", SalesReturnCreateInput{SONo: "SO-HTTP-C1", CustomerID: 7, WarehouseID: 1, Lines: []SalesReturnLineInput{{LineNo: 9, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_LINE_NOT_FOUND", 404, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns", tc.in, nil), tc.status, tc.code)
			if tc.field != "" && str(t, d["field"]) != tc.field {
				t.Fatalf("details.field=%v，期望 %s", d["field"], tc.field)
			}
		})
	}

	// 累计防超退（plan §3.1：退量 ≤ 已发货量 − 已退量）→ 409。
	env.sales.seed("SO-HTTP-C3", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 2})
	if _, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-HTTP-C3", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "2.0000", Reason: "x"}},
	}); err != nil {
		t.Fatalf("预置首张退货单失败: %v", err)
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns", SalesReturnCreateInput{
		SONo: "SO-HTTP-C3", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "1.0000", Reason: "x"}},
	}, nil), http.StatusConflict, "RETURNS_QTY_EXCEEDED")
}

// TestSalesReturnDetailHTTP GET /api/returns/:id：成功含明细、404、非法 id、越界 fail-closed。
func TestSalesReturnDetailHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order, _ := seedSalesReturn(t, env.testEnv, "SO-HTTP-D1", 1, 11, 5)

	m := wantOK(t, doReq(t, env.engine, http.MethodGet, fmt.Sprintf("/api/returns/%d", order.ID.Int64()), nil, nil))
	if str(t, m["return_no"]) != order.ReturnNo || str(t, m["status"]) != "APPROVED" {
		t.Fatalf("详情回显不符: %v/%v", m["return_no"], m["status"])
	}
	if str(t, m["source_no"]) != "SO-HTTP-D1" {
		t.Fatalf("来源单回显不符: %v", m["source_no"])
	}
	if items := itemsOf(t, m); len(items) != 1 || str(t, items[0]["qty_return"]) != "5.0000" {
		t.Fatalf("明细不符: %v", m["items"])
	}

	wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/returns/999", nil, nil), http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
	for _, raw := range []string{"abc", "0", "-1"} {
		d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/returns/"+raw, nil, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		if str(t, d["field"]) != "id" {
			t.Fatalf("id=%s details.field=%v，期望 id", raw, d["field"])
		}
	}

	// 数据权限：越界按不存在处理（fail-closed，handler.go getReturn）。
	uc := whScopeUser(2)
	scoped := newScopeEngine(t, env.svc, &uc)
	wantErr(t, doReq(t, scoped, http.MethodGet, fmt.Sprintf("/api/returns/%d", order.ID.Int64()), nil, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestSalesReturnSubmitHTTP POST /api/returns/:id/submit：成功/重复/404/非法 id。
func TestSalesReturnSubmitHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	env.sales.seed("SO-HTTP-S1", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 4})
	order, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-HTTP-S1", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "4.0000", Reason: "x"}},
	})
	if err != nil {
		t.Fatalf("预置失败: %v", err)
	}
	id := order.ID.Int64()

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/submit", id), nil, nil))
	if str(t, m["status"]) != "PENDING_APPROVAL" {
		t.Fatalf("提交后状态=%v，期望 PENDING_APPROVAL", m["status"])
	}
	if len(env.repo.approvals) != 1 || env.repo.approvals[0].Action != "SUBMIT" {
		t.Fatalf("审批记录缺失: %+v（business-flow §12.2）", env.repo.approvals)
	}
	found := false
	for _, a := range env.spy.actions() {
		if a == "submit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("审计缺 submit 动作: %v", env.spy.actions())
	}

	// 重复提交 → 状态冲突 409。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/submit", id), nil, nil),
		http.StatusConflict, "RETURNS_STATUS_CONFLICT")
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/999/submit", nil, nil), http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/abc/submit", nil, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// TestSalesReturnApproveHTTP POST /api/returns/:id/approve：通过/驳回、缺体 400、重复 409。
func TestSalesReturnApproveHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	mk := func(soNo string) int64 {
		t.Helper()
		env.sales.seed(soNo, 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 2})
		o, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
			SONo: soNo, CustomerID: 7, WarehouseID: 1,
			Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "2.0000", Reason: "x"}},
		})
		if err != nil {
			t.Fatalf("预置失败: %v", err)
		}
		if _, err := env.svc.SubmitReturn(ctx, actorA(), o.ID.Int64()); err != nil {
			t.Fatalf("预置提交失败: %v", err)
		}
		return o.ID.Int64()
	}

	// 审批通过。
	idOK := mk("SO-HTTP-A1")
	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/approve", idOK),
		ApproveInput{Approved: true, Opinion: "同意退货"}, nil))
	if str(t, m["status"]) != "APPROVED" {
		t.Fatalf("审批后状态=%v，期望 APPROVED", m["status"])
	}
	// 重复审批 → 409。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/approve", idOK),
		ApproveInput{Approved: true}, nil), http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	// 审批驳回 → DRAFT。
	idRej := mk("SO-HTTP-A2")
	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/approve", idRej),
		ApproveInput{Approved: false, Opinion: "凭证不全"}, nil))
	if str(t, m["status"]) != "DRAFT" {
		t.Fatalf("驳回后状态=%v，期望 DRAFT", m["status"])
	}

	// 缺请求体 → 400（approved 必须显式给出）。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/approve", mk("SO-HTTP-A3")), nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	// 404。
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/999/approve", ApproveInput{Approved: true}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestSalesReturnReceiveHTTP POST /api/returns/:id/receive：成功入待检、Idempotency-Key
// 头透传幂等重放（handler.go:307）、缺明细 400、404。
func TestSalesReturnReceiveHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order, _ := seedSalesReturn(t, env.testEnv, "SO-HTTP-R1", 1, 11, 10)
	id := order.ID.Int64()
	rcv := ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: "4.0000"}}}

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", id), rcv,
		map[string]string{"Idempotency-Key": "HTTP-RCV-1"}))
	if str(t, m["status"]) != "RECEIVING" {
		t.Fatalf("首次收货后状态=%v，期望 RECEIVING", m["status"])
	}
	row := env.stock.stateOf(t, 1, 2, 3, 101, 11, 0)
	if row.pending != mustQty(4) || row.total != mustQty(4) {
		t.Fatalf("收货后待检库存不符: %+v", row)
	}
	if items := itemsOf(t, m); str(t, items[0]["qty_received"]) != "4.0000" {
		t.Fatalf("已收量回显不符: %v", items[0]["qty_received"])
	}

	// 同头同体重试：幂等重放不重复累计（plan §8.5，事件型收货头优先）。
	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", id), rcv,
		map[string]string{"Idempotency-Key": "HTTP-RCV-1"}))
	if items := itemsOf(t, m); str(t, items[0]["qty_received"]) != "4.0000" {
		t.Fatalf("幂等重放后已收量被重复累计: %v", items[0]["qty_received"])
	}

	// 余量收货（无头，服务端确定性键）。
	wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", id),
		ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: "6.0000"}}}, nil))
	if row := env.stock.stateOf(t, 1, 2, 3, 101, 11, 0); row.pending != mustQty(10) {
		t.Fatalf("二次收货后待检库存=%s，期望 10", row.pending)
	}

	// 缺明细 / 404。
	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", id),
		ReceiveInput{}, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "lines" {
		t.Fatalf("details.field=%v，期望 lines", d["field"])
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/999/receive",
		ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: "1.0000"}}}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestSalesReturnQCHTTP POST /api/returns/:id/submit-qc 与 /quality：HTTP 面成功路径、
// 未收齐 409、缺参 400。
func TestSalesReturnQCHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order, _ := seedSalesReturn(t, env.testEnv, "SO-HTTP-Q1", 1, 11, 10)
	id := order.ID.Int64()
	rcv := func(qty string) {
		t.Helper()
		wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", id),
			ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: qty}}}, nil))
	}

	// 未收齐提交质检 → 409（business-flow §9.1 收货→质检顺序）。
	rcv("4.0000")
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/submit-qc", id), SubmitQCInput{}, nil),
		http.StatusConflict, "RETURNS_QTY_EXCEEDED")

	// 收齐 → 提交质检成功，返回质检单号。
	rcv("6.0000")
	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/submit-qc", id),
		SubmitQCInput{Remark: "全检"}, nil))
	if no := str(t, m["qc_no"]); !strings.HasPrefix(no, "QC-TEST-") {
		t.Fatalf("质检单号不符: %v", m["qc_no"])
	}
	if str(t, m["order"].(map[string]any)["status"]) != "IN_QC" {
		t.Fatalf("提交质检后状态=%v，期望 IN_QC", m["order"])
	}

	// 质检结果：7 合格 → available，3 不良 → defective（§9.1 质检决定去向）。
	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/quality", id), QCResultInput{
		QCNo:  str(t, m["qc_no"]),
		Lines: []QCResultLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, QtyQualified: "7.0000", QtyDefective: "3.0000"}},
	}, nil))
	if str(t, m["status"]) != "COMPLETED" {
		t.Fatalf("全部检完状态=%v，期望 COMPLETED", m["status"])
	}
	row := env.stock.stateOf(t, 1, 2, 3, 101, 11, 0)
	if row.avail != mustQty(7) || row.defect != mustQty(3) || row.pending != 0 {
		t.Fatalf("质检后库存去向不符: %+v", row)
	}

	// 缺参：qc_no / lines。
	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/quality", id),
		QCResultInput{Lines: []QCResultLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, QtyQualified: "1.0000"}}}, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "qc_no" {
		t.Fatalf("details.field=%v，期望 qc_no", d["field"])
	}
	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/quality", id),
		QCResultInput{QCNo: "QC-TEST-x"}, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "lines" {
		t.Fatalf("details.field=%v，期望 lines", d["field"])
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/999/submit-qc", SubmitQCInput{}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestSalesReturnCancelHTTP POST /api/returns/:id/cancel：DRAFT 取消成功（审批记录+审计）、
// 已收货 409、404。
func TestSalesReturnCancelHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	env.sales.seed("SO-HTTP-X1", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 2})
	order, err := env.svc.CreateSalesReturn(ctx, actorA(), SalesReturnCreateInput{
		SONo: "SO-HTTP-X1", CustomerID: 7, WarehouseID: 1,
		Lines: []SalesReturnLineInput{{LineNo: 1, SKUID: 11, QtyReturn: "2.0000", Reason: "x"}},
	})
	if err != nil {
		t.Fatalf("预置失败: %v", err)
	}
	id := order.ID.Int64()

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/cancel", id),
		CancelInput{Reason: "客户撤销"}, nil))
	if str(t, m["status"]) != "CANCELLED" {
		t.Fatalf("取消后状态=%v，期望 CANCELLED", m["status"])
	}
	if got, _ := env.repo.FindReturnOrder(ctx, id); got.CancelledAt.IsZero() {
		t.Fatal("取消时间列未落（business-flow §13.4）")
	}
	foundCancel, foundAudit := false, false
	for _, a := range env.repo.approvals {
		if a.Action == "CANCEL" {
			foundCancel = true
		}
	}
	for _, a := range env.spy.actions() {
		if a == "cancel" {
			foundAudit = true
		}
	}
	if !foundCancel || !foundAudit {
		t.Fatalf("审批记录/审计缺失: approvals=%v audit=%v", env.repo.approvals, env.spy.actions())
	}

	// 已收货不可取消（business-flow §13.3：只能反向冲正）→ 409。
	order2, _ := seedSalesReturn(t, env.testEnv, "SO-HTTP-X2", 1, 11, 2)
	wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/receive", order2.ID.Int64()),
		ReceiveInput{Lines: []ReceiveLineInput{{LineNo: 1, ZoneID: 2, ShelfID: 3, BinID: 101, Qty: "2.0000"}}}, nil))
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/returns/%d/cancel", order2.ID.Int64()),
		CancelInput{Reason: "x"}, nil), http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/returns/999/cancel", CancelInput{}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// ---- 采购退货 ----

// TestPurchaseReturnListHTTP GET /api/purchase-returns：类型隔离、筛选、参数校验、数据权限。
func TestPurchaseReturnListHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-PL1", 1, 33, 5)
	seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-PL2", 2, 33, 3)
	seedSalesReturn(t, env.testEnv, "SO-HTTP-PL1", 1, 11, 2) // 干扰项：销售退货

	cases := []struct {
		name      string
		kv        []string
		wantTotal int
	}{
		{"全量（不混入销售退货）", nil, 2},
		{"状态过滤", []string{"status", "DRAFT"}, 2},
		{"来源单过滤", []string{"source_no", "PO-HTTP-PL1"}, 1},
		{"仓库过滤", []string{"warehouse_id", "2"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wantOK(t, doReq(t, env.engine, http.MethodGet, qPath("/api/purchase-returns", tc.kv...), nil, nil))
			if got := int(num(t, m["total"])); got != tc.wantTotal {
				t.Fatalf("total=%d，期望 %d", got, tc.wantTotal)
			}
			for _, it := range itemsOf(t, m) {
				if got := str(t, it["type"]); got != "PURCHASE" {
					t.Fatalf("列表混入非采购退货: %s", got)
				}
			}
		})
	}

	d := wantErr(t, doReq(t, env.engine, http.MethodGet, qPath("/api/purchase-returns", "warehouse_id", "abc"), nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["field"]) != "warehouse_id" {
		t.Fatalf("details.field=%v，期望 warehouse_id", d["field"])
	}

	// 数据权限：范围 [1] 只见仓 1。
	uc := whScopeUser(1)
	scoped := newScopeEngine(t, env.svc, &uc)
	m := wantOK(t, doReq(t, scoped, http.MethodGet, "/api/purchase-returns", nil, nil))
	if got := int(num(t, m["total"])); got != 1 {
		t.Fatalf("范围外数据泄漏：total=%d，期望 1", got)
	}
	if items := itemsOf(t, m); num(t, items[0]["warehouse_id"]) != 1 {
		t.Fatalf("越界仓库可见: %v", items[0]["warehouse_id"])
	}
}

// TestPurchaseReturnCreateHTTP POST /api/purchase-returns：成功、校验失败表、累计超退 409。
func TestPurchaseReturnCreateHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	env.purchase.seed("PO-HTTP-PC1", 1, ReturnableLine{LineNo: 1, SKUID: 33, Qty: 5})

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns", PurchaseReturnCreateInput{
		PONo: "PO-HTTP-PC1", SupplierID: 9, WarehouseID: 1,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "5.0000", Reason: "来料不良"}},
	}, nil))
	if no := str(t, m["return_no"]); !strings.HasPrefix(no, "RT-") {
		t.Fatalf("单号前缀不符: %s", no)
	}
	if str(t, m["type"]) != "PURCHASE" || str(t, m["status"]) != "DRAFT" {
		t.Fatalf("类型/状态不符: %v/%v", m["type"], m["status"])
	}
	if num(t, m["supplier_id"]) != 9 {
		t.Fatalf("供应商回显不符: %v", m["supplier_id"])
	}
	if items := itemsOf(t, m); len(items) != 1 || str(t, items[0]["qty_return"]) != "5.0000" {
		t.Fatalf("明细回显不符: %v", m["items"])
	}

	d := wantErr(t, doRaw(t, env.engine, http.MethodPost, "/api/purchase-returns", "{oops"),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["reason"]) != "请求体格式错误" {
		t.Fatalf("绑定失败 details.reason=%v", d["reason"])
	}

	env.purchase.seed("PO-HTTP-PC2", 9, ReturnableLine{LineNo: 1, SKUID: 33, Qty: 5})
	cases := []struct {
		name   string
		in     PurchaseReturnCreateInput
		code   string
		status int
		field  string
	}{
		{"缺供应商", PurchaseReturnCreateInput{PONo: "PO-HTTP-PC1", WarehouseID: 1, Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}}}, "COMMON_INVALID_PARAM", 400, "supplier_id"},
		{"缺来源单号", PurchaseReturnCreateInput{SupplierID: 9, WarehouseID: 1, Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}}}, "COMMON_INVALID_PARAM", 400, "source_no"},
		{"来源单不存在", PurchaseReturnCreateInput{PONo: "PO-NOPE", SupplierID: 9, WarehouseID: 1, Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_SOURCE_ORDER_NOT_FOUND", 400, ""},
		{"仓库与来源单不一致", PurchaseReturnCreateInput{PONo: "PO-HTTP-PC2", SupplierID: 9, WarehouseID: 1, Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_SOURCE_MISMATCH", 400, ""},
		{"行不存在", PurchaseReturnCreateInput{PONo: "PO-HTTP-PC1", SupplierID: 9, WarehouseID: 1, Lines: []PurchaseReturnLineInput{{LineNo: 7, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}}}, "RETURNS_LINE_NOT_FOUND", 404, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns", tc.in, nil), tc.status, tc.code)
			if tc.field != "" && str(t, d["field"]) != tc.field {
				t.Fatalf("details.field=%v，期望 %s", d["field"], tc.field)
			}
		})
	}

	// 累计防超退 → 409。
	env.purchase.seed("PO-HTTP-PC3", 1, ReturnableLine{LineNo: 1, SKUID: 33, Qty: 2})
	if _, err := env.svc.CreatePurchaseReturn(ctx, actorA(), PurchaseReturnCreateInput{
		PONo: "PO-HTTP-PC3", SupplierID: 9, WarehouseID: 1,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "2.0000", Reason: "x"}},
	}); err != nil {
		t.Fatalf("预置首张失败: %v", err)
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns", PurchaseReturnCreateInput{
		PONo: "PO-HTTP-PC3", SupplierID: 9, WarehouseID: 1,
		Lines: []PurchaseReturnLineInput{{LineNo: 1, SKUID: 33, QtyReturn: "1.0000", Reason: "x"}},
	}, nil), http.StatusConflict, "RETURNS_QTY_EXCEEDED")
}

// TestPurchaseReturnDetailHTTP GET /api/purchase-returns/:id：成功、404、非法 id、越界。
func TestPurchaseReturnDetailHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order := seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-PD1", 1, 33, 4)

	m := wantOK(t, doReq(t, env.engine, http.MethodGet, fmt.Sprintf("/api/purchase-returns/%d", order.ID.Int64()), nil, nil))
	if str(t, m["return_no"]) != order.ReturnNo || str(t, m["type"]) != "PURCHASE" {
		t.Fatalf("详情回显不符: %v/%v", m["return_no"], m["type"])
	}
	if items := itemsOf(t, m); len(items) != 1 || str(t, items[0]["qty_return"]) != "4.0000" {
		t.Fatalf("明细不符: %v", m["items"])
	}

	wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/purchase-returns/999", nil, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
	d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/purchase-returns/abc", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["field"]) != "id" {
		t.Fatalf("details.field=%v，期望 id", d["field"])
	}

	uc := whScopeUser(2)
	scoped := newScopeEngine(t, env.svc, &uc)
	wantErr(t, doReq(t, scoped, http.MethodGet, fmt.Sprintf("/api/purchase-returns/%d", order.ID.Int64()), nil, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestPurchaseReturnSubmitApproveHTTP 采退 submit/approve（与销退共用 handler，经采退路由走）。
func TestPurchaseReturnSubmitApproveHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order := seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-PS1", 1, 33, 3)
	id := order.ID.Int64()

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/submit", id), nil, nil))
	if str(t, m["status"]) != "PENDING_APPROVAL" {
		t.Fatalf("提交后状态=%v", m["status"])
	}
	if len(env.repo.approvals) != 1 || env.repo.approvals[0].Action != "SUBMIT" {
		t.Fatalf("审批记录缺失: %+v", env.repo.approvals)
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/submit", id), nil, nil),
		http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/approve", id),
		ApproveInput{Approved: true, Opinion: "同意"}, nil))
	if str(t, m["status"]) != "APPROVED" {
		t.Fatalf("审批后状态=%v", m["status"])
	}
	if got, _ := env.repo.FindReturnOrder(ctx, id); got.ApprovedAt.IsZero() || got.ApprovedBy != 1 {
		t.Fatalf("approved_at/approved_by 未落: %+v", got)
	}
	// 重复审批 → 409；驳回回草稿；404。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/approve", id),
		ApproveInput{Approved: true}, nil), http.StatusConflict, "RETURNS_STATUS_CONFLICT")
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns/999/approve", ApproveInput{Approved: true}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestPurchaseReturnShipCompleteHTTP 采退出库/完成（HTTP 面）：状态守卫、库存扣减、
// 出库后取消 409、完成闭环、对销退单 complete 400。
func TestPurchaseReturnShipCompleteHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	const wh, zone, shelf, bin, sku = 2, 2, 3, 301, 33
	seedStockForHTTP(env.testEnv, wh, zone, shelf, bin, sku, 6)
	order := seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-SH1", wh, sku, 6)
	id := order.ID.Int64()
	shipIn := ShipInput{Lines: []ShipLineInput{{LineNo: 1, ZoneID: zone, ShelfID: shelf, BinID: bin, Qty: "6.0000"}}}

	// 未审批直接出库 → 409。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/ship", id), shipIn, nil),
		http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	approvePurchaseForHTTP(t, env.testEnv, id)
	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/ship", id), shipIn, nil))
	if str(t, m["status"]) != "SHIPPED" {
		t.Fatalf("出库后状态=%v，期望 SHIPPED", m["status"])
	}
	if row := env.stock.stateOf(t, wh, zone, shelf, bin, sku, 0); row.total != 0 || row.locked != 0 {
		t.Fatalf("Lock→Deduct 后库存不符: %+v", row)
	}

	// 已出库不可取消（business-flow §13.3：只能冲正）→ 409。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/cancel", id),
		CancelInput{Reason: "x"}, nil), http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	// 完成确认 → COMPLETED；重复完成 → 409。
	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/complete", id), nil, nil))
	if str(t, m["status"]) != "COMPLETED" {
		t.Fatalf("完成后状态=%v，期望 COMPLETED", m["status"])
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/complete", id), nil, nil),
		http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	// 未出库（APPROVED）直接完成 → 409。
	order2 := seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-SH2", wh, sku, 1)
	approvePurchaseForHTTP(t, env.testEnv, order2.ID.Int64())
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/complete", order2.ID.Int64()), nil, nil),
		http.StatusConflict, "RETURNS_STATUS_CONFLICT")

	// 对销售退货单调用完成 → 400（仅采购退货有出库完成态）。
	so, _ := seedSalesReturn(t, env.testEnv, "SO-HTTP-SH1", 1, 11, 1)
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/complete", so.ID.Int64()), nil, nil),
		http.StatusBadRequest, "RETURNS_SOURCE_MISMATCH")

	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns/999/complete", nil, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns/999/ship", shipIn, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// TestPurchaseReturnCancelHTTP 采退取消：DRAFT 取消成功（审批记录 CANCEL）、404。
func TestPurchaseReturnCancelHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	order := seedPurchaseReturnForHTTP(t, env.testEnv, "PO-HTTP-CX1", 1, 33, 2)
	id := order.ID.Int64()

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/purchase-returns/%d/cancel", id),
		CancelInput{Reason: "计划有变"}, nil))
	if str(t, m["status"]) != "CANCELLED" {
		t.Fatalf("取消后状态=%v", m["status"])
	}
	found := false
	for _, a := range env.repo.approvals {
		if a.Action == "CANCEL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CANCEL 审批记录缺失: %+v", env.repo.approvals)
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/purchase-returns/999/cancel", CancelInput{}, nil),
		http.StatusNotFound, "RETURNS_RETURN_NOT_FOUND")
}

// ---- 异常中心 ----

// TestExceptionListHTTP GET /api/exceptions：列表+多维筛选+分页校验。
func TestExceptionListHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	seedExceptionID(t, env.testEnv, "库存异常", "CK-HTTP-1")
	seedExceptionID(t, env.testEnv, "拣货异常", "PK-HTTP-1")
	seedExceptionID(t, env.testEnv, "质检异常", "QC-HTTP-1")

	cases := []struct {
		name      string
		kv        []string
		wantTotal int
		wantType  string
	}{
		{"全量", nil, 3, ""},
		{"类型过滤", []string{"type", "拣货异常"}, 1, "拣货异常"},
		{"状态过滤", []string{"status", "OPEN"}, 3, ""},
		{"来源单过滤", []string{"source_no", "PK-HTTP-1"}, 1, "拣货异常"},
		{"来源类型过滤", []string{"source_type", "manual"}, 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wantOK(t, doReq(t, env.engine, http.MethodGet, qPath("/api/exceptions", tc.kv...), nil, nil))
			if got := int(num(t, m["total"])); got != tc.wantTotal {
				t.Fatalf("total=%d，期望 %d", got, tc.wantTotal)
			}
			for _, it := range itemsOf(t, m) {
				if tc.wantType != "" && str(t, it["type"]) != tc.wantType {
					t.Fatalf("筛选失效: %s", it["type"])
				}
				if str(t, it["status"]) != "OPEN" {
					t.Fatalf("预置异常状态应为 OPEN: %v", it["status"])
				}
			}
		})
	}

	if d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/exceptions?page=0", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "page" {
		t.Fatalf("details.field=%v，期望 page", d["field"])
	}
}

// TestExceptionCreateHTTP POST /api/exceptions：成功（无冻结/冻结联动库存）、非法类型、
// 冻结缺定位/缺量。
func TestExceptionCreateHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions", ExceptionCreateInput{
		Type: "收货异常", SourceType: "receipt", SourceNo: "RC-HTTP-1",
		Detail: "数量短缺", SKUID: 11, BinID: 101,
	}, nil))
	if no := str(t, m["exception_no"]); !strings.HasPrefix(no, "EX-") {
		t.Fatalf("异常单号前缀不符: %s", no)
	}

	// 冻结成功：EXCEPTION_FREEZE → frozen（plan §6.10 / inventory-rules §4.2）。
	seedStockForHTTP(env.testEnv, 3, 2, 3, 401, 55, 20)
	wantOK(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions", ExceptionCreateInput{
		Type: "库存异常", SourceType: "count_order", SourceNo: "CK-HTTP-2", Detail: "差异",
		SKUID: 55, BinID: 401, FreezeEnabled: true, FreezeWarehouseID: 3, FreezeQty: "5.0000",
	}, nil))
	if row := env.stock.stateOf(t, 3, 2, 3, 401, 55, 0); row.frozen != mustQty(5) || row.avail != mustQty(15) {
		t.Fatalf("冻结后库存不符: %+v", row)
	}

	cases := []struct {
		name   string
		in     ExceptionCreateInput
		code   string
		status int
		field  string
	}{
		{"非法类型", ExceptionCreateInput{Type: "外星异常"}, "RETURNS_EXCEPTION_TYPE_INVALID", 400, ""},
		{"冻结缺定位", ExceptionCreateInput{Type: "库存异常", FreezeEnabled: true, FreezeWarehouseID: 3}, "RETURNS_FREEZE_TARGET_REQUIRED", 400, ""},
		{"冻结缺量", ExceptionCreateInput{Type: "库存异常", FreezeEnabled: true, FreezeWarehouseID: 3, BinID: 401, SKUID: 55}, "COMMON_INVALID_PARAM", 400, "freeze_qty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions", tc.in, nil), tc.status, tc.code)
			if tc.field != "" && str(t, d["field"]) != tc.field {
				t.Fatalf("details.field=%v，期望 %s", d["field"], tc.field)
			}
		})
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions", nil, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// TestExceptionDetailHTTP GET /api/exceptions/:id：成功（记录/图片数组）、404、非法 id。
func TestExceptionDetailHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	id := seedExceptionID(t, env.testEnv, "拣货异常", "PK-HTTP-D1")

	m := wantOK(t, doReq(t, env.engine, http.MethodGet, fmt.Sprintf("/api/exceptions/%d", id), nil, nil))
	if str(t, m["type"]) != "拣货异常" || str(t, m["status"]) != "OPEN" || str(t, m["source_no"]) != "PK-HTTP-D1" {
		t.Fatalf("详情回显不符: %v/%v/%v", m["type"], m["status"], m["source_no"])
	}
	if _, ok := m["handle_records"].([]any); !ok {
		t.Fatalf("handle_records 应为数组（jsonb 载体）: %v", m["handle_records"])
	}
	if _, ok := m["image_refs"].([]any); !ok {
		t.Fatalf("image_refs 应为数组: %v", m["image_refs"])
	}
	if num(t, m["freeze_lock_id"]) != 0 {
		t.Fatalf("未冻结单 freeze_lock_id 应为 0: %v", m["freeze_lock_id"])
	}

	wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/exceptions/999", nil, nil),
		http.StatusNotFound, "RETURNS_EXCEPTION_NOT_FOUND")
	d := wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/exceptions/abc", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	if str(t, d["field"]) != "id" {
		t.Fatalf("details.field=%v，期望 id", d["field"])
	}
}

// TestExceptionAssignHTTP POST /api/exceptions/:id/assign：成功（状态+处理人+记录）、
// 缺处理人 400、重复分派 409、404。
func TestExceptionAssignHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	id := seedExceptionID(t, env.testEnv, "拣货异常", "PK-HTTP-AS1")

	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/assign", id),
		ExceptionAssignInput{AssigneeID: 5, AssigneeName: "王五"}, nil))
	if str(t, m["status"]) != "ASSIGNED" || num(t, m["assignee_id"]) != 5 || str(t, m["assignee_name"]) != "王五" {
		t.Fatalf("分派回显不符: %v/%v/%v", m["status"], m["assignee_id"], m["assignee_name"])
	}
	// 追加记录以 fresh GET 校验（动作响应的 handle_records 为装载期快照，缺本次动作
	// 记录——已知问题见 TestExceptionActionResponseHandleRecordsStale）。
	fresh, ferr := env.svc.GetException(ctx, id)
	if ferr != nil || len(fresh.HandleRecords) != 1 || fresh.HandleRecords[0].Action != "assign" {
		t.Fatalf("assign 处理记录未追加: err=%v records=%+v", ferr, fresh.HandleRecords)
	}

	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions/999/assign",
		ExceptionAssignInput{AssigneeID: 3, AssigneeName: "x"}, nil), http.StatusNotFound, "RETURNS_EXCEPTION_NOT_FOUND"); num(t, d["exception_id"]) != 999 {
		t.Fatalf("details.exception_id=%v，期望 999", d["exception_id"])
	}
	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/assign", id),
		ExceptionAssignInput{AssigneeName: "缺 ID"}, nil), http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "assignee_id" {
		t.Fatalf("details.field=%v，期望 assignee_id", d["field"])
	}
	// 重复分派 → 409（OPEN→ASSIGNED 单向，plan §6.10）。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/assign", id),
		ExceptionAssignInput{AssigneeID: 6, AssigneeName: "赵六"}, nil), http.StatusConflict, "RETURNS_EXCEPTION_STATUS_CONFLICT")
}

// TestExceptionImagesHTTP POST /api/exceptions/:id/images：挂接成功+引用形态、去重幂等、
// 空/无效文件 400、生命周期终点 409、检查器未装配 500 fail-closed。
func TestExceptionImagesHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	id := seedExceptionID(t, env.testEnv, "质检异常", "QC-HTTP-IMG")
	env.img.valid[101], env.img.valid[102] = true, true

	// 成功挂接：file_ids 以字符串 ID 传参（database.ID 反序列化同受字符串/数字，
	// internal/database/model.go:26 前端字符串约定）；image_refs 为文件中心下载通路
	// 引用（service_exception_images.go:41）。
	m := wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", id),
		map[string]any{"file_ids": []any{"101", "102"}}, nil))
	refs, _ := m["image_refs"].([]any)
	if len(refs) != 2 || str(t, refs[0]) != "/api/files/101/download" || str(t, refs[1]) != "/api/files/102/download" {
		t.Fatalf("图片引用不符: %v", m["image_refs"])
	}
	records, _ := m["handle_records"].([]any)
	if len(records) != 1 || str(t, records[0].(map[string]any)["action"]) != "attach_images" {
		t.Fatalf("挂接处理记录缺失: %v", m["handle_records"])
	}

	// 重复挂接：幂等无变化，不追加处理记录。
	m = wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", id),
		map[string]any{"file_ids": []any{"101"}}, nil))
	if refs, _ = m["image_refs"].([]any); len(refs) != 2 {
		t.Fatalf("重复挂接产生冗余引用: %v", m["image_refs"])
	}
	if records, _ = m["handle_records"].([]any); len(records) != 1 {
		t.Fatalf("重复挂接不应追加处理记录: %v", m["handle_records"])
	}

	// 空集合 / 无效文件 → 400。
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", id),
		map[string]any{"file_ids": []any{}}, nil), http.StatusBadRequest, "RETURNS_EXCEPTION_IMAGES_INVALID")
	if d := wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", id),
		map[string]any{"file_ids": []any{"777"}}, nil), http.StatusBadRequest, "RETURNS_EXCEPTION_IMAGES_INVALID"); num(t, d["file_id"]) != 777 {
		t.Fatalf("details.file_id=%v，期望 777", d["file_id"])
	}

	// 生命周期终点（RESOLVED）→ 409。
	exs, _, _ := env.svc.ListExceptions(ctx, ExceptionFilter{SourceNo: "QC-HTTP-IMG", Page: 1, PageSize: 1})
	exID := exs[0].IDInt.Int64()
	for _, step := range []func() error{
		func() error {
			_, err := env.svc.AssignException(ctx, actorB(), exID, ExceptionAssignInput{AssigneeID: 5, AssigneeName: "王五"})
			return err
		},
		func() error { _, err := env.svc.StartException(ctx, actorB(), exID, ExceptionNoteInput{}); return err },
		func() error { _, err := env.svc.ReviewException(ctx, actorB(), exID, ExceptionNoteInput{}); return err },
		func() error {
			_, err := env.svc.ResolveException(ctx, actorB(), exID, ExceptionNoteInput{Note: "ok"})
			return err
		},
	} {
		if err := step(); err != nil {
			t.Fatalf("预置生命周期推进失败: %v", err)
		}
	}
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", exID),
		map[string]any{"file_ids": []any{"102"}}, nil), http.StatusConflict, "RETURNS_EXCEPTION_IMAGES_CLOSED")

	// 检查器未装配 fail-closed（plan §3.1 规则①）→ 500。
	bare := newTestEnv(t)
	bareID := seedExceptionID(t, bare, "质检异常", "QC-HTTP-IMG-2")
	noImgUC := superUser()
	noImg := newReturnsEngine(t, bare.svc, &noImgUC)
	wantErr(t, doReq(t, noImg, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/images", bareID),
		map[string]any{"file_ids": []any{"101"}}, nil), http.StatusInternalServerError, "RETURNS_GATEWAY_MISSING")
}

// TestExceptionLifecycleHTTP 异常生命周期 HTTP 面：assign→start→review→resolve（解冻）→
// close，处理记录追加式；OPEN 跳状态 409；404。
func TestExceptionLifecycleHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	seedStockForHTTP(env.testEnv, 3, 2, 3, 401, 55, 20)
	wantOK(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions", ExceptionCreateInput{
		Type: "库存异常", SourceType: "count_order", SourceNo: "CK-HTTP-LC1", Detail: "差异",
		SKUID: 55, BinID: 401, FreezeEnabled: true, FreezeWarehouseID: 3, FreezeQty: "5.0000",
	}, nil))
	exs, _, _ := env.svc.ListExceptions(ctx, ExceptionFilter{SourceNo: "CK-HTTP-LC1", Page: 1, PageSize: 1})
	id := exs[0].IDInt.Int64()
	post := func(action string, in ExceptionNoteInput) map[string]any {
		t.Helper()
		return wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/%s", id, action), in, nil))
	}
	// 动作处理记录以 fresh GET 校验（动作响应的 handle_records 为装载期快照，缺本次
	// 动作记录——已知问题见 TestExceptionActionResponseHandleRecordsStale）。
	lastAction := func() string {
		t.Helper()
		fresh, err := env.svc.GetException(ctx, id)
		if err != nil {
			t.Fatalf("fresh GET 失败: %v", err)
		}
		if len(fresh.HandleRecords) == 0 {
			t.Fatal("处理记录为空")
		}
		return fresh.HandleRecords[len(fresh.HandleRecords)-1].Action
	}

	wantOK(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/assign", id),
		ExceptionAssignInput{AssigneeID: 5, AssigneeName: "王五"}, nil))
	if m := post("start", ExceptionNoteInput{Note: "开工"}); str(t, m["status"]) != "PROCESSING" || lastAction() != "start" {
		t.Fatalf("开始处理后状态/记录不符: %v", m["status"])
	}
	if m := post("review", ExceptionNoteInput{Note: "完成"}); str(t, m["status"]) != "PENDING_REVIEW" || lastAction() != "review" {
		t.Fatalf("提交复核后状态/记录不符: %v", m["status"])
	}
	if row := env.stock.stateOf(t, 3, 2, 3, 401, 55, 0); row.frozen != mustQty(5) {
		t.Fatalf("解决前冻结应保持: %+v", row)
	}
	m := post("resolve", ExceptionNoteInput{Note: "已调整"})
	if str(t, m["status"]) != "RESOLVED" || num(t, m["freeze_lock_id"]) != 0 {
		t.Fatalf("解决后状态/冻结锁不符: %v/%v", m["status"], m["freeze_lock_id"])
	}
	if row := env.stock.stateOf(t, 3, 2, 3, 401, 55, 0); row.frozen != 0 || row.avail != mustQty(20) {
		t.Fatalf("解决必须释放冻结（inventory-rules §4.2）: %+v", row)
	}
	if m := post("close", ExceptionNoteInput{Note: "归档"}); str(t, m["status"]) != "CLOSED" {
		t.Fatalf("关闭后状态=%v", m["status"])
	}
	if fresh, _ := env.svc.GetException(ctx, id); len(fresh.HandleRecords) < 7 {
		t.Fatalf("处理记录应追加式保留（assign/start/review/resolve/release/close…）: %d", len(fresh.HandleRecords))
	}

	// OPEN 跳状态 → 409。
	id2 := seedExceptionID(t, env.testEnv, "拣货异常", "PK-HTTP-LC2")
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/resolve", id2), ExceptionNoteInput{}, nil),
		http.StatusConflict, "RETURNS_EXCEPTION_STATUS_CONFLICT")
	wantErr(t, doReq(t, env.engine, http.MethodPost, fmt.Sprintf("/api/exceptions/%d/close", id2), ExceptionNoteInput{}, nil),
		http.StatusConflict, "RETURNS_EXCEPTION_STATUS_CONFLICT")
	wantErr(t, doReq(t, env.engine, http.MethodPost, "/api/exceptions/999/start", ExceptionNoteInput{}, nil),
		http.StatusNotFound, "RETURNS_EXCEPTION_NOT_FOUND")
}

// TestExceptionActionResponseHandleRecordsStale 已知 bug 复现位（ask 约束：不修业务代码）。
// AssignException/StartException/ReviewException/ResolveException/CloseException 在
// repo.AppendHandleRecord 落库后，直接以装载期快照 newExceptionView(e) 出响应
// （service_exception.go:295-307 等）——响应 handle_records 缺本次动作的记录；
// 对比 attachExceptionImages 显式同步内存快照并自述"避免响应缺记录"
// （service_exception_images.go:145-147）。库内数据正确（append-only），仅响应缺
// 本次记录，severity=low。修复后取消 Skip，将 body 的断言改为响应 handle_records
// 末位含本次动作记录。
func TestExceptionActionResponseHandleRecordsStale(t *testing.T) {
	t.Skip("已知bug：异常生命周期动作响应的 handle_records 缺本次动作记录（AppendHandleRecord 落库后未同步内存快照）")
}

// ---- 追溯 ----

// TestTraceHTTP GET /api/inventory/trace：成功链、序列号锚定、参数校验、数据权限
// （多仓 fail-closed 403 / 唯一绑定仓自动收窄）、序列号 404。
func TestTraceHTTP(t *testing.T) {
	env := newHTTPEnv(t, superUser())
	base := time.Now()
	env.ledgers.seed(
		TraceLedger{ID: 1, LedgerNo: "LED-A", SKUID: 11, WarehouseID: 1, BinID: 101, ChangeType: "INBOUND",
			BusinessType: "purchase_order", BusinessNo: "PO-HTTP-TR1", QtyChange: "10.0000",
			RequestID: "req-A", CreatedAt: base},
		TraceLedger{ID: 2, LedgerNo: "LED-B", SKUID: 11, WarehouseID: 1, BinID: 101, ChangeType: "OUTBOUND",
			BusinessType: "sales_order", BusinessNo: "SO-HTTP-TR1", QtyChange: "-10.0000",
			RequestID: "req-B", CreatedAt: base.Add(time.Hour)},
	)
	env.state.seedRows(TraceStockRow{WarehouseID: 1, BinID: 101, SKUID: 11, Total: "3.0000", Available: "3.0000"})
	env.state.seedSerial("SN-HTTP-9", TraceSerialRow{SerialNo: "SN-HTTP-9", SKUID: 11, Status: "RETURNED", WarehouseID: 1, BinID: 101})
	env.repo.seedOperationLog(middleware.OperationLog{ID: 9001, RequestID: "req-B", Module: "inventory", Action: "ship", Success: true})
	env.sales.seed("SO-HTTP-TR1", 1, ReturnableLine{LineNo: 1, SKUID: 11, Qty: 10})

	// 按 SKU：链时间正序 + 单据富化 + 操作日志关联。
	m := wantOK(t, doReq(t, env.engine, http.MethodGet, qPath("/api/inventory/trace", "sku_id", "11"), nil, nil))
	if num(t, m["sku_id"]) != 11 {
		t.Fatalf("sku_id 回显不符: %v", m["sku_id"])
	}
	chain, _ := m["chain"].([]any)
	if len(chain) != 2 {
		t.Fatalf("链长度=%d，期望 2", len(chain))
	}
	if str(t, chain[0].(map[string]any)["ledger_no"]) != "LED-A" {
		t.Fatal("追溯链应时间正序（append-only）")
	}
	foundSO := false
	for _, d := range m["documents"].([]any) {
		dm := d.(map[string]any)
		if str(t, dm["type"]) == "sales_order" && str(t, dm["no"]) == "SO-HTTP-TR1" && dm["found"] == true {
			foundSO = true
		}
	}
	if !foundSO {
		t.Fatalf("单据富化缺失: %v", m["documents"])
	}
	if ops, _ := m["operations"].([]any); len(ops) != 1 {
		t.Fatalf("操作日志关联数=%v，期望 1", m["operations"])
	}

	// 按序列号：锚定 SKU + 序列号台账。
	m = wantOK(t, doReq(t, env.engine, http.MethodGet, qPath("/api/inventory/trace", "serial_no", "SN-HTTP-9"), nil, nil))
	if serial, _ := m["serial"].(map[string]any); str(t, serial["serial_no"]) != "SN-HTTP-9" || num(t, m["sku_id"]) != 11 {
		t.Fatalf("序列号锚定不符: %v", m)
	}

	// 参数校验与错误分支。
	wantErr(t, doReq(t, env.engine, http.MethodGet, "/api/inventory/trace", nil, nil),
		http.StatusBadRequest, "RETURNS_TRACE_PARAM_REQUIRED")
	if d := wantErr(t, doReq(t, env.engine, http.MethodGet, qPath("/api/inventory/trace", "sku_id", "abc"), nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "sku_id" {
		t.Fatalf("details.field=%v，期望 sku_id", d["field"])
	}
	if d := wantErr(t, doReq(t, env.engine, http.MethodGet, qPath("/api/inventory/trace", "sku_id", "11", "limit", "0"), nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM"); str(t, d["field"]) != "limit" {
		t.Fatalf("details.field=%v，期望 limit", d["field"])
	}
	wantErr(t, doReq(t, env.engine, http.MethodGet, qPath("/api/inventory/trace", "serial_no", "SN-404"), nil, nil),
		http.StatusNotFound, "RETURNS_SERIAL_NOT_FOUND")

	// 数据权限：多仓范围不指定仓库 → 403 fail-closed（service_trace.go:126）。
	uc := whScopeUser(1, 3)
	scoped := newScopeEngine(t, env.svc, &uc)
	wantErr(t, doReq(t, scoped, http.MethodGet, qPath("/api/inventory/trace", "sku_id", "11"), nil, nil),
		http.StatusForbidden, "COMMON_PERMISSION_DENIED")
	// 越界仓库 → 403。
	uc1 := whScopeUser(1)
	scoped1 := newScopeEngine(t, env.svc, &uc1)
	wantErr(t, doReq(t, scoped1, http.MethodGet, qPath("/api/inventory/trace", "sku_id", "11", "warehouse_id", "2"), nil, nil),
		http.StatusForbidden, "COMMON_PERMISSION_DENIED")
	// 唯一绑定仓库自动收窄 → 200。
	ucAuto := whScopeUser(1)
	m = wantOK(t, doReq(t, newScopeEngine(t, env.svc, &ucAuto), http.MethodGet, qPath("/api/inventory/trace", "sku_id", "11"), nil, nil))
	if num(t, m["warehouse_id"]) != 1 {
		t.Fatalf("唯一绑定仓库应自动收窄: %v", m["warehouse_id"])
	}
}
