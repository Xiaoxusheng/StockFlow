package auth

// auth 域 zap 日志（安全审查 S7：login_logs 写失败不得静默丢弃）。
// 注入模式与 response.SetErrorLogger 一致（main 装配期调用一次）；
// 未注入时惰性构建默认 business 通道（info/json，stdout），保证独立运行（单测/工具）也可见。

import (
	"sync"

	"go.uber.org/zap"

	"github.com/stockflow/server/internal/logger"
	"github.com/stockflow/server/internal/middleware"
)

var (
	logMu     sync.RWMutex
	bizLogger *zap.Logger
)

// SetLogger 注入业务日志（main 装配期调用一次；传 nil 复位为未注入态）。
func SetLogger(l *zap.Logger) {
	logMu.Lock()
	defer logMu.Unlock()
	bizLogger = l
}

// businessLogger 取业务日志；未注入时惰性构建（info/json，channel=business），
// 构建失败退化为 Nop（日志问题不中断业务）。
func businessLogger() *zap.Logger {
	logMu.RLock()
	l := bizLogger
	logMu.RUnlock()
	if l != nil {
		return l
	}
	logMu.Lock()
	defer logMu.Unlock()
	if bizLogger != nil {
		return bizLogger
	}
	logs, err := logger.New("info", "json")
	if err != nil { // 常量级别/编码合法，理论不可达；退化为 Nop 防中断
		bizLogger = zap.NewNop()
		return bizLogger
	}
	bizLogger = logs.Business
	return bizLogger
}

// logLoginLogWriteFailed login_logs 写失败的兜底日志（S7）：
// 记录 request_id/用户名/IP/失败原因与底层错误，供审计完整性排查。
// 只含 login_logs 本就落库的字段，不含密码、Token 等敏感值（architecture.md §6 日志红线）。
func logLoginLogWriteFailed(err error, e middleware.LoginLogEntry, reason, requestID string) {
	businessLogger().Error("login_logs 写入失败（认证日志缺位，需审计完整性排查）",
		zap.Error(err),
		zap.String("request_id", requestID),
		zap.String("username", e.Username),
		zap.String("ip", e.IP),
		zap.String("fail_reason", reason),
	)
}
