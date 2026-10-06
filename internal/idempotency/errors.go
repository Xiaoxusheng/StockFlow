package idempotency

import (
	"net/http"

	"github.com/stockflow/server/internal/response"
)

// idempotency 域错误码（architecture.md §2 模块命名空间；统一经 internal/response
// 注册，禁止 handler 直接 c.JSON）。
var (
	// ErrKeyRequired RequiredMiddleware 模式下缺失 Idempotency-Key 头。
	ErrKeyRequired = response.Register("IDEMPOTENCY_KEY_REQUIRED", "缺少 Idempotency-Key 请求头", http.StatusBadRequest)
	// ErrKeyInvalid 键格式非法（[A-Za-z0-9._:-] 1-64 字符，000024 CHECK 同源）。
	ErrKeyInvalid = response.Register("IDEMPOTENCY_KEY_INVALID", "Idempotency-Key 格式非法", http.StatusBadRequest)
	// ErrInProgress 并发同键第二请求（占用行仍 PROCESSING）或键无法回放的保守拒绝。
	ErrInProgress = response.Register("IDEMPOTENCY_IN_PROGRESS", "相同请求正在处理中，请勿重复提交", http.StatusConflict)
	// ErrRequestMismatch 同键不同请求体（request_hash 不符——客户端键复用缺陷防御）。
	ErrRequestMismatch = response.Register("IDEMPOTENCY_REQUEST_MISMATCH", "幂等键已绑定其他请求内容", http.StatusConflict)
)
