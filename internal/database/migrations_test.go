package database

// 迁移与种子 SQL 交付物的结构自查（不依赖 PostgreSQL/Redis/网络）。
//
// 背景：本开发环境没有 PostgreSQL，无法实际执行 migrate up/down（database.md §9 的双向验证
// 须在具备 PG 的环境补做）。本文件对 db/migrations、db/grants、db/seed 做静态结构校验，
// 作为最低限度的交付防线：成对性、升/降对称、表全集、通用字段与软删除范围、恒等式 CHECK、
// 关键唯一/部分唯一索引、命名规约、方言约束（timestamptz）。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const migrationsDir = "../../db/migrations"

var (
	migFileRe    = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)
	createRe     = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	dropRe       = regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	indexRe      = regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+([a-z_][a-z0-9_]*)`)
	constraintRe = regexp.MustCompile(`(?i)CONSTRAINT\s+([a-z_][a-z0-9_]*)\s+(?:CHECK|FOREIGN|UNIQUE|PRIMARY)`)
)

// tableDef 提取的表定义（body 已小写，便于子串断言）。
type tableDef struct {
	Name string
	Body string
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", rel, err)
	}
	return string(b)
}

// extractTables 括号配平地提取 CREATE TABLE 定义（DDL 字符串字面量不含 ASCII 圆括号，
// 全角括号不影响配平）。
func extractTables(t *testing.T, sqlText string) []tableDef {
	t.Helper()
	var defs []tableDef
	// createRe 以 \( 结尾：整段匹配的最后一个字符即 '('，其下标为 loc[1]-1
	for _, loc := range createRe.FindAllStringSubmatchIndex(sqlText, -1) {
		name := sqlText[loc[2]:loc[3]]
		open := loc[1] - 1
		depth := 0
		end := -1
		for i := open; i < len(sqlText); i++ {
			switch sqlText[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				end = i
				break
			}
		}
		if end < 0 {
			t.Fatalf("CREATE TABLE %s 括号不配平", name)
		}
		defs = append(defs, tableDef{Name: name, Body: strings.ToLower(sqlText[open+1 : end])})
	}
	return defs
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// loadMigrations 读取迁移目录并校验：命名规约、up/down 成对且名称对称、版本连续无空洞。
func loadMigrations(t *testing.T) map[int]map[string]string { // version -> kind -> 路径
	t.Helper()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("读取迁移目录失败: %v", err)
	}
	byVersion := map[int]map[string]string{}
	seen := map[string]bool{} // "version|desc" 去重
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			t.Fatalf("迁移文件命名不符合 NNNNNN_name.{up,down}.sql 规约: %s", e.Name())
		}
		version := 0
		for _, c := range m[1] {
			version = version*10 + int(c-'0')
		}
		key := m[1] + "|" + m[2]
		if seen[key] && byVersion[version][m[3]] != "" {
			t.Fatalf("迁移文件重复: %s", e.Name())
		}
		seen[key] = true
		if byVersion[version] == nil {
			byVersion[version] = map[string]string{}
		}
		byVersion[version][m[3]] = migrationsDir + "/" + e.Name()
		if len(byVersion[version]) == 2 && byVersion[version]["up"] == byVersion[version]["down"] {
			t.Fatalf("up/down 文件名冲突: %s", e.Name())
		}
	}
	if len(byVersion) == 0 {
		t.Fatal("迁移目录为空")
	}
	for v, pair := range byVersion {
		if pair["up"] == "" || pair["down"] == "" {
			t.Fatalf("版本 %06d 缺少 up/down 成对文件: %v", v, pair)
		}
	}
	// 版本连续（1..N 无空洞、无重复）
	max := 0
	for v := range byVersion {
		if v > max {
			max = v
		}
	}
	for v := 1; v <= max; v++ {
		if byVersion[v] == nil {
			t.Fatalf("迁移版本空洞：缺少 %06d（golang-migrate 按序执行，空洞会导致编号错乱）", v)
		}
	}
	return byVersion
}

// M1 五组迁移与其表集合（backend-m1-plan §6.1 冻结契约，26 表）。
var m1Files = map[string][]string{
	"000001_create_auth_tables": {
		"users", "roles", "permissions", "departments",
		"user_roles", "role_permissions", "user_warehouses",
	},
	"000002_create_audit_log_tables": {"operation_logs", "login_logs"},
	"000003_create_masterdata_tables": {
		"product_categories", "units", "products", "skus",
		"barcodes", "suppliers", "customers",
	},
	"000004_create_warehouse_tables": {"warehouses", "zones", "shelves", "bins"},
	"000005_create_inventory_tables": {
		"batches", "serial_numbers", "inventory", "inventory_locks",
		"inventory_ledgers", "inventory_adjustments",
	},
}

// M2 五组迁移与其表集合（backend-m2-plan §5 冻结清单，29 表）。
var m2Files = map[string][]string{
	"000006_create_doc_shared_tables": {"doc_number_counters", "document_approvals"},
	"000007_create_purchase_tables": {
		"purchase_orders", "purchase_order_items",
		"inbound_orders", "inbound_items",
		"receipts", "receipt_items",
		"putaway_tasks", "quality_orders", "quality_items",
	},
	"000008_create_sales_tables": {
		"sales_orders", "sales_order_items",
		"outbound_orders", "outbound_items",
		"allocation_records", "pick_tasks", "check_tasks",
		"packing_records", "packing_items", "shipments",
	},
	"000009_create_stockops_tables": {
		"transfer_orders", "transfer_items",
		"count_orders", "count_items", "count_differences",
	},
	"000010_create_returns_tables": {"return_orders", "return_items", "exceptions"},
}

// M3 四组迁移与其表集合（backend-m3-plan §5 冻结清单，17 表）。
var m3Files = map[string][]string{
	"000011_create_datax_tables": {
		"import_tasks", "import_task_rows", "export_tasks", "files",
	},
	"000012_create_printing_tables": {
		"print_templates", "print_tasks", "print_task_rows",
	},
	"000013_create_device_tables": {
		"devices", "device_configs", "scan_logs", "device_logs", "app_versions",
	},
	"000014_create_sysops_tables": {
		"scheduled_jobs", "scheduled_job_runs", "system_configs", "notifications", "backup_records",
	},
}

// database.md §5.1 软删除清单（库位=bin）。
// M3 追加 files（backend-m3-plan §5 000011：deleted_at NULL——plan §6.4 删除=软删 + 审计）。
var softDeleteTables = map[string]bool{
	"users": true, "products": true, "skus": true, "suppliers": true,
	"customers": true, "warehouses": true, "bins": true, "files": true,
}

// join 表：仅 created_at/created_by。
var joinTables = map[string]bool{
	"user_roles": true, "role_permissions": true, "user_warehouses": true,
}

// append-only 表（database.md §7 审计数据 + inventory-rules §5 流水）：
// 仅 created_at，无 updated_at/updated_by/deleted_at。
// document_approvals 审批记录随 M2 000006 加入（business-flow §12.2 不可修改删除，plan §5）。
// M3 000013 追加 scan_logs/device_logs（backend-m3-plan §5：append-only 审计表，grants 仅 SELECT+INSERT）。
var appendOnlyTables = map[string]bool{
	"operation_logs": true, "login_logs": true, "inventory_ledgers": true,
	"document_approvals": true, "scan_logs": true, "device_logs": true,
}

// 自然主键表（M3 000014 system_configs：key varchar 主键，键即业务标识
// 如 inventory.alert.expiry_days——backend-m3-plan §5 000014 冻结；
// 无 bigserial 代理键，其余通用字段与软删除范围同业务表口径）。
var naturalPkTables = map[string]bool{
	"system_configs": true,
}

// 计数表（docnum 引擎，backend-m2-plan §4.1）：复合主键 prefix+period，无 bigserial 代理键；
// 通用字段仅 created_at/updated_at（updated_at 记录最近发放时间），无 deleted_at。
var counterTables = map[string]bool{
	"doc_number_counters": true,
}

func TestMigrationFilesPairedAndSequential(t *testing.T) {
	loadMigrations(t)
}

func TestM1MigrationNamesAndTableSets(t *testing.T) {
	byVersion := loadMigrations(t)

	created := map[string]string{} // 表名 → 所在迁移
	for desc, wantTables := range m1Files {
		// 从版本号定位文件：描述名与版本号的映射按 loadMigrations 解析
		var path string
		for v, pair := range byVersion {
			for _, kind := range []string{"up"} {
				p := pair[kind]
				if strings.HasSuffix(filepath.Base(p), desc+".up.sql") {
					path = p
					_ = v
				}
			}
		}
		if path == "" {
			t.Fatalf("缺少 M1 迁移文件 %s.up.sql（backend-m1-plan §6.1 冻结契约）", desc)
		}
		up := readRepoFile(t, path)
		defs := extractTables(t, up)
		if len(defs) == 0 {
			t.Fatalf("%s 未定义任何表", path)
		}
		got := map[string]bool{}
		for _, d := range defs {
			got[d.Name] = true
			if prev, ok := created[d.Name]; ok {
				t.Fatalf("表 %s 在 %s 与 %s 重复定义", d.Name, prev, path)
			}
			created[d.Name] = path
		}
		for _, want := range wantTables {
			if !got[want] {
				t.Fatalf("%s 缺少契约表 %s", path, want)
			}
		}
		if len(got) != len(wantTables) {
			t.Fatalf("%s 表数量 %d 与契约 %d 不符（多定义: %v）", path, len(got), len(wantTables), got)
		}
	}
	if len(created) != 26 {
		t.Fatalf("M1 应为 26 表（backend-m1-plan §6.2），实际 %d", len(created))
	}
}

// contractUpPath 按文件描述名定位 up 文件路径（backend-m1-plan/backend-m2-plan 冻结文件名）。
func contractUpPath(t *testing.T, byVersion map[int]map[string]string, desc string) string {
	t.Helper()
	for _, pair := range byVersion {
		if strings.HasSuffix(filepath.Base(pair["up"]), desc+".up.sql") {
			return pair["up"]
		}
	}
	return ""
}

func TestM2MigrationNamesAndTableSets(t *testing.T) {
	byVersion := loadMigrations(t)

	// 全库表名 → 首次定义所在迁移（跨 M1/M2 重复定义即违约：同一表只允许一处 DDL 契约）。
	created := map[string]string{}
	for desc, wantTables := range m1Files {
		for _, want := range wantTables {
			created[want] = desc
		}
	}

	for desc, wantTables := range m2Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M2 迁移文件 %s.up.sql（backend-m2-plan §5 冻结清单）", desc)
		}
		up := readRepoFile(t, path)
		defs := extractTables(t, up)
		if len(defs) == 0 {
			t.Fatalf("%s 未定义任何表", path)
		}
		got := map[string]bool{}
		for _, d := range defs {
			got[d.Name] = true
			if prev, ok := created[d.Name]; ok {
				t.Fatalf("表 %s 在 %s 与 %s 重复定义", d.Name, prev, path)
			}
			created[d.Name] = desc
		}
		for _, want := range wantTables {
			if !got[want] {
				t.Fatalf("%s 缺少契约表 %s", path, want)
			}
		}
		if len(got) != len(wantTables) {
			t.Fatalf("%s 表数量 %d 与冻结清单 %d 不符（多定义: %v）", path, len(got), len(wantTables), got)
		}
	}
	// 26（M1）+ 29（M2）= 55：全库业务表全集封闭（database.md §2"至少覆盖"清单的落地基线）。
	if len(created) != 55 {
		t.Fatalf("M1+M2 应为 55 表（backend-m1-plan §6.2 + backend-m2-plan §5），实际 %d", len(created))
	}
}

// TestM2NoCrossDomainForeignKeys M2 全部单据表不建任何外键：跨域引用一律逻辑单号
// 或裸 ID + Service 层校验（backend-m2-plan §5 通用规则；域内明细→主单亦走同事务
// 裸 ID 引用，与 000005 哨兵值约定一致）。
func TestM2NoCrossDomainForeignKeys(t *testing.T) {
	byVersion := loadMigrations(t)
	for desc := range m2Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M2 迁移文件 %s.up.sql", desc)
		}
		if strings.Contains(strings.ToUpper(readRepoFile(t, path)), "REFERENCES") {
			t.Fatalf("%s 不应包含任何 REFERENCES（跨域不建 FK，backend-m2-plan §5）", path)
		}
	}
}

// TestM2StatusValueDomains M2 状态机/值域 CHECK 与冻结契约同源（backend-m2-plan §5/§6；
// 迁移是状态机第二道防线——与 TestStatusValueDomains 的 M1 契约同构）。
func TestM2StatusValueDomains(t *testing.T) {
	byVersion := loadMigrations(t)
	m2Up := ""
	for desc := range m2Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M2 迁移文件 %s.up.sql", desc)
		}
		m2Up += readRepoFile(t, path)
	}
	cases := map[string][]string{
		// 000006
		"chk_document_approvals_action": {"'SUBMIT'", "'APPROVE'", "'REJECT'", "'CANCEL'"},
		// 000007（plan §6.1–§6.3 状态机 + business-flow §4 值域）
		"chk_purchase_orders_status": {
			"'DRAFT'", "'PENDING_APPROVAL'", "'APPROVED'", "'PARTIAL_RECEIVED'",
			"'RECEIVED_ALL'", "'COMPLETED'", "'CANCELLED'",
		},
		"chk_inbound_orders_source_type": {"'PURCHASE'", "'OTHER'"},
		"chk_inbound_orders_status": {
			"'DRAFT'", "'RECEIVING'", "'AWAITING_QC'", "'AWAITING_PUTAWAY'",
			"'COMPLETED'", "'CANCELLED'", "'CLOSED'",
		},
		"chk_putaway_tasks_from_state":       {"'available'", "'pending_inspect'"},
		"chk_putaway_tasks_status":           {"'PENDING'", "'IN_PROGRESS'", "'COMPLETED'", "'CANCELLED'"},
		"chk_quality_orders_source_type":     {"'INBOUND'", "'RETURN'"},
		"chk_quality_orders_inspection_type": {"'免检'", "'抽检'", "'全检'"},
		"chk_quality_orders_result": {
			"'合格'", "'部分合格'", "'不合格'", "'退供应商'", "'报废'",
			"'返工'", "'降级'", "'转不良品仓'", "'特批放行'",
		},
		"chk_quality_orders_status": {"'PENDING'", "'INSPECTING'", "'COMPLETED'"},
		// 000008（plan §6.4–§6.5 状态机 + business-flow §7.1/§8.3/§8.5 值域）
		"chk_sales_orders_status": {
			"'DRAFT'", "'PENDING_APPROVAL'", "'APPROVED'", "'REJECTED'",
			"'PARTIAL_SHIPPED'", "'SHIPPED_ALL'", "'COMPLETED'", "'CANCELLED'",
		},
		"chk_outbound_orders_type": {"'销售出库'", "'生产领料'", "'调拨出库'", "'其他出库'", "'报损出库'"},
		"chk_outbound_orders_status": {
			"'PENDING_ALLOCATE'", "'ALLOCATED'", "'PICKING'", "'PICKED'", "'CHECKED'",
			"'PACKED'", "'PARTIAL_SHIPPED'", "'SHIPPED_ALL'", "'CANCELLED'", "'CLOSED'",
		},
		"chk_pick_tasks_status":           {"'PENDING'", "'CLAIMED'", "'PICKING'", "'PICKED'", "'EXCEPTION'", "'CANCELLED'"},
		"chk_check_tasks_status":          {"'PENDING'", "'DONE'", "'EXCEPTION'"},
		"chk_check_tasks_result":          {"''", "'错货'", "'少货'", "'多货'", "'批次错误'", "'序列号错误'"},
		"chk_allocation_records_strategy": {"'FIFO'", "'FEFO'", "'指定批次'", "'指定仓库'", "'指定库位'"},
		"chk_shipments_status":            {"'PENDING'", "'SHIPPED'", "'IN_TRANSIT'", "'SIGNED'", "'ABNORMAL'"},
		// 000009（plan §6.6–§6.7 状态机）
		"chk_transfer_orders_type": {"'WAREHOUSE'", "'BIN'"},
		"chk_transfer_orders_status": {
			"'DRAFT'", "'PENDING_APPROVAL'", "'APPROVED'", "'TRANSFERRING'",
			"'AWAITING_RECEIPT'", "'COMPLETED'", "'CANCELLED'",
		},
		"chk_count_orders_status":      {"'DRAFT'", "'COUNTING'", "'PENDING_REVIEW'", "'COMPLETED'", "'CANCELLED'"},
		"chk_count_differences_status": {"'PENDING'", "'APPROVED'", "'REJECTED'", "'EXECUTED'"},
		// 000010（plan §6.9–§6.10 状态机 + business-flow §11.2 九类）
		"chk_return_orders_type": {"'SALES'", "'PURCHASE'"},
		"chk_return_orders_status": {
			"'DRAFT'", "'PENDING_APPROVAL'", "'APPROVED'", "'RECEIVING'",
			"'IN_QC'", "'SHIPPED'", "'COMPLETED'", "'CANCELLED'",
		},
		"chk_exceptions_type": {
			"'收货异常'", "'质检异常'", "'上架异常'", "'库存异常'", "'拣货异常'",
			"'复核异常'", "'物流异常'", "'盘点异常'", "'系统异常'",
		},
		"chk_exceptions_status": {
			"'OPEN'", "'ASSIGNED'", "'PROCESSING'", "'PENDING_REVIEW'", "'RESOLVED'", "'CLOSED'",
		},
	}
	for constraint, values := range cases {
		if !strings.Contains(m2Up, constraint) {
			t.Fatalf("M2 缺少状态值域约束 %s", constraint)
		}
		for _, v := range values {
			if !strings.Contains(m2Up, v) {
				t.Fatalf("约束 %s 值域缺少 %s", constraint, v)
			}
		}
	}
}

// TestM2IdempotencyAndDocnumInfra 幂等键部分唯一索引（architecture §3.2/plan §5：
// HTTP 事件型表 receipts/packing_records/shipments）与 docnum 计数表基础设施
// （plan §4.1：复合主键 prefix+period，两语句发放落点）。
func TestM2IdempotencyAndDocnumInfra(t *testing.T) {
	byVersion := loadMigrations(t)
	m2Up := ""
	for desc := range m2Files {
		m2Up += readRepoFile(t, contractUpPath(t, byVersion, desc))
	}
	// 事件型表幂等键部分唯一索引（NULL 不参与，与 000005 流水幂等键同款口径）。
	for _, want := range []string{
		"CREATE UNIQUE INDEX uk_receipts_idempotency ON receipts (idempotency_key) WHERE idempotency_key IS NOT NULL",
		"CREATE UNIQUE INDEX uk_packing_records_idempotency ON packing_records (idempotency_key) WHERE idempotency_key IS NOT NULL",
		"CREATE UNIQUE INDEX uk_shipments_idempotency ON shipments (idempotency_key) WHERE idempotency_key IS NOT NULL",
	} {
		if !strings.Contains(normalize(m2Up), want) {
			t.Fatalf("M2 缺少幂等键部分唯一索引：%s", want)
		}
	}
	// 单号唯一（database.md §6 / plan §5：全部主单据 *_no UNIQUE 索引）。
	for _, idx := range []string{
		"uk_purchase_orders_no", "uk_inbound_orders_no", "uk_receipts_no", "uk_putaway_tasks_no",
		"uk_quality_orders_no", "uk_sales_orders_no", "uk_outbound_orders_no", "uk_pick_tasks_no",
		"uk_check_tasks_no", "uk_packing_records_no", "uk_shipments_no", "uk_transfer_orders_no",
		"uk_count_orders_no", "uk_return_orders_no", "uk_exceptions_no",
	} {
		if !strings.Contains(m2Up, "CREATE UNIQUE INDEX "+idx) {
			t.Fatalf("M2 缺少单号唯一索引 %s", idx)
		}
	}
	// doc_number_counters 复合主键（plan §4.1：发放走 UPDATE ... WHERE prefix=? AND period=?）。
	if !strings.Contains(normalize(m2Up), "PRIMARY KEY (prefix, period)") {
		t.Fatal("doc_number_counters 缺少复合主键 (prefix, period)（backend-m2-plan §4.1）")
	}
	// (warehouse_id, status) 列表索引（database.md §6.1 + plan §10.5 数据权限过滤免 join）。
	for _, idx := range []string{
		"idx_purchase_orders_warehouse_status", "idx_inbound_orders_warehouse_status",
		"idx_quality_orders_warehouse_status", "idx_sales_orders_warehouse_status",
		"idx_outbound_orders_warehouse_status", "idx_pick_tasks_warehouse_status",
		"idx_check_tasks_warehouse_status", "idx_shipments_warehouse_status",
		"idx_count_orders_warehouse_status",
	} {
		if !strings.Contains(m2Up, "CREATE INDEX "+idx) {
			t.Fatalf("M2 缺少 (warehouse_id, status) 列表索引 %s", idx)
		}
	}
}

// TestM3MigrationNamesAndTableSets M3 四组迁移表清单与 backend-m3-plan §5 冻结契约一致，
// 且与 M1/M2 表全集无重复定义（同一表只允许一处 DDL 契约）。
func TestM3MigrationNamesAndTableSets(t *testing.T) {
	byVersion := loadMigrations(t)

	created := map[string]string{} // 表名 → 首次定义所在迁移
	for desc, wantTables := range m1Files {
		for _, want := range wantTables {
			created[want] = desc
		}
	}
	for desc, wantTables := range m2Files {
		for _, want := range wantTables {
			created[want] = desc
		}
	}

	for desc, wantTables := range m3Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M3 迁移文件 %s.up.sql（backend-m3-plan §5 冻结清单）", desc)
		}
		up := readRepoFile(t, path)
		defs := extractTables(t, up)
		if len(defs) == 0 {
			t.Fatalf("%s 未定义任何表", path)
		}
		got := map[string]bool{}
		for _, d := range defs {
			got[d.Name] = true
			if prev, ok := created[d.Name]; ok {
				t.Fatalf("表 %s 在 %s 与 %s 重复定义", d.Name, prev, path)
			}
			created[d.Name] = desc
		}
		for _, want := range wantTables {
			if !got[want] {
				t.Fatalf("%s 缺少契约表 %s", path, want)
			}
		}
		if len(got) != len(wantTables) {
			t.Fatalf("%s 表数量 %d 与冻结清单 %d 不符（多定义: %v）", path, len(got), len(wantTables), got)
		}
	}
	// 26（M1）+ 29（M2）+ 17（M3）= 72：全库业务表全集封闭。
	if len(created) != 72 {
		t.Fatalf("M1+M2+M3 应为 72 表（backend-m1-plan §6.2 + backend-m2-plan §5 + backend-m3-plan §5），实际 %d", len(created))
	}
}

// TestM3NoCrossDomainForeignKeys M3 全部平台表不建任何外键：devices.warehouse_id、
// files.business_no 等一律裸 ID/单号 + Service 层校验（backend-m3-plan §5 通用规则）。
func TestM3NoCrossDomainForeignKeys(t *testing.T) {
	byVersion := loadMigrations(t)
	for desc := range m3Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M3 迁移文件 %s.up.sql", desc)
		}
		if strings.Contains(strings.ToUpper(readRepoFile(t, path)), "REFERENCES") {
			t.Fatalf("%s 不应包含任何 REFERENCES（跨域不建 FK，backend-m3-plan §5）", path)
		}
	}
}

// TestM3StatusValueDomains M3 状态机/值域 CHECK 与 backend-m3-plan §5 冻结契约同源
// （值域大小写逐字对齐方案：scheduled_jobs/system_configs 为小写值域，其余大写）。
func TestM3StatusValueDomains(t *testing.T) {
	byVersion := loadMigrations(t)
	m3Up := ""
	for desc := range m3Files {
		path := contractUpPath(t, byVersion, desc)
		if path == "" {
			t.Fatalf("缺少 M3 迁移文件 %s.up.sql", desc)
		}
		m3Up += readRepoFile(t, path)
	}
	cases := map[string][]string{
		// 000011（plan §5/§6.1–§6.3：导入九类、向导六态、行六态、导出 16 模块、范围五值、任务五态）
		"chk_import_tasks_type": {
			"'PRODUCT'", "'SKU'", "'SUPPLIER'", "'CUSTOMER'", "'WAREHOUSE'", "'LOCATION'",
			"'PURCHASE_ORDER'", "'SALES_ORDER'", "'INITIAL_INVENTORY'",
		},
		"chk_import_tasks_status": {
			"'PARSED'", "'VALIDATED'", "'EXECUTING'", "'SUCCESS'", "'PARTIAL_SUCCESS'", "'FAILED'",
		},
		"chk_import_task_rows_status": {"'RAW'", "'VALID'", "'INVALID'", "'QUEUED'", "'SUCCESS'", "'FAILED'"},
		"chk_export_tasks_module": {
			"'PRODUCT'", "'SKU'", "'SUPPLIER'", "'CUSTOMER'", "'WAREHOUSE'", "'LOCATION'",
			"'PURCHASE_ORDER'", "'PURCHASE_INBOUND'", "'QUALITY'", "'SALES_OUTBOUND'",
			"'INVENTORY'", "'INVENTORY_LEDGER'", "'TRANSFER'", "'COUNT'", "'EXCEPTION'", "'REPORT'",
		},
		"chk_export_tasks_scope":  {"'CURRENT_PAGE'", "'SELECTED'", "'ALL'", "'BY_FILTER'", "'TIME_RANGE'"},
		"chk_export_tasks_status": {"'QUEUED'", "'PROCESSING'", "'SUCCESS'", "'PARTIAL_SUCCESS'", "'FAILED'"},
		// 000012（plan §5/§7：打印 9 对象、5 纸张、5 码制、任务四态 + 执行确认结果）
		"chk_print_templates_object_type": {
			"'SKU_LABEL'", "'BIN_LABEL'", "'CARTON_CODE'", "'PALLET_CODE'", "'INBOUND_ORDER'",
			"'OUTBOUND_ORDER'", "'PICK_ORDER'", "'COUNT_ORDER'", "'SHIPMENT_ORDER'",
		},
		"chk_print_templates_paper":             {"'A4'", "'A5'", "'THERMAL_40_30'", "'THERMAL_60_40'", "'THERMAL_100_50'"},
		"chk_print_templates_barcode_symbology": {"'CODE128'", "'CODE39'", "'EAN13'", "'EAN8'", "'UPC'"},
		"chk_print_templates_status":            {"'ENABLED'", "'DISABLED'"},
		"chk_print_tasks_status":                {"'QUEUED'", "'PROCESSING'", "'SUCCESS'", "'FAILED'"},
		"chk_print_tasks_result":                {"'SUCCESS'", "'FAILED'"},
		// 000013（plan §5/§8：设备类型/状态/激活为小写/大写混合冻结值域）
		"chk_devices_type":              {"'pc'", "'pad'", "'pda'", "'scanner'", "'printer'"},
		"chk_devices_status":            {"'ENABLED'", "'DISABLED'"},
		"chk_devices_activation_status": {"'PENDING'", "'ACTIVATED'"},
		"chk_device_logs_level":         {"'INFO'", "'WARN'", "'ERROR'"},
		"chk_app_versions_platform":     {"'android'"},
		"chk_app_versions_status":       {"'DRAFT'", "'PUBLISHED'", "'DEPRECATED'"},
		// 000014（plan §5/§4.3/§10：last_run_status 小写、触发方式三值、配置类型四值、通知六类、备份混合模式状态机）
		"chk_scheduled_jobs_last_run_status": {"'success'", "'failed'", "'running'", "'never'"},
		"chk_scheduled_job_runs_trigger":     {"'SCHEDULED'", "'MANUAL'", "'SKIPPED'"},
		"chk_system_configs_type":            {"'text'", "'number'", "'boolean'", "'enum'"},
		"chk_notifications_type": {
			"'SYSTEM'", "'APPROVAL'", "'STOCK_ALERT'", "'EXPIRY_ALERT'", "'EXCEPTION'", "'TASK'",
		},
		"chk_backup_records_trigger": {"'AUTO'", "'MANUAL'"},
		"chk_backup_records_status":  {"'REQUESTED'", "'RUNNING'", "'SUCCESS'", "'FAILED'"},
	}
	for constraint, values := range cases {
		if !strings.Contains(m3Up, constraint) {
			t.Fatalf("M3 缺少状态值域约束 %s", constraint)
		}
		for _, v := range values {
			if !strings.Contains(m3Up, v) {
				t.Fatalf("约束 %s 值域缺少 %s", constraint, v)
			}
		}
	}
}

// TestM3IdempotencyAndPlatformInfra M3 幂等键部分唯一索引（plan §5 000014：notifications
// dedup_key 预警幂等、backup_records 在途唯一——并发手动触发 409）、单号唯一（IMP/EXP/PT）、
// 复合唯一与设备/配置基础设施索引。
func TestM3IdempotencyAndPlatformInfra(t *testing.T) {
	byVersion := loadMigrations(t)
	m3Up := ""
	for desc := range m3Files {
		m3Up += readRepoFile(t, contractUpPath(t, byVersion, desc))
	}
	// 部分唯一索引（NULL/状态过滤不参与约束，与 M1 流水幂等键同款口径）。
	for _, want := range []string{
		"CREATE UNIQUE INDEX uk_notifications_dedup ON notifications (dedup_key) WHERE dedup_key IS NOT NULL",
		`CREATE UNIQUE INDEX uk_backup_records_inflight ON backup_records ("trigger") WHERE status IN ('REQUESTED', 'RUNNING')`,
		"CREATE INDEX idx_files_expires_at ON files (expires_at) WHERE expires_at IS NOT NULL",
	} {
		if !strings.Contains(normalize(m3Up), want) {
			t.Fatalf("M3 缺少部分索引：%s", want)
		}
	}
	// 任务单号唯一（IMP/EXP/PT 经 internal/docnum 发放，plan §12.3）。
	for _, idx := range []string{
		"uk_import_tasks_no", "uk_export_tasks_no", "uk_print_tasks_no",
	} {
		if !strings.Contains(m3Up, "CREATE UNIQUE INDEX "+idx) {
			t.Fatalf("M3 缺少单号唯一索引 %s", idx)
		}
	}
	// 复合/业务唯一索引。
	for _, idx := range []string{
		"uk_import_task_rows_task_row", "uk_print_task_rows_task_seq",
		"uk_devices_code", "uk_device_configs_device", "uk_app_versions_platform_code",
		"uk_scheduled_jobs_code",
	} {
		if !strings.Contains(m3Up, "CREATE UNIQUE INDEX "+idx) {
			t.Fatalf("M3 缺少唯一索引 %s", idx)
		}
	}
}

func TestUpDownSymmetry(t *testing.T) {
	byVersion := loadMigrations(t)
	for v, pair := range byVersion {
		up := readRepoFile(t, pair["up"])
		down := readRepoFile(t, pair["down"])

		if strings.Contains(strings.ToUpper(up), "DROP TABLE") {
			t.Fatalf("%06d up 不应包含 DROP TABLE", v)
		}
		if strings.Contains(strings.ToUpper(down), "CREATE TABLE") {
			t.Fatalf("%06d down 不应包含 CREATE TABLE", v)
		}

		ups := extractTables(t, up)
		// down 的 DROP 顺序必须与 up 的建表顺序严格相反（域内 FK 依赖）
		var downs []string
		for _, loc := range dropRe.FindAllStringSubmatchIndex(down, -1) {
			downs = append(downs, strings.ToLower(down[loc[2]:loc[3]]))
		}
		if len(downs) != len(ups) {
			t.Fatalf("%06d down 表数量 %d 与 up %d 不一致", v, len(downs), len(ups))
		}
		for i := range ups {
			upName := ups[len(ups)-1-i].Name
			if downs[i] != upName {
				t.Fatalf("%06d down 第 %d 个 DROP 为 %s，应为（与 up 相反序）%s", v, i, downs[i], upName)
			}
		}
		// 括号配平（廉价语法防线）
		for _, f := range []string{pair["up"], pair["down"]} {
			content := readRepoFile(t, f)
			depth := 0
			for _, r := range content {
				switch r {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth < 0 {
					t.Fatalf("%s 括号不配平（多余的右括号）", f)
				}
			}
			if depth != 0 {
				t.Fatalf("%s 括号不配平（缺少右括号）", f)
			}
		}
	}
}

func TestCommonColumnsAndSoftDeleteScope(t *testing.T) {
	byVersion := loadMigrations(t)
	for _, pair := range byVersion {
		for _, d := range extractTables(t, readRepoFile(t, pair["up"])) {
			body := d.Body
			switch {
			case appendOnlyTables[d.Name]:
				for _, forbidden := range []string{"updated_at", "updated_by", "deleted_at"} {
					if strings.Contains(body, forbidden) {
						t.Fatalf("append-only 表 %s 不得包含 %s（database.md §7 / inventory-rules §5）", d.Name, forbidden)
					}
				}
				if !strings.Contains(body, "created_at") {
					t.Fatalf("append-only 表 %s 缺少 created_at", d.Name)
				}
			case counterTables[d.Name]:
				// 计数表（plan §4.1）：复合主键无 bigserial；created_at/updated_at 记录
				// 建行与最近发放，无 deleted_at（号码不回收，无生命周期删除语义）。
				for _, need := range []string{"created_at", "updated_at", "next_no"} {
					if !strings.Contains(body, need) {
						t.Fatalf("计数表 %s 缺少 %s（backend-m2-plan §4.1）", d.Name, need)
					}
				}
				for _, forbidden := range []string{"deleted_at", "bigserial"} {
					if strings.Contains(body, forbidden) {
						t.Fatalf("计数表 %s 不应包含 %s", d.Name, forbidden)
					}
				}
			case joinTables[d.Name]:
				for _, need := range []string{"created_at", "created_by"} {
					if !strings.Contains(body, need) {
						t.Fatalf("join 表 %s 缺少 %s", d.Name, need)
					}
				}
				for _, forbidden := range []string{"updated_at", "updated_by", "deleted_at"} {
					if strings.Contains(body, forbidden) {
						t.Fatalf("join 表 %s 不应包含 %s", d.Name, forbidden)
					}
				}
			case naturalPkTables[d.Name]:
				// 自然主键表（system_configs，backend-m3-plan §5 000014）：key 即业务主键，
				// 无 bigserial 代理键；通用字段与业务表同口径，不做软删除。
				if !strings.Contains(body, "primary key") {
					t.Fatalf("自然主键表 %s 缺少 primary key 声明", d.Name)
				}
				for _, need := range []string{"created_at", "updated_at", "created_by", "updated_by"} {
					if !strings.Contains(body, need) {
						t.Fatalf("自然主键表 %s 缺少通用字段 %s", d.Name, need)
					}
				}
				for _, forbidden := range []string{"bigserial", "deleted_at"} {
					if strings.Contains(body, forbidden) {
						t.Fatalf("自然主键表 %s 不应包含 %s", d.Name, forbidden)
					}
				}
			default:
				if !strings.Contains(body, "bigserial") {
					t.Fatalf("业务表 %s 缺少 bigserial 主键", d.Name)
				}
				for _, need := range []string{"created_at", "updated_at", "created_by", "updated_by"} {
					if !strings.Contains(body, need) {
						t.Fatalf("业务表 %s 缺少通用字段 %s（database.md §3）", d.Name, need)
					}
				}
				hasDeleted := strings.Contains(body, "deleted_at")
				if softDeleteTables[d.Name] && !hasDeleted {
					t.Fatalf("软删除表 %s 缺少 deleted_at（database.md §5.1）", d.Name)
				}
				if !softDeleteTables[d.Name] && hasDeleted {
					t.Fatalf("表 %s 不在软删除清单却包含 deleted_at（database.md §5.1）", d.Name)
				}
			}
		}
	}
}

// stripSQLComments 去掉 -- 行注释（语句级检查只看实际执行的 SQL）。
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestDialectAndNamingRules(t *testing.T) {
	byVersion := loadMigrations(t)
	for v, pair := range byVersion {
		for _, kind := range []string{"up", "down"} {
			content := stripSQLComments(readRepoFile(t, pair[kind]))
			upper := strings.ToUpper(content)
			if strings.Contains(upper, "BEGIN") || strings.Contains(upper, "COMMIT") {
				t.Fatalf("%06d %s 不应包含显式事务语句（golang-migrate 每文件单事务执行）", v, kind)
			}
			// 时间列一律 timestamptz：剔除 timestamptz 后不应再出现裸 timestamp（RE2 无负向前瞻）
			if strings.Contains(strings.ReplaceAll(strings.ToLower(content), "timestamptz", ""), "timestamp") {
				t.Fatalf("%06d %s 使用了无时区 timestamp，时间列一律 timestamptz（plan §6.2）", v, kind)
			}
			for _, m := range indexRe.FindAllStringSubmatch(content, -1) {
				if !regexp.MustCompile(`^(uk|idx)_[a-z0-9_]+$`).MatchString(m[1]) {
					t.Fatalf("%06d %s 索引命名不合规（须 uk_/idx_ 前缀）: %s", v, kind, m[1])
				}
			}
			for _, m := range constraintRe.FindAllStringSubmatch(content, -1) {
				if !regexp.MustCompile(`^(chk|fk)_[a-z0-9_]+$`).MatchString(m[1]) {
					t.Fatalf("%06d %s 约束命名不合规（须 chk_/fk_ 前缀）: %s", v, kind, m[1])
				}
			}
		}
	}
}

func TestInventoryIdentityAndUniqueConstraints(t *testing.T) {
	byVersion := loadMigrations(t)
	invUp := ""
	for v, pair := range byVersion {
		if strings.HasSuffix(filepath.Base(pair["up"]), "000005_create_inventory_tables.up.sql") {
			invUp = readRepoFile(t, pair["up"])
			_ = v
		}
	}
	if invUp == "" {
		t.Fatal("缺少 000005_create_inventory_tables.up.sql")
	}

	var invBody string
	for _, d := range extractTables(t, invUp) {
		if d.Name == "inventory" {
			invBody = d.Body
		}
	}
	if invBody == "" {
		t.Fatal("000005 未定义 inventory 表")
	}

	// 恒等式 + 非负 CHECK（inventory-rules §2、plan §8.1）：数据库层最后防线
	flat := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, invBody)
	identity := "total_qty=available_qty+locked_qty+frozen_qty+pending_inspect_qty+defective_qty"
	if !strings.Contains(flat, identity) {
		t.Fatalf("inventory 缺少恒等式 CHECK：%s", identity)
	}
	for _, col := range []string{"total_qty", "available_qty", "locked_qty", "frozen_qty", "pending_inspect_qty", "defective_qty"} {
		if !strings.Contains(flat, col+">=0") {
			t.Fatalf("inventory 恒等式 CHECK 缺少非负约束 %s>=0", col)
		}
	}
	if !strings.Contains(invUp, "chk_inventory_identity") {
		t.Fatal("inventory 恒等式约束应命名为 chk_inventory_identity")
	}

	// 五维唯一索引（inventory-rules §3：同仓同位同批同 SKU 恰一行）
	wantIdx := "CREATE UNIQUE INDEX uk_inventory_location ON inventory (warehouse_id, bin_id, sku_id, batch_id)"
	if !strings.Contains(normalize(invUp), wantIdx) {
		t.Fatalf("inventory 缺少五维唯一索引：%s", wantIdx)
	}

	// 幂等键部分唯一索引（plan §8.5）
	wantIdem := "CREATE UNIQUE INDEX uk_inventory_ledgers_idempotency_key ON inventory_ledgers (idempotency_key) WHERE idempotency_key IS NOT NULL"
	if !strings.Contains(normalize(invUp), wantIdem) {
		t.Fatalf("inventory_ledgers 缺少幂等键部分唯一索引：%s", wantIdem)
	}

	// 000005 无跨域外键（plan §6.4：一致性由 Service 层校验；batch_id=0 哨兵值也无法过 FK）
	if strings.Contains(strings.ToUpper(invUp), "REFERENCES") {
		t.Fatal("000005 不应包含任何 REFERENCES（跨域不建 FK，plan §6.4）")
	}

	// 流水 change_type 值域（plan §8.4 冻结枚举）
	for _, v := range []string{"'INBOUND'", "'OUTBOUND'", "'TRANSFER_OUT'", "'TRANSFER_IN'",
		"'LOCK'", "'RELEASE'", "'MOVE'", "'INSPECT_PASS'", "'INSPECT_DEFECTIVE'", "'ADJUST'"} {
		if !strings.Contains(invUp, v) {
			t.Fatalf("inventory_ledgers change_type 值域缺少 %s", v)
		}
	}
}

func TestStatusValueDomains(t *testing.T) {
	byVersion := loadMigrations(t)
	allUp := ""
	for _, pair := range byVersion {
		allUp += readRepoFile(t, pair["up"])
	}
	cases := map[string][]string{
		// 状态字段值域与冻结契约一致（plan §6.2；users 数据权限五范围 permission.md §4）
		"chk_users_status":          {"'ACTIVE'", "'DISABLED'"},
		"chk_users_data_scope":      {"'ALL'", "'SPECIFIED_WAREHOUSE'", "'DEPARTMENT'", "'SELF'", "'SELF_IN_CHARGE'"},
		"chk_permissions_type":      {"'MENU'", "'BUTTON'", "'API'"},
		"chk_serial_numbers_status": {"'IN_STOCK'", "'LOCKED'", "'OUTBOUND'", "'RETURNED'", "'FROZEN'"},
		"chk_inventory_locks_lock_type": {
			"'ORDER_HOLD'", "'COUNT_FREEZE'", "'QC_FREEZE'", "'MANUAL_FREEZE'", "'EXCEPTION_FREEZE'",
		},
		"chk_inventory_locks_status": {"'ACTIVE'", "'RELEASED'", "'CONSUMED'"},
		"chk_inventory_adjustments_status": {
			"'DRAFT'", "'PENDING_APPROVAL'", "'APPROVED'", "'REJECTED'", "'EXECUTED'", "'CANCELLED'",
		},
	}
	for constraint, values := range cases {
		if !strings.Contains(allUp, constraint) {
			t.Fatalf("缺少状态值域约束 %s", constraint)
		}
		for _, v := range values {
			if !strings.Contains(allUp, v) {
				t.Fatalf("约束 %s 值域缺少 %s", constraint, v)
			}
		}
	}
}

func TestGrantsAndSeedFilesPresent(t *testing.T) {
	grants := readRepoFile(t, "../../db/grants/app_grants.sql")
	// document_approvals 审批记录随 M2 000006 加入审计分层（backend-m2-plan §5：仅 INSERT）；
	// scan_logs/device_logs 随 M3 000013 加入（backend-m3-plan §5 grants 纪律：仅 SELECT+INSERT）。
	for _, audit := range []string{"inventory_ledgers", "document_approvals", "operation_logs", "login_logs", "scan_logs", "device_logs"} {
		if !strings.Contains(grants, audit) {
			t.Fatalf("app_grants.sql 未覆盖审计表 %s（database.md §7.2）", audit)
		}
	}
	if !strings.Contains(grants, "REVOKE UPDATE, DELETE, TRUNCATE") ||
		!strings.Contains(grants, "GRANT SELECT, INSERT") {
		t.Fatal("app_grants.sql 缺少审计表“仅 SELECT+INSERT”的分层授权")
	}
	// M3 清理维护角色段（backend-m3-plan §5 grants 纪律/§4.3/§15）：审计日志/扫码日志
	// 清理由部署侧维护脚本以独立维护角色执行，应用运行账号仍无 UPDATE/DELETE。
	if !strings.Contains(grants, "maint_user") || !strings.Contains(grants, "GRANT SELECT, DELETE") {
		t.Fatal("app_grants.sql 缺少清理维护角色段（backend-m3-plan §5：仅 operation_logs/login_logs/scan_logs/device_logs 的 SELECT+DELETE）")
	}

	seed := readRepoFile(t, "../../db/seed/dev_seed.sql")
	if !strings.Contains(seed, `\if :is_dev`) || !strings.Contains(seed, `\endif`) {
		t.Fatal("dev_seed.sql 缺少 is_dev 环境门禁（database.md §8.2：生产不可能注入演示数据）")
	}
	if !strings.Contains(seed, "ON CONFLICT") {
		t.Fatal("dev_seed.sql 应为幂等（ON CONFLICT DO NOTHING）")
	}
}
