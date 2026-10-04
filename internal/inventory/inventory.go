package inventory

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// Option RegisterRoutes/NewService 的可选注入项：仅用于跨域消费接口注入
// （plan §4.3/§5.2），不得携带业务配置。With* 构造器见 ports.go。
type Option func(*options)

type options struct {
	skuChecker SKUChecker
	binChecker BinChecker
}

// RegisterRoutes 库存域路由（plan §5.2 冻结签名：三参形态 + 可选 Option）。
//
// M1 HTTP 面只读（plan §8.7）：写操作（锁定/释放/扣减等）以 Service 层为边界，
// 被 M2 业务单据域消费，不暴露 HTTP。全部路由挂 auth.RequirePermission 权限点
// （permission.md §2：接口声明权限点，中间件统一校验）：
//
//	GET /api/inventory           inventory:inventory:list（列表）
//	GET /api/inventory/{id}      inventory:inventory:list（详情；§5.4.1 冻结该资源仅 list 动作）
//	GET /api/inventory/locks     inventory:lock:list（锁定记录查询，M2 §8.3 条 5）
//	GET /api/inventory/adjustments stockops:adjustment:list（调整单列表，M2 §8.3 条 5）
//	GET /api/inventory-ledgers   inventory:ledger:list
//	GET /api/batches             inventory:batch:list
//	GET /api/serials             inventory:serial:list
//
// 数据权限：列表/详情按 auth.WarehouseScope 的仓库范围过滤（permission.md §4，
// 禁止接受前端传入的仓库范围参数决定数据可见性）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	if db == nil {
		// 装配错误启动期 fail-fast（deployment.md §3 禁止带病启动；
		// 与 masterdata 域同约定）。
		panic("inventory 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	// plan §4.3 规则①：M1 必需 Checker 未注入即启动 fail-fast，杜绝静默跳过业务校验
	// （router.go T6 TODO 同约定；实现 masterdata.NewSKUChecker / warehouse.NewBinChecker）。
	if o.skuChecker == nil || o.binChecker == nil {
		panic("inventory 装配失败: SKU/库位跨域校验未注入（router 必须传 WithSKUChecker/WithBinChecker，plan §4.3 规则①）")
	}
	svc := NewService(db, rdb, opts...)
	h := &handler{svc: svc}

	rg.GET("/inventory", auth.RequirePermission(auth.PermInventoryList), h.listInventory)
	rg.GET("/inventory/locks", auth.RequirePermission(auth.PermLockList), h.listLocks)
	rg.GET("/inventory/adjustments", auth.RequirePermission(auth.PermAdjustmentList), h.listAdjustments)
	rg.GET("/inventory/:id", auth.RequirePermission(auth.PermInventoryList), h.getInventory)
	rg.GET("/inventory-ledgers", auth.RequirePermission(auth.PermLedgerList), h.listLedgers)
	rg.GET("/batches", auth.RequirePermission(auth.PermBatchList), h.listBatches)
	rg.GET("/serials", auth.RequirePermission(auth.PermSerialList), h.listSerials)
}
