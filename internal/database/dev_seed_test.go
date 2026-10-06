package database

// dev_seed.sql / dev_seed_verify.sql 演示数据交付物的静态自查（不依赖 PostgreSQL/psql，
// 本环境无 PG——database.md §9 的真库验证须在具备 PG 的环境补做）。
//
// 覆盖（本 ask 验收口径；2026-10-05 电子厂场景扩容轮随 db/seed/dev_seed.sql §10/§11
// 同步更新静态契约——§10 场景扩展对主数据/仓储/库存各追加 1 条 INSERT（每表 2 条，
// inventory_ledgers 期初成对+补影各 2 条=4 条），§11 单据链新增 29 张自然键幂等表）：
//   - 门禁：is_dev 变量开关、拒绝文案、ON_ERROR_STOP、整体 BEGIN/COMMIT 单事务；
//   - INSERT-only + 幂等：禁止独立 UPDATE/DELETE/TRUNCATE/DROP/ALTER；幂等守卫三形态
//     （ON CONFLICT DO NOTHING / doc_number_counters 单调 upsert DO UPDATE+GREATEST /
//     document_approvals NOT EXISTS）；
//   - 显式 9xxx 主键（带 id 列的表）与自然键白名单（§11 单据链表）+ 每表 INSERT 条数冻结；
//   - 实体覆盖：测试账号/角色/仓库绑定、仓库四级结构、商品/SKU 四种三开关组合/条码/供应商/客户；
//   - 期初库存只写 total/available 两列，且与期初流水同文件同事务按段 1:1 成对（0→n、
//     期初、DEV SEED；§8 基础段与 §10 电子厂段各自成对，合并后键集合仍须一致）；
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

// insertsOf 取某表的全部 INSERT（不做条数校验）。表内可能同时存在不同构的 INSERT
// （如 inventory 的期初行只写 total/available、§12.1 状态行六列全写），故先取再分类。
func insertsOf(ins []seedInsert, table string) []seedInsert {
	var out []seedInsert
	for _, s := range ins {
		if s.table == table {
			out = append(out, s)
		}
	}
	return out
}

// mergeInserts 把若干同构（列清单完全一致）INSERT 合并为一条：元组串接、文本拼接后
// 供成对/覆盖断言跨段统一计算。列清单不一致即失败（异形段不得误合并）。
func mergeInserts(t *testing.T, found []seedInsert, what string) seedInsert {
	t.Helper()
	if len(found) == 0 {
		t.Fatalf("%s 未解析到 INSERT", what)
	}
	merged := found[0]
	for _, s := range found[1:] {
		if strings.Join(s.columns, ",") != strings.Join(merged.columns, ",") {
			t.Fatalf("%s 的多条 INSERT 列清单不一致（同构段才能合并）", what)
		}
		merged.tuples = append(merged.tuples, s.tuples...)
		merged.text += "\n" + s.text
	}
	return merged
}

// mergedInsertsOf 取某表全部 INSERT（条数须恰为 want）并合并为一条：要求各条列清单
// 完全一致（§10 电子厂扩展段必须与基础段同构），元组串接、文本拼接后供成对/覆盖断言
// 跨段计算（2026-10-05 §10/§11 扩容后基础段各表 2 条、inventory_ledgers 4 条）。
func mergedInsertsOf(t *testing.T, ins []seedInsert, table string, want int) seedInsert {
	t.Helper()
	found := insertsOf(ins, table)
	if len(found) != want {
		t.Fatalf("表 %s 的 INSERT 语句应为 %d 条，实际 %d", table, want, len(found))
	}
	if want == 0 {
		return seedInsert{}
	}
	return mergeInserts(t, found, "表 "+table)
}

// seedAliasRe SELECT 型插入的 `... FROM (VALUES ...) AS v(col, ...)` 源别名清单。
var seedAliasRe = regexp.MustCompile(`(?is)AS\s+v\s*\(([^()]*)\)`)

