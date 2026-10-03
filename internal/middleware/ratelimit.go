// ratelimit.go IP 维度组级限流（安全审查 S4）：
// /api/auth 公开组的滑动窗口限流（登录/刷新是无凭证可刷的暴露面，单独防护）。
//
// 设计要点：
//   - 滑动窗口以 Redis ZSET 实现：member=请求唯一值、score=毫秒时间戳。每次请求
//     ZREMRANGEBYSCORE 清出窗外旧值 → ZCARD 计数 → 未超限才 ZADD 占位并 Expire 兜底
//     （TTL = 窗口 + 1 分钟，防僵死键永久占位）。
//   - 三条命令非原子（无 Lua 脚本），极端并发下存在少量超发；IP 维度组级粗粒度
//     限流可接受，用户名维度的严格限流由 auth 域 loginGuard 负责（契约分工见各自清单）。
//   - Redis 故障 fail-open（放行）并记 error 日志：可用性优先——认证入口被 Redis 故障
//     整体拒绝的代价高于限流短暂失效，与登录锁定（loginGuard）的取舍一致；
//     禁止静默吞错，故障必须可从 error 日志定位。
//   - rdb 为 nil（redis.enabled=false）时不启用限流（无存储可计数，同 fail-open 取舍）；
//     该形态下认证本身也不可用（auth 域启动期 fail-fast），此处仅防御装配顺序。
package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/cache"
	"github.com/stockflow/server/internal/response"
)

// ipRateLimitWindow 窗口时长（rate_limit_ip_per_minute 按"次/分钟"口径固定 1 分钟）。
const ipRateLimitWindow = time.Minute

// rateLimitStore 限流所需 Redis 命令窄接口（*redis.Client 天然满足；单测以内存替身实现，
// 不依赖 Redis/网络——沿用各包现有替身模式）。
type rateLimitStore interface {
	ZRemRangeByScore(ctx context.Context, key, min, max string) *redis.IntCmd
	ZCard(ctx context.Context, key string) *redis.IntCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
}

// AuthIPRateLimit /api/auth 公开组 IP 维度滑动窗口限流（router 装配入口）。
// limit 为窗口内每 IP 允许的请求数（auth.rate_limit_ip_per_minute）。
func AuthIPRateLimit(limit int64, rdb *redis.Client) gin.HandlerFunc {
	if rdb == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return ipRateLimit(limit, ipRateLimitWindow, rdb)
}

// ipRateLimit 实际限流逻辑（store 抽象，供单测注入内存替身）。
func ipRateLimit(limit int64, window time.Duration, store rateLimitStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := cache.Key("authrl", c.ClientIP())
		ctx := c.Request.Context()
		now := time.Now()

		// 1) 滑动窗口：清出窗外旧值
		windowStart := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)
		if err := store.ZRemRangeByScore(ctx, key, "-inf", windowStart).Err(); err != nil {
			errorLogger().Error("IP 限流 Redis 故障，fail-open 放行",
				zap.String("key", key), zap.Error(err),
				zap.String("request_id", c.GetString(response.RequestIDKey)))
			c.Next()
			return
		}

		// 2) 计数：已达上限即拒绝（被拒请求不占位，信封走统一错误码 COMMON_RATE_LIMITED）
		n, err := store.ZCard(ctx, key).Result()
		if err != nil {
			errorLogger().Error("IP 限流 Redis 故障，fail-open 放行",
				zap.String("key", key), zap.Error(err),
				zap.String("request_id", c.GetString(response.RequestIDKey)))
			c.Next()
			return
		}
		if n >= limit {
			// 不额外记业务日志：429 已由访问日志逐条记录（含 request_id/IP）
			response.Err(c, response.NewError(response.CodeRateLimited, gin.H{
				"limit":          limit,
				"window_seconds": int(window.Seconds()),
			}))
			c.Abort()
			return
		}

		// 3) 占位 + TTL 兜底。占位失败按 fail-open 放行（计数已过，宁多放不误拒）；
		//    TTL 失败仅影响键清理（ZREMRANGEBYSCORE 仍会滑出旧值），记 error 不中断。
		if err := store.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixMilli()), Member: uuid.NewString()}).Err(); err != nil {
			errorLogger().Error("IP 限流 Redis 故障，fail-open 放行",
				zap.String("key", key), zap.Error(err),
				zap.String("request_id", c.GetString(response.RequestIDKey)))
			c.Next()
			return
		}
		if err := store.Expire(ctx, key, window+time.Minute).Err(); err != nil {
			errorLogger().Error("IP 限流键 TTL 设置失败（不影响本次放行）",
				zap.String("key", key), zap.Error(err),
				zap.String("request_id", c.GetString(response.RequestIDKey)))
		}

		c.Next()
	}
}
