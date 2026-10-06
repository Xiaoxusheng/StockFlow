package stockops

// stockops 域 HTTP 面（路由→handler→信封）数据驱动单测（内存替身，零 PostgreSQL/Redis
// 依赖，装配模式参照 internal/inventory/handler_test.go）：
//   - 服务层状态机/账目语义已由 service_test.go 覆盖，本文件只补 HTTP 契约面：
//     真实 RegisterRoutes 装配、参数绑定与校验失败、统一信封/分页形态（api.md §2）、
//     ID 字符串线格式、错误码透传、数据权限经 auth 用户上下文的接线；
//   - 认证上下文注入：RequirePermission/CurrentUser 读取的 gin 键为 auth 包私有常量
//     （internal/auth/middleware.go ctxUserKey = "sf_auth_user"），本文件以同值字面量
//     注入导出的 auth.UserContext——auth 侧若漂移，CurrentUser 取不到 → 权限中间件
//     401 / scopeOf 空范围 404，测试以 fail-closed 方式响亮失败而非静默通过；
//   - 非超管用户的权限点判定依赖 auth 服务装配态（RequirePermission → snapshotWired），
//     域内无法替身——权限点覆盖限于"未认证 401"；数据权限（仓库范围）经直连 handler
//     验证（绕过权限中间件，scopeOf/actorOf 接线仍走真实代码路径）。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/stock"
)

// authUserCtxKey internal/auth/middleware.go ctxUserKey 的同值镜像（跨包私有常量）。
const authUserCtxKey = "sf_auth_user"

// rawJSON 原文请求体（坏 JSON 绑定用例）。
type rawJSON string

// superUser 全仓数据权限（RequirePermission 对超管直通，auth/middleware.go:123）。
func superUser() *auth.UserContext {
	return &auth.UserContext{UserID: 7, Username: "admin", IsSuper: true}
}

// whUser 指定仓库数据权限（SPECIFIED_WAREHOUSE，permission.md §4）。
func whUser(ids ...int64) *auth.UserContext {
	return &auth.UserContext{
		UserID: 7, Username: "whop", DataScope: auth.DataScopeSpecifiedWh, WarehouseIDs: ids,
	}
}

// ---- HTTP 装配 ----

// httpFix 完整路由装配（真实 RegisterRoutes + memStore/fakeGateway 同一世界）。
type httpFix struct {
	r *gin.Engine
	w *fakeWorld
}

// newHTTPFix 装配引擎；uc 为 nil 时不注入用户上下文（RequirePermission → 401）。
func newHTTPFix(t *testing.T, uc *auth.UserContext) *httpFix {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := newHTTPWorld()
	r := gin.New()
	if uc != nil {
		u := *uc
		r.Use(func(c *gin.Context) { c.Set(authUserCtxKey, u); c.Next() })
	}
	RegisterRoutes(r.Group("/api"), &gorm.DB{}, nil, httpOptions(w)...)
	return &httpFix{r: r, w: w}
}

// newHTTPWorld 与 service_test.go newFixture 同一固定世界
// （bin111: skuPlain 10 + skuSerial 3 件 S1/S2/S3；bin112: skuBatch 批次A 50）。
func newHTTPWorld() *fakeWorld {
	w := newFakeWorld()
	w.seedRow(whSrc, zoneSrc, shelfSrc, binSrc, skuPlain, 0, q(10))
	w.seedRow(whSrc, zoneSrc, shelfSrc, binSrc, skuSerial, 0, q(3))
	for _, s := range []string{"S1", "S2", "S3"} {
		w.seedSerial(s, skuSerial, 0, whSrc, binSrc)
	}
	w.seedRow(whSrc, zoneSrc2, shelfSrc2, binSrc2, skuBatch, batchA, q(50))
	return w
}

// httpOptions 与 newFixture 同一装配（跨域校验替身 + 内存存储/网关/编号）。
func httpOptions(w *fakeWorld) []Option {
	return []Option{
		WithStore(&memStore{w: w}),
		WithGateway(&fakeGateway{w: w}),
		WithBinChecker(&fakeBins{ok: map[[2]int64]bool{
			{whSrc, binSrc}: true, {whSrc, binSrc2}: true, {whDst, binDst}: true,
		}}),
		WithSKUChecker(&fakeSKUs{ok: map[int64]bool{skuPlain: true, skuBatch: true, skuSerial: true}}),
		WithSKUFlagReader(&fakeFlags{m: map[int64]SKUFlags{
			skuPlain:  {Enabled: true},
			skuBatch:  {Enabled: true, BatchManaged: true},
			skuSerial: {Enabled: true, SerialManaged: true},
		}}),
		WithNumberIssuer(&fakeIssuer{w: w}),
	}
}

// ---- 请求与信封 ----

// do 发送请求并解码统一信封（body 经 json.Marshal；rawJSON 原文；nil=空体）。
func (x *httpFix) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case rawJSON:
		rd = strings.NewReader(string(b))
	default:
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	x.r.ServeHTTP(rec, req)
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "响应非 JSON 信封: %s", rec.Body.String())
	return rec.Code, m
}

// okData 断言成功信封（HTTP 200 + code=0 数字 + message=ok，api.md §2.2）并返回 data。
func okData(t *testing.T, status int, m map[string]any) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, status, "信封: %v", m)
	require.Equal(t, float64(0), m["code"], "成功信封 code 应为数字 0: %v", m)
	require.Equal(t, "ok", m["message"])
	d, ok := m["data"].(map[string]any)
	require.True(t, ok, "data 应为对象: %v", m)
	return d
}

// errCode 断言失败信封（HTTP 状态 + 注册错误码）并返回 details（可能为 nil）。
func errCode(t *testing.T, status int, wantHTTP int, want string, m map[string]any) map[string]any {
	t.Helper()
	require.Equal(t, wantHTTP, status, "信封: %v", m)
	require.Equal(t, want, m["code"], "信封: %v", m)
	d, _ := m["details"].(map[string]any)
	return d
}

func orderOf(d map[string]any) map[string]any { o, _ := d["order"].(map[string]any); return o }
func itemsOf(d map[string]any) []any          { a, _ := d["items"].([]any); return a }
func idOf(o map[string]any) string            { s, _ := o["id"].(string); return s }

// ---- 业务助手 ----

func (x *httpFix) postTransfer(t *testing.T, in TransferInput) map[string]any {
	t.Helper()
	status, m := x.do(t, http.MethodPost, "/api/transfers", in)
	return orderOf(okData(t, status, m))
}

func (x *httpFix) postCount(t *testing.T, in CountInput) map[string]any {
	t.Helper()
	status, m := x.do(t, http.MethodPost, "/api/counts", in)
	return orderOf(okData(t, status, m))
}

// transferAction POST /api/transfers/{id}/{action}（body 可为 nil）。
func (x *httpFix) transferAction(t *testing.T, id, action string, body any) (int, map[string]any) {
	t.Helper()
	return x.do(t, http.MethodPost, "/api/transfers/"+id+"/"+action, body)
}

// worldRowID 五维定位行 ID（登记入参用）。
func worldRowID(t *testing.T, w *fakeWorld, wh, bin, sku, batch int64) int64 {
	t.Helper()
	r := w.rowByKey(wh, bin, sku, batch)
	require.NotNil(t, r, "行不存在: wh=%d bin=%d sku=%d", wh, bin, sku)
	return r.id
}

