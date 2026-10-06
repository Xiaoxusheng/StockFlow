package devices

// HTTP 层数据驱动表测试（缺口接口：listDevices/getDevice/getActivation/listDeviceLogs/
// selfConfig/selfUploadLogs + 全部 17 条端点的 HTTP 面）。模式沿用
// internal/returns/http_test.go：
//   - 引擎经 mountTestRoutes 逐条镜像 RegisterRoutes（handler.go:184-211）的路由与
//     auth.RequirePermission 权限中间件，handler 直接装配 fakes_test.go 内存替身背书的
//     Service；路由面一致性由 TestHTTPRouteSurfaceMirrorsRegisterRoutes 守卫（镜像漂移
//     即测试失败）；
//   - 管理端认证以中间件注入 auth.UserContext（键为 auth 中间件私有键 "sf_auth_user"，
//     internal/auth/middleware.go:26；漂移则 CurrentUser 落空 → 全部 401 响亮失败）；
//     超管直通 RequirePermission（permission.md §1）。数据权限用例以"仅注入认证上下文、
//     不挂权限中间件"的引擎验证（非超管权限判定需 auth 全局装配，单测无 Redis 不可达
//     ——returns 同豁免；数据范围过滤走 auth.WarehouseScope 纯读取路径）；
//   - 设备端认证走真 DeviceAuthRequired，令牌经完整 HTTP 流程获取（创建 → 提取二维码
//     token → activate），同时覆盖 activate 成功路径；
//   - resolve 双轨：设备轨走真 resolveAuth（tokenIssuerProbe → AuthenticateDevice，
//     无 auth 装配依赖）；用户轨 AuthRequired 需 auth 全局装配（Redis 会话）单测不可达，
//     以认证注入 + RequirePermission 替身覆盖 handler 用户归因分支（Service 层双轨归因
//     由 TestResolveScanLogAttribution 覆盖，真中间件拒绝路径由 routes_test.go
//     TestRegisterRoutesMounting 覆盖）。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
)

// ---- HTTP 基建 ----

// httpUserCtxKey auth 中间件的 gin 用户上下文键（internal/auth/middleware.go:26，
// 未导出——同 module 测试以同值注入；漂移时 CurrentUser 落空 → 全部 401 响亮失败）。
const httpUserCtxKey = "sf_auth_user"

func httpUserInject(uc auth.UserContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(httpUserCtxKey, uc)
		c.Next()
	}
}

// superUser 超级管理员（RequirePermission 直通，permission.md §1）。
func superUser() auth.UserContext {
	return auth.UserContext{UserID: 1, Username: "管理员", IsSuper: true}
}

// whScopeUser 指定仓库数据权限用户（permission.md §4 数据范围快照）。
func whScopeUser(ids ...int64) auth.UserContext {
	return auth.UserContext{UserID: 2, Username: "李四", DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids}
}

// httpEngineOpts 引擎装配变体。
type httpEngineOpts struct {
	uc               *auth.UserContext // 非 nil：引擎级注入认证上下文
	withPerm         bool              // 挂 auth.RequirePermission（超管直通；非超管用例须 false）
	resolveUserTrack bool              // resolve 挂测试用户轨替身（否则真双轨中间件）
}

// newDeviceHTTPEnv 构建内存环境 + 镜像路由引擎。
func newDeviceHTTPEnv(t *testing.T, opts []Option, eo httpEngineOpts) (*testEnv, *gin.Engine) {
	t.Helper()
	e := newTestEnv(t, opts...)
	return e, mountTestRoutes(t, e, eo)
}

// newScopeEngineFor 数据权限用例引擎（同环境追加挂载：仅注入指定仓库范围用户上下文，
// 不挂权限中间件——见文件头 withPerm 豁免说明）。
func newScopeEngineFor(t *testing.T, e *testEnv, ids ...int64) *gin.Engine {
	t.Helper()
	uc := whScopeUser(ids...)
	return mountTestRoutes(t, e, httpEngineOpts{uc: &uc, withPerm: false})
}

// mountTestRoutes 逐条镜像 RegisterRoutes（handler.go:184-211）的路由表与权限点，
// handler 装配替身 Service；一致性由 TestHTTPRouteSurfaceMirrorsRegisterRoutes 守卫。
func mountTestRoutes(t *testing.T, e *testEnv, eo httpEngineOpts) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	if eo.uc != nil {
		api.Use(httpUserInject(*eo.uc))
	}
	h := &handler{svc: e.svc}
	perm := func(p string, hd gin.HandlerFunc) []gin.HandlerFunc {
		if !eo.withPerm {
			return []gin.HandlerFunc{hd}
		}
		return []gin.HandlerFunc{auth.RequirePermission(p), hd}
	}

	// 管理端 /api/devices（protected 组，用户 JWT）
	dv := api.Group("/devices")
	dv.GET("", perm(PermDeviceList, h.listDevices)...)
	dv.POST("", perm(PermDeviceCreate, h.createDevice)...)
	dv.GET("/:id", perm(PermDeviceRead, h.getDevice)...)
	dv.GET("/:id/activation", perm(PermDeviceRead, h.getActivation)...)
	dv.PUT("/:id/activation", perm(PermDeviceUpdate, h.regenActivation)...)
	dv.POST("/:id/bind", perm(PermDeviceUpdate, h.bindDevice)...)
	dv.POST("/:id/unbind", perm(PermDeviceUpdate, h.unbindDevice)...)
	dv.POST("/:id/disable", perm(PermDeviceStatus, h.disableDevice)...)
	dv.PUT("/:id/config", perm(PermDeviceUpdate, h.putConfig)...)
	dv.GET("/:id/logs", perm(PermDeviceRead, h.listDeviceLogs)...)
	api.GET("/scanner/logs", perm(PermScanLogList, h.listScanLogs)...)

	// 设备端（真实装配为不含 AuthRequired 的 /api 根组——WithDeviceAPI 同形）
	devAuth := e.svc.DeviceAuthRequired()
	api.POST("/devices/activate", h.activate)
	api.POST("/devices/heartbeat", devAuth, h.heartbeat)
	api.GET("/devices/self/config", devAuth, h.selfConfig)
	api.POST("/devices/self/logs", devAuth, h.selfUploadLogs)
	api.GET("/devices/app/versions/latest", devAuth, h.latestAppVersion)

	// 统一解析（双轨：设备令牌或用户链）
	if eo.resolveUserTrack {
		api.POST("/scanner/resolve", auth.RequirePermission(PermScannerResolveList), h.resolve)
	} else {
		api.POST("/scanner/resolve", e.svc.resolveAuth(), h.resolve)
	}
	return r
}

