package warehouse

// HTTP handler 层数据驱动表驱动测试（ask 约束：不依赖 PostgreSQL/Redis/网络）。
//
// 形态对齐 internal/inventory/handler_test.go：
//   - 数据由 fakeRepo 内存承载（fakerepo_test.go），Service 走真实业务校验与
//     假方言器事务（fakedb_test.go）；
//   - 请求经真实 gin 路由树驱动（c.Param/c.Query/ShouldBindJSON 全链路生效）；
//   - 认证上下文以 auth.UserContext 注入（AuthRequired 的等价替身：AuthRequired
//     依赖 Redis 会话校验，单测不可用；仅写 ctxUserKey 同名键，见 authUserKey）。
//
// 与 RegisterRoutes 的关系：registerRoutesForTest 为可注入 fakeRepo 的手动装配
// （不含 RequirePermission——非超管用户在 auth 服务未装配时会被中间件以 500 拦截，
// 无法触达 handler 内的数据权限分支 handler.go:71-74）；其与 RegisterRoutes 的
// 路由集合一致性由 TestRegisterRoutes_MatchesTestWiring 锁定，权限点挂载与未认证
// 401 由 TestRegisterRoutes_WiringAndAuth 用真实 RegisterRoutes 验证。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/auth"
)

// authUserKey 锚定 auth/middleware.go ctxUserKey（"sf_auth_user"，未导出）。
// 若 auth 侧键名漂移，setAuthUser 的自检会让所有 handler 测试立刻失败，不静默放行。
const authUserKey = "sf_auth_user"

// superUC 全量数据权限用户（IsSuper 直通权限点，auth/middleware.go:123；scope 归一 ALL）。
func superUC() auth.UserContext {
	return auth.UserContext{UserID: 1, Username: "root", IsSuper: true, DataScope: auth.DataScopeAll}
}

// scopedUC 指定仓库范围用户（SPECIFIED_WAREHOUSE，auth/middleware.go:210）。
func scopedUC(ids ...int64) auth.UserContext {
	return auth.UserContext{UserID: 2, Username: "op", DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids}
}

// newHandlerEngine 构造注入认证用户的手动装配引擎（每次调用独立，避免用例间污染）。
func newHandlerEngine(t *testing.T, svc *Service, uc auth.UserContext) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(authUserKey, uc)
		// 自检：注入键漂移时立刻失败（auth.CurrentUser 读不到即说明键漂移）。
		if got, ok := auth.CurrentUser(c); !ok || got.UserID != uc.UserID {
			t.Fatalf("认证上下文注入失效：authUserKey 与 auth.ctxUserKey 漂移")
		}
		c.Next()
	})
	registerRoutesForTest(r.Group("/api"), &handler{svc: svc})
	return r
}

// registerRoutesForTest 与 RegisterRoutes（handler.go:35-64）同路由表手动装配，
// 换取可注入 fakeRepo 的 Service；一致性由 TestRegisterRoutes_MatchesTestWiring 保证。
func registerRoutesForTest(rg *gin.RouterGroup, h *handler) {
	wh := rg.Group("/warehouses")
	wh.GET("", h.listWarehouses)
	wh.POST("", h.createWarehouse)
	wh.GET("/:id", h.getWarehouse)
	wh.PUT("/:id", h.updateWarehouse)
	wh.DELETE("/:id", h.deleteWarehouse)
	wh.PUT("/:id/status", h.updateWarehouseStatus)
	wh.GET("/:id/map", h.warehouseMap)

	zo := rg.Group("/zones")
	zo.GET("", h.listZones)
	zo.POST("", h.createZone)
	zo.GET("/:id", h.getZone)
	zo.PUT("/:id", h.updateZone)
	zo.PUT("/:id/status", h.updateZoneStatus)

	sh := rg.Group("/shelves")
	sh.GET("", h.listShelves)
	sh.POST("", h.createShelf)
	sh.GET("/:id", h.getShelf)
	sh.PUT("/:id", h.updateShelf)
	sh.PUT("/:id/status", h.updateShelfStatus)

	bn := rg.Group("/bins")
	bn.GET("", h.listBins)
	bn.POST("", h.createBin)
	bn.GET("/:id", h.getBin)
	bn.PUT("/:id", h.updateBin)
	bn.DELETE("/:id", h.deleteBin)
	bn.PUT("/:id/status", h.updateBinStatus)
}

// doReq 发起请求（body 为空串时不带请求体）。
func doReq(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decodeBody 解析统一信封（api.md §2.2）。
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "响应非 JSON: %s", w.Body.String())
	return m
}

// expectOKData 断言成功信封：HTTP 200 + code=0（数字）+ data 为对象，返回 data。
func expectOKData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	m := decodeBody(t, w)
	require.Equal(t, float64(0), m["code"], "成功信封 code 应为数字 0（api.md §2.2）: %s", w.Body.String())
	require.Equal(t, "ok", m["message"])
	data, ok := m["data"].(map[string]any)
	require.True(t, ok, "data 应为对象: %s", w.Body.String())
	return data
}

// expectBiz 断言失败信封：HTTP 状态 + 字符串错误码，返回完整信封。
func expectBiz(t *testing.T, w *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	require.Equal(t, status, w.Code, "body: %s", w.Body.String())
	m := decodeBody(t, w)
	require.Equal(t, code, m["code"], "body: %s", w.Body.String())
	require.NotEmpty(t, m["message"], "失败信封必须带用户可读 message")
	return m
}

// detailsOf 取 details 对象（api.md §4：校验失败必须带 details）。
func detailsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	d, ok := m["details"].(map[string]any)
	require.True(t, ok, "失败响应必须带 details: %v", m)
	return d
}

// mustParseID 视图 ID 字符串 → int64（database.ID 序列化为字符串，model.go:22）。
func mustParseID(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err, "ID 应为整数字符串: %q", s)
	return n
}

// idStr database.ID（出参）→ JSON 字符串断言值。
func idStr(v interface{ Int64() int64 }) string { return strconv.FormatInt(v.Int64(), 10) }

