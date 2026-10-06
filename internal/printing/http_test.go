package printing

// HTTP 层数据驱动表测试——补齐两条单查端点（getTemplate/getTask）的数据面缺口：
//   - 引擎逐条镜像 RegisterRoutes（handler.go:129/137）的路由与 auth.RequirePermission
//     权限点，handler 直接装配 fakes_test.go 内存替身背书的 Service（RegisterRoutes
//     固定装配 NewGormRepository，假 gorm 驱动无法承载真 SQL 扫描——returns/http_test.go
//     同款先例）；路由面全量冻结守卫在 routes_test.go（TestRegisterRoutes_MountsAllEndpoints），
//     其余 10 条端点的数据行为已由 service_test.go 同一 fake 基建覆盖，此处不重复；
//   - 认证上下文以中间件注入 auth.UserContext（键为 auth 中间件私有键 "sf_auth_user"，
//     internal/auth/middleware.go:26；auth 侧漂移则 CurrentUser 落空 → 全部 401 响亮
//     失败），超级管理员经 RequirePermission 直通（permission.md §1，单测环境无 RBAC 装配）；
//   - 覆盖：路径参数绑定（pathID 值域）、未认证 401、资源 404（信封 details）、统一
//     信封与 HTTP 状态映射、成功读视图（ID 字符串化/字段绑定预设键序文案/omitempty
//     形态/任务详情渲染数据包与模板快照）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// ---- HTTP 基建 ----

// authUserCtxKey auth 中间件 gin 用户上下文键（internal/auth/middleware.go:26，
// 未导出——同 module 测试以同值注入；漂移时 CurrentUser 落空 → 全部 401 响亮失败）。
const authUserCtxKey = "sf_auth_user"

func userInject(uc auth.UserContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(authUserCtxKey, uc)
		c.Next()
	}
}

// superUser 超级管理员（RequirePermission 直通，permission.md §1）。
func superUser() auth.UserContext {
	return auth.UserContext{UserID: 1, Username: "超管", IsSuper: true}
}

func ucPtr(uc auth.UserContext) *auth.UserContext { return &uc }

// httpReadEngine 装配两条单查端点的镜像路由（权限点与 handler.go:129/137 逐字一致）；
// uc 非 nil 时前置注入认证上下文中间件（IsSuper 直通 RequirePermission）。
func httpReadEngine(t *testing.T, env *testEnv, uc *auth.UserContext) *gin.Engine {
	t.Helper()
	r, api := routeTestEngine(t)
	h := &handler{svc: env.svc}
	p := api.Group("/prints")
	if uc != nil {
		p.Use(userInject(*uc))
	}
	p.GET("/templates/:id", auth.RequirePermission(PermTemplateRead), h.getTemplate)
	p.GET("/tasks/:id", auth.RequirePermission(PermTaskRead), h.getTask)
	return r
}

// httpEnvelope 统一响应信封（api.md §2：{code,message,data,request_id,details}；
// 成功 code=0 数字，失败为模块命名空间字符串错误码）。
type httpEnvelope struct {
	Code      any            `json:"code"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id"`
}

// doGET 发起 GET 并解码统一信封（信封契约本身也是被测面——非法 JSON 即失败）。
func doGET(t *testing.T, r *gin.Engine, path string) (int, httpEnvelope) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body httpEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非合法 JSON 信封: %v; body=%s", err, w.Body.String())
	}
	return w.Code, body
}

// assertEnvelopeCode 断言信封 code（成功数字 0 / 失败字符串错误码；json.Unmarshal
// 数字一律 float64）。
func assertEnvelopeCode(t *testing.T, got, want any) {
	t.Helper()
	switch w := want.(type) {
	case string:
		s, ok := got.(string)
		if !ok || s != w {
			t.Fatalf("信封 code 应为 %q，得到 %v", w, got)
		}
	case int:
		n, ok := got.(float64)
		if !ok || int(n) != w {
			t.Fatalf("信封 code 应为数字 %d，得到 %v", w, got)
		}
	default:
		t.Fatalf("未支持的 wantCode 形态: %v", want)
	}
}

// strOf / boolOf / intOf / strItemOf 信封 map 取值助手（类型不符即失败，不做宽容转换）。
func strOf(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	s, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s 应为字符串，得到 %T(%v)", key, m[key], m[key])
	}
	return s
}

