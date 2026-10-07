package asynqx

// asynqx 单测——不依赖 Redis/网络（backend-m3-plan §14：队列行为属集成测试面，
// inline 降级路径与注册表契约在本文件覆盖；asynq 真队列由 //go:build integration 覆盖）。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// wantPanic 断言 fn 触发 panic（注册表防线：清单外/重复/nil 注册为编程错误）。
func wantPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("%s 应触发 panic（编程错误快速失败），实际未触发", name)
		}
	}()
	fn()
}

func TestTaskTypeRegistry(t *testing.T) {
	// M3 冻结全量三类（plan §4.2）；清单外类型零容忍。
	want := map[string]string{
		TaskTypeImportCommit: QueueDatax,
		TaskTypeExportRun:    QueueDatax,
		TaskTypePrintRender:  QueuePrinting,
	}
	if len(taskQueues) != len(want) {
		t.Fatalf("任务类型注册表应含 %d 项（M3 冻结全量），实际 %d", len(want), len(taskQueues))
	}
	for taskType, queue := range want {
		got, ok := QueueFor(taskType)
		if !ok {
			t.Fatalf("注册表缺少任务类型 %s", taskType)
		}
		if got != queue {
			t.Fatalf("任务类型 %s 队列应为 %s，实际 %s", taskType, queue, got)
		}
	}
	if _, ok := QueueFor("datax:unknown"); ok {
		t.Fatal("清单外任务类型不应有队列归属")
	}
}

func TestRegisterHandlerGuards(t *testing.T) {
	wantPanic(t, "清单外任务类型注册", func() {
		RegisterHandler("datax:unknown", func(ctx context.Context, t Task) error { return nil })
	})
	wantPanic(t, "nil handler 注册", func() {
		registerHandler(TaskTypeExportRun, nil, true)
	})
	// 重复注册（严格模式）快速失败；allowReplace 仅测试替身可用。
	registerHandler(TaskTypeExportRun, func(ctx context.Context, t Task) error { return nil }, true)
	wantPanic(t, "重复注册", func() {
		RegisterHandler(TaskTypeExportRun, func(ctx context.Context, t Task) error { return nil })
	})
}

func TestInlineQueueSynchronousExecution(t *testing.T) {
	var got Task
	registerHandler(TaskTypeExportRun, func(ctx context.Context, t Task) error {
		got = t
		return nil
	}, true)

	q := inlineQueue{log: zap.NewNop()}
	err := q.Enqueue(context.Background(), Task{
		Type:    TaskTypeExportRun,
		Payload: []byte(`{"export_no":"EXP-20261003-000001"}`),
		TaskID:  "EXP-20261003-000001",
	})
	if err != nil {
		t.Fatalf("inline 入队应同步执行成功，实际 %v", err)
	}
	if got.Type != TaskTypeExportRun || got.TaskID != "EXP-20261003-000001" {
		t.Fatalf("handler 未收到完整 Task： %+v", got)
	}
	if string(got.Payload) != `{"export_no":"EXP-20261003-000001"}` {
		t.Fatalf("payload 透传失真: %s", got.Payload)
	}
}

func TestInlineQueueHandlerErrorNotPropagated(t *testing.T) {
	// 业务失败仅记日志、不向调用方传播——任务终态由 handler 落任务行（plan §4.1 契约一致）。
	registerHandler(TaskTypeImportCommit, func(ctx context.Context, t Task) error {
		return errors.New("第 3 行编码重复")
	}, true)

	q := inlineQueue{log: zap.NewNop()}
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypeImportCommit}); err != nil {
		t.Fatalf("handler 业务失败不应作为入队错误传播，实际 %v", err)
	}
}

func TestInlineQueueHandlerPanicRecovered(t *testing.T) {
	registerHandler(TaskTypePrintRender, func(ctx context.Context, t Task) error {
		panic("boom")
	}, true)

	q := inlineQueue{log: zap.NewNop()}
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypePrintRender}); err != nil {
		t.Fatalf("handler panic 应被 recover 并记日志，不应向调用方传播，实际 %v", err)
	}
}