// idInt int64（种子值）→ 字符串。
func idInt(v int64) string { return strconv.FormatInt(v, 10) }

// ---- 装配与权限挂载（真实 RegisterRoutes + 假 gorm 句柄，请求不触库）----

func newRealEngine(t *testing.T) *gin.Engine {
	t.Helper()
	g, err := openTestGorm()
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), g, nil)
	return r
}

// TestRegisterRoutes_WiringAndAuth 装配不 panic；23 条冻结路由全部挂
// RequirePermission（未认证 401，permission.md §2；请求在权限中间件被拦，
// 不触达数据层——假驱动下即便触库也不会污染）。
func TestRegisterRoutes_WiringAndAuth(t *testing.T) {
	r := newRealEngine(t)
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/warehouses"},
		{http.MethodPost, "/api/warehouses"},
		{http.MethodGet, "/api/warehouses/1"},
		{http.MethodPut, "/api/warehouses/1"},
		{http.MethodDelete, "/api/warehouses/1"},
		{http.MethodPut, "/api/warehouses/1/status"},
		{http.MethodGet, "/api/warehouses/1/map"},
		{http.MethodGet, "/api/zones"},
		{http.MethodPost, "/api/zones"},
		{http.MethodGet, "/api/zones/1"},
		{http.MethodPut, "/api/zones/1"},
		{http.MethodPut, "/api/zones/1/status"},
		{http.MethodGet, "/api/shelves"},
		{http.MethodPost, "/api/shelves"},
		{http.MethodGet, "/api/shelves/1"},
		{http.MethodPut, "/api/shelves/1"},
		{http.MethodPut, "/api/shelves/1/status"},
		{http.MethodGet, "/api/bins"},
		{http.MethodPost, "/api/bins"},
		{http.MethodGet, "/api/bins/1"},
		{http.MethodPut, "/api/bins/1"},
		{http.MethodDelete, "/api/bins/1"},
		{http.MethodPut, "/api/bins/1/status"},
	}
	require.Len(t, cases, 23)
	for _, tc := range cases {
		w := doReq(r, tc.method, tc.path, "{}")
		require.Equal(t, http.StatusUnauthorized, w.Code, "%s %s 必须认证后访问: %s", tc.method, tc.path, w.Body.String())
		m := decodeBody(t, w)
		require.Equal(t, "COMMON_UNAUTHORIZED", m["code"], "%s %s", tc.method, tc.path)
	}
}

// TestRegisterRoutes_MatchesTestWiring 锁定手动装配（registerRoutesForTest）与
// 业务装配（RegisterRoutes）的路由集合完全一致——手动表漂移即刻暴露。
func TestRegisterRoutes_MatchesTestWiring(t *testing.T) {
	r := newRealEngine(t)
	want := map[string]bool{}
	for _, ri := range r.Routes() {
		want[ri.Method+" "+ri.Path] = true
	}
	require.Len(t, want, 23, "RegisterRoutes 冻结路由数")

	gin.SetMode(gin.TestMode)
	test := gin.New()
	registerRoutesForTest(test.Group("/api"), &handler{svc: NewService(newFakeRepo())})
	got := map[string]bool{}
	for _, ri := range test.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	require.Equal(t, want, got, "手动装配与 RegisterRoutes 路由集合不一致")
}

// ---- GET /api/warehouses ----

func TestWarehouseRoutes_ListWarehouses(t *testing.T) {
	s, _ := newTestService()
	seedWarehouse(t, s, "WHA")
	seedWarehouse(t, s, "WHB")
	cold, err := s.CreateWarehouse(ctx, superActor(), WarehouseCreateInput{Code: "WHC", Name: "冷仓", Type: "COLD"})
	mustOK(t, err)
	mustOK(t, s.UpdateWarehouseStatus(ctx, superActor(), cold.ID.Int64(), StatusDisabled))
	r := newHandlerEngine(t, s, superUC())

	t.Run("默认分页含真实数据", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses", ""))
		require.Equal(t, float64(1), data["page"])
		require.Equal(t, float64(20), data["pageSize"])
		require.Equal(t, float64(3), data["total"])
		items := data["items"].([]any)
		require.Len(t, items, 3)
		first := items[0].(map[string]any)
		require.Equal(t, "WHA", first["code"])
		require.Equal(t, "ENABLED", first["status"])
		require.NotEmpty(t, first["id"], "ID 以字符串形态输出（database.ID）")
		require.NotEmpty(t, first["created_at"])
	})
	t.Run("keyword 过滤 code", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses?keyword=WHA", ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("status 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses?status=DISABLED", ""))
		require.Equal(t, float64(1), data["total"])
		require.Equal(t, "WHC", data["items"].([]any)[0].(map[string]any)["code"])
	})
	t.Run("type 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses?type=COLD", ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("范围外用户 fail-closed 不可见", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(999999))
		data := expectOKData(t, doReq(r2, http.MethodGet, "/api/warehouses", ""))
		require.Equal(t, float64(0), data["total"])
		items := data["items"].([]any)
		require.Empty(t, items)
	})
	for _, tc := range []struct {
		name  string
		query string
		field string
	}{
		{"page 非法", "?page=0", "page"},
		{"page 非数字", "?page=abc", "page"},
		{"pageSize 超上限", "?pageSize=101", "pageSize"},
		{"status 非法", "?status=OFF", "status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses"+tc.query, ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			require.Equal(t, tc.field, detailsOf(t, m)["field"])
		})
	}
}

// ---- POST /api/warehouses ----

