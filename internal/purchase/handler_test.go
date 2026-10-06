package purchase

// HTTP handler 层数据驱动表驱动测试（ask：为本域 32 条接口补齐含真实业务数据的
// 合法/非法/边界用例；严格沿用既有 fake 基建 fakerepo/fakeports/fakedb，不另起炉灶）。
//
// 装配形态与生产同路径：gin engine → RegisterRoutes（RequirePermission → handler →
// Service → fakeRepo）。auth 域中间件栈（Redis 会话/权限回源）在本包不可装配
// （snapshotWired 包内私有），认证用户以 auth 冻结上下文键直接注入 UserContext：
//   - 超管用户：RequirePermission 直通（auth/middleware.go:123-125），业务分支可达；
//   - 未认证：RequirePermission 以 401 拒绝（auth/middleware.go:114-120），单独断言；
//   - 非超管用户的权限点回源分支依赖 auth 域内部装配，包内不可达——对应"可测的
//     权限分支"以未认证 401 + service 层数据权限 fail-closed（见
//     handler_qc_putaway_test.go TestDetailEndpointsScopeFailClosed）覆盖。
//
// 质量追溯/不合格品（service_quality_trace.go）对 qualityDB 走原生 SQL，假驱动
// （fakedb_test.go fakeRows）恒返回单行 id=1——这两个接口的成功用例只断言信封结构
// 与 service 编排（record_type 恒 inspection），不断言业务数据行。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// sfAuthUserKey auth 中间件注入 UserContext 的 gin 上下文键（auth/middleware.go:26
// 未导出常量的冻结字面量；跨包只允许经 CurrentUser/WarehouseScope 读取——测试以
// 同一键注入，键名变更时本文件全部用例以 401 失败暴露，不会静默造假）。
const sfAuthUserKey = "sf_auth_user"

// ---- HTTP 测试基建 ----

// apiEnvelope 统一响应信封（api.md §2.2：{code,message,data,request_id,details}）。
type apiEnvelope struct {
	Code      json.RawMessage `json:"code"` // 成功数字 0；失败为模块命名空间错误码字符串
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	Details   json.RawMessage `json:"details,omitempty"`
	RequestID string          `json:"request_id"`
}

// codeString 信封 code 归一为字符串（成功 "0"；失败原样错误码）。
func (e apiEnvelope) codeString(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(string(e.Code))
	if strings.HasPrefix(raw, `"`) {
		var str string
		require.NoError(t, json.Unmarshal(e.Code, &str), "失败信封 code 应为字符串错误码: %s", raw)
		return str
	}
	return raw
}

// apiCall 单次请求结果（HTTP 状态 + 统一信封）。
type apiCall struct {
	status int
	env    apiEnvelope
}

// pageData 分页出参（api.md §2.1：{page,pageSize,total,items}）。
type pageData struct {
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int64           `json:"total"`
	Items    json.RawMessage `json:"items"`
}

// httpEnv HTTP 层测试环境：testEnv（service + fake 全套）+ 生产路径装配的 gin engine。
type httpEnv struct {
	*testEnv
	r    *gin.Engine
	user *auth.UserContext // nil = 未认证（RequirePermission 401）
}

// superUser 超管测试用户（超管直通权限点且数据范围 ALL——auth/middleware.go:206-208）。
func superUser() auth.UserContext {
	return auth.UserContext{UserID: 9, Username: "测试员", IsSuper: true, DataScope: auth.DataScopeAll}
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &httpEnv{testEnv: newTestEnv(t), r: gin.New()}
	u := superUser()
	h.user = &u
	api := h.r.Group("/api")
	api.Use(func(c *gin.Context) {
		if h.user != nil {
			c.Set(sfAuthUserKey, *h.user)
		}
		c.Set(response.RequestIDKey, "req-http-test")
		c.Next()
	})
	registerTestRoutes(api, h.svc)
	return h
}

