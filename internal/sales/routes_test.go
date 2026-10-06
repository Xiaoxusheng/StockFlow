package sales

// HTTP 层数据驱动测试（routes.go 28 端点，单元 sales）：handler 参数绑定 → Service →
// fakeRepo/fakeStock 全链，不依赖 PostgreSQL/Redis（ask 约束；真实 PG 行为见
// integration_test.go，//go:build integration）。
//
//   - 路由装配与权限面：真实 RegisterRoutes（db 以零值 *gorm.DB 占位，注册不触库，
//     inventory/handler_test.go 同型）——冻结 28 条路由表 + 未认证 401（permission.md §2）
//     + 装配 fail-fast（routes.go:58-68）；
//   - 业务路径：handler 直驱（gin.CreateTestContext），登录用户经 auth gin 上下文键
//     注入（sfUserKey 字面量 = internal/auth/middleware.go:26 ctxUserKey，跨包不可引用；
//     越仓/绑定仓用例同时自检该键漂移——键失效则范围过滤失效、用例必然失败）；
//   - 业务数据经 Service 层种子方法构造（真实业务流转产生，非手写脏数据），
//     复用 fakes_test.go 全套替身与 service_test.go 的 harness；
//   - Service 层数据矩阵（状态机/全链/幂等/FEFO/短拣等）见 service_test.go，不重复。
//
// 断言口径：HTTP 状态码 + 统一信封 code（api.md §2.2/§4：校验失败必须带 details）
// + data 载荷关键字段（Qty 线格式裸数字，解码后为 float64，stock/qty.go:71-74）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// sfUserKey auth 包 gin 上下文键的字面量镜像（internal/auth/middleware.go:26，未导出）。
// 自检：若 auth 侧键名漂移，whUser 注入失效 → 数据权限用例（绑定仓 total/越仓 404）必然失败。
const sfUserKey = "sf_auth_user"

// ---- 登录用户构造（auth.UserContext 字段见 internal/auth/auth.go:13-21） ----

func superUser() auth.UserContext {
	return auth.UserContext{UserID: 9, Username: "super", IsSuper: true, DataScope: auth.DataScopeAll}
}

func whUser(ids ...int64) auth.UserContext {
	return auth.UserContext{UserID: 8, Username: "wh-op", DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids}
}

// newHandlerEnv 每用例独立装配：全新 fake 基建 + 挂接同一 Service 的 handler
// （service_test.go newHarness 同源，handler 与 handler.go:19-21 同构）。
func newHandlerEnv(t *testing.T) (*harness, *handler) {
	t.Helper()
	h := newHarness(t)
	return h, &handler{svc: h.svc}
}

// ---- 单行取数助手（种子后单行场景取 ID/单号；多行时按 ID 升序取首个） ----

func minKey[T any](m map[int64]T) int64 {
	var ids []int64
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func firstSOID(h *harness) int64    { return minKey(h.repo.soRows) }
func firstOBNo(h *harness) string   { return h.repo.obRows[minKey(h.repo.obRows)].OutboundNo }
func firstPickID(h *harness) int64  { return minKey(h.repo.picks) }
func firstCheckID(h *harness) int64 { return minKey(h.repo.checks) }
func firstShipID(h *harness) int64  { return minKey(h.repo.shipments) }

// ---- 业务种子（经 Service 真实流转构造数据） ----

// seedSubmittedByID 提交指定订单（PENDING_APPROVAL）。
func (h *harness) seedSubmittedByID(t *testing.T, so *SalesOrder) {
	t.Helper()
	if _, err := h.svc.SubmitSalesOrder(h.ctx, Actor{ID: 9}, so.ID.Int64()); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
}

// seedSubmitted 建单→提交（PENDING_APPROVAL）。
func (h *harness) seedSubmitted(t *testing.T, skuID int64, qty Qty) *SalesOrder {
	t.Helper()
	so := h.createOrder(t, skuID, qty)
	h.seedSubmittedByID(t, so)
	return so
}

// seedPickable 审核后生成拣货任务（出库单 PICKING）。自给自足：单仓单库位（wh1/bin1，
// 批次 0）种库存——本文件拣/复/包/发管线种子统一走 skuPlain。
func (h *harness) seedPickable(t *testing.T, skuID int64, qty Qty) (string, []PickTask) {
	t.Helper()
	h.seedStock(wh1, bin1, skuID, 0, qty, 1, 11)
	_, res := h.submitAndApprove(t, skuID, qty)
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, Actor{ID: 7, Name: "picker"}, res.OutboundNo)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	return res.OutboundNo, picks
}

// seedPicked 全部拣货任务领取+满量确认（出库单 PICKED），返回单号与联动复核任务。
func (h *harness) seedPicked(t *testing.T, skuID int64, qty Qty) (string, []CheckTask) {
	t.Helper()
	no, picks := h.seedPickable(t, skuID, qty)
	a := Actor{ID: 7, Name: "picker"}
	for _, p := range picks {
		if _, err := h.svc.ClaimPickTask(h.ctx, a, p.ID.Int64()); err != nil {
			t.Fatalf("领取失败: %v", err)
		}
		if _, err := h.svc.ConfirmPick(h.ctx, a, p.ID.Int64(), PickConfirmInput{PickedQty: p.Qty}); err != nil {
			t.Fatalf("拣货确认失败: %v", err)
		}
	}
	checks, err := h.repo.ListCheckTasksByOutbound(nil, no)
	if err != nil {
		t.Fatalf("读取复核任务失败: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("拣货确认应联动创建复核任务")
	}
	return no, checks
}

// seedChecked 复核全部通过（出库单 CHECKED）。
func (h *harness) seedChecked(t *testing.T, skuID int64, qty Qty) string {
	t.Helper()
	no, checks := h.seedPicked(t, skuID, qty)
	a := Actor{ID: 6, Name: "checker"}
	for _, c := range checks {
		if _, err := h.svc.ConfirmCheck(h.ctx, a, c.ID.Int64(), CheckConfirmInput{Pass: true}); err != nil {
			t.Fatalf("复核确认失败: %v", err)
		}
	}
	return no
}

// seedPacked 打包完成（出库单 PACKED）。
func (h *harness) seedPacked(t *testing.T, skuID int64, qty Qty) string {
	t.Helper()
	no := h.seedChecked(t, skuID, qty)
	if _, err := h.svc.Pack(h.ctx, Actor{ID: 5, Name: "packer"}, PackInput{
		OutboundNo: no, Lines: []PackLineInput{{LineNo: 1, Qty: qty}},
	}); err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	return no
}

// seedShipped 发货完成（出库单 SHIPPED_ALL + 发货单 SHIPPED）。
func (h *harness) seedShipped(t *testing.T, skuID int64, qty Qty) *ShipResult {
	t.Helper()
	no := h.seedPacked(t, skuID, qty)
	res, err := h.svc.Ship(h.ctx, Actor{ID: 4, Name: "shipper"},
		ShipInput{OutboundNo: no, Carrier: "SF", TrackingNo: "SF-1"})
	if err != nil {
		t.Fatalf("发货失败: %v", err)
	}
	return res
}

// seedPartialShipped 两行订单只发行 1 → 出库单 PARTIAL_SHIPPED（行 2 预占保留）。
// 返回（销售订单 ID, 出库单号）。
func (h *harness) seedPartialShipped(t *testing.T) (int64, string) {
	t.Helper()
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
	h.seedStock(wh1, bin2, skuFIFO, 4001, qtyOf(4), 1, 12)
	h.seedBatches(wh1, skuFIFO, []BatchCandidate{{BatchID: 4001, BatchNo: "F1", AvailableQty: qtyOf(4)}})
	o, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{
			{LineNo: 1, SKUID: skuPlain, Qty: qtyOf(2)},
			{LineNo: 2, SKUID: skuFIFO, Qty: qtyOf(3)},
		},
	})
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	h.seedSubmittedByID(t, o)
	if _, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "APPROVE"}); err != nil {
		t.Fatalf("审核失败: %v", err)
	}
	ob, _ := h.repo.ListOutboundOrdersBySO(nil, o.SoNo)
	no := ob[0].OutboundNo
	a := Actor{ID: 7, Name: "picker"}
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, a, no)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	for _, p := range picks {
		if _, err := h.svc.ClaimPickTask(h.ctx, a, p.ID.Int64()); err != nil {
			t.Fatalf("领取失败: %v", err)
		}
		if _, err := h.svc.ConfirmPick(h.ctx, a, p.ID.Int64(), PickConfirmInput{PickedQty: p.Qty}); err != nil {
			t.Fatalf("拣货确认失败: %v", err)
		}
	}
	checks, err := h.repo.ListCheckTasksByOutbound(nil, no)
	if err != nil {
		t.Fatalf("读取复核任务失败: %v", err)
	}
	for _, c := range checks {
		if _, err := h.svc.ConfirmCheck(h.ctx, a, c.ID.Int64(), CheckConfirmInput{Pass: true}); err != nil {
			t.Fatalf("复核确认失败: %v", err)
		}
	}
	if _, err := h.svc.Pack(h.ctx, a, PackInput{OutboundNo: no, Lines: []PackLineInput{
		{LineNo: 1, Qty: qtyOf(2)}, {LineNo: 2, Qty: qtyOf(3)},
	}}); err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	if _, err := h.svc.Ship(h.ctx, a, ShipInput{OutboundNo: no, Lines: []ShipLineInput{{LineNo: 1}}}); err != nil {
		t.Fatalf("部分发货失败: %v", err)
	}
	return o.ID.Int64(), no
}

