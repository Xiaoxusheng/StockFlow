package idempotency

// RouteGuard 组级路由感知挂载单测（集成收口交付——router 白名单形态的包内语义保证）：
//   - 表内端点：附键请求走幂等仲裁（首执 + 回放），无键请求灰度放行；
//   - 表外端点 / 读方法 / 未注册路径：零干预放行（含附键请求也不缓存不拦截）；
//   - Required 模式表项：缺键 400 IDEMPOTENCY_KEY_REQUIRED。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// rgEngine 装配 RouteGuard 形态引擎：探针端点挂 protected 组（与 router 装配
// 同构——Guard 先于路由注册 Use）。
func rgEngine(t *testing.T, svc *Service, endpoints map[string]EndpointPolicy, probe *atomic.Int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op", IsSuper: true})
		c.Set(response.RequestIDKey, "req-current")
		c.Next()
	})
	api := r.Group("/api")
	api.Use(RouteGuard(svc, endpoints))
	h := func(c *gin.Context) {
		n := probe.Add(1)
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"n": n}, "request_id": c.GetString(response.RequestIDKey)})
	}
	api.POST("/receipts", h)
	api.POST("/other", h)
	api.GET("/receipts", h)
	return r
}

func rgDo(t *testing.T, r *gin.Engine, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set(IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestRouteGuardEngagesListedEndpointOnly 表内端点附键即防护（首执+回放），表外
// 端点与 GET 附键不缓存不拦截、无键放行。
func TestRouteGuardEngagesListedEndpointOnly(t *testing.T) {
	var probe atomic.Int64
	endpoints := map[string]EndpointPolicy{"POST /api/receipts": {}}
	r := rgEngine(t, NewService(newFakeRepo(), nil), endpoints, &probe)
	body := `{"qty":1}`

	if rec := rgDo(t, r, http.MethodPost, "/api/receipts", "rg-key-1", body); rec.Code != http.StatusOK || probe.Load() != 1 {
		t.Fatalf("表内首次应真实执行: code=%d probe=%d", rec.Code, probe.Load())
	}
	if rec := rgDo(t, r, http.MethodPost, "/api/receipts", "rg-key-1", body); rec.Code != http.StatusOK || probe.Load() != 1 {
		t.Fatalf("表内同键重放不应再执行: code=%d probe=%d", rec.Code, probe.Load())
	}
	if rec := rgDo(t, r, http.MethodPost, "/api/receipts", "", body); rec.Code != http.StatusOK || probe.Load() != 2 {
		t.Fatalf("灰度模式无键应放行真实执行: code=%d probe=%d", rec.Code, probe.Load())
	}
	if rec := rgDo(t, r, http.MethodPost, "/api/other", "rg-key-1", body); rec.Code != http.StatusOK || probe.Load() != 3 {
		t.Fatalf("表外端点应零干预放行: code=%d probe=%d", rec.Code, probe.Load())
	}
	if rec := rgDo(t, r, http.MethodGet, "/api/receipts", "rg-key-1", body); rec.Code != http.StatusOK || probe.Load() != 4 {
		t.Fatalf("读方法不参与幂等（§2.10 GET 不做）: code=%d probe=%d", rec.Code, probe.Load())
	}
}

// TestRouteGuardRequiredMode 表内 Required 端点缺键 400；附键正常防护。
func TestRouteGuardRequiredMode(t *testing.T) {
	var probe atomic.Int64
	endpoints := map[string]EndpointPolicy{"POST /api/receipts": {Required: true}}
	r := rgEngine(t, NewService(newFakeRepo(), nil), endpoints, &probe)

	rec := rgDo(t, r, http.MethodPost, "/api/receipts", "", `{"qty":1}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("Required 缺键应 400 IDEMPOTENCY_KEY_REQUIRED: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if probe.Load() != 0 {
		t.Fatalf("缺键不应触达业务 handler，probe=%d", probe.Load())
	}
	if rec := rgDo(t, r, http.MethodPost, "/api/receipts", "rg-key-req", `{"qty":1}`); rec.Code != http.StatusOK || probe.Load() != 1 {
		t.Fatalf("附键应真实执行: code=%d probe=%d", rec.Code, probe.Load())
	}
}

// TestRouteGuardUnmatchedPathPassThrough 未注册路径（FullPath 空）零干预放行——
// 与 protect 的 404 兜底口径一致，不拦 404 响应。
func TestRouteGuardUnmatchedPathPassThrough(t *testing.T) {
	var probe atomic.Int64
	endpoints := map[string]EndpointPolicy{"POST /api/receipts": {}}
	r := rgEngine(t, NewService(newFakeRepo(), nil), endpoints, &probe)
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"code": 404})
	})

	rec := rgDo(t, r, http.MethodPost, "/api/nope", "rg-key-1", `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未注册路径应走 404 兜底: code=%d", rec.Code)
	}
}
