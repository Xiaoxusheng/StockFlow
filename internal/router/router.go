// Package router 全局路由装配（backend-m1-plan §5.3）。
// 唯一允许静态 import 全部域包的包（plan §2/§3）；M2 新增域只改本文件；
// 跨域消费接口的装配注入也只在此处（plan §4.3）。
package router

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/health"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/masterdata"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/warehouse"
)

// New 装配 gin.Engine：受信代理 → 全局中间件 → 探针（免认证，api.md §6.1）→ /api 装配。
func New(cfg *config.Config, db *gorm.DB, rdb *redis.Client) *gin.Engine {
	r := gin.New()
	// 受信代理（安全审查 S5）：显式按配置设置——空列表 = 不信任任何代理，ClientIP
	// 一律取直连 RemoteAddr（gin 默认信任全部代理，会放行 X-Forwarded-For 伪造）。
	// 网段非法属启动期配置错误，config.Validate 已先行拦截，此处 fail-fast 双保险
	// （与 auth 装配 panic 同一模式，deployment.md §3 禁止带病启动）。
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		panic("router 装配失败: 设置受信代理失败: " + err.Error())
	}
	// 全局中间件链（plan §5.3 冻结形态）：Recovery → RequestID → MaxBody → AccessLog → CORS。
	// MaxBody（安全审查 S6）置于 Recovery 之后、RequestID 之后——413 统一信封携带
	// request_id，且超限请求同样留下访问日志。
	r.Use(middleware.Recovery(), middleware.RequestID(), middleware.MaxBody(cfg.Server.MaxBodyBytes),
		middleware.AccessLog(cfg), middleware.CORS(cfg.CORS.AllowedOrigins))

	// 探针（免认证：api.md §6.1 豁免名单）
	r.GET("/health", health.Liveness())
	r.GET("/ready", health.Readiness(db, rdb))

	// 未匹配路由 / 未允许方法也走统一信封（前端 client.ts 依赖 message/request_id）
	r.NoRoute(func(c *gin.Context) {
		response.Err(c, response.NewError(response.CodeNotFound, nil))
	})
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		// 405 语义（REST 方法不匹配），区别于路径不存在的 404；信封结构不变。
		response.Err(c, response.NewError(response.CodeMethodNotAllowed, gin.H{"reason": "method not allowed"}))
	})

	api := r.Group("/api")

	// 公开组：认证登录/刷新（api.md §6.1 豁免；plan §5.3）。
	// IP 维度组级滑动窗口限流（安全审查 S4，auth.rate_limit_ip_per_minute 默认 30）：
	// Redis 故障 fail-open 并记 error 日志；用户名维度的严格限流在 auth 域 guard 内
	// （契约分工见各自清单）。
	authPublic := api.Group("/auth")
	authPublic.Use(middleware.AuthIPRateLimit(int64(cfg.Auth.RateLimitIPPerMinute), rdb))
	auth.RegisterPublicRoutes(authPublic, db, rdb)

	// 保护组：全部业务接口；跨域消费接口在本处按 plan §4.3/§5.3 注入（唯一装配点，
	// 各域对"必需 Checker 未注入"启动期 fail-fast，plan §4.3 规则①；db==nil 属装配
	// 错误，域内同样 fail-fast，deployment.md §3 禁止带病启动）。
	protected := api.Group("")
	protected.Use(auth.AuthRequired())
	auth.RegisterProtectedRoutes(protected, db, rdb, auth.WithWarehouseChecker(warehouse.NewChecker(db)))
	masterdata.RegisterRoutes(protected, db, rdb)
	warehouse.RegisterRoutes(protected, db, rdb,
		warehouse.WithBinOccupancy(binOccupancyBridge(inventory.NewBinOccupancy(db))))
	inventory.RegisterRoutes(protected, db, rdb,
		inventory.WithSKUChecker(masterdata.NewSKUChecker(db)),
		inventory.WithBinChecker(warehouse.NewBinChecker(db)))

	return r
}

// ---- 跨域接口桥接（plan §4.3：仅 router 装配处允许引用多域实现）----

// occupancyBridgeFunc 闭包适配器：将函数适配为 warehouse.BinOccupancyReader。
type occupancyBridgeFunc func(ctx context.Context, warehouseID int64) (quantity, locked map[int64]float64, err error)

// OccupancyByWarehouse 实现 warehouse.BinOccupancyReader。
func (f occupancyBridgeFunc) OccupancyByWarehouse(ctx context.Context, warehouseID int64) (map[int64]float64, map[int64]float64, error) {
	return f(ctx, warehouseID)
}

// binOccupancyBridge 将 inventory.BinOccupancy.SumByBin 桥接为 warehouse.BinOccupancyReader
// （warehouse/ports.go 装配约定，plan §4.3 ②/§5.3）：SumByBin 返回 inventory 域具名类型
// 切片（[]BinOccupancySummary），warehouse 包不可命名，故以闭包适配器桥接为内建类型
// 窄接口。locked 无独立数据源（SumByBin 仅聚合六状态总和），按契约返回 nil
// （缺省键 = 无占用信息，不造假数据，requirements.md §10）。
func binOccupancyBridge(o *inventory.BinOccupancy) warehouse.BinOccupancyReader {
	return occupancyBridgeFunc(func(ctx context.Context, warehouseID int64) (map[int64]float64, map[int64]float64, error) {
		rows, err := o.SumByBin(ctx, warehouseID)
		if err != nil {
			return nil, nil, fmt.Errorf("汇总仓库 %d 库位占用失败: %w", warehouseID, err)
		}
		quantity := make(map[int64]float64, len(rows))
		for _, r := range rows {
			// Qty 为 numeric(18,4) 精确十进制载体，String() 输出 4 位小数文本，
			// ParseFloat 取最接近表示；窄接口 float64 为跨域契约既定形态。
			q, perr := strconv.ParseFloat(r.TotalQty.String(), 64)
			if perr != nil {
				return nil, nil, fmt.Errorf("解析仓库 %d 库位 %d 占用数量失败: %w", warehouseID, r.BinID, perr)
			}
			quantity[r.BinID] = q
		}
		return quantity, nil, nil
	})
}
