package reports

// 路由装配单测（不依赖 PostgreSQL/网络）：gin 路由树无冲突（/inventory/summary 静态
// 与 /api 组内其他 :param 同级共存）、全部业务路由挂权限点后的 fail-closed 401 信封形态。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

func TestRegisterRoutesMounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil)

	want := map[string]bool{
		"GET /api/reports":                           false,
		"GET /api/reports/inventory-summary":         false,
		"GET /api/reports/inbound-stats":             false,
		"GET /api/reports/outbound-stats":            false,
		"GET /api/reports/inventory-turnover":        false,
		"GET /api/reports/stagnant-stock":            false,
		"GET /api/reports/replenishment-suggestions": false,
		"GET /api/reports/flow-trend":                false,
		"GET /api/inventory/summary":                 false,
		"GET /api/inventory/alerts":                  false,
		"GET /api/inventory/analytics":               false,
		"GET /api/inventory/sku-top":                 false,
		"GET /api/inventory/turnover-trend":          false,
		"GET /api/warehouses/workload":               false,
		"GET /api/inbounds/status-composition":       false,
		"GET /api/inbounds/supplier-rank":            false,
		"GET /api/outbounds/completion-rate":         false,
		"GET /api/outbounds/product-rank":            false,
		"GET /api/purchases/analytics/trend":         false,
		"GET /api/purchases/supplier-rank":           false,
		"GET /api/purchases/status-composition":      false,
		"GET /api/sales/analytics/trend":             false,
		"GET /api/sales/product-rank":                false,
		"GET /api/sales/status-composition":          false,
		"GET /api/workbench/summary":                 false,
		"GET /api/workbench/recent-operations":       false, // 效率层一期 §2.5（B3 交付，集成收口补录冻结清单）
		"GET /api/workbench/priorities":              false, // 效率层一期（工作台优先处理，集成收口补录冻结清单）
		"GET /api/tasks":                             false,
		"GET /api/tasks/next":                        false, // 效率层一期 §2.4（B3 交付，集成收口补录冻结清单）
	}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for path, seen := range want {
		if !seen {
			t.Fatalf("路由 %s 未注册（plan §9.1 冻结端点集）", path)
		}
	}
}

func TestRoutesFailClosedWithoutAuth(t *testing.T) {
	// RequirePermission 在无认证上下文必须 401（不因未装配 auth 而放行）。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	response.SetErrorLogger(nil)
	r.NoRoute(func(c *gin.Context) {
		response.Err(c, response.NewError(response.CodeNotFound, nil))
	})
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reports/inventory-summary", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证访问报表应 401，实际 %d", rec.Code)
	}
	if want := `"code":"COMMON_UNAUTHORIZED"`; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("应返回统一信封错误码 %s，实际 %s", want, rec.Body.String())
	}
}

func TestRegisterRoutesPanicsWithoutDB(t *testing.T) {
	// plan §3.1 规则①：装配缺依赖 fail-fast（禁止带病启动）。
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("db 为 nil 装配应 panic")
		}
	}()
	gin.SetMode(gin.TestMode)
	RegisterRoutes(gin.New().Group("/api"), nil, nil)
}
