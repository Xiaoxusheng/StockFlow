package reports

// reports 单测——不依赖 PostgreSQL/Redis/网络（backend-m3-plan §14：补货建议公式与
// 依据字段、报表聚合边界、积压分档、阈值口径以纯函数/替身覆盖；聚合 SQL 的真库行为
// 属 //go:build integration 集成测试面）。

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---- 时间范围校验（报表聚合边界）----

func TestValidateRangeDefaults(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	from, to, err := validateRange(nil, nil, now)
	if err != nil {
		t.Fatalf("缺省范围不应报错: %v", err)
	}
	// 缺省近 30 天窗口。
	if d := to.Sub(from); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("缺省窗口应约 30 天，实际 %v", d)
	}
}

func TestValidateRangeBoundary366(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	start := now.AddDate(0, 0, -366)
	if _, _, err := validateRange(&start, &now, now); err != nil {
		t.Fatalf("366 天边界不应报错: %v", err)
	}
	start367 := now.AddDate(0, 0, -367)
	if _, _, err := validateRange(&start367, &now, now); err == nil {
		t.Fatal("367 天应报 REPORT_RANGE_TOO_LARGE 语义错误")
	}
}

func TestValidateRangeInverted(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local)
	from := now.AddDate(0, 0, -1)
	to := now.AddDate(0, 0, -2)
	if _, _, err := validateRange(&from, &to, now); err == nil {
		t.Fatal("from > to 应报错")
	}
}

func TestParseDay(t *testing.T) {
	if _, err := parseDay("2026-10-03"); err != nil {
		t.Fatalf("日期格式应可解析: %v", err)
	}
	if _, err := parseDay("2026-10-03 08:30:00"); err != nil {
		t.Fatalf("日期时间格式应可解析: %v", err)
	}
	if _, err := parseDay("2026/10/03"); err == nil {
		t.Fatal("非法格式应报错")
	}
}

// ---- 积压分档（inventory-rules §11：30/60/90 天未动）----

func TestStagnantTierBoundaries(t *testing.T) {
	thresholds := []int{90, 60, 30} // parsePositiveIntList 降序输出
	cases := []struct {
		idle int
		want string
	}{
		{29, ""}, {30, "30"}, {59, "30"}, {60, "60"}, {89, "60"}, {90, "90"}, {365, "90"},
	}
	for _, c := range cases {
		if got := stagnantTier(c.idle, thresholds); got != c.want {
			t.Fatalf("未动 %d 天档位应为 %q，实际 %q", c.idle, c.want, got)
		}
	}
	if got := stagnantTier(5, []int{45, 15}); got != "" {
		t.Fatalf("低于最小档位应返回空串，实际 %q", got)
	}
}

// ---- 补货建议公式与依据（plan §9.3；inventory-rules §11 只读红线）----

func TestCalcReplenishmentNormal(t *testing.T) {
	// 日均 10 × (7+3) = 100 > 安全库存 40 → 目标 100；可用 20、在途 10 → 建议 70。
	b := calcReplenishment(20, 40, 10, 10, 7, 3)
	if b.TargetStock != 100 {
		t.Fatalf("目标库存应为 100，实际 %v", b.TargetStock)
	}
	if b.SuggestedQty != 70 {
		t.Fatalf("建议补货量应为 70，实际 %v", b.SuggestedQty)
	}
	if b.CurrentAvailable != 20 || b.IncomingQty != 10 || b.SafetyStock != 40 {
		t.Fatalf("依据字段应原样携带: %+v", b)
	}
}

func TestCalcReplenishmentSafetyDominates(t *testing.T) {
	// 日均销量为 0 时目标库存 = 安全库存（max 语义）。
	b := calcReplenishment(0, 50, 0, 0, 7, 3)
	if b.TargetStock != 50 || b.SuggestedQty != 50 {
		t.Fatalf("目标/建议应为安全库存 50: %+v", b)
	}
}

func TestCalcReplenishmentClampsNonNegative(t *testing.T) {
	// 可用 + 在途已超目标 → 建议 0（不产生负建议）；负输入按 0 归一。
	b := calcReplenishment(120, 40, 10, 10, 7, 3)
	if b.SuggestedQty != 0 {
		t.Fatalf("充足库存建议应为 0，实际 %v", b.SuggestedQty)
	}
	b = calcReplenishment(-5, 40, -10, -3, 7, 3)
	if b.CurrentAvailable != 0 || b.DailyAvgSales != 0 || b.IncomingQty != 0 {
		t.Fatalf("负输入应归一为 0: %+v", b)
	}
	if b.TargetStock != 40 || b.SuggestedQty != 40 {
		t.Fatalf("归一后应按安全库存建议: %+v", b)
	}
}

