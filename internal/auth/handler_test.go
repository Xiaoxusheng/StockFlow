package auth

// handler 层数据驱动表测试（ask 指定交付项：27 条 HTTP 接口的参数绑定/统一信封/
// 权限中间件组合路径）。Service 层数据语义已有 service_test.go / service_security_test.go
// 密集覆盖，本文件不重复造 Service 用例，聚焦 handler 组合路径：
//   - 真实路由注册形态：RegisterPublicRoutes/RegisterProtectedRoutes + AuthRequired
//     （auth.go:41-106），权限点经 RequirePermission 中间件校验（middleware.go:113）；
//   - 统一信封 {code,message,data,request_id}（response.go:14-33，成功 code=0 数字）；
//   - 路径/查询参数 fail-fast（handler.go:49-75）与 binding 校验（如 handler.go:101-103）。
// 全部经 fakeRedis/fakeRepo/openTestGorm 替身，不依赖 PostgreSQL/Redis。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
)

// ---- fixture 与请求助手 ----

// handlerFixture 完整 HTTP 组合路径环境：公开+受保护路由按真实形态注册，
// 依赖全部为替身（wireTest + fakeWhChecker，service_test.go:341）。
type handlerFixture struct {
	r           *gin.Engine
	svc         *Service
	repo        *fakeRepo
	rdb         *fakeRedis
	superRoleID int64
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	f := &handlerFixture{svc: nil}
	rdb := newFakeRedis()
	repo := newFakeRepo()
	f.svc = wireTest(t, rdb, repo)
	f.repo, f.rdb = repo, rdb
	f.superRoleID = repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)

	r := newTestGin()
	// 真实挂载形态（router.go:91-100）：公开组挂 /api/auth（RegisterPublicRoutes 内注册
	// /login、/refresh），受保护组挂 /api（RegisterProtectedRoutes 内注册 /auth/*、/users…）。
	RegisterPublicRoutes(r.Group("/api/auth"), nil, nil)
	RegisterProtectedRoutes(r.Group("/api", AuthRequired()), nil, nil,
		WithWarehouseChecker(fakeWhChecker{ok: map[int64]bool{7: true}}))
	f.r = r
	return f
}

// loginSuper 注册并登录一个超级管理员操作者（IsSuper 经会话快照直通 RequirePermission，
// middleware.go:123）。返回 Bearer token 与 uid。
func (f *handlerFixture) loginSuper(t *testing.T, username string) (string, int64) {
	t.Helper()
	uid := f.repo.seedUser(username, testPassword, nil)
	f.repo.bindRole(uid, f.superRoleID)
	tok, _ := loginToken(t, f.svc, username)
	return tok, uid
}

// loginStaff 注册并登录一个只带空角色的普通操作者（无任何 auth:* 权限点 → 403 路径）。
func (f *handlerFixture) loginStaff(t *testing.T, username string) (string, int64) {
	t.Helper()
	uid := f.repo.seedUser(username, testPassword, nil)
	f.repo.bindRole(uid, f.repo.seedRole("role-"+username, StatusEnabled, false))
	tok, _ := loginToken(t, f.svc, username)
	return tok, uid
}

// loginWithPerm 注册并登录一个持有指定权限点的普通操作者（非超管）。
func (f *handlerFixture) loginWithPerm(t *testing.T, username string, perm string) (string, int64) {
	t.Helper()
	uid := f.repo.seedUser(username, testPassword, nil)
	role := f.repo.seedRole("role-"+username, StatusEnabled, false)
	f.repo.bindPerm(role, f.repo.seedPerm(perm, StatusEnabled))
	f.repo.bindRole(uid, role)
	tok, _ := loginToken(t, f.svc, username)
	return tok, uid
}

// doJSON 发送 JSON 请求（body 为空时不设 Content-Type；token 为空不带认证头）。
func doJSON(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// envCode 信封 code 归一化：成功为数字 0（response.go:31-33），失败为字符串错误码。
func envCode(t *testing.T, body string) string {
	t.Helper()
	m := mustJSON(t, body)
	switch v := m["code"].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		t.Fatalf("信封 code 类型异常: %T (%s)", m["code"], body)
		return ""
	}
}

