package auth

// 安全审查修复的专项单元测试（S1/S4/S7/S8/S9/S10/S14/S15）：
// 全部经接口替身与内存驱动执行，不依赖 PostgreSQL/Redis（ask 约束）。
// S7 用 zap observer 断言兜底日志；login_logs 落库行为经 fakedb 的 SQL 捕获断言。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// scopeActor 带数据范围快照的操作者（S1/S9 判定数据源；模拟 handler actorOf 注入的会话快照）。
func scopeActor(uid int64, username, scope string, deptID int64, whIDs ...int64) Actor {
	a := testActor(uid, username, false)
	a.DataScope = scope
	a.DeptID = deptID
	a.WarehouseIDs = whIDs
	return a
}

func strPtr(s string) *string { return &s }

// ---- S1：授予侧特权边界 ----

func TestAssignRolesEscalationDenied(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	superRole := repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)
	permList := repo.seedPerm(PermUserList, StatusEnabled)
	permRole := repo.seedPerm(PermRoleList, StatusEnabled)
	staffRole := repo.seedRole("staff", StatusEnabled, false)
	repo.bindPerm(staffRole, permList)
	repo.bindPerm(staffRole, permRole) // staff 携带操作者不持有的权限点
	bareRole := repo.seedRole("bare", StatusEnabled, false)

	op := repo.seedUser("op", testPassword, nil)
	opRole := repo.seedRole("op", StatusEnabled, false)
	repo.bindRole(op, opRole)
	repo.bindPerm(opRole, permList) // 操作者仅持有 auth:user:list
	// S9 读写对称：操作者须可见目标方可写——此处赋予 ALL 数据范围（非超管，S1 边界照常生效）。
	actor := scopeActor(op, "op", DataScopeAll, 0)
	target := repo.seedUser("target", testPassword, nil)

	// 授予 super_admin 角色 → 拒绝（details 指明被拒角色）。
	err := svc.AssignRoles(ctx, actor, target, AssignRolesInput{RoleIDs: []int64{superRole}})
	require.Equal(t, "AUTH_ROLE_ESCALATION_DENIED", codeOf(t, err))
	var e *response.Error
	require.True(t, errors.As(err, &e))
	require.Contains(t, fmt.Sprint(e.Details), "super_admin")

	// 授予携带越界权限点的角色 → 拒绝（details 含越界权限编码）。
	err = svc.AssignRoles(ctx, actor, target, AssignRolesInput{RoleIDs: []int64{staffRole}})
	require.Equal(t, "AUTH_PERM_ESCALATION_DENIED", codeOf(t, err))
	require.True(t, errors.As(err, &e))
	require.Contains(t, fmt.Sprint(e.Details), PermRoleList)

	// 授予无权限点角色 / 自身持有范围内角色 → 放行；super_admin 操作者不受限。
	require.NoError(t, svc.AssignRoles(ctx, actor, target, AssignRolesInput{RoleIDs: []int64{bareRole}}))
	require.NoError(t, svc.AssignRoles(ctx, testActor(9, "root", true), target,
		AssignRolesInput{RoleIDs: []int64{superRole, staffRole}}))
}

