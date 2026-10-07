// Queue 接口与双实现（backend-m3-plan §4.1 冻结契约）。
//
//   - asynqQueue：Redis 队列（生产形态）。入队即投递，执行由 asynq Server 异步完成，
//     MaxRetry 取 queue.max_retry（指数退避为 asynq 内建默认退避策略）；Timeout 显式
//     下发 DefaultTimeout=2h（防 asynq 库默认 30 分钟静默生效，见 DefaultTimeout 注）。
//   - inlineQueue：Redis 未启用（redis.enabled=false）时的同步降级——Enqueue 即在请求
//     goroutine 内执行已注册 handler，任务行照常落库、进度照常更新（plan §4.1），
//     保证无 Redis 开发环境与场景 5/6 演示可用。执行失败仅记错误日志、不向调用方传播
//     （业务终态经 FailureFinalizer 落任务行：业务失败 handler 已自落 FAILED；
//     基础设施失败由终态回调落 FAILED + 告警——inline 无重试，本次失败即最终失败，
//     plan §4.2"重试耗尽终态"的 inline 适配），入队契约语义保持一致。
package asynqx

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// Queue 异步任务队列接口（plan §4.1 冻结签名；业务包一律以本接口持有，禁止 import asynq）。
type Queue interface {
	Enqueue(ctx context.Context, t Task, opts ...Option) error
}

// Config 队列配置（来源 config.QueueConfig：queue.concurrency / queue.max_retry）。
type Config struct {
	// Concurrency asynq Server worker 并发数（0 取 DefaultConcurrency）。
	Concurrency int
	// MaxRetry 入队默认最大重试次数（0 取 DefaultMaxRetry；显式 1 表示不重试需经 WithMaxRetry(1)）。
	MaxRetry int
}

// plan §3.2 冻结默认值。
const (
	DefaultConcurrency = 10
	DefaultMaxRetry    = 3
)

// DefaultTimeout 任务执行超时缺省（Enqueue 显式下发 asynq.Timeout）。asynq 库在
// 入队未显式设置 Timeout 时静默取内建默认 30 分钟（asynq client.go defaultTimeout）
// ——超大导出/导入注定被误杀：datax 单导出产物上限 ExportMaxFileBytes=200MB
// （internal/datax，全量查询 + 流式写 Excel/CSV + 落盘耗时可远超 30 分钟），且
// 超时即整文件重生成（每次重试重复全量 DB/IO，浪费倍增）。取 2h 覆盖该量级；
// 同时是幂等租约 TTL 的对齐基准——internal/idempotency defaultLeaseTTL=3h 必须大
// 于本值，在途长任务执行期内其幂等占用权才不会被同键重试误接管（恰一执行）。
const DefaultTimeout = 2 * time.Hour

// normalize 补零值默认（plan §3.2：queue.concurrency=10、queue.max_retry=3）。
func (c Config) normalize() Config {
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}
	if c.MaxRetry <= 0 {
		c.MaxRetry = DefaultMaxRetry
	}
	return c
}

// RedisOptions asynq 所需的 Redis 连接参数（cmd/server main 从 config.RedisConfig 映射；
// 本包禁止 import internal/config——plan §2.3 判据 2 依赖红线）。
type RedisOptions struct {
	Addr     string
	Password string
	DB       int
}

func (o RedisOptions) asynqOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: o.Addr, Password: o.Password, DB: o.DB}
}

// enqueueOptions 入队选项解析结果。
type enqueueOptions struct {
	queue    string
	maxRetry int
	// timeout 单次执行超时（asynq.Timeout 显式下发；0/负值在 resolve 时回退
	// DefaultTimeout——不显式下发会静默吃到 asynq 库默认 30 分钟）。
	timeout time.Duration
}

// Option 入队可选项（队列名覆盖 / 重试次数覆盖 / 执行超时覆盖）。
type Option func(*enqueueOptions)

// WithQueue 覆盖任务类型的默认队列（冻结映射之外的队列须显式声明，默认禁止漂移）。
func WithQueue(name string) Option {
	return func(o *enqueueOptions) { o.queue = name }
}

// WithMaxRetry 覆盖本次入队的最大重试次数。
func WithMaxRetry(n int) Option {
	return func(o *enqueueOptions) { o.maxRetry = n }
}

// WithTimeout 覆盖本次入队的任务执行超时（0/负值在 resolve 时回退 DefaultTimeout）。
func WithTimeout(d time.Duration) Option {
	return func(o *enqueueOptions) { o.timeout = d }
}

// resolveOptions 合成入队选项：默认队列取任务类型冻结映射，默认重试取队列配置，
// 默认超时取 DefaultTimeout（零值/负值回退，不透传给 asynq 的 30 分钟库默认）。
func resolveOptions(t Task, cfgMaxRetry int, opts []Option) (enqueueOptions, error) {
	o := enqueueOptions{queue: "", maxRetry: cfgMaxRetry}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.queue == "" {
		q, ok := QueueFor(t.Type)
		if !ok {
			return o, fmt.Errorf("asynqx: 任务类型 %q 不在冻结注册表，禁止入队", t.Type)
		}
		o.queue = q
	}
	if o.timeout <= 0 {
		o.timeout = DefaultTimeout
	}
	return o, nil
}

