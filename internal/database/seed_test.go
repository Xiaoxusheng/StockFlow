package database

// seed.go 种子数据的结构自查（不依赖 PostgreSQL/Redis/网络）：
//   - 权限点/角色清单与冻结契约逐字核对（backend-m1-plan §5.4.1/§7.1、permission.md §1）；
//   - 菜单父子序（seedBootstrapData 依赖“父先子后”构建 code→id 映射）；
//   - 角色绑定规则（plan §7.1 四角色映射，只读角色绝不含写动作）；
//   - 管理员初始密码策略与“不触库快速失败”路径。

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestSystemRolesMatchesPermissionSpec 16 内置角色与 permission.md §1 清单逐一对应。
func TestSystemRolesMatchesPermissionSpec(t *testing.T) {
	wantNames := []string{
		"超级管理员", "系统管理员", "仓库经理", "仓库管理员",
		"收货员", "上架员", "拣货员", "复核员", "打包员", "发货员",
		"盘点员", "质检员", "采购人员", "销售人员", "财务/查看人员", "普通用户",
	}
	if len(systemRoles) != len(wantNames) {
		t.Fatalf("内置角色应为 16 个（permission.md §1），实际 %d", len(systemRoles))
	}
	codes := map[string]bool{}
	for i, r := range systemRoles {
		if r.Name != wantNames[i] {
			t.Fatalf("第 %d 个角色应为 %q，实际 %q", i, wantNames[i], r.Name)
		}
		if r.Code == "" {
			t.Fatalf("角色 %q 编码为空", r.Name)
		}
		if codes[r.Code] {
			t.Fatalf("角色编码重复: %s", r.Code)
		}
		codes[r.Code] = true
	}
}