func TestCreateUserScopeEscalation(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	svc.useChecker(fakeWhChecker{ok: map[int64]bool{7: true, 8: true}})
	dept := repo.seedDept("D-A", StatusEnabled, 0)
	other := repo.seedDept("D-B", StatusEnabled, 0)

	// SPECIFIED_WAREHOUSE 操作者（自身绑定仓库 7）。
	whOp := repo.seedUser("whop", testPassword, nil)
	whActor := scopeActor(whOp, "whop", DataScopeSpecifiedWh, 0, 7)

	// 授予 ALL / 超出自身仓库集 → 拒绝；自身仓库集内 → 放行。
	_, err := svc.CreateUser(ctx, whActor, UserCreateInput{Username: "u.all", Password: testPassword, DataScope: DataScopeAll})
	require.Equal(t, "AUTH_SCOPE_ESCALATION_DENIED", codeOf(t, err))
	_, err = svc.CreateUser(ctx, whActor, UserCreateInput{
		Username: "u.wh2", Password: testPassword, DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{7, 8},
	})
	require.Equal(t, "AUTH_SCOPE_ESCALATION_DENIED", codeOf(t, err))
	_, err = svc.CreateUser(ctx, whActor, UserCreateInput{
		Username: "u.ok", Password: testPassword, DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{7},
	})
	require.NoError(t, err)

	// DEPARTMENT 操作者：仅可授予自身部门。
	dOp := repo.seedUser("dop", testPassword, nil)
	dActor := scopeActor(dOp, "dop", DataScopeDepartment, dept)
	_, err = svc.CreateUser(ctx, dActor, UserCreateInput{
		Username: "u.dept2", Password: testPassword, DataScope: DataScopeDepartment, DepartmentID: &other,
	})
	require.Equal(t, "AUTH_SCOPE_ESCALATION_DENIED", codeOf(t, err))
	_, err = svc.CreateUser(ctx, dActor, UserCreateInput{
		Username: "u.dept.ok", Password: testPassword, DataScope: DataScopeDepartment, DepartmentID: &dept,
	})
	require.NoError(t, err)

	// 最小范围操作者：可授 SELF（无放大），不可授 SELF_IN_CHARGE / ALL。
	sOp := repo.seedUser("sop", testPassword, nil)
	sActor := scopeActor(sOp, "sop", DataScopeSelf, 0)
	_, err = svc.CreateUser(ctx, sActor, UserCreateInput{Username: "u.self.ok", Password: testPassword, DataScope: DataScopeSelf})
	require.NoError(t, err)
	_, err = svc.CreateUser(ctx, sActor, UserCreateInput{Username: "u.sic", Password: testPassword, DataScope: DataScopeSelfInCharge})
	require.Equal(t, "AUTH_SCOPE_ESCALATION_DENIED", codeOf(t, err))
}

func TestUpdateUserEscalationDenied(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	permList := repo.seedPerm(PermUserList, StatusEnabled)
	permRole := repo.seedPerm(PermRoleList, StatusEnabled)
	staff := repo.seedRole("staff", StatusEnabled, false)
	repo.bindPerm(staff, permList)
	repo.bindPerm(staff, permRole)
	dept := repo.seedDept("D-A", StatusEnabled, 0)
	mutA := func(u *User) { id := database.ID(dept); u.DepartmentID = &id }

	op := repo.seedUser("op", testPassword, mutA)
	opRole := repo.seedRole("op", StatusEnabled, false)
	repo.bindRole(op, opRole)
	repo.bindPerm(opRole, permList)
	// S9 读写对称：操作者须可见目标方可写——同部门 + DEPARTMENT 范围（保持非 ALL，
	// 使数据范围越界断言依然成立）。
	actor := scopeActor(op, "op", DataScopeDepartment, dept)
	target := repo.seedUser("target", testPassword, mutA)

	// 数据范围越界 → 拒绝。
	_, err := svc.UpdateUser(ctx, actor, target, UserUpdateInput{DataScope: strPtr(DataScopeAll)})
	require.Equal(t, "AUTH_SCOPE_ESCALATION_DENIED", codeOf(t, err))
	// 角色越界（携带操作者不持有的权限点）→ 拒绝。
	_, err = svc.UpdateUser(ctx, actor, target, UserUpdateInput{RoleIDs: []int64{staff}})
	require.Equal(t, "AUTH_PERM_ESCALATION_DENIED", codeOf(t, err))
	// 仅改姓名不涉授予 → 放行。
	_, err = svc.UpdateUser(ctx, actor, target, UserUpdateInput{RealName: strPtr("乙")})
	require.NoError(t, err)
}

func TestAssignPermissionsEscalation(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	permList := repo.seedPerm(PermUserList, StatusEnabled)
	permRole := repo.seedPerm(PermRoleList, StatusEnabled)
	custom := repo.seedRole("custom", StatusEnabled, false)
	sys := repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)

	op := repo.seedUser("op", testPassword, nil)
	opRole := repo.seedRole("op", StatusEnabled, false)
	repo.bindRole(op, opRole)
	repo.bindPerm(opRole, permList)
	actor := testActor(op, "op", false)

	// 内置角色禁止重绑权限点（对齐 UpdateRoleStatus 的 is_system 保护，超管亦受限）。
	err := svc.AssignPermissions(ctx, testActor(9, "root", true), sys,
		AssignPermissionsInput{PermissionIDs: []int64{permList}})
	require.Equal(t, "AUTH_ROLE_SYSTEM_LOCKED", codeOf(t, err))

	// 越界权限点 → 拒绝；自身持有范围内 → 放行。
	err = svc.AssignPermissions(ctx, actor, custom, AssignPermissionsInput{PermissionIDs: []int64{permList, permRole}})
	require.Equal(t, "AUTH_PERM_ESCALATION_DENIED", codeOf(t, err))
	require.NoError(t, svc.AssignPermissions(ctx, actor, custom, AssignPermissionsInput{PermissionIDs: []int64{permList}}))
}

