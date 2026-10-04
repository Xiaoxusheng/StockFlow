package auth

// 测试替身：内存版 Repository（ask 约束：单测不依赖 PostgreSQL，用接口替身）。
// Service 的全部数据访问经本替身承载；事务句柄来自 openTestGorm（no-op 驱动）。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

type fakeRepo struct {
	mu         sync.Mutex
	db         *gorm.DB
	seq        int64
	users      map[int64]*User
	byUsername map[string]int64
	roles      map[int64]*Role
	roleByCode map[string]int64
	perms      map[int64]*Permission
	permByCode map[string]int64
	depts      map[int64]*Department
	deptByCode map[string]int64
	userRoles  map[int64][]int64
	rolePerms  map[int64][]int64
	userWhs    map[int64][]int64

	lockCalls        int // LockUser 调用计数（登录保护断言）
	lockCallsUntil   time.Time
	rolePermReplaced map[int64]int // ReplaceRolePermissions 调用计数
	errPermCodes     bool          // 故障注入：ListPermissionCodesByUser 失败（fail-closed 用例）
}

func newFakeRepo() *fakeRepo {
	db, err := openTestGorm()
	if err != nil {
		panic("打开假 gorm 失败: " + err.Error())
	}
	return &fakeRepo{
		db:               db,
		users:            map[int64]*User{},
		byUsername:       map[string]int64{},
		roles:            map[int64]*Role{},
		roleByCode:       map[string]int64{},
		perms:            map[int64]*Permission{},
		permByCode:       map[string]int64{},
		depts:            map[int64]*Department{},
		deptByCode:       map[string]int64{},
		userRoles:        map[int64][]int64{},
		rolePerms:        map[int64][]int64{},
		userWhs:          map[int64][]int64{},
		rolePermReplaced: map[int64]int{},
	}
}

func (f *fakeRepo) DB() *gorm.DB { return f.db }

func (f *fakeRepo) nextID() int64 {
	f.seq++
	return f.seq
}

// seedUser 写入一个用户（密码已哈希）。returns uid
func (f *fakeRepo) seedUser(username, password string, mut func(*User)) int64 {
	hash, _ := HashPassword(password)
	id := f.nextID()
	u := &User{
		BaseModel: database.BaseModel{ID: database.ID(id)},
		Username:  username, PasswordHash: hash,
		DataScope: DataScopeAll, Status: UserStatusActive,
	}
	if mut != nil {
		mut(u)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id] = u
	f.byUsername[username] = id
	return id
}

func (f *fakeRepo) seedRole(code string, status string, isSystem bool) int64 {
	id := f.nextID()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roles[id] = &Role{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		Code:     code, Name: code, Status: status, IsSystem: isSystem,
	}
	f.roleByCode[code] = id
	return id
}

func (f *fakeRepo) seedPerm(code string, status string) int64 {
	id := f.nextID()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perms[id] = &Permission{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		Code:     code, Name: code, Type: PermTypeAPI, Status: status,
	}
	f.permByCode[code] = id
	return id
}

func (f *fakeRepo) seedDept(code string, status string, parentID int64) int64 {
	id := f.nextID()
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &Department{
		BaseCols: database.BaseCols{ID: database.ID(id)},
		Code:     code, Name: code, Status: status,
	}
	if parentID > 0 {
		pid := database.ID(parentID)
		d.ParentID = &pid
	}
	f.depts[id] = d
	f.deptByCode[code] = id
	return id
}

func (f *fakeRepo) bindRole(uid, rid int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userRoles[uid] = append(f.userRoles[uid], rid)
}

func (f *fakeRepo) bindWh(uid int64, whIDs ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userWhs[uid] = append(f.userWhs[uid], whIDs...)
}

func (f *fakeRepo) bindPerm(rid, pid int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rolePerms[rid] = append(f.rolePerms[rid], pid)
}

// cloneUser 深拷贝（避免测试间意外共享指针）。
func cloneUser(u *User) *User {
	cp := *u
	if u.DepartmentID != nil {
		d := *u.DepartmentID
		cp.DepartmentID = &d
	}
	return &cp
}

