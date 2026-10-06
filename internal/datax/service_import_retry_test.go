package datax

// retry-failed 单测（效率层一期计划 §2.8 / §7.1 T12；内存替身，零外部依赖）：
//   - 仅源任务 INVALID/FAILED 行进入新任务（成功行不入集，不重复成功数据）；
//   - 新任务走正常校验+确认流程：fake writer 提交计数 = 失败行数；
//   - 源任务行状态不被篡改（可重复发起，幂等依据）。

import (
	"context"
	"strings"
	"testing"

	"github.com/stockflow/server/internal/asynqx"
)

// seedRetrySource 构造源任务：3 行（INVALID/FAILED/SUCCESS 各一，raw 为合法模板行）。
func seedRetrySource(t *testing.T, e *env) *ImportTask {
	t.Helper()
	actor := testActor()
	src := &ImportTask{
		ImportType: ImportProduct, Status: TaskStatusPartial,
		TotalRows: 3, SuccessRows: 1, FailedRows: 1, ErrorRows: 1,
		CreatedBy: actor.UserID,
	}
	if err := e.repo.InsertImportTask(context.Background(), e.repo.DB(), src); err != nil {
		t.Fatalf("seed 源任务失败: %v", err)
	}
	raw := func(code string) JSONMap {
		return JSONMap{"code": code, "name": "商品" + code, "weight": "1", "price": "1", "made_at": ""}
	}
	rows := []*ImportTaskRow{
		{TaskID: src.ID.Int64(), RowNo: 1, Raw: raw("P-1"), Status: RowStatusInvalid, CreatedBy: actor.UserID},
		{TaskID: src.ID.Int64(), RowNo: 2, Raw: raw("P-2"), Status: RowStatusFailed, CreatedBy: actor.UserID},
		{TaskID: src.ID.Int64(), RowNo: 3, Raw: raw("P-3"), Status: RowStatusSuccess, CreatedBy: actor.UserID},
	}
	if err := e.repo.InsertImportRows(e.repo.DB(), rows); err != nil {
		t.Fatalf("seed 源任务行失败: %v", err)
	}
	return src
}

// TestRetryFailed_OnlyFailedRowsEnterNewTask T12 主断言：仅 INVALID/FAILED 行进入
// 新任务；fake writer 提交计数 = 失败行数；源任务行状态不被篡改。
func TestRetryFailed_OnlyFailedRowsEnterNewTask(t *testing.T) {
	e := newEnv(t)
	e.queue.inline = func(ctx context.Context, task asynqx.Task) error {
		return e.svc.RunImportCommit(ctx, task)
	}
	actor := testActor()
	ctx := context.Background()
	src := seedRetrySource(t, e)

	res, err := e.svc.RetryFailed(ctx, actor, src.ID.Int64())
	if err != nil {
		t.Fatalf("retry-failed 失败: %v", err)
	}
	// 响应形态对齐既有导入任务创建端点（POST /api/imports Upload）。
	if res.ID.Int64() == 0 || res.ID.Int64() == src.ID.Int64() {
		t.Fatalf("应返回新任务 id: %+v", res)
	}
	if !strings.HasPrefix(res.ImportNo, "IMP-") {
		t.Fatalf("新任务应经既有单号引擎发放 IMP- 单号: %s", res.ImportNo)
	}
	if res.ImportType != ImportProduct || res.Status != TaskStatusParsed || res.TotalRows != 2 {
		t.Fatalf("新任务形态不符: %+v", res)
	}

	// 新任务行 = 仅 INVALID/FAILED 行（行号沿用源任务，raw 原样复制）。
	newRows, err := e.repo.ListImportRows(ctx, res.ID.Int64(), nil, 0)
	if err != nil {
		t.Fatalf("读新任务行失败: %v", err)
	}
	if len(newRows) != 2 {
		t.Fatalf("新任务应恰 2 行（成功行不入集）: %d", len(newRows))
	}
	if newRows[0].RowNo != 1 || newRows[1].RowNo != 2 {
		t.Fatalf("行号应沿用源任务: %d/%d", newRows[0].RowNo, newRows[1].RowNo)
	}
	for _, r := range newRows {
		if r.Status != RowStatusRaw {
			t.Fatalf("新任务行应为 RAW（走正常校验+确认流程）: %+v", r)
		}
	}

	// 源任务行状态不被篡改（retry-failed 对源任务只读）。
	srcRows, err := e.repo.ListImportRows(ctx, src.ID.Int64(), nil, 0)
	if err != nil {
		t.Fatalf("读源任务行失败: %v", err)
	}
	want := map[int]string{1: RowStatusInvalid, 2: RowStatusFailed, 3: RowStatusSuccess}
	for _, r := range srcRows {
		if want[r.RowNo] != r.Status {
			t.Fatalf("源任务行 %d 状态被篡改: %s", r.RowNo, r.Status)
		}
	}

	// 走正常校验+确认流程：fake writer 提交计数 = 失败行数（2），且全部来自新任务。
	if _, err := e.svc.Validate(ctx, actor, res.ID.Int64()); err != nil {
		t.Fatalf("新任务校验失败: %v", err)
	}
	if _, err := e.svc.Confirm(ctx, actor, res.ID.Int64(), ConfirmInput{}); err != nil {
		t.Fatalf("新任务确认失败: %v", err)
	}
	committed := 0
	for _, rows := range e.writer.commitRows {
		committed += len(rows)
	}
	if committed != 2 {
		t.Fatalf("fake writer 提交计数应 = 失败行数 2: %d", committed)
	}
	// 新任务终态 SUCCESS（2 行全部重导成功）。
	final, err := e.repo.FindImportTask(ctx, res.ID.Int64())
	if err != nil {
		t.Fatalf("回读新任务失败: %v", err)
	}
	if final.Status != TaskStatusSuccess || final.SuccessRows != 2 {
		t.Fatalf("新任务应 SUCCESS(2): %+v", final)
	}
}

// TestRetryFailed_NoFailedRows 与不存在任务：无可重导行 → 400；任务不存在 → 404。
func TestRetryFailed_NoFailedRows(t *testing.T) {
	e := newEnv(t)
	actor := testActor()
	ctx := context.Background()

	// 全 SUCCESS 任务（0 失败行）。
	src := &ImportTask{ImportType: ImportProduct, Status: TaskStatusSuccess, TotalRows: 1, CreatedBy: actor.UserID}
	if err := e.repo.InsertImportTask(ctx, e.repo.DB(), src); err != nil {
		t.Fatalf("seed 失败: %v", err)
	}
	if err := e.repo.InsertImportRows(e.repo.DB(), []*ImportTaskRow{
		{TaskID: src.ID.Int64(), RowNo: 1, Raw: JSONMap{"code": "P-1"}, Status: RowStatusSuccess, CreatedBy: actor.UserID},
	}); err != nil {
		t.Fatalf("seed 行失败: %v", err)
	}
	_, err := e.svc.RetryFailed(ctx, actor, src.ID.Int64())
	requireErrCode(t, err, "COMMON_INVALID_PARAM")

	// 不存在任务 → DATAX_TASK_NOT_FOUND。
	_, err = e.svc.RetryFailed(ctx, actor, 999)
	requireErrCode(t, err, "DATAX_TASK_NOT_FOUND")

	// 全程不得产生新任务。
	if _, total, _ := e.repo.ListImportTasks(ctx, TaskListFilter{Page: 1, PageSize: 10}); total != 1 {
		t.Fatalf("不得产生新任务: %d", total)
	}
}
