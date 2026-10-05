package printing

// Service 单元测试（ask 交付项：模板校验、任务状态机、批量上限；内存替身，
// 不依赖 PostgreSQL/Redis/网络——fakes_test.go 假 gorm 驱动 + 内存 Repository/Queue/Reader）。

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stockflow/server/internal/asynqx"
)

func actor() Actor {
	return Actor{UserID: 42, Username: "tester", RequestID: "req-1"}
}

// ---- 模板校验（plan §7.1）----

func TestCreateTemplate_ValidationMatrix(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		in      TemplateSaveInput
		wantErr string // 期望错误码；空=成功
	}{
		{"名称为空", TemplateSaveInput{ObjectType: ObjectSKULabel, Paper: PaperA4, BarcodeSymbology: SymbologyCode128}, "COMMON_INVALID_PARAM"},
		{"名称超列宽", TemplateSaveInput{Name: strings.Repeat("名", nameMaxLen+1), ObjectType: ObjectSKULabel, Paper: PaperA4, BarcodeSymbology: SymbologyCode128}, "COMMON_INVALID_PARAM"},
		{"对象类型非法", TemplateSaveInput{Name: "x", ObjectType: "NOT_A_TYPE", Paper: PaperA4}, "PRINT_OBJECT_TYPE_INVALID"},
		{"纸张非法", TemplateSaveInput{Name: "x", ObjectType: ObjectSKULabel, Paper: "B5"}, "PRINT_PAPER_INVALID"},
		{"标签类缺码制", TemplateSaveInput{Name: "x", ObjectType: ObjectSKULabel, Paper: PaperThermal6040}, "PRINT_SYMBOLOGY_INVALID"},
		{"单据类带码制", TemplateSaveInput{Name: "x", ObjectType: ObjectInboundOrder, Paper: PaperA4, BarcodeSymbology: SymbologyCode128}, "PRINT_SYMBOLOGY_INVALID"},
		{"码制值域外", TemplateSaveInput{Name: "x", ObjectType: ObjectSKULabel, Paper: PaperA4, BarcodeSymbology: "PDF417"}, "PRINT_SYMBOLOGY_INVALID"},
		{"绑定键不在预设", TemplateSaveInput{Name: "x", ObjectType: ObjectSKULabel, Paper: PaperA4, BarcodeSymbology: SymbologyCode128, Fields: []string{"sku_code", "pallet_code"}}, "PRINT_FIELDS_INVALID"},
		{"合法标签模板", TemplateSaveInput{Name: "SKU标签", ObjectType: ObjectSKULabel, Paper: PaperThermal6040, BarcodeSymbology: SymbologyCode128, Fields: []string{"sku_code", "product_name"}}, ""},
		{"合法单据模板", TemplateSaveInput{Name: "入库单", ObjectType: ObjectInboundOrder, Paper: PaperA4, QRCodeEnabled: true, Fields: []string{"order_no", "supplier_name"}}, ""},
	}
	for _, tc := range cases {
		view, err := env.svc.CreateTemplate(ctx, actor(), tc.in)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: 期望成功，得到 %v", tc.name, err)
			}
			if view.Status != StatusEnabled {
				t.Fatalf("%s: 创建应恒 ENABLED，得到 %s", tc.name, view.Status)
			}
			continue
		}
		asPrintErr(t, err, tc.wantErr)
	}
}