// envData 取信封 data 节点（对象形态）。
func envData(t *testing.T, body string) map[string]any {
	t.Helper()
	m := mustJSON(t, body)
	d, ok := m["data"].(map[string]any)
	require.True(t, ok, "信封缺 data 节点: %s", body)
	return d
}

// envItems 取分页信封 items 数组（OKPage 出参 {page,pageSize,total,items}，response.go:36-41）。
func envItems(t *testing.T, body string) []any {
	t.Helper()
	d := envData(t, body)
	require.IsType(t, []any{}, d["items"], "items 必须是数组而非 null: %s", body)
	return d["items"].([]any)
}

// ---- POST /api/auth/login（handleLogin；captcha.go 人机闸在凭据校验之前）----

// captchaFields 签发一条验证码并返回请求体片段；answer 传 "" 时取真答案（fakeRedis 直读），
// 传其他值模拟错误答案。验证码一次性消费，每条用例必须独立签发。
func (f *handlerFixture) captchaFields(t *testing.T, answer string) string {
	t.Helper()
	ch, err := f.svc.IssueLoginCaptcha(context.Background())
	require.NoError(t, err)
	code := answer
	if code == "" {
		code = f.rdb.val(captchaKey(ch.CaptchaID))
	}
	return fmt.Sprintf(`"captcha_id":%q,"captcha_code":%q`, ch.CaptchaID, code)
}

func TestHandlerLogin(t *testing.T) {
	f := newHandlerFixture(t)
	uid := f.repo.seedUser("alice", testPassword, nil)

	okCaptcha := f.captchaFields(t, "")
	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
		check    func(t *testing.T, body string)
	}{
		{
			name: "缺字段_绑定失败", body: `{}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM",
		},
		{
			name: "非JSON体_绑定失败", body: `not-json`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM",
		},
		{
			name: "缺验证码字段_绑定失败", body: `{"username":"alice","password":"Passw0rd"}`,
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM",
		},
		{
			name: "验证码答案错误", body: fmt.Sprintf(`{"username":"alice","password":"Passw0rd",%s}`, f.captchaFields(t, "XXXX")),
			wantHTTP: 400, wantCode: "AUTH_CAPTCHA_INVALID",
		},
		{
			name: "成功", body: fmt.Sprintf(`{"username":"alice","password":"Passw0rd",%s}`, okCaptcha),
			wantHTTP: 200, wantCode: "0",
			check: func(t *testing.T, body string) {
				d := envData(t, body)
				require.NotEmpty(t, d["access_token"])
				require.Equal(t, "Bearer", d["token_type"])
				require.Positive(t, d["expires_in"].(float64))
				require.NotEmpty(t, d["refresh_token"])
				user := d["user"].(map[string]any)
				require.Equal(t, "alice", user["username"])
				require.Equal(t, strconv.FormatInt(uid, 10), user["id"]) // ID 字符串形态（database/model.go:22）
			},
		},
		{
			name: "密码错误_验证码已过闸", body: fmt.Sprintf(`{"username":"alice","password":"wrongPass1",%s}`, f.captchaFields(t, "")),
			wantHTTP: 401, wantCode: "AUTH_CREDENTIALS_INVALID",
		},
		{
			name: "未知用户_防枚举统一文案", body: fmt.Sprintf(`{"username":"ghost","password":"whatever1",%s}`, f.captchaFields(t, "")),
			wantHTTP: 401, wantCode: "AUTH_CREDENTIALS_INVALID",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPost, "/api/auth/login", "", tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
			if tc.check != nil {
				tc.check(t, rec.Body.String())
			}
		})
	}
}

// TestHandlerCaptcha GET /api/auth/captcha 签发形态（captcha.go CaptchaChallenge 契约）。
func TestHandlerCaptcha(t *testing.T) {
	f := newHandlerFixture(t)
	rec := doJSON(f.r, http.MethodGet, "/api/auth/captcha", "", "")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.NotEmpty(t, d["captcha_id"])
	require.True(t, strings.HasPrefix(d["image"].(string), "data:image/svg+xml;base64,"))
	require.Positive(t, d["expires_in"].(float64))
}

// httpLogin 经公开路由完成带验证码的登录（HTTP 全路径），返回成功信封 data。
func (f *handlerFixture) httpLogin(t *testing.T, username string) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q,%s}`, username, testPassword, f.captchaFields(t, ""))
	rec := doJSON(f.r, http.MethodPost, "/api/auth/login", "", body)
	require.Equal(t, http.StatusOK, rec.Code, "HTTP 登录应成功: %s", rec.Body.String())
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	return envData(t, rec.Body.String())
}