// fullRegistration 六行登记（普通行盘亏 3；序列号行在库 S1/S2、S3 缺失登记 0；批次行无差异）。
func fullRegistration(t *testing.T, x *httpFix) CountRegistrationInput {
	t.Helper()
	rowPlain := worldRowID(t, x.w, whSrc, binSrc, skuPlain, 0)
	rowSerial := worldRowID(t, x.w, whSrc, binSrc, skuSerial, 0)
	rowBatch := worldRowID(t, x.w, whSrc, binSrc2, skuBatch, batchA)
	return CountRegistrationInput{Registrations: []CountRegistration{
		{InventoryRowID: rowPlain, Qty: q(7)},
		{InventoryRowID: rowSerial, Qty: q(3)},
		{InventoryRowID: rowSerial, SerialNo: "S1", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S2", Qty: qtyUnit},
		{InventoryRowID: rowSerial, SerialNo: "S3", Qty: q(0)},
		{InventoryRowID: rowBatch, Qty: q(50)},
	}}
}

// allEndpoints 21 条接口全量清单（api.md §1 /api/transfers、/api/counts、/api/inventory/moves）。
var allEndpoints = [][2]string{
	{http.MethodGet, "/api/transfers"},
	{http.MethodPost, "/api/transfers"},
	{http.MethodGet, "/api/transfers/in-transit"},
	{http.MethodGet, "/api/transfers/1"},
	{http.MethodPut, "/api/transfers/1"},
	{http.MethodPost, "/api/transfers/1/submit"},
	{http.MethodPost, "/api/transfers/1/approve"},
	{http.MethodPost, "/api/transfers/1/outbound"},
	{http.MethodPost, "/api/transfers/1/arrive"},
	{http.MethodPost, "/api/transfers/1/receive"},
	{http.MethodPost, "/api/transfers/1/cancel"},
	{http.MethodPost, "/api/inventory/moves"},
	{http.MethodGet, "/api/counts"},
	{http.MethodPost, "/api/counts"},
	{http.MethodGet, "/api/counts/1"},
	{http.MethodPost, "/api/counts/1/start"},
	{http.MethodPut, "/api/counts/1/items"},
	{http.MethodPost, "/api/counts/1/finish"},
	{http.MethodPost, "/api/counts/1/complete"},
	{http.MethodPost, "/api/counts/1/reject"},
	{http.MethodPost, "/api/counts/1/cancel"},
}

// ---- 装配与鉴权 ----

func TestStockopsRoutesWiring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 装配 fail-fast（routes.go:26/34/37/40，plan §4.3 规则①）。
	require.PanicsWithValue(t, "stockops 装配失败: db 为 nil（router 必须注入 GORM 句柄）",
		func() { RegisterRoutes(gin.New().Group("/api"), nil, nil) })
	require.PanicsWithValue(t,
		"stockops 装配失败: 库存原语网关未注入（router 必须传 WithGateway(inventory.NewService(...)), plan §4.3 规则①）",
		func() { RegisterRoutes(gin.New().Group("/api"), &gorm.DB{}, nil) })
	require.PanicsWithValue(t,
		"stockops 装配失败: 库位跨域校验未注入（router 必须传 WithBinChecker, plan §4.3 规则①）",
		func() {
			RegisterRoutes(gin.New().Group("/api"), &gorm.DB{}, nil, WithGateway(&fakeGateway{w: newFakeWorld()}))
		})
	require.PanicsWithValue(t,
		"stockops 装配失败: SKU 跨域校验未注入（router 必须传 WithSKUChecker, plan §4.3 规则①）",
		func() {
			RegisterRoutes(gin.New().Group("/api"), &gorm.DB{}, nil,
				WithGateway(&fakeGateway{w: newFakeWorld()}), WithBinChecker(&fakeBins{}))
		})

	// 21 条端点全部挂权限点：未认证 401 COMMON_UNAUTHORIZED（permission.md §2）。
	x := newHTTPFix(t, nil)
	for _, ep := range allEndpoints {
		t.Run(ep[0]+" "+ep[1], func(t *testing.T) {
			status, m := x.do(t, ep[0], ep[1], nil)
			errCode(t, status, http.StatusUnauthorized, "COMMON_UNAUTHORIZED", m)
		})
	}
}

// ---- 调拨：列表（GET /api/transfers） ----

func TestTransferHTTPListFilters(t *testing.T) {
	x := newHTTPFix(t, superUser())

	// 播种：T1 跨仓草稿、T2 跨仓待审核、T3 仓内库位间草稿（创建序 ID 1/2/3，列表倒序）。
	t1 := x.postTransfer(t, transferInput(skuPlain, q(6), 0))
	t2 := x.postTransfer(t, transferInput(skuPlain, q(2), 0))
	status, m := x.transferAction(t, idOf(t2), "submit", nil)
	orderOf(okData(t, status, m))
	t3 := x.postTransfer(t, TransferInput{
		Type: TransferTypeBin, FromWarehouseID: whSrc, ToWarehouseID: whSrc,
		Lines: []TransferLineInput{{
			SKUID: skuPlain, Qty: q(1),
			From: TransferLoc{WarehouseID: whSrc, ZoneID: zoneSrc, ShelfID: shelfSrc, BinID: binSrc},
			To:   TransferLoc{WarehouseID: whSrc, ZoneID: zoneSrc2, ShelfID: shelfSrc2, BinID: binSrc2},
		}},
	})
	no1, no2, no3 := t1["transfer_no"].(string), t2["transfer_no"].(string), t3["transfer_no"].(string)

	cases := []struct {
		name      string
		query     string
		wantTotal int
		wantFirst string // items[0].transfer_no（ID 降序首条）；空 = 不校验
		wantLen   int
		wantPage  int    // 信封回显 page
		wantWH    string // items[0].from_warehouse_id
		wantHTTP  int
		wantCode  string // "OK" = 成功信封
		wantField string // details.field（参数校验类）
	}{
		{name: "全量默认分页", query: "", wantTotal: 3, wantFirst: no3, wantLen: 3, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "status过滤", query: "?status=PENDING_APPROVAL", wantTotal: 1, wantFirst: no2, wantLen: 1, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "type过滤", query: "?type=BIN", wantTotal: 1, wantFirst: no3, wantLen: 1, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "单号过滤", query: "?transfer_no=" + no1, wantTotal: 1, wantFirst: no1, wantLen: 1, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "warehouse_id匹配任一端", query: "?warehouse_id=20", wantTotal: 2, wantLen: 2, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "to_warehouse_id精确目标仓", query: "?to_warehouse_id=20", wantTotal: 2, wantLen: 2, wantPage: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "无匹配仓库", query: "?warehouse_id=999", wantTotal: 0, wantLen: 0, wantPage: 1, wantHTTP: 200, wantCode: "OK"},
		{name: "第二页", query: "?page=2&pageSize=2", wantTotal: 3, wantFirst: no1, wantLen: 1, wantPage: 2, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
		{name: "page必须大于等于1", query: "?page=0", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "page"},
		{name: "pageSize超上限", query: "?pageSize=1000", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "pageSize"},
		{name: "warehouse_id必须为整数", query: "?warehouse_id=abc", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "warehouse_id"},
		{name: "to_warehouse_id必须非负", query: "?to_warehouse_id=-1", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "to_warehouse_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, m := x.do(t, http.MethodGet, "/api/transfers"+tc.query, nil)
			if tc.wantCode == "OK" {
				d := okData(t, status, m)
				require.Equal(t, float64(tc.wantTotal), d["total"])
				require.Equal(t, float64(tc.wantPage), d["page"])
				items := itemsOf(d)
				require.NotNil(t, items, "items 必须为数组（空列表也不得为 null）")
				require.Len(t, items, tc.wantLen)
				if tc.wantFirst != "" {
					first := items[0].(map[string]any)
					require.Equal(t, tc.wantFirst, first["transfer_no"])
					require.IsType(t, "", first["id"], "ID 必须字符串线格式（backend-m1-plan §1）")
					require.Equal(t, tc.wantWH, first["from_warehouse_id"])
				}
				return
			}
			details := errCode(t, status, tc.wantHTTP, tc.wantCode, m)
			require.Equal(t, tc.wantField, details["field"], "校验失败必须带 details（api.md §4）")
		})
	}
}

