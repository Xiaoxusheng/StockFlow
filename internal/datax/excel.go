package datax

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/stockflow/server/internal/response"

	"github.com/xuri/excelize/v2"
)

// excelize 工具层：模板现场生成、错误 Excel、流式导出装配（excel §1.2/§1.4/§2.3）。
// 模板无静态文件——按 TemplateSpec 列定义现场生成（plan §6.1）；
// 导出经 StreamWriter 逐批写（plan §6.3：数据行分批进入，不整表驻留内存）。

// 时间与数字格式（api.md §2 统一格式；excelize 自定义数字格式串）。
const (
	dateLayout     = "2006-01-02 15:04:05"
	dateLayoutOnly = "2006-01-02"
	numFmtDate     = "yyyy-mm-dd hh:mm:ss" // 日期列日期类型（excel §2.3：不要全字符串导出）
	numFmtQty      = "#,##0.####"          // 数量千分位
	numFmtMoney    = "#,##0.00"            // 金额千分位两位小数
)

// 解压放大守卫上限（防 zip 炸弹——excelize.OpenReader 全量加载 sharedStrings/工作簿
// 部件，恶意工作簿可在 ≤storage.upload_max_bytes 的上传内把内存放大数十倍；行数上限
// 守卫在迭代开始后才生效，单个超大部件可先于守卫耗尽内存）。上限为常量不新增冻结配置键
// （plan §3.2 清单外加键禁止）：ImportMaxRows 缺省 5000 行量级的合法工作簿解压通常
// 数 MB 内，64MB 总量/32MB 单部件留 6~10 倍余量，攻击比率（千倍级）必然命中。
// f15 收紧：预算必须对"HTTP 请求 goroutine 内同步二次全量加载"的内存峰值负责
// ——512MB 解压量经 excelize 转为 Go 字符串结构后单请求峰值可达 GB 级，收紧到
// 64MB 后单请求解析内存峰值被限制在数百 MB 内（高压缩比 xlsx 的放大攻击在
// 预检即被拒绝，合法导入不受影响）。
const (
	maxImportZipEntries        = 10_000   // 工作簿部件数上限（合法 xlsx 部件数十个量级）
	maxImportEntryUncompressed = 32 << 20 // 单部件解压上限 32MB
	maxImportTotalUncompressed = 64 << 20 // 全簿累计解压上限 64MB
)

// guardImportZipBudget 在 excelize.OpenReader 之前预检解压预算（excel §1.2 解析闸前置）：
// 逐部件流式解压到 io.Discard 核对实际字节数（O(1) 内存；不信任 zip 头声明的
// UncompressedSize64——恶意容器可谎报小尺寸），任一部件/累计实际解压超限即拒绝。
// 非 zip 或损坏容器放行给 excelize，由 OpenReader 统一按不可解析拒绝（错误口径不变）。
func guardImportZipBudget(r io.ReadSeeker) error {
	if _, err := r.Seek(0, io.SeekEnd); err != nil {
		return nil // 无法定位尺寸（非可寻址流）：按原路径交 excelize
	}
	size, err := r.Seek(0, io.SeekCurrent)
	if err != nil || size <= 0 {
		_, _ = r.Seek(0, io.SeekStart)
		return nil
	}
	zr, err := zip.NewReader(r.(io.ReaderAt), size)
	if err != nil {
		_, _ = r.Seek(0, io.SeekStart)
		return nil // 非 zip 容器：excelize OpenReader 统一拒绝
	}
	if len(zr.File) > maxImportZipEntries {
		return response.NewError(ErrImportFileSuspicious, map[string]any{"reason": "工作簿部件数超出安全上限"})
	}
	var total uint64
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			_, _ = r.Seek(0, io.SeekStart)
			return nil // 部件不可解（加密/不支持的方法）：excelize 按原路径拒绝
		}
		n, err := io.Copy(io.Discard, io.LimitReader(rc, maxImportEntryUncompressed+1))
		_ = rc.Close()
		if err != nil {
			_, _ = r.Seek(0, io.SeekStart)
			return nil // 数据损坏：excelize 按原路径拒绝
		}
		total += uint64(n)
		if n > maxImportEntryUncompressed {
			return response.NewError(ErrImportFileSuspicious, map[string]any{"entry": f.Name, "reason": "工作簿部件解压后超出安全上限"})
		}
		if total > maxImportTotalUncompressed {
			return response.NewError(ErrImportFileSuspicious, map[string]any{"reason": "工作簿解压总量超出安全上限"})
		}
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("datax: 回卷上传流失败: %w", err)
	}
	return nil
}

