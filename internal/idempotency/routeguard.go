package idempotency

import (
	"github.com/gin-gonic/gin"
)

// 路由感知组级挂载（效率层一期集成收口交付）。
//
// 集成形态裁决（与实现批 wiringNotes 的逐路由行内挂载语义等价）：RouteGuard 实例
// 挂 protected 组——auth.AuthRequired 之后、各域 RegisterRoutes 之前（gin 组中间件
// 按注册时点快照链，晚于路由注册的 Use 对已注册路由不生效）——仅「写方法 +
// c.FullPath() 命中 endpoints 注册表」的请求进入幂等仲裁，其余请求（读方法、表外
// 端点、未命中路由）零干预放行。组级形态不侵入八个域包的路由文件与 Option 装配面，
// 改动集中在装配层；端点注册表由 router 唯一持有（路由知识唯一装配点口径），
// 漂移由 router 装配尾部启动核验 fail-fast 兜底（verifyRelaxedRoutes 同款，
// deployment.md §3 禁止带病启动）。
//
// 挂载顺序约束与 protect 一致：auth.AuthRequired 之后（依赖 auth.CurrentUser）。

// EndpointPolicy 高风险端点防护模式。
type EndpointPolicy struct {
	// Required=true：缺 Idempotency-Key 头 400 IDEMPOTENCY_KEY_REQUIRED（fail-closed
	// 终态——须前端提交点全部接 useIdempotentMutation 附键后逐端点升级）；
	// Required=false：无头放行（灰度形态，计划 §2.10 渐进接入；附键请求即获
	// 「恰一执行 + 首次响应回放」防护）。
	Required bool
}

// RouteGuard 路由感知组级中间件：endpoints 键 = EndpointOf(method, 路由模式)
// （如 "POST /api/receipts"），与 gin 路由树 c.FullPath() 逐字同源。
func RouteGuard(svc *Service, endpoints map[string]EndpointPolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.FullPath() == "" {
			c.Next() // 未命中路由模式（404 兜底路径）：无端点维度，不处理
			return
		}
		pol, ok := endpoints[EndpointOf(c.Request.Method, c.FullPath())]
		if !ok {
			c.Next()
			return
		}
		protect(svc, pol.Required)(c)
	}
}
