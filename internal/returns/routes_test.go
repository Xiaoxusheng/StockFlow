package returns

// 路由装配测试：gin 通配冲突（/inventory/:id 与 /inventory/trace 共存）与
// RegisterRoutes 启动期 fail-fast（plan §3.1 规则①）。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func routeTestEngine(t *testing.T) (*gin.Engine, *gin.RouterGroup) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	return r, api
}

// TestRouteRegistrationNoWildcardConflict 复现 router 装配形态：inventory 先注册
// /inventory/:id，returns 再注册 /inventory/trace——gin v1.12 静态路由与参数路由
// 共存则不 panic（plan §8.3 条 5 的挂载前提）。
func TestRouteRegistrationNoWildcardConflict(t *testing.T) {
	env := newTestEnv(t)
	r, api := routeTestEngine(t)
	// 模拟 inventory.RegisterRoutes 既有路由（含通配 :id）。
	api.GET("/inventory/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	// returns 装配（全套注入齐备）。
	RegisterRoutes(api, env.repo.DB(), nil,
		WithStock(env.stock), WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase),
		WithQCCreator(env.qc), WithLedgers(env.ledgers), WithStockState(env.state))

	found := r.Routes()
	paths := map[string]bool{}
	for _, ri := range found {
		paths[ri.Path] = true
	}
	for _, want := range []string{
		"/api/returns", "/api/returns/:id", "/api/returns/:id/receive",
		"/api/purchase-returns", "/api/purchase-returns/:id/ship",
		"/api/exceptions", "/api/exceptions/:id/assign",
		"/api/inventory/trace",
	} {
		if !paths[want] {
			t.Fatalf("路由缺失: %s（已注册 %d 条）", want, len(found))
		}
	}
	// 静态路由优先于通配：/api/inventory/trace 命中 trace handler（其 RequirePermission
	// 中间件先以 401 拒绝未认证请求——若落到通配 /inventory/:id 的裸 handler 则会 200）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/inventory/trace", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401（trace 路由的权限中间件生效），得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestRegisterRoutesFailFast 必需跨域接口未注入 → 启动期 panic（plan §3.1 规则①，
// 与 inventory.RegisterRoutes 对 Checker 的约定一致）。
func TestRegisterRoutesFailFast(t *testing.T) {
	env := newTestEnv(t)
	_, api := routeTestEngine(t)
	cases := []struct {
		name string
		opts []Option
	}{
		{"缺 StockGateway", nil},
		{"缺 SalesOrderReader", []Option{WithStock(env.stock), WithPurchaseOrders(env.purchase), WithQCCreator(env.qc), WithLedgers(env.ledgers), WithStockState(env.state)}},
		{"缺 QCCreator", []Option{WithStock(env.stock), WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase), WithLedgers(env.ledgers), WithStockState(env.state)}},
		{"缺 LedgerReader", []Option{WithStock(env.stock), WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase), WithQCCreator(env.qc), WithStockState(env.state)}},
		{"缺 StockStateReader", []Option{WithStock(env.stock), WithSalesOrders(env.sales), WithPurchaseOrders(env.purchase), WithQCCreator(env.qc), WithLedgers(env.ledgers)}},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s：期望启动期 panic", tc.name)
				}
			}()
			RegisterRoutes(api, env.repo.DB(), nil, tc.opts...)
		}()
	}
	// db 为 nil 同样 fail-fast。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("db 为 nil：期望启动期 panic")
			}
		}()
		RegisterRoutes(api, nil, nil, WithStock(env.stock))
	}()
}
