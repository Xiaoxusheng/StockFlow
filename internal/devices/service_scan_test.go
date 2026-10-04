package devices

// 统一解析 Service 单测（ask 清单：resolve 匹配器矩阵与歧义、去重窗口；内存替身）。
// 断言依据：scanner.md §5（匹配管线/响应形态）、§6.1（错误码字面）、§6.6（幂等去重）、
// backend-m3-plan §8.3（匹配器顺序冻结/scan_logs 降级放行/设备归属口径）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stockflow/server/internal/response"
)

// timeUnit/timeBase 去重窗口测试的时钟粒度与基准。
const timeUnit = time.Second

var timeBase = time.Date(2026, 10, 3, 0, 0, 0, 0, time.Local)

// ---- 匹配器替身（表驱动数据）----

type fakeSKUBarcodes struct{ hits map[string]Hit }

func (f *fakeSKUBarcodes) FindByBarcode(_ context.Context, code string) (Hit, bool, error) {
	h, ok := f.hits[code]
	return h, ok, nil
}

type fakeBins struct{ byCode map[string][]Hit }

func (f *fakeBins) FindByCode(_ context.Context, code string) ([]Hit, error) {
	return f.byCode[code], nil
}

type fakeSerials struct{ hits map[string]Hit }

func (f *fakeSerials) FindSerial(_ context.Context, sn string) (Hit, bool, error) {
	h, ok := f.hits[sn]
	return h, ok, nil
}

type fakeBatches struct{ byNo map[string][]Hit }

func (f *fakeBatches) FindBatch(_ context.Context, batchNo string) ([]Hit, error) {
	return f.byNo[batchNo], nil
}

type fakeDocs struct {
	found map[string]Hit
	err   error // 注入读失败（fail-closed 断言）
}

func (f *fakeDocs) FindByNo(_ context.Context, docNo string) (Hit, bool, error) {
	if f.err != nil {
		return Hit{}, false, f.err
	}
	h, ok := f.found[docNo]
	return h, ok, nil
}

// resolveEnv 组装带全套匹配器替身的环境（data 覆盖缺省值）。
func resolveEnv(t *testing.T, sku *fakeSKUBarcodes, bins *fakeBins, serials *fakeSerials, batches *fakeBatches,
	purchase, sales, stockops, returns *fakeDocs) *testEnv {
	t.Helper()
	if sku == nil {
		sku = &fakeSKUBarcodes{hits: map[string]Hit{}}
	}
	if bins == nil {
		bins = &fakeBins{byCode: map[string][]Hit{}}
	}
	if serials == nil {
		serials = &fakeSerials{hits: map[string]Hit{}}
	}
	if batches == nil {
		batches = &fakeBatches{byNo: map[string][]Hit{}}
	}
	if purchase == nil {
		purchase = &fakeDocs{found: map[string]Hit{}}
	}
	if sales == nil {
		sales = &fakeDocs{found: map[string]Hit{}}
	}
	if stockops == nil {
		stockops = &fakeDocs{found: map[string]Hit{}}
	}
	if returns == nil {
		returns = &fakeDocs{found: map[string]Hit{}}
	}
	return newTestEnv(t,
		WithSKUBarcodes(sku), WithBins(bins), WithSerials(serials), WithBatches(batches),
		WithPurchaseDocs(purchase), WithSalesDocs(sales), WithStockopsDocs(stockops), WithReturnsDocs(returns),
	)
}

func userIDent() ResolveIdentity {
	return ResolveIdentity{UserID: 9, Username: "张三", IP: "10.0.0.9"}
}

// ident2DeviceID 取测试设备 ID（归因断言用）。
func ident2DeviceID(e *testEnv) int64 {
	d := e.mustDevice("SF-SCAN-001")
	return int64(d.ID)
}

func deviceIdent(e *testEnv) (ResolveIdentity, DeviceContext) {
	d := e.mustDevice("SF-SCAN-001")
	return ResolveIdentity{DeviceID: int64(d.ID), DeviceCode: d.Code, WarehouseID: d.WarehouseID},
		DeviceContext{ID: int64(d.ID), Code: d.Code, WarehouseID: d.WarehouseID}
}

