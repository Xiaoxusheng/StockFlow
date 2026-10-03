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

// database.md §5.1 软删除清单（库位=bin）。
var softDeleteTables = map[string]bool{
	"users": true, "products": true, "skus": true, "suppliers": true,
	"customers": true, "warehouses": true, "bins": true,
}

// join 表：仅 created_at/created_by。
var joinTables = map[string]bool{
	"user_roles": true, "role_permissions": true, "user_warehouses": true,
}

// append-only 表（database.md §7 审计数据 + inventory-rules §5 流水）：
// 仅 created_at，无 updated_at/updated_by/deleted_at。
var appendOnlyTables = map[string]bool{
	"operation_logs": true, "login_logs": true, "inventory_ledgers": true,
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
	for _, audit := range []string{"inventory_ledgers", "operation_logs", "login_logs"} {
		if !strings.Contains(grants, audit) {
			t.Fatalf("app_grants.sql 未覆盖审计表 %s（database.md §7.2）", audit)
		}
	}
	if !strings.Contains(grants, "REVOKE UPDATE, DELETE, TRUNCATE") ||
		!strings.Contains(grants, "GRANT SELECT, INSERT") {
		t.Fatal("app_grants.sql 缺少审计表“仅 SELECT+INSERT”的分层授权")
	}

	seed := readRepoFile(t, "../../db/seed/dev_seed.sql")
	if !strings.Contains(seed, `\if :is_dev`) || !strings.Contains(seed, `\endif`) {
		t.Fatal("dev_seed.sql 缺少 is_dev 环境门禁（database.md §8.2：生产不可能注入演示数据）")
	}
	if !strings.Contains(seed, "ON CONFLICT") {
		t.Fatal("dev_seed.sql 应为幂等（ON CONFLICT DO NOTHING）")
	}
}