func TestWarehouseRoutes_CreateWarehouse(t *testing.T) {
	s, repo := newTestService()
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功并规范化", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/warehouses",
			`{"code":" WH01 ","name":" 一号仓 ","address":"上海市","contact":"张三","phone":"021-1234","area":100.5,"capacity":50}`))
		require.Equal(t, "WH01", data["code"], "编码应去空白")
		require.Equal(t, "一号仓", data["name"])
		require.Equal(t, "NORMAL", data["type"], "type 缺省 NORMAL")
		require.Equal(t, "ENABLED", data["status"])
		require.Equal(t, float64(100.5), data["area"])
		require.NotEmpty(t, data["id"])
		require.NotEmpty(t, data["created_at"])
		// 回读闭环（GET /:id）
		got := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses/"+data["id"].(string), ""))
		require.Equal(t, "WH01", got["code"])
		require.Equal(t, "张三", got["contact"])
		// 操作者归因（actorOf → CreatedBy）
		stored, err := repo.FindWarehouseByID(ctx, mustParseID(t, data["id"].(string)))
		mustOK(t, err)
		require.Equal(t, int64(1), stored.CreatedBy.Int64())
	})
	t.Run("缺必填字段", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/warehouses", `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		d := detailsOf(t, m)
		require.Equal(t, "请求体格式错误", d["reason"])
		fields := d["fields"].(map[string]any)
		require.Contains(t, fields, "code")
		require.Contains(t, fields, "name")
	})
	t.Run("非法 JSON", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/warehouses", `{bad`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		d := detailsOf(t, m)
		require.Equal(t, "请求体格式错误", d["reason"])
		require.Nil(t, d["fields"], "语法错误不透传字段明细")
	})
	t.Run("空请求体", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/warehouses", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "请求体格式错误", detailsOf(t, m)["reason"])
	})
	t.Run("业务校验失败定位字段", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/warehouses", `{"code":"仓1","name":"n"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "code", detailsOf(t, m)["field"])
	})
	t.Run("编码重复 409", func(t *testing.T) {
		seedWarehouse(t, s, "WHX")
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/warehouses", `{"code":"WHX","name":"重复"}`),
			http.StatusConflict, "WAREHOUSE_CODE_EXISTS")
		require.Equal(t, "WHX", detailsOf(t, m)["code"])
	})
}

// ---- GET /api/warehouses/:id ----

func TestWarehouseRoutes_GetWarehouse(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功返回完整视图", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses/"+idStr(w1.ID), ""))
		require.Equal(t, "WHA", data["code"])
		require.Equal(t, "仓库-WHA", data["name"])
		require.Equal(t, idStr(w1.ID), data["id"])
		require.Equal(t, "ENABLED", data["status"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/99999", ""), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
	t.Run("范围外按不存在处理", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(w2.ID.Int64()))
		expectBiz(t, doReq(r2, http.MethodGet, "/api/warehouses/"+idStr(w1.ID), ""), http.StatusNotFound, "COMMON_NOT_FOUND")
	})
	for _, tc := range []struct {
		name, raw string
	}{
		{"非数字", "abc"}, {"零", "0"}, {"负数", "-1"}, {"浮点", "1.5"},
	} {
		t.Run("非法 id "+tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/"+tc.raw, ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			require.Equal(t, "id", detailsOf(t, m)["field"])
		})
	}
}

// ---- PUT /api/warehouses/:id ----