// aliasIndex 解析 INSERT 语句 VALUES 源的 `AS v(col, ...)` 别名清单为 列名→元组下标
// （元组由 parseDevSeedInserts 从内层 VALUES 提取，与别名一一对应；多条语句同构时
// 取末次清单即可）。期初成对/补影净零断言按列名定位——§8 与 §10 段元组宽度可不同
// （如补影段 §10 增加 batch_no 列），位置硬编码跨段不可靠。
func aliasIndex(t *testing.T, stmt, ctx string) map[string]int {
	t.Helper()
	ms := seedAliasRe.FindAllStringSubmatch(stmt, -1)
	if len(ms) == 0 {
		t.Fatalf("%s 缺少 AS v(...) VALUES 源别名清单", ctx)
	}
	out := map[string]int{}
	for i, name := range strings.Split(ms[len(ms)-1][1], ",") {
		out[strings.TrimSpace(name)] = i
	}
	return out
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
	// INSERT-only：seed 禁止破坏性/覆盖性写入。唯一豁免：ON CONFLICT DO UPDATE 形态
	// （doc_number_counters 单号计数单调推进，next_no = GREATEST(现值, EXCLUDED)——
	// 幂等可重跑且不回退，2026-10-05 §11 引入；剥离该形态后仍禁任何独立 UPDATE）。
	body := strings.ToUpper(stripSQLComments(content))
	bodyNoUpsert := strings.ReplaceAll(body, "DO UPDATE", "")
	for _, bad := range []string{"UPDATE ", "DELETE FROM", "TRUNCATE", "DROP ", "ALTER "} {
		if strings.Contains(bodyNoUpsert, bad) {
			t.Fatalf("dev_seed.sql 含有禁止的写入形态 %q（seed 仅允许 INSERT 与单调计数 upsert，防覆盖已有数据）", bad)
		}
	}
	// 幂等守卫三形态（§11 起）：ON CONFLICT DO NOTHING（一般业务表）/ ON CONFLICT
	// DO UPDATE（仅计数器，必须伴随 GREATEST 防回退）/ WHERE NOT EXISTS（除主键外无唯一
	// 索引的追加表：document_approvals 审批留痕、allocation_records 分配记录）。
	// 每条 INSERT 必须恰落其一——三形态计数之和等于 INSERT 语句总数（2026-10-06 真库
	// 验证发现 allocation_records 无自然唯一键，ON CONFLICT DO NOTHING 不触发导致重跑
	// 每次重复追加 3 行，故补齐第三形态并在此按总数强校验）。
	nIns := len(locs)
	nConflict := strings.Count(body, "ON CONFLICT")
	nNothing := strings.Count(body, "DO NOTHING")
	nUpsert := strings.Count(body, "DO UPDATE")
	nNotExists := strings.Count(body, "NOT EXISTS")
	if nConflict == 0 || nConflict != nNothing+nUpsert {
		t.Fatalf("每条 ON CONFLICT 都应配 DO NOTHING（或计数器 DO UPDATE 单调推进），ON CONFLICT=%d DO NOTHING=%d DO UPDATE=%d",
			nConflict, nNothing, nUpsert)
	}
	if nConflict+nNotExists != nIns {
		t.Fatalf("每条 INSERT 都必须带幂等守卫（ON CONFLICT DO NOTHING / 计数器 DO UPDATE / WHERE NOT EXISTS），INSERT=%d ON CONFLICT=%d NOT EXISTS=%d",
			nIns, nConflict, nNotExists)
	}
	if nUpsert > 0 && (!strings.Contains(body, "GREATEST(") || !strings.Contains(body, "EXCLUDED.")) {
		t.Fatalf("DO UPDATE 仅允许计数器单调推进形态（next_no = GREATEST(现值, EXCLUDED)），实际缺 GREATEST/EXCLUDED 防回退守卫")
	}
	if nNotExists == 0 {
		t.Fatal("无自然唯一键的追加表（document_approvals / allocation_records）应以 WHERE NOT EXISTS 防重跑重复")
	}
}

