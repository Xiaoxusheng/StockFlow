package datax

// 执行期失败行错误 Excel 合成下载单测（效率层一期 ask：失败行 xlsx 下载内容含
// 原始数据与错误原因；内存替身零外部依赖）：
//   - PARTIAL_SUCCESS/FAILED 任务（无 error_file_id）按 import_task_rows 现场合成，
//     内容 = 既有错误 Excel 同构（原数据回填 + 「错误原因」列 + 填表说明页）；
//   - 仅 INVALID/FAILED 行进入错误簿（成功行不得出现）；
//   - 数据权限：范围受限非创建人 404 防探测；全量范围/创建人可下载；
//   - 无失败行任务保持 404 口径（handler_test.go:488 既有断言不受影响）。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/stockflow/server/internal/response"
)

// seedExecuteFailedTask 构造执行期部分失败任务：3 行（SUCCESS / FAILED×2 带 errors）。
func seedExecuteFailedTask(t *testing.T, e *env) *ImportTask {
	t.Helper()
	actor := testActor()
	task := &ImportTask{
		ImportType: ImportProduct, Status: TaskStatusPartial,
		TotalRows: 3, SuccessRows: 1, FailedRows: 2,
		CreatedBy: actor.UserID, UpdatedBy: actor.UserID,
	}
	if err := e.repo.InsertImportTask(context.Background(), e.repo.DB(), task); err != nil {
		t.Fatalf("seed 任务失败: %v", err)
	}
	raw := func(code string) JSONMap {
		return JSONMap{"code": code, "name": "商品" + code, "weight": "1.5", "price": "9.9", "made_at": "2026-01-02 03:04:05"}
	}
	rows := []*ImportTaskRow{
		{TaskID: task.ID.Int64(), RowNo: 1, Raw: raw("P-OK"), Status: RowStatusSuccess, CreatedBy: actor.UserID},
		{TaskID: task.ID.Int64(), RowNo: 2, Raw: raw("P-DUP"), Status: RowStatusFailed,
			Errors: []RowError{{Row: 2, Column: "code", Message: "SKU 编码已存在"}}, CreatedBy: actor.UserID},
		{TaskID: task.ID.Int64(), RowNo: 3, Raw: raw("P-BAD"), Status: RowStatusFailed,
			Errors: []RowError{{Row: 3, Column: "name", Message: "SKU 名称不能为空"}}, CreatedBy: actor.UserID},
	}
	if err := e.repo.InsertImportRows(e.repo.DB(), rows); err != nil {
		t.Fatalf("seed 任务行失败: %v", err)
	}
	return task
}

// TestImportErrorFileSynthesizedForExecuteFailedRows 失败行下载内容：原始数据 +
// 错误原因齐备，成功行不出现。
func TestImportErrorFileSynthesizedForExecuteFailedRows(t *testing.T) {
	e := newEnv(t)
	task := seedExecuteFailedTask(t, e)

	sf, err := e.svc.GetImportErrorFile(context.Background(), task.ID.Int64(), nil)
	if err != nil {
		t.Fatalf("合成下载失败: %v", err)
	}
	defer func() { _ = sf.Body.Close() }()
	if sf.ID != 0 {
		t.Fatalf("合成产物无 files 登记行，ID 应为 0: %d", sf.ID)
	}
	if !strings.HasPrefix(sf.FileName, "导入错误明细-IMP-") {
		t.Fatalf("文件名形态不符: %s", sf.FileName)
	}

	ef := openWorkbook(t, readAll(t, sf))
	sheet := e.writer.spec.Sheet
	reasonCol := len(e.writer.spec.Columns) + 1

	// 表头末列 = 错误原因（与既有错误 Excel 同构）。
	head, _ := excelize.CoordinatesToCellName(reasonCol, 1)
	if v, _ := ef.GetCellValue(sheet, head); v != "错误原因" {
		t.Fatalf("缺少错误原因列头: %q", v)
	}
	// 物理行 = 数据行号 + 1（表头占 1 行；buildErrorWorkbook 同口径）。
	expect := map[int]struct {
		rawCode string
		reason  string
	}{
		2: {"P-DUP", "SKU 编码已存在"},
		3: {"P-BAD", "SKU 名称不能为空"},
	}
	for rowNo, want := range expect {
		codeCell, _ := excelize.CoordinatesToCellName(1, rowNo+1)
		if v, _ := ef.GetCellValue(sheet, codeCell); v != want.rawCode {
			t.Fatalf("第 %d 行原始数据回填不符: got=%q want=%q", rowNo, v, want.rawCode)
		}
		reasonCell, _ := excelize.CoordinatesToCellName(reasonCol, rowNo+1)
		if v, _ := ef.GetCellValue(sheet, reasonCell); !strings.Contains(v, want.reason) {
			t.Fatalf("第 %d 行错误原因不符: got=%q want 含 %q", rowNo, v, want.reason)
		}
	}
	// 成功行（RowNo=1 → 物理行 2）不得被错误簿写入：错误原因列为空；原始数据列
	// 保持模板示例行值（"P-1"）而非成功行数据（"P-OK"）——buildErrorWorkbook 既有
	// 语义：RowNo=1 的错误行会覆盖示例行位，此处成功行未被任何错误行触达。
	okCode, _ := excelize.CoordinatesToCellName(1, 2)
	if v, _ := ef.GetCellValue(sheet, okCode); v == "P-OK" {
		t.Fatalf("成功行原始数据不应被回填: %q", v)
	}
	okReason, _ := excelize.CoordinatesToCellName(reasonCol, 2)
	if v, _ := ef.GetCellValue(sheet, okReason); v != "" {
		t.Fatalf("成功行不应有错误原因: %q", v)
	}
	// 既有列的原始数据完整回填（weight/price 列抽检）。
	wCell, _ := excelize.CoordinatesToCellName(3, 4)
	if v, _ := ef.GetCellValue(sheet, wCell); v != "1.5" {
		t.Fatalf("原始数据列回填不符 (weight): %q", v)
	}
}