func TestWarehouseRoutes_UpdateWarehouse(t *testing.T) {
	s, repo := newTestService()
	seedWarehouse(t, s, "WH01")
	w2 := seedWarehouse(t, s, "WH02")
	r := newHandlerEngine(t, s, superUC())

	t.Run("部分更新生效且不改 status", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(w2.ID),
			`{"name":"二号仓改","area":9.9,"phone":"010-8888"}`))
		require.Equal(t, "二号仓改", data["name"])
		require.Equal(t, float64(9.9), data["area"])
		require.Equal(t, "010-8888", data["phone"])
		require.Equal(t, "WH02", data["code"], "未提供字段不变更")
		require.Equal(t, "ENABLED", data["status"], "status 不在更新面（dto.go §注释）")
	})
	t.Run("操作者归因 updated_by", func(t *testing.T) {
		stored, err := repo.FindWarehouseByID(ctx, w2.ID.Int64())
		mustOK(t, err)
		require.Equal(t, int64(1), stored.UpdatedBy.Int64())
	})
	t.Run("空更新体拒绝", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(w2.ID), `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "body", detailsOf(t, m)["field"])
	})
	t.Run("编码冲突 409", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(w2.ID), `{"code":"WH01"}`),
			http.StatusConflict, "WAREHOUSE_CODE_EXISTS")
		require.Equal(t, "WH01", detailsOf(t, m)["code"])
	})
	t.Run("非法编码格式 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(w2.ID), `{"code":"@!"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "code", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/99999", `{"name":"x"}`), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/abc", `{"name":"x"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- DELETE /api/warehouses/:id ----

func TestWarehouseRoutes_DeleteWarehouse(t *testing.T) {
	s, _ := newTestService()
	empty := seedWarehouse(t, s, "WHE")
	withZone := seedWarehouse(t, s, "WHZ")
	seedZone(t, s, withZone.ID.Int64(), "A")
	r := newHandlerEngine(t, s, superUC())

	t.Run("空仓库软删成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodDelete, "/api/warehouses/"+idStr(empty.ID), ""))
		require.Equal(t, true, data["deleted"])
		expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/"+idStr(empty.ID), ""), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
	t.Run("存在库区阻塞 409", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodDelete, "/api/warehouses/"+idStr(withZone.ID), ""),
			http.StatusConflict, "WAREHOUSE_HAS_ZONES")
		require.Equal(t, float64(1), detailsOf(t, m)["zones"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodDelete, "/api/warehouses/99999", ""), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
}

// ---- PUT /api/warehouses/:id/status ----

func TestWarehouseRoutes_UpdateWarehouseStatus(t *testing.T) {
	s, repo := newTestService()
	free := seedWarehouse(t, s, "WHF")
	blocked := seedWarehouse(t, s, "WHB")
	seedZone(t, s, blocked.ID.Int64(), "A")
	r := newHandlerEngine(t, s, superUC())

	t.Run("停用成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(free.ID)+"/status", `{"status":"DISABLED"}`))
		require.Equal(t, "DISABLED", data["status"])
		stored, err := repo.FindWarehouseByID(ctx, free.ID.Int64())
		mustOK(t, err)
		require.Equal(t, StatusDisabled, stored.Status)
	})
	t.Run("重新启用成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(free.ID)+"/status", `{"status":"ENABLED"}`))
		require.Equal(t, "ENABLED", data["status"])
	})
	t.Run("启用库区阻塞停用 409", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(blocked.ID)+"/status", `{"status":"DISABLED"}`),
			http.StatusConflict, "WAREHOUSE_HAS_ENABLED_ZONES")
		require.Equal(t, float64(1), detailsOf(t, m)["enabled_zones"])
	})
	for _, tc := range []struct {
		name, body string
	}{
		{"非法状态值", `{"status":"OFF"}`},
		{"缺 status 字段", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/"+idStr(free.ID)+"/status", tc.body), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			d := detailsOf(t, m)
			if fields, ok := d["fields"].(map[string]any); ok {
				require.Contains(t, fields, "status")
			} else {
				require.Equal(t, "status", d["field"])
			}
		})
	}
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/warehouses/99999/status", `{"status":"DISABLED"}`), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
}

// ---- GET /api/warehouses/:id/map ----

func TestWarehouseRoutes_WarehouseMap(t *testing.T) {
	s, _ := newTestService()
	whID := seedMapTree(t, s)
	r := newHandlerEngine(t, s, superUC())

	t.Run("完整层级树与占用着色", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/warehouses/"+idInt(whID)+"/map", ""))
		wh := data["warehouse"].(map[string]any)
		require.Equal(t, "WH01", wh["code"])
		zones := data["zones"].([]any)
		require.Len(t, zones, 2, "区按编码排序")
		zoneA := zones[0].(map[string]any)
		require.Equal(t, "A", zoneA["zone"].(map[string]any)["code"])
		shelvesA := zoneA["shelves"].([]any)
		require.Len(t, shelvesA, 1)
		binsA := shelvesA[0].(map[string]any)["bins"].([]any)
		require.Len(t, binsA, 2)
		require.Equal(t, "IDLE", binsA[0].(map[string]any)["occupancy_status"])
		partial := binsA[1].(map[string]any)
		require.Equal(t, "PARTIAL", partial["occupancy_status"])
		require.Equal(t, float64(6), partial["current_capacity"])
		// 未注入占用读取器：不出现库存数量字段（requirements.md §10 禁造假数据）
		require.Nil(t, binsA[0].(map[string]any)["quantity"])
		require.Equal(t, "FULL", zones[1].(map[string]any)["shelves"].([]any)[0].(map[string]any)["bins"].([]any)[0].(map[string]any)["occupancy_status"])
	})
	t.Run("占用读取器失败整图 503", func(t *testing.T) {
		s.occ = &fakeOccupancy{err: io.EOF}
		expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/"+idInt(whID)+"/map", ""),
			http.StatusServiceUnavailable, "COMMON_SERVICE_UNAVAILABLE")
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/99999/map", ""), http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
	t.Run("范围外按不存在处理", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(42))
		expectBiz(t, doReq(r2, http.MethodGet, "/api/warehouses/"+idInt(whID)+"/map", ""), http.StatusNotFound, "COMMON_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodGet, "/api/warehouses/abc/map", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- GET /api/zones ----

func TestZoneRoutes_List(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	z1 := seedZone(t, s, w1.ID.Int64(), "A1")
	seedZone(t, s, w1.ID.Int64(), "A2")
	seedZone(t, s, w2.ID.Int64(), "B1")
	mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z1.ID.Int64(), StatusDisabled))
	r := newHandlerEngine(t, s, superUC())

	t.Run("默认分页", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/zones", ""))
		require.Equal(t, float64(3), data["total"])
		items := data["items"].([]any)
		require.Equal(t, "STORAGE", items[0].(map[string]any)["zone_type"], "缺省类型")
	})
	t.Run("warehouseId 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/zones?warehouseId="+idStr(w2.ID), ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("status 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/zones?status=DISABLED", ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("keyword 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/zones?keyword=A2", ""))
		require.Equal(t, float64(1), data["total"])
	})
	for _, tc := range []struct {
		name, query, field string
	}{
		{"warehouseId 非数字", "?warehouseId=abc", "warehouseId"},
		{"warehouseId 非正整数", "?warehouseId=0", "warehouseId"},
		{"status 非法", "?status=X", "status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodGet, "/api/zones"+tc.query, ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			require.Equal(t, tc.field, detailsOf(t, m)["field"])
		})
	}
}

// ---- POST /api/zones ----

func TestZoneRoutes_Create(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功缺省类型", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/zones",
			`{"warehouse_id":`+idStr(w1.ID)+`,"code":"A","name":"存储区","capacity":8.8}`))
		require.Equal(t, "A", data["code"])
		require.Equal(t, "STORAGE", data["zone_type"])
		require.Equal(t, "ENABLED", data["status"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"], "锚点为归属仓库（出参 ID 为字符串）")
	})
	t.Run("显式类型", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/zones",
			`{"warehouse_id":`+idStr(w1.ID)+`,"code":"P","name":"拣货区","zone_type":"PICKING"}`))
		require.Equal(t, "PICKING", data["zone_type"])
	})
	t.Run("仓库内编码重复 409", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/zones",
			`{"warehouse_id":`+idStr(w1.ID)+`,"code":"A","name":"重复"}`), http.StatusConflict, "WAREHOUSE_ZONE_CODE_EXISTS")
	})
	t.Run("不同仓库同码合法", func(t *testing.T) {
		expectOKData(t, doReq(r, http.MethodPost, "/api/zones",
			`{"warehouse_id":`+idStr(w2.ID)+`,"code":"A","name":"另一仓同码"}`))
	})
	t.Run("仓库不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/zones", `{"warehouse_id":99999,"code":"B","name":"n"}`),
			http.StatusNotFound, "WAREHOUSE_NOT_FOUND")
	})
	t.Run("父仓库停用 400", func(t *testing.T) {
		// 独立仓库（w2 上已有启用库区，停用会被 WAREHOUSE_HAS_ENABLED_ZONES 阻塞）。
		w3 := seedWarehouse(t, s, "WHC")
		mustOK(t, s.UpdateWarehouseStatus(ctx, superActor(), w3.ID.Int64(), StatusDisabled))
		expectBiz(t, doReq(r, http.MethodPost, "/api/zones",
			`{"warehouse_id":`+idStr(w3.ID)+`,"code":"C","name":"n"}`), http.StatusBadRequest, "WAREHOUSE_PARENT_DISABLED")
	})
	t.Run("缺必填字段", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/zones", `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		fields := detailsOf(t, m)["fields"].(map[string]any)
		// fields 键为结构体字段名小写（response.BindErrorDetails：strings.ToLower(fe.Field())）。
		require.Contains(t, fields, "warehouseid")
		require.Contains(t, fields, "code")
		require.Contains(t, fields, "name")
	})
}