// TestDevSeedBareConflictOnNaturalKeyTables 带自然键唯一索引的表不得用 ON CONFLICT (id)：
// 运行期（扫码/收货/打印/导出/异常登记）已按 docnum 同日期段生成同号单据，再灌演示数据
// 会撞自然键唯一索引并中断整个 seed 事务（2026-10-06 真库验证实测 abort）。
func TestDevSeedBareConflictOnNaturalKeyTables(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	byTable := map[string]int{}
	for _, s := range ins {
		if !devSeedBareConflictTables[s.table] {
			continue
		}
		byTable[s.table]++
		upper := strings.ToUpper(s.text)
		if !strings.Contains(upper, "ON CONFLICT") {
			t.Fatalf("表 %s 缺少 ON CONFLICT 幂等守卫", s.table)
		}
		if regexp.MustCompile(`ON CONFLICT\s*\(\s*ID\s*\)`).MatchString(upper) {
			t.Fatalf("表 %s 带自然键唯一索引，不得用 ON CONFLICT (id)（同号自然键会撞唯一索引并中断整个 seed 事务）", s.table)
		}
	}
	for tbl := range devSeedBareConflictTables {
		if byTable[tbl] == 0 {
			t.Fatalf("冻结清单中的表 %s 未在 dev_seed.sql 中解析到 INSERT（清单需同步）", tbl)
		}
	}
}

// TestDevSeedReferencedCodesExist 静态校验演示数据引用的仓库/库位/SKU 编码都在本文件内定义：
// 用 `JOIN ... ON b.code = v.bin_code` 定位的段一旦写错编码（§10.6 曾把 FP-01-11 误写成
// FR-01-11），JOIN 不命中 → 该行被**静默丢弃**，静态契约与真库 verify 都只会看到「少了几行」。
// 该缺陷 2026-10-06 由真库验证发现（inventory 落库 47 而非 48 行），此处前置拦截。
func TestDevSeedReferencedCodesExist(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))

	collect := func(table, col string, want int) map[string]bool {
		m := mergedInsertsOf(t, ins, table, want)
		idx := -1
		for i, c := range m.columns {
			if c == col {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("%s INSERT 缺少 %s 列", table, col)
		}
		out := map[string]bool{}
		for _, tup := range m.tuples {
			out[normLit(tup[idx])] = true
		}
		return out
	}
	valid := map[string]map[string]bool{
		"仓库":  collect("warehouses", "code", 2),
		"库位":  collect("bins", "code", 2),
		"SKU": collect("skus", "code", 2),
	}
	kindOf := func(alias string) string {
		switch alias {
		case "wh_code", "from_wh", "to_wh":
			return "仓库"
		case "bin_code", "from_bin", "to_bin":
			return "库位"
		case "sku_code":
			return "SKU"
		}
		return ""
	}
	checked := 0
	for _, s := range ins {
		if len(seedAliasRe.FindAllStringSubmatch(s.text, -1)) == 0 {
			continue // 纯 VALUES 插入（bins/warehouses 等定义段）无 AS v(...) 别名，不引用编码
		}
		idx := aliasIndex(t, s.text, s.table)
		for alias, pos := range idx {
			kind := kindOf(alias)
			if kind == "" {
				continue
			}
			for _, tup := range s.tuples {
				code := normLit(tup[pos])
				if code == "" {
					continue // 允许空串（如系统类异常单无 SKU/库位）
				}
				if !valid[kind][code] {
					t.Fatalf("表 %s 引用了本文件未定义的%s编码 %q（JOIN 不命中会静默丢弃该行）", s.table, kind, code)
				}
				checked++
			}
		}
	}
	if checked < 200 {
		t.Fatalf("编码引用校验覆盖数偏低（%d），解析器可能失效", checked)
	}
}

// ---- 显式 9xxx 主键 + 白名单表 + 每表 INSERT 条数冻结 ----