// seedPickedLine1 双行订单只拣行 1（出库单 PICKING、行 1 已拣）——重分配禁行场景。
func (h *harness) seedPickedLine1(t *testing.T) string {
	t.Helper()
	h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(5), 1, 11)
	o, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: wh1,
		Items: []OrderItemInput{
			{LineNo: 1, SKUID: skuPlain, Qty: qtyOf(2)},
			{LineNo: 2, SKUID: skuPlain, Qty: qtyOf(3)},
		},
	})
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	h.seedSubmittedByID(t, o)
	if _, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o.ID.Int64(), ApproveInput{Action: "APPROVE"}); err != nil {
		t.Fatalf("审核失败: %v", err)
	}
	ob, _ := h.repo.ListOutboundOrdersBySO(nil, o.SoNo)
	_, picks, err := h.svc.GeneratePickTasks(h.ctx, Actor{ID: 7}, ob[0].OutboundNo)
	if err != nil {
		t.Fatalf("生成拣货任务失败: %v", err)
	}
	for _, p := range picks {
		if p.OutboundLineNo != 1 {
			continue
		}
		if _, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: 7}, p.ID.Int64()); err != nil {
			t.Fatalf("领取失败: %v", err)
		}
		if _, err := h.svc.ConfirmPick(h.ctx, Actor{ID: 7}, p.ID.Int64(), PickConfirmInput{PickedQty: p.Qty}); err != nil {
			t.Fatalf("拣货确认失败: %v", err)
		}
	}
	return ob[0].OutboundNo
}

// ---- HTTP 驱动与用例表 ----

type httpCase struct {
	name     string
	method   string
	target   string                  // 静态路径
	targetF  func(h *harness) string // 种子后动态路径（按 ID/单号）
	body     any                     // JSON 请求体（nil = 空体）
	bodyF    func(h *harness) any    // 种子后动态请求体（依赖业务单号）
	rawBody  string                  // 非空 = 原始字节（绑定失败用例）
	headers  map[string]string
	user     func() auth.UserContext // nil = superUser
	seed     func(t *testing.T, h *harness)
	wantHTTP int
	wantCode string // "" = 成功信封（code==0）
	check    func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any)
}

// runHTTP 表驱动执行：每用例独立 harness（fakeRepo 全量快照隔离）。bind 返回
// （gin 路由模式, handler）——模式用于注入路径参数（见 driveHTTP/pathParams）。
func runHTTP(t *testing.T, bind func(*handler) (string, gin.HandlerFunc), cases []httpCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h, hd := newHandlerEnv(t)
			pattern, fn := bind(hd)
			if tc.seed != nil {
				tc.seed(t, h)
			}
			target := tc.target
			if tc.targetF != nil {
				target = tc.targetF(h)
			}
			body := tc.body
			if tc.bodyF != nil {
				body = tc.bodyF(h)
			}
			user := superUser()
			if tc.user != nil {
				user = tc.user()
			}
			rec, env := driveHTTP(t, fn, tc.method, pattern, target, body, tc.rawBody, tc.headers, user)
			if rec.Code != tc.wantHTTP {
				t.Fatalf("HTTP 状态不符: want %d got %d body=%s", tc.wantHTTP, rec.Code, rec.Body.String())
			}
			if tc.wantCode == "" {
				if v, ok := env["code"].(float64); !ok || v != 0 {
					t.Fatalf("期望成功信封 code=0，got %v body=%s", env["code"], rec.Body.String())
				}
			} else if env["code"] != tc.wantCode {
				t.Fatalf("信封码不符: want %s got %v body=%s", tc.wantCode, env["code"], rec.Body.String())
			}
			if tc.check != nil {
				tc.check(t, h, rec, env)
			}
		})
	}
}

// pathParams 依据 gin 路由模式提取路径参数（CreateTestContext 无路由树，
// c.Param 恒空——需按模式/目标逐段比对手动注入，与 routes.go 冻结模式对应）。
func pathParams(pattern, target string) gin.Params {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	ts := strings.Split(strings.Trim(target, "/"), "/")
	var out gin.Params
	for i, seg := range ps {
		if strings.HasPrefix(seg, ":") && i < len(ts) {
			out = append(out, gin.Param{Key: seg[1:], Value: ts[i]})
		}
	}
	return out
}

// driveHTTP 构造 gin 测试上下文并驱动 handler（参数绑定/响应信封全真）。
func driveHTTP(t *testing.T, fn gin.HandlerFunc, method, pattern, target string,
	body any, rawBody string, headers map[string]string, user auth.UserContext,
) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var rdr *strings.Reader
	switch {
	case rawBody != "":
		rdr = strings.NewReader(rawBody)
	case body != nil:
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("构造请求体失败: %v", err)
		}
		rdr = strings.NewReader(string(b))
	default:
		rdr = strings.NewReader("") // 空体（可空体端点应容忍 EOF）
	}
	req := httptest.NewRequest(method, target, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	c.Params = pathParams(pattern, target)
	if user.UserID != 0 || user.IsSuper {
		c.Set(sfUserKey, user)
	}
	fn(c)
	return rec, decodeEnv(t, rec)
}

func decodeEnv(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应非 JSON 信封: %v body=%s", err, rec.Body.String())
	}
	return m
}

// ---- 信封取数助手 ----

func dataObj(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	d, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 应为对象，got %T", env["data"])
	}
	return d
}

func dataArr(t *testing.T, env map[string]any, key string) []any {
	t.Helper()
	arr, ok := dataObj(t, env)[key].([]any)
	if !ok {
		t.Fatalf("data.%s 应为数组，got %T", key, dataObj(t, env)[key])
	}
	return arr
}

func numAt(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%s 应为数字，got %T（%v）", key, m[key], m[key])
	}
	return v
}

func strAt(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s 应为字符串，got %T", key, m[key])
	}
	return v
}

func objAt(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s 应为对象，got %T", key, m[key])
	}
	return v
}

// ---- 路由装配 + 权限面（真实 RegisterRoutes，28 端点冻结表） ----

// salesRoutes routes.go:73-112 冻结路由表（method + 路径）。
var salesRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/sales"},
	{http.MethodPost, "/api/sales"},
	{http.MethodGet, "/api/sales/:id"},
	{http.MethodPut, "/api/sales/:id"},
	{http.MethodPut, "/api/sales/:id/submit"},
	{http.MethodPut, "/api/sales/:id/approve"},
	{http.MethodPut, "/api/sales/:id/cancel"},
	{http.MethodPut, "/api/sales/:id/close"},
	{http.MethodGet, "/api/outbounds"},
	{http.MethodGet, "/api/outbounds/:no"},
	{http.MethodPost, "/api/outbounds/:no/picks"},
	{http.MethodPut, "/api/outbounds/:no/cancel"},
	{http.MethodPut, "/api/outbounds/:no/close"},
	{http.MethodGet, "/api/allocations"},
	{http.MethodPost, "/api/allocations"},
	{http.MethodGet, "/api/picks"},
	{http.MethodPut, "/api/picks/:id/claim"},
	{http.MethodPut, "/api/picks/:id/confirm"},
	{http.MethodPut, "/api/picks/:id/exception"},
	{http.MethodGet, "/api/checks"},
	{http.MethodPut, "/api/checks/:id/claim"},
	{http.MethodPut, "/api/checks/:id/confirm"},
	{http.MethodPut, "/api/checks/:id/reopen"},
	{http.MethodGet, "/api/packing"},
	{http.MethodPost, "/api/packing"},
	{http.MethodGet, "/api/shipments"},
	{http.MethodPost, "/api/shipments"},
	{http.MethodPut, "/api/shipments/:id/status"},
}

// registerTestEngine 真实 RegisterRoutes（db 零值占位：注册不触库，未认证在
// RequirePerm 即被拒——inventory/handler_test.go newWiredEngine 同型）。
func registerTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil,
		WithStock(newFakeStock()), WithSKUAttr(newFakeSKUs(nil)),
		WithCustomerChecker(fakeCustomers{ok: true}), WithBinChecker(fakeBins{ok: true}))
	return r
}

