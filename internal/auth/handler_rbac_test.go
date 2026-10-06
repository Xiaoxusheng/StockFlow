package auth

// handler 层数据驱动表测试（续）：/api/roles、/api/permissions、/api/departments 共 12 条。
// 环境与助手见 handler_test.go（handlerFixture/doJSON/envCode/envData）。
// GET /api/permissions 为 ask 点名的无数据覆盖接口，本文件给出全分支覆盖。

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
)

// ---- GET /api/roles（handleRoleList）----

func TestHandlerRoleList(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")
	f.repo.seedRole("alpha", StatusEnabled, false)
	f.repo.seedRole("beta", StatusDisabled, false)

	// 无 auth:role:list → 403。
	rec := doJSON(f.r, http.MethodGet, "/api/roles", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))

	// 非法分页。
	rec = doJSON(f.r, http.MethodGet, "/api/roles?page=0", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 成功：全量（super_admin + loginStaff 的 role-op + alpha + beta = 4）。
	rec = doJSON(f.r, http.MethodGet, "/api/roles", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.EqualValues(t, 4, d["total"])
	require.IsType(t, []any{}, d["items"])

	// keyword 过滤。
	rec = doJSON(f.r, http.MethodGet, "/api/roles?keyword=alpha", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])

	// status 过滤（role-op 为 ENABLED，不计入）。
	rec = doJSON(f.r, http.MethodGet, "/api/roles?status=DISABLED", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])
}

// ---- POST /api/roles（handleRoleCreate）----

func TestHandlerRoleCreate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")

	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
	}{
		{name: "缺name_绑定不拦但Service校验", body: `{"code":"gamma"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法编码_大写开头", body: `{"code":"BAD","name":"x"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法状态值", body: `{"code":"gamma","name":"伽马","status":"PAUSED"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPost, "/api/roles", superTok, tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}

	t.Run("无权限", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/roles", staffTok, `{"code":"gamma","name":"伽马"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("成功_is_system恒false", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/roles", superTok, `{"code":"gamma","name":"伽马"}`)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "0", envCode(t, rec.Body.String()))
		d := envData(t, rec.Body.String())
		require.Equal(t, "gamma", d["code"])
		require.Equal(t, "伽马", d["name"])
		require.Equal(t, false, d["is_system"])
		require.Equal(t, StatusEnabled, d["status"], "缺省启用（service_rbac.go:779-782）")
	})

	t.Run("重复编码_409", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/roles", superTok, `{"code":"gamma","name":"另一"}`)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "AUTH_ROLE_CODE_EXISTS", envCode(t, rec.Body.String()))
	})
}

// ---- GET /api/roles/:id（handleRoleDetail）----

func TestHandlerRoleDetail(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, superUID := f.loginSuper(t, "admin")
	role := f.repo.seedRole("alpha", StatusEnabled, false)
	perm := f.repo.seedPerm(PermUserList, StatusEnabled)
	f.repo.bindPerm(role, perm)
	f.repo.bindRole(superUID, role)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodGet, "/api/roles/abc", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodGet, "/api/roles/999", superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_ROLE_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功：permission_ids + user_count 装配（service_rbac.go:953-974）。
	rec = doJSON(f.r, http.MethodGet, "/api/roles/"+strconv.FormatInt(role, 10), superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	d := envData(t, rec.Body.String())
	require.Equal(t, "alpha", d["code"])
	require.Equal(t, []any{strconv.FormatInt(perm, 10)}, d["permission_ids"])
	require.EqualValues(t, 1, d["user_count"])
}

// ---- PUT /api/roles/:id（handleRoleUpdate）----

func TestHandlerRoleUpdate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	role := f.repo.seedRole("alpha", StatusEnabled, false)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/roles/abc", superTok, `{"name":"x"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// name 空 → 400（service_rbac.go:821-824）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10), superTok, `{"name":"  "}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/999", superTok, `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_ROLE_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功改名。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10), superTok, `{"name":"阿尔法"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "阿尔法", envData(t, rec.Body.String())["name"])
}

// ---- PUT /api/roles/:id/status（handleRoleStatus）----

