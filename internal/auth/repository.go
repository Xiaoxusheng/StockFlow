package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
)

// Repository 消费方窄接口（plan §4.3 精神）：Service 只依赖本接口，
// 单元测试以内存替身实现（ask 约束：不依赖 PostgreSQL）。GORM 实现在本文件。
// 约束：repository 无业务判断（architecture.md §1），只做数据访问。
type Repository interface {
	DB() *gorm.DB

	// —— users ——
	FindUserByUsername(ctx context.Context, username string) (*User, error) // 未命中 (nil, nil)
	FindUserByID(ctx context.Context, id int64) (*User, error)              // 未命中 (nil, nil)
	ListUsers(ctx context.Context, f UserListFilter) ([]*User, int64, error)
	InsertUser(ctx context.Context, tx *gorm.DB, u *User) error
	UpdateUserCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	LockUser(ctx context.Context, id int64, until time.Time) error // 登录失败锁定（短路径，独立更新）
	CountUsersByDept(ctx context.Context, deptID int64, onlyActive bool) (int64, error)

	// —— 用户关联（roles/warehouses）——
	ListRoleIDsByUser(ctx context.Context, uid int64) ([]int64, error)
	ListRoleByCodeForUser(ctx context.Context, uid int64, roleCode string) (bool, error)
	DeleteUserRoles(ctx context.Context, tx *gorm.DB, uid int64) error
	InsertUserRoles(ctx context.Context, tx *gorm.DB, uid int64, roleIDs []int64, createdBy int64) error
	ListWarehouseIDsByUser(ctx context.Context, uid int64) ([]int64, error)
	DeleteUserWarehouses(ctx context.Context, tx *gorm.DB, uid int64) error
	InsertUserWarehouses(ctx context.Context, tx *gorm.DB, uid int64, whIDs []int64, createdBy int64) error
	CountUsersByRole(ctx context.Context, roleID int64) (int64, error)
	ListUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error)

	// —— roles ——
	FindRoleByID(ctx context.Context, id int64) (*Role, error)
	FindRoleByCode(ctx context.Context, code string) (*Role, error)
	ListRoles(ctx context.Context, f RoleListFilter) ([]*Role, int64, error)
	InsertRole(ctx context.Context, tx *gorm.DB, r *Role) error
	UpdateRoleCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	CountEnabledRolesByIDs(ctx context.Context, ids []int64) (int64, error)

	// —— permissions ——
	ListPermissions(ctx context.Context, f PermissionListFilter) ([]*Permission, int64, error)
	ListPermissionIDsByRole(ctx context.Context, roleID int64) ([]int64, error)
	CountEnabledPermissionsByIDs(ctx context.Context, ids []int64) (int64, error)
	ReplaceRolePermissions(ctx context.Context, tx *gorm.DB, roleID int64, permIDs []int64, createdBy int64) error
	ListPermissionCodesByUser(ctx context.Context, uid int64) ([]string, error)
	// 授予侧特权边界（安全审查 S1）：角色携带/指定集合的启用权限点编码。
	ListPermissionCodesByRole(ctx context.Context, roleID int64) ([]string, error)
	ListPermissionCodesByIDs(ctx context.Context, ids []int64) ([]string, error)

	// —— departments ——
	FindDeptByID(ctx context.Context, id int64) (*Department, error)
	FindDeptByCode(ctx context.Context, code string) (*Department, error)
	ListDepts(ctx context.Context, f DeptListFilter) ([]*Department, int64, error)
	InsertDept(ctx context.Context, tx *gorm.DB, d *Department) error
	UpdateDeptCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error
	CountEnabledChildDepts(ctx context.Context, parentID int64) (int64, error)
}

// repo GORM 实现。
type repo struct {
	db *gorm.DB
}

// NewRepository 构建 GORM Repository。
func NewRepository(db *gorm.DB) Repository { return &repo{db: db} }

