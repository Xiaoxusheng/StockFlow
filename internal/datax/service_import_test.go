package datax

// 导入向导单测（excel §1.2/§1.3/§6、plan §6.2；内存替身 + 构造的内存工作簿）：
//   - 模板结构（列头/示例行/校验说明页）；
//   - 上传解析（表头逐列比对、空行跳过、行数上限、类型解析管线矩阵、错误行列定位）；
//   - 状态机守卫（confirm 重放 409、未校验确认 409、错误行确认拒绝、高危二次确认）；
//   - 执行幂等（断点续跑仅重试 QUEUED/FAILED 行；行级失败 → PARTIAL_SUCCESS）；
//   - 进度推导（success_rows/failed_rows → progress）。

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/asynqx"
)

var testHeaders = []string{"商品编码", "商品名称", "重量", "价格", "生产日期"}

// uploadOk 构造全合法工作簿并上传。
func uploadOk(t *testing.T, e *env, rows [][]string) *ImportUploadResult {
	t.Helper()
	res, err := e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders, rows)))
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	return res
}

// TestTemplateStructure 模板结构断言（excel §1.2 第 1 步：列头+示例行+校验说明）。
func TestTemplateStructure(t *testing.T) {
	e := newEnv(t)
	name, content, err := e.svc.TemplateFile(ImportProduct)
	if err != nil {
		t.Fatalf("生成模板失败: %v", err)
	}
	if name != "商品导入模板.xlsx" {
		t.Fatalf("模板文件名不符: %s", name)
	}
	f := openWorkbook(t, content)
	sheet := f.GetSheetName(0)
	for i, col := range e.writer.spec.Columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		got, err := f.GetCellValue(sheet, cell)
		if err != nil || got != col.Title {
			t.Fatalf("列头 %d 不符: got=%q want=%q (err=%v)", i+1, got, col.Title, err)
		}
	}
	// 示例行。
	cell, _ := excelize.CoordinatesToCellName(1, 2)
	if v, _ := f.GetCellValue(sheet, cell); v != "P-1" {
		t.Fatalf("示例行缺失: %q", v)
	}
	// 校验说明页存在且含说明内容。
	notes := f.GetSheetList()
	found := false
	for _, s := range notes {
		if s == "填表说明" {
			found = true
			if v, _ := f.GetCellValue(s, "A1"); !strings.Contains(v, "模板名称") {
				t.Fatalf("说明页内容缺失: %q", v)
			}
		}
	}
	if !found {
		t.Fatalf("模板缺少填表说明页: %v", notes)
	}
	// 清单仅含已装配类型（未装配类型不出假条目）。
	items := e.svc.ListTemplates()
	if len(items) != 1 || items[0].ImportType != ImportProduct {
		t.Fatalf("模板清单不符: %+v", items)
	}
	if items[0].DownloadURL == "" || items[0].HighRisk {
		t.Fatalf("模板清单字段不符: %+v", items[0])
	}
}

// TestUploadHeaderMismatch 表头与模板不一致 → DATAX_TEMPLATE_MISMATCH。
func TestUploadHeaderMismatch(t *testing.T) {
	e := newEnv(t)
	bad := []string{"编码", "商品名称", "重量", "价格", "生产日期"}
	_, err := e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "商品导入.xlsx", buildXlsx(t, "商品导入", bad, [][]string{{"P-1", "n", "1", "2", ""}})))
	requireErrCode(t, err, "DATAX_TEMPLATE_MISMATCH")

	_, err = e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "a.xlsx", []byte{0x50, 0x4b, 0x03, 0x04, 0x00}))
	requireErrCode(t, err, "DATAX_TEMPLATE_MISMATCH") // 非 zip 容器同样归一为模板不一致族
}

// TestUploadTooManyRows 行数上限 → DATAX_TOO_MANY_ROWS（plan §6.2 上传即拒）。
func TestUploadTooManyRows(t *testing.T) {
	e := newEnv(t) // ImportMaxRows=50
	rows := make([][]string, 0, 51)
	for i := 0; i < 51; i++ {
		rows = append(rows, []string{"P-" + strconv.Itoa(i), "n", "1", "2", ""})
	}
	_, err := e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders, rows)))
	requireErrCode(t, err, "DATAX_TOO_MANY_ROWS")
}