// TestPermissionSeedsMatchFrozenPlan 权限点与冻结清单逐字核对
// （M1：plan §5.4.1；M2：backend-m2-plan §9.2/§9.3 收编记录。测试内为字面清单，
// 与 seed.go 数据源独立交叉验证，防止遗漏/私自扩清单）。
func TestPermissionSeedsMatchFrozenPlan(t *testing.T) {
	wantActionCodes := map[string]bool{
		// auth（plan §5.4.1）
		"auth:user:list": true, "auth:user:read": true, "auth:user:create": true, "auth:user:update": true,
		"auth:user:status": true, "auth:user:assign-role": true, "auth:user:reset-password": true, "auth:user:unlock": true,
		"auth:role:list": true, "auth:role:read": true, "auth:role:create": true, "auth:role:update": true,
		"auth:role:status": true, "auth:role:assign-permission": true,
		"auth:permission:list": true,
		"auth:dept:list":       true, "auth:dept:read": true, "auth:dept:create": true, "auth:dept:update": true, "auth:dept:status": true,
		"auth:session:list": true, "auth:session:kick": true,
		// masterdata
		"masterdata:product:list": true, "masterdata:product:read": true, "masterdata:product:create": true,
		"masterdata:product:update": true, "masterdata:product:delete": true, "masterdata:product:status": true,
		"masterdata:sku:list": true, "masterdata:sku:read": true, "masterdata:sku:create": true,
		"masterdata:sku:update": true, "masterdata:sku:delete": true, "masterdata:sku:status": true,
		"masterdata:category:list": true, "masterdata:category:read": true, "masterdata:category:create": true,
		"masterdata:category:update": true, "masterdata:category:status": true,
		"masterdata:unit:list": true, "masterdata:unit:read": true, "masterdata:unit:create": true,
		"masterdata:unit:update": true, "masterdata:unit:status": true,
		"masterdata:supplier:list": true, "masterdata:supplier:read": true, "masterdata:supplier:create": true,
		"masterdata:supplier:update": true, "masterdata:supplier:delete": true, "masterdata:supplier:status": true,
		"masterdata:customer:list": true, "masterdata:customer:read": true, "masterdata:customer:create": true,
		"masterdata:customer:update": true, "masterdata:customer:delete": true, "masterdata:customer:status": true,
		// warehouse
		"warehouse:warehouse:list": true, "warehouse:warehouse:read": true, "warehouse:warehouse:create": true,
		"warehouse:warehouse:update": true, "warehouse:warehouse:delete": true, "warehouse:warehouse:status": true,
		"warehouse:zone:list": true, "warehouse:zone:read": true, "warehouse:zone:create": true,
		"warehouse:zone:update": true, "warehouse:zone:status": true,
		"warehouse:shelf:list": true, "warehouse:shelf:read": true, "warehouse:shelf:create": true,
		"warehouse:shelf:update": true, "warehouse:shelf:status": true,
		"warehouse:bin:list": true, "warehouse:bin:read": true, "warehouse:bin:create": true,
		"warehouse:bin:update": true, "warehouse:bin:delete": true, "warehouse:bin:status": true,
		// inventory（M1 HTTP 面只读，plan §8.7；batch/serial 为 2026-10-02 复核补录）
		"inventory:inventory:list": true, "inventory:ledger:list": true,
		"inventory:batch:list": true, "inventory:serial:list": true,
		// —— M2 采购入库（backend-m2-plan §9.2）——
		"purchase:purchase:list": true, "purchase:purchase:read": true, "purchase:purchase:create": true,
		"purchase:purchase:update": true, "purchase:purchase:submit": true, "purchase:purchase:approve": true,
		"purchase:purchase:cancel": true, "purchase:purchase:close": true,
		"purchase:inbound:list": true, "purchase:inbound:read": true, "purchase:inbound:create": true,
		"purchase:inbound:update": true, "purchase:inbound:cancel": true, "purchase:inbound:close": true,
		"purchase:receipt:list": true, "purchase:receipt:read": true, "purchase:receipt:execute": true,
		"purchase:putaway:list": true, "purchase:putaway:read": true, "purchase:putaway:claim": true, "purchase:putaway:execute": true,
		"purchase:quality:list": true, "purchase:quality:read": true, "purchase:quality:create": true, "purchase:quality:execute": true,
		// —— M2 销售出库 ——
		"sales:sales:list": true, "sales:sales:read": true, "sales:sales:create": true, "sales:sales:update": true,
		"sales:sales:submit": true, "sales:sales:approve": true, "sales:sales:cancel": true, "sales:sales:close": true,
		"sales:outbound:list": true, "sales:outbound:read": true, "sales:outbound:create": true,
		"sales:outbound:cancel": true, "sales:outbound:close": true,
		"sales:allocation:list": true, "sales:allocation:read": true, "sales:allocation:create": true, "sales:allocation:execute": true,
		"sales:pick:list": true, "sales:pick:read": true, "sales:pick:claim": true, "sales:pick:execute": true,
		"sales:check:list": true, "sales:check:read": true, "sales:check:claim": true, "sales:check:execute": true,
		"sales:packing:list": true, "sales:packing:read": true, "sales:packing:execute": true,
		"sales:shipment:list": true, "sales:shipment:read": true, "sales:shipment:execute": true,
		// —— M2 库存作业 ——
		"stockops:transfer:list": true, "stockops:transfer:read": true, "stockops:transfer:create": true,
		"stockops:transfer:update": true, "stockops:transfer:submit": true, "stockops:transfer:approve": true,
		"stockops:transfer:execute": true, "stockops:transfer:cancel": true, "stockops:transfer:close": true,
		"stockops:count:list": true, "stockops:count:read": true, "stockops:count:create": true,
		"stockops:count:execute": true, "stockops:count:approve": true,
		"stockops:count:cancel": true, "stockops:count:close": true,
		"stockops:adjustment:list": true, "stockops:adjustment:read": true, "stockops:adjustment:create": true,
		"stockops:adjustment:approve": true, "stockops:adjustment:execute": true, "stockops:adjustment:cancel": true,
		"stockops:move:list": true, "stockops:move:execute": true,
		"inventory:lock:list": true,
		// —— M2 退货/异常/追溯 ——
		"returns:salesreturn:list": true, "returns:salesreturn:read": true, "returns:salesreturn:create": true,
		"returns:salesreturn:update": true, "returns:salesreturn:submit": true, "returns:salesreturn:approve": true,
		"returns:salesreturn:execute": true, "returns:salesreturn:cancel": true, "returns:salesreturn:close": true,
		"returns:purchasereturn:list": true, "returns:purchasereturn:read": true, "returns:purchasereturn:create": true,
		"returns:purchasereturn:update": true, "returns:purchasereturn:submit": true, "returns:purchasereturn:approve": true,
		"returns:purchasereturn:execute": true, "returns:purchasereturn:cancel": true, "returns:purchasereturn:close": true,
		"returns:exception:list": true, "returns:exception:read": true, "returns:exception:create": true,
		"returns:exception:assign": true, "returns:exception:execute": true, "returns:exception:close": true,
		"returns:trace:list": true,
		// —— M3 数据中心（backend-m3-plan §11.1）——
		"datax:import:list": true, "datax:import:read": true, "datax:import:create": true, "datax:import:execute": true,
		"datax:export:list": true, "datax:export:read": true, "datax:export:create": true,
		"datax:file:list": true, "datax:file:read": true, "datax:file:create": true, "datax:file:delete": true,
		// —— M3 打印中心 ——
		"printing:template:list": true, "printing:template:read": true, "printing:template:create": true,
		"printing:template:update": true, "printing:template:status": true,
		"printing:task:list": true, "printing:task:read": true, "printing:task:create": true, "printing:task:execute": true,
		// —— M3 设备与扫码 ——
		"devices:device:list": true, "devices:device:read": true, "devices:device:create": true,
		"devices:device:update": true, "devices:device:status": true,
		"devices:scanlog:list": true, "devices:scanlog:read": true,
		"scanner:resolve:list": true,
		// —— M3 报表 ——
		"reports:report:list": true, "reports:report:read": true,
		// —— M3 平台运维 ——
		"system:log:list": true, "system:log:read": true,
		"system:job:list": true, "system:job:read": true, "system:job:status": true,
		"system:config:list": true, "system:config:update": true,
		"system:monitor:list": true,
		"system:backup:list":  true, "system:backup:read": true, "system:backup:create": true,
	}

	wantMenuCodes := map[string]bool{
		"menu:dashboard": true,
		"menu:inventory": true, "inventory:inventory": true, "inventory:ledger": true,
		"inventory:batch": true, "inventory:serial": true,
		"menu:warehouse": true, "warehouse:warehouse": true, "warehouse:zone": true, "warehouse:shelf": true, "warehouse:bin": true,
		"menu:masterdata": true, "masterdata:product": true, "masterdata:sku": true, "masterdata:category": true,
		"masterdata:unit": true, "masterdata:supplier": true, "masterdata:customer": true,
		"menu:system": true, "auth:user": true, "auth:role": true, "auth:permission": true, "auth:dept": true, "auth:session": true,
		// —— M2 菜单（采购/销售/库存作业/退货与异常四组，backend-m2-plan §9.3）——
		"menu:purchase": true, "purchase:purchase": true, "purchase:inbound": true,
		"purchase:receipt": true, "purchase:quality": true, "purchase:putaway": true,
		"menu:sales": true, "sales:sales": true, "sales:outbound": true, "sales:allocation": true,
		"sales:pick": true, "sales:check": true, "sales:packing": true, "sales:shipment": true,
		"menu:stockops": true, "stockops:transfer": true, "stockops:count": true,
		"stockops:adjustment": true, "stockops:move": true, "inventory:lock": true,
		"menu:returns": true, "returns:salesreturn": true, "returns:purchasereturn": true,
		"returns:exception": true, "returns:trace": true,
		// —— M3 菜单（数据中心/设备中心/报表中心 + 系统管理增叶，backend-m3-plan §11.2）——
		"menu:datax": true, "datax:import": true, "datax:export": true, "printing:template": true, "datax:file": true,
		"menu:devices": true, "devices:device": true, "devices:scanlog": true,
		"menu:reports": true, "reports:report": true,
		"system:log": true, "system:job": true, "system:config": true, "system:monitor": true,
	}

	seeds := buildPermissionSeeds()
	gotAction := map[string]bool{}
	gotMenu := map[string]bool{}
	countByType := map[string]int{}
	for _, s := range seeds {
		switch s.Type {
		case "MENU":
			gotMenu[s.Code] = true
		case "BUTTON", "API":
			gotAction[s.Code] = true
		default:
			t.Fatalf("权限点 %s 类型非法: %s", s.Code, s.Type)
		}
		countByType[s.Type]++
		if gotAction[s.Code] && gotMenu[s.Code] {
			t.Fatalf("权限点编码既作菜单又作动作: %s", s.Code)
		}
	}
	for code := range wantActionCodes {
		if !gotAction[code] {
			t.Fatalf("缺少冻结清单权限点: %s", code)
		}
	}
	for code := range wantMenuCodes {
		if !gotMenu[code] {
			t.Fatalf("缺少菜单权限点: %s", code)
		}
	}
	if len(gotAction) != len(wantActionCodes) {
		t.Fatalf("动作权限点数量 %d 与冻结清单 %d 不符（多出: 检查是否发明清单外动作）", len(gotAction), len(wantActionCodes))
	}
	if len(gotMenu) != len(wantMenuCodes) {
		t.Fatalf("菜单权限点数量 %d 与默认菜单清单 %d 不符", len(gotMenu), len(wantMenuCodes))
	}
	// 三级分布（MENU=页面，BUTTON=写动作，API=list/read）：M1 32 API/50 BUTTON +
	// M2 39 API/67 BUTTON + M3 25 API/16 BUTTON = 96/133。
	if countByType["MENU"] != len(wantMenuCodes) || countByType["API"] != 96 || countByType["BUTTON"] != 133 {
		t.Fatalf("权限点三级分布异常: %v（应 MENU=%d API=96 BUTTON=133）", countByType, len(wantMenuCodes))
	}

	// 动作点名称必须有中文映射（permActionNames 完备性）
	for _, r := range permResources {
		for _, a := range r.Actions {
			if permActionNames[a] == "" {
				t.Fatalf("动作词 %s 缺少中文名映射", a)
			}
		}
	}
	// 动作词不得超出冻结枚举（M1 基础 + 特有动作 + backend-m2-plan §9.1 作业/审批动作）
	allowedActions := map[string]bool{
		"list": true, "read": true, "create": true, "update": true, "delete": true, "status": true,
		"assign-role": true, "assign-permission": true, "reset-password": true, "unlock": true, "kick": true,
		"submit": true, "approve": true, "cancel": true, "close": true,
		"execute": true, "claim": true, "assign": true,
	}
	for a := range permActionNames {
		if !allowedActions[a] {
			t.Fatalf("发明了清单外动作词: %s（plan §5.4.1 / §9.1 禁止）", a)
		}
	}
}