func TestCreateTemplate_FieldsDerivedFromPreset(t *testing.T) {
	env := newTestEnv(t)
	view, err := env.svc.CreateTemplate(context.Background(), actor(), TemplateSaveInput{
		Name: "SKU标签", ObjectType: ObjectSKULabel, Paper: PaperThermal6040,
		BarcodeSymbology: SymbologyCode128,
		Fields:           []string{"product_name", "sku_code", "product_name"}, // 重复键去重
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if len(view.Fields) != 2 {
		t.Fatalf("去重后应 2 个绑定字段，得到 %d: %+v", len(view.Fields), view.Fields)
	}
	// 预设键序输出：sku_code 在 product_name 之前。
	if view.Fields[0].Key != "sku_code" || view.Fields[1].Key != "product_name" {
		t.Fatalf("预设键序不符: %+v", view.Fields)
	}
	// 展示文案由预设注册表派生（不信前端）。
	if view.Fields[0].Label != "SKU编码" || view.Fields[1].Label != "商品名称" {
		t.Fatalf("预设文案不符: %+v", view.Fields)
	}
}

func TestUpdateTemplate_NotFoundAndSnapshot(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	_, err := env.svc.UpdateTemplate(ctx, actor(), 999, TemplateSaveInput{
		Name: "x", ObjectType: ObjectBinLabel, Paper: PaperThermal4030, BarcodeSymbology: SymbologyCode128,
	})
	asPrintErr(t, err, "PRINT_TEMPLATE_NOT_FOUND")

	tpl := env.seedTemplate(t, ObjectBinLabel, true)
	view, err := env.svc.UpdateTemplate(ctx, actor(), tpl.ID.Int64(), TemplateSaveInput{
		Name: "库位标签v2", ObjectType: ObjectBinLabel, Paper: PaperThermal4030,
		BarcodeSymbology: SymbologyCode128, Fields: []string{"bin_code", "zone_name"},
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if view.Name != "库位标签v2" || view.Paper != PaperThermal4030 || len(view.Fields) != 2 {
		t.Fatalf("更新结果不符: %+v", view)
	}
	// 审计带 before/after（action=update）。
	if !hasAuditAction(env.spy, "update") {
		t.Fatalf("模板修改应有 update 审计: %+v", env.spy.all())
	}
}

func TestCopyTemplate(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	view, err := env.svc.CopyTemplate(context.Background(), actor(), tpl.ID.Int64())
	if err != nil {
		t.Fatalf("复制失败: %v", err)
	}
	if view.ID == strconv.FormatInt(tpl.ID.Int64(), 10) {
		t.Fatalf("副本应有新 ID")
	}
	if view.Name != tpl.Name+copySuffix {
		t.Fatalf("副本名应追加副本标识: %s", view.Name)
	}
	if view.Status != StatusEnabled || view.BarcodeSymbology != SymbologyCode128 {
		t.Fatalf("副本字段不符: %+v", view)
	}
	if !hasAuditAction(env.spy, "copy") {
		t.Fatalf("复制应有 copy 审计")
	}
}

func TestCopyName_TruncatedToColumnWidth(t *testing.T) {
	long := strings.Repeat("码", nameMaxLen) // 128 字节名称（多字节）
	got := copyName(long)
	if len(got) > nameMaxLen {
		t.Fatalf("副本名超列宽: %d", len(got))
	}
	if !strings.HasSuffix(got, copySuffix) {
		t.Fatalf("截断后仍应保留副本标识: %s", got)
	}
}

func TestSetTemplateStatus(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	tpl := env.seedTemplate(t, ObjectSKULabel, true)

	// 非法值。
	_, err := env.svc.SetTemplateStatus(ctx, actor(), tpl.ID.Int64(), "PAUSED")
	if err == nil {
		t.Fatalf("非法状态应报参数错误")
	}
	// 停用。
	view, err := env.svc.SetTemplateStatus(ctx, actor(), tpl.ID.Int64(), StatusDisabled)
	if err != nil || view.Status != StatusDisabled {
		t.Fatalf("停用失败: %v %+v", err, view)
	}
	// 同状态幂等。
	view, err = env.svc.SetTemplateStatus(ctx, actor(), tpl.ID.Int64(), StatusDisabled)
	if err != nil || view.Status != StatusDisabled {
		t.Fatalf("同状态停用应幂等: %v", err)
	}
	// 重新启用。
	view, err = env.svc.SetTemplateStatus(ctx, actor(), tpl.ID.Int64(), StatusEnabled)
	if err != nil || view.Status != StatusEnabled {
		t.Fatalf("启用失败: %v", err)
	}
	if !hasAuditAction(env.spy, "status") {
		t.Fatalf("启停应有 status 审计")
	}
}

func TestListTemplates_PaginationAndFilter(t *testing.T) {
	env := newTestEnv(t)
	for i := 0; i < 3; i++ {
		env.seedTemplate(t, ObjectSKULabel, true)
	}
	env.seedTemplate(t, ObjectInboundOrder, true)

	items, total, err := env.svc.ListTemplates(context.Background(), TemplateFilter{Page: 1, PageSize: 2, ObjectType: ObjectSKULabel})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("分页/过滤不符: total=%d items=%d", total, len(items))
	}
	if _, _, err := env.svc.ListTemplates(context.Background(), TemplateFilter{}); err == nil {
		t.Fatalf("缺分页参数应报错（列表接口强制分页）")
	}
}

// ---- 任务状态机与批量上限（plan §7.2/§13.2/裁决④）----

func TestCreateTask_BatchLimit500(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	for i := 1; i <= MaxDataIDs+1; i++ {
		id := strconv.Itoa(i)
		env.reader.rowsByID[id] = ContentRow{ID: id, Code: "BC" + id}
	}

	ids := make([]string, 0, MaxDataIDs+1)
	for i := 1; i <= MaxDataIDs+1; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	_, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: ids})
	de := asPrintErr(t, err, "PRINT_TOO_MANY_DATA_IDS")
	if de.Details == nil {
		t.Fatalf("超限错误必须带 details（limit/actual）")
	}

	// 恰好 500 成功（裁决④上限值本身放行）。
	ids = ids[:MaxDataIDs]
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: ids})
	if err != nil {
		t.Fatalf("500 行应放行: %v", err)
	}
	if view.TotalCount != MaxDataIDs {
		t.Fatalf("total_count 应为 500，得到 %d", view.TotalCount)
	}
}

