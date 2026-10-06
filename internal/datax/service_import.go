package datax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/storage"
)

// 导入向导（excel §1.2 完整流程；每步一个端点，状态守卫防重放——plan §6.2）：
//
//	GET  /api/imports/templates            模板清单（按列定义现场生成 xlsx，无静态文件）
//	POST /api/imports                      上传+解析 → PARSED（数据行落 import_task_rows.raw）
//	POST /api/imports/{id}/validate        结构层+业务层校验 → VALIDATED（error_rows>0 生成错误 Excel）
//	GET  /api/imports/{id}/preview         前 100 行结构化预览
//	POST /api/imports/{id}/confirm         状态守卫 VALIDATED→EXECUTING → 入队执行
//	GET  /api/imports                      任务列表（数据中心，excel §4）
//
// 导入执行幂等（excel §6.3）：confirm 状态守卫保证任务只执行一次；执行器以行状态
// 断点续跑（service_import_run.go：仅重试 QUEUED/FAILED 行，SUCCESS 行跳过）。

// TemplateItem 模板清单项（前端 data.ts ImportTemplate 对齐：downloadUrl 后端下发）。
type TemplateItem struct {
	ImportType  string `json:"import_type"`
	Name        string `json:"name"`
	FileName    string `json:"file_name"`
	Description string `json:"description"`
	DownloadURL string `json:"download_url"`
	HighRisk    bool   `json:"high_risk"` // 初始化库存导入（excel §6.1 高危二次确认）
}

// ListTemplates 模板清单（仅列出 Writer 已装配的导入类型——装配缺位不出假条目）。
func (s *Service) ListTemplates() []TemplateItem {
	items := make([]TemplateItem, 0, len(importTypes))
	for _, t := range importTypes {
		w, ok := s.writers[t]
		if !ok || w == nil {
			continue
		}
		spec := w.Template()
		items = append(items, TemplateItem{
			ImportType:  t,
			Name:        spec.Name,
			FileName:    templateFileName(spec),
			Description: spec.Description,
			DownloadURL: "/api/imports/templates/" + t,
			HighRisk:    IsHighRiskImport(t),
		})
	}
	return items
}

// TemplateFile 现场生成模板工作簿（excelize 内存工作簿 → xlsx 字节）。
func (s *Service) TemplateFile(importType string) (string, []byte, error) {
	w, err := s.writerFor(importType)
	if err != nil {
		return "", nil, err
	}
	spec := w.Template()
	f, err := buildTemplate(spec)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	buf, err := f.WriteToBuffer()
	if err != nil {
		return "", nil, fmt.Errorf("datax: 生成模板失败: %w", err)
	}
	return templateFileName(spec), buf.Bytes(), nil
}

func templateFileName(spec TemplateSpec) string {
	if spec.FileName != "" {
		return spec.FileName
	}
	return spec.Name + "模板.xlsx"
}

// ImportUploadResult 上传解析结果（前端 data.ts ImportUploadResult 同构）。
type ImportUploadResult struct {
	ID         database.ID `json:"id"`
	ImportNo   string      `json:"import_no"`
	ImportType string      `json:"import_type"`
	FileName   string      `json:"file_name"`
	TotalRows  int         `json:"total_rows"`
	Status     string      `json:"status"`
}