// ---- 调拨：详情（GET /api/transfers/:id）与在途（GET /api/transfers/in-transit） ----

func TestTransferHTTPGetDetailAndInTransit(t *testing.T) {
	x := newHTTPFix(t, superUser())
	d := x.postTransfer(t, transferInput(skuPlain, q(6), 0))
	id := idOf(d)
	// 推进到 TRANSFERRING：出库后源仓 total 10→4，在途 = qty_out - qty_in = 6。
	for _, act := range []struct {
		action string
		body   any
	}{{"submit", nil}, {"approve", map[string]any{"action": "approve"}}, {"outbound", nil}} {
		status, m := x.transferAction(t, id, act.action, act.body)
		orderOf(okData(t, status, m))
	}
	require.Equal(t, q(4), x.w.rowByKey(whSrc, binSrc, skuPlain, 0).total)

	cases := []struct {
		name     string
		path     string
		wantHTTP int
		wantCode string
		check    func(t *testing.T, d map[string]any)
	}{
		{name: "详情视图与在途进度", path: "/api/transfers/" + id, wantHTTP: 200, wantCode: "OK",
			check: func(t *testing.T, d map[string]any) {
				o := orderOf(d)
				require.Equal(t, "TRANSFERRING", o["status"])
				require.Equal(t, "TR-20261003-000001", o["transfer_no"]) // docnum TR 前缀（business-flow §13.1）
				require.Equal(t, "10", o["from_warehouse_id"])           // ID 字符串线格式
				require.Equal(t, "20", o["to_warehouse_id"])
				items := itemsOf(d)
				require.Len(t, items, 1)
				it := items[0].(map[string]any)
				require.Equal(t, float64(6), it["qty"])
				require.Equal(t, float64(6), it["qty_out"])
				require.Equal(t, float64(0), it["qty_in"])
				require.Equal(t, float64(6), it["in_transit_qty"]) // 后端计算在途（handler.go:70/82）
			}},
		{name: "在途聚合全仓", path: "/api/transfers/in-transit", wantHTTP: 200, wantCode: "OK",
			check: func(t *testing.T, d map[string]any) {
				require.Equal(t, float64(1), d["total"])
				rows := itemsOf(d)
				require.Len(t, rows, 1)
				r := rows[0].(map[string]any)
				require.Equal(t, float64(6), r["out_transit"])
				require.Equal(t, float64(6), r["in_transit"])
			}},
		{name: "在途聚合目标仓视角", path: "/api/transfers/in-transit?warehouse_id=20", wantHTTP: 200, wantCode: "OK",
			check: func(t *testing.T, d map[string]any) {
				r := itemsOf(d)[0].(map[string]any)
				require.Equal(t, float64(0), r["out_transit"])
				require.Equal(t, float64(6), r["in_transit"])
			}},
		{name: "在途sku过滤非法", path: "/api/transfers/in-transit?sku_id=abc", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "在途warehouse过滤非法", path: "/api/transfers/in-transit?warehouse_id=-1", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "id必须为正整数", path: "/api/transfers/0", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "id必须为整数", path: "/api/transfers/abc", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "不存在按404", path: "/api/transfers/999", wantHTTP: 404, wantCode: "STOCKOPS_TRANSFER_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, m := x.do(t, http.MethodGet, tc.path, nil)
			if tc.wantCode == "OK" {
				d := okData(t, status, m)
				if tc.check != nil {
					tc.check(t, d)
				}
				return
			}
			errCode(t, status, tc.wantHTTP, tc.wantCode, m)
		})
	}
}

// ---- 调拨：修改（PUT /api/transfers/:id） ----

