package masterdata

// 路由契约测试（backend-m1-plan §5.4 路由总表 / §5.2 冻结签名）：
// 断言 RegisterRoutes 注册的 method+path 集合与 plan §5.4 masterdata 行完全一致，
// 且 nil db 启动期 fail-fast。权限点挂载（auth.RequirePermission）在 masterdata.go
// 每条路由静态声明，配合代码评审核验。

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// wantRoutes plan §5.4：products/skus 各 6 条（CRUD+status+delete），
// product-categories/units 各 5 条（无 delete），suppliers/customers 各 6 条。
func wantRoutes() []string {
	routes := []string{}
	appendRoute := func(base string, hasDelete bool) {
		routes = append(routes,
			"GET "+base,
			"POST "+base,
			"GET "+base+"/:id",
			"PUT "+base+"/:id",
			"PUT "+base+"/:id/status")
		if hasDelete {
			routes = append(routes, "DELETE "+base+"/:id")
		}
	}
	appendRoute("/products", true)
	appendRoute("/skus", true)
	appendRoute("/product-categories", false)
	appendRoute("/units", false)
	appendRoute("/suppliers", true)
	appendRoute("/customers", true)
	sort.Strings(routes)
	return routes
}

func TestRegisterRoutesContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo(nil)

	engine := gin.New()
	rg := engine.Group("/api")
	require.NotPanics(t, func() { RegisterRoutes(rg, repo.DB(), nil) })

	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+strings.TrimPrefix(r.Path, "/api"))
	}
	sort.Strings(got)
	require.Equal(t, wantRoutes(), got, "路由集合必须与 backend-m1-plan §5.4 完全一致")
}

func TestRegisterRoutesNilDBFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rg := gin.New().Group("/api")
	require.PanicsWithValue(t,
		"masterdata 装配失败: db 为 nil（router 必须注入 GORM 句柄）",
		func() { RegisterRoutes(rg, nil, nil) },
		fmt.Sprintf("nil db 必须 fail-fast"))
}
