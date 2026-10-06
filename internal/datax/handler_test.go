package datax

// 数据中心 HTTP handler 层表驱动测试（gin + httptest；不依赖 PostgreSQL/Redis/网络）：
//   - 18 条端点全部注册且全部挂 RequirePermission（未认证 401 统一信封）；
//   - 参数绑定（路径 :id / 分页 page/pageSize / JSON body / multipart 字段）；
//   - 真实业务数据链路：上传→校验→预览→错误文件→确认→列表/任务卡，导出创建→执行→
//     产物下载，文件中心上传→列表→下载/预览/删除；
//   - 统一信封形态（api.md §2：成功 code=0 数字；失败 code 为注册错误码字符串）。
//
// 认证上下文注入 gin 键 sf_auth_user（internal/auth/middleware.go:26 ctxUserKey 冻结
// 字面量）+ IsSuper 超管走 RequirePermission 直通路径（internal/auth/middleware.go:123）。
// 非超管会触达 auth 全局装配态（snapshotWired，internal/auth/config.go:205）——属 auth
// 域装配行为、单测环境无法合法装配，故 FileScope 收口的细粒度规则不在 HTTP 层重复
// （service_file_test.go TestFileScopeVisibility 已覆盖）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/auth"
)

// sfAuthUserKey 认证上下文 gin 键（internal/auth/middleware.go:26 ctxUserKey 冻结字面量）。
const sfAuthUserKey = "sf_auth_user"

// hSuper 超管认证上下文（scopeOf → (true, nil)，FileScope{All:true}——
// internal/auth/middleware.go:207）。
func hSuper() auth.UserContext {
	return auth.UserContext{UserID: 42, Username: "tester", IsSuper: true, DataScope: auth.DataScopeAll}
}

// hEngine 装配 gin 引擎 + datax 路由。u 为 nil 时不注入认证上下文（401 用例）。
func hEngine(t *testing.T, e *env, u *auth.UserContext) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		if u != nil {
			c.Set(sfAuthUserKey, *u)
		}
		c.Next()
	})
	RegisterRoutes(api, e.svc)
	return r
}

// ---- HTTP 请求/信封 helper ----

func hDo(t *testing.T, r *gin.Engine, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func hGet(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	return hDo(t, r, http.MethodGet, path, nil, "")
}

func hPostJSON(t *testing.T, r *gin.Engine, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	if raw, ok := payload.(string); ok {
		// 原始字符串载荷：构造非法 JSON body（参数绑定失败用例）。
		return hDo(t, r, http.MethodPost, path, strings.NewReader(raw), "application/json")
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(mustJSON(t, payload))
	}
	return hDo(t, r, http.MethodPost, path, body, "application/json")
}

// hField/hFilePart multipart 表单字段与文件部件（保序写入）。
type hField struct{ name, value string }

type hFilePart struct {
	fileName string
	content  []byte
}

// hUpload 构造 multipart 请求（file 部件名固定 "file"——handler.go uploadImport/
// uploadFile 的 c.FormFile("file") 契约）；fp 为 nil 时不上传文件。
func hUpload(t *testing.T, r *gin.Engine, path string, fields []hField, fp *hFilePart) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			t.Fatalf("写表单字段 %s 失败: %v", f.name, err)
		}
	}
	if fp != nil {
		part, err := mw.CreateFormFile("file", fp.fileName)
		if err != nil {
			t.Fatalf("构造文件部件失败: %v", err)
		}
		if _, err := part.Write(fp.content); err != nil {
			t.Fatalf("写文件部件失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	return hDo(t, r, http.MethodPost, path, &buf, mw.FormDataContentType())
}

// hEnvelope 统一信封（api.md §2：成功 code=0 数字，失败 code 为错误码字符串）。
type hEnvelope struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	Details any    `json:"details,omitempty"`
}

func hDecode(t *testing.T, rec *httptest.ResponseRecorder) hEnvelope {
	t.Helper()
	var env hEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应非统一信封 JSON: %v body=%s", err, rec.Body.String())
	}
	return env
}

// hRequireOK 断言成功信封（HTTP 200 + code=0 数字）。
func hRequireOK(t *testing.T, rec *httptest.ResponseRecorder) hEnvelope {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	env := hDecode(t, rec)
	if n, ok := env.Code.(float64); !ok || n != 0 {
		t.Fatalf("期望 code=0（数字），得到 %v: %s", env.Code, rec.Body.String())
	}
	return env
}

// hRequireErrCode 断言失败信封（HTTP 状态 + 字符串错误码）。
func hRequireErrCode(t *testing.T, rec *httptest.ResponseRecorder, wantHTTP int, wantCode string) {
	t.Helper()
	if rec.Code != wantHTTP {
		t.Fatalf("期望 HTTP %d，得到 %d: %s", wantHTTP, rec.Code, rec.Body.String())
	}
	env := hDecode(t, rec)
	if code, ok := env.Code.(string); !ok || code != wantCode {
		t.Fatalf("期望错误码 %s，得到 %v: %s", wantCode, env.Code, rec.Body.String())
	}
}

