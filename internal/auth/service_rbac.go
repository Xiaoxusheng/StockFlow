package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// RBAC 管理服务：/api/users、/api/roles、/api/permissions、/api/departments
// （backend-m1-plan §5.4；敏感操作审计清单见 §4.4——用户创建/更新/启停/重置密码/解锁/
// 绑定角色、角色变更与权限绑定、部门变更全部落 operation_logs）。

var (
	// usernameRe 用户名规则：2-64 位字母/数字/下划线/点/连字符，字母开头。
	usernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{1,63}$`)
	// roleCodeRe 角色编码：2-64 位小写字母/数字/下划线。
	roleCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	// deptCodeRe 部门编码：2-64 位大写字母/数字/下划线/连字符。
	deptCodeRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{1,63}$`)
	maxNameLen    = 64
	maxContactLen = 128
)

// validDataScope 数据权限范围枚举校验（permission.md §4 五范围）。
func validDataScope(v string) bool {
	switch v {
	case DataScopeAll, DataScopeSpecifiedWh, DataScopeDepartment, DataScopeSelf, DataScopeSelfInCharge:
		return true
	}
	return false
}

func validUserStatus(v string) bool  { return v == UserStatusActive || v == UserStatusDisabled }
func validOnOffStatus(v string) bool { return v == StatusEnabled || v == StatusDisabled }

// ---------------- 用户 ----------------

// UserCreateInput 创建用户入参。
type UserCreateInput struct {
	Username     string  `json:"username"`
	Password     string  `json:"password"`
	RealName     string  `json:"real_name"`
	Phone        string  `json:"phone"`
	Email        string  `json:"email"`
	DepartmentID *int64  `json:"department_id"`
	DataScope    string  `json:"data_scope"`
	RoleIDs      []int64 `json:"role_ids"`
	WarehouseIDs []int64 `json:"warehouse_ids"`
}

// UserUpdateInput 更新用户入参：指针/切片三态语义——nil 不修改，显式值/空切片为修改。
type UserUpdateInput struct {
	RealName     *string `json:"real_name"`
	Phone        *string `json:"phone"`
	Email        *string `json:"email"`
	DepartmentID *int64  `json:"department_id"`
	DataScope    *string `json:"data_scope"`
	RoleIDs      []int64 `json:"role_ids"`
	WarehouseIDs []int64 `json:"warehouse_ids"`
}

// CreateUser 创建用户（校验 + RBAC/仓库绑定 + 审计）。
func (s *Service) CreateUser(ctx context.Context, actor Actor, in UserCreateInput) (*UserView, error) {
	in.Username = strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(in.Username) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "username", "reason": "2-64 位，字母开头，可含数字/下划线/点/连字符",
		})
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	if err := validateContact(in.RealName, in.Phone, in.Email); err != nil {
		return nil, err
	}
	scope := in.DataScope
	if scope == "" {
		scope = DataScopeSelf // 缺省最小授权（deny-by-default）
	}
	if !validDataScope(scope) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "data_scope", "reason": "非法范围"})
	}
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		dept, err := s.repo.FindDeptByID(ctx, *in.DepartmentID)
		if err != nil {
			return nil, err
		}
		if dept == nil || dept.Status != StatusEnabled {
			return nil, response.NewError(ErrDeptNotFound, map[string]any{"department_id": *in.DepartmentID})
		}
	}
	if err := s.assertRolesUsable(ctx, in.RoleIDs); err != nil {
		return nil, err
	}
	if err := s.assertWarehousesUsable(ctx, in.WarehouseIDs); err != nil {
		return nil, err
	}
	if scope == DataScopeSpecifiedWh && len(in.WarehouseIDs) == 0 {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "warehouse_ids", "reason": "数据范围为指定仓库时必须绑定至少一个仓库",
		})
	}

	// 授予侧特权边界（安全审查 S1）：CreateUser 没有目标用户、不走 assertUserMutable，
	// 边界落在"操作者 → 被授角色/权限/数据范围"上——非超管不得授予 super_admin 角色、
	// 不得授予自身不持有的权限点（经角色携带）、不得授予超出自身 data_scope 的范围。
	if err := s.assertGrantableRoles(ctx, actor, in.RoleIDs); err != nil {
		return nil, err
	}
	deptID := int64(0)
	if in.DepartmentID != nil {
		deptID = *in.DepartmentID
	}
	if err := s.assertGrantableScope(actor, scope, deptID, in.WarehouseIDs); err != nil {
		return nil, err
	}

	if exist, err := s.repo.FindUserByUsername(ctx, in.Username); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrUsernameExists, nil)
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u := &User{
		Username:     in.Username,
		PasswordHash: hash,
		RealName:     in.RealName,
		Phone:        in.Phone,
		Email:        in.Email,
		DataScope:    scope,
		Status:       UserStatusActive,
		// S14：初始凭证必须轮换——与 bootstrap 管理员 / 重置密码路径一致（must_change_password=TRUE）。
		MustChangePassword: true,
	}
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		id := database.ID(*in.DepartmentID)
		u.DepartmentID = &id
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertUser(ctx, tx, u); err != nil {
			return err
		}
		if err := s.repo.InsertUserRoles(ctx, tx, u.ID.Int64(), in.RoleIDs, actor.UserID); err != nil {
			return err
		}
		if err := s.repo.InsertUserWarehouses(ctx, tx, u.ID.Int64(), in.WarehouseIDs, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("user", u.ID.Int64(), "create")
		e.Success = true
		e.After = viewUser(u) // 不含密码字段（PasswordHash json:"-"）
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewUser(u), nil
}