func (r *repo) DB() *gorm.DB { return r.db }

// withCtx 统一挂载 context（超时/取消贯穿，api-and-data 规范）。
func withCtx(ctx context.Context, q *gorm.DB) *gorm.DB { return q.WithContext(ctx) }

// —— users ——

func (r *repo) FindUserByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := withCtx(ctx, r.db).Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户 %s 失败: %w", username, err)
	}
	return &u, nil
}

func (r *repo) FindUserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户 %d 失败: %w", id, err)
	}
	return &u, nil
}

// UserListFilter 用户列表筛选（分页强制，architecture.md §7）。
type UserListFilter struct {
	Keyword      string // username / real_name 模糊匹配
	Status       string // ACTIVE/DISABLED，空为全部
	DepartmentID int64  // 0 为全部
	OnlyUserID   int64  // >0 时仅该用户（安全审查 S9：非特权操作者 fail-closed 仅见本人；服务层注入）
	Page         int
	PageSize     int
}

func (r *repo) ListUsers(ctx context.Context, f UserListFilter) ([]*User, int64, error) {
	q := withCtx(ctx, r.db).Model(&User{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("username ILIKE ? OR real_name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.DepartmentID > 0 {
		q = q.Where("department_id = ?", f.DepartmentID)
	}
	if f.OnlyUserID > 0 {
		q = q.Where("id = ?", f.OnlyUserID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计用户总数失败: %w", err)
	}
	var users []*User
	err := q.Order("id ASC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&users).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询用户列表失败: %w", err)
	}
	return users, total, nil
}

func (r *repo) InsertUser(ctx context.Context, tx *gorm.DB, u *User) error {
	if err := withCtx(ctx, tx).Create(u).Error; err != nil {
		if isUniqueViolation(err) {
			// users.username 部分唯一索引（uk_users_username）兜底；
			// 竞态窗口内的重复插入在此显式转为业务冲突。
			return response.NewError(ErrUsernameExists, nil)
		}
		return fmt.Errorf("创建用户失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateUserCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	if len(cols) == 0 {
		return nil
	}
	if err := withCtx(ctx, tx).Model(&User{}).Where("id = ?", id).Updates(cols).Error; err != nil {
		return fmt.Errorf("更新用户 %d 失败: %w", id, err)
	}
	return nil
}

func (r *repo) LockUser(ctx context.Context, id int64, until time.Time) error {
	return r.UpdateUserCols(ctx, r.db, id, map[string]any{"locked_until": database.JSONTime{Time: until}})
}

func (r *repo) CountUsersByDept(ctx context.Context, deptID int64, onlyActive bool) (int64, error) {
	q := withCtx(ctx, r.db).Model(&User{}).Where("department_id = ?", deptID)
	if onlyActive {
		q = q.Where("status = ?", UserStatusActive)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("统计部门 %d 用户数失败: %w", deptID, err)
	}
	return n, nil
}

// —— 用户关联 ——

func (r *repo) ListRoleIDsByUser(ctx context.Context, uid int64) ([]int64, error) {
	var ids []int64
	err := withCtx(ctx, r.db).Model(&UserRole{}).Where("user_id = ?", uid).
		Order("role_id ASC").Pluck("role_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询用户 %d 角色失败: %w", uid, err)
	}
	return ids, nil
}

func (r *repo) ListRoleByCodeForUser(ctx context.Context, uid int64, roleCode string) (bool, error) {
	var n int64
	err := withCtx(ctx, r.db).Model(&UserRole{}).
		Joins("JOIN roles r ON r.id = user_roles.role_id").
		Where("user_roles.user_id = ? AND r.code = ?", uid, roleCode).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("查询用户 %d 是否为 %s 失败: %w", uid, roleCode, err)
	}
	return n > 0, nil
}

func (r *repo) DeleteUserRoles(ctx context.Context, tx *gorm.DB, uid int64) error {
	if err := withCtx(ctx, tx).Where("user_id = ?", uid).Delete(&UserRole{}).Error; err != nil {
		return fmt.Errorf("清理用户 %d 角色绑定失败: %w", uid, err)
	}
	return nil
}

func (r *repo) InsertUserRoles(ctx context.Context, tx *gorm.DB, uid int64, roleIDs []int64, createdBy int64) error {
	if len(roleIDs) == 0 {
		return nil
	}
	rows := make([]UserRole, 0, len(roleIDs))
	for _, rid := range roleIDs {
		rows = append(rows, UserRole{
			UserID: database.ID(uid), RoleID: database.ID(rid),
			CreatedAt: database.Now(), CreatedBy: database.ID(createdBy),
		})
	}
	if err := withCtx(ctx, tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入用户 %d 角色绑定失败: %w", uid, err)
	}
	return nil
}

func (r *repo) ListWarehouseIDsByUser(ctx context.Context, uid int64) ([]int64, error) {
	var ids []int64
	err := withCtx(ctx, r.db).Model(&UserWarehouse{}).Where("user_id = ?", uid).
		Order("warehouse_id ASC").Pluck("warehouse_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询用户 %d 仓库绑定失败: %w", uid, err)
	}
	return ids, nil
}