// ---- S8：登录响应统一防枚举 ----

func TestLoginUniformErrorForDisabledAndLocked(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	resetSQLCapture()
	ctx := context.Background()

	repo.seedUser("dan", testPassword, func(u *User) { u.Status = UserStatusDisabled })
	_, err := svc.Login(ctx, LoginInput{Username: "dan", Password: testPassword, IP: "1.1.1.1"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err)) // 不回显停用状态
	require.True(t, sqlCaptured("login_logs", "AUTH_ACCOUNT_DISABLED"), "真实原因入 login_logs")

	repo.seedUser("larry", testPassword, func(u *User) {
		u.LockedUntil = database.JSONTime{Time: time.Now().Add(10 * time.Minute)}
	})
	_, err = svc.Login(ctx, LoginInput{Username: "larry", Password: testPassword, IP: "1.1.1.1"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err)) // 不回显锁定状态
	var e *response.Error
	require.True(t, errors.As(err, &e))
	require.Nil(t, e.Details, "不得回显 locked_until")
	require.True(t, sqlCaptured("login_logs", "AUTH_ACCOUNT_LOCKED"), "真实原因入 login_logs")
}

// ---- S4：登录失败计数 username+IP 双键 ----

func TestLoginSprayLockedByIP(t *testing.T) {
	svc, repo, rdb := newServiceFixture(t)
	ctx := context.Background()
	repo.seedUser("bob", testPassword, nil)

	// 5 次未知用户名尝试：全部计入 IP 维度（防跨用户名喷洒），不计用户名维度，不锁任何账户行。
	for i := 0; i < 5; i++ {
		_, err := svc.Login(ctx, LoginInput{Username: fmt.Sprintf("ghost%d", i), Password: "whatever1", IP: "9.9.9.9"})
		require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	}
	require.Equal(t, int64(5), rdb.intVal(failKey(failIPDim("9.9.9.9"))))
	require.Equal(t, int64(0), rdb.intVal(failKey("ghost0")))
	require.Equal(t, 0, repo.lockCalls, "IP 维度达到阈值不触发账户行锁")

	// 同 IP 携带正确凭证也被拒（喷洒预算耗尽）；换 IP 正常登录。
	_, err := svc.Login(ctx, LoginInput{Username: "bob", Password: testPassword, IP: "9.9.9.9"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	res, err := svc.Login(ctx, LoginInput{Username: "bob", Password: testPassword, IP: "8.8.8.8"})
	require.NoError(t, err)
	require.NotEmpty(t, res.AccessToken)
}

func TestLoginFailureCountsBothDimensions(t *testing.T) {
	svc, repo, rdb := newServiceFixture(t)
	ctx := context.Background()
	repo.seedUser("bob", testPassword, nil)

	for i := 0; i < 3; i++ {
		_, err := svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "7.7.7.7"})
		require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	}
	require.Equal(t, int64(3), rdb.intVal(failKey("bob")))
	require.Equal(t, int64(3), rdb.intVal(failKey(failIPDim("7.7.7.7"))))

	// 用户名维度达到阈值 → 锁定账户行（不同 IP 的失败同样累计到用户名维度）。
	_, err := svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "6.6.6.6"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	_, err = svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "6.6.6.6"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	require.Equal(t, 1, repo.lockCalls, "仅用户名维度达阈值触发 LockUser")
	u, err := repo.FindUserByID(ctx, 1)
	require.NoError(t, err)
	require.False(t, u.LockedUntil.IsZero())
}

func TestLoginGuardFailOpenOnRedisError(t *testing.T) {
	svc, repo, rdb := newServiceFixture(t)
	ctx := context.Background()
	repo.seedUser("bob", testPassword, nil)
	rdb.errIncr = true // 模拟 Redis 故障：计数写入失败

	for i := 0; i < 6; i++ {
		_, err := svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "5.5.5.5"})
		require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	}
	require.Equal(t, 0, repo.lockCalls, "Redis 故障时计数缺位 → 不锁定（fail-open，与现状一致）")
	rdb.errIncr = false
	res, err := svc.Login(ctx, LoginInput{Username: "bob", Password: testPassword, IP: "5.5.5.5"})
	require.NoError(t, err)
	require.NotEmpty(t, res.AccessToken)
}

