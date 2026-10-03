package auth

// 单元测试：登录全流程、登录保护锁定、刷新轮换、改密、踢会话、RBAC 管理校验
// （ask 指定交付项：登录保护计数；全部经接口替身，不依赖 PostgreSQL/Redis）。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/database"
)

const testPassword = "Passw0rd"

func newServiceFixture(t *testing.T) (*Service, *fakeRepo, *fakeRedis) {
	t.Helper()
	rdb := newFakeRedis()
	repo := newFakeRepo()
	svc := wireTest(t, rdb, repo)
	return svc, repo, rdb
}

// ---- 登录 ----

func TestLoginSuccess(t *testing.T) {
	svc, repo, rdb := newServiceFixture(t)
	uid := repo.seedUser("alice", testPassword, nil)

	res, err := svc.Login(context.Background(), LoginInput{Username: "alice", Password: testPassword, IP: "1.1.1.1", UserAgent: "ua"})
	require.NoError(t, err)
	require.Equal(t, "Bearer", res.TokenType)
	require.NotEmpty(t, res.AccessToken)
	require.NotEmpty(t, res.RefreshToken)
	require.False(t, res.MustChangePassword)
	require.EqualValues(t, uid, res.User.ID.Int64())
	require.Positive(t, res.ExpiresIn)

	// 会话已登记（plan §7.2 双轨）。
	cfg, store, _, _, _, _, _ := snapshotWired()
	sess, err := store.Get(context.Background(), mustSID(t, cfg, res.AccessToken))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, "alice", sess.Username)
	require.Equal(t, "1.1.1.1", sess.IP)

	// 登录成功后失败计数清零（permission.md §3.2）。
	n, err := svc.guard.Count(context.Background(), "alice")
	require.NoError(t, err)
	require.Zero(t, n)

	// last_login 更新（plan §7.3）。
	u, err := repo.FindUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.False(t, u.LastLoginAt.IsZero())
	require.Equal(t, "1.1.1.1", u.LastLoginIP)
	require.True(t, rdb.has(sessionKey(sess.SID)))
}

func mustSID(t *testing.T, cfg runtimeConfig, token string) string {
	t.Helper()
	_, sid, err := parseAccessToken(cfg, token)
	require.NoError(t, err)
	return sid
}

func TestLoginUnknownUserUniformError(t *testing.T) {
	svc, _, _ := newServiceFixture(t)
	_, err := svc.Login(context.Background(), LoginInput{Username: "ghost", Password: "whatever1", IP: "x", UserAgent: "ua"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err)) // 防枚举：统一文案（plan §7.3）
}

