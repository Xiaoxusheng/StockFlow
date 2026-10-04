package database

// dev_seed.sql / dev_seed_verify.sql 演示数据交付物的静态自查（不依赖 PostgreSQL/psql，
// 本环境无 PG——database.md §9 的真库验证须在具备 PG 的环境补做）。
//
// 覆盖（本 ask 验收口径）：
//   - 门禁：is_dev 变量开关、拒绝文案、ON_ERROR_STOP、整体 BEGIN/COMMIT 单事务；
//   - 实体覆盖：测试账号/角色/仓库绑定、仓库四级结构、商品/SKU 四种三开关组合/条码/供应商/客户；
//   - 期初库存只写 total/available 两列，且与期初流水同文件同事务 1:1 成对（0→n、期初、DEV SEED）；
//   - 全部业务行固定显式 9xxx 主键 + ON CONFLICT DO NOTHING（幂等）；
//   - 无生产初始化污染：不写 roles/permissions 等生产初始化表、启动路径零引用、Makefile 门禁在位；
//   - bcrypt 口令哈希可与注释明文互验（cost 12）。
//
// 注意：本测试文件不得出现 "INSERT INTO <库存族表>" 等字面量组合（plan §8.6 违约守卫按
// 字符串扫描 Go 源码，db/** 才豁免）；表名一律经正则捕获解析。

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

const (
	devSeedPath       = "../../db/seed/dev_seed.sql"
	devSeedVerifyPath = "../../db/seed/dev_seed_verify.sql"
)

// ---- 解析辅助（全部引号感知：字符串字面量内的括号/逗号/分号不参与结构判断） ----

// seedInsert 一条 INSERT 语句的解析结果；SELECT 型插入的 tuples 取自其 VALUES 源。
type seedInsert struct {
	table   string
	columns []string
	tuples  [][]string
	text    string // 语句原文（已去注释）
}

var devSeedInsertRe = regexp.MustCompile(`(?is)INSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s*\(`)

// scanBalanced 从 open（须指向 '('）扫描到配平的 ')'，返回其下标与下一位置。
func scanBalanced(t *testing.T, s string, open int, what string) (int, int) {
	t.Helper()
	depth, inQ := 0, false
	for i := open; i < len(s); i++ {
		switch {
		case inQ:
			if s[i] == '\'' {
				inQ = false
			}
		case s[i] == '\'':
			inQ = true
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
			if depth == 0 {
				return i, i + 1
			}
		}
	}
	t.Fatalf("%s 括号不配平（起始 %d）: %.80s", what, open, s[open:])
	return -1, -1
}

// splitTopLevel 按“顶层逗号”切分（引号与括号内的逗号不切）。
func splitTopLevel(t *testing.T, s, what string) []string {
	t.Helper()
	var parts []string
	depth, inQ, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case inQ:
			if s[i] == '\'' {
				inQ = false
			}
		case s[i] == '\'':
			inQ = true
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
		case s[i] == ',' && depth == 0:
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if inQ {
		t.Fatalf("%s 存在未闭合的字符串字面量", what)
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func parseDevSeedInserts(t *testing.T, sqlText string) []seedInsert {
	t.Helper()
	var out []seedInsert
	for _, loc := range devSeedInsertRe.FindAllStringSubmatchIndex(sqlText, -1) {
		table := sqlText[loc[2]:loc[3]]
		open := loc[1] - 1 // '('
		closeIdx, after := scanBalanced(t, sqlText, open, "INSERT INTO "+table+" 列清单")
		cols := splitTopLevel(t, sqlText[open+1:closeIdx], table+" 列清单")
		end := len(sqlText)
		for i := after; i < len(sqlText); i++ { // 语句终点：引号/括号外的 ';'
			switch {
			case sqlText[i] == '\'':
				for j := i + 1; j < len(sqlText); j++ {
					if sqlText[j] == '\'' {
						i = j
						break
					}
				}
			case sqlText[i] == '(':
				_, n := scanBalanced(t, sqlText, i, table+" 语句体")
				i = n - 1
			case sqlText[i] == ';':
				end = i
				i = len(sqlText)
			}
		}
		stmt := sqlText[after:end]
		ins := seedInsert{table: table, columns: cols, text: stmt}
		if m := regexp.MustCompile(`(?i)\bVALUES\b`).FindStringIndex(stmt); m != nil {
			rest := stmt[m[1]:]
			for i := 0; i < len(rest); {
				c := rest[i]
				switch {
				case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
					i++
				case c == '(':
					ci, na := scanBalanced(t, rest, i, table+" VALUES 元组")
					ins.tuples = append(ins.tuples, splitTopLevel(t, rest[i+1:ci], table+" VALUES 元组"))
					i = na
				default:
					i = len(rest) // AS v(...) / ON CONFLICT 等：元组结束
				}
			}
		}
		out = append(out, ins)
	}
	return out
}

func insertsOf(t *testing.T, ins []seedInsert, table string, want int) seedInsert {
	t.Helper()
	var found []seedInsert
	for _, s := range ins {
		if s.table == table {
			found = append(found, s)
		}
	}
	if len(found) != want {
		t.Fatalf("表 %s 的 INSERT 语句应为 %d 条，实际 %d", table, want, len(found))
	}
	if want == 0 {
		return seedInsert{}
	}
	return found[0]
}

func hasCol(cols []string, name string) bool {
	for _, c := range cols {
		if c == name {
			return true
		}
	}
	return false
}

func parseID(t *testing.T, raw, ctx string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("%s 主键非整数字面量: %q", ctx, raw)
	}
	return n
}

func parseQty(t *testing.T, raw, ctx string) float64 {
	t.Helper()
	s := raw
	if i := strings.Index(s, "::"); i >= 0 {
		s = s[:i]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		t.Fatalf("%s 数量非数字: %q", ctx, raw)
	}
	return f
}

func normLit(s string) string {
	s = strings.TrimSpace(s)
	if s == "NULL" {
		return ""
	}
	if len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return s[1 : len(s)-1]
	}
	return s
}

