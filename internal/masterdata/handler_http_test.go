package masterdata

// HTTP handler 层数据驱动表测试（ask：34 条接口逐一覆盖参数绑定/校验失败/成功路径/
// 可测错误分支；不依赖 PostgreSQL/Redis，数据访问走 fakeRepo 内存仓储 + 假 gorm 事务，
// 与 service_test.go 同一 fake 基建，非第二套机制）。
//
// 测试通路：gin.TestMode 引擎直接挂载包内 handler 纯函数（handler.go:15 文件注——
// "handler 均为纯函数 (c, svc)"），不经 auth.RequirePermission：权限点挂载集合由
// routes_test.go 契约测试锁定，RequirePermission 的鉴权行为（401/403/fail-closed）
// 属 auth 域职责且依赖 auth 包级装配 snapshotWired（internal/auth/config.go:205，
// 装配函数 setWiredForTest 为 auth 包内私有，跨包不可达）——故本文件的"权限分支"
// 口径为：操作者归因链路（actorOf）与未装配用户上下文时的零值行为。
//
// 用户上下文注入：auth.CurrentUser 只认 gin 键 "sf_auth_user"（internal/auth/
// middleware.go:26 ctxUserKey，包内私有常量，plan §5.1 冻结"跨包只经 CurrentUser
// 取值"）。本包测试以同字面量镜像注入（ctxUserKeyMirror）；若 auth 改键名，归因
// 断言将响亮失败而非静默放过。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

const (
	// testRequestID 注入每个请求的 request_id（真实路径：middleware → response 信封回显）。
	testRequestID = "req-http-test"
	// testUserAgent do() 统一设置的 UA（actorOf 归因断言用）。
	testUserAgent = "unit-http"
)

// ctxUserKeyMirror 见文件头注（镜像 internal/auth/middleware.go:26 ctxUserKey）。
const ctxUserKeyMirror = "sf_auth_user"

// ---- 测试支撑 ----

type handlerHarness struct {
	t      *testing.T
	engine *gin.Engine
	svc    *Service
	repo   *fakeRepo
	spy    *auditSpy
	user   *auth.UserContext // 非 nil 时挂载 handler 前 c.Set 注入（走 actorOf 真实提取路径）
}

func newHandlerTest(t *testing.T) *handlerHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	spy := &auditSpy{}
	repo := newFakeRepo(spy)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set(response.RequestIDKey, testRequestID)
		c.Next()
	})
	return &handlerHarness{t: t, engine: e, svc: NewService(repo), repo: repo, spy: spy}
}

// mountHTTP 以真实路径前缀挂载 handler（路由集合本身由 routes_test.go 契约锁定，
// 此处只需让请求命中对应 handler）。
func (h *handlerHarness) mountHTTP(method, route string, handle func(*gin.Context, *Service)) {
	h.t.Helper()
	h.engine.Handle(method, route, func(c *gin.Context) {
		if h.user != nil {
			c.Set(ctxUserKeyMirror, *h.user)
		}
		handle(c, h.svc)
	})
}

// do 发送请求并解析统一信封（api.md §2.2：{code,message,data,request_id}）。
func (h *handlerHarness) do(method, path, body string) (int, map[string]any) {
	h.t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", testUserAgent)
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		h.t.Fatalf("响应不是合法 JSON 信封: %v; http=%d body=%s", err, w.Code, w.Body.String())
	}
	return w.Code, env
}

// httpCase 单用例：wantCode 成功=0（信封数字），失败=错误码字符串；check 做细粒度断言；
// setup 在请求前执行（切换 Service 注入态，如删除引用读取器）。位置初始化时尾部
// 可选函数依序为 check、setup——带 setup 的用例一律用字段名初始化。
type httpCase struct {
	name       string
	method     string
	path       string
	body       string
	wantStatus int
	wantCode   any
	check      func(t *testing.T, env map[string]any)
	setup      func(t *testing.T, h *handlerHarness)
}

// run 顺序执行用例（共享 repo，顺序在每组用例注释中声明；不用 t.Parallel）。
func (h *handlerHarness) run(cases []httpCase) {
	h.t.Helper()
	for _, tc := range cases {
		tc := tc
		h.t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t, h)
			}
			status, env := h.do(tc.method, tc.path, tc.body)
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
	require.IsType(t, []any{}, data["items"], "items 应为数组（api.md §2.1 分页信封）")
	return data["items"].([]any)
}

func detailsOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	require.IsType(t, map[string]any{}, env["details"], "失败必须带 details（api.md §4）")
	return env["details"].(map[string]any)
}

func fieldOf(t *testing.T, env map[string]any, field string) {
	t.Helper()
	require.Equal(t, field, detailsOf(t, env)["field"], "details.field 不符")
}

func fieldsOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	details := detailsOf(t, env)
	require.IsType(t, map[string]any{}, details["fields"], "bind 校验失败 details.fields 应为对象")
	return details["fields"].(map[string]any)
}