// hData 把信封 data 再绑定到具体 DTO（类型化断言用）。
func hData(t *testing.T, env hEnvelope, out any) {
	t.Helper()
	b, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("data 重序列化失败: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("data 绑定 %T 失败: %v（%s）", out, err, b)
	}
}

// hPage 统一分页结构（api.md §2.1：{page,pageSize,total,items}）。
type hPage struct {
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int64           `json:"total"`
	Items    json.RawMessage `json:"items"`
}

// hItems 把分页信封的 items 绑定到行类型。
func hItems(t *testing.T, env hEnvelope, out any) hPage {
	t.Helper()
	var p hPage
	hData(t, env, &p)
	if err := json.Unmarshal(p.Items, out); err != nil {
		t.Fatalf("items 绑定 %T 失败: %v", out, err)
	}
	return p
}

// handlerRoutes 19 条端点：path 为请求形态、route 为 gin 注册形态
// （handler.go RegisterRoutes:106-133 冻结端点集；retry-failed 为效率层一期 §2.8 增量）。
var handlerRoutes = []struct{ method, path, route string }{
	{http.MethodGet, "/api/imports", "GET /api/imports"},
	{http.MethodGet, "/api/imports/templates", "GET /api/imports/templates"},
	{http.MethodGet, "/api/imports/templates/PRODUCT", "GET /api/imports/templates/:type"},
	{http.MethodPost, "/api/imports", "POST /api/imports"},
	{http.MethodPost, "/api/imports/1/validate", "POST /api/imports/:id/validate"},
	{http.MethodGet, "/api/imports/1/preview", "GET /api/imports/:id/preview"},
	{http.MethodPost, "/api/imports/1/confirm", "POST /api/imports/:id/confirm"},
	{http.MethodPost, "/api/imports/1/retry-failed", "POST /api/imports/:id/retry-failed"},
	{http.MethodGet, "/api/imports/1/error-file", "GET /api/imports/:id/error-file"},
	{http.MethodGet, "/api/exports", "GET /api/exports"},
	{http.MethodPost, "/api/exports", "POST /api/exports"},
	{http.MethodGet, "/api/exports/1/file", "GET /api/exports/:id/file"},
	{http.MethodGet, "/api/files", "GET /api/files"},
	{http.MethodPost, "/api/files", "POST /api/files"},
	{http.MethodGet, "/api/files/1/download", "GET /api/files/:id/download"},
	{http.MethodGet, "/api/files/1/preview", "GET /api/files/:id/preview"},
	{http.MethodDelete, "/api/files/1", "DELETE /api/files/:id"},
	{http.MethodGet, "/api/data-tasks/import/1", "GET /api/data-tasks/import/:id"},
	{http.MethodGet, "/api/data-tasks/export/1", "GET /api/data-tasks/export/:id"},
}

// TestHandlerRoutesMounted 18 条端点全部注册（plan §6.2/§6.3/§6.4 冻结端点集；
// /api/imports/templates 静态段与 /api/imports/:id 参数段共存不冲突——gin 静态优先）。
func TestHandlerRoutesMounted(t *testing.T) {
	e := newEnv(t)
	u := hSuper()
	r := hEngine(t, e, &u)
	paths := map[string]bool{}
	for _, ri := range r.Routes() {
		paths[ri.Method+" "+ri.Path] = true
	}
	for _, want := range handlerRoutes {
		if !paths[want.route] {
			t.Fatalf("路由 %s 未注册（已注册 %d 条）", want.route, len(paths))
		}
	}
	// 静态优先实证：GET /api/imports/templates 命中模板清单（code=0）而非落入
	// /api/imports/:id 族（后者该形态无对应路由）。
	hRequireOK(t, hGet(t, r, "/api/imports/templates"))
}

// TestHandlerRoutesRequireAuth 全部端点未认证 → 401 COMMON_UNAUTHORIZED（permission.md §2：
// 接口声明权限点，中间件统一校验；internal/auth/middleware.go:116-121）。
func TestHandlerRoutesRequireAuth(t *testing.T) {
	e := newEnv(t)
	r := hEngine(t, e, nil)
	for _, tc := range handlerRoutes {
		t.Run(tc.method+" "+tc.route, func(t *testing.T) {
			rec := hDo(t, r, tc.method, tc.path, nil, "")
			hRequireErrCode(t, rec, http.StatusUnauthorized, "COMMON_UNAUTHORIZED")
		})
	}
}