// Upload 上传并解析（multipart：import_type + file；plan §6.2）。
// .xlsx 白名单与 MIME 交叉校验（storage.Save，api.md §5）→ excelize 流式逐行读取 →
// 表头与模板逐列比对 → 数据行落 import_task_rows（raw）→ 任务 PARSED；行数超限整体拒绝。
// 源文件登记 files（module=datax、business_no=IMP- 单号、保留期推导），与任务/行同事务。
func (s *Service) Upload(ctx context.Context, actor Actor, importType string, fh *multipart.FileHeader) (*ImportUploadResult, error) {
	w, err := s.writerFor(importType)
	if err != nil {
		return nil, err
	}
	if fh == nil || fh.Size == 0 {
		return nil, response.NewError(ErrImportFileRequired, nil)
	}
	// excelize v2 仅支持 xlsx（storage 白名单同口径）；非 xlsx 的白名单类型对导入无意义，
	// 提前给出精确错误码。
	if !strings.HasSuffix(strings.ToLower(fh.Filename), ".xlsx") {
		return nil, response.NewError(ErrImportFileType, map[string]any{"filename": fh.Filename})
	}
	src, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("datax: 读取上传文件失败: %w", err)
	}
	defer func() { _ = src.Close() }()

	// 物理保存（扩展名/MIME 交叉/大小上限/服务端重命名全量在 storage.Save）；
	// multipart.File 为 ReadSeeker，保存后回卷供解析器复用。
	stored, err := s.store.Save(src, fh.Filename, s.cfg.UploadMaxBytes)
	if err != nil {
		return nil, err
	}
	if _, err := src.Seek(0, 0); err != nil {
		_ = s.store.Remove(stored.StoragePath)
		return nil, fmt.Errorf("datax: 回卷上传流失败: %w", err)
	}

	rawRows, parseErr := parseWorkbookStream(src, w.Template(), s.cfg.ImportMaxRows)
	if parseErr != nil {
		_ = s.store.Remove(stored.StoragePath) // 未登记的物理文件就地清理，防孤儿
		return nil, parseErr
	}

	// 登记文件 + 建任务（docnum IMP 发号同事务）+ 落行 + 审计：单事务（architecture §4）。
	task := &ImportTask{
		ImportType: importType,
		Status:     TaskStatusParsed,
		TotalRows:  len(rawRows),
		CreatedBy:  actor.UserID,
		UpdatedBy:  actor.UserID,
	}
	rows := make([]*ImportTaskRow, 0, len(rawRows))
	for _, rr := range rawRows {
		rows = append(rows, &ImportTaskRow{
			TaskID:    0, // 建任务后回填
			RowNo:     rr.RowNo,
			Raw:       rawToJSON(rr.Raw),
			Status:    RowStatusRaw,
			CreatedBy: actor.UserID,
		})
	}
	err = s.inTx(ctx, func(tx *gorm.DB) error {
		file := &storage.File{
			FileName:     fh.Filename,
			StoredName:   stored.StoredName,
			StoragePath:  stored.StoragePath,
			MimeType:     stored.MimeType,
			FileType:     stored.FileType,
			SizeBytes:    stored.Size,
			Module:       "datax",
			UploaderID:   actor.UserID,
			UploaderName: actor.Username,
			ExpiresAt:    s.expiry(),
			CreatedBy:    actor.UserID,
		}
		if err := s.repo.InsertFile(tx, file); err != nil {
			return err
		}
		task.SourceFileID = file.ID
		if err := s.repo.InsertImportTask(ctx, tx, task); err != nil {
			return err
		}
		for i := range rows {
			rows[i].TaskID = task.ID.Int64()
		}
		if err := s.repo.InsertImportRows(tx, rows); err != nil {
			return err
		}
		e := actor.auditEntry("import_task", task.ID.Int64(), "upload")
		e.Success = true
		e.Request = map[string]any{"import_type": importType, "file_name": fh.Filename, "total_rows": len(rawRows)}
		e.After = map[string]any{"import_no": task.ImportNo, "status": task.Status, "total_rows": task.TotalRows}
		return s.audit(tx, e)
	})
	if err != nil {
		// 事务失败：物理文件无登记行，就地清理。
		_ = s.store.Remove(stored.StoragePath)
		return nil, err
	}
	return &ImportUploadResult{
		ID:         task.ID,
		ImportNo:   task.ImportNo,
		ImportType: importType,
		FileName:   fh.Filename,
		TotalRows:  task.TotalRows,
		Status:     task.Status,
	}, nil
}