// bindReasonOK 断言 bindJSON 收敛文案（internal/response/errors.go:135 BindErrorReason）。
func bindReasonOK(t *testing.T, env map[string]any) {
	require.Equal(t, "请求体格式错误", detailsOf(t, env)["reason"])
}

// findProductID 按编码反查商品行 ID（seq 为各 seed 共享计数器，测试不按序号猜 ID）。
func findProductID(t *testing.T, repo *fakeRepo, code string) int64 {
	t.Helper()
	p, err := repo.FindProductByCode(t.Context(), code)
	require.NoError(t, err)
	require.NotNil(t, p, "种子商品 %s 缺失", code)
	return p.ID.Int64()
}

func findSKUID(t *testing.T, repo *fakeRepo, code string) int64 {
	t.Helper()
	s, err := repo.FindSKUByCode(t.Context(), code)
	require.NoError(t, err)
	require.NotNil(t, s, "种子 SKU %s 缺失", code)
	return s.ID.Int64()
}

// ---- 删除引用校验替身（refreaders.go 窄接口；Service 私有字段同包测试直接注入，
// 对齐 router 装配 RegisterRoutes(…, WithSupplierRefReader(…)) 的运行时形态）----

type stubSupplierRefs struct{ has bool }

func (s stubSupplierRefs) HasBusinessRecord(context.Context, int64) (bool, error) { return s.has, nil }

type stubCustomerRefs struct{ has bool }

func (s stubCustomerRefs) HasBusinessRecord(context.Context, int64) (bool, error) { return s.has, nil }

// ---- 商品：6 接口（POST/GET 列表/GET 详情/PUT/PUT status/DELETE）----