func TestCreateTask_InputValidation(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	ctx := context.Background()

	if _, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: 0, DataIDs: []string{"1"}}); err == nil {
		t.Fatalf("template_id 必填")
	}
	if _, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: nil}); err == nil {
		t.Fatalf("data_ids 不能为空")
	}
	if _, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}, Copies: 1000}); err == nil {
		t.Fatalf("份数超上限应报错")
	}
	if _, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{""}}); err == nil {
		t.Fatalf("空标识应报错")
	}
}

func TestCreateTask_TemplateGuards(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: 77, DataIDs: []string{"1"}})
	asPrintErr(t, err, "PRINT_TEMPLATE_NOT_FOUND")

	disabled := env.seedTemplate(t, ObjectSKULabel, false)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "BC1"}
	_, err = env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: disabled.ID.Int64(), DataIDs: []string{"1"}})
	// printing.md §2：停用即不可被新任务选用。
	asPrintErr(t, err, "PRINT_TEMPLATE_DISABLED")
}

func TestCreateTask_DataMissingAllOrNothing(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "BC1"}
	// 2 存在、3 缺失 → 整体拒绝（reader 契约：禁止部分行静默缺失）。
	env.reader.rowsByID["2"] = ContentRow{ID: "2", Code: "BC2"}
	_, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{
		TemplateID: tpl.ID.Int64(), DataIDs: []string{"2", "3"},
	})
	de := asPrintErr(t, err, "PRINT_DATA_NOT_FOUND")
	if de.Details == nil {
		t.Fatalf("缺失错误必须带 details.missing_ids")
	}
	if len(env.queue.all()) != 0 {
		t.Fatalf("装配失败不得入队")
	}
}

func TestCreateTask_OK_QueueSnapshotRows(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "6901234567892", Values: map[string]string{"sku_code": "SKU-1"}}
	env.reader.rowsByID["2"] = ContentRow{ID: "2", Code: "6901234567893", Values: map[string]string{"sku_code": "SKU-2"}}

	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{
		TemplateID: tpl.ID.Int64(), DataIDs: []string{"1", "2"},
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if !strings.HasPrefix(view.PrintNo, "PT-") {
		t.Fatalf("单号应为 PT 前缀（plan §12.3）: %s", view.PrintNo)
	}
	if view.Status != TaskStatusQueued {
		t.Fatalf("初始状态应为 QUEUED，得到 %s", view.Status)
	}
	// reader 收到的绑定键 = 模板 fields（此处模板未绑定 → 空集）。
	if env.reader.lastIDs[0] != "1" || env.reader.lastIDs[1] != "2" {
		t.Fatalf("装配入参应保序: %v", env.reader.lastIDs)
	}
	// 入队捕获：类型/队列归属/TaskID。
	q := env.queue.all()
	if len(q) != 1 {
		t.Fatalf("应恰好入队一次，得到 %d", len(q))
	}
	if q[0].Type != asynqx.TaskTypePrintRender || q[0].TaskID != view.PrintNo {
		t.Fatalf("入队任务不符: %+v", q[0])
	}
	if _, ok := asynqx.QueueFor(q[0].Type); !ok {
		t.Fatalf("任务类型应在冻结注册表")
	}
	// 渲染数据包落库（seq 1 起、code/values 快照）。
	detail, err := env.svc.GetTask(context.Background(), mustParse(t, view.ID))
	if err != nil {
		t.Fatalf("详情失败: %v", err)
	}
	if len(detail.Rows) != 2 || detail.Rows[0].Seq != 1 || detail.Rows[1].Seq != 2 {
		t.Fatalf("行序不符: %+v", detail.Rows)
	}
	if detail.Rows[0].Code != "6901234567892" || detail.Rows[0].Values["sku_code"] != "SKU-1" {
		t.Fatalf("行内容不符: %+v", detail.Rows[0])
	}
	if detail.Template == nil || detail.Template.Name != tpl.Name {
		t.Fatalf("详情应携带模板快照: %+v", detail.Template)
	}
}

