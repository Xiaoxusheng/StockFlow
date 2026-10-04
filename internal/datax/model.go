package datax

import (
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
)

// 任务表 GORM 模型（db/migrations/000011_create_datax_tables.up.sql DDL 契约；
// files 表模型由 internal/storage 承载，本包直接复用不建第二套）。
// 状态机（DDL CHECK 同源）：
//   - import_tasks: PARSED → VALIDATED → EXECUTING → SUCCESS/PARTIAL_SUCCESS/FAILED
//   - import_task_rows: RAW → VALID/INVALID → QUEUED → SUCCESS/FAILED
//   - export_tasks: QUEUED → PROCESSING → SUCCESS/FAILED（PARTIAL_SUCCESS 为 DDL
//     合法值，导出执行口径见 service_export_run.go：失败整文件重试重生成，不落部分产物）

// 导入任务状态。
const (
	TaskStatusParsed     = "PARSED"
	TaskStatusValidated  = "VALIDATED"
	TaskStatusExecuting  = "EXECUTING"
	TaskStatusSuccess    = "SUCCESS"
	TaskStatusPartial    = "PARTIAL_SUCCESS"
	TaskStatusFailed     = "FAILED"
	TaskStatusQueued     = "QUEUED"
	TaskStatusProcessing = "PROCESSING"
)

// 导入任务行状态（断点续跑依据，plan §6.2：仅重试 QUEUED/FAILED 行，SUCCESS 行跳过）。
const (
	RowStatusRaw     = "RAW"
	RowStatusValid   = "VALID"
	RowStatusInvalid = "INVALID"
	RowStatusQueued  = "QUEUED"
	RowStatusSuccess = "SUCCESS"
	RowStatusFailed  = "FAILED"
)

// ImportTask import_tasks 行。
type ImportTask struct {
	ID           database.ID       `gorm:"primaryKey;autoIncrement"`
	ImportNo     string            `gorm:"column:import_no;size:64"`
	ImportType   string            `gorm:"column:import_type;size:32"`
	Status       string            `gorm:"column:status;size:32"`
	SourceFileID int64             `gorm:"column:source_file_id"`
	TotalRows    int               `gorm:"column:total_rows"`
	ValidRows    int               `gorm:"column:valid_rows"`
	ErrorRows    int               `gorm:"column:error_rows"`
	SuccessRows  int               `gorm:"column:success_rows"`
	FailedRows   int               `gorm:"column:failed_rows"`
	ErrorFileID  int64             `gorm:"column:error_file_id"`
	ErrorMessage string            `gorm:"column:error_message"`
	StartedAt    *time.Time        `gorm:"column:started_at"`
	FinishedAt   *time.Time        `gorm:"column:finished_at"`
	CreatedAt    database.JSONTime `gorm:"column:created_at"`
	UpdatedAt    database.JSONTime `gorm:"column:updated_at"`
	CreatedBy    int64             `gorm:"column:created_by"`
	UpdatedBy    int64             `gorm:"column:updated_by"`
}

// TableName 表名（DDL 契约）。
func (ImportTask) TableName() string { return "import_tasks" }

// ImportTaskRow import_task_rows 行。
type ImportTaskRow struct {
	ID        database.ID       `gorm:"primaryKey;autoIncrement"`
	TaskID    int64             `gorm:"column:task_id"`
	RowNo     int               `gorm:"column:row_no"`
	Raw       JSONMap           `gorm:"column:raw;type:jsonb"`
	Parsed    JSONMap           `gorm:"column:parsed;type:jsonb"`
	Status    string            `gorm:"column:status;size:16"`
	Errors    []RowError        `gorm:"column:errors;type:jsonb;serializer:json"`
	BatchNo   int               `gorm:"column:batch_no"`
	CreatedAt database.JSONTime `gorm:"column:created_at"`
	UpdatedAt database.JSONTime `gorm:"column:updated_at"`
	CreatedBy int64             `gorm:"column:created_by"`
	UpdatedBy int64             `gorm:"column:updated_by"`
}

// TableName 表名（DDL 契约）。
func (ImportTaskRow) TableName() string { return "import_task_rows" }

// ExportTask export_tasks 行。
type ExportTask struct {
	ID           database.ID       `gorm:"primaryKey;autoIncrement"`
	ExportNo     string            `gorm:"column:export_no;size:64"`
	Module       string            `gorm:"column:module;size:32"`
	Scope        string            `gorm:"column:scope;size:16"`
	Params       JSONMap           `gorm:"column:params;type:jsonb"`
	Status       string            `gorm:"column:status;size:32"`
	Progress     int               `gorm:"column:progress"`
	TotalRows    int64             `gorm:"column:total_rows"`
	FileID       int64             `gorm:"column:file_id"`
	ErrorMessage string            `gorm:"column:error_message"`
	StartedAt    *time.Time        `gorm:"column:started_at"`
	FinishedAt   *time.Time        `gorm:"column:finished_at"`
	CreatedAt    database.JSONTime `gorm:"column:created_at"`
	UpdatedAt    database.JSONTime `gorm:"column:updated_at"`
	CreatedBy    int64             `gorm:"column:created_by"`
	UpdatedBy    int64             `gorm:"column:updated_by"`
}

// TableName 表名（DDL 契约）。
func (ExportTask) TableName() string { return "export_tasks" }

// JSONMap jsonb 列载体（map 形态：raw/parsed = 列键 → 值；params = 导出参数快照）。
// pgx 对 jsonb 目标列按原始 JSON 处理 string，故 Value 返回序列化字符串。
type JSONMap map[string]any

// Value 实现 driver.Valuer。
func (m JSONMap) Value() (any, error) {
	if m == nil {
		return nil, nil
	}
	b, err := jsonMarshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner。
func (m *JSONMap) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = nil
	case []byte:
		return jsonUnmarshal(v, m)
	case string:
		return jsonUnmarshal([]byte(v), m)
	default:
		return gorm.ErrInvalidData
	}
	return nil
}