// RetryFailed 重新导入失败行（效率层一期计划 §2.8）：读源任务 status IN
// ('INVALID','FAILED') 的行 → 以同一 ImportWriter 管线创建新导入任务（PARSED 态，
// 走正常校验+确认流程；复用 ImportWizard 前端既有六步向导语义）。
//
// 幂等依据：仅重导上次失败行（上次成功行不入集，不重复成功数据）；PO/SO 写入器经
// 既有单号引擎与状态守卫；新任务单号经 docnum IMP 引擎发放（建任务同事务）。
// 事务边界（计划 8.9 前置核验结论）：各域 Writer.Commit 逐行/逐单经既有域内 Service
// 通路落库（每行/每单独立事务，行级失败整体回滚该行/该单，行状态回写另在批次事务）——
// FAILED 行无部分落库，可安全进入重导集。
// 源任务只读：行状态不被篡改，可重复发起（每次产生独立新任务）。
func (s *Service) RetryFailed(ctx context.Context, actor Actor, id int64) (*ImportUploadResult, error) {
	src, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	// 写入器装配核验 fail-closed（与 Confirm 同口径：缺位不产任务）。
	if _, err := s.writerFor(src.ImportType); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListImportRows(ctx, id, []string{RowStatusInvalid, RowStatusFailed}, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{
			"reason": "任务没有可重导的失败行（仅 INVALID/FAILED 行进入新任务）",
		})
	}
	if len(rows) > s.cfg.ImportMaxRows {
		return nil, response.NewError(ErrTooManyRows, map[string]any{"limit": s.cfg.ImportMaxRows})
	}

	task := &ImportTask{
		ImportType: src.ImportType,
		Status:     TaskStatusParsed,
		TotalRows:  len(rows),
		CreatedBy:  actor.UserID,
		UpdatedBy:  actor.UserID,
	}
	newRows := make([]*ImportTaskRow, 0, len(rows))
	for _, r := range rows {
		newRows = append(newRows, &ImportTaskRow{
			TaskID:    0, // 建任务后回填
			RowNo:     r.RowNo,
			Raw:       r.Raw,
			Status:    RowStatusRaw,
			CreatedBy: actor.UserID,
		})
	}
	err = s.inTx(ctx, func(tx *gorm.DB) error {
		if err := s.repo.InsertImportTask(ctx, tx, task); err != nil {
			return err
		}
		for i := range newRows {
			newRows[i].TaskID = task.ID.Int64()
		}
		if err := s.repo.InsertImportRows(tx, newRows); err != nil {
			return err
		}
		e := actor.auditEntry("import_task", task.ID.Int64(), "retry_failed")
		e.Success = true
		e.Request = map[string]any{"source_task_id": id}
		e.After = map[string]any{
			"import_no": task.ImportNo, "source_import_no": src.ImportNo, "total_rows": task.TotalRows,
		}
		return s.audit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	// 响应对齐既有导入任务创建端点（POST /api/imports Upload）形态；无源文件落
	// files 表——file_name 空串即「行数据自源任务复制」。
	return &ImportUploadResult{
		ID:         task.ID,
		ImportNo:   task.ImportNo,
		ImportType: task.ImportType,
		TotalRows:  task.TotalRows,
		Status:     task.Status,
	}, nil
}

// parseWorkbookStream 流式解析工作簿：表头与模板逐列比对（结构层第一道闸），数据行
// raw 提取 + 行数上限守卫。excelize f.Rows 迭代器逐行读，不整表驻留内存。
func parseWorkbookStream(r io.ReadSeeker, spec TemplateSpec, maxRows int) ([]ImportRow, error) {
	// 解压放大守卫（api.md §5 解压放大维度）：先于 OpenReader 预检 zip 部件解压预算。
	if err := guardImportZipBudget(r); err != nil {
		return nil, err
	}
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, response.NewError(ErrTemplateMismatch, map[string]any{"reason": "无法解析的 Excel 文件"})
	}
	defer func() { _ = f.Close() }()

	sheet := templateSheetName(spec)
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, response.NewError(ErrSheetEmpty, nil)
	}
	found := false
	for _, name := range sheets {
		if name == sheet {
			found = true
			break
		}
	}
	if !found {
		sheet = sheets[0] // 兼容重命名数据页：退化为首个工作页，列头比对仍强制
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, response.NewError(ErrTemplateMismatch, map[string]any{"reason": "工作页不可读"})
	}
	defer func() { _ = rows.Close() }()

	// 表头行逐列比对（列名/顺序一致，多列少列均拒绝——修复后重新导入语义不变）。
	if !rows.Next() {
		return nil, response.NewError(ErrSheetEmpty, nil)
	}
	header, err := rows.Columns()
	if err != nil {
		return nil, response.NewError(ErrTemplateMismatch, map[string]any{"reason": "表头行不可读"})
	}
	if len(header) != len(spec.Columns) {
		return nil, templateMismatchError(header, spec.Columns)
	}
	for i, col := range spec.Columns {
		if strings.TrimSpace(header[i]) != col.Title {
			return nil, templateMismatchError(header, spec.Columns)
		}
	}

	out := make([]ImportRow, 0, 128)
	rowNo := 0
	for rows.Next() {
		cells, err := rows.Columns()
		if err != nil {
			return nil, response.NewError(ErrTemplateMismatch, map[string]any{"reason": "数据行不可读", "row": rowNo + 1})
		}
		raw := make(map[string]string, len(spec.Columns))
		nonEmpty := false
		for i, col := range spec.Columns {
			v := ""
			if i < len(cells) {
				v = strings.TrimSpace(cells[i])
			}
			raw[col.Key] = v
			if v != "" {
				nonEmpty = true
			}
		}
		if !nonEmpty {
			continue // 全空行跳过不计
		}
		rowNo++
		if rowNo > maxRows {
			return nil, response.NewError(ErrTooManyRows, map[string]any{"limit": maxRows})
		}
		out = append(out, ImportRow{RowNo: rowNo, Raw: raw})
	}
	if rowNo == 0 {
		return nil, response.NewError(ErrSheetEmpty, nil)
	}
	return out, nil
}

