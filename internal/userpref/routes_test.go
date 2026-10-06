package userpref

// 路由装配单测（sysops/routes_test.go 同款口径）：gin 路由树无冲突、
// 计划 §4.1 B1 冻结端点集全量注册（T14 口径——漏注册即测试红）、
// 未认证 fail-closed 401、装配缺依赖 fail-fast。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TestRegisterRoutesMounting 冻结端点集（计划 §4.1 B1 段，T14 口径）。
func TestRegisterRoutesMounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{})

	want := []string{
		"GET /api/user/views", "POST /api/user/views",
		"PUT /api/user/views/:id", "DELETE /api/user/views/:id",
		"GET /api/user/preferences", "PUT /api/user/preferences/:key",
	}
	got := map[string]bool{}
	for _, route := range r.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	for _, path := range want {
		if !got[path] {
			t.Fatalf("路由 %s 未注册（计划 §4.1 冻结端点集）", path)
		}
	}
}

// TestRoutesFailClosedWithoutAuth 未认证（无用户上下文）一律 401 统一信封。
func TestRoutesFailClosedWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{})

	for _, path := range []string{
		"/api/user/views?page_key=stock.list",
		"/api/user/preferences",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("未认证访问 %s 应 401，实际 %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"COMMON_UNAUTHORIZED"`) {
			t.Fatalf("%s 应返回统一信封 401，实际 %s", path, rec.Body.String())
		}
	}
}

// TestRegisterRoutesPanicsWithoutDB 装配缺依赖 fail-fast（plan §3.1 规则①）。
func TestRegisterRoutesPanicsWithoutDB(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("未注入 Service 且 db 为 nil 装配应 panic")
		}
	}()
	gin.SetMode(gin.TestMode)
	RegisterRoutes(gin.New().Group("/api"), nil)
}