// ---- S9：用户目录收敛 ----

func TestGetUsersScopeFiltering(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	dA := repo.seedDept("D-A", StatusEnabled, 0)
	dB := repo.seedDept("D-B", StatusEnabled, 0)
	mutA := func(u *User) { id := database.ID(dA); u.DepartmentID = &id }
	mutB := func(u *User) { id := database.ID(dB); u.DepartmentID = &id }

	ua1 := repo.seedUser("dept-a-1", testPassword, mutA)
	repo.seedUser("dept-a-2", testPassword, mutA)
	repo.seedUser("dept-b-1", testPassword, mutB)
	op := repo.seedUser("dir-op", testPassword, mutA)
	require.NoError(t, repo.UpdateUserCols(ctx, nil, ua1, map[string]any{"last_login_ip": "10.0.0.2"}))
	require.NoError(t, repo.UpdateUserCols(ctx, nil, op, map[string]any{"last_login_ip": "10.0.0.1"}))

	// DEPARTMENT 范围：仅本部门（含本人），且覆盖前端传入的 department_id 参数。
	dActor := scopeActor(op, "dir-op", DataScopeDepartment, dA)
	items, total, err := svc.GetUsers(ctx, dActor, UserListFilter{DepartmentID: dB})
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	got := map[string]bool{}
	for _, it := range items {
		got[it.Username] = true
	}
	require.True(t, got["dept-a-1"] && got["dept-a-2"] && got["dir-op"])
	require.False(t, got["dept-b-1"], "越部门用户不可见")

	// SELF 范围：仅本人。
	sActor := scopeActor(op, "dir-op", DataScopeSelf, dA)
	items, total, err = svc.GetUsers(ctx, sActor, UserListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "dir-op", items[0].Username)

	// ALL 范围：全量。
	aActor := scopeActor(op, "dir-op", DataScopeAll, dA)
	_, total, err = svc.GetUsers(ctx, aActor, UserListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 4, total)

	// 列表响应不返回 last_login_ip（S9），即使本人也不回显。
	sItems, _, err := svc.GetUsers(ctx, sActor, UserListFilter{})
	require.NoError(t, err)
	raw, err := json.Marshal(sItems[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "last_login_ip")
}

func TestGetUserDetailScopeAndIP(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	dA := repo.seedDept("D-A", StatusEnabled, 0)
	dB := repo.seedDept("D-B", StatusEnabled, 0)
	mutA := func(u *User) { id := database.ID(dA); u.DepartmentID = &id }
	mutB := func(u *User) { id := database.ID(dB); u.DepartmentID = &id }

	ua1 := repo.seedUser("dept-a-1", testPassword, mutA)
	require.NoError(t, repo.UpdateUserCols(ctx, nil, ua1, map[string]any{"last_login_ip": "10.0.0.2"}))
	ub := repo.seedUser("dept-b-1", testPassword, mutB)
	op := repo.seedUser("dir-op", testPassword, mutA)

	// DEPARTMENT 操作者：本部门可见，详情保留 last_login_ip（对持有 auth:user:read 的操作者）。
	dActor := scopeActor(op, "dir-op", DataScopeDepartment, dA)
	view, err := svc.GetUserDetail(ctx, dActor, ua1)
	require.NoError(t, err)
	require.Equal(t, "10.0.0.2", view.LastLoginIP)

	// 跨部门目标 → 404（与不存在同形，不确认存在性）。
	_, err = svc.GetUserDetail(ctx, dActor, ub)
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))

	// SELF 操作者：他人一律 404，仅本人可见。
	sActor := scopeActor(op, "dir-op", DataScopeSelf, dA)
	_, err = svc.GetUserDetail(ctx, sActor, ua1)
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))
	self, err := svc.GetUserDetail(ctx, sActor, op)
	require.NoError(t, err)
	require.Equal(t, "dir-op", self.Username)

	// super_admin 不受限。
	view, err = svc.GetUserDetail(ctx, testActor(9, "root", true), ub)
	require.NoError(t, err)
	require.Equal(t, "dept-b-1", view.Username)
}

// ---- S10：UpdateUser 特权变更后强制下线 ----

