package auth

// 测试公共支撑：装配替身、构造 gin、断言错误码。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/response"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// testCfg 固定密钥与时长（不读环境变量，避免用例间串扰）。
func testCfg() runtimeConfig {
	return runtimeConfig{
		jwtSecret:        []byte("unit-test-secret-0123456789abcdef"),
		issuer:           jwtIssuer,
		accessTTL:        2 * time.Hour,
		refreshTTL:       7 * 24 * time.Hour,
		maxLoginFailures: 5,
		lockDuration:     15 * time.Minute,
		permsCacheTTL:    10 * time.Minute,
	}
}

// wireTest 以替身装配包级依赖；用例结束自动复位。
func wireTest(t *testing.T, rdb redisStore, repo Repository) *Service {
	t.Helper()
	resetWiredForTest()
	t.Cleanup(resetWiredForTest)
	setWiredForTest(testCfg(), repo, rdb)
	_, _, _, _, svc, _, ok := snapshotWired()
	if !ok || svc == nil {
		t.Fatal("装配失败")
	}
	return svc
}

// codeOf 取业务错误码字符串（*response.Error 的 Error() 形如 "CODE: message"，取前缀）。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *response.Error
	if !errors.As(err, &e) {
		t.Fatalf("期望业务错误，得到 %T: %v", err, err)
	}
	full := e.Error()
	idx := strings.Index(full, ": ")
	require.Positive(t, idx, "错误码格式异常: %q", full)
	return full[:idx]
}

// testActor 操作者。
func testActor(uid int64, username string, isSuper bool) Actor {
	return Actor{UserID: uid, Username: username, IsSuper: isSuper,
		RequestID: "req-test", IP: "127.0.0.1", UserAgent: "unit", Method: "PUT", Path: "/test"}
}

// newTestGin 空引擎。
func newTestGin() *gin.Engine {
	return gin.New()
}