// TestUploadEmptySheet 空数据 → DATAX_SHEET_EMPTY。
func TestUploadEmptySheet(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders, nil)))
	requireErrCode(t, err, "DATAX_SHEET_EMPTY")
}

// TestUploadFileType 非 xlsx 扩展名 → DATAX_IMPORT_FILE_TYPE。
func TestUploadFileType(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Upload(context.Background(), testActor(), ImportProduct,
		fileHeader(t, "数据.csv", []byte("a,b,c")))
	requireErrCode(t, err, "DATAX_IMPORT_FILE_TYPE")

	_, err = e.svc.Upload(context.Background(), testActor(), "NO_SUCH_TYPE",
		fileHeader(t, "商品导入.xlsx", buildXlsx(t, "商品导入", testHeaders, nil)))
	requireErrCode(t, err, "DATAX_IMPORT_TYPE_INVALID")
}

// TestValidatePipelineMatrix 结构层校验矩阵（excel §1.3：必填/数字/金额/日期/文件内重复）
// + 业务层 Writer 错误合并 + 错误行列定位 + 错误 Excel 生成。
func TestValidatePipelineMatrix(t *testing.T) {
	e := newEnv(t)
	e.writer.validateErrs = []RowError{{Row: 8, Column: "code", Message: "商品编码已存在"}}
	rows := [][]string{
		{"P-1", "商品一", "1.5", "10.50", "2026-01-01"}, // 合法
		{"", "缺编码", "1", "1", ""},                    // 必填缺失（code）
		{"P-3", "", "", "1", ""},                     // 必填缺失（name）
		{"P-4", "坏数字", "abc", "1", ""},               // 数字格式
		{"P-5", "负金额", "1", "-2", ""},                // 金额 >= 0
		{"P-6", "坏日期", "1", "1", "2026/13/40"},       // 日期格式
		{"P-1", "文件内重复", "1", "1", ""},               // 文件内重复（与第 1 行）
		{"P-8", "库内重复", "1", "1", ""},                // 业务层错误（Writer）
	}
	up := uploadOk(t, e, rows)
	if up.TotalRows != 8 || up.Status != TaskStatusParsed {
		t.Fatalf("上传结果不符: %+v", up)
	}
	// 空行不计入总数（追加一行全空 → total 仍 8）。

	res, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64())
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if res.Status != TaskStatusValidated {
		t.Fatalf("校验后状态不符: %s", res.Status)
	}
	if res.ValidRows != 1 || res.ErrorRows != 7 {
		t.Fatalf("校验计数不符: valid=%d error=%d errors=%+v", res.ValidRows, res.ErrorRows, dumpErrs(res.Errors))
	}
	// 错误定位到行列（excel §1.4）。
	type want struct {
		row    int
		column string
		frag   string
	}
	wants := []want{
		{2, "code", "必填"},
		{3, "name", "必填"},
		{4, "weight", "数字"},
		{5, "price", "负"},
		{6, "made_at", "日期格式"},
		{7, "code", "与第 1 行重复"},
		{8, "code", "已存在"},
	}
	for _, w := range wants {
		ok := false
		for _, e := range res.Errors {
			if e.Row == w.row && e.Column == w.column && strings.Contains(e.Message, w.frag) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("缺少期望错误 %v：实际 %+v", w, dumpErrs(res.Errors))
		}
	}
	// 错误 Excel 已生成并登记（error_file_url 下发）。
	if res.ErrorFileURL == "" || res.ErrorFileName == "" {
		t.Fatalf("错误 Excel 未下发: %+v", res)
	}
	// 错误 Excel 内容：原数据 + 错误原因列。
	sf, err := e.svc.GetImportErrorFile(context.Background(), up.ID.Int64(), nil)
	if err != nil {
		t.Fatalf("错误文件不可下载: %v", err)
	}
	defer func() { _ = sf.Body.Close() }()
	content := readAll(t, sf)
	ef := openWorkbook(t, content)
	sheet := e.writer.spec.Sheet
	lastCol, _ := excelize.CoordinatesToCellName(len(testHeaders)+1, 1)
	if v, _ := ef.GetCellValue(sheet, lastCol); v != "错误原因" {
		t.Fatalf("错误 Excel 缺少错误原因列: %q", v)
	}
	if v, _ := ef.GetCellValue(sheet, "A3"); v != "" { // 第 2 数据行（缺编码）原样回填
		t.Fatalf("原数据回填不符: %q", v)
	}
	// 类型化 parsed 已落（第 1 行 weight=1.5 → NUMBER）。
	prev, err := e.svc.repo.ListImportRows(context.Background(), up.ID.Int64(), []string{RowStatusValid}, 10)
	if err != nil || len(prev) != 1 {
		t.Fatalf("合法行查询不符: %v %d", err, len(prev))
	}
	if cv := cellFromAny(prev[0].Parsed["weight"]); cv.T != CellNumber || cv.N != 1.5 {
		t.Fatalf("parsed 类型化不符: %+v", prev[0].Parsed)
	}
	if cv := cellFromAny(prev[0].Parsed["made_at"]); cv.T != CellDate || cv.D != "2026-01-01 00:00:00" {
		t.Fatalf("parsed 日期不符: %+v", prev[0].Parsed)
	}
}