func TestUpdateUserKicksSessionsOnPrivilegeChange(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	uid := repo.seedUser("worker", testPassword, nil)
	admin := testActor(9, "admin", true)

	res, err := svc.Login(ctx, LoginInput{Username: "worker", Password: testPassword, IP: "2.2.2.2"})
	require.NoError(t, err)
	cfg, store, _, _, _, _, _ := snapshotWired()
	sid := mustSID(t, cfg, res.AccessToken)

	// 仅改姓名 → 会话保留。
	_, err = svc.UpdateUser(ctx, admin, uid, UserUpdateInput{RealName: strPtr("张三")})
	require.NoError(t, err)
	sess, err := store.Get(ctx, sid)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// data_scope 变更 → 会话全部下线（plan §13.3 漂移窗口销项）。
	_, err = svc.UpdateUser(ctx, admin, uid, UserUpdateInput{DataScope: strPtr(DataScopeSelf)})
	require.NoError(t, err)
	sess, err = store.Get(ctx, sid)
	require.NoError(t, err)
	require.Nil(t, sess)

	// 角色变更 → 同样下线。
	res2, err := svc.Login(ctx, LoginInput{Username: "worker", Password: testPassword, IP: "2.2.2.2"})
	require.NoError(t, err)
	sid2 := mustSID(t, cfg, res2.AccessToken)
	role := repo.seedRole("staff", StatusEnabled, false)
	_, err = svc.UpdateUser(ctx, admin, uid, UserUpdateInput{RoleIDs: []int64{role}})
	require.NoError(t, err)
	sess2, err := store.Get(ctx, sid2)
	require.NoError(t, err)
	require.Nil(t, sess2)
}

// TestAssignRolesKicksSessions S10 复审补修：专用"绑定角色"端点（PUT /api/users/:id/roles）
// 变更角色后同样强制下线——否则被收回 super_admin 角色的目标在途会话仍持 IsSuper=true
// 快照直通 RequirePermission 豁免，最长 2h。
func TestAssignRolesKicksSessions(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	superRole := repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)
	root := repo.seedUser("root", testPassword, nil)
	repo.bindRole(root, superRole)
	worker := repo.seedUser("worker", testPassword, nil)
	admin := testActor(9, "admin", true)

	res, err := svc.Login(ctx, LoginInput{Username: "worker", Password: testPassword, IP: "2.2.2.2"})
	require.NoError(t, err)
	cfg, store, _, _, _, _, _ := snapshotWired()
	sid := mustSID(t, cfg, res.AccessToken)

	// 经专用端点替换 worker 角色 → 会话全部下线（与 UpdateUser 写路径同一处理）。
	require.NoError(t, svc.AssignRoles(ctx, admin, worker, AssignRolesInput{RoleIDs: []int64{}}))
	sess, err := store.Get(ctx, sid)
	require.NoError(t, err)
	require.Nil(t, sess, "角色变更后目标会话必须全部下线")

	// 收回 super_admin 角色后，目标重登不再持有 IsSuper=true 快照（RequirePermission 豁免随之失效）。
	res2, err := svc.Login(ctx, LoginInput{Username: "root", Password: testPassword, IP: "2.2.2.2"})
	require.NoError(t, err)
	require.NoError(t, svc.AssignRoles(ctx, admin, root, AssignRolesInput{RoleIDs: []int64{}}))
	res3, err := svc.Login(ctx, LoginInput{Username: "root", Password: testPassword, IP: "2.2.2.2"})
	require.NoError(t, err)
	sess3, err := store.Get(ctx, mustSID(t, cfg, res3.AccessToken))
	require.NoError(t, err)
	require.NotNil(t, sess3)
	require.False(t, sess3.IsSuper, "收回 super_admin 角色后重登快照不再是超管")
	_ = res2
}