// TestImportErrorFileSynthesizedScope 数据权限：范围受限非创建人 404（防探测）；
// 创建人/全量范围可下载。
func TestImportErrorFileSynthesizedScope(t *testing.T) {
	e := newEnv(t)
	task := seedExecuteFailedTask(t, e)
	id := task.ID.Int64()

	if _, err := e.svc.GetImportErrorFile(context.Background(), id, &FileScope{All: false, UserID: 99}); err == nil {
		t.Fatal("范围受限非创建人应拒绝")
	} else {
		var re *response.Error
		if !errors.As(err, &re) || !strings.HasPrefix(re.Error(), "DATAX_FILE_NOT_FOUND") {
			t.Fatalf("越界访问应按不存在处理: %v", err)
		}
	}
	if _, err := e.svc.GetImportErrorFile(context.Background(), id, &FileScope{All: false, UserID: 42}); err != nil {
		t.Fatalf("创建人应可下载: %v", err)
	}
	if _, err := e.svc.GetImportErrorFile(context.Background(), id, &FileScope{All: true, UserID: 99}); err != nil {
		t.Fatalf("全量范围应可下载: %v", err)
	}
}

// TestImportErrorFileNoFailedRowsStill404 无 INVALID/FAILED 行的任务保持 404 口径。
func TestImportErrorFileNoFailedRowsStill404(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	task := &ImportTask{
		ImportType: ImportProduct, Status: TaskStatusSuccess,
		TotalRows: 1, SuccessRows: 1, FailedRows: 0,
		CreatedBy: actor.UserID,
	}
	if err := e.repo.InsertImportTask(context.Background(), e.repo.DB(), task); err != nil {
		t.Fatalf("seed 任务失败: %v", err)
	}
	if err := e.repo.InsertImportRows(e.repo.DB(), []*ImportTaskRow{
		{TaskID: task.ID.Int64(), RowNo: 1, Raw: JSONMap{"code": "P-1"}, Status: RowStatusSuccess, CreatedBy: actor.UserID},
	}); err != nil {
		t.Fatalf("seed 任务行失败: %v", err)
	}
	_, err := e.svc.GetImportErrorFile(context.Background(), task.ID.Int64(), nil)
	var re *response.Error
	if !errors.As(err, &re) || !strings.HasPrefix(re.Error(), "DATAX_FILE_NOT_FOUND") {
		t.Fatalf("无失败行任务应 404 DATAX_FILE_NOT_FOUND: %v", err)
	}
}

// TestImportErrorFileWriterMissingFailClosed Writer 缺位 fail-closed（不产空簿假成功）。
func TestImportErrorFileWriterMissingFailClosed(t *testing.T) {
	e := newEnv(t)
	// 构造 Writer 未装配的导入类型任务（SALES_ORDER 值合法但测试环境未装配 Writer）。
	task := &ImportTask{ImportType: "SALES_ORDER", Status: TaskStatusPartial, CreatedBy: 42}
	if err := e.repo.InsertImportTask(context.Background(), e.repo.DB(), task); err != nil {
		t.Fatalf("seed 任务失败: %v", err)
	}
	if err := e.repo.InsertImportRows(e.repo.DB(), []*ImportTaskRow{
		{TaskID: task.ID.Int64(), RowNo: 1, Raw: JSONMap{"so_no": "SO-1"}, Status: RowStatusFailed,
			Errors: []RowError{{Row: 1, Message: "客户不存在"}}, CreatedBy: 42},
	}); err != nil {
		t.Fatalf("seed 任务行失败: %v", err)
	}
	if _, err := e.svc.GetImportErrorFile(context.Background(), task.ID.Int64(), nil); err == nil {
		t.Fatal("Writer 未装配应 fail-closed 拒绝")
	}
}
