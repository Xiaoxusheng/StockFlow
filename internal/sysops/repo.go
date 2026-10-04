package sysops

import "gorm.io/gorm"

// runtimeRepo 平台运维只读/自有表写数据访问（Handler→Service→Repository 分层，
// architecture §1：handler 禁止直连数据库）。
//
// 写通路白名单（backend-m3-plan §2.3 判据 3 guard-readonly）：scheduled_jobs /
// scheduled_job_runs（store.go）/ system_configs / notifications / backup_records +
// files 清理通路（watch.go）；operation_logs/login_logs/scan_logs 等审计表零写
// （database §7.2 审计红线，应用账号仅 SELECT+INSERT）。
type runtimeRepo struct {
	db *gorm.DB
}

func newRuntimeRepo(db *gorm.DB) *runtimeRepo { return &runtimeRepo{db: db} }
