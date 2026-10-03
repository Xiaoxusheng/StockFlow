package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/stockflow/server/internal/response"
)

// RequestIDHeader 请求追踪头（architecture.md §3.1：接受前端传入或自动生成，
// 写入响应头并贯穿整条日志链）。
const RequestIDHeader = "X-Request-ID"

// maxRequestIDLen 上限：对齐 operation_logs.request_id varchar(64)（安全审查 S7）。
// 客户端提供的合法值超长时截断至 64 字符而非整值作废——保持分布式追踪链可关联；
// 字符集限定 ASCII（见 validRequestID），字节截断即字符截断。
const maxRequestIDLen = 64

// RequestID request_id 接收/生成与透传：
//   - 合法传入值（字母数字与 -_.）原样采用，超长截断至 maxRequestIDLen；
//   - 缺失或含不安全字符（日志/响应头注入风险）时生成 UUID。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(RequestIDHeader))
		if !validRequestID(id) {
			id = uuid.NewString()
		} else if len(id) > maxRequestIDLen {
			id = id[:maxRequestIDLen]
		}
		c.Set(response.RequestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// validRequestID 仅放行安全字符（ASCII 字母数字与 -_.），防日志注入与响应头注入；
// 长度不在本函数裁决（超长由调用方截断）。
func validRequestID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