// devSeedExpectedCounts 每表 INSERT 条数冻结表（2026-10-06 演示数据补全轮后契约）：
// §0–§9 基础段每表 1 条；§10 电子厂扩展段对主数据/仓储/库存各追加 1 条；
// §11 单据链每表 1 条；§12 状态行 + 库存锁定/调整、§13 退货/异常、§14 设备、
// §15 数据、§16 运维、§17 各单据状态补全各追加 1 条（退货单 §13.1/§13.2 两条）。
// 任何增删都必须有意识地改本表（防漏写/误写双份——与 routes 冻结端点集同思路）。
var devSeedExpectedCounts = map[string]int{
	// 基础段（单条）
	"departments": 1, "users": 1, "user_roles": 1, "user_warehouses": 1,
	// 基础段 + §10 电子厂扩展段（各 1 条）
	"warehouses": 2, "zones": 2, "shelves": 2, "bins": 2,
	"product_categories": 2, "units": 2, "products": 2, "skus": 2, "barcodes": 2,
	"suppliers": 2, "customers": 2, "batches": 2,
	// 期初段 ×2 + §12.1 状态行 ×1
	"inventory": 3, "serial_numbers": 2,
	// 期初成对 ×3 段 + 演示出库补影 ×2 段
	"inventory_ledgers": 5,
	// §11 单据链（自然键幂等，每表 1 条）+ §17 状态补全（每表 1 条）
	"purchase_orders": 2, "purchase_order_items": 2,
	"inbound_orders": 2, "inbound_items": 2,
	"receipts": 1, "receipt_items": 1,
	"quality_orders": 2, "quality_items": 2,
	"putaway_tasks": 2,
	"sales_orders":  2, "sales_order_items": 2,
	"outbound_orders": 2, "outbound_items": 2,
	"allocation_records": 1, "pick_tasks": 2, "check_tasks": 2,
	"packing_records": 1, "packing_items": 1, "shipments": 2,
	"transfer_orders": 2, "transfer_items": 2,
	"count_orders": 2, "count_items": 2, "count_differences": 2,
	"print_templates": 1, "print_tasks": 1, "print_task_rows": 1,
	"doc_number_counters": 2, "document_approvals": 2,
	// §12 演示数据补全轮新增表（各 1 条，除 return_orders 销售/采购两条）
	"inventory_locks": 1, "inventory_adjustments": 1,
	"return_orders": 2, "return_items": 1,
	"exceptions": 1,
	"devices":    1, "device_configs": 1, "device_logs": 1, "scan_logs": 1, "app_versions": 1,
	"files": 1, "import_tasks": 1, "import_task_rows": 1, "export_tasks": 1,
	"scheduled_job_runs": 1, "notifications": 1, "backup_records": 1,
}

// devSeedNaturalKeyTables §11 单据链/共享平台中不带显式 id 列的表：以自然键
// （单号/编号）ON CONFLICT（或 document_approvals 的 WHERE NOT EXISTS）幂等，
// 不适用 9xxx 显式主键约束（print_templates/print_task_rows 带 id，不在本集）。
var devSeedNaturalKeyTables = map[string]bool{
	"purchase_orders": true, "purchase_order_items": true,
	"inbound_orders": true, "inbound_items": true,
	"receipts": true, "receipt_items": true,
	"quality_orders": true, "quality_items": true,
	"putaway_tasks": true,
	"sales_orders":  true, "sales_order_items": true,
	"outbound_orders": true, "outbound_items": true,
	"allocation_records": true, "pick_tasks": true, "check_tasks": true,
	"packing_records": true, "packing_items": true, "shipments": true,
	"transfer_orders": true, "transfer_items": true,
	"count_orders": true, "count_items": true, "count_differences": true,
	"print_tasks":         true,
	"doc_number_counters": true, "document_approvals": true,
}

// devSeedBareConflictTables 除主键 id 外还带自然键唯一索引的表（迁移 DDL 事实）：
//
//	exceptions(uk_exceptions_no) / return_orders(uk_return_orders_no) /
//	return_items(uk_return_items_return_line) / inventory_adjustments(uk_inventory_adjustments_no) /
//	devices(uk_devices_code) / device_configs(uk_device_configs_device) /
//	app_versions(uk_app_versions_platform_code) / import_tasks(uk_import_tasks_no) /
//	import_task_rows(uk_import_task_rows_task_row) / export_tasks(uk_export_tasks_no) /
//	notifications(uk_notifications_dedup 部分索引) / backup_records(uk_backup_records_inflight 部分索引) /
//	print_tasks(uk_print_tasks_no) / print_task_rows(uk_print_task_rows_task_seq)。
//
// 这些表的幂等守卫必须是无目标的 ON CONFLICT DO NOTHING：写成 ON CONFLICT (id) 时，
// 运行期（扫码/收货/打印/导出/异常登记）已按 docnum 同日期段生成同号单据，
// 再灌演示数据会撞自然键唯一索引并**中断整个 seed 事务**——2026-10-06 真库验证实测到
// （exceptions 撞 uk_exceptions_no 直接 abort）。静态解析器看不到 DDL 索引，故冻结于此。
var devSeedBareConflictTables = map[string]bool{
	"exceptions": true, "return_orders": true, "return_items": true,
	"inventory_adjustments": true, "devices": true, "device_configs": true,
	"app_versions": true, "import_tasks": true, "import_task_rows": true,
	"export_tasks": true, "notifications": true, "backup_records": true,
	"print_tasks": true, "print_task_rows": true,
}