func TestHTTPProductEndpoints(t *testing.T) {
	h := newHandlerTest(t)
	h.user = &auth.UserContext{UserID: 9, Username: "http-user"}

	catID := h.repo.seedCategory("CAT-A", StatusEnabled, 0)
	unitID := h.repo.seedUnit("PCS-A", StatusEnabled)
	disabledCat := h.repo.seedCategory("CAT-X", StatusDisabled, 0)
	skuPID := h.repo.seedProduct("P-SKU", StatusEnabled, nil) // 停用级联/删除守卫用
	h.repo.seedSKU("SKU-C1", skuPID, true, nil)
	h.repo.seedSKU("SKU-C2", skuPID, true, nil)
	h.repo.seedSKU("SKU-C3", skuPID, false, nil)              // 已停用不计级联，但计删除引用
	delPID := h.repo.seedProduct("P-DEL", StatusEnabled, nil) // 删除成功用（无 SKU）

	h.mountHTTP("GET", "/api/products", handleProductList)
	h.mountHTTP("POST", "/api/products", handleProductCreate)
	h.mountHTTP("GET", "/api/products/:id", handleProductDetail)
	h.mountHTTP("PUT", "/api/products/:id", handleProductUpdate)
	h.mountHTTP("PUT", "/api/products/:id/status", handleProductStatus)
	h.mountHTTP("DELETE", "/api/products/:id", handleProductDelete)

	// 第一段：创建（成功+失败分支）与列表查询。
	h.run([]httpCase{
		{
			name: "创建商品全字段成功", method: "POST", path: "/api/products",
			body: `{"code":"P-H1","name":"矿泉水","short_name":"水","category_id":` + itoa64(catID) +
				`,"unit_id":` + itoa64(unitID) + `,"brand":"SF","weight":"0.5","image_urls":["http://img/1.png"],"remark":"主打"}`,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "P-H1", data["code"])
				require.Equal(t, StatusEnabled, data["status"], "创建恒为启用")
				require.Equal(t, itoa64(catID), data["category_id"], "ID JSON 为字符串形态（database.ID MarshalJSON）")
				require.Equal(t, 0.5, data["weight"], "Number 出参为裸数字")
				require.Equal(t, []any{"http://img/1.png"}, data["image_urls"])
				// 操作者归因链路：HTTP 用户上下文 → actorOf（handler.go:18-30）→ 审计行
				entries := h.spy.all()
				require.Len(t, entries, 1, "create 应落一条审计")
				require.EqualValues(t, 9, entries[0].UserID)
				require.Equal(t, "http-user", entries[0].Username)
				require.Equal(t, testRequestID, entries[0].RequestID)
				require.Equal(t, "POST", entries[0].Method)
				require.Equal(t, "/api/products", entries[0].Path)
				require.Equal(t, testUserAgent, entries[0].UserAgent)
				require.Equal(t, "192.0.2.1", entries[0].IP, "httptest.NewRequest 默认 RemoteAddr 归因")
			},
		},
		{
			name: "创建编码重复", method: "POST", path: "/api/products",
			body: `{"code":"P-H1","name":"重复"}`, wantStatus: 409, wantCode: "MASTERDATA_PRODUCT_CODE_EXISTS",
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "P-H1", detailsOf(t, env)["code"])
			},
		},
		{"创建请求体缺失", "POST", "/api/products", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{"创建请求体非法 JSON", "POST", "/api/products", "{bad", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{"创建重量非法字面量", "POST", "/api/products", `{"code":"P-H2","name":"x","weight":"abc"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{"创建引用停用分类", "POST", "/api/products",
			`{"code":"P-H3","name":"x","category_id":` + itoa64(disabledCat) + `}`, 409, "MASTERDATA_CATEGORY_DISABLED", nil, nil},
		{"创建负重量", "POST", "/api/products", `{"code":"P-H4","name":"x","weight":"-1"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "weight") }, nil},
		{
			name: "列表默认分页信封", method: "GET", path: "/api/products", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 1, data["page"], "缺省 page=1")
				require.EqualValues(t, 20, data["pageSize"], "缺省 pageSize=20")
				require.GreaterOrEqual(t, data["total"], float64(3), "种子商品数")
				require.NotEmpty(t, itemsOf(t, env))
			},
		},
		{
			name: "列表关键字过滤", method: "GET", path: "/api/products?keyword=P-DEL", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 1)
				require.Equal(t, "P-DEL", items[0].(map[string]any)["code"])
			},
		},
		{
			name: "列表分类与状态过滤", method: "GET",
			path:       "/api/products?category_id=" + itoa64(catID) + "&status=" + StatusEnabled,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["total"], "仅 P-H1 挂在 CAT-A")
			},
		},
		{"列表 category_id=0 边界视为不过滤", "GET", "/api/products?category_id=0", "", 200, 0,
			func(t *testing.T, env map[string]any) {
				require.GreaterOrEqual(t, dataOf(t, env)["total"], float64(3))
			}, nil},
		{"列表 category_id 非法", "GET", "/api/products?category_id=abc", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "category_id") }, nil},
		{"列表 category_id 负数", "GET", "/api/products?category_id=-1", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "category_id") }, nil},
		{"列表 page=0", "GET", "/api/products?page=0", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "page") }, nil},
		{"列表 pageSize 超上限", "GET", "/api/products?pageSize=101", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "pageSize") }, nil},
	})

	pid := findProductID(t, h.repo, "P-H1")

	// 第二段：详情/更新/启停/删除（消费第一段创建与预置种子行）。
	h.run([]httpCase{
		{
			name: "详情装配分类单位名称", method: "GET", path: "/api/products/" + itoa64(pid),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "分类CAT-A", data["category_name"], "详情才装配名称")
				require.Equal(t, "单位PCS-A", data["unit_name"])
			},
		},
		{"详情不存在", "GET", "/api/products/99999", "", 404, "MASTERDATA_PRODUCT_NOT_FOUND", nil, nil},
		{"详情 id=0", "GET", "/api/products/0", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) {
				fieldOf(t, env, "id")
				require.Equal(t, "必须为正整数", detailsOf(t, env)["reason"])
			}, nil},
		{"详情 id 非数字", "GET", "/api/products/abc", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "id") }, nil},
		{
			name: "更新商品名称与重量", method: "PUT", path: "/api/products/" + itoa64(pid),
			body: `{"name":"矿泉水(新)","weight":"1.5"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "矿泉水(新)", data["name"])
				require.Equal(t, 1.5, data["weight"])
			},
		},
		{"更新 id 非法（pathID 先于请求体绑定）", "PUT", "/api/products/-1", `{"name":"x"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "id") }, nil},
		{"更新请求体非法 JSON", "PUT", "/api/products/" + itoa64(pid), "{", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{
			name: "停用商品级联停用启用 SKU", method: "PUT",
			path: "/api/products/" + itoa64(skuPID) + "/status", body: `{"status":"DISABLED"}`,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, StatusDisabled, data["status"])
				require.EqualValues(t, 2, data["cascade_disabled_skus"], "仅启用中的 SKU 计级联")
			},
		},
		{"启停请求体缺 status", "PUT", "/api/products/" + itoa64(pid) + "/status", `{}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) {
				require.Equal(t, "必填", fieldsOf(t, env)["status"], "binding:required 收敛文案")
			}, nil},
		{"启停状态非法枚举", "PUT", "/api/products/" + itoa64(pid) + "/status", `{"status":"PAUSED"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "status") }, nil},
		{
			name: "删除被 SKU 引用阻止", method: "DELETE", path: "/api/products/" + itoa64(skuPID),
			wantStatus: 409, wantCode: "MASTERDATA_PRODUCT_HAS_SKU",
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 3, detailsOf(t, env)["sku_count"], "停用 SKU 仍计引用")
			},
		},
		{
			name: "删除成功返回 deleted", method: "DELETE", path: "/api/products/" + itoa64(delPID),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, true, dataOf(t, env)["deleted"])
			},
		},
		{"删除后详情不可见（软删）", "GET", "/api/products/" + itoa64(delPID), "", 404, "MASTERDATA_PRODUCT_NOT_FOUND", nil, nil},
		{"删除不存在", "DELETE", "/api/products/424242", "", 404, "MASTERDATA_PRODUCT_NOT_FOUND", nil, nil},
	})
}

// ---- SKU 与条码：6 接口 ----