// TestMenuSeedsParentsFirst menuSeeds 必须父先子后（seedBootstrapData 按“父先子后”
// 构建 code→id 映射；乱序会导致子节点父级缺失而报错）。
func TestMenuSeedsParentsFirst(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range menuSeeds {
		if m.Parent != "" && !seen[m.Parent] {
			t.Fatalf("菜单 %s 的父级 %s 未先出现（menuSeeds 必须父先子后）", m.Code, m.Parent)
		}
		if seen[m.Code] {
			t.Fatalf("菜单编码重复: %s", m.Code)
		}
		seen[m.Code] = true
	}
	// 菜单叶子编码必须来自冻结资源清单（菜单树与权限点同源，plan §7.5）
	resources := map[string]bool{}
	for _, r := range permResources {
		resources[r.Code] = true
	}
	for _, m := range menuSeeds {
		if m.Parent == "" && !strings.HasPrefix(m.Code, "menu:") {
			t.Fatalf("顶级菜单编码须以 menu: 前缀命名: %s", m.Code)
		}
		if m.Parent != "" && !resources[m.Code] {
			t.Fatalf("叶子菜单 %s 不是资源编码（与 §5.4.1 资源清单不同源）", m.Code)
		}
		if m.Parent == "menu:dashboard" {
			t.Fatalf("menu:dashboard 不应有子菜单: %s", m.Code)
		}
	}
}