func TestDevSeedExplicitPKsIn9xxxRange(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	wantTotal := 0
	for _, n := range devSeedExpectedCounts {
		wantTotal += n
	}
	if len(ins) != wantTotal {
		t.Fatalf("INSERT 语句应恰为 %d 条（每表条数冻结表合计），实际 %d", wantTotal, len(ins))
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, s := range ins {
		counts[s.table]++
		if _, ok := devSeedExpectedCounts[s.table]; !ok {
			t.Fatalf("dev_seed.sql 出现冻结契约之外的写入目标 %s（roles/permissions 等生产初始化表禁止写入；新表须更新 devSeedExpectedCounts）", s.table)
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
			// 自然键幂等表（§11 单据链）：无显式 id，凭单号/编号 ON CONFLICT 幂等
			if !devSeedNaturalKeyTables[s.table] {
				t.Fatalf("业务表 %s 未显式指定主键 id，也不在自然键白名单（要求固定显式主键保证幂等）", s.table)
			}
			// 纯 SELECT-JOIN 形态（无 VALUES 源，如 count_differences 按 JOIN 现有行
			// 生成差异行）解析不到元组属预期；VALUES 源形态必须有数据行。
			if len(s.tuples) == 0 && !strings.Contains(strings.ToUpper(s.text), "SELECT") {
				t.Fatalf("自然键表 %s 未解析到数据行", s.table)
			}
			continue
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
	for tbl, want := range devSeedExpectedCounts {
		if counts[tbl] != want {
			t.Fatalf("表 %s 的 INSERT 语句数应为 %d，实际 %d", tbl, want, counts[tbl])
		}
	}
}

// ---- 测试账号 / 角色绑定 / 仓库绑定（数据权限验证前提） ----

func TestDevSeedAccountsRolesAndWarehouseBinding(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))

	users := mergedInsertsOf(t, ins, "users", 1)
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

	uw := mergedInsertsOf(t, ins, "user_warehouses", 1)
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

	ur := mergedInsertsOf(t, ins, "user_roles", 1)
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
	whs := mergedInsertsOf(t, ins, "warehouses", 2)
	if len(whs.tuples) < 2 {
		t.Fatalf("演示仓库应 ≥2 个，实际 %d", len(whs.tuples))
	}
	zones := mergedInsertsOf(t, ins, "zones", 2)
	shelves := mergedInsertsOf(t, ins, "shelves", 2)
	bins := mergedInsertsOf(t, ins, "bins", 2)

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
		// ≥1：任何库区不得无货架。§10 电子厂扩展的不良品隔离区（NG）按作业形态
		// 仅 1 组货架，不再要求基础段的 ≥2 密度（仓库级库区 ≥2 下限仍保留）。
		if n < 1 {
			t.Fatalf("库区 %s 货架数应 ≥1（不得存在空库区），实际 %d", z, n)
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
		// ≥1：任何货架不得无库位。§10 电子厂扩展的半成品仓 SB-01 等作业形态
		// 仅 1 库位（demo 密度不再强制基础段的 ≥2）。
		if n < 1 {
			t.Fatalf("货架 %s 库位数应 ≥1（不得存在空货架），实际 %d", sh, n)
		}
	}
	if len(binsPerShelf) != len(shelves.tuples) {
		t.Fatalf("每个货架都应有库位：货架 %d 个，有库位的货架 %d 个", len(shelves.tuples), len(binsPerShelf))
	}
}

// ---- 主数据：商品/SKU 四组合/条码/供应商/客户/批次 ----

func TestDevSeedMasterdataCoverage(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))

	cats := mergedInsertsOf(t, ins, "product_categories", 2)
	if len(cats.tuples) < 4 {
		t.Fatalf("商品分类应 ≥4（含父子两级），实际 %d", len(cats.tuples))
	}
	units := mergedInsertsOf(t, ins, "units", 2)
	if len(units.tuples) < 3 {
		t.Fatalf("计量单位应 ≥3，实际 %d", len(units.tuples))
	}
	prods := mergedInsertsOf(t, ins, "products", 2)
	if len(prods.tuples) < 10 {
		t.Fatalf("演示商品应 ≥10，实际 %d", len(prods.tuples))
	}
	skus := mergedInsertsOf(t, ins, "skus", 2)
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

	bcs := mergedInsertsOf(t, ins, "barcodes", 2)
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

	sups := mergedInsertsOf(t, ins, "suppliers", 2)
	cuss := mergedInsertsOf(t, ins, "customers", 2)
	if len(sups.tuples) < 3 || len(cuss.tuples) < 3 {
		t.Fatalf("供应商应 ≥3（实际 %d）、客户应 ≥3（实际 %d）", len(sups.tuples), len(cuss.tuples))
	}
	bats := mergedInsertsOf(t, ins, "batches", 2)
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

	// 库存 INSERT 分两类：期初行（§8/§10，只写 total/available 两列）与状态行
	//（§12.1，六列全写）。两类各与同段 '期初' 流水 1:1 成对，合并后键集合仍须整体一致。
	var invOpening, invStatus []seedInsert
	for _, s := range insertsOf(ins, "inventory") {
		if hasCol(s.columns, "locked_qty") {
			invStatus = append(invStatus, s)
		} else {
			invOpening = append(invOpening, s)
		}
	}
	if len(invOpening) != 2 || len(invStatus) != 1 {
		t.Fatalf("期初库存 INSERT 应 2 条（§8+§10）、状态行 INSERT 应 1 条（§12.1），实际 %d/%d",
			len(invOpening), len(invStatus))
	}
	inv := mergeInserts(t, invOpening, "期初库存")

	// 期初段只写 total/available 两列（其余默认 0，恒等式因此成立；两段列清单同构）
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

	// 状态行（§12.1）：六列全写，逐行满足恒等式
	// total = available + locked + frozen + pending_inspect + defective，且四态齐备。
	status := invStatus[0]
	statusIdx := aliasIndex(t, status.text, "状态行库存")
	for _, c := range []string{"qty", "avail", "locked", "frozen", "pending", "defect"} {
		if _, ok := statusIdx[c]; !ok {
			t.Fatalf("状态行库存 VALUES 源别名清单缺少 %s", c)
		}
	}
	if len(status.tuples) < 4 {
		t.Fatalf("状态行应覆盖 ≥4 种库存状态组合，实际 %d", len(status.tuples))
	}
	seenState := map[string]bool{}
	for _, tup := range status.tuples {
		q := parseQty(t, tup[statusIdx["qty"]], "状态行库存")
		a := parseQty(t, tup[statusIdx["avail"]], "状态行库存")
		l := parseQty(t, tup[statusIdx["locked"]], "状态行库存")
		f := parseQty(t, tup[statusIdx["frozen"]], "状态行库存")
		p := parseQty(t, tup[statusIdx["pending"]], "状态行库存")
		d := parseQty(t, tup[statusIdx["defect"]], "状态行库存")
		if q != a+l+f+p+d {
			t.Fatalf("状态行库存不满足恒等式 total=available+locked+frozen+pending_inspect+defective: %v", tup)
		}
		switch {
		case l > 0:
			seenState["locked"] = true
		case f > 0:
			seenState["frozen"] = true
		case p > 0:
			seenState["pending"] = true
		case d > 0:
			seenState["defective"] = true
		}
	}
	for _, st := range []string{"locked", "frozen", "pending", "defective"} {
		if !seenState[st] {
			t.Fatalf("状态行未覆盖库存状态 %s（要求四态齐备）", st)
		}
	}

	// 流水分段：'期初' 三条（§8+§10+§12.1 各 1），补影两条（business_type='演示'，
	// 含 '期初' 之外的段——净零对，不改现存量锚点）。
	var ledOpening, shadows []seedInsert
	for _, s := range insertsOf(ins, "inventory_ledgers") {
		if strings.Contains(s.text, "'期初'") {
			ledOpening = append(ledOpening, s)
		} else {
			shadows = append(shadows, s)
		}
	}
	if len(ledOpening) != 3 {
		t.Fatalf("期初流水 INSERT 应为 3 条（§8+§10+§12.1），实际 %d", len(ledOpening))
	}
	if len(shadows) != 2 {
		t.Fatalf("补影流水 INSERT 应为 2 条（基础段+电子厂段），实际 %d", len(shadows))
	}
	// 补影段逐条净零对：Σ qty_change = 0（不改任何现存量锚点）；
	// qty_change 列按 AS v(...) 别名定位（§8 与 §10 段元组宽度不同：§10 增加 batch_no）
	for _, sh := range shadows {
		idx := aliasIndex(t, sh.text, "补影流水")
		qc, ok := idx["q_change"]
		if !ok {
			t.Fatal("补影流水 VALUES 源别名清单缺少 q_change")
		}
		shadowNet := 0.0
		for _, tup := range sh.tuples {
			shadowNet += parseQty(t, tup[qc], "补影流水")
		}
		if shadowNet != 0 {
			t.Fatalf("补影流水净变化应恒为 0（净零对），实际 %v", shadowNet)
		}
	}

	ledTuples := 0
	for _, led := range ledOpening {
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
		ledTuples += len(led.tuples)
	}

	// 成对：全部库存行（期初+状态）与全部 '期初' 流水按 (仓库, 库位, SKU, 批次) 键
	// 集合与数量完全一致（各段 VALUES 源别名经 aliasIndex 逐条定位，不依赖物理位置）。
	pairKey := func(tup []string, idx map[string]int, ctx string) [4]string {
		for _, c := range []string{"wh_code", "bin_code", "sku_code", "batch_no"} {
			if _, ok := idx[c]; !ok {
				t.Fatalf("%s VALUES 源别名清单缺少 %s", ctx, c)
			}
		}
		return [4]string{normLit(tup[idx["wh_code"]]), normLit(tup[idx["bin_code"]]),
			normLit(tup[idx["sku_code"]]), normLit(tup[idx["batch_no"]])}
	}
	invQty := map[[4]string]float64{}
	invRows := 0
	for _, seg := range []seedInsert{inv, status} {
		idx := aliasIndex(t, seg.text, "库存")
		for _, tup := range seg.tuples {
			key := pairKey(tup, idx, "库存")
			q := parseQty(t, tup[idx["qty"]], "库存")
			if q <= 0 {
				t.Fatalf("库存数量应 >0: %v", tup)
			}
			if _, dup := invQty[key]; dup {
				t.Fatalf("库存键重复（五维唯一会被 ON CONFLICT 跳过导致流水失衡）: %v", key)
			}
			invQty[key] = q
			invRows++
		}
	}
	ledQty := map[[4]string]float64{}
	for _, led := range ledOpening {
		idx := aliasIndex(t, led.text, "期初流水")
		for _, tup := range led.tuples {
			ledQty[pairKey(tup, idx, "期初流水")] = parseQty(t, tup[idx["qty"]], "期初流水")
		}
	}
	if len(invQty) != len(ledQty) {
		t.Fatalf("库存 %d 行与期初流水 %d 行数量不等（必须 1:1 成对）", len(invQty), len(ledQty))
	}
	for key, q := range invQty {
		if ledQty[key] != q {
			t.Fatalf("期初键 %v 库存数量 %v 与流水数量 %v 不一致", key, q, ledQty[key])
		}
	}
	// 流水行数与库存行数相等（元组层面）
	if ledTuples != invRows {
		t.Fatalf("库存元组 %d 与期初流水元组 %d 不相等", invRows, ledTuples)
	}
}