func TestHTTPSKUEndpoints(t *testing.T) {
	h := newHandlerTest(t)
	pid := h.repo.seedProduct("P-S1", StatusEnabled, nil)
	other := h.repo.seedProduct("P-S2", StatusEnabled, nil)
	otherSKU := h.repo.seedSKU("SKU-OTH", other, true, nil)
	h.repo.seedBarcode(otherSKU, "TAKEN-1", false)
	h.repo.seedSKU("SKU-OFF", pid, false, nil) // 供删除用例

	h.mountHTTP("GET", "/api/skus", handleSKUList)
	h.mountHTTP("POST", "/api/skus", handleSKUCreate)
	h.mountHTTP("GET", "/api/skus/:id", handleSKUDetail)
	h.mountHTTP("PUT", "/api/skus/:id", handleSKUUpdate)
	h.mountHTTP("PUT", "/api/skus/:id/status", handleSKUStatus)
	h.mountHTTP("DELETE", "/api/skus/:id", handleSKUDelete)

	// 第一段：创建（成功+失败分支）与列表过滤。
	h.run([]httpCase{
		{
			name: "创建 SKU 带条码与规格", method: "POST", path: "/api/skus",
			body: `{"code":"SKU-H1","product_id":` + itoa64(pid) + `,"spec_attrs":{"color":"红"},` +
				`"cost_price":"2.5","sale_price":"3.8","safety_stock":"10","max_stock":"100",` +
				`"is_batch_managed":true,"is_expiry_managed":true,` +
				`"barcodes":[{"barcode":"6901234567892","is_primary":true}]}`,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "SKU-H1", data["code"])
				require.Equal(t, true, data["is_enabled"], "缺省启用")
				require.Equal(t, "红", data["spec_attrs"].(map[string]any)["color"])
				bcs := data["barcodes"].([]any)
				require.Len(t, bcs, 1)
				require.Equal(t, "6901234567892", bcs[0].(map[string]any)["barcode"])
			},
		},
		{"创建效期缺批次拒绝", "POST", "/api/skus",
			`{"code":"SKU-H2","product_id":` + itoa64(pid) + `,"is_expiry_managed":true}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "is_expiry_managed") }, nil},
		{"创建商品不存在", "POST", "/api/skus", `{"code":"SKU-H3","product_id":99999}`, 404, "MASTERDATA_PRODUCT_NOT_FOUND", nil, nil},
		{
			name: "创建条码被其他 SKU 占用", method: "POST", path: "/api/skus",
			body:       `{"code":"SKU-H4","product_id":` + itoa64(pid) + `,"barcodes":[{"barcode":"TAKEN-1"}]}`,
			wantStatus: 409, wantCode: "MASTERDATA_BARCODE_EXISTS",
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "TAKEN-1", detailsOf(t, env)["barcode"], "details 携带冲突条码（api.md §4）")
			},
		},
		{"创建请求体缺失", "POST", "/api/skus", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{
			name: "列表按商品过滤", method: "GET", path: "/api/skus?product_id=" + itoa64(pid),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 2, dataOf(t, env)["total"], "SKU-H1 + SKU-OFF")
			},
		},
		{
			name: "列表启用过滤", method: "GET",
			path: "/api/skus?product_id=" + itoa64(pid) + "&enabled=true", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["total"])
				require.Equal(t, "SKU-H1", itemsOf(t, env)[0].(map[string]any)["code"])
			},
		},
		{"列表 enabled 非布尔", "GET", "/api/skus?enabled=xyz", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "enabled") }, nil},
		{"列表 product_id 负数", "GET", "/api/skus?product_id=-2", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "product_id") }, nil},
	})

	sid := findSKUID(t, h.repo, "SKU-H1")
	delSID := findSKUID(t, h.repo, "SKU-OFF")

	// 第二段：详情/更新/启停/删除。
	h.run([]httpCase{
		{
			name: "详情装配商品与条码", method: "GET", path: "/api/skus/" + itoa64(sid),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "P-S1", data["product_code"])
				require.Len(t, data["barcodes"].([]any), 1)
			},
		},
		{"详情不存在", "GET", "/api/skus/99999", "", 404, "MASTERDATA_SKU_NOT_FOUND", nil, nil},
		{
			name: "更新售价", method: "PUT", path: "/api/skus/" + itoa64(sid),
			body: `{"sale_price":"9.9"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, 9.9, dataOf(t, env)["sale_price"])
			},
		},
		{"更新条码被占用", "PUT", "/api/skus/" + itoa64(sid),
			`{"barcodes":[{"barcode":"TAKEN-1"}]}`, 409, "MASTERDATA_BARCODE_EXISTS", nil, nil},
		{
			name: "启停 SKU 关闭", method: "PUT", path: "/api/skus/" + itoa64(sid) + "/status",
			body: `{"enabled":false}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, false, dataOf(t, env)["is_enabled"], "响应回显 is_enabled")
			},
		},
		{"启停请求体缺 enabled", "PUT", "/api/skus/" + itoa64(sid) + "/status", `{}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) {
				require.Equal(t, "必填", fieldsOf(t, env)["enabled"])
			}, nil},
		{"启停 enabled 非布尔", "PUT", "/api/skus/" + itoa64(sid) + "/status", `{"enabled":"yes"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{
			name: "删除 SKU", method: "DELETE", path: "/api/skus/" + itoa64(delSID),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, true, dataOf(t, env)["deleted"])
			},
		},
		{"删除不存在", "DELETE", "/api/skus/424242", "", 404, "MASTERDATA_SKU_NOT_FOUND", nil, nil},
	})
}

// ---- 商品分类：5 接口（无删除）----

func TestHTTPCategoryEndpoints(t *testing.T) {
	h := newHandlerTest(t)
	root := h.repo.seedCategory("CAT-R", StatusEnabled, 0)
	child := h.repo.seedCategory("CAT-C", StatusEnabled, root)
	h.repo.seedCategory("CAT-DIS", StatusDisabled, 0)

	h.mountHTTP("GET", "/api/product-categories", handleCategoryList)
	h.mountHTTP("POST", "/api/product-categories", handleCategoryCreate)
	h.mountHTTP("GET", "/api/product-categories/:id", handleCategoryDetail)
	h.mountHTTP("PUT", "/api/product-categories/:id", handleCategoryUpdate)
	h.mountHTTP("PUT", "/api/product-categories/:id/status", handleCategoryStatus)

	// 第一段：列表过滤（parent_id 三态 + 非法）与创建（成功+失败分支）。
	h.run([]httpCase{
		{
			name: "列表全部", method: "GET", path: "/api/product-categories", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 3, dataOf(t, env)["total"])
			},
		},
		{
			name: "列表 parent_id=0 只看顶级", method: "GET", path: "/api/product-categories?parent_id=0",
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 2, dataOf(t, env)["total"], "CAT-R + CAT-DIS")
				for _, it := range itemsOf(t, env) {
					require.Nil(t, it.(map[string]any)["parent_id"])
				}
			},
		},
		{
			name: "列表按上级过滤", method: "GET", path: "/api/product-categories?parent_id=" + itoa64(root),
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				items := itemsOf(t, env)
				require.Len(t, items, 1)
				require.Equal(t, "CAT-C", items[0].(map[string]any)["code"])
			},
		},
		{"列表 parent_id 非数字", "GET", "/api/product-categories?parent_id=abc", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "parent_id") }, nil},
		{"列表 parent_id 负数", "GET", "/api/product-categories?parent_id=-1", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "parent_id") }, nil},
		{
			name: "列表状态过滤", method: "GET", path: "/api/product-categories?status=" + StatusDisabled,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["total"])
			},
		},
		{
			name: "创建顶级分类", method: "POST", path: "/api/product-categories",
			body: `{"code":"CAT-H1","name":"家居"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, StatusEnabled, data["status"], "创建默认启用")
				require.Nil(t, data["parent_id"])
			},
		},
		{
			name: "创建子分类", method: "POST", path: "/api/product-categories",
			body: `{"parent_id":` + itoa64(root) + `,"code":"CAT-H2","name":"厨具"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, itoa64(root), dataOf(t, env)["parent_id"], "ID JSON 为字符串形态")
			},
		},
		{"创建编码重复", "POST", "/api/product-categories", `{"code":"CAT-R","name":"重复"}`, 409, "MASTERDATA_CATEGORY_CODE_EXISTS", nil, nil},
		{"创建上级不存在", "POST", "/api/product-categories", `{"code":"CAT-H3","name":"x","parent_id":99999}`, 404, "MASTERDATA_CATEGORY_NOT_FOUND", nil, nil},
		{"创建请求体非法 JSON", "POST", "/api/product-categories", "{", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
	})

	// 反查 ID（seq 为共享计数器，不按序号猜）。
	dis, err := h.repo.FindCategoryByCode(t.Context(), "CAT-DIS")
	require.NoError(t, err)
	require.NotNil(t, dis)
	top, err := h.repo.FindCategoryByCode(t.Context(), "CAT-H1")
	require.NoError(t, err)
	require.NotNil(t, top)

	// 第二段：详情/更新/环/启停。
	h.run([]httpCase{
		{"详情", "GET", "/api/product-categories/" + itoa64(child), "", 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "CAT-C", dataOf(t, env)["code"]) }, nil},
		{"详情不存在", "GET", "/api/product-categories/99999", "", 404, "MASTERDATA_CATEGORY_NOT_FOUND", nil, nil},
		{"详情 id=0", "GET", "/api/product-categories/0", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "id") }, nil},
		{
			name: "更新名称与排序", method: "PUT", path: "/api/product-categories/" + itoa64(child),
			body: `{"name":"厨具(新)","sort":3}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "厨具(新)", data["name"])
				require.EqualValues(t, 3, data["sort"])
			},
		},
		{
			name: "更新换上级构成环拒绝（自引用）", method: "PUT",
			path:       "/api/product-categories/" + itoa64(child),
			body:       `{"parent_id":` + itoa64(child) + `}`,
			wantStatus: 400, wantCode: "MASTERDATA_CATEGORY_CYCLE",
		},
		{
			name: "停用存在启用子分类拒绝", method: "PUT", path: "/api/product-categories/" + itoa64(root) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 409, wantCode: "MASTERDATA_CATEGORY_HAS_ENABLED_CHILDREN",
		},
		{
			name: "重新启用停用分类", method: "PUT", path: "/api/product-categories/" + itoa64(dis.ID.Int64()) + "/status",
			body: `{"status":"ENABLED"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, StatusEnabled, dataOf(t, env)["status"], "响应回显 status")
			},
		},
		{
			name: "停用无引用分类", method: "PUT", path: "/api/product-categories/" + itoa64(top.ID.Int64()) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, StatusDisabled, dataOf(t, env)["status"])
			},
		},
		{"启停请求体缺 status", "PUT", "/api/product-categories/" + itoa64(child) + "/status", `{}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) {
				require.Equal(t, "必填", fieldsOf(t, env)["status"])
			}, nil},
	})
}