// templateMismatchError 表头不一致错误（details 携带实际表头与期望列，api.md §4）。
func templateMismatchError(header []string, cols []Column) error {
	want := make([]string, 0, len(cols))
	for _, c := range cols {
		want = append(want, c.Title)
	}
	got := header
	if len(got) > 20 {
		got = got[:20]
	}
	return response.NewError(ErrTemplateMismatch, map[string]any{"got": got, "want": want})
}

// parseCell 单元格类型解析（结构层；excel §1.3 数据类型/日期格式/数字格式/金额合法性）。
// 返回类型化值；解析失败返回错误消息。
func parseCell(raw string, col Column) (CellValue, string) {
	switch col.Type {
	case CellNumber:
		n, ok := parseNumber(raw)
		if !ok {
			return CellValue{}, col.Title + "必须是数字"
		}
		return CellValue{T: CellNumber, N: n}, ""
	case CellMoney:
		n, ok := parseNumber(raw)
		if !ok {
			return CellValue{}, col.Title + "必须是金额数字"
		}
		if n < 0 {
			return CellValue{}, col.Title + "不能为负数"
		}
		return CellValue{T: CellMoney, N: n}, ""
	case CellDate:
		if _, ok := parseDateCell(raw); !ok {
			return CellValue{}, col.Title + "日期格式应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss"
		}
		t, _ := parseDateCell(raw)
		return CellValue{T: CellDate, D: t.Format(dateLayout)}, ""
	default:
		return CellValue{T: CellText, S: raw}, ""
	}
}

// ImportValidationError 校验错误（前端 data.ts ImportValidationError 同构：
// 行列定位，excel §1.4"第 N 行：原因"）。
type ImportValidationError = RowError

// ImportValidateResult 校验结果（前端 data.ts ImportValidateResult 同构）。
type ImportValidateResult struct {
	ID            database.ID             `json:"id"`
	Status        string                  `json:"status"`
	TotalRows     int                     `json:"total_rows"`
	ValidRows     int                     `json:"valid_rows"`
	ErrorRows     int                     `json:"error_rows"`
	Errors        []ImportValidationError `json:"errors"`
	ErrorFileURL  string                  `json:"error_file_url,omitempty"`
	ErrorFileName string                  `json:"error_file_name,omitempty"`
}