// TestResolveMatcherMatrix 匹配器矩阵：类型识别/命中编码/名称（scanner.md §5.2、
// plan §8.3 顺序冻结——doc 前缀 → SKU 条码 → 库位 → 序列号 → 批次 → UNKNOWN）。
func TestResolveMatcherMatrix(t *testing.T) {
	sku := &fakeSKUBarcodes{hits: map[string]Hit{
		"6901234567890": {ID: 11, Code: "SKU001", Name: "iPhone 256G", Status: "ENABLED"},
		"BAD-SKU":       {ID: 12, Code: "SKU002", Name: "停用商品", Status: "DISABLED"},
	}}
	bins := &fakeBins{byCode: map[string][]Hit{
		"A-01-03-05": {{ID: 21, Code: "A-01-03-05", Name: "WH-A-A-01-03-05", WarehouseID: 1, Status: "ENABLED"}},
	}}
	serials := &fakeSerials{hits: map[string]Hit{
		"SN0001": {ID: 31, Code: "SN0001", Name: "序列号 SN0001", WarehouseID: 1, Status: "IN_STOCK"},
	}}
	batches := &fakeBatches{byNo: map[string][]Hit{
		"B20261001": {{ID: 41, Code: "B20261001", Name: "批次 B20261001（SKU001）"}},
	}}
	purchase := &fakeDocs{found: map[string]Hit{
		"PO-20261002-000001": {ID: 51, Code: "PO-20261002-000001", Name: "采购单 PO-20261002-000001", WarehouseID: 1, Status: "APPROVED"},
		"PW-20261002-000003": {ID: 52, Code: "PW-20261002-000003", Name: "上架任务 PW-20261002-000003", WarehouseID: 1, Status: "PENDING"},
	}}

	e := resolveEnv(t, sku, bins, serials, batches, purchase, nil, nil, nil)
	ctx := context.Background()
	ident := userIDent()

	cases := []struct {
		name     string
		code     string
		wantType string
		wantID   int64
		wantCode string
	}{
		{"单据-采购单", "PO-20261002-000001", "doc", 51, "PO-20261002-000001"},
		{"任务-上架任务", "PW-20261002-000003", "doc", 52, "PW-20261002-000003"},
		{"SKU条码", "6901234567890", "sku", 11, "SKU001"},
		{"库位码", "A-01-03-05", "bin", 21, "A-01-03-05"},
		{"序列号", "SN0001", "serial", 31, "SN0001"},
		{"批次码", "B20261001", "batch", 41, "B20261001"},
	}
	for _, tc := range cases {
		res, err := e.svc.Resolve(ctx, ident, ResolveInput{Code: tc.code})
		if err != nil {
			t.Fatalf("%s: 解析失败: %v", tc.name, err)
		}
		if res.Type != tc.wantType || int64(res.ID) != tc.wantID || res.Code != tc.wantCode {
			t.Fatalf("%s: 期望 %s/%d/%s，得到 %s/%d/%s", tc.name, tc.wantType, tc.wantID, tc.wantCode, res.Type, res.ID, res.Code)
		}
	}
	// 扫码审计：6 次成功落 6 行。
	if logs, total, _ := e.svc.ListScanLogs(ctx, ScanLogFilter{UserID: 9, AllWarehouses: true}); total != int64(len(cases)) || len(logs) != len(cases) {
		t.Fatalf("scan_logs 落库数期望 %d，得到 %d", len(cases), total)
	}
}

// TestResolveDocPrefixSemantics 单据前缀语义：命中但单据不存在 → ORDER/TASK_NOT_FOUND；
// 不可扫前缀（LED/IMP）落后续匹配器；错误码字面（scanner.md §6.1 / plan §8.3 条 6）。
func TestResolveDocPrefixSemantics(t *testing.T) {
	e := resolveEnv(t, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	ident := userIDent()

	// 单据类前缀不存在。
	_, err := e.svc.Resolve(ctx, ident, ResolveInput{Code: "SO-20261002-000009"})
	assertCode(t, err, "ORDER_NOT_FOUND")
	// 任务类前缀不存在（PW/PK/CH）。
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: "PK-20261002-000001"})
	assertCode(t, err, "TASK_NOT_FOUND")
	// 不可扫前缀（LED/ADJ/IMP/EXP/PT）不是单据类型信号 → 落后续匹配器 → UNKNOWN。
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: "LED-2026-000001"})
	assertCode(t, err, "UNKNOWN_BARCODE")
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: "IMP-20261002-000001"})
	assertCode(t, err, "UNKNOWN_BARCODE")
}