// TestHTTPRouteSurfaceMirrorsRegisterRoutes 镜像路由面守卫：镜像表与真实 RegisterRoutes
// 的 devices/scanner 路由 (方法,路径) 全集双向一致（镜像漂移即失败）。
func TestHTTPRouteSurfaceMirrorsRegisterRoutes(t *testing.T) {
	got := httpRouteSet(newMountedEngine(t))
	e := newTestEnv(t)
	want := httpRouteSet(mountTestRoutes(t, e, httpEngineOpts{}))
	if len(got) != 17 {
		t.Fatalf("真实引擎 devices/scanner 路由期望 17 条，得到 %d: %v", len(got), got)
	}
	for p := range want {
		if !got[p] {
			t.Fatalf("真实 RegisterRoutes 缺镜像路由: %s", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Fatalf("镜像表缺真实路由: %s", p)
		}
	}
}

func httpRouteSet(r *gin.Engine) map[string]bool {
	m := map[string]bool{}
	for _, ri := range r.Routes() {
		if strings.HasPrefix(ri.Path, "/api/devices") || strings.HasPrefix(ri.Path, "/api/scanner") {
			m[ri.Method+" "+ri.Path] = true
		}
	}
	return m
}

// ---- 请求与信封助手 ----

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// doRaw 原始请求体（坏 JSON 绑定失败用例）。
func doRaw(t *testing.T, r *gin.Engine, method, path, rawBody string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type httpEnvelope struct {
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Details json.RawMessage `json:"details"`
}

func decodeEnv(t *testing.T, w *httptest.ResponseRecorder) httpEnvelope {
	t.Helper()
	var e httpEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应非统一信封 JSON（api.md §2）: %v（%s）", err, w.Body.String())
	}
	return e
}

// wantOK 断言 200 + code=0（数字）并返回 data 对象。
func wantOK(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", w.Code, w.Body.String())
	}
	e := decodeEnv(t, w)
	if n, ok := e.Code.(float64); !ok || n != 0 {
		t.Fatalf("期望 code=0（数字），得到 %v: %s", e.Code, w.Body.String())
	}
	if e.Message != "ok" {
		t.Fatalf("成功信封 message 期望 ok，得到 %q", e.Message)
	}
	var m map[string]any
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &m); err != nil {
			t.Fatalf("data 非对象: %v（%s）", err, string(e.Data))
		}
	}
	return m
}

// wantErr 断言 HTTP 状态与错误码字符串，返回 details（可 nil）。
func wantErr(t *testing.T, w *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("期望 %d，得到 %d: %s", status, w.Code, w.Body.String())
	}
	e := decodeEnv(t, w)
	if s, ok := e.Code.(string); !ok || s != code {
		t.Fatalf("期望错误码 %s，得到 %v: %s", code, e.Code, w.Body.String())
	}
	if len(e.Details) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(e.Details, &m)
	return m
}

func itemsOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	arr, ok := m["items"].([]any)
	if !ok {
		// 空页 items=null（Go slice 零值经 OKPage 序列化）——视为空列表。
		if raw, exists := m["items"]; exists && raw == nil {
			return nil
		}
		t.Fatalf("items 缺失或非数组: %v", m)
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		out = append(out, it.(map[string]any))
	}
	return out
}

func num(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("期望数字字段，得到 %T（%v）", v, v)
	}
	return f
}

func str(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("期望字符串字段，得到 %T（%v）", v, v)
	}
	return s
}

// bearerHeader 设备令牌请求头。
func bearerHeader(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

// ---- 种子助手 ----

// insertScanLog 直接落一条扫码审计（列表/统计种子）。
func insertScanLog(t *testing.T, e *testEnv, l *ScanLog) {
	t.Helper()
	if err := e.repo.InsertScanLog(nil, l); err != nil {
		t.Fatalf("种子扫码日志失败: %v", err)
	}
}

// activateViaQR 从创建响应的 qr_content 提取一次性 token（plan §8.2 三字段协议）。
func activateViaQR(t *testing.T, qr string) (deviceCode, token string) {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(qr), &m); err != nil {
		t.Fatalf("二维码内容非法: %v（%q）", err, qr)
	}
	if m["token"] == "" || m["device_code"] == "" || m["server_url"] == "" {
		t.Fatalf("二维码内容缺字段: %q", qr)
	}
	return m["device_code"], m["token"]
}

// createAndActivateViaHTTP 走完整 HTTP 流程创建并激活设备，返回 (设备 ID, 设备令牌)。
func createAndActivateViaHTTP(t *testing.T, r *gin.Engine, code string) (int64, string) {
	t.Helper()
	w := doJSON(t, r, http.MethodPost, "/api/devices", map[string]any{
		"code": code, "name": "设备-" + code, "type": TypePDA, "warehouse_id": 1,
	}, nil)
	m := wantOK(t, w)
	dc, token := activateViaQR(t, str(t, m["qr_content"]))
	w = doJSON(t, r, http.MethodPost, "/api/devices/activate", map[string]any{
		"device_code": dc, "token": token,
		"brand": "Zebra", "model": "TC27", "os": "Android 13", "app_version": "1.0.0",
	}, nil)
	res := wantOK(t, w)
	id, err := strconv.ParseInt(str(t, res["device_id"]), 10, 64) // database.ID → 字符串
	if err != nil {
		t.Fatalf("device_id 非整数字符串: %v", err)
	}
	tok := str(t, res["device_token"])
	if tok == "" {
		t.Fatalf("激活响应缺 device_token: %v", res)
	}
	return id, tok
}

// ---- GET /api/devices（缺口① listDevices）----

