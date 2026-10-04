package devices

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/stockflow/server/internal/database"
)

// devices 域 GORM 模型（迁移 000013_create_device_tables.up.sql 列结构）。
// devices/device_configs/app_versions 为状态表（updated_at/created_by 全量字段）；
// scan_logs/device_logs 为 append-only 审计表（database.md §3 但书：无 updated_by/
// deleted_at，应用层仅 INSERT——db/grants/app_grants.sql 对两表仅 SELECT+INSERT）。

// ---- 值域（与迁移 CHECK 约束同源）----

// 设备类型（chk_devices_type；devices.md §6/前端设备中心分组小写值域）。
const (
	TypePC      = "pc"
	TypePad     = "pad"
	TypePDA     = "pda"
	TypeScanner = "scanner"
	TypePrinter = "printer"
)

// deviceTypes 类型值域（供校验）。
var deviceTypes = map[string]bool{TypePC: true, TypePad: true, TypePDA: true, TypeScanner: true, TypePrinter: true}

// DeviceTypeValid 设备类型值域判定。
func DeviceTypeValid(t string) bool { return deviceTypes[t] }

// 设备启停状态（chk_devices_status）。
const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
)

// 激活状态（chk_devices_activation_status；plan §12.4 条 4：值冻结为大写枚举，
// expired/disabled 为前端派生态）。
const (
	ActivationPending   = "PENDING"
	ActivationActivated = "ACTIVATED"
)

// 设备日志级别（chk_device_logs_level）。
const (
	LogLevelInfo  = "INFO"
	LogLevelWarn  = "WARN"
	LogLevelError = "ERROR"
)

// logLevels 级别值域。
var logLevels = map[string]bool{LogLevelInfo: true, LogLevelWarn: true, LogLevelError: true}

// LogLevelValid 设备日志级别值域判定。
func LogLevelValid(l string) bool { return logLevels[l] }

// App 平台（chk_app_versions_platform——M3 值域仅 android）。
const PlatformAndroid = "android"

// App 版本状态（chk_app_versions_status）。
const (
	AppVersionDraft      = "DRAFT"
	AppVersionPublished  = "PUBLISHED"
	AppVersionDeprecated = "DEPRECATED"
)

// ---- jsonb 载体（列 device_configs.config / device_logs.context；形态对齐 returns.jsonb 约定）----

type jsonb json.RawMessage

func (j jsonb) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

func (j *jsonb) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append(jsonb(nil), v...)
	case string:
		*j = jsonb(v)
	default:
		return fmt.Errorf("devices.jsonb.Scan 不支持类型 %T", src)
	}
	return nil
}

func (j jsonb) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *jsonb) UnmarshalJSON(b []byte) error {
	*j = append(jsonb(nil), b...)
	return nil
}

// ---- devices ----