// UpdateUser 更新用户（角色/仓库绑定全量替换；快照失效）。
func (s *Service) UpdateUser(ctx context.Context, actor Actor, id int64, in UserUpdateInput) (*UserView, error) {
	target, err := s.assertUserMutable(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if in.RealName != nil || in.Phone != nil || in.Email != nil {
		realName := target.RealName
		if in.RealName != nil {
			realName = *in.RealName
		}
		phone := target.Phone
		if in.Phone != nil {
			phone = *in.Phone
		}
		email := target.Email
		if in.Email != nil {
			email = *in.Email
		}
		if err := validateContact(realName, phone, email); err != nil {
			return nil, err
		}
	}
	if in.DataScope != nil {
		if !validDataScope(*in.DataScope) {
			return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "data_scope", "reason": "非法范围"})
		}
		if *in.DataScope == DataScopeSpecifiedWh && in.WarehouseIDs != nil && len(in.WarehouseIDs) == 0 {
			return nil, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "warehouse_ids", "reason": "数据范围为指定仓库时必须绑定至少一个仓库",
			})
		}
	}
	if in.DepartmentID != nil && *in.DepartmentID > 0 {
		dept, err := s.repo.FindDeptByID(ctx, *in.DepartmentID)
		if err != nil {
			return nil, err
		}
		if dept == nil || dept.Status != StatusEnabled {
			return nil, response.NewError(ErrDeptNotFound, map[string]any{"department_id": *in.DepartmentID})
		}
	}
	if in.RoleIDs != nil {
		if err := s.assertRolesUsable(ctx, in.RoleIDs); err != nil {
			return nil, err
		}
		// S1：授予侧特权边界——非超管不得授予 super_admin / 超出自身权限点的角色。
		if err := s.assertGrantableRoles(ctx, actor, in.RoleIDs); err != nil {
			return nil, err
		}
	}
	if in.WarehouseIDs != nil {
		if err := s.assertWarehousesUsable(ctx, in.WarehouseIDs); err != nil {
			return nil, err
		}
	}
	// S1：授予侧数据范围/仓库绑定边界（按本次变更后的生效值判定）。
	if in.DataScope != nil || in.WarehouseIDs != nil {
		scope := target.DataScope
		if in.DataScope != nil {
			scope = *in.DataScope
		}
		deptID := int64(0)
		if target.DepartmentID != nil {
			deptID = target.DepartmentID.Int64()
		}
		if in.DepartmentID != nil {
			if *in.DepartmentID > 0 {
				deptID = *in.DepartmentID
			} else {
				deptID = 0 // 显式清空部门
			}
		}
		whIDs := in.WarehouseIDs
		if whIDs == nil {
			whIDs, err = s.repo.ListWarehouseIDsByUser(ctx, id)
			if err != nil {
				return nil, err
			}
		}
		if in.DataScope != nil {
			if err := s.assertGrantableScope(actor, scope, deptID, whIDs); err != nil {
				return nil, err
			}
		}
		if in.WarehouseIDs != nil {
			if err := s.assertGrantableWarehouses(actor, in.WarehouseIDs); err != nil {
				return nil, err
			}
		}
	}

	cols := map[string]any{"updated_by": database.ID(actor.UserID)}
	if in.RealName != nil {
		cols["real_name"] = strings.TrimSpace(*in.RealName)
	}
	if in.Phone != nil {
		cols["phone"] = strings.TrimSpace(*in.Phone)
	}
	if in.Email != nil {
		cols["email"] = strings.TrimSpace(*in.Email)
	}
	if in.DepartmentID != nil {
		if *in.DepartmentID > 0 {
			idv := database.ID(*in.DepartmentID)
			cols["department_id"] = idv
		} else {
			cols["department_id"] = nil // 显式清空部门
		}
	}
	if in.DataScope != nil {
		cols["data_scope"] = *in.DataScope
	}

	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, id, cols); err != nil {
			return err
		}
		if in.RoleIDs != nil {
			if err := s.repo.DeleteUserRoles(ctx, tx, id); err != nil {
				return err
			}
			if err := s.repo.InsertUserRoles(ctx, tx, id, in.RoleIDs, actor.UserID); err != nil {
				return err
			}
		}
		if in.WarehouseIDs != nil {
			if err := s.repo.DeleteUserWarehouses(ctx, tx, id); err != nil {
				return err
			}
			if err := s.repo.InsertUserWarehouses(ctx, tx, id, in.WarehouseIDs, actor.UserID); err != nil {
				return err
			}
		}
		e := actor.auditEntry("user", id, "update")
		e.Success = true
		e.Before = viewUser(target)
		e.After = cols
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}

	// 角色/数据范围/仓库绑定变更：权限缓存定向失效 + 目标用户全部会话强制下线（S10）。
	// 会话与 access token 携带的数据范围/权限快照即刻失效——写路径自动执行，
	// 不再依赖"管理员手动踢下线兜底"（plan §13.3 快照漂移已知限制的销项）。
	if in.RoleIDs != nil {
		if err := s.store.DelPerms(ctx, id); err != nil {
			return nil, err
		}
	}
	if in.RoleIDs != nil || in.DataScope != nil || in.WarehouseIDs != nil {
		if err := s.kickUserSessions(ctx, id, "", actor); err != nil {
			return nil, err
		}
	}

	fresh, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return viewUser(fresh), nil
}

