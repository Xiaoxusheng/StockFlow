//go:build integration

// 真实 PostgreSQL + Redis 依赖的集成测试：默认 go test 不编译本文件（单元测试零外部依赖约束）。
// 运行：go test -tags integration ./internal/auth/
// 前置环境变量：SF_TEST_PG_HOST / SF_TEST_PG_PORT / SF_TEST_PG_USER / SF_TEST_PG_PASSWORD /
// SF_TEST_PG_NAME / SF_TEST_REDIS_ADDR（未设置即 Skip）。
// 覆盖：迁移 → 空库初始化（BootstrapIfEmpty）→ 真实登录/改密/踢下线/RBAC 读取全链路。
package auth

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/database"
)

func integrationEnv(t *testing.T) (*gorm.DB, *redis.Client) {
	t.Helper()
	if os.Getenv("SF_TEST_PG_NAME") == "" || os.Getenv("SF_TEST_REDIS_ADDR") == "" {
		t.Skip("未设置 SF_TEST_PG_* / SF_TEST_REDIS_ADDR 环境变量，跳过集成测试")
	}
	port, err := strconv.Atoi(envOr("SF_TEST_PG_PORT", "5432"))
	require.NoError(t, err)
	dbCfg := config.DatabaseConfig{
		Host: envOr("SF_TEST_PG_HOST", "127.0.0.1"), Port: port,
		User: envOr("SF_TEST_PG_USER", "postgres"), Password: os.Getenv("SF_TEST_PG_PASSWORD"),
		Name: os.Getenv("SF_TEST_PG_NAME"), SSLMode: "disable",
		MaxOpenConns: 5, MaxIdleConns: 2, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: 10 * time.Minute,
	}
	db, err := database.Connect(dbCfg)
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	// 迁移到最新（auth 域 5 张表）。
	require.NoError(t, database.MigrateUp(dbCfg.DSN(), "db/migrations"))

	rdb := redis.NewClient(&redis.Options{Addr: os.Getenv("SF_TEST_REDIS_ADDR")})
	require.NoError(t, rdb.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = rdb.Close() })
	return db, rdb
}

func TestIntegrationAuthFlow(t *testing.T) {
	db, rdb := integrationEnv(t)
	ctx := context.Background()

	// 空库初始化（幂等：重复执行不报错）。
	adminPw := "SfAdmin2026"
	for i := 0; i < 2; i++ {
		_, err := database.BootstrapIfEmpty(db, adminPw)
		require.NoError(t, err)
	}

	// 服务装配（走真实仓储与会话存储）。
	resetWiredForTest()
	t.Cleanup(resetWiredForTest)
	cfg := testCfg()
	repo := NewRepository(db)
	setWiredForTest(cfg, repo, rdb)
	_, _, _, _, svc, _, ok := snapshotWired()
	require.True(t, ok)

	// admin 登录（must_change_password=true）→ 403 门禁 → 改密。
	res, err := svc.Login(ctx, LoginInput{Username: database.AdminUsername, Password: adminPw, IP: "127.0.0.1", UserAgent: "integration"})
	require.NoError(t, err)
	require.True(t, res.MustChangePassword)

	newPw := "SfAdmin2027!"
	sid := sidOfToken(t, cfg, res.AccessToken)
	sess, err := svc.store.Get(ctx, sid)
	require.NoError(t, err)
	require.NotNil(t, sess)
	actor := testActor(sess.UserID, sess.Username, true)
	require.NoError(t, svc.ChangePassword(ctx, sess, actor, adminPw, newPw))

	// 旧密码拒绝；新密码通过。
	_, err = svc.Login(ctx, LoginInput{Username: database.AdminUsername, Password: adminPw})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	res2, err := svc.Login(ctx, LoginInput{Username: database.AdminUsername, Password: newPw})
	require.NoError(t, err)
	require.False(t, res2.MustChangePassword)

	// 登录保护：连续失败 5 次后锁定（真实 Redis 计数 + users.locked_until 落库）。
	for i := 0; i < cfg.maxLoginFailures; i++ {
		_, err = svc.Login(ctx, LoginInput{Username: database.AdminUsername, Password: "wrong-only-1"})
		require.Error(t, err)
	}
	_, err = svc.Login(ctx, LoginInput{Username: database.AdminUsername, Password: newPw})
	// S8/S4：锁定（且 IP 维度同达阈值）对客户端统一凭证错误文案，不再回显 AUTH_ACCOUNT_LOCKED。
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	require.NoError(t, svc.UnlockUser(ctx, testActor(res2.User.ID.Int64(), "admin", true), res2.User.ID.Int64()))

	// 踢下线：DELETE 会话后 token 立即失效（plan §7.2）。
	sid2 := sidOfToken(t, cfg, res2.AccessToken)
	require.NoError(t, svc.KickSession(ctx, actor, sid2))
	gone, err := svc.store.Get(ctx, sid2)
	require.NoError(t, err)
	require.Nil(t, gone)

	// RBAC 真库读取：admin 持有全部权限点。
	perms, err := svc.PermissionCodes(ctx, res2.User.ID.Int64())
	require.NoError(t, err)
	require.Contains(t, perms, PermUserList)
	require.Contains(t, perms, PermSessionKick)

	// 登录日志落库验证（permission.md §3.3）。
	var n int64
	require.NoError(t, db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM login_logs WHERE username = ? AND success = TRUE`, database.AdminUsername,
	).Scan(&n).Error)
	require.Positive(t, n, "登录成功必须写入 login_logs")
}

func sidOfToken(t *testing.T, cfg runtimeConfig, token string) string {
	t.Helper()
	_, sid, err := parseAccessToken(cfg, token)
	require.NoError(t, err)
	return sid
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fmt.Sprint(fallback)
}
