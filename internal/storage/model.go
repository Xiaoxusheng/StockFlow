// files 表 GORM 模型（000011_create_datax_tables.up.sql DDL 契约，backend-m3-plan §5）。
// 业务编排（登记事务/下载/删除审计/清理）由 internal/datax 承接——本包只承载存储层模型。
package storage

import (
	"time"

	"gorm.io/gorm"
)

// File 文件中心统一登记（database.md §2 注：attachments 由本表承载，不建第二套附件表；
// 下载记录复用 operation_logs，module=file, action=download——plan §5 000011 注）。
type File struct {
	ID int64 `gorm:"column:id;primaryKey"`
	// FileName 原始文件名（仅展示，绝不参与存储路径拼装——api §5）。
	FileName string `gorm:"column:file_name"`
	// StoredName 服务端生成存储名（uuid + 白名单扩展名）。
	StoredName string `gorm:"column:stored_name"`
	// StoragePath 相对 storage.root 的存储路径（yyyyMM/stored_name，防路径穿越）。
	StoragePath string `gorm:"column:storage_path"`
	MimeType    string `gorm:"column:mime_type"`
	// FileType 扩展名白名单值（internal/storage 白名单注册表冻结）。
	FileType  string `gorm:"column:file_type"`
	SizeBytes int64  `gorm:"column:size_bytes"`
	Module    string `gorm:"column:module"`
	// BusinessNo 业务单号逻辑引用（如 IMP-/EXP- 任务号；跨域不建 FK）。
	BusinessNo   string `gorm:"column:business_no"`
	UploaderID   int64  `gorm:"column:uploader_id"`
	UploaderName string `gorm:"column:uploader_name"`
	// ExpiresAt 过期时间（datax.file_retention_days 推导；NULL=不过期，file_cleanup 清理）。
	ExpiresAt *time.Time `gorm:"column:expires_at"`
	// DeletedAt 软删除（GORM 自动过滤已删除行；物理删除由 file_cleanup 承担）。
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	CreatedBy int64     `gorm:"column:created_by"`
	UpdatedBy int64     `gorm:"column:updated_by"`
}

// TableName 表名（DDL 契约：files）。
func (File) TableName() string { return "files" }
