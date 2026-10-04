package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/response"
)

// maxPanicLogBytes panic 值日志上限（对齐 accesslog/audit 的日志截断防线）：panic 值可为
// 任意类型（含大对象/map/用户数据），统一 fmt.Sprintf("%v") 归一为字符串后按字节截断，
// 消除日志注入与体积风险；完整定位信息由 stack 提供。
const maxPanicLogBytes = 2048

// Recovery panic 恢复：统一转 COMMON_INTERNAL_ERROR 信封（response.AbortInternal）。
// 完整堆栈与 panic 值只入错误日志，不回传客户端；若 panic 前响应已开始写出，
// 无法再改写信封，仅中断连接并记录日志。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				errorLogger().Error("panic 已恢复",
					zap.String("panic", truncateStr(fmt.Sprintf("%v", r), maxPanicLogBytes)),
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