func TestCalcReplenishmentRoundsTo4dp(t *testing.T) {
	// 日均销量 = 流水合计/30 可能出现无限小数 → numeric(18,4) 标度取整。
	b := calcReplenishment(0, 0, 10.0/3.0, 0, 7, 3)
	if b.DailyAvgSales != 3.3333 {
		t.Fatalf("日均销量应四舍五入到 4 位小数，实际 %v", b.DailyAvgSales)
	}
	// 目标 = 3.3333…×10 = 33.333… → 33.3333。
	if b.TargetStock != 33.3333 {
		t.Fatalf("目标库存应取整到 4 位小数，实际 %v", b.TargetStock)
	}
}

// ---- 库存周转（出库量/平均库存）----

func TestTurnoverRate(t *testing.T) {
	rate, days := turnoverRate(200, 100, 30)
	if rate != 2 {
		t.Fatalf("周转率应为 2，实际 %v", rate)
	}
	if days != 15 {
		t.Fatalf("周转天数应为 30/2=15，实际 %v", days)
	}
	// 平均库存为 0（空仓）不造假分母。
	if rate, days := turnoverRate(200, 0, 30); rate != 0 || days != 0 {
		t.Fatalf("平均库存为 0 应返回 0/0: %v/%v", rate, days)
	}
	if rate, _ := turnoverRate(0, 100, 30); rate != 0 {
		t.Fatalf("无出库周转率应为 0，实际 %v", rate)
	}
}

// ---- 阈值清单解析（inventory-rules §7.1 口径）----

func TestParsePositiveIntList(t *testing.T) {
	got, err := parsePositiveIntList("30,15,7,3")
	if err != nil {
		t.Fatalf("标准清单不应报错: %v", err)
	}
	if got[0] != 30 || got[1] != 15 || got[2] != 7 || got[3] != 3 {
		t.Fatalf("应降序输出 30,15,7,3，实际 %v", got)
	}
	got, err = parsePositiveIntList("90, 30,60,30")
	if err != nil {
		t.Fatalf("重复与空白应容忍: %v", err)
	}
	if len(got) != 3 || got[0] != 90 || got[1] != 60 || got[2] != 30 {
		t.Fatalf("应去重降序 90,60,30，实际 %v", got)
	}
	if _, err := parsePositiveIntList("30,abc"); err == nil {
		t.Fatal("非数字项应报错（fail-closed）")
	}
	if _, err := parsePositiveIntList("0,-3"); err == nil {
		t.Fatal("非正数项应报错")
	}
	if _, err := parsePositiveIntList(",,"); err == nil {
		t.Fatal("空清单应报错")
	}
}

// ---- 报表目录（代码内冻结注册表，plan §9.1）----

func TestCatalogFrozen(t *testing.T) {
	c := Catalog()
	want := map[string]bool{
		"inventory-summary": false, "inbound-stats": false, "outbound-stats": false,
		"inventory-turnover": false, "stagnant-stock": false, "replenishment-suggestions": false,
	}
	for _, item := range c {
		if _, ok := want[item.Key]; !ok {
			t.Fatalf("目录含冻结清单外报表: %q", item.Key)
		}
		want[item.Key] = true
		if item.Name == "" || item.Category == "" {
			t.Fatalf("报表 %q 缺少名称/分类", item.Key)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("目录缺少冻结报表: %q", k)
		}
	}
	// 副本语义：外部修改不影响注册表。
	c[0].Key = "tampered"
	if Catalog()[0].Key == "tampered" {
		t.Fatal("Catalog 应返回副本")
	}
}

// ---- Service 装配：可注入阈值/补货参数替身（plan §14 reader 替身注入）----

func TestServiceReplenishmentParamsInjection(t *testing.T) {
	var called bool
	s := NewService(nil, WithReplenishmentParams(func(context.Context) (int, int, error) {
		called = true
		return 14, 7, nil
	}))
	lead, buffer, err := s.replenishmentParams(context.Background())
	if err != nil || !called || lead != 14 || buffer != 7 {
		t.Fatalf("补货参数替身应生效: called=%v lead=%d buffer=%d err=%v", called, lead, buffer, err)
	}
}

func TestServiceAlertThresholdsInjection(t *testing.T) {
	s := NewService(nil, WithAlertThresholds(func(context.Context) ([]int, []int, error) {
		return nil, nil, errors.New("config down")
	}))
	if _, _, err := s.alertThresholds(context.Background()); err == nil {
		t.Fatal("阈值替身错误应上抛（fail-closed，不静默用缺省值）")
	}
}
