package devices

// 设备管理 Service 单测（ask 清单：激活码一次性/时效、心跳状态机；内存替身，无 PG/Redis/网络）。
// 断言依据：backend-m3-plan §8.1/§8.2（激活协议/token_version 撤销语义）、devices.md §6–§7。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stockflow/server/internal/database"
)

// scopeAll 全仓数据权限（单测缺省——与超管/ALL 范围同口径）。
func scopeAll() WarehouseScope { return WarehouseScope{All: true} }

func actorAdmin() Actor {
	return Actor{UserID: 42, Username: "admin", RequestID: "req-1", IP: "10.0.0.1"}
}

func createInput(code string) DeviceCreateInput {
	return DeviceCreateInput{Code: code, Name: "测试设备-" + code, Type: TypePDA, WarehouseID: 1}
}

// activateByPayload 走完整激活流程（取 CreateDevice 载荷中的 token 完成激活）。
func activateByPayload(t *testing.T, e *testEnv, payload *DeviceActivationPayload, appVersion string) *ActivationResult {
	t.Helper()
	token := extractToken(t, payload.QRContent)
	res, err := e.svc.Activate(context.Background(), ActivateInput{
		DeviceCode: payload.DeviceCode, Token: token,
		Brand: "Zebra", Model: "TC27", OS: "Android 13", AppVersion: appVersion,
	}, "10.0.0.8")
	if err != nil {
		t.Fatalf("激活失败: %v", err)
	}
	return res
}

// extractToken 从二维码内容 JSON 提取 token（plan §8.2 三字段协议）。
func extractToken(t *testing.T, qr string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(qr), &m); err != nil {
		t.Fatalf("二维码内容非法: %v（%q）", err, qr)
	}
	if m["token"] == "" || m["device_code"] == "" || m["server_url"] == "" {
		t.Fatalf("二维码内容缺字段: %q", qr)
	}
	return m["token"]
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %s，得到 nil", want)
	}
	if got := errorCodeOf(err); got != want {
		t.Fatalf("期望错误码 %s，得到 %s（%v）", want, got, err)
	}
}

// TestCreateDeviceValidation 创建校验：编码/名称/类型/仓库存在性（api.md §4 后端完整校验）。
func TestCreateDeviceValidation(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	// 编码非法（小写字母开头允许；此处测特殊字符与单字符）。
	if _, err := e.svc.CreateDevice(ctx, actorAdmin(), DeviceCreateInput{Code: "SF SCAN@001", Name: "x", Type: TypePDA}, "http://api"); err == nil {
		t.Fatal("期望编码校验拒绝")
	} else {
		assertCode(t, err, "DEVICE_CODE_INVALID")
	}
	// 类型非法。
	_, err := e.svc.CreateDevice(ctx, actorAdmin(), DeviceCreateInput{Code: "SF-SCAN-001", Name: "x", Type: "robot"}, "http://api")
	assertCode(t, err, "DEVICE_TYPE_INVALID")
	// 仓库不存在（checker 返回 false）。
	_, err = e.svc.CreateDevice(ctx, actorAdmin(), DeviceCreateInput{Code: "SF-SCAN-001", Name: "x", Type: TypePDA, WarehouseID: 9}, "http://api")
	assertCode(t, err, "DEVICE_WAREHOUSE_NOT_FOUND")
	// 编码冲突。
	if _, err := e.svc.CreateDevice(ctx, actorAdmin(), createInput("SF-SCAN-001"), "http://api"); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	_, err = e.svc.CreateDevice(ctx, actorAdmin(), createInput("SF-SCAN-001"), "http://api")
	assertCode(t, err, "DEVICE_CODE_CONFLICT")
}