// ---- POST /api/auth/refresh（handleRefresh）----

func TestHandlerRefresh(t *testing.T) {
	f := newHandlerFixture(t)
	f.repo.seedUser("alice", testPassword, nil)

	old := f.httpLogin(t, "alice")

	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
	}{
		{name: "缺refresh_token_绑定失败", body: `{}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "垃圾凭证", body: `{"refresh_token":"garbage"}`, wantHTTP: 401, wantCode: "AUTH_REFRESH_INVALID"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPost, "/api/auth/refresh", "", tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}

	t.Run("轮换成功_旧凭证即刻作废", func(t *testing.T) {
		body := fmt.Sprintf(`{"refresh_token":%q}`, old["refresh_token"])
		rec := doJSON(f.r, http.MethodPost, "/api/auth/refresh", "", body)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "0", envCode(t, rec.Body.String()))
		fresh := envData(t, rec.Body.String())
		require.NotEqual(t, old["refresh_token"], fresh["refresh_token"], "刷新凭证必须轮换（session.go:169-190）")
		require.NotEqual(t, old["access_token"], fresh["access_token"])

		// 旧刷新凭证再刷 → 401（plan §7.2 轮换）。
		rec = doJSON(f.r, http.MethodPost, "/api/auth/refresh", "", body)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Equal(t, "AUTH_REFRESH_INVALID", envCode(t, rec.Body.String()))
	})
}

// ---- POST /api/auth/logout（handleLogout）----

func TestHandlerLogout(t *testing.T) {
	f := newHandlerFixture(t)
	token, _ := f.loginSuper(t, "admin")

	// 未认证 → 中间件 401 AUTH_TOKEN_INVALID。
	rec := doJSON(f.r, http.MethodPost, "/api/auth/logout", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_TOKEN_INVALID", envCode(t, rec.Body.String()))

	// 登出成功。
	rec = doJSON(f.r, http.MethodPost, "/api/auth/logout", token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, true, envData(t, rec.Body.String())["logout"])

	// Token 即刻失效（plan §7.2：会话删除后 AuthRequired 401 SESSION_INVALID）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", token, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_SESSION_INVALID", envCode(t, rec.Body.String()))
}

// ---- GET /api/auth/me（handleMe）----

func TestHandlerMe(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "root")

	// 未认证 → 401。
	rec := doJSON(f.r, http.MethodGet, "/api/auth/me", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 超管：is_super=true、user 节点、permissions 为数组（空数组而非 null，service_auth.go:465-467）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.Equal(t, true, d["is_super"])
	require.Equal(t, DataScopeAll, d["data_scope"])
	user := d["user"].(map[string]any)
	require.Equal(t, "root", user["username"])
	require.IsType(t, []any{}, d["permissions"])

	// 普通用户：permissions 携带角色绑定权限点。
	role := f.repo.seedRole("viewer", StatusEnabled, false)
	perm := f.repo.seedPerm(PermUserList, StatusEnabled)
	f.repo.bindPerm(role, perm)
	uid := f.repo.seedUser("op", testPassword, nil)
	f.repo.bindRole(uid, role)
	opTok, _ := loginToken(t, f.svc, "op")
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", opTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	perms := envData(t, rec.Body.String())["permissions"].([]any)
	require.Len(t, perms, 1)
	require.Equal(t, PermUserList, perms[0])
}

// TestHandlerMeWithoutAuthContext 无 UserContext 时的 handler 防御分支（handler.go:194-198）。
func TestHandlerMeWithoutAuthContext(t *testing.T) {
	newHandlerFixture(t) // 仅为装配包级依赖（svcOf 需要 wired）
	r := newTestGin()
	r.GET("/api/auth/me", handleMe) // 不挂 AuthRequired：svc 可用但上下文缺位
	rec := doJSON(r, http.MethodGet, "/api/auth/me", "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "COMMON_UNAUTHORIZED", envCode(t, rec.Body.String()))
}

// ---- PUT /api/auth/password（handlePassword）----

func TestHandlerPassword(t *testing.T) {
	f := newHandlerFixture(t)
	token, _ := f.loginSuper(t, "gina")

	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
	}{
		{name: "未认证", body: `{"old_password":"Passw0rd","new_password":"NewPassw0rd"}`,
			wantHTTP: 401, wantCode: "AUTH_TOKEN_INVALID"},
		{name: "缺字段_绑定失败", body: `{}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "原密码错误", body: `{"old_password":"wrongOld1","new_password":"NewPassw0rd"}`,
			wantHTTP: 400, wantCode: "AUTH_PASSWORD_MISMATCH"},
		{name: "新密码不满足策略", body: `{"old_password":"Passw0rd","new_password":"simple"}`,
			wantHTTP: 400, wantCode: "AUTH_PASSWORD_WEAK"},
	}
	for _, tc := range tests[:1] {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPut, "/api/auth/password", "", tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}
	for _, tc := range tests[1:] {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPut, "/api/auth/password", token, tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}

	t.Run("成功后旧密码失效", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPut, "/api/auth/password", token,
			`{"old_password":"Passw0rd","new_password":"NewPassw0rd"}`)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "0", envCode(t, rec.Body.String()))
		require.Equal(t, true, envData(t, rec.Body.String())["changed"])

		// 旧密码登录被拒、新密码登录成功（permission.md §3.2）。
		oldLogin := fmt.Sprintf(`{"username":"gina","password":"Passw0rd",%s}`, f.captchaFields(t, ""))
		rec = doJSON(f.r, http.MethodPost, "/api/auth/login", "", oldLogin)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		newLogin := fmt.Sprintf(`{"username":"gina","password":"NewPassw0rd",%s}`, f.captchaFields(t, ""))
		rec = doJSON(f.r, http.MethodPost, "/api/auth/login", "", newLogin)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