func TestSalesRoutesRegisteredAndRequireAuth(t *testing.T) {
	r := registerTestEngine(t)
	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	if len(r.Routes()) != len(salesRoutes) {
		t.Fatalf("本域应恰好注册 %d 条路由，实际 %d（注册清单漂移需同步 routes.go 冻结表）",
			len(salesRoutes), len(r.Routes()))
	}
	for _, w := range salesRoutes {
		key := w.method + " " + w.path
		if !got[key] {
			t.Fatalf("路由未注册: %s", key)
		}
		// 未认证访问 → 401 COMMON_UNAUTHORIZED（权限中间件先于一切业务逻辑）。
		p := strings.ReplaceAll(strings.ReplaceAll(w.path, ":id", "1"), ":no", "1")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(w.method, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 未认证应 401，实际 %d: %s", key, rec.Code, rec.Body.String())
		}
		env := decodeEnv(t, rec)
		if env["code"] != "COMMON_UNAUTHORIZED" {
			t.Fatalf("%s 应返回统一信封 401，实际 %s", key, rec.Body.String())
		}
	}
}

func TestRegisterRoutesFailFast(t *testing.T) {
	full := []Option{WithStock(newFakeStock()), WithSKUAttr(newFakeSKUs(nil)),
		WithCustomerChecker(fakeCustomers{ok: true}), WithBinChecker(fakeBins{ok: true})}
	cases := []struct {
		name string
		db   *gorm.DB
		opts []Option
	}{
		{"db 为 nil", nil, full},
		{"跨域依赖全部缺位", &gorm.DB{}, nil},
		{"仅注入库存网关", &gorm.DB{}, []Option{WithStock(newFakeStock())}},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s：期望启动期 panic（routes.go:58-68 fail-fast）", tc.name)
				}
			}()
			gin.SetMode(gin.TestMode)
			RegisterRoutes(gin.New().Group("/api"), tc.db, nil, tc.opts...)
		}()
	}
}

// ---- GET /api/sales 销售订单列表 ----

// seedTwoOrders wh1 DRAFT + wh2 APPROVED（审核需 wh2 有库存）。
func seedTwoOrders(t *testing.T, h *harness) {
	t.Helper()
	h.createOrder(t, skuPlain, qtyOf(2))
	h.seedStock(22, 201, skuPlain, 0, qtyOf(3), 2, 21)
	o2, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
		CustomerID: 77, WarehouseID: 22,
		Items: []OrderItemInput{{SKUID: skuPlain, Qty: qtyOf(3), Price: qtyOf(2)}},
	})
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	h.seedSubmittedByID(t, o2)
	if _, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o2.ID.Int64(), ApproveInput{Action: "APPROVE"}); err != nil {
		t.Fatalf("审核失败: %v", err)
	}
}

func TestHTTPListSalesOrders(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales", hd.listSalesOrders }, []httpCase{
		{name: "全仓范围可见全部", method: http.MethodGet, target: "/api/sales",
			seed: seedTwoOrders, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				if numAt(t, d, "total") != 2 || len(dataArr(t, env, "items")) != 2 {
					t.Fatalf("应见 2 张订单: %s", rec.Body.String())
				}
				if numAt(t, d, "page") != 1 || numAt(t, d, "pageSize") != 20 {
					t.Fatalf("分页回显不符: %s", rec.Body.String())
				}
			}},
		{name: "绑定仓范围行级过滤", method: http.MethodGet, target: "/api/sales",
			seed: seedTwoOrders, user: func() auth.UserContext { return whUser(wh1) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 1 {
					t.Fatalf("wh1 范围应仅见 1 张（=0 说明登录上下文键漂移）: %v", got)
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/sales",
			seed: seedTwoOrders, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见任何行: %v", got)
				}
			}},
		{name: "status 筛选命中已审单", method: http.MethodGet, target: "/api/sales?status=APPROVED",
			seed: seedTwoOrders, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 ||
					strAt(t, items[0].(map[string]any), "status") != "APPROVED" {
					t.Fatalf("status 筛选结果不符: %s", rec.Body.String())
				}
			}},
		{name: "时间范围参数解析通过（替身不执行时间过滤）", method: http.MethodGet,
			target: "/api/sales?created_from=2026-01-01&created_to=2026-12-31",
			seed:   seedTwoOrders, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 2 {
					t.Fatalf("内存替身不做时间过滤，应回全部: %v", got)
				}
			}},
		{name: "非法 customer_id → 400", method: http.MethodGet, target: "/api/sales?customer_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "负数 warehouse_id → 400", method: http.MethodGet, target: "/api/sales?warehouse_id=-1",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法 created_from 格式 → 400", method: http.MethodGet, target: "/api/sales?created_from=2026/01/01",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法 page → 400 + details", method: http.MethodGet, target: "/api/sales?page=0",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM",
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if env["details"] == nil {
					t.Fatal("校验失败必须带 details（api.md §4）")
				}
			}},
	})
}

// ---- POST /api/sales 创建销售订单 ----