// ---- 计量单位：5 接口（无删除）----

func TestHTTPUnitEndpoints(t *testing.T) {
	h := newHandlerTest(t)
	h.repo.seedUnit("PCS", StatusEnabled)
	used := h.repo.seedUnit("KG", StatusEnabled) // 被商品引用
	h.repo.seedUnit("BOX", StatusEnabled)
	h.repo.seedProduct("P-U1", StatusEnabled, func(p *Product) {
		id := database.ID(used) // 单位列载体为 database.ID（models.go Unit/Product）
		p.UnitID = &id
	})

	h.mountHTTP("GET", "/api/units", handleUnitList)
	h.mountHTTP("POST", "/api/units", handleUnitCreate)
	h.mountHTTP("GET", "/api/units/:id", handleUnitDetail)
	h.mountHTTP("PUT", "/api/units/:id", handleUnitUpdate)
	h.mountHTTP("PUT", "/api/units/:id/status", handleUnitStatus)

	box, err := h.repo.FindUnitByCode(t.Context(), "BOX")
	require.NoError(t, err)
	require.NotNil(t, box)
	boxID := box.ID.Int64()

	h.run([]httpCase{
		{
			name: "列表信封", method: "GET", path: "/api/units", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 3, dataOf(t, env)["total"])
			},
		},
		{
			name: "列表关键字过滤", method: "GET", path: "/api/units?keyword=BOX", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["total"])
			},
		},
		{"创建单位", "POST", "/api/units", `{"code":"PLT-H1","name":"托盘"}`, 200, 0,
			func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "PLT-H1", data["code"])
				require.Equal(t, StatusEnabled, data["status"])
			}, nil},
		{"创建编码重复", "POST", "/api/units", `{"code":"PCS","name":"重复"}`, 409, "MASTERDATA_UNIT_CODE_EXISTS", nil, nil},
		{"创建编码含空格", "POST", "/api/units", `{"code":"B 1","name":"x"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "code") }, nil},
		{"创建编码超 32 位", "POST", "/api/units",
			`{"code":"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456","name":"x"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "code") }, nil},
		{"创建请求体缺失", "POST", "/api/units", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{"详情", "GET", "/api/units/" + itoa64(boxID), "", 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "BOX", dataOf(t, env)["code"]) }, nil},
		{"详情不存在", "GET", "/api/units/99999", "", 404, "MASTERDATA_UNIT_NOT_FOUND", nil, nil},
		{"更新名称", "PUT", "/api/units/" + itoa64(boxID), `{"name":"箱(新)"}`, 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "箱(新)", dataOf(t, env)["name"]) }, nil},
		{
			name: "停用被商品引用拒绝", method: "PUT", path: "/api/units/" + itoa64(used) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 409, wantCode: "MASTERDATA_UNIT_IN_USE",
		},
		{
			name: "停用无引用单位", method: "PUT", path: "/api/units/" + itoa64(boxID) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, StatusDisabled, dataOf(t, env)["status"])
			},
		},
		{"启停状态非法枚举", "PUT", "/api/units/" + itoa64(boxID) + "/status", `{"status":"PAUSED"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "status") }, nil},
		{"启停请求体缺 status", "PUT", "/api/units/" + itoa64(boxID) + "/status", `{}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) {
				require.Equal(t, "必填", fieldsOf(t, env)["status"])
			}, nil},
	})
}

