package purchase

// 路由契约测试：断言 RegisterRoutes 注册的 method+path 集合与本域交付面完全一致
// （api.md §1 领域划分 + ask 路由清单），且 nil db 启动期 fail-fast。
// 权限点挂载（auth.RequirePermission）在 purchase.go 每条路由静态声明，配合代码
// 评审核验（masterdata/routes_test.go 同款口径）。

import (
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func wantRoutes() []string {
	routes := []string{
		// —— 采购订单 ——
		"GET /purchases", "POST /purchases",
		"GET /purchases/:id", "PUT /purchases/:id",
		"POST /purchases/:id/submit", "POST /purchases/:id/approve",
		"POST /purchases/:id/cancel", "POST /purchases/:id/close",
		// —— 入库单 ——
		"GET /inbounds", "POST /inbounds",
		"GET /inbounds/:id", "PUT /inbounds/:id",
		"POST /inbounds/:id/cancel", "POST /inbounds/:id/close",
		// —— 收货 ——
		"GET /receipts", "POST /receipts",
		"GET /receipts/no/:no", "GET /receipts/:id",
		// —— 质检 ——
		"GET /quality", "POST /quality",
		"GET /quality/:id", "POST /quality/:id/start", "POST /quality/:id/execute",
		// —— 上架任务 ——
		"GET /putaway", "GET /putaway/recommend", "GET /putaway/:id",
		"POST /putaway/:id/claim", "POST /putaway/:id/execute",
	}
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
	require.Equal(t, wantRoutes(), got, "路由集合必须与本域交付面完全一致")
}

func TestRegisterRoutesNilDBFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rg := gin.New().Group("/api")
	require.PanicsWithValue(t,
		"purchase 装配失败: db 为 nil（router 必须注入 GORM 句柄）",
		func() { RegisterRoutes(rg, nil, nil) },
	)
}
