package datax

import (
	"github.com/stockflow/server/internal/asynqx"
)

// asynqx handler 注册（plan §4.2：任务 handler 归属消费包；router/main 装配期调用，
// 须先于 asynq Server.Start 与首次 inline 入队）。
//
// 幂等口径（plan §4.2）：handler 进入即以任务行状态守卫确认执行权——RunImportCommit
// 守卫 EXECUTING、RunExportRun 守卫 QUEUED→PROCESSING / PROCESSING 续跑；重投递
// 0 行返回 nil 静默丢弃；重试断点续跑见各自实现文件。

// RegisterQueueHandlers 注册本包任务类型 handler（重复注册 panic——asynqx 契约，
// 启动期快速失败）。失败终态回调同批注册（plan §4.2"重试耗尽 → 终态 FAILED +
// error_message + 站内告警"——基础设施失败重试耗尽/inline 无重试时任务行落终态）。
func RegisterQueueHandlers(svc *Service) {
	asynqx.RegisterHandler(asynqx.TaskTypeImportCommit, svc.RunImportCommit)
	asynqx.RegisterHandler(asynqx.TaskTypeExportRun, svc.RunExportRun)
	asynqx.RegisterFailureFinalizer(asynqx.TaskTypeImportCommit, svc.FinalizeImportFailure)
	asynqx.RegisterFailureFinalizer(asynqx.TaskTypeExportRun, svc.FinalizeExportFailure)
}
