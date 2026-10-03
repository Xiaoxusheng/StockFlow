package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stockflow/server/internal/config"
	"github.com/stockflow/server/internal/response"
)

// sensitiveQueryKeys 访问日志查询串脱敏键（命中即整体替换为 ***）。
// 日志红线：密码/Token/密钥禁止入日志（architecture.md §6）。
var sensitiveQueryKeys = map[string]struct{}{
	"password": {}, "passwd": {}, "pwd": {},
	"token": {}, "access_token": {}, "refresh_token": {}, "id_token": {},
	"secret": {}, "api_key": {}, "authorization": {},
}

// 日志字段列宽对齐（安全审查 S7）：IP 对齐 login_logs/operation_logs.ip varchar(64)、
// User-Agent 对齐 user_agent varchar(512)。按字节截断：IP 为 ASCII；UA 含多字节字符时
// 截断结果的字符数只会更少，绝不超列宽（超列宽会使落库 INSERT 失败）。
const (
	maxLogIPLen        = 64
	maxLogUserAgentLen = 512
)

// truncateStr 按字节截断至 n。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// AccessLog 访问日志：request_id 贯穿（architecture.md §3.1）。
// 只记录元信息（method/path/查询串/状态/耗时/IP/UA），请求体与 Authorization 头永不入日志；
// 查询串经 redactQuery 脱敏；5xx 记 error、4xx 记 warn、其余 info。
func AccessLog(cfg *config.Config) gin.HandlerFunc {
	_ = cfg // 预留：慢请求阈值/采样等按配置扩展（当前恒全量记录）
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		fields := []zap.Field{
			zap.String("request_id", c.GetString(response.RequestIDKey)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("query", redactQuery(c.Request.URL.RawQuery)),
			zap.Int("status", status),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", truncateStr(c.ClientIP(), maxLogIPLen)),
			zap.String("user_agent", truncateStr(c.Request.UserAgent(), maxLogUserAgentLen)),
		}
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("error", c.Errors.String()))
		}

		l := accessLogger()
		switch {
		case status >= http.StatusInternalServerError:
			l.Error("http access", fields...)
		case status >= http.StatusBadRequest:
			l.Warn("http access", fields...)
		default:
			l.Info("http access", fields...)
		}
	}
}

// redactQuery 脱敏查询串：敏感键值替换为 ***；其余键原样保留。
func redactQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil { // 无法解析的查询串整体脱敏，避免原样泄漏
		return "***"
	}
	changed := false
	for key := range values {
		if _, hit := sensitiveQueryKeys[strings.ToLower(key)]; hit {
			values[key] = []string{"***"}
			changed = true
		}
	}
	if !changed {
		return rawQuery
	}
	return values.Encode()
}
