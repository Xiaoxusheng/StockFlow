package database

// 首次启动安全初始化（database.md §8.1、backend-m1-plan.md §7.5）。
//
// 职责落位（Orchestrator 裁决 2026-10-02）：生产安全初始化在本文件实现并接线于
// cmd/server/main.go；internal/auth.BootstrapIfEmpty / internal/warehouse.EnsureDefaultWarehouse
// 为冻结 stub 签名（plan §5.1/§5.2），对应域实现交付时应委托本入口，禁止出现第二套种子逻辑。
//
// 行为契约：
//   - 仅空库（users 表为空）首次启动执行；幂等：全部 INSERT 带 ON CONFLICT DO NOTHING，
//     重复启动不重复插入、不覆盖已有数据（database.md §8.1）；
//   - 事务 + pg_advisory_xact_lock 串行化多副本并发首启，后到者检查 users 非空后跳过；
//   - 默认管理员初始密码经环境变量 SF_ADMIN_INITIAL_PASSWORD 注入（cmd 传入本函数）：
//     缺失或弱密码即启动失败（deployment.md §3 禁止带病启动）；哈希 bcrypt cost 12；
//     创建后 must_change_password=TRUE 强制修改（database.md §8.1）；
//   - 种子内容：16 内置角色（permission.md §1，is_system 禁删）、M1 全量权限点
//     （backend-m1-plan §5.4.1 冻结清单：24 个 MENU 菜单 + 82 个动作点，MENU 即"默认菜单"
//     的落位）、默认管理员（绑定 super_admin）、默认仓库示例（database.md §8.1）；
//   - 演示数据完全分离：db/seed/dev_seed.sql + make seed-demo（仅 SF_ENV=dev），
//     任何启动路径不加载（database.md §8.2）；
//   - 日志红线：任何日志/错误不携带密码与哈希（architecture.md §6）。

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	// AdminUsername 默认管理员账号。
	AdminUsername = "admin"

	// BcryptCost 密码哈希强度（backend-m1-plan §7.3：bcrypt cost 12）。
	BcryptCost = 12

	// DefaultWarehouseCode 默认仓库示例编码（database.md §8.1“默认仓库示例”）。
	DefaultWarehouseCode = "WH-DEFAULT"

	// bootstrapLockKey pg_advisory_xact_lock 的键（"StockFlo" 前八字节的整型表示，任意固定值即可）。
	bootstrapLockKey int64 = 0x53746F636B466C6F
)

// BootstrapResult 初始化结果（cmd 据此记业务日志；不含任何敏感值）。
type BootstrapResult struct {
	RolesSeeded             int  // 本次实际插入的内置角色数
	PermissionsSeeded       int  // 本次实际插入的权限点数
	RoleBindingsSeeded      int  // 本次实际插入的角色-权限绑定数
	AdminCreated            bool // 是否本次创建了默认管理员（日志只记事件本身）
	DefaultWarehouseCreated bool // 是否本次创建了默认仓库示例
}

// ---- 种子数据（冻结清单，修改须先改 backend-m1-plan §5.4.1/§7.1 再改这里） ----

// permActionNames 动作词 → 中文名（backend-m1-plan §5.4.1 冻结动作枚举，禁止发明清单外动作词）。
var permActionNames = map[string]string{
	"list":              "列表",
	"read":              "详情",
	"create":            "新建",
	"update":            "编辑",
	"delete":            "删除",
	"status":            "启停",
	"assign-role":       "绑定角色",
	"assign-permission": "绑定权限",
	"reset-password":    "重置密码",
	"unlock":            "解锁",
	"kick":              "强制下线",
}

// permResource 权限资源及其动作全集（backend-m1-plan §5.4.1 全量冻结清单，auth 种子同源）。
type permResource struct {
	Code    string
	Name    string
	Actions []string
}

