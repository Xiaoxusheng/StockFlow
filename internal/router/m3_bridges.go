package router

// M3 跨域窄接口桥接与装配助手（backend-m3-plan §12.1/§12.2 唯一跨域机制，M2 先例
// m2_bridges.go 同构：接口定义在消费方域包、实现由被消费域/平台包提供，router 装配注入
// ——本文件是全部 M3 桥接的唯一落点，plan §2.3 判据 7）。
//
// 桥接形态说明：
//   - datax 导入写入器的编码解析依赖：masterdata.DataxCodeResolver / warehouse.
//     DataxWarehouseResolver（实现方导出，签名返回实现方值类型 CodeRef/BinRef）与
//     purchase/sales/inventory 消费方接口（内建返回值/inventory.WarehouseBinRef）
//     逐字段闭包桥接；消费方对实现方无感知，域包之间禁止 import（判据 2）。
//   - asynqx.Inspector → sysops.QueueStatsReader：/api/system/monitor 队列积压统计
//     （plan §4.1/§10.4）；sysops.QueueStat 为消费方值类型，逐字段映射。inline 模式
//     （redis.enabled=false）无 Inspector，缺省不注入——monitor 省略 queued_tasks。
//   - 上传路由 body 上限局部放宽（plan §12.1）：全局链为单点 route-aware 检查——
//     gin 在调用中间件链前已完成路由匹配（gin v1.12 gin.go:720-722，c.fullPath 先于
//     c.Next 赋值），引擎级中间件可按命中路由取 storage.upload_max_bytes 宽松上限；
//     非上传路由维持 1MB 全局防线（安全审查 S6），router 侧无法在 datax 子组上"覆盖"
//     已执行的引擎级中间件，故以本形态等价落位方案语义。
//   - asynqx datax 任务 handler 注册进程级一次（注册表重复注册 panic，沿 printing
//     renderRegisterOnce 先例；生产路径 New 仅调用一次，多次调用属测试场景）。

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/asynqx"
	"github.com/stockflow/server/internal/datax"
	"github.com/stockflow/server/internal/inventory"
	"github.com/stockflow/server/internal/masterdata"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/purchase"
	"github.com/stockflow/server/internal/sales"
	"github.com/stockflow/server/internal/sysops"
	"github.com/stockflow/server/internal/warehouse"
)

// ---- 上传路由 body 上限局部放宽（plan §12.1，api §5）----

// uploadRelaxedRoutes 需局部放宽 body 上限的上传路由（"METHOD gin全路径" → 上限字节）。
// 与 datax.RegisterRoutes 冻结路由一一对应；启动期经 verifyRelaxedRoutes 核验存在，
// 路由漂移即 fail-fast（不静默退化为全局 1MB 拒绝）。
func uploadRelaxedRoutes(uploadMaxBytes int64) map[string]int64 {
	return map[string]int64{
		"POST /api/imports": uploadMaxBytes, // 导入文件上传解析
		"POST /api/files":   uploadMaxBytes, // 文件中心上传
	}
}

// bodyLimit route-aware 全局请求体上限（安全审查 S6 单点防线 + M3 上传局部放宽）。
// 未命中放宽清单（含 NoRoute 的空 FullPath）一律取 defaultLimit。
func bodyLimit(defaultLimit int64, relaxed map[string]int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := defaultLimit
		if fp := c.FullPath(); fp != "" {
			if v, ok := relaxed[c.Request.Method+" "+fp]; ok && v > limit {
				limit = v
			}
		}
		middleware.MaxBody(limit)(c)
	}
}

// verifyRelaxedRoutes 启动期核验放宽清单中的路由确已注册（datax 路由漂移即启动失败）。
func verifyRelaxedRoutes(r *gin.Engine, relaxed map[string]int64) {
	registered := make(map[string]bool, len(relaxed))
	for _, ri := range r.Routes() {
		registered[ri.Method+" "+ri.Path] = true
	}
	for key := range relaxed {
		if !registered[key] {
			panic("router 装配失败: 上传放宽路由 " + key + " 未注册（datax 冻结路由漂移，body 上限守卫失效，plan §12.1）")
		}
	}
}

// ---- datax 导入写入器编码解析桥接（plan §12.2）----

// purchaseDataxCodeBridge 实现 purchase.CodeResolver（masterdata.DataxCodeResolver 承载）。
type purchaseDataxCodeBridge struct{ r *masterdata.DataxCodeResolver }

func (b purchaseDataxCodeBridge) ResolveSupplierCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveSupplierCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

func (b purchaseDataxCodeBridge) ResolveSKUCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveSKUCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

// salesDataxCodeBridge 实现 sales.CodeResolver（masterdata.DataxCodeResolver 承载）。
type salesDataxCodeBridge struct{ r *masterdata.DataxCodeResolver }

func (b salesDataxCodeBridge) ResolveCustomerCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveCustomerCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

func (b salesDataxCodeBridge) ResolveSKUCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveSKUCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

