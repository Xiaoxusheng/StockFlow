package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// 路由装配与 handler 校验逻辑单元测试（不依赖 PG/Redis/网络）：
//   - RegisterRoutes 正确装配（gin 路由树无冲突：/inventory/:id 与 /inventory-ledgers 并存）；
//   - 全部路由挂 RequirePermission（未认证 401，permission.md §2）；
//   - 参数校验先行（api.md §4：非法分页参数 400 + details，不触达数据层）。
// 注意：db 以零值 *gorm.DB 占位——本测试只覆盖注册与不触库的校验路径；
// 触达数据层的并发/事务行为见 integration_test.go（//go:build integration）。

// fakeSKUChecker/fakeBinChecker 测试用跨域校验实现（真实实现归 masterdata/warehouse 域，
// 经 router 注入；integration_test.go 复用同型实现）。
type fakeSKUChecker struct{}

func (fakeSKUChecker) ExistsActive(ctx context.Context, skuID int64) (bool, error) {
	return skuID > 0, nil
}

type fakeBinChecker struct{}

func (fakeBinChecker) ExistsActive(ctx context.Context, warehouseID, binID int64) (bool, error) {
	return warehouseID > 0 && binID > 0, nil
}

func newWiredEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	RegisterRoutes(api, &gorm.DB{}, nil,
		WithSKUChecker(fakeSKUChecker{}), WithBinChecker(fakeBinChecker{}))
	return r
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	return m
}

func TestRegisterRoutesWiring(t *testing.T) {
	require.NotPanics(t, func() { newWiredEngine(t) },
		"合法装配（非 nil db + 两 Checker）不应 panic")

	// 装配错误 fail-fast（plan §4.3 规则① / deployment.md §3）。
	require.PanicsWithValue(t,
		"inventory 装配失败: db 为 nil（router 必须注入 GORM 句柄）",
		func() {
			gin.SetMode(gin.TestMode)
			RegisterRoutes(gin.New().Group("/api"), nil, nil)
		})
	require.PanicsWithValue(t,
		"inventory 装配失败: SKU/库位跨域校验未注入（router 必须传 WithSKUChecker/WithBinChecker，plan §4.3 规则①）",
		func() {
			gin.SetMode(gin.TestMode)
			RegisterRoutes(gin.New().Group("/api"), &gorm.DB{}, nil)
		})
}

func TestRegisterRoutesAllEndpointsRequirePermission(t *testing.T) {
	r := newWiredEngine(t)
	paths := []string{
		"/api/inventory",
		"/api/inventory/1",
		"/api/inventory/1/distribution",
		"/api/inventory-ledgers",
		"/api/batches",
		"/api/serials",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, "路径 %s 必须认证后访问", p)
		body := decodeEnvelope(t, rec)
		require.Equal(t, "COMMON_UNAUTHORIZED", body["code"], "路径 %s", p)
		require.Equal(t, response.RequestIDKey, "sf_request_id") // 常量自检（防漂移）
	}
}

func TestHandlerParamValidationBeforeDB(t *testing.T) {
	// 直接驱动 handler：参数校验发生在数据访问之前（db 为零值也不得触达）。
	gin.SetMode(gin.TestMode)
	h := &handler{svc: NewService(&gorm.DB{}, nil)}

	// 非法 page → 400 COMMON_INVALID_PARAM + details。
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/inventory?page=0", nil)
	h.listInventory(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := decodeEnvelope(t, rec)
	require.Equal(t, "COMMON_INVALID_PARAM", body["code"])
	require.NotNil(t, body["details"], "校验失败必须带 details（api.md §4）")

	// 非法 pageSize（超上限）→ 400。
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/inventory?pageSize=1000", nil)
	h.listInventory(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法 change_type → 400 + allowed 清单。
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/inventory-ledgers?change_type=HACK", nil)
	h.listLedgers(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body = decodeEnvelope(t, rec)
	details, ok := body["details"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, details, "allowed")

	// 非法序列号状态 → 400。
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/serials?status=NOPE", nil)
	h.listSerials(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法时间过滤 → 400。
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/inventory-ledgers?created_from=2026/01/01", nil)
	h.listLedgers(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法 created_to（早于 created_from）→ 400。
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/inventory-ledgers?created_from=2026-01-02&created_to=2026-01-01", nil)
	h.listLedgers(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestParseTimeParamLayouts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 每个用例使用全新 gin 上下文（gin.Context 缓存查询串，复用会读到旧参数）。
	parse := func(raw string, endOfDay bool) (*time.Time, bool, int) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, raw, nil)
		tm, ok := parseTimeParam(c, "t", endOfDay)
		return tm, ok, rec.Code
	}

	tm, ok, code := parse("/x?t=2026-01-02%2003:04:05", false)
	require.True(t, ok)
	require.NotNil(t, tm)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "2026-01-02 03:04:05", tm.Format("2006-01-02 15:04:05"))

	// 纯日期的 to 端点补全为当日 23:59:59（含上界）。
	tm, ok, _ = parse("/x?t=2026-01-02", true)
	require.True(t, ok)
	require.NotNil(t, tm)
	require.Equal(t, "2026-01-02 23:59:59", tm.Format("2006-01-02 15:04:05"))

	// 非法格式 → 写 400 响应并返回 false。
	_, ok, code = parse("/x?t=bad", false)
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, code)
}