var permResources = []permResource{
	{"auth:user", "用户管理", []string{"list", "read", "create", "update", "status", "assign-role", "reset-password", "unlock"}},
	{"auth:role", "角色管理", []string{"list", "read", "create", "update", "status", "assign-permission"}},
	{"auth:permission", "权限点", []string{"list"}},
	{"auth:dept", "部门管理", []string{"list", "read", "create", "update", "status"}},
	{"auth:session", "在线会话", []string{"list", "kick"}},
	{"masterdata:product", "商品", []string{"list", "read", "create", "update", "delete", "status"}},
	{"masterdata:sku", "SKU", []string{"list", "read", "create", "update", "delete", "status"}},
	{"masterdata:category", "商品分类", []string{"list", "read", "create", "update", "status"}},
	{"masterdata:unit", "计量单位", []string{"list", "read", "create", "update", "status"}},
	{"masterdata:supplier", "供应商", []string{"list", "read", "create", "update", "delete", "status"}},
	{"masterdata:customer", "客户", []string{"list", "read", "create", "update", "delete", "status"}},
	{"warehouse:warehouse", "仓库", []string{"list", "read", "create", "update", "delete", "status"}},
	{"warehouse:zone", "库区", []string{"list", "read", "create", "update", "status"}},
	{"warehouse:shelf", "货架", []string{"list", "read", "create", "update", "status"}},
	{"warehouse:bin", "库位", []string{"list", "read", "create", "update", "delete", "status"}},
	{"inventory:inventory", "实时库存", []string{"list"}},
	{"inventory:ledger", "库存流水", []string{"list"}},
	{"inventory:batch", "批次台账", []string{"list"}},
	{"inventory:serial", "序列号", []string{"list"}},
}

// menuSeed 菜单权限点：MENU 类型即 database.md §8.1“默认菜单”的落位（plan §7.5），
// 菜单树与 §5.4.1 资源清单同源——叶子菜单编码复用资源编码。父节点必须先于子节点排列。
type menuSeed struct {
	Code   string
	Name   string
	Parent string // 空 = 顶级
	Sort   int
}

var menuSeeds = []menuSeed{
	{"menu:dashboard", "Dashboard", "", 0},
	{"menu:inventory", "库存中心", "", 10},
	{"inventory:inventory", "实时库存", "menu:inventory", 1},
	{"inventory:ledger", "库存流水", "menu:inventory", 2},
	{"inventory:batch", "批次台账", "menu:inventory", 3},
	{"inventory:serial", "序列号", "menu:inventory", 4},
	{"menu:warehouse", "仓库中心", "", 20},
	{"warehouse:warehouse", "仓库", "menu:warehouse", 1},
	{"warehouse:zone", "库区", "menu:warehouse", 2},
	{"warehouse:shelf", "货架", "menu:warehouse", 3},
	{"warehouse:bin", "库位", "menu:warehouse", 4},
	{"menu:masterdata", "基础资料", "", 30},
	{"masterdata:product", "商品", "menu:masterdata", 1},
	{"masterdata:sku", "SKU", "menu:masterdata", 2},
	{"masterdata:category", "商品分类", "menu:masterdata", 3},
	{"masterdata:unit", "计量单位", "menu:masterdata", 4},
	{"masterdata:supplier", "供应商", "menu:masterdata", 5},
	{"masterdata:customer", "客户", "menu:masterdata", 6},
	{"menu:system", "系统管理", "", 40},
	{"auth:user", "用户", "menu:system", 1},
	{"auth:role", "角色", "menu:system", 2},
	{"auth:permission", "权限点", "menu:system", 3},
	{"auth:dept", "部门", "menu:system", 4},
	{"auth:session", "在线会话", "menu:system", 5},
}

// systemRole 内置角色（permission.md §1 的 16 个角色全部种子化，is_system=TRUE 禁删）。
type systemRole struct{ Code, Name string }

var systemRoles = []systemRole{
	{"super_admin", "超级管理员"},
	{"sys_admin", "系统管理员"},
	{"warehouse_manager", "仓库经理"},
	{"warehouse_operator", "仓库管理员"},
	{"receiver", "收货员"},
	{"putaway_operator", "上架员"},
	{"picker", "拣货员"},
	{"checker", "复核员"},
	{"packer", "打包员"},
	{"shipper", "发货员"},
	{"stocktaker", "盘点员"},
	{"inspector", "质检员"},
	{"purchaser", "采购人员"},
	{"salesperson", "销售人员"},
	{"viewer", "财务/查看人员"},
	{"user", "普通用户"},
}