// ---- GET /api/zones/:id ----

func TestZoneRoutes_Get(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	w2 := seedWarehouse(t, s, "WHB")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功返回完整视图", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/zones/"+idStr(z1.ID), ""))
		require.Equal(t, "A", data["code"])
		require.Equal(t, "库区-A", data["name"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"])
		require.Equal(t, "STORAGE", data["zone_type"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodGet, "/api/zones/99999", ""), http.StatusNotFound, "WAREHOUSE_ZONE_NOT_FOUND")
	})
	t.Run("范围外按不存在处理", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(w2.ID.Int64()))
		expectBiz(t, doReq(r2, http.MethodGet, "/api/zones/"+idStr(z1.ID), ""), http.StatusNotFound, "COMMON_NOT_FOUND")
	})
	for _, tc := range []struct{ name, raw string }{
		{"非数字", "abc"}, {"零", "0"},
	} {
		t.Run("非法 id "+tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodGet, "/api/zones/"+tc.raw, ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			require.Equal(t, "id", detailsOf(t, m)["field"])
		})
	}
}

// ---- PUT /api/zones/:id ----

func TestZoneRoutes_Update(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	zA := seedZone(t, s, w1.ID.Int64(), "A")
	seedZone(t, s, w1.ID.Int64(), "B")
	r := newHandlerEngine(t, s, superUC())

	t.Run("部分更新生效且锚点不可变", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID),
			`{"name":"存储区改","zone_type":"PICKING","capacity":5.5}`))
		require.Equal(t, "存储区改", data["name"])
		require.Equal(t, "PICKING", data["zone_type"])
		require.Equal(t, float64(5.5), data["capacity"])
		require.Equal(t, "A", data["code"], "未提供字段不变更")
		require.Equal(t, idStr(w1.ID), data["warehouse_id"], "warehouse_id 不可变更")
	})
	t.Run("操作者归因 updated_by", func(t *testing.T) {
		stored, err := repo.FindZoneByID(ctx, zA.ID.Int64())
		mustOK(t, err)
		require.Equal(t, int64(1), stored.UpdatedBy.Int64())
	})
	t.Run("空更新体拒绝", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID), `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "body", detailsOf(t, m)["field"])
	})
	t.Run("编码冲突 409", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID), `{"code":"B"}`), http.StatusConflict, "WAREHOUSE_ZONE_CODE_EXISTS")
	})
	t.Run("改回自身编码放行", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID), `{"code":"A"}`))
		require.Equal(t, "A", data["code"])
	})
	t.Run("非法类型值 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID), `{"zone_type":"pick"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "zone_type", detailsOf(t, m)["field"])
	})
	t.Run("负容量 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(zA.ID), `{"capacity":-1}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "capacity", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/zones/99999", `{"name":"x"}`), http.StatusNotFound, "WAREHOUSE_ZONE_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/abc", `{"name":"x"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- PUT /api/zones/:id/status ----

func TestZoneRoutes_UpdateStatus(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	free := seedZone(t, s, w1.ID.Int64(), "A")
	blocked := seedZone(t, s, w1.ID.Int64(), "B")
	seedShelf(t, s, blocked.ID.Int64(), "SB1")
	r := newHandlerEngine(t, s, superUC())

	t.Run("停用成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(free.ID)+"/status", `{"status":"DISABLED"}`))
		require.Equal(t, "DISABLED", data["status"])
		stored, err := repo.FindZoneByID(ctx, free.ID.Int64())
		mustOK(t, err)
		require.Equal(t, StatusDisabled, stored.Status)
	})
	t.Run("启用货架阻塞 409", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(blocked.ID)+"/status", `{"status":"DISABLED"}`),
			http.StatusConflict, "WAREHOUSE_ZONE_HAS_ENABLED_SHELVES")
		require.Equal(t, float64(1), detailsOf(t, m)["enabled_shelves"])
	})
	t.Run("非法状态值 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/zones/"+idStr(free.ID)+"/status", `{"status":"OFF"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "status", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/zones/99999/status", `{"status":"DISABLED"}`), http.StatusNotFound, "WAREHOUSE_ZONE_NOT_FOUND")
	})
}

// ---- GET /api/shelves ----