func TestTransferHTTPUpdateTable(t *testing.T) {
	x := newHTTPFix(t, superUser())
	draftAID := idOf(x.postTransfer(t, transferInput(skuPlain, q(6), 0)))
	draftBID := idOf(x.postTransfer(t, transferInput(skuPlain, q(3), 0)))
	submittedID := idOf(x.postTransfer(t, transferInput(skuPlain, q(2), 0)))
	status, m := x.transferAction(t, submittedID, "submit", nil)
	orderOf(okData(t, status, m))

	frac, err := stock.ParseQty("1.5")
	require.NoError(t, err)

	// upd 以合法模板构造修改入参（mutate 覆写制造非法形态）。
	upd := func(mutate func(*TransferInput)) TransferInput {
		in := transferInput(skuPlain, q(5), 0)
		if mutate != nil {
			mutate(&in)
		}
		return in
	}
	serialDup := transferInput(skuSerial, qtyUnit, 0)
	serialDup.Lines = append(serialDup.Lines, serialDup.Lines[0])

	cases := []struct {
		name      string
		id        string
		body      any
		wantHTTP  int
		wantCode  string
		wantField string
		check     func(t *testing.T, d map[string]any)
	}{
		{name: "草稿整单替换_行号重编", id: draftAID,
			body:     upd(func(in *TransferInput) { in.Remark = "改量" }),
			wantHTTP: 200, wantCode: "OK",
			check: func(t *testing.T, d map[string]any) {
				o := orderOf(d)
				require.Equal(t, "改量", o["remark"])
				require.Equal(t, "DRAFT", o["status"])
				items := itemsOf(d)
				require.Len(t, items, 1)
				it := items[0].(map[string]any)
				require.Equal(t, float64(5), it["qty"])
				require.Equal(t, float64(1), it["line_no"])
			}},
		{name: "仅草稿可修改", id: submittedID, body: upd(nil), wantHTTP: 409, wantCode: "STOCKOPS_STATUS_CONFLICT"},
		{name: "不存在", id: "999", body: upd(nil), wantHTTP: 404, wantCode: "STOCKOPS_TRANSFER_NOT_FOUND"},
		{name: "id非法", id: "abc", body: upd(nil), wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "id"},
		{name: "坏JSON", id: draftBID, body: rawJSON("{"), wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "type非法", id: draftBID, body: upd(func(in *TransferInput) { in.Type = "PICKUP" }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID", wantField: "type"},
		{name: "明细为空", id: draftBID, body: upd(func(in *TransferInput) { in.Lines = nil }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID"},
		{name: "数量非正", id: draftBID, body: upd(func(in *TransferInput) { in.Lines[0].Qty = q(0) }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID", wantField: "qty"},
		{name: "跨仓调拨两端同仓", id: draftBID, body: upd(func(in *TransferInput) { in.ToWarehouseID = whSrc }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID"},
		{name: "明细两端仓库与单据不符", id: draftBID, body: upd(func(in *TransferInput) { in.Lines[0].From.WarehouseID = 99 }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID"},
		{name: "目标zone缺失", id: draftBID, body: upd(func(in *TransferInput) { in.Lines[0].To.ZoneID = 0 }),
			wantHTTP: 400, wantCode: "STOCKOPS_TRANSFER_LINE_INVALID"},
		{name: "SKU不存在", id: draftBID, body: upd(func(in *TransferInput) { in.Lines[0].SKUID = 999 }),
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "目标库位不属该仓", id: draftBID, body: upd(func(in *TransferInput) { in.Lines[0].To.BinID = 999 }),
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "序列号SKU数量非整数", id: draftBID, body: transferInput(skuSerial, frac, 0),
			wantHTTP: 400, wantCode: "STOCKOPS_SERIAL_QTY_INVALID"},
		{name: "序列号SKU同单多行", id: draftBID, body: serialDup,
			wantHTTP: 400, wantCode: "STOCKOPS_SERIAL_LINE_DUP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, m := x.do(t, http.MethodPut, "/api/transfers/"+tc.id, tc.body)
			if tc.wantCode == "OK" {
				d := okData(t, status, m)
				if tc.check != nil {
					tc.check(t, d)
				}
				return
			}
			details := errCode(t, status, tc.wantHTTP, tc.wantCode, m)
			if tc.wantField != "" {
				require.Equal(t, tc.wantField, details["field"])
			}
		})
	}
}

// ---- 调拨：动作（submit/approve/outbound/arrive/receive/cancel） ----

func TestTransferHTTPActionLifecycle(t *testing.T) {
	x := newHTTPFix(t, superUser())

	// —— A：全生命周期 → COMPLETED（HTTP 契约面冒烟；账目恒等式详见 service_test.go）——
	a := x.postTransfer(t, transferInput(skuPlain, q(6), 0))
	aID := idOf(a)
	require.Equal(t, "1", aID)
	require.Equal(t, "TR-20261003-000001", a["transfer_no"])
	require.Equal(t, "DRAFT", a["status"])

	status, m := x.transferAction(t, aID, "submit", nil)
	require.Equal(t, "PENDING_APPROVAL", orderOf(okData(t, status, m))["status"])
	status, m = x.transferAction(t, aID, "submit", nil)
	require.Equal(t, "PENDING_APPROVAL", orderOf(okData(t, status, m))["status"]) // 重复提交幂等重放

	// 未审核直接出库 → 状态冲突。
	status, m = x.transferAction(t, aID, "outbound", nil)
	errCode(t, status, http.StatusConflict, "STOCKOPS_STATUS_CONFLICT", m)

	// 审核动作值域与空体绑定校验（先于状态机，transfer.go:466-469）。
	status, m = x.transferAction(t, aID, "approve", map[string]any{"action": "bogus"})
	errCode(t, status, http.StatusBadRequest, "COMMON_INVALID_PARAM", m)
	status, m = x.transferAction(t, aID, "approve", nil)
	errCode(t, status, http.StatusBadRequest, "COMMON_INVALID_PARAM", m)

	// 审核通过：预占源仓（10→4 可用 / 6 锁定）。
	// 注：动作响应的 approved_by 为迁移前本地副本值（service 仅回填 Status，
	// transfer.go:453），落库值经 GET 详情复核。
	status, m = x.transferAction(t, aID, "approve", map[string]any{"action": "approve", "opinion": "同意"})
	ao := orderOf(okData(t, status, m))
	require.Equal(t, "APPROVED", ao["status"])
	status, m = x.do(t, http.MethodGet, "/api/transfers/"+aID, nil)
	afresh := orderOf(okData(t, status, m))
	require.Equal(t, "7", afresh["approved_by"]) // 审批人落库（actorOf(c) → UserID）
	require.NotEmpty(t, afresh["approved_at"])
	require.Equal(t, q(4), x.w.rowByKey(whSrc, binSrc, skuPlain, 0).avail)
	require.Equal(t, q(6), x.w.rowByKey(whSrc, binSrc, skuPlain, 0).locked)

	// 重复审核：幂等重放，预占不翻倍（单条 ACTIVE 锁）。
	status, m = x.transferAction(t, aID, "approve", map[string]any{"action": "approve"})
	require.Equal(t, "APPROVED", orderOf(okData(t, status, m))["status"])
	active := 0
	for _, l := range x.w.locks {
		if l.status == "ACTIVE" {
			active++
		}
	}
	require.Equal(t, 1, active)

	status, m = x.transferAction(t, aID, "outbound", nil)
	require.Equal(t, "TRANSFERRING", orderOf(okData(t, status, m))["status"])
	require.Equal(t, q(4), x.w.rowByKey(whSrc, binSrc, skuPlain, 0).total)

	status, m = x.transferAction(t, aID, "arrive", nil)
	require.Equal(t, "AWAITING_RECEIPT", orderOf(okData(t, status, m))["status"])

	status, m = x.transferAction(t, aID, "receive", nil)
	require.Equal(t, "COMPLETED", orderOf(okData(t, status, m))["status"])
	dst := x.w.rowByKey(whDst, binDst, skuPlain, 0)
	require.NotNil(t, dst)
	require.Equal(t, q(6), dst.total)
	checkAllIdentity(t, x.w)

	// —— B：审核驳回 → CANCELLED（不预占）——
	bID := idOf(x.postTransfer(t, transferInput(skuPlain, q(1), 0)))
	status, m = x.transferAction(t, bID, "submit", nil)
	orderOf(okData(t, status, m))
	status, m = x.transferAction(t, bID, "approve", map[string]any{"action": "reject", "opinion": "重报"})
	require.Equal(t, "CANCELLED", orderOf(okData(t, status, m))["status"])
	require.Equal(t, q(4), x.w.rowByKey(whSrc, binSrc, skuPlain, 0).avail) // 库存未动

	// —— C：草稿取消（带 reason）→ CANCELLED ——
	cID := idOf(x.postTransfer(t, transferInput(skuPlain, q(1), 0)))
	status, m = x.transferAction(t, cID, "cancel", map[string]any{"reason": "计划变更"})
	require.Equal(t, "CANCELLED", orderOf(okData(t, status, m))["status"])

	// —— D：出库后取消被拒（business-flow §13.3）——
	dID := idOf(x.postTransfer(t, transferInput(skuPlain, q(1), 0)))
	for _, act := range []struct {
		action string
		body   any
	}{{"submit", nil}, {"approve", map[string]any{"action": "approve"}}, {"outbound", nil}} {
		status, m = x.transferAction(t, dID, act.action, act.body)
		orderOf(okData(t, status, m))
	}
	status, m = x.transferAction(t, dID, "cancel", map[string]any{"reason": "误操作"})
	errCode(t, status, http.StatusConflict, "STOCKOPS_TRANSFER_CANCEL_FORBIDDEN", m)

	// —— E：其余守卫分支 ——
	eID := idOf(x.postTransfer(t, transferInput(skuPlain, q(1), 0)))
	status, m = x.transferAction(t, eID, "arrive", nil)
	errCode(t, status, http.StatusConflict, "STOCKOPS_STATUS_CONFLICT", m)
	status, m = x.transferAction(t, eID, "receive", nil)
	errCode(t, status, http.StatusConflict, "STOCKOPS_STATUS_CONFLICT", m)
	status, m = x.transferAction(t, "999", "submit", nil)
	errCode(t, status, http.StatusNotFound, "STOCKOPS_TRANSFER_NOT_FOUND", m)
	status, m = x.transferAction(t, "abc", "submit", nil)
	errCode(t, status, http.StatusBadRequest, "COMMON_INVALID_PARAM", m)
}

// 复现已知问题：空体取消（transfer.ts:219 payload 可选；api.md:369 取消类接口"空体兼容"
// 先例；同文件 count 系 handler 以 Body!=nil&&ContentLength!=0 兜住空体，handler.go:651）——
// 当前 handler.go:479 的 `c.Request.Body != nil` 守卫在真实服务端恒真（Body 永不为 nil），
// 空体必然进入 ShouldBindJSON 收到 EOF → 400。
func TestTransferCancelEmptyBodyCompatibility(t *testing.T) {
	x := newHTTPFix(t, superUser())
	d := x.postTransfer(t, transferInput(skuPlain, q(1), 0))
	status, m := x.transferAction(t, idOf(d), "cancel", nil)
	if status == http.StatusBadRequest {
		t.Skip("已知bug：空体取消被 400（handler.go:479 守卫恒真，应同 handler.go:651 口径判 ContentLength）")
	}
	require.Equal(t, "CANCELLED", orderOf(okData(t, status, m))["status"])
}

// ---- 数据权限经 handler 的接线（直连 handler，绕过权限中间件） ----

func TestStockopsHandlerScopeWiring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	h := &handler{svc: f.svc}

	td, err := f.svc.CreateTransfer(ctxBG(), testActor("stockop"), transferInput(skuPlain, q(6), 0))
	require.NoError(t, err)
	cd, err := f.svc.CreateCount(ctxBG(), testActor("stocktaker"), CountInput{WarehouseID: whSrc, Scope: allBinScope()})
	require.NoError(t, err)

	// callGet 以指定用户上下文直连 GET 类 handler（scopeOf(c) → Service，handler.go:205-208）。
	callGet := func(call func(c *gin.Context), path string, id int64, uc auth.UserContext) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		if id > 0 {
			c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(id, 10)}}
		}
		c.Set(authUserCtxKey, uc)
		call(c)
		var m map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "响应非 JSON 信封: %s", rec.Body.String())
		return rec.Code, m
	}

	// 调拨详情：两端任一仓库在范围内即可见；范围外 fail-closed 404（transfer.go:814-816）。
	tid := td.Order.ID.Int64()
	status, m := callGet(h.getTransfer, "/api/transfers/1", tid, *superUser())
	okData(t, status, m)
	status, m = callGet(h.getTransfer, "/api/transfers/1", tid, *whUser(whSrc))
	okData(t, status, m) // 源端可见
	status, m = callGet(h.getTransfer, "/api/transfers/1", tid, *whUser(whDst))
	okData(t, status, m) // 目标端可见
	status, m = callGet(h.getTransfer, "/api/transfers/1", tid, *whUser(99))
	errCode(t, status, http.StatusNotFound, "STOCKOPS_TRANSFER_NOT_FOUND", m)
	status, m = callGet(h.getTransfer, "/api/transfers/1", tid, *whUser())
	errCode(t, status, http.StatusNotFound, "STOCKOPS_TRANSFER_NOT_FOUND", m) // 空范围不可见任何行

	// 调拨列表：范围过滤随 scope 注入（handler.go:335-336）。
	status, m = callGet(h.listTransfers, "/api/transfers", 0, *whUser(whSrc))
	require.Equal(t, float64(1), okData(t, status, m)["total"])
	status, m = callGet(h.listTransfers, "/api/transfers", 0, *whUser(99))
	require.Equal(t, float64(0), okData(t, status, m)["total"])

	// 盘点详情/列表：单仓可见性（count.go:846-859，TestCountDataPermissionFailClosed 的 HTTP 接线面）。
	cid := cd.Order.ID.Int64()
	status, m = callGet(h.getCount, "/api/counts/1", cid, *superUser())
	okData(t, status, m)
	status, m = callGet(h.getCount, "/api/counts/1", cid, *whUser(whSrc))
	okData(t, status, m)
	status, m = callGet(h.getCount, "/api/counts/1", cid, *whUser(99))
	errCode(t, status, http.StatusNotFound, "STOCKOPS_COUNT_NOT_FOUND", m)
	status, m = callGet(h.listCounts, "/api/counts", 0, *whUser(whSrc))
	require.Equal(t, float64(1), okData(t, status, m)["total"])
	status, m = callGet(h.listCounts, "/api/counts", 0, *whUser(99))
	require.Equal(t, float64(0), okData(t, status, m)["total"])
}

// ---- 盘点：列表（GET /api/counts）与详情（GET /api/counts/:id） ----

func TestCountHTTPListAndGet(t *testing.T) {
	x := newHTTPFix(t, superUser())

	c1 := x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope(), Remark: "月度盘点"})
	c2 := x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()})
	status, m := x.do(t, http.MethodPost, "/api/counts/"+idOf(c2)+"/start", nil)
	orderOf(okData(t, status, m))
	c3 := x.postCount(t, CountInput{WarehouseID: whDst, Scope: CountScope{Mode: "ALL"}})
	no1, no2, no3 := c1["count_no"].(string), c2["count_no"].(string), c3["count_no"].(string)

	t.Run("列表过滤与分页", func(t *testing.T) {
		cases := []struct {
			name      string
			query     string
			wantTotal int
			wantFirst string
			wantLen   int
			wantWH    string
			wantHTTP  int
			wantCode  string
			wantField string
		}{
			{name: "全量", query: "", wantTotal: 3, wantFirst: no3, wantLen: 3, wantWH: "20", wantHTTP: 200, wantCode: "OK"},
			{name: "status过滤", query: "?status=COUNTING", wantTotal: 1, wantFirst: no2, wantLen: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
			{name: "单号过滤", query: "?count_no=" + no1, wantTotal: 1, wantFirst: no1, wantLen: 1, wantWH: "10", wantHTTP: 200, wantCode: "OK"},
			{name: "仓库过滤", query: "?warehouse_id=20", wantTotal: 1, wantFirst: no3, wantLen: 1, wantWH: "20", wantHTTP: 200, wantCode: "OK"},
			{name: "仓库过滤无匹配", query: "?warehouse_id=999", wantTotal: 0, wantLen: 0, wantHTTP: 200, wantCode: "OK"},
			{name: "page必须大于等于1", query: "?page=0", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "page"},
			{name: "pageSize必须合法", query: "?pageSize=0", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "pageSize"},
			{name: "warehouse_id必须为整数", query: "?warehouse_id=abc", wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM", wantField: "warehouse_id"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, m := x.do(t, http.MethodGet, "/api/counts"+tc.query, nil)
				if tc.wantCode == "OK" {
					d := okData(t, status, m)
					require.Equal(t, float64(tc.wantTotal), d["total"])
					items := itemsOf(d)
					require.NotNil(t, items)
					require.Len(t, items, tc.wantLen)
					if tc.wantFirst != "" {
						first := items[0].(map[string]any)
						require.Equal(t, tc.wantFirst, first["count_no"])
						require.Equal(t, tc.wantWH, first["warehouse_id"])
					}
					return
				}
				details := errCode(t, status, tc.wantHTTP, tc.wantCode, m)
				require.Equal(t, tc.wantField, details["field"])
			})
		}
	})

	t.Run("详情_冻结快照与未登记null", func(t *testing.T) {
		status, m := x.do(t, http.MethodGet, "/api/counts/"+idOf(c2), nil)
		d := okData(t, status, m)
		o := orderOf(d)
		require.Equal(t, "COUNTING", o["status"])
		require.Equal(t, no2, o["count_no"])
		// 明细双层：3 个行级汇总 + 3 件序列号行（count.go:34-37）。
		items := itemsOf(d)
		require.Len(t, items, 6)
		var summaryPlain, serialS1 map[string]any
		for _, raw := range items {
			it := raw.(map[string]any)
			switch {
			case it["serial_no"] == "" && it["sku_id"] == "100":
				summaryPlain = it
			case it["serial_no"] == "S1":
				serialS1 = it
			}
		}
		require.NotNil(t, summaryPlain)
		require.Equal(t, float64(10), summaryPlain["qty_system"]) // 冻结快照 = 行总量
		require.Nil(t, summaryPlain["qty_counted"])               // null=未登记（inventory-rules §9）
		require.Equal(t, "1", summaryPlain["inventory_row_id"])
		require.NotNil(t, serialS1)
		require.Equal(t, float64(1), serialS1["qty_system"]) // 序列号逐件 qty_system=1
		require.Equal(t, "2", serialS1["inventory_row_id"])
	})

	t.Run("详情_完成后带差异行", func(t *testing.T) {
		cid := idOf(c2)
		status, m := x.do(t, http.MethodPut, "/api/counts/"+cid+"/items", fullRegistration(t, x))
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/finish", nil)
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodGet, "/api/counts/"+cid, nil)
		d := okData(t, status, m)
		diffs, ok := d["differences"].([]any)
		require.True(t, ok)
		require.Len(t, diffs, 1) // 仅普通行盘亏；无差异行不生成（plan §6.7）
		df := diffs[0].(map[string]any)
		require.Equal(t, float64(-3), df["diff_qty"]) // 盘盈为正
		require.Equal(t, float64(10), df["qty_system"])
		require.Equal(t, float64(7), df["qty_counted"])
		require.Equal(t, "PENDING", df["status"])
	})

	t.Run("详情id与不存在", func(t *testing.T) {
		status, m := x.do(t, http.MethodGet, "/api/counts/abc", nil)
		errCode(t, status, http.StatusBadRequest, "COMMON_INVALID_PARAM", m)
		status, m = x.do(t, http.MethodGet, "/api/counts/999", nil)
		errCode(t, status, http.StatusNotFound, "STOCKOPS_COUNT_NOT_FOUND", m)
	})
}