func TestHTTPCreateSalesOrder(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"customer_id": 77, "warehouse_id": wh1, "shipping_address": "上海市浦东新区",
			"delivery_method": "快递",
			"items":           []map[string]any{{"line_no": 1, "sku_id": skuPlain, "qty": 2, "price": 2, "remark": "急件"}},
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales", hd.createSalesOrder }, []httpCase{
		{name: "创建成功（金额=Σ数量×单价，同事务审计）", method: http.MethodPost, target: "/api/sales",
			body: valid(), wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				o := dataObj(t, env)
				if !strings.HasPrefix(strAt(t, o, "so_no"), "SO-") || strAt(t, o, "status") != "DRAFT" {
					t.Fatalf("草稿订单不符: %s", rec.Body.String())
				}
				if numAt(t, o, "total_amount") != 4 || numAt(t, o, "customer_id") != 77 {
					t.Fatalf("金额/客户不符: %s", rec.Body.String())
				}
				if h.audit.count("create") != 1 {
					t.Fatalf("创建应同事务写审计 1 条，实际 %d", h.audit.count("create"))
				}
			}},
		{name: "非法 JSON → 400 + details 且无副作用", method: http.MethodPost, target: "/api/sales",
			rawBody: `{"items":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM",
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if env["details"] == nil {
					t.Fatal("绑定失败必须带 details")
				}
				if len(h.repo.soRows) != 0 {
					t.Fatalf("绑定失败不应有副作用: %d", len(h.repo.soRows))
				}
			}},
		{name: "缺明细 → 400", method: http.MethodPost, target: "/api/sales",
			body:     map[string]any{"customer_id": 77, "warehouse_id": wh1, "items": []map[string]any{}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "数量为 0 → 400", method: http.MethodPost, target: "/api/sales",
			body: map[string]any{"customer_id": 77, "warehouse_id": wh1,
				"items": []map[string]any{{"sku_id": skuPlain, "qty": 0}}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "单价为负 → 400", method: http.MethodPost, target: "/api/sales",
			body: map[string]any{"customer_id": 77, "warehouse_id": wh1,
				"items": []map[string]any{{"sku_id": skuPlain, "qty": 2, "price": -1}}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "行号重复 → 400", method: http.MethodPost, target: "/api/sales",
			body: map[string]any{"customer_id": 77, "warehouse_id": wh1,
				"items": []map[string]any{
					{"line_no": 1, "sku_id": skuPlain, "qty": 1},
					{"line_no": 1, "sku_id": skuPlain, "qty": 2},
				}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "客户停用 → 400 SALES_CUSTOMER_NOT_FOUND", method: http.MethodPost, target: "/api/sales",
			body: valid(), wantHTTP: 400, wantCode: "SALES_CUSTOMER_NOT_FOUND",
			seed: func(t *testing.T, h *harness) { h.svc.customers = fakeCustomers{ok: false} }},
		{name: "SKU 未启用 → 400 SALES_SKU_NOT_FOUND", method: http.MethodPost, target: "/api/sales",
			body: map[string]any{"customer_id": 77, "warehouse_id": wh1,
				"items": []map[string]any{{"sku_id": 999, "qty": 2}}},
			wantHTTP: 400, wantCode: "SALES_SKU_NOT_FOUND"},
	})
}

// ---- GET /api/sales/:id 订单详情 ----

func TestHTTPGetSalesOrder(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id", hd.getSalesOrder }, []httpCase{
		{name: "详情含明细行", method: http.MethodGet,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				if !strings.HasPrefix(strAt(t, objAt(t, d, "order"), "so_no"), "SO-") ||
					len(dataArr(t, env, "items")) != 1 {
					t.Fatalf("详情结构不符: %s", rec.Body.String())
				}
				it := dataArr(t, env, "items")[0].(map[string]any)
				if numAt(t, it, "sku_id") != skuPlain || numAt(t, it, "qty") != 2 {
					t.Fatalf("明细数据不符: %v", it)
				}
			}},
		{name: "订单不存在 → 404", method: http.MethodGet, target: "/api/sales/424242",
			wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
		{name: "越仓详情 → 404（行级数据权限）", method: http.MethodGet,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			user:     func() auth.UserContext { return whUser(999) },
			wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
		{name: "非法路径 id（非数字）→ 400", method: http.MethodGet, target: "/api/sales/abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法路径 id（0）→ 400", method: http.MethodGet, target: "/api/sales/0",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/sales/:id 草稿编辑 ----

func TestHTTPUpdateSalesOrder(t *testing.T) {
	editBody := map[string]any{"customer_id": 77, "warehouse_id": wh1,
		"items": []map[string]any{{"line_no": 1, "sku_id": skuPlain, "qty": 3, "price": 2}}}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id", hd.updateSalesOrder }, []httpCase{
		{name: "草稿编辑成功（金额重算+明细重写）", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			body:    editBody, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total_amount"); got != 6 {
					t.Fatalf("金额应重算为 6: %v", got)
				}
				items := h.repo.soItems[firstSOID(h)]
				if len(items) != 1 || items[0].Qty != qtyOf(3) {
					t.Fatalf("明细应整组重写: %+v", items)
				}
			}},
		{name: "已提交订单不可编辑 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedSubmitted(t, skuPlain, qtyOf(2)) },
			body:    editBody, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "订单不存在 → 404", method: http.MethodPut, target: "/api/sales/424242",
			body: editBody, wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
		{name: "非法请求体 → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			rawBody: `{"items":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "缺明细 → 400", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			body:     map[string]any{"customer_id": 77, "warehouse_id": wh1},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/sales/:id/submit 提交审核 ----

func TestHTTPSubmitSalesOrder(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id/submit", hd.submitSalesOrder }, []httpCase{
		{name: "提交成功 DRAFT→PENDING_APPROVAL", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d/submit", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != "PENDING_APPROVAL" {
					t.Fatalf("状态不符: %s", rec.Body.String())
				}
				if len(h.repo.approvals) != 1 || h.repo.approvals[0].Action != "SUBMIT" {
					t.Fatalf("应落 SUBMIT 审批记录: %+v", h.repo.approvals)
				}
			}},
		{name: "重复提交 → 409", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d/submit", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.seedSubmitted(t, skuPlain, qtyOf(2)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "订单不存在 → 404", method: http.MethodPut, target: "/api/sales/424242/submit",
			wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
		{name: "非法路径 id → 400", method: http.MethodPut, target: "/api/sales/abc/submit",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/sales/:id/approve 审核（通过=预占/驳回=终态） ----

func TestHTTPApproveSalesOrder(t *testing.T) {
	withStock := func(qty Qty) func(t *testing.T, h *harness) {
		return func(t *testing.T, h *harness) {
			h.seedStock(wh1, bin1, skuPlain, 0, qty, 1, 11)
			h.seedSubmitted(t, skuPlain, qtyOf(2))
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id/approve", hd.approveSalesOrder }, []httpCase{
		{name: "审核通过并预占（审核即预占）", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/approve", firstSOID(h)) },
			seed:    withStock(qtyOf(2)),
			body:    map[string]any{"action": "APPROVE"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				if !strings.HasPrefix(strAt(t, d, "outbound_no"), "OUT-") {
					t.Fatalf("应联动创建出库单: %s", rec.Body.String())
				}
				if numAt(t, d, "locked_qty") != 2 || numAt(t, d, "lock_count") != 1 {
					t.Fatalf("预占回执不符: %s", rec.Body.String())
				}
				if _, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0); locked != qtyOf(2) {
					t.Fatalf("库存应预占 2")
				}
				if strAt(t, objAt(t, d, "order"), "status") != "APPROVED" {
					t.Fatalf("订单应 APPROVED: %s", rec.Body.String())
				}
			}},
		{name: "驳回落 REJECT 审批记录", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/approve", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedSubmitted(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"action": "REJECT", "opinion": "价格有误"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, objAt(t, dataObj(t, env), "order"), "status") != "REJECTED" {
					t.Fatalf("订单应 REJECTED: %s", rec.Body.String())
				}
				// 种子提交已落 SUBMIT 记录，驳回追加 REJECT 记录（含意见）。
				if n := len(h.repo.approvals); n != 2 || h.repo.approvals[n-1].Action != "REJECT" ||
					h.repo.approvals[n-1].Opinion != "价格有误" {
					t.Fatalf("审批记录不符: %+v", h.repo.approvals)
				}
			}},
		{name: "库存不足 → 409 INVENTORY_NOT_ENOUGH 且停留待审", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/approve", firstSOID(h)) },
			seed: func(t *testing.T, h *harness) {
				h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(1), 1, 11)
				h.seedSubmitted(t, skuPlain, qtyOf(5))
			},
			body: map[string]any{"action": "APPROVE"}, wantHTTP: 409, wantCode: "INVENTORY_NOT_ENOUGH",
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				so := h.repo.soRows[firstSOID(h)]
				if so.Status != SOStatusPendingApproval {
					t.Fatalf("预占失败订单应停留 PENDING_APPROVAL，实际 %s", so.Status)
				}
				if len(h.repo.obRows) != 0 {
					t.Fatalf("回滚后不应残留出库单: %d", len(h.repo.obRows))
				}
			}},
		{name: "草稿直接审核 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/approve", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"action": "APPROVE"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "非法 action → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/approve", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedSubmitted(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"action": "MAYBE"}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "绑定失败 → 400", method: http.MethodPut, target: "/api/sales/1/approve",
			rawBody: `{"action":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "订单不存在 → 404", method: http.MethodPut, target: "/api/sales/424242/approve",
			body: map[string]any{"action": "APPROVE"}, wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
	})
}

// ---- PUT /api/sales/:id/cancel 取消（空体允许） ----

func TestHTTPCancelSalesOrder(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id/cancel", hd.cancelSalesOrder }, []httpCase{
		{name: "草稿取消（空请求体允许）", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/sales/%d/cancel", firstSOID(h)) },
			seed:     func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != "CANCELLED" {
					t.Fatalf("应 CANCELLED: %s", rec.Body.String())
				}
			}},
		{name: "审核后取消释放预占", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/cancel", firstSOID(h)) },
			seed: func(t *testing.T, h *harness) {
				h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
				h.submitAndApprove(t, skuPlain, qtyOf(2))
			},
			body: map[string]any{"reason": "客户取消"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if _, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0); locked != 0 {
					t.Fatalf("取消后锁应全部释放")
				}
				if items := h.repo.soItems[firstSOID(h)]; items[0].QtyAllocated != 0 {
					t.Fatalf("明细预占应归零: %s", items[0].QtyAllocated)
				}
			}},
		{name: "已取消不可再取消 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/cancel", firstSOID(h)) },
			seed: func(t *testing.T, h *harness) {
				so := h.createOrder(t, skuPlain, qtyOf(2))
				if _, err := h.svc.CancelSalesOrder(h.ctx, Actor{ID: 9}, so.ID.Int64(), CancelInput{}); err != nil {
					t.Fatalf("首次取消失败: %v", err)
				}
			},
			body: map[string]any{"reason": "again"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "订单不存在 → 404", method: http.MethodPut, target: "/api/sales/424242/cancel",
			wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
	})
}

// ---- PUT /api/sales/:id/close 差额关闭 ----

func TestHTTPCloseSalesOrder(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/sales/:id/close", hd.closeSalesOrder }, []httpCase{
		{name: "缺原因 → 400 SALES_CLOSE_REASON_REQUIRED", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/close", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"reason": "  "}, wantHTTP: 400, wantCode: "SALES_CLOSE_REASON_REQUIRED"},
		{name: "草稿不可关闭 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/close", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.createOrder(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"reason": "x"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "部分发货差额关闭 → COMPLETED", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/sales/%d/close", firstSOID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPartialShipped(t) },
			body:    map[string]any{"reason": "余量差额关闭"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != "COMPLETED" {
					t.Fatalf("应 COMPLETED: %s", rec.Body.String())
				}
			}},
		{name: "绑定失败 → 400", method: http.MethodPut, target: "/api/sales/1/close",
			rawBody: `{"reason":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "订单不存在 → 404", method: http.MethodPut, target: "/api/sales/424242/close",
			body: map[string]any{"reason": "x"}, wantHTTP: 404, wantCode: "SALES_ORDER_NOT_FOUND"},
	})
}

// ---- GET /api/outbounds 出库单列表 ----

