// 定时任务冻结注册表（backend-m3-plan §4.3，architecture §9）。
//
// M3 应用内注册表为五任务；两类任务有意不进本注册表（plan §4.3"不进本注册表的两类任务"）：
//   - log_cleanup（审计日志/扫码日志清理）：grants 审计红线，应用进程不持审计表删除权，
//     清理由部署侧维护脚本以独立维护角色执行；
//   - backup_daily（数据库备份）：Orchestrator 裁决②混合模式——pg_dump 由部署侧执行器
//     承担，应用侧只做 backup_records 登记/状态核对（核对并入 file_cleanup）。
package sysops

import (
	"context"
	"fmt"
	"sync"
)

// JobDef 定时任务定义（代码注册表为缺省值源，DB 行 enabled 为启停覆盖——plan §4.3）。
type JobDef struct {
	// Code 任务编码（scheduled_jobs.code，冻结值）。
	Code string
	// Name 任务名称（管理端展示）。
	Name string
	// CronExpr 默认 cron 表达式（5 字段标准语法；M3 冻结在代码，运行时不开放修改——plan §10.3）。
	CronExpr string
	// Remark 职责说明（upsert 到 scheduled_jobs.remark）。
	Remark string
}

// M3 冻结五任务（plan §4.3 注册表；handler 归属：预警扫描/超时检测/文件清理由 MT5 与
// datax 域经 RegisterJobHandler 注册——空实现位）。
var frozenJobs = []JobDef{
	{
		Code:     "inventory_low_stock_scan",
		Name:     "库存预警扫描",
		CronExpr: "*/10 * * * *",
		Remark:   "available ≤ safety_stock 行 → 站内通知（dedup_key=日+SKU+仓+收件人）",
	},
	{
		Code:     "inventory_expiry_scan",
		Name:     "效期预警扫描",
		CronExpr: "0 6 * * *",
		Remark:   "临期 30/15/7/3 天 + 已过期（阈值 system_configs）→ 站内通知（dedup_key=日+批次+级别+收件人）",
	},
	{
		Code:     "inventory_stagnant_scan",
		Name:     "长期库存（积压）扫描",
		CronExpr: "0 6 * * *",
		Remark:   "30/60/90 天未动 → 站内通知 + 供 /api/reports 复核（dedup_key=日+SKU+仓+档位+收件人）",
	},
	{
		Code:     "task_timeout_scan",
		Name:     "作业任务超时检测",
		CronExpr: "0 * * * *",
		Remark:   "拣货/上架任务超时（创建时间 + 阈值）→ 站内通知（dedup_key=任务号+收件人）",
	},
	{
		Code:     "file_cleanup",
		Name:     "过期文件清理",
		CronExpr: "30 3 * * *",
		Remark:   "files.expires_at 已过期 → 标记删除 + 物理删除；备份文件超期清理；backup_records REQUESTED/RUNNING 超时（30 分钟）→ 标记 FAILED + 告警",
	},
}

// jobByCode 按编码取冻结任务定义。
func jobByCode(code string) (JobDef, bool) {
	for _, j := range frozenJobs {
		if j.Code == code {
			return j, true
		}
	}
	return JobDef{}, false
}

// JobFunc 任务执行体：由各域实现并经 RegisterJobHandler 注册（空实现位接入点）。
// handler 自行管理业务事务与通知写入（dedup_key 幂等），框架不重试同周期失败
// （失败记 FAILED + 站内告警，下一周期自然重试——plan §4.3）。
type JobFunc func(ctx context.Context) error

var (
	handlerMu sync.RWMutex
	handlers  = map[string]JobFunc{}
)

// RegisterJobHandler 注册任务执行体（须先于 Scheduler.Start——装配期或 init 调用；
// 未注册 handler 的任务不调度，启动日志明确记录）。清单外编码、重复注册、nil 函数
// 均视为编程错误，启动期 panic 快速失败（与 internal/response.Register 同款防线）。
func RegisterJobHandler(code string, fn JobFunc) {
	registerJobHandler(code, fn, false)
}

// registerJobHandler 注册实现（allowReplace 仅供测试替身注入）。
func registerJobHandler(code string, fn JobFunc, allowReplace bool) {
	if _, ok := jobByCode(code); !ok {
		panic(fmt.Sprintf("sysops.RegisterJobHandler: 任务编码 %q 不在冻结注册表（plan §4.3），禁止注册清单外任务", code))
	}
	if fn == nil {
		panic(fmt.Sprintf("sysops.RegisterJobHandler: %q 的执行体不能为 nil", code))
	}
	handlerMu.Lock()
	defer handlerMu.Unlock()
	if _, dup := handlers[code]; dup && !allowReplace {
		panic(fmt.Sprintf("sysops.RegisterJobHandler: 任务编码 %q 重复注册", code))
	}
	handlers[code] = fn
}

// jobHandlerFor 读取已注册执行体。
func jobHandlerFor(code string) (JobFunc, bool) {
	handlerMu.RLock()
	defer handlerMu.RUnlock()
	fn, ok := handlers[code]
	return fn, ok
}
