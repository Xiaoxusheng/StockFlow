// Package logger 构建 zap 分类日志：access（访问）/ error（错误）/ business（业务）
// （architecture.md §8、backend-m1-plan §2）。
//
// 约束：
//   - 结构化输出（json/console 可配），request_id 由调用方以字段携带，贯穿日志链；
//   - 日志红线：密码、Token、敏感密钥禁止入日志（architecture.md §6）；
//     访问日志的查询串脱敏在 internal/middleware 实现。
package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// 日志通道名（写入每条记录的 channel 字段，便于检索/分流）。
const (
	ChannelAccess   = "access"
	ChannelError    = "error"
	ChannelBusiness = "business"
)

// Loggers 分类日志集合。
type Loggers struct {
	Access   *zap.Logger // 访问日志（middleware.AccessLog）
	Error    *zap.Logger // 错误日志（panic 恢复、未归类内部错误）
	Business *zap.Logger // 业务日志（各域 Service 使用）
}

// New 按级别与编码构建三类日志，输出 stdout（容器收集由部署层负责，deployment.md §8）。
func New(level, format string) (*Loggers, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, fmt.Errorf("日志级别非法 %q: %w", level, err)
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.Encoding = format // json | console
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeDuration = zapcore.MillisDurationEncoder
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.Sampling = nil // 审计相关性优先：不采样，保证逐条可见

	build := func(channel string) (*zap.Logger, error) {
		l, err := cfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
		if err != nil {
			return nil, err
		}
		return l.With(zap.String("channel", channel)), nil
	}

	access, err := build(ChannelAccess)
	if err != nil {
		return nil, err
	}
	errLog, err := build(ChannelError)
	if err != nil {
		return nil, err
	}
	business, err := build(ChannelBusiness)
	if err != nil {
		return nil, err
	}
	return &Loggers{Access: access, Error: errLog, Business: business}, nil
}

// Sync 刷新底层缓冲，进程退出前调用。Windows 下对 stdout 的 Sync 可能报错，属预期，忽略。
func (l *Loggers) Sync() {
	_ = l.Access.Sync()
	_ = l.Error.Sync()
	_ = l.Business.Sync()
}
