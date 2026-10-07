package auth

// 单元测试：会话存储与登录失败计数（ask 指定交付项：登录保护计数，接口替身）。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionCreateGetDelete(t *testing.T) {
	rdb := newFakeRedis()
	store := newSessionStore(rdb, testCfg())
	ctx := context.Background()

	sess := &sessionData{UserID: 7, Username: "alice", DataScope: DataScopeAll, LoginAt: time.Now()}
	require.NoError(t, store.createWithTokens(ctx, sess))
	require.NotEmpty(t, sess.SID)
	require.NotEmpty(t, sess.RefreshToken)
	require.True(t, rdb.has(sessionKey(sess.SID)))
	require.Equal(t, sess.SID, rdb.val(refreshKey(sess.RefreshToken)))

	got, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, int64(7), got.UserID)
	require.Equal(t, "alice", got.Username)

	require.NoError(t, store.Delete(ctx, sess))
	require.False(t, rdb.has(sessionKey(sess.SID)))
	require.False(t, rdb.has(refreshKey(sess.RefreshToken)))

	gone, err := store.Get(ctx, sess.SID)
	require.NoError(t, err)
	require.Nil(t, gone) // 被踢/过期 → (nil, nil) → 上层 401 SESSION_INVALID
}

func TestSessionRotateInvalidatesOld(t *testing.T) {
	rdb := newFakeRedis()
	store := newSessionStore(rdb, testCfg())
	ctx := context.Background()

	old := &sessionData{UserID: 7, Username: "alice", LoginAt: time.Now()}
	require.NoError(t, store.createWithTokens(ctx, old))

	fresh, err := store.Rotate(ctx, old)
	require.NoError(t, err)
	require.NotEqual(t, old.SID, fresh.SID)
	require.NotEqual(t, old.RefreshToken, fresh.RefreshToken)

	require.False(t, rdb.has(sessionKey(old.SID)), "旧会话必须作废（plan §7.2 轮换）")
	require.False(t, rdb.has(refreshKey(old.RefreshToken)))
	require.True(t, rdb.has(sessionKey(fresh.SID)))

	byToken, err := store.FindByRefreshToken(ctx, fresh.RefreshToken)
	require.NoError(t, err)
	require.NotNil(t, byToken)
	require.Equal(t, fresh.SID, byToken.SID)

	byOld, err := store.FindByRefreshToken(ctx, old.RefreshToken)
	require.NoError(t, err)
	require.Nil(t, byOld)
}

func TestSessionList(t *testing.T) {
	rdb := newFakeRedis()
	store := newSessionStore(rdb, testCfg())
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s := &sessionData{UserID: int64(i + 1), Username: "u", LoginAt: time.Now()}
		require.NoError(t, store.createWithTokens(ctx, s))
	}
	list, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)
}

func TestSessionTouchSlidesTTL(t *testing.T) {
	rdb := newFakeRedis()
	store := newSessionStore(rdb, testCfg())
	ctx := context.Background()
	s := &sessionData{UserID: 1, LoginAt: time.Now()}
	require.NoError(t, store.createWithTokens(ctx, s))

	require.Zero(t, rdb.expired[sessionKey(s.SID)])
	store.Touch(ctx, s)
	require.Equal(t, 1, rdb.expired[sessionKey(s.SID)])
	require.Equal(t, 1, rdb.expired[refreshKey(s.RefreshToken)])
}

func TestLoginGuardCounting(t *testing.T) {
	rdb := newFakeRedis()
	cfg := testCfg()
	cfg.maxLoginFailures = 3
	cfg.lockDuration = 7 * time.Minute
	guard := &loginGuard{rdb: rdb, maxFailures: cfg.maxLoginFailures, window: cfg.lockDuration}
	ctx := context.Background()

	// 连续失败累计（permission.md §3.2）：1 → 2 → 3。
	for i := int64(1); i <= 3; i++ {
		n, err := guard.Fail(ctx, "alice")
		require.NoError(t, err)
		require.Equal(t, i, n)
	}
	require.Equal(t, int64(3), rdb.intVal(failKey("alice")))

	// 窗口 TTL 在首个失败时设置一次（plan §7.3：TTL=锁定窗口）。
	require.Equal(t, 1, rdb.expired[failKey("alice")])
	require.Equal(t, cfg.lockDuration, rdb.ttl[failKey("alice")])

	// 计数查询与重置（登录成功/解锁路径）。
	n, err := guard.Count(ctx, "alice")
	require.NoError(t, err)
	require.Equal(t, int64(3), n)

	require.NoError(t, guard.Reset(ctx, "alice"))
	n, err = guard.Count(ctx, "alice")
	require.NoError(t, err)
	require.Zero(t, n)

	// 独立用户互不影响（按用户名分键 sf:loginfail:{username}）。
	_, err = guard.Fail(ctx, "bob")
	require.NoError(t, err)
	require.Equal(t, int64(1), rdb.intVal(failKey("bob")))
	require.Equal(t, int64(0), rdb.intVal(failKey("alice")))
}

func TestLoginGuardFailAtomicWindowTTL(t *testing.T) {
	rdb := newFakeRedis()
	guard := &loginGuard{rdb: rdb, maxFailures: 5, window: 15 * time.Minute}
	ctx := context.Background()

	// INCR 与窗口 TTL 经 Lua 原子执行（安全渗透修复）：不存在"INCR 成功、EXPIRE 单独
	// 失败"的中间态——首笔失败即带窗口 TTL，不再可能产生无 TTL 的永久计数键。
	n, err := guard.Fail(ctx, "carol")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.Equal(t, guard.window, rdb.ttl[failKey("carol")])

	// 等价验证（Expire 不再可能失败留下无 TTL 键）：旧版本故障产物——有计数、无 TTL
	// 的孤儿键，经 Eval 脚本的 TTL==-1 修复分支在继续累计的同时补设窗口 TTL。
	rdb.mu.Lock()
	rdb.str[failKey("dave")] = "4" // 模拟遗留孤儿键（绕过 Set/Expire，不落 ttl 表）
	rdb.mu.Unlock()
	n, err = guard.Fail(ctx, "dave")
	require.NoError(t, err)
	require.Equal(t, int64(5), n)
	require.Equal(t, guard.window, rdb.ttl[failKey("dave")], "孤儿键必须被补设窗口 TTL")

	// 窗口 TTL 只在首笔/修复时设置一次，后续累计不重置（窗口语义不变）。
	_, err = guard.Fail(ctx, "carol")
	require.NoError(t, err)
	require.Equal(t, 1, rdb.expired[failKey("carol")])
	require.Equal(t, guard.window, rdb.ttl[failKey("carol")])
}

func TestPermsCacheRoundtrip(t *testing.T) {
	rdb := newFakeRedis()
	store := newSessionStore(rdb, testCfg())
	ctx := context.Background()

	codes, hit, err := store.GetPerms(ctx, 9)
	require.NoError(t, err)
	require.False(t, hit)
	require.Nil(t, codes)

	store.SetPerms(ctx, 9, []string{"auth:user:list", "auth:role:list"}, time.Minute)
	codes, hit, err = store.GetPerms(ctx, 9)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, []string{"auth:user:list", "auth:role:list"}, codes)

	// 定向失效（RBAC 变更路径）。
	require.NoError(t, store.DelPerms(ctx, 9))
	_, hit, err = store.GetPerms(ctx, 9)
	require.NoError(t, err)
	require.False(t, hit)
}