func TestLoginWrongPasswordCountsAndLocks(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("bob", testPassword, nil)
	ctx := context.Background()

	// 连续错 4 次：仅累计，不锁定。
	for i := 0; i < 4; i++ {
		_, err := svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "x", UserAgent: "ua"})
		require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	}
	u, err := repo.FindUserByID(ctx, 1)
	require.NoError(t, err)
	require.True(t, u.LockedUntil.IsZero(), "未达阈值不锁定")
	require.Equal(t, 0, repo.lockCalls, "LockUser 未被调用（计数断言）")

	// 第 5 次（达到默认阈值 maxLoginFailures=5）：锁定落库（plan §7.3 进程重启不丢锁）。
	_, err = svc.Login(ctx, LoginInput{Username: "bob", Password: "wrong1", IP: "x", UserAgent: "ua"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
	require.Equal(t, 1, repo.lockCalls, "仅达阈值的那次触发 LockUser")
	u, err = repo.FindUserByID(ctx, 1)
	require.NoError(t, err)
	require.False(t, u.LockedUntil.IsZero())
	require.WithinDuration(t, time.Now().Add(svc.cfg.lockDuration), u.LockedUntil.Time, time.Minute)

	// 锁定窗口内（即使密码正确）→ S8 统一防枚举文案（不再回显 AUTH_ACCOUNT_LOCKED/locked_until）；
	// 且 S4 IP 维度已达阈值，同样被入口预检拒绝。
	_, err = svc.Login(ctx, LoginInput{Username: "bob", Password: testPassword, IP: "x", UserAgent: "ua"})
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
}

func TestLoginLockExpiryAllowsRetry(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	// 锁定已过期（locked_until 为过去时刻）→ 允许再次尝试。
	repo.seedUser("carl", testPassword, func(u *User) {
		u.LockedUntil = database.JSONTime{Time: time.Now().Add(-time.Minute)}
	})
	res, err := svc.Login(context.Background(), LoginInput{Username: "carl", Password: testPassword})
	require.NoError(t, err)
	require.NotEmpty(t, res.AccessToken)
}

func TestLoginDisabledUser(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("dan", testPassword, func(u *User) { u.Status = UserStatusDisabled })
	_, err := svc.Login(context.Background(), LoginInput{Username: "dan", Password: testPassword})
	// S8：停用账户对客户端与凭证错误统一文案（防枚举预言机），真实原因仅入 login_logs。
	require.Equal(t, "AUTH_CREDENTIALS_INVALID", codeOf(t, err))
}

func TestLoginBadInput(t *testing.T) {
	svc, _, _ := newServiceFixture(t)
	_, err := svc.Login(context.Background(), LoginInput{Username: "", Password: "x1"})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	_, err = svc.Login(context.Background(), LoginInput{Username: "alice", Password: ""})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	_, err = svc.Login(context.Background(), LoginInput{Username: "abcdefghijklmnopqrstuvwxyz0123456789012345678901234567890123456789012345678", Password: "x1"})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
}

// ---- 刷新 ----

func TestRefreshRotatesSession(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("erin", testPassword, func(u *User) { u.DataScope = DataScopeSpecifiedWh })
	uid := int64(1)
	repo.bindWh(uid, 11, 22)

	res, err := svc.Login(context.Background(), LoginInput{Username: "erin", Password: testPassword})
	require.NoError(t, err)

	fresh, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: res.RefreshToken, IP: "2.2.2.2", UserAgent: "ua"})
	require.NoError(t, err)
	require.NotEqual(t, res.AccessToken, fresh.AccessToken)
	require.NotEqual(t, res.RefreshToken, fresh.RefreshToken)
	require.Equal(t, DataScopeSpecifiedWh, fresh.User.DataScope)

	// 旧刷新凭证已作废（轮换，plan §7.2）。
	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: res.RefreshToken})
	require.Equal(t, "AUTH_REFRESH_INVALID", codeOf(t, err))

	// 新会话可用（uid/sid 校验通过）。
	cfg, store, _, _, _, _, _ := snapshotWired()
	sess, err := store.Get(context.Background(), mustSID(t, cfg, fresh.AccessToken))
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, []int64{11, 22}, sess.WarehouseIDs)
}

func TestRefreshInvalidAndDisabled(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("finn", testPassword, nil)
	res, err := svc.Login(context.Background(), LoginInput{Username: "finn", Password: testPassword})
	require.NoError(t, err)

	// 空凭证 / 垃圾凭证。
	_, err = svc.Refresh(context.Background(), RefreshInput{})
	require.Equal(t, "AUTH_REFRESH_INVALID", codeOf(t, err))
	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: "garbage"})
	require.Equal(t, "AUTH_REFRESH_INVALID", codeOf(t, err))

	// 停用后旧凭证立即失效（fail-closed）。
	require.NoError(t, repo.UpdateUserCols(context.Background(), nil, 1, map[string]any{"status": UserStatusDisabled}))
	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: res.RefreshToken})
	require.Equal(t, "AUTH_ACCOUNT_DISABLED", codeOf(t, err))

	cfg, store, _, _, _, _, _ := snapshotWired()
	sess, err := store.Get(context.Background(), mustSID(t, cfg, res.AccessToken))
	require.NoError(t, err)
	require.Nil(t, sess, "停用用户的会话必须被撤销")
}

// ---- 改密与踢会话 ----

func TestChangePasswordLifecycle(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("gina", testPassword, func(u *User) { u.MustChangePassword = true })

	res, err := svc.Login(context.Background(), LoginInput{Username: "gina", Password: testPassword})
	require.NoError(t, err)
	cfg, store, _, _, _, _, _ := snapshotWired()
	sid := mustSID(t, cfg, res.AccessToken)
	sess, err := store.Get(context.Background(), sid)
	require.NoError(t, err)
	require.True(t, sess.MustChangePassword)

	// 原密码错误。
	err = svc.ChangePassword(context.Background(), sess, testActor(1, "gina", false), "wrongOld1", "NewPassw0rd")
	require.Equal(t, "AUTH_PASSWORD_MISMATCH", codeOf(t, err))

	// 新密码不满足策略。
	err = svc.ChangePassword(context.Background(), sess, testActor(1, "gina", false), testPassword, "simple")
	require.Equal(t, "AUTH_PASSWORD_WEAK", codeOf(t, err))

	// 成功：旧密码失效、标记复位（database.md §8.1 强制改密闭环）。
	err = svc.ChangePassword(context.Background(), sess, testActor(1, "gina", false), testPassword, "NewPassw0rd")
	require.NoError(t, err)

	u, err := repo.FindUserByID(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, u.MustChangePassword)
	require.True(t, VerifyPassword(u.PasswordHash, "NewPassw0rd"))
	require.False(t, VerifyPassword(u.PasswordHash, testPassword))

	updated, err := store.Get(context.Background(), sid)
	require.NoError(t, err)
	require.False(t, updated.MustChangePassword, "当前会话复位标记，无需重登")
}

