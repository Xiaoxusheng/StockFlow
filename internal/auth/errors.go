package auth

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// auth 域错误码（architecture.md §2 模块命名空间 AUTH_*；api.md §4 校验失败必须带 details）。
// 统一经 internal/response 注册与输出，禁止 handler 直接 c.JSON（plan §4.2 判据 2）。
var (
	// ErrCredentialsInvalid 凭证错误：统一文案，不区分"用户不存在/密码错误"（plan §7.3 防枚举）。
	ErrCredentialsInvalid = response.Register("AUTH_CREDENTIALS_INVALID", "用户名或密码错误", http.StatusUnauthorized)
	ErrTokenInvalid       = response.Register("AUTH_TOKEN_INVALID", "认证凭证无效或缺失", http.StatusUnauthorized)
	ErrTokenExpired       = response.Register("AUTH_TOKEN_EXPIRED", "认证凭证已过期，请刷新", http.StatusUnauthorized)
	ErrSessionInvalid     = response.Register("AUTH_SESSION_INVALID", "会话已失效，请重新登录", http.StatusUnauthorized)
	ErrRefreshInvalid     = response.Register("AUTH_REFRESH_INVALID", "刷新凭证无效或已过期", http.StatusUnauthorized)

	ErrAccountLocked           = response.Register("AUTH_ACCOUNT_LOCKED", "账户已锁定，请稍后重试或联系管理员解锁", http.StatusForbidden)
	ErrAccountDisabled         = response.Register("AUTH_ACCOUNT_DISABLED", "账户已停用，请联系管理员", http.StatusForbidden)
	ErrPasswordChangeRequired  = response.Register("AUTH_PASSWORD_CHANGE_REQUIRED", "必须先修改初始密码", http.StatusForbidden)
	ErrPasswordWeak            = response.Register("AUTH_PASSWORD_WEAK", "密码不满足安全策略：长度至少 8 位且必须同时包含字母与数字", http.StatusBadRequest)
	ErrPasswordMismatch        = response.Register("AUTH_PASSWORD_MISMATCH", "原密码不正确", http.StatusBadRequest)
	ErrSuperUserProtected      = response.Register("AUTH_SUPER_USER_PROTECTED", "超级管理员账户受保护，禁止该操作", http.StatusForbidden)
	ErrCannotDisableSelf       = response.Register("AUTH_SELF_OPERATION_FORBIDDEN", "不能对当前登录账户执行该操作", http.StatusForbidden)
	ErrWarehouseCheckerMissing = response.Register("AUTH_WAREHOUSE_CHECKER_MISSING", "仓库校验服务未装配", http.StatusInternalServerError)

	// 授予侧特权边界（安全审查 S1）：非超管操作者授予越界角色/权限点/数据范围时拒绝。
	// details 说明被拒对象（role_id / permission_codes / warehouse_ids 等）。
	ErrRoleEscalationDenied  = response.Register("AUTH_ROLE_ESCALATION_DENIED", "不允许授予该角色：超出操作者权限边界", http.StatusForbidden)
	ErrPermEscalationDenied  = response.Register("AUTH_PERM_ESCALATION_DENIED", "不允许授予该权限：超出操作者权限边界", http.StatusForbidden)
	ErrScopeEscalationDenied = response.Register("AUTH_SCOPE_ESCALATION_DENIED", "不允许授予该数据范围：超出操作者数据权限边界", http.StatusForbidden)

	ErrUserNotFound      = response.Register("AUTH_USER_NOT_FOUND", "用户不存在", http.StatusNotFound)
	ErrUsernameExists    = response.Register("AUTH_USERNAME_EXISTS", "用户名已存在", http.StatusConflict)
	ErrRoleNotFound      = response.Register("AUTH_ROLE_NOT_FOUND", "角色不存在", http.StatusNotFound)
	ErrRoleCodeExists    = response.Register("AUTH_ROLE_CODE_EXISTS", "角色编码已存在", http.StatusConflict)
	ErrRoleSystemLocked  = response.Register("AUTH_ROLE_SYSTEM_LOCKED", "内置角色不允许该操作", http.StatusConflict)
	ErrDeptNotFound      = response.Register("AUTH_DEPT_NOT_FOUND", "部门不存在", http.StatusNotFound)
	ErrDeptCodeExists    = response.Register("AUTH_DEPT_CODE_EXISTS", "部门编码已存在", http.StatusConflict)
	ErrDeptHasChildren   = response.Register("AUTH_DEPT_HAS_ENABLED_CHILDREN", "存在启用中的子部门，无法停用", http.StatusConflict)
	ErrDeptHasUsers      = response.Register("AUTH_DEPT_HAS_ACTIVE_USERS", "部门下存在启用中的用户，无法停用", http.StatusConflict)
	ErrPermissionInvalid = response.Register("AUTH_PERMISSION_INVALID", "权限点不存在或已停用", http.StatusBadRequest)
	ErrWarehouseInvalid  = response.Register("AUTH_WAREHOUSE_INVALID", "绑定的仓库不存在或已停用", http.StatusBadRequest)
	ErrSessionNotFound   = response.Register("AUTH_SESSION_NOT_FOUND", "会话不存在或已下线", http.StatusNotFound)
)