// TestHTTPListDevices 列表：筛选矩阵（type/status/warehouse_id/keyword/online）、
// 分页信封（page/pageSize/total/items）、非法参数 400、仓库范围数据权限（permission.md §4）。
func TestHTTPListDevices(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	a := e.seedDevice("SF-SCAN-A", 1)
	e.seedDevice("SF-SCAN-B", 1)
	// pc 型停用设备直接落行（seedDevice 固定 pda 型，不经 helper）。
	if err := e.repo.InsertDevice(nil, &Device{
		Code: "SF-PC-C", Name: "设备-SF-PC-C", Type: TypePC, WarehouseID: 1,
		Status: StatusDisabled, TokenVersion: 1, CreatedAt: database.JSONTime{Time: e.now},
	}); err != nil {
		t.Fatalf("种子 pc 设备失败: %v", err)
	}
	e.seedDevice("SF-SCAN-D", 2)
	e.seedDevice("SF-SCAN-E", 0) // 未绑定仓库设备

	allCodes := []string{"SF-SCAN-E", "SF-SCAN-D", "SF-PC-C", "SF-SCAN-B", "SF-SCAN-A"} // fake 按 ID 降序
	cases := []struct {
		name      string
		query     string
		wantTotal int
		wantCodes []string
	}{
		{"默认全部（含未绑定仓库设备）", "", 5, allCodes},
		{"type 筛选", "type=pda", 4, []string{"SF-SCAN-E", "SF-SCAN-D", "SF-SCAN-B", "SF-SCAN-A"}},
		{"status 筛选", "status=DISABLED", 1, []string{"SF-PC-C"}},
		{"仓库筛选", "warehouse_id=2", 1, []string{"SF-SCAN-D"}},
		{"keyword 命中编码", "keyword=PC", 1, []string{"SF-PC-C"}},
		{"keyword 无命中", "keyword=NOPE", 0, nil},
		{"分页第二页", "page=2&pageSize=2", 5, []string{"SF-PC-C", "SF-SCAN-B"}},
		{"online=false 全离线", "online=false", 5, allCodes},
		{"online=true 无在线", "online=true", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(t, r, http.MethodGet, "/api/devices?"+tc.query, nil, nil)
			m := wantOK(t, w)
			if int(num(t, m["total"])) != tc.wantTotal {
				t.Fatalf("total 期望 %d，得到 %v", tc.wantTotal, m["total"])
			}
			items := itemsOf(t, m)
			if len(items) != len(tc.wantCodes) {
				t.Fatalf("items 数期望 %d，得到 %d: %v", len(tc.wantCodes), len(items), items)
			}
			for i, want := range tc.wantCodes {
				if got := str(t, items[i]["code"]); got != want {
					t.Fatalf("items[%d].code 期望 %s，得到 %s", i, want, got)
				}
			}
			if _, ok := m["page"]; !ok {
				t.Fatalf("分页信封缺 page 字段: %v", m)
			}
		})
	}

	// online 派生位：心跳后 3 分钟窗口内在线（plan §8.2 冻结窗口）。
	hctx := context.Background()
	if _, err := e.svc.Heartbeat(hctx, DeviceContext{ID: int64(a.ID), Code: a.Code, Type: a.Type, WarehouseID: a.WarehouseID},
		HeartbeatInput{BatteryLevel: nil, AppVersion: "1.2.0"}, "10.0.0.8"); err != nil {
		t.Fatalf("心跳失败: %v", err)
	}
	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices?online=true", nil, nil))
	items := itemsOf(t, m)
	if len(items) != 1 || str(t, items[0]["code"]) != "SF-SCAN-A" || items[0]["online"] != true {
		t.Fatalf("心跳后 online=true 期望仅 SF-SCAN-A: %v", items)
	}
	e.advance(3 * time.Minute) // 越过窗口 → 离线
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices?online=true", nil, nil))
	if num(t, m["total"]) != 0 {
		t.Fatalf("3 分钟无心跳应判定离线: %v", m["total"])
	}

	// 非法参数（handler.go listDevices/ParsePage 校验）。
	badCases := []struct {
		name  string
		query string
		field string
	}{
		{"warehouse_id 非数字", "warehouse_id=abc", "warehouse_id"},
		{"warehouse_id 负数", "warehouse_id=-1", "warehouse_id"},
		{"page 越下界", "page=0", "page"},
		{"pageSize 越上界", "pageSize=101", "pageSize"},
	}
	for _, tc := range badCases {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			d := wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices?"+tc.query, nil, nil),
				http.StatusBadRequest, "COMMON_INVALID_PARAM")
			if d != nil && d["field"] != tc.field {
				t.Fatalf("details.field 期望 %s，得到 %v", tc.field, d["field"])
			}
		})
	}

	// 数据权限：仓库受限用户只見绑定仓设备（未绑定仓库设备 fail-closed 不可见）。
	w1 := newScopeEngineFor(t, e, 1)
	m = wantOK(t, doJSON(t, w1, http.MethodGet, "/api/devices", nil, nil))
	if num(t, m["total"]) != 3 {
		t.Fatalf("1 号仓受限用户期望 total=3（A/B/C，未绑定仓库的 E 不可见），得到 %v", m["total"])
	}
	w2 := newScopeEngineFor(t, e, 2)
	m = wantOK(t, doJSON(t, w2, http.MethodGet, "/api/devices", nil, nil))
	items = itemsOf(t, m)
	if num(t, m["total"]) != 1 || len(items) != 1 || str(t, items[0]["code"]) != "SF-SCAN-D" {
		t.Fatalf("2 号仓受限用户期望仅 SF-SCAN-D: total=%v items=%v", m["total"], items)
	}
}

// ---- GET /api/devices/:id（缺口② getDevice）----