// TestFailureFinalizer 失败终态回调（plan §4.2"重试耗尽 → 终态 FAILED + error_message
// + 站内告警"的基座机制）：inline 无重试——handler error 即触发回调；回调失败仅记日志
// 不上抛；未注册类型静默跳过（warn）。asynq 侧的重试耗尽判定（GetRetryCount≥MaxRetry）
// 由 Server mux 包装，集成面覆盖。
func TestFailureFinalizer(t *testing.T) {
	registerHandler(TaskTypeImportCommit, func(ctx context.Context, t Task) error {
		return errors.New("数据库连接中断")
	}, true)

	var fired Task
	var cause error
	registerFinalizerForTest(TaskTypeImportCommit, func(ctx context.Context, t Task, err error) error {
		fired = t
		cause = err
		return nil
	})
	q := inlineQueue{log: zap.NewNop()}
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypeImportCommit, TaskID: "IMP-1"}); err != nil {
		t.Fatalf("inline 入队不应传播 handler 错误，实际 %v", err)
	}
	if fired.TaskID != "IMP-1" {
		t.Fatalf("终态回调未收到任务行号: %+v", fired)
	}
	if cause == nil || cause.Error() != "数据库连接中断" {
		t.Fatalf("终态回调未收到失败原因: %v", cause)
	}

	// 回调自身失败：仅记日志，不向调用方传播（任务行悬挂由日志暴露）。
	registerFinalizerForTest(TaskTypeImportCommit, func(ctx context.Context, t Task, err error) error {
		return errors.New("终态回写失败")
	})
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypeImportCommit}); err != nil {
		t.Fatalf("终态回调失败不应向调用方传播，实际 %v", err)
	}

	// 注册守卫：清单外类型 / nil / 重复注册 panic 快速失败。
	wantPanic(t, "清单外任务类型注册终态回调", func() {
		RegisterFailureFinalizer("datax:unknown", func(ctx context.Context, t Task, err error) error { return nil })
	})
	wantPanic(t, "nil 终态回调注册", func() {
		RegisterFailureFinalizer(TaskTypeExportRun, nil)
	})
	registerFinalizerForTest(TaskTypeExportRun, func(ctx context.Context, t Task, err error) error { return nil })
	wantPanic(t, "重复注册终态回调", func() {
		RegisterFailureFinalizer(TaskTypeExportRun, func(ctx context.Context, t Task, err error) error { return nil })
	})

	// 未注册终态回调：静默跳过（仅 warn），入队语义不变。
	clearFinalizerForTest(TaskTypePrintRender)
	registerHandler(TaskTypePrintRender, func(ctx context.Context, t Task) error {
		return errors.New("x")
	}, true)
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypePrintRender}); err != nil {
		t.Fatalf("未注册终态回调不应报错，实际 %v", err)
	}
}

// registerFinalizerForTest / clearFinalizerForTest 终态回调注册表测试助手
// （包级全局，与 handler 注册表同锁；nil/重复/清单外守卫走 RegisterFailureFinalizer）。
func registerFinalizerForTest(taskType string, fn FailureFinalizer) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	finalizers[taskType] = fn
}

func clearFinalizerForTest(taskType string) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	delete(finalizers, taskType)
}

// clearHandlerForTest 清除指定类型的 handler 注册（注册表为包级全局，
// 守卫用例需要"未注册"场景；依赖顺序：仅可放在该类型最后一次注册之后，本文件无 t.Parallel）。
func clearHandlerForTest(taskType string) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	delete(handlers, taskType)
}

func TestInlineQueueGuards(t *testing.T) {
	q := inlineQueue{log: zap.NewNop()}
	if err := q.Enqueue(context.Background(), Task{Type: "datax:unknown"}); err == nil {
		t.Fatal("清单外任务类型入队应被拒绝")
	}
	// 未注册 handler：inline 无队列缓冲，装配缺位即刻失败（与 asynq 模式差异，见 queue.go 注）。
	clearHandlerForTest(TaskTypePrintRender)
	if err := q.Enqueue(context.Background(), Task{Type: TaskTypePrintRender}); err == nil {
		t.Fatal("未注册 handler 的任务类型 inline 入队应报错")
	}
}