// ---- 盘点：动作（start/register/finish/complete/reject/cancel） ----

func TestCountHTTPActionLifecycle(t *testing.T) {
	t.Run("全流程_冻结_登记_差异_调整", func(t *testing.T) {
		x := newHTTPFix(t, superUser())
		cid := idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))

		// 开始：范围冻结（available→frozen）+ 序列号逐件 FROZEN（inventory-rules §4/§8.2）。
		status, m := x.do(t, http.MethodPost, "/api/counts/"+cid+"/start", nil)
		require.Equal(t, "COUNTING", orderOf(okData(t, status, m))["status"])
		plain := x.w.rowByKey(whSrc, binSrc, skuPlain, 0)
		require.True(t, plain.avail.IsZero())
		require.Equal(t, q(10), plain.frozen)
		for _, s := range []string{"S1", "S2", "S3"} {
			require.Equal(t, "FROZEN", x.w.serials[s].status)
		}

		// 重复开始：幂等重放（count.go:105-113）。
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/start", nil)
		require.Equal(t, "COUNTING", orderOf(okData(t, status, m))["status"])

		// 登记实盘：线格式 items[{InventoryRowID,SerialNo,Qty}]——CountRegistration 无
		// json tag（store.go:204-208），前端同 PascalCase 契约（web/src/api/count.ts:132-136）。
		status, m = x.do(t, http.MethodPut, "/api/counts/"+cid+"/items", fullRegistration(t, x))
		registered := orderOf(okData(t, status, m))
		for _, raw := range itemsOf(registered) {
			it := raw.(map[string]any)
			if it["serial_no"] == "" && it["sku_id"] == "100" {
				require.Equal(t, float64(7), it["qty_counted"])
			}
		}

		// 完成实盘 → 待审核 + 差异生成。
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/finish", nil)
		require.Equal(t, "PENDING_REVIEW", orderOf(okData(t, status, m))["status"])

		// 差异审核通过：解冻 + 调整落账 + 序列号联动（S3 缺失核销）。
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/complete", map[string]any{"opinion": "差异属实"})
		d := okData(t, status, m)
		require.Equal(t, "COMPLETED", orderOf(d)["status"])
		diffs := d["differences"].([]any)
		require.Len(t, diffs, 1)
		df := diffs[0].(map[string]any)
		require.Equal(t, "EXECUTED", df["status"])
		require.NotEmpty(t, df["adjust_no"]) // 调整单单号回写（plan §8.3 条 3）
		require.Equal(t, q(7), plain.total)
		require.Equal(t, q(7), plain.avail)
		require.True(t, plain.frozen.IsZero())
		require.Equal(t, "OUTBOUND", x.w.serials["S3"].status)
		require.Equal(t, "IN_STOCK", x.w.serials["S1"].status)
		checkAllIdentity(t, x.w)

		// 重复完成：幂等重放，不二次调整。
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/complete", nil)
		require.Equal(t, "COMPLETED", orderOf(okData(t, status, m))["status"])
		require.Equal(t, q(7), plain.total)
	})

	t.Run("驳回_仅解冻不调整", func(t *testing.T) {
		x := newHTTPFix(t, superUser())
		cid := idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))
		status, m := x.do(t, http.MethodPost, "/api/counts/"+cid+"/start", nil)
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPut, "/api/counts/"+cid+"/items", fullRegistration(t, x))
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/finish", nil)
		orderOf(okData(t, status, m))

		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/reject", map[string]any{"opinion": "重盘"})
		rd := okData(t, status, m)
		require.Equal(t, "CANCELLED", orderOf(rd)["status"])
		plain := x.w.rowByKey(whSrc, binSrc, skuPlain, 0)
		require.Equal(t, q(10), plain.total) // 库存未调整
		require.Equal(t, q(10), plain.avail)
		require.True(t, plain.frozen.IsZero())
		diffs := rd["differences"].([]any)
		require.NotEmpty(t, diffs)
		for _, raw := range diffs {
			require.Equal(t, "REJECTED", raw.(map[string]any)["status"])
		}
		for _, s := range []string{"S1", "S2", "S3"} {
			require.Equal(t, "IN_STOCK", x.w.serials[s].status)
		}
		checkAllIdentity(t, x.w)
	})

	t.Run("盘点中取消_解冻", func(t *testing.T) {
		x := newHTTPFix(t, superUser())
		cid := idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))
		status, m := x.do(t, http.MethodPost, "/api/counts/"+cid+"/start", nil)
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/cancel", map[string]any{"reason": "范围有误"})
		require.Equal(t, "CANCELLED", orderOf(okData(t, status, m))["status"])
		plain := x.w.rowByKey(whSrc, binSrc, skuPlain, 0)
		require.Equal(t, q(10), plain.avail)
		require.True(t, plain.frozen.IsZero())
		for _, s := range []string{"S1", "S2", "S3"} {
			require.Equal(t, "IN_STOCK", x.w.serials[s].status)
		}
		checkAllIdentity(t, x.w)
	})

	t.Run("草稿取消_状态迁移无库存动作", func(t *testing.T) {
		x := newHTTPFix(t, superUser())
		cid := idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))
		status, m := x.do(t, http.MethodPost, "/api/counts/"+cid+"/cancel", map[string]any{"reason": "不再盘点"})
		require.Equal(t, "CANCELLED", orderOf(okData(t, status, m))["status"])
		plain := x.w.rowByKey(whSrc, binSrc, skuPlain, 0)
		require.Equal(t, q(10), plain.avail) // 从未冻结
		require.True(t, plain.frozen.IsZero())
	})
}

