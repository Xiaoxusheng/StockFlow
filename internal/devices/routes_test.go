package devices

// 路由装配测试：静态/参数路由共存（/devices/activate 与 /devices/:id）、双轨解析路由
// 注册、RegisterRoutes 启动期 fail-fast（plan §3.1 规则①）与必需依赖缺位 panic 文案。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// namedOption 带名的注入项（fail-fast 用例按名剔除）。
type namedOption struct {
	name string
	opt  Option
}

// fullOptions 全套必需注入（匹配器替身 + checker + 设备端组 /api 根组）。
func fullOptions(api *gin.RouterGroup) []namedOption {
	return []namedOption{
		{"WithWarehouseChecker", WithWarehouseChecker(&fakeChecker{exists: map[int64]bool{1: true}})},
		{"WithSKUBarcodes", WithSKUBarcodes(&fakeSKUBarcodes{hits: map[string]Hit{}})},
		{"WithBins", WithBins(&fakeBins{byCode: map[string][]Hit{}})},
		{"WithSerials", WithSerials(&fakeSerials{hits: map[string]Hit{}})},
		{"WithBatches", WithBatches(&fakeBatches{byNo: map[string][]Hit{}})},
		{"WithSfqrSkus", WithSfqrSkus(&fakeSfqrSkus{hits: map[string]Hit{}})},
		{"WithPurchaseDocs", WithPurchaseDocs(&fakeDocs{found: map[string]Hit{}})},
		{"WithSalesDocs", WithSalesDocs(&fakeDocs{found: map[string]Hit{}})},
		{"WithStockopsDocs", WithStockopsDocs(&fakeDocs{found: map[string]Hit{}})},
		{"WithReturnsDocs", WithReturnsDocs(&fakeDocs{found: map[string]Hit{}})},
		{"WithDeviceAPI", WithDeviceAPI(api)},
	}
}

func unpack(named []namedOption) []Option {
	opts := make([]Option, 0, len(named))
	for _, n := range named {
		opts = append(opts, n.opt)
	}
	return opts
}

// newMountedEngine 全量装配 gin 引擎（protected 模拟 router 的 /api 保护组）。
// auth.RegisterProtectedRoutes(nil, nil) 为 auth 域 T0 测试装配豁免形态——仅用于让
// resolve 用户链（AuthRequired）在无 Redis 单测下可走通 401 拒绝路径。
func newMountedEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openTestGorm(nil)
	if err != nil {
		t.Fatalf("打开假 gorm 失败: %v", err)
	}
	r := gin.New()
	api := r.Group("/api")
	auth.RegisterProtectedRoutes(r.Group("/api"), nil, nil)
	RegisterRoutes(r.Group("/api"), db, nil, unpack(fullOptions(api))...)
	return r
}

// TestRegisterRoutesMounting 全量装配：管理端/设备端/解析路由注册齐全，
// 静态路由（activate/self/app）与参数路由（:id）共存不冲突（gin v1.12）。
func TestRegisterRoutesMounting(t *testing.T) {
	r := newMountedEngine(t)

	paths := map[string]bool{}
	for _, ri := range r.Routes() {
		paths[ri.Method+" "+ri.Path] = true
	}
	want := []string{
		// 管理端（plan §8.1）
		"GET /api/devices", "POST /api/devices", "GET /api/devices/:id",
		"GET /api/devices/:id/activation", "PUT /api/devices/:id/activation",
		"POST /api/devices/:id/bind", "POST /api/devices/:id/unbind",
		"POST /api/devices/:id/disable", "PUT /api/devices/:id/config",
		"GET /api/devices/:id/logs",
		// 扫码日志（plan §11.1 devices:scanlog:list）
		"GET /api/scanner/logs",
		// 设备端（plan §8.2；activate 免中间件）
		"POST /api/devices/activate", "POST /api/devices/heartbeat",
		"GET /api/devices/self/config", "POST /api/devices/self/logs",
		"GET /api/devices/app/versions/latest",
		// 统一解析（双轨）
		"POST /api/scanner/resolve",
	}
	for _, p := range want {
		if !paths[p] {
			t.Fatalf("路由缺失: %s（已注册 %d 条）", p, len(paths))
		}
	}

	// 静态优先：/api/devices/activate 不落入 :id 参数分支——activate handler 直接收
	// 请求体（空 token → 401 激活失败信封，三因统一不区分——plan §8.2 防探测），
	// 证明设备端点未被保护组语义拦截。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/devices/activate", strings.NewReader("{}"))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "DEVICE_ACTIVATION_INVALID") {
		t.Fatalf("activate 应直接进入 handler（401 激活失败信封），得到 %d: %s", w.Code, w.Body.String())
	}

	// resolve 双轨：未携带凭证 → 用户链 AuthRequired 拒绝（401 信封）。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/scanner/resolve", strings.NewReader(`{"code":"x"}`))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("resolve 未认证期望 401，得到 %d: %s", w.Code, w.Body.String())
	}

	// 设备端受保护：无令牌心跳 → 401 DEVICE_TOKEN_INVALID。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/devices/heartbeat", strings.NewReader("{}"))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "DEVICE_TOKEN_INVALID") {
		t.Fatalf("无令牌心跳期望 401 DEVICE_TOKEN_INVALID，得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestRegisterRoutesFailFast 必需注入缺位 → 启动期 panic（plan §3.1 规则①，与
// returns/purchase 等域启动期 fail-fast 同约定）。
func TestRegisterRoutesFailFast(t *testing.T) {
	cases := []struct {
		drop   string
		expect string
	}{
		{"WithWarehouseChecker", "仓库校验器未注入"},
		{"WithDeviceAPI", "设备端挂载组未注入"},
		{"WithSKUBarcodes", "resolve 匹配器窄接口未注入"},
		{"WithSfqrSkus", "resolve 匹配器窄接口未注入"},
		{"WithReturnsDocs", "resolve 匹配器窄接口未注入"},
	}
	for _, tc := range cases {
		t.Run(tc.drop, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			db, err := openTestGorm(nil)
			if err != nil {
				t.Fatalf("打开假 gorm 失败: %v", err)
			}
			r := gin.New()
			api := r.Group("/api")
			var named []namedOption
			for _, n := range fullOptions(api) {
				if n.name != tc.drop {
					named = append(named, n)
				}
			}
			defer func() {
				rec := recover()
				if rec == nil {
					t.Fatalf("缺 %s 应启动期 panic", tc.drop)
				}
				msg, _ := rec.(string)
				if !strings.Contains(msg, tc.expect) {
					t.Fatalf("panic 文案缺 %q: %v", tc.expect, msg)
				}
			}()
			RegisterRoutes(r.Group("/api"), db, nil, unpack(named)...)
		})
	}
}
