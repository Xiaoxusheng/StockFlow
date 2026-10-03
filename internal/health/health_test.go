package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

func TestLiveness(t *testing.T) {
	c, rec := newCtx(t)
	Liveness()(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, float64(0), body["code"])
	data := body["data"].(map[string]any)
	require.Equal(t, "UP", data["status"])
}

func TestReadinessWithoutDBIs503(t *testing.T) {
	// nil db → 判 DOWN（装配错误不静默）；nil rdb → DISABLED 不阻塞
	c, rec := newCtx(t)
	Readiness(nil, nil)(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "COMMON_SERVICE_UNAVAILABLE", body["code"])
	details := body["details"].(map[string]any)
	require.Equal(t, "DOWN", details["database"])
	require.Equal(t, "DISABLED", details["redis"])
}

// countingProbe 计数替身：可编程返回结果，记录探测次数。
type countingProbe struct {
	mu    sync.Mutex
	calls int
	h     gin.H
	ready bool
}

func (p *countingProbe) probe(context.Context) (gin.H, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.h, p.ready
}

func (p *countingProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// S13：TTL 内复用缓存——第二个请求不再触发探测，状态细节原样保留。
func TestReadinessCacheHitsWithinTTL(t *testing.T) {
	p := &countingProbe{h: gin.H{"database": "UP", "redis": "DISABLED"}, ready: true}
	h := cachedReadiness(p.probe)

	c1, rec1 := newCtx(t)
	h(c1)
	time.Sleep(100 * time.Millisecond) // TTL 内
	c2, rec2 := newCtx(t)
	h(c2)

	require.Equal(t, 1, p.count(), "TTL 内第二次请求不应重新探测")
	require.Equal(t, http.StatusOK, rec1.Code)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Contains(t, rec2.Body.String(), `"redis":"DISABLED"`) // 组件状态细节保留
}

// S13：TTL 过期后重新探测（时钟注入驱动，不真睡）。
func TestReadinessCacheExpiresAndReprobes(t *testing.T) {
	p := &countingProbe{h: gin.H{"database": "DOWN"}, ready: false}
	current := time.Now()
	h := cachedReadinessWithClock(p.probe, func() time.Time { return current })

	c1, rec1 := newCtx(t)
	h(c1)
	require.Equal(t, 1, p.count())
	require.Equal(t, http.StatusServiceUnavailable, rec1.Code) // DOWN 也缓存（含 503 细节）

	current = current.Add(1100 * time.Millisecond) // 越过 1 秒 TTL
	c2, rec2 := newCtx(t)
	h(c2)

	require.Equal(t, 2, p.count(), "TTL 过期后应重新探测")
	require.Equal(t, http.StatusServiceUnavailable, rec2.Code)
	require.Contains(t, rec2.Body.String(), `"database":"DOWN"`)
}
