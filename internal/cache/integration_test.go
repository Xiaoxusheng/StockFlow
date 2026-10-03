//go:build integration

// 真实 Redis 依赖的集成测试：默认 go test 不编译本文件。
// 运行：go test -tags integration ./internal/cache/
// 前置环境变量：SF_TEST_REDIS_ADDR（未设置即 Skip）。
package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/config"
)

func TestIntegrationRedisRoundTrip(t *testing.T) {
	addr := os.Getenv("SF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 SF_TEST_REDIS_ADDR，跳过 Redis 集成测试")
	}

	cli, err := New(config.RedisConfig{Enabled: true, Addr: addr, PoolSize: 5, MinIdleConns: 1})
	require.NoError(t, err)
	require.NotNil(t, cli)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := Key("integration_test", "round-trip")
	err = SetJSON(ctx, cli, key, map[string]any{"n": 1}, time.Minute)
	require.NoError(t, err)

	var dest map[string]any
	found, err := GetJSON(ctx, cli, key, &dest)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, float64(1), dest["n"])

	require.NoError(t, cli.Del(ctx, key).Err())
}