// parseDateCell 日期单元格解析（导入侧宽松格式：统一格式 / 纯日期 / 斜杠变体）。
func parseDateCell(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{dateLayout, dateLayoutOnly, "2006/01/02 15:04:05", "2006/01/02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// buildTemplate 按模板规格现场生成 xlsx 工作簿（excel §1.2 第 1 步）：
//   - 数据页：首行列头（按 Columns 顺序，加粗底色 + 列宽），随后示例行；
//   - 填表说明页：用途说明 + 逐列说明（列名/必填/类型/示例/说明）+ 校验说明清单。
func buildTemplate(spec TemplateSpec) (*excelize.File, error) {
	f := excelize.NewFile()

	sheet := templateSheetName(spec)
	if _, err := f.NewSheet(sheet); err != nil {
		return nil, fmt.Errorf("datax: 创建模板数据页失败: %w", err)
	}
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return nil, fmt.Errorf("datax: 清理默认页失败: %w", err)
	}

	titleStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#F2F2F2"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return nil, fmt.Errorf("datax: 创建列头样式失败: %w", err)
	}
	for i, col := range spec.Columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, cell, col.Title); err != nil {
			return nil, fmt.Errorf("datax: 写模板列头失败: %w", err)
		}
		if col.Width > 0 {
			colName, _ := excelize.ColumnNumberToName(i + 1)
			_ = f.SetColWidth(sheet, colName, colName, col.Width)
		}
	}
	if len(spec.Columns) > 0 {
		head, _ := excelize.CoordinatesToCellName(1, 1)
		tail, _ := excelize.CoordinatesToCellName(len(spec.Columns), 1)
		if err := f.SetCellStyle(sheet, head, tail, titleStyle); err != nil {
			return nil, fmt.Errorf("datax: 设置列头样式失败: %w", err)
		}
	}
	for r, sample := range spec.SampleRows {
		for i := 0; i < len(spec.Columns) && i < len(sample); i++ {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := f.SetCellValue(sheet, cell, sample[i]); err != nil {
				return nil, fmt.Errorf("datax: 写模板示例行失败: %w", err)
			}
		}
	}

	if err := buildNotesSheet(f, spec); err != nil {
		return nil, err
	}
	return f, nil
}

func templateSheetName(spec TemplateSpec) string {
	if spec.Sheet != "" {
		return spec.Sheet
	}
	return "导入数据"
}

// buildNotesSheet 填表说明页（excel §1.2：模板必须携带校验说明）。
func buildNotesSheet(f *excelize.File, spec TemplateSpec) error {
	const notesSheet = "填表说明"
	if _, err := f.NewSheet(notesSheet); err != nil {
		return fmt.Errorf("datax: 创建说明页失败: %w", err)
	}
	row := 1
	write := func(vals ...string) {
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			_ = f.SetCellValue(notesSheet, cell, v)
		}
		row++
	}
	write("模板名称", spec.Name)
	write("用途说明", spec.Description)
	write("")
	write("列名", "必填", "类型", "示例", "说明")
	for _, col := range spec.Columns {
		req := "否"
		if col.Required {
			req = "是"
		}
		write(col.Title, req, typeLabel(col.Type), col.Example, col.Note)
	}
	write("")
	write("校验说明")
	for _, n := range spec.Notes {
		write(n)
	}
	return nil
}

func typeLabel(t CellType) string {
	switch t {
	case CellNumber:
		return "数字"
	case CellMoney:
		return "金额（>=0）"
	case CellDate:
		return "日期（YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss）"
	default:
		return "文本"
	}
}