func TestHTTPListOutbounds(t *testing.T) {
	seedTwoOutbounds := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
		h.submitAndApprove(t, skuPlain, qtyOf(2)) // wh1 → OUT
		h.seedStock(22, 201, skuPlain, 0, qtyOf(1), 2, 21)
		o2, err := h.svc.CreateSalesOrder(h.ctx, Actor{ID: 9}, CreateOrderInput{
			CustomerID: 77, WarehouseID: 22,
			Items: []OrderItemInput{{SKUID: skuPlain, Qty: qtyOf(1)}},
		})
		if err != nil {
			t.Fatalf("建单失败: %v", err)
		}
		h.seedSubmittedByID(t, o2)
		if _, err := h.svc.ApproveSalesOrder(h.ctx, Actor{ID: 8}, o2.ID.Int64(), ApproveInput{Action: "APPROVE"}); err != nil {
			t.Fatalf("审核失败: %v", err)
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/outbounds", hd.listOutbounds }, []httpCase{
		{name: "全仓范围可见全部", method: http.MethodGet, target: "/api/outbounds",
			seed: seedTwoOutbounds, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 2 || len(items) != 2 {
					t.Fatalf("应见 2 张出库单: %s", rec.Body.String())
				}
				if !strings.HasPrefix(strAt(t, items[0].(map[string]any), "outbound_no"), "OUT-") {
					t.Fatalf("出库单号不符: %s", rec.Body.String())
				}
			}},
		{name: "绑定仓范围行级过滤", method: http.MethodGet, target: "/api/outbounds",
			seed: seedTwoOutbounds, user: func() auth.UserContext { return whUser(wh1) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 1 {
					t.Fatalf("wh1 范围应仅见 1 张: %v", got)
				}
			}},
		{name: "非法 warehouse_id → 400", method: http.MethodGet, target: "/api/outbounds?warehouse_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "pageSize 超上限 → 400", method: http.MethodGet, target: "/api/outbounds?pageSize=1000",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- GET /api/outbounds/:no 出库单详情（含任务族） ----

func TestHTTPGetOutbound(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/outbounds/:no", hd.getOutbound }, []httpCase{
		{name: "详情含分配与拣/复任务族", method: http.MethodGet,
			targetF:  func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) },
			seed:     func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				for _, key := range []string{"outbound", "items", "allocations", "picks", "checks", "packages", "shipments"} {
					if _, ok := d[key]; !ok {
						t.Fatalf("详情缺 %s 键: %s", key, rec.Body.String())
					}
				}
				if strAt(t, objAt(t, d, "outbound"), "status") != "PICKED" {
					t.Fatalf("出库单应 PICKED: %s", rec.Body.String())
				}
				if len(dataArr(t, env, "items")) != 1 || len(dataArr(t, env, "allocations")) != 1 ||
					len(dataArr(t, env, "picks")) != 1 || len(dataArr(t, env, "checks")) != 1 {
					t.Fatalf("任务族数量不符: %s", rec.Body.String())
				}
				it := dataArr(t, env, "items")[0].(map[string]any)
				if numAt(t, it, "qty_picked") != 2 {
					t.Fatalf("明细拣货进度不符: %v", it)
				}
			}},
		{name: "出库单不存在 → 404", method: http.MethodGet, target: "/api/outbounds/OUT-NONE",
			wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
		{name: "越仓详情 → 404", method: http.MethodGet,
			targetF:  func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) },
			seed:     func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			user:     func() auth.UserContext { return whUser(999) },
			wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
	})
}

// ---- POST /api/outbounds/:no/picks 生成拣货任务 ----

func TestHTTPReleaseToPick(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/outbounds/:no/picks", hd.releaseToPick }, []httpCase{
		{name: "生成拣货任务 ALLOCATED→PICKING", method: http.MethodPost,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/picks" },
			seed: func(t *testing.T, h *harness) {
				h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
				h.submitAndApprove(t, skuPlain, qtyOf(2))
			},
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				if strAt(t, objAt(t, d, "outbound"), "status") != "PICKING" {
					t.Fatalf("出库单应 PICKING: %s", rec.Body.String())
				}
				picks := dataArr(t, env, "picks")
				if len(picks) != 1 {
					t.Fatalf("应生成 1 张任务: %s", rec.Body.String())
				}
				p := picks[0].(map[string]any)
				if numAt(t, p, "source_bin_id") != bin1 || numAt(t, p, "qty") != 2 ||
					!strings.HasPrefix(strAt(t, p, "pick_no"), "PK-") {
					t.Fatalf("任务内容不符（SKU→来源库位→数量）: %v", p)
				}
			}},
		{name: "重复生成 → 409", method: http.MethodPost,
			targetF:  func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/picks" },
			seed:     func(t *testing.T, h *harness) { h.seedPickable(t, skuPlain, qtyOf(2)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "出库单不存在 → 404", method: http.MethodPost, target: "/api/outbounds/OUT-NONE/picks",
			wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
	})
}

// ---- PUT /api/outbounds/:no/cancel 取消出库单 ----

func TestHTTPCancelOutbound(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/outbounds/:no/cancel", hd.cancelOutbound }, []httpCase{
		{name: "ALLOCATED 取消释放预占", method: http.MethodPut,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/cancel" },
			seed: func(t *testing.T, h *harness) {
				h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
				h.submitAndApprove(t, skuPlain, qtyOf(2))
			},
			body: map[string]any{"reason": "计划变更"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != "CANCELLED" {
					t.Fatalf("应 CANCELLED: %s", rec.Body.String())
				}
				if _, locked, _ := h.stock.Snapshot(wh1, bin1, skuPlain, 0); locked != 0 {
					t.Fatalf("取消后锁应释放")
				}
			}},
		{name: "PICKING 取消联动取消任务（空体允许）", method: http.MethodPut,
			targetF:  func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/cancel" },
			seed:     func(t *testing.T, h *harness) { h.seedPickable(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				picks, _ := h.repo.ListPickTasksByOutbound(nil, firstOBNo(h))
				for _, p := range picks {
					if p.Status != PickStatusCancelled {
						t.Fatalf("任务应联动取消: %+v", p)
					}
				}
				if items := h.repo.soItems[firstSOID(h)]; items[0].QtyAllocated != 0 {
					t.Fatalf("销售订单行预占应归零: %s", items[0].QtyAllocated)
				}
			}},
		{name: "已部分发货不可取消 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/cancel" },
			seed:    func(t *testing.T, h *harness) { h.seedPartialShipped(t) },
			body:    map[string]any{"reason": "x"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "出库单不存在 → 404", method: http.MethodPut, target: "/api/outbounds/OUT-NONE/cancel",
			wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
	})
}

// ---- PUT /api/outbounds/:no/close 出库单差额关闭 ----

func TestHTTPCloseOutbound(t *testing.T) {
	seedAllocated := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
		h.submitAndApprove(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/outbounds/:no/close", hd.closeOutbound }, []httpCase{
		{name: "缺原因 → 400 SALES_CLOSE_REASON_REQUIRED", method: http.MethodPut,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/close" },
			seed:    seedAllocated,
			body:    map[string]any{"reason": ""}, wantHTTP: 400, wantCode: "SALES_CLOSE_REASON_REQUIRED"},
		{name: "未发货不可关闭 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/close" },
			seed:    seedAllocated,
			body:    map[string]any{"reason": "x"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "部分发货差额关闭 → CLOSED + 剩余锁释放", method: http.MethodPut,
			targetF: func(h *harness) string { return "/api/outbounds/" + firstOBNo(h) + "/close" },
			seed:    func(t *testing.T, h *harness) { h.seedPartialShipped(t) },
			body:    map[string]any{"reason": "余量客户不要了"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != "CLOSED" {
					t.Fatalf("应 CLOSED: %s", rec.Body.String())
				}
				if _, locked, _ := h.stock.Snapshot(wh1, bin2, skuFIFO, 4001); locked != 0 {
					t.Fatalf("关闭后剩余锁应释放")
				}
			}},
		{name: "出库单不存在 → 404", method: http.MethodPut, target: "/api/outbounds/OUT-NONE/close",
			body: map[string]any{"reason": "x"}, wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
	})
}

// ---- GET /api/allocations 分配记录列表 ----