func TestHandlerRoleStatus(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	role := f.repo.seedRole("switchable", StatusEnabled, false)
	perm := f.repo.seedPerm(PermUserList, StatusEnabled)
	f.repo.bindPerm(role, perm)
	holderUID := f.repo.seedUser("holder", testPassword, nil)
	f.repo.bindRole(holderUID, role)
	holderTok, _ := loginToken(t, f.svc, "holder")

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/roles/abc/status", superTok, `{"status":"DISABLED"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 非法状态值 → 400（service_rbac.go:849-851）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10)+"/status", superTok, `{"status":"PAUSED"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 内置角色禁停用 → 409（service_rbac.go:859-861）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(f.superRoleID, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "AUTH_ROLE_SYSTEM_LOCKED", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/999/status", superTok, `{"status":"DISABLED"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 持权用户先访问一次 → 权限缓存回填。
	rec = doJSON(f.r, http.MethodGet, "/api/users", holderTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, f.rdb.has(permsKey(holderUID)), "访问后权限缓存必须回填")

	// 成功停用 → 200；该角色全部用户权限缓存定向失效（service_rbac.go:877-883）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10)+"/status", superTok, `{"status":"DISABLED"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, "DISABLED", envData(t, rec.Body.String())["status"])
	require.False(t, f.rdb.has(permsKey(holderUID)), "停用角色后绑定用户权限缓存必须失效")

	// 停用后持权用户再访问 → 403（回源无启用角色，fail-closed）。
	rec = doJSON(f.r, http.MethodGet, "/api/users", holderTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))
}

// ---- PUT /api/roles/:id/permissions（handleRoleAssignPermissions）----

