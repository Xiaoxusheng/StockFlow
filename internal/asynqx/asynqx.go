// Package asynqx asynq 异步任务队列基座（backend-m3-plan §4.1/§4.2，architecture §11.2）。
//
// 职责（平台基建，不含任何业务 handler）：
//   - Queue 接口与双实现：asynqQueue（Redis 队列，生产）/ inlineQueue（Redis 未启用时
//     在请求 goroutine 内同步执行已注册 handler——开发/演示环境无 Redis 可用，plan §4.1）；
//   - 任务类型注册表：M3 冻结全量三类（datax:import:commit / datax:export:run /
//     printing:task:render），类型→队列映射同源冻结（§4.2）；
//   - Handler 注册表：消费包（datax/printing）经 RegisterHandler 注册，asynq Server 与
//     inline 降级共用同一注册表；
//   - asynq Server / Inspector 生命周期封装：由 cmd/server main 启动与优雅关闭（plan §13.5）。
//
// 幂等与重试口径（plan §4.2 冻结，基座只提供机制、业务在 handler 内落实）：
//   - handler 进入即以任务行状态守卫确认执行权（UPDATE ... WHERE status=<前置态>），
//     0 行 = 已被处理，返回 nil 静默丢弃重投递；
//   - 业务失败返回 error → asynq 按队列配置重试（指数退避）→ 重试耗尽进归档队列；
//   - Task.TaskID 为业务任务行号（IMP-/EXP-/PT-），入队时同步设为 asynq task ID：
//     同任务号在途重复入队将被拒绝（第二道防线；第一道是 handler 状态守卫），
//     payload 仍是业务语义的真相源。
//
// 依赖红线（plan §2.3 判据 2/4）：本包仅依赖标准库 + redis + asynq + logger，
// 禁止依赖任何业务/平台包；asynq 库仅允许本包 import（guard-asynq）。
package asynqx

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// 任务类型（M3 冻结全量，backend-m3-plan §4.2；handler 归属消费包，本包不含业务逻辑）。
const (
	// TaskTypeImportCommit 导入确认执行：按行断点分批调用 ImportWriter.Commit（plan §6.2）。
	TaskTypeImportCommit = "datax:import:commit"
	// TaskTypeExportRun 导出执行：Count → 流式 StreamWriter → files 登记（plan §6.3）。
	TaskTypeExportRun = "datax:export:run"
	// TaskTypePrintRender 打印条码 PNG 预生成与进程内缓存（plan §7.2）。
	TaskTypePrintRender = "printing:task:render"
)

// 队列名（M3 冻结：datax/printing 独立并发与优先级，plan §4.1）。
const (
	QueueDatax    = "datax"
	QueuePrinting = "printing"
)

// taskIDHeader 业务任务行号（Task.TaskID）的 asynq 传输头。
// asynq v0.26 的 handler 侧 *asynq.Task 不暴露 msg.ID，经自定义头透传：
// 入队时写入（asynqQueue），mux 分发时读出还原为 Task.TaskID——asynqx 包内私有契约，
// inline 实现直传 Task 结构不经过该头。
const taskIDHeader = "sf.asynq.task_id"

// taskQueues 任务类型 → 默认队列映射（与任务类型注册表同源冻结，plan §4.2）。
var taskQueues = map[string]string{
	TaskTypeImportCommit: QueueDatax,
	TaskTypeExportRun:    QueueDatax,
	TaskTypePrintRender:  QueuePrinting,
}

// QueueFor 返回任务类型的冻结队列归属（未注册类型返回 false——入队前校验，防拼写漂移）。
func QueueFor(taskType string) (string, bool) {
	q, ok := taskQueues[taskType]
	return q, ok
}

// Task 入队任务（plan §4.1 冻结结构）。
type Task struct {
	// Type 任务类型（注册表值，§4.2）。
	Type string
	// Payload 消费包序列化的 payload（JSON）——业务语义的真相源。
	Payload []byte
	// TaskID 业务任务行号（IMP-/EXP-/PT-），handler 用于状态守卫与进度；
	// 非空时同步设为 asynq task ID（同任务号在途重复入队被拒）。
	TaskID string
}

// HandlerFunc 任务处理器：由消费包实现并经 RegisterHandler 注册。
// 幂等要求见包注（进入即任务行状态守卫，重投递 0 行返回 nil）。
type HandlerFunc func(ctx context.Context, t Task) error

