package sysops

import (
	"context"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 系统监控（GET /api/system/monitor，backend-m3-plan §10.4、deployment §5）。
//
// 指标矩阵（Orchestrator 裁决①：CPU/内存/磁盘系统资源指标豁免至阶段 21，gopsutil 不引入；
// 磁盘容量告警由部署侧宿主机监控承担）：数据库连接（sql.DBStats + Ping 健康）与 Redis 连通、
// API 请求/错误率与 24h 趋势（进程内分钟级环形采样器，router 挂 MetricsMiddleware，
// 重启清零——局限在响应 remarks 注明，不做持久化指标表）、队列任务（asynqx Inspector 统计，
// router 注入 QueueStatsReader，缺省 omit）、定时任务（scheduled_jobs 最近状态）、
// 版本与运行时长。

// metricsMinute 指标采样粒度（分钟级环形）。
const metricsMinute = time.Minute

// metricsHorizon 趋势窗口（24h，plan §10.4）。
const metricsHorizon = 24 * time.Hour

// ringSampler 进程内分钟级环形计数器（请求/错误计数；并发安全，-race 覆盖）。
type ringSampler struct {
	mu      sync.Mutex
	buckets []bucket // 环形缓冲，容量 = horizon/minute + 1
	started time.Time
	window  time.Duration
	granule time.Duration
}

type bucket struct {
	minute   int64 // unix 分钟序号
	requests int64
	errors   int64
}

func newRingSampler(window, granule time.Duration) *ringSampler {
	cap0 := int(window/granule) + 2
	return &ringSampler{
		buckets: make([]bucket, cap0),
		started: time.Now(),
		window:  window,
		granule: granule,
	}
}

// add 请求计数（error = HTTP 5xx 服务端错误；4xx 为客户端业务失败，不计服务错误率）。
func (s *ringSampler) add(err bool) {
	now := time.Now()
	minute := now.Unix() / int64(s.granule/time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(minute)
	idx := int(minute % int64(len(s.buckets)))
	if s.buckets[idx].minute != minute {
		s.buckets[idx] = bucket{minute: minute}
	}
	s.buckets[idx].requests++
	if err {
		s.buckets[idx].errors++
	}
}

// evictLocked 清理窗口外桶（幂等——同分钟重复请求只清一次）。
func (s *ringSampler) evictLocked(currentMinute int64) {
	for i := range s.buckets {
		if s.buckets[i].minute != 0 && currentMinute-s.buckets[i].minute > int64(s.window/s.granule) {
			s.buckets[i] = bucket{}
		}
	}
}

// snapshot 汇总窗口内（window 内分钟桶）请求/错误计数。
func (s *ringSampler) snapshot(window time.Duration) (requests, errors int64) {
	minute := time.Now().Unix() / int64(s.granule/time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(minute)
	maxAge := int64(window / s.granule)
	for i := range s.buckets {
		b := s.buckets[i]
		if b.minute != 0 && minute-b.minute < maxAge {
			requests += b.requests
			errors += b.errors
		}
	}
	return requests, errors
}

// trend 24h 趋势点（按 bucketMinutes 聚桶降采样，减少 1440 点负载）。
func (s *ringSampler) trend(bucketMinutes int) []trendPoint {
	if bucketMinutes <= 0 {
		bucketMinutes = 5
	}
	nowMinute := time.Now().Unix() / 60
	span := int64(metricsHorizon / metricsMinute)
	s.mu.Lock()
	defer s.mu.Unlock()
	// 旧→新遍历窗口内每桶，按 bucketMinutes 归并（对齐分钟边界）。
	points := make([]trendPoint, 0, span/int64(bucketMinutes)+1)
	cur := trendPoint{}
	curKey := int64(-1)
	flush := func() {
		if curKey >= 0 {
			cur.Time = time.Unix(curKey*60, 0).Format("2006-01-02 15:04:05")
			points = append(points, cur)
		}
	}
	for off := span; off >= 0; off-- {
		m := nowMinute - off
		idx := int(m % int64(len(s.buckets)))
		b := s.buckets[idx]
		key := (m / int64(bucketMinutes)) * int64(bucketMinutes)
		if key != curKey {
			flush()
			cur = trendPoint{}
			curKey = key
		}
		if b.minute == m {
			cur.Requests += b.requests
			cur.Errors += b.errors
		}
	}
	flush()
	return points
}

// trendPoint API 趋势点（api.md §2 时间格式）。
type trendPoint struct {
	Time     string `json:"time"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
}

// defaultSampler 全局采样器：MetricsMiddleware 写入、monitor Service 读取。
// 进程单例（router 挂中间件与查询面共享；重启清零——plan §10.4 局限已注明）。
var defaultSampler = newRingSampler(metricsHorizon, metricsMinute)

// MetricsMiddleware API 指标采样中间件（router 挂 /api 组，plan §12.1）：
// 记录请求计数与 5xx 错误计数，供 /api/system/monitor 趋势与错误率。
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		defaultSampler.add(c.Writer.Status() >= 500)
	}
}

// QueueStat 队列统计（sysops 侧契约值类型；router 以适配器桥接 asynqx.Inspector——
// 本包不 import asynqx/asynq，guard-asynq 红线：asynq 库仅 asynqx 可 import）。
type QueueStat struct {
	Queue     string `json:"queue"`
	Pending   int    `json:"pending"`
	Active    int    `json:"active"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
}

// QueueStatsReader 队列积压统计（消费方窄接口；缺省不注入时 monitor 省略 queued_tasks）。
type QueueStatsReader interface {
	QueueStats(ctx context.Context, queues ...string) ([]QueueStat, error)
}

// monitorService 监控读取面。
type monitorService struct {
	db           *gorm.DB
	rdb          redisPinger
	queues       QueueStatsReader
	version      string
	processStart time.Time
}

// redisPinger Redis 连通探测最小接口（*redis.Client 结构化满足；本包不 import go-redis
// 的客户端构造，仅探测——依赖面最小化）。
type redisPinger interface {
	Ping(ctx context.Context) error
}

// monitorResponse 监控响应（deployment §5 指标矩阵；snake_case tag）。
type monitorResponse struct {
	// Database 数据库连接与健康。
	Database monitorDatabase `json:"database"`
	// Redis 连通性（enabled=false 时 healthy 置 true 不误报，status 注明未启用）。
	Redis monitorRedis `json:"redis"`
	// API 请求/错误率（近 1h 窗口）。
	API monitorAPI `json:"api"`
	// Jobs 定时任务最近状态汇总。
	Jobs monitorJobs `json:"jobs"`
	// QueuedTasks 队列积压（QueueStatsReader 未注入时为 nil）。
	QueuedTasks []QueueStat `json:"queued_tasks,omitempty"`
	// UptimeSeconds 进程运行时长（秒）。
	UptimeSeconds int64 `json:"uptime_seconds"`
	// Version 应用版本（构建注入或 dev）。
	Version string `json:"version"`
	// GoVersion 运行时版本。
	GoVersion string `json:"go_version"`
	// Commit 构建提交（debug.ReadBuildInfo vcs.revision，缺失为空）。
	Commit string `json:"commit,omitempty"`
	// Trend 24h API 请求/错误趋势（5 分钟聚桶）。
	Trend []trendPoint `json:"api_trend"`
	// Remarks 指标口径注记（进程内采样局限、系统资源指标豁免——裁决①）。
	Remarks []string `json:"remarks"`
}

type monitorDatabase struct {
	MaxOpen   int   `json:"max_open_connections"`
	InUse     int   `json:"in_use"`
	Idle      int   `json:"idle"`
	WaitCount int64 `json:"wait_count"`
	Healthy   bool  `json:"healthy"`
}

type monitorRedis struct {
	Enabled bool   `json:"enabled"`
	Healthy bool   `json:"healthy"`
	Status  string `json:"status"`
}

type monitorAPI struct {
	RequestCount1h   int64   `json:"request_count_1h"`
	ErrorCount1h     int64   `json:"error_count_1h"`
	ErrorRatePercent float64 `json:"error_rate_percent"`
}

type monitorJobs struct {
	Total         int64 `json:"total"`
	Enabled       int64 `json:"enabled"`
	FailedLastRun int64 `json:"failed_last_run"`
}

func (m *monitorService) snapshot(ctx context.Context) (*monitorResponse, error) {
	resp := &monitorResponse{}
	// 数据库连接池（sql.DBStats）+ Ping 健康（2s 超时，快速失败）。
	if sqlDB, err := m.db.DB(); err == nil {
		st := sqlDB.Stats()
		resp.Database = monitorDatabase{
			MaxOpen: st.MaxOpenConnections, InUse: st.InUse, Idle: st.Idle, WaitCount: st.WaitCount,
		}
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resp.Database.Healthy = sqlDB.PingContext(pingCtx) == nil
	} else {
		resp.Database.Healthy = false
	}
	// Redis 连通（未启用不视为故障）。
	if m.rdb == nil {
		resp.Redis = monitorRedis{Enabled: false, Healthy: true, Status: "disabled"}
	} else if err := m.rdb.Ping(ctx); err != nil {
		resp.Redis = monitorRedis{Enabled: true, Healthy: false, Status: "unreachable"}
	} else {
		resp.Redis = monitorRedis{Enabled: true, Healthy: true, Status: "ok"}
	}
	// API 指标（近 1h + 24h 趋势）。
	reqs, errs := defaultSampler.snapshot(time.Hour)
	resp.API = monitorAPI{RequestCount1h: reqs, ErrorCount1h: errs}
	if reqs > 0 {
		resp.API.ErrorRatePercent = float64(errs) / float64(reqs) * 100
	}
	resp.Trend = defaultSampler.trend(5)
	// 定时任务最近状态。
	_ = m.db.WithContext(ctx).Raw(`
		SELECT COUNT(*)::bigint AS total,
		       COUNT(*) FILTER (WHERE enabled)::bigint AS enabled,
		       COUNT(*) FILTER (WHERE last_run_status = 'failed')::bigint AS failed_last_run
		FROM scheduled_jobs`).Scan(&resp.Jobs).Error
	// 队列积压（reader 未注入省略——inline 降级/未接线场景无队列语义，不造假数据）。
	if m.queues != nil {
		if stats, err := m.queues.QueueStats(ctx); err == nil {
			resp.QueuedTasks = stats
		}
	}
	resp.UptimeSeconds = int64(time.Since(m.processStart).Seconds())
	resp.Version = m.version
	resp.GoVersion = goVersion()
	resp.Commit = buildCommit()
	resp.Remarks = []string{
		"API 请求/错误指标为进程内分钟级环形采样，重启清零，未做持久化（backend-m3-plan §10.4）",
		"系统资源指标（CPU/内存/磁盘）按 Orchestrator 裁决①豁免至阶段 21；磁盘容量告警由部署侧宿主机监控承担",
	}
	return resp, nil
}

// goVersion 运行时版本（runtime.Version 标准口径，如 go1.27.0）。
func goVersion() string {
	return runtime.Version()
}

// buildCommit 从构建信息取提交哈希（vcs.revision；本地 go run 无 VCS 信息时为空——
// 不造假版本号，版本经 -ldflags 注入的 WithVersion 覆盖）。
func buildCommit() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}