func (f *fakeRepo) FindUserByUsername(_ context.Context, username string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byUsername[username]
	if !ok {
		return nil, nil
	}
	return cloneUser(f.users[id]), nil
}

func (f *fakeRepo) FindUserByID(_ context.Context, id int64) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, nil
	}
	return cloneUser(u), nil
}

func (f *fakeRepo) ListUsers(_ context.Context, filter UserListFilter) ([]*User, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*User
	for _, u := range f.users {
		if filter.Keyword != "" &&
			!strings.Contains(u.Username, filter.Keyword) &&
			!strings.Contains(u.RealName, filter.Keyword) {
			continue
		}
		if filter.Status != "" && u.Status != filter.Status {
			continue
		}
		if filter.DepartmentID > 0 && (u.DepartmentID == nil || u.DepartmentID.Int64() != filter.DepartmentID) {
			continue
		}
		if filter.OnlyUserID > 0 && u.ID.Int64() != filter.OnlyUserID {
			continue
		}
		out = append(out, cloneUser(u))
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) InsertUser(_ context.Context, _ *gorm.DB, u *User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.byUsername[u.Username]; dup {
		return fmt.Errorf("duplicate username（测试替身模拟唯一冲突）")
	}
	u.ID = database.ID(f.nextID())
	f.users[u.ID.Int64()] = u
	f.byUsername[u.Username] = u.ID.Int64()
	return nil
}

var userColSetters = map[string]func(u *User, v any){
	"real_name":            func(u *User, v any) { u.RealName = v.(string) },
	"phone":                func(u *User, v any) { u.Phone = v.(string) },
	"email":                func(u *User, v any) { u.Email = v.(string) },
	"data_scope":           func(u *User, v any) { u.DataScope = v.(string) },
	"status":               func(u *User, v any) { u.Status = v.(string) },
	"password_hash":        func(u *User, v any) { u.PasswordHash = v.(string) },
	"must_change_password": func(u *User, v any) { u.MustChangePassword = v.(bool) },
	"last_login_ip":        func(u *User, v any) { u.LastLoginIP = v.(string) },
	"locked_until": func(u *User, v any) {
		if v == nil {
			u.LockedUntil = database.JSONTime{}
			return
		}
		u.LockedUntil = v.(database.JSONTime)
	},
	"last_login_at": func(u *User, v any) { u.LastLoginAt = v.(database.JSONTime) },
	"department_id": func(u *User, v any) {
		if v == nil {
			u.DepartmentID = nil
			return
		}
		id := v.(database.ID)
		u.DepartmentID = &id
	},
	"updated_by": func(u *User, v any) { u.UpdatedBy = v.(database.ID) },
}

func (f *fakeRepo) UpdateUserCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return fmt.Errorf("用户 %d 不存在（测试替身）", id)
	}
	for k, v := range cols {
		if set, ok := userColSetters[k]; ok {
			set(u, v)
		}
	}
	return nil
}

func (f *fakeRepo) LockUser(ctx context.Context, id int64, until time.Time) error {
	f.mu.Lock()
	f.lockCalls++
	f.lockCallsUntil = until
	f.mu.Unlock()
	return f.UpdateUserCols(ctx, nil, id, map[string]any{
		"locked_until": database.JSONTime{Time: until},
	})
}

func (f *fakeRepo) CountUsersByDept(_ context.Context, deptID int64, onlyActive bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, u := range f.users {
		if u.DepartmentID != nil && u.DepartmentID.Int64() == deptID {
			if onlyActive && u.Status != UserStatusActive {
				continue
			}
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListRoleIDsByUser(_ context.Context, uid int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]int64(nil), f.userRoles[uid]...)
	return out, nil
}

func (f *fakeRepo) ListRoleIDsByUsers(_ context.Context, uids []int64) (map[int64][]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int64][]int64, len(uids))
	for _, uid := range uids {
		out[uid] = append([]int64(nil), f.userRoles[uid]...)
	}
	return out, nil
}