func ean13CheckOK(code string) bool {
	if len(code) != 13 {
		return false
	}
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(code[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if i%2 == 0 {
			sum += d
		} else {
			sum += 3 * d
		}
	}
	cd := int(code[12] - '0')
	return cd >= 0 && cd <= 9 && (10-sum%10)%10 == cd
}

// ---- 门禁与单事务 ----

func TestDevSeedGateAndSingleTransaction(t *testing.T) {
	content := readRepoFile(t, devSeedPath)
	if !strings.Contains(content, `\set ON_ERROR_STOP on`) {
		t.Fatal("dev_seed.sql 缺少 ON_ERROR_STOP（出错必须中止而非半灌）")
	}
	code := stripSQLComments(content) // 头部注释里对 \if 的文字说明不参与结构计数
	if n := strings.Count(code, `\if :is_dev`); n < 2 {
		t.Fatalf("dev_seed.sql 缺少 is_dev 环境门禁（database.md §8.2），\\if :is_dev 出现 %d 次", n)
	}
	if strings.Count(code, `\if`) != strings.Count(code, `\endif`) {
		t.Fatal("\\if 与 \\endif 数量不配对")
	}
	if !strings.Contains(content, "拒绝执行") {
		t.Fatal("dev_seed.sql 缺少未带 is_dev 时的拒绝提示")
	}
	begin := regexp.MustCompile(`(?m)^BEGIN;$`).FindAllStringIndex(content, -1)
	commit := regexp.MustCompile(`(?m)^COMMIT;$`).FindAllStringIndex(content, -1)
	if len(begin) != 1 || len(commit) != 1 {
		t.Fatalf("整个数据集应包裹在单个 BEGIN/COMMIT 事务内（期初库存与流水成对提交），实际 BEGIN=%d COMMIT=%d", len(begin), len(commit))
	}
	locs := devSeedInsertRe.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		t.Fatal("dev_seed.sql 未解析到任何 INSERT")
	}
	if begin[0][0] > locs[0][0] || commit[0][0] < locs[len(locs)-1][0] {
		t.Fatal("存在位于事务外的 INSERT（全部 INSERT 必须在 BEGIN 之后、COMMIT 之前）")
	}
	// INSERT-only：seed 禁止破坏性/覆盖性写入
	body := strings.ToUpper(stripSQLComments(content))
	for _, bad := range []string{"UPDATE ", "DELETE FROM", "TRUNCATE", "DROP ", "ALTER "} {
		if strings.Contains(body, bad) {
			t.Fatalf("dev_seed.sql 含有禁止的写入形态 %q（seed 仅允许 INSERT，防覆盖已有数据）", bad)
		}
	}
	if n1 := strings.Count(body, "ON CONFLICT"); n1 == 0 || n1 != strings.Count(body, "DO NOTHING") {
		t.Fatalf("每条 INSERT 都应 ON CONFLICT DO NOTHING（幂等），ON CONFLICT=%d DO NOTHING=%d",
			n1, strings.Count(body, "DO NOTHING"))
	}
}