// registerTestRoutes 按生产 RegisterRoutes（purchase.go:40-83）的挂接形态——同权限点
// 中间件、同 handler 纯函数——把 handler 接到测试 Service（fakeRepo 内存版）上。
// 不直接调 RegisterRoutes 的原因：其内部 NewRepository(db)（purchase.go:36）必然构造
// 真 GORM 仓储，假 gorm 驱动（fakedb_test.go）对任何查询只回固定单行，查询面会得到
// 假数据；逐条对照挂接的路由集合由 TestTestRoutesMatchRegisterRoutes 锁定与生产一致。
func registerTestRoutes(rg *gin.RouterGroup, svc *Service) {
	// —— 采购订单 ——
	rg.GET("/purchases", auth.RequirePermission(PermPurchaseList), func(c *gin.Context) { handlePOList(c, svc) })
	rg.POST("/purchases", auth.RequirePermission(PermPurchaseCreate), func(c *gin.Context) { handlePOCreate(c, svc) })
	rg.GET("/purchases/:id", auth.RequirePermission(PermPurchaseRead), func(c *gin.Context) { handlePODetail(c, svc) })
	rg.PUT("/purchases/:id", auth.RequirePermission(PermPurchaseUpdate), func(c *gin.Context) { handlePOUpdate(c, svc) })
	rg.POST("/purchases/:id/submit", auth.RequirePermission(PermPurchaseSubmit), func(c *gin.Context) { handlePOSubmit(c, svc) })
	rg.POST("/purchases/:id/approve", auth.RequirePermission(PermPurchaseApprove), func(c *gin.Context) { handlePOApprove(c, svc) })
	rg.POST("/purchases/:id/cancel", auth.RequirePermission(PermPurchaseCancel), func(c *gin.Context) { handlePOCancel(c, svc) })
	rg.POST("/purchases/:id/close", auth.RequirePermission(PermPurchaseClose), func(c *gin.Context) { handlePOClose(c, svc) })
	// —— 入库单 ——
	rg.GET("/inbounds", auth.RequirePermission(PermInboundList), func(c *gin.Context) { handleInboundList(c, svc) })
	rg.POST("/inbounds", auth.RequirePermission(PermInboundCreate), func(c *gin.Context) { handleInboundCreate(c, svc) })
	rg.GET("/inbounds/:id", auth.RequirePermission(PermInboundRead), func(c *gin.Context) { handleInboundDetail(c, svc) })
	rg.PUT("/inbounds/:id", auth.RequirePermission(PermInboundUpdate), func(c *gin.Context) { handleInboundUpdate(c, svc) })
	rg.POST("/inbounds/:id/cancel", auth.RequirePermission(PermInboundCancel), func(c *gin.Context) { handleInboundCancel(c, svc) })
	rg.POST("/inbounds/:id/close", auth.RequirePermission(PermInboundClose), func(c *gin.Context) { handleInboundClose(c, svc) })
	// —— 收货 ——
	rg.GET("/receipts", auth.RequirePermission(PermReceiptList), func(c *gin.Context) { handleReceiptList(c, svc) })
	rg.GET("/receipts/no/:no", auth.RequirePermission(PermReceiptRead), func(c *gin.Context) { handleReceiptDetail(c, svc) })
	rg.GET("/receipts/:id", auth.RequirePermission(PermReceiptRead), func(c *gin.Context) { handleReceiptDetail(c, svc) })
	rg.POST("/receipts", auth.RequirePermission(PermReceiptExecute), func(c *gin.Context) { handleReceiptConfirm(c, svc) })
	// —— 质检 ——
	rg.GET("/quality", auth.RequirePermission(PermQualityList), func(c *gin.Context) { handleQCList(c, svc) })
	rg.GET("/quality/trace", auth.RequirePermission(PermQualityList), func(c *gin.Context) { handleQCTrace(c, svc) })
	rg.GET("/quality/nonconforming", auth.RequirePermission(PermQualityList), func(c *gin.Context) { handleQCNonconforming(c, svc) })
	rg.POST("/quality", auth.RequirePermission(PermQualityCreate), func(c *gin.Context) { handleQCCreate(c, svc) })
	rg.GET("/quality/:id", auth.RequirePermission(PermQualityRead), func(c *gin.Context) { handleQCDetail(c, svc) })
	rg.POST("/quality/:id/start", auth.RequirePermission(PermQualityExecute), func(c *gin.Context) { handleQCStart(c, svc) })
	rg.POST("/quality/:id/execute", auth.RequirePermission(PermQualityExecute), func(c *gin.Context) { handleQCExecute(c, svc) })
	// —— 上架任务 ——
	rg.GET("/putaway", auth.RequirePermission(PermPutawayList), func(c *gin.Context) { handleTaskList(c, svc) })
	rg.GET("/putaway/recommend", auth.RequirePermission(PermPutawayRead), func(c *gin.Context) { handleTaskRecommend(c, svc) })
	rg.GET("/putaway/:id", auth.RequirePermission(PermPutawayRead), func(c *gin.Context) { handleTaskDetail(c, svc) })
	rg.POST("/putaway/:id/claim", auth.RequirePermission(PermPutawayClaim), func(c *gin.Context) { handleTaskClaim(c, svc) })
	rg.POST("/putaway/:id/pause", auth.RequirePermission(PermPutawayExecute), func(c *gin.Context) { handleTaskPause(c, svc) })
	rg.POST("/putaway/:id/resume", auth.RequirePermission(PermPutawayExecute), func(c *gin.Context) { handleTaskResume(c, svc) })
	rg.POST("/putaway/:id/execute", auth.RequirePermission(PermPutawayExecute), func(c *gin.Context) { handleTaskExecute(c, svc) })
}

// TestTestRoutesMatchRegisterRoutes 测试挂接与生产 RegisterRoutes 的 method+path 集合
// 必须完全一致（防两份声明漂移；权限点对应关系由 purchase.go 静态声明与
// routes_test.go TestRegisterRoutesContract 另行锁定）。
func TestTestRoutesMatchRegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prod := gin.New()
	require.NotPanics(t, func() { RegisterRoutes(prod.Group("/api"), newFakeRepo(nil).DB(), nil) })
	test := gin.New()
	registerTestRoutes(test.Group("/api"), newTestEnv(t).svc)

	collect := func(r *gin.Engine) []string {
		var out []string
		for _, rt := range r.Routes() {
			out = append(out, rt.Method+" "+rt.Path)
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, collect(prod), collect(test), "测试路由挂接必须与生产 RegisterRoutes 一致")
}

func (h *httpEnv) doReq(t *testing.T, method, path, rawBody string, headers map[string]string) apiCall {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), "响应必须是统一信封 JSON（api.md §2.2）: HTTP %d %s", w.Code, w.Body.String())
	return apiCall{status: w.Code, env: env}
}

// do 发送 JSON 请求体（body nil = 空体）。
func (h *httpEnv) do(t *testing.T, method, path string, body any) apiCall {
	t.Helper()
	raw := ""
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		raw = string(b)
	}
	return h.doReq(t, method, path, raw, nil)
}

// decodeData 解析信封 data。
func decodeData(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(raw, out), "data 解析失败: %s", string(raw))
}

// wantOK 断言成功信封：HTTP 200 + 数字 0 + request_id 透传（api.md §2.2）。
func wantOK(t *testing.T, call apiCall) {
	t.Helper()
	require.Equal(t, http.StatusOK, call.status, "body: %s", call.env.Data)
	require.Equal(t, "0", call.env.codeString(t), "body: %s", call.env.Data)
	require.Equal(t, "req-http-test", call.env.RequestID)
}