// ---- 供应商：6 接口 ----

func TestHTTPSupplierEndpoints(t *testing.T) {
	h := newHandlerTest(t)
	h.repo.seedSupplier("SUP-OLD", StatusEnabled) // 删除分支用行（ID 经反查）

	h.mountHTTP("GET", "/api/suppliers", handleSupplierList)
	h.mountHTTP("POST", "/api/suppliers", handleSupplierCreate)
	h.mountHTTP("GET", "/api/suppliers/:id", handleSupplierDetail)
	h.mountHTTP("PUT", "/api/suppliers/:id", handleSupplierUpdate)
	h.mountHTTP("PUT", "/api/suppliers/:id/status", handleSupplierStatus)
	h.mountHTTP("DELETE", "/api/suppliers/:id", handleSupplierDelete)

	// 第一段：创建（成功+失败分支）与列表。
	h.run([]httpCase{
		{
			name: "创建供应商全字段", method: "POST", path: "/api/suppliers",
			body: `{"code":"SUP-H1","name":"华东饮水","contact":"李四","phone":"138-0000-0000",` +
				`"email":"li@sup.cn","address":"上海市浦东新区","remark":"年度框架"}`,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "li@sup.cn", data["email"])
				require.Equal(t, StatusEnabled, data["status"])
			},
		},
		{"创建编码重复", "POST", "/api/suppliers", `{"code":"SUP-H1","name":"重复"}`, 409, "MASTERDATA_SUPPLIER_CODE_EXISTS", nil, nil},
		{"创建邮箱非法", "POST", "/api/suppliers", `{"code":"SUP-H2","name":"x","email":"bad-email"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "email") }, nil},
		{"创建编码非法字符", "POST", "/api/suppliers", `{"code":"S 1","name":"x"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "code") }, nil},
		{"创建请求体非法 JSON", "POST", "/api/suppliers", "{", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{
			name: "列表分页切片", method: "GET", path: "/api/suppliers?pageSize=1", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.EqualValues(t, 1, data["pageSize"])
				require.EqualValues(t, 2, data["total"], "SUP-OLD + SUP-H1")
				require.Len(t, itemsOf(t, env), 1, "pageSize=1 只出一行")
			},
		},
		{"列表无命中 items 为空数组", "GET", "/api/suppliers?keyword=NOPE", "", 200, 0,
			func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 0, dataOf(t, env)["total"])
				require.Empty(t, itemsOf(t, env))
			}, nil},
	})

	old, err := h.repo.FindSupplierByCode(t.Context(), "SUP-OLD")
	require.NoError(t, err)
	require.NotNil(t, old)
	oldID := old.ID.Int64()
	created, err := h.repo.FindSupplierByCode(t.Context(), "SUP-H1")
	require.NoError(t, err)
	require.NotNil(t, created)
	supID := created.ID.Int64()

	// 第二段：详情/更新/启停/删除（删除经 setup 切换引用读取器注入态，覆盖
	// 409 阻止与未注入 fail-open 两分支，service_partner.go:342-350）。
	h.run([]httpCase{
		{"详情", "GET", "/api/suppliers/" + itoa64(supID), "", 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "李四", dataOf(t, env)["contact"]) }, nil},
		{"详情不存在", "GET", "/api/suppliers/99999", "", 404, "MASTERDATA_SUPPLIER_NOT_FOUND", nil, nil},
		{"更新联系人", "PUT", "/api/suppliers/" + itoa64(supID), `{"contact":"李四(新)"}`, 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "李四(新)", dataOf(t, env)["contact"]) }, nil},
		{"更新邮箱非法", "PUT", "/api/suppliers/" + itoa64(supID), `{"email":"bad-email"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "email") }, nil},
		{
			name: "停用供应商", method: "PUT", path: "/api/suppliers/" + itoa64(supID) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, StatusDisabled, dataOf(t, env)["status"])
			},
		},
		{
			name: "删除已产生采购记录拒绝", method: "DELETE", path: "/api/suppliers/" + itoa64(oldID),
			wantStatus: 409, wantCode: "MASTERDATA_SUPPLIER_IN_USE",
			setup: func(t *testing.T, h *handlerHarness) { h.svc.supplierRefs = stubSupplierRefs{has: true} },
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, oldID, detailsOf(t, env)["id"])
			},
		},
		{
			name: "删除无引用供应商（读取器复位后放行）", method: "DELETE", path: "/api/suppliers/" + itoa64(oldID),
			wantStatus: 200, wantCode: 0,
			setup: func(t *testing.T, h *handlerHarness) { h.svc.supplierRefs = nil },
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, true, dataOf(t, env)["deleted"])
			},
		},
		{"删除不存在", "DELETE", "/api/suppliers/424242", "", 404, "MASTERDATA_SUPPLIER_NOT_FOUND", nil, nil},
	})
}

// ---- 客户：6 接口 ----

func TestHTTPCustomerEndpoints(t *testing.T) {
	h := newHandlerTest(t)

	h.mountHTTP("GET", "/api/customers", handleCustomerList)
	h.mountHTTP("POST", "/api/customers", handleCustomerCreate)
	h.mountHTTP("GET", "/api/customers/:id", handleCustomerDetail)
	h.mountHTTP("PUT", "/api/customers/:id", handleCustomerUpdate)
	h.mountHTTP("PUT", "/api/customers/:id/status", handleCustomerStatus)
	h.mountHTTP("DELETE", "/api/customers/:id", handleCustomerDelete)

	// 第一段：创建（成功+失败分支）与列表。
	h.run([]httpCase{
		{
			name: "创建客户带收货地址", method: "POST", path: "/api/customers",
			body: `{"code":"CUS-H1","name":"便利连锁","contact":"王五","phone":"020-88886666",` +
				`"address":"广州市","shipping_address":"广州市天河区 1 号"}`,
			wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				data := dataOf(t, env)
				require.Equal(t, "广州市天河区 1 号", data["shipping_address"])
				require.Equal(t, StatusEnabled, data["status"])
			},
		},
		{"创建编码重复", "POST", "/api/customers", `{"code":"CUS-H1","name":"重复"}`, 409, "MASTERDATA_CUSTOMER_CODE_EXISTS", nil, nil},
		{"创建电话非法", "POST", "/api/customers", `{"code":"CUS-H2","name":"x","phone":"bad^phone"}`, 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { fieldOf(t, env, "phone") }, nil},
		{"创建请求体缺失", "POST", "/api/customers", "", 400, "COMMON_INVALID_PARAM",
			func(t *testing.T, env map[string]any) { bindReasonOK(t, env) }, nil},
		{
			name: "列表信封", method: "GET", path: "/api/customers", wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, 1, dataOf(t, env)["total"])
			},
		},
	})

	created, err := h.repo.FindCustomerByCode(t.Context(), "CUS-H1")
	require.NoError(t, err)
	require.NotNil(t, created)
	cusID := created.ID.Int64()

	// 第二段：详情/更新/启停/删除（删除覆盖 409 阻止与放行两分支）。
	h.run([]httpCase{
		{"详情", "GET", "/api/customers/" + itoa64(cusID), "", 200, 0,
			func(t *testing.T, env map[string]any) { require.Equal(t, "便利连锁", dataOf(t, env)["name"]) }, nil},
		{"详情不存在", "GET", "/api/customers/99999", "", 404, "MASTERDATA_CUSTOMER_NOT_FOUND", nil, nil},
		{
			name: "更新收货地址", method: "PUT", path: "/api/customers/" + itoa64(cusID),
			body: `{"shipping_address":"广州市天河区 2 号"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, "广州市天河区 2 号", dataOf(t, env)["shipping_address"])
			},
		},
		{
			name: "停用客户", method: "PUT", path: "/api/customers/" + itoa64(cusID) + "/status",
			body: `{"status":"DISABLED"}`, wantStatus: 200, wantCode: 0,
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, StatusDisabled, dataOf(t, env)["status"])
			},
		},
		{
			name: "删除已产生销售记录拒绝", method: "DELETE", path: "/api/customers/" + itoa64(cusID),
			wantStatus: 409, wantCode: "MASTERDATA_CUSTOMER_IN_USE",
			setup: func(t *testing.T, h *handlerHarness) { h.svc.customerRefs = stubCustomerRefs{has: true} },
			check: func(t *testing.T, env map[string]any) {
				require.EqualValues(t, cusID, detailsOf(t, env)["id"])
			},
		},
		{
			name: "删除客户成功（读取器复位后放行）", method: "DELETE", path: "/api/customers/" + itoa64(cusID),
			wantStatus: 200, wantCode: 0,
			setup: func(t *testing.T, h *handlerHarness) { h.svc.customerRefs = nil },
			check: func(t *testing.T, env map[string]any) {
				require.Equal(t, true, dataOf(t, env)["deleted"])
			},
		},
		{"删除后详情不可见", "GET", "/api/customers/" + itoa64(cusID), "", 404, "MASTERDATA_CUSTOMER_NOT_FOUND", nil, nil},
	})
}