// ---- 显式 9xxx 主键 + 白名单表 ----

var devSeedAllowedTables = map[string]bool{
	"departments": true, "users": true, "user_roles": true, "user_warehouses": true,
	"warehouses": true, "zones": true, "shelves": true, "bins": true,
	"product_categories": true, "units": true, "products": true, "skus": true, "barcodes": true,
	"suppliers": true, "customers": true, "batches": true,
	"inventory": true, "inventory_ledgers": true, "serial_numbers": true,
}

func TestDevSeedExplicitPKsIn9xxxRange(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	// 2026-10-05 联调轮：inventory_ledgers 允许第 2 条 INSERT——演示出库形态补影段
	// （净零对，business_type='演示'；docs/database.md §8.2 扩展，api.md §9 回写），
	// 其余每表仍恰一条。
	const extraLedgerInserts = 1
	if len(ins) != len(devSeedAllowedTables)+extraLedgerInserts {
		t.Fatalf("INSERT 语句应恰为 %d 条（每表一条 + ledger 补影段 %d 条），实际 %d",
			len(devSeedAllowedTables)+extraLedgerInserts, extraLedgerInserts, len(ins))
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, s := range ins {
		counts[s.table]++
		if !devSeedAllowedTables[s.table] {
			t.Fatalf("dev_seed.sql 出现白名单之外的写入目标 %s（roles/permissions 等生产初始化表禁止写入）", s.table)
		}
		isJoin := s.table == "user_roles" || s.table == "user_warehouses"
		if isJoin {
			if hasCol(s.columns, "id") {
				t.Fatalf("join 表 %s 不应包含 id 列", s.table)
			}
			if len(s.tuples) == 0 {
				t.Fatalf("join 表 %s 应有绑定数据", s.table)
			}
			continue
		}
		if !hasCol(s.columns, "id") {
			t.Fatalf("业务表 %s 未显式指定主键 id（要求固定显式主键保证幂等）", s.table)
		}
		if s.columns[0] != "id" {
			t.Fatalf("业务表 %s 的 id 应为第一列，实际 %v", s.table, s.columns)
		}
		if len(s.tuples) == 0 {
			t.Fatalf("业务表 %s 未解析到数据行", s.table)
		}
		for _, tup := range s.tuples {
			id := parseID(t, tup[0], s.table)
			if id < 9000 || id > 9999 {
				t.Fatalf("表 %s 主键 %d 不在约定的 9xxx 段（避开自增与生产初始化 1xx 段）", s.table, id)
			}
			key := s.table + "|" + tup[0]
			if seen[key] {
				t.Fatalf("表 %s 显式主键重复: %s", s.table, tup[0])
			}
			seen[key] = true
		}
	}
	for tbl := range devSeedAllowedTables {
		// 2026-10-05 联调轮：inventory_ledgers 允许 2 条（期初成对段 + 演示出库补影段，
		// 见 TestDevSeedOpeningInventoryPairedWithLedger 的分段断言），其余每表 1 条。
		want := 1
		if tbl == "inventory_ledgers" {
			want = 2
		}
		if counts[tbl] != want {
			t.Fatalf("表 %s 的 INSERT 语句数应为 %d，实际 %d", tbl, want, counts[tbl])
		}
	}
}

// ---- 测试账号 / 角色绑定 / 仓库绑定（数据权限验证前提） ----

func TestDevSeedAccountsRolesAndWarehouseBinding(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))

	users := insertsOf(t, ins, "users", 1)
	if !hasCol(users.columns, "data_scope") || !hasCol(users.columns, "password_hash") {
		t.Fatalf("users INSERT 缺少 password_hash/data_scope 列: %v", users.columns)
	}
	if len(users.tuples) < 5 {
		t.Fatalf("测试账号应 ≥5 个，实际 %d", len(users.tuples))
	}
	names := map[string]bool{}
	scoped := 0
	for _, tup := range users.tuples {
		u := normLit(tup[1])
		if names[u] {
			t.Fatalf("测试账号重复: %s", u)
		}
		names[u] = true
		if !strings.HasPrefix(u, "dev_") {
			t.Fatalf("测试账号应统一 dev_ 前缀（误灌可检出）: %s", u)
		}
		if normLit(tup[8]) != "ACTIVE" {
			t.Fatalf("测试账号 %s 应为 ACTIVE", u)
		}
		if normLit(tup[7]) == "SPECIFIED_WAREHOUSE" {
			scoped++
		}
	}
	if scoped < 2 {
		t.Fatalf("绑定仓库型账号（SPECIFIED_WAREHOUSE）应 ≥2 个以验证数据权限，实际 %d", scoped)
	}

	uw := insertsOf(t, ins, "user_warehouses", 1)
	userIDs := map[string]bool{}
	for _, tup := range users.tuples {
		userIDs[tup[0]] = true
	}
	whIDs := map[string]bool{}
	for _, tup := range uw.tuples {
		if !userIDs[tup[0]] {
			t.Fatalf("user_warehouses 引用了不存在的演示账号 id %s", tup[0])
		}
		whIDs[normLit(tup[1])] = true
	}
	if len(whIDs) < 2 {
		t.Fatalf("至少 2 个账号绑定不同仓库（数据权限对照），实际绑定仓库集 %v", whIDs)
	}

	ur := insertsOf(t, ins, "user_roles", 1)
	wantRoles := map[string]bool{
		"warehouse_manager": true, "receiver": true, "shipper": true,
		"stocktaker": true, "viewer": true,
	}
	gotRoles := map[string]bool{}
	for _, tup := range ur.tuples {
		rc := normLit(tup[1])
		if !wantRoles[rc] {
			t.Fatalf("演示账号绑定了预期之外的角色 %s（管理员由生产初始化的 admin 承担）", rc)
		}
		gotRoles[rc] = true
	}
	if len(gotRoles) < 4 {
		t.Fatalf("管理员外角色应 ≥4 种（仓库经理/收货员/发货员/盘点员/财务查看），实际 %d 种", len(gotRoles))
	}
}

