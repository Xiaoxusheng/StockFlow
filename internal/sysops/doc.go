// Package sysops 平台运维面（backend-m3-plan §2.1 Scope S/§4.3/§10）。
//
// 当前交付范围（平台基座工程，按 orchestrator 指令落位本包）：robfig/cron 定时任务
// 框架基座——冻结注册表、handler 注册、防重入、执行日志落 scheduled_jobs/scheduled_job_runs。
// 本包的 API 面（/api/logs /api/system /api/notifications、预警扫描 handler、备份登记）
// 由 MT5（工程师 S）在本包内叠加，注册表五任务的 handler 即其接入点（空实现位）。
//
// 写 SQL 白名单（plan §2.3 判据 3 guard-readonly）：本包写 SQL 仅允许命中自有表
// scheduled_jobs/scheduled_job_runs/system_configs/notifications/backup_records 与
// files 清理通路；审计日志/扫码日志应用账号无 UPDATE/DELETE（grants 红线，plan §15）。
package sysops
