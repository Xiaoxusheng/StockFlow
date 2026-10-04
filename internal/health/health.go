// Package health 存活/就绪探针（api.md §8、deployment.md §3），免认证。
// /health 只表明进程存活；/ready 检查 DB + Redis + 文件中心存储根
// （存储检查由 backend-m3-plan §6.4 引入，M1 §12 挂账销项）。
package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
//
// @Summary 进程存活探针
// @Tags 健康检查
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /health [get]
func Liveness() gin.HandlerFunc {
	return func(c *gin.Context) {
		response.OK(c, gin.H{"status": "UP"})
	}
}

// Readiness GET /ready：DB + Redis + 文件存储就绪。
//   - rdb 为 nil：配置关闭 Redis，计 DISABLED，不阻塞就绪；
//   - db 为 nil：装配错误，判 DOWN（503）；
//   - storageRoots 非 nil（长度 > 0）：M3 文件中心存储根可写探测（backend-m3-plan §6.4，
//     M1 §12 挂账销项，deployment §3）——根目录可创建且探测文件可写可删，任一失败判 DOWN。
//
// 探测失败返回 503 + COMMON_SERVICE_UNAVAILABLE，details 携带各组件状态。
// 结果带 1 秒本地缓存（readinessCacheTTL）；缓存为本次 Readiness() 调用实例私有
// （闭包变量 + mutex），不同装配/不同进程间互不串扰。
//
// @Summary 就绪探针（DB + Redis + 文件存储）
// @Tags 健康检查
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Failure 503 {object} response.Envelope "服务暂不可用（组件 DOWN，details 携带各组件状态）"
// @Router /ready [get]
func Readiness(db *gorm.DB, rdb *redis.Client, storageRoots ...string) gin.HandlerFunc {
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

		// 文件中心存储根可写探测（探针文件名固定 + 服务端生成，不拼接任何外部输入；
		// 探测失败判 DOWN——文件中心上传/导出产物落盘依赖该根）。
		for _, root := range storageRoots {
			if err := probeWritableDir(root); err != nil {
				status["storage"] = "DOWN"
				ready = false
				break
			}
		}
		if _, ok := status["storage"]; !ok && len(storageRoots) > 0 {
			status["storage"] = "UP"
		}

		return status, ready
	})
}

// probeWritableDir 存储根可写探测：确保目录存在 → 唯一探测文件写入 → 校验 → 删除。
// 任一步失败即返回错误（权限/磁盘/挂载问题在 /ready 及时暴露，deployment §3 禁止带病部署）。
func probeWritableDir(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("创建存储根 %s 失败: %w", root, err)
	}
	probe := filepath.Join(root, ".ready-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("写入存储根探测文件失败: %w", err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("清理存储根探测文件失败: %w", err)
	}
	return nil
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
