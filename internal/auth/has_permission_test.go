package auth

// has_permission_test.go 导出 HasPermission(c, code) 编程式权限判定测试（2026-10-06
// 测试加固轮补缺口）。既有 TestHasPermission（middleware_test.go:278）仅覆盖纯函数
// hasPermission(codes, perm)，RequirePermission 中间件路径有完整用例；而 has_permission.go
// 导出入口此前零用例——GET /api/search 组内逐 type 过滤委托的正是它
// （internal/search/handler.go:177 permOf 回退路径，搜索端点只挂认证不挂 RequirePermission）。
// testing.md §12.1 T1 要求「超管直通语义与 RequirePermission 同构」，本文件把该同构
// 断言落到导出函数本体：
//
//	1. 未认证（无 AuthRequired / 无用户快照）         → false（fail-closed）
//	2. 超管（持 super_admin 角色）无任何权限点        → true（直通，permission.md §1）
//	3. 普通用户持码（role→perm 绑定且状态 ENABLED）   → true
//	4. 普通用户无码                                  → false
//	5. 缓存+回源双故障                               → false（fail-closed，不放大权限；
//	                                                   与 TestRequirePermissionFailsClosed 同判据）

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// hasPermissionProbe 装配 AuthRequired + 判定探针路由（响应 {"ok": bool}），code 按用例注入。
func hasPermissionProbe(code string) *gin.Engine {
	r := newTestGin()
	r.Use(AuthRequired())
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": HasPermission(c, code)})
	})
	return r
}

// probeOK 发探针请求并解出 ok 布尔（HTTP 层恒 200，判定结果在 body）。
func probeOK(t *testing.T, r *gin.Engine, token string) bool {
	t.Helper()
	rec := doReq(r, http.MethodGet, "/probe", "Bearer "+token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	ok, _ := body["ok"].(bool)
	return ok
}

// TestHasPermissionUnauthenticatedFailClosed 未认证（AuthRequired 未挂载/未通过的
// 直调路径）→ false；与 RequirePermission 的 401 分支同语义（has_permission.go:22）。
func TestHasPermissionUnauthenticatedFailClosed(t *testing.T) {
	r := newTestGin()
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": HasPermission(c, PermInventoryList)})
	})
	rec := doReq(r, http.MethodGet, "/probe", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.False(t, body["ok"].(bool), "未认证必须 fail-closed false")
}

// TestHasPermissionSuperBypass 超管仅持 super_admin 角色（零权限点绑定）→ true。
// 与 TestRequirePermissionSuperBypass 同构；遗漏该分支则超管全局搜索看不到任何分组。
func TestHasPermissionSuperBypass(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)
	repo.seedUser("root", "Passw0rd", nil)
	repo.bindRole(1, repo.seedRole(SuperAdminRoleCode, StatusEnabled, true))

	token, _ := loginToken(t, svc, "root")
	require.True(t, probeOK(t, hasPermissionProbe(PermUserList), token),
		"超管无权限点也应直通（RequirePermission 同构）")
}

// TestHasPermissionByCode 普通用户：持码放行 / 无码拒绝（走缓存优先→回源真实路径）。
func TestHasPermissionByCode(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)
	role := repo.seedRole("operator", StatusEnabled, false)
	perm := repo.seedPerm(PermInventoryList, StatusEnabled)
	repo.bindPerm(role, perm)
	opUID := repo.seedUser("op", "Passw0rd", nil)
	repo.bindRole(opUID, role)

	token, _ := loginToken(t, svc, "op")
	require.True(t, probeOK(t, hasPermissionProbe(PermInventoryList), token), "持码用户应放行")
	require.False(t, probeOK(t, hasPermissionProbe(PermUserList), token), "未持码用户应拒绝")
}

// TestHasPermissionFailsClosed 权限数据不可用 → false（fail-closed，不放大权限；
// has_permission.go:32-34）。故障面取 repo.errPermCodes（回源故障）且权限缓存从未
// 命中/填充：缓存 miss → 回源失败 → error → false。不注入 rdb.errGet——会话读取
// 复用同一 Get 命令，errGet 会在 AuthRequired 层先 503「会话存储不可用」而到不了
// 权限判定面（middleware_test.go:270-276 的 503 即来自该层）。
// 另断言缓存先行可用性：权限点已缓存后回源故障，命中缓存照常放行（cache-first，
// service_auth.go:646-657——可用性与 fail-closed 并存，不互相拆台）。
func TestHasPermissionFailsClosed(t *testing.T) {
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)
	role := repo.seedRole("operator", StatusEnabled, false)
	perm := repo.seedPerm(PermInventoryList, StatusEnabled)
	repo.bindPerm(role, perm)
	opUID := repo.seedUser("op", "Passw0rd", nil)
	repo.bindRole(opUID, role)

	// 回源故障 + 缓存从未填充 → fail-closed false。
	repo.errPermCodes = true
	token, _ := loginToken(t, svc, "op")
	require.False(t, probeOK(t, hasPermissionProbe(PermInventoryList), token),
		"权限数据不可用必须 fail-closed false")

	// 缓存先行：回源故障但缓存已有码（另一用户先查询填充过）→ 照常放行。
	repo.errPermCodes = false
	probeOK(t, hasPermissionProbe(PermInventoryList), token) // 回源成功并回填缓存
	repo.errPermCodes = true
	require.True(t, probeOK(t, hasPermissionProbe(PermInventoryList), token),
		"缓存命中时回源故障不应影响判定")
}