// ---- 仓库四级结构：2 仓 × 每仓库区 ≥2 × 每库区货架 ≥2 × 每货架库位 ≥2 ----

func TestDevSeedWarehouseStructure(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	whs := insertsOf(t, ins, "warehouses", 1)
	if len(whs.tuples) < 2 {
		t.Fatalf("演示仓库应 ≥2 个，实际 %d", len(whs.tuples))
	}
	zones := insertsOf(t, ins, "zones", 1)
	shelves := insertsOf(t, ins, "shelves", 1)
	bins := insertsOf(t, ins, "bins", 1)

	zonesPerWH := map[string]int{}
	zoneWH := map[string]string{}
	for _, tup := range zones.tuples {
		zonesPerWH[tup[1]]++
		zoneWH[tup[0]] = tup[1]
	}
	for wh, n := range zonesPerWH {
		if n < 2 {
			t.Fatalf("仓库 %s 库区数应 ≥2，实际 %d", wh, n)
		}
	}
	shelvesPerZone := map[string]int{}
	shelfZone := map[string]string{}
	for _, tup := range shelves.tuples {
		shelvesPerZone[tup[2]]++
		shelfZone[tup[0]] = tup[2]
	}
	for z, n := range shelvesPerZone {
		if n < 2 {
			t.Fatalf("库区 %s 货架数应 ≥2，实际 %d", z, n)
		}
	}
	binsPerShelf := map[string]int{}
	for _, tup := range bins.tuples {
		binsPerShelf[tup[3]]++
		// 层级一致性：bin 的 warehouse_id 必须等于 shelf→zone→warehouse 推导值
		if zoneWH[shelfZone[tup[3]]] != tup[1] {
			t.Fatalf("库位 %s 的 warehouse_id 与所属货架层级推导不一致", tup[6])
		}
	}
	for sh, n := range binsPerShelf {
		if n < 2 {
			t.Fatalf("货架 %s 库位数应 ≥2（每货架若干库位），实际 %d", sh, n)
		}
	}
	if len(binsPerShelf) != len(shelves.tuples) {
		t.Fatalf("每个货架都应有库位：货架 %d 个，有库位的货架 %d 个", len(shelves.tuples), len(binsPerShelf))
	}
}

// ---- 主数据：商品/SKU 四组合/条码/供应商/客户/批次 ----