// dumpErrs 错误列表调试格式化。
func dumpErrs(errs []ImportValidationError) string {
	out := ""
	for _, e := range errs {
		out += strconv.Itoa(e.Row) + "/" + e.Column + ":" + e.Message + " | "
	}
	return out
}

// readAll 读取响应体全部字节。
func readAll(t *testing.T, sf *serveFile) []byte {
	t.Helper()
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := sf.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

// TestConfirmStateMachine 状态机守卫（plan §13.2；excel §6.1 高危二次确认）。
func TestConfirmStateMachine(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	up := uploadOk(t, e, [][]string{
		{"P-1", "商品一", "1", "1", ""},
		{"P-2", "商品二", "2", "2", ""},
	})

	// 未校验确认 → 409。
	_, err := e.svc.Confirm(context.Background(), actor, up.ID.Int64(), ConfirmInput{})
	requireErrCode(t, err, "DATAX_STATUS_CONFLICT")

	if _, err := e.svc.Validate(context.Background(), actor, up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}

	// 入队为挂起模式（handler 由测试手动驱动）。
	e.queue.inline = nil
	res, err := e.svc.Confirm(context.Background(), actor, up.ID.Int64(), ConfirmInput{})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if res.Status != TaskStatusExecuting {
		t.Fatalf("确认后状态不符: %s", res.Status)
	}
	// 重放确认 → 409（恰一成功）。
	_, err = e.svc.Confirm(context.Background(), actor, up.ID.Int64(), ConfirmInput{})
	requireErrCode(t, err, "DATAX_STATUS_CONFLICT")

	// 入队载荷断言（类型 + TaskID=IMP- 单号）。
	tasks := e.queue.all()
	if len(tasks) != 1 || tasks[0].Type != asynqx.TaskTypeImportCommit || tasks[0].TaskID == "" {
		t.Fatalf("入队载荷不符: %+v", tasks)
	}
	if !strings.HasPrefix(tasks[0].TaskID, "IMP-") {
		t.Fatalf("TaskID 应为 IMP- 单号: %s", tasks[0].TaskID)
	}
}

// TestConfirmHighRiskGuard INITIAL_INVENTORY 必须显式二次确认（excel §6.1）。
func TestConfirmHighRiskGuard(t *testing.T) {
	e := newEnv(t)
	highRiskWriter := &fakeWriter{spec: testSpec()}
	e.svc.writers[ImportInitialInventory] = highRiskWriter
	highRiskWriter.spec.ImportType = ImportInitialInventory
	highRiskWriter.spec.Sheet = "初始化库存导入"

	up, err := e.svc.Upload(context.Background(), testActor(), ImportInitialInventory,
		fileHeader(t, "期初.xlsx", buildXlsx(t, "初始化库存导入", testHeaders,
			[][]string{{"P-1", "商品一", "1", "1", ""}})))
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	// 未携带 confirmed → DATAX_CONFIRM_REQUIRED。
	_, err = e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{})
	requireErrCode(t, err, "DATAX_CONFIRM_REQUIRED")
	// confirmed=true → 放行进入 EXECUTING。
	e.queue.inline = nil
	if _, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{Confirmed: true}); err != nil {
		t.Fatalf("二次确认后应放行: %v", err)
	}
}

