// asynq Server 与 Inspector 生命周期封装（backend-m3-plan §4.1/§13.5）。
//
// 生命周期由 cmd/server main 持有：Start（非阻塞启动 worker）→ 业务运行 → Shutdown
// （优雅退出，等待在途任务完成）。go-dev-standard 规则 3：goroutine 有主——main 启动、
// main 停止、错误经返回值上抛。未注册 handler 的任务类型不进 mux（装配缺位不静默）。
package asynqx

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// Server asynq worker 服务器封装（仅 Redis 模式创建；inline 模式下 Runtime.Server 为 nil）。
type Server struct {
	srv *asynq.Server
	log *zap.Logger
}

// Start 非阻塞启动 worker：按已注册 handler 构建 mux 并开始消费 datax/printing 队列。
// Redis 不可达等启动失败立即返回错误（main fail-fast，deployment §3 禁止带病启动）。
func (s *Server) Start() error {
	mux := asynq.NewServeMux()
	for _, taskType := range registeredTypes() {
		fn, ok := handlerFor(taskType)
		if !ok {
			continue // registeredTypes 与 handlerFor 同锁快照，理论上不发生；防御性跳过
		}
		fn, taskType := fn, taskType
		mux.HandleFunc(taskType, func(ctx context.Context, task *asynq.Task) error {
			t := Task{
				Type:    taskType,
				Payload: task.Payload(),
				TaskID:  task.Headers()[taskIDHeader],
			}
			err := fn(ctx, t)
			if err != nil {
				// 重试耗尽判定（plan §4.2：重试耗尽 → 终态 FAILED + error_message + 告警）：
				// 本次失败的已重试次数 ≥ 任务 MaxRetry → asynq 归档后不再重投，
				// 业务任务行须由失败终态回调落终态，否则永久悬挂在非终态。
				n, _ := asynq.GetRetryCount(ctx)
				max, maxOK := asynq.GetMaxRetry(ctx)
				if maxOK && n >= max {
					finalizeFailure(s.log, ctx, t, err)
				}
			}
			return err
		})
	}
	if err := s.srv.Start(mux); err != nil {
		return fmt.Errorf("asynqx: asynq server 启动失败: %w", err)
	}
	return nil
}

// Shutdown 优雅停机：停止接新任务并等待在途任务完成（main 在 HTTP Shutdown 之后调用）。
func (s *Server) Shutdown() {
	s.srv.Shutdown()
}

// QueueStat 队列积压统计（/api/system/monitor queued_tasks 数据源，deployment §5）。
type QueueStat struct {
	Queue     string
	Pending   int // 待处理
	Active    int // 执行中
	Scheduled int // 计划延迟中
	Retry     int // 待重试
}

// Inspector asynq 队列统计封装（plan §4.1：Inspector 提供 datax/printing 队列
// pending/active 统计；任务进度真相源是任务表，本统计仅作平台监控面）。
type Inspector struct {
	insp *asynq.Inspector
}

// QueueStats 逐队列拉取积压统计；任一队列查询失败即返回错误（调用方决定降级展示）。
func (i *Inspector) QueueStats(ctx context.Context, queues ...string) ([]QueueStat, error) {
	if len(queues) == 0 {
		queues = []string{QueueDatax, QueuePrinting}
	}
	stats := make([]QueueStat, 0, len(queues))
	for _, q := range queues {
		info, err := i.insp.GetQueueInfo(q)
		if err != nil {
			return nil, fmt.Errorf("asynqx: 查询队列 %s 统计失败: %w", q, err)
		}
		stats = append(stats, QueueStat{
			Queue:     q,
			Pending:   info.Pending,
			Active:    info.Active,
			Scheduled: info.Scheduled,
			Retry:     info.Retry,
		})
	}
	return stats, nil
}

// Close 释放 Inspector 连接。
func (i *Inspector) Close() error {
	return i.insp.Close()
}