// permissionSeed 权限点：三级覆盖（permission.md §2）——
// MENU=页面级；BUTTON=操作级（写动作，同一编码同时保护按钮与 API）；API=接口级（list/read）。
type permissionSeed struct {
	Code   string
	Name   string
	Type   string // MENU / BUTTON / API
	Parent string // 空 = 无父级；动作点挂在资源菜单节点下
	Sort   int
}

// buildPermissionSeeds 汇总全部权限点：24 个菜单 + 82 个动作点 = 106 行（冻结清单，
// 含 2026-10-02 复核补录的 inventory:batch / inventory:serial 两资源）。
// 纯函数，供种子写入与单元测试共用（seed_test.go 以字面冻结清单交叉核对）。
func buildPermissionSeeds() []permissionSeed {
	seeds := make([]permissionSeed, 0, len(menuSeeds)+len(permResources)*6)
	for _, m := range menuSeeds {
		seeds = append(seeds, permissionSeed{Code: m.Code, Name: m.Name, Type: "MENU", Parent: m.Parent, Sort: m.Sort})
	}
	for _, r := range permResources {
		for i, a := range r.Actions {
			typ := "BUTTON" // 写动作 = 操作级
			if a == "list" || a == "read" {
				typ = "API" // 查询动作 = 接口级
			}
			seeds = append(seeds, permissionSeed{
				Code:   r.Code + ":" + a,
				Name:   r.Name + permActionNames[a],
				Type:   typ,
				Parent: r.Code,
				Sort:   i + 1,
			})
		}
	}
	return seeds
}

// rolePermissionCodes 返回角色应绑定的权限编码集合（plan §7.1：M1 仅对超级管理员、
// 系统管理员、仓库管理员、财务/查看人员四个角色配置映射，其余角色随 M2 业务域补配，
// 避免为不存在的页面造权限映射）。纯函数，供种子写入与单元测试共用。
func rolePermissionCodes(role string) []string {
	seeds := buildPermissionSeeds()
	all := make([]string, 0, len(seeds))
	menuCodes := make(map[string]bool, len(menuSeeds))
	for _, s := range seeds {
		all = append(all, s.Code)
		if s.Type == "MENU" {
			menuCodes[s.Code] = true
		}
	}
	readOnly := func(code string) bool {
		return strings.HasSuffix(code, ":list") || strings.HasSuffix(code, ":read")
	}

	switch role {
	case "super_admin", "sys_admin":
		return all

	case "warehouse_operator": // 仓库管理员：仓库 + 库存查看（plan §7.1）
		allowed := map[string]bool{
			"menu:dashboard":           true,
			"menu:inventory":           true,
			"inventory:inventory":      true,
			"inventory:inventory:list": true,
			"inventory:ledger":         true,
			"inventory:ledger:list":    true,
			"menu:warehouse":           true,
			"warehouse:warehouse":      true,
			"warehouse:zone":           true,
			"warehouse:shelf":          true,
			"warehouse:bin":            true,
		}
		for _, res := range []string{"warehouse:warehouse", "warehouse:zone", "warehouse:shelf", "warehouse:bin"} {
			allowed[res+":list"] = true
			allowed[res+":read"] = true
		}
		out := make([]string, 0, len(allowed))
		for _, c := range all {
			if allowed[c] {
				out = append(out, c)
			}
		}
		return out

	case "viewer": // 财务/查看人员：只读（全部菜单 + list/read）
		// 安全审查 S9：auth:user:list/read 指向用户目录（含手机号/邮箱等 PII），
		// 不授权给查看类角色——仅管理类角色（super_admin/sys_admin）可见。
		out := make([]string, 0, len(all))
		for _, c := range all {
			if c == "auth:user:list" || c == "auth:user:read" {
				continue
			}
			if menuCodes[c] || readOnly(c) {
				out = append(out, c)
			}
		}
		return out

	default:
		return nil // 其余 12 个内置角色 M1 不配映射（随 M2 业务域补配）
	}
}