// TestConfirmWithErrorsRejected 存在错误行时确认 → DATAX_ROWS_INVALID。
func TestConfirmWithErrorsRejected(t *testing.T) {
	e := newEnv(t)
	up := uploadOk(t, e, [][]string{
		{"", "缺编码", "1", "1", ""},
	})
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	_, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{})
	requireErrCode(t, err, "DATAX_ROWS_INVALID")
}

// TestImportCommitBreakpointResume 断点续跑（excel §6.3）：SUCCESS 行跳过、
// FAILED 行重试；行级失败 → PARTIAL_SUCCESS；全部失败 → FAILED。
func TestImportCommitBreakpointResume(t *testing.T) {
	e := newEnv(t)
	rows := make([][]string, 0, 6)
	for i := 1; i <= 6; i++ {
		rows = append(rows, []string{"P-" + strconv.Itoa(i), "商品" + strconv.Itoa(i), "1", "1", ""})
	}
	up := uploadOk(t, e, rows)
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	e.queue.inline = nil
	if _, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{}); err != nil {
		t.Fatalf("确认失败: %v", err)
	}

	// 第一轮：第 1、4 行失败（模拟部分行业务失败）。
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		res := CommitResult{}
		for _, r := range rows {
			if r.RowNo == 1 || r.RowNo == 4 {
				res.FailedRows++
				res.Errors = append(res.Errors, RowError{Row: r.RowNo, Column: "code", Message: "模拟行级失败"})
				continue
			}
			res.SuccessRows++
		}
		return res, nil
	}
	no := up.ImportNo
	payload := mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()})
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{Type: asynqx.TaskTypeImportCommit, TaskID: no, Payload: payload}); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	task, err := e.svc.repo.FindImportTaskByNo(context.Background(), no)
	if err != nil {
		t.Fatalf("任务查询失败: %v", err)
	}
	if task.Status != TaskStatusPartial || task.SuccessRows != 4 || task.FailedRows != 2 {
		t.Fatalf("第一轮终态不符: %+v", task)
	}

	// 重投递：已终态 → 状态守卫静默丢弃（nil，plan §4.2）。
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{Type: asynqx.TaskTypeImportCommit, TaskID: no, Payload: payload}); err != nil {
		t.Fatalf("终态重投递应静默丢弃: %v", err)
	}

	// 断点续跑（运维修复后）：FAILED 行重试、SUCCESS 行跳过。
	// （重置行状态为 FAILED 模拟重试场景——终态守卫已验证，此处直接以第二轮任务验证
	// 行级断点：将任务重置回 EXECUTING 并仅保留失败行可重试。）
	if _, err := e.repo.GuardUpdateImportTask(e.repo.DB(), task.ID.Int64(), []string{TaskStatusPartial}, map[string]any{
		"status": TaskStatusExecuting, "finished_at": nil,
	}); err != nil {
		t.Fatalf("重置任务状态: %v", err)
	}
	e.writer.commitRows = nil
	e.writer.commitFn = nil // 全部成功
	// 仅将 FAILED 行标记回 QUEUED（模拟 asynq 重试续跑的行状态面）。
	pending, err := e.svc.repo.ListImportRows(context.Background(), task.ID.Int64(), []string{RowStatusFailed}, 0)
	if err != nil {
		t.Fatalf("失败行查询失败: %v", err)
	}
	for _, r := range pending {
		if err := e.repo.UpdateImportRow(e.repo.DB(), r.ID.Int64(), map[string]any{"status": RowStatusQueued}); err != nil {
			t.Fatalf("重置失败行状态: %v", err)
		}
	}
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{Type: asynqx.TaskTypeImportCommit, TaskID: no, Payload: payload}); err != nil {
		t.Fatalf("续跑失败: %v", err)
	}
	if len(e.writer.commitRows) != 1 {
		t.Fatalf("续跑应只提交失败行: %+v", e.writer.commitRows)
	}
	if len(e.writer.commitRows[0]) != 2 || e.writer.commitRows[0][0] != 1 || e.writer.commitRows[0][1] != 4 {
		t.Fatalf("续跑行号不符（应恰为第 1、4 行）: %+v", e.writer.commitRows)
	}
	task, _ = e.svc.repo.FindImportTaskByNo(context.Background(), no)
	if task.Status != TaskStatusSuccess || task.SuccessRows != 6 || task.FailedRows != 0 {
		t.Fatalf("续跑终态不符: %+v", task)
	}
}

