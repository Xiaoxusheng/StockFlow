package datax

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/storage"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// 数据访问层（architecture.md §1：Service → Repository → Database；handler 禁止直连数据库）。
// Repository 为接口：Service 依赖接口，单元测试以内存替身替换（ask 约束：单测不依赖
// PostgreSQL/Redis/网络，internal/returns/internal/masterdata 既有替身模式）。
//
// 写表白名单（plan §2.3 判据 3）：import_tasks / import_task_rows / export_tasks / files。
// files 模型复用 internal/storage.File（存储层交付，不建第二套登记模型）。

// TaskListFilter 任务列表过滤（导入/导出共用；列表强制分页，architecture.md §7）。
type TaskListFilter struct {
	Module   string // import_type / module，空=全部
	Status   string // 空=全部
	Page     int
	PageSize int
}

// FileListFilter 文件列表过滤（excel §7：按业务模块/关联单据过滤 + 数据权限范围）。
type FileListFilter struct {
	Module     string
	BusinessNo string
	Keyword    string // 文件名 ILIKE
	Page       int
	PageSize   int
	// Scope 文件中心数据权限（plan §6.4：沿用仓库范围 Service 层过滤；nil = 全量，
	// 仅测试替身场景使用——生产 handler 必传，fail-closed 语义由调用方保证）。
	Scope *FileScope
}

// FileScope 文件中心数据权限范围（handler 自 auth.WarehouseScope + CurrentUser 组装）。
// files 表无仓库承载列（000011 冻结 DDL），范围受限用户的可见性按以下规则推导
// （fail-closed，冻结规则注释见 service_file.go）：
//   - All=true：全部可见；
//   - 范围受限：本人上传文件 ∪ datax 导出产物（归属导出任务仓库范围快照与可见仓库集
//     相交）∪ datax 导入任务产物（归属导入任务创建人——import_tasks 无仓库快照列）。
type FileScope struct {
	All          bool
	WarehouseIDs []int64
	UserID       int64
}

// Repository datax 数据访问接口。
type Repository interface {
	// DB 返回根句柄（Service 开启事务、写审计用；masterdata 同款约定）。
	DB() *gorm.DB

	// —— 导入任务 ——
	// InsertImportTask 事务内建任务行（含 docnum.Next 发号，发号与建行同事务）。
	InsertImportTask(ctx context.Context, tx *gorm.DB, t *ImportTask) error
	FindImportTask(ctx context.Context, id int64) (*ImportTask, error)
	FindImportTaskByNo(ctx context.Context, no string) (*ImportTask, error)
	ListImportTasks(ctx context.Context, f TaskListFilter) ([]*ImportTask, int64, error)
	// GuardUpdateImportTask 状态机守卫 UPDATE：WHERE id=? AND status IN from，
	// 影响行数 0 = 状态冲突（plan §13.2 统一形态；set 为列值映射，列名白名单由
	// 调用方构造——本仓唯一调用方是 datax Service，列名即模型列常量）。
	GuardUpdateImportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error)

	// —— 导入任务行 ——
	InsertImportRows(tx *gorm.DB, rows []*ImportTaskRow) error
	ListImportRows(ctx context.Context, taskID int64, statuses []string, limit int) ([]*ImportTaskRow, error)
	UpdateImportRow(tx *gorm.DB, rowID int64, set map[string]any) error
	// CountImportRowsByStatus 行状态计数（终态汇总/进度推导）。
	CountImportRowsByStatus(ctx context.Context, taskID int64) (map[string]int64, error)

	// —— 导出任务 ——
	InsertExportTask(ctx context.Context, tx *gorm.DB, t *ExportTask) error
	FindExportTask(ctx context.Context, id int64) (*ExportTask, error)
	FindExportTaskByNo(ctx context.Context, no string) (*ExportTask, error)
	ListExportTasks(ctx context.Context, f TaskListFilter) ([]*ExportTask, int64, error)
	GuardUpdateExportTask(tx *gorm.DB, id int64, from []string, set map[string]any) (int64, error)
	UpdateExportTask(ctx context.Context, id int64, set map[string]any) error // 进度回填等无守卫列更新

	// —— 文件中心（files 表；模型复用 internal/storage.File）——
	InsertFile(tx *gorm.DB, f *storage.File) error
	FindFile(ctx context.Context, id int64) (*storage.File, error)
	ListFiles(ctx context.Context, f FileListFilter) ([]*storage.File, int64, error)
	SoftDeleteFile(tx *gorm.DB, id int64, by int64) (int64, error)
}