func TestNewRuntimeInlineMode(t *testing.T) {
	rt := NewRuntime(nil, Config{}, nil)
	if !rt.IsInline() {
		t.Fatal("redis=nil 应装配 inline 降级队列（plan §4.1）")
	}
	if rt.Server != nil || rt.Inspector != nil {
		t.Fatal("inline 模式不应创建 asynq Server/Inspector")
	}
	if rt.Queue == nil {
		t.Fatal("inline 模式仍须提供 Queue 接口实现")
	}
	rt.Close() // 幂等，不应 panic
}

func TestNewRuntimeAsynqModeLazy(t *testing.T) {
	// asynq client 为懒连接：构造不触网（连通性由 cache.New 启动 Ping 统一把关），
	// 因此可用不可达地址安全构造（真实 Start 属集成测试面）。
	rt := NewRuntime(&RedisOptions{Addr: "127.0.0.1:1", Password: "", DB: 0}, Config{Concurrency: 3, MaxRetry: 2}, nil)
	if rt.IsInline() {
		t.Fatal("redis 非 nil 应装配 asynq 队列")
	}
	if rt.Server == nil || rt.Inspector == nil {
		t.Fatal("asynq 模式应创建 Server 与 Inspector")
	}
	rt.Close()
	rt.Close() // 幂等
}

func TestConfigNormalize(t *testing.T) {
	got := Config{}.normalize()
	if got.Concurrency != DefaultConcurrency || got.MaxRetry != DefaultMaxRetry {
		t.Fatalf("零值配置应取 plan §3.2 默认（%d/%d），实际 %+v", DefaultConcurrency, DefaultMaxRetry, got)
	}
	if got := (Config{Concurrency: 3, MaxRetry: 2}).normalize(); got.Concurrency != 3 || got.MaxRetry != 2 {
		t.Fatalf("显式配置不应被覆盖，实际 %+v", got)
	}
}

// TestResolveOptionsTimeout 入队超时选项解析：零值（未传 Option）默认 DefaultTimeout
// ——asynq 在未显式设置 Timeout 时静默取库默认 30 分钟，超大导出/导入注定被误杀
// 且每次重试整文件重生成，入队必须显式下发；WithTimeout 显式覆盖；0/负值回退默认。
func TestResolveOptionsTimeout(t *testing.T) {
	o, err := resolveOptions(Task{Type: TaskTypeExportRun}, DefaultMaxRetry, nil)
	if err != nil {
		t.Fatalf("解析入队选项失败: %v", err)
	}
	if o.timeout != DefaultTimeout {
		t.Fatalf("零值应默认 DefaultTimeout=%v，实际 %v", DefaultTimeout, o.timeout)
	}

	o, err = resolveOptions(Task{Type: TaskTypeExportRun}, DefaultMaxRetry, []Option{WithTimeout(15 * time.Minute)})
	if err != nil {
		t.Fatalf("解析入队选项失败: %v", err)
	}
	if o.timeout != 15*time.Minute {
		t.Fatalf("WithTimeout 显式覆盖应生效（15m），实际 %v", o.timeout)
	}

	for name, d := range map[string]time.Duration{"零值": 0, "负值": -time.Minute} {
		o, err = resolveOptions(Task{Type: TaskTypeExportRun}, DefaultMaxRetry, []Option{WithTimeout(d)})
		if err != nil {
			t.Fatalf("%s: 解析入队选项失败: %v", name, err)
		}
		if o.timeout != DefaultTimeout {
			t.Fatalf("%s: 应回退 DefaultTimeout=%v，实际 %v", name, DefaultTimeout, o.timeout)
		}
	}
}

func TestInlineQueueConcurrentEnqueue(t *testing.T) {
	// -race 覆盖：并发入队全部同步执行且互不串扰（plan §14：inline 队列入 -race 面）。
	var executed atomic.Int64
	registerHandler(TaskTypeImportCommit, func(ctx context.Context, t Task) error {
		executed.Add(1)
		return nil
	}, true)

	q := inlineQueue{log: zap.NewNop()}
	const n = 64
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := q.Enqueue(context.Background(), Task{Type: TaskTypeImportCommit, TaskID: fmt.Sprintf("IMP-%d", i)}); err != nil {
				t.Errorf("并发入队失败: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := executed.Load(); got != n {
		t.Fatalf("inline 应同步执行全部任务，实际 %d/%d", got, n)
	}
}
