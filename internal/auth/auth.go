package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 冻结导出契约（backend-m1-plan §5.1：其他包只允许调用，导出签名不得变更）。
// 本文件是签名锚点；实现分布在 middleware.go / handler.go / service_*.go / session.go。

// UserContext 当前用户上下文（plan §5.1 冻结结构）。
type UserContext struct {
	UserID       int64
	Username     string
	IsSuper      bool
	DataScope    string  // ALL / SPECIFIED_WAREHOUSE / DEPARTMENT / SELF / SELF_IN_CHARGE
	WarehouseIDs []int64 // scope=SPECIFIED_WAREHOUSE 时的仓库集
	DeptID       int64
	SessionID    string
}

// Option RegisterProtectedRoutes 的可选注入项：仅用于跨域消费接口注入
// （plan §4.3/§5.2），不得携带业务配置。
type Option func(*options)

type options struct {
	checker WarehouseChecker
}

// WithWarehouseChecker 注入用户绑定仓库的存在性校验器（plan §4.3：仓库表归 warehouse 域，
// auth 禁止直查；实现为 warehouse.NewChecker(db)，方法签名 ExistsActive(ctx, warehouseID)）。
// M1 必需 Checker：未注入时真实装配（db 非 nil）启动 fail-fast（plan §4.3 规则①）。
func WithWarehouseChecker(c WarehouseChecker) Option {
	return func(o *options) { o.checker = c }
}

// RegisterPublicRoutes 公开路由：POST /login、POST /refresh（api.md §6.1 豁免名单之内）。
// 同时完成本域依赖装配（配置解析 fail-fast：release 模式缺 SF_AUTH_JWT_SECRET、
// 非法时长/弱密钥直接 panic，deployment.md §3 禁止带病启动）。
func RegisterPublicRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	ensureWired(db, rdb)
	rg.POST("/login", handleLogin)
	rg.POST("/refresh", handleRefresh)
}

// RegisterProtectedRoutes 受保护路由（plan §5.4 路由总表；rg 为已挂 AuthRequired 的保护组）：
//
//	POST /auth/logout、GET /auth/me、PUT /auth/password（本人操作，无需权限点）
//	GET/DELETE /auth/sessions（auth:session:list / auth:session:kick）
//	/api/users CRUD + status/reset-password/unlock/roles（auth:user:*）
//	/api/roles CRUD + status/permissions（auth:role:*）
//	GET /api/permissions（auth:permission:list）
//	/api/departments CRUD + status（auth:dept:*）
func RegisterProtectedRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	ensureWired(db, rdb)

	o := options{}
	for _, fn := range opts {
		fn(&o)
	}
	// plan §4.3 规则①：M1 必需 Checker 未注入即启动 fail-fast，杜绝静默跳过
	// 用户绑定仓库的存在性校验。db==nil 仅存在于测试装配（域为 stub 的 T0 用例），豁免。
	if db != nil && o.checker == nil {
		panic("auth 装配失败: WithWarehouseChecker 未注入（plan §4.3 规则①，" +
			"router 需以 auth.WithWarehouseChecker(warehouse.NewChecker(db)) 注入）")
	}
	applyChecker(o.checker)

	// —— 本人认证操作（认证即可，不挂权限点）——
	rg.POST("/auth/logout", handleLogout)
	rg.GET("/auth/me", handleMe)
	rg.PUT("/auth/password", handlePassword)

	// —— 在线会话（permission.md §3.4）——
	rg.GET("/auth/sessions", RequirePermission(PermSessionList), handleSessionList)
	rg.DELETE("/auth/sessions/:id", RequirePermission(PermSessionKick), handleSessionKick)

	// —— 用户（plan §5.4：CRUD + 启停/重置密码/解锁/绑定角色）——
	rg.GET("/users", RequirePermission(PermUserList), handleUserList)
	rg.POST("/users", RequirePermission(PermUserCreate), handleUserCreate)
	rg.GET("/users/:id", RequirePermission(PermUserRead), handleUserDetail)
	rg.PUT("/users/:id", RequirePermission(PermUserUpdate), handleUserUpdate)
	rg.PUT("/users/:id/status", RequirePermission(PermUserStatus), handleUserStatus)
	rg.PUT("/users/:id/reset-password", RequirePermission(PermUserResetPassword), handleUserResetPassword)
	rg.PUT("/users/:id/unlock", RequirePermission(PermUserUnlock), handleUserUnlock)
	rg.PUT("/users/:id/roles", RequirePermission(PermUserAssignRole), handleUserAssignRoles)

	// —— 角色 ——
	rg.GET("/roles", RequirePermission(PermRoleList), handleRoleList)
	rg.POST("/roles", RequirePermission(PermRoleCreate), handleRoleCreate)
	rg.GET("/roles/:id", RequirePermission(PermRoleRead), handleRoleDetail)
	rg.PUT("/roles/:id", RequirePermission(PermRoleUpdate), handleRoleUpdate)
	rg.PUT("/roles/:id/status", RequirePermission(PermRoleStatus), handleRoleStatus)
	rg.PUT("/roles/:id/permissions", RequirePermission(PermRoleAssignPermission), handleRoleAssignPermissions)

	// —— 权限点 ——
	rg.GET("/permissions", RequirePermission(PermPermissionList), handlePermissionList)

	// —— 部门 ——
	rg.GET("/departments", RequirePermission(PermDeptList), handleDeptList)
	rg.POST("/departments", RequirePermission(PermDeptCreate), handleDeptCreate)
	rg.GET("/departments/:id", RequirePermission(PermDeptRead), handleDeptDetail)
	rg.PUT("/departments/:id", RequirePermission(PermDeptUpdate), handleDeptUpdate)
	rg.PUT("/departments/:id/status", RequirePermission(PermDeptStatus), handleDeptStatus)
}

// applyChecker 把跨域校验器写入装配态（Service 与全局快照各一份）。
func applyChecker(c WarehouseChecker) {
	appDeps.mu.Lock()
	defer appDeps.mu.Unlock()
	appDeps.checker = c
	if appDeps.svc != nil {
		appDeps.svc.useChecker(c)
	}
}
