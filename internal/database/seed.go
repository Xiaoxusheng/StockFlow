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
//   - 种子内容：16 内置角色（permission.md §1，is_system 禁删）、全量权限点
//     （M1：backend-m1-plan §5.4.1 冻结清单；M2：backend-m2-plan §9.2 冻结清单；
//     M3：backend-m3-plan §11 冻结清单——合计 63 个 MENU 菜单 + 229 个动作点，
//     MENU 即"默认菜单"的落位）、默认管理员（绑定 super_admin）、默认仓库示例
//     （database.md §8.1）；
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

// permActionNames 动作词 → 中文名。M1 基础枚举（backend-m1-plan §5.4.1）+ M2 作业/审批
// 动作（backend-m2-plan §9.1 冻结扩展，禁止发明清单外动作词）。
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
	// —— M2（backend-m2-plan §9.1）——
	"submit":  "提交审核",
	"approve": "审核",
	"cancel":  "取消",
	"close":   "关闭",
	"execute": "作业执行",
	"claim":   "任务领取",
	"assign":  "异常分派",
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
	// —— M2 单据域（backend-m2-plan §9.2 冻结清单，域按 api.md §1 领域划分）——
	{"purchase:purchase", "采购订单", []string{"list", "read", "create", "update", "submit", "approve", "cancel", "close"}},
	{"purchase:inbound", "入库单", []string{"list", "read", "create", "update", "cancel", "close"}},
	{"purchase:receipt", "收货单", []string{"list", "read", "execute"}},
	{"purchase:putaway", "上架任务", []string{"list", "read", "claim", "execute"}},
	{"purchase:quality", "质检单", []string{"list", "read", "create", "execute"}},
	{"sales:sales", "销售订单", []string{"list", "read", "create", "update", "submit", "approve", "cancel", "close"}},
	{"sales:outbound", "出库单", []string{"list", "read", "create", "cancel", "close"}},
	{"sales:allocation", "库存分配", []string{"list", "read", "create", "execute"}},
	{"sales:pick", "拣货任务", []string{"list", "read", "claim", "execute"}},
	{"sales:check", "复核任务", []string{"list", "read", "claim", "execute"}},
	{"sales:packing", "打包记录", []string{"list", "read", "execute"}},
	{"sales:shipment", "发货单", []string{"list", "read", "execute"}},
	{"stockops:transfer", "调拨单", []string{"list", "read", "create", "update", "submit", "approve", "execute", "cancel", "close"}},
	{"stockops:count", "盘点单", []string{"list", "read", "create", "execute", "approve", "cancel", "close"}},
	{"stockops:adjustment", "库存调整", []string{"list", "read", "create", "approve", "execute", "cancel"}},
	{"stockops:move", "仓内移库", []string{"list", "execute"}},
	{"inventory:lock", "库存锁定", []string{"list"}},
	{"returns:salesreturn", "销售退货", []string{"list", "read", "create", "update", "submit", "approve", "execute", "cancel", "close"}},
	{"returns:purchasereturn", "采购退货", []string{"list", "read", "create", "update", "submit", "approve", "execute", "cancel", "close"}},
	{"returns:exception", "异常中心", []string{"list", "read", "create", "assign", "execute", "close"}},
	{"returns:trace", "库存追溯", []string{"list"}},
	// —— M3 平台域（backend-m3-plan §11.1 冻结清单，动作词沿用 M1/M2 冻结枚举零新增）——
	{"datax:import", "Excel 导入", []string{"list", "read", "create", "execute"}},
	{"datax:export", "Excel 导出", []string{"list", "read", "create"}},
	{"datax:file", "文件中心", []string{"list", "read", "create", "delete"}},
	{"printing:template", "打印模板", []string{"list", "read", "create", "update", "status"}},
	{"printing:task", "打印任务", []string{"list", "read", "create", "execute"}},
	{"devices:device", "设备", []string{"list", "read", "create", "update", "status"}},
	{"devices:scanlog", "扫码日志", []string{"list", "read"}},
	{"scanner:resolve", "统一扫码解析", []string{"list"}},
	{"reports:report", "报表", []string{"list", "read"}},
	{"system:log", "审计日志", []string{"list", "read"}},
	{"system:job", "定时任务", []string{"list", "read", "status"}},
	{"system:config", "系统配置", []string{"list", "update"}},
	{"system:monitor", "系统监控", []string{"list"}},
	{"system:backup", "备份管理", []string{"list", "read", "create"}},
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
	// —— M2 单据域菜单（backend-m2-plan §9.3：menuSeeds 追加 M2 资源与菜单节点，
	//    叶子编码 = 资源编码，父先子后）——
	{"menu:purchase", "采购中心", "", 50},
	{"purchase:purchase", "采购订单", "menu:purchase", 1},
	{"purchase:inbound", "入库单", "menu:purchase", 2},
	{"purchase:receipt", "收货", "menu:purchase", 3},
	{"purchase:quality", "质检", "menu:purchase", 4},
	{"purchase:putaway", "上架", "menu:purchase", 5},
	{"menu:sales", "销售中心", "", 60},
	{"sales:sales", "销售订单", "menu:sales", 1},
	{"sales:outbound", "出库单", "menu:sales", 2},
	{"sales:allocation", "库存分配", "menu:sales", 3},
	{"sales:pick", "拣货", "menu:sales", 4},
	{"sales:check", "复核", "menu:sales", 5},
	{"sales:packing", "打包", "menu:sales", 6},
	{"sales:shipment", "发货", "menu:sales", 7},
	{"menu:stockops", "库存作业", "", 70},
	{"stockops:transfer", "调拨单", "menu:stockops", 1},
	{"stockops:count", "盘点单", "menu:stockops", 2},
	{"stockops:adjustment", "库存调整", "menu:stockops", 3},
	{"stockops:move", "仓内移库", "menu:stockops", 4},
	{"inventory:lock", "库存锁定", "menu:stockops", 5},
	{"menu:returns", "退货与异常", "", 80},
	{"returns:salesreturn", "销售退货", "menu:returns", 1},
	{"returns:purchasereturn", "采购退货", "menu:returns", 2},
	{"returns:exception", "异常中心", "menu:returns", 3},
	{"returns:trace", "库存追溯", "menu:returns", 4},
	// —— M3 平台域菜单（backend-m3-plan §11.2：数据中心 4 叶 / 设备中心 2 叶 /
	//    报表中心 1 叶 / 系统管理增 4 叶，叶子编码 = 资源编码，父先子后。
	//    对 §11.2 原文 6 叶设备中心与"系统管理含设备管理"的落位偏离记录见方案 §11.2 注：
	//    设备类型页（scanners/pda/pads/printers）共用 devices:device 一个资源点，
	//    登录日志与审计日志共用 system:log 资源点，叶编码唯一约束（seed_test 判重）
	//    下各落一叶，页面级拆分由前端对齐轮消化）——
	{"menu:datax", "数据中心", "", 90},
	{"datax:import", "Excel 导入", "menu:datax", 1},
	{"datax:export", "Excel 导出", "menu:datax", 2},
	{"printing:template", "打印中心", "menu:datax", 3},
	{"datax:file", "文件中心", "menu:datax", 4},
	{"menu:devices", "设备中心", "", 100},
	{"devices:device", "设备管理", "menu:devices", 1},
	{"devices:scanlog", "扫码日志", "menu:devices", 2},
	{"menu:reports", "报表中心", "", 110},
	{"reports:report", "报表", "menu:reports", 1},
	{"system:log", "审计日志", "menu:system", 6},
	{"system:job", "定时任务", "menu:system", 7},
	{"system:config", "系统配置", "menu:system", 8},
	{"system:monitor", "系统监控", "menu:system", 9},
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