func TestDevSeedMasterdataCoverage(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))

	cats := insertsOf(t, ins, "product_categories", 1)
	if len(cats.tuples) < 4 {
		t.Fatalf("商品分类应 ≥4（含父子两级），实际 %d", len(cats.tuples))
	}
	units := insertsOf(t, ins, "units", 1)
	if len(units.tuples) < 3 {
		t.Fatalf("计量单位应 ≥3，实际 %d", len(units.tuples))
	}
	prods := insertsOf(t, ins, "products", 1)
	if len(prods.tuples) < 10 {
		t.Fatalf("演示商品应 ≥10，实际 %d", len(prods.tuples))
	}
	skus := insertsOf(t, ins, "skus", 1)
	if len(skus.tuples) < 20 {
		t.Fatalf("演示 SKU 应 ≥20，实际 %d", len(skus.tuples))
	}
	prodIDs := map[string]bool{}
	skuFlagsByCode := map[string][3]bool{}
	batchFlagByID := map[string]bool{}
	for _, tup := range prods.tuples {
		prodIDs[tup[0]] = true
	}
	combos := map[[3]bool]bool{}
	for _, tup := range skus.tuples {
		if !prodIDs[tup[2]] {
			t.Fatalf("SKU %s 引用了不存在的商品 id %s", tup[1], tup[2])
		}
		var f [3]bool
		for i, raw := range tup[9:12] {
			switch normLit(raw) {
			case "TRUE":
				f[i] = true
			case "FALSE":
				f[i] = false
			default:
				t.Fatalf("SKU %s 三开关值非法: %s", tup[1], raw)
			}
		}
		combos[f] = true
		skuFlagsByCode[tup[1]] = f
		batchFlagByID[tup[0]] = f[0]
	}
	for _, c := range [][3]bool{{false, false, false}, {true, false, false}, {true, true, false}, {false, false, true}} {
		if !combos[c] {
			t.Fatalf("SKU 未覆盖组合 批次=%v 效期=%v 序列号=%v（要求四种组合齐备）", c[0], c[1], c[2])
		}
	}

	bcs := insertsOf(t, ins, "barcodes", 1)
	primaryPerSKU := map[string]int{}
	for _, tup := range bcs.tuples {
		code := normLit(tup[2])
		if normLit(tup[3]) == "EAN13" && !ean13CheckOK(code) {
			t.Fatalf("EAN13 校验位无效: %s", code)
		}
		if normLit(tup[4]) == "TRUE" {
			primaryPerSKU[tup[1]]++
		}
	}
	if len(primaryPerSKU) < len(skus.tuples) {
		t.Fatalf("每个 SKU 都应有主条码：SKU %d 个，有主条码的 %d 个", len(skus.tuples), len(primaryPerSKU))
	}

	sups := insertsOf(t, ins, "suppliers", 1)
	cuss := insertsOf(t, ins, "customers", 1)
	if len(sups.tuples) < 3 || len(cuss.tuples) < 3 {
		t.Fatalf("供应商应 ≥3（实际 %d）、客户应 ≥3（实际 %d）", len(sups.tuples), len(cuss.tuples))
	}
	bats := insertsOf(t, ins, "batches", 1)
	if len(bats.tuples) < 5 {
		t.Fatalf("演示批次应 ≥5，实际 %d", len(bats.tuples))
	}
	for _, tup := range bats.tuples {
		if !batchFlagByID[tup[1]] {
			t.Fatalf("批次 %s 挂在未启用批次管理的 SKU 上（sku_id=%s）", tup[2], tup[1])
		}
	}
}

// ---- 期初库存只写 total/available，且与期初流水同事务 1:1 成对（0→n、期初、DEV SEED） ----