// TestHTTPGetDevice 详情：基础信息/在线派生位/配置/扫码统计（plan §8.1）、路径参数
// 校验、404 与跨仓 fail-closed。
func TestHTTPGetDevice(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	d := e.seedDevice("SF-SCAN-001", 1)
	devID := int64(d.ID)

	// 配置 + 扫码统计种子（2 条今日 / 1 条昨日）。
	if _, err := e.svc.PutDeviceConfig(context.Background(), actorAdmin(), devID,
		json.RawMessage(`{"sound":true,"scan_timeout_seconds":30}`), scopeAll()); err != nil {
		t.Fatalf("配置下发失败: %v", err)
	}
	insertScanLog(t, e, &ScanLog{DeviceID: &devID, WarehouseID: 1, RawCode: "SKU-A", Success: true, CreatedAt: database.JSONTime{Time: e.now}})
	insertScanLog(t, e, &ScanLog{DeviceID: &devID, WarehouseID: 1, RawCode: "SKU-B", Success: false, ErrorCode: "UNKNOWN_BARCODE", CreatedAt: database.JSONTime{Time: e.now}})
	insertScanLog(t, e, &ScanLog{DeviceID: &devID, WarehouseID: 1, RawCode: "SKU-C", Success: true, CreatedAt: database.JSONTime{Time: e.now.Add(-24 * time.Hour)}})

	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+strconv.FormatInt(devID, 10), nil, nil))
	if str(t, m["code"]) != "SF-SCAN-001" || str(t, m["type"]) != TypePDA {
		t.Fatalf("基础信息不符: %v", m)
	}
	if m["online"] != false {
		t.Fatalf("无心跳设备应离线: %v", m["online"])
	}
	cfg, ok := m["config"].(map[string]any)
	if !ok || cfg["sound"] != true || num(t, cfg["scan_timeout_seconds"]) != 30 {
		t.Fatalf("config 不符: %v", m["config"])
	}
	if num(t, m["config_version"]) != 1 {
		t.Fatalf("config_version 期望 1，得到 %v", m["config_version"])
	}
	if num(t, m["scan_total"]) != 3 || num(t, m["scan_today"]) != 2 {
		t.Fatalf("扫码统计期望 total=3 today=2，得到 %v/%v", m["scan_total"], m["scan_today"])
	}

	// 路径参数非法（pathID：非正整数字符串）。
	for _, bad := range []string{"abc", "0", "-1"} {
		det := wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/"+bad, nil, nil),
			http.StatusBadRequest, "COMMON_INVALID_PARAM")
		if det == nil || det["field"] != "id" {
			t.Fatalf("非法 id %q details 期望 field=id: %v", bad, det)
		}
	}
	// 不存在 → 404 DEVICE_NOT_FOUND。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/999", nil, nil),
		http.StatusNotFound, "DEVICE_NOT_FOUND")
	// 跨仓用户 → 按不存在处理（防跨仓 ID 枚举，permission.md §4）。
	w2 := newScopeEngineFor(t, e, 2)
	wantErr(t, doJSON(t, w2, http.MethodGet, "/api/devices/"+strconv.FormatInt(devID, 10), nil, nil),
		http.StatusNotFound, "DEVICE_NOT_FOUND")
}

// ---- GET /api/devices/:id/activation（缺口③ getActivation）----

// TestHTTPGetActivation 激活状态查询：派生态 pending/expired/disabled（plan §12.4 条 4）、
// qr_content 不可还原（token 仅哈希存储——plan §8.2）、非法 id/404。
func TestHTTPGetActivation(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})

	// PENDING（HTTP 创建设备后查询）。
	w := doJSON(t, r, http.MethodPost, "/api/devices", map[string]any{
		"code": "SF-SCAN-001", "name": "设备-SF-SCAN-001", "type": TypePDA, "warehouse_id": 1,
	}, nil)
	created := wantOK(t, w)
	devID, _ := strconv.ParseInt(str(t, created["device_id"]), 10, 64)
	p := strconv.FormatInt(devID, 10)

	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/activation", nil, nil))
	if str(t, m["device_code"]) != "SF-SCAN-001" || str(t, m["activation_status"]) != ActivationPending {
		t.Fatalf("激活查询不符: %v", m)
	}
	if str(t, m["status"]) != "pending" {
		t.Fatalf("前端派生态期望 pending，得到 %v", m["status"])
	}
	if str(t, m["server_url"]) != "http://example.com" { // httptest 请求 Host 推导（handler.go requestBaseURL）
		t.Fatalf("server_url 期望请求推导值，得到 %v", m["server_url"])
	}
	if _, ok := m["expires_at"]; !ok {
		t.Fatal("PENDING 设备应携带激活码时效")
	}
	if _, ok := m["qr_content"]; ok {
		t.Fatal("查询端点不得回传 qr_content（token 仅哈希存储不可还原——plan §8.2）")
	}

	// expired 派生：时钟推进越过 15 分钟时效。
	e.advance(16 * time.Minute)
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/activation", nil, nil))
	if str(t, m["status"]) != "expired" {
		t.Fatalf("过期后派生态期望 expired，得到 %v", m["status"])
	}

	// disabled 派生：已激活设备停用后（activation_status 仍为冻结枚举 ACTIVATED）。
	d := e.seedDevice("SF-SCAN-002", 1)
	if err := e.svc.DisableDevice(context.Background(), actorAdmin(), int64(d.ID), scopeAll()); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+strconv.FormatInt(int64(d.ID), 10)+"/activation", nil, nil))
	if str(t, m["status"]) != "disabled" || str(t, m["activation_status"]) != ActivationActivated {
		t.Fatalf("停用设备激活查询期望 disabled/ACTIVATED，得到 %v/%v", m["status"], m["activation_status"])
	}

	// 非法 id 与 404。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/xyz/activation", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/999/activation", nil, nil),
		http.StatusNotFound, "DEVICE_NOT_FOUND")
}

// ---- GET /api/devices/:id/logs（缺口④ listDeviceLogs）----

