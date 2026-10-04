package datax

import (
	"context"
	"time"
)

// 跨域窄接口与值类型（backend-m3-plan §12.2 冻结签名；消费方包内定义、router 注入、
// 实现落 §2.2 冻结新增文件）。签名一律内建类型或本包值类型（同 internal/stock 先例），
// 不携带任何域类型——域包 import 本包方向合法（plan §2.3 判据 2：域包 → 平台契约包），
// 本包反向 import 域包即违约。
//
// ImportWriter（×9）/ ExportSource（×16）的最小形态：
//
//	ImportWriter: Template() TemplateSpec；Validate(ctx, rows) ([]RowError, error)；
//	              Commit(ctx, actor, taskRef, rows) (CommitResult, error)
//	ExportSource: Columns() []Column；Count(ctx, ExportFilter) (int64, error)；
//	              Batch(ctx, ExportFilter, Cursor) ([]Row, Cursor, error)；Summary() []SummaryRow
//
// Summary 为可选语义：返回 nil 即不生成合计行（excel §2.3 总计/合计落位，plan §6.3）。

// CellType 单元格类型（excel §2.3：禁止全字符串导出——数值列数值单元格、日期列日期类型；
// 导入侧同类型驱动解析规则）。
type CellType string

const (
	CellText   CellType = "TEXT"   // 文本（原样）
	CellNumber CellType = "NUMBER" // 数量/度量：>0 校验由业务层承担；导出千分位 #,##0.####
	CellMoney  CellType = "MONEY"  // 金额：>=0、4 位精度；导出千分位 #,##0.00
	CellDate   CellType = "DATE"   // 日期时间：YYYY-MM-DD HH:mm:ss（api.md §2 统一格式）
)

// CellValue 通用单元格值（jsonb parsed 列的存储形态 + 预览/导出的传输形态）：
// TEXT→S；NUMBER/MONEY→N；DATE→D（统一 YYYY-MM-DD HH:mm:ss 文本，导出时转 time.Time
// 写日期单元格）。零值判空按 T 分派。
type CellValue struct {
	T CellType `json:"t"`
	S string   `json:"s,omitempty"`
	N float64  `json:"n,omitempty"`
	D string   `json:"d,omitempty"`
}

// IsEmpty 值是否为空（必填校验用；类型化空值与空白文本均为空）。
func (v CellValue) IsEmpty() bool {
	switch v.T {
	case CellNumber, CellMoney:
		return v.N == 0
	case CellDate:
		return v.D == ""
	default:
		return v.S == ""
	}
}

// Time 解析 DATE 值为 time.Time（导出单元格用；解析失败返回零值，调用方无需二次处理——
// 写入端保证 D 为本包可解析格式）。
func (v CellValue) Time() time.Time {
	t, _ := parseDateCell(v.D)
	return t
}

// Column 列定义（模板列头 + 导出列头同构；导入侧 Required/UniqueInFile 驱动结构层校验，
// 导出侧 Type 驱动单元格类型与数字格式）。
type Column struct {
	Key   string   `json:"key"`             // 列键（raw/parsed jsonb 键、预览列 key）
	Title string   `json:"title"`           // 列头标题（模板首行/导出首行）
	Type  CellType `json:"type"`            // 单元格类型
	Width float64  `json:"width,omitempty"` // 列宽（字符）
	// —— 导入模板专用 ——
	Required     bool   `json:"required,omitempty"`       // 必填（excel §1.3 必填字段）
	UniqueInFile bool   `json:"unique_in_file,omitempty"` // 文件内去重（excel §1.3 重复数据（文件内重复））
	Example      string `json:"example,omitempty"`        // 模板示例值
	Note         string `json:"note,omitempty"`           // 填表说明（模板"填表说明"页逐列说明）
}

// TemplateSpec 导入模板规格（Writer 声明；模板下载 = datax 按列定义用 excelize 现场生成，
// 无静态文件——plan §6.1）。
type TemplateSpec struct {
	ImportType  string     `json:"import_type"`
	Name        string     `json:"name"`        // 展示名（如"商品导入"）
	FileName    string     `json:"file_name"`   // 模板下载文件名（如"商品导入模板.xlsx"）
	Description string     `json:"description"` // 用途说明
	Sheet       string     `json:"sheet"`       // 数据页名
	Columns     []Column   `json:"columns"`     // 列定义（上传时表头逐列比对依据）
	SampleRows  [][]string `json:"sample_rows"` // 示例行（与 Columns 等长对齐的原始文本）
	Notes       []string   `json:"notes"`       // 校验说明（excel §1.2 模板必须携带校验说明）
}

// RowError 行级错误（excel §1.4 精确到行列；errors jsonb 存储形态 [{row,column,message}]，
// 前端 ImportValidationError 同构）。
type RowError struct {
	Row     int    `json:"row"`              // 数据行号（1 起，不含表头）
	Column  string `json:"column,omitempty"` // 出错列键；行级/跨列错误缺省
	Message string `json:"message"`
}

// ImportRow 解析后的导入行（Writer 校验/提交的输入；Cells 为类型化值，Raw 为原始文本——
// 错误 Excel 按原数据回填）。
type ImportRow struct {
	RowNo int                  `json:"row_no"`
	Cells map[string]CellValue `json:"cells"`
	Raw   map[string]string    `json:"raw"`
}