func (r *repo) DeleteUserWarehouses(ctx context.Context, tx *gorm.DB, uid int64) error {
	if err := withCtx(ctx, tx).Where("user_id = ?", uid).Delete(&UserWarehouse{}).Error; err != nil {
		return fmt.Errorf("清理用户 %d 仓库绑定失败: %w", uid, err)
	}
	return nil
}

func (r *repo) InsertUserWarehouses(ctx context.Context, tx *gorm.DB, uid int64, whIDs []int64, createdBy int64) error {
	if len(whIDs) == 0 {
		return nil
	}
	rows := make([]UserWarehouse, 0, len(whIDs))
	for _, wid := range whIDs {
		rows = append(rows, UserWarehouse{
			UserID: database.ID(uid), WarehouseID: database.ID(wid),
			CreatedAt: database.Now(), CreatedBy: database.ID(createdBy),
		})
	}
	if err := withCtx(ctx, tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入用户 %d 仓库绑定失败: %w", uid, err)
	}
	return nil
}

func (r *repo) CountUsersByRole(ctx context.Context, roleID int64) (int64, error) {
	var n int64
	if err := withCtx(ctx, r.db).Model(&UserRole{}).Where("role_id = ?", roleID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("统计角色 %d 用户数失败: %w", roleID, err)
	}
	return n, nil
}

func (r *repo) ListUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error) {
	var ids []int64
	err := withCtx(ctx, r.db).Model(&UserRole{}).Where("role_id = ?", roleID).
		Order("user_id ASC").Pluck("user_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询角色 %d 用户列表失败: %w", roleID, err)
	}
	return ids, nil
}

// —— roles ——

func (r *repo) FindRoleByID(ctx context.Context, id int64) (*Role, error) {
	var x Role
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询角色 %d 失败: %w", id, err)
	}
	return &x, nil
}

func (r *repo) FindRoleByCode(ctx context.Context, code string) (*Role, error) {
	var x Role
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询角色 %s 失败: %w", code, err)
	}
	return &x, nil
}

// RoleListFilter 角色列表筛选。
type RoleListFilter struct {
	Keyword  string // code / name 模糊
	Status   string
	Page     int
	PageSize int
}

func (r *repo) ListRoles(ctx context.Context, f RoleListFilter) ([]*Role, int64, error) {
	q := withCtx(ctx, r.db).Model(&Role{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计角色总数失败: %w", err)
	}
	var roles []*Role
	err := q.Order("id ASC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&roles).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询角色列表失败: %w", err)
	}
	return roles, total, nil
}

