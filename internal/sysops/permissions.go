package sysops

import "github.com/stockflow/server/internal/auth"

// 平台运维域权限点常量（backend-m3-plan §11.1：system:log/system:job/system:config/
// system:monitor/system:backup 资源；动作词沿用冻结枚举 list/read/status/update/create，
// 清单外动作词禁止发明——plan §2.3 判据 9）。
//
// 收编落位（MT6，plan §11）：字符串值唯一来源已并入 internal/auth/permissions.go
// （M3 段，seed 种子同源）——本文件保留同名包内别名（sales/permissions.go 收敛先例），
// 字符串值不变、路由挂载零改动。
// notifications 个人收件箱为认证即可用（无权限点，个人数据自见原则，plan §10.6）。
const (
	// PermSystemLogList 操作/登录日志查询（audit 只读）。
	PermSystemLogList = auth.PermSystemLogList
	// PermSystemLogRead 日志详情（三快照 jsonb 完整内容）。
	PermSystemLogRead = auth.PermSystemLogRead

	// PermSystemJobList 定时任务列表/执行日志。
	PermSystemJobList = auth.PermSystemJobList
	// PermSystemJobRead 任务执行日志读取。
	PermSystemJobRead = auth.PermSystemJobRead
	// PermSystemJobStatus 任务启停（热更新）。
	PermSystemJobStatus = auth.PermSystemJobStatus

	// PermSystemConfigList 系统配置读取。
	PermSystemConfigList = auth.PermSystemConfigList
	// PermSystemConfigUpdate 系统配置修改（敏感操作，逐项审计——permission §6）。
	PermSystemConfigUpdate = auth.PermSystemConfigUpdate

	// PermSystemMonitorList 系统监控（只读平台端点）。
	PermSystemMonitorList = auth.PermSystemMonitorList

	// PermSystemBackupList 备份记录列表。
	PermSystemBackupList = auth.PermSystemBackupList
	// PermSystemBackupRead 备份下载/命令模板（触发=登记 REQUESTED 记录，裁决②混合模式）。
	PermSystemBackupRead = auth.PermSystemBackupRead
	// PermSystemBackupCreate 手动触发备份登记。
	PermSystemBackupCreate = auth.PermSystemBackupCreate
)