// TestCreateDevicePayload 创建成功：返回激活二维码 payload（一次性 token 仅哈希落库）、
// 15 分钟时效、同事务审计（plan §8.1/§8.2/§13.8）。
func TestCreateDevicePayload(t *testing.T) {
	e := newTestEnv(t)
	payload, err := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-001"), "http://api.test")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if payload.QRContent == "" || !strings.Contains(payload.QRContent, "SF-SCAN-001") {
		t.Fatalf("二维码内容缺设备编码: %q", payload.QRContent)
	}
	if payload.ServerURL != "http://api.test" {
		t.Fatalf("server_url 应取请求推导值，得到 %q", payload.ServerURL)
	}
	if payload.Status != "pending" || payload.ActivationStatus != ActivationPending {
		t.Fatalf("激活状态期望 pending/PENDING，得到 %s/%s", payload.Status, payload.ActivationStatus)
	}
	// 时效 = now + 15min（plan §8.2 冻结）。
	want := e.now.Add(activationTTL)
	if !payload.ExpiresAt.Time.Equal(want) {
		t.Fatalf("激活码时效期望 %v，得到 %v", want, payload.ExpiresAt.Time)
	}
	// token 仅哈希落库：库内哈希 = SHA-256(token)。
	token := extractToken(t, payload.QRContent)
	d, _ := e.repo.FindDeviceByCode(context.Background(), "SF-SCAN-001")
	if d.ActivationTokenHash != hashActivationToken(token) {
		t.Fatal("库内激活哈希与 token 不匹配")
	}
	if d.ActivationTokenHash == token {
		t.Fatal("激活 token 不得明文落库（plan §8.2）")
	}
	// 同事务审计：module=devices action=create。
	entries := e.spy.all()
	if len(entries) != 1 || entries[0].Action != "create" || entries[0].Module != "devices" {
		t.Fatalf("审计断言失败: %+v", entries)
	}
}

// TestActivateOneTimeAndExpiry 激活协议：错码拒绝、一次性消费（重放拒绝）、过期拒绝
// （plan §8.2 防重放裁决——三因统一 DEVICE_ACTIVATION_INVALID）。
func TestActivateOneTimeAndExpiry(t *testing.T) {
	e := newTestEnv(t)
	payload, err := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-001"), "http://api")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	token := extractToken(t, payload.QRContent)

	// 错误 token。
	_, err = e.svc.Activate(context.Background(), ActivateInput{DeviceCode: "SF-SCAN-001", Token: "wrong-token-wrong-token-wrong"}, "")
	assertCode(t, err, "DEVICE_ACTIVATION_INVALID")

	// 正确激活 → 设备令牌可用。
	res := activateByPayload(t, e, payload, "1.0.0")
	if res.DeviceCode != "SF-SCAN-001" || res.DeviceToken == "" || res.WarehouseID != 1 {
		t.Fatalf("激活结果异常: %+v", res)
	}
	claims, err := e.svc.token.Parse(res.DeviceToken)
	if err != nil {
		t.Fatalf("设备令牌解析失败: %v", err)
	}
	if claims.Code != "SF-SCAN-001" || claims.Version != 1 || claims.Type != TypePDA {
		t.Fatalf("设备令牌 claims 异常: %+v", claims)
	}
	if res.ExpiresAt.Time.Sub(e.now) != TokenTTL {
		t.Fatalf("设备令牌 TTL 期望 365d，得到 %v", res.ExpiresAt.Time.Sub(e.now))
	}

	// 重放同 token（一次性消费——哈希已清）。
	_, err = e.svc.Activate(context.Background(), ActivateInput{DeviceCode: "SF-SCAN-001", Token: token}, "")
	assertCode(t, err, "DEVICE_ACTIVATION_INVALID")

	// 过期 token（先创建、时钟推进 16 分钟 > 15 分钟时效后再激活）。
	payload2, err := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-002"), "http://api")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	token2 := extractToken(t, payload2.QRContent)
	e.advance(16 * time.Minute)
	_, err = e.svc.Activate(context.Background(), ActivateInput{DeviceCode: "SF-SCAN-002", Token: token2}, "")
	assertCode(t, err, "DEVICE_ACTIVATION_INVALID")
}