// ---- 序列号：一物一行数量与期初库存吻合，且只挂在序列号管理的 SKU 上 ----

func TestDevSeedSerialsMatchOpeningInventory(t *testing.T) {
	ins := parseDevSeedInserts(t, stripSQLComments(readRepoFile(t, devSeedPath)))
	skus := mergedInsertsOf(t, ins, "skus", 2)
	// 序列号管理开关按列名定位（两段列清单同构，位置稳定）
	skuSerialIdx := -1
	for i, c := range skus.columns {
		if c == "is_serial_managed" {
			skuSerialIdx = i
		}
	}
	if skuSerialIdx < 0 {
		t.Fatal("skus INSERT 缺少 is_serial_managed 列")
	}
	serialFlag := map[string]bool{}
	for _, tup := range skus.tuples {
		serialFlag[tup[1]] = normLit(tup[skuSerialIdx]) == "TRUE"
	}
	// 只取期初段（§8+§10，只写 total/available）；状态行 §12.1 六列全写且刻意避开
	// 序列号管理 SKU（E008-01/E013-01/E013-02/E014-01），一物一行口径不受其影响。
	var invOpening []seedInsert
	for _, s := range insertsOf(ins, "inventory") {
		if !hasCol(s.columns, "locked_qty") {
			invOpening = append(invOpening, s)
		}
	}
	inv := mergeInserts(t, invOpening, "期初库存")
	invIdx := aliasIndex(t, inv.text, "期初库存")
	invQty := map[[2]string]float64{}
	for _, tup := range inv.tuples {
		key := [2]string{normLit(tup[invIdx["wh_code"]]), normLit(tup[invIdx["sku_code"]])}
		invQty[key] += parseQty(t, tup[invIdx["qty"]], "期初库存")
	}
	// §9 段无 status 别名（SELECT 恒 'IN_STOCK'）；§10 段带 status（已发货行
	// status=OUTBOUND 留痕，不计一物一行口径——与期初库存吻合的只是 IN_STOCK 数）。
	// 两段 VALUES 源宽度不同（6/10 列），别名清单必须逐条解析，不能跨段合并。
	var snInserts []seedInsert
	for _, s := range ins {
		if s.table == "serial_numbers" {
			snInserts = append(snInserts, s)
		}
	}
	if len(snInserts) != 2 {
		t.Fatalf("serial_numbers INSERT 应为 2 条（基础段+电子厂段），实际 %d", len(snInserts))
	}
	snTexts := make([]string, 0, len(snInserts))
	for _, s := range snInserts {
		snTexts = append(snTexts, s.text)
	}
	if !strings.Contains(strings.Join(snTexts, "\n"), "'IN_STOCK'") {
		t.Fatal("演示序列号应为 IN_STOCK 状态")
	}
	snCount := map[[2]string]int{}
	for _, s := range snInserts {
		idx := aliasIndex(t, s.text, "序列号")
		for _, c := range []string{"serial_no", "wh_code", "sku_code"} {
			if _, ok := idx[c]; !ok {
				t.Fatalf("序列号 VALUES 源别名清单缺少 %s", c)
			}
		}
		for _, tup := range s.tuples {
			status := "IN_STOCK"
			if j, ok := idx["status"]; ok {
				status = normLit(tup[j])
			}
			if status != "IN_STOCK" {
				continue
			}
			key := [2]string{normLit(tup[idx["wh_code"]]), normLit(tup[idx["sku_code"]])}
			if !serialFlag[tup[idx["sku_code"]]] {
				t.Fatalf("序列号 %s 挂在未启用序列号管理的 SKU %s 上（inventory-rules §8）", tup[idx["serial_no"]], tup[idx["sku_code"]])
			}
			snCount[key]++
		}
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
	users := mergedInsertsOf(t, ins, "users", 1)
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
		// '' 仅允许作为独立空串字面量（如 remark 列占位 ''——§11 单据链大量使用）：
		// 前后均为分隔符时静态解析器按引号两两配对仍可靠；字面量内部的转义
		//（如 'it''s'）会使引号感知解析歧义，仍然禁止。
		for i := 0; i+1 < len(content); i++ {
			if content[i] != '\'' || content[i+1] != '\'' {
				continue
			}
			prevOK := i == 0 || strings.ContainsRune(" (,\t\n\r=", rune(content[i-1]))
			nextOK := i+2 == len(content) || strings.ContainsRune(" ),;\t\n\r", rune(content[i+2]))
			if prevOK && nextOK {
				continue
			}
			t.Fatalf("%s 位置 %d 存在字面量内部转义引号（'' 仅允许独立空串字面量，保障静态校验可靠）", path, i)
		}
	}
}