// buildPermissionSeeds 汇总全部权限点：63 个菜单 + 229 个动作点 = 292 行
// （M1 冻结清单 24 菜单 + 82 动作点，M2 冻结清单 25 菜单 + 106 动作点，
// M3 冻结清单 14 菜单 + 41 动作点——backend-m1-plan §5.4.1 + backend-m2-plan §9.2/§9.3
// + backend-m3-plan §11，收编记录见 §11.4 注）。
// 纯函数，供种子写入与单元测试共用（seed_test.go 以字面冻结清单交叉核对）。
//
// 动作点父级仅指向「确有菜单叶子的资源」：printing:task/scanner:resolve/system:backup
// 按冻结设计无独立菜单叶子（seed_test.go：printing:task 无独立菜单叶子，菜单叶 =
// printing:template，§11.2 落位；m3MenuParents 注：scanner:resolve 是能力点非页面、
// 备份页挂系统管理组），其动作点落为顶级权限点（parent_id NULL，000001 迁移允许）。
// 若无条件挂 r.Code，空库首启 seedPermissions 必然误判「父级未就绪」中断引导
// （2026-10-04 本地全新库自举实测复现：printing:task:list 父级缺失）。
func buildPermissionSeeds() []permissionSeed {
	menuCodes := make(map[string]bool, len(menuSeeds))
	for _, m := range menuSeeds {
		menuCodes[m.Code] = true
	}
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
			parent := ""
			if menuCodes[r.Code] {
				parent = r.Code // 动作点挂在资源菜单节点下（该资源确有叶子时）
			}
			seeds = append(seeds, permissionSeed{
				Code:   r.Code + ":" + a,
				Name:   r.Name + permActionNames[a],
				Type:   typ,
				Parent: parent,
				Sort:   i + 1,
			})
		}
	}
	return seeds
}

