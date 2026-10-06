package sales

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// RequirePerm auth.RequirePermission 的域内别名（全部业务路由挂权限点，permission.md §2）。
func RequirePerm(perm string) gin.HandlerFunc { return auth.RequirePermission(perm) }

// Option 透传给 Service 的装配注入项（见 ports.go；router 唯一装配点，plan §4.3）。
type RouteOption = Option

// RegisterRoutes 销售出库域路由（plan §5.2 冻结三参形态 + 可选 Option；路径严格对齐
// api.md §1 领域划分与 plan §2.2 S 行交付清单）。
//
//	GET    /api/sales                 sales:sales:list       销售订单列表（分页+数据权限）
//	POST   /api/sales                 sales:sales:create     创建草稿订单
//	GET    /api/sales/{id}            sales:sales:read       订单详情
//	PUT    /api/sales/{id}            sales:sales:update     草稿编辑
//	PUT    /api/sales/{id}/submit     sales:sales:submit     提交审核
//	PUT    /api/sales/{id}/approve    sales:sales:approve    审核（APPROVE/REJECT 共用，审核即预占）
//	PUT    /api/sales/{id}/cancel     sales:sales:cancel     取消（释放预占）
//	PUT    /api/sales/{id}/close      sales:sales:close      差额关闭
//	GET    /api/outbounds             sales:outbound:list    出库单列表
//	GET    /api/outbounds/{no}        sales:outbound:read    出库单详情（含任务族）
//	POST   /api/outbounds/{no}/picks  sales:outbound:create  生成拣货任务（ALLOCATED→PICKING；
//	                                  创建类动作挂 create，禁止只读角色推进状态机）
//	PUT    /api/outbounds/{no}/cancel sales:outbound:cancel  取消出库单（释放预占）
//	PUT    /api/outbounds/{no}/close  sales:outbound:close   差额关闭
//	GET    /api/allocations           sales:allocation:list  分配记录列表
//	POST   /api/allocations           sales:allocation:execute 重新分配（释放旧锁+重分配）
//	GET    /api/picks                 sales:pick:list        拣货任务列表
//	PUT    /api/picks/{id}/claim      sales:pick:claim       领取（原子抢占）
//	PUT    /api/picks/{id}/confirm    sales:pick:execute     拣货确认
//	PUT    /api/picks/{id}/exception  sales:pick:execute     缺货/少货/库位异常上报
//	POST   /api/picks/batch-claim     sales:pick:claim       批量领取（效率层一期 B3，批量结果契约）
//	PUT    /api/picks/{id}/priority   sales:pick:assign      任务优先级（效率层一期 B3）
//	GET    /api/checks                sales:check:list       复核任务列表
//	PUT    /api/checks/{id}/claim     sales:check:claim      领取（原子指派）
//	PUT    /api/checks/{id}/confirm   sales:check:execute    复核确认（通过/五类异常）
//	PUT    /api/checks/{id}/reopen    sales:check:execute    复核异常重开（EXCEPTION→PENDING，
//	                                  异常处置后重新复核——出库单恢复 CHECKED 推进通路）
//	POST   /api/checks/batch-claim    sales:check:claim      批量领取（效率层一期 B3）
//	PUT    /api/checks/{id}/priority  sales:check:assign     任务优先级（效率层一期 B3）
//	GET    /api/packing               sales:packing:list     打包记录列表
//	POST   /api/packing               sales:packing:execute  打包（幂等键）
//	GET    /api/shipments             sales:shipment:list    发货单列表
//	POST   /api/shipments             sales:shipment:execute 发货确认（Deduct 核销预占，幂等键）
//	PUT    /api/shipments/{id}/status sales:shipment:execute 物流态流转（纯记录）
//
// 生成拣货任务挂 sales:outbound:create：任务生成同时推进出库单状态（ALLOCATED→PICKING）
// 且创建任务行，属创建类动作——只读角色不得推进状态机（permission.md §5 最小授权）。
//
// fail-fast（plan §3.1 规则①）：db 为 nil、跨域必需依赖（库存网关/SKU 开关/客户/
// 库位校验）未注入即 panic——杜绝带病启动与静默跳过业务校验。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...Option) {
	_ = rdb // 冻结签名占位：本域无 Redis 消费点（幂等最终准绳在 DB 唯一索引）
	if db == nil {
		panic("sales 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	if o.stock == nil || o.skus == nil || o.customers == nil || o.bins == nil {
		panic("sales 装配失败: 库存网关/SKU 开关/客户/库位跨域服务未注入" +
			"（router 必须传 WithStock/WithSKUAttr/WithCustomerChecker/WithBinChecker，plan §3.1 规则①）")
	}
	svc := NewService(newGormRepository(db), opts...)
	h := &handler{svc: svc}

	// —— 销售订单 ——
	rg.GET("/sales", RequirePerm(PermSalesList), h.listSalesOrders)
	rg.POST("/sales", RequirePerm(PermSalesCreate), h.createSalesOrder)
	rg.GET("/sales/:id", RequirePerm(PermSalesRead), h.getSalesOrder)
	rg.PUT("/sales/:id", RequirePerm(PermSalesUpdate), h.updateSalesOrder)
	rg.PUT("/sales/:id/submit", RequirePerm(PermSalesSubmit), h.submitSalesOrder)
	rg.PUT("/sales/:id/approve", RequirePerm(PermSalesApprove), h.approveSalesOrder)
	rg.PUT("/sales/:id/cancel", RequirePerm(PermSalesCancel), h.cancelSalesOrder)
	rg.PUT("/sales/:id/close", RequirePerm(PermSalesClose), h.closeSalesOrder)

	// —— 出库单 ——
	rg.GET("/outbounds", RequirePerm(PermOutboundList), h.listOutbounds)
	rg.GET("/outbounds/:no", RequirePerm(PermOutboundRead), h.getOutbound)
	rg.POST("/outbounds/:no/picks", RequirePerm(PermOutboundCreate), h.releaseToPick)
	rg.PUT("/outbounds/:no/cancel", RequirePerm(PermOutboundCancel), h.cancelOutbound)
	rg.PUT("/outbounds/:no/close", RequirePerm(PermOutboundClose), h.closeOutbound)

	// —— 库存分配 ——
	rg.GET("/allocations", RequirePerm(PermAllocationList), h.listAllocations)
	rg.POST("/allocations", RequirePerm(PermAllocationExecute), h.reallocate)

	// —— 拣货 ——
	rg.GET("/picks", RequirePerm(PermPickList), h.listPicks)
	rg.PUT("/picks/:id/claim", RequirePerm(PermPickClaim), h.claimPick)
	rg.PUT("/picks/:id/confirm", RequirePerm(PermPickExecute), h.confirmPick)
	rg.PUT("/picks/:id/exception", RequirePerm(PermPickExecute), h.reportPickException)
	// 批量领取（效率层一期 B3：复用 claim 权限点，零新码——§2.7）与优先级（assign 码）。
	rg.POST("/picks/batch-claim", RequirePerm(PermPickClaim), h.batchClaimPicks)
	rg.PUT("/picks/:id/priority", RequirePerm(PermPickAssign), h.setPickPriority)

	// —— 复核 ——
	rg.GET("/checks", RequirePerm(PermCheckList), h.listChecks)
	rg.PUT("/checks/:id/claim", RequirePerm(PermCheckClaim), h.claimCheck)
	rg.PUT("/checks/:id/confirm", RequirePerm(PermCheckExecute), h.confirmCheck)
	rg.PUT("/checks/:id/reopen", RequirePerm(PermCheckExecute), h.reopenCheck)
	rg.POST("/checks/batch-claim", RequirePerm(PermCheckClaim), h.batchClaimChecks)
	rg.PUT("/checks/:id/priority", RequirePerm(PermCheckAssign), h.setCheckPriority)

	// —— 打包 ——
	rg.GET("/packing", RequirePerm(PermPackingList), h.listPacking)
	rg.POST("/packing", RequirePerm(PermPackingExecute), h.pack)

	// —— 发货 ——
	rg.GET("/shipments", RequirePerm(PermShipmentList), h.listShipments)
	rg.POST("/shipments", RequirePerm(PermShipmentExecute), h.ship)
	rg.PUT("/shipments/:id/status", RequirePerm(PermShipmentExecute), h.updateShipmentStatus)
}