// TestRoleBindingsFrozenMapping plan §7.1（M1 四角色）+ backend-m2-plan §9.3（M2 角色映射）：
// 断言各角色绑定与冻结口径一致——多授权与漏授权都失败。
func TestRoleBindingsFrozenMapping(t *testing.T) {
	seeds := buildPermissionSeeds()
	allCodes := make([]string, 0, len(seeds))
	typeByCode := map[string]string{}
	for _, s := range seeds {
		allCodes = append(allCodes, s.Code)
		typeByCode[s.Code] = s.Type
	}
	apiTotal := 0
	for _, s := range seeds {
		if s.Type == "API" {
			apiTotal++
		}
	}

	// 超级管理员 / 系统管理员：全部权限点
	for _, role := range []string{"super_admin", "sys_admin"} {
		got := rolePermissionCodes(role)
		if len(got) != len(allCodes) {
			t.Fatalf("%s 应绑定全部 %d 个权限点，实际 %d", role, len(allCodes), len(got))
		}
	}

	// M2 资源编码（backend-m2-plan §9.2 清单，测试内字面罗列——与 seed.go 独立）。
	m2Resources := map[string]bool{
		"purchase:purchase": true, "purchase:inbound": true, "purchase:receipt": true,
		"purchase:putaway": true, "purchase:quality": true,
		"sales:sales": true, "sales:outbound": true, "sales:allocation": true,
		"sales:pick": true, "sales:check": true, "sales:packing": true, "sales:shipment": true,
		"stockops:transfer": true, "stockops:count": true, "stockops:adjustment": true,
		"stockops:move": true, "inventory:lock": true,
		"returns:salesreturn": true, "returns:purchasereturn": true,
		"returns:exception": true, "returns:trace": true,
	}
	// M3 资源编码（backend-m3-plan §11.1 清单，测试内字面罗列——与 seed.go 独立）。
	m3Resources := map[string]bool{
		"datax:import": true, "datax:export": true, "datax:file": true,
		"printing:template": true, "printing:task": true,
		"devices:device": true, "devices:scanlog": true, "scanner:resolve": true,
		"reports:report": true,
		"system:log":     true, "system:job": true, "system:config": true, "system:monitor": true,
		"system:backup": true,
	}
	resourceOf := func(code string) string {
		i := strings.LastIndex(code, ":")
		if i < 0 {
			return ""
		}
		return code[:i]
	}
	isM2Menu := func(code string) bool { return typeByCode[code] == "MENU" && m2Resources[code] }
	isM2ReadOnly := func(code string) bool {
		if typeByCode[code] != "API" {
			return false
		}
		return m2Resources[resourceOf(code)]
	}
	// M3 :read 明细点（backend-m3-plan §11.3：viewer 仅授权 M3 资源 list，read 不授）。
	isM3ReadDetail := func(code string) bool {
		return typeByCode[code] == "API" && strings.HasSuffix(code, ":read") && m3Resources[resourceOf(code)]
	}

	// 仓库管理员：M1 仓库+库存查看（9 菜单 + 10 查询点）+ M2 四域全部列表读 + 移库执行（§9.3）
	// + M3 scanner:resolve + datax:file 全动作 + reports:report:list（§11.3）。
	// M2/M3 顶级菜单随资源带出（叶子菜单编码 = 资源编码，§9.3/§11.3 菜单与动作同源）。
	wantWH := map[string]bool{
		"menu:dashboard": true, "menu:inventory": true, "inventory:inventory": true, "inventory:ledger": true,
		"inventory:inventory:list": true, "inventory:ledger:list": true,
		"menu:warehouse": true, "warehouse:warehouse": true, "warehouse:zone": true, "warehouse:shelf": true, "warehouse:bin": true,
		"warehouse:warehouse:list": true, "warehouse:warehouse:read": true,
		"warehouse:zone:list": true, "warehouse:zone:read": true,
		"warehouse:shelf:list": true, "warehouse:shelf:read": true,
		"warehouse:bin:list": true, "warehouse:bin:read": true,
		"stockops:move:execute": true,
		"menu:purchase":         true, "menu:sales": true, "menu:stockops": true, "menu:returns": true,
		// M3（§11.3）
		"menu:datax": true, "datax:file": true, "menu:reports": true, "reports:report": true,
		"datax:file:list": true, "datax:file:read": true, "datax:file:create": true, "datax:file:delete": true,
		"scanner:resolve:list": true, "reports:report:list": true,
	}
	for _, s := range seeds {
		if isM2Menu(s.Code) || isM2ReadOnly(s.Code) {
			wantWH[s.Code] = true
		}
	}
	gotWH := rolePermissionCodes("warehouse_operator")
	if len(gotWH) != len(wantWH) {
		t.Fatalf("warehouse_operator 绑定数 %d 与预期 %d 不符", len(gotWH), len(wantWH))
	}
	for _, c := range gotWH {
		if !wantWH[c] {
			t.Fatalf("warehouse_operator 出现清单外权限点: %s", c)
		}
	}
	for code := range wantWH {
		found := false
		for _, c := range gotWH {
			if c == code {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("warehouse_operator 缺少预期权限点: %s", code)
		}
	}

	// 仓库经理（§9.3 + §11.3）：M2 四域全部列表读 + 各域 approve + 调拨/盘点全量 + 移库执行
	// + M1 库存查看；M3：datax/printing/reports/devices 全 list+read + datax:export:create
	// + printing:template 全量 + devices:device:update；system:* 运维面不授。
	wantManager := map[string]bool{
		"menu:dashboard":        true,
		"stockops:move:execute": true,
		// M2 顶级菜单（随资源带出，§9.3 同源口径）
		"menu:purchase": true, "menu:sales": true, "menu:stockops": true, "menu:returns": true,
		// M1 库存查看基线（调拨/盘点全量的可视性前提，seed.go 编译注释同口径）
		"menu:inventory": true, "inventory:inventory": true, "inventory:inventory:list": true,
		"inventory:ledger": true, "inventory:ledger:list": true,
		// M3（§11.3）：datax/printing/reports/devices 全 list+read 的顶级菜单与叶子随
		// read/full 带出；system:* 运维面菜单与动作均不授。
		// 注：printing:task 无独立菜单叶子（菜单叶 = printing:template，§11.2 落位）。
		"menu:datax": true, "menu:devices": true, "menu:reports": true,
		"datax:import": true, "datax:export": true, "datax:file": true,
		"printing:template": true,
		"devices:device":    true, "devices:scanlog": true, "reports:report": true,
		"datax:export:create":      true,
		"devices:device:update":    true,
		"printing:template:create": true, "printing:template:update": true, "printing:template:status": true,
	}
	// 经理的 M3 "全 list+read" 资源（与 seed.go m3ManagerReadResources 同源冻结，测试内独立罗列）。
	m3ManagerReadSet := map[string]bool{
		"datax:import": true, "datax:export": true, "datax:file": true,
		"printing:template": true, "printing:task": true,
		"devices:device": true, "devices:scanlog": true, "scanner:resolve": true,
		"reports:report": true,
	}
	for _, s := range seeds {
		if isM2Menu(s.Code) || isM2ReadOnly(s.Code) {
			wantManager[s.Code] = true
		}
		if m3ManagerReadSet[resourceOf(s.Code)] && (typeByCode[s.Code] == "MENU" || typeByCode[s.Code] == "API") {
			wantManager[s.Code] = true
		}
	}
	for _, c := range []string{
		"stockops:transfer:create", "stockops:transfer:update", "stockops:transfer:submit",
		"stockops:transfer:approve", "stockops:transfer:execute", "stockops:transfer:cancel", "stockops:transfer:close",
		"stockops:count:create", "stockops:count:execute", "stockops:count:approve",
		"stockops:count:cancel", "stockops:count:close",
		"purchase:purchase:approve", "sales:sales:approve", "stockops:adjustment:approve",
		"returns:salesreturn:approve", "returns:purchasereturn:approve",
	} {
		wantManager[c] = true
	}
	gotManager := rolePermissionCodes("warehouse_manager")
	if len(gotManager) != len(wantManager) {
		t.Fatalf("warehouse_manager 绑定数 %d 与预期 %d 不符", len(gotManager), len(wantManager))
	}
	for _, c := range gotManager {
		if !wantManager[c] {
			t.Fatalf("warehouse_manager 出现清单外权限点: %s", c)
		}
	}

	// 财务/查看人员：只读——全部菜单 + list/read（含 M2 资源），绝无写动作。
	gotViewer := rolePermissionCodes("viewer")
	menuTotal := 0
	actionTotal := 0
	for _, c := range gotViewer {
		switch typeByCode[c] {
		case "MENU":
			menuTotal++
		case "API":
			actionTotal++
		default:
			t.Fatalf("viewer 含非只读权限点 %s（type=%q）", c, typeByCode[c])
		}
	}
	// S9（安全审查）：用户目录含 PII（手机号/邮箱等），auth:user:list/read 不授权查看类角色
	viewerSet := map[string]bool{}
	for _, c := range gotViewer {
		viewerSet[c] = true
	}
	for _, banned := range []string{"auth:user:list", "auth:user:read"} {
		if viewerSet[banned] {
			t.Fatalf("viewer 不应持有 %s（用户目录含 PII，仅授权管理类角色）", banned)
		}
	}
	// 全部查询点剔除上述 2 个与 M3 read 明细点（§11.3：viewer 仅授权 M3 资源 list）
	m3ReadCount := 0
	for _, s := range seeds {
		if isM3ReadDetail(s.Code) {
			m3ReadCount++
		}
	}
	if len(gotViewer) != len(menuSeeds)+apiTotal-2-m3ReadCount || menuTotal != len(menuSeeds) || actionTotal != apiTotal-2-m3ReadCount {
		t.Fatalf("viewer 应含 %d 菜单 + %d 查询点，实际绑定 %d（%d 菜单 + %d 查询）",
			len(menuSeeds), apiTotal-2-m3ReadCount, len(gotViewer), menuTotal, actionTotal)
	}
	// M3 只读口径抽查（§11.3）：M3 资源 list 持有、read 明细点与写动作不持有。
	viewerHas := func(codes ...string) {
		t.Helper()
		for _, c := range codes {
			if !viewerSet[c] {
				t.Fatalf("viewer 缺少预期权限点 %s", c)
			}
		}
	}
	viewerHas("datax:file:list", "reports:report:list", "system:monitor:list", "scanner:resolve:list")
	for _, banned := range []string{"datax:file:read", "reports:report:read", "system:config:update", "system:job:status", "system:backup:create"} {
		if viewerSet[banned] {
			t.Fatalf("viewer 不应持有 %s（§11.3 M3 资源仅 list；运维写动作不授）", banned)
		}
	}

	// M2 作业/审批角色抽查（backend-m2-plan §9.3 映射落位；持有断言 + 越界断言成对）。
	assertGranted := func(role string, want, denied []string) {
		t.Helper()
		got := rolePermissionCodes(role)
		set := map[string]bool{}
		for _, c := range got {
			set[c] = true
			if typeByCode[c] != "MENU" && typeByCode[c] != "BUTTON" && typeByCode[c] != "API" {
				t.Fatalf("%s 持有未知权限点 %s", role, c)
			}
		}
		for _, c := range want {
			if !set[c] {
				t.Fatalf("%s 缺少预期权限点 %s", role, c)
			}
		}
		for _, c := range denied {
			if set[c] {
				t.Fatalf("%s 不应持有 %s", role, c)
			}
		}
	}
	assertGranted("purchaser",
		[]string{"purchase:purchase:create", "purchase:purchase:submit", "purchase:purchase:approve", "purchase:inbound:read", "returns:trace:list"},
		[]string{"purchase:receipt:execute", "purchase:putaway:execute", "returns:exception:create"})
	assertGranted("receiver",
		[]string{"purchase:receipt:execute", "purchase:inbound:read", "returns:exception:create"},
		[]string{"purchase:purchase:create", "purchase:putaway:execute"})
	assertGranted("picker",
		[]string{"sales:pick:claim", "sales:pick:execute", "returns:exception:create"},
		[]string{"sales:sales:create", "sales:shipment:execute"})
	assertGranted("stocktaker",
		[]string{"stockops:count:execute", "stockops:count:approve", "stockops:adjustment:create", "stockops:adjustment:list",
			"scanner:resolve:list", "datax:file:read", "reports:report:list"},
		[]string{"stockops:adjustment:execute", "stockops:transfer:list", "datax:file:delete", "reports:report:read"})

	// M3 角色映射抽查（backend-m3-plan §11.3 落位；持有断言 + 越界断言成对）。
	// 注：§11.3 裸资源名（datax:file）按 compileRoleGrant 语法落位为资源全动作
	// （list/read/create/delete，delete 为审计敏感操作）——如需收紧属权限策略调整。
	assertGranted("purchaser",
		[]string{"datax:export:list", "datax:export:read", "datax:export:create", "datax:file:create", "datax:file:delete",
			"reports:report:list", "reports:report:read", "menu:datax", "menu:reports"},
		[]string{"datax:import:execute", "system:config:update", "devices:device:update"})
	assertGranted("receiver",
		[]string{"scanner:resolve:list", "datax:file:list", "datax:file:read", "reports:report:list", "menu:datax"},
		[]string{"datax:file:create", "datax:export:create", "printing:task:execute", "system:job:status"})
	assertGranted("warehouse_manager",
		[]string{"datax:import:read", "datax:export:create", "printing:template:create", "printing:template:status",
			"printing:task:read", "devices:device:update", "devices:scanlog:list", "reports:report:read"},
		[]string{"datax:import:execute", "system:config:update", "system:job:status", "system:backup:create", "system:log:read"})
	assertGranted("warehouse_operator",
		[]string{"scanner:resolve:list", "datax:file:create", "datax:file:delete", "reports:report:list"},
		[]string{"datax:import:create", "printing:task:create", "devices:device:update", "system:monitor:list"})

	// user（backend-m3-plan §11.3）：仅统一扫码解析（个人通知无权限点），不配其他业务映射。
	gotUser := rolePermissionCodes("user")
	if len(gotUser) != 1 || gotUser[0] != "scanner:resolve:list" {
		t.Fatalf("user 应仅绑定 scanner:resolve:list（§11.3），实际 %v", gotUser)
	}
}

// TestValidateAdminInitialPassword 强密码策略（S3 收紧：≥12 位且三类字符）与缺失注入的快速失败。
func TestValidateAdminInitialPassword(t *testing.T) {
	cases := []struct {
		pw      string
		wantErr string
	}{
		{"", "SF_ADMIN_INITIAL_PASSWORD"},
		{"Sh0rt", "强密码策略"},        // 过短
		{"abcdefgh", "强密码策略"},     // 不足 12 位且无数字
		{"12345678", "强密码策略"},     // 无字母
		{"Admin12345", "强密码策略"},   // 三类但仅 10 位（原 8 位策略可通过，收紧后拒绝）
		{"abcdefghijkl", "强密码策略"}, // 恰 12 位但单一类别
		{"AbcdEfgh1234", ""},      // 恰 12 位，三类（大小写+数字）
		{"Sf-P@ssw0rd2026", ""},   // 15 位四类
		{"!!!!!!!!!!!!", "强密码策略"}, // 恰 12 位仅符号一类
	}
	for _, c := range cases {
		err := validateAdminInitialPassword(c.pw)
		if c.wantErr == "" && err != nil {
			t.Fatalf("密码长度 %d 应通过校验，实际报错: %v", len(c.pw), err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Fatalf("密码（长度 %d）应报错且含 %q，实际: %v", len(c.pw), c.wantErr, err)
		}
	}
	// 日志红线：错误信息不得回显密码内容
	err := validateAdminInitialPassword("Leak1234")
	if err == nil {
		err = validateAdminInitialPassword("weak1")
	}
	if err != nil && strings.Contains(err.Error(), "Leak1234") {
		t.Fatal("错误信息回显了密码内容（architecture.md §6 日志红线）")
	}
}

// TestBootstrapIfEmptyNilDB 不触库快速失败：连接为空必须返回错误而非 panic。
func TestBootstrapIfEmptyNilDB(t *testing.T) {
	if _, err := BootstrapIfEmpty(nil, ""); err == nil || !strings.Contains(err.Error(), "数据库连接为空") {
		t.Fatalf("nil db 应返回明确错误，实际: %v", err)
	}
	if _, err := BootstrapIfEmpty(nil, "Admin12345"); err == nil {
		t.Fatal("nil db（即使密码合法）也应返回错误")
	}
}

// TestBcryptCostAndHash bcrypt 强度与哈希可验证性（cost 12，plan §7.3）。
func TestBcryptCostAndHash(t *testing.T) {
	if BcryptCost != 12 {
		t.Fatalf("BcryptCost 应为 12（backend-m1-plan §7.3），实际 %d", BcryptCost)
	}
	pw := "Admin12345"
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if strings.Contains(string(h), pw) {
		t.Fatal("哈希值中包含明文密码")
	}
	if err := bcrypt.CompareHashAndPassword(h, []byte(pw)); err != nil {
		t.Fatalf("哈希验证失败: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(h, []byte("Wrong1234")); err == nil {
		t.Fatal("错误密码不应通过哈希验证")
	}
}

// fakePermGateway 内存假网关：模拟「部分初始化的残留数据」——
// 同 code 行已存在时 insertSeed 返回 sql.ErrNoRows（ON CONFLICT DO NOTHING 无 RETURNING 行），
// selectIDByCode 返回残留行 id。
type fakePermGateway struct {
	residual map[string]int64 // code → 残留行 id
	nextID   int64
	inserted []string
}

func (f *fakePermGateway) insertSeed(p permissionSeed, _ any) (int64, error) {
	if _, ok := f.residual[p.Code]; ok {
		return 0, sql.ErrNoRows
	}
	f.nextID++
	f.inserted = append(f.inserted, p.Code)
	return f.nextID, nil
}

func (f *fakePermGateway) selectIDByCode(code string) (int64, error) {
	if id, ok := f.residual[code]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("权限点 %s 不存在", code)
}

// TestSeedPermissionsResidualRowsIdempotent 残留父行场景：users 空但 permissions 有
// 部分初始化残留（F11）——父行命中 ON CONFLICT 后必须回读 id 回填映射，
// 子权限正常挂接，引导整体幂等继续而非失败。
func TestSeedPermissionsResidualRowsIdempotent(t *testing.T) {
	seeds := []permissionSeed{
		{Code: "sys:user", Name: "用户管理", Type: "MENU", Sort: 1},
		{Code: "sys:user:read", Name: "用户查询", Type: "API", Parent: "sys:user", Sort: 1},
		{Code: "sys:user:create", Name: "用户新增", Type: "BUTTON", Parent: "sys:user", Sort: 2},
	}
	gw := &fakePermGateway{residual: map[string]int64{"sys:user": 42}, nextID: 100}
	res := &BootstrapResult{}

	permIDs, err := seedPermissions(gw, seeds, res)
	if err != nil {
		t.Fatalf("残留父行应幂等跳过并回填映射，实际报错: %v", err)
	}
	if got := permIDs["sys:user"]; got != 42 {
		t.Fatalf("残留父行 sys:user 映射应回读为 42，实际 %d", got)
	}
	if permIDs["sys:user:read"] == 0 || permIDs["sys:user:create"] == 0 {
		t.Fatalf("子权限应正常写入并挂接，实际映射: %v", permIDs)
	}
	if res.PermissionsSeeded != 2 {
		t.Fatalf("新写权限点应为 2（残留行不计入），实际 %d", res.PermissionsSeeded)
	}
	if len(gw.inserted) != 2 {
		t.Fatalf("实际 INSERT 应仅 2 行，实际 %v", gw.inserted)
	}
}

// TestSeedPermissionsMissingParentFails 无残留注入时父级缺失仍快速失败（原有守卫不回退）。
func TestSeedPermissionsMissingParentFails(t *testing.T) {
	seeds := []permissionSeed{
		{Code: "orphan:read", Name: "孤儿动作", Type: "API", Parent: "no:such:menu", Sort: 1},
	}
	gw := &fakePermGateway{nextID: 1}
	res := &BootstrapResult{}
	if _, err := seedPermissions(gw, seeds, res); err == nil || !strings.Contains(err.Error(), "父级 no:such:menu 未就绪") {
		t.Fatalf("父级缺失应快速失败，实际: %v", err)
	}
}