func TestDevSeedOpeningInventoryPairedWithLedger(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	inv := insertsOf(t, ins, "inventory", 1)
	// 2026-10-05 联调轮：inventory_ledgers 有两条 INSERT（期初成对段 + 演示出库补影段）。
	// 成对断言只圈定期初条（含 '期初' 字面口径）；补影条按净零对单独断言（语义与
	// dev_seed_verify.sql §6 按 business_type='期初' 圈定同口径）。
	var led, shadow seedInsert
	shadowCount := 0
	for _, s := range ins {
		if s.table != "inventory_ledgers" {
			continue
		}
		if strings.Contains(s.text, "'期初'") {
			led = s
		} else {
			shadow = s
			shadowCount++
		}
	}
	if led.text == "" {
		t.Fatal("未找到期初流水 INSERT（应含 '期初' 口径字面）")
	}
	if shadowCount != 1 {
		t.Fatalf("补影流水 INSERT 应恰 1 条，实际 %d", shadowCount)
	}
	if led.text == "" {
		t.Fatal("未找到期初流水 INSERT（应含 '期初' 口径字面）")
	}
	// 补影段净零对：Σ qty_change = 0（不改任何现存量锚点）
	var shadowNet float64
	for _, tup := range shadow.tuples {
		if len(tup) != 9 {
			t.Fatalf("补影流水 VALUES 元组应为 9 列，实际 %v", tup)
		}
		shadowNet += parseQty(t, tup[6], "补影流水")
	}
	if shadowNet != 0 {
		t.Fatalf("补影流水净变化应恒为 0（净零对），实际 %v", shadowNet)
	}

	// 六列数量中只写 total/available 两列（其余默认 0，恒等式因此成立）
	for _, c := range []string{"total_qty", "available_qty"} {
		if !hasCol(inv.columns, c) {
			t.Fatalf("期初库存 INSERT 缺少 %s 列", c)
		}
	}
	for _, c := range []string{"locked_qty", "frozen_qty", "pending_inspect_qty", "defective_qty"} {
		if hasCol(inv.columns, c) {
			t.Fatalf("期初库存只写 total/available 两列，却出现了 %s", c)
		}
	}
	if len(inv.tuples) < 10 {
		t.Fatalf("期初库存行应 ≥10 才够演示，实际 %d", len(inv.tuples))
	}
	for _, c := range []string{"change_type", "business_type", "business_no", "status_from", "status_to",
		"qty_before", "qty_change", "qty_after", "remark"} {
		if !hasCol(led.columns, c) {
			t.Fatalf("期初流水 INSERT 缺少 %s 列", c)
		}
	}
	// 流水口径（SELECT 列表内）：0→n、INBOUND、期初、available、DEV SEED 标记
	for _, want := range []string{"'INBOUND'", "'期初'", "'DEV SEED'", "'available', 'available'"} {
		if !strings.Contains(led.text, want) {
			t.Fatalf("期初流水口径缺少 %s", want)
		}
	}
	if !regexp.MustCompile(`0,\s*v\.qty,\s*v\.qty`).MatchString(led.text) {
		t.Fatal("期初流水应为 qty_before=0、qty_change=qty_after=n（变化前 0 → 变化后 n）")
	}

	// 成对：同一 (仓库, 库位, SKU, 批次) 键集合与数量完全一致（两段 VALUES 同源成对）
	invQty := map[[3]string]float64{}
	for _, tup := range inv.tuples {
		if len(tup) != 6 {
			t.Fatalf("期初库存 VALUES 元组应为 6 列，实际 %v", tup)
		}
		key := [3]string{normLit(tup[1]), normLit(tup[2]), normLit(tup[3]) + "|" + normLit(tup[4])}
		q := parseQty(t, tup[5], "期初库存")
		if q <= 0 {
			t.Fatalf("期初库存数量应 >0: %v", tup)
		}
		if _, dup := invQty[key]; dup {
			t.Fatalf("期初库存键重复（五维唯一会被 ON CONFLICT 跳过导致流水失衡）: %v", key)
		}
		invQty[key] = q
	}
	ledQty := map[[3]string]float64{}
	for _, tup := range led.tuples {
		if len(tup) != 6 {
			t.Fatalf("期初流水 VALUES 元组应为 6 列，实际 %v", tup)
		}
		key := [3]string{normLit(tup[1]), normLit(tup[2]), normLit(tup[3]) + "|" + normLit(tup[4])}
		ledQty[key] = parseQty(t, tup[5], "期初流水")
	}
	if len(invQty) != len(ledQty) {
		t.Fatalf("期初库存 %d 行与期初流水 %d 行数量不等（必须 1:1 成对）", len(invQty), len(ledQty))
	}
	for key, q := range invQty {
		if ledQty[key] != q {
			t.Fatalf("期初键 %v 库存数量 %v 与流水数量 %v 不一致", key, q, ledQty[key])
		}
	}
	// 流水行数与库存行数相等（元组层面）
	if len(led.tuples) != len(inv.tuples) {
		t.Fatalf("期初库存元组 %d 与期初流水元组 %d 不相等", len(inv.tuples), len(led.tuples))
	}
}

// ---- 序列号：一物一行数量与期初库存吻合，且只挂在序列号管理的 SKU 上 ----

