package stockops

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// RegisterRoutes 库存作业域路由（backend-m2-plan §2.2 O 交付 + api.md §1：
// /api/transfers、/api/counts）。签名沿用 M1 冻结形态（三参 + 可选 Option），
// 跨域消费接口经 Option 注入（plan §4.3：唯一装配点在 internal/router）：
//
//	stockops.RegisterRoutes(protected, db, rdb,
//	    stockops.WithGateway(inventory.NewService(db, rdb, ...)),
//	    stockops.WithBinChecker(warehouse.NewBinChecker(db)),
//	    stockops.WithSKUChecker(masterdata.NewSKUChecker(db)),
//	    stockops.WithSKUFlagReader(masterdata 读取器（MT5 接线）))
//
// 必需依赖（gateway/binChecker/skuChecker）未注入即启动期 fail-fast（plan §4.3 规则①）；
// SKUFlagReader 缺省启动不失败，依赖它的业务动作 fail-closed 拒绝（ErrReaderMissing——
// 读接口实现方为 masterdata M2 规划导出，见 ports.go 与交付偏差说明）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	if db == nil {
		panic("stockops 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	_ = rdb // 预留：与 inventory.Service 一致的热点缓存/幂等快路径参数位（M2 正确性不依赖）
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	if o.gateway == nil {
		panic("stockops 装配失败: 库存原语网关未注入（router 必须传 WithGateway(inventory.NewService(...)), plan §4.3 规则①）")
	}
	if o.bins == nil {
		panic("stockops 装配失败: 库位跨域校验未注入（router 必须传 WithBinChecker, plan §4.3 规则①）")
	}
	if o.skus == nil {
		panic("stockops 装配失败: SKU 跨域校验未注入（router 必须传 WithSKUChecker, plan §4.3 规则①）")
	}
	svc := NewService(db, opts...)
	h := &handler{svc: svc}

	// —— 调拨（api.md §1 /api/transfers；数据权限：from/to 任一端在范围内）——
	rg.GET("/transfers", auth.RequirePermission(PermTransferList), h.listTransfers)
	rg.POST("/transfers", auth.RequirePermission(PermTransferCreate), h.createTransfer)
	rg.GET("/transfers/in-transit", auth.RequirePermission(PermTransferList), h.listInTransit)
	rg.GET("/transfers/:id", auth.RequirePermission(PermTransferRead), h.getTransfer)
	rg.PUT("/transfers/:id", auth.RequirePermission(PermTransferUpdate), h.updateTransfer)
	rg.POST("/transfers/:id/submit", auth.RequirePermission(PermTransferSubmit), h.submitTransfer)
	rg.POST("/transfers/:id/approve", auth.RequirePermission(PermTransferApprove), h.approveTransfer)
	rg.POST("/transfers/:id/outbound", auth.RequirePermission(PermTransferExecute), h.outboundTransfer)
	rg.POST("/transfers/:id/arrive", auth.RequirePermission(PermTransferExecute), h.arriveTransfer)
	rg.POST("/transfers/:id/receive", auth.RequirePermission(PermTransferExecute), h.receiveTransfer)
	rg.POST("/transfers/:id/cancel", auth.RequirePermission(PermTransferCancel), h.cancelTransfer)

	// —— 仓内移库（backend-m2-plan §8.3 条 5：POST /api/inventory/moves，
	// MoveBin 原语 HTTP 化；路径挂 inventory 前缀、注册归本域——plan §2.2 O 行）——
	rg.POST("/inventory/moves", auth.RequirePermission(PermMoveExecute), h.moveBin)

	// —— 盘点（api.md §1 /api/counts；差异审核=approve，通过/驳回共用资源点 plan §9.1）——
	rg.GET("/counts", auth.RequirePermission(PermCountList), h.listCounts)
	rg.POST("/counts", auth.RequirePermission(PermCountCreate), h.createCount)
	rg.GET("/counts/:id", auth.RequirePermission(PermCountRead), h.getCount)
	rg.POST("/counts/:id/start", auth.RequirePermission(PermCountExecute), h.startCount)
	rg.PUT("/counts/:id/items", auth.RequirePermission(PermCountExecute), h.registerCount)
	rg.POST("/counts/:id/finish", auth.RequirePermission(PermCountExecute), h.finishCount)
	rg.POST("/counts/:id/complete", auth.RequirePermission(PermCountApprove), h.completeCount)
	rg.POST("/counts/:id/reject", auth.RequirePermission(PermCountApprove), h.rejectCount)
	rg.POST("/counts/:id/cancel", auth.RequirePermission(PermCountCancel), h.cancelCount)
}