// Device 设备档案（devices.md §6：注册/二维码激活/绑定仓库/心跳）。
// 无软删除（000013 DDL 无 deleted_at）：下线走 DISABLED + token_version+1。
type Device struct {
	ID                  database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	Code                string            `gorm:"column:code;size:64" json:"code"`
	Name                string            `gorm:"column:name;size:128" json:"name"`
	Type                string            `gorm:"column:type;size:16" json:"type"`
	Brand               string            `gorm:"column:brand;size:64" json:"brand"`
	Model               string            `gorm:"column:model;size:64" json:"model"`
	OS                  string            `gorm:"column:os;size:32" json:"os"`
	WarehouseID         int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	BoundUserID         int64             `gorm:"column:bound_user_id" json:"bound_user_id"`
	Status              string            `gorm:"column:status;size:16" json:"status"`
	ActivationStatus    string            `gorm:"column:activation_status;size:16" json:"activation_status"`
	ActivationTokenHash string            `gorm:"column:activation_token_hash;size:128" json:"-"`
	ActivationExpiresAt database.JSONTime `gorm:"column:activation_expires_at" json:"activation_expires_at"`
	ActivatedAt         database.JSONTime `gorm:"column:activated_at" json:"activated_at"`
	ActivatedBy         int64             `gorm:"column:activated_by" json:"activated_by"`
	AppVersion          string            `gorm:"column:app_version;size:32" json:"app_version"`
	LastOnlineAt        database.JSONTime `gorm:"column:last_online_at" json:"last_online_at"`
	LastScanAt          database.JSONTime `gorm:"column:last_scan_at" json:"last_scan_at"`
	BatteryLevel        *int              `gorm:"column:battery_level" json:"battery_level"`
	IP                  string            `gorm:"column:ip;size:64" json:"ip"`
	TokenVersion        int               `gorm:"column:token_version" json:"token_version"`
	Remark              string            `gorm:"column:remark" json:"remark"`
	CreatedAt           database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt           database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy           int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy           int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (Device) TableName() string { return "devices" }

// DeviceConfig 设备配置（devices.md §7.3 下发项；device_id UNIQUE 一机一配置，
// version 单调递增，心跳响应携带——plan §8.2）。
type DeviceConfig struct {
	ID        database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID  int64             `gorm:"column:device_id" json:"device_id"`
	Config    jsonb             `gorm:"column:config;type:jsonb" json:"config"`
	Version   int               `gorm:"column:version" json:"version"`
	CreatedAt database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (DeviceConfig) TableName() string { return "device_configs" }

// ScanLog 扫码审计（devices.md §13.2：谁、在哪台设备、什么时候、扫了什么——append-only，
// 应用层仅 INSERT）。device_id/device_code 可空（NULL = PC Web HID 场景无设备行，
// 以 user_id/username/ip 归因——plan §8.3）。
type ScanLog struct {
	ID          database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID    *int64            `gorm:"column:device_id" json:"device_id"`
	DeviceCode  *string           `gorm:"column:device_code" json:"device_code"`
	UserID      int64             `gorm:"column:user_id" json:"user_id"`
	Username    string            `gorm:"column:username;size:64" json:"username"`
	IP          string            `gorm:"column:ip;size:64" json:"ip"`
	WarehouseID int64             `gorm:"column:warehouse_id" json:"warehouse_id"`
	RawCode     string            `gorm:"column:raw_code;size:255" json:"raw_code"`
	Symbology   string            `gorm:"column:symbology;size:32" json:"symbology"`
	ResolveType string            `gorm:"column:resolve_type;size:32" json:"resolve_type"`
	ResolveID   int64             `gorm:"column:resolve_id" json:"resolve_id"`
	ResolveCode string            `gorm:"column:resolve_code;size:128" json:"resolve_code"`
	Page        string            `gorm:"column:page;size:128" json:"page"`
	BusinessNo  string            `gorm:"column:business_no;size:64" json:"business_no"`
	Success     bool              `gorm:"column:success" json:"success"`
	ErrorCode   string            `gorm:"column:error_code;size:64" json:"error_code"`
	CreatedAt   database.JSONTime `gorm:"column:created_at" json:"created_at"`
}

// TableName 显式指定表名。
func (ScanLog) TableName() string { return "scan_logs" }

// DeviceLog 设备运行日志（设备端批量上报 ≤100 条/次——plan §8.2；append-only）。
type DeviceLog struct {
	ID         database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID   int64             `gorm:"column:device_id" json:"device_id"`
	Level      string            `gorm:"column:level;size:8" json:"level"`
	EventType  string            `gorm:"column:event_type;size:64" json:"event_type"`
	Message    string            `gorm:"column:message" json:"message"`
	Context    jsonb             `gorm:"column:context;type:jsonb" json:"context"`
	OccurredAt database.JSONTime `gorm:"column:occurred_at" json:"occurred_at"`
	CreatedAt  database.JSONTime `gorm:"column:created_at" json:"created_at"`
}

// TableName 显式指定表名。
func (DeviceLog) TableName() string { return "device_logs" }

// AppVersion App 版本（devices.md §7.4 架构预留：M3 建表 + latest 查询，无写入端点——
// plan §15，APK 上传/OTA 随 scan 域立项）。
type AppVersion struct {
	ID               database.ID       `gorm:"primaryKey;autoIncrement" json:"id"`
	Platform         string            `gorm:"column:platform;size:16" json:"platform"`
	VersionCode      int               `gorm:"column:version_code" json:"version_code"`
	VersionName      string            `gorm:"column:version_name;size:32" json:"version_name"`
	ReleaseNotes     string            `gorm:"column:release_notes" json:"release_notes"`
	FileURL          string            `gorm:"column:file_url;size:512" json:"file_url"`
	MinSupportedCode int               `gorm:"column:min_supported_code" json:"min_supported_code"`
	ForceUpdate      bool              `gorm:"column:force_update" json:"force_update"`
	Status           string            `gorm:"column:status;size:16" json:"status"`
	PublishedAt      database.JSONTime `gorm:"column:published_at" json:"published_at"`
	CreatedAt        database.JSONTime `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        database.JSONTime `gorm:"column:updated_at" json:"updated_at"`
	CreatedBy        int64             `gorm:"column:created_by" json:"created_by"`
	UpdatedBy        int64             `gorm:"column:updated_by" json:"updated_by"`
}

// TableName 显式指定表名。
func (AppVersion) TableName() string { return "app_versions" }
