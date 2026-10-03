package auth

import (
	"github.com/stockflow/server/internal/database"
)

// auth 域 GORM 模型——与 000001 迁移列结构一一对应（backend-m1-plan §6.2 冻结 DDL）。
// 判据 4（plan §4.2）：users/roles/permissions/user_* 的模型与 SQL 仅存在于本包。

// 数据权限范围（permission.md §4 五范围；users.data_scope CHECK 约束同枚举）。
const (
	DataScopeAll          = "ALL"
	DataScopeSpecifiedWh  = "SPECIFIED_WAREHOUSE"
	DataScopeDepartment   = "DEPARTMENT"
	DataScopeSelf         = "SELF"
	DataScopeSelfInCharge = "SELF_IN_CHARGE"
)

// 用户状态（chk_users_status）。
const (
	UserStatusActive   = "ACTIVE"
	UserStatusDisabled = "DISABLED"
)

// 通用启用/停用状态（departments/roles/permissions：chk_*_status）。
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)

// 权限点类型（permission.md §2 三级覆盖；chk_permissions_type）。
const (
	PermTypeMenu   = "MENU"
	PermTypeButton = "BUTTON"
	PermTypeAPI    = "API"
)

// User 用户（软删除对象，database.md §5.1）。
type User struct {
	database.BaseModel
	Username           string            `json:"username"`
	PasswordHash       string            `json:"-"` // 内部字段禁止出 API（api-and-data 规范）
	RealName           string            `json:"real_name"`
	Phone              string            `json:"phone"`
	Email              string            `json:"email"`
	DepartmentID       *database.ID      `json:"department_id"`
	DataScope          string            `json:"data_scope"`
	Status             string            `json:"status"`
	MustChangePassword bool              `json:"must_change_password"`
	LockedUntil        database.JSONTime `json:"locked_until"`
	LastLoginAt        database.JSONTime `json:"last_login_at"`
	LastLoginIP        string            `json:"last_login_ip"`
}

// TableName 显式指定表名。
func (User) TableName() string { return "users" }

// Role 角色（16 内置角色 is_system 禁删；无删除接口，database.md §5.1 清单不含 roles）。
type Role struct {
	database.BaseModel
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsSystem bool   `json:"is_system"`
	Status   string `json:"status"`
}

// TableName 显式指定表名。
func (Role) TableName() string { return "roles" }

// Permission 权限点（MENU/BUTTON/API 三级；parent_id 自引用构成树）。
type Permission struct {
	database.BaseModel
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Type     string       `json:"type"`
	ParentID *database.ID `json:"parent_id"`
	Sort     int          `json:"sort"`
	Status   string       `json:"status"`
}

// TableName 显式指定表名。
func (Permission) TableName() string { return "permissions" }

// Department 部门（permission.md §4 部门维度数据权限实体；parent_id 自引用）。
type Department struct {
	database.BaseModel
	ParentID *database.ID `json:"parent_id"`
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Status   string       `json:"status"`
}

// TableName 显式指定表名。
func (Department) TableName() string { return "departments" }

// UserRole 用户-角色关联（复合主键）。
type UserRole struct {
	UserID    database.ID       `gorm:"primaryKey" json:"user_id"`
	RoleID    database.ID       `gorm:"primaryKey" json:"role_id"`
	CreatedAt database.JSONTime `json:"created_at"`
	CreatedBy database.ID       `json:"created_by"`
}

// TableName 显式指定表名。
func (UserRole) TableName() string { return "user_roles" }

// RolePermission 角色-权限点关联（复合主键）。
type RolePermission struct {
	RoleID       database.ID       `gorm:"primaryKey" json:"role_id"`
	PermissionID database.ID       `gorm:"primaryKey" json:"permission_id"`
	CreatedAt    database.JSONTime `json:"created_at"`
	CreatedBy    database.ID       `json:"created_by"`
}

// TableName 显式指定表名。
func (RolePermission) TableName() string { return "role_permissions" }

// UserWarehouse 用户-仓库绑定（permission.md §4 SPECIFIED_WAREHOUSE 的仓库集）。
type UserWarehouse struct {
	UserID      database.ID       `gorm:"primaryKey" json:"user_id"`
	WarehouseID database.ID       `gorm:"primaryKey" json:"warehouse_id"`
	CreatedAt   database.JSONTime `json:"created_at"`
	CreatedBy   database.ID       `json:"created_by"`
}

// TableName 显式指定表名。
func (UserWarehouse) TableName() string { return "user_warehouses" }