func TestHTTPListAllocations(t *testing.T) {
	seedAlloc := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(2), 1, 11)
		h.submitAndApprove(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/allocations", hd.listAllocations }, []httpCase{
		{name: "全仓可见分配记录", method: http.MethodGet, target: "/api/allocations",
			seed: seedAlloc, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 || len(items) != 1 {
					t.Fatalf("应见 1 条分配: %s", rec.Body.String())
				}
				a := items[0].(map[string]any)
				if !strings.HasPrefix(strAt(t, a, "outbound_no"), "OUT-") ||
					strAt(t, a, "strategy") != AllocStrategyFIFO || numAt(t, a, "qty") != 2 {
					t.Fatalf("分配记录不符: %v", a)
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/allocations",
			seed: seedAlloc, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见: %v", got)
				}
			}},
		{name: "非法 sku_id → 400", method: http.MethodGet, target: "/api/allocations?sku_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- POST /api/allocations 重新分配 ----

func TestHTTPReallocate(t *testing.T) {
	seedTwoBins := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedStock(wh1, bin1, skuPlain, 0, qtyOf(4), 1, 11)
		h.seedStock(wh1, bin2, skuPlain, 0, qtyOf(6), 1, 12)
		h.submitAndApprove(t, skuPlain, qtyOf(5)) // bin1(4) + bin2(1)
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/allocations", hd.reallocate }, []httpCase{
		{name: "重分配整组替换（旧锁释放+新锁产生）", method: http.MethodPost, target: "/api/allocations",
			seed: func(t *testing.T, h *harness) {
				seedTwoBins(t, h)
				h.repo.binStock[binKey(wh1, skuPlain, 0)] = []BinStock{{ // bin1 库存"消失"
					WarehouseID: wh1, ZoneID: 1, ShelfID: 12, BinID: bin2, SKUID: skuPlain, AvailableQty: qtyOf(6),
				}}
			},
			bodyF:    func(h *harness) any { return map[string]any{"outbound_no": firstOBNo(h)} },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				allocs := dataArr(t, env, "allocations")
				if len(allocs) != 1 {
					t.Fatalf("应整组替换为 1 条: %s", rec.Body.String())
				}
				a := allocs[0].(map[string]any)
				if numAt(t, a, "bin_id") != bin2 || numAt(t, a, "qty") != 5 {
					t.Fatalf("应全部落 bin2×5: %v", a)
				}
				if _, locked, _ := h.stock.Snapshot(wh1, bin2, skuPlain, 0); locked != qtyOf(5) {
					t.Fatalf("新锁量不符")
				}
			}},
		{name: "缺 outbound_no → 400", method: http.MethodPost, target: "/api/allocations",
			body: map[string]any{}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "出库单不存在 → 404", method: http.MethodPost, target: "/api/allocations",
			body: map[string]any{"outbound_no": "OUT-NONE"}, wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
		{name: "已拣货行 → 409 SALES_REALLOC_FORBIDDEN", method: http.MethodPost, target: "/api/allocations",
			seed:     func(t *testing.T, h *harness) { h.seedPickedLine1(t) },
			bodyF:    func(h *harness) any { return map[string]any{"outbound_no": firstOBNo(h), "line_no": 1} },
			wantHTTP: 409, wantCode: "SALES_REALLOC_FORBIDDEN"},
	})
}

// ---- GET /api/picks 拣货任务列表 ----

