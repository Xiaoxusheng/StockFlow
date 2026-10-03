//go:build integration

// 真实 PostgreSQL 依赖的集成测试：默认 go test 不编译本文件（单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/database/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME（未设置即 Skip）。
// 迁移的双向验证（migrate up/down）属 DB 工程师（backend-m1-plan §6.4/§10 T1），
// 此处仅验证 Connect 的连接与连通性检查。
package database

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/config"
)

func TestIntegrationConnect(t *testing.T) {
	if os.Getenv("SF_TEST_PG_NAME") == "" {
		t.Skip("未设置 SF_TEST_PG_* 环境变量，跳过 PostgreSQL 集成测试")
	}
	port, err := strconv.Atoi(envOr("SF_TEST_PG_PORT", "5432"))
	require.NoError(t, err)

	cfg := config.DatabaseConfig{
		Host:            envOr("SF_TEST_PG_HOST", "127.0.0.1"),
		Port:            port,
		User:            envOr("SF_TEST_PG_USER", "postgres"),
		Password:        os.Getenv("SF_TEST_PG_PASSWORD"),
		Name:            os.Getenv("SF_TEST_PG_NAME"),
		SSLMode:         "disable",
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 10 * time.Minute,
	}

	db, err := Connect(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping())
	require.NoError(t, sqlDB.Close())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