// ---- GET /api/auth/sessions（handleSessionList）----

func TestHandlerSessionList(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")

	// 无 auth:session:list → 403 + details.permission（middleware.go:145-151）。
	rec := doJSON(f.r, http.MethodGet, "/api/auth/sessions", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))
	require.Equal(t, PermSessionList, mustJSON(t, rec.Body.String())["details"].(map[string]any)["permission"])

	// 非法分页参数（response.ParsePage，response.go:52-72）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions?page=0", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions?pageSize=200", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 成功：两个在线会话；内存分页（handler.go:269-286）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions?page=1&pageSize=1", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.EqualValues(t, 2, d["total"])
	require.EqualValues(t, 1, d["pageSize"])
	require.Len(t, envItems(t, rec.Body.String()), 1)

	// 第 2 页：另一条会话。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions?page=2&pageSize=1", superTok, "")
	require.Len(t, envItems(t, rec.Body.String()), 1)

	// 全量页：当前请求会话标记 current=true（handler.go:278-285）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions", superTok, "")
	items := envItems(t, rec.Body.String())
	require.Len(t, items, 2)
	current, other := 0, 0
	for _, it := range items {
		m := it.(map[string]any)
		require.NotEmpty(t, m["session_id"])
		require.NotEmpty(t, m["username"])
		if m["username"] == "admin" {
			require.Equal(t, true, m["current"], "当前请求会话必须标记 current")
			current++
		} else {
			require.Equal(t, false, m["current"])
			other++
		}
	}
	require.Equal(t, 1, current)
	require.Equal(t, 1, other)
}