// m2RoleGrant M2 角色映射规格（backend-m2-plan §9.3 冻结映射的落位形态）：
// 以资源为粒度声明授权动作——full=资源冻结清单全部动作；read=list+read；list=list；
// pick=显式动作子集；extra=裸权限编码。菜单可见性（叶子 + 顶级 + Dashboard）随资源
// 自动带出（叶子菜单编码 = 资源编码，§9.3"菜单与动作同源"口径），不逐条罗列菜单。
type m2RoleGrant struct {
	full  []string
	read  []string
	list  []string
	pick  map[string][]string
	extra []string
}

// m2MenuParents M2 四域菜单叶子的顶级父节点（与 menuSeeds 同源；inventory:lock 落
// 库存作业组）——角色映射编译时用于带出顶级菜单可见性。
var m2MenuParents = map[string]string{
	"purchase:purchase": "menu:purchase", "purchase:inbound": "menu:purchase",
	"purchase:receipt": "menu:purchase", "purchase:quality": "menu:purchase",
	"purchase:putaway": "menu:purchase",
	"sales:sales":      "menu:sales", "sales:outbound": "menu:sales", "sales:allocation": "menu:sales",
	"sales:pick": "menu:sales", "sales:check": "menu:sales",
	"sales:packing": "menu:sales", "sales:shipment": "menu:sales",
	"stockops:transfer": "menu:stockops", "stockops:count": "menu:stockops",
	"stockops:adjustment": "menu:stockops", "stockops:move": "menu:stockops",
	"inventory:lock":      "menu:stockops",
	"returns:salesreturn": "menu:returns", "returns:purchasereturn": "menu:returns",
	"returns:exception": "menu:returns", "returns:trace": "menu:returns",
}