// wantFail 断言失败信封：HTTP 状态与注册错误码一致（response.Register 绑定）。
func wantFail(t *testing.T, call apiCall, code string) {
	t.Helper()
	reg, ok := response.Lookup(code)
	require.True(t, ok, "错误码未注册: %s", code)
	require.Equal(t, reg.HTTPStatus, call.status, "body: %s", string(call.env.Details))
	require.Equal(t, code, call.env.codeString(t))
}

// wantFailField 校验失败必须携带 details（api.md §4）且 details 含定位字段文本。
func wantFailField(t *testing.T, call apiCall, code, contains string) {
	t.Helper()
	wantFail(t, call, code)
	require.NotEmpty(t, call.env.Details, "校验失败必须带 details（api.md §4）")
	if contains != "" {
		require.Contains(t, string(call.env.Details), contains, "details: %s", string(call.env.Details))
	}
}

// idPath database.ID → URL 路径段十进制文本（database.ID 无 String 方法）。
func idPath(id database.ID) string { return strconv.FormatInt(id.Int64(), 10) }

// seedReceipt 直接落一条收货记录（列表/详情夹具；绕开收货服务链路）。
func (h *httpEnv) seedReceipt(t *testing.T, inboundNo string, whID int64, skuID int64) *Receipt {
	t.Helper()
	rc := &Receipt{
		ReceiptNo:   "RC-TEST-000001",
		InboundNo:   inboundNo,
		WarehouseID: whID,
		OperatorID:  9, OperatorName: "测试员",
	}
	items := []*ReceiptItem{{
		LineNo: 1, SKUID: skuID, QtyGood: qty(t, "4"), QtyRejected: qty(t, "1"),
	}}
	require.NoError(t, h.repo.InsertReceipt(context.Background(), nil, rc, items))
	return rc
}

// ---- 认证（RequirePermission 可测分支：未认证 401）----

func TestHTTPRejectsUnauthenticated(t *testing.T) {
	h := newHTTPEnv(t)
	h.user = nil
	call := h.do(t, http.MethodGet, "/api/purchases", nil)
	wantFail(t, call, response.CodeUnauthorized.Code)
}

// ---- 采购订单：GET /api/purchases（handlePOList）----

func TestHandlePOListHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	po1 := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
	h.repo.seedPO(POStatusDraft, 2, map[int64]stock.Qty{101: qty(t, "3")})
	h.repo.seedPO(POStatusApproved, 1, map[int64]stock.Qty{100: qty(t, "2")})

	cases := []struct {
		name      string
		query     string
		wantTotal int64
		wantLen   int
	}{
		{"默认分页全量", "", 3, 3},
		{"状态筛选", "?status=" + POStatusDraft, 2, 2},
		{"单号关键词精确命中", "?keyword=" + po1.PONo, 1, 1},
		{"供应商筛选命中", "?supplier_id=11", 3, 3},
		{"供应商无数据", "?supplier_id=12", 0, 0},
		{"supplier_id 为 0 视为不过滤", "?supplier_id=0", 3, 3},
		{"仓库筛选", "?warehouse_id=2", 1, 1},
		{"第二页截取", "?page=2&pageSize=2", 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/purchases"+tc.query, nil)
			wantOK(t, call)
			var pg pageData
			decodeData(t, call.env.Data, &pg)
			require.Equal(t, tc.wantTotal, pg.Total)
			var items []PurchaseOrder
			decodeData(t, pg.Items, &items)
			require.Len(t, items, tc.wantLen)
		})
	}

	t.Run("列表项冻结契约字段", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/purchases?keyword="+po1.PONo, nil)
		wantOK(t, call)
		var pg pageData
		decodeData(t, call.env.Data, &pg)
		var items []PurchaseOrder
		decodeData(t, pg.Items, &items)
		require.Len(t, items, 1)
		require.Equal(t, po1.PONo, items[0].PONo)
		require.Equal(t, POStatusDraft, items[0].Status)
		require.Equal(t, int64(11), items[0].SupplierID)
		require.Equal(t, "5.0000", items[0].TotalAmount.String(), "金额 numeric(18,4) 线格式")
	})

	for _, tc := range []struct{ name, query string }{
		{"page 为 0", "?page=0"},
		{"page 非整数", "?page=abc"},
		{"pageSize 为 0", "?pageSize=0"},
		{"pageSize 超上限", "?pageSize=101"},
		{"supplier_id 非整数", "?supplier_id=abc"},
		{"supplier_id 负数", "?supplier_id=-1"},
		{"warehouse_id 负数", "?warehouse_id=-3"},
	} {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/purchases"+tc.query, nil)
			wantFailField(t, call, response.CodeInvalidParam.Code, `"field"`)
		})
	}
}

// ---- 采购订单：POST /api/purchases（handlePOCreate）----

func TestHandlePOCreateHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	h.chk.seedSKU(103, false, false, false, false) // 停用 SKU
	ctx := context.Background()

	t.Run("合法创建（金额服务端计算、remark 去空白）", func(t *testing.T) {
		call := h.doReq(t, http.MethodPost, "/api/purchases",
			`{"supplier_id":11,"warehouse_id":1,"remark":"  紧急采购  ","items":[{"sku_id":100,"qty":"5","price":"2.5"}]}`, nil)
		wantOK(t, call)
		var created PurchaseOrder
		decodeData(t, call.env.Data, &created)
		require.True(t, strings.HasPrefix(created.PONo, "PO-"), "单号冻结规则 PO-（docnum 注册表）: %s", created.PONo)
		require.Equal(t, POStatusDraft, created.Status)
		require.Equal(t, "12.5000", created.TotalAmount.String(), "amount=qty×price 服务端计算")
		require.Equal(t, "紧急采购", created.Remark)
		rows, err := h.repo.ListPOItems(ctx, created.ID.Int64())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, 1, rows[0].LineNo, "行号从 1 连续编号")
		require.Equal(t, int64(100), rows[0].SKUID)
		require.Equal(t, "5.0000", rows[0].QtyOrdered.String())
		require.Equal(t, "12.5000", rows[0].Amount.String())
	})

	t.Run("单价缺省为 0", func(t *testing.T) {
		call := h.do(t, http.MethodPost, "/api/purchases", map[string]any{
			"supplier_id": 11, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "3"}},
		})
		wantOK(t, call)
		var created PurchaseOrder
		decodeData(t, call.env.Data, &created)
		require.Equal(t, "0.0000", created.TotalAmount.String())
	})

	t.Run("items 空数组", func(t *testing.T) {
		// validator required 对非 nil 空 slice 放行，空数组由 service 层拒绝
		// （ErrPOLinesRequired 为域错误码，不带 details）。
		call := h.doReq(t, http.MethodPost, "/api/purchases",
			`{"supplier_id":11,"warehouse_id":1,"items":[]}`, nil)
		wantFail(t, call, ErrPOLinesRequired.Code)
	})

	cases := []struct {
		name     string
		body     string
		wantCode string
		contains string // details 应包含的定位文本；空 = 只断言非空 details
	}{
		{"缺 supplier_id", `{"warehouse_id":1,"items":[{"sku_id":100,"qty":"1"}]}`, response.CodeInvalidParam.Code, response.BindErrorReason},
		{"缺 items", `{"supplier_id":11,"warehouse_id":1}`, response.CodeInvalidParam.Code, response.BindErrorReason},
		{"非法 JSON", `{`, response.CodeInvalidParam.Code, response.BindErrorReason},
		{"SKU 不存在", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":999,"qty":"1"}]}`, response.CodeInvalidParam.Code, "sku_id"},
		{"SKU 已停用", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":103,"qty":"1"}]}`, response.CodeInvalidParam.Code, "已停用"},
		{"SKU 明细重复", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":100,"qty":"1"},{"sku_id":100,"qty":"2"}]}`, ErrPOLineDuplicated.Code, "sku_id"},
		{"qty 为 0", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":100,"qty":"0"}]}`, response.CodeInvalidParam.Code, "items[0].qty"},
		{"qty 负数", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":100,"qty":"-1"}]}`, response.CodeInvalidParam.Code, "items[0].qty"},
		{"price 负数", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":100,"qty":"1","price":"-0.5"}]}`, response.CodeInvalidParam.Code, "items[0].price"},
		{"金额列溢出", `{"supplier_id":11,"warehouse_id":1,"items":[{"sku_id":100,"qty":"99999999999999.9999","price":"99999999999999.9999"}]}`, response.CodeInvalidParam.Code, "items[0].price"},
		{"供应商不存在", `{"supplier_id":12,"warehouse_id":1,"items":[{"sku_id":100,"qty":"1"}]}`, response.CodeInvalidParam.Code, "supplier_id"},
		{"仓库不存在", `{"supplier_id":11,"warehouse_id":99,"items":[{"sku_id":100,"qty":"1"}]}`, response.CodeInvalidParam.Code, "warehouse_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.doReq(t, http.MethodPost, "/api/purchases", tc.body, nil)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}
}

// ---- 采购订单：GET /api/purchases/:id（handlePODetail）----

func TestHandlePODetailHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})

	t.Run("详情含明细", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/purchases/"+idPath(po.ID), nil)
		wantOK(t, call)
		var detail PODetail
		decodeData(t, call.env.Data, &detail)
		require.Equal(t, po.PONo, detail.Order.PONo)
		require.Equal(t, POStatusDraft, detail.Order.Status)
		require.Len(t, detail.Items, 1)
		require.Equal(t, "5.0000", detail.Items[0].QtyOrdered.String())
		require.Equal(t, 1, detail.Items[0].LineNo)
	})

	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/purchases/999", nil), ErrPONotFound.Code)
	})
	for _, id := range []string{"abc", "0", "-1"} {
		t.Run("非法路径 id "+id, func(t *testing.T) {
			wantFailField(t, h.do(t, http.MethodGet, "/api/purchases/"+id, nil), response.CodeInvalidParam.Code, `"id"`)
		})
	}
}

// ---- 采购订单：PUT /api/purchases/:id（handlePOUpdate）----

func TestHandlePOUpdateHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()

	t.Run("草稿改供应商与备注", func(t *testing.T) {
		po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPut, "/api/purchases/"+idPath(po.ID), map[string]any{
			"supplier_id": 11, "remark": "改备注",
		})
		wantOK(t, call)
		require.Equal(t, "改备注", h.repo.po(po.ID.Int64()).Remark)
		require.Len(t, h.repo.poItems[po.ID.Int64()], 1, "未传 items 不替换明细")
	})

	t.Run("草稿整单替换明细", func(t *testing.T) {
		po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPut, "/api/purchases/"+idPath(po.ID), map[string]any{
			"items": []map[string]any{{"sku_id": 101, "qty": "3", "price": "1"}},
		})
		wantOK(t, call)
		rows, err := h.repo.ListPOItems(ctx, po.ID.Int64())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, int64(101), rows[0].SKUID, "明细整单替换")
	})

	cases := []struct {
		name     string
		status   string
		body     map[string]any
		wantCode string
		contains string
	}{
		{"非草稿拒绝修改", POStatusApproved, map[string]any{"remark": "x"}, ErrPOStatusNotAllowed.Code, ""},
		{"供应商显式 0 拒绝", POStatusDraft, map[string]any{"supplier_id": 0}, response.CodeInvalidParam.Code, "supplier_id"},
		{"仓库显式负数拒绝", POStatusDraft, map[string]any{"warehouse_id": -1}, response.CodeInvalidParam.Code, "warehouse_id"},
		{"供应商不存在", POStatusDraft, map[string]any{"supplier_id": 12}, response.CodeInvalidParam.Code, "supplier_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			po := h.repo.seedPO(tc.status, 1, map[int64]stock.Qty{100: qty(t, "5")})
			call := h.do(t, http.MethodPut, "/api/purchases/"+idPath(po.ID), tc.body)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}

	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPut, "/api/purchases/999", map[string]any{"remark": "x"}), ErrPONotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPut, "/api/purchases/abc", map[string]any{}), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 采购订单：POST /api/purchases/:id/submit（handlePOSubmit）----

func TestHandlePOSubmitHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("草稿提交成功落审批记录", func(t *testing.T) {
		po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/submit", nil)
		wantOK(t, call)
		var got PurchaseOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, POStatusPendingApproval, got.Status)
		approvals := h.repo.approvalsFor(po.PONo)
		require.Len(t, approvals, 1)
		require.Equal(t, ApprovalActionSubmit, approvals[0].Action, "SUBMIT 审批记录与业务同事务（business-flow §12.2）")
	})

	t.Run("待审核重复提交 409", func(t *testing.T) {
		po := h.repo.seedPO(POStatusPendingApproval, 1, map[int64]stock.Qty{100: qty(t, "5")})
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/submit", nil), ErrPOStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/999/submit", nil), ErrPONotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/purchases/0/submit", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 采购订单：POST /api/purchases/:id/approve（handlePOApprove）----

func TestHandlePOApproveHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("审核通过", func(t *testing.T) {
		po := h.repo.seedPO(POStatusPendingApproval, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/approve", map[string]any{"approved": true})
		wantOK(t, call)
		var got PurchaseOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, POStatusApproved, got.Status)
		approvals := h.repo.approvalsFor(po.PONo)
		require.Len(t, approvals, 1)
		require.Equal(t, ApprovalActionApprove, approvals[0].Action)
	})

	t.Run("驳回带意见回草稿", func(t *testing.T) {
		po := h.repo.seedPO(POStatusPendingApproval, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/approve",
			map[string]any{"approved": false, "opinion": "单价不符"})
		wantOK(t, call)
		var got PurchaseOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, POStatusDraft, got.Status)
		approvals := h.repo.approvalsFor(po.PONo)
		require.Len(t, approvals, 1)
		require.Equal(t, ApprovalActionReject, approvals[0].Action)
		require.Equal(t, "单价不符", approvals[0].Opinion, "审批意见入审批记录（business-flow §12.2）")
	})

	cases := []struct {
		name     string
		body     map[string]any
		status   string
		wantCode string
	}{
		{"空体拒绝", map[string]any{}, POStatusPendingApproval, response.CodeInvalidParam.Code},
		{"驳回无意见拒绝（绑定层）", map[string]any{"approved": false}, POStatusPendingApproval, response.CodeInvalidParam.Code},
		{"草稿不能审核", map[string]any{"approved": true}, POStatusDraft, ErrPOStatusNotAllowed.Code},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			po := h.repo.seedPO(tc.status, 1, map[int64]stock.Qty{100: qty(t, "5")})
			wantFail(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/approve", tc.body), tc.wantCode)
		})
	}
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/999/approve", map[string]any{"approved": true}), ErrPONotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/purchases/xyz/approve", map[string]any{"approved": true}),
			response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 采购订单：POST /api/purchases/:id/cancel（handlePOCancel）----

func TestHandlePOCancelHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()

	t.Run("空体取消草稿（原因可选）", func(t *testing.T) {
		po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/cancel", nil)
		wantOK(t, call)
		var got PurchaseOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, POStatusCancelled, got.Status)
	})

	t.Run("带原因取消落审批记录", func(t *testing.T) {
		po := h.repo.seedPO(POStatusPendingApproval, 1, map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/cancel", map[string]any{"reason": "预算砍掉"})
		wantOK(t, call)
		approvals := h.repo.approvalsFor(po.PONo)
		require.Len(t, approvals, 1)
		require.Equal(t, ApprovalActionCancel, approvals[0].Action)
		require.Equal(t, "预算砍掉", approvals[0].Opinion)
	})

	t.Run("有收货拒绝取消 409", func(t *testing.T) {
		po := h.repo.seedPO(POStatusApproved, 1, map[int64]stock.Qty{100: qty(t, "10")})
		items, err := h.repo.ListPOItems(ctx, po.ID.Int64())
		require.NoError(t, err)
		_, err = h.repo.AddPOItemReceived(ctx, nil, items[0].ID.Int64(), qty(t, "2"), 0, 9)
		require.NoError(t, err)
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/cancel", nil), ErrPOHasReceipts.Code)
	})

	t.Run("已完成不能取消 409", func(t *testing.T) {
		po := h.repo.seedPO(POStatusCompleted, 1, map[int64]stock.Qty{100: qty(t, "5")})
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/cancel", nil), ErrPOStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/999/cancel", nil), ErrPONotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/purchases/-1/cancel", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 采购订单：POST /api/purchases/:id/close（handlePOClose）----

func TestHandlePOCloseHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()
	seedPartial := func() *PurchaseOrder {
		po := h.repo.seedPO(POStatusPartialReceived, 1, map[int64]stock.Qty{100: qty(t, "10")})
		items, _ := h.repo.ListPOItems(ctx, po.ID.Int64())
		_, err := h.repo.AddPOItemReceived(ctx, nil, items[0].ID.Int64(), qty(t, "4"), 0, 9)
		require.NoError(t, err)
		return po
	}

	t.Run("差额关闭无原因拒绝", func(t *testing.T) {
		po := seedPartial()
		wantFailField(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/close", map[string]any{}),
			response.CodeInvalidParam.Code, "reason")
	})
	t.Run("差额关闭带原因成功", func(t *testing.T) {
		po := seedPartial()
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/close", map[string]any{"reason": "余量取消采购"})
		wantOK(t, call)
		var got PurchaseOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, POStatusCompleted, got.Status)
	})
	t.Run("上架全部完成无原因关闭", func(t *testing.T) {
		po := h.repo.seedPO(POStatusReceivedAll, 1, map[int64]stock.Qty{100: qty(t, "10")})
		items, _ := h.repo.ListPOItems(ctx, po.ID.Int64())
		_, err := h.repo.AddPOItemReceived(ctx, nil, items[0].ID.Int64(), qty(t, "10"), 0, 9)
		require.NoError(t, err)
		_, err = h.repo.AddPOItemPutaway(ctx, nil, items[0].ID.Int64(), qty(t, "10"), 9)
		require.NoError(t, err)
		call := h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/close", map[string]any{})
		wantOK(t, call)
	})
	t.Run("草稿不能关闭 409", func(t *testing.T) {
		po := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/"+idPath(po.ID)+"/close", map[string]any{"reason": "x"}), ErrPOStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/purchases/999/close", map[string]any{"reason": "x"}), ErrPONotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/purchases/abc/close", map[string]any{"reason": "x"}),
			response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 入库单：GET /api/inbounds（handleInboundList）----

func TestHandleInboundListHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	h.repo.seedInbound(InboundStatusDraft, 1, SourceTypePurchase, "PO-1", map[int64]stock.Qty{100: qty(t, "5")})
	h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{101: qty(t, "2")})
	h.repo.seedInbound(InboundStatusAwaitingQC, 2, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "1")})

	cases := []struct {
		name      string
		query     string
		wantTotal int64
		wantLen   int
	}{
		{"默认分页全量", "", 3, 3},
		{"状态筛选", "?status=" + InboundStatusReceiving, 1, 1},
		{"来源类型筛选", "?source_type=" + SourceTypePurchase, 1, 1},
		{"来源单号筛选", "?source_no=PO-1", 1, 1},
		{"仓库筛选", "?warehouse_id=2", 1, 1},
		{"第二页截取", "?page=2&pageSize=2", 3, 1},
		{"条件组合无命中", "?status=COMPLETED&warehouse_id=2", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/inbounds"+tc.query, nil)
			wantOK(t, call)
			var pg pageData
			decodeData(t, call.env.Data, &pg)
			require.Equal(t, tc.wantTotal, pg.Total)
			var items []InboundOrder
			decodeData(t, pg.Items, &items)
			require.Len(t, items, tc.wantLen)
		})
	}
	for _, tc := range []struct{ name, query string }{
		{"pageSize 为 0", "?pageSize=0"},
		{"warehouse_id 非整数", "?warehouse_id=abc"},
	} {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			wantFailField(t, h.do(t, http.MethodGet, "/api/inbounds"+tc.query, nil), response.CodeInvalidParam.Code, `"field"`)
		})
	}
}