// Validate 校验管线（excel §1.3 全量规则；plan §6.2）：
// 结构层（本包）：必填 / 类型（数字/金额/日期）/ 文件内重复（UniqueInFile 列）；
// 业务层（Writer.Validate）：存在性 / 库内重复 / 业务关系。
// error_rows>0 时生成错误 Excel（原数据+错误原因列）登记 files 并回填 error_file_id。
// 状态口径：PARSED/VALIDATED 可执行（校验为行内容纯函数，重复校验幂等）；
// 执行态/终态拒绝（DATAX_STATUS_CONFLICT）。
func (s *Service) Validate(ctx context.Context, actor Actor, id int64) (*ImportValidateResult, error) {
	task, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != TaskStatusParsed && task.Status != TaskStatusValidated {
		return nil, response.NewError(ErrStatusConflict, map[string]any{
			"status": task.Status, "reason": "任务已进入执行或终态，不可重复校验",
		})
	}
	w, err := s.writerFor(task.ImportType)
	if err != nil {
		return nil, err
	}
	spec := w.Template()
	rows, err := s.repo.ListImportRows(ctx, id, nil, 0)
	if err != nil {
		return nil, err
	}

	// —— 结构层（datax：必填/类型/文件内重复）——
	structErrs := make(map[int][]RowError)      // 行号 → 错误
	typed := make(map[int]map[string]CellValue) // 行号 → 类型化单元格
	for _, col := range spec.Columns {
		seen := map[string]int{} // UniqueInFile：值 → 首现行号
		for _, r := range rows {
			raw := strings.TrimSpace(rawString(r.Raw, col.Key))
			if raw == "" {
				if col.Required {
					structErrs[r.RowNo] = append(structErrs[r.RowNo], RowError{
						Row: r.RowNo, Column: col.Key, Message: col.Title + "为必填项",
					})
				}
				continue
			}
			cv, perr := parseCell(raw, col)
			if perr != "" {
				structErrs[r.RowNo] = append(structErrs[r.RowNo], RowError{
					Row: r.RowNo, Column: col.Key, Message: perr,
				})
				continue
			}
			if typed[r.RowNo] == nil {
				typed[r.RowNo] = map[string]CellValue{}
			}
			typed[r.RowNo][col.Key] = cv
			if col.UniqueInFile {
				if first, dup := seen[raw]; dup {
					structErrs[r.RowNo] = append(structErrs[r.RowNo], RowError{
						Row: r.RowNo, Column: col.Key,
						Message: fmt.Sprintf("%s 与第 %d 行重复", col.Title, first),
					})
				} else {
					seen[raw] = r.RowNo
				}
			}
		}
	}

	// —— 业务层（仅结构通过行；Writer 只读校验）——
	validRows := make([]ImportRow, 0, len(rows))
	for _, r := range rows {
		if len(structErrs[r.RowNo]) > 0 {
			continue
		}
		cells := typed[r.RowNo]
		if cells == nil {
			cells = map[string]CellValue{}
		}
		validRows = append(validRows, ImportRow{RowNo: r.RowNo, Cells: cells, Raw: jsonToRaw(r.Raw)})
	}
	var bizErrs []RowError
	if len(validRows) > 0 {
		if bizErrs, err = w.Validate(ctx, validRows); err != nil {
			return nil, err
		}
	}
	bizByRow := map[int][]RowError{}
	for _, e := range bizErrs {
		bizByRow[e.Row] = append(bizByRow[e.Row], e)
	}

	// —— 结果组装（纯计算，事务外）——
	var allErrs []ImportValidationError
	var errRows []errorRowData
	validCount, errorCount := 0, 0
	rowSets := make([]struct {
		id  int64
		set map[string]any
	}, 0, len(rows))
	for _, r := range rows {
		errs := append(append([]RowError{}, structErrs[r.RowNo]...), bizByRow[r.RowNo]...)
		set := map[string]any{"updated_by": actor.UserID}
		if tm := typed[r.RowNo]; tm != nil {
			parsed := JSONMap{}
			for k, v := range tm {
				parsed[k] = v
			}
			set["parsed"] = parsed
		} else {
			set["parsed"] = nil
		}
		if len(errs) > 0 {
			set["status"] = RowStatusInvalid
			set["errors"] = errs
			errorCount++
			msgs := make([]string, 0, len(errs))
			for _, e := range errs {
				allErrs = append(allErrs, e)
				msgs = append(msgs, e.Message)
			}
			errRows = append(errRows, errorRowData{RowNo: r.RowNo, Raw: jsonToRaw(r.Raw), Messages: msgs})
		} else {
			set["status"] = RowStatusValid
			set["errors"] = nil
			validCount++
		}
		rowSets = append(rowSets, struct {
			id  int64
			set map[string]any
		}{r.ID.Int64(), set})
	}

	// 错误 Excel（excel §1.4）物理落盘在事务前；事务失败就地清理。
	var storedFile *storage.StoredFile
	var errFileName string
	if errorCount > 0 {
		content, berr := buildErrorWorkbook(spec, errRows)
		if berr != nil {
			return nil, berr
		}
		errFileName = "导入错误明细-" + task.ImportNo + ".xlsx"
		sf, serr := s.store.Save(bytes.NewReader(content), errFileName, ExportMaxFileBytes)
		if serr != nil {
			return nil, serr
		}
		storedFile = &sf
	}

	// —— 事务：行结果 + 任务守卫回填 + files 登记 + 审计 ——
	task.Status = TaskStatusValidated
	task.ValidRows = validCount
	task.ErrorRows = errorCount
	err = s.inTx(ctx, func(tx *gorm.DB) error {
		if storedFile != nil {
			file := &storage.File{
				FileName:     errFileName,
				StoredName:   storedFile.StoredName,
				StoragePath:  storedFile.StoragePath,
				MimeType:     storedFile.MimeType,
				FileType:     storedFile.FileType,
				SizeBytes:    storedFile.Size,
				Module:       "datax",
				BusinessNo:   task.ImportNo,
				UploaderID:   actor.UserID,
				UploaderName: actor.Username,
				ExpiresAt:    s.expiry(),
				CreatedBy:    actor.UserID,
			}
			if err := s.repo.InsertFile(tx, file); err != nil {
				return err
			}
			task.ErrorFileID = file.ID
		}
		for _, rs := range rowSets {
			if err := s.repo.UpdateImportRow(tx, rs.id, rs.set); err != nil {
				return err
			}
		}
		set := map[string]any{
			"status": TaskStatusValidated, "valid_rows": validCount, "error_rows": errorCount,
			"updated_by": actor.UserID, "updated_at": database.Now(),
		}
		if task.ErrorFileID > 0 {
			set["error_file_id"] = task.ErrorFileID
		}
		n, err := s.repo.GuardUpdateImportTask(tx, id, []string{TaskStatusParsed, TaskStatusValidated}, set)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStatusConflict, nil)
		}
		e := actor.auditEntry("import_task", id, "validate")
		e.Success = true
		e.After = map[string]any{"import_no": task.ImportNo, "valid_rows": validCount, "error_rows": errorCount}
		return s.audit(tx, e)
	})
	if err != nil {
		if storedFile != nil {
			_ = s.store.Remove(storedFile.StoragePath)
		}
		return nil, err
	}

	res := &ImportValidateResult{
		ID:        task.ID,
		Status:    task.Status,
		TotalRows: task.TotalRows,
		ValidRows: validCount,
		ErrorRows: errorCount,
		Errors:    allErrs,
	}
	if res.Errors == nil {
		res.Errors = []ImportValidationError{}
	}
	if task.ErrorFileID > 0 {
		// 错误明细链接按 Actor 仓库范围快照收口（FileScope 冻结规则；跨范围用户仅
		// 下载已 404，此处元数据同样不下发）。
		if f, ferr := s.loadFileRow(ctx, task.ErrorFileID); ferr == nil && s.fileVisible(ctx, f, actorFileScope(actor)) {
			res.ErrorFileURL = "/api/imports/" + strconv.FormatInt(id, 10) + "/error-file"
			res.ErrorFileName = errFileName
		}
	}
	return res, nil
}