// ---- DELETE /api/auth/sessions/:id（handleSessionKick）----

func TestHandlerSessionKick(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")

	// 无 auth:session:kick → 403。
	rec := doJSON(f.r, http.MethodDelete, "/api/auth/sessions/sid-x", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))

	// 不存在的会话 → 404（service_rbac 经 KickSession，errors.go:45）。
	rec = doJSON(f.r, http.MethodDelete, "/api/auth/sessions/sid-nonexistent", superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_SESSION_NOT_FOUND", envCode(t, rec.Body.String()))

	// 超管经会话列表定位 op 的 SID（纯 HTTP 端到端）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/sessions", superTok, "")
	var opSID string
	for _, it := range envItems(t, rec.Body.String()) {
		m := it.(map[string]any)
		if m["username"] == "op" {
			opSID = m["session_id"].(string)
		}
	}
	require.NotEmpty(t, opSID, "会话列表必须能定位目标用户")

	// 踢下线成功。
	rec = doJSON(f.r, http.MethodDelete, "/api/auth/sessions/"+opSID, superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, true, envData(t, rec.Body.String())["kicked"])

	// 被踢用户 token 即刻失效（permission.md §3.4）。
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", staffTok, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "AUTH_SESSION_INVALID", envCode(t, rec.Body.String()))

	// 重复踢 → 404（幂等核验）。
	rec = doJSON(f.r, http.MethodDelete, "/api/auth/sessions/"+opSID, superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ---- GET /api/users（handleUserList）----

func TestHandlerUserList(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")
	dept := f.repo.seedDept("D-HQ", StatusEnabled, 0)
	f.repo.seedUser("carol", testPassword, func(u *User) {
		id := database.ID(dept)
		u.DepartmentID = &id
	})
	f.repo.seedUser("dave", testPassword, func(u *User) { u.Status = UserStatusDisabled })

	// 无 auth:user:list → 403。
	rec := doJSON(f.r, http.MethodGet, "/api/users", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))

	// 非法查询/分页参数（queryID fail-fast，handler.go:62-75）。
	for _, p := range []string{
		"/api/users?department_id=abc", "/api/users?department_id=0", "/api/users?page=0", "/api/users?pageSize=101",
	} {
		rec = doJSON(f.r, http.MethodGet, p, superTok, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, p)
		require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()), p)
	}

	// 成功：全量 4 人 + 分页信封字段。
	rec = doJSON(f.r, http.MethodGet, "/api/users", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.EqualValues(t, 4, d["total"])
	require.EqualValues(t, 1, d["page"])
	require.EqualValues(t, 20, d["pageSize"])
	require.IsType(t, []any{}, d["items"])

	// keyword 过滤（real_name/username）。
	rec = doJSON(f.r, http.MethodGet, "/api/users?keyword=carol", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])
	// 列表不泄露 last_login_ip（S9，service_rbac.go:524）。
	item := envItems(t, rec.Body.String())[0].(map[string]any)
	require.Equal(t, "carol", item["username"])
	require.Empty(t, item["last_login_ip"])

	// status 过滤。
	rec = doJSON(f.r, http.MethodGet, "/api/users?status=DISABLED", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])

	// department_id 过滤。
	rec = doJSON(f.r, http.MethodGet, "/api/users?department_id="+strconv.FormatInt(dept, 10), superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])

	// S9 端到端：DEPARTMENT 范围操作者的 department_id 入参被会话范围覆盖（service_rbac.go:500-506）。
	dept2 := f.repo.seedDept("D-OTHER", StatusEnabled, 0)
	f.repo.seedUser("other", testPassword, func(u *User) {
		id := database.ID(dept2)
		u.DepartmentID = &id
	})
	mgrUID := f.repo.seedUser("mgr", testPassword, func(u *User) {
		u.DataScope = DataScopeDepartment
		id := database.ID(dept)
		u.DepartmentID = &id
	})
	mgrRole := f.repo.seedRole("mgr-role", StatusEnabled, false)
	f.repo.bindPerm(mgrRole, f.repo.seedPerm(PermUserList, StatusEnabled))
	f.repo.bindRole(mgrUID, mgrRole)
	mgrTok, _ := loginToken(t, f.svc, "mgr")
	rec = doJSON(f.r, http.MethodGet, "/api/users?department_id="+strconv.FormatInt(dept2, 10), mgrTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	// D-HQ 部门下有 carol 与 mgr 本人；传入的 D-OTHER（other）与 DISABLED 的 dave 均不可见。
	names := map[string]bool{}
	for _, it := range envItems(t, rec.Body.String()) {
		names[it.(map[string]any)["username"].(string)] = true
	}
	require.Equal(t, map[string]bool{"carol": true, "mgr": true}, names,
		"DEPARTMENT 范围必须收敛到本部门，禁止前端传参放大（permission.md §4）")
}