// ---- 入库单：POST /api/inbounds（handleInboundCreate）----

func TestHandleInboundCreateHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	h.chk.seedSKU(103, false, false, false, false)
	po := h.repo.seedPO(POStatusApproved, 1, map[int64]stock.Qty{100: qty(t, "5")})
	draftPO := h.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})

	t.Run("其他入库合法创建（来源号可空）", func(t *testing.T) {
		call := h.do(t, http.MethodPost, "/api/inbounds", map[string]any{
			"source_type": SourceTypeOther, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "5"}},
		})
		wantOK(t, call)
		var created InboundOrder
		decodeData(t, call.env.Data, &created)
		require.True(t, strings.HasPrefix(created.InboundNo, "IN-"), "单号冻结规则 IN-: %s", created.InboundNo)
		require.Equal(t, InboundStatusDraft, created.Status)
		rows, err := h.repo.ListInboundItems(context.Background(), created.ID.Int64())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "5.0000", rows[0].Qty.String())
	})

	t.Run("采购入库引用已审核订单", func(t *testing.T) {
		call := h.do(t, http.MethodPost, "/api/inbounds", map[string]any{
			"source_type": SourceTypePurchase, "source_no": po.PONo, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "5"}},
		})
		wantOK(t, call)
		var created InboundOrder
		decodeData(t, call.env.Data, &created)
		require.Equal(t, po.PONo, created.SourceNo, "采购入库必须回填采购订单号（api.md §4）")
	})

	cases := []struct {
		name     string
		body     map[string]any
		wantCode string
		contains string
	}{
		{"采购入库缺来源号", map[string]any{"source_type": SourceTypePurchase, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}}}, response.CodeInvalidParam.Code, "source_no"},
		{"来源订单不存在", map[string]any{"source_type": SourceTypePurchase, "source_no": "PO-NOPE", "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}}}, ErrInboundSourceInvalid.Code, "source_no"},
		{"来源订单未审核", map[string]any{"source_type": SourceTypePurchase, "source_no": draftPO.PONo, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}}}, ErrInboundSourceInvalid.Code, "未审核"},
		{"入库仓与订单收货仓不一致", map[string]any{"source_type": SourceTypePurchase, "source_no": po.PONo, "warehouse_id": 2,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}}}, response.CodeInvalidParam.Code, "warehouse_id"},
		{"来源类型非法", map[string]any{"source_type": "RETURN", "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}}}, response.CodeInvalidParam.Code, "source_type"},
		{"缺 items", map[string]any{"source_type": SourceTypeOther, "warehouse_id": 1}, response.CodeInvalidParam.Code, response.BindErrorReason},
		{"SKU 明细重复", map[string]any{"source_type": SourceTypeOther, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "1"}, {"sku_id": 100, "qty": "2"}}}, ErrPOLineDuplicated.Code, "入库单明细 SKU 重复"},
		{"SKU 已停用", map[string]any{"source_type": SourceTypeOther, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 103, "qty": "1"}}}, response.CodeInvalidParam.Code, "已停用"},
		{"qty 为 0", map[string]any{"source_type": SourceTypeOther, "warehouse_id": 1,
			"items": []map[string]any{{"sku_id": 100, "qty": "0"}}}, response.CodeInvalidParam.Code, "items[0].qty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodPost, "/api/inbounds", tc.body)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}
}