// UpdateUserStatus 启停用户（停用 = 离岗路径：M1 不提供删除接口，plan §5.4.1）。
func (s *Service) UpdateUserStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validUserStatus(status) {
		return response.NewError(response.CodeInvalidParam, map[string]any{"field": "status", "reason": "ACTIVE/DISABLED"})
	}
	target, err := s.assertUserMutable(ctx, actor, id)
	if err != nil {
		return err
	}
	if target.ID.Int64() == actor.UserID && status == UserStatusDisabled {
		return response.NewError(ErrCannotDisableSelf, nil)
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("user", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": target.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	if status == UserStatusDisabled {
		// 停用即离岗：全部会话强制下线 + 权限缓存失效（fail-closed）。
		if err := s.kickUserSessions(ctx, id, "", actor); err != nil {
			return err
		}
		if err := s.store.DelPerms(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ResetPasswordInput 管理员重置密码入参。
type ResetPasswordInput struct {
	NewPassword string `json:"new_password"`
}

// ResetUserPassword 管理员重置密码（permission.md §6 敏感操作：强制审计）。
// 重置后目标用户 must_change_password=TRUE 且全部会话强制下线（旧凭证即刻失效）。
func (s *Service) ResetUserPassword(ctx context.Context, actor Actor, id int64, in ResetPasswordInput) error {
	if err := ValidatePassword(in.NewPassword); err != nil {
		return err
	}
	if _, err := s.assertUserMutable(ctx, actor, id); err != nil {
		return err
	}
	hash, err := HashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, id, map[string]any{
			"password_hash":        hash,
			"must_change_password": true,
			"updated_by":           database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		// 快照仅记录标记位变化，绝不记录密码（architecture.md §6 日志红线）。
		e := actor.auditEntry("user", id, "reset-password")
		e.Success = true
		e.After = map[string]any{"must_change_password": true}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	return s.kickUserSessions(ctx, id, "", actor)
}

// UnlockUser 解锁账户（permission.md §3.2 锁定与解锁、§6 敏感操作审计）：
// 重置 locked_until 并清空 Redis 失败计数，管理员无需等待自动到期（plan §7.3）。
func (s *Service) UnlockUser(ctx context.Context, actor Actor, id int64) error {
	target, err := s.assertUserMutable(ctx, actor, id)
	if err != nil {
		return err
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateUserCols(ctx, tx, id, map[string]any{
			"locked_until": nil, "updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("user", id, "unlock")
		e.Success = true
		e.Before = map[string]any{"locked_until": target.LockedUntil}
		e.After = map[string]any{"locked_until": nil}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	// 清空失败计数（best-effort：残余计数只影响后续窗口的累计速度）。
	_ = s.guard.Reset(ctx, target.Username)
	return nil
}

// AssignRolesInput 绑定角色入参（全量替换语义）。
type AssignRolesInput struct {
	RoleIDs []int64 `json:"role_ids"`
}

// AssignRoles 绑定角色（permission.md §6 敏感操作：强制审计）。
// 授予侧特权边界（安全审查 S1）：非超管操作者不得授予 super_admin 角色、
// 不得授予携带自身不持有权限点的角色。
func (s *Service) AssignRoles(ctx context.Context, actor Actor, id int64, in AssignRolesInput) error {
	if _, err := s.assertUserMutable(ctx, actor, id); err != nil {
		return err
	}
	if err := s.assertRolesUsable(ctx, in.RoleIDs); err != nil {
		return err
	}
	if err := s.assertGrantableRoles(ctx, actor, in.RoleIDs); err != nil {
		return err
	}
	before, err := s.repo.ListRoleIDsByUser(ctx, id)
	if err != nil {
		return err
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.DeleteUserRoles(ctx, tx, id); err != nil {
			return err
		}
		if err := s.repo.InsertUserRoles(ctx, tx, id, in.RoleIDs, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("user", id, "assign-role")
		e.Success = true
		e.Before = map[string]any{"role_ids": before}
		e.After = map[string]any{"role_ids": in.RoleIDs}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	// 角色全量替换属特权变更（S10，复审补修）：权限缓存定向失效 + 目标会话全部强制下线——
	// 专用绑定角色端点与 UpdateUser 写路径同一处理，否则被收回 super_admin 角色的目标
	// 在途会话仍持 IsSuper=true 快照直通 RequirePermission 豁免，最长 2h（plan §13.3）。
	if err := s.store.DelPerms(ctx, id); err != nil {
		return err
	}
	return s.kickUserSessions(ctx, id, "", actor)
}

// GetUsers 用户分页列表。
// 用户目录收敛（安全审查 S9）：非超管操作者按数据范围过滤——ALL 全量；
// DEPARTMENT 仅本部门（覆盖前端传入的 department_id，permission.md §4 禁止前端传参决定范围）；
// 其余范围（SPECIFIED_WAREHOUSE/SELF/SELF_IN_CHARGE/异常值）fail-closed 仅本人。
// 列表响应不返回 last_login_ip。
func (s *Service) GetUsers(ctx context.Context, actor Actor, f UserListFilter) ([]*UserView, int64, error) {
	if !actor.IsSuper && actor.DataScope != DataScopeAll {
		if actor.DataScope == DataScopeDepartment && actor.DeptID > 0 {
			f.DepartmentID = actor.DeptID
		} else {
			f.OnlyUserID = actor.UserID
		}
	}
	users, total, err := s.repo.ListUsers(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*UserView, 0, len(users))
	for _, u := range users {
		v := viewUser(u)
		v.LastLoginIP = "" // S9：列表不泄露他人登录 IP（详情对 auth:user:read 保留）
		out = append(out, v)
	}
	return out, total, nil
}

// userVisibleTo 用户目录可见性判定（安全审查 S9，fail-closed）：
// 非超管/非 ALL 操作者仅可见本人，及 DEPARTMENT 范围下的本部门用户。
func (s *Service) userVisibleTo(actor Actor, u *User) bool {
	if actor.IsSuper || actor.DataScope == DataScopeAll {
		return true
	}
	if actor.UserID == u.ID.Int64() {
		return true
	}
	return actor.DataScope == DataScopeDepartment && actor.DeptID > 0 &&
		u.DepartmentID != nil && u.DepartmentID.Int64() == actor.DeptID
}

// GetUserDetail 用户详情（含角色/仓库绑定）。
// 用户目录收敛（安全审查 S9）：越范围目标与不存在同样返回 NOT_FOUND（不确认存在性）。
func (s *Service) GetUserDetail(ctx context.Context, actor Actor, id int64) (*UserView, error) {
	u, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil || !s.userVisibleTo(actor, u) {
		return nil, response.NewError(ErrUserNotFound, nil)
	}
	view := viewUser(u)
	roleIDs, err := s.repo.ListRoleIDsByUser(ctx, id)
	if err != nil {
		return nil, err
	}
	view.RoleIDs = toIDs(roleIDs)
	whIDs, err := s.repo.ListWarehouseIDsByUser(ctx, id)
	if err != nil {
		return nil, err
	}
	view.WarehouseIDs = toIDs(whIDs)
	return view, nil
}

// assertUserMutable 目标用户存在 + 超管保护（非超管不得改动超管账户）
// + 数据范围可见性（S9 读写对称，复审补修）：UpdateUser/UpdateUserStatus/ResetUserPassword/
// AssignRoles/UnlockUser 全部写路径经此单一入口——非特权操作者仅可改动目录范围内用户
// （与 GetUserDetail 同一 userVisibleTo 判定），越范围目标一律 404，不确认存在性。
func (s *Service) assertUserMutable(ctx context.Context, actor Actor, id int64) (*User, error) {
	u, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewError(ErrUserNotFound, nil)
	}
	if !actor.IsSuper {
		isSuperTarget, err := s.repo.ListRoleByCodeForUser(ctx, id, SuperAdminRoleCode)
		if err != nil {
			return nil, err
		}
		if isSuperTarget {
			return nil, response.NewError(ErrSuperUserProtected, nil)
		}
		if !s.userVisibleTo(actor, u) {
			return nil, response.NewError(ErrUserNotFound, nil)
		}
	}
	return u, nil
}

// assertRolesUsable 校验角色 ID 集合全部存在且启用（api.md §4 业务关系校验）。
func (s *Service) assertRolesUsable(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	n, err := s.repo.CountEnabledRolesByIDs(ctx, ids)
	if err != nil {
		return err
	}
	if int(n) != countDistinct(ids) {
		return response.NewError(ErrRoleNotFound, map[string]any{"role_ids": ids})
	}
	return nil
}

// assertWarehousesUsable 校验仓库绑定存在性——经 §4.3 注入的 WarehouseChecker
// （仓库表归 warehouse 域，判据 9 禁止本域直查）。校验器未装配时 fail-closed。
func (s *Service) assertWarehousesUsable(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if s.checker == nil {
		return response.NewError(ErrWarehouseCheckerMissing, map[string]any{
			"reason": "WithWarehouseChecker 未注入（plan §4.3）",
		})
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, wid := range ids {
		if wid <= 0 {
			return response.NewError(response.CodeInvalidParam, map[string]any{"field": "warehouse_ids", "reason": "非法仓库 ID"})
		}
		if _, dup := seen[wid]; dup {
			continue
		}
		seen[wid] = struct{}{}
		ok, err := s.checker.ExistsActive(ctx, wid)
		if err != nil {
			return fmt.Errorf("校验仓库 %d 存在性失败: %w", wid, err)
		}
		if !ok {
			return response.NewError(ErrWarehouseInvalid, map[string]any{"warehouse_id": wid})
		}
	}
	return nil
}

// ---- 授予侧特权边界（安全审查 S1）----
// 原则：非 super_admin 操作者不得制造自己不持有的权力——不授予 super_admin 角色、
// 不授予自身不持有的权限点（直接绑定或经角色携带）、不授予超出自身 data_scope 的范围。
// super_admin 操作者直通（permission.md §1）。

// assertGrantableRoles 角色授予边界：super_admin 角色仅超管可授；
// 角色携带的权限点集合必须 ⊆ 操作者自身权限点集合（横向提权拦截）。
func (s *Service) assertGrantableRoles(ctx context.Context, actor Actor, roleIDs []int64) error {
	if actor.IsSuper || len(roleIDs) == 0 {
		return nil
	}
	own, err := s.PermissionCodes(ctx, actor.UserID)
	if err != nil {
		return err
	}
	ownSet := stringSet(own)
	for _, rid := range distinctIDs(roleIDs) {
		r, err := s.repo.FindRoleByID(ctx, rid)
		if err != nil {
			return err
		}
		if r == nil {
			return response.NewError(ErrRoleNotFound, map[string]any{"role_id": rid})
		}
		if r.Code == SuperAdminRoleCode {
			return response.NewError(ErrRoleEscalationDenied, map[string]any{
				"role_id": rid, "role_code": r.Code,
				"reason": "super_admin 角色仅超级管理员可授予",
			})
		}
		codes, err := s.repo.ListPermissionCodesByRole(ctx, rid)
		if err != nil {
			return err
		}
		if beyond := beyondSet(codes, ownSet); len(beyond) > 0 {
			return response.NewError(ErrPermEscalationDenied, map[string]any{
				"role_id": rid, "role_code": r.Code, "permission_codes": beyond,
			})
		}
	}
	return nil
}

// assertGrantablePermissions 权限点绑定边界：绑定集合必须 ⊆ 操作者自身权限点集合
// （调用方已先行做存在性/启用校验；此处按编码比对）。
func (s *Service) assertGrantablePermissions(ctx context.Context, actor Actor, permIDs []int64) error {
	if actor.IsSuper || len(permIDs) == 0 {
		return nil
	}
	own, err := s.PermissionCodes(ctx, actor.UserID)
	if err != nil {
		return err
	}
	codes, err := s.repo.ListPermissionCodesByIDs(ctx, distinctIDs(permIDs))
	if err != nil {
		return err
	}
	if beyond := beyondSet(codes, stringSet(own)); len(beyond) > 0 {
		return response.NewError(ErrPermEscalationDenied, map[string]any{
			"permission_codes": beyond,
		})
	}
	return nil
}

// assertGrantableScope 数据范围授予边界（permission.md §4 五范围，fail-closed）：
//   - 超管 / ALL 操作者：可授予任意范围；
//   - 目标 SELF：任何操作者可授（最小范围，无放大）；
//   - 目标 SELF_IN_CHARGE / ALL：仅超管 / ALL 操作者（SELF_IN_CHARGE 行级语义 M2 才落地，
//     M1 保守禁止中低权限操作者预授权）；
//   - 目标 DEPARTMENT：仅 DEPARTMENT 操作者，且目标部门必须等于自身部门；
//   - 目标 SPECIFIED_WAREHOUSE：仅 SPECIFIED_WAREHOUSE 操作者，且仓库集 ⊆ 自身绑定集。
func (s *Service) assertGrantableScope(actor Actor, scope string, deptID int64, whIDs []int64) error {
	if actor.IsSuper || actor.DataScope == DataScopeAll {
		return nil
	}
	deny := func(details map[string]any) error {
		details["field"] = "data_scope"
		details["data_scope"] = scope
		return response.NewError(ErrScopeEscalationDenied, details)
	}
	switch scope {
	case DataScopeSelf:
		return nil
	case DataScopeAll, DataScopeSelfInCharge:
		return deny(map[string]any{"reason": "超出操作者数据权限边界"})
	case DataScopeDepartment:
		if actor.DataScope != DataScopeDepartment || actor.DeptID <= 0 || deptID != actor.DeptID {
			return deny(map[string]any{"reason": "仅可授予自身部门"})
		}
		return nil
	case DataScopeSpecifiedWh:
		if actor.DataScope != DataScopeSpecifiedWh {
			return deny(map[string]any{"reason": "操作者无仓库范围授权"})
		}
		return s.assertGrantableWarehouses(actor, whIDs)
	}
	return nil // 非法范围由调用方先行校验拦截
}

// assertGrantableWarehouses 仓库绑定写入边界：非特权操作者必须自身为 SPECIFIED_WAREHOUSE
// 且绑定集覆盖待写入集合（清空绑定不设限——只减不增不构成提权）。
func (s *Service) assertGrantableWarehouses(actor Actor, whIDs []int64) error {
	if actor.IsSuper || actor.DataScope == DataScopeAll || len(whIDs) == 0 {
		return nil
	}
	if beyond := beyondSetInt(whIDs, actor.WarehouseIDs); actor.DataScope != DataScopeSpecifiedWh || len(beyond) > 0 {
		return response.NewError(ErrScopeEscalationDenied, map[string]any{
			"field": "warehouse_ids", "warehouse_ids": beyond,
			"reason": "超出操作者仓库范围",
		})
	}
	return nil
}

// ---------------- 角色 ----------------

// RoleCreateInput 创建角色入参。
type RoleCreateInput struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// CreateRole 创建角色（code 唯一；is_system 恒 false）。
func (s *Service) CreateRole(ctx context.Context, actor Actor, in RoleCreateInput) (*RoleView, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if !roleCodeRe.MatchString(in.Code) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "code", "reason": "2-64 位，小写字母开头，可含数字/下划线",
		})
	}
	if in.Name == "" || len([]rune(in.Name)) > maxNameLen {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "name", "reason": "必填且 ≤64 字"})
	}
	status := in.Status
	if status == "" {
		status = StatusEnabled
	}
	if !validOnOffStatus(status) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "status", "reason": "ENABLED/DISABLED"})
	}
	if exist, err := s.repo.FindRoleByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrRoleCodeExists, nil)
	}
	r := &Role{Code: in.Code, Name: in.Name, IsSystem: false, Status: status}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertRole(ctx, tx, r); err != nil {
			return err
		}
		e := actor.auditEntry("role", r.ID.Int64(), "create")
		e.Success = true
		e.After = viewRole(r)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewRole(r), nil
}