// TestHTTPListDeviceLogs 设备日志分页：level 筛选、分页信封、404/非法 id/跨仓 fail-closed。
func TestHTTPListDeviceLogs(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	d := e.seedDevice("SF-SCAN-001", 1)
	devID := int64(d.ID)
	p := strconv.FormatInt(devID, 10)

	rows := []*DeviceLog{
		{DeviceID: devID, Level: LogLevelError, EventType: "scan_fail", Message: "解码失败-1", CreatedAt: database.JSONTime{Time: e.now}},
		{DeviceID: devID, Level: LogLevelError, EventType: "scan_fail", Message: "解码失败-2", CreatedAt: database.JSONTime{Time: e.now}},
		{DeviceID: devID, Level: LogLevelInfo, EventType: "boot", Message: "启动完成", CreatedAt: database.JSONTime{Time: e.now}},
	}
	if err := e.repo.InsertDeviceLogs(nil, rows); err != nil {
		t.Fatalf("种子设备日志失败: %v", err)
	}

	cases := []struct {
		name      string
		query     string
		wantTotal int
		wantLevel string
	}{
		{"全部", "", 3, ""},
		{"level=ERROR", "level=ERROR", 2, LogLevelError},
		{"level=WARN 无命中", "level=WARN", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/logs?"+tc.query, nil, nil))
			if num(t, m["total"]) != float64(tc.wantTotal) {
				t.Fatalf("total 期望 %d，得到 %v", tc.wantTotal, m["total"])
			}
			for _, it := range itemsOf(t, m) {
				if tc.wantLevel != "" && str(t, it["level"]) != tc.wantLevel {
					t.Fatalf("level 筛选失效: %v", it)
				}
			}
		})
	}
	// 分页：pageSize=2 第一页 2 条，total 仍为 3。
	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/logs?page=1&pageSize=2", nil, nil))
	if num(t, m["total"]) != 3 || len(itemsOf(t, m)) != 2 {
		t.Fatalf("分页期望 total=3 items=2: total=%v", m["total"])
	}

	// 非法 id / 404 / 跨仓 fail-closed。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/0/logs", nil, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/999/logs", nil, nil),
		http.StatusNotFound, "DEVICE_NOT_FOUND")
	w2 := newScopeEngineFor(t, e, 2)
	wantErr(t, doJSON(t, w2, http.MethodGet, "/api/devices/"+p+"/logs", nil, nil),
		http.StatusNotFound, "DEVICE_NOT_FOUND")
}

// ---- GET /api/devices/self/config（缺口⑤ selfConfig）+ PUT /api/devices/:id/config HTTP 面 ----

// TestHTTPSelfConfigLifecycle 设备端配置拉取生命周期：无令牌 401 → 激活后空配置 →
// 管理端下发 → 再拉取一致；含配置下发 HTTP 面校验失败分支。
func TestHTTPSelfConfigLifecycle(t *testing.T) {
	su := superUser()
	_, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})

	// 无令牌 → 401 DEVICE_TOKEN_INVALID（DeviceAuthRequired）。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, nil),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")

	devID, tok := createAndActivateViaHTTP(t, r, "SF-SCAN-001")
	p := strconv.FormatInt(devID, 10)

	// 激活后未配置：config=null、config_version=0（plan §8.2 激活响应同形）。
	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, bearerHeader(tok)))
	v, has := m["config"]
	if !has || v != nil {
		t.Fatalf("未配置设备 config 期望 null，得到 %v（exists=%v）", v, has)
	}
	if num(t, m["config_version"]) != 0 {
		t.Fatalf("未配置 config_version 期望 0，得到 %v", m["config_version"])
	}

	// 管理端下发成功 → version=1。
	w := doJSON(t, r, http.MethodPut, "/api/devices/"+p+"/config", map[string]any{
		"sound": true, "scan_timeout_seconds": 30,
	}, nil)
	m = wantOK(t, w)
	if num(t, m["version"]) != 1 {
		t.Fatalf("下发响应 version 期望 1，得到 %v", m["version"])
	}

	// 设备端拉取一致。
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, bearerHeader(tok)))
	cfg, ok := m["config"].(map[string]any)
	if !ok || cfg["sound"] != true || num(t, cfg["scan_timeout_seconds"]) != 30 {
		t.Fatalf("设备端拉取配置不符: %v", m["config"])
	}
	if num(t, m["config_version"]) != 1 {
		t.Fatalf("config_version 期望 1，得到 %v", m["config_version"])
	}

	// 下发非法载荷：未知键 / 值类型错误（devices.md §7.3 白名单）→ 400。
	det := wantErr(t, doJSON(t, r, http.MethodPut, "/api/devices/"+p+"/config",
		map[string]any{"hacker_key": true}, nil), http.StatusBadRequest, "DEVICE_CONFIG_INVALID")
	if det == nil || det["key"] != "hacker_key" {
		t.Fatalf("未知键 details 期望 key=hacker_key: %v", det)
	}
	wantErr(t, doJSON(t, r, http.MethodPut, "/api/devices/"+p+"/config",
		map[string]any{"sound": "yes"}, nil), http.StatusBadRequest, "DEVICE_CONFIG_INVALID")
	// 坏 JSON → 绑定失败 400。
	wantErr(t, doRaw(t, r, http.MethodPut, "/api/devices/"+p+"/config", "{bad", nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
}

// ---- POST /api/devices/self/logs（缺口⑥ selfUploadLogs）----

// TestHTTPSelfUploadLogs 设备日志批量上报：成功 count 与落库、occurred_at 两种格式与
// 缺省兜底、批次/级别/时间/context 校验 400、坏 JSON。
func TestHTTPSelfUploadLogs(t *testing.T) {
	su := superUser()
	_, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})

	// 无令牌 → 401。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/self/logs", map[string]any{"logs": []any{}}, nil),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")

	devID, tok := createAndActivateViaHTTP(t, r, "SF-SCAN-001")
	p := strconv.FormatInt(devID, 10)

	// 成功：2 条（时间戳两格式——YYYY-MM-DD HH:mm:ss 与 RFC3339）→ count=2。
	w := doJSON(t, r, http.MethodPost, "/api/devices/self/logs", map[string]any{
		"logs": []map[string]any{
			{"level": "ERROR", "event_type": "scan_fail", "message": "解码失败", "occurred_at": "2026-10-03 09:00:00"},
			{"level": "INFO", "event_type": "boot", "message": "启动完成", "occurred_at": "2026-10-03T09:05:00+08:00"},
		},
	}, bearerHeader(tok))
	m := wantOK(t, w)
	if num(t, m["count"]) != 2 {
		t.Fatalf("上报响应 count 期望 2，得到 %v", m["count"])
	}
	// 管理端日志查询落库一致。
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/logs", nil, nil))
	if num(t, m["total"]) != 2 {
		t.Fatalf("上报后设备日志 total 期望 2，得到 %v", m["total"])
	}

	// occurred_at 缺省 → 服务端时钟兜底（plan §8.2 设备端时钟可缺省）。
	m = wantOK(t, doJSON(t, r, http.MethodPost, "/api/devices/self/logs", map[string]any{
		"logs": []map[string]any{{"level": "WARN", "event_type": "battery_low", "message": "电量低"}},
	}, bearerHeader(tok)))
	if num(t, m["count"]) != 1 {
		t.Fatalf("缺省 occurred_at 上报期望 count=1，得到 %v", m["count"])
	}

	// 非法载荷表驱动（Service UploadDeviceLogs 校验——service_device.go:806）。
	bigCtx := `{"k":"` + strings.Repeat("a", 4100) + `"}`
	badCases := []struct {
		name string
		body map[string]any
	}{
		{"空批次", map[string]any{"logs": []any{}}},
		{"超 100 条", func() map[string]any {
			many := make([]map[string]any, 101)
			for i := range many {
				many[i] = map[string]any{"level": "INFO", "event_type": "x", "message": "y"}
			}
			return map[string]any{"logs": many}
		}()},
		{"非法 level", map[string]any{"logs": []map[string]any{{"level": "VERBOSE", "event_type": "x", "message": "y"}}}},
		{"非法 occurred_at", map[string]any{"logs": []map[string]any{{"level": "INFO", "event_type": "x", "message": "y", "occurred_at": "not-a-time"}}}},
		{"context 超 4096 字节", map[string]any{"logs": []map[string]any{{"level": "INFO", "event_type": "x", "message": "y", "context": json.RawMessage(bigCtx)}}}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/self/logs", tc.body, bearerHeader(tok)),
				http.StatusBadRequest, "DEVICE_LOG_BATCH_INVALID")
		})
	}
	// 坏 JSON → 绑定失败。
	wantErr(t, doRaw(t, r, http.MethodPost, "/api/devices/self/logs", `{"logs": [}`, bearerHeader(tok)),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	// 校验失败不落库：total 仍为 3。
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/"+p+"/logs", nil, nil))
	if num(t, m["total"]) != 3 {
		t.Fatalf("非法上报不得落库：total 期望 3，得到 %v", m["total"])
	}
}

