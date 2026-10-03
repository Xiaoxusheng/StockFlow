// Package health 存活/就绪探针（api.md §8、deployment.md §3），免认证。
// /health 只表明进程存活；/ready 检查 DB + Redis（api.md §8 的文件系统检查
// 随阶段 14 文件中心引入，backend-m1-plan §12 已挂账）。
package health

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// checkTimeout 单依赖探测超时，避免探针被慢依赖拖死。
const checkTimeout = 2 * time.Second

// readinessCacheTTL /ready 结果本地缓存时长（安全审查 S13）：
// 编排器高频拉取就绪探针时组件探测（连接池/Ping）有实际成本，1 秒缓存平滑峰值；
// 组件状态细节（details）原样保留。依赖恢复的感知延迟 ≤1 秒，属可接受取舍。
const readinessCacheTTL = time.Second

// Liveness GET /health：进程存活（不查依赖）。
func Liveness() gin.HandlerFunc {
	return func(c *gin.Context) {
		response.OK(c, gin.H{"status": "UP"})
	}
}

// Readiness GET /ready：DB + Redis 就绪。
//   - rdb 为 nil：配置关闭 Redis，计 DISABLED，不阻塞就绪；
//   - db 为 nil：装配错误，判 DOWN（503）。
//
// 探测失败返回 503 + COMMON_SERVICE_UNAVAILABLE，details 携带各组件状态。
// 结果带 1 秒本地缓存（readinessCacheTTL）；缓存为本次 Readiness() 调用实例私有
// （闭包变量 + mutex），不同装配/不同进程间互不串扰。
func Readiness(db *gorm.DB, rdb *redis.Client) gin.HandlerFunc {
	return cachedReadiness(func(ctx context.Context) (gin.H, bool) {
		status := gin.H{}
		ready := true

		if db == nil {
			status["database"] = "DOWN"
			ready = false
		} else if sqlDB, err := db.DB(); err != nil {
			status["database"] = "DOWN"
			ready = false
		} else if err := sqlDB.PingContext(ctx); err != nil {
			status["database"] = "DOWN"
			ready = false
		} else {
			status["database"] = "UP"
		}

		if rdb == nil {
			status["redis"] = "DISABLED"
		} else if err := rdb.Ping(ctx).Err(); err != nil {
			status["redis"] = "DOWN"
			ready = false
		} else {
			status["redis"] = "UP"
		}

		return status, ready
	})
}

// probeFunc 单次就绪探测：返回组件状态与整体就绪（抽象便于单测注入计数替身）。
type probeFunc func(ctx context.Context) (status gin.H, ready bool)

// cachedReadiness 带 1 秒本地缓存的就绪探测。首个请求触发探测，TTL 内直接复用结果。
func cachedReadiness(probe probeFunc) gin.HandlerFunc {
	return cachedReadinessWithClock(probe, time.Now)
}

// cachedReadinessWithClock cachedReadiness 的时钟注入形态（单测驱动过期路径，不真睡 1 秒）。
func cachedReadinessWithClock(probe probeFunc, now func() time.Time) gin.HandlerFunc {
	var (
		mu          sync.Mutex
		cachedAt    time.Time
		cachedH     gin.H // nil = 尚无缓存
		cachedReady bool
	)
	return func(c *gin.Context) {
		mu.Lock()
		if cachedH != nil && now().Sub(cachedAt) < readinessCacheTTL {
			status, ready := cachedH, cachedReady
			mu.Unlock()
			respondReady(c, status, ready)
			return
		}
		mu.Unlock()

		// 探测在锁外执行：探测耗时（最长 checkTimeout）不阻塞并发探针请求，
		// 代价是 TTL 边界的并发请求可能各探测一次（合并不做，保持简单）。
		ctx, cancel := context.WithTimeout(c.Request.Context(), checkTimeout)
		defer cancel()
		status, ready := probe(ctx)

		mu.Lock()
		cachedAt, cachedH, cachedReady = now(), status, ready
		mu.Unlock()

		respondReady(c, status, ready)
	}
}

// respondReady 就绪结果统一出口：失败 503 + COMMON_SERVICE_UNAVAILABLE（details 带各组件状态）。
func respondReady(c *gin.Context, status gin.H, ready bool) {
	if !ready {
		response.Err(c, response.NewError(response.CodeServiceUnavailable, status))
		return
	}
	response.OK(c, status)
}