// validateAdminInitialPassword 管理员初始密码校验——强密码策略（安全审查 S3 收紧）：
// 长度 ≥ 12 且至少包含大写字母/小写字母/数字/符号四类中的三类
// （permission.md §3.2、backend-m1-plan §7.3、deployment.md §1.1/§6）。
// 错误信息绝不回显密码内容。
func validateAdminInitialPassword(pw string) error {
	if pw == "" {
		return errors.New("环境变量 SF_ADMIN_INITIAL_PASSWORD 未设置：空库首次启动必须注入管理员初始密码（deployment.md §3 禁止带病启动）")
	}
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	classes := 0
	for _, hit := range []bool{hasUpper, hasLower, hasDigit, hasSymbol} {
		if hit {
			classes++
		}
	}
	if len(pw) < 12 || classes < 3 {
		return errors.New("管理员初始密码不满足强密码策略：长度至少 12 位且必须包含大小写字母/数字/符号中的至少三类")
	}
	return nil
}

// BootstrapIfEmpty 首次启动安全初始化入口（见文件头注释）。空库返回种子结果；已初始化的库
// 幂等返回零值结果，不做任何写入。adminPassword 为 SF_ADMIN_INITIAL_PASSWORD 注入值，
// 仅在空库分支消费，绝不写入任何日志。
func BootstrapIfEmpty(db *gorm.DB, adminPassword string) (BootstrapResult, error) {
	if db == nil {
		return BootstrapResult{}, errors.New("首次启动安全初始化失败：数据库连接为空")
	}

	var res BootstrapResult
	err := db.Transaction(func(tx *gorm.DB) error {
		// 并发首启串行化：事务级咨询锁（事务结束自动释放）。
		// 后到者阻塞至先到者提交，再检查 users 非空而跳过——杜绝重复种子与竞态。
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", bootstrapLockKey).Error; err != nil {
			return fmt.Errorf("获取初始化咨询锁失败: %w", err)
		}

		var userCount int64
		if err := tx.Raw("SELECT COUNT(*) FROM users").Scan(&userCount).Error; err != nil {
			return fmt.Errorf("检查 users 表失败（迁移未执行或库不可用）: %w", err)
		}
		if userCount > 0 {
			return nil // 已初始化：不覆盖任何已有数据（database.md §8.1）
		}

		// 空库才需要初始密码；缺失/弱密码在此失败，进程经 cmd 快速退出。
		if err := validateAdminInitialPassword(adminPassword); err != nil {
			return err
		}
		return seedBootstrapData(tx, adminPassword, &res)
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	return res, nil
}

// seedBootstrapData 在单事务内完成全部种子（角色/权限点/角色绑定/管理员/默认仓库）。
// 说明：循环内为单行幂等 INSERT，仅限一次性引导事务（架构 §7 的“禁止循环内查询”
// 针对业务热路径，此处为 bootstrap 一次性写入，规模 ≤ 102 行，换取逐行可定位的错误信息）。
func seedBootstrapData(tx *gorm.DB, adminPassword string, res *BootstrapResult) error {
	// 1) 16 内置角色（批量 + ON CONFLICT DO NOTHING，逗号分隔经 unnest 展开——
	//    编码/名称均不含逗号，见 systemRoles 定义）
	roleCodes := make([]string, 0, len(systemRoles))
	roleNames := make([]string, 0, len(systemRoles))
	for _, r := range systemRoles {
		roleCodes = append(roleCodes, r.Code)
		roleNames = append(roleNames, r.Name)
	}
	ra := tx.Exec(`INSERT INTO roles (code, name, is_system, status, created_at, updated_at, created_by, updated_by)
		SELECT c, n, TRUE, 'ENABLED', now(), now(), 0, 0
		FROM unnest(string_to_array(?, ','), string_to_array(?, ',')) AS t(c, n)
		ON CONFLICT (code) DO NOTHING`,
		strings.Join(roleCodes, ","), strings.Join(roleNames, ",")).RowsAffected
	res.RolesSeeded = int(ra)

	// 2) 权限点：逐行 INSERT ... RETURNING id 以构建 code→id 映射做父子挂接
	//    （menuSeeds 父先子后；动作点父级=资源菜单节点，保证已入映射）
	permIDs := make(map[string]int64, 128)
	for _, p := range buildPermissionSeeds() {
		var parentID any
		if p.Parent != "" {
			id, ok := permIDs[p.Parent]
			if !ok {
				return fmt.Errorf("种子权限点 %s 失败：父级 %s 未就绪（menuSeeds 必须父先子后）", p.Code, p.Parent)
			}
			parentID = id
		}
		var id int64
		err := tx.Raw(`INSERT INTO permissions (code, name, type, parent_id, sort, status, created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, 'ENABLED', now(), now(), 0, 0)
			ON CONFLICT (code) DO NOTHING
			RETURNING id`,
			p.Code, p.Name, p.Type, parentID, p.Sort).Row().Scan(&id)
		switch {
		case err == nil:
			permIDs[p.Code] = id
			res.PermissionsSeeded++
		case errors.Is(err, sql.ErrNoRows):
			// 已存在（部分初始化的残留数据）：跳过，不覆盖
		default:
			return fmt.Errorf("种子权限点 %s 失败: %w", p.Code, err)
		}
	}

	// 3) 角色绑定权限点（M1 四角色映射，plan §7.1）
	bindRole := func(roleCode string, permCodes []string) error {
		if len(permCodes) == 0 {
			return nil
		}
		var roleID int64
		if err := tx.Raw(`SELECT id FROM roles WHERE code = ?`, roleCode).Row().Scan(&roleID); err != nil {
			return fmt.Errorf("查询内置角色 %s 失败: %w", roleCode, err)
		}
		bound := tx.Exec(`INSERT INTO role_permissions (role_id, permission_id, created_at, created_by)
			SELECT ?, p.id, now(), 0
			FROM permissions p
			WHERE p.code = ANY (string_to_array(?, ','))
			ON CONFLICT DO NOTHING`,
			roleID, strings.Join(permCodes, ",")).RowsAffected
		res.RoleBindingsSeeded += int(bound)
		return nil
	}
	for _, roleCode := range []string{"super_admin", "sys_admin", "warehouse_operator", "viewer"} {
		if err := bindRole(roleCode, rolePermissionCodes(roleCode)); err != nil {
			return err
		}
	}

	// 4) 默认管理员（仅空库分支到达此处；密码仅用于生成哈希，绝不入库外任何地方）
	pwHash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), BcryptCost)
	if err != nil {
		return fmt.Errorf("生成管理员密码哈希失败: %w", err)
	}
	ra = tx.Exec(`INSERT INTO users (username, password_hash, real_name, data_scope, status, must_change_password, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, '系统管理员', 'ALL', 'ACTIVE', TRUE, now(), now(), 0, 0)
		ON CONFLICT (username) WHERE deleted_at IS NULL DO NOTHING`,
		AdminUsername, string(pwHash)).RowsAffected
	if ra > 0 {
		res.AdminCreated = true
		// 绑定超级管理员角色（角色随第 1 步保证存在；ON CONFLICT 幂等）
		if err := tx.Exec(`INSERT INTO user_roles (user_id, role_id, created_at, created_by)
			SELECT u.id, r.id, now(), 0
			FROM users u, roles r
			WHERE u.username = ? AND u.deleted_at IS NULL AND r.code = 'super_admin'
			ON CONFLICT DO NOTHING`, AdminUsername).Error; err != nil {
			return fmt.Errorf("绑定默认管理员角色失败: %w", err)
		}
	}

	// 5) 默认仓库示例（database.md §8.1；warehouse 域实现交付后如 EnsureDefaultWarehouse
	//    委托本入口，此处幂等语义不变）
	var whCount int64
	if err := tx.Raw("SELECT COUNT(*) FROM warehouses").Scan(&whCount).Error; err != nil {
		return fmt.Errorf("检查 warehouses 表失败: %w", err)
	}
	if whCount == 0 {
		ra = tx.Exec(`INSERT INTO warehouses (code, name, type, status, created_at, updated_at, created_by, updated_by)
			VALUES (?, '默认仓库', 'NORMAL', 'ENABLED', now(), now(), 0, 0)
			ON CONFLICT (code) WHERE deleted_at IS NULL DO NOTHING`, DefaultWarehouseCode).RowsAffected
		res.DefaultWarehouseCreated = ra > 0
	}

	return nil
}
