package database

// seed.go 种子数据的结构自查（不依赖 PostgreSQL/Redis/网络）：
//   - 权限点/角色清单与冻结契约逐字核对（backend-m1-plan §5.4.1/§7.1、permission.md §1）；
//   - 菜单父子序（seedBootstrapData 依赖“父先子后”构建 code→id 映射）；
//   - 角色绑定规则（plan §7.1 四角色映射，只读角色绝不含写动作）；
//   - 管理员初始密码策略与“不触库快速失败”路径。

import (
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

// TestPermissionSeedsMatchFrozenPlan 权限点与 plan §5.4.1 冻结清单逐字核对
// （测试内为字面清单，与 seed.go 数据源独立交叉验证，防止遗漏/私自扩清单）。
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
	}

	wantMenuCodes := map[string]bool{
		"menu:dashboard": true,
		"menu:inventory": true, "inventory:inventory": true, "inventory:ledger": true,
		"inventory:batch": true, "inventory:serial": true,
		"menu:warehouse": true, "warehouse:warehouse": true, "warehouse:zone": true, "warehouse:shelf": true, "warehouse:bin": true,
		"menu:masterdata": true, "masterdata:product": true, "masterdata:sku": true, "masterdata:category": true,
		"masterdata:unit": true, "masterdata:supplier": true, "masterdata:customer": true,
		"menu:system": true, "auth:user": true, "auth:role": true, "auth:permission": true, "auth:dept": true, "auth:session": true,
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
	// 三级分布（MENU=页面，BUTTON=写动作，API=list/read）
	if countByType["MENU"] != len(wantMenuCodes) || countByType["API"] != 32 || countByType["BUTTON"] != 50 {
		t.Fatalf("权限点三级分布异常: %v（应 MENU=%d API=32 BUTTON=50）", countByType, len(wantMenuCodes))
	}

	// 动作点名称必须有中文映射（permActionNames 完备性）
	for _, r := range permResources {
		for _, a := range r.Actions {
			if permActionNames[a] == "" {
				t.Fatalf("动作词 %s 缺少中文名映射", a)
			}
		}
	}
	// 动作词不得超出冻结枚举
	allowedActions := map[string]bool{
		"list": true, "read": true, "create": true, "update": true, "delete": true, "status": true,
		"assign-role": true, "assign-permission": true, "reset-password": true, "unlock": true, "kick": true,
	}
	for a := range permActionNames {
		if !allowedActions[a] {
			t.Fatalf("发明了清单外动作词: %s（plan §5.4.1 禁止）", a)
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

// TestRoleBindingsFrozenMapping plan §7.1：M1 仅四角色配置映射。
func TestRoleBindingsFrozenMapping(t *testing.T) {
	seeds := buildPermissionSeeds()
	allCodes := make([]string, 0, len(seeds))
	for _, s := range seeds {
		allCodes = append(allCodes, s.Code)
	}

	// 超级管理员 / 系统管理员：全部权限点
	for _, role := range []string{"super_admin", "sys_admin"} {
		got := rolePermissionCodes(role)
		if len(got) != len(allCodes) {
			t.Fatalf("%s 应绑定全部 %d 个权限点，实际 %d", role, len(allCodes), len(got))
		}
	}

	// 仓库管理员：仓库 + 库存查看（9 菜单 + 10 查询点）
	wantWH := map[string]bool{
		"menu:dashboard": true, "menu:inventory": true, "inventory:inventory": true, "inventory:ledger": true,
		"inventory:inventory:list": true, "inventory:ledger:list": true,
		"menu:warehouse": true, "warehouse:warehouse": true, "warehouse:zone": true, "warehouse:shelf": true, "warehouse:bin": true,
		"warehouse:warehouse:list": true, "warehouse:warehouse:read": true,
		"warehouse:zone:list": true, "warehouse:zone:read": true,
		"warehouse:shelf:list": true, "warehouse:shelf:read": true,
		"warehouse:bin:list": true, "warehouse:bin:read": true,
	}
	gotWH := rolePermissionCodes("warehouse_operator")
	if len(gotWH) != len(wantWH) {
		t.Fatalf("warehouse_operator 绑定数 %d 与预期 %d 不符: %v", len(gotWH), len(wantWH), gotWH)
	}
	for _, c := range gotWH {
		if !wantWH[c] {
			t.Fatalf("warehouse_operator 出现清单外权限点: %s", c)
		}
	}

	// 财务/查看人员：只读——全部菜单 + list/read，绝无写动作
	typeByCode := map[string]string{}
	for _, s := range seeds {
		typeByCode[s.Code] = s.Type
	}
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
	// 32 个查询点剔除上述 2 个 → 30（其余只读权限不变）
	if len(gotViewer) != len(menuSeeds)+30 || menuTotal != len(menuSeeds) || actionTotal != 30 {
		t.Fatalf("viewer 应含 %d 菜单 + 30 查询点，实际绑定 %d（%d 菜单 + %d 查询）",
			len(menuSeeds), len(gotViewer), menuTotal, actionTotal)
	}

	// 其余角色 M1 无映射
	for _, role := range []string{"warehouse_manager", "receiver", "picker", "user"} {
		if got := rolePermissionCodes(role); len(got) != 0 {
			t.Fatalf("%s 在 M1 不应配置权限映射（plan §7.1），实际 %d 个", role, len(got))
		}
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
