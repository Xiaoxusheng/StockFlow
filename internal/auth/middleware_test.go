package auth

// 单元测试：认证/授权中间件与权限判定、数据权限范围（ask 指定交付项：权限判定）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// loginToken 经完整登录拿到 Access Token（走 Service 真实路径）。
func loginToken(t *testing.T, svc *Service, username string) (token string, sid string) {
	t.Helper()
	res, err := svc.Login(context.Background(), LoginInput{Username: username, Password: "Passw0rd", IP: "1.2.3.4", UserAgent: "ua"})
	require.NoError(t, err)
	// 包级 cfg 密钥与 testCfg 一致（setWiredForTest 注入），复用装配态 cfg 解析出 sid。
	cfg, _, _, _, _, _, _ := snapshotWired()
	_, sidOut, err := parseAccessToken(cfg, res.AccessToken)
	require.NoError(t, err)
	return res.AccessToken, sidOut
}

func doReq(r *gin.Engine, method, path, authorization string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	r.ServeHTTP(rec, req)
	return rec
}

// mustJSON 解析响应信封。
func mustJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	return m
}

// ---- AuthRequired ----

func TestAuthRequiredHappyPath(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	repo.seedUser("alice", "Passw0rd", nil)
	svc := wireTest(t, rdb, repo)

	token, _ := loginToken(t, svc, "alice")

	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/api/me", func(c *gin.Context) {
		uc, ok := CurrentUser(c)
		require.True(t, ok)
		require.Equal(t, "alice", uc.Username)
		require.EqualValues(t, 1, uc.UserID)
		c.String(http.StatusOK, "ok")
	})

	rec := doReq(r, http.MethodGet, "/api/me", "Bearer "+token)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthRequiredRejects(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	repo.seedUser("alice", "Passw0rd", nil)
	svc := wireTest(t, rdb, repo)

	token, sid := loginToken(t, svc, "alice")
	_, store, _, _, _, _, _ := snapshotWired()

	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 无 Authorization 头 → 401 AUTH_TOKEN_INVALID
	rec := doReq(r, http.MethodGet, "/x", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_TOKEN_INVALID", mustJSON(t, rec.Body.String())["code"])

	// 非 Bearer scheme → 401
	rec = doReq(r, http.MethodGet, "/x", "Basic dXNlcjpwYXNz")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 垃圾 token → 401
	rec = doReq(r, http.MethodGet, "/x", "Bearer garbage")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_TOKEN_INVALID", mustJSON(t, rec.Body.String())["code"])

	// 过期 token → 401 AUTH_TOKEN_EXPIRED（permission.md §5：前端据此刷新）
	expCfg := testCfg()
	expCfg.accessTTL = -time.Minute
	expToken, _, err := issueAccessToken(expCfg, 1, "sid-exp")
	require.NoError(t, err)
	rec = doReq(r, http.MethodGet, "/x", "Bearer "+expToken)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_TOKEN_EXPIRED", mustJSON(t, rec.Body.String())["code"])

	// 合法 token 但会话被踢 → 401 AUTH_SESSION_INVALID（plan §7.2 强制下线即刻生效）
	sess, err := store.Get(context.Background(), sid)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NoError(t, store.Delete(context.Background(), sess))
	rec = doReq(r, http.MethodGet, "/x", "Bearer "+token)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_SESSION_INVALID", mustJSON(t, rec.Body.String())["code"])
}

func TestAuthRequiredUIDMismatchRejected(t *testing.T) {
	// 合法签名但 uid 与会话不一致（凭证拼装攻击）→ 拒绝。
	rdb := newFakeRedis()
	repo := newFakeRepo()
	repo.seedUser("alice", "Passw0rd", nil)
	svc := wireTest(t, rdb, repo)

	// 会话属于 uid 2；token 声称 uid 1。
	sess := &sessionData{UserID: 2, Username: "mallory", LoginAt: time.Now()}
	require.NoError(t, svc.store.createWithTokens(context.Background(), sess))
	cfg, _, _, _, _, _, _ := snapshotWired()
	token, _, err := issueAccessToken(cfg, 1, sess.SID)
	require.NoError(t, err)

	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	rec := doReq(r, http.MethodGet, "/x", "Bearer "+token)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_SESSION_INVALID", mustJSON(t, rec.Body.String())["code"])
}

func TestAuthRequiredPasswordChangeGate(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	repo.seedUser("fresh", "Passw0rd", func(u *User) { u.MustChangePassword = true })
	svc := wireTest(t, rdb, repo)

	res, err := svc.Login(context.Background(), LoginInput{Username: "fresh", Password: "Passw0rd"})
	require.NoError(t, err)
	require.True(t, res.MustChangePassword) // 默认管理员首登强制改密（database.md §8.1）

	r := newTestGin()
	r.Use(AuthRequired())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/api/inventory", ok)
	r.GET("/api/auth/me", ok)
	r.POST("/api/auth/logout", ok)
	r.PUT("/api/auth/password", ok)

	// 未改密访问业务接口 → 403 AUTH_PASSWORD_CHANGE_REQUIRED（plan §7.3）
	rec := doReq(r, http.MethodGet, "/api/inventory", "Bearer "+res.AccessToken)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "AUTH_PASSWORD_CHANGE_REQUIRED", mustJSON(t, rec.Body.String())["code"])

	// 白名单：me / logout / password（交付披露：放宽自计划原文的仅 password）
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodPut, "/api/auth/password"},
	} {
		rec = doReq(r, tc.method, tc.path, "Bearer "+res.AccessToken)
		require.Equal(t, http.StatusOK, rec.Code, tc.path)
	}
}

