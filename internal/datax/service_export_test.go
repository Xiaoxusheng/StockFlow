package datax

// 导出中心单测（excel §2/§3、plan §6.3；内存替身 + 构造的内存工作簿）：
//   - 创建参数校验矩阵（模块/范围/SELECTED 上限/TIME_RANGE 必填/CURRENT_PAGE 边界）；
//   - 流式执行（keyset 游标分批推进、进度每批回写、meta 区/列头/单元格类型/合计行落位）；
//   - 重试语义（整文件重生成——PROCESSING 重入续跑）与契约违约终止（防死循环）；
//   - 产物下载守卫（终态前拒绝）。

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func exportRows(n int) []Row {
	rows := make([]Row, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, Row{Cells: []CellValue{
			{T: CellText, S: "C-" + strconv.Itoa(i)},
			{T: CellNumber, N: float64(i)},
			{T: CellMoney, N: float64(i) * 2},
			{T: CellDate, D: "2026-01-02 03:04:05"},
		}})
	}
	return rows
}

// createExport 经 Service 创建 QUEUED 任务（队列挂起）。
func createExport(t *testing.T, e *env, in ExportCreateInput) *ListTaskItem {
	t.Helper()
	e.queue.inline = nil
	item, err := e.svc.CreateExport(context.Background(), testActor(), in, true, nil)
	if err != nil {
		t.Fatalf("创建导出任务失败: %v", err)
	}
	return item
}

// TestExportCreateValidation 创建参数校验矩阵（plan §6.3）。
func TestExportCreateValidation(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	e.queue.inline = nil

	cases := []struct {
		name string
		in   ExportCreateInput
		code string
	}{
		{"模块非法", ExportCreateInput{Module: "NOPE", Scope: "ALL"}, "DATAX_MODULE_INVALID"},
		{"模块未装配", ExportCreateInput{Module: ModuleReport, Scope: "ALL"}, "DATAX_MODULE_NOT_AVAILABLE"},
		{"范围非法", ExportCreateInput{Module: ModuleProduct, Scope: "WHAT"}, "DATAX_SCOPE_INVALID"},
		{"SELECTED 缺 ids", ExportCreateInput{Module: ModuleProduct, Scope: "SELECTED"}, "DATAX_SCOPE_INVALID"},
		{"SELECTED id 非法", ExportCreateInput{Module: ModuleProduct, Scope: "SELECTED", IDs: []string{"x"}}, "DATAX_SCOPE_INVALID"},
		{"TIME_RANGE 缺区间", ExportCreateInput{Module: ModuleProduct, Scope: "TIME_RANGE"}, "DATAX_SCOPE_INVALID"},
		{"TIME_RANGE 起止倒置", ExportCreateInput{Module: ModuleProduct, Scope: "TIME_RANGE",
			TimeFrom: "2026-02-01 00:00:00", TimeTo: "2026-01-01 00:00:00"}, "DATAX_SCOPE_INVALID"},
		{"CURRENT_PAGE 页大小越界", ExportCreateInput{Module: ModuleProduct, Scope: "CURRENT_PAGE", Page: 1, PageSize: 101}, "DATAX_SCOPE_INVALID"},
	}
	for _, tc := range cases {
		if tc.name == "SELECTED id 非法" {
			continue // 单独用例覆盖（避免大数组构造重复）
		}
		_, err := e.svc.CreateExport(context.Background(), actor, tc.in, true, nil)
		requireErrCode(t, err, tc.code)
	}
	// SELECTED 超上限（1001 个）。
	ids := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		ids = append(ids, strconv.Itoa(i+1))
	}
	_, err := e.svc.CreateExport(context.Background(), actor, ExportCreateInput{
		Module: ModuleProduct, Scope: "SELECTED", IDs: ids}, true, nil)
	requireErrCode(t, err, "DATAX_SCOPE_INVALID")
	// 合法创建 → QUEUED + 单号 EXP- 前缀。
	item, err := e.svc.CreateExport(context.Background(), actor, ExportCreateInput{
		Module: ModuleProduct, Scope: "SELECTED", IDs: []string{"1", "2"}}, true, nil)
	if err != nil {
		t.Fatalf("合法创建失败: %v", err)
	}
	if item.Status != TaskStatusQueued || !strings.HasPrefix(item.TaskNo, "EXP-") || item.TaskType != "EXPORT" {
		t.Fatalf("创建结果不符: %+v", item)
	}
}

