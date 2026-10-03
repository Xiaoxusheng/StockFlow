// maxbody.go 全局请求体上限（安全审查 S6）：挂载于全局链（Recovery 之后），
// 与 main.go 的 http.Server.MaxHeaderBytes（请求头上限）共同构成入口防护。
//
// 两层防护：
//  1. Content-Length 显式超限 → 立即 413 统一信封（不等 handler 读 body，快速失败）；
//  2. 其余请求经 http.MaxBytesReader 包装：读取越限时底层返回 *http.MaxBytesError，
//     response.Err 将其归一为 413 COMMON_PAYLOAD_TOO_LARGE（chunked / 长度谎报的兜底路径）。
//
// 说明：现有域 handler 的 JSON 绑定错误走各自 COMMON_INVALID_PARAM（400）通道
// （冻结代码，不经 response.Err 透传原始错误），413 的确定性保证在 Content-Length
// 路径——当前客户端（axios JSON）均携带 Content-Length；文件中心阶段（阶段 14）的
// 上传 handler 应直接消费 MaxBytesError 或依赖反代层 client_max_body_size。
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/response"
)

// MaxBody 全局请求体上限（字节）。limit <= 0 视为未配置上限直接放行
// （config.Validate 已强制 > 0，此处为装配防御）。
func MaxBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limit <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}
		if c.Request.ContentLength > limit {
			response.Err(c, response.NewError(response.CodePayloadTooLarge, gin.H{"limit_bytes": limit}))
			c.Abort()
			return
		}
		// MaxBytesReader 越限时还会向底层声明连接回收，防止客户端继续发送
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}