func TestHandlerRoleAssignPermissions(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	role := f.repo.seedRole("alpha", StatusEnabled, false)
	perm := f.repo.seedPerm(PermUserList, StatusEnabled)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/roles/abc/permissions", superTok, `{"permission_ids":[]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// is_system 角色禁止重绑 → 409（service_rbac.go:901-903）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(f.superRoleID, 10)+"/permissions", superTok,
		`{"permission_ids":[]}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "AUTH_ROLE_SYSTEM_LOCKED", envCode(t, rec.Body.String()))

	// 非法权限点 → 400（service_rbac.go:904-911）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10)+"/permissions", superTok,
		`{"permission_ids":[999]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "AUTH_PERMISSION_INVALID", envCode(t, rec.Body.String()))

	// 角色不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/999/permissions", superTok, `{"permission_ids":[]}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 成功 → 200；绑定落库（全量替换语义）。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10)+"/permissions", superTok,
		fmt.Sprintf(`{"permission_ids":[%d]}`, perm))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, true, envData(t, rec.Body.String())["assigned"])
	pids, err := f.repo.ListPermissionIDsByRole(context.Background(), role)
	require.NoError(t, err)
	require.Equal(t, []int64{perm}, pids)

	// 空集全量替换 = 清空绑定。
	rec = doJSON(f.r, http.MethodPut, "/api/roles/"+strconv.FormatInt(role, 10)+"/permissions", superTok,
		`{"permission_ids":[]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	pids, err = f.repo.ListPermissionIDsByRole(context.Background(), role)
	require.NoError(t, err)
	require.Empty(t, pids)
}

// ---- GET /api/permissions（handlePermissionList）——ask 点名的无数据覆盖接口 ----

func TestHandlerPermissionList(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")

	// seed 四个权限点：MENU/API 各类型 + 启用/停用。
	pUser := f.repo.seedPerm(PermUserList, StatusEnabled) // API
	pMenu := f.repo.seedPerm("x:menu.view", StatusEnabled)
	f.repo.perms[pMenu].Type = PermTypeMenu
	f.repo.seedPerm("x:stock.audit", StatusDisabled)
	f.repo.seedPerm(PermDeptList, StatusEnabled)

	// 无 auth:permission:list → 403。
	rec := doJSON(f.r, http.MethodGet, "/api/permissions", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))

	// 非法分页参数。
	for _, p := range []string{"/api/permissions?page=0", "/api/permissions?pageSize=200"} {
		rec = doJSON(f.r, http.MethodGet, p, superTok, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, p)
		require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()), p)
	}

	// 成功：全量 4 条 + 分页信封。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	d := envData(t, rec.Body.String())
	require.EqualValues(t, 4, d["total"])
	require.EqualValues(t, 1, d["page"])
	require.EqualValues(t, 20, d["pageSize"])
	require.IsType(t, []any{}, d["items"])

	// type 筛选：MENU 仅 1 条，item 形态（ID 字符串/code/type/status）。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions?type=MENU", superTok, "")
	d = envData(t, rec.Body.String())
	require.EqualValues(t, 1, d["total"])
	item := envItems(t, rec.Body.String())[0].(map[string]any)
	require.Equal(t, PermTypeMenu, item["type"])
	require.Equal(t, "x:menu.view", item["code"])
	require.Equal(t, strconv.FormatInt(pMenu, 10), item["id"])
	require.Equal(t, StatusEnabled, item["status"])

	// status 筛选：DISABLED 仅 1 条。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions?status=DISABLED", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])

	// keyword 筛选（code 命中）。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions?keyword=x:stock", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])

	// keyword + type 组合：API 类型的 user 相关（user:list、dept:list 均不含 user 关键字……）。
	// 用精确 keyword=auth:user:list 验证组合过滤不串。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions?keyword="+PermUserList+"&type=API", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])
	item = envItems(t, rec.Body.String())[0].(map[string]any)
	require.Equal(t, strconv.FormatInt(pUser, 10), item["id"])

	// 分页内存切片边界：pageSize=2 时仍返回 repo 全量（fake 不分页，仅校验 total 稳定）。
	rec = doJSON(f.r, http.MethodGet, "/api/permissions?page=1&pageSize=2", superTok, "")
	require.EqualValues(t, 4, envData(t, rec.Body.String())["total"])
}

// ---- GET /api/departments（handleDeptList）----

func TestHandlerDeptList(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")
	f.repo.seedDept("D-HQ", StatusEnabled, 0)
	f.repo.seedDept("D-WHS", StatusDisabled, 0)

	// 无 auth:dept:list → 403。
	rec := doJSON(f.r, http.MethodGet, "/api/departments", staffTok, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "COMMON_PERMISSION_DENIED", envCode(t, rec.Body.String()))

	// 非法分页。
	rec = doJSON(f.r, http.MethodGet, "/api/departments?page=-1", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 成功：全量 2。
	rec = doJSON(f.r, http.MethodGet, "/api/departments", superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.EqualValues(t, 2, envData(t, rec.Body.String())["total"])

	// status / keyword 过滤。
	rec = doJSON(f.r, http.MethodGet, "/api/departments?status=ENABLED", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])
	rec = doJSON(f.r, http.MethodGet, "/api/departments?keyword=D-WHS", superTok, "")
	require.EqualValues(t, 1, envData(t, rec.Body.String())["total"])
}

// ---- POST /api/departments（handleDeptCreate）----

func TestHandlerDeptCreate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	staffTok, _ := f.loginStaff(t, "op")
	parent := f.repo.seedDept("D-PARENT", StatusEnabled, 0)

	tests := []struct {
		name     string
		body     string
		wantHTTP int
		wantCode string
	}{
		{name: "非法编码_须大写开头", body: `{"code":"d-low","name":"x"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "缺name", body: `{"code":"D-NEW"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "非法状态值", body: `{"code":"D-NEW","name":"新","status":"PAUSED"}`, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "父部门不存在", body: `{"code":"D-NEW","name":"新","parent_id":999}`, wantHTTP: 404, wantCode: "AUTH_DEPT_NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(f.r, http.MethodPost, "/api/departments", superTok, tc.body)
			require.Equal(t, tc.wantHTTP, rec.Code)
			require.Equal(t, tc.wantCode, envCode(t, rec.Body.String()))
		})
	}

	t.Run("无权限", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/departments", staffTok, `{"code":"D-NEW","name":"新"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("成功_挂父部门", func(t *testing.T) {
		body := fmt.Sprintf(`{"code":"D-CHILD","name":"子部门","parent_id":%d}`, parent)
		rec := doJSON(f.r, http.MethodPost, "/api/departments", superTok, body)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "0", envCode(t, rec.Body.String()))
		d := envData(t, rec.Body.String())
		require.Equal(t, "D-CHILD", d["code"])
		require.Equal(t, strconv.FormatInt(parent, 10), d["parent_id"])
		require.Equal(t, StatusEnabled, d["status"])
	})

	t.Run("重复编码_409", func(t *testing.T) {
		rec := doJSON(f.r, http.MethodPost, "/api/departments", superTok, `{"code":"D-CHILD","name":"另一个"}`)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.Equal(t, "AUTH_DEPT_CODE_EXISTS", envCode(t, rec.Body.String()))
	})
}

// ---- GET /api/departments/:id（handleDeptDetail）----

func TestHandlerDeptDetail(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	parent := f.repo.seedDept("D-PARENT", StatusEnabled, 0)
	child := f.repo.seedDept("D-CHILD", StatusEnabled, parent)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodGet, "/api/departments/abc", superTok, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodGet, "/api/departments/999", superTok, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_DEPT_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成功：parent_id 回显。
	rec = doJSON(f.r, http.MethodGet, "/api/departments/"+strconv.FormatInt(child, 10), superTok, "")
	require.Equal(t, http.StatusOK, rec.Code)
	d := envData(t, rec.Body.String())
	require.Equal(t, "D-CHILD", d["code"])
	require.Equal(t, strconv.FormatInt(parent, 10), d["parent_id"])
}

// ---- PUT /api/departments/:id（handleDeptUpdate）----

func TestHandlerDeptUpdate(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	parent := f.repo.seedDept("D-PARENT", StatusEnabled, 0)
	child := f.repo.seedDept("D-CHILD", StatusEnabled, parent)
	other := f.repo.seedDept("D-OTHER", StatusEnabled, 0)

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/departments/abc", superTok, `{"name":"x"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// name 空 → 400（service_rbac.go:1071-1074）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10), superTok, `{"name":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/999", superTok, `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "AUTH_DEPT_NOT_FOUND", envCode(t, rec.Body.String()))

	// 成环 → 400（父级不能挂到自身子孙，service_rbac.go:1088-1102）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10), superTok,
		fmt.Sprintf(`{"name":"总部","parent_id":%d}`, child))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 以自身为父级 → 400（service_rbac.go:1078-1080）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10), superTok,
		fmt.Sprintf(`{"name":"总部","parent_id":%d}`, parent))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 成功：改名 + 迁移到 other。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10), superTok,
		fmt.Sprintf(`{"name":"总部","parent_id":%d}`, other))
	require.Equal(t, http.StatusOK, rec.Code)
	d := envData(t, rec.Body.String())
	require.Equal(t, "总部", d["name"])
	require.Equal(t, strconv.FormatInt(other, 10), d["parent_id"])

	// 父级传 0 → 提升为顶级（service_rbac.go:1105-1107）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10), superTok,
		`{"name":"总部","parent_id":0}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, nil, envData(t, rec.Body.String())["parent_id"], "顶级部门 parent_id 为 null")
}

