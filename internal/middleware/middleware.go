// Package middleware 全局中间件：RequestID、Recovery（panic→统一错误）、
// CORS 白名单、访问日志（脱敏）（backend-m1-plan §2）。
//
// 认证/权限中间件归 internal/auth（plan §2：AuthRequired/RequirePermission），不在本包。
package middleware

import (
	"sync"

	"go.uber.org/zap"

	"github.com/stockflow/server/internal/logger"
)

var (
	logMu     sync.RWMutex
	accessLog *zap.Logger
	errorLog  *zap.Logger
)

// SetLoggers 注入分类日志（main 装配时调用一次）。传 nil 复位为未注入状态
// （测试用：之后再次取用时会重建内置默认日志）。
func SetLoggers(l *logger.Loggers) {
	logMu.Lock()
	defer logMu.Unlock()
	if l == nil {
		accessLog, errorLog = nil, nil
		return
	}
	accessLog, errorLog = l.Access, l.Error
}

func accessLogger() *zap.Logger {
	ensureLoggers()
	logMu.RLock()
	defer logMu.RUnlock()
	return accessLog
}

func errorLogger() *zap.Logger {
	ensureLoggers()
	logMu.RLock()
	defer logMu.RUnlock()
	return errorLog
}

// ensureLoggers 未注入时惰性构建内置默认日志（info/json），保证中间件可独立工作
// （单测/工具场景）。正常路径 main 必然先 SetLoggers。
func ensureLoggers() {
	logMu.Lock()
	defer logMu.Unlock()
	if accessLog != nil && errorLog != nil {
		return
	}
	logs, err := logger.New("info", "json")
	if err != nil { // 常量级别/编码合法，理论不可达；退化为 Nop 防中断
		accessLog, errorLog = zap.NewNop(), zap.NewNop()
		return
	}
	accessLog, errorLog = logs.Access, logs.Error
}
