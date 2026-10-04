package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/config"
)

func newTestServer(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)
	// M1 全量装配（backend-m1-plan §5.3）：域已实现，db==nil 属装配错误（各域启动期
	// fail-fast），故 db 用假驱动 gorm 句柄、rdb 用不可达地址的 Redis 客户端——
	// 单测不依赖 PostgreSQL/Redis/网络（见 fakedb_test.go 头注）。
	// 假驱动 Ping 必败 + Redis 不可达：/ready 探测据此判 DOWN，
	// 支撑 TestReadyFailsWithoutDependencies 的"依赖不可达"语义。
	db, err := openFakeGorm()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:0", // 端口 0：立即连接失败，不触网
		DialTimeout: time.Millisecond,
		MaxRetries:  -1, // 关闭重试，/ready 探测立即失败
	})
	// M3（backend-m3-plan §12.1）：queue Runtime 以 inline 降级形态装配（redis=nil，
	// 不依赖 Redis/网络）；存储根指向临时目录（/ready 存储探测不落仓库工作目录）。
	rt := asynqx.NewRuntime(nil, asynqx.Config{}, nil)
	return New(cfg, db, rdb, rt)
}

// newTestConfig 默认配置 + 存储根指向临时目录（/ready 存储探测与 datax store 装配
// 均消费 storage.root；默认值 ./data/files 会在测试工作目录建目录，测试隔离）。
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	require.NoError(t, err)
	cfg.Storage.Root = t.TempDir()
	return cfg
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	return m
}

func TestHealthLivenessWithRequestID(t *testing.T) {
	r := newTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, float64(0), body["code"]) // 探针也走统一信封
	require.Equal(t, "UP", body["data"].(map[string]any)["status"])
	require.NotEmpty(t, rec.Header().Get("X-Request-ID")) // RequestID 中间件已挂载
}

func TestReadyFailsWithoutDependencies(t *testing.T) {
	r := newTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, "COMMON_SERVICE_UNAVAILABLE", body["code"])
	details := body["details"].(map[string]any)
	require.Equal(t, "DOWN", details["database"])
	// M3 存储根探测（§6.4）：临时目录可写 → storage=UP（数据库仍 DOWN，整体 503）。
	require.Equal(t, "UP", details["storage"])
}

func TestUnknownRouteReturnsEnvelope404(t *testing.T) {
	r := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, "COMMON_NOT_FOUND", body["code"]) // NoRoute 走统一信封
	require.NotEmpty(t, body["request_id"])
	require.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestMethodNotAllowedReturnsEnvelope(t *testing.T) {
	r := newTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/health", nil))

	// 405 语义（2026-10-02 复核修复：原误用 404/COMMON_NOT_FOUND，与 REST 方法不匹配语义有偏差），
	// 信封结构与 request_id 契约不变；NoRoute 的 404 由 TestUnknownRouteReturnsEnvelope404 覆盖。
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, "COMMON_METHOD_NOT_ALLOWED", body["code"])
	require.NotEmpty(t, body["request_id"])
}

func TestAPIProtectedGroupWired(t *testing.T) {
	// M1（plan §5.3）：/api 保护组挂 AuthRequired，域路由已注册——无凭证请求在
	// 中间件被拒（401 统一信封）。此处验证全量装配不 panic，且受保护接口的
	// 拒绝响应带信封与 request_id（T0 时代"stub 不注册路由 → 404"的前提已随
	// 域实现交付失效，断言随契约更新为 401 AUTH_TOKEN_INVALID）。
	r := newTestServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/products", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, "AUTH_TOKEN_INVALID", body["code"]) // AuthRequired 走统一信封
	require.NotEmpty(t, body["request_id"])
}

// ---- 受信代理（安全审查 S5）：设置与空值 ----

// newServerWithConfig 以指定配置装配引擎（其余装配同 newTestServer：假驱动 gorm +
// 不可达 Redis，单测不依赖 PostgreSQL/Redis/网络）。附带测试探针路由回显 ClientIP。
func newServerWithConfig(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := openFakeGorm()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:0",
		DialTimeout: time.Millisecond,
		MaxRetries:  -1,
	})
	if cfg.Storage.Root == "" || cfg.Storage.Root == "./data/files" {
		cfg.Storage.Root = t.TempDir() // 测试隔离：存储探测/装配不落仓库工作目录
	}
	rt := asynqx.NewRuntime(nil, asynqx.Config{}, nil)
	r := New(cfg, db, rdb, rt)
	r.GET("/__probe_ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return r
}

