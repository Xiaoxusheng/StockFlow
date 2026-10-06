// 幂等中间件挂载注册表（效率层一期 §2.10，集成收口 2026-10-06）：
// RouteGuard 组级路由感知挂载的端点白名单与启动核验，装配知识唯一归 router。
package router

import (
	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/idempotency"
)

// idempotentEndpoints 高风险端点幂等挂载注册表（实现批 ask 冻结 12 项清单，
// internal/idempotency/doc.go）。键 = idempotency.EndpointOf(method, gin 路由模式)，
// 与各域 RegisterRoutes 注册路径逐字同源（绝对路径，router 传入 protected 组）；
// 任一表项未命中已注册路由由 verifyIdempotentEndpoints 启动核验 fail-fast。
//
// 全表 Required=false（灰度形态）：前端提交点经 useIdempotentMutation
// （web/src/hooks/useIdempotentMutation.ts）附 Idempotency-Key 头后即获「恰一执行 +
// 首次响应回放」防护；逐端点升级 Required 属后续批次（api.md §9 披露升级前提）。
// 与库存原语行级幂等（inventory_ledgers.idempotency_key 唯一索引 + stockops 头键
// 合成，b220dea）两层正交，api.md §7。
//
// 清单对照与差异披露（12 项逐项核对域路由实注册情况）：
//  1. 收货确认 POST /api/receipts（purchase/purchase.go:61）✓
//  2. 入库确认 POST /api/inbounds/:id/confirm —— 域内无此路由：purchase 入库确认
//     入口即收货确认（收货确认生成入库，business-flow §4），不挂（不造路由）
//  3. 上架执行 POST /api/putaway/:id/execute（purchase/purchase.go:87）✓
//  4. 库存调整 —— inventory 域无调整写端点（仅 GET /api/inventory/adjustments 列表，
//     调整单由盘点完成生成），不挂
//  5. 调拨 approve/execute（stockops/routes.go:52-55，execute=outbound/arrive/receive）✓
//  6. 拣货确认 PUT /api/picks/:id/confirm（sales/routes.go:100）✓
//  7. 复核确认 PUT /api/checks/:id/confirm（sales/routes.go:109）✓
//  8. 打包 POST /api/packing（sales/routes.go:116）✓
//  9. 发货 POST /api/shipments（sales/routes.go:120）✓
//  10. 盘点调整 POST /api/counts/:id/complete（stockops/routes.go:69，调整单生成点）✓
//  11. 批量操作×4：POST /api/putaway/batch-claim（purchase/purchase.go:80）、
//     POST /api/picks/batch-claim 与 POST /api/checks/batch-claim
//     （sales/routes.go:103/111）、POST /api/prints/tasks（printing/handler.go:138）✓
//  12. 重导失败行 POST /api/imports/:id/retry-failed（datax/handler.go:115）——
//     实现批建议 RequiredMiddleware，但已交付前端 dataApi.imports.retryFailed
//     （web/src/api/data.ts:336）未附键，Required 即刻 400 打断重导入口；
//     本期同表灰度挂载（附键即防护），升级随前端附键批次（followup）
var idempotentEndpoints = map[string]idempotency.EndpointPolicy{
	"POST /api/receipts":                 {},
	"POST /api/putaway/:id/execute":      {},
	"POST /api/transfers/:id/approve":    {},
	"POST /api/transfers/:id/outbound":   {},
	"POST /api/transfers/:id/arrive":     {},
	"POST /api/transfers/:id/receive":    {},
	"PUT /api/picks/:id/confirm":         {},
	"PUT /api/checks/:id/confirm":        {},
	"POST /api/packing":                  {},
	"POST /api/shipments":                {},
	"POST /api/counts/:id/complete":      {},
	"POST /api/putaway/batch-claim":      {},
	"POST /api/picks/batch-claim":        {},
	"POST /api/checks/batch-claim":       {},
	"POST /api/prints/tasks":             {},
	"POST /api/imports/:id/retry-failed": {},
}

// verifyIdempotentEndpoints 启动核验：注册表每项必须命中已注册路由——防域路由
// 改名/删除致防护静默失效（verifyRelaxedRoutes 同款 fail-fast，deployment.md §3
// 禁止带病启动；路由表漂移在装配期即 panic 而非运行期丢防护）。
func verifyIdempotentEndpoints(r *gin.Engine, endpoints map[string]idempotency.EndpointPolicy) {
	registered := make(map[string]bool, len(endpoints))
	for _, ri := range r.Routes() {
		registered[ri.Method+" "+ri.Path] = true
	}
	for key := range endpoints {
		if !registered[key] {
			panic("router 装配失败: 幂等挂载端点 " + key + " 未注册（域路由漂移，幂等防护失效，效率层一期 §2.10）")
		}
	}
}
