package sysops

// sysops 单测——不依赖 PostgreSQL/Redis/网络（backend-m3-plan §14：dedup_key 复合收件人
// 去重与接收人解析规则、cron 包装器注入 clock 以内存替身覆盖；预警扫描的 SQL 聚合与
// notifications 真库唯一索引行为属 //go:build integration 集成测试面）。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// ---- 阈值清单与口径（inventory-rules §7.1/§11）----

func TestParsePositiveIntList(t *testing.T) {
	got, err := parsePositiveIntList("30,15,7,3")
	if err != nil {
		t.Fatalf("标准效期阈值不应报错: %v", err)
	}
	if got[0] != 30 || got[1] != 15 || got[2] != 7 || got[3] != 3 {
		t.Fatalf("应降序 30,15,7,3，实际 %v", got)
	}
	if _, err := parsePositiveIntList("30,x"); err == nil {
		t.Fatal("非法项应报错（fail-closed，不静默截断）")
	}
	if _, err := parsePositiveIntList(""); err == nil {
		t.Fatal("空清单应报错")
	}
}

func TestExpiryLevelThresholds(t *testing.T) {
	// inventory-rules §7.1：30/15/7/3 + 已过期；降序清单首个 >= daysLeft 的档位命中。
	thresholds := []int{30, 15, 7, 3}
	cases := []struct {
		daysLeft int
		want     string
	}{
		{-5, "expired"}, {0, "expired"},
		{1, "3"}, {3, "3"}, {4, "7"}, {7, "7"}, {8, "15"}, {15, "15"},
		{16, "30"}, {30, "30"}, {31, ""}, // 超出最大档位不预警
	}
	for _, c := range cases {
		if got := (&scanRunner{}).expiryLevel(c.daysLeft, thresholds); got != c.want {
			t.Fatalf("剩余 %d 天档位应为 %q，实际 %q", c.daysLeft, c.want, got)
		}
	}
}

func TestStagnantTierForBoundaries(t *testing.T) {
	// inventory-rules §11：30/60/90 分档，命中最大档位。
	thresholds := []int{90, 60, 30}
	cases := []struct {
		idle int
		want string
	}{
		{29, ""}, {30, "30"}, {59, "30"}, {60, "60"}, {89, "60"}, {90, "90"},
	}
	for _, c := range cases {
		if got := stagnantTierFor(c.idle, thresholds); got != c.want {
			t.Fatalf("未动 %d 天档位应为 %q，实际 %q", c.idle, c.want, got)
		}
	}
}

func TestParseBoolConfig(t *testing.T) {
	if v, err := parseBoolConfig("true", "k"); err != nil || !v {
		t.Fatalf("true 应解析为启用: %v %v", v, err)
	}
	if v, err := parseBoolConfig("False", "k"); err != nil || v {
		t.Fatalf("False 应解析为停用: %v %v", v, err)
	}
	if _, err := parseBoolConfig("yes", "k"); err == nil {
		t.Fatal("宽松值域外应报错（fail-closed）")
	}
}

func TestParseIntConfig(t *testing.T) {
	if v, err := parseIntConfig("14", "k"); err != nil || v != 14 {
		t.Fatalf("14 应解析成功: %v %v", v, err)
	}
	if _, err := parseIntConfig("-1", "k"); err == nil {
		t.Fatal("负数应报错")
	}
}

// ---- dedup_key 复合收件人与通知幂等（plan §4.3/§14：内存替身模拟唯一索引）----

// fakeNotifyStore 内存通知存储：dedup 键唯一索引语义（重复键 = ON CONFLICT DO NOTHING）。
type fakeNotifyStore struct {
	mu      sync.Mutex
	seen    map[string]bool
	deduped []string
	failOn  map[string]error
}

func newFakeNotifyStore() *fakeNotifyStore {
	return &fakeNotifyStore{seen: map[string]bool{}, failOn: map[string]error{}}
}

func (f *fakeNotifyStore) insertNotification(_ context.Context, userID int64, dedup string, in notifyInput) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failOn[dedup]; err != nil {
		return false, err
	}
	if dedup != "" {
		if f.seen[dedup] {
			f.deduped = append(f.deduped, dedup)
			return false, nil // 唯一索引命中 → 跳过（幂等）
		}
		f.seen[dedup] = true
	}
	return true, nil
}

func TestDedupKeyComposesRecipient(t *testing.T) {
	// plan §4.3：dedup_key = 对象键:{user_id} 复合收件人（如 lowstock:20261003:SKU-001:WH-A:42）。
	if got := dedupKeyFor("lowstock:20261003:SKU-001:WH-A", 42); got != "lowstock:20261003:SKU-001:WH-A:42" {
		t.Fatalf("dedup 键应复合收件人后缀，实际 %q", got)
	}
}