// ImportPreviewColumn 预览列定义（前端 data.ts ImportPreviewColumn 同构）。
type ImportPreviewColumn struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// ImportPreviewResult 预览（前端 data.ts ImportPreviewResult 同构：列定义 + 行）。
type ImportPreviewResult struct {
	ID        database.ID           `json:"id"`
	TotalRows int                   `json:"total_rows"`
	Columns   []ImportPreviewColumn `json:"columns"`
	Rows      []map[string]any      `json:"rows"`
}

// Preview 前 100 行结构化预览（plan §6.2：parsed 优先，未校验行以 raw 文本回显）。
func (s *Service) Preview(ctx context.Context, id int64) (*ImportPreviewResult, error) {
	task, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	w, err := s.writerFor(task.ImportType)
	if err != nil {
		return nil, err
	}
	spec := w.Template()
	cols := make([]ImportPreviewColumn, 0, len(spec.Columns))
	for _, c := range spec.Columns {
		cols = append(cols, ImportPreviewColumn{Key: c.Key, Title: c.Title})
	}
	rows, err := s.repo.ListImportRows(ctx, id, nil, previewRows)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		row := make(map[string]any, len(spec.Columns))
		for _, c := range spec.Columns {
			if cv, ok := r.Parsed[c.Key]; ok && cv != nil {
				row[c.Key] = cellValueOut(c.Type, cv)
				continue
			}
			row[c.Key] = r.Raw[c.Key]
		}
		out = append(out, row)
	}
	return &ImportPreviewResult{ID: task.ID, TotalRows: task.TotalRows, Columns: cols, Rows: out}, nil
}

// cellValueOut jsonb 反序列化值 → 预览输出（按列类型给标量； NUMBER/MONEY → 数字，
// DATE → 统一格式文本，TEXT → 文本）。
func cellValueOut(t CellType, v any) any {
	cv := cellFromAny(v)
	switch t {
	case CellNumber, CellMoney:
		return cv.N
	case CellDate:
		return cv.D
	default:
		return cv.S
	}
}

// cellFromAny jsonb 载荷 → CellValue（tolerate 已类型化值）。
func cellFromAny(v any) CellValue {
	switch x := v.(type) {
	case CellValue:
		return x
	case map[string]any:
		cv := CellValue{S: x["s"].(string)}
		if t, ok := x["t"].(string); ok {
			cv.T = CellType(t)
		}
		if n, ok := x["n"].(float64); ok {
			cv.N = n
		}
		if d, ok := x["d"].(string); ok {
			cv.D = d
		}
		return cv
	case string:
		return CellValue{T: CellText, S: x}
	default:
		return CellValue{T: CellText}
	}
}

// rowCellsFromJSON 行数据还原（执行器用）：parsed jsonb → 类型化 Cells；缺列回退 raw。
func rowCellsFromJSON(spec TemplateSpec, parsed JSONMap, raw map[string]string) map[string]CellValue {
	cells := make(map[string]CellValue, len(spec.Columns))
	for _, col := range spec.Columns {
		if v, ok := parsed[col.Key]; ok && v != nil {
			cells[col.Key] = cellFromAny(v)
			continue
		}
		cv, perr := parseCell(raw[col.Key], col)
		if perr == "" {
			cells[col.Key] = cv
		} else {
			cells[col.Key] = CellValue{T: CellText, S: raw[col.Key]}
		}
	}
	return cells
}

// ConfirmInput 确认导入入参（高危类型必须 confirmed=true，excel §6.1 二次确认）。
type ConfirmInput struct {
	Confirmed bool `json:"confirmed"`
}

