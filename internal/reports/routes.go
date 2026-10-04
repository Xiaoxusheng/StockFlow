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
//	GET /api/inventory/summary                   inventory:inventory:list   Dashboard/库存页汇总条
//	GET /api/inventory/alerts                    inventory:inventory:list   Dashboard/库存页预警条
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

	// Dashboard/库存页聚合（reports 实现、inventory 前缀挂载——plan §9.1，GET
	// /api/inventory/trace 先例；权限挂 inventory:inventory:list，口径见文件头注）。
	rg.GET("/inventory/summary", RequirePerm(auth.PermInventoryList), h.dashboardSummary)
	rg.GET("/inventory/alerts", RequirePerm(auth.PermInventoryList), h.dashboardAlerts)
}
