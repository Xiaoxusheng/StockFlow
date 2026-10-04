package sysops

// 路由装配单测（不依赖 PostgreSQL/网络）：gin 路由树无冲突（/system/backups/pg-dump-template
// 静态与 /system/backups/:id/download 参数同级共存）、未认证 fail-closed 401、装配缺依赖 fail-fast。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestRegisterRoutesMounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil)

	want := []string{
		"GET /api/logs/operations", "GET /api/logs/operations/:id", "GET /api/logs/logins",
		"GET /api/system/configs", "PUT /api/system/configs",
		"GET /api/system/jobs", "PUT /api/system/jobs/:id/status", "GET /api/system/jobs/:id/run-logs",
		"GET /api/system/monitor",
		"GET /api/system/backups", "POST /api/system/backups",
		"GET /api/system/backups/pg-dump-template", "GET /api/system/backups/:id/download",
		"GET /api/notifications/unread-count", "GET /api/notifications",
		"POST /api/notifications/:id/read", "POST /api/notifications/read-all",
	}
	got := map[string]bool{}
	for _, route := range r.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	for _, path := range want {
		if !got[path] {
			t.Fatalf("路由 %s 未注册（plan §10/§12.1 冻结端点集）", path)
		}
	}
}

func TestRoutesFailClosedWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil)

	// 业务权限面：未认证 401。
	for _, path := range []string{"/api/logs/operations", "/api/system/monitor", "/api/system/backups"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("未认证访问 %s 应 401，实际 %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"COMMON_UNAUTHORIZED"`) {
			t.Fatalf("%s 应返回统一信封 401，实际 %s", path, rec.Body.String())
		}
	}
	// 个人收件箱同样要求认证（认证即可用 ≠ 匿名可用）。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证访问收件箱应 401，实际 %d", rec.Code)
	}
}

func TestRegisterRoutesPanicsWithoutDB(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("db 为 nil 装配应 panic")
		}
	}()
	gin.SetMode(gin.TestMode)
	RegisterRoutes(gin.New().Group("/api"), nil, nil)
}