// TestRegenerateActivationInvalidatesOldToken 重新生成激活码：旧设备令牌全失效
// （token_version+1）、旧激活码作废、返回新二维码（plan §8.1）。
func TestRegenerateActivationInvalidatesOldToken(t *testing.T) {
	e := newTestEnv(t)
	payload, _ := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-001"), "http://api")
	res := activateByPayload(t, e, payload, "1.0.0")

	newPayload, err := e.svc.RegenerateActivation(context.Background(), actorAdmin(), int64(payload.DeviceID), "http://api", scopeAll())
	if err != nil {
		t.Fatalf("重新生成失败: %v", err)
	}
	// 旧设备令牌已失效（token_version+1）。
	_, err = e.svc.AuthenticateDevice(context.Background(), res.DeviceToken)
	assertCode(t, err, "DEVICE_TOKEN_INVALID")
	// 旧激活码已作废。
	_, err = e.svc.Activate(context.Background(), ActivateInput{
		DeviceCode: "SF-SCAN-001", Token: extractToken(t, payload.QRContent)}, "")
	assertCode(t, err, "DEVICE_ACTIVATION_INVALID")
	// 新激活码可用 → 新令牌 token_version=2。
	res2 := activateByPayload(t, e, newPayload, "1.0.1")
	claims, err := e.svc.token.Parse(res2.DeviceToken)
	if err != nil || claims.Version != 2 {
		t.Fatalf("新令牌版本期望 2，得到 %+v err=%v", claims, err)
	}
	// 审计。
	found := false
	for _, en := range e.spy.all() {
		if en.Action == "regenerate_activation" {
			found = true
		}
	}
	if !found {
		t.Fatal("重新生成激活码缺少审计（plan §13.8）")
	}
}

// TestDisableDeviceInvalidatesToken 停用：状态守卫 + token_version+1（设备端全部拒绝，
// plan §8.1）；重复停用冲突。
func TestDisableDeviceInvalidatesToken(t *testing.T) {
	e := newTestEnv(t)
	payload, _ := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-001"), "http://api")
	res := activateByPayload(t, e, payload, "1.0.0")

	if err := e.svc.DisableDevice(context.Background(), actorAdmin(), int64(payload.DeviceID), scopeAll()); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	_, err := e.svc.AuthenticateDevice(context.Background(), res.DeviceToken)
	assertCode(t, err, "DEVICE_TOKEN_INVALID")
	// 重复停用 → 状态冲突。
	err = e.svc.DisableDevice(context.Background(), actorAdmin(), int64(payload.DeviceID), scopeAll())
	assertCode(t, err, "DEVICE_STATUS_CONFLICT")
}

// TestBindUnbindTokenLifecycle 绑定/解绑（devices.md §6.4；解绑 token_version+1——
// 000013 DDL 注撤销语义）。
func TestBindUnbindTokenLifecycle(t *testing.T) {
	e := newTestEnv(t)
	payload, _ := e.svc.CreateDevice(context.Background(), actorAdmin(), createInput("SF-SCAN-001"), "http://api")
	res := activateByPayload(t, e, payload, "1.0.0")

	// 绑定：不影响设备令牌。
	if err := e.svc.BindDevice(context.Background(), actorAdmin(), int64(payload.DeviceID), 7, scopeAll()); err != nil {
		t.Fatalf("绑定失败: %v", err)
	}
	if _, err := e.svc.AuthenticateDevice(context.Background(), res.DeviceToken); err != nil {
		t.Fatalf("绑定后设备令牌应仍有效: %v", err)
	}
	d, _ := e.repo.FindDeviceByCode(context.Background(), "SF-SCAN-001")
	if d.BoundUserID != 7 {
		t.Fatalf("绑定操作者期望 7，得到 %d", d.BoundUserID)
	}
	// 非法 user_id。
	err := e.svc.BindDevice(context.Background(), actorAdmin(), int64(payload.DeviceID), 0, scopeAll())
	assertCode(t, err, "COMMON_INVALID_PARAM")
	// 解绑：token_version+1 → 旧令牌失效。
	if err := e.svc.UnbindDevice(context.Background(), actorAdmin(), int64(payload.DeviceID), scopeAll()); err != nil {
		t.Fatalf("解绑失败: %v", err)
	}
	_, err = e.svc.AuthenticateDevice(context.Background(), res.DeviceToken)
	assertCode(t, err, "DEVICE_TOKEN_INVALID")
	d, _ = e.repo.FindDeviceByCode(context.Background(), "SF-SCAN-001")
	if d.BoundUserID != 0 || d.TokenVersion != 2 {
		t.Fatalf("解绑后期望 bound=0 tver=2（创建 1 + 解绑 bump），得到 %d/%d", d.BoundUserID, d.TokenVersion)
	}
}