// RoleUpdateInput 更新角色入参（code 不可变）。
type RoleUpdateInput struct {
	Name string `json:"name"`
}

// UpdateRole 更新角色名称（内置角色允许改名，禁删禁停用）。
func (s *Service) UpdateRole(ctx context.Context, actor Actor, id int64, in RoleUpdateInput) (*RoleView, error) {
	r, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, response.NewError(ErrRoleNotFound, nil)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > maxNameLen {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "name", "reason": "必填且 ≤64 字"})
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateRoleCols(ctx, tx, id, map[string]any{
			"name": in.Name, "updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("role", id, "update")
		e.Success = true
		e.Before = map[string]any{"name": r.Name}
		e.After = map[string]any{"name": in.Name}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	fresh, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return viewRole(fresh), nil
}

// UpdateRoleStatus 启停角色（内置角色禁停用；停用即该角色全部用户失权，fail-closed）。
func (s *Service) UpdateRoleStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return response.NewError(response.CodeInvalidParam, map[string]any{"field": "status", "reason": "ENABLED/DISABLED"})
	}
	r, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return err
	}
	if r == nil {
		return response.NewError(ErrRoleNotFound, nil)
	}
	if r.IsSystem {
		return response.NewError(ErrRoleSystemLocked, nil)
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateRoleCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("role", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": r.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	// 该角色全部用户权限缓存定向失效（plan §13.3）。
	uids, err := s.listUserIDsByRole(ctx, id)
	if err != nil {
		return err
	}
	return s.store.DelPerms(ctx, uids...)
}

// AssignPermissionsInput 绑定权限入参（全量替换语义）。
type AssignPermissionsInput struct {
	PermissionIDs []int64 `json:"permission_ids"`
}

// AssignPermissions 角色绑定权限（permission.md §6 敏感操作：强制审计）。
// 安全审查 S1：is_system 角色禁止重绑权限点（对齐 UpdateRoleStatus 的既有保护）；
// 非超管操作者不得绑定自身不持有的权限点。
func (s *Service) AssignPermissions(ctx context.Context, actor Actor, roleID int64, in AssignPermissionsInput) error {
	r, err := s.repo.FindRoleByID(ctx, roleID)
	if err != nil {
		return err
	}
	if r == nil {
		return response.NewError(ErrRoleNotFound, nil)
	}
	if r.IsSystem {
		return response.NewError(ErrRoleSystemLocked, nil)
	}
	if in.PermissionIDs != nil {
		n, err := s.repo.CountEnabledPermissionsByIDs(ctx, in.PermissionIDs)
		if err != nil {
			return err
		}
		if int(n) != countDistinct(in.PermissionIDs) {
			return response.NewError(ErrPermissionInvalid, map[string]any{"permission_ids": in.PermissionIDs})
		}
		if err := s.assertGrantablePermissions(ctx, actor, in.PermissionIDs); err != nil {
			return err
		}
	}
	before, err := s.repo.ListPermissionIDsByRole(ctx, roleID)
	if err != nil {
		return err
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.ReplaceRolePermissions(ctx, tx, roleID, in.PermissionIDs, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("role", roleID, "assign-permission")
		e.Success = true
		e.Before = map[string]any{"permission_ids": before}
		e.After = map[string]any{"permission_ids": in.PermissionIDs}
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return err
	}
	uids, err := s.listUserIDsByRole(ctx, roleID)
	if err != nil {
		return err
	}
	return s.store.DelPerms(ctx, uids...)
}

// GetRoles 角色分页列表。
func (s *Service) GetRoles(ctx context.Context, f RoleListFilter) ([]*RoleView, int64, error) {
	roles, total, err := s.repo.ListRoles(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*RoleView, 0, len(roles))
	for _, r := range roles {
		out = append(out, viewRole(r))
	}
	return out, total, nil
}

// GetRoleDetail 角色详情（含权限绑定与用户数）。
func (s *Service) GetRoleDetail(ctx context.Context, id int64) (*RoleView, error) {
	r, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, response.NewError(ErrRoleNotFound, nil)
	}
	view := viewRole(r)
	permIDs, err := s.repo.ListPermissionIDsByRole(ctx, id)
	if err != nil {
		return nil, err
	}
	view.PermissionIDs = toIDs(permIDs)
	n, err := s.repo.CountUsersByRole(ctx, id)
	if err != nil {
		return nil, err
	}
	view.UserCount = n
	return view, nil
}

// ---------------- 权限点 ----------------

// GetPermissions 权限点分页列表（GET /api/permissions，plan §5.4）。
func (s *Service) GetPermissions(ctx context.Context, f PermissionListFilter) ([]*PermissionView, int64, error) {
	perms, total, err := s.repo.ListPermissions(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*PermissionView, 0, len(perms))
	for _, p := range perms {
		out = append(out, viewPerm(p))
	}
	return out, total, nil
}

// ---------------- 部门 ----------------

// DeptCreateInput 创建部门入参。
type DeptCreateInput struct {
	ParentID *int64 `json:"parent_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Status   string `json:"status"`
}

// CreateDept 创建部门（parent 存在且启用；code 唯一——000001 未建唯一索引，
// 由本服务查询前置校验 + 部署后补索引兜底，见交付披露）。
func (s *Service) CreateDept(ctx context.Context, actor Actor, in DeptCreateInput) (*DepartmentView, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if !deptCodeRe.MatchString(in.Code) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"field": "code", "reason": "2-64 位，大写字母开头，可含数字/下划线/连字符",
		})
	}
	if in.Name == "" || len([]rune(in.Name)) > maxNameLen {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "name", "reason": "必填且 ≤64 字"})
	}
	status := in.Status
	if status == "" {
		status = StatusEnabled
	}
	if !validOnOffStatus(status) {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "status", "reason": "ENABLED/DISABLED"})
	}
	if in.ParentID != nil && *in.ParentID > 0 {
		parent, err := s.repo.FindDeptByID(ctx, *in.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil || parent.Status != StatusEnabled {
			return nil, response.NewError(ErrDeptNotFound, map[string]any{"parent_id": *in.ParentID})
		}
	}
	if exist, err := s.repo.FindDeptByCode(ctx, in.Code); err != nil {
		return nil, err
	} else if exist != nil {
		return nil, response.NewError(ErrDeptCodeExists, nil)
	}

	d := &Department{Code: in.Code, Name: in.Name, Status: status}
	if in.ParentID != nil && *in.ParentID > 0 {
		id := database.ID(*in.ParentID)
		d.ParentID = &id
	}
	err := database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.InsertDept(ctx, tx, d); err != nil {
			return err
		}
		e := actor.auditEntry("department", d.ID.Int64(), "create")
		e.Success = true
		e.After = viewDept(d)
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return viewDept(d), nil
}

// DeptUpdateInput 更新部门入参（code 不可变；parent 可迁移但禁止成环）。
type DeptUpdateInput struct {
	ParentID *int64 `json:"parent_id"`
	Name     string `json:"name"`
}

// UpdateDept 更新部门（名称必填校验 + 父级迁移成环检查，环检测沿父链上溯）。
func (s *Service) UpdateDept(ctx context.Context, actor Actor, id int64, in DeptUpdateInput) (*DepartmentView, error) {
	d, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, response.NewError(ErrDeptNotFound, nil)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > maxNameLen {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "name", "reason": "必填且 ≤64 字"})
	}
	var newParent *database.ID
	if in.ParentID != nil {
		if *in.ParentID > 0 {
			if *in.ParentID == id {
				return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "parent_id", "reason": "不能以自身为父级"})
			}
			parent, err := s.repo.FindDeptByID(ctx, *in.ParentID)
			if err != nil {
				return nil, err
			}
			if parent == nil || parent.Status != StatusEnabled {
				return nil, response.NewError(ErrDeptNotFound, map[string]any{"parent_id": *in.ParentID})
			}
			// 成环检查：沿新父链上溯，出现自身即成环（上限 64 层防脏数据死循环）。
			cursor := parent.ParentID
			for i := 0; cursor != nil && *cursor > 0 && i < 64; i++ {
				if int64(*cursor) == id {
					return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "parent_id", "reason": "父级链成环"})
				}
				node, err := s.repo.FindDeptByID(ctx, cursor.Int64())
				if err != nil {
					return nil, err
				}
				if node == nil {
					break
				}
				cursor = node.ParentID
			}
			pid := database.ID(*in.ParentID)
			newParent = &pid
		} else {
			newParent = nil // 提升为顶级
		}
	}
	cols := map[string]any{"name": in.Name, "updated_by": database.ID(actor.UserID)}
	if in.ParentID != nil {
		if newParent != nil {
			cols["parent_id"] = *newParent
		} else {
			cols["parent_id"] = nil
		}
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateDeptCols(ctx, tx, id, cols); err != nil {
			return err
		}
		e := actor.auditEntry("department", id, "update")
		e.Success = true
		e.Before = viewDept(d)
		e.After = cols
		return middleware.Audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	fresh, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return viewDept(fresh), nil
}

// UpdateDeptStatus 启停部门（存在启用中子部门或启用中用户时禁止停用）。
func (s *Service) UpdateDeptStatus(ctx context.Context, actor Actor, id int64, status string) error {
	if !validOnOffStatus(status) {
		return response.NewError(response.CodeInvalidParam, map[string]any{"field": "status", "reason": "ENABLED/DISABLED"})
	}
	d, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return err
	}
	if d == nil {
		return response.NewError(ErrDeptNotFound, nil)
	}
	if status == StatusDisabled {
		children, err := s.repo.CountEnabledChildDepts(ctx, id)
		if err != nil {
			return err
		}
		if children > 0 {
			return response.NewError(ErrDeptHasChildren, map[string]any{"enabled_children": children})
		}
		users, err := s.repo.CountUsersByDept(ctx, id, true)
		if err != nil {
			return err
		}
		if users > 0 {
			return response.NewError(ErrDeptHasUsers, map[string]any{"active_users": users})
		}
	}
	err = database.Tx(ctx, s.repo.DB(), func(tx *gorm.DB) error {
		if err := s.repo.UpdateDeptCols(ctx, tx, id, map[string]any{
			"status": status, "updated_by": database.ID(actor.UserID),
		}); err != nil {
			return err
		}
		e := actor.auditEntry("department", id, "status")
		e.Success = true
		e.Before = map[string]any{"status": d.Status}
		e.After = map[string]any{"status": status}
		return middleware.Audit(tx, e)
	})
	return err
}

// GetDepts 部门分页列表。
func (s *Service) GetDepts(ctx context.Context, f DeptListFilter) ([]*DepartmentView, int64, error) {
	depts, total, err := s.repo.ListDepts(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*DepartmentView, 0, len(depts))
	for _, d := range depts {
		out = append(out, viewDept(d))
	}
	return out, total, nil
}

// GetDeptDetail 部门详情。
func (s *Service) GetDeptDetail(ctx context.Context, id int64) (*DepartmentView, error) {
	d, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, response.NewError(ErrDeptNotFound, nil)
	}
	return viewDept(d), nil
}

// ---------------- 辅助 ----------------

// listUserIDsByRole 角色下全部用户 ID（权限缓存定向失效用，plan §13.3）。
func (s *Service) listUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error) {
	return s.repo.ListUserIDsByRole(ctx, roleID)
}

func toIDs(xs []int64) []database.ID {
	out := make([]database.ID, 0, len(xs))
	for _, x := range xs {
		out = append(out, database.ID(x))
	}
	return out
}

func countDistinct(ids []int64) int {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}

// distinctIDs 去重保序（授予侧边界校验按去重集合逐个判定）。
func distinctIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// stringSet / beyondSet / beyondSetInt 授予侧边界的小工具：求待授集合中超出自身持有的部分。
func stringSet(xs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		set[x] = struct{}{}
	}
	return set
}

func beyondSet(xs []string, own map[string]struct{}) []string {
	var beyond []string
	seen := map[string]struct{}{}
	for _, x := range xs {
		if _, ok := own[x]; ok {
			continue
		}
		if _, dup := seen[x]; dup {
			continue
		}
		seen[x] = struct{}{}
		beyond = append(beyond, x)
	}
	return beyond
}

func beyondSetInt(xs []int64, own []int64) []int64 {
	return beyondSetMapInt(xs, intSet(own))
}

func intSet(xs []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(xs))
	for _, x := range xs {
		set[x] = struct{}{}
	}
	return set
}

func beyondSetMapInt(xs []int64, own map[int64]struct{}) []int64 {
	var beyond []int64
	for _, x := range distinctIDs(xs) {
		if _, ok := own[x]; !ok {
			beyond = append(beyond, x)
		}
	}
	return beyond
}

// validateContact 联系信息长度校验（api.md §4 完整校验）。
func validateContact(realName, phone, email string) error {
	if len([]rune(strings.TrimSpace(realName))) > maxNameLen {
		return response.NewError(response.CodeInvalidParam, map[string]any{"field": "real_name", "reason": "≤64 字"})
	}
	if strings.TrimSpace(phone) != "" {
		if len(phone) > 32 || !phoneRe.MatchString(strings.TrimSpace(phone)) {
			return response.NewError(response.CodeInvalidParam, map[string]any{"field": "phone", "reason": "电话格式非法"})
		}
	}
	if strings.TrimSpace(email) != "" {
		if len(email) > maxContactLen || !emailRe.MatchString(strings.TrimSpace(email)) {
			return response.NewError(response.CodeInvalidParam, map[string]any{"field": "email", "reason": "邮箱格式非法"})
		}
	}
	return nil
}

// phoneRe/emailRe 宽松格式（含 +86 等前缀/常见企业邮箱），仅拦截明显非法输入。
var (
	phoneRe = regexp.MustCompile(`^\+?[0-9][0-9-]{4,31}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)