// ---- POST /api/devices/activate + POST /api/devices/heartbeat（HTTP 面）----

// TestHTTPActivateAndHeartbeat 激活与心跳 HTTP 面：错码/重放 401（一次性语义）、
// 心跳成功与电量越界、停用后令牌失效（行复查）。
func TestHTTPActivateAndHeartbeat(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})

	w := doJSON(t, r, http.MethodPost, "/api/devices", map[string]any{
		"code": "SF-SCAN-001", "name": "设备-SF-SCAN-001", "type": TypePDA, "warehouse_id": 1,
	}, nil)
	created := wantOK(t, w)
	dc, token := activateViaQR(t, str(t, created["qr_content"]))

	// 错误 token → 401 DEVICE_ACTIVATION_INVALID（plan §8.2 三因统一防探测）。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/activate",
		map[string]any{"device_code": dc, "token": "wrong-token-wrong-token-wrong-token"}, nil),
		http.StatusUnauthorized, "DEVICE_ACTIVATION_INVALID")

	// 成功激活：device_token/expires_at/仓库/类型回显。
	w = doJSON(t, r, http.MethodPost, "/api/devices/activate", map[string]any{
		"device_code": dc, "token": token, "brand": "Zebra", "model": "TC27",
		"os": "Android 13", "app_version": "1.0.0",
	}, nil)
	m := wantOK(t, w)
	tok := str(t, m["device_token"])
	if tok == "" || str(t, m["device_code"]) != "SF-SCAN-001" ||
		str(t, m["device_type"]) != TypePDA || num(t, m["warehouse_id"]) != 1 {
		t.Fatalf("激活响应不符: %v", m)
	}
	if _, ok := m["expires_at"]; !ok {
		t.Fatal("激活响应缺 expires_at（plan §8.2 TTL 365d）")
	}

	// 重放同 token（一次性消费——哈希已清）→ 401。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/activate",
		map[string]any{"device_code": dc, "token": token}, nil),
		http.StatusUnauthorized, "DEVICE_ACTIVATION_INVALID")

	// 无令牌心跳 → 401。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/heartbeat", map[string]any{}, nil),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")

	// 心跳成功：online=true、server_time。
	m = wantOK(t, doJSON(t, r, http.MethodPost, "/api/devices/heartbeat",
		map[string]any{"battery_level": 66, "app_version": "1.0.1"}, bearerHeader(tok)))
	if m["online"] != true {
		t.Fatalf("心跳响应 online 期望 true: %v", m)
	}
	if _, ok := m["server_time"]; !ok {
		t.Fatal("心跳响应缺 server_time")
	}

	// 电量越界（DDL CHECK 0–100）→ 400。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/heartbeat",
		map[string]any{"battery_level": 101}, bearerHeader(tok)),
		http.StatusBadRequest, "DEVICE_BATTERY_INVALID")
	// app_version 超长（>32）→ 400。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/heartbeat",
		map[string]any{"app_version": strings.Repeat("v", 33)}, bearerHeader(tok)),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")

	// HTTP 停用后心跳 → 401（DeviceAuthRequired 行复查 token_version 不匹配）。
	d, err := e.repo.FindDeviceByCode(context.Background(), "SF-SCAN-001")
	if err != nil {
		t.Fatalf("查设备失败: %v", err)
	}
	w = doJSON(t, r, http.MethodPost, "/api/devices/"+strconv.FormatInt(int64(d.ID), 10)+"/disable", nil, nil)
	wantOK(t, w)
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/heartbeat",
		map[string]any{}, bearerHeader(tok)), http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")
}

// ---- 管理端动作 HTTP 面（bind/unbind/disable/重新生成激活码）----