// TestHeartbeatStateMachine 心跳：在线判定（3 分钟窗口）、电量校验、last_error 落
// 设备日志、config_version 携带（plan §8.2/§8.1；devices.md §7.2）。
func TestHeartbeatStateMachine(t *testing.T) {
	e := newTestEnv(t)
	d := e.seedDevice("SF-SCAN-001", 1)
	dev := DeviceContext{ID: int64(d.ID), Code: d.Code, Type: d.Type, WarehouseID: d.WarehouseID}
	ctx := context.Background()

	// 未心跳：离线。
	views, total, err := e.svc.ListDevices(ctx, DeviceFilter{AllWarehouses: true, Online: boolPtr(false), Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(views) != 1 || views[0].Online {
		t.Fatalf("初始应离线: total=%d views=%+v err=%v", total, views, err)
	}

	// 电量越界（DDL CHECK 0–100）。
	bad := 101
	_, err = e.svc.Heartbeat(ctx, dev, HeartbeatInput{BatteryLevel: &bad}, "10.0.0.8")
	assertCode(t, err, "DEVICE_BATTERY_INVALID")

	// 正常心跳：在线 + 电量/版本落库。
	battery := 66
	hb, err := e.svc.Heartbeat(ctx, dev, HeartbeatInput{BatteryLevel: &battery, AppVersion: "1.2.0"}, "10.0.0.8")
	if err != nil {
		t.Fatalf("心跳失败: %v", err)
	}
	if !hb.Online || hb.ServerTime.Time.Equal(e.now) == false {
		t.Fatalf("心跳响应异常: %+v", hb)
	}
	views, _, _ = e.svc.ListDevices(ctx, DeviceFilter{AllWarehouses: true, Online: boolPtr(true), Page: 1, PageSize: 20})
	if len(views) != 1 || !views[0].Online || views[0].BatteryLevel == nil || *views[0].BatteryLevel != 66 {
		t.Fatalf("心跳后应在线且电量 66: %+v", views)
	}
	// 越过 3 分钟窗口 → 离线（plan §8.2 冻结窗口）。
	e.advance(3 * time.Minute)
	views, _, _ = e.svc.ListDevices(ctx, DeviceFilter{AllWarehouses: true, Online: boolPtr(true), Page: 1, PageSize: 20})
	if len(views) != 0 {
		t.Fatalf("3 分钟无心跳应判定离线: %+v", views)
	}
	// last_error → device_logs ERROR 行（devices.md §7.2 最近错误）。
	if _, err := e.svc.Heartbeat(ctx, dev, HeartbeatInput{LastError: "scanner init failed"}, ""); err != nil {
		t.Fatalf("心跳失败: %v", err)
	}
	logs, total, err := e.svc.ListDeviceLogs(ctx, DeviceLogFilter{DeviceID: dev.ID, Level: LogLevelError, Page: 1, PageSize: 20}, scopeAll())
	if err != nil || total != 1 || logs[0].Message != "scanner init failed" {
		t.Fatalf("last_error 应落 ERROR 设备日志: total=%d logs=%+v err=%v", total, logs, err)
	}
	// config_version：配置下发后心跳响应携带（plan §8.2）。
	cfg := map[string]any{"sound": true, "scan_timeout_seconds": float64(30)}
	raw, _ := json.Marshal(cfg)
	v, err := e.svc.PutDeviceConfig(ctx, actorAdmin(), dev.ID, raw, scopeAll())
	if err != nil || v != 1 {
		t.Fatalf("配置下发失败: v=%d err=%v", v, err)
	}
	hb2, err := e.svc.Heartbeat(ctx, dev, HeartbeatInput{}, "")
	if err != nil || hb2.ConfigVersion != 1 {
		t.Fatalf("心跳应携带 config_version=1: %+v err=%v", hb2, err)
	}
	// 设备令牌撤销后心跳被拒（token_version 不匹配——AuthenticateDevice 路径）。
	e.repo.BumpTokenVersion(nil, dev.ID)
	stale := d
	staleTok, _, _ := e.svc.token.Issue(stale)
	_, err = e.svc.AuthenticateDevice(ctx, staleTok)
	assertCode(t, err, "DEVICE_TOKEN_INVALID")
}

// TestPutDeviceConfigWhitelist 配置白名单校验（devices.md §7.3 冻结下发项；
// api.md §4 键/值类型完整校验）。
func TestPutDeviceConfigWhitelist(t *testing.T) {
	e := newTestEnv(t)
	d := e.seedDevice("SF-SCAN-001", 1)
	ctx := context.Background()

	// 未知键拒绝。
	raw, _ := json.Marshal(map[string]any{"hacker_key": true})
	_, err := e.svc.PutDeviceConfig(ctx, actorAdmin(), int64(d.ID), raw, scopeAll())
	assertCode(t, err, "DEVICE_CONFIG_INVALID")
	// 类型错误拒绝（bool 键传 string）。
	raw, _ = json.Marshal(map[string]any{"sound": "yes"})
	_, err = e.svc.PutDeviceConfig(ctx, actorAdmin(), int64(d.ID), raw, scopeAll())
	assertCode(t, err, "DEVICE_CONFIG_INVALID")
	// 负数拒绝。
	raw, _ = json.Marshal(map[string]any{"scan_timeout_seconds": float64(-1)})
	_, err = e.svc.PutDeviceConfig(ctx, actorAdmin(), int64(d.ID), raw, scopeAll())
	assertCode(t, err, "DEVICE_CONFIG_INVALID")
	// 合法下发 → version 单调递增（1 → 2）。
	raw, _ = json.Marshal(map[string]any{"sound": true, "auto_lock_minutes": float64(5)})
	v, err := e.svc.PutDeviceConfig(ctx, actorAdmin(), int64(d.ID), raw, scopeAll())
	if err != nil || v != 1 {
		t.Fatalf("首次下发失败: v=%d err=%v", v, err)
	}
	raw, _ = json.Marshal(map[string]any{"sound": false, "auto_lock_minutes": float64(0)})
	v, err = e.svc.PutDeviceConfig(ctx, actorAdmin(), int64(d.ID), raw, scopeAll())
	if err != nil || v != 2 {
		t.Fatalf("二次下发版本期望 2: v=%d err=%v", v, err)
	}
	// 设备端拉取一致。
	res, err := e.svc.GetSelfConfig(ctx, DeviceContext{ID: int64(d.ID), Code: d.Code})
	if err != nil || res.ConfigVersion != 2 {
		t.Fatalf("设备端配置拉取异常: %+v err=%v", res, err)
	}
	var got map[string]any
	_ = json.Unmarshal(res.Config, &got)
	if got["sound"] != false || got["auto_lock_minutes"] != float64(0) {
		t.Fatalf("配置内容不符: %+v", got)
	}
	// 审计。
	found := false
	for _, en := range e.spy.all() {
		if en.Action == "config" {
			found = true
		}
	}
	if !found {
		t.Fatal("配置下发缺少审计（plan §13.8）")
	}
}

// TestLatestAppVersion App 版本查询（devices.md §7.4；plan §15：无已发布行 → 404 字面）。
func TestLatestAppVersion(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	_, err := e.svc.LatestAppVersion(ctx, PlatformAndroid)
	assertCode(t, err, "DEVICE_APP_VERSION_NOT_FOUND")

	e.repo.seedAppVersion(&AppVersion{Platform: PlatformAndroid, VersionCode: 3, VersionName: "1.3.0", Status: AppVersionPublished, PublishedAt: database.JSONTime{Time: e.now}})
	e.repo.seedAppVersion(&AppVersion{Platform: PlatformAndroid, VersionCode: 5, VersionName: "1.5.0", Status: AppVersionDraft}) // 未发布不参与
	e.repo.seedAppVersion(&AppVersion{Platform: PlatformAndroid, VersionCode: 4, VersionName: "1.4.0", Status: AppVersionPublished})
	e.repo.seedAppVersion(&AppVersion{Platform: "ios", VersionCode: 9, VersionName: "0.9.0", Status: AppVersionPublished}) // 平台隔离

	v, err := e.svc.LatestAppVersion(ctx, PlatformAndroid)
	if err != nil || v.VersionCode != 4 {
		t.Fatalf("期望 android 最新已发布 4，得到 %+v err=%v", v, err)
	}
	_, err = e.svc.LatestAppVersion(ctx, "ios")
	assertCode(t, err, "DEVICE_PLATFORM_INVALID")
}

// TestAuthenticateDeviceChecks 认证行复查矩阵：停用/未激活/token_version 不匹配/编码不符
// 一律 DEVICE_TOKEN_INVALID（plan §8.2 DeviceAuthRequired 语义）。
func TestAuthenticateDeviceChecks(t *testing.T) {
	e := newTestEnv(t)
	d := e.seedDevice("SF-SCAN-001", 1)
	ctx := context.Background()
	issue := func() string {
		tok, _, err := e.svc.token.Issue(d)
		if err != nil {
			t.Fatalf("签发失败: %v", err)
		}
		return tok
	}

	// 未激活设备。
	pending := &Device{Code: "SF-SCAN-002", Name: "x", Type: TypePDA, Status: StatusEnabled, ActivationStatus: ActivationPending, TokenVersion: 1}
	_ = e.repo.InsertDevice(nil, pending)
	pendingDev := e.mustDevice("SF-SCAN-002")
	tokPending, _, _ := e.svc.token.Issue(pendingDev)
	_, err := e.svc.AuthenticateDevice(ctx, tokPending)
	assertCode(t, err, "DEVICE_TOKEN_INVALID")

	// 停用。
	e.repo.UpdateDeviceStatus(nil, int64(d.ID), StatusEnabled, StatusDisabled, 0)
	_, err = e.svc.AuthenticateDevice(ctx, issue())
	assertCode(t, err, "DEVICE_TOKEN_INVALID")

	// 恢复启用但 token_version 漂移。
	e.repo.UpdateDeviceStatus(nil, int64(d.ID), StatusDisabled, StatusEnabled, 0)
	e.repo.BumpTokenVersion(nil, int64(d.ID))
	_, err = e.svc.AuthenticateDevice(ctx, issue())
	assertCode(t, err, "DEVICE_TOKEN_INVALID")

	// 正常路径。
	fresh := e.mustDevice("SF-SCAN-001")
	tok, _, _ := e.svc.token.Issue(fresh)
	dev, err := e.svc.AuthenticateDevice(ctx, tok)
	if err != nil || dev.Code != "SF-SCAN-001" || dev.WarehouseID != 1 {
		t.Fatalf("正常认证失败: %+v err=%v", dev, err)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestDeviceScopeVisibility 数据权限（permission.md §4）：仓库受限用户对跨仓设备的
// 单点端点（详情/激活/停用/配置）一律按不存在处理，防跨仓 ID 枚举直改他仓设备。
func TestDeviceScopeVisibility(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	payload, err := e.svc.CreateDevice(ctx, actorAdmin(), createInput("SF-SCAN-WH1"), "http://api")
	if err != nil {
		t.Fatalf("创建设备失败: %v", err)
	}
	devID := int64(payload.DeviceID)

	// 仓库受限（仅 2 号仓）用户：单点读/写全部 404，且不产生任何状态变化。
	restricted := WarehouseScope{IDs: []int64{2}}
	if _, err := e.svc.GetDevice(ctx, devID, restricted); err == nil {
		t.Fatalf("跨仓详情应被拒绝，得到 nil")
	} else if got := errorCodeOf(err); got != "DEVICE_NOT_FOUND" {
		t.Fatalf("跨仓详情期望 DEVICE_NOT_FOUND，得到 %s", got)
	}
	if _, err := e.svc.GetActivation(ctx, devID, "http://api", restricted); err == nil {
		t.Fatalf("跨仓激活查询应被拒绝，得到 nil")
	}
	if err := e.svc.DisableDevice(ctx, actorAdmin(), devID, restricted); err == nil {
		t.Fatalf("跨仓停用应被拒绝，得到 nil")
	}
	if _, err := e.svc.PutDeviceConfig(ctx, actorAdmin(), devID, json.RawMessage(`{"sound":true}`), restricted); err == nil {
		t.Fatalf("跨仓配置下发应被拒绝，得到 nil")
	}
	if err := e.svc.BindDevice(ctx, actorAdmin(), devID, 7, restricted); err == nil {
		t.Fatalf("跨仓绑定应被拒绝，得到 nil")
	}
	if _, total, err := e.svc.ListDeviceLogs(ctx, DeviceLogFilter{DeviceID: devID, Page: 1, PageSize: 10}, restricted); err == nil {
		t.Fatalf("跨仓设备日志应被拒绝，得到 nil（total=%d）", total)
	}

	// 同仓用户（1 号仓）可见可操作；未绑定仓库（0）设备对受限用户不可见。
	if _, err := e.svc.GetDevice(ctx, devID, WarehouseScope{IDs: []int64{1, 2}}); err != nil {
		t.Fatalf("同仓详情不应被拒绝: %v", err)
	}
	if err := e.svc.DisableDevice(ctx, actorAdmin(), devID, WarehouseScope{IDs: []int64{1}}); err != nil {
		t.Fatalf("同仓停用不应被拒绝: %v", err)
	}
}