// TestResolveAmbiguity 歧义消解：库位跨仓同码/批次跨 SKU 同码 → items 列表 + 顶层 ID=0
// （多命中未定位——000013 DDL 注 resolve_id 口径）；全部停用 → BIN_NOT_FOUND。
func TestResolveAmbiguity(t *testing.T) {
	bins := &fakeBins{byCode: map[string][]Hit{
		"A-01-03-05": {
			{ID: 21, Code: "A-01-03-05", Name: "WH-A-A-01-03-05", WarehouseID: 1, Status: "ENABLED"},
			{ID: 22, Code: "A-01-03-05", Name: "WH-B-A-01-03-05", WarehouseID: 2, Status: "ENABLED"},
		},
		"B-99-99-99": {
			{ID: 23, Code: "B-99-99-99", Name: "WH-A-B-99-99-99", WarehouseID: 1, Status: "DISABLED"},
		},
	}}
	batches := &fakeBatches{byNo: map[string][]Hit{
		"B20261001": {
			{ID: 41, Code: "B20261001", Name: "批次 B20261001（SKU001）"},
			{ID: 42, Code: "B20261001", Name: "批次 B20261001（SKU002）"},
		},
	}}
	e := resolveEnv(t, nil, bins, nil, batches, nil, nil, nil, nil)
	ctx := context.Background()
	ident := userIDent()

	// 库位跨仓多命中。
	res, err := e.svc.Resolve(ctx, ident, ResolveInput{Code: "A-01-03-05"})
	if err != nil {
		t.Fatalf("多命中解析失败: %v", err)
	}
	if len(res.Items) != 2 || int64(res.ID) != 0 {
		t.Fatalf("跨仓同码期望 2 items + 顶层 ID=0: %+v", res)
	}
	if res.Items[0].WarehouseID != 1 || res.Items[1].WarehouseID != 2 {
		t.Fatalf("items 仓库归属异常: %+v", res.Items)
	}
	// 批次跨 SKU 多命中。
	res, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: "B20261001"})
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("批次跨 SKU 多命中异常: %+v err=%v", res, err)
	}
	// 库位命中但全部停用。
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: "B-99-99-99"})
	assertCode(t, err, "BIN_NOT_FOUND")
}

// TestResolveDisabledSKU 命中类型但对象停用 → SKU_NOT_FOUND（plan §8.3 条 6）。
func TestResolveDisabledSKU(t *testing.T) {
	sku := &fakeSKUBarcodes{hits: map[string]Hit{
		"6901234567890": {ID: 12, Code: "SKU002", Name: "停用商品", Status: "DISABLED"},
	}}
	e := resolveEnv(t, sku, nil, nil, nil, nil, nil, nil, nil)
	_, err := e.svc.Resolve(context.Background(), userIDent(), ResolveInput{Code: "6901234567890"})
	assertCode(t, err, "SKU_NOT_FOUND")
}

// TestResolveUnknownAndInvalid 全未命中/入参校验（scanner.md §6.1 UNKNOWN_BARCODE）。
func TestResolveUnknownAndInvalid(t *testing.T) {
	e := resolveEnv(t, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	ident := userIDent()

	_, err := e.svc.Resolve(ctx, ident, ResolveInput{Code: "TOTALLY-UNKNOWN"})
	assertCode(t, err, "UNKNOWN_BARCODE")
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: ""})
	assertCode(t, err, "SCANNER_CODE_INVALID")
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	_, err = e.svc.Resolve(ctx, ident, ResolveInput{Code: string(long)})
	assertCode(t, err, "SCANNER_CODE_INVALID")
}

// TestResolveReaderMissing 匹配器未装配 → 运行期兜底拒绝（fail-closed，不静默"未识别"）。
func TestResolveReaderMissing(t *testing.T) {
	e := newTestEnv(t) // 未注入任何匹配器（仅 checker）
	_, err := e.svc.Resolve(context.Background(), userIDent(), ResolveInput{Code: "whatever"})
	assertCode(t, err, "DEVICE_READER_MISSING")
}

// TestResolveReaderError 匹配器读失败 → 透传内部错误（不静默按未命中处理——
// go-dev-standard：不隐藏错误）。
func TestResolveReaderError(t *testing.T) {
	purchase := &fakeDocs{found: map[string]Hit{}, err: errors.New("db down")}
	e := resolveEnv(t, nil, nil, nil, nil, purchase, nil, nil, nil)
	_, err := e.svc.Resolve(context.Background(), userIDent(), ResolveInput{Code: "PO-20261002-000001"})
	if err == nil || errorCodeOf(err) == "UNKNOWN_BARCODE" {
		t.Fatalf("读失败应透传而非按未命中: %v", err)
	}
}