// String 快捷读取 TEXT 列。
func (r ImportRow) String(key string) string { return r.Cells[key].S }

// TaskRef 导入任务引用（Writer Commit 入参；Writer 不得依赖任务表结构，仅凭引用归因）。
type TaskRef struct {
	TaskID     int64  // import_tasks.id
	ImportNo   string // IMP- 单号（幂等键构成 + 流水 business_no）
	ImportType string // 九值之一
}

// CommitResult 批次提交结果（逐行结果：Errors 未点名的行视为成功；行号必须落在
// 入参 rows 集合内，越界行号视为 Writer 契约违约按错误处理）。
type CommitResult struct {
	SuccessRows int
	FailedRows  int
	Errors      []RowError
}

// ImportWriter 导入写入器窄接口（plan §12.2 冻结签名；九类实现落 §2.2 冻结文件：
// masterdata/warehouse/purchase/sales/inventory 各自 datax_import.go）。
//
// 写路径红线（plan §2.2 规则①/§2.3 判据 10）：Commit 必须经域内既有 Service/Repository
// 通路写入（期初库存经库存原语 EnsureBatch+Putaway），禁止另写旁路 SQL、禁止直写库存族表。
type ImportWriter interface {
	// Template 模板规格（模板下载现场生成的数据源）。
	Template() TemplateSpec
	// Validate 业务层校验（存在性/库内重复/业务关系，excel §1.3）：只读，逐行返回
	// RowError（行号 = 入参行 RowNo）；结构层（必填/类型/文件内重复）由 datax 在调用前完成。
	Validate(ctx context.Context, rows []ImportRow) ([]RowError, error)
	// Commit 分批提交（excel §6.2 分批事务、单批失败不影响已完成批次）：按行独立落库，
	// 返回逐行结果；行级失败进 errors，基础设施错误返回 error（asynq 断点续跑重试）。
	Commit(ctx context.Context, actor Actor, ref TaskRef, rows []ImportRow) (CommitResult, error)
}

// ExportFilter 导出筛选（scope 五值 excel §2.2；仓库范围数据权限由 handler 从
// auth.WarehouseScope 注入，域内实现过滤——plan §13.6）。
type ExportFilter struct {
	Scope    string  // CURRENT_PAGE/SELECTED/ALL/BY_FILTER/TIME_RANGE
	IDs      []int64 // SELECTED：选中记录 ID（≤1000，datax 创建时已校验）
	Page     int     // CURRENT_PAGE：页码（1 起）
	PageSize int     // CURRENT_PAGE：页大小
	// Filters BY_FILTER：各域列表筛选键白名单（plan §18.5：实现内只读白名单键，
	// 未知键忽略，杜绝任意键注入查询）。
	Filters map[string]string
	// TimeFrom/TimeTo TIME_RANGE：创建时间区间（含边界；两者必填）。
	TimeFrom *time.Time
	TimeTo   *time.Time
	// 数据权限仓库范围（auth.WarehouseScope 快照；AllWarehouses=true 全量，
	// false 时 WarehouseIDs 为可见仓库集，空集 = 不可见任何仓库行——fail-closed）。
	AllWarehouses bool
	WarehouseIDs  []int64
	// BatchSize 每批行数（执行器从 datax.export_batch_size 注入；源实现取 0 时
	// 以自有缺省值兜底）。
	BatchSize int
}

// Cursor keyset 游标（plan §6.3：游标 = 排序键最后值，禁止 offset 深翻页）。
// Value 语义由 Source 自定（本仓口径 = 主键 id 十进制文本，升序分批）；
// Done=true 表示无更多数据。
type Cursor struct {
	Value string `json:"v,omitempty"`
	Done  bool   `json:"done"`
}

// Row 导出行（Cells 与 Source.Columns 一一对应，顺序即列序）。
type Row struct {
	Cells []CellValue
}

// SummaryRow 合计声明（可选）：对 ColumnKey 数值列在表尾输出合计（excel §2.3 总计/合计）。
type SummaryRow struct {
	ColumnKey string `json:"column_key"`
	Label     string `json:"label"` // 首列说明文案（如"合计"）；空则用"合计"
}

// ExportSource 导出行源窄接口（plan §12.2 冻结签名；16 模块实现落 §2.2 冻结文件；
// REPORT 行源归 MT4 报表域）。实现只读本域表（含既有索引），数据权限按
// ExportFilter 仓库范围在实现内过滤。
type ExportSource interface {
	// Columns 导出列定义（表头 + 单元格类型驱动）。
	Columns() []Column
	// Count 满足筛选的总行数（进度分母 + total_rows）。
	Count(ctx context.Context, f ExportFilter) (int64, error)
	// Batch 下一批数据（每批 ≤ datax.export_batch_size）：f/c 为不可变入参；
	// 返回值 next 为续读游标，无数据时 next.Done=true。契约：Batch 返回 0 行且
	// Done=false 视为违约（datax 终止任务防死循环）。
	Batch(ctx context.Context, f ExportFilter, c Cursor) ([]Row, Cursor, error)
	// Summary 合计声明（nil = 不生成合计行）。
	Summary() []SummaryRow
}