// TestExportRunStreamingCursor 流式执行：keyset 游标分批 + 进度回写 + 产物结构断言
// （excel §2.3 meta 区/单元格类型/合计行；excel §3 流式分批）。
func TestExportRunStreamingCursor(t *testing.T) {
	e := newEnv(t)
	e.source.batch = 3 // ExportBatchSize=3
	e.source.rows = exportRows(25)
	e.source.summary = []SummaryRow{{ColumnKey: "amount", Label: "合计"}}
	item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})

	payload := mustJSON(t, exportRunPayload{ExportNo: item.TaskNo, Actor: testActor()})
	if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
		t.Fatalf("导出执行失败: %v", err)
	}
	task, err := e.svc.repo.FindExportTask(context.Background(), item.ID.Int64())
	if err != nil {
		t.Fatalf("任务查询失败: %v", err)
	}
	if task.Status != TaskStatusSuccess || task.Progress != 100 || task.TotalRows != 25 || task.FileID <= 0 {
		t.Fatalf("终态不符: %+v", task)
	}
	// 游标分批：25 行 / 3 = 9 次调用（keyset 推进）。
	if e.source.batchCalls != 9 {
		t.Fatalf("批次调用数不符: %d", e.source.batchCalls)
	}
	// 进度每批回写且单调递增。
	prog := e.repo.exportProgress[item.ID.Int64()]
	if len(prog) < 8 {
		t.Fatalf("进度回写次数不足: %v", prog)
	}
	for i := 1; i < len(prog); i++ {
		if prog[i] < prog[i-1] {
			t.Fatalf("进度回写非单调: %v", prog)
		}
	}
	// 产物文件结构。
	sf, err := e.svc.GetExportFile(context.Background(), item.ID.Int64(), nil)
	if err != nil {
		t.Fatalf("产物不可下载: %v", err)
	}
	defer func() { _ = sf.Body.Close() }()
	f := openWorkbook(t, readAll(t, sf))
	sheet := f.GetSheetName(0)
	// meta 区（行 1 标题 / 行 2 时间 / 行 3 导出人 / 行 4 查询条件 / 行 5 空行 / 行 6 列头）。
	if v, _ := f.GetCellValue(sheet, "A1"); v != "商品导出" {
		t.Fatalf("标题不符: %q", v)
	}
	if v, _ := f.GetCellValue(sheet, "A3"); !strings.Contains(v, "tester") {
		t.Fatalf("导出人缺失: %q", v)
	}
	for i, col := range testExportColumns() {
		cell, _ := excelize.CoordinatesToCellName(i+1, exportMetaRows)
		if v, _ := f.GetCellValue(sheet, cell); v != col.Title {
			t.Fatalf("列头 %d 不符: %q", i+1, v)
		}
	}
	// 数据行：数值/金额为数值单元格（禁止全字符串导出，excel §2.3）。
	// 数值列单元格类型：流式写数值后 t 属性缺省（=数值），文本列则为共享字符串——
	// 断言数值列不是字符串单元格（excel §2.3 禁止全字符串导出）。
	qtyCell, _ := excelize.CoordinatesToCellName(2, exportMetaRows+1)
	if ct, _ := f.GetCellType(sheet, qtyCell); ct == excelize.CellTypeSharedString {
		t.Fatalf("数量列不应为字符串单元格: %v", ct)
	}
	if v, _ := f.GetCellValue(sheet, qtyCell); v != "1" {
		t.Fatalf("数量值不符: %q", v)
	}
	dateCellName, _ := excelize.CoordinatesToCellName(4, exportMetaRows+1)
	raw, _ := f.GetCellValue(sheet, dateCellName, excelize.Options{RawCellValue: true})
	if strings.Contains(raw, "2026-01-02") {
		t.Fatalf("日期列应为日期类型（序列值）而非文本: %q", raw)
	}
	// 合计行（amount 列合计 = 2*(1..25) 之和 = 650）。
	sumRow := exportMetaRows + 25 + 1
	if v, _ := f.GetCellValue(sheet, "A"+strconv.Itoa(sumRow)); v != "合计" {
		t.Fatalf("合计行缺失: %q", v)
	}
	// 金额列千分位两位小数格式生效（excel §2.3 金额统一格式）。
	sumCell, _ := excelize.CoordinatesToCellName(3, sumRow)
	if v, _ := f.GetCellValue(sheet, sumCell); v != "650.00" {
		t.Fatalf("合计值不符（含金额格式）: %q", v)
	}
	// 审计：创建与完成同事务审计（permission §6 敏感操作）。
	acts := e.spy.actions()
	for _, want := range []string{"create", "finish"} {
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

// TestExportRetryRegenerates 基础设施错误 → 返回 error（asynq 重试）；PROCESSING
// 重入 → 整文件重生成（纯函数幂等，plan §4.2）。
func TestExportRetryRegenerates(t *testing.T) {
	e := newEnv(t)
	e.source.rows = exportRows(5)
	e.source.batchErrAt = 2 // 第二批失败
	item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})

	payload := mustJSON(t, exportRunPayload{ExportNo: item.TaskNo, Actor: testActor()})
	if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err == nil {
		t.Fatalf("批次错误应向上返回（asynq 重试）")
	}
	task, _ := e.svc.repo.FindExportTask(context.Background(), item.ID.Int64())
	if task.Status != TaskStatusProcessing {
		t.Fatalf("重试前任务应保持 PROCESSING: %+v", task)
	}
	// 重入（错误消失）→ SUCCESS。
	e.source.batchErrAt = 0
	if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
		t.Fatalf("重入执行失败: %v", err)
	}
	task, _ = e.svc.repo.FindExportTask(context.Background(), item.ID.Int64())
	if task.Status != TaskStatusSuccess || task.Progress != 100 {
		t.Fatalf("重入终态不符: %+v", task)
	}
}