func TestDevSeedSerialsMatchOpeningInventory(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	skus := insertsOf(t, ins, "skus", 1)
	serialFlag := map[string]bool{}
	for _, tup := range skus.tuples {
		serialFlag[tup[1]] = normLit(tup[11]) == "TRUE"
	}
	inv := insertsOf(t, ins, "inventory", 1)
	invQty := map[[2]string]float64{}
	for _, tup := range inv.tuples {
		key := [2]string{normLit(tup[1]), normLit(tup[3])}
		invQty[key] += parseQty(t, tup[5], "期初库存")
	}
	sn := insertsOf(t, ins, "serial_numbers", 1)
	if !strings.Contains(sn.text, "'IN_STOCK'") {
		t.Fatal("演示序列号应为 IN_STOCK 状态")
	}
	snCount := map[[2]string]int{}
	for _, tup := range sn.tuples {
		if len(tup) != 6 {
			t.Fatalf("序列号 VALUES 元组应为 6 列，实际 %v", tup)
		}
		key := [2]string{normLit(tup[2]), normLit(tup[4])}
		if !serialFlag[tup[4]] {
			t.Fatalf("序列号 %s 挂在未启用序列号管理的 SKU %s 上（inventory-rules §8）", tup[1], tup[4])
		}
		snCount[key]++
	}
	if len(snCount) < 3 {
		t.Fatalf("序列号演示应覆盖 ≥3 组 仓库×SKU，实际 %d", len(snCount))
	}
	for key, n := range snCount {
		if invQty[key] != float64(n) {
			t.Fatalf("仓库×SKU %v 序列号 %d 个与期初库存 %v 不吻合（一物一行）", key, n, invQty[key])
		}
	}
}

// ---- bcrypt 口令哈希与注释明文互验（cost 12，BcryptCost 一致） ----

func TestDevSeedBcryptPasswordHashes(t *testing.T) {
	content := readRepoFile(t, devSeedPath)
	if !strings.Contains(content, "仅 dev 测试") {
		t.Fatal("dev_seed.sql 缺少明文密码注释及“仅 dev 测试”标记")
	}
	ins := parseDevSeedInserts(t, stripSQLComments(content))
	users := insertsOf(t, ins, "users", 1)
	plaintexts := map[string]string{
		"dev_manager": "DevMg#2026", "dev_receiver": "DevRc#2026", "dev_shipper": "DevSp#2026",
		"dev_stocktaker": "DevSt#2026", "dev_viewer": "DevVw#2026",
	}
	for _, tup := range users.tuples {
		u := normLit(tup[1])
		want, ok := plaintexts[u]
		if !ok {
			t.Fatalf("存在未登记明文的测试账号 %s（注释明文与账号必须一一对应）", u)
		}
		hash := normLit(tup[2])
		if !strings.HasPrefix(hash, "$2") {
			t.Fatalf("账号 %s 密码不是 bcrypt 哈希", u)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(want)); err != nil {
			t.Fatalf("账号 %s 的哈希与注释明文不匹配: %v", u, err)
		}
		cost, err := strconv.Atoi(strings.Split(hash, "$")[2])
		if err != nil || cost != BcryptCost {
			t.Fatalf("账号 %s 哈希 cost=%d 应与 BcryptCost=%d 一致", u, cost, BcryptCost)
		}
		delete(plaintexts, u)
	}
	if len(plaintexts) != 0 {
		t.Fatalf("以下明文密码没有对应的测试账号行: %v", plaintexts)
	}
}

// ---- 无生产初始化污染：生产初始化表零写入、启动路径零引用、Makefile 门禁在位 ----

func TestDevSeedNoProductionInitPollution(t *testing.T) {
	content := readRepoFile(t, devSeedPath)
	ins := parseDevSeedInserts(t, stripSQLComments(content))
	for _, s := range ins {
		switch s.table {
		case "roles", "permissions", "role_permissions", "operation_logs", "login_logs", "system_configs":
			t.Fatalf("dev_seed.sql 禁止写入生产初始化/审计表 %s（internal/database/seed.go 专职负责）", s.table)
		}
	}
	if strings.Contains(content, "WH-DEFAULT") {
		t.Fatal("dev_seed.sql 不得触碰生产初始化默认仓库 WH-DEFAULT（seed.go DefaultWarehouseCode）")
	}
	if strings.Contains(content, "'admin'") {
		t.Fatal("dev_seed.sql 不得创建/触碰默认管理员 admin（生产初始化专职）")
	}

	// 启动路径零引用：cmd/ 与 internal/ 生产代码不得以字符串/嵌入方式加载演示数据
	// （seed.go 头注的文档性提及不算加载；此处只匹配单行字符串字面量与 go:embed 形态）。
	roots := []string{"../../cmd", "../../internal"}
	loadRe := regexp.MustCompile(`"(?:[^\n"]*db/seed[^\n"]*|[^\n"]*dev_seed[^\n"]*)"|go:embed[^\n]*dev_seed`)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if m := loadRe.Find(b); m != nil {
				t.Fatalf("生产代码 %s 以字符串/嵌入方式引用演示数据（启动路径不得加载 db/seed）: %s", path, m)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描 %s 失败: %v", root, err)
		}
	}
}