// dataxWarehouseCodeBridge 实现 purchase.WarehouseResolver / sales.WarehouseResolver
// （两者同形；warehouse.DataxWarehouseResolver 承载，warehouseRef 为未导出值类型，
// 字段经类型推断访问——M1 binOccupancyBridge 同款手法）。
type dataxWarehouseCodeBridge struct {
	r *warehouse.DataxWarehouseResolver
}

func (b dataxWarehouseCodeBridge) ResolveWarehouseCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveWarehouseCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

// inventoryDataxSKUBridge 实现 inventory.SKUCodes：SKU 编码解析 + 三开关合成
// （masterdata.DataxCodeResolver + masterdata.SKUFlagService 承载，两读同源 skus 表）。
type inventoryDataxSKUBridge struct {
	codes *masterdata.DataxCodeResolver
	flags *masterdata.SKUFlagService
}

func (b inventoryDataxSKUBridge) ResolveSKUWithFlags(ctx context.Context, code string) (inventory.SKURef, error) {
	ref, err := b.codes.ResolveSKUCode(ctx, code)
	if err != nil || !ref.Found {
		return inventory.SKURef{}, err
	}
	f, found, err := b.flags.GetFlags(ctx, ref.ID)
	if err != nil {
		return inventory.SKURef{}, err
	}
	if !found {
		// 两次读取间 SKU 行消失（删除竞态）：按未命中处理，不虚构开关状态。
		return inventory.SKURef{}, nil
	}
	return inventory.SKURef{
		ID:            ref.ID,
		Enabled:       ref.Enabled,
		Found:         true,
		BatchManaged:  f.BatchManaged,
		ExpiryManaged: f.ExpiryManaged,
	}, nil
}

// inventoryDataxWarehouseBridge 实现 inventory.WarehouseCodes（warehouse.DataxWarehouseResolver 承载）。
type inventoryDataxWarehouseBridge struct {
	r *warehouse.DataxWarehouseResolver
}

func (b inventoryDataxWarehouseBridge) ResolveWarehouseCode(ctx context.Context, code string) (int64, bool, bool, error) {
	ref, err := b.r.ResolveWarehouseCode(ctx, code)
	return ref.ID, ref.Enabled, ref.Found, err
}

func (b inventoryDataxWarehouseBridge) ResolveBinCode(ctx context.Context, warehouseID int64, code string) (inventory.WarehouseBinRef, error) {
	ref, err := b.r.ResolveBinCode(ctx, warehouseID, code)
	if err != nil {
		return inventory.WarehouseBinRef{}, err
	}
	return inventory.WarehouseBinRef{
		WarehouseID: ref.WarehouseID,
		ZoneID:      ref.ZoneID,
		ShelfID:     ref.ShelfID,
		BinID:       ref.BinID,
		Enabled:     ref.Enabled,
		Found:       ref.Found,
	}, nil
}

// ---- 队列统计桥接（plan §10.4：/api/system/monitor queued_tasks）----

// inspectorQueueStats 桥接 asynqx.Inspector → sysops.QueueStatsReader（逐队列统计映射）。
type inspectorQueueStats struct{ insp *asynqx.Inspector }

func (a inspectorQueueStats) QueueStats(ctx context.Context, queues ...string) ([]sysops.QueueStat, error) {
	stats, err := a.insp.QueueStats(ctx, queues...)
	if err != nil {
		return nil, err
	}
	out := make([]sysops.QueueStat, 0, len(stats))
	for _, s := range stats {
		out = append(out, sysops.QueueStat{
			Queue:     s.Queue,
			Pending:   s.Pending,
			Active:    s.Active,
			Scheduled: s.Scheduled,
			Retry:     s.Retry,
		})
	}
	return out, nil
}

// ---- asynqx datax 任务 handler 进程级一次注册（plan §4.2）----

// registerDataxQueueOnce asynqx handler 全进程一次注册防线（asynqx.RegisterHandler
// 重复注册 panic；生产路径 New 仅调用一次，router_test 多次 New 属测试场景）。
var registerDataxQueueOnce sync.Once

// registerDataxQueueHandlers 注册 datax 域任务 handler（datax:import:commit /
// datax:export:run；asynq Server.Start 与 inline 降级共用同一注册表——须先于两者执行，
// router 装配期满足）。
func registerDataxQueueHandlers(svc *datax.Service) {
	registerDataxQueueOnce.Do(func() { datax.RegisterQueueHandlers(svc) })
}

// ---- 编译期接口满足断言（漂移即编译失败，M2 m2_bridges.go 同款防线）----

var (
	_ purchase.CodeResolver      = purchaseDataxCodeBridge{}
	_ purchase.WarehouseResolver = dataxWarehouseCodeBridge{}
	_ sales.CodeResolver         = salesDataxCodeBridge{}
	_ sales.WarehouseResolver    = dataxWarehouseCodeBridge{}
	_ inventory.SKUCodes         = inventoryDataxSKUBridge{}
	_ inventory.WarehouseCodes   = inventoryDataxWarehouseBridge{}
	_ sysops.QueueStatsReader    = inspectorQueueStats{}
)