func TestChangePasswordKicksOtherSessions(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("hank", testPassword, nil)

	first, err := svc.Login(context.Background(), LoginInput{Username: "hank", Password: testPassword})
	require.NoError(t, err)
	second, err := svc.Login(context.Background(), LoginInput{Username: "hank", Password: testPassword})
	require.NoError(t, err)

	cfg, store, _, _, _, _, _ := snapshotWired()
	sess, err := store.Get(context.Background(), mustSID(t, cfg, first.AccessToken))
	require.NoError(t, err)

	require.NoError(t, svc.ChangePassword(context.Background(), sess, testActor(1, "hank", false), testPassword, "NewPassw0rd"))

	// 其余会话被强制下线，改密所用会话保留。
	gone, err := store.Get(context.Background(), mustSID(t, cfg, second.AccessToken))
	require.NoError(t, err)
	require.Nil(t, gone)
	kept, err := store.Get(context.Background(), mustSID(t, cfg, first.AccessToken))
	require.NoError(t, err)
	require.NotNil(t, kept)
}

func TestKickSession(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	repo.seedUser("iris", testPassword, nil)
	res, err := svc.Login(context.Background(), LoginInput{Username: "iris", Password: testPassword})
	require.NoError(t, err)

	cfg, store, _, _, _, _, _ := snapshotWired()
	sid := mustSID(t, cfg, res.AccessToken)

	require.NoError(t, svc.KickSession(context.Background(), testActor(9, "admin", true), sid))
	gone, err := store.Get(context.Background(), sid)
	require.NoError(t, err)
	require.Nil(t, gone) // 被踢 → AuthRequired 立即 401（plan §7.2）

	// 重复踢 → 会话不存在（404）。
	err = svc.KickSession(context.Background(), testActor(9, "admin", true), sid)
	require.Equal(t, "AUTH_SESSION_NOT_FOUND", codeOf(t, err))
}

// ---- 用户管理 ----

func TestCreateUserValidationAndSuccess(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	role := repo.seedRole("staff", StatusEnabled, false)
	dept := repo.seedDept("D-HQ", StatusEnabled, 0)
	ctx := context.Background()
	actor := testActor(9, "admin", false)

	// 校验矩阵（api.md §4 后端完整校验）。
	_, err := svc.CreateUser(ctx, actor, UserCreateInput{Username: "1bad", Password: testPassword})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: "short"})
	require.Equal(t, "AUTH_PASSWORD_WEAK", codeOf(t, err))
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: testPassword, DataScope: "OTHER"})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	badDept := int64(999)
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: testPassword, DepartmentID: &badDept})
	require.Equal(t, "AUTH_DEPT_NOT_FOUND", codeOf(t, err))
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: testPassword, RoleIDs: []int64{999}})
	require.Equal(t, "AUTH_ROLE_NOT_FOUND", codeOf(t, err))

	// 指定仓库范围必须绑定仓库。
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: testPassword, DataScope: DataScopeSpecifiedWh})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))

	// 成功：缺省 data_scope=SELF（最小授权）、绑定角色/部门、密码可验证。
	view, err := svc.CreateUser(ctx, actor, UserCreateInput{
		Username: "ok.name", Password: testPassword, RealName: "甲", Email: "a@b.co",
		DepartmentID: &dept, DataScope: DataScopeSelf, RoleIDs: []int64{role},
	})
	require.NoError(t, err)
	require.Positive(t, view.ID.Int64())
	require.Equal(t, DataScopeSelf, view.DataScope)
	u, err := repo.FindUserByID(ctx, view.ID.Int64())
	require.NoError(t, err)
	require.True(t, VerifyPassword(u.PasswordHash, testPassword))
	// S14：创建用户与 bootstrap 管理员/重置密码路径一致——初始凭证必须轮换。
	require.True(t, u.MustChangePassword, "管理员创建的用户首登强制改密")

	// 重复用户名（唯一性校验）。
	_, err = svc.CreateUser(ctx, actor, UserCreateInput{Username: "ok.name", Password: testPassword})
	require.Equal(t, "AUTH_USERNAME_EXISTS", codeOf(t, err))
}

