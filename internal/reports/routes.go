package reports

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/auth"
)

// RequirePerm auth.RequirePermission 的域内别名（全部业务路由挂权限点，permission.md §2）。
func RequirePerm(perm string) gin.HandlerFunc { return auth.RequirePermission(perm) }

// ServiceOption 装配注入项别名（router 唯一装配点，plan §4.3）。
type ServiceOption = Option

// RegisterRoutes 报表域路由（三参冻结形态 + 可选 Option；plan §9.1 冻结端点集。
// 由 router 装配挂 protected 组——/api/reports/* 与 /api/inventory/summary|alerts）。
//
//	GET /api/reports                             reports:report:list   报表目录
//	GET /api/reports/inventory-summary           reports:report:read   库存汇总
//	GET /api/reports/inbound-stats               reports:report:read   入库统计
//	GET /api/reports/outbound-stats              reports:report:read   出库统计
//	GET /api/reports/inventory-turnover          reports:report:read   库存周转
//	GET /api/reports/stagnant-stock              reports:report:read   积压识别
//	GET /api/reports/replenishment-suggestions   reports:report:read   智能补货建议
//	GET /api/reports/flow-trend                  reports:report:read   出入库流水趋势
//	GET /api/inventory/summary                   inventory:inventory:list   Dashboard/库存页汇总条
//	GET /api/inventory/alerts                    inventory:inventory:list   Dashboard/库存页预警条
//	GET /api/inventory/analytics                 inventory:inventory:list   库存分析
//	GET /api/inventory/sku-top                   inventory:inventory:list   SKU 库存 TOP N
//	GET /api/inventory/turnover-trend            inventory:inventory:list   库存周转趋势
//	GET /api/warehouses/workload                 inventory:inventory:list   仓库作业量
//	GET /api/inbounds/status-composition         reports:report:read   入库单状态构成
//	GET /api/inbounds/supplier-rank              reports:report:read   供应商入库排行
//	GET /api/outbounds/completion-rate           reports:report:read   出库订单完成率
//	GET /api/outbounds/product-rank              reports:report:read   商品出库排行
//	GET /api/purchases/analytics/trend           purchase:purchase:list   采购订单金额趋势
//	GET /api/purchases/supplier-rank             purchase:purchase:list   供应商采购排行
//	GET /api/purchases/status-composition        purchase:purchase:list   采购单状态构成
//	GET /api/sales/analytics/trend               sales:sales:list   销售订单金额趋势
//	GET /api/sales/product-rank                  sales:sales:list   商品销售排行
//	GET /api/sales/status-composition            sales:sales:list   销售单状态构成
//
// /api/inventory/summary|alerts 挂载口径（2026-10-04 裁决）：inventory 前缀端点的消费方
// 是库存域页面与 Dashboard（web/src/api/inventory.ts stockSummary/alerts、Pad 库存页、
// DashboardLists），页面入口权限为 inventory:inventory:list——沿用 reports:report:read
// 会造成"仅持报表列表权限的角色汇总条恒 403"的域间错配，故挂 inventory 域列表读权限
// （与 /api/inventory/trace 挂 returns:trace:list 的"数据域读权限承载 reports 实现"
// 先例同口径）。
//
// fail-fast：db 为 nil 即 panic（杜绝带病启动，plan §3.1 规则①）。
func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, opts ...ServiceOption) {
	_ = rdb // 冻结签名占位：本域无 Redis 消费点
	if db == nil {
		panic("reports 装配失败: db 为 nil（router 必须注入 GORM 句柄）")
	}
	h := &handler{svc: NewService(db, opts...)}

	rg.GET("/reports", RequirePerm(PermReportList), h.catalog)
	rg.GET("/reports/inventory-summary", RequirePerm(PermReportRead), h.inventorySummary)
	rg.GET("/reports/inbound-stats", RequirePerm(PermReportRead), h.inboundStats)
	rg.GET("/reports/outbound-stats", RequirePerm(PermReportRead), h.outboundStats)
	rg.GET("/reports/inventory-turnover", RequirePerm(PermReportRead), h.turnover)
	rg.GET("/reports/stagnant-stock", RequirePerm(PermReportRead), h.stagnantStock)
	rg.GET("/reports/replenishment-suggestions", RequirePerm(PermReportRead), h.replenishment)

	// Dashboard 五端点（2026-10-05 联调轮补齐前端先行挂账契约——dashboard.go；
	// 消费方 Dashboard 页无独立权限码、全员可见，故挂库存域列表读权限，
	// 与下方 summary/alerts 的"数据域读权限承载 reports 实现"裁决同口径）。
	rg.GET("/reports/dashboard/today", RequirePerm(auth.PermInventoryList), h.dashboardToday)
	rg.GET("/reports/dashboard/trend", RequirePerm(auth.PermInventoryList), h.dashboardTrend)
	rg.GET("/reports/dashboard/tasks", RequirePerm(auth.PermInventoryList), h.dashboardTasks)
	rg.GET("/reports/dashboard/alerts", RequirePerm(auth.PermInventoryList), h.dashboardAlertFeed)
	rg.GET("/reports/dashboard/warehouse-stock", RequirePerm(auth.PermInventoryList), h.dashboardWarehouseStock)

	// Dashboard/库存页聚合（reports 实现、inventory 前缀挂载——plan §9.1，GET
	// /api/inventory/trace 先例；权限挂 inventory:inventory:list，口径见文件头注）。
	rg.GET("/inventory/summary", RequirePerm(auth.PermInventoryList), h.dashboardSummary)
	rg.GET("/inventory/alerts", RequirePerm(auth.PermInventoryList), h.dashboardAlerts)
	// 库存分析（/inventory/analytics 页数据源，2026-10-05 补齐；inventory 前缀挂载同上）。
	rg.GET("/inventory/analytics", RequirePerm(auth.PermInventoryList), h.inventoryAnalytics)

	// 聚合分析端点批（2026-10-05 分析卡片轮，docs/api.md §9 契约先行——analytics.go/
	// workbench.go；静态段与各域 :id/:no 动态段共存（gin 静态优先，/inventory/summary
	// 与 /inventory/:id 同组共存先例），全部免分页直出、scopeOf 会话仓库快照）。
	// 库存域三端点：挂 inventory:inventory:list（消费方库存分析页，/inventory/analytics 同码先例）。
	rg.GET("/inventory/sku-top", RequirePerm(auth.PermInventoryList), h.skuTop)
	rg.GET("/inventory/turnover-trend", RequirePerm(auth.PermInventoryList), h.turnoverTrend)
	// 报表域趋势（ReportFlowStats 页既有 inbound/outbound-stats 同码，去分页销
	// report-flowstats-trend-cap）。
	rg.GET("/reports/flow-trend", RequirePerm(PermReportRead), h.flowTrend)
	// 入库/出域四端点：挂 reports:report:read（所在分析页既有趋势端点同码，
	// 页面同权限面"菜单可见⟺数据可达"）。
	rg.GET("/inbounds/status-composition", RequirePerm(PermReportRead), h.inboundStatusComposition)
	rg.GET("/inbounds/supplier-rank", RequirePerm(PermReportRead), h.inboundSupplierRank)
	rg.GET("/outbounds/completion-rate", RequirePerm(PermReportRead), h.outboundCompletionRate)
	rg.GET("/outbounds/product-rank", RequirePerm(PermReportRead), h.outboundProductRank)
	// 仓库作业量：挂 inventory:inventory:list（仓库分析页 warehouse-stock 同码）。
	rg.GET("/warehouses/workload", RequirePerm(auth.PermInventoryList), h.warehouseWorkload)
	// 采购/销售六端点：挂域列表读权限（新分析页独立权限面，current.md 挂账口径；
	// 立项若裁决改挂 reports:report:read 可平移不改契约形状）。
	rg.GET("/purchases/analytics/trend", RequirePerm(auth.PermPurchaseList), h.purchaseTrend)
	rg.GET("/purchases/supplier-rank", RequirePerm(auth.PermPurchaseList), h.purchaseSupplierRank)
	rg.GET("/purchases/status-composition", RequirePerm(auth.PermPurchaseList), h.purchaseStatusComposition)
	rg.GET("/sales/analytics/trend", RequirePerm(auth.PermSalesList), h.salesTrend)
	rg.GET("/sales/product-rank", RequirePerm(auth.PermSalesList), h.salesProductRank)
	rg.GET("/sales/status-composition", RequirePerm(auth.PermSalesList), h.salesStatusComposition)

	// 平台批（2026-10-05——task.ts 前端先行契约：工作台四块计数 + 我的任务明细化；
	// 挂 inventory:inventory:list——Dashboard/tasks 计数版已在同码暴露同一信息面，
	// 明细化不抬高敏感面；workbench_summary.go/workbench.go 实现；/api/tasks 无路由
	// 冲突——全域唯一 /api/prints/tasks 属 prints 组前缀不同）。
	rg.GET("/workbench/summary", RequirePerm(auth.PermInventoryList), h.workbenchSummary)
	rg.GET("/tasks", RequirePerm(auth.PermInventoryList), h.myTasks)
}