// TestResolveScanLogAttribution 双轨归因（plan §8.3）：设备端 → device_id/device_code 落值
// + devices.last_scan_at 挂接；Web 用户端 → user 归因、device 列空。
func TestResolveScanLogAttribution(t *testing.T) {
	sku := &fakeSKUBarcodes{hits: map[string]Hit{"690": {ID: 11, Code: "SKU001", Name: "x", Status: "ENABLED"}}}
	e := resolveEnv(t, sku, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	e.seedDevice("SF-SCAN-001", 2)

	ident, _ := deviceIdent(e)
	if _, err := e.svc.Resolve(ctx, ident, ResolveInput{Code: "690", Symbology: "CODE128", Page: "收货"}); err != nil {
		t.Fatalf("设备端解析失败: %v", err)
	}
	logs, total, _ := e.svc.ListScanLogs(ctx, ScanLogFilter{DeviceID: ident.DeviceID, AllWarehouses: true})
	if total != 1 {
		t.Fatalf("设备端 scan_logs 应 1 行，得到 %d", total)
	}
	l := logs[0]
	if l.DeviceID == nil || *l.DeviceID != ident.DeviceID || l.DeviceCode == nil || *l.DeviceCode != "SF-SCAN-001" {
		t.Fatalf("设备归因缺失: %+v", l)
	}
	if l.ResolveType != "sku" || l.ResolveID != 11 || l.ResolveCode != "SKU001" || l.Symbology != "CODE128" || l.Page != "收货" {
		t.Fatalf("scan_logs 内容异常: %+v", l)
	}
	if l.UserID != 0 || l.Username != "" {
		t.Fatalf("设备端不应携带用户归因: %+v", l)
	}
	d := e.mustDevice("SF-SCAN-001")
	if d.LastScanAt.IsZero() {
		t.Fatal("设备 last_scan_at 未挂接（devices.md §7.1）")
	}

	// Web 用户端：device 列空、user 归因（plan §8.3 可空口径）。
	if _, err := e.svc.Resolve(ctx, userIDent(), ResolveInput{Code: "690"}); err != nil {
		t.Fatalf("用户端解析失败: %v", err)
	}
	userLogs, _, _ := e.svc.ListScanLogs(ctx, ScanLogFilter{UserID: 9, AllWarehouses: true})
	if len(userLogs) != 1 || userLogs[0].DeviceID != nil {
		t.Fatalf("用户端 scan_logs 应无设备行: %+v", userLogs)
	}
}

// TestResolveScanLogDegrade scan_logs 写失败降级放行（plan §8.3"写失败记 error 日志并
// 降级放行"——ask"方案偏离6"）：识别结果与错误码不受影响。
func TestResolveScanLogDegrade(t *testing.T) {
	sku := &fakeSKUBarcodes{hits: map[string]Hit{"690": {ID: 11, Code: "SKU001", Name: "x", Status: "ENABLED"}}}
	e := resolveEnv(t, sku, nil, nil, nil, nil, nil, nil, nil)
	e.repo.scanLogFail = true
	ctx := context.Background()

	// 成功识别：写失败降级，结果正常返回。
	res, err := e.svc.Resolve(ctx, userIDent(), ResolveInput{Code: "690"})
	if err != nil || res.Type != "sku" {
		t.Fatalf("写失败应降级放行: res=%+v err=%v", res, err)
	}
	// 识别失败（未知码）：同样先落审计（失败）再返回错误——写失败仍降级放行。
	_, err = e.svc.Resolve(ctx, userIDent(), ResolveInput{Code: "UNKNOWN-CODE"})
	assertCode(t, err, "UNKNOWN_BARCODE")
}

// TestDedupWindow 去重窗口（scanner.md §6.6）：同码+同页面+同来源极短窗口 → duplicate；
// 换页面/换来源/跨窗口 → 非重复；重复事件不抑制识别、scan_logs 照常落库。
func TestDedupWindow(t *testing.T) {
	sku := &fakeSKUBarcodes{hits: map[string]Hit{"690": {ID: 11, Code: "SKU001", Name: "x", Status: "ENABLED"}}}
	e := resolveEnv(t, sku, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	ident := userIDent()

	// 首扫非重复。
	res, _ := e.svc.Resolve(ctx, ident, ResolveInput{Code: "690", Page: "收货"})
	if res.Duplicate {
		t.Fatal("首扫不应标记重复")
	}
	// 极短窗口内同码同页面 → duplicate=true。
	res, _ = e.svc.Resolve(ctx, ident, ResolveInput{Code: "690", Page: "收货"})
	if !res.Duplicate {
		t.Fatal("窗口内重复事件应标记 duplicate")
	}
	// 换页面 → 非重复（§6.6：同码+同页面+同上下文才去重）。
	res, _ = e.svc.Resolve(ctx, ident, ResolveInput{Code: "690", Page: "拣货"})
	if res.Duplicate {
		t.Fatal("不同页面不应标记重复")
	}
	// 换来源（设备 vs 用户）→ 非重复。
	e.seedDevice("SF-SCAN-001", 1)
	dIdent, _ := deviceIdent(e)
	res, _ = e.svc.Resolve(ctx, dIdent, ResolveInput{Code: "690", Page: "收货"})
	if res.Duplicate {
		t.Fatal("不同来源（设备）不应标记重复")
	}
	// 正常连续扫码允许重复：窗口滑动推进超过 TTL → 非重复（同 SKU 连续计数场景）。
	e.advance(DefaultDedupTTL + timeUnit)
	res, _ = e.svc.Resolve(ctx, ident, ResolveInput{Code: "690", Page: "收货"})
	if res.Duplicate {
		t.Fatal("跨窗口的正常连续扫码不应标记重复")
	}
	// 重复事件不抑制识别、scan_logs 照常落库（append-only 不丢事件）。
	// 用户源 4 行（1 首扫 + 1 窗口内重复 + 1 换页面 + 1 跨窗口）+ 设备源 1 行（另归因）。
	logs, total, _ := e.svc.ListScanLogs(ctx, ScanLogFilter{UserID: 9, AllWarehouses: true})
	if total != 4 || len(logs) != 4 {
		t.Fatalf("重复事件也应落审计：用户源期望 4 行，得到 %d", total)
	}
	if _, dtotal, _ := e.svc.ListScanLogs(ctx, ScanLogFilter{DeviceID: ident2DeviceID(e), AllWarehouses: true}); dtotal != 1 {
		t.Fatalf("设备源期望 1 行，得到 %d", dtotal)
	}
	// 全部行均为成功识别（无抑制）。
	for _, l := range logs {
		if !l.Success || l.ResolveType != "sku" {
			t.Fatalf("识别被抑制: %+v", l)
		}
	}
}

// TestDedupWindowComponent DedupWindow 组件语义：Mark 首次 true / 窗口内 false /
// 滑动刷新 / 惰性清扫有界内存（go-dev-standard：无界内存增长禁项）。
func TestDedupWindowComponent(t *testing.T) {
	now := timeBase
	w := NewDedupWindow(100 * timeUnit)
	w.now = func() time.Time { return now }

	if !w.Mark("k1") {
		t.Fatal("首Mark应=true")
	}
	if w.Mark("k1") {
		t.Fatal("窗口内重复Mark应=false")
	}
	if !w.Mark("k2") {
		t.Fatal("不同键应=true")
	}
	now = now.Add(50 * timeUnit)
	if w.Mark("k1") {
		t.Fatal("窗口内（t=50s < ttl=100s）应判定重复")
	}
	now = now.Add(101 * timeUnit)
	if !w.Mark("k1") {
		t.Fatal("跨窗口（距上次登记 101s >= ttl）应=true")
	}
	if w.Len() == 0 {
		t.Fatal("窗口内应有活跃键")
	}
	now = now.Add(1000 * timeUnit)
	_ = w.Mark("k3") // 触发清扫
	if w.Len() != 1 {
		t.Fatalf("到期键应被清扫，剩余 %d", w.Len())
	}
	// DedupKey 稳定性：同输入同键，异输入异键。
	if DedupKey("a", "p", "c", "") != DedupKey("a", "p", "c", "") {
		t.Fatal("DedupKey 应稳定")
	}
	if DedupKey("a", "p", "c", "") == DedupKey("b", "p", "c", "") {
		t.Fatal("不同来源应得不同键")
	}
}

// TestResolveErrorCodeLiteral 错误码字面冻结核验（plan §8.3：scanner.md §6.1 字面）。
func TestResolveErrorCodeLiteral(t *testing.T) {
	for _, c := range []struct {
		code response.Code
		want string
	}{
		{ErrUnknownBarcode, "UNKNOWN_BARCODE"},
		{ErrSKUNotFound, "SKU_NOT_FOUND"},
		{ErrBinNotFound, "BIN_NOT_FOUND"},
		{ErrOrderNotFound, "ORDER_NOT_FOUND"},
		{ErrTaskNotFound, "TASK_NOT_FOUND"},
	} {
		if c.code.Code != c.want {
			t.Fatalf("错误码字面偏离 scanner.md §6.1: 期望 %s 得到 %s", c.want, c.code.Code)
		}
	}
}