func TestHTTPListPicks(t *testing.T) {
	seedPick := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedPickable(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/picks", hd.listPicks }, []httpCase{
		{name: "全仓可见拣货任务", method: http.MethodGet, target: "/api/picks",
			seed: seedPick, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 || len(items) != 1 {
					t.Fatalf("应见 1 张任务: %s", rec.Body.String())
				}
				p := items[0].(map[string]any)
				if !strings.HasPrefix(strAt(t, p, "pick_no"), "PK-") ||
					strAt(t, p, "status") != PickStatusPending || numAt(t, p, "qty") != 2 {
					t.Fatalf("任务数据不符: %v", p)
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/picks",
			seed: seedPick, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见: %v", got)
				}
			}},
		{name: "非法 assignee_id → 400", method: http.MethodGet, target: "/api/picks?assignee_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/picks/:id/claim 领取（原子抢占） ----

func TestHTTPClaimPick(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/picks/:id/claim", hd.claimPick }, []httpCase{
		{name: "领取成功写入指派", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/picks/%d/claim", firstPickID(h)) },
			seed:     func(t *testing.T, h *harness) { h.seedPickable(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				p := dataObj(t, env)
				if strAt(t, p, "status") != PickStatusClaimed ||
					numAt(t, p, "assignee_id") != 9 || strAt(t, p, "assignee_name") != "super" {
					t.Fatalf("领取回执不符: %s", rec.Body.String())
				}
			}},
		{name: "二次领取 → 409 SALES_CLAIM_CONFLICT", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/claim", firstPickID(h)) },
			seed: func(t *testing.T, h *harness) {
				_, picks := h.seedPickable(t, skuPlain, qtyOf(2))
				if _, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: 6, Name: "first"}, picks[0].ID.Int64()); err != nil {
					t.Fatalf("首次领取失败: %v", err)
				}
			},
			wantHTTP: 409, wantCode: "SALES_CLAIM_CONFLICT"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/picks/424242/claim",
			wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
		{name: "非法路径 id → 400", method: http.MethodPut, target: "/api/picks/abc/claim",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/picks/:id/confirm 拣货确认 ----

func TestHTTPConfirmPick(t *testing.T) {
	claimed := func(t *testing.T, h *harness) {
		t.Helper()
		_, picks := h.seedPickable(t, skuPlain, qtyOf(2))
		if _, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: 7}, picks[0].ID.Int64()); err != nil {
			t.Fatalf("领取失败: %v", err)
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/picks/:id/confirm", hd.confirmPick }, []httpCase{
		{name: "确认成功并联动复核任务", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/confirm", firstPickID(h)) },
			seed:    claimed,
			body:    map[string]any{"picked_qty": 2, "scanned_code": "6901234567890"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				p := dataObj(t, env)
				if strAt(t, p, "status") != PickStatusPicked || numAt(t, p, "picked_qty") != 2 ||
					strAt(t, p, "scanned_code") != "6901234567890" {
					t.Fatalf("确认回执不符: %s", rec.Body.String())
				}
				no := strAt(t, p, "outbound_no")
				checks, _ := h.repo.ListCheckTasksByOutbound(nil, no)
				if len(checks) != 1 {
					t.Fatalf("应联动创建复核任务: %d", len(checks))
				}
				if ob, _ := h.repo.GetOutboundOrderByNo(nil, no); ob.Status != OBStatusPicked {
					t.Fatalf("出库单应 PICKED: %s", ob.Status)
				}
			}},
		{name: "数量为 0 → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/confirm", firstPickID(h)) },
			seed:    claimed,
			body:    map[string]any{"picked_qty": 0}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "超任务量 → 400 SALES_PICK_EXCEED", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/confirm", firstPickID(h)) },
			seed:    claimed,
			body:    map[string]any{"picked_qty": 3}, wantHTTP: 400, wantCode: "SALES_PICK_EXCEED"},
		{name: "未领取先确认 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/confirm", firstPickID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPickable(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"picked_qty": 2}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "非序列号 SKU 带序列号 → 400", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/picks/%d/confirm", firstPickID(h)) },
			seed:     claimed,
			body:     map[string]any{"picked_qty": 2, "serials": []string{"SN-1"}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "绑定失败 → 400", method: http.MethodPut, target: "/api/picks/1/confirm",
			rawBody: `{"picked_qty":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/picks/424242/confirm",
			body: map[string]any{"picked_qty": 1}, wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
	})
}

// ---- PUT /api/picks/:id/exception 异常上报 ----

func TestHTTPReportPickException(t *testing.T) {
	claimed := func(t *testing.T, h *harness) {
		t.Helper()
		_, picks := h.seedPickable(t, skuPlain, qtyOf(2))
		if _, err := h.svc.ClaimPickTask(h.ctx, Actor{ID: 7}, picks[0].ID.Int64()); err != nil {
			t.Fatalf("领取失败: %v", err)
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/picks/:id/exception", hd.reportPickException }, []httpCase{
		{name: "上报成功 → EXCEPTION + 异常中心登记", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/exception", firstPickID(h)) },
			seed:    claimed,
			body:    map[string]any{"reason": "库位实际无货"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != PickStatusException {
					t.Fatalf("任务应 EXCEPTION: %s", rec.Body.String())
				}
				if len(h.exc.rows) != 1 {
					t.Fatalf("异常中心未登记: %+v", h.exc.rows)
				}
			}},
		{name: "缺原因 → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/exception", firstPickID(h)) },
			seed:    claimed, body: map[string]any{}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "未领取任务 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/picks/%d/exception", firstPickID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPickable(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"reason": "缺货"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/picks/424242/exception",
			body: map[string]any{"reason": "缺货"}, wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
	})
}

// ---- GET /api/checks 复核任务列表 ----

func TestHTTPListChecks(t *testing.T) {
	seedCheck := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedPicked(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/checks", hd.listChecks }, []httpCase{
		{name: "全仓可见复核任务", method: http.MethodGet, target: "/api/checks",
			seed: seedCheck, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 || len(items) != 1 {
					t.Fatalf("应见 1 张任务: %s", rec.Body.String())
				}
				c := items[0].(map[string]any)
				if !strings.HasPrefix(strAt(t, c, "check_no"), "CH-") ||
					strAt(t, c, "status") != CheckStatusPending || numAt(t, c, "qty") != 2 {
					t.Fatalf("复核任务数据不符: %v", c)
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/checks",
			seed: seedCheck, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见: %v", got)
				}
			}},
		{name: "非法 assignee_id → 400", method: http.MethodGet, target: "/api/checks?assignee_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- PUT /api/checks/:id/claim 复核领取（原子指派，不迁移状态） ----

func TestHTTPClaimCheck(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/checks/:id/claim", hd.claimCheck }, []httpCase{
		{name: "指派成功且不迁移状态", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/checks/%d/claim", firstCheckID(h)) },
			seed:     func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				c := dataObj(t, env)
				if strAt(t, c, "status") != CheckStatusPending ||
					numAt(t, c, "assignee_id") != 9 || strAt(t, c, "assignee_name") != "super" {
					t.Fatalf("指派回执不符（值域无 CLAIMED，状态应保持 PENDING）: %s", rec.Body.String())
				}
			}},
		{name: "已完结任务再领取 → 409 SALES_CLAIM_CONFLICT", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/claim", firstCheckID(h)) },
			seed: func(t *testing.T, h *harness) {
				_, checks := h.seedPicked(t, skuPlain, qtyOf(2))
				if _, err := h.svc.ConfirmCheck(h.ctx, Actor{ID: 6}, checks[0].ID.Int64(), CheckConfirmInput{Pass: true}); err != nil {
					t.Fatalf("复核确认失败: %v", err)
				}
			},
			wantHTTP: 409, wantCode: "SALES_CLAIM_CONFLICT"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/checks/424242/claim",
			wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
	})
}

// ---- PUT /api/checks/:id/confirm 复核确认（通过/五类异常） ----

func TestHTTPConfirmCheck(t *testing.T) {
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/checks/:id/confirm", hd.confirmCheck }, []httpCase{
		{name: "通过 → DONE + 出库单 CHECKED", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/confirm", firstCheckID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"pass": true}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				c := dataObj(t, env)
				if strAt(t, c, "status") != CheckStatusDone {
					t.Fatalf("任务应 DONE: %s", rec.Body.String())
				}
				if ob, _ := h.repo.GetOutboundOrderByNo(nil, strAt(t, c, "outbound_no")); ob.Status != OBStatusChecked {
					t.Fatalf("出库单应 CHECKED: %s", ob.Status)
				}
			}},
		{name: "异常（少货）→ EXCEPTION + 异常中心", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/confirm", firstCheckID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"pass": false, "result": "少货"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				c := dataObj(t, env)
				if strAt(t, c, "status") != CheckStatusException || strAt(t, c, "result") != "少货" {
					t.Fatalf("异常确认回执不符: %s", rec.Body.String())
				}
				if len(h.exc.rows) != 1 {
					t.Fatalf("异常中心未登记: %+v", h.exc.rows)
				}
			}},
		{name: "非法异常类型 → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/confirm", firstCheckID(h)) },
			seed:    func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			body:    map[string]any{"pass": false, "result": "其他"}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "重复确认 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/confirm", firstCheckID(h)) },
			seed: func(t *testing.T, h *harness) {
				_, checks := h.seedPicked(t, skuPlain, qtyOf(2))
				if _, err := h.svc.ConfirmCheck(h.ctx, Actor{ID: 6}, checks[0].ID.Int64(), CheckConfirmInput{Pass: true}); err != nil {
					t.Fatalf("首次确认失败: %v", err)
				}
			},
			body: map[string]any{"pass": true}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "绑定失败 → 400", method: http.MethodPut, target: "/api/checks/1/confirm",
			rawBody: `{"pass":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/checks/424242/confirm",
			body: map[string]any{"pass": true}, wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
	})
}

// ---- PUT /api/checks/:id/reopen 复核异常重开 ----

func TestHTTPReopenCheck(t *testing.T) {
	seedException := func(t *testing.T, h *harness) {
		t.Helper()
		_, checks := h.seedPicked(t, skuPlain, qtyOf(2))
		if _, err := h.svc.ConfirmCheck(h.ctx, Actor{ID: 6}, checks[0].ID.Int64(),
			CheckConfirmInput{Pass: false, Result: "少货"}); err != nil {
			t.Fatalf("异常上报失败: %v", err)
		}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/checks/:id/reopen", hd.reopenCheck }, []httpCase{
		{name: "异常任务重开 → PENDING", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/checks/%d/reopen", firstCheckID(h)) },
			seed:    seedException, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				c := dataObj(t, env)
				if strAt(t, c, "status") != CheckStatusPending || strAt(t, c, "result") != "" {
					t.Fatalf("重开后应 PENDING 且 result 清空: %s", rec.Body.String())
				}
			}},
		{name: "非异常任务重开 → 409", method: http.MethodPut,
			targetF:  func(h *harness) string { return fmt.Sprintf("/api/checks/%d/reopen", firstCheckID(h)) },
			seed:     func(t *testing.T, h *harness) { h.seedPicked(t, skuPlain, qtyOf(2)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "任务不存在 → 404", method: http.MethodPut, target: "/api/checks/424242/reopen",
			wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
	})
}

// ---- GET /api/packing 打包记录列表 ----

func TestHTTPListPacking(t *testing.T) {
	seedPack := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedPacked(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/packing", hd.listPacking }, []httpCase{
		{name: "全仓可见打包记录", method: http.MethodGet, target: "/api/packing",
			seed: seedPack, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 || len(items) != 1 {
					t.Fatalf("应见 1 条包裹: %s", rec.Body.String())
				}
				if !strings.HasPrefix(strAt(t, items[0].(map[string]any), "package_no"), "BP-") {
					t.Fatalf("包裹号不符: %s", rec.Body.String())
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/packing",
			seed: seedPack, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见: %v", got)
				}
			}},
		{name: "非法 warehouse_id → 400", method: http.MethodGet, target: "/api/packing?warehouse_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- POST /api/packing 打包 ----

func TestHTTPPack(t *testing.T) {
	seedCheckedEnv := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedChecked(t, skuPlain, qtyOf(2))
	}
	packBody := func(no string) map[string]any {
		return map[string]any{"outbound_no": no, "packing_material": "纸箱",
			"lines": []map[string]any{{"line_no": 1, "qty": 2}}}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/packing", hd.pack }, []httpCase{
		{name: "打包成功 CHECKED→PACKED", method: http.MethodPost, target: "/api/packing",
			seed:     seedCheckedEnv,
			bodyF:    func(h *harness) any { return packBody(firstOBNo(h)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				p := objAt(t, dataObj(t, env), "package")
				if !strings.HasPrefix(strAt(t, p, "package_no"), "BP-") ||
					strAt(t, p, "outbound_no") != firstOBNo(h) || strAt(t, p, "packing_material") != "纸箱" {
					t.Fatalf("包裹数据不符: %s", rec.Body.String())
				}
				if ob, _ := h.repo.GetOutboundOrderByNo(nil, firstOBNo(h)); ob.Status != OBStatusPacked {
					t.Fatalf("出库单应 PACKED: %s", ob.Status)
				}
			}},
		{name: "缺 outbound_no → 400", method: http.MethodPost, target: "/api/packing",
			seed: seedCheckedEnv, body: map[string]any{"lines": []map[string]any{{"line_no": 1, "qty": 2}}},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "缺 lines → 400", method: http.MethodPost, target: "/api/packing",
			seed: seedCheckedEnv, body: map[string]any{"outbound_no": "OUT"},
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "超量打包 → 400 SALES_PACK_EXCEED", method: http.MethodPost, target: "/api/packing",
			seed: seedCheckedEnv,
			bodyF: func(h *harness) any {
				return map[string]any{"outbound_no": firstOBNo(h),
					"lines": []map[string]any{{"line_no": 1, "qty": 3}}}
			},
			wantHTTP: 400, wantCode: "SALES_PACK_EXCEED"},
		{name: "非 CHECKED 状态 → 409", method: http.MethodPost, target: "/api/packing",
			seed:     func(t *testing.T, h *harness) { h.seedPacked(t, skuPlain, qtyOf(2)) },
			bodyF:    func(h *harness) any { return packBody(firstOBNo(h)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "绑定失败 → 400", method: http.MethodPost, target: "/api/packing",
			rawBody: `{"outbound_no":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
	// Idempotency-Key 头回填（handler.go:730-732）→ 幂等重放，需同会话两次请求独立驱动。
	t.Run("Idempotency-Key 头幂等重放", func(t *testing.T) {
		h, hd := newHandlerEnv(t)
		seedCheckedEnv(t, h)
		no := firstOBNo(h)
		hdr := map[string]string{"Idempotency-Key": "bp-http-1"}
		rec1, env1 := driveHTTP(t, hd.pack, http.MethodPost, "/api/packing", "/api/packing", packBody(no), "", hdr, superUser())
		if rec1.Code != 200 {
			t.Fatalf("首次打包失败: %s", rec1.Body.String())
		}
		rec2, env2 := driveHTTP(t, hd.pack, http.MethodPost, "/api/packing", "/api/packing", packBody(no), "", hdr, superUser())
		if rec2.Code != 200 {
			t.Fatalf("重放失败: %s", rec2.Body.String())
		}
		if dataObj(t, env2)["replay"] != true {
			t.Fatalf("同键重放应 replay=true: %s", rec2.Body.String())
		}
		no1 := strAt(t, objAt(t, dataObj(t, env1), "package"), "package_no")
		no2 := strAt(t, objAt(t, dataObj(t, env2), "package"), "package_no")
		if no1 != no2 {
			t.Fatalf("重放应返回既有包裹: %s vs %s", no1, no2)
		}
	})
}

// ---- GET /api/shipments 发货单列表 ----

func TestHTTPListShipments(t *testing.T) {
	seedShip := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedShipped(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/shipments", hd.listShipments }, []httpCase{
		{name: "全仓可见发货单", method: http.MethodGet, target: "/api/shipments",
			seed: seedShip, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				items := dataArr(t, env, "items")
				if numAt(t, dataObj(t, env), "total") != 1 || len(items) != 1 {
					t.Fatalf("应见 1 张发货单: %s", rec.Body.String())
				}
				s := items[0].(map[string]any)
				if !strings.HasPrefix(strAt(t, s, "shipment_no"), "SH-") ||
					strAt(t, s, "status") != ShipStatusShipped || strAt(t, s, "carrier") != "SF" {
					t.Fatalf("发货单数据不符: %v", s)
				}
			}},
		{name: "越仓范围不可见", method: http.MethodGet, target: "/api/shipments",
			seed: seedShip, user: func() auth.UserContext { return whUser(999) }, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if got := numAt(t, dataObj(t, env), "total"); got != 0 {
					t.Fatalf("越仓应不可见: %v", got)
				}
			}},
		{name: "非法 warehouse_id → 400", method: http.MethodGet, target: "/api/shipments?warehouse_id=abc",
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}

// ---- POST /api/shipments 发货确认 ----

func TestHTTPShip(t *testing.T) {
	seedPackedEnv := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedPacked(t, skuPlain, qtyOf(2))
	}
	shipBody := func(no string) map[string]any {
		return map[string]any{"outbound_no": no, "carrier": "SF", "tracking_no": "SF123"}
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) { return "/api/shipments", hd.ship }, []httpCase{
		{name: "发货成功核销预占（Deduct）", method: http.MethodPost, target: "/api/shipments",
			seed:     seedPackedEnv,
			bodyF:    func(h *harness) any { return shipBody(firstOBNo(h)) },
			wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				d := dataObj(t, env)
				if d["replay"] != false {
					t.Fatalf("首次发货不应是重放: %s", rec.Body.String())
				}
				sh := objAt(t, d, "shipment")
				if !strings.HasPrefix(strAt(t, sh, "shipment_no"), "SH-") || strAt(t, sh, "status") != ShipStatusShipped {
					t.Fatalf("发货单应 SHIPPED: %s", rec.Body.String())
				}
				if numAt(t, objAt(t, d, "shipped"), "1") != 2 {
					t.Fatalf("行 1 应发货 2: %v", d["shipped"])
				}
				if _, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0); locked != 0 || total != 0 {
					t.Fatalf("Deduct 应核销预占")
				}
				if ob, _ := h.repo.GetOutboundOrderByNo(nil, firstOBNo(h)); ob.Status != OBStatusShippedAll {
					t.Fatalf("出库单应 SHIPPED_ALL: %s", ob.Status)
				}
				if so := h.repo.soRows[firstSOID(h)]; so.Status != SOStatusShippedAll {
					t.Fatalf("销售订单应 SHIPPED_ALL: %s", so.Status)
				}
			}},
		{name: "缺 outbound_no → 400", method: http.MethodPost, target: "/api/shipments",
			body: map[string]any{"carrier": "SF"}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "未打包不可发货 → 409", method: http.MethodPost, target: "/api/shipments",
			seed: func(t *testing.T, h *harness) {
				h.seedChecked(t, skuPlain, qtyOf(2))
			},
			bodyF:    func(h *harness) any { return shipBody(firstOBNo(h)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "出库单不存在 → 404", method: http.MethodPost, target: "/api/shipments",
			body: map[string]any{"outbound_no": "OUT-NONE"}, wantHTTP: 404, wantCode: "SALES_OUTBOUND_NOT_FOUND"},
		{name: "行号不存在 → 400 SALES_SHIP_QTY_INVALID", method: http.MethodPost, target: "/api/shipments",
			seed: seedPackedEnv,
			bodyF: func(h *harness) any {
				return map[string]any{"outbound_no": firstOBNo(h), "lines": []map[string]any{{"line_no": 9}}}
			},
			wantHTTP: 400, wantCode: "SALES_SHIP_QTY_INVALID"},
		{name: "重复发货（无幂等键）→ 409 状态守卫", method: http.MethodPost, target: "/api/shipments",
			seed:     func(t *testing.T, h *harness) { h.seedShipped(t, skuPlain, qtyOf(2)) },
			bodyF:    func(h *harness) any { return shipBody(firstOBNo(h)) },
			wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT",
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if _, locked, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0); locked != 0 || total != 0 {
					t.Fatalf("重复发货库存不应变化")
				}
			}},
		{name: "绑定失败 → 400", method: http.MethodPost, target: "/api/shipments",
			rawBody: `{"outbound_no":`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
	// Idempotency-Key 头回填（handler.go:783-785）→ 幂等重放，需同会话两次请求独立驱动。
	t.Run("Idempotency-Key 头幂等重放", func(t *testing.T) {
		h, hd := newHandlerEnv(t)
		seedPackedEnv(t, h)
		no := firstOBNo(h)
		hdr := map[string]string{"Idempotency-Key": "ship-http-1"}
		rec1, env1 := driveHTTP(t, hd.ship, http.MethodPost, "/api/shipments", "/api/shipments", shipBody(no), "", hdr, superUser())
		if rec1.Code != 200 {
			t.Fatalf("首次发货失败: %s", rec1.Body.String())
		}
		rec2, env2 := driveHTTP(t, hd.ship, http.MethodPost, "/api/shipments", "/api/shipments", shipBody(no), "", hdr, superUser())
		if rec2.Code != 200 || dataObj(t, env2)["replay"] != true {
			t.Fatalf("同键重放应 replay=true: %d %s", rec2.Code, rec2.Body.String())
		}
		no1 := strAt(t, objAt(t, dataObj(t, env1), "shipment"), "shipment_no")
		no2 := strAt(t, objAt(t, dataObj(t, env2), "shipment"), "shipment_no")
		if no1 != no2 {
			t.Fatalf("重放应返回既有发货单: %s vs %s", no1, no2)
		}
		if _, _, total := h.stock.Snapshot(wh1, bin1, skuPlain, 0); total != 0 {
			t.Fatalf("重放不应二次扣减")
		}
	})
}

// ---- PUT /api/shipments/:id/status 物流态流转 ----

func TestHTTPUpdateShipmentStatus(t *testing.T) {
	seedShippedEnv := func(t *testing.T, h *harness) {
		t.Helper()
		h.seedShipped(t, skuPlain, qtyOf(2))
	}
	runHTTP(t, func(hd *handler) (string, gin.HandlerFunc) {
		return "/api/shipments/:id/status", hd.updateShipmentStatus
	}, []httpCase{
		{name: "SHIPPED→IN_TRANSIT 流转成功", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/shipments/%d/status", firstShipID(h)) },
			seed:    seedShippedEnv,
			body:    map[string]any{"status": "IN_TRANSIT", "remark": "已发出"}, wantHTTP: 200,
			check: func(t *testing.T, h *harness, rec *httptest.ResponseRecorder, env map[string]any) {
				if strAt(t, dataObj(t, env), "status") != ShipStatusInTransit {
					t.Fatalf("应 IN_TRANSIT: %s", rec.Body.String())
				}
			}},
		{name: "SHIPPED→SIGNED 非法迁移 → 409", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/shipments/%d/status", firstShipID(h)) },
			seed:    seedShippedEnv,
			body:    map[string]any{"status": "SIGNED"}, wantHTTP: 409, wantCode: "SALES_STATE_CONFLICT"},
		{name: "非法目标态 → 400", method: http.MethodPut,
			targetF: func(h *harness) string { return fmt.Sprintf("/api/shipments/%d/status", firstShipID(h)) },
			seed:    seedShippedEnv,
			body:    map[string]any{"status": "PENDING"}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "发货单不存在 → 404", method: http.MethodPut, target: "/api/shipments/424242/status",
			body: map[string]any{"status": "IN_TRANSIT"}, wantHTTP: 404, wantCode: "SALES_TASK_NOT_FOUND"},
		{name: "非法路径 id → 400", method: http.MethodPut, target: "/api/shipments/abc/status",
			body: map[string]any{"status": "IN_TRANSIT"}, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	})
}