func TestExecuteTask_OnceGuardAndAudit(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectInboundOrder, true)
	env.reader.rowsByID["9"] = ContentRow{ID: "9", Code: "IN-20261003-000001", Lines: []map[string]string{{"sku_code": "SKU-1", "qty": "10"}}}
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"9"}})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	ctx := context.Background()

	// 非法结果值。
	if _, err := env.svc.ExecuteTask(ctx, actor(), mustParse(t, view.ID), ExecuteInput{Result: "MAYBE"}); err == nil {
		t.Fatalf("result 值域应校验")
	}
	// 首次确认回填（printed_by/printed_at/result）。
	done, err := env.svc.ExecuteTask(ctx, actor(), mustParse(t, view.ID), ExecuteInput{Result: ResultSuccess})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if done.Result != ResultSuccess || done.PrintedBy != "42" || done.PrintedAt == "" {
		t.Fatalf("回填不符: %+v", done)
	}
	// 回填一次守卫（plan §13.2）：重复确认 409。
	_, err = env.svc.ExecuteTask(ctx, actor(), mustParse(t, view.ID), ExecuteInput{Result: ResultFailed, Message: "再来"})
	asPrintErr(t, err, "PRINT_ALREADY_CONFIRMED")
	// 打印日志审计（printing.md §1.2/§6：谁、何时、用什么模板、打了什么）。
	if !hasAuditActionModule(env.spy, "printing", "execute") {
		t.Fatalf("执行确认应有 execute 审计")
	}
}

func TestHistory_OnlyConfirmed(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectInboundOrder, true)
	ctx := context.Background()
	for _, id := range []string{"1", "2", "3"} {
		env.reader.rowsByID[id] = ContentRow{ID: id, Code: "IN-" + id}
		view, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{id}})
		if err != nil {
			t.Fatalf("创建失败: %v", err)
		}
		if id != "2" { // 2 留在未确认态
			if _, err := env.svc.ExecuteTask(ctx, actor(), mustParse(t, view.ID), ExecuteInput{Result: ResultSuccess}); err != nil {
				t.Fatalf("确认失败: %v", err)
			}
		}
	}
	items, total, err := env.svc.ListHistory(ctx, TaskFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("历史失败: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("历史应为已确认子集: total=%d", total)
	}
	for _, it := range items {
		if it.PrintedBy == "" || it.PrintedAt == "" || it.Result == "" {
			t.Fatalf("历史五字段缺失: %+v", it)
		}
		if it.TemplateName == "" {
			t.Fatalf("模板名应由快照派生: %+v", it)
		}
	}
	// result 筛选。
	failed := ResultFailed
	env.repo.tasks[mustParse(t, items[0].ID)].Result = &failed
	if _, total, _ := env.svc.ListHistory(ctx, TaskFilter{Page: 1, PageSize: 20, Result: ResultFailed}); total != 1 {
		t.Fatalf("result 筛选不符")
	}
}

func TestListTasks_IDExactFilter(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "BC1"}
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	items, total, err := env.svc.ListTasks(context.Background(), TaskFilter{Page: 1, PageSize: 20, ID: mustParse(t, view.ID)})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("id 精确过滤不符: %v %d", err, total)
	}
	if items[0].Rows != nil {
		t.Fatalf("列表不携带 rows（详情端点才携带）")
	}
}