func (r *repo) InsertRole(ctx context.Context, tx *gorm.DB, x *Role) error {
	if err := withCtx(ctx, tx).Create(x).Error; err != nil {
		return fmt.Errorf("创建角色失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateRoleCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	if len(cols) == 0 {
		return nil
	}
	if err := withCtx(ctx, tx).Model(&Role{}).Where("id = ?", id).Updates(cols).Error; err != nil {
		return fmt.Errorf("更新角色 %d 失败: %w", id, err)
	}
	return nil
}

func (r *repo) CountEnabledRolesByIDs(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var n int64
	err := withCtx(ctx, r.db).Model(&Role{}).
		Where("id IN ? AND status = ?", ids, StatusEnabled).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("校验角色存在性失败: %w", err)
	}
	return n, nil
}

// —— permissions ——

// PermissionListFilter 权限点列表筛选。
type PermissionListFilter struct {
	Keyword  string // code / name 模糊
	Type     string // MENU/BUTTON/API
	Status   string
	Page     int
	PageSize int
}

func (r *repo) ListPermissions(ctx context.Context, f PermissionListFilter) ([]*Permission, int64, error) {
	q := withCtx(ctx, r.db).Model(&Permission{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计权限点总数失败: %w", err)
	}
	var perms []*Permission
	err := q.Order("sort ASC, id ASC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&perms).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询权限点列表失败: %w", err)
	}
	return perms, total, nil
}

func (r *repo) ListPermissionIDsByRole(ctx context.Context, roleID int64) ([]int64, error) {
	var ids []int64
	err := withCtx(ctx, r.db).Model(&RolePermission{}).Where("role_id = ?", roleID).
		Order("permission_id ASC").Pluck("permission_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询角色 %d 权限绑定失败: %w", roleID, err)
	}
	return ids, nil
}

func (r *repo) CountEnabledPermissionsByIDs(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var n int64
	err := withCtx(ctx, r.db).Model(&Permission{}).
		Where("id IN ? AND status = ?", ids, StatusEnabled).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("校验权限点存在性失败: %w", err)
	}
	return n, nil
}

func (r *repo) ReplaceRolePermissions(ctx context.Context, tx *gorm.DB, roleID int64, permIDs []int64, createdBy int64) error {
	if err := withCtx(ctx, tx).Where("role_id = ?", roleID).Delete(&RolePermission{}).Error; err != nil {
		return fmt.Errorf("清理角色 %d 权限绑定失败: %w", roleID, err)
	}
	if len(permIDs) == 0 {
		return nil
	}
	rows := make([]RolePermission, 0, len(permIDs))
	for _, pid := range permIDs {
		rows = append(rows, RolePermission{
			RoleID: database.ID(roleID), PermissionID: database.ID(pid),
			CreatedAt: database.Now(), CreatedBy: database.ID(createdBy),
		})
	}
	if err := withCtx(ctx, tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入角色 %d 权限绑定失败: %w", roleID, err)
	}
	return nil
}

// ListPermissionCodesByUser 汇总用户全部启用权限点编码（RBAC 判定数据源）。
// 仅统计启用角色上的启用权限点（停用角色/权限立即失权）。
func (r *repo) ListPermissionCodesByUser(ctx context.Context, uid int64) ([]string, error) {
	var codes []string
	err := withCtx(ctx, r.db).Model(&Permission{}).
		Joins("JOIN role_permissions rp ON rp.permission_id = permissions.id").
		Joins("JOIN user_roles ur ON ur.role_id = rp.role_id").
		Joins("JOIN roles r ON r.id = ur.role_id").
		Where("ur.user_id = ? AND permissions.status = ? AND r.status = ?", uid, StatusEnabled, StatusEnabled).
		Distinct().Pluck("permissions.code", &codes).Error
	if err != nil {
		return nil, fmt.Errorf("查询用户 %d 权限点失败: %w", uid, err)
	}
	return codes, nil
}