func boolOf(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	b, ok := m[key].(bool)
	if !ok {
		t.Fatalf("%s 应为布尔，得到 %T(%v)", key, m[key], m[key])
	}
	return b
}

func intOf(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	n, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%s 应为数字，得到 %T(%v)", key, m[key], m[key])
	}
	return int(n)
}

func strItemOf(t *testing.T, m map[string]any, key string, i int, sub string) string {
	t.Helper()
	arr, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s 应为数组，得到 %T(%v)", key, m[key], m[key])
	}
	item, ok := arr[i].(map[string]any)
	if !ok {
		t.Fatalf("%s[%d] 应为对象，得到 %T", key, i, arr[i])
	}
	return strOf(t, item, sub)
}

// ---- GET /api/prints/templates/:id ----

// TestHTTPGetTemplate 模板详情端点（handler.go:193 → Service.GetTemplate
// service_template.go:241）：路径参数绑定 / 未认证 401 / 404 details / 读视图。
func TestHTTPGetTemplate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// 真路径建两张模板：标签类（字段绑定+码制+二维码）与单据类（qrcode、空绑定、无码制）。
	sku, err := env.svc.CreateTemplate(ctx, actor(), TemplateSaveInput{
		Name: "SKU标签", ObjectType: ObjectSKULabel, Paper: PaperThermal6040,
		BarcodeSymbology: SymbologyCode128, QRCodeEnabled: true,
		Fields: []string{"product_name", "spec"}, Remark: "热敏标签",
	})
	if err != nil {
		t.Fatalf("seed SKU 模板失败: %v", err)
	}
	inbound, err := env.svc.CreateTemplate(ctx, actor(), TemplateSaveInput{
		Name: "入库单模板", ObjectType: ObjectInboundOrder, Paper: PaperA4, QRCodeEnabled: true,
	})
	if err != nil {
		t.Fatalf("seed 入库单模板失败: %v", err)
	}

	rAuthed := httpReadEngine(t, env, ucPtr(superUser()))
	rNoAuth := httpReadEngine(t, env, nil)

	cases := []struct {
		name       string
		engine     *gin.Engine
		path       string
		wantStatus int
		wantCode   any // 成功 0；失败字符串错误码
		check      func(t *testing.T, b httpEnvelope)
	}{
		{"未认证401（RequirePermission 生效）", rNoAuth, "/api/prints/templates/" + sku.ID,
			http.StatusUnauthorized, "COMMON_UNAUTHORIZED", nil},
		{"id 非数字 → 400 带 field", rAuthed, "/api/prints/templates/abc",
			http.StatusBadRequest, "COMMON_INVALID_PARAM", func(t *testing.T, b httpEnvelope) {
				if strOf(t, b.Details, "field") != "id" {
					t.Fatalf("details.field 应为 id: %+v", b.Details)
				}
			}},
		{"id 为 0 → 400", rAuthed, "/api/prints/templates/0",
			http.StatusBadRequest, "COMMON_INVALID_PARAM", nil},
		{"id 为负数 → 400", rAuthed, "/api/prints/templates/-1",
			http.StatusBadRequest, "COMMON_INVALID_PARAM", nil},
		{"不存在 → 404 带 template_id", rAuthed, "/api/prints/templates/999",
			http.StatusNotFound, "PRINT_TEMPLATE_NOT_FOUND", func(t *testing.T, b httpEnvelope) {
				if intOf(t, b.Details, "template_id") != 999 {
					t.Fatalf("details.template_id 应为 999: %+v", b.Details)
				}
			}},
		{"标签模板详情（字段绑定预设键序与文案）", rAuthed, "/api/prints/templates/" + sku.ID,
			http.StatusOK, 0, func(t *testing.T, b httpEnvelope) {
				if b.Message != "ok" {
					t.Fatalf("成功信封 message 应为 ok: %s", b.Message)
				}
				d := b.Data
				if strOf(t, d, "id") != sku.ID {
					t.Fatalf("id 应为字符串化路径 ID: %v", d["id"])
				}
				if strOf(t, d, "name") != "SKU标签" || strOf(t, d, "object_type") != ObjectSKULabel ||
					strOf(t, d, "paper") != PaperThermal6040 || strOf(t, d, "status") != StatusEnabled ||
					strOf(t, d, "remark") != "热敏标签" || strOf(t, d, "barcode_symbology") != SymbologyCode128 ||
					!boolOf(t, d, "qrcode_enabled") {
					t.Fatalf("读视图字段不符: %+v", d)
				}
				// 字段绑定：预设键序（service.go templateView → orderedFieldList），
				// 展示文案由后端预设注册表派生（不信前端传入文案）。
				if got := strItemOf(t, d, "fields", 0, "key"); got != "product_name" {
					t.Fatalf("fields[0].key 应为 product_name: %v", got)
				}
				if got := strItemOf(t, d, "fields", 0, "label"); got != "商品名称" {
					t.Fatalf("fields[0].label 应由预设派生: %v", got)
				}
				if got := strItemOf(t, d, "fields", 1, "key"); got != "spec" {
					t.Fatalf("fields[1].key 应为 spec: %v", got)
				}
			}},
		{"单据模板详情（无码制键 omitempty/空绑定数组）", rAuthed, "/api/prints/templates/" + inbound.ID,
			http.StatusOK, 0, func(t *testing.T, b httpEnvelope) {
				d := b.Data
				if _, ok := d["barcode_symbology"]; ok {
					t.Fatalf("单据类无码制，barcode_symbology 应 omitempty: %v", d["barcode_symbology"])
				}
				arr, ok := d["fields"].([]any)
				if !ok || len(arr) != 0 {
					t.Fatalf("空绑定 fields 应为空数组（非 null）: %#v", d["fields"])
				}
				if !boolOf(t, d, "qrcode_enabled") {
					t.Fatalf("qrcode_enabled 应为 true: %+v", d)
				}
				if strOf(t, d, "created_by") != "42" {
					t.Fatalf("created_by 应为操作者字符串化 ID: %v", d["created_by"])
				}
				if strOf(t, d, "created_at") == "" || strOf(t, d, "updated_at") == "" {
					t.Fatalf("时间戳应非空: %+v", d)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doGET(t, tc.engine, tc.path)
			if status != tc.wantStatus {
				t.Fatalf("HTTP 状态应为 %d，得到 %d: %s", tc.wantStatus, status, body.Message)
			}
			assertEnvelopeCode(t, body.Code, tc.wantCode)
			if tc.check != nil {
				tc.check(t, body)
			}
		})
	}
}