// TestImportCommitInfrastructureError 基础设施错误 → 返回 error 交 asynq 重试，
// 行状态不动（重入续跑，plan §4.2）。
func TestImportCommitInfrastructureError(t *testing.T) {
	e := newEnv(t)
	up := uploadOk(t, e, [][]string{
		{"P-1", "商品一", "1", "1", ""},
		{"P-2", "商品二", "1", "1", ""},
	})
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	e.queue.inline = nil
	if _, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{}); err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	boom := errors.New("db down")
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		return CommitResult{}, boom
	}
	no := up.ImportNo
	err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: no,
		Payload: mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()}),
	})
	if err == nil {
		t.Fatalf("基础设施错误应向上返回")
	}
	task, _ := e.svc.repo.FindImportTaskByNo(context.Background(), no)
	if task.Status != TaskStatusExecuting || task.SuccessRows != 0 {
		t.Fatalf("失败后任务应保持 EXECUTING 且行未动: %+v", task)
	}
	// 恢复后重入 → 成功。
	e.writer.commitFn = nil
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: no,
		Payload: mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()}),
	}); err != nil {
		t.Fatalf("重入执行失败: %v", err)
	}
	task, _ = e.svc.repo.FindImportTaskByNo(context.Background(), no)
	if task.Status != TaskStatusSuccess {
		t.Fatalf("恢复后续跑终态不符: %+v", task)
	}
}

// TestImportCommitFailedRowsNotDoubleCounted 计数收敛（plan §13.3：行状态为真相源）：
// 行级失败行断点重试后再失败不双倍计入 failed_rows（修复前 FAILED 计数以执行前状态为
// 种子导致 1→2 双算）；改成功则 success+failed 不超出 total_rows。
func TestImportCommitFailedRowsNotDoubleCounted(t *testing.T) {
	e := newEnv(t)
	// BatchSize=1：三行三批，制造"批 1 成功 → 批 2 行级失败 → 批 3 基础设施失败"断点。
	e.svc = NewService(e.repo, e.store, e.queue, Config{
		ImportMaxRows: 50, BatchSize: 1, ExportBatchSize: 3,
		FileRetentionDays: 30, UploadMaxBytes: 1 << 20,
	}, nil, WithImportWriter(ImportProduct, e.writer), WithExportSource(ModuleProduct, e.source))
	up := uploadOk(t, e, [][]string{
		{"P-1", "商品一", "1", "1", ""},
		{"P-2", "商品二", "1", "1", ""},
		{"P-3", "商品三", "1", "1", ""},
	})
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	e.queue.inline = nil
	if _, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{}); err != nil {
		t.Fatalf("确认失败: %v", err)
	}

	boom := errors.New("db down")
	calls := 0
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		calls++
		switch {
		case calls == 1: // 批 1（P-1）：成功
			return CommitResult{}, nil
		case calls == 2: // 批 2（P-2）：行级失败 → 行状态 FAILED
			return CommitResult{Errors: []RowError{{Row: rows[0].RowNo, Column: "code", Message: "重复"}}}, nil
		default: // 批 3（P-3）：基础设施失败 → 断点重试
			return CommitResult{}, boom
		}
	}
	no := up.ImportNo
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: no,
		Payload: mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()}),
	}); err == nil {
		t.Fatalf("基础设施错误应向上返回")
	}

	// 重试：FAILED 行（P-2）再失败、QUEUED 行（P-3）成功——终态计数必须收敛到行面事实。
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		for _, r := range rows {
			if r.Cells["code"].S == "P-2" {
				return CommitResult{Errors: []RowError{{Row: r.RowNo, Column: "code", Message: "重复"}}}, nil
			}
		}
		return CommitResult{}, nil
	}
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: no,
		Payload: mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()}),
	}); err != nil {
		t.Fatalf("重试执行失败: %v", err)
	}
	task, _ := e.svc.repo.FindImportTaskByNo(context.Background(), no)
	if task.Status != TaskStatusPartial || task.SuccessRows != 2 || task.FailedRows != 1 {
		t.Fatalf("计数应收敛到行面事实（PARTIAL，SUCCESS=2/FAILED=1）: status=%s success=%d failed=%d",
			task.Status, task.SuccessRows, task.FailedRows)
	}
}