func TestCreateUserWarehouseChecker(t *testing.T) {
	svc, _, _ := newServiceFixture(t)
	ctx := context.Background()
	actor := testActor(9, "admin", false)

	// 校验器未注入 → fail-closed 500（plan §4.3：杜绝静默跳过业务校验）。
	_, err := svc.CreateUser(ctx, actor, UserCreateInput{
		Username: "wh.user", Password: testPassword, WarehouseIDs: []int64{1},
	})
	require.Equal(t, "AUTH_WAREHOUSE_CHECKER_MISSING", codeOf(t, err))

	// 注入替身校验器：仓库 7 存在。
	// （S1 授予侧边界后，SPECIFIED_WAREHOUSE 授予以 super_admin 操作者执行——
	// 非特权操作者授予 WH 范围/仓库绑定被拒的专项用例见 service_security_test.go。）
	svc.useChecker(fakeWhChecker{ok: map[int64]bool{7: true}})
	_, err = svc.CreateUser(ctx, testActor(9, "admin", true), UserCreateInput{
		Username: "wh.user", Password: testPassword, DataScope: DataScopeSpecifiedWh, WarehouseIDs: []int64{7},
	})
	require.NoError(t, err)
	whs, werr := svc.repo.ListWarehouseIDsByUser(ctx, 1)
	require.NoError(t, werr)
	require.Equal(t, []int64{7}, whs)

	// 仓库不存在 → 业务错误。
	_, err = svc.CreateUser(ctx, testActor(9, "admin", true), UserCreateInput{
		Username: "wh.user2", Password: testPassword, WarehouseIDs: []int64{99},
	})
	require.Equal(t, "AUTH_WAREHOUSE_INVALID", codeOf(t, err))
}

type fakeWhChecker struct{ ok map[int64]bool }

func (f fakeWhChecker) ExistsActive(_ context.Context, warehouseID int64) (bool, error) {
	return f.ok[warehouseID], nil
}

func TestUserStatusGuards(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	uid := repo.seedUser("ivan", testPassword, nil)
	ctx := context.Background()
	// S9 读写对称后，目录写操作的主路径以 super_admin 操作者执行；
	// 非特权视角的可见性专项用例见 service_security_test.go TestWritePathScopeGuard。
	actor := testActor(9, "admin", true)

	// 非法状态值。
	err := svc.UpdateUserStatus(ctx, actor, uid, "PAUSED")
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))

	// 停用 → 会话全部下线（离岗路径，plan §5.4.1）。
	res, err := svc.Login(ctx, LoginInput{Username: "ivan", Password: testPassword})
	require.NoError(t, err)
	cfg, store, _, _, _, _, _ := snapshotWired()
	require.NoError(t, svc.UpdateUserStatus(ctx, actor, uid, UserStatusDisabled))
	sess, err := store.Get(ctx, mustSID(t, cfg, res.AccessToken))
	require.NoError(t, err)
	require.Nil(t, sess)

	// 自停用保护。
	err = svc.UpdateUserStatus(ctx, testActor(uid, "ivan", false), uid, UserStatusDisabled)
	require.Equal(t, "AUTH_SELF_OPERATION_FORBIDDEN", codeOf(t, err))
}

func TestSuperUserProtected(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	// 超管与普通管理员各一（uid 以 seed 返回为准）。
	rootRole := repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)
	uidRoot := repo.seedUser("root", testPassword, nil)
	repo.bindRole(uidRoot, rootRole)
	adminRole := repo.seedRole("admin2", StatusEnabled, false)
	uidAdmin := repo.seedUser("admin2", testPassword, nil)
	repo.bindRole(uidAdmin, adminRole)

	ctx := context.Background()
	// 普通管理员动超管 → 拒绝（数据级保护）。
	err := svc.UpdateUserStatus(ctx, testActor(uidAdmin, "admin2", false), uidRoot, UserStatusDisabled)
	require.Equal(t, "AUTH_SUPER_USER_PROTECTED", codeOf(t, err))
	err = svc.ResetUserPassword(ctx, testActor(uidAdmin, "admin2", false), uidRoot, ResetPasswordInput{NewPassword: "NewPassw0rd"})
	require.Equal(t, "AUTH_SUPER_USER_PROTECTED", codeOf(t, err))
	err = svc.AssignRoles(ctx, testActor(uidAdmin, "admin2", false), uidRoot, AssignRolesInput{RoleIDs: []int64{adminRole}})
	require.Equal(t, "AUTH_SUPER_USER_PROTECTED", codeOf(t, err))

	// 超管动超管 → 允许。
	err = svc.UnlockUser(ctx, testActor(uidRoot, "root", true), uidRoot)
	require.NoError(t, err)
}

