// secure_headers.go 全局安全响应头（安全渗透修复）：
// 为全部响应补齐浏览器侧的基础加固头，防范 MIME 嗅探、点击劫持与 Referrer 泄漏。
//
// 刻意不设的头（落位说明）：
//   - Content-Security-Policy：CSP 属前端页面策略，需按页面实际资源清单（脚本/样式/
//     接口来源）逐页制定，由前端构建与反代层落实，后端统一注入一个宽泛 CSP 只会
//     制造虚假安全感并易与前端策略冲突；
//   - Strict-Transport-Security：HSTS 属传输层策略，TLS 在反向代理终结，由反代
//     （nginx/网关）按证书与域名配置统一下发，应用层重复下发易造成 max-age 漂移。
package middleware

import "github.com/gin-gonic/gin"

// SecureHeaders 安全响应头中间件：
//   - X-Content-Type-Options: nosniff——禁用 MIME 嗅探，防上传内容被浏览器改判执行；
//   - X-Frame-Options: DENY——禁止任何 iframe 嵌套，防点击劫持（管理端无嵌套诉求）；
//   - Referrer-Policy: strict-origin-when-cross-origin——跨站仅泄漏 origin，
//     同源保留完整 URL（含 request_id 的接口 URL 不外泄 path/query 明细）。
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}