// m2RoleGrants 12 个业务角色到 M2 资源的映射（backend-m2-plan §9.3 表格逐行落位；
// super_admin/sys_admin 全量、viewer 全只读、user 不配映射——三者走 switch 既有分支）。
var m2RoleGrants = map[string]m2RoleGrant{
	// 采购人员：采购订单全量 + 入库/质检只读 + 追溯/库存列表。
	// M3（§11.3）：+ datax:export:list/read/create + datax:file（全动作）+ reports:report:list/read。
	"purchaser": {
		full:  []string{"purchase:purchase", "datax:file"},
		read:  []string{"purchase:inbound", "purchase:quality", "datax:export", "reports:report"},
		list:  []string{"returns:trace", "inventory:inventory"},
		extra: []string{"datax:export:create"},
	},
	// 收货/上架/质检作业员：各自作业域全量 + 异常创建（§11.2 异常统一入口）。
	// M3（§11.3 仓内作业角色）：+ scanner:resolve + datax:file:list/read（附件查看）+ reports:report:list。
	"receiver": {
		full: []string{"purchase:receipt"},
		read: []string{"purchase:inbound", "datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	"putaway_operator": {
		full: []string{"purchase:putaway"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	"inspector": {
		full: []string{"purchase:quality"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	// 销售人员：销售订单全量 + 出库/分配只读。
	// M3（§11.3）：+ datax:export:list/read/create + datax:file（全动作）+ reports:report:list/read。
	"salesperson": {
		full:  []string{"sales:sales", "datax:file"},
		read:  []string{"sales:outbound", "sales:allocation", "datax:export", "reports:report"},
		extra: []string{"datax:export:create"},
	},
	// 拣货/复核/打包/发货作业员：各自任务域全量 + 异常创建。
	// M3（§11.3 仓内作业角色）：+ scanner:resolve + datax:file:list/read + reports:report:list。
	"picker": {
		full: []string{"sales:pick"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	"checker": {
		full: []string{"sales:check"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	"packer": {
		full: []string{"sales:packing"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	"shipper": {
		full: []string{"sales:shipment"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"returns:exception": {"create"}},
	},
	// 盘点员：盘点全量 + 库存调整创建/列表（差异执行走审批链，plan §6.8）。
	// M3（§11.3 仓内作业角色）：+ scanner:resolve + datax:file:list/read + reports:report:list。
	"stocktaker": {
		full: []string{"stockops:count"},
		read: []string{"datax:file"},
		list: []string{"scanner:resolve", "reports:report"},
		pick: map[string][]string{"stockops:adjustment": {"create", "list"}},
	},
}

// m2AllResources M2 全部资源（backend-m2-plan §9.2 表内资源编码；manager/operator 的
// "四域列表读"按本清单展开）。
var m2AllResources = []string{
	"purchase:purchase", "purchase:inbound", "purchase:receipt", "purchase:putaway", "purchase:quality",
	"sales:sales", "sales:outbound", "sales:allocation", "sales:pick", "sales:check", "sales:packing", "sales:shipment",
	"stockops:transfer", "stockops:count", "stockops:adjustment", "stockops:move", "inventory:lock",
	"returns:salesreturn", "returns:purchasereturn", "returns:exception", "returns:trace",
}

// m3MenuParents M3 平台域菜单叶子的顶级父节点（与 menuSeeds 同源；scanner:resolve 与
// system:backup 无独立菜单叶子——resolve 是能力点非页面、备份页挂系统管理组，
// 叶子编码 = 资源编码）。
var m3MenuParents = map[string]string{
	"datax:import": "menu:datax", "datax:export": "menu:datax",
	"printing:template": "menu:datax", "datax:file": "menu:datax",
	"devices:device": "menu:devices", "devices:scanlog": "menu:devices",
	"reports:report": "menu:reports",
	"system:log":     "menu:system", "system:job": "menu:system",
	"system:config": "menu:system", "system:monitor": "menu:system",
}

// m3AllResources M3 全部资源（backend-m3-plan §11.1 表内 14 资源编码；viewer 的
// "全部 M3 资源 list"与 viewer 排除 :read 明细点按本清单展开）。
var m3AllResources = []string{
	"datax:import", "datax:export", "datax:file",
	"printing:template", "printing:task",
	"devices:device", "devices:scanlog", "scanner:resolve",
	"reports:report",
	"system:log", "system:job", "system:config", "system:monitor", "system:backup",
}

// m3ManagerReadResources 仓库经理的 M3 "全 list+read" 资源（§11.3：datax/printing/
// reports/devices 全部资源；system:* 运维面不授）。
var m3ManagerReadResources = []string{
	"datax:import", "datax:export", "datax:file",
	"printing:template", "printing:task",
	"devices:device", "devices:scanlog", "scanner:resolve",
	"reports:report",
}

// isM3ReadDetail 判断权限编码是否为 M3 资源的 :read 明细点（§11.3 viewer 授权
// "全部 M3 资源 list"不含 read——M1/M2 资源 read 照旧授权）。
func isM3ReadDetail(code string) bool {
	if !strings.HasSuffix(code, ":read") {
		return false
	}
	res := code[:strings.LastIndex(code, ":")]
	for _, r := range m3AllResources {
		if r == res {
			return true
		}
	}
	return false
}

// compileRoleGrant 把映射规格编译为权限编码集合（动作取自 permResources 冻结动作集，
// 规格声明的动作词不在资源清单内时忽略——不发明清单外动作）。
func compileRoleGrant(spec m2RoleGrant) map[string]bool {
	actionsByRes := make(map[string][]string, len(permResources))
	for _, r := range permResources {
		actionsByRes[r.Code] = r.Actions
	}
	allowed := map[string]bool{"menu:dashboard": true}
	grantRes := func(res string, want []string) {
		if want != nil {
			set := make(map[string]bool, len(want))
			for _, a := range want {
				set[a] = true
			}
			for _, a := range actionsByRes[res] {
				if set[a] {
					allowed[res+":"+a] = true
				}
			}
		}
		if p, ok := m2MenuParents[res]; ok {
			allowed[p] = true // 顶级菜单
		}
		if p, ok := m3MenuParents[res]; ok {
			allowed[p] = true // M3 平台域顶级菜单（§11.2/§11.3）
		}
		allowed[res] = true // 叶子菜单（编码 = 资源编码）
	}
	for _, res := range spec.full {
		grantRes(res, actionsByRes[res]) // 全部动作
	}
	for _, res := range spec.read {
		grantRes(res, []string{"list", "read"})
	}
	for _, res := range spec.list {
		grantRes(res, []string{"list"})
	}
	for res, acts := range spec.pick {
		grantRes(res, acts)
	}
	for _, code := range spec.extra {
		allowed[code] = true
	}
	return allowed
}

// rolePermissionCodes 返回角色应绑定的权限编码集合。M1 四角色映射沿用（plan §7.1），
// M2 按方案 §9.3 扩展 12 个业务角色映射（super_admin/sys_admin 全量、viewer 全只读、
// user 不配业务映射）。纯函数，供种子写入与单元测试共用。
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

	case "viewer": // 财务/查看人员：只读（全部菜单 + list/read，含 M2 资源——§9.3 追加口径；
		// M3 资源仅 list 不含 read 明细点——backend-m3-plan §11.3）
		// 安全审查 S9：auth:user:list/read 指向用户目录（含手机号/邮箱等 PII），
		// 不授权给查看类角色——仅管理类角色（super_admin/sys_admin）可见。
		out := make([]string, 0, len(all))
		for _, c := range all {
			if c == "auth:user:list" || c == "auth:user:read" {
				continue
			}
			if isM3ReadDetail(c) {
				continue // §11.3：M3 资源仅授权 list，read 明细点不授
			}
			if menuCodes[c] || readOnly(c) {
				out = append(out, c)
			}
		}
		return out

	case "warehouse_operator": // 仓库管理员：M1 仓库+库存查看 + M2 四域列表读 + 移库执行（§9.3）
		m1 := map[string]bool{
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
			m1[res+":list"] = true
			m1[res+":read"] = true
		}
		m2 := compileRoleGrant(m2RoleGrant{
			read:  m2AllResources,
			extra: []string{"stockops:move:execute"},
		})
		for code := range m2 {
			m1[code] = true
		}
		// M3（backend-m3-plan §11.3）：scanner:resolve + datax:file（全动作，附件管理）
		// + reports:report:list。
		m3 := compileRoleGrant(m2RoleGrant{
			full: []string{"datax:file"},
			list: []string{"scanner:resolve", "reports:report"},
		})
		for code := range m3 {
			m1[code] = true
		}
		out := make([]string, 0, len(m1))
		for _, c := range all {
			if m1[c] {
				out = append(out, c)
			}
		}
		return out

	case "warehouse_manager": // 仓库经理：四域全部列表读 + 各域 approve + 调拨/盘点全量 + 移库执行（§9.3）
		// 调拨/盘点全量以库存可视性为前提，随 M1 库存查看基线（plan §7.1 仓库视图）补齐
		// inventory:inventory/ledger 两查询点——不扩及 masterdata/warehouse 管理动作。
		// M3（§11.3）：datax/printing/reports/devices 全 list+read + datax:export:create
		// + printing:template 全量 + devices:device:update；system:* 运维面不授。
		allowed := compileRoleGrant(m2RoleGrant{
			read: append(append([]string{}, m2AllResources...), m3ManagerReadResources...),
			full: []string{"stockops:transfer", "stockops:count", "printing:template"},
			pick: map[string][]string{
				"purchase:purchase":      {"approve"},
				"sales:sales":            {"approve"},
				"stockops:adjustment":    {"approve"},
				"returns:salesreturn":    {"approve"},
				"returns:purchasereturn": {"approve"},
			},
			extra: []string{"stockops:move:execute", "datax:export:create", "devices:device:update"},
		})
		for _, c := range []string{
			"menu:inventory", "inventory:inventory", "inventory:inventory:list",
			"inventory:ledger", "inventory:ledger:list",
		} {
			allowed[c] = true
		}
		out := make([]string, 0, len(allowed))
		for _, c := range all {
			if allowed[c] {
				out = append(out, c)
			}
		}
		return out

	default:
		if role == "user" {
			// 普通用户（backend-m3-plan §11.3）：仅统一扫码解析（经手任务需要）——
			// 个人通知为认证即可用（无权限点）；其余不配业务映射，不配 dashboard 菜单
			// （沿 M1/M2 user 零映射口径）。
			return []string{"scanner:resolve:list"}
		}
		if spec, ok := m2RoleGrants[role]; ok {
			allowed := compileRoleGrant(spec)
			out := make([]string, 0, len(allowed))
			for _, c := range all {
				if allowed[c] {
					out = append(out, c)
				}
			}
			return out
		}
		return nil // 其余未映射角色：仅菜单与各自经手任务（同 M1 口径）
	}
}

// FrozenSeedPermissionCodes 返回全部种子权限点编码（MENU + 动作点，冻结清单顺序）。
// 探针测试面（backend-m3-plan §11 收编模式：internal/database 外部测试包断言 auth
// 权限常量与种子同源，常量漏种/漂移即失败）；生产路径不消费。
func FrozenSeedPermissionCodes() []string {
	seeds := buildPermissionSeeds()
	out := make([]string, 0, len(seeds))
	for _, s := range seeds {
		out = append(out, s.Code)
	}
	return out
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

// permissionSeedGateway 权限点种子存储网关（seedPermissions 依赖注入点，
// 单测以内存假实现覆盖残留数据场景，不依赖 PostgreSQL）。
type permissionSeedGateway interface {
	// insertSeed 插入权限点行，返回新行 id；code 已存在（ON CONFLICT 命中）时返回 sql.ErrNoRows。
	insertSeed(p permissionSeed, parentID any) (int64, error)
	// selectIDByCode 回读残留行的 id（部分初始化后的幂等续跑依赖此回填）。
	selectIDByCode(code string) (int64, error)
}

// gormPermissionGateway permissionSeedGateway 的 PostgreSQL 实现（引导事务内逐行写入）。
type gormPermissionGateway struct {
	tx *gorm.DB
}

func (g gormPermissionGateway) insertSeed(p permissionSeed, parentID any) (int64, error) {
	var id int64
	err := g.tx.Raw(`INSERT INTO permissions (code, name, type, parent_id, sort, status, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, 'ENABLED', now(), now(), 0, 0)
		ON CONFLICT (code) DO NOTHING
		RETURNING id`,
		p.Code, p.Name, p.Type, parentID, p.Sort).Row().Scan(&id)
	return id, err
}

func (g gormPermissionGateway) selectIDByCode(code string) (int64, error) {
	var id int64
	err := g.tx.Raw(`SELECT id FROM permissions WHERE code = ?`, code).Row().Scan(&id)
	return id, err
}

// seedPermissions 逐行幂等写入权限点并构建 code→id 映射。
// 残留数据（同 code 行已存在，INSERT..RETURNING 返回 sql.ErrNoRows）：跳过不覆盖，
// 但回读 id 回填映射，保证其子权限不会误判「父级未就绪」而中断引导。
func seedPermissions(gw permissionSeedGateway, seeds []permissionSeed, res *BootstrapResult) (map[string]int64, error) {
	permIDs := make(map[string]int64, len(seeds))
	for _, p := range seeds {
		var parentID any
		if p.Parent != "" {
			id, ok := permIDs[p.Parent]
			if !ok {
				return nil, fmt.Errorf("种子权限点 %s 失败：父级 %s 未就绪（menuSeeds 必须父先子后）", p.Code, p.Parent)
			}
			parentID = id
		}
		id, err := gw.insertSeed(p, parentID)
		switch {
		case err == nil:
			permIDs[p.Code] = id
			res.PermissionsSeeded++
		case errors.Is(err, sql.ErrNoRows):
			existing, qerr := gw.selectIDByCode(p.Code)
			if qerr != nil {
				return nil, fmt.Errorf("回读残留权限点 %s 失败: %w", p.Code, qerr)
			}
			permIDs[p.Code] = existing
		default:
			return nil, fmt.Errorf("种子权限点 %s 失败: %w", p.Code, err)
		}
	}
	return permIDs, nil
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
	if _, err := seedPermissions(gormPermissionGateway{tx: tx}, buildPermissionSeeds(), res); err != nil {
		return err
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