// ---- 操作者归因：注入与零值两路径（handler.go:18-30 actorOf）----

func TestHTTPActorAttribution(t *testing.T) {
	t.Run("注入用户上下文完整归因", func(t *testing.T) {
		h := newHandlerTest(t)
		h.user = &auth.UserContext{UserID: 42, Username: "attribution-user", IsSuper: true}
		h.mountHTTP("POST", "/api/suppliers", handleSupplierCreate)

		status, env := h.do("POST", "/api/suppliers", `{"code":"SUP-A1","name":"归因校验"}`)
		require.Equal(t, 200, status)
		require.EqualValues(t, 0, env["code"])

		entries := h.spy.all()
		require.Len(t, entries, 1)
		require.EqualValues(t, 42, entries[0].UserID)
		require.Equal(t, "attribution-user", entries[0].Username)
		require.Equal(t, "masterdata", entries[0].Module)
		require.Equal(t, "supplier", entries[0].ObjectType)
		require.Equal(t, "create", entries[0].Action)
	})

	t.Run("未装配用户上下文零值不阻断请求", func(t *testing.T) {
		// handler.go:19 actorOf 显式忽略 CurrentUser 的 ok——handler 直挂场景设计如此；
		// 审计仍落行（UserID=0），请求不因缺用户上下文失败。
		h := newHandlerTest(t)
		h.mountHTTP("POST", "/api/suppliers", handleSupplierCreate)

		status, env := h.do("POST", "/api/suppliers", `{"code":"SUP-A2","name":"零值归因"}`)
		require.Equal(t, 200, status)
		require.EqualValues(t, 0, env["code"])

		entries := h.spy.all()
		require.Len(t, entries, 1)
		require.EqualValues(t, 0, entries[0].UserID)
	})
}

// ---- 工具 ----

func itoa64(v int64) string { return fmt.Sprintf("%d", v) }