// FailureFinalizer 失败终态回调（backend-m3-plan §4.2："重试耗尽 → 终态 FAILED +
// error_message + 站内告警"）：handler 返回 error 且本次失败后不会再重试时由基座调用——
//   - asynq 模式：本次尝试的已重试次数 ≥ 任务 MaxRetry（下次失败不再重投，任务将归档）；
//   - inline 模式：任何 handler error（同步执行无重试，本次失败即最终失败）。
//
// 消费包在回调内落业务任务行终态（FAILED + error_message）并发站内告警；回调自身的
// 失败由基座记错误日志（任务行悬挂由日志暴露，不向调用方传播）。
type FailureFinalizer func(ctx context.Context, t Task, cause error) error

var (
	handlerMu  sync.RWMutex
	handlers   = map[string]HandlerFunc{}
	finalizers = map[string]FailureFinalizer{}
)

// RegisterHandler 注册任务 handler（消费包在 init 或装配期调用，须先于 Server.Start）。
// 未注册类型的 handler、重复注册、nil handler 均视为编程错误，启动期直接 panic 快速失败
// （与 internal/response.Register 同款防线）。
func RegisterHandler(taskType string, fn HandlerFunc) {
	registerHandler(taskType, fn, false)
}

// registerHandler 注册实现（allowReplace 仅供测试替身注入）。
func registerHandler(taskType string, fn HandlerFunc, allowReplace bool) {
	if _, ok := taskQueues[taskType]; !ok {
		panic(fmt.Sprintf("asynqx.RegisterHandler: 任务类型 %q 不在冻结注册表（plan §4.2），禁止注册清单外类型", taskType))
	}
	if fn == nil {
		panic(fmt.Sprintf("asynqx.RegisterHandler: %q 的 handler 不能为 nil", taskType))
	}
	handlerMu.Lock()
	defer handlerMu.Unlock()
	if _, dup := handlers[taskType]; dup && !allowReplace {
		panic(fmt.Sprintf("asynqx.RegisterHandler: 任务类型 %q 重复注册", taskType))
	}
	handlers[taskType] = fn
}

// handlerFor 读取已注册 handler（快照读取，队列实现内调用）。
func handlerFor(taskType string) (HandlerFunc, bool) {
	handlerMu.RLock()
	defer handlerMu.RUnlock()
	fn, ok := handlers[taskType]
	return fn, ok
}

// RegisterFailureFinalizer 注册任务失败终态回调（可选；重复注册 panic 快速失败，
// 与 RegisterHandler 同款防线——装配期调用，须先于 Server.Start 与首次 inline 入队）。
func RegisterFailureFinalizer(taskType string, fn FailureFinalizer) {
	if _, ok := taskQueues[taskType]; !ok {
		panic(fmt.Sprintf("asynqx.RegisterFailureFinalizer: 任务类型 %q 不在冻结注册表（plan §4.2），禁止注册清单外类型", taskType))
	}
	if fn == nil {
		panic(fmt.Sprintf("asynqx.RegisterFailureFinalizer: %q 的失败终态回调不能为 nil", taskType))
	}
	handlerMu.Lock()
	defer handlerMu.Unlock()
	if _, dup := finalizers[taskType]; dup {
		panic(fmt.Sprintf("asynqx.RegisterFailureFinalizer: 任务类型 %q 重复注册失败终态回调", taskType))
	}
	finalizers[taskType] = fn
}

// failureFinalizerFor 读取已注册失败终态回调（快照读取）。
func failureFinalizerFor(taskType string) (FailureFinalizer, bool) {
	handlerMu.RLock()
	defer handlerMu.RUnlock()
	fn, ok := finalizers[taskType]
	return fn, ok
}

// finalizeFailure 触发失败终态回调（未注册类型静默跳过——回调为可选契约；
// 回调失败仅记日志，不改变 handler 原错误的上抛语义）。
func finalizeFailure(log *zap.Logger, ctx context.Context, t Task, cause error) {
	fn, ok := failureFinalizerFor(t.Type)
	if !ok || fn == nil {
		if log != nil {
			log.Warn("asynqx: 任务执行失败且无重试机会，但未注册失败终态回调（任务行可能悬挂）",
				zap.String("type", t.Type), zap.String("task_id", t.TaskID), zap.Error(cause))
		}
		return
	}
	if err := fn(ctx, t, cause); err != nil && log != nil {
		log.Error("asynqx: 失败终态回调执行失败（任务行可能悬挂）",
			zap.String("type", t.Type), zap.String("task_id", t.TaskID), zap.Error(err))
	}
}

// registeredTypes 返回已注册任务类型清单（Server 构建 mux 用快照）。
func registeredTypes() []string {
	handlerMu.RLock()
	defer handlerMu.RUnlock()
	types := make([]string, 0, len(handlers))
	for t := range handlers {
		types = append(types, t)
	}
	return types
}
