package purchase

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// 路由装配（api.md §1 领域划分 + plan §2.2 P 交付面）：
//
//	/api/purchases   采购订单（列表/详情/创建/修改/submit/approve/cancel/close）
//	/api/inbounds    入库单（列表/详情/创建/修改/cancel/close）
//	/api/receipts    收货（列表/详情/按单号/收货确认 execute）
//	/api/quality     质检单（列表/详情/创建/start/execute）
//	/api/putaway     上架任务（列表/详情/推荐库位/claim/execute）
//
// 全部挂 auth.RequirePermission（权限点常量本包导出，plan §9.2，供集成工程师收编
// internal/auth/permissions.go）；列表强制分页（api.md §2.1）；统一经 internal/response
// 输出（判据 6）；rdb 暂无本域缓存消费点，保留入参对齐 M1 冻结签名形态。
// RegisterRoutes 签名与 M1 三域同形（rg, db, rdb, opts...），集成工程师 MT5 直接挂接。

// Option RegisterRoutes 的可选注入项（跨域消费接口，ports.go）。
type RegisterOption = Option

// RegisterRoutes 采购入库域路由。约定：
//   - rg 已挂 auth.AuthRequired()；域内对每条路由挂 auth.RequirePermission(...)；
//   - db 为 nil 属装配错误，启动期 fail-fast（deployment.md §3 禁止带病启动）；
//   - 收货/质检/上架的库存落账必需 WithStock 注入库存原语网关（运行期 fail-closed）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	_ = rdb // rdb 暂无本域缓存消费点（对齐 M1 冻结签名形态保留入参）
	if db == nil {
		panic("purchase 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	repo := NewRepository(db)
	svc := NewService(repo, opts...)

	// —— 采购订单 ——
	rg.GET("/purchases", auth.RequirePermission(PermPurchaseList), func(c *gin.Context) { handlePOList(c, svc) })
	rg.POST("/purchases", auth.RequirePermission(PermPurchaseCreate), func(c *gin.Context) { handlePOCreate(c, svc) })
	rg.GET("/purchases/:id", auth.RequirePermission(PermPurchaseRead), func(c *gin.Context) { handlePODetail(c, svc) })
	rg.PUT("/purchases/:id", auth.RequirePermission(PermPurchaseUpdate), func(c *gin.Context) { handlePOUpdate(c, svc) })
	rg.POST("/purchases/:id/submit", auth.RequirePermission(PermPurchaseSubmit), func(c *gin.Context) { handlePOSubmit(c, svc) })
	rg.POST("/purchases/:id/approve", auth.RequirePermission(PermPurchaseApprove), func(c *gin.Context) { handlePOApprove(c, svc) })
	rg.POST("/purchases/:id/cancel", auth.RequirePermission(PermPurchaseCancel), func(c *gin.Context) { handlePOCancel(c, svc) })
	rg.POST("/purchases/:id/close", auth.RequirePermission(PermPurchaseClose), func(c *gin.Context) { handlePOClose(c, svc) })

	// —— 入库单 ——
	rg.GET("/inbounds", auth.RequirePermission(PermInboundList), func(c *gin.Context) { handleInboundList(c, svc) })
	rg.POST("/inbounds", auth.RequirePermission(PermInboundCreate), func(c *gin.Context) { handleInboundCreate(c, svc) })
	rg.GET("/inbounds/:id", auth.RequirePermission(PermInboundRead), func(c *gin.Context) { handleInboundDetail(c, svc) })
	rg.PUT("/inbounds/:id", auth.RequirePermission(PermInboundUpdate), func(c *gin.Context) { handleInboundUpdate(c, svc) })
	rg.POST("/inbounds/:id/cancel", auth.RequirePermission(PermInboundCancel), func(c *gin.Context) { handleInboundCancel(c, svc) })
	rg.POST("/inbounds/:id/close", auth.RequirePermission(PermInboundClose), func(c *gin.Context) { handleInboundClose(c, svc) })

	// —— 收货 ——
	rg.GET("/receipts", auth.RequirePermission(PermReceiptList), func(c *gin.Context) { handleReceiptList(c, svc) })
	rg.GET("/receipts/no/:no", auth.RequirePermission(PermReceiptRead), func(c *gin.Context) { handleReceiptDetail(c, svc) })
	rg.GET("/receipts/:id", auth.RequirePermission(PermReceiptRead), func(c *gin.Context) { handleReceiptDetail(c, svc) })
	rg.POST("/receipts", auth.RequirePermission(PermReceiptExecute), func(c *gin.Context) { handleReceiptConfirm(c, svc) })

	// —— 质检 ——
	rg.GET("/quality", auth.RequirePermission(PermQualityList), func(c *gin.Context) { handleQCList(c, svc) })
	rg.POST("/quality", auth.RequirePermission(PermQualityCreate), func(c *gin.Context) { handleQCCreate(c, svc) })
	rg.GET("/quality/:id", auth.RequirePermission(PermQualityRead), func(c *gin.Context) { handleQCDetail(c, svc) })
	rg.POST("/quality/:id/start", auth.RequirePermission(PermQualityExecute), func(c *gin.Context) { handleQCStart(c, svc) })
	rg.POST("/quality/:id/execute", auth.RequirePermission(PermQualityExecute), func(c *gin.Context) { handleQCExecute(c, svc) })

	// —— 上架任务 ——
	rg.GET("/putaway", auth.RequirePermission(PermPutawayList), func(c *gin.Context) { handleTaskList(c, svc) })
	rg.GET("/putaway/recommend", auth.RequirePermission(PermPutawayRead), func(c *gin.Context) { handleTaskRecommend(c, svc) })
	rg.GET("/putaway/:id", auth.RequirePermission(PermPutawayRead), func(c *gin.Context) { handleTaskDetail(c, svc) })
	rg.POST("/putaway/:id/claim", auth.RequirePermission(PermPutawayClaim), func(c *gin.Context) { handleTaskClaim(c, svc) })
	rg.POST("/putaway/:id/execute", auth.RequirePermission(PermPutawayExecute), func(c *gin.Context) { handleTaskExecute(c, svc) })
}
