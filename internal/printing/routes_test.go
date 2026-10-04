package printing

// 路由装配测试：端点冻结清单、权限中间件挂载与 RegisterRoutes 启动期 fail-fast
// （plan §3.1 规则①、§7.1/§7.2 路由清单）。

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

// testRouteOpts 全量装配选项（7 类外部 reader + queue）。
func testRouteOpts(env *testEnv) []Option {
	opts := []Option{WithQueue(env.queue)}
	for _, ot := range objectTypes {
		if IsBuiltinObjectType(ot) {
			continue
		}
		opts = append(opts, WithContentReader(ot, env.reader))
	}
	return opts
}

// TestRegisterRoutes_MountsAllEndpoints 冻结端点全量挂载 + 权限中间件先于 handler
// 生效（未认证请求 401，证明每个路由都挂了 auth.RequirePermission——ask 约束）。
func TestRegisterRoutes_MountsAllEndpoints(t *testing.T) {
	env := newTestEnv(t)
	r, api := routeTestEngine(t)
	RegisterRoutes(api, env.repo.DB(), nil, testRouteOpts(env)...)

	paths := map[string]bool{}
	for _, ri := range r.Routes() {
		paths[ri.Method+" "+ri.Path] = true
	}
	want := []string{
		"GET /api/prints/templates", "POST /api/prints/templates",
		"GET /api/prints/templates/:id", "PUT /api/prints/templates/:id",
		"POST /api/prints/templates/:id/copy", "PUT /api/prints/templates/:id/status",
		"GET /api/prints/tasks", "POST /api/prints/tasks",
		"GET /api/prints/tasks/:id", "POST /api/prints/tasks/:id/execute",
		"GET /api/prints/history",
		"GET /api/prints/barcode",
	}
	for _, w := range want {
		if !paths[w] {
			t.Fatalf("冻结端点缺失: %s", w)
		}
	}
	// 未认证 → 401（RequirePermission 生效；信封格式由 response.Err 写出）。
	for _, probe := range []string{
		"/api/prints/templates", "/api/prints/tasks", "/api/prints/history", "/api/prints/barcode",
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, probe, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 期望 401（权限中间件生效），得到 %d: %s", probe, w.Code, w.Body.String())
		}
	}
}

// TestRegisterRoutes_FailFast reader/queue 缺位 → 启动期 panic（plan §3.1 规则①）。
func TestRegisterRoutes_FailFast(t *testing.T) {
	env := newTestEnv(t)
	_, api := routeTestEngine(t)

	// 缺 queue。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("缺 queue：期望启动期 panic")
			}
		}()
		opts := []Option{}
		for _, ot := range objectTypes {
			if IsBuiltinObjectType(ot) {
				continue
			}
			opts = append(opts, WithContentReader(ot, env.reader))
		}
		RegisterRoutes(api, env.repo.DB(), nil, opts...)
	}()

	// 缺任一外部 reader（以缺 SKU_LABEL 为例——7 类必须全量，CARTON/PALLET 内置豁免）。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("缺 reader：期望启动期 panic")
			}
		}()
		opts := []Option{WithQueue(env.queue)}
		for _, ot := range objectTypes {
			if IsBuiltinObjectType(ot) || ot == ObjectSKULabel {
				continue
			}
			opts = append(opts, WithContentReader(ot, env.reader))
		}
		RegisterRoutes(api, env.repo.DB(), nil, opts...)
	}()
}

// TestRenderHandlerRegisteredOnce RegisterRoutes 多次调用不触发 asynqx 重复注册
// panic（进程级注册表防线；gin 侧路由重复 panic 与本测试无关——各用独立 engine）。
func TestRenderHandlerRegisteredOnce(t *testing.T) {
	env := newTestEnv(t)
	_, api1 := routeTestEngine(t)
	RegisterRoutes(api1, env.repo.DB(), nil, testRouteOpts(env)...)
	_, api2 := routeTestEngine(t)
	RegisterRoutes(api2, env.repo.DB(), nil, testRouteOpts(env)...) // 不 panic 即通过
}