// TestWritePathScopeGuard S9 复审补修（读写对称）：目录写路径（UpdateUser/UpdateUserStatus/
// ResetUserPassword/AssignRoles/UnlockUser）与读路径同一可见性判定——范围外目标一律 404。
func TestWritePathScopeGuard(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	dept := repo.seedDept("D-A", StatusEnabled, 0)
	other := repo.seedDept("D-B", StatusEnabled, 0)
	mutA := func(u *User) { id := database.ID(dept); u.DepartmentID = &id }
	mutB := func(u *User) { id := database.ID(other); u.DepartmentID = &id }
	cross := repo.seedUser("cross", testPassword, mutB) // 范围外目标
	peer := repo.seedUser("peer", testPassword, mutA)   // 同部门目标
	op := repo.seedUser("dir-op", testPassword, mutA)
	actor := scopeActor(op, "dir-op", DataScopeDepartment, dept)

	// 范围外目标：五个写路径一律 AUTH_USER_NOT_FOUND（与详情同形，不确认存在性）。
	_, err := svc.UpdateUser(ctx, actor, cross, UserUpdateInput{RealName: strPtr("改")})
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))
	err = svc.UpdateUserStatus(ctx, actor, cross, UserStatusDisabled)
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))
	err = svc.ResetUserPassword(ctx, actor, cross, ResetPasswordInput{NewPassword: "NewPassw0rd"})
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))
	err = svc.AssignRoles(ctx, actor, cross, AssignRolesInput{RoleIDs: []int64{}})
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))
	err = svc.UnlockUser(ctx, actor, cross)
	require.Equal(t, "AUTH_USER_NOT_FOUND", codeOf(t, err))

	// 同部门目标与本人：写路径放行。
	_, err = svc.UpdateUser(ctx, actor, peer, UserUpdateInput{RealName: strPtr("乙")})
	require.NoError(t, err)
	require.NoError(t, svc.UnlockUser(ctx, actor, peer))
	_, err = svc.UpdateUser(ctx, actor, op, UserUpdateInput{RealName: strPtr("本人")})
	require.NoError(t, err)
}

// ---- S15：改密原密码失败计数与短期锁定 ----

func TestChangePasswordOldPasswordGuard(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	repo.seedUser("carl", testPassword, nil)
	resetSQLCapture()

	res, err := svc.Login(ctx, LoginInput{Username: "carl", Password: testPassword, IP: "3.3.3.3"})
	require.NoError(t, err)
	cfg, store, _, _, _, _, _ := snapshotWired()
	sess, err := store.Get(ctx, mustSID(t, cfg, res.AccessToken))
	require.NoError(t, err)
	actor := testActor(1, "carl", false)
	actor.IP = "3.3.3.3"

	// 连续 5 次原密码错误 → 计数并写 login_logs（键维度 username|ip）。
	for i := 0; i < 5; i++ {
		err = svc.ChangePassword(ctx, sess, actor, "wrongOld1", "NewPassw0rd")
		require.Equal(t, "AUTH_PASSWORD_MISMATCH", codeOf(t, err))
	}
	require.True(t, sqlCaptured("login_logs", "AUTH_PASSWORD_MISMATCH"), "S15：失败写 login_logs")

	// 达到阈值：即使原密码正确也拒绝（短期锁定，独立于登录保护）。
	err = svc.ChangePassword(ctx, sess, actor, testPassword, "NewPassw0rd")
	require.Equal(t, "AUTH_ACCOUNT_LOCKED", codeOf(t, err))

	// 换 IP 不受影响；成功改密后该维度计数清零。
	actor2 := testActor(1, "carl", false)
	actor2.IP = "4.4.4.4"
	require.NoError(t, svc.ChangePassword(ctx, sess, actor2, testPassword, "NewPassw0rd1"))
	err = svc.ChangePassword(ctx, sess, actor2, "wrongOld1", "NewPassw0rd2")
	require.Equal(t, "AUTH_PASSWORD_MISMATCH", codeOf(t, err), "成功改密后计数已清零，不立即锁定")
}

// ---- S7：login_logs 写失败记 zap error ----

// failingDBRepo 仅让 DB() 返回 nil：WriteLoginLog 收到 nil 句柄即失败（ErrAuditDBNil），
// 用于验证 S7 的兜底日志；其余数据访问沿用 fakeRepo 内存替身。
type failingDBRepo struct{ *fakeRepo }

func (f *failingDBRepo) DB() *gorm.DB { return nil }

func TestLoginLogWriteFailureIsLogged(t *testing.T) {
	rdb := newFakeRedis()
	svc := wireTest(t, rdb, &failingDBRepo{fakeRepo: newFakeRepo()})

	core, recorded := observer.New(zapcore.InfoLevel)
	SetLogger(zap.New(core))
	t.Cleanup(func() { SetLogger(nil) })

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "ghost", Password: "whatever1", IP: "x", RequestID: "req-42",
	})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err)) // 认证结论不因日志缺位改变

	entries := recorded.FilterMessageSnippet("login_logs 写入失败").All()
	require.Len(t, entries, 1, "S7：login_logs 写失败必须记 zap error")
	fields := entries[0].ContextMap()
	require.Equal(t, "ghost", fields["username"])
	require.Equal(t, "req-42", fields["request_id"])
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", fields["fail_reason"])
	require.NotEmpty(t, fields["error"])
}