// buildErrorWorkbook 错误 Excel（excel §1.4：原数据 + 错误原因列，用户修复后重新导入）。
// 数据页按模板列序回填原数据、末列追加"错误原因"；附"填表说明"页保持模板结构
// （修复后重新上传仍按同模板校验）。
func buildErrorWorkbook(spec TemplateSpec, rows []errorRowData) ([]byte, error) {
	f, err := buildTemplate(spec)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sheet := templateSheetName(spec)
	reasonCol := len(spec.Columns) + 1
	head, _ := excelize.CoordinatesToCellName(reasonCol, 1)
	if err := f.SetCellValue(sheet, head, "错误原因"); err != nil {
		return nil, fmt.Errorf("datax: 写错误列头失败: %w", err)
	}
	for _, r := range rows {
		rowNo := r.RowNo + 1 // 数据行号 → Excel 物理行（表头占 1 行）
		for i, col := range spec.Columns {
			c, _ := excelize.CoordinatesToCellName(i+1, rowNo)
			if err := f.SetCellValue(sheet, c, r.Raw[col.Key]); err != nil {
				return nil, fmt.Errorf("datax: 回填错误行失败: %w", err)
			}
		}
		c, _ := excelize.CoordinatesToCellName(reasonCol, rowNo)
		if err := f.SetCellValue(sheet, c, strings.Join(r.Messages, "；")); err != nil {
			return nil, fmt.Errorf("datax: 写错误原因失败: %w", err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("datax: 生成错误 Excel 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// errorRowData 错误 Excel 回填行（原行原始文本 + 错误消息）。
type errorRowData struct {
	RowNo    int
	Raw      map[string]string
	Messages []string
}

// exportMetaRows 导出表 meta 区行数（标题/导出时间/导出人/查询条件/空行/列头——excel §2.3）。
const exportMetaRows = 6

// exportWorkbook 流式导出装配器（plan §6.3）：StreamWriter 从 A1 起按行顺序写，
// meta 区 + 列头先行，数据行经 AppendRow 逐批进入，Flush 前追加合计行。
type exportWorkbook struct {
	f       *excelize.File
	sheet   string
	sw      *excelize.StreamWriter
	cols    []Column
	styles  map[CellType]int
	nextRow int
	// 合计累计（Summary 声明的数值列；excel §2.3 总计/合计落位）。
	sumIndex map[int]SummaryRow
	sums     map[int]float64
	written  int
}

// newExportWorkbook 构建流式导出工作簿：title=模块名，meta=[导出时间, 导出人, 查询条件]。
func newExportWorkbook(title string, meta []string, cols []Column, summary []SummaryRow) (*exportWorkbook, error) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)

	styles := map[CellType]int{}
	for _, ct := range []CellType{CellDate, CellNumber, CellMoney} {
		fmtStr := ""
		switch ct {
		case CellDate:
			fmtStr = numFmtDate
		case CellNumber:
			fmtStr = numFmtQty
		case CellMoney:
			fmtStr = numFmtMoney
		}
		id, err := f.NewStyle(&excelize.Style{CustomNumFmt: &fmtStr})
		if err != nil {
			return nil, fmt.Errorf("datax: 创建导出单元格样式失败: %w", err)
		}
		styles[ct] = id
	}
	headStyle, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, fmt.Errorf("datax: 创建导出表头样式失败: %w", err)
	}

	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return nil, fmt.Errorf("datax: 创建流式写出器失败: %w", err)
	}
	w := &exportWorkbook{
		f: f, sheet: sheet, sw: sw, cols: cols, styles: styles,
		nextRow: 1, sumIndex: map[int]SummaryRow{}, sums: map[int]float64{},
	}
	// 列宽（须先于写行——StreamWriter 口径；未声明按标题长度估算，中文按 2 字符宽）。
	for i, c := range cols {
		if cw := w.widthOf(c); cw > 0 {
			_ = w.sw.SetColWidth(i+1, i+1, cw)
		}
	}
	// meta 区（excel §2.3：标题、导出时间、导出人、查询条件）。
	if err := w.writeTextRow([]string{title + "导出"}, nil); err != nil {
		return nil, err
	}
	for _, m := range meta {
		if err := w.writeTextRow([]string{m}, nil); err != nil {
			return nil, err
		}
	}
	if err := w.writeTextRow(nil, nil); err != nil { // 空行分隔
		return nil, err
	}
	// 列头行。
	cells := make([]any, 0, len(cols))
	for _, c := range cols {
		cells = append(cells, excelize.Cell{StyleID: headStyle, Value: c.Title})
	}
	if err := w.setRow(cells); err != nil {
		return nil, err
	}
	// 合计声明索引。
	for _, s := range summary {
		for i, c := range cols {
			if c.Key == s.ColumnKey {
				w.sumIndex[i] = s
			}
		}
	}
	return w, nil
}

// widthOf 列宽（未声明按标题长度估算，中文字符按 2 计）。
func (w *exportWorkbook) widthOf(c Column) float64 {
	if c.Width > 0 {
		return c.Width
	}
	n := float64(0)
	for _, r := range c.Title {
		if r > 0xFF {
			n += 2
		} else {
			n += 1
		}
	}
	if n < 10 {
		return 10
	}
	return n + 2
}

// writeTextRow 写纯文本行（meta 区；nil 写空行）。
func (w *exportWorkbook) writeTextRow(values []string, styleID *int) error {
	cells := make([]any, 0, len(values))
	for _, v := range values {
		c := excelize.Cell{Value: v}
		if styleID != nil {
			c.StyleID = *styleID
		}
		cells = append(cells, c)
	}
	return w.setRow(cells)
}

// setRow 在当前行写单元格并推进行号（StreamWriter 要求行序连续递增）。
func (w *exportWorkbook) setRow(cells []any) error {
	cellName, _ := excelize.CoordinatesToCellName(1, w.nextRow)
	if err := w.sw.SetRow(cellName, cells); err != nil {
		return fmt.Errorf("datax: 写出第 %d 行失败: %w", w.nextRow, err)
	}
	w.nextRow++
	return nil
}

// AppendRow 写一行数据（Row.Cells 与列定义一一对应；类型驱动单元格类型——excel §2.3
// 禁止全字符串导出），并累计声明合计的数值列。
func (w *exportWorkbook) AppendRow(r Row) error {
	cells := make([]any, len(w.cols))
	for i, col := range w.cols {
		v := CellValue{T: CellText}
		if i < len(r.Cells) {
			v = r.Cells[i]
		}
		c := excelize.Cell{}
		switch col.Type {
		case CellNumber:
			c.Value = v.N
			c.StyleID = w.styles[CellNumber]
			if _, declared := w.sumIndex[i]; declared {
				w.sums[i] += v.N
			}
		case CellMoney:
			c.Value = v.N
			c.StyleID = w.styles[CellMoney]
			if _, declared := w.sumIndex[i]; declared {
				w.sums[i] += v.N
			}
		case CellDate:
			if t, ok := parseDateCell(v.D); ok {
				c.Value = t
			} else {
				c.Value = v.D
			}
			c.StyleID = w.styles[CellDate]
		default:
			c.Value = v.S
		}
		cells[i] = c
	}
	if err := w.setRow(cells); err != nil {
		return err
	}
	w.written++
	return nil
}

// Written 已写数据行数（进度分母核对）。
func (w *exportWorkbook) Written() int { return w.written }

// FinalizeTo 追加合计行（有声明时）并流式写出 xlsx 到 out（StreamWriter Flush 后
// WriteTo 逐部件写入——不把整簿一次性物化为内存 []byte；excel §3"流式写出 Excel，
// 不整表驻留内存"对序列化阶段的延伸，plan §6.3）。
func (w *exportWorkbook) FinalizeTo(out io.Writer) error {
	if len(w.sumIndex) > 0 {
		cells := make([]any, len(w.cols))
		label := "合计"
		for _, s := range w.sumIndex {
			if s.Label != "" {
				label = s.Label
			}
		}
		cells[0] = excelize.Cell{Value: label}
		for i := range w.sums {
			cells[i] = excelize.Cell{Value: w.sums[i], StyleID: w.styles[w.cols[i].Type]}
		}
		if err := w.setRow(cells); err != nil {
			return err
		}
	}
	if err := w.sw.Flush(); err != nil {
		return fmt.Errorf("datax: 刷新流式写出器失败: %w", err)
	}
	if _, err := w.f.WriteTo(out); err != nil {
		return fmt.Errorf("datax: 生成导出文件失败: %w", err)
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("datax: 关闭导出工作簿失败: %w", err)
	}
	return nil
}

// Finalize 内存 buffer 形态（小簿场景/测试替身便利；导出执行器走 FinalizeTo 流式落盘）。
func (w *exportWorkbook) Finalize() ([]byte, error) {
	var buf bytes.Buffer
	if err := w.FinalizeTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Abort 中途失败释放工作簿资源（调用方决定重试；无磁盘残留）。
func (w *exportWorkbook) Abort() {
	_ = w.f.Close()
}

// parseNumber 数值单元格解析（结构层：NUMBER/MONEY 共用；金额负值由调用方按列语义拒绝）。
func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// formatCellText 单元格值 → 人类可读文本（错误 Excel 回填 / 预览缺省）。
func formatCellText(v CellValue) string {
	switch v.T {
	case CellNumber, CellMoney:
		return strconv.FormatFloat(v.N, 'f', -1, 64)
	case CellDate:
		return v.D
	default:
		return v.S
	}
}