// TestCreateTask_DisabledSKURejected 装配层返回停用拒绝 → 整体拒绝原样透传
// PRINT_SKU_DISABLED + details.disabled_ids（qr-code.md §9 不可打印校验——约束 10
// 「商品已停用」；409 语义），不落任务不入队。
func TestCreateTask_DisabledSKURejected(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.err = NewDataDisabledError([]string{"SKU-OFF"})

	_, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{
		TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"},
	})
	de := asPrintErr(t, err, "PRINT_SKU_DISABLED")
	details, ok := de.Details.(map[string]any)
	if !ok {
		t.Fatalf("停用错误必须带 details.disabled_ids: %+v", de.Details)
	}
	disabled, ok := details["disabled_ids"].([]string)
	if !ok || len(disabled) != 1 || disabled[0] != "SKU-OFF" {
		t.Fatalf("disabled_ids 不符: %+v", details["disabled_ids"])
	}
	if len(env.queue.all()) != 0 {
		t.Fatalf("装配拒绝不得入队")
	}
}

// TestTaskRowDataID_PersistedAndReadBack 行业务身份快照（迁移 000019，qr-code.md
// §7.4）：CreateTask 落库行 data_id=装配行 ID；详情回读一致；旧行 NULL → 空串
// （凡空即前端 fail-closed 不可自动重打）。
func TestTaskRowDataID_PersistedAndReadBack(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["42"] = ContentRow{ID: "42", Code: "BC-42"}
	env.reader.rowsByID["43"] = ContentRow{ID: "43", Code: "BC-43"}

	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{
		TemplateID: tpl.ID.Int64(), DataIDs: []string{"42", "43"},
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	taskID := mustParse(t, view.ID)
	detail, err := env.svc.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("详情失败: %v", err)
	}
	if detail.Rows[0].DataID != "42" || detail.Rows[1].DataID != "43" {
		t.Fatalf("data_id 持久化/回读不符: %+v", detail.Rows)
	}
	// 模拟 000019 之前的旧行（data_id NULL → 空串）。
	env.repo.rows[taskID][0].DataID = ""
	detail, err = env.svc.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("旧行详情失败: %v", err)
	}
	if detail.Rows[0].DataID != "" {
		t.Fatalf("旧行 data_id 应为空串: %+v", detail.Rows[0])
	}
}

// TestListHistory_TemplateIDFilter 历史按模板筛选（只加查询参数不加端点——
// qr-code.md 闭环；命中/过滤空两例）。
func TestListHistory_TemplateIDFilter(t *testing.T) {
	env := newTestEnv(t)
	tplA := env.seedTemplate(t, ObjectSKULabel, true)
	tplB := env.seedTemplate(t, ObjectBinLabel, true)
	ctx := context.Background()
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "BC1"}
	for _, tpl := range []*PrintTemplate{tplA, tplB} {
		view, err := env.svc.CreateTask(ctx, actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}})
		if err != nil {
			t.Fatalf("创建失败: %v", err)
		}
		if _, err := env.svc.ExecuteTask(ctx, actor(), mustParse(t, view.ID), ExecuteInput{Result: ResultSuccess}); err != nil {
			t.Fatalf("确认失败: %v", err)
		}
	}

	if _, total, _ := env.svc.ListHistory(ctx, TaskFilter{Page: 1, PageSize: 20, TemplateID: tplA.ID.Int64()}); total != 1 {
		t.Fatalf("模板 A 筛选应命中 1 条，得到 %d", total)
	}
	if _, total, _ := env.svc.ListHistory(ctx, TaskFilter{Page: 1, PageSize: 20, TemplateID: 999}); total != 0 {
		t.Fatalf("不存在模板应过滤为空，得到 %d", total)
	}
}

// ---- render handler 状态机（QUEUED→PROCESSING→SUCCESS/FAILED，plan §4.2/§13.2）----

func renderPayloadBytes(printNo string) []byte {
	b, _ := json.Marshal(renderPayload{PrintNo: printNo})
	return b
}