func TestShelfRoutes_List(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	z2 := seedZone(t, s, w2.ID.Int64(), "B")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	seedShelf(t, s, z1.ID.Int64(), "S2")
	seedShelf(t, s, z2.ID.Int64(), "S3")
	mustOK(t, s.UpdateShelfStatus(ctx, superActor(), sh1.ID.Int64(), StatusDisabled))
	r := newHandlerEngine(t, s, superUC())

	t.Run("默认分页", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/shelves", ""))
		require.Equal(t, float64(3), data["total"])
		items := data["items"].([]any)
		require.Equal(t, float64(1), items[0].(map[string]any)["layers"], "缺省层列")
	})
	t.Run("zoneId 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/shelves?zoneId="+idStr(z2.ID), ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("warehouseId 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/shelves?warehouseId="+idStr(w2.ID), ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("status 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/shelves?status=DISABLED", ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("非法 zoneId 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodGet, "/api/shelves?zoneId=abc", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "zoneId", detailsOf(t, m)["field"])
	})
}

// ---- POST /api/shelves ----

func TestShelfRoutes_Create(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	z2 := seedZone(t, s, w2.ID.Int64(), "B")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功缺省层列", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z1.ID)+`,"code":"S01"}`))
		require.Equal(t, "S01", data["code"])
		require.Equal(t, float64(1), data["layers"])
		require.Equal(t, float64(1), data["columns"])
		require.Equal(t, "ENABLED", data["status"])
		require.Equal(t, idStr(z1.ID), data["zone_id"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"], "锚点以 zone 归属为准")
	})
	t.Run("显式层列与容量", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z1.ID)+`,"code":"S02","layers":4,"columns":6,"capacity":8}`))
		require.Equal(t, float64(4), data["layers"])
		require.Equal(t, float64(6), data["columns"])
		require.Equal(t, float64(8), data["capacity"])
	})
	t.Run("warehouse_id 与 zone 归属不一致 400", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z1.ID)+`,"warehouse_id":`+idStr(w2.ID)+`,"code":"S09"}`),
			http.StatusBadRequest, "WAREHOUSE_HIERARCHY_MISMATCH")
	})
	t.Run("库区内编码重复 409", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z1.ID)+`,"code":"S01"}`), http.StatusConflict, "WAREHOUSE_SHELF_CODE_EXISTS")
	})
	t.Run("不同库区同码合法", func(t *testing.T) {
		expectOKData(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z2.ID)+`,"code":"S01"}`))
	})
	t.Run("库区不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/shelves", `{"zone_id":99999,"code":"SX"}`),
			http.StatusNotFound, "WAREHOUSE_ZONE_NOT_FOUND")
	})
	t.Run("父库区停用 400", func(t *testing.T) {
		// 独立空库区（z1/z2 已有启用货架，停用会被 WAREHOUSE_ZONE_HAS_ENABLED_SHELVES 阻塞）。
		z3 := seedZone(t, s, w1.ID.Int64(), "C")
		mustOK(t, s.UpdateZoneStatus(ctx, superActor(), z3.ID.Int64(), StatusDisabled))
		expectBiz(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z3.ID)+`,"code":"SX"}`), http.StatusBadRequest, "WAREHOUSE_PARENT_DISABLED")
	})
	t.Run("层列越界 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/shelves",
			`{"zone_id":`+idStr(z1.ID)+`,"code":"S10","layers":0}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "layers", detailsOf(t, m)["field"])
	})
}

// ---- GET /api/shelves/:id ----

func TestShelfRoutes_Get(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	w2 := seedWarehouse(t, s, "WHB")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功返回完整视图", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/shelves/"+idStr(sh1.ID), ""))
		require.Equal(t, "S1", data["code"])
		require.Equal(t, idStr(z1.ID), data["zone_id"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"])
		require.Equal(t, float64(1), data["layers"])
		require.Equal(t, "ENABLED", data["status"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodGet, "/api/shelves/99999", ""), http.StatusNotFound, "WAREHOUSE_SHELF_NOT_FOUND")
	})
	t.Run("范围外按不存在处理", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(w2.ID.Int64()))
		expectBiz(t, doReq(r2, http.MethodGet, "/api/shelves/"+idStr(sh1.ID), ""), http.StatusNotFound, "COMMON_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodGet, "/api/shelves/abc", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- PUT /api/shelves/:id ----

func TestShelfRoutes_Update(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	seedShelf(t, s, z1.ID.Int64(), "S2")
	r := newHandlerEngine(t, s, superUC())

	t.Run("部分更新生效且锚点不可变", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(sh1.ID),
			`{"code":"S1X","layers":3,"columns":5,"capacity":2.5}`))
		require.Equal(t, "S1X", data["code"])
		require.Equal(t, float64(3), data["layers"])
		require.Equal(t, float64(5), data["columns"])
		require.Equal(t, float64(2.5), data["capacity"])
		require.Equal(t, idStr(z1.ID), data["zone_id"], "zone_id 不可变更")
		require.Equal(t, "ENABLED", data["status"], "status 不在更新面")
	})
	t.Run("操作者归因 updated_by", func(t *testing.T) {
		stored, err := repo.FindShelfByID(ctx, sh1.ID.Int64())
		mustOK(t, err)
		require.Equal(t, int64(1), stored.UpdatedBy.Int64())
	})
	t.Run("空更新体拒绝", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(sh1.ID), `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "body", detailsOf(t, m)["field"])
	})
	t.Run("编码冲突 409", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(sh1.ID), `{"code":"S2"}`), http.StatusConflict, "WAREHOUSE_SHELF_CODE_EXISTS")
	})
	t.Run("层列越界 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(sh1.ID), `{"columns":0}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "columns", detailsOf(t, m)["field"])
	})
	t.Run("负容量 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(sh1.ID), `{"capacity":-2}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "capacity", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/99999", `{"layers":2}`), http.StatusNotFound, "WAREHOUSE_SHELF_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/abc", `{"layers":2}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- PUT /api/shelves/:id/status ----

func TestShelfRoutes_UpdateStatus(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	free := seedShelf(t, s, z1.ID.Int64(), "S1")
	blocked := seedShelf(t, s, z1.ID.Int64(), "S2")
	seedBin(t, s, blocked.ID.Int64(), "SB01")
	r := newHandlerEngine(t, s, superUC())

	t.Run("停用成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(free.ID)+"/status", `{"status":"DISABLED"}`))
		require.Equal(t, "DISABLED", data["status"])
		stored, err := repo.FindShelfByID(ctx, free.ID.Int64())
		mustOK(t, err)
		require.Equal(t, StatusDisabled, stored.Status)
	})
	t.Run("启用库位阻塞 409", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(blocked.ID)+"/status", `{"status":"DISABLED"}`),
			http.StatusConflict, "WAREHOUSE_SHELF_HAS_ENABLED_BINS")
		require.Equal(t, float64(1), detailsOf(t, m)["enabled_bins"])
	})
	t.Run("非法状态值 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/"+idStr(free.ID)+"/status", `{"status":"OFF"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "status", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/shelves/99999/status", `{"status":"DISABLED"}`), http.StatusNotFound, "WAREHOUSE_SHELF_NOT_FOUND")
	})
}

