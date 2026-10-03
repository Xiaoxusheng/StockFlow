package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/logger"
	"github.com/stockflow/server/internal/response"
)

func newGin() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestRequestIDGenerated(t *testing.T) {
	r := newGin()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": c.GetString(response.RequestIDKey)})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	id := rec.Header().Get(RequestIDHeader)
	require.NotEmpty(t, id)
	require.Contains(t, rec.Body.String(), id) // 上下文与响应头一致（透传）
}

func TestRequestIDAcceptedFromHeader(t *testing.T) {
	r := newGin()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, c.GetString(response.RequestIDKey)) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(RequestIDHeader, "trace-123")
	r.ServeHTTP(rec, req)

	require.Equal(t, "trace-123", rec.Header().Get(RequestIDHeader)) // 前端传入优先（architecture.md §3.1）
	require.Equal(t, "trace-123", rec.Body.String())
}

func TestRequestIDRejectsUnsafeValue(t *testing.T) {
	r := newGin()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, c.GetString(response.RequestIDKey)) })

	for _, bad := range []string{"含中文", "a b", "x\ny", "", "over!"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set(RequestIDHeader, bad)
		r.ServeHTTP(rec, req)
		require.NotEqual(t, bad, rec.Body.String()) // 不安全值被替换为 UUID
	}
}

// S7 回归：合法但 65+ 字符的 request_id 截断至 64（对齐 operation_logs.request_id varchar(64)）。
func TestRequestIDTruncatesOverlongValue(t *testing.T) {
	r := newGin()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, c.GetString(response.RequestIDKey)) })

	long := strings.Repeat("a", 100) // 100 字符合法字符
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(RequestIDHeader, long)
	r.ServeHTTP(rec, req)

	want := long[:64]
	require.Equal(t, want, rec.Header().Get(RequestIDHeader)) // 响应头截断至 64
	require.Equal(t, want, rec.Body.String())                 // 上下文同步截断（贯穿日志链）
	require.Len(t, rec.Body.String(), 64)

	// 恰 64 字符不截断；65 字符截至 64
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(RequestIDHeader, strings.Repeat("b", 64))
	r.ServeHTTP(rec, req)
	require.Len(t, rec.Body.String(), 64)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(RequestIDHeader, strings.Repeat("c", 65))
	r.ServeHTTP(rec, req)
	require.Equal(t, strings.Repeat("c", 64), rec.Body.String())
}

func TestRecoveryConvertsPanicToEnvelope(t *testing.T) {
	SetLoggers(&logger.Loggers{Access: zap.NewNop(), Error: zap.NewNop(), Business: zap.NewNop()})
	defer SetLoggers(nil)

	r := newGin()
	r.Use(RequestID(), Recovery())
	r.GET("/boom", func(c *gin.Context) { panic("boom-secret-detail") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decodeJSON(t, rec.Body.String())
	require.Equal(t, "COMMON_INTERNAL_ERROR", body["code"]) // panic → 统一错误信封
	require.NotContains(t, rec.Body.String(), "boom-secret-detail")
}

func TestCORSDirective(t *testing.T) {
	r := newGin()
	r.Use(CORS([]string{"http://localhost:5173"}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 白名单内：放行并回显 Origin
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(rec, req)
	require.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "Origin", rec.Header().Get("Vary"))

	// 白名单外：无 CORS 头
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	r.ServeHTTP(rec, req)
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	// 预检：204
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAccessLogRedactsSensitiveQuery(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	SetLoggers(&logger.Loggers{Access: zap.New(core), Error: zap.NewNop(), Business: zap.NewNop()})
	defer SetLoggers(nil)

	r := newGin()
	r.Use(RequestID(), AccessLog(nil))
	r.GET("/search", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/search?keyword=box&password=super-secret&access_token=tok-123", nil)
	req.Header.Set("Authorization", "Bearer jwt-value") // Authorization 头永不入日志
	r.ServeHTTP(rec, req)

	entry := findAccessEntry(logs)
	require.NotNil(t, entry, "应产生访问日志")

	// 脱敏断言：非敏感键保留、敏感键替换为 ***（architecture.md §6 日志红线）
	all := entryString(entry)
	require.Contains(t, all, "keyword=box")
	require.Contains(t, all, "password=%2A%2A%2A")
	require.Contains(t, all, "access_token=%2A%2A%2A")
	require.NotContains(t, all, "super-secret")
	require.NotContains(t, all, "tok-123")
	require.NotContains(t, all, "jwt-value")
}

func TestAccessLogLevelsByStatus(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	SetLoggers(&logger.Loggers{Access: zap.New(core), Error: zap.NewNop(), Business: zap.NewNop()})
	defer SetLoggers(nil)

	r := newGin()
	r.Use(RequestID(), AccessLog(&config.Config{}))
	r.GET("/bad", func(c *gin.Context) { c.JSON(http.StatusBadRequest, gin.H{}) })
	r.GET("/err", func(c *gin.Context) { c.JSON(http.StatusInternalServerError, gin.H{}) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/bad", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/err", nil))

	entries := logs.All()
	require.Len(t, entries, 2)
	require.Equal(t, zapcore.WarnLevel, entries[0].Level)  // 4xx → warn
	require.Equal(t, zapcore.ErrorLevel, entries[1].Level) // 5xx → error
}

// S7 回归：访问日志 User-Agent 截断至 512、IP 截断至 64（对齐 login_logs/operation_logs 列宽）。
func TestAccessLogTruncatesUserAgentAndIP(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	SetLoggers(&logger.Loggers{Access: zap.New(core), Error: zap.NewNop(), Business: zap.NewNop()})
	defer SetLoggers(nil)

	r := newGin()
	r.Use(RequestID(), AccessLog(&config.Config{}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	longUA := strings.Repeat("U", 600)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("User-Agent", longUA)
	r.ServeHTTP(rec, req)

	entry := findAccessEntry(logs)
	require.NotNil(t, entry, "应产生访问日志")
	var uaField, ipField string
	for _, f := range entry.Context {
		switch f.Key {
		case "user_agent":
			uaField = f.String
		case "ip":
			ipField = f.String
		}
	}
	require.Len(t, uaField, 512) // 字节截断至 512（user_agent varchar(512)）
	require.True(t, strings.HasPrefix(longUA, uaField))
	require.LessOrEqual(t, len(ipField), 64) // ClientIP 实际上限为 IPv6 的 45 字符，64 为列宽防御

	// truncateStr 字节截断语义（>n 截断、<=n 原样）
	require.Equal(t, strings.Repeat("x", 64), truncateStr(strings.Repeat("x", 100), 64))
	require.Equal(t, "short", truncateStr("short", 64))
}

// —— 测试辅助 ——

func decodeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	return m
}

func findAccessEntry(logs *observer.ObservedLogs) *observer.LoggedEntry {
	for i := range logs.All() {
		e := logs.All()[i]
		if e.Message == "http access" {
			return &e
		}
	}
	return nil
}

func entryString(e *observer.LoggedEntry) string {
	s := e.Message
	for _, f := range e.Context {
		s += "|" + f.Key + "=" + f.String
	}
	return s
}