// TestHTTPDeviceAdminActions 绑定/解绑/停用/重新生成激活码：成功回显、参数校验 400、
// 状态冲突 409、旧令牌撤销链路。
func TestHTTPDeviceAdminActions(t *testing.T) {
	su := superUser()
	_, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	devID, tok := createAndActivateViaHTTP(t, r, "SF-SCAN-001")
	p := strconv.FormatInt(devID, 10)

	// 绑定：成功回显 bound_user_id。
	m := wantOK(t, doJSON(t, r, http.MethodPost, "/api/devices/"+p+"/bind", map[string]any{"user_id": 7}, nil))
	if num(t, m["bound_user_id"]) != 7 {
		t.Fatalf("绑定回显期望 7，得到 %v", m["bound_user_id"])
	}
	// 绑定非法 user_id（Service BindDevice 校验——service_device.go:489）→ 400。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/"+p+"/bind", map[string]any{"user_id": 0}, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	// 坏 JSON → 400。
	wantErr(t, doRaw(t, r, http.MethodPost, "/api/devices/"+p+"/bind", `{`, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")
	// 路径 id 非法 → 400（pathID）。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/abc/bind", map[string]any{"user_id": 1}, nil),
		http.StatusBadRequest, "COMMON_INVALID_PARAM")

	// 解绑：bound_user_id=0，token_version+1 → 旧设备令牌失效（000013 撤销语义）。
	m = wantOK(t, doJSON(t, r, http.MethodPost, "/api/devices/"+p+"/unbind", nil, nil))
	if num(t, m["bound_user_id"]) != 0 {
		t.Fatalf("解绑回显期望 0，得到 %v", m["bound_user_id"])
	}
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, bearerHeader(tok)),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")

	// 重新生成激活码：新二维码返回（qr_content 回传）、回退 PENDING。
	w := doJSON(t, r, http.MethodPut, "/api/devices/"+p+"/activation", nil, nil)
	m = wantOK(t, w)
	if str(t, m["qr_content"]) == "" || str(t, m["activation_status"]) != ActivationPending {
		t.Fatalf("重新生成响应不符: %v", m)
	}
	dc, newToken := activateViaQR(t, str(t, m["qr_content"]))
	w = doJSON(t, r, http.MethodPost, "/api/devices/activate", map[string]any{
		"device_code": dc, "token": newToken, "app_version": "1.1.0",
	}, nil)
	m = wantOK(t, w)
	tok2 := str(t, m["device_token"])
	if tok2 == "" {
		t.Fatalf("新激活码激活失败: %v", m)
	}
	m = wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, bearerHeader(tok2)))
	if num(t, m["config_version"]) != 0 {
		t.Fatalf("新令牌拉取配置期望 version=0，得到 %v", m["config_version"])
	}

	// 停用：status=DISABLED 回显；重复停用 → 409 DEVICE_STATUS_CONFLICT。
	m = wantOK(t, doJSON(t, r, http.MethodPost, "/api/devices/"+p+"/disable", nil, nil))
	if str(t, m["status"]) != StatusDisabled {
		t.Fatalf("停用回显期望 DISABLED，得到 %v", m["status"])
	}
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/devices/"+p+"/disable", nil, nil),
		http.StatusConflict, "DEVICE_STATUS_CONFLICT")
	// 停用后设备端动作全部拒绝（行复查）。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/self/config", nil, bearerHeader(tok2)),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")
}

// ---- GET /api/scanner/logs（listScanLogs）----

// TestHTTPListScanLogs 扫码日志：筛选矩阵（device_id/user_id/warehouse_id/success/keyword）、
// 非法参数 400、仓库范围数据权限。
func TestHTTPListScanLogs(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	d := e.seedDevice("SF-SCAN-001", 1)
	devID := int64(d.ID)
	devCode := d.Code
	insertScanLog(t, e, &ScanLog{DeviceID: &devID, DeviceCode: &devCode, WarehouseID: 1, RawCode: "SKU-A", Success: true, CreatedAt: database.JSONTime{Time: e.now}})
	insertScanLog(t, e, &ScanLog{DeviceID: &devID, DeviceCode: &devCode, WarehouseID: 1, RawCode: "PO-20261001-000001", Success: false, ErrorCode: "ORDER_NOT_FOUND", CreatedAt: database.JSONTime{Time: e.now}})
	uid := int64(7)
	insertScanLog(t, e, &ScanLog{UserID: uid, Username: "李四", WarehouseID: 2, RawCode: "6901234567890", Success: true, CreatedAt: database.JSONTime{Time: e.now}})

	cases := []struct {
		name      string
		query     string
		wantTotal int
	}{
		{"全部", "", 3},
		{"success=true", "success=true", 2},
		{"success=false", "success=false", 1},
		{"device_id", "device_id=" + strconv.FormatInt(devID, 10), 2},
		{"user_id", "user_id=7", 1},
		{"warehouse_id", "warehouse_id=2", 1},
		{"keyword 命中 raw_code", "keyword=PO-2026", 1},
		{"keyword 命中 device_code", "keyword=SF-SCAN-001", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/scanner/logs?"+tc.query, nil, nil))
			if num(t, m["total"]) != float64(tc.wantTotal) {
				t.Fatalf("total 期望 %d，得到 %v", tc.wantTotal, m["total"])
			}
		})
	}

	// 非法参数（handler.go listScanLogs 逐一校验）。
	badCases := []struct {
		name  string
		query string
		field string
	}{
		{"device_id 非数字", "device_id=abc", "device_id"},
		{"device_id 零值", "device_id=0", "device_id"},
		{"user_id 非数字", "user_id=abc", "user_id"},
		{"warehouse_id 负数", "warehouse_id=-1", "warehouse_id"},
	}
	for _, tc := range badCases {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			det := wantErr(t, doJSON(t, r, http.MethodGet, "/api/scanner/logs?"+tc.query, nil, nil),
				http.StatusBadRequest, "COMMON_INVALID_PARAM")
			if det == nil || det["field"] != tc.field {
				t.Fatalf("details.field 期望 %s，得到 %v", tc.field, det)
			}
		})
	}

	// 数据权限：仓库受限用户只見本仓扫码日志（handler fail-closed 注入 + fake 过滤）。
	w1 := newScopeEngineFor(t, e, 1)
	m1 := wantOK(t, doJSON(t, w1, http.MethodGet, "/api/scanner/logs", nil, nil))
	if num(t, m1["total"]) != 2 {
		t.Fatalf("1 号仓受限用户期望 total=2，得到 %v", m1["total"])
	}
	// 空仓库集（SPECIFIED_WAREHOUSE 无绑定）→ fail-closed 不可见任何行。
	wNone := newScopeEngineFor(t, e)
	m2 := wantOK(t, doJSON(t, wNone, http.MethodGet, "/api/scanner/logs", nil, nil))
	if num(t, m2["total"]) != 0 {
		t.Fatalf("无绑定仓库用户期望 total=0，得到 %v", m2["total"])
	}
}

// ---- POST /api/scanner/resolve（双轨 HTTP 面）----