// ImportConfirmResult 确认/执行结果（前端 data.ts ImportConfirmResult 同构）。
type ImportConfirmResult struct {
	ID          database.ID             `json:"id"`
	Status      string                  `json:"status"`
	TotalRows   int                     `json:"total_rows"`
	SuccessRows int                     `json:"success_rows"`
	FailedRows  int                     `json:"failed_rows"`
	Errors      []ImportValidationError `json:"errors,omitempty"`
	// FinishedAt 统一 database.JSONTime（api.md §2；裸 *time.Time 序列化 RFC3339——
	// 2026-10-04 统一）。nil（未完成）→ 零值序列化 null。
	FinishedAt database.JSONTime `json:"finished_at,omitempty"`
}

// Confirm 确认导入（plan §6.2）：
// 前置校验（VALIDATED 态、无错误行、高危二次确认）→ 状态守卫 VALIDATED→EXECUTING
// （0 行 = 409，重放/并发确认恰一成功，plan §13.2）→ VALID 行按 datax.batch_size 分批
// 标 QUEUED + batch_no → 入队 datax:import:commit（inline 模式同步执行，plan §4.1）→
// 回读任务。入队失败补偿回 VALIDATED（guard UPDATE），杜绝任务卡 EXECUTING。
func (s *Service) Confirm(ctx context.Context, actor Actor, id int64, in ConfirmInput) (*ImportConfirmResult, error) {
	task, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.writerFor(task.ImportType); err != nil {
		return nil, err
	}
	if task.Status != TaskStatusValidated {
		return nil, response.NewError(ErrStatusConflict, map[string]any{
			"status": task.Status, "reason": "仅校验通过（VALIDATED）的任务可确认导入",
		})
	}
	if task.ErrorRows > 0 {
		return nil, response.NewError(ErrRowsInvalid, map[string]any{"error_rows": task.ErrorRows})
	}
	if IsHighRiskImport(task.ImportType) && !in.Confirmed {
		return nil, response.NewError(ErrConfirmRequired, map[string]any{
			"import_type": task.ImportType,
		})
	}

	validRows, err := s.repo.ListImportRows(ctx, id, []string{RowStatusValid}, 0)
	if err != nil {
		return nil, err
	}
	err = s.inTx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.GuardUpdateImportTask(tx, id, []string{TaskStatusValidated}, map[string]any{
			"status": TaskStatusExecuting, "started_at": time.Now(),
			"updated_by": actor.UserID, "updated_at": database.Now(),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStatusConflict, nil)
		}
		for i, r := range validRows {
			if err := s.repo.UpdateImportRow(tx, r.ID.Int64(), map[string]any{
				"status": RowStatusQueued, "batch_no": i / s.cfg.BatchSize, "updated_by": actor.UserID,
			}); err != nil {
				return err
			}
		}
		e := actor.auditEntry("import_task", id, "confirm")
		e.Success = true
		e.Request = map[string]any{"confirmed": in.Confirmed}
		e.Before = map[string]any{"status": TaskStatusValidated}
		e.After = map[string]any{"status": TaskStatusExecuting, "queued_rows": len(validRows)}
		return s.audit(tx, e)
	})
	if err != nil {
		return nil, err
	}

	// 入队（inline 模式在 Enqueue 内同步执行——必须发生在 confirm 事务提交后，执行器
	// 状态守卫才能看到 EXECUTING；TaskID=IMP- 单号：在途重复入队第二道防线，plan §4.2）。
	payload, _ := json.Marshal(importCommitPayload{ImportNo: task.ImportNo, Actor: actor})
	if err := s.queue.Enqueue(ctx, asynqx.Task{
		Type:    asynqx.TaskTypeImportCommit,
		Payload: payload,
		TaskID:  task.ImportNo,
	}); err != nil {
		// 补偿：EXECUTING → VALIDATED（用户可重试确认）；补偿失败即任务卡执行态，
		// error 日志披露（go-dev-standard 规则 10）。
		if n, uerr := s.repo.GuardUpdateImportTask(s.repo.DB(), id, []string{TaskStatusExecuting}, map[string]any{
			"status": TaskStatusValidated, "updated_by": actor.UserID,
		}); uerr != nil || n == 0 {
			s.log.Error("datax: 入队失败且状态补偿失败（任务卡 EXECUTING）",
				zap.String("import_no", task.ImportNo), zap.Error(uerr))
		}
		return nil, response.NewError(ErrEnqueueFailed, nil)
	}

	// 回读任务（inline 已终态 / asynq 尚在排队）。
	final, err := s.repo.FindImportTask(ctx, id)
	if err != nil {
		return nil, err
	}
	res := &ImportConfirmResult{
		ID:          final.ID,
		Status:      final.Status,
		TotalRows:   final.TotalRows,
		SuccessRows: final.SuccessRows,
		FailedRows:  final.FailedRows,
	}
	if final.FinishedAt != nil {
		res.FinishedAt = database.JSONTime{Time: *final.FinishedAt}
	}
	if final.FailedRows > 0 {
		if rows, lerr := s.repo.ListImportRows(ctx, id, []string{RowStatusFailed}, previewRows); lerr == nil {
			for _, r := range rows {
				res.Errors = append(res.Errors, r.Errors...)
			}
		}
	}
	return res, nil
}

