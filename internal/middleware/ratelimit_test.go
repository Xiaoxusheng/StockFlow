package middleware

// ratelimit.go 测试：内存版 rateLimitStore 替身（ask 约束：单测不依赖 Redis/网络），
// 仅实现限流用到的四条命令语义（ZREMRANGEBYSCORE/ZCARD/ZADD/EXPIRE）。

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/stockflow/server/internal/logger"
)

type fakeZ struct {
	Score  float64
	Member string
}

// fakeZSetStore ZSET 内存替身：按 key 保存成员切片（score 毫秒时间戳语义）。
type fakeZSetStore struct {
	mu     sync.Mutex
	sets   map[string][]fakeZ
	ttls   map[string]time.Duration
	expire map[string]int // Expire 调用计数
	errOn  bool           // 故障注入：全部命令失败（fail-open 路径用）
}

func newFakeZSetStore() *fakeZSetStore {
	return &fakeZSetStore{sets: map[string][]fakeZ{}, ttls: map[string]time.Duration{}, expire: map[string]int{}}
}

func (f *fakeZSetStore) cmdErr() error {
	return fmt.Errorf("fake redis: 故障注入")
}

func (f *fakeZSetStore) ZRemRangeByScore(_ context.Context, key, min, max string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn {
		cmd.SetErr(f.cmdErr())
		return cmd
	}
	minF, errMin := parseScoreBound(min)
	maxF, errMax := parseScoreBound(max)
	if errMin != nil || errMax != nil {
		cmd.SetErr(fmt.Errorf("fake redis: 非法 score 边界 %s/%s", min, max))
		return cmd
	}
	kept := f.sets[key][:0]
	removed := int64(0)
	for _, z := range f.sets[key] {
		if z.Score >= minF && z.Score <= maxF {
			removed++
			continue
		}
		kept = append(kept, z)
	}
	f.sets[key] = kept
	cmd.SetVal(removed)
	return cmd
}

func (f *fakeZSetStore) ZCard(_ context.Context, key string) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn {
		cmd.SetErr(f.cmdErr())
		return cmd
	}
	cmd.SetVal(int64(len(f.sets[key])))
	return cmd
}

func (f *fakeZSetStore) ZAdd(_ context.Context, key string, members ...redis.Z) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn {
		cmd.SetErr(f.cmdErr())
		return cmd
	}
	added := int64(0)
	for _, m := range members {
		s, ok := m.Member.(string)
		if !ok {
			cmd.SetErr(fmt.Errorf("fake redis: member 非字符串 %T", m.Member))
			return cmd
		}
		f.sets[key] = append(f.sets[key], fakeZ{Score: m.Score, Member: s})
		added++
	}
	cmd.SetVal(added)
	return cmd
}

func (f *fakeZSetStore) Expire(_ context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn {
		cmd.SetErr(f.cmdErr())
		return cmd
	}
	if _, ok := f.sets[key]; !ok {
		cmd.SetVal(false)
		return cmd
	}
	f.ttls[key] = expiration
	f.expire[key]++
	cmd.SetVal(true)
	return cmd
}

// parseScoreBound 解析 score 边界（替身只需支持 -inf 与毫秒整数——限流实现仅用这两种）。
func parseScoreBound(b string) (float64, error) {
	if b == "-inf" {
		return math.Inf(-1), nil
	}
	n, err := strconv.ParseFloat(b, 64)
	return n, err
}

// ---- 断言辅助 ----

func (f *fakeZSetStore) members(key string) []fakeZ {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeZ(nil), f.sets[key]...)
}