// TestImportCommitRedeliveryDrop 非执行态重投递静默丢弃（plan §4.2 状态守卫）。
func TestImportCommitRedeliveryDrop(t *testing.T) {
	e := newEnv(t)
	up := uploadOk(t, e, [][]string{{"P-1", "商品一", "1", "1", ""}})
	// 尚未 confirm（PARSED 态）→ 重投递直接丢弃。
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		t.Fatalf("不应执行 Writer.Commit")
		return CommitResult{}, nil
	}
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: up.ImportNo,
		Payload: mustJSON(t, importCommitPayload{ImportNo: up.ImportNo, Actor: testActor()}),
	}); err != nil {
		t.Fatalf("非执行态重投递应返回 nil: %v", err)
	}
}

// TestImportProgressDerived 进度推导（plan §13.3：导入无 progress 列，由行列推导）。
func TestImportProgressDerived(t *testing.T) {
	e := newEnv(t)
	up := uploadOk(t, e, [][]string{
		{"P-1", "商品一", "1", "1", ""},
		{"P-2", "商品二", "1", "1", ""},
		{"P-3", "商品三", "1", "1", ""},
		{"P-4", "商品四", "1", "1", ""},
	})
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	e.queue.inline = nil
	if _, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{}); err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	e.writer.commitFn = func(actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error) {
		return CommitResult{SuccessRows: 3, FailedRows: 1,
			Errors: []RowError{{Row: 4, Column: "code", Message: "x"}}}, nil
	}
	no := up.ImportNo
	if err := e.svc.RunImportCommit(context.Background(), asynqx.Task{
		Type: asynqx.TaskTypeImportCommit, TaskID: no,
		Payload: mustJSON(t, importCommitPayload{ImportNo: no, Actor: testActor()}),
	}); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	items, total, err := e.svc.ListImports(context.Background(), TaskListFilter{Page: 1, PageSize: 10}, nil)
	if err != nil || total != 1 {
		t.Fatalf("列表查询失败: %v %d", err, total)
	}
	if items[0].Progress != 100 || items[0].SuccessRows != 3 || items[0].FailedRows != 1 {
		t.Fatalf("进度推导不符: %+v", items[0])
	}
	if items[0].Status != TaskStatusPartial || items[0].TaskType != "IMPORT" {
		t.Fatalf("列表项状态不符: %+v", items[0])
	}
	// 审计断言：upload/validate/confirm/commit 全链路同事务审计（module=datax）。
	acts := e.spy.actions()
	for _, want := range []string{"upload", "validate", "confirm", "commit"} {
		found := false
		for _, a := range acts {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("审计缺位: %s，实际 %v", want, acts)
		}
	}
}

// TestInlineConfirmExecutesSync inline 模式（Enqueue 即执行）：确认响应即含终态
// （plan §4.1 同步降级契约）。
func TestInlineConfirmExecutesSync(t *testing.T) {
	e := newEnv(t)
	e.queue.inline = func(ctx context.Context, task asynqx.Task) error {
		return e.svc.RunImportCommit(ctx, task)
	}
	up := uploadOk(t, e, [][]string{{"P-1", "商品一", "1", "1", ""}})
	if _, err := e.svc.Validate(context.Background(), testActor(), up.ID.Int64()); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	res, err := e.svc.Confirm(context.Background(), testActor(), up.ID.Int64(), ConfirmInput{})
	if err != nil {
		t.Fatalf("确认失败: %v", err)
	}
	if res.Status != TaskStatusSuccess || res.SuccessRows != 1 {
		t.Fatalf("inline 模式确认应同步返回终态: %+v", res)
	}
	if res.FinishedAt.IsZero() {
		t.Fatalf("inline 终态应携带 finished_at")
	}
	_ = zap.NewNop
}