// TestHTTPResolveTracks resolve 双轨：设备轨成功与 scan_logs 设备归因、识别类错误码
// HTTP 映射（404/400）；用户轨归因（测试用户链替身——见文件头说明）。
func TestHTTPResolveTracks(t *testing.T) {
	su := superUser()
	opts := []Option{
		WithSKUBarcodes(&fakeSKUBarcodes{hits: map[string]Hit{
			"690123": {ID: 11, Code: "SKU001", Name: "测试 SKU", Status: "ENABLED"},
		}}),
		WithPurchaseDocs(&fakeDocs{found: map[string]Hit{
			"PO-20261001-000001": {ID: 5, Code: "PO-20261001-000001", Name: "采购单 PO", DocKind: "PO"},
		}}),
		// 其余匹配器空命中替身（生产装配禁 nil——RegisterRoutes fail-fast，plan §3.1 规则①）。
		WithBins(&fakeBins{byCode: map[string][]Hit{}}),
		WithSerials(&fakeSerials{hits: map[string]Hit{}}),
		WithBatches(&fakeBatches{byNo: map[string][]Hit{}}),
		WithSfqrSkus(&fakeSfqrSkus{hits: map[string]Hit{}}),
		WithSalesDocs(&fakeDocs{found: map[string]Hit{}}),
		WithStockopsDocs(&fakeDocs{found: map[string]Hit{}}),
		WithReturnsDocs(&fakeDocs{found: map[string]Hit{}}),
	}
	e, r := newDeviceHTTPEnv(t, opts, httpEngineOpts{uc: &su, withPerm: true})
	devID, tok := createAndActivateViaHTTP(t, r, "SF-SCAN-001")

	// 设备轨成功：type/id/code 回显，scan_logs 落设备归因（plan §8.3）。
	m := wantOK(t, doJSON(t, r, http.MethodPost, "/api/scanner/resolve",
		map[string]any{"code": "690123", "symbology": "CODE128", "page": "pda_stock"}, bearerHeader(tok)))
	if str(t, m["type"]) != "sku" || str(t, m["code"]) != "SKU001" || str(t, m["id"]) != "11" {
		t.Fatalf("resolve 响应不符: %v", m)
	}
	if len(e.repo.scanLogs) != 1 || e.repo.scanLogs[0].DeviceID == nil ||
		*e.repo.scanLogs[0].DeviceID != devID || !e.repo.scanLogs[0].Success {
		t.Fatalf("设备归因 scan_logs 不符: %+v", e.repo.scanLogs)
	}

	// 未知码 → 404 UNKNOWN_BARCODE；失败也落审计（devices.md §13.2）。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/scanner/resolve",
		map[string]any{"code": "mystery-code"}, bearerHeader(tok)), http.StatusNotFound, "UNKNOWN_BARCODE")
	if len(e.repo.scanLogs) != 2 || e.repo.scanLogs[1].Success ||
		e.repo.scanLogs[1].ErrorCode != "UNKNOWN_BARCODE" {
		t.Fatalf("失败审计落库不符: %+v", e.repo.scanLogs[1])
	}

	// code 非法：空 / 超 255（raw_code varchar(255)）→ 400。
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/scanner/resolve",
		map[string]any{"code": "  "}, bearerHeader(tok)), http.StatusBadRequest, "SCANNER_CODE_INVALID")
	wantErr(t, doJSON(t, r, http.MethodPost, "/api/scanner/resolve",
		map[string]any{"code": strings.Repeat("x", 256)}, bearerHeader(tok)),
		http.StatusBadRequest, "SCANNER_CODE_INVALID")

	// 用户轨（Web HID）：归因 user_id（resolveIdentity 用户分支 + scan_logs.user_id）。
	e2, r2 := newDeviceHTTPEnv(t, opts, httpEngineOpts{uc: &su, withPerm: true, resolveUserTrack: true})
	m = wantOK(t, doJSON(t, r2, http.MethodPost, "/api/scanner/resolve",
		map[string]any{"code": "PO-20261001-000001"}, nil))
	if str(t, m["type"]) != "doc" || str(t, m["doc_kind"]) != "PO" || str(t, m["id"]) != "5" {
		t.Fatalf("用户轨单据解析不符: %v", m)
	}
	if len(e2.repo.scanLogs) != 1 || e2.repo.scanLogs[0].UserID != 1 ||
		e2.repo.scanLogs[0].DeviceID != nil {
		t.Fatalf("用户归因 scan_logs 不符（device 列应 NULL）: %+v", e2.repo.scanLogs)
	}
}

// ---- GET /api/devices/app/versions/latest（HTTP 面）----

// TestHTTPLatestAppVersion 最新 App 版本：无令牌 401、无已发布行 404、成功回显、
// 平台值域 400（M3 仅 android）。
func TestHTTPLatestAppVersion(t *testing.T) {
	su := superUser()
	e, r := newDeviceHTTPEnv(t, nil, httpEngineOpts{uc: &su, withPerm: true})
	_, tok := createAndActivateViaHTTP(t, r, "SF-SCAN-001")

	// 无已发布版本 → 404（plan §15 字面冻结）。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/app/versions/latest?platform=android", nil, bearerHeader(tok)),
		http.StatusNotFound, "DEVICE_APP_VERSION_NOT_FOUND")

	e.repo.seedAppVersion(&AppVersion{Platform: PlatformAndroid, VersionCode: 3, VersionName: "1.3.0", Status: AppVersionPublished, PublishedAt: database.JSONTime{Time: e.now}})
	e.repo.seedAppVersion(&AppVersion{Platform: PlatformAndroid, VersionCode: 5, VersionName: "1.5.0", Status: AppVersionDraft})

	m := wantOK(t, doJSON(t, r, http.MethodGet, "/api/devices/app/versions/latest?platform=android", nil, bearerHeader(tok)))
	if num(t, m["version_code"]) != 3 || str(t, m["version_name"]) != "1.3.0" {
		t.Fatalf("最新版本期望已发布 1.3.0（DRAFT 不参与），得到 %v", m)
	}

	// 平台非法（DDL CHECK 仅 android）→ 400。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/app/versions/latest?platform=ios", nil, bearerHeader(tok)),
		http.StatusBadRequest, "DEVICE_PLATFORM_INVALID")
	// 无令牌 → 401。
	wantErr(t, doJSON(t, r, http.MethodGet, "/api/devices/app/versions/latest?platform=android", nil, nil),
		http.StatusUnauthorized, "DEVICE_TOKEN_INVALID")
}