// ---- Makefile 门禁与 verify 目标 ----

func extractMakeTarget(t *testing.T, mk, name string) string {
	t.Helper()
	lines := strings.Split(mk, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimRight(ln, " \r") == name+":" {
			start = i + 1
			break
		}
	}
	if start == 0 {
		t.Fatalf("Makefile 缺少目标 %s", name)
	}
	var body []string
	for _, ln := range lines[start:] {
		if strings.TrimSpace(ln) != "" && !strings.HasPrefix(ln, "\t") {
			break
		}
		body = append(body, ln)
	}
	return strings.Join(body, "\n")
}

func TestDevSeedMakefileGate(t *testing.T) {
	mk := readRepoFile(t, "../../Makefile")
	seed := extractMakeTarget(t, mk, "seed-demo")
	if !strings.Contains(seed, `"$(SF_ENV)" = "dev"`) {
		t.Fatal("make seed-demo 必须强制 SF_ENV=dev（database.md §8.2 第一道门禁）")
	}
	if !strings.Contains(seed, "-v is_dev=1") || !strings.Contains(seed, "dev_seed.sql") {
		t.Fatal("make seed-demo 必须以 -v is_dev=1 执行 dev_seed.sql（第二道门禁）")
	}
	verify := extractMakeTarget(t, mk, "seed-demo-verify")
	if !strings.Contains(verify, "dev_seed_verify.sql") {
		t.Fatal("make seed-demo-verify 应执行 db/seed/dev_seed_verify.sql")
	}
}

// ---- verify 脚本：只读、覆盖成对/恒等式断言 ----

func TestDevSeedVerifyScriptReadOnly(t *testing.T) {
	content := readRepoFile(t, devSeedVerifyPath)
	if !strings.Contains(content, `\set ON_ERROR_STOP on`) {
		t.Fatal("dev_seed_verify.sql 缺少 ON_ERROR_STOP（断言失败必须非 0 退出）")
	}
	body := strings.ToUpper(stripSQLComments(content))
	for _, bad := range []string{"INSERT INTO", "UPDATE ", "DELETE FROM", "TRUNCATE", "DROP ", "ALTER ", "GRANT ", "COPY "} {
		if strings.Contains(body, bad) {
			t.Fatalf("dev_seed_verify.sql 必须只读，却包含 %q", bad)
		}
	}
	for _, need := range []string{"'期初'", "'DEV SEED'", "DEV-SEED-OPEN-", "DO $$"} {
		if !strings.Contains(content, need) {
			t.Fatalf("dev_seed_verify.sql 缺少成对/标记断言要素: %s", need)
		}
	}
}

// ---- 语法底线：括号/引号配平（无 PG 环境的最低限度防线） ----

func TestDevSeedSQLBalance(t *testing.T) {
	for _, path := range []string{devSeedPath, devSeedVerifyPath} {
		content := readRepoFile(t, path)
		// 注释先剥离：注释中的 ASCII 括号/引号（如编号"1)"）不参与 SQL 结构配平
		content = stripSQLComments(content)
		depth, inQ := 0, false
		for i := 0; i < len(content); i++ {
			switch {
			case inQ:
				if content[i] == '\'' {
					inQ = false
				}
			case content[i] == '\'':
				inQ = true
			case content[i] == '(':
				depth++
			case content[i] == ')':
				depth--
				if depth < 0 {
					t.Fatalf("%s 多余的右括号（位置 %d）", path, i)
				}
			}
		}
		if depth != 0 {
			t.Fatalf("%s 括号不配平（深度 %d）", path, depth)
		}
		if inQ {
			t.Fatalf("%s 字符串引号不配对", path)
		}
		if strings.Count(content, "'")%2 != 0 {
			t.Fatalf("%s 单引号数量为奇数（引号不配对）", path)
		}
		if strings.Contains(content, "''") {
			t.Fatalf("%s 使用了转义引号（本文件约定避免，保障静态校验可靠）", path)
		}
	}
}