func (f *fakeRepo) ListRoleByCodeForUser(_ context.Context, uid int64, roleCode string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rid := range f.userRoles[uid] {
		if r, ok := f.roles[rid]; ok && r.Code == roleCode {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) DeleteUserRoles(_ context.Context, _ *gorm.DB, uid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.userRoles, uid)
	return nil
}

func (f *fakeRepo) InsertUserRoles(_ context.Context, _ *gorm.DB, uid int64, roleIDs []int64, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userRoles[uid] = append(f.userRoles[uid], roleIDs...)
	return nil
}

func (f *fakeRepo) ListWarehouseIDsByUser(_ context.Context, uid int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]int64(nil), f.userWhs[uid]...)
	return out, nil
}

func (f *fakeRepo) DeleteUserWarehouses(_ context.Context, _ *gorm.DB, uid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.userWhs, uid)
	return nil
}

func (f *fakeRepo) InsertUserWarehouses(_ context.Context, _ *gorm.DB, uid int64, whIDs []int64, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userWhs[uid] = append(f.userWhs[uid], whIDs...)
	return nil
}

func (f *fakeRepo) CountUsersByRole(_ context.Context, roleID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, rids := range f.userRoles {
		for _, r := range rids {
			if r == roleID {
				n++
			}
		}
	}
	return n, nil
}