// ---- 入库单：GET /api/inbounds/:id（handleInboundDetail）----

func TestHandleInboundDetailHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})

	t.Run("详情含明细", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/inbounds/"+idPath(in.ID), nil)
		wantOK(t, call)
		var detail InboundDetail
		decodeData(t, call.env.Data, &detail)
		require.Equal(t, in.InboundNo, detail.Order.InboundNo)
		require.Len(t, detail.Items, 1)
		require.Equal(t, "5.0000", detail.Items[0].Qty.String())
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/inbounds/999", nil), ErrInboundNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/inbounds/abc", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 入库单：PUT /api/inbounds/:id（handleInboundUpdate）----

func TestHandleInboundUpdateHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()

	t.Run("草稿改备注与明细", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPut, "/api/inbounds/"+idPath(in.ID), map[string]any{
			"remark": "改备注",
			"items":  []map[string]any{{"sku_id": 101, "qty": "3"}},
		})
		wantOK(t, call)
		require.Equal(t, "改备注", h.repo.inbounds[in.ID.Int64()].Remark)
		rows, err := h.repo.ListInboundItems(ctx, in.ID.Int64())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, int64(101), rows[0].SKUID, "明细整单替换")
	})

	t.Run("非草稿拒绝修改 409", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		wantFail(t, h.do(t, http.MethodPut, "/api/inbounds/"+idPath(in.ID), map[string]any{"remark": "x"}), ErrInboundStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPut, "/api/inbounds/999", map[string]any{"remark": "x"}), ErrInboundNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPut, "/api/inbounds/0", map[string]any{}), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 入库单：POST /api/inbounds/:id/cancel（handleInboundCancel）----

func TestHandleInboundCancelHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("空体取消草稿", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/cancel", nil)
		wantOK(t, call)
		var got InboundOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, InboundStatusCancelled, got.Status)
	})
	t.Run("收货中不能取消 409", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		wantFail(t, h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/cancel", nil), ErrInboundStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/inbounds/999/cancel", nil), ErrInboundNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/inbounds/abc/cancel", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 入库单：POST /api/inbounds/:id/close（handleInboundClose）----

func TestHandleInboundCloseHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("差额关闭联动取消待领取任务", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
		task := h.repo.seedTask(in.InboundNo, 100, 0, qty(t, "2"), FromStateAvailable, TaskStatusPending, 1, 101)
		call := h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/close", map[string]any{"reason": "余量不再到货"})
		wantOK(t, call)
		var got InboundOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, InboundStatusClosed, got.Status)
		require.Equal(t, TaskStatusCancelled, h.repo.tasks[task.ID.Int64()].Status, "待领取任务联动取消（plan §6.3）")
	})

	t.Run("进行中任务阻断关闭 409", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
		h.repo.seedTask(in.InboundNo, 100, 0, qty(t, "2"), FromStateAvailable, TaskStatusInProgress, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/close", map[string]any{"reason": "x"}),
			ErrInboundHasActiveTasks.Code)
	})
	t.Run("缺 reason 绑定拒绝", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
		wantFailField(t, h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/close", map[string]any{}),
			response.CodeInvalidParam.Code, response.BindErrorReason)
	})
	t.Run("草稿不能差额关闭 409", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
		wantFail(t, h.do(t, http.MethodPost, "/api/inbounds/"+idPath(in.ID)+"/close", map[string]any{"reason": "x"}), ErrInboundStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/inbounds/999/close", map[string]any{"reason": "x"}), ErrInboundNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/inbounds/-2/close", map[string]any{"reason": "x"}),
			response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 收货：GET /api/receipts（handleReceiptList）----

func TestHandleReceiptListHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
	rc := h.seedReceipt(t, in.InboundNo, 1, 100)

	cases := []struct {
		name      string
		query     string
		wantTotal int64
		wantLen   int
	}{
		{"默认分页全量", "", 1, 1},
		{"入库单号筛选", "?inbound_no=" + in.InboundNo, 1, 1},
		{"入库单号无命中", "?inbound_no=IN-NOPE", 0, 0},
		{"收货单号精确", "?receipt_no=" + rc.ReceiptNo, 1, 1},
		{"仓库无命中", "?warehouse_id=2", 0, 0},
		{"第二页为空", "?page=2", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/receipts"+tc.query, nil)
			wantOK(t, call)
			var pg pageData
			decodeData(t, call.env.Data, &pg)
			require.Equal(t, tc.wantTotal, pg.Total)
			var items []Receipt
			decodeData(t, pg.Items, &items)
			require.Len(t, items, tc.wantLen)
		})
	}
	t.Run("非法参数 warehouse_id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/receipts?warehouse_id=abc", nil), response.CodeInvalidParam.Code, `"field"`)
	})
}

// ---- 收货：POST /api/receipts（handleReceiptConfirm）----

func TestHandleReceiptConfirmHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()
	newChain := func() (*PurchaseOrder, *InboundOrder) {
		po := h.repo.seedPO(POStatusApproved, 1, map[int64]stock.Qty{100: qty(t, "10")})
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypePurchase, po.PONo, map[int64]stock.Qty{100: qty(t, "10")})
		return po, in
	}

	t.Run("合法部分收货（免检直通）", func(t *testing.T) {
		po, in := newChain()
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 100, "qty_good": "4", "require_inspect": false}},
		})
		wantOK(t, call)
		var res ReceiptResult
		decodeData(t, call.env.Data, &res)
		require.True(t, strings.HasPrefix(res.ReceiptNo, "RC-"), "收货单号冻结规则: %s", res.ReceiptNo)
		require.False(t, res.Replay)
		require.Len(t, res.PutawayTasks, 1, "免检行生成上架任务")
		require.Equal(t, InboundStatusReceiving, res.InboundStatus)
		require.Equal(t, POStatusPartialReceived, res.POStatus, "采购订单推进部分到货（plan §6.1）")
		items, err := h.repo.ListInboundItems(ctx, in.ID.Int64())
		require.NoError(t, err)
		require.Equal(t, "4.0000", items[0].QtyReceived.String())
		_ = po
	})

	t.Run("Idempotency-Key 头优先于请求体键", func(t *testing.T) {
		_, in := newChain()
		body := map[string]any{
			"inbound_no": in.InboundNo, "idempotency_key": "op-http-body",
			"lines": []map[string]any{{"sku_id": 100, "qty_good": "2", "require_inspect": false}},
		}
		call := h.doReq(t, http.MethodPost, "/api/receipts", mustJSON(t, body), map[string]string{"Idempotency-Key": "op-http-head"})
		wantOK(t, call)
		var first ReceiptResult
		decodeData(t, call.env.Data, &first)
		rcHead, err := h.repo.FindReceiptByIdempotencyKey(ctx, "op-http-head")
		require.NoError(t, err)
		require.NotNil(t, rcHead)
		require.NotNil(t, rcHead.IdempotencyKey)
		require.Equal(t, "op-http-head", *rcHead.IdempotencyKey, "请求头幂等键优先落库（plan §7）")
		rcBody, err := h.repo.FindReceiptByIdempotencyKey(ctx, "op-http-body")
		require.NoError(t, err)
		require.Nil(t, rcBody, "请求体键不得落库（头优先）")

		replay := h.doReq(t, http.MethodPost, "/api/receipts", mustJSON(t, body), map[string]string{"Idempotency-Key": "op-http-head"})
		wantOK(t, replay)
		var again ReceiptResult
		decodeData(t, replay.env.Data, &again)
		require.True(t, again.Replay, "同幂等键重放返回既有结果")
		require.Equal(t, first.ReceiptNo, again.ReceiptNo)
	})

	t.Run("超量收货 400 且整体回滚", func(t *testing.T) {
		_, in := newChain()
		before := len(h.repo.receipts)
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 100, "qty_good": "11"}},
		})
		wantFail(t, call, ErrOverReceipt.Code)
		require.Len(t, h.repo.receipts, before, "任何步骤失败整体回滚（architecture.md §4）")
		items, err := h.repo.ListInboundItems(ctx, in.ID.Int64())
		require.NoError(t, err)
		require.Equal(t, "0.0000", items[0].QtyReceived.String())
	})

	cases := []struct {
		name     string
		body     map[string]any
		wantCode string
		contains string
	}{
		{"缺 inbound_no", map[string]any{"lines": []map[string]any{{"sku_id": 100, "qty_good": "1"}}},
			response.CodeInvalidParam.Code, response.BindErrorReason},
		{"缺 lines", map[string]any{"inbound_no": "IN-TEST-000001"},
			response.CodeInvalidParam.Code, response.BindErrorReason},
		{"入库单不存在", map[string]any{"inbound_no": "IN-NOPE", "lines": []map[string]any{{"sku_id": 100, "qty_good": "1"}}},
			ErrInboundNotFound.Code, "inbound_no"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodPost, "/api/receipts", tc.body)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}

	t.Run("合格与拒收合计为 0", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 100, "qty_good": "0"}},
		})
		wantFailField(t, call, ErrReceiptQtyInvalid.Code, "line")
	})
	t.Run("SKU 不在入库单明细", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 999, "qty_good": "1"}},
		})
		wantFailField(t, call, ErrReceiptInboundLineUnknown.Code, "sku_id")
	})

	t.Run("待质检状态拒绝收货 409", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusAwaitingQC, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 100, "qty_good": "1"}},
		})
		wantFail(t, call, ErrInboundStatusNotAllowed.Code)
	})

	t.Run("批次管理 SKU 必须采集批次号", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{101: qty(t, "5")})
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 101, "qty_good": "5"}},
		})
		wantFail(t, call, ErrBatchRequired.Code)
	})

	t.Run("序列号件数与合格数量不一致", func(t *testing.T) {
		in := h.repo.seedInbound(InboundStatusDraft, 1, SourceTypeOther, "", map[int64]stock.Qty{102: qty(t, "2")})
		call := h.do(t, http.MethodPost, "/api/receipts", map[string]any{
			"inbound_no": in.InboundNo,
			"lines":      []map[string]any{{"sku_id": 102, "qty_good": "2", "serials": []string{"SN-0001"}}},
		})
		wantFail(t, call, ErrSerialQtyMismatch.Code)
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// ---- 收货：GET /api/receipts/no/:no 与 GET /api/receipts/:id（handleReceiptDetail）----

func TestHandleReceiptDetailHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	in := h.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
	rc := h.seedReceipt(t, in.InboundNo, 1, 100)

	t.Run("按单号查询含明细", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/receipts/no/"+rc.ReceiptNo, nil)
		wantOK(t, call)
		var detail ReceiptDetail
		decodeData(t, call.env.Data, &detail)
		require.Equal(t, rc.ReceiptNo, detail.Receipt.ReceiptNo)
		require.Equal(t, in.InboundNo, detail.Receipt.InboundNo)
		require.Len(t, detail.Items, 1)
		require.Equal(t, "4.0000", detail.Items[0].QtyGood.String())
		require.Equal(t, "1.0000", detail.Items[0].QtyRejected.String(), "拒收单独记录（§2.3）")
	})

	t.Run("按 ID 查询", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/receipts/"+idPath(rc.ID), nil)
		wantOK(t, call)
		var detail ReceiptDetail
		decodeData(t, call.env.Data, &detail)
		require.Equal(t, rc.ReceiptNo, detail.Receipt.ReceiptNo)
	})

	t.Run("单号不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/receipts/no/RC-NOPE", nil), ErrReceiptNotFound.Code)
	})
	t.Run("ID 不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/receipts/999", nil), ErrReceiptNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/receipts/abc", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}