// ListTaskItem 任务列表项（excel §4 数据中心任务记录模型；前端 data.ts DataTaskItem
// 对齐，snake_case 出参——plan §12.4 条 1/2：后端为冻结契约）。时间字段统一
// database.JSONTime（api.md §2 YYYY-MM-DD HH:mm:ss；裸 *time.Time 序列化 RFC3339、
// CreatedAtStr 为格式化串——两种形态混用违反统一时间格式，2026-10-04 统一）。
type ListTaskItem struct {
	ID            database.ID       `json:"id"`
	TaskNo        string            `json:"task_no"`
	TaskType      string            `json:"task_type"` // IMPORT / EXPORT
	Module        string            `json:"module"`
	ModuleName    string            `json:"module_name"`
	Scope         string            `json:"scope,omitempty"`
	Status        string            `json:"status"`
	Progress      int               `json:"progress,omitempty"`
	TotalRows     int64             `json:"total_rows,omitempty"`
	SuccessRows   int64             `json:"success_rows,omitempty"`
	FailedRows    int64             `json:"failed_rows,omitempty"`
	CreatorID     int64             `json:"creator_id,omitempty"`
	FileName      string            `json:"file_name,omitempty"`
	FileURL       string            `json:"file_url,omitempty"`
	FileExpiredAt database.JSONTime `json:"file_expired_at,omitempty"`
	StartedAt     database.JSONTime `json:"started_at,omitempty"`
	FinishedAt    database.JSONTime `json:"finished_at,omitempty"`
	CreatedAt     database.JSONTime `json:"created_at"`
	ErrorMessage  string            `json:"error_message,omitempty"`
}

// ListImports 导入任务列表（excel §4 数据中心；筛选 status/module=import_type）。
func (s *Service) ListImports(ctx context.Context, f TaskListFilter, scope *FileScope) ([]ListTaskItem, int64, error) {
	f.Page, f.PageSize = normalizePage(f.Page, f.PageSize)
	rows, total, err := s.repo.ListImportTasks(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ListTaskItem, 0, len(rows))
	for _, t := range rows {
		item := ListTaskItem{
			ID: t.ID, TaskNo: t.ImportNo, TaskType: "IMPORT",
			Module: t.ImportType, ModuleName: ImportTypeLabel(t.ImportType),
			Status: t.Status,
			// 进度由 success_rows/failed_rows 推导（plan §13.3：import_tasks 无 progress 列）。
			Progress:    deriveProgress(int64(t.SuccessRows+t.FailedRows), int64(t.TotalRows)),
			TotalRows:   int64(t.TotalRows),
			SuccessRows: int64(t.SuccessRows),
			FailedRows:  int64(t.FailedRows),
			CreatorID:   t.CreatedBy,
			StartedAt:   jsonTimePtr(t.StartedAt), FinishedAt: jsonTimePtr(t.FinishedAt),
			CreatedAt:    t.CreatedAt,
			ErrorMessage: t.ErrorMessage,
		}
		// 错误明细联动字段按 FileScope 收口（跨仓不可见任务的错误明细链接不下发，
		// 与下载 404 同口径——导入任务产物按创建人规则可见）。
		if t.ErrorFileID > 0 {
			if ef, ferr := s.loadFileRow(ctx, t.ErrorFileID); ferr == nil && s.fileVisible(ctx, ef, scope) {
				item.FileURL = "/api/imports/" + strconv.FormatInt(t.ID.Int64(), 10) + "/error-file"
				item.FileName = ef.FileName
				item.FileExpiredAt = jsonTimePtr(ef.ExpiresAt)
			}
		}
		items = append(items, item)
	}
	return items, total, nil
}

// deriveProgress 进度推导（0–100；无数据行时恒 0）。
func deriveProgress(done, total int64) int {
	if total <= 0 || done <= 0 {
		return 0
	}
	p := done * 100 / total
	if p > 100 {
		p = 100
	}
	return int(p)
}

// normalizePage 分页兜底（Service 层入口统一；handler 已先经 response.ParsePage）。
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}