func TestNotifyRecipientsDedupIdempotent(t *testing.T) {
	store := newFakeNotifyStore()
	in := notifyInput{
		Type: NotifyTypeStockAlert, Title: "低库存预警",
		Content:  "可用库存 5.0000 已低于等于安全库存 10.0000",
		DedupKey: "lowstock:20261003:SKU-001:WH-A",
	}
	recipients := []int64{1, 2, 3}
	inserted, err := notifyRecipients(context.Background(), store, in, recipients)
	if err != nil {
		t.Fatalf("首轮写入不应报错: %v", err)
	}
	if inserted != int64(len(recipients)) {
		t.Fatalf("首轮应逐收件人各落一条（%d），实际 %d", len(recipients), inserted)
	}
	// 同日重复扫描（幂等重投递）：全部命中 dedup，零新增。
	inserted2, err := notifyRecipients(context.Background(), store, in, recipients)
	if err != nil {
		t.Fatalf("重扫描不应报错: %v", err)
	}
	if inserted2 != 0 {
		t.Fatalf("重扫描应零新增（幂等），实际 %d", inserted2)
	}
	// 新的一天（对象键日期段变化）→ 重新通知。
	in2 := in
	in2.DedupKey = "lowstock:20261004:SKU-001:WH-A"
	inserted3, err := notifyRecipients(context.Background(), store, in2, recipients)
	if err != nil || inserted3 != int64(len(recipients)) {
		t.Fatalf("次日应重新通知: inserted=%d err=%v", inserted3, err)
	}
	// 无 dedup 键（失败告警自带唯一时间戳前）→ 不去重。
	in3 := in
	in3.DedupKey = ""
	inserted4, _ := notifyRecipients(context.Background(), store, in3, []int64{1, 1})
	if inserted4 != 2 {
		t.Fatalf("无 dedup 键不应去重，实际 %d", inserted4)
	}
}

func TestNotifyRecipientsFailFastOnError(t *testing.T) {
	store := newFakeNotifyStore()
	store.failOn[dedupKeyFor("lowstock:20261003:S:W", 2)] = errors.New("db down")
	in := notifyInput{Type: NotifyTypeStockAlert, DedupKey: "lowstock:20261003:S:W"}
	inserted, err := notifyRecipients(context.Background(), store, in, []int64{1, 2, 3})
	if err == nil {
		t.Fatal("单收件人写入失败应上抛（调用方记执行日志 FAILED）")
	}
	if inserted != 1 {
		t.Fatalf("失败前已落库行数应保留（1），实际 %d", inserted)
	}
}

func TestNotifyRecipientsNilStore(t *testing.T) {
	if _, err := notifyRecipients(context.Background(), nil, notifyInput{}, []int64{1}); err == nil {
		t.Fatal("nil store 应报错（不静默丢通知）")
	}
}

// ---- cron 注册表接入（wiring：本包四任务注册，task_timeout_scan 保持空实现位）----

func TestWireJobHandlers(t *testing.T) {
	// cron_test.go 先行用例可能已向包级注册表写入 task_timeout_scan——
	// 断言 wireJobHandlers 自身不改变其注册状态（保持空实现位语义）。
	_, before := jobHandlerFor("task_timeout_scan")
	wireJobHandlers(&gormStub{}, nil)
	for _, code := range []string{
		"inventory_low_stock_scan", "inventory_expiry_scan",
		"inventory_stagnant_scan", "file_cleanup",
	} {
		if fn, ok := jobHandlerFor(code); !ok || fn == nil {
			t.Fatalf("任务 %s 的 handler 应已注册", code)
		}
	}
	// plan §2.2：task_timeout_scan 读取器属 sales/purchase 域，本包无数据通路——空实现位。
	if _, after := jobHandlerFor("task_timeout_scan"); after != before {
		t.Fatal("wireJobHandlers 不应注册 task_timeout_scan（保持空实现位语义）")
	}
}

// gormStub 非nil gorm.DB 句柄替身（仅满足注册闭包捕获，不执行 SQL）。
type gormStub = gorm.DB

// ---- 时钟与键段 ----

func TestDayKey(t *testing.T) {
	tm := time.Date(2026, 10, 3, 23, 59, 59, 0, time.Local)
	if got := dayKey(tm); got != "20261003" {
		t.Fatalf("dayKey 应为 YYYYMMDD，实际 %q", got)
	}
}

func TestExpiryLevelText(t *testing.T) {
	if got := expiryLevelText("expired", -2); got != "已过期" {
		t.Fatalf("过期档位文案异常: %q", got)
	}
	if got := expiryLevelText("15", 12); got == "" {
		t.Fatal("临期档位文案不应为空")
	}
}

// ---- 预警通知接收人角色清单（plan §10.6 冻结）----

func TestRecipientRoleLists(t *testing.T) {
	if len(alertRecipientRoles) != 3 {
		t.Fatalf("预警收件人应为 super_admin/sys_admin/warehouse_manager 三角色，实际 %v", alertRecipientRoles)
	}
	if len(failureRecipientRoles) != 2 {
		t.Fatalf("失败告警收件人应为 super_admin/sys_admin 两角色，实际 %v", failureRecipientRoles)
	}
}
