package sysops

import (
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 任务 handler 装配（backend-m3-plan §4.3：注册表任务的接入点在 sysops 内收口）。
//
// NewScheduler 生产构造时以 allowReplace 语义注册本包实现的执行体（重复构造/单测
// 重建调度器安全）；五任务全部接线执行体（task_timeout_scan 于 2026-10-04 落地——
// 拣货/上架任务行只读扫描在 sysops 内收口，与预警扫描同数据面口径，原"空实现位"
// 偏差注记见 plan §4.3 MT6 注记② 修订）。

// wireJobHandlers 注册本包实现的任务执行体（db 为 nil 跳过——单测不触库）。
func wireJobHandlers(db *gorm.DB, log *zap.Logger) {
	if db == nil {
		return
	}
	if log == nil {
		log = zap.NewNop()
	}
	runner := scanRunner{db: db, log: log, now: time.Now}
	registerJobHandler("inventory_low_stock_scan", runner.lowStockScan, true)
	registerJobHandler("inventory_expiry_scan", runner.expiryScan, true)
	registerJobHandler("inventory_stagnant_scan", runner.stagnantScan, true)
	registerJobHandler("task_timeout_scan", runner.taskTimeoutScan, true)
	registerJobHandler("file_cleanup", runner.watchScan, true)
}

// ---- 进程内调度器引用（管理端启停热更新接入，plan §4.3 单实例语义）----

var schedRef atomic.Pointer[Scheduler]

// attachScheduler 保存进程内调度器实例（jobs API 启停热更新读取；多次创建以最后一次为准——
// 与 main 单实例装配一致，多实例创建属测试场景）。
func attachScheduler(s *Scheduler) { schedRef.Store(s) }

// attachedScheduler 取当前调度器（无则 nil——路由已装配而调度器未创建的窗口）。
func attachedScheduler() *Scheduler { return schedRef.Load() }