// ---- GET /api/bins ----

func TestBinRoutes_List(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	sh2 := seedShelf(t, s, z1.ID.Int64(), "S2")
	b1, err := s.CreateBin(ctx, superActor(), BinCreateInput{ShelfID: sh1.ID.Int64(), Code: "B1", BinType: "STORAGE"})
	mustOK(t, err)
	b2 := seedBin(t, s, sh2.ID.Int64(), "B2")
	mustOK(t, s.DeleteBin(ctx, superActor(), b2.ID.Int64())) // 软删行不进列表
	r := newHandlerEngine(t, s, superUC())

	t.Run("默认分页且排除软删", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/bins", ""))
		require.Equal(t, float64(1), data["total"])
		item := data["items"].([]any)[0].(map[string]any)
		require.Equal(t, "B1", item["code"])
		require.NotNil(t, item["stock_sku_count"], "存量展示列恒透出（fake 桩为零值）")
	})
	t.Run("shelfId 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/bins?shelfId="+idStr(sh2.ID), ""))
		require.Equal(t, float64(0), data["total"])
	})
	t.Run("binType 过滤", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/bins?binType=STORAGE", ""))
		require.Equal(t, float64(1), data["total"])
		data = expectOKData(t, doReq(r, http.MethodGet, "/api/bins?binType=PICK", ""))
		require.Equal(t, float64(0), data["total"])
	})
	t.Run("status 过滤", func(t *testing.T) {
		mustOK(t, s.UpdateBinStatus(ctx, superActor(), b1.ID.Int64(), StatusDisabled))
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/bins?status=DISABLED", ""))
		require.Equal(t, float64(1), data["total"])
	})
	t.Run("非法 shelfId 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodGet, "/api/bins?shelfId=abc", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "shelfId", detailsOf(t, m)["field"])
	})
}

// ---- POST /api/bins ----