func TestAuthRequiredFailsClosedOnRedisError(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	repo.seedUser("alice", "Passw0rd", nil)
	svc := wireTest(t, rdb, repo)
	token, _ := loginToken(t, svc, "alice")

	rdb.errGet = true // Redis 故障注入
	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	rec := doReq(r, http.MethodGet, "/x", "Bearer "+token)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code) // fail-closed：不放行
}

// ---- RequirePermission ----

// permFixture 权限判定用例的公共环境。
type permFixture struct {
	engine   *gin.Engine
	rdb      *fakeRedis
	repo     *fakeRepo
	svc      *Service
	operator string // 绑定了 inventory:list 的普通用户
	opUID    int64
}

func newPermFixture(t *testing.T) *permFixture {
	t.Helper()
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)

	role := repo.seedRole("operator", StatusEnabled, false)
	perm := repo.seedPerm(PermInventoryList, StatusEnabled)
	repo.bindPerm(role, perm)
	opUID := repo.seedUser("op", "Passw0rd", nil)
	repo.bindRole(opUID, role)

	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/protected", RequirePermission(PermInventoryList), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/forbidden", RequirePermission(PermUserList), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return &permFixture{engine: r, rdb: rdb, repo: repo, svc: svc, operator: "op", opUID: opUID}
}

func TestRequirePermissionAllowedAndDenied(t *testing.T) {
	f := newPermFixture(t)
	token, _ := loginToken(t, f.svc, f.operator)

	// 持有 inventory:inventory:list → 通过。
	rec := doReq(f.engine, http.MethodGet, "/protected", "Bearer "+token)
	require.Equal(t, http.StatusOK, rec.Code)

	// 未持有 auth:user:list → 403 + details 携带权限点（api.md §4）。
	rec = doReq(f.engine, http.MethodGet, "/forbidden", "Bearer "+token)
	require.Equal(t, http.StatusForbidden, rec.Code)
	body := mustJSON(t, rec.Body.String())
	require.Equal(t, "COMMON_PERMISSION_DENIED", body["code"])
	details := body["details"].(map[string]any)
	require.Equal(t, PermUserList, details["permission"])
}

func TestRequirePermissionCacheFallback(t *testing.T) {
	f := newPermFixture(t)
	token, _ := loginToken(t, f.svc, f.operator)

	// 清缓存 → 回源 DB（fakeRepo）→ 回填缓存 → 仍通过。
	require.NoError(t, f.svc.store.DelPerms(context.Background(), f.opUID))
	rec := doReq(f.engine, http.MethodGet, "/protected", "Bearer "+token)
	require.Equal(t, http.StatusOK, rec.Code)
	codes, hit, err := f.svc.store.GetPerms(context.Background(), f.opUID)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, []string{PermInventoryList}, codes)
}

func TestRequirePermissionSuperBypass(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)
	repo.seedUser("root", "Passw0rd", nil)
	repo.bindRole(1, repo.seedRole(SuperAdminRoleCode, StatusEnabled, true))

	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/forbidden", RequirePermission(PermUserList), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	token, _ := loginToken(t, svc, "root")
	rec := doReq(r, http.MethodGet, "/forbidden", "Bearer "+token)
	require.Equal(t, http.StatusOK, rec.Code) // 超管直通（permission.md §1）
}

func TestRequirePermissionFailsClosed(t *testing.T) {
	f := newPermFixture(t)
	token, _ := loginToken(t, f.svc, f.operator)

	// 缓存与回源均不可用 → fail-closed 503（go-dev-standard：安全功能不能"没有缓存=有权限"）。
	f.rdb.errGet = true
	f.repo.errPermCodes = true
	rec := doReq(f.engine, http.MethodGet, "/protected", "Bearer "+token)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "COMMON_SERVICE_UNAVAILABLE", mustJSON(t, rec.Body.String())["code"])
}

func TestHasPermission(t *testing.T) {
	require.True(t, hasPermission([]string{"a", "b"}, "a"))
	require.False(t, hasPermission([]string{"a", "b"}, "c"))
	require.False(t, hasPermission(nil, "a"))
}

// ---- 数据权限范围 ----

func TestScopeOf(t *testing.T) {
	a, ids := scopeOf(UserContext{IsSuper: true, DataScope: DataScopeSpecifiedWh})
	require.True(t, a)
	require.Nil(t, ids)

	a, _ = scopeOf(UserContext{DataScope: DataScopeAll})
	require.True(t, a)

	// SPECIFIED_WAREHOUSE：绑定仓库集可见，未绑定仓库不可见。
	a, ids = scopeOf(UserContext{DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{1, 3}})
	require.False(t, a)
	require.Equal(t, []int64{1, 3}, ids)

	// M1 未落行级的范围：不可见任何仓库（不静默放大，plan §7.4）。
	for _, scope := range []string{DataScopeDepartment, DataScopeSelf, DataScopeSelfInCharge} {
		a, ids = scopeOf(UserContext{DataScope: scope})
		require.False(t, a, scope)
		require.Nil(t, ids, scope)
	}
}

func TestBearerTokenParse(t *testing.T) {
	require.Equal(t, "tok", bearerToken("Bearer tok"))
	require.Equal(t, "tok", bearerToken("bearer tok"))
	require.Equal(t, "tok", bearerToken("  Bearer   tok  "))
	require.Equal(t, "", bearerToken(""))
	require.Equal(t, "", bearerToken("tok"))
	require.Equal(t, "", bearerToken("Bearer"))
	require.Equal(t, "", bearerToken("Basic abc"))
}