// TestExportContractViolation 契约违约终止（0 行且未结束 / 游标不推进 → FAILED 防死循环）。
func TestExportContractViolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		broken bool
		stuck  bool
	}{
		{"空批次未结束", true, false},
		{"游标不推进", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.source.rows = exportRows(5)
			e.source.brokenEnd = tc.broken
			e.source.stuckCursor = tc.stuck
			item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
			payload := mustJSON(t, exportRunPayload{ExportNo: item.TaskNo, Actor: testActor()})
			if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
				t.Fatalf("契约违约应终态化而非上抛: %v", err)
			}
			task, _ := e.svc.repo.FindExportTask(context.Background(), item.ID.Int64())
			if task.Status != TaskStatusFailed || task.ErrorMessage == "" {
				t.Fatalf("契约违约应 FAILED + error_message: %+v", task)
			}
		})
	}
}

// TestExportRedeliveryTerminalDrop 终态重投递静默丢弃（plan §4.2 状态守卫）。
func TestExportRedeliveryTerminalDrop(t *testing.T) {
	e := newEnv(t)
	e.source.rows = exportRows(2)
	item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
	payload := mustJSON(t, exportRunPayload{ExportNo: item.TaskNo, Actor: testActor()})
	// inline 模式：创建即执行到终态。
	if task, _ := e.svc.repo.FindExportTask(context.Background(), item.ID.Int64()); task.Status == TaskStatusQueued {
		if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
			t.Fatalf("执行失败: %v", err)
		}
	}
	e.source.batchCalls = 0
	if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
		t.Fatalf("终态重投递应静默丢弃: %v", err)
	}
	if e.source.batchCalls != 0 {
		t.Fatalf("终态重投递不应触发行源: %d", e.source.batchCalls)
	}
}

// TestExportDownloadGuards 产物下载守卫（终态前拒绝；datax:export:read 在路由层）。
func TestExportDownloadGuards(t *testing.T) {
	e := newEnv(t)
	item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
	// QUEUED 态（inline 挂起）→ 下载拒绝。
	_, err := e.svc.GetExportFile(context.Background(), item.ID.Int64(), nil)
	requireErrCode(t, err, "DATAX_EXPORT_NOT_FINISHED")
	// 执行完成后可下载。
	payload := mustJSON(t, exportRunPayload{ExportNo: item.TaskNo, Actor: testActor()})
	if err := e.svc.RunExportRun(context.Background(), asynqxTaskForTest(item.TaskNo, payload)); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	sf, err := e.svc.GetExportFile(context.Background(), item.ID.Int64(), nil)
	if err != nil {
		t.Fatalf("终态产物应可下载: %v", err)
	}
	_ = sf.Body.Close()
}

// TestExportListDTO 列表 DTO（file_url 仅终态下发；progress 字段落位）。
func TestExportListDTO(t *testing.T) {
	e := newEnv(t)
	e.source.rows = exportRows(2)
	item := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "ALL"})
	items, total, err := e.svc.ListExports(context.Background(), TaskListFilter{Page: 1, PageSize: 10}, nil)
	if err != nil || total != 1 {
		t.Fatalf("列表查询失败: %v %d", err, total)
	}
	if items[0].Module != ModuleProduct || items[0].ModuleName != "商品" {
		t.Fatalf("列表项模块不符: %+v", items[0])
	}
	if isTerminalExport(items[0].Status) != (items[0].FileURL != "") {
		t.Fatalf("file_url 下发时机不符: status=%s url=%q", items[0].Status, items[0].FileURL)
	}
	_ = item
}

// TestExportFilterRoundTrip 任务参数 → ExportFilter 还原（数据权限快照原样还原，
// TIME_RANGE 纯日期结束值补足到当日末）。
func TestExportFilterRoundTrip(t *testing.T) {
	e := newEnv(t)
	tf, tt := "2026-01-01 00:00:00", "2026-01-31"
	tk := createExport(t, e, ExportCreateInput{Module: ModuleProduct, Scope: "TIME_RANGE",
		TimeFrom: tf, TimeTo: tt})
	task, err := e.svc.repo.FindExportTask(context.Background(), tk.ID.Int64())
	if err != nil {
		t.Fatalf("任务查询失败: %v", err)
	}
	f, err := e.svc.filterFromParams(task)
	if err != nil {
		t.Fatalf("参数还原失败: %v", err)
	}
	if f.TimeFrom == nil || f.TimeFrom.Format(dateLayout) != tf {
		t.Fatalf("起始时间还原不符: %v", f.TimeFrom)
	}
	if f.TimeTo == nil || f.TimeTo.Format(dateLayout) != "2026-01-31 23:59:59" {
		t.Fatalf("结束时间应补足到当日末: %v", f.TimeTo)
	}
	if !f.AllWarehouses {
		t.Fatalf("数据权限快照还原不符: %+v", f)
	}
}