func TestBinRoutes_Create(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	w2 := seedWarehouse(t, s, "WHB")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	z2 := seedZone(t, s, w2.ID.Int64(), "B")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	sh2 := seedShelf(t, s, z1.ID.Int64(), "S2")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功缺省属性", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"A-S1-0101"}`))
		require.Equal(t, "PICK", data["bin_type"])
		require.Equal(t, float64(1), data["layer"])
		require.Equal(t, float64(1), data["column_no"])
		require.Equal(t, float64(0), data["current_capacity"], "创建恒为 0")
		require.Equal(t, idStr(sh1.ID), data["shelf_id"])
		require.Equal(t, idStr(z1.ID), data["zone_id"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"], "锚点以 shelf 归属为准")
	})
	t.Run("显式属性", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"A-S1-0102","bin_type":"STORAGE","layer":2,"column_no":3,"max_capacity":12.5}`))
		require.Equal(t, "STORAGE", data["bin_type"])
		require.Equal(t, float64(2), data["layer"])
		require.Equal(t, float64(3), data["column_no"])
		require.Equal(t, float64(12.5), data["max_capacity"])
	})
	t.Run("zone 归属不一致 400", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"zone_id":`+idStr(z2.ID)+`,"code":"X1"}`),
			http.StatusBadRequest, "WAREHOUSE_HIERARCHY_MISMATCH")
	})
	t.Run("warehouse 归属不一致 400", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"warehouse_id":`+idStr(w2.ID)+`,"code":"X2"}`),
			http.StatusBadRequest, "WAREHOUSE_HIERARCHY_MISMATCH")
	})
	t.Run("仓库内编码重复（跨货架）409", func(t *testing.T) {
		seedBin(t, s, sh2.ID.Int64(), "DUP")
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"DUP"}`), http.StatusConflict, "WAREHOUSE_BIN_CODE_EXISTS")
		require.Equal(t, "DUP", detailsOf(t, m)["code"])
	})
	t.Run("货架不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPost, "/api/bins", `{"shelf_id":99999,"code":"X3"}`),
			http.StatusNotFound, "WAREHOUSE_SHELF_NOT_FOUND")
	})
	t.Run("负容量 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"X4","max_capacity":-3}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "max_capacity", detailsOf(t, m)["field"])
	})
	t.Run("非法 bin_type 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"X5","bin_type":"pick"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "bin_type", detailsOf(t, m)["field"])
	})
	t.Run("层越界 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPost, "/api/bins",
			`{"shelf_id":`+idStr(sh1.ID)+`,"code":"X6","layer":0}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "layer", detailsOf(t, m)["field"])
	})
}

// ---- GET /api/bins/:id ----

func TestBinRoutes_Get(t *testing.T) {
	s, _ := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	b1 := seedBin(t, s, sh1.ID.Int64(), "B1")
	w2 := seedWarehouse(t, s, "WHB")
	r := newHandlerEngine(t, s, superUC())

	t.Run("成功返回完整视图", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodGet, "/api/bins/"+idStr(b1.ID), ""))
		require.Equal(t, "B1", data["code"])
		require.Equal(t, idStr(sh1.ID), data["shelf_id"])
		require.Equal(t, idStr(z1.ID), data["zone_id"])
		require.Equal(t, idStr(w1.ID), data["warehouse_id"])
		require.Equal(t, "PICK", data["bin_type"])
		require.Equal(t, float64(0), data["current_capacity"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodGet, "/api/bins/99999", ""), http.StatusNotFound, "WAREHOUSE_BIN_NOT_FOUND")
	})
	t.Run("范围外按不存在处理", func(t *testing.T) {
		r2 := newHandlerEngine(t, s, scopedUC(w2.ID.Int64()))
		expectBiz(t, doReq(r2, http.MethodGet, "/api/bins/"+idStr(b1.ID), ""), http.StatusNotFound, "COMMON_NOT_FOUND")
	})
	for _, tc := range []struct{ name, raw string }{
		{"非数字", "abc"}, {"零", "0"},
	} {
		t.Run("非法 id "+tc.name, func(t *testing.T) {
			m := expectBiz(t, doReq(r, http.MethodGet, "/api/bins/"+tc.raw, ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
			require.Equal(t, "id", detailsOf(t, m)["field"])
		})
	}
}

// ---- PUT /api/bins/:id ----

func TestBinRoutes_Update(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	sh2 := seedShelf(t, s, z1.ID.Int64(), "S2")
	b1 := seedBin(t, s, sh1.ID.Int64(), "B01")
	seedBin(t, s, sh2.ID.Int64(), "B02")
	r := newHandlerEngine(t, s, superUC())

	t.Run("部分更新生效且 current_capacity 不受输入影响", func(t *testing.T) {
		// 先模拟上架维护占用，再更新——更新入参不存在 current_capacity 字段（dto.go BinUpdateInput）。
		stored, err := repo.FindBinByID(ctx, b1.ID.Int64())
		mustOK(t, err)
		stored.CurrentCapacity = 5
		repo.bins[b1.ID.Int64()] = stored
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID),
			`{"code":"B01X","bin_type":"STORAGE","layer":2,"column_no":4,"max_capacity":20}`))
		require.Equal(t, "B01X", data["code"])
		require.Equal(t, "STORAGE", data["bin_type"])
		require.Equal(t, float64(2), data["layer"])
		require.Equal(t, float64(4), data["column_no"])
		require.Equal(t, float64(20), data["max_capacity"])
		require.Equal(t, float64(5), data["current_capacity"], "占用容量由业务维护，更新不触及")
		stored, err = repo.FindBinByID(ctx, b1.ID.Int64())
		mustOK(t, err)
		require.Equal(t, float64(5), stored.CurrentCapacity)
	})
	t.Run("空更新体拒绝", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID), `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "body", detailsOf(t, m)["field"])
	})
	t.Run("编码冲突 409", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID), `{"code":"B02"}`), http.StatusConflict, "WAREHOUSE_BIN_CODE_EXISTS")
	})
	t.Run("列越界 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID), `{"column_no":0}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "column_no", detailsOf(t, m)["field"])
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/bins/99999", `{"layer":2}`), http.StatusNotFound, "WAREHOUSE_BIN_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/bins/abc", `{"layer":2}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- DELETE /api/bins/:id ----

func TestBinRoutes_Delete(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	occupied := seedBin(t, s, sh1.ID.Int64(), "BOCC")
	free := seedBin(t, s, sh1.ID.Int64(), "BFREE")
	r := newHandlerEngine(t, s, superUC())

	t.Run("占用容量阻塞 409", func(t *testing.T) {
		stored, err := repo.FindBinByID(ctx, occupied.ID.Int64())
		mustOK(t, err)
		stored.CurrentCapacity = 5 // 模拟上架维护结果
		repo.bins[occupied.ID.Int64()] = stored
		m := expectBiz(t, doReq(r, http.MethodDelete, "/api/bins/"+idStr(occupied.ID), ""),
			http.StatusConflict, "WAREHOUSE_BIN_OCCUPIED")
		require.Equal(t, float64(5), detailsOf(t, m)["current_capacity"])
	})
	t.Run("空闲软删成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodDelete, "/api/bins/"+idStr(free.ID), ""))
		require.Equal(t, true, data["deleted"])
		expectBiz(t, doReq(r, http.MethodGet, "/api/bins/"+idStr(free.ID), ""), http.StatusNotFound, "WAREHOUSE_BIN_NOT_FOUND")
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodDelete, "/api/bins/99999", ""), http.StatusNotFound, "WAREHOUSE_BIN_NOT_FOUND")
	})
	t.Run("非法 id 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodDelete, "/api/bins/abc", ""), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "id", detailsOf(t, m)["field"])
	})
}

// ---- PUT /api/bins/:id/status ----

func TestBinRoutes_UpdateStatus(t *testing.T) {
	s, repo := newTestService()
	w1 := seedWarehouse(t, s, "WHA")
	z1 := seedZone(t, s, w1.ID.Int64(), "A")
	sh1 := seedShelf(t, s, z1.ID.Int64(), "S1")
	b1 := seedBin(t, s, sh1.ID.Int64(), "B1")
	r := newHandlerEngine(t, s, superUC())

	t.Run("停用成功（叶子节点无子级校验）", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID)+"/status", `{"status":"DISABLED"}`))
		require.Equal(t, "DISABLED", data["status"])
		stored, err := repo.FindBinByID(ctx, b1.ID.Int64())
		mustOK(t, err)
		require.Equal(t, StatusDisabled, stored.Status)
	})
	t.Run("重新启用成功", func(t *testing.T) {
		data := expectOKData(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID)+"/status", `{"status":"ENABLED"}`))
		require.Equal(t, "ENABLED", data["status"])
	})
	t.Run("非法状态值 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID)+"/status", `{"status":"OFF"}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		require.Equal(t, "status", detailsOf(t, m)["field"])
	})
	t.Run("缺 status 字段 400", func(t *testing.T) {
		m := expectBiz(t, doReq(r, http.MethodPut, "/api/bins/"+idStr(b1.ID)+"/status", `{}`), http.StatusBadRequest, "COMMON_INVALID_PARAM")
		fields := detailsOf(t, m)["fields"].(map[string]any)
		require.Contains(t, fields, "status")
	})
	t.Run("不存在 404", func(t *testing.T) {
		expectBiz(t, doReq(r, http.MethodPut, "/api/bins/99999/status", `{"status":"DISABLED"}`), http.StatusNotFound, "WAREHOUSE_BIN_NOT_FOUND")
	})
}