// ---- PUT /api/departments/:id/status（handleDeptStatus）----

func TestHandlerDeptStatus(t *testing.T) {
	f := newHandlerFixture(t)
	superTok, _ := f.loginSuper(t, "admin")
	parent := f.repo.seedDept("D-PARENT", StatusEnabled, 0)
	child := f.repo.seedDept("D-CHILD", StatusEnabled, parent)
	f.repo.seedUser("emp", testPassword, func(u *User) {
		id := database.ID(parent)
		u.DepartmentID = &id
	})

	// 路径 ID 非法 → 400。
	rec := doJSON(f.r, http.MethodPut, "/api/departments/abc/status", superTok, `{"status":"DISABLED"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 非法状态值 → 400（service_rbac.go:1139-1141）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10)+"/status", superTok,
		`{"status":"PAUSED"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "COMMON_INVALID_PARAM", envCode(t, rec.Body.String()))

	// 不存在 → 404。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/999/status", superTok, `{"status":"DISABLED"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 存在启用中子部门 → 409（service_rbac.go:1149-1156）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "AUTH_DEPT_HAS_ENABLED_CHILDREN", envCode(t, rec.Body.String()))

	// 停用子部门成功。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(child, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", envCode(t, rec.Body.String()))
	require.Equal(t, "DISABLED", envData(t, rec.Body.String())["status"])

	// 子部门已停用，但父部门下存在启用中用户 → 409（service_rbac.go:1157-1163）。
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(parent, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "AUTH_DEPT_HAS_ACTIVE_USERS", envCode(t, rec.Body.String()))
	require.EqualValues(t, 1, mustJSON(t, rec.Body.String())["details"].(map[string]any)["active_users"])

	// 无用户的部门可正常启停（再启用核验幂等）。
	free := f.repo.seedDept("D-FREE", StatusEnabled, 0)
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(free, 10)+"/status", superTok,
		`{"status":"DISABLED"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = doJSON(f.r, http.MethodPut, "/api/departments/"+strconv.FormatInt(free, 10)+"/status", superTok,
		`{"status":"ENABLED"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ENABLED", envData(t, rec.Body.String())["status"])
}

// ---- 未装配防御路径（abortUnwired，handler.go:22-26）----

func TestHandlerUnwiredAbort(t *testing.T) {
	resetWiredForTest()
	t.Cleanup(resetWiredForTest)

	r := newTestGin()
	r.POST("/api/auth/login", handleLogin)
	r.GET("/api/users", handleUserList) // 无权限中间件：直接到达 handler 的 svcOf 判定

	rec := doJSON(r, http.MethodPost, "/api/auth/login", "", `{"username":"a","password":"b"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "COMMON_INTERNAL_ERROR", envCode(t, rec.Body.String()))
	details := mustJSON(t, rec.Body.String())["details"].(map[string]any)
	require.Contains(t, details["reason"], "未装配")

	rec = doJSON(r, http.MethodGet, "/api/users", "", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "COMMON_INTERNAL_ERROR", envCode(t, rec.Body.String()))
}