// ListPermissionCodesByRole 角色绑定的启用权限点编码（授予侧边界校验，S1）。
func (r *repo) ListPermissionCodesByRole(ctx context.Context, roleID int64) ([]string, error) {
	var codes []string
	err := withCtx(ctx, r.db).Model(&Permission{}).
		Joins("JOIN role_permissions rp ON rp.permission_id = permissions.id").
		Where("rp.role_id = ? AND permissions.status = ?", roleID, StatusEnabled).
		Distinct().Pluck("permissions.code", &codes).Error
	if err != nil {
		return nil, fmt.Errorf("查询角色 %d 权限点失败: %w", roleID, err)
	}
	return codes, nil
}

// ListPermissionCodesByIDs 指定权限点集合中的启用权限点编码（授予侧边界校验，S1）。
func (r *repo) ListPermissionCodesByIDs(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var codes []string
	err := withCtx(ctx, r.db).Model(&Permission{}).
		Where("id IN ? AND status = ?", ids, StatusEnabled).
		Pluck("code", &codes).Error
	if err != nil {
		return nil, fmt.Errorf("查询权限点 %v 编码失败: %w", ids, err)
	}
	return codes, nil
}

// —— departments ——

func (r *repo) FindDeptByID(ctx context.Context, id int64) (*Department, error) {
	var x Department
	err := withCtx(ctx, r.db).Where("id = ?", id).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询部门 %d 失败: %w", id, err)
	}
	return &x, nil
}

func (r *repo) FindDeptByCode(ctx context.Context, code string) (*Department, error) {
	var x Department
	err := withCtx(ctx, r.db).Where("code = ?", code).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询部门 %s 失败: %w", code, err)
	}
	return &x, nil
}

// DeptListFilter 部门列表筛选。
type DeptListFilter struct {
	Keyword  string // code / name 模糊
	Status   string
	Page     int
	PageSize int
}

func (r *repo) ListDepts(ctx context.Context, f DeptListFilter) ([]*Department, int64, error) {
	q := withCtx(ctx, r.db).Model(&Department{})
	if kw := likeEscape(f.Keyword); kw != "" {
		q = q.Where("code ILIKE ? OR name ILIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计部门总数失败: %w", err)
	}
	var depts []*Department
	err := q.Order("id ASC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize).Find(&depts).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询部门列表失败: %w", err)
	}
	return depts, total, nil
}

func (r *repo) InsertDept(ctx context.Context, tx *gorm.DB, x *Department) error {
	if err := withCtx(ctx, tx).Create(x).Error; err != nil {
		return fmt.Errorf("创建部门失败: %w", err)
	}
	return nil
}

func (r *repo) UpdateDeptCols(ctx context.Context, tx *gorm.DB, id int64, cols map[string]any) error {
	if len(cols) == 0 {
		return nil
	}
	if err := withCtx(ctx, tx).Model(&Department{}).Where("id = ?", id).Updates(cols).Error; err != nil {
		return fmt.Errorf("更新部门 %d 失败: %w", id, err)
	}
	return nil
}

func (r *repo) CountEnabledChildDepts(ctx context.Context, parentID int64) (int64, error) {
	var n int64
	err := withCtx(ctx, r.db).Model(&Department{}).
		Where("parent_id = ? AND status = ?", parentID, StatusEnabled).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("统计部门 %d 子部门失败: %w", parentID, err)
	}
	return n, nil
}

// —— 辅助 ——

// likeEscape 转义 ILIKE 通配符，防止用户输入 %/_ 扰动匹配范围（api-and-data：搜索安全）。
func likeEscape(kw string) string {
	kw = strings.TrimSpace(kw)
	kw = strings.ReplaceAll(kw, "\\", "\\\\")
	kw = strings.ReplaceAll(kw, "%", "\\%")
	kw = strings.ReplaceAll(kw, "_", "\\_")
	return kw
}

// isUniqueViolation 识别 PostgreSQL 唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