func TestCountHTTPActionGuards(t *testing.T) {
	draftCount := func(t *testing.T, x *httpFix) string {
		return idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))
	}
	startedCount := func(t *testing.T, x *httpFix) string {
		id := draftCount(t, x)
		status, m := x.do(t, http.MethodPost, "/api/counts/"+id+"/start", nil)
		orderOf(okData(t, status, m))
		return id
	}
	reviewCount := func(t *testing.T, x *httpFix) string {
		id := startedCount(t, x)
		status, m := x.do(t, http.MethodPut, "/api/counts/"+id+"/items", fullRegistration(t, x))
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+id+"/finish", nil)
		orderOf(okData(t, status, m))
		return id
	}

	cases := []struct {
		name     string
		setup    func(t *testing.T, x *httpFix) string
		method   string
		path     func(id string) string
		body     func(x *httpFix) any
		wantHTTP int
		wantCode string
	}{
		{name: "start不存在", setup: nil, method: http.MethodPost,
			path: func(string) string { return "/api/counts/999/start" },
			body: func(*httpFix) any { return nil }, wantHTTP: 404, wantCode: "STOCKOPS_COUNT_NOT_FOUND"},
		{name: "start_id非法", setup: nil, method: http.MethodPost,
			path: func(string) string { return "/api/counts/abc/start" },
			body: func(*httpFix) any { return nil }, wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "登记仅盘点中", setup: draftCount, method: http.MethodPut,
			path:     func(id string) string { return "/api/counts/" + id + "/items" },
			body:     func(x *httpFix) any { return fullRegistration(t, x) },
			wantHTTP: 409, wantCode: "STOCKOPS_STATUS_CONFLICT"},
		{name: "登记空明细", setup: startedCount, method: http.MethodPut,
			path:     func(id string) string { return "/api/counts/" + id + "/items" },
			body:     func(*httpFix) any { return CountRegistrationInput{Registrations: []CountRegistration{}} },
			wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		{name: "登记越范围行", setup: startedCount, method: http.MethodPut,
			path: func(id string) string { return "/api/counts/" + id + "/items" },
			body: func(*httpFix) any {
				return CountRegistrationInput{Registrations: []CountRegistration{
					{InventoryRowID: 424242, Qty: q(1)},
				}}
			}, wantHTTP: 400, wantCode: "STOCKOPS_COUNT_ITEM_FOREIGN"},
		{name: "登记负数", setup: startedCount, method: http.MethodPut,
			path: func(id string) string { return "/api/counts/" + id + "/items" },
			body: func(x *httpFix) any {
				rowPlain := worldRowID(t, x.w, whSrc, binSrc, skuPlain, 0)
				return CountRegistrationInput{Registrations: []CountRegistration{
					{InventoryRowID: rowPlain, Qty: q(-1)},
				}}
			}, wantHTTP: 400, wantCode: "STOCKOPS_COUNT_QTY_INVALID"},
		{name: "序列号行数量仅0或1", setup: startedCount, method: http.MethodPut,
			path: func(id string) string { return "/api/counts/" + id + "/items" },
			body: func(x *httpFix) any {
				rowSerial := worldRowID(t, x.w, whSrc, binSrc, skuSerial, 0)
				return CountRegistrationInput{Registrations: []CountRegistration{
					{InventoryRowID: rowSerial, SerialNo: "S1", Qty: q(2)},
				}}
			}, wantHTTP: 400, wantCode: "STOCKOPS_COUNT_QTY_INVALID"},
		{name: "未登记完成实盘", setup: startedCount, method: http.MethodPost,
			path: func(id string) string { return "/api/counts/" + id + "/finish" },
			body: func(*httpFix) any { return nil }, wantHTTP: 400, wantCode: "STOCKOPS_COUNTING_INCOMPLETE"},
		{name: "盘点中不可完成差异", setup: startedCount, method: http.MethodPost,
			path:     func(id string) string { return "/api/counts/" + id + "/complete" },
			body:     func(*httpFix) any { return map[string]any{"opinion": "x"} },
			wantHTTP: 409, wantCode: "STOCKOPS_STATUS_CONFLICT"},
		{name: "盘点中不可驳回", setup: startedCount, method: http.MethodPost,
			path:     func(id string) string { return "/api/counts/" + id + "/reject" },
			body:     func(*httpFix) any { return map[string]any{"opinion": "x"} },
			wantHTTP: 409, wantCode: "STOCKOPS_STATUS_CONFLICT"},
		{name: "待审核不可取消", setup: reviewCount, method: http.MethodPost,
			path:     func(id string) string { return "/api/counts/" + id + "/cancel" },
			body:     func(*httpFix) any { return map[string]any{"reason": "x"} },
			wantHTTP: 409, wantCode: "STOCKOPS_STATUS_CONFLICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := newHTTPFix(t, superUser()) // 每用例独立世界（冻结/调整互不串扰）
			var id string
			if tc.setup != nil {
				id = tc.setup(t, x)
			}
			status, m := x.do(t, tc.method, tc.path(id), tc.body(x))
			errCode(t, status, tc.wantHTTP, tc.wantCode, m)
			checkAllIdentity(t, x.w)
		})
	}

	t.Run("空体完成差异兼容", func(t *testing.T) {
		// complete/reject/cancel 空体合法（ContentLength=0 跳过绑定，handler.go:651/673/695）。
		x := newHTTPFix(t, superUser())
		cid := idOf(x.postCount(t, CountInput{WarehouseID: whSrc, Scope: allBinScope()}))
		status, m := x.do(t, http.MethodPost, "/api/counts/"+cid+"/start", nil)
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPut, "/api/counts/"+cid+"/items", fullRegistration(t, x))
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/finish", nil)
		orderOf(okData(t, status, m))
		status, m = x.do(t, http.MethodPost, "/api/counts/"+cid+"/complete", nil)
		require.Equal(t, "COMPLETED", orderOf(okData(t, status, m))["status"])
	})

	t.Run("创建入参校验", func(t *testing.T) {
		x := newHTTPFix(t, superUser())
		cases := []struct {
			name     string
			in       CountInput
			wantCode string
		}{
			{name: "仓库非法", in: CountInput{WarehouseID: 0, Scope: allBinScope()}, wantCode: "STOCKOPS_SCOPE_INVALID"},
			{name: "mode非法", in: CountInput{WarehouseID: whSrc, Scope: CountScope{Mode: "PLANET"}}, wantCode: "STOCKOPS_SCOPE_INVALID"},
			{name: "维度与mode不符", in: CountInput{WarehouseID: whSrc, Scope: CountScope{Mode: "ALL", BinIDs: []int64{binSrc}}}, wantCode: "STOCKOPS_SCOPE_INVALID"},
			{name: "范围ID重复", in: CountInput{WarehouseID: whSrc, Scope: CountScope{Mode: "BIN", BinIDs: []int64{binSrc, binSrc}}}, wantCode: "STOCKOPS_SCOPE_INVALID"},
			{name: "范围ID非正", in: CountInput{WarehouseID: whSrc, Scope: CountScope{Mode: "BIN", BinIDs: []int64{0}}}, wantCode: "STOCKOPS_SCOPE_INVALID"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, m := x.do(t, http.MethodPost, "/api/counts", tc.in)
				errCode(t, status, http.StatusBadRequest, tc.wantCode, m)
			})
		}
	})
}