// 空值（默认配置）：不信任任何代理 → X-Forwarded-For 被忽略，ClientIP 取直连 RemoteAddr。
func TestTrustedProxiesEmptyIgnoresXFF(t *testing.T) {
	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Empty(t, cfg.Server.TrustedProxies) // 默认空 = 不信任任何代理

	r := newServerWithConfig(t, cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__probe_ip", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7") // 伪造头必须被忽略
	r.ServeHTTP(rec, req)

	require.Equal(t, "192.0.2.1", rec.Body.String()) // httptest 默认 RemoteAddr 的直连 IP
}

// 设置：RemoteAddr 落在受信网段内 → 按信任链取 X-Forwarded-For 为客户端 IP。
func TestTrustedProxiesHonorsXFFFromTrustedRemote(t *testing.T) {
	cfg, err := config.Load("")
	require.NoError(t, err)
	cfg.Server.TrustedProxies = []string{"192.0.2.0/24"} // 覆盖 httptest 默认 RemoteAddr 192.0.2.1

	r := newServerWithConfig(t, cfg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/__probe_ip", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.ServeHTTP(rec, req)

	require.Equal(t, "203.0.113.7", rec.Body.String())

	// 非受信来源伪造同样头：仍取直连 IP（信任范围不外溢）
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/__probe_ip", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.RemoteAddr = "198.51.100.9:1234"
	r.ServeHTTP(rec, req)
	require.Equal(t, "198.51.100.9", rec.Body.String())
}

// 非法网段：装配期 fail-fast（Validate 已拦截，此处为 router 层双保险语义）。
func TestTrustedProxiesInvalidCIDRFailsFast(t *testing.T) {
	cfg, err := config.Load("")
	require.NoError(t, err)
	cfg.Server.TrustedProxies = []string{"not-a-cidr"}
	require.Panics(t, func() { newServerWithConfig(t, cfg) })
}

// ---- M3 装配冒烟（backend-m3-plan §12.1）：五域路由注册与上传限宽 ----

// TestM3RoutesRegisteredSmoke M3 五域全量装配不 panic、路由已注册——无凭证请求在
// 认证中间件被拒（401/403 统一信封），而非 404（路由缺位）。设备端 activate 挂
// deviceAPI 组（无 AuthRequired），错误码为设备域语义而非 404。
func TestM3RoutesRegisteredSmoke(t *testing.T) {
	r := newTestServer(t)
	// 受保护 M3 路由（AuthRequired 先于权限点，无凭证 → 401）。
	for _, path := range []string{
		"/api/imports",           // datax 导入
		"/api/exports",           // datax 导出
		"/api/files",             // datax 文件中心
		"/api/prints/templates",  // printing 模板
		"/api/prints/tasks",      // printing 任务
		"/api/devices",           // devices 管理端
		"/api/scanner/logs",      // devices 扫码日志
		"/api/reports",           // reports 目录
		"/api/inventory/summary", // reports Dashboard 聚合
		"/api/logs/operations",   // sysops 审计日志
		"/api/system/monitor",    // sysops 监控
		"/api/system/jobs",       // sysops 定时任务
		"/api/system/backups",    // sysops 备份
		"/api/notifications",     // sysops 通知（认证即可用）
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, "GET %s 应 401（路由已注册、认证拦截）", path)
		body := decode(t, rec.Body.String())
		require.NotEmpty(t, body["request_id"], "GET %s 应带 request_id", path)
	}
	// 设备端（deviceAPI 组，设备令牌/双轨）：无凭证 → 401 设备语义，非 404。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/devices/activate", nil))
	require.NotEqual(t, http.StatusNotFound, rec.Code, "POST /api/devices/activate 应已注册")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scanner/resolve", nil))
	require.NotEqual(t, http.StatusNotFound, rec.Code, "POST /api/scanner/resolve 应已注册（双轨认证拦截）")
}

// TestUploadBodyLimitRelaxed 上传路由局部放宽 body 上限（backend-m3-plan §12.1）：
// 引擎级 route-aware 检查——2MB（> 全局 1MB、< 上传上限 20MB）在 POST /api/imports
// 放行至认证层（401），在非上传 POST 路由仍被 413 拦截（安全审查 S6 防线不外溢）。
func TestUploadBodyLimitRelaxed(t *testing.T) {
	r := newTestServer(t)
	big := make([]byte, 2<<20) // 2MB

	// 上传路由：body 上限放宽生效 → 进入认证层 401（若 413 即放宽失效）。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/imports", bytes.NewReader(big))
	req.ContentLength = int64(len(big))
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "POST /api/imports 2MB 应放宽放行至认证层")
	body := decode(t, rec.Body.String())
	require.Equal(t, "AUTH_TOKEN_INVALID", body["code"])

	// 非上传路由：全局 1MB 防线不变 → 413 统一信封。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/products", bytes.NewReader(big))
	req.ContentLength = int64(len(big))
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	body = decode(t, rec.Body.String())
	require.Equal(t, "COMMON_PAYLOAD_TOO_LARGE", body["code"])
}
