package docnum

// 单据编号引擎单测——不依赖 PostgreSQL/Redis/网络（backend-m2-plan §11、AGENTS 硬性规则）。
// 覆盖纯函数面：规则值域校验、周期键、格式组装、冻结前缀注册表、唯一冲突判定与
// nil 事务防线；数据库发放路径（两语句 UPDATE RETURNING）由 testcontainers 集成测试覆盖
// （本环境无 PostgreSQL，见 internal/database/migrations_test.go 头注同款说明）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stockflow/server/internal/response"
)

// fixedTime 固定基准时间（business-flow §13.1 示例日期 2026-10-02）。
var fixedTime = time.Date(2026, 10, 2, 15, 4, 5, 0, time.UTC)

func TestRuleValidate(t *testing.T) {
	valid := []Rule{
		{Prefix: "PO", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay},
		{Prefix: "LED", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetAll},
		{Prefix: "A1", DateSeg: false, SeqWidth: 1, Reset: ResetDay},
		{Prefix: "OUT", SeqWidth: 0, Reset: ResetDay}, // SeqWidth 0 取默认
		{Prefix: "CK", SeqWidth: 9, Reset: ResetDay},  // 位宽上界
	}
	for _, r := range valid {
		if err := r.validate(); err != nil {
			t.Fatalf("规则 %+v 应合法，实际 %v", r, err)
		}
	}
	invalid := []Rule{
		{Prefix: "", Reset: ResetDay},                 // 空前缀
		{Prefix: "PURCHASEORDER", Reset: ResetDay},    // 超长（>8）
		{Prefix: "po", Reset: ResetDay},               // 小写
		{Prefix: "PO-1", Reset: ResetDay},             // 非法字符
		{Prefix: "PO", Reset: "MONTH"},                // 非法重置周期
		{Prefix: "PO", Reset: ""},                     // 重置周期缺省
		{Prefix: "PO", SeqWidth: -1, Reset: ResetDay}, // 负位宽
		{Prefix: "PO", SeqWidth: 10, Reset: ResetDay}, // 位宽上界外
	}
	for _, r := range invalid {
		err := r.validate()
		if err == nil {
			t.Fatalf("规则 %+v 应非法，实际通过", r)
		}
		var rerr *response.Error
		if !errors.As(err, &rerr) {
			t.Fatalf("非法规则应返回业务错误，实际 %v", err)
		}
		// 错误串格式 "CODE: message"（response.Error.Error()），跨包断言错误码用前缀匹配。
		if !strings.HasPrefix(rerr.Error(), "DOCNUM_RULE_INVALID: ") {
			t.Fatalf("非法规则错误码应为 DOCNUM_RULE_INVALID，实际 %s", rerr.Error())
		}
	}
}

