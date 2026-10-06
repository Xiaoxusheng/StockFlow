package datax

import (
	"bytes"
	"context"
	"time"

	"github.com/stockflow/server/internal/response"
)

// 执行期失败行错误 Excel 合成下载（效率层一期 ask：失败行 xlsx 下载 = 原始数据 +
// 错误原因）。
//
// 缺口背景：错误 Excel 的登记发生在 Validate 阶段（service_import.go Validate，
// 仅当校验错误行 >0 时生成并回填 error_file_id）。执行阶段产生的 FAILED 行
// （任务终态 PARTIAL_SUCCESS/FAILED、Validate 阶段零错误行故无 error_file_id）
// 此前下载恒 404（service_export.go 旧口径）——逐行错误已在 ImportConfirmResult
// Errors 与任务行 errors jsonb 中，但无修复载体，用户无法按行修复后重导。
//
// 本合成路径按 import_task_rows（status IN (INVALID, FAILED)）现场生成与既有
// 错误 Excel 同构的工作簿（buildErrorWorkbook：原数据 + 错误原因列 + 填表说明页）：
//   - 只读幂等：不落 files、不改任务行，每次按行面事实重建（行状态终态后不变）；
//   - 数据权限：与 files 登记路径同规则（fileVisible ③——导入任务产物仅创建人
//     及全量范围用户可见；越界按 404 处理防 ID 枚举探测）；
//   - 修复后重导衔接：POST /api/imports/:id/retry-failed（service_import.go）。
func (s *Service) synthesizeImportErrorFile(ctx context.Context, task *ImportTask, scope *FileScope) (*serveFile, error) {
	// 数据权限（导入任务产物仅创建人可见——service_file.go fileVisible ③ 同规则）。
	if scope != nil && !scope.All && int64(task.CreatedBy) != scope.UserID {
		return nil, response.NewError(ErrFileNotFound, nil)
	}
	w, err := s.writerFor(task.ImportType)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListImportRows(ctx, task.ID.Int64(), []string{RowStatusInvalid, RowStatusFailed}, 0)
	if err != nil {
		return nil, err
	}
	errRows := make([]errorRowData, 0, len(rows))
	for _, r := range rows {
		msgs := make([]string, 0, len(r.Errors))
		for _, e := range r.Errors {
			msgs = append(msgs, e.Message)
		}
		if len(msgs) == 0 {
			continue // 无错误说明的失败行不进错误簿（正常流程不发生，防御）
		}
		errRows = append(errRows, errorRowData{RowNo: r.RowNo, Raw: jsonToRaw(r.Raw), Messages: msgs})
	}
	if len(errRows) == 0 {
		return nil, response.NewError(ErrFileNotFound, map[string]any{"reason": "该任务无错误明细文件"})
	}
	content, err := buildErrorWorkbook(w.Template(), errRows)
	if err != nil {
		return nil, err
	}
	modTime := task.UpdatedAt.Time
	if modTime.IsZero() {
		modTime = time.Now()
	}
	return &serveFile{
		ID:       0, // 合成产物无 files 登记行——下载审计 object_id=0 如实反映
		FileName: "导入错误明细-" + task.ImportNo + ".xlsx",
		MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		ModTime:  modTime,
		Body:     byteBody{Reader: bytes.NewReader(content)},
	}, nil
}

// byteBody 内存工作簿响应体（bytes.Reader 具备 Read/Seek；Close 空实现满足
// readSeekCloser——http.ServeContent 流式响应，handler 负责关闭）。
type byteBody struct{ *bytes.Reader }

func (byteBody) Close() error { return nil }