func (f *fakeRepo) ListUserIDsByRole(_ context.Context, roleID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int64
	for uid, rids := range f.userRoles {
		for _, r := range rids {
			if r == roleID {
				out = append(out, uid)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) FindRoleByID(_ context.Context, id int64) (*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeRepo) FindRoleByCode(_ context.Context, code string) (*Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.roleByCode[code]
	if !ok {
		return nil, nil
	}
	cp := *f.roles[id]
	return &cp, nil
}

func (f *fakeRepo) ListRoles(_ context.Context, filter RoleListFilter) ([]*Role, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Role
	for _, r := range f.roles {
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" &&
			!strings.Contains(r.Code, filter.Keyword) && !strings.Contains(r.Name, filter.Keyword) {
			continue
		}
		cp := *r
		out = append(out, &cp)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) InsertRole(_ context.Context, _ *gorm.DB, r *Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.roleByCode[r.Code]; dup {
		return fmt.Errorf("duplicate role code（测试替身模拟唯一冲突）")
	}
	r.ID = database.ID(f.nextID())
	f.roles[r.ID.Int64()] = r
	f.roleByCode[r.Code] = r.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateRoleCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[id]
	if !ok {
		return fmt.Errorf("角色 %d 不存在（测试替身）", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			r.Name = v.(string)
		case "status":
			r.Status = v.(string)
		case "updated_by":
			r.UpdatedBy = v.(database.ID)
		}
	}
	return nil
}

func (f *fakeRepo) CountEnabledRolesByIDs(_ context.Context, ids []int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, id := range ids {
		if r, ok := f.roles[id]; ok && r.Status == StatusEnabled {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListPermissions(_ context.Context, filter PermissionListFilter) ([]*Permission, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Permission
	for _, p := range f.perms {
		if filter.Status != "" && p.Status != filter.Status {
			continue
		}
		if filter.Type != "" && p.Type != filter.Type {
			continue
		}
		if filter.Keyword != "" &&
			!strings.Contains(p.Code, filter.Keyword) && !strings.Contains(p.Name, filter.Keyword) {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) ListPermissionIDsByRole(_ context.Context, roleID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]int64(nil), f.rolePerms[roleID]...)
	return out, nil
}

func (f *fakeRepo) CountEnabledPermissionsByIDs(_ context.Context, ids []int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, id := range ids {
		if p, ok := f.perms[id]; ok && p.Status == StatusEnabled {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ReplaceRolePermissions(_ context.Context, _ *gorm.DB, roleID int64, permIDs []int64, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rolePermReplaced[roleID]++
	f.rolePerms[roleID] = append([]int64(nil), permIDs...)
	return nil
}

func (f *fakeRepo) ListPermissionCodesByUser(_ context.Context, uid int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errPermCodes {
		return nil, fmt.Errorf("fake repo: 权限查询故障注入")
	}
	seen := map[int64]bool{}
	var codes []string
	for _, rid := range f.userRoles[uid] {
		r, ok := f.roles[rid]
		if !ok || r.Status != StatusEnabled {
			continue
		}
		for _, pid := range f.rolePerms[rid] {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			if p, ok := f.perms[pid]; ok && p.Status == StatusEnabled {
				codes = append(codes, p.Code)
			}
		}
	}
	return codes, nil
}

// ListPermissionCodesByRole 角色绑定的启用权限点编码（授予侧边界校验替身，S1）。
func (f *fakeRepo) ListPermissionCodesByRole(_ context.Context, roleID int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[int64]bool{}
	var codes []string
	for _, pid := range f.rolePerms[roleID] {
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if p, ok := f.perms[pid]; ok && p.Status == StatusEnabled {
			codes = append(codes, p.Code)
		}
	}
	return codes, nil
}

// ListPermissionCodesByIDs 指定权限点集合中的启用权限点编码（授予侧边界校验替身，S1）。
func (f *fakeRepo) ListPermissionCodesByIDs(_ context.Context, ids []int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var codes []string
	for _, id := range ids {
		if p, ok := f.perms[id]; ok && p.Status == StatusEnabled {
			codes = append(codes, p.Code)
		}
	}
	return codes, nil
}

func (f *fakeRepo) FindDeptByID(_ context.Context, id int64) (*Department, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.depts[id]
	if !ok {
		return nil, nil
	}
	cp := *d
	if d.ParentID != nil {
		p := *d.ParentID
		cp.ParentID = &p
	}
	return &cp, nil
}

func (f *fakeRepo) FindDeptByCode(_ context.Context, code string) (*Department, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.deptByCode[code]
	if !ok {
		return nil, nil
	}
	cp := *f.depts[id]
	if f.depts[id].ParentID != nil {
		p := *f.depts[id].ParentID
		cp.ParentID = &p
	}
	return &cp, nil
}

func (f *fakeRepo) ListDepts(_ context.Context, filter DeptListFilter) ([]*Department, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*Department
	for _, d := range f.depts {
		if filter.Status != "" && d.Status != filter.Status {
			continue
		}
		if filter.Keyword != "" &&
			!strings.Contains(d.Code, filter.Keyword) && !strings.Contains(d.Name, filter.Keyword) {
			continue
		}
		cp := *d
		out = append(out, &cp)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) InsertDept(_ context.Context, _ *gorm.DB, d *Department) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.deptByCode[d.Code]; dup {
		return fmt.Errorf("duplicate dept code（测试替身模拟唯一冲突）")
	}
	d.ID = database.ID(f.nextID())
	f.depts[d.ID.Int64()] = d
	f.deptByCode[d.Code] = d.ID.Int64()
	return nil
}

func (f *fakeRepo) UpdateDeptCols(_ context.Context, _ *gorm.DB, id int64, cols map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.depts[id]
	if !ok {
		return fmt.Errorf("部门 %d 不存在（测试替身）", id)
	}
	for k, v := range cols {
		switch k {
		case "name":
			d.Name = v.(string)
		case "status":
			d.Status = v.(string)
		case "parent_id":
			if v == nil {
				d.ParentID = nil
			} else {
				idv := v.(database.ID)
				d.ParentID = &idv
			}
		case "updated_by":
			d.UpdatedBy = v.(database.ID)
		}
	}
	return nil
}

func (f *fakeRepo) CountEnabledChildDepts(_ context.Context, parentID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, d := range f.depts {
		if d.ParentID != nil && d.ParentID.Int64() == parentID && d.Status == StatusEnabled {
			n++
		}
	}
	return n, nil
}
