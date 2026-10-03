package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/response"
)

// Recovery panic 恢复：统一转 COMMON_INTERNAL_ERROR 信封（response.AbortInternal）。
// 完整堆栈与 panic 值只入错误日志，不回传客户端；若 panic 前响应已开始写出，
// 无法再改写信封，仅中断连接并记录日志。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				errorLogger().Error("panic 已恢复",
					zap.Any("panic", r),
					zap.ByteString("stack", debug.Stack()),
					zap.String("request_id", c.GetString(response.RequestIDKey)),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
				)
				if c.Writer.Written() {
					c.AbortWithStatus(http.StatusInternalServerError)
					return
				}
				response.AbortInternal(c)
			}
		}()
		c.Next()
	}
}