func TestHandleRenderTask_StateMachine(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "6901234567892"}
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	err = env.svc.HandleRenderTask(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypePrintRender, Payload: renderPayloadBytes(view.PrintNo), TaskID: view.PrintNo,
	})
	if err != nil {
		t.Fatalf("render 失败: %v", err)
	}
	task, _ := env.repo.FindTask(context.Background(), mustParse(t, view.ID))
	if task.Status != TaskStatusSuccess {
		t.Fatalf("终态应为 SUCCESS，得到 %s", task.Status)
	}

	// 重投递静默丢弃（0 行守卫——plan §4.2）。
	if err := env.svc.HandleRenderTask(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypePrintRender, Payload: renderPayloadBytes(view.PrintNo), TaskID: view.PrintNo,
	}); err != nil {
		t.Fatalf("重投递应静默成功: %v", err)
	}
}

func TestHandleRenderTask_FailFastTerminal(t *testing.T) {
	env := newTestEnv(t)
	tpl := env.seedTemplate(t, ObjectSKULabel, true)
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "BC1"}
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	env.repo.mu.Lock()
	env.repo.failFindTaskByNo = true
	env.repo.mu.Unlock()

	// 预生成阶段失败：fail-fast 落 FAILED 终态（纯函数失败重试无增益），返回 nil。
	if err := env.svc.HandleRenderTask(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypePrintRender, Payload: renderPayloadBytes(view.PrintNo), TaskID: view.PrintNo,
	}); err != nil {
		t.Fatalf("render 失败路径应返回 nil: %v", err)
	}
	env.repo.mu.Lock()
	env.repo.failFindTaskByNo = false
	env.repo.mu.Unlock()
	task, _ := env.repo.FindTask(context.Background(), mustParse(t, view.ID))
	if task.Status != TaskStatusFailed || task.ErrorMessage == "" {
		t.Fatalf("终态应为 FAILED 且带 error_message: %+v", task)
	}
	// FAILED 为终态：重投递静默丢弃。
	if err := env.svc.HandleRenderTask(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypePrintRender, Payload: renderPayloadBytes(view.PrintNo), TaskID: view.PrintNo,
	}); err != nil {
		t.Fatalf("FAILED 重投递应静默: %v", err)
	}
}

func TestHandleRenderTask_PregeneratesIntoCache(t *testing.T) {
	env := newTestEnv(t)
	sym := SymbologyCode128
	tpl := &PrintTemplate{
		Name: "缓存模板", ObjectType: ObjectSKULabel, Paper: PaperThermal6040,
		BarcodeSymbology: &sym, QRCodeEnabled: true,
		Fields: jsonb("{}"), Status: StatusEnabled,
	}
	if err := env.repo.InsertTemplate(env.repo.DB(), tpl); err != nil {
		t.Fatalf("seed 失败: %v", err)
	}
	env.reader.rowsByID["1"] = ContentRow{ID: "1", Code: "SF-CACHE-1"}
	view, err := env.svc.CreateTask(context.Background(), actor(), TaskCreateInput{TemplateID: tpl.ID.Int64(), DataIDs: []string{"1"}})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := env.svc.HandleRenderTask(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypePrintRender, Payload: renderPayloadBytes(view.PrintNo), TaskID: view.PrintNo,
	}); err != nil {
		t.Fatalf("render 失败: %v", err)
	}
	// 预生成进缓存：主码（默认 320×120）与二维码（默认 240×240）均命中。
	for _, key := range []string{
		cacheKey(SymbologyCode128, "SF-CACHE-1", DefaultBarcodeWidth, DefaultBarcodeHeight),
		cacheKey(SymbologyQR, "SF-CACHE-1", DefaultQRSize, DefaultQRSize),
	} {
		if _, ok := env.svc.cache.get(key); !ok {
			t.Fatalf("预生成应命中缓存键 %s", key)
		}
	}
}

// ---- 工具 ----

func mustParse(t *testing.T, s string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("非法数字串: %s", s)
	}
	return id
}

func hasAuditAction(spy *auditSpy, action string) bool {
	for _, e := range spy.all() {
		if e.Action == action {
			return true
		}
	}
	return false
}

func hasAuditActionModule(spy *auditSpy, module, action string) bool {
	for _, e := range spy.all() {
		if e.Module == module && e.Action == action {
			return true
		}
	}
	return false
}