// NewRuntime 按 Redis 配置装配队列运行时（cmd/server main 唯一装配点）：
// redis 为 nil（redis.enabled=false）→ inline 同步降级（Server/Inspector 为 nil）；
// 否则 → asynq 队列 + Server + Inspector（client/inspector 连接由 Close 释放）。
// 构造不建立网络连接（asynq client 为懒连接），连通性由 cache.New 启动 Ping 统一把关。
func NewRuntime(redis *RedisOptions, cfg Config, log *zap.Logger) *Runtime {
	cfg = cfg.normalize()
	if log == nil {
		log = zap.NewNop()
	}
	rt := &Runtime{cfg: cfg, log: log}
	if redis == nil {
		rt.Queue = inlineQueue{log: log}
		return rt
	}
	client := asynq.NewClient(redis.asynqOpt())
	rt.Queue = &asynqQueue{client: client, maxRetry: cfg.MaxRetry}
	rt.Server = &Server{srv: asynq.NewServer(redis.asynqOpt(), asynq.Config{
		Concurrency: cfg.Concurrency,
		// 队列优先级权重（plan §4.1：datax/printing 独立并发与优先级——导入导出批量任务
		// 为主要吞吐，打印渲染为低延迟辅助；asynq 按权重轮转出队）。
		Queues: map[string]int{QueueDatax: 10, QueuePrinting: 5},
	}), log: log}
	rt.Inspector = &Inspector{insp: asynq.NewInspector(redis.asynqOpt())}
	return rt
}

// Runtime 队列运行时集合：Queue 注入业务包（以 Queue 接口持有），Server/Inspector
// 由 main 启动与关闭；inline 模式下 Server/Inspector 为 nil。
type Runtime struct {
	Queue     Queue
	Server    *Server
	Inspector *Inspector

	cfg Config
	log *zap.Logger
}

// IsInline 是否为 inline 同步降级模式（Redis 未启用）。
func (r *Runtime) IsInline() bool {
	_, inline := r.Queue.(inlineQueue)
	return inline
}

// Close 释放 asynq client/inspector 连接（幂等；inline 模式无连接资源）。
func (r *Runtime) Close() {
	if r.Inspector != nil {
		_ = r.Inspector.Close()
	}
	if q, ok := r.Queue.(*asynqQueue); ok {
		_ = q.client.Close()
	}
}

// asynqQueue Redis 队列实现（asynq 库唯一封装点，plan §2.3 判据 4）。
type asynqQueue struct {
	client   *asynq.Client
	maxRetry int
}

func (q *asynqQueue) Enqueue(ctx context.Context, t Task, opts ...Option) error {
	if _, ok := QueueFor(t.Type); !ok {
		return fmt.Errorf("asynqx: 任务类型 %q 不在冻结注册表，禁止入队", t.Type)
	}
	o, err := resolveOptions(t, q.maxRetry, opts)
	if err != nil {
		return err
	}
	// Timeout 显式下发（resolve 已回退零值）：防 asynq 库默认 30 分钟静默生效——
	// 超大导出/导入会被误杀，且每次重试整文件重生成（量级对齐见 DefaultTimeout 注）。
	at := []asynq.Option{asynq.Queue(o.queue), asynq.MaxRetry(o.maxRetry), asynq.Timeout(o.timeout)}
	var task *asynq.Task
	if t.TaskID != "" {
		// 业务任务行号经传输头透传给 handler（asynq v0.26 handler 侧不暴露 msg.ID），
		// 同时设为 asynq task ID：同任务号在途重复入队返回冲突错误
		// （幂等第二道防线；第一道为 handler 的任务行状态守卫——plan §4.2）。
		at = append(at, asynq.TaskID(t.TaskID))
		task = asynq.NewTaskWithHeaders(t.Type, t.Payload, map[string]string{taskIDHeader: t.TaskID})
	} else {
		task = asynq.NewTask(t.Type, t.Payload)
	}
	if _, err := q.client.EnqueueContext(ctx, task, at...); err != nil {
		return fmt.Errorf("asynqx: 入队失败（type=%s task_id=%s queue=%s）: %w", t.Type, t.TaskID, o.queue, err)
	}
	return nil
}

// inlineQueue 同步降级实现：Enqueue 即执行（plan §4.1）。
type inlineQueue struct {
	log *zap.Logger
}

func (q inlineQueue) Enqueue(ctx context.Context, t Task, opts ...Option) error {
	if _, ok := QueueFor(t.Type); !ok {
		return fmt.Errorf("asynqx: 任务类型 %q 不在冻结注册表，禁止入队", t.Type)
	}
	fn, ok := handlerFor(t.Type)
	if !ok {
		// 与 asynq 模式的差异：inline 无队列缓冲，未注册 handler 即刻失败（装配缺位暴露为编程错误）。
		return fmt.Errorf("asynqx: 任务类型 %q 未注册 handler（inline 模式要求先注册）", t.Type)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// 同步执行；业务失败仅记错误日志、不向调用方传播——业务终态由 handler 落任务行
	// （FAILED + error_message）；基础设施失败（handler 未落终态即返回 error）经
	// FailureFinalizer 落终态——inline 无重试，本次失败即最终失败（plan §4.2 终态契约，
	// 防任务行永久悬挂在 EXECUTING/PROCESSING）。
	err := execHandler(ctx, fn, t)
	if err != nil {
		finalizeFailure(q.log, ctx, t, err)
		if q.log != nil {
			q.log.Error("asynqx inline 执行失败（终态经 FailureFinalizer 落任务行）",
				zap.String("type", t.Type), zap.String("task_id", t.TaskID), zap.Error(err))
		}
	}
	return nil
}

// execHandler 执行 handler 并兜底 recover（inline 无 asynq 的 panic 隔离，须防请求 goroutine 崩溃）。
func execHandler(ctx context.Context, fn HandlerFunc, t Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return fn(ctx, t)
}