// ---- GET /api/prints/tasks/:id ----

// TestHTTPGetTask 任务详情端点（handler.go:349 → Service.GetTask service_task.go:202）：
// 详情 = 任务视图 + 模板快照 + 渲染数据包（前端 react-to-print 消费；列表不携带 rows
// 的反命题已由 service_test.go TestListTasks_IDExactFilter 守卫）。
func TestHTTPGetTask(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// 内置承接对象 CARTON_CODE（ports.go builtinReader：data_ids 即码值原文），经
	// CreateTask 真路径落库（PT 单号/模板快照/渲染数据包行）；fakeQueue 仅捕获不入队
	// 执行 → 任务保持 QUEUED。
	tpl, err := env.svc.CreateTemplate(ctx, actor(), TemplateSaveInput{
		Name: "箱码模板", ObjectType: ObjectCartonCode, Paper: PaperThermal1005,
		BarcodeSymbology: SymbologyCode128, Fields: []string{"carton_code", "box_seq"},
	})
	if err != nil {
		t.Fatalf("seed 箱码模板失败: %v", err)
	}
	task, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{
		TemplateID: mustParse(t, tpl.ID), DataIDs: []string{"CTN-0001", "CTN-0002"},
	})
	if err != nil {
		t.Fatalf("seed 打印任务失败: %v", err)
	}

	rAuthed := httpReadEngine(t, env, ucPtr(superUser()))
	rNoAuth := httpReadEngine(t, env, nil)

	cases := []struct {
		name       string
		engine     *gin.Engine
		path       string
		wantStatus int
		wantCode   any
		check      func(t *testing.T, b httpEnvelope)
	}{
		{"未认证401（RequirePermission 生效）", rNoAuth, "/api/prints/tasks/" + task.ID,
			http.StatusUnauthorized, "COMMON_UNAUTHORIZED", nil},
		{"id 非数字 → 400 带 field", rAuthed, "/api/prints/tasks/x9",
			http.StatusBadRequest, "COMMON_INVALID_PARAM", func(t *testing.T, b httpEnvelope) {
				if strOf(t, b.Details, "field") != "id" {
					t.Fatalf("details.field 应为 id: %+v", b.Details)
				}
			}},
		{"不存在 → 404 带 task_id", rAuthed, "/api/prints/tasks/888",
			http.StatusNotFound, "PRINT_TASK_NOT_FOUND", func(t *testing.T, b httpEnvelope) {
				if intOf(t, b.Details, "task_id") != 888 {
					t.Fatalf("details.task_id 应为 888: %+v", b.Details)
				}
			}},
		{"详情渲染数据包（快照+rows 序）", rAuthed, "/api/prints/tasks/" + task.ID,
			http.StatusOK, 0, func(t *testing.T, b httpEnvelope) {
				d := b.Data
				if !strings.HasPrefix(strOf(t, d, "print_no"), "PT-") {
					t.Fatalf("print_no 应为 PT 前缀（plan §12.3）: %v", d["print_no"])
				}
				if strOf(t, d, "status") != TaskStatusQueued {
					t.Fatalf("fakeQueue 仅捕获，状态应保持 QUEUED: %v", d["status"])
				}
				if strOf(t, d, "object_type") != ObjectCartonCode ||
					strOf(t, d, "template_id") != task.TemplateID ||
					strOf(t, d, "template_name") != "箱码模板" ||
					strOf(t, d, "paper") != PaperThermal1005 {
					t.Fatalf("任务视图字段不符: %+v", d)
				}
				// 份数默认 1（CreateTask copies==0 → MinCopies）；总数=行数。
				if intOf(t, d, "copies") != 1 || intOf(t, d, "total_count") != 2 {
					t.Fatalf("copies/total_count 不符: copies=%v total=%v", d["copies"], d["total_count"])
				}
				// 未确认任务：result/printed_by/printed_at omitempty 不出现。
				for _, k := range []string{"result", "printed_by", "printed_at", "error_message"} {
					if _, ok := d[k]; ok {
						t.Fatalf("未确认任务不应携带 %s: %v", k, d[k])
					}
				}
				// 模板快照（任务创建时冻结，service_task.go SnapshotFromTemplate）。
				snap, ok := d["template"].(map[string]any)
				if !ok {
					t.Fatalf("详情应携带模板快照对象: %T", d["template"])
				}
				if strOf(t, snap, "name") != "箱码模板" || strOf(t, snap, "object_type") != ObjectCartonCode ||
					strOf(t, snap, "paper") != PaperThermal1005 || strOf(t, snap, "barcode_symbology") != SymbologyCode128 {
					t.Fatalf("模板快照不符: %+v", snap)
				}
				// 渲染数据包 rows：seq 1 起与 data_ids 序一致；内置承接行
				// id=code=data_id=码值原文（ports.go builtinReader + taskRows）。
				rows, ok := d["rows"].([]any)
				if !ok || len(rows) != 2 {
					t.Fatalf("详情应携带 2 行渲染数据包: %#v", d["rows"])
				}
				for i, want := range []string{"CTN-0001", "CTN-0002"} {
					row, ok := rows[i].(map[string]any)
					if !ok {
						t.Fatalf("rows[%d] 应为对象: %T", i, rows[i])
					}
					if intOf(t, row, "seq") != i+1 {
						t.Fatalf("rows[%d].seq 应为 %d: %v", i, i+1, row["seq"])
					}
					for _, k := range []string{"id", "code", "data_id"} {
						if strOf(t, row, k) != want {
							t.Fatalf("rows[%d].%s 应为码值原文 %s: %v", i, k, want, row[k])
						}
					}
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doGET(t, tc.engine, tc.path)
			if status != tc.wantStatus {
				t.Fatalf("HTTP 状态应为 %d，得到 %d: %s", tc.wantStatus, status, body.Message)
			}
			assertEnvelopeCode(t, body.Code, tc.wantCode)
			if tc.check != nil {
				tc.check(t, body)
			}
		})
	}
}