// TestImportCenterHandler 导入中心 HTTP 链路（excel §1.2 向导全流程 + 缺口接口）：
// 上传解析 → 列表（筛选/分页/非法分页）→ 模板清单/模板下载 → 校验（幂等重放）→
// 预览（parsed 优先/raw 回退/路径参数边界）→ 错误文件下载 → 确认（状态守卫/坏 body/
// inline 终态/终态重放）。
func TestImportCenterHandler(t *testing.T) {
	e := newEnv(t)
	u := hSuper()
	r := hEngine(t, e, &u)

	// —— 上传三个任务：A（2 行合法）/ B（1 行缺必填）/ C（不校验，raw 预览用）——
	rowsA := [][]string{
		{"P-1", "商品一", "1", "2.5", "2026-01-01 08:00:00"},
		{"P-2", "商品二", "2", "3", ""},
	}
	up := func(rows [][]string) (int64, ImportUploadResult) {
		t.Helper()
		rec := hUpload(t, r, "/api/imports",
			[]hField{{"import_type", ImportProduct}},
			&hFilePart{"商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders, rows)})
		env := hRequireOK(t, rec)
		var res ImportUploadResult
		hData(t, env, &res)
		return res.ID.Int64(), res
	}
	idA, resA := up(rowsA)
	idB, _ := up([][]string{{"", "缺编码", "1", "1", ""}})
	idC, _ := up([][]string{{"P-9", "未校验", "1", "1", ""}})

	if resA.Status != TaskStatusParsed || resA.TotalRows != 2 || resA.FileName != "商品导入.xlsx" {
		t.Fatalf("上传结果不符: %+v", resA)
	}
	if !strings.HasPrefix(resA.ImportNo, "IMP-") {
		t.Fatalf("import_no 应为 IMP- 单号: %s", resA.ImportNo)
	}

	// 上传非法矩阵（表驱动：参数绑定/类型白名单/表头比对/空表）。
	upErrs := []struct {
		name   string
		fields []hField
		file   *hFilePart
		code   string
	}{
		{"缺 import_type", nil, &hFilePart{"a.xlsx", buildXlsx(t, "商品导入", testHeaders, rowsA)},
			"DATAX_IMPORT_TYPE_INVALID"},
		{"缺文件", []hField{{"import_type", ImportProduct}}, nil, "DATAX_FILE_REQUIRED"},
		{"非 xlsx", []hField{{"import_type", ImportProduct}}, &hFilePart{"数据.csv", []byte("a,b,c")},
			"DATAX_IMPORT_FILE_TYPE"},
		{"表头不一致", []hField{{"import_type", ImportProduct}},
			&hFilePart{"a.xlsx", buildXlsx(t, "商品导入", []string{"编码", "商品名称", "重量", "价格", "生产日期"}, rowsA)},
			"DATAX_TEMPLATE_MISMATCH"},
		{"无数据行", []hField{{"import_type", ImportProduct}},
			&hFilePart{"a.xlsx", buildXlsx(t, "商品导入", testHeaders, nil)},
			"DATAX_SHEET_EMPTY"},
	}
	for _, tc := range upErrs {
		t.Run("上传/"+tc.name, func(t *testing.T) {
			rec := hUpload(t, r, "/api/imports", tc.fields, tc.file)
			hRequireErrCode(t, rec, http.StatusBadRequest, tc.code)
		})
	}

	// —— GET /api/imports 列表（缺口接口）：筛选/分页/信封形态 ——
	rec := hGet(t, r, "/api/imports")
	var p hPage
	var items []ListTaskItem
	hData(t, hRequireOK(t, rec), &p)
	if p.Total != 3 || p.Page != 1 || p.PageSize != 20 {
		t.Fatalf("分页信封不符: %+v", p)
	}
	if err := json.Unmarshal(p.Items, &items); err != nil || len(items) != 3 {
		t.Fatalf("items 数量不符: %s", p.Items)
	}
	first := items[0] // id DESC：最后上传的 C 在前
	if first.ID.Int64() != idC || first.TaskType != "IMPORT" || first.Module != ImportProduct ||
		first.ModuleName != "商品导入" || first.Status != TaskStatusParsed ||
		first.Progress != 0 || first.CreatorID != 42 || !strings.HasPrefix(first.TaskNo, "IMP-") {
		t.Fatalf("列表项不符: %+v", first)
	}
	if first.FileName != "" || first.FileURL != "" {
		t.Fatalf("无错误文件的任务不应下发产物联动字段: %+v", first)
	}

	// 筛选：module/status（空结果 items=[] 非 null——nil slice 序列化契约）。
	for _, tc := range []struct {
		query string
		want  int64
	}{
		{"?module=" + ImportProduct, 3},
		{"?module=" + ImportSKU, 0},
		{"?status=" + TaskStatusValidated, 0},
	} {
		rec = hGet(t, r, "/api/imports"+tc.query)
		var pf hPage
		var its []ListTaskItem
		hData(t, hRequireOK(t, rec), &pf)
		if pf.Total != tc.want {
			t.Fatalf("筛选 %s 期望 total=%d，得到 %d", tc.query, tc.want, pf.Total)
		}
		if err := json.Unmarshal(pf.Items, &its); err != nil || len(its) != int(tc.want) {
			t.Fatalf("筛选 %s items 应为空数组形态且长度 %d: %s", tc.query, tc.want, pf.Items)
		}
	}
	// 分页：page=2&pageSize=2 → total=3、items=1。
	rec = hGet(t, r, "/api/imports?page=2&pageSize=2")
	var p2 hPage
	var its2 []ListTaskItem
	hData(t, hRequireOK(t, rec), &p2)
	if p2.Total != 3 || p2.Page != 2 || p2.PageSize != 2 {
		t.Fatalf("分页回显不符: %+v", p2)
	}
	if err := json.Unmarshal(p2.Items, &its2); err != nil || len(its2) != 1 {
		t.Fatalf("第 2 页应恰 1 条: %s", p2.Items)
	}
	// 非法分页（response.ParsePage，internal/response/response.go:52-72）。
	hRequireErrCode(t, hGet(t, r, "/api/imports?page=0"), 400, "COMMON_INVALID_PARAM")
	hRequireErrCode(t, hGet(t, r, "/api/imports?pageSize=101"), 400, "COMMON_INVALID_PARAM")

	// —— GET /api/imports/templates 模板清单（缺口接口）——
	// 清单 data 为数组形态（非分页对象——ListTemplates 直接 OK 下发）。
	rec = hGet(t, r, "/api/imports/templates")
	var tpls []TemplateItem
	hData(t, hRequireOK(t, rec), &tpls)
	if len(tpls) != 1 {
		t.Fatalf("模板清单应仅含已装配类型: %+v", tpls)
	}
	tp := tpls[0]
	if tp.ImportType != ImportProduct || tp.Name != "商品导入" || tp.FileName != "商品导入模板.xlsx" ||
		tp.DownloadURL != "/api/imports/templates/PRODUCT" || tp.HighRisk {
		t.Fatalf("模板清单项不符: %+v", tp)
	}

	// —— GET /api/imports/templates/:type 模板下载（缺口接口）——
	rec = hGet(t, r, "/api/imports/templates/PRODUCT")
	if rec.Code != http.StatusOK {
		t.Fatalf("模板下载期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("模板下载 Content-Type 不符: %s", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("模板下载应为附件下发: %s", cd)
	}
	tpl := openWorkbook(t, rec.Body.Bytes())
	tplSheet := tpl.GetSheetName(0)
	for i, title := range testHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if v, _ := tpl.GetCellValue(tplSheet, cell); v != title {
			t.Fatalf("模板列头 %d 不符: %q", i+1, v)
		}
	}
	hRequireErrCode(t, hGet(t, r, "/api/imports/templates/NOPE"), 400, "DATAX_IMPORT_TYPE_INVALID")

	// —— POST /api/imports/:id/validate（HTTP 层）+ 幂等重放 ——
	rec = hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/validate", idA), nil)
	var vrA ImportValidateResult
	hData(t, hRequireOK(t, rec), &vrA)
	if vrA.Status != TaskStatusValidated || vrA.ValidRows != 2 || vrA.ErrorRows != 0 || len(vrA.Errors) != 0 {
		t.Fatalf("A 校验结果不符: %+v", vrA)
	}
	if vrA.ErrorFileURL != "" {
		t.Fatalf("无错误行不应下发错误明细链接: %+v", vrA)
	}
	// VALIDATED 态重复校验幂等放行（service_import.go:365 状态口径）。
	hRequireOK(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/validate", idA), nil))

	rec = hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/validate", idB), nil)
	var vrB ImportValidateResult
	hData(t, hRequireOK(t, rec), &vrB)
	if vrB.ErrorRows != 1 || vrB.ValidRows != 0 || len(vrB.Errors) != 1 {
		t.Fatalf("B 校验结果不符: %+v", vrB)
	}
	if vrB.Errors[0].Row != 1 || vrB.Errors[0].Column != "code" || !strings.Contains(vrB.Errors[0].Message, "必填") {
		t.Fatalf("错误定位不符（excel §1.4 行列定位）: %+v", vrB.Errors)
	}
	if vrB.ErrorFileURL != fmt.Sprintf("/api/imports/%d/error-file", idB) || vrB.ErrorFileName == "" {
		t.Fatalf("错误明细链接不符: %+v", vrB)
	}

	// —— GET /api/imports/:id/preview（缺口接口）：parsed 优先/raw 回退/边界 ——
	rec = hGet(t, r, fmt.Sprintf("/api/imports/%d/preview", idA))
	var pvA ImportPreviewResult
	hData(t, hRequireOK(t, rec), &pvA)
	if pvA.TotalRows != 2 || len(pvA.Columns) != 5 || pvA.Columns[0].Key != "code" || pvA.Columns[0].Title != "商品编码" {
		t.Fatalf("预览列定义不符: %+v", pvA.Columns)
	}
	if v, ok := pvA.Rows[0]["code"].(string); !ok || v != "P-1" {
		t.Fatalf("预览 TEXT 列不符: %v", pvA.Rows[0]["code"])
	}
	if v, ok := pvA.Rows[0]["weight"].(float64); !ok || v != 1 {
		t.Fatalf("预览 NUMBER 列应为数值: %v", pvA.Rows[0]["weight"])
	}
	if v, ok := pvA.Rows[0]["price"].(float64); !ok || v != 2.5 {
		t.Fatalf("预览 MONEY 列应为数值: %v", pvA.Rows[0]["price"])
	}
	if v, _ := pvA.Rows[0]["made_at"].(string); v != "2026-01-01 08:00:00" {
		t.Fatalf("预览 DATE 列不符: %v", pvA.Rows[0]["made_at"])
	}
	// 未校验任务：parsed 缺位 → raw 文本回显（service_import.go:591 注释口径）。
	rec = hGet(t, r, fmt.Sprintf("/api/imports/%d/preview", idC))
	var pvC ImportPreviewResult
	hData(t, hRequireOK(t, rec), &pvC)
	if v, ok := pvC.Rows[0]["weight"].(string); !ok || v != "1" {
		t.Fatalf("未校验行应以 raw 文本回显: %v", pvC.Rows[0]["weight"])
	}
	// 路径参数边界（pathID，handler.go:67-77）。
	for _, tc := range []struct {
		path string
		http int
		code string
	}{
		{"/api/imports/abc/preview", 400, "COMMON_INVALID_PARAM"},
		{"/api/imports/0/preview", 400, "COMMON_INVALID_PARAM"},
		{"/api/imports/999999/preview", 404, "DATAX_TASK_NOT_FOUND"},
	} {
		t.Run("预览边界 "+tc.path, func(t *testing.T) {
			hRequireErrCode(t, hGet(t, r, tc.path), tc.http, tc.code)
		})
	}

	// —— GET /api/imports/:id/error-file（缺口接口）——
	rec = hGet(t, r, fmt.Sprintf("/api/imports/%d/error-file", idB))
	if rec.Code != http.StatusOK {
		t.Fatalf("错误文件下载期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte{0x50, 0x4B}) {
		t.Fatal("错误文件应为 xlsx（PK zip 魔数）")
	}
	ef := openWorkbook(t, rec.Body.Bytes())
	efSheet := ef.GetSheetName(0)
	lastCol, _ := excelize.CoordinatesToCellName(len(testHeaders)+1, 1)
	if v, _ := ef.GetCellValue(efSheet, lastCol); v != "错误原因" {
		t.Fatalf("错误 Excel 缺少错误原因列: %q", v)
	}
	// 无错误明细文件的任务 → 404（service_export.go:303）；任务不存在 → 404。
	hRequireErrCode(t, hGet(t, r, fmt.Sprintf("/api/imports/%d/error-file", idA)), 404, "DATAX_FILE_NOT_FOUND")
	hRequireErrCode(t, hGet(t, r, "/api/imports/999999/error-file"), 404, "DATAX_TASK_NOT_FOUND")
	// 错误文件下载审计（module=file, action=download——handler.go:291；本用例至此
	// 唯一 download 触达点即该次下载）。
	foundDL := false
	for _, entry := range e.spy.all() {
		if entry.Action == "download" && entry.Module == "file" {
			foundDL = true
		}
	}
	if !foundDL {
		t.Fatalf("错误文件下载审计缺位: %v", e.spy.actions())
	}

	// —— POST /api/imports/:id/confirm（HTTP 层）——
	// PARSED 态确认 → 409 状态守卫（service_import.go:712-716）。
	hRequireErrCode(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/confirm", idC), ConfirmInput{}),
		409, "DATAX_STATUS_CONFLICT")
	// 坏 JSON body → 400 参数绑定（handler.go:256-262；绑定先于 service）。
	hRequireErrCode(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/confirm", idA), "{bad"),
		400, "COMMON_INVALID_PARAM")
	// inline 模式确认 → 同步终态（plan §4.1；装配同 TestInlineConfirmExecutesSync）。
	e.queue.inline = func(ctx context.Context, task asynqx.Task) error {
		return e.svc.RunImportCommit(ctx, task)
	}
	rec = hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/confirm", idA), ConfirmInput{Confirmed: true})
	var cr ImportConfirmResult
	hData(t, hRequireOK(t, rec), &cr)
	if cr.Status != TaskStatusSuccess || cr.SuccessRows != 2 || cr.FailedRows != 0 || cr.FinishedAt.IsZero() {
		t.Fatalf("确认结果不符: %+v", cr)
	}
	// 终态重放确认 → 409（恰一成功，plan §13.2）。
	hRequireErrCode(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/confirm", idA), ConfirmInput{}),
		409, "DATAX_STATUS_CONFLICT")
}

// TestExportCenterHandler 导出中心 HTTP 链路：创建（QUEUED/inline 终态/错误矩阵）→
// 列表 → 产物下载守卫与下载 → GET /api/data-tasks/export/:id 任务卡。
func TestExportCenterHandler(t *testing.T) {
	e := newEnv(t)
	e.source.rows = exportRows(2)
	u := hSuper()
	r := hEngine(t, e, &u)

	// —— 创建：队列挂起 → QUEUED，非终态不下发产物链接 ——
	e.queue.inline = nil
	rec := hPostJSON(t, r, "/api/exports", ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
	var queued ListTaskItem
	hData(t, hRequireOK(t, rec), &queued)
	if queued.Status != TaskStatusQueued || queued.TaskType != "EXPORT" ||
		!strings.HasPrefix(queued.TaskNo, "EXP-") || queued.FileURL != "" {
		t.Fatalf("QUEUED 创建结果不符: %+v", queued)
	}
	// 终态前下载 → 409（service_export.go:282-284）。
	hRequireErrCode(t, hGet(t, r, fmt.Sprintf("/api/exports/%d/file", queued.ID.Int64())),
		409, "DATAX_EXPORT_NOT_FINISHED")

	// —— inline 模式创建 → 同步 SUCCESS + 产物链接下发 ——
	e.queue.inline = func(ctx context.Context, task asynqx.Task) error {
		return e.svc.RunExportRun(ctx, task)
	}
	rec = hPostJSON(t, r, "/api/exports", ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
	var done ListTaskItem
	hData(t, hRequireOK(t, rec), &done)
	if done.Status != TaskStatusSuccess || done.Progress != 100 || done.TotalRows != 2 ||
		done.FileURL != fmt.Sprintf("/api/exports/%d/file", done.ID.Int64()) || done.FileName == "" {
		t.Fatalf("inline 创建终态不符: %+v", done)
	}

	// —— 创建错误矩阵（表驱动）——
	cases := []struct {
		name    string
		payload any
		http    int
		code    string
	}{
		{"模块非法", ExportCreateInput{Module: "NOPE", Scope: "ALL"}, 400, "DATAX_MODULE_INVALID"},
		{"模块未装配", ExportCreateInput{Module: ModuleReport, Scope: "ALL"}, 409, "DATAX_MODULE_NOT_AVAILABLE"},
		{"范围非法", ExportCreateInput{Module: ModuleProduct, Scope: "NOSUCH"}, 400, "DATAX_SCOPE_INVALID"},
		{"SELECTED 缺 ids", ExportCreateInput{Module: ModuleProduct, Scope: "SELECTED"}, 400, "DATAX_SCOPE_INVALID"},
		{"TIME_RANGE 缺区间", ExportCreateInput{Module: ModuleProduct, Scope: "TIME_RANGE"}, 400, "DATAX_SCOPE_INVALID"},
		{"坏 JSON body", "{bad", 400, "COMMON_INVALID_PARAM"},
	}
	for _, tc := range cases {
		t.Run("创建/"+tc.name, func(t *testing.T) {
			hRequireErrCode(t, hPostJSON(t, r, "/api/exports", tc.payload), tc.http, tc.code)
		})
	}

	// —— GET /api/exports 列表 + 状态筛选 ——
	rec = hGet(t, r, "/api/exports")
	var p hPage
	var items []ListTaskItem
	hData(t, hRequireOK(t, rec), &p)
	if p.Total != 2 {
		t.Fatalf("导出列表 total 应为 2: %+v", p)
	}
	if err := json.Unmarshal(p.Items, &items); err != nil || len(items) != 2 {
		t.Fatalf("导出列表 items 不符: %s", p.Items)
	}
	if items[0].TaskType != "EXPORT" || items[0].ModuleName != "商品" || items[0].Module != ModuleProduct {
		t.Fatalf("导出列表项不符: %+v", items[0])
	}
	rec = hGet(t, r, "/api/exports?status="+TaskStatusFailed)
	var pf hPage
	hData(t, hRequireOK(t, rec), &pf)
	if pf.Total != 0 {
		t.Fatalf("FAILED 筛选应空: %+v", pf)
	}

	// —— GET /api/exports/:id/file 产物下载（终态 + 审计 + 工作簿结构）——
	// 下载走 http.ServeContent 流式响应：不携带 Content-Disposition（中文文件名由前端
	// axios Blob 自行命名——handler.go:560 冻结注释），文件名断言落在信封 file_name。
	rec = hGet(t, r, fmt.Sprintf("/api/exports/%d/file", done.ID.Int64()))
	if rec.Code != http.StatusOK {
		t.Fatalf("产物下载期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	wb := openWorkbook(t, rec.Body.Bytes())
	sheet := wb.GetSheetName(0)
	if v, _ := wb.GetCellValue(sheet, "A1"); v != "商品导出" {
		t.Fatalf("产物标题不符: %q", v)
	}
	foundDL := false
	for _, entry := range e.spy.all() {
		if entry.Action == "download" && entry.Module == "file" {
			foundDL = true
		}
	}
	if !foundDL {
		t.Fatalf("产物下载审计缺位（handler.go:390）: %v", e.spy.actions())
	}
	hRequireErrCode(t, hGet(t, r, "/api/exports/999999/file"), 404, "DATAX_TASK_NOT_FOUND")
	hRequireErrCode(t, hGet(t, r, "/api/exports/abc/file"), 400, "COMMON_INVALID_PARAM")

	// —— GET /api/data-tasks/export/:id 任务卡（缺口接口）——
	rec = hGet(t, r, fmt.Sprintf("/api/data-tasks/export/%d", done.ID.Int64()))
	var card ListTaskItem
	hData(t, hRequireOK(t, rec), &card)
	if card.TaskType != "EXPORT" || card.Module != ModuleProduct || card.ModuleName != "商品" ||
		card.Status != TaskStatusSuccess || card.FileURL == "" {
		t.Fatalf("导出任务卡不符: %+v", card)
	}
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/export/999999"), 404, "DATAX_TASK_NOT_FOUND")
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/export/0"), 400, "COMMON_INVALID_PARAM")
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/export/abc"), 400, "COMMON_INVALID_PARAM")
}

// TestFileCenterHandler 文件中心 HTTP 链路（excel §7、api.md §5）：上传（合法/非法矩阵）
// → 列表过滤 → 下载（内容一致 + 审计）→ 预览（图片放行/非图片拒绝）→ 软删幂等。
func TestFileCenterHandler(t *testing.T) {
	e := newEnv(t)
	u := hSuper()
	r := hEngine(t, e, &u)

	// —— POST /api/files 合法上传（图片）——
	rec := hUpload(t, r, "/api/files",
		[]hField{{"module", ModuleProduct}, {"business_no", "IMP-20260101-000001"}},
		&hFilePart{"异常图.png", pngMagic})
	var img FileItem
	hData(t, hRequireOK(t, rec), &img)
	if img.FileName != "异常图.png" || img.FileType != ".png" || img.MimeType != "image/png" ||
		img.ModuleName != "商品" || img.UploaderName != "tester" ||
		img.DownloadURL != fmt.Sprintf("/api/files/%d/download", img.ID) {
		t.Fatalf("上传登记不符: %+v", img)
	}
	// 第二个：xlsx 文档（module=SKU）。
	rec = hUpload(t, r, "/api/files", []hField{{"module", ModuleSKU}}, &hFilePart{"文档.xlsx", xlsxMagic})
	var doc FileItem
	hData(t, hRequireOK(t, rec), &doc)

	// —— 上传非法矩阵（表驱动）——
	badCases := []struct {
		name   string
		fields []hField
		file   *hFilePart
		code   string
	}{
		{"缺 module", nil, &hFilePart{"a.xlsx", xlsxMagic}, "DATAX_MODULE_PARAM_INVALID"},
		{"module 含路径字符", []hField{{"module", "a/b"}}, &hFilePart{"a.xlsx", xlsxMagic}, "DATAX_MODULE_PARAM_INVALID"},
		{"business_no 非法", []hField{{"module", ModuleProduct}, {"business_no", "bad no!"}}, &hFilePart{"a.xlsx", xlsxMagic}, "DATAX_MODULE_PARAM_INVALID"},
		{"缺文件", []hField{{"module", ModuleProduct}}, nil, "DATAX_FILE_REQUIRED"},
	}
	for _, tc := range badCases {
		t.Run("上传/"+tc.name, func(t *testing.T) {
			hRequireErrCode(t, hUpload(t, r, "/api/files", tc.fields, tc.file), 400, tc.code)
		})
	}

	// —— GET /api/files 列表 + module 过滤 ——
	rec = hGet(t, r, "/api/files")
	var p hPage
	var items []FileItem
	hData(t, hRequireOK(t, rec), &p)
	if p.Total != 2 {
		t.Fatalf("文件列表 total 应为 2: %+v", p)
	}
	if err := json.Unmarshal(p.Items, &items); err != nil || len(items) != 2 {
		t.Fatalf("文件列表 items 不符: %s", p.Items)
	}
	if items[0].ID != doc.ID || items[0].DownloadURL == "" {
		t.Fatalf("文件列表项不符: %+v", items[0])
	}
	rec = hGet(t, r, "/api/files?module="+ModuleProduct)
	var pf hPage
	hData(t, hRequireOK(t, rec), &pf)
	if pf.Total != 1 {
		t.Fatalf("module=PRODUCT 过滤应恰 1 条: %+v", pf)
	}

	// —— GET /api/files/:id/download（内容一致 + 审计）——
	rec = hGet(t, r, fmt.Sprintf("/api/files/%d/download", img.ID))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngMagic) {
		t.Fatalf("图片下载不符: code=%d len=%d", rec.Code, rec.Body.Len())
	}
	foundDL := false
	for _, entry := range e.spy.all() {
		if entry.Action == "download" && entry.Module == "file" && entry.ObjectID == img.ID {
			foundDL = true
		}
	}
	if !foundDL {
		t.Fatalf("下载审计缺位（handler.go:464）: %v", e.spy.all())
	}
	hRequireErrCode(t, hGet(t, r, "/api/files/abc/download"), 400, "COMMON_INVALID_PARAM")
	// 文件行不存在：FindFile 统一走 responseNotFound() → 404 DATAX_TASK_NOT_FOUND
	// （repo_gorm.go:242-251 与 fakes_test.go:594 同语义——见 findings 记录）。
	hRequireErrCode(t, hGet(t, r, "/api/files/999999/download"), 404, "DATAX_TASK_NOT_FOUND")

	// —— GET /api/files/:id/preview（仅图片原样返回，plan §6.4）——
	rec = hGet(t, r, fmt.Sprintf("/api/files/%d/preview", img.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("图片预览期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("图片预览 Content-Type 不符: %s", ct)
	}
	hRequireErrCode(t, hGet(t, r, fmt.Sprintf("/api/files/%d/preview", doc.ID)),
		400, "DATAX_PREVIEW_UNSUPPORTED")

	// —— DELETE /api/files/:id（软删 + 幂等 404 + 删除后下载拒绝）——
	rec = hDo(t, r, http.MethodDelete, fmt.Sprintf("/api/files/%d", doc.ID), nil, "")
	var delRes map[string]any
	hData(t, hRequireOK(t, rec), &delRes)
	if delRes["deleted"] != true {
		t.Fatalf("删除响应不符: %v", delRes)
	}
	hRequireErrCode(t, hDo(t, r, http.MethodDelete, fmt.Sprintf("/api/files/%d", doc.ID), nil, ""),
		404, "DATAX_FILE_DELETED")
	hRequireErrCode(t, hGet(t, r, fmt.Sprintf("/api/files/%d/download", doc.ID)), 404, "DATAX_FILE_DELETED")
	foundDel := false
	for _, act := range e.spy.actions() {
		if act == "delete" {
			foundDel = true
		}
	}
	if !foundDel {
		t.Fatalf("删除审计缺位: %v", e.spy.actions())
	}
}

// TestImportTaskCardHandler GET /api/data-tasks/import/:id（缺口接口）：任务卡详情
// 随状态推进刷新（PARSED→VALIDATED→终态）+ 路径参数边界。
func TestImportTaskCardHandler(t *testing.T) {
	e := newEnv(t)
	u := hSuper()
	r := hEngine(t, e, &u)

	rec := hUpload(t, r, "/api/imports",
		[]hField{{"import_type", ImportProduct}},
		&hFilePart{"商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders,
			[][]string{{"P-1", "商品一", "1", "2.5", ""}})})
	var res ImportUploadResult
	hData(t, hRequireOK(t, rec), &res)
	id := res.ID.Int64()

	// PARSED 态任务卡。
	rec = hGet(t, r, fmt.Sprintf("/api/data-tasks/import/%d", id))
	var card ListTaskItem
	hData(t, hRequireOK(t, rec), &card)
	if card.TaskType != "IMPORT" || card.Module != ImportProduct || card.ModuleName != "商品导入" ||
		card.Status != TaskStatusParsed || card.TotalRows != 1 || card.CreatorID != 42 ||
		card.Progress != 0 || card.FileURL != "" {
		t.Fatalf("导入任务卡（PARSED）不符: %+v", card)
	}

	// 校验推进 → VALIDATED 可见（任务卡刷新语义）。
	hRequireOK(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/validate", id), nil))
	rec = hGet(t, r, fmt.Sprintf("/api/data-tasks/import/%d", id))
	hData(t, hRequireOK(t, rec), &card)
	if card.Status != TaskStatusValidated {
		t.Fatalf("导入任务卡应刷新为 VALIDATED: %+v", card)
	}

	// inline 确认 → 终态（无错误行不产生错误明细链接）。
	e.queue.inline = func(ctx context.Context, task asynqx.Task) error {
		return e.svc.RunImportCommit(ctx, task)
	}
	hRequireOK(t, hPostJSON(t, r, fmt.Sprintf("/api/imports/%d/confirm", id), ConfirmInput{}))
	rec = hGet(t, r, fmt.Sprintf("/api/data-tasks/import/%d", id))
	hData(t, hRequireOK(t, rec), &card)
	if card.Status != TaskStatusSuccess || card.SuccessRows != 1 || card.Progress != 100 || card.FileURL != "" {
		t.Fatalf("导入任务卡（SUCCESS）不符: %+v", card)
	}

	// 边界：路径参数非法 / 任务不存在。
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/import/0"), 400, "COMMON_INVALID_PARAM")
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/import/abc"), 400, "COMMON_INVALID_PARAM")
	hRequireErrCode(t, hGet(t, r, "/api/data-tasks/import/999999"), 404, "DATAX_TASK_NOT_FOUND")
}