// ---- POST /api/users（handleUserCreate）----

func TestHandlerUserCreate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")
	role := f.repo.seedRole("staff", StatusEnabled, false)

	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
	}{
		{name: "非JSON体_绑定失败", body: `not-json`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法用户名", body: `{"username":"1bad","password":"Passw0rd"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "弱密码", body: `{"username":"ok1","password":"short"}`, wantHTTP: 400, wantCode: "AUTH_PASSWORD_WEAK"},
		{name: "非法数据范围", body: `{"username":"ok1","password":"Passw0rd","data_scope":"OTHER"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "部门不存在", body: `{"username":"ok1","password":"Passw0rd","department_id":999}`, wantHTTP: 404, wantCode: "AUTH_DEPT_NOT_FOUND"},
		{name: "角色不存在", body: `{"username":"ok1","password":"Passw0rd","role_ids":[999]}`, wantHTTP: 404, wantCode: "AUTH_ROLE_NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPost, "/api/users", superTok, tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}

	t.Run("无权限", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/users", staffTok, `{"username":"ok1","password":"Passw0rd"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("成功_缺省SELF最小授权_首登强制改密", func(t *testing.T) {
		body := fmt.Sprintf(`{"username":"ok.name","password":"Passw0rd","real_name":"甲","role_ids":[%d]}`, role)
		rec := doJSON(f.r, http.MethodPost, "/api/users", superTok, body)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "0", envCode(t, rec.Body.String()))
		d := envData(t, rec.Body.String())
		require.Equal(t, "ok.name", d["username"])
		require.Equal(t, DataScopeSelf, d["data_scope"], "缺省 data_scope=SELF（deny-by-default）")
		uid, err := strconv.ParseInt(d["id"].(string), 10, 64)
		require.NoError(t, err)
		u, err := f.repo.FindUserByID(context.Background(), uid)
		require.NoError(t, err)
		require.True(t, u.MustChangePassword, "S14：管理员创建的用户首登强制改密")
	})

	t.Run("重复用户名_409", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/users", superTok, `{"username":"ok.name","password":"Passw0rd"}`)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "AUTH_USERNAME_EXISTS", envCode(t, rec.Body.String()))
	})
}

// ---- GET /api/users/:id（handleUserDetail）----

func TestHandlerUserDetail(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, superUID := f.loginSuper(t, "admin")
	staffTok, staffUID := f.loginStaff(t, "op")

	// 路径 ID 非法（pathID fail-fast，handler.go:49-59）。
	for _, p := range []string{"/api/users/abc", "/api/users/0", "/api/users/-1"} {
		rec := doJSON(f.r, http.MethodGet, p, superTok, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, p)
		require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()), p)
	}

	// 不存在 → 404（与越权同形，S9 不确认存在性）。
	rec := doJSON(f.r, http.MethodGet, "/api/users/999", superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_USER_NOT_FOUND", envCode(t, rec.Body.String()))

	// 无权限 → 403。
	rec = doJSON(f.r, http.MethodGet, "/api/users/"+strconv.FormatInt(superUID, 10), staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 成功：ID 字符串形态 + role_ids 装配（service_rbac.go:546-568）。
	rec = doJSON(f.r, http.MethodGet, "/api/users/"+strconv.FormatInt(superUID, 10), superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	d := envData(t, rec.Body.String())
	require.Equal(t, strconv.FormatInt(superUID, 10), d["id"])
	require.Equal(t, "admin", d["username"])
	require.IsType(t, []any{}, d["role_ids"])
	require.EqualValues(t, []any{fmt.Sprintf("%d", f.superRoleID)}, d["role_ids"])

	// S9 端到端：SELF 范围操作者查看他人 → 与不存在同形 404；本人可见。
	selfRole := f.repo.seedRole("self-role", StatusEnabled, false)
	f.repo.bindPerm(selfRole, f.repo.seedPerm(PermUserRead, StatusEnabled))
	selfUID := f.repo.seedUser("selfuser", testPassword, func(u *User) { u.DataScope = DataScopeSelf })
	f.repo.bindRole(selfUID, selfRole)
	selfTok, _ := loginToken(t, f.svc, "selfuser")
	rec = doJSON(f.r, http.MethodGet, "/api/users/"+strconv.FormatInt(superUID, 10), selfTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = doJSON(f.r, http.MethodGet, "/api/users/"+strconv.FormatInt(selfUID, 10), selfTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "selfuser", envData(t, rec.Body.String())["username"])

	_ = staffUID
}

// ---- PUT /api/users/:id（handleUserUpdate）----

func TestHandlerUserUpdate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, superUID := f.loginSuper(t, "admin")
	dept := f.repo.seedDept("D-HQ", StatusEnabled, 0)
	carolUID := f.repo.seedUser("carol", testPassword, nil)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/users/abc", superTok, `{"real_name":"x"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 非法 data_scope → 400（service 校验，service_rbac.go:195-198）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(carolUID, 10), superTok, `{"data_scope":"OTHER"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 目标不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/users/999", superTok, `{"real_name":"x"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_USER_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功：改名 + 挂部门（响应回读 + 落库核验）。
	body := fmt.Sprintf(`{"real_name":"新名字","department_id":%d}`, dept)
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(carolUID, 10), superTok, body)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.Equal(t, "新名字", d["real_name"])
	require.Equal(t, strconv.FormatInt(dept, 10), d["department_id"])
	u, err := f.repo.FindUserByID(context.Background(), carolUID)
	require.NoError(t, err)
	require.Equal(t, "新名字", u.RealName)

	// S1 端到端：非超管操作者授予 super_admin 角色或携带越界权限点的角色 → 403（service_rbac.go:650-684）。
	opTok, opUID := f.loginWithPerm(t, "op2", PermUserUpdate)
	richRole := f.repo.seedRole("rich", StatusEnabled, false)
	f.repo.bindPerm(richRole, f.repo.seedPerm(PermRoleList, StatusEnabled))
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "授予super_admin角色", body: fmt.Sprintf(`{"role_ids":[%d]}`, f.superRoleID), wantCode: "AUTH_ROLE_ESCALATION_DENIED"},
		{name: "授予自身不持有的权限点角色", body: fmt.Sprintf(`{"role_ids":[%d]}`, richRole), wantCode: "AUTH_PERM_ESCALATION_DENIED"},
	}
	for _, tc := range tests {
		t.Run("S1_"+tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(carolUID, 10), opTok, tc.body)
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}
	_ = opUID
	_ = superUID
}

// ---- PUT /api/users/:id/status（handleUserStatus）----

func TestHandlerUserStatus(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, superUID := f.loginSuper(t, "admin")
	targetTok, targetUID := f.loginStaff(t, "op")

	// 非超管动超管 → 403（assertUserMutable，service_rbac.go:582-589）。
	guardTok, guardUID := f.loginWithPerm(t, "guard", PermUserStatus)
	rec := doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(superUID, 10)+"/status", guardTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "AUTH_SUPER_USER_PROTECTED", envCode(t, rec.Body.String()))

	// 缺 status 字段 → 400 绑定失败（StatusRequest required，handler.go:683-685）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/status", superTok, `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 非法状态值（通过绑定、被 Service 校验拒绝，service_rbac.go:345-347）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/status", superTok,
		`{"status":"PAUSED"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 自停用保护 → 403（service_rbac.go:352-354）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(superUID, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "AUTH_SELF_OPERATION_FORBIDDEN", envCode(t, rec.Body.String()))

	// 成功停用 → 200 + 会话全部下线（离岗路径，service_rbac.go:370-378）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, "DISABLED", envData(t, rec.Body.String())["status"])
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", targetTok, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	_ = guardUID
}

// ---- PUT /api/users/:id/reset-password（handleUserResetPassword）----

func TestHandlerUserResetPassword(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	targetTok, targetUID := f.loginStaff(t, "op")

	// 弱密码 → 400（service_rbac.go:390-392）。
	rec := doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/reset-password", superTok,
		`{"new_password":"short"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "AUTH_PASSWORD_WEAK", envCode(t, rec.Body.String()))

	// 目标不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/users/999/reset-password", superTok, `{"new_password":"ResetPw123"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_USER_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功 → 200；目标 must_change_password=true、全部会话下线（service_rbac.go:387-418）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/reset-password", superTok,
		`{"new_password":"ResetPw123"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, true, envData(t, rec.Body.String())["reset"])

	u, err := f.repo.FindUserByID(context.Background(), targetUID)
	require.NoError(t, err)
	require.True(t, u.MustChangePassword)
	require.True(t, VerifyPassword(u.PasswordHash, "ResetPw123"))
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", targetTok, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// ---- PUT /api/users/:id/unlock（handleUserUnlock）----

func TestHandlerUserUnlock(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	uid := f.repo.seedUser("leon", testPassword, func(u *User) {
		u.LockedUntil = database.JSONTime{Time: time.Now().Add(10 * time.Minute)}
	})
	_, _ = f.svc.guard.Fail(context.Background(), "leon")
	_, _ = f.svc.guard.Fail(context.Background(), "leon")

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/users/abc/unlock", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/users/999/unlock", superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_USER_NOT_FOUND", envCode(t, rec.Body.String()))

	// 无权限 → 403。
	staffTok, _ := f.loginStaff(t, "op")
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(uid, 10)+"/unlock", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 成功 → 200；locked_until 清零 + 失败计数清空（service_rbac.go:422-445）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(uid, 10)+"/unlock", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, true, envData(t, rec.Body.String())["unlocked"])
	u, err := f.repo.FindUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.True(t, u.LockedUntil.IsZero())
	n, err := f.svc.guard.Count(context.Background(), "leon")
	require.NoError(t, err)
	require.Zero(t, n)
}

// ---- PUT /api/users/:id/roles（handleUserAssignRoles）----

func TestHandlerUserAssignRoles(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	role := f.repo.seedRole("staff", StatusEnabled, false)
	targetTok, targetUID := f.loginStaff(t, "op")

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/users/abc/roles", superTok, `{"role_ids":[]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法角色 → 404（assertRolesUsable，service_rbac.go:598-610）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/roles", superTok,
		`{"role_ids":[999]}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_ROLE_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功 → 200；DB 落库 + 目标会话强制下线（S10，service_rbac.go:485-491）。
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/roles", superTok,
		fmt.Sprintf(`{"role_ids":[%d]}`, role))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, true, envData(t, rec.Body.String())["assigned"])
	rids, err := f.repo.ListRoleIDsByUser(context.Background(), targetUID)
	require.NoError(t, err)
	require.Equal(t, []int64{role}, rids)
	rec = doJSON(f.r, http.MethodGet, "/api/auth/me", targetTok, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "S10：特权变更后目标会话必须下线")

	// 绑定停用角色 → 404。
	disabled := f.repo.seedRole("disabled-role", StatusDisabled, false)
	rec = doJSON(f.r, http.MethodPut, "/api/users/"+strconv.FormatInt(targetUID, 10)+"/roles", superTok,
		fmt.Sprintf(`{"role_ids":[%d]}`, disabled))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