// runLimitRequest 经 gin 引擎发一次请求（ClientIP 固定为 httptest 默认 192.0.2.1）。
func runLimitRequest(t *testing.T, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := newGin()
	r.GET("/auth/login", h, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	return rec
}

func TestIPRateLimitAllowsWithinLimit(t *testing.T) {
	store := newFakeZSetStore()
	h := ipRateLimit(3, time.Minute, store)

	for i := 0; i < 3; i++ {
		rec := runLimitRequest(t, h)
		require.Equal(t, http.StatusOK, rec.Code, "第 %d 次应放行", i+1)
	}
	require.Len(t, store.members("sf:authrl:192.0.2.1"), 3)
}

func TestIPRateLimitRejectsOverLimit(t *testing.T) {
	store := newFakeZSetStore()
	h := ipRateLimit(2, time.Minute, store)

	require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code)
	require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code)
	rec := runLimitRequest(t, h) // 第 3 次 → 429
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "COMMON_RATE_LIMITED")
	require.Contains(t, rec.Body.String(), `"limit":2`)
	// 被拒请求不占位
	require.Len(t, store.members("sf:authrl:192.0.2.1"), 2)
}

func TestIPRateLimitIndependentPerIP(t *testing.T) {
	store := newFakeZSetStore()
	h := ipRateLimit(1, time.Minute, store)
	r := newGin()
	r.GET("/auth/login", h, func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	do := func(ip string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = ip + ":1234"
		r.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusOK, do("198.51.100.7").Code)
	require.Equal(t, http.StatusTooManyRequests, do("198.51.100.7").Code)
	require.Equal(t, http.StatusOK, do("203.0.113.9").Code) // 另一 IP 不受影响
}

// 滑动窗口：窗外旧值被 ZREMRANGEBYSCORE 清出，不占用当前窗口配额。
func TestIPRateLimitSlidingWindowPrunesOldMembers(t *testing.T) {
	store := newFakeZSetStore()
	key := "sf:authrl:192.0.2.1"
	old := time.Now().Add(-2 * time.Minute)
	_, err := store.ZAdd(context.Background(), key,
		redis.Z{Score: float64(old.UnixMilli()), Member: "stale-member"}).Result()
	require.NoError(t, err)

	h := ipRateLimit(1, time.Minute, store)
	require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code, "窗外旧值清理后应放行")

	members := store.members(key)
	require.Len(t, members, 1)
	require.NotEqual(t, "stale-member", members[0].Member, "窗外旧值应被清出，仅剩本次新占位")
}

func TestIPRateLimitSetsTTL(t *testing.T) {
	store := newFakeZSetStore()
	h := ipRateLimit(30, time.Minute, store)
	require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code)

	require.Equal(t, 1, store.expire["sf:authrl:192.0.2.1"])
	ttl := store.ttls["sf:authrl:192.0.2.1"]
	require.True(t, ttl >= time.Minute && ttl <= time.Minute+time.Minute, "TTL 应为窗口+1 分钟兜底，实际 %v", ttl)
}

// Redis 故障 fail-open：请求放行且 error 日志留痕（禁止静默）。
func TestIPRateLimitFailsOpenOnRedisError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	SetLoggers(&logger.Loggers{Access: zap.NewNop(), Error: zap.New(core), Business: zap.NewNop()})
	defer SetLoggers(nil)

	store := newFakeZSetStore()
	store.errOn = true
	h := ipRateLimit(1, time.Minute, store)

	for i := 0; i < 3; i++ { // 超过 limit 也放行：故障期间不限流
		require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code)
	}

	entries := logs.All()
	require.NotEmpty(t, entries, "应记录 error 日志")
	for _, e := range entries {
		require.Contains(t, e.Message, "fail-open")
		require.Equal(t, zapcore.ErrorLevel, e.Level)
	}
}

// rdb 为 nil：限流不启用（无存储可计数），恒放行。
func TestAuthIPRateLimitNilRedisIsNoop(t *testing.T) {
	h := AuthIPRateLimit(1, nil)
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, runLimitRequest(t, h).Code)
	}
}

// *redis.Client 满足 rateLimitStore（接口与 go-redis v9 签名一致的编译期锚点）。
var _ rateLimitStore = (*redis.Client)(nil)