func TestUnlockUser(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	uid := repo.seedUser("leon", testPassword, func(u *User) {
		u.LockedUntil = database.JSONTime{Time: time.Now().Add(10 * time.Minute)}
	})
	_, _ = svc.guard.Fail(context.Background(), "leon")
	_, _ = svc.guard.Fail(context.Background(), "leon")

	require.NoError(t, svc.UnlockUser(context.Background(), testActor(9, "admin", true), uid))

	u, err := repo.FindUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.True(t, u.LockedUntil.IsZero()) // locked_until 重置（plan §7.3 提前解锁）
	n, err := svc.guard.Count(context.Background(), "leon")
	require.NoError(t, err)
	require.Zero(t, n) // 失败计数清空
}

// ---- 角色与部门 ----

func TestRoleLifecycle(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	actor := testActor(9, "admin", false)

	// 编码非法 / 重复。
	_, err := svc.CreateRole(ctx, actor, RoleCreateInput{Code: "BAD", Name: "x"})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))
	_, err = svc.CreateRole(ctx, actor, RoleCreateInput{Code: "alpha", Name: "A"})
	require.NoError(t, err)
	_, err = svc.CreateRole(ctx, actor, RoleCreateInput{Code: "alpha", Name: "B"})
	require.Equal(t, "AUTH_ROLE_CODE_EXISTS", codeOf(t, err))

	// 内置角色禁停用。
	sys := repo.seedRole(SuperAdminRoleCode, StatusEnabled, true)
	err = svc.UpdateRoleStatus(ctx, actor, sys, StatusDisabled)
	require.Equal(t, "AUTH_ROLE_SYSTEM_LOCKED", codeOf(t, err))

	// 权限绑定：非法权限点拒绝；合法绑定生效并失效目标用户缓存。
	// （S1 授予侧边界后，绑定操作以 super_admin 操作者执行——非超管受"自身权限点"约束，另有专项用例。）
	perm := repo.seedPerm(PermUserList, StatusEnabled)
	err = svc.AssignPermissions(ctx, actor, 1, AssignPermissionsInput{PermissionIDs: []int64{999}})
	require.Equal(t, "AUTH_PERMISSION_INVALID", codeOf(t, err))
	require.NoError(t, svc.AssignPermissions(ctx, testActor(9, "admin", true), 1, AssignPermissionsInput{PermissionIDs: []int64{perm}}))
	ids, err := repo.ListPermissionIDsByRole(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{perm}, ids)

	detail, err := svc.GetRoleDetail(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []database.ID{database.ID(perm)}, detail.PermissionIDs)
}

func TestDeptLifecycle(t *testing.T) {
	svc, repo, _ := newServiceFixture(t)
	ctx := context.Background()
	actor := testActor(9, "admin", false)

	parent := repo.seedDept("D-PARENT", StatusEnabled, 0)

	// 重复编码。
	_, err := svc.CreateDept(ctx, actor, DeptCreateInput{Code: "D-PARENT", Name: "x"})
	require.Equal(t, "AUTH_DEPT_CODE_EXISTS", codeOf(t, err))

	// 子部门 + 用户（用户直接挂在父部门）。
	child := repo.seedDept("D-CHILD", StatusEnabled, parent)
	repo.seedUser("emp", testPassword, func(u *User) {
		id := database.ID(parent)
		u.DepartmentID = &id
	})

	// 存在启用中的子部门 → 禁止停用（级联校验，business-flow §1.6 精神）。
	err = svc.UpdateDeptStatus(ctx, actor, parent, StatusDisabled)
	require.Equal(t, "AUTH_DEPT_HAS_ENABLED_CHILDREN", codeOf(t, err))

	// 成环检查：父级不能挂到自己的子孙（child 的父链上有 parent 自身）。
	_, err = svc.UpdateDept(ctx, actor, parent, DeptUpdateInput{Name: "总部", ParentID: &child})
	require.Equal(t, "COMMON_INVALID_PARAM", codeOf(t, err))

	// 停用子部门后，父部门下仍有启用用户 → 禁止停用。
	require.NoError(t, svc.UpdateDeptStatus(ctx, actor, child, StatusDisabled))
	err = svc.UpdateDeptStatus(ctx, actor, parent, StatusDisabled)
	require.Equal(t, "AUTH_DEPT_HAS_ACTIVE_USERS", codeOf(t, err))

	// 正常更新。
	view, err := svc.UpdateDept(ctx, actor, parent, DeptUpdateInput{Name: "总部"})
	require.NoError(t, err)
	require.Equal(t, "总部", view.Name)
}