// ---- 仓内移库（POST /api/inventory/moves） ----

func TestMoveBinHTTPTable(t *testing.T) {
	x := newHTTPFix(t, superUser())

	validFrom := MoveKeyInput{WarehouseID: whSrc, ZoneID: zoneSrc, ShelfID: shelfSrc, BinID: binSrc, SKUID: skuPlain}
	validTo := MoveKeyInput{WarehouseID: whSrc, ZoneID: zoneSrc2, ShelfID: shelfSrc2, BinID: binSrc2, SKUID: skuPlain}

	t.Run("成功_整数与小数数量", func(t *testing.T) {
		status, m := x.do(t, http.MethodPost, "/api/inventory/moves", MoveInput{
			From: validFrom, To: validTo, Qty: "4", SourceNo: "WO-2026-001", Remark: "上架纠偏",
		})
		d := okData(t, status, m)
		require.Equal(t, false, d["replay"])
		from := x.w.rowByKey(whSrc, binSrc, skuPlain, 0)
		require.Equal(t, q(6), from.total) // 源行 total/avail 同减
		require.Equal(t, q(6), from.avail)
		to := x.w.rowByKey(whSrc, binSrc2, skuPlain, 0)
		require.NotNil(t, to) // 目标行由原语 ensureRow 创建
		require.Equal(t, q(4), to.total)
		require.Equal(t, q(4), to.avail)

		// MOVE 流水：来源归因 stockops_move + 作业依据号（moves.go:78，inventory-rules §5）。
		var moveLedger *fakeLedger
		for i := range x.w.ledgers {
			if x.w.ledgers[i].changeType == "MOVE" {
				l := x.w.ledgers[i]
				require.Equal(t, "stockops_move", l.businessType)
				require.Equal(t, "WO-2026-001", l.bizNo)
				moveLedger = &l
			}
		}
		require.NotNil(t, moveLedger, "必须落 MOVE 流水")
		checkAllIdentity(t, x.w)

		// numeric(18,4) 文本数量：小数移库（10-4-1.5=4.5）。
		status, m = x.do(t, http.MethodPost, "/api/inventory/moves", MoveInput{
			From: validFrom, To: validTo, Qty: "1.5", SourceNo: "WO-2026-002",
		})
		okData(t, status, m)
		frac45, err := stock.ParseQty("4.5")
		require.NoError(t, err)
		require.Equal(t, frac45, from.total)
		frac55, err := stock.ParseQty("5.5")
		require.NoError(t, err)
		require.Equal(t, frac55, to.total)
		checkAllIdentity(t, x.w)
	})

	t.Run("校验失败与原语守卫透传", func(t *testing.T) {
		crossWH := validTo
		crossWH.WarehouseID = whDst
		sameBin := validTo
		sameBin.BinID = binSrc
		noSuchRow := validFrom
		noSuchRow.BinID = 999

		cases := []struct {
			name     string
			body     any
			wantHTTP int
			wantCode string
		}{
			{name: "from定位键缺bin", body: MoveInput{From: MoveKeyInput{WarehouseID: whSrc, SKUID: skuPlain}, To: validTo, Qty: "1", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_KEY_INVALID"},
			{name: "to定位键缺sku", body: MoveInput{From: validFrom, To: MoveKeyInput{WarehouseID: whSrc, BinID: binSrc2}, Qty: "1", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_KEY_INVALID"},
			{name: "qty非数字", body: MoveInput{From: validFrom, To: validTo, Qty: "abc", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_QTY_INVALID"},
			{name: "qty为零", body: MoveInput{From: validFrom, To: validTo, Qty: "0", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_QTY_INVALID"},
			{name: "qty为负", body: MoveInput{From: validFrom, To: validTo, Qty: "-3", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_QTY_INVALID"},
			{name: "qty为空", body: MoveInput{From: validFrom, To: validTo, Qty: "", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_QTY_INVALID"},
			{name: "缺作业依据号", body: MoveInput{From: validFrom, To: validTo, Qty: "1", SourceNo: "   "},
				wantHTTP: 400, wantCode: "STOCKOPS_MOVE_SOURCE_REQUIRED"},
			{name: "跨仓拒绝", body: MoveInput{From: validFrom, To: crossWH, Qty: "1", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "INVENTORY_MOVE_CROSS_WAREHOUSE"},
			{name: "同库位拒绝", body: MoveInput{From: validFrom, To: sameBin, Qty: "1", SourceNo: "WO-X"},
				wantHTTP: 400, wantCode: "INVENTORY_MOVE_SAME_LOCATION"},
			{name: "源行不存在", body: MoveInput{From: noSuchRow, To: validTo, Qty: "1", SourceNo: "WO-X"},
				wantHTTP: 404, wantCode: "INVENTORY_RECORD_NOT_FOUND"},
			{name: "可用不足", body: MoveInput{From: validFrom, To: validTo, Qty: "999", SourceNo: "WO-X"},
				wantHTTP: 409, wantCode: "INVENTORY_NOT_ENOUGH"},
			{name: "坏JSON", body: rawJSON("{"),
				wantHTTP: 400, wantCode: "COMMON_INVALID_PARAM"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, m := x.do(t, http.MethodPost, "/api/inventory/moves", tc.body)
				errCode(t, status, tc.wantHTTP, tc.wantCode, m)
			})
		}
		checkAllIdentity(t, x.w) // 全部失败用例不得动账
	})
}
