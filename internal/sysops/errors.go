package sysops

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// 平台运维域错误码（backend-m3-plan §1 错误码清单 SYSTEM_* 段；经 internal/response 注册，
// 形态对齐 M2 域错误码：注册 Code + 包级 *Error 单例——本包不对单例调用 WithMessage/Details）。
var (
	// codeConfigReadonly 只读配置项拒绝写入（plan §10.2）。
	codeConfigReadonly = response.Register("SYSTEM_CONFIG_READONLY", "该配置项为只读，不可修改", http.StatusBadRequest)
	// ErrConfigReadonly 只读配置项写入错误。
	ErrConfigReadonly = response.NewError(codeConfigReadonly, nil)

	codeConfigNotFound = response.Register("SYSTEM_CONFIG_NOT_FOUND", "配置项不存在", http.StatusNotFound)
	// ErrConfigNotFound 配置键不存在（fail-closed：禁止经 API 发明清单外配置键）。
	ErrConfigNotFound = response.NewError(codeConfigNotFound, nil)

	codeConfigEmpty = response.Register("SYSTEM_CONFIG_EMPTY", "未提交任何配置项", http.StatusBadRequest)
	// ErrConfigEmpty 批量保存提交为空。
	ErrConfigEmpty = response.NewError(codeConfigEmpty, nil)

	codeJobNotFound = response.Register("SYSTEM_JOB_NOT_FOUND", "定时任务不存在", http.StatusNotFound)
	// ErrJobNotFound 任务编码/ID 不在注册表。
	ErrJobNotFound = response.NewError(codeJobNotFound, nil)

	codeJobNotScheduled = response.Register("SYSTEM_JOB_NOT_SCHEDULED", "该任务未装配执行体，暂不可启停热更新", http.StatusConflict)
	// ErrJobNotScheduled 任务存在但调度器未装配（handler 未注册空实现位/未启动）。
	ErrJobNotScheduled = response.NewError(codeJobNotScheduled, nil)

	codeBackupInflight = response.Register("SYSTEM_BACKUP_INFLIGHT", "已有备份任务在途，请等待完成后再触发", http.StatusConflict)
	// ErrBackupInflight 已有在途备份记录（REQUESTED/RUNNING 唯一索引，plan §10.5）。
	ErrBackupInflight = response.NewError(codeBackupInflight, nil)

	codeBackupNotFound = response.Register("SYSTEM_BACKUP_NOT_FOUND", "备份记录不存在", http.StatusNotFound)
	// ErrBackupNotFound 备份记录不存在。
	ErrBackupNotFound = response.NewError(codeBackupNotFound, nil)

	codeBackupNotDownloadable = response.Register("SYSTEM_BACKUP_NOT_DOWNLOADABLE", "备份文件不可下载（记录未完成或无文件）", http.StatusConflict)
	// ErrBackupNotDownloadable 记录未到 SUCCESS 或无文件。
	ErrBackupNotDownloadable = response.NewError(codeBackupNotDownloadable, nil)

	codeBackupFileMissing = response.Register("SYSTEM_BACKUP_FILE_MISSING", "备份文件不存在，请联系管理员核对备份执行器", http.StatusNotFound)
	// ErrBackupFileMissing 备份文件物理缺失。
	ErrBackupFileMissing = response.NewError(codeBackupFileMissing, nil)

	codeStorageRootMissing = response.Register("SYSTEM_STORAGE_ROOT_NOT_CONFIGURED", "存储根目录未配置，无法提供备份文件下载", http.StatusServiceUnavailable)
	// ErrStorageRootNotConfigured 存储根未配置（router 装配未注入 WithStorageRoot）。
	ErrStorageRootNotConfigured = response.NewError(codeStorageRootMissing, nil)

	codeBackupPathInvalid = response.Register("SYSTEM_BACKUP_PATH_INVALID", "备份文件路径非法", http.StatusBadRequest)
	// ErrBackupPathInvalid 备份相对路径越界（防路径穿越，api.md §5）。
	ErrBackupPathInvalid = response.NewError(codeBackupPathInvalid, nil)

	codeLogNotFound = response.Register("SYSTEM_LOG_NOT_FOUND", "日志记录不存在", http.StatusNotFound)
	// ErrLogNotFound 审计日志行不存在。
	ErrLogNotFound = response.NewError(codeLogNotFound, nil)

	codeNotificationNotFound = response.Register("SYSTEM_NOTIFICATION_NOT_FOUND", "通知不存在", http.StatusNotFound)
	// ErrNotificationNotFound 通知不存在或不属于当前用户。
	ErrNotificationNotFound = response.NewError(codeNotificationNotFound, nil)
)