func TestPeriodFor(t *testing.T) {
	if got := periodFor(Rule{Prefix: "PO", Reset: ResetDay}, fixedTime); got != "20261002" {
		t.Fatalf("ResetDay 周期键应为当日 YYYYMMDD，实际 %s", got)
	}
	if got := periodFor(Rule{Prefix: "LED", Reset: ResetAll}, fixedTime); got != "ALL" {
		t.Fatalf("ResetAll 周期键应为 ALL（永不重置），实际 %s", got)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		seq  int64
		want string
	}{
		{"§13.1 示例口径", Rule{Prefix: "PO", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetDay}, 1, "PO-20261002-000001"},
		{"多位流水补零", Rule{Prefix: "TR", DateSeg: true, SeqWidth: 6, Reset: ResetDay}, 42, "TR-20261002-000042"},
		{"超位宽原样扩展不截断", Rule{Prefix: "IN", DateSeg: true, SeqWidth: 6, Reset: ResetDay}, 1234567, "IN-20261002-1234567"},
		{"SeqWidth 0 取默认位宽", Rule{Prefix: "OUT", DateSeg: true, Reset: ResetDay}, 7, "OUT-20261002-000007"},
		{"ResetAll 仍带日期段（M1 承接口径）", Rule{Prefix: "LED", DateSeg: true, SeqWidth: DefaultSeqWidth, Reset: ResetAll}, 3, "LED-20261002-000003"},
		{"无日期段规则预留", Rule{Prefix: "ADJ", DateSeg: false, SeqWidth: 4, Reset: ResetAll}, 9, "ADJ-0009"},
	}
	for _, c := range cases {
		if got := format(c.rule, fixedTime, c.seq); got != c.want {
			t.Fatalf("%s: format = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFrozenPrefixRegistry(t *testing.T) {
	// M2 冻结 17 前缀（backend-m2-plan §4.1）+ M3 追加 3 前缀（backend-m3-plan §12.3）= 20。
	wantPrefixes := []string{
		"PO", "IN", "RC", "QC", "PW", "SO", "OUT", "PK", "CH",
		"BP", "SH", "TR", "CK", "RT", "EX", "LED", "ADJ",
		"IMP", "EXP", "PT",
	}
	if len(frozenRules) != len(wantPrefixes) {
		t.Fatalf("冻结前缀注册表应含 %d 项，实际 %d 项（清单外前缀禁止加入）", len(wantPrefixes), len(frozenRules))
	}
	for _, p := range wantPrefixes {
		rule, ok := RuleFor(p)
		if !ok {
			t.Fatalf("冻结前缀注册表缺少 %s（plan §4.1 / backend-m3-plan §12.3）", p)
		}
		if rule.Prefix != p {
			t.Fatalf("注册表键 %s 与规则前缀 %s 不一致", p, rule.Prefix)
		}
		if err := rule.validate(); err != nil {
			t.Fatalf("冻结规则 %s 应通过值域校验：%v", p, err)
		}
		if !rule.DateSeg {
			t.Fatalf("全部前缀均应携带日期段（plan §4.1/§12.3），%s 未携带", p)
		}
		wantReset := ResetDay
		if p == "LED" || p == "ADJ" {
			wantReset = ResetAll // M1 存量前缀承接：永不重置
		}
		if rule.Reset != wantReset {
			t.Fatalf("前缀 %s 重置周期应为 %s，实际 %s", p, wantReset, rule.Reset)
		}
	}
	// M3 前缀格式抽查（backend-m3-plan §12.3：ResetDay + 6 位流水）。
	if got := format(mustRule(t, "IMP"), fixedTime, 1); got != "IMP-20261002-000001" {
		t.Fatalf("IMP 单号格式不符: %s", got)
	}
	if got := format(mustRule(t, "EXP"), fixedTime, 42); got != "EXP-20261002-000042" {
		t.Fatalf("EXP 单号格式不符: %s", got)
	}
	if got := format(mustRule(t, "PT"), fixedTime, 7); got != "PT-20261002-000007" {
		t.Fatalf("PT 单号格式不符: %s", got)
	}
	// 有意避开 PRT（backend-m3-plan §12.3：api §7 prt: 为采购退货幂等键动作前缀）。
	if _, ok := RuleFor("PRT"); ok {
		t.Fatal("PRT 不应进注册表（与 prt: 幂等键动作前缀混淆）")
	}
	if _, ok := RuleFor("XX"); ok {
		t.Fatal("清单外前缀 XX 不应在注册表中（§2.3 判据 9 同源约束）")
	}
}

// mustRule 取注册表规则（测试辅助；清单外前缀直接失败）。
func mustRule(t *testing.T, prefix string) Rule {
	t.Helper()
	r, ok := RuleFor(prefix)
	if !ok {
		t.Fatalf("前缀 %s 不在冻结注册表", prefix)
	}
	return r
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("23505 应判定为唯一约束冲突")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "42P01"}) {
		t.Fatal("42P01 不应判定为唯一约束冲突")
	}
	if isUniqueViolation(fmt.Errorf("普通错误")) {
		t.Fatal("非 pgconn 错误不应判定为唯一约束冲突")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil 不应判定为唯一约束冲突")
	}
	// 包装错误也应被 errors.As 解出（重试路径经过 gorm 中间层）。
	wrapped := fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"})
	if !isUniqueViolation(wrapped) {
		t.Fatal("包装后的 23505 应判定为唯一约束冲突")
	}
}

func TestNextRequiresTx(t *testing.T) {
	// nil 事务直接拒绝（流水发放必须与单据创建同事务，plan §4.1），不触达任何数据库。
	_, err := Next(context.Background(), nil, Rule{Prefix: "PO", DateSeg: true, Reset: ResetDay})
	if err == nil {
		t.Fatal("nil 事务取号应被拒绝")
	}
	var rerr *response.Error
	if !errors.As(err, &rerr) {
		t.Fatalf("nil 事务应返回业务错误，实际 %v", err)
	}
	if !strings.HasPrefix(rerr.Error(), "DOCNUM_TX_REQUIRED: ") {
		t.Fatalf("错误码应为 DOCNUM_TX_REQUIRED，实际 %s", rerr.Error())
	}
}
