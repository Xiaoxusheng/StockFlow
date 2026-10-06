package idempotency

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// gin 中间件：高风险端点零改动接入幂等（挂载点清单见 doc.go；集成统一执行挂载，
// 本包不改路由注册）。
//
// 挂载顺序约束：auth.AuthRequired 之后（依赖 auth.CurrentUser）、业务 handler 之前。
//
//	保护范围：写方法（POST/PUT/PATCH/DELETE）且命中已注册路由（FullPath 非空）；
//	          GET 等读方法不缓存不拦截（计划 §2.10：GET 请求幂等不做）。
//	键来源：Idempotency-Key 请求头（计划 §7 头优先口径，purchase handler.go:502
//	          先例；请求体键位不在本包扩展——库存原语行级键另有合成规则）。
//	请求体：整读后回填 c.Request.Body（hash 与业务 handler 各自可读）；
//	          读上限 maxCaptureBodyBytes（超限 413，防御性兜底——全局 maxbody
//	          中间件先行时不会触达）。
//	响应：captureWriter 双写捕获（业务 handler 的 c.JSON/OK/Err 照常写出给客户端，
//	          同步缓冲用于快照）。
//
// 两种模式（集成按端点选型）：
//   - Middleware：键可选。无键请求不缓存不拦截（灰度/渐进接入安全）；
//   - RequiredMiddleware：无键 400（高风险端点最终形态——fail-closed）。

// IdempotencyKeyHeader 幂等键请求头名。
const IdempotencyKeyHeader = "Idempotency-Key"

// maxCaptureBodyBytes 请求体捕获上限（与全局 maxbody 缺省同量级；防御性兜底，
// 挂载端点已由路由组局部上限约束时不触达）。
const maxCaptureBodyBytes = 8 << 20

// Middleware 键可选模式：无 Idempotency-Key 头的请求直接放行。
func Middleware(svc *Service) gin.HandlerFunc {
	return protect(svc, false)
}

// RequiredMiddleware 键必需模式：缺头 400 IDEMPOTENCY_KEY_REQUIRED。
func RequiredMiddleware(svc *Service) gin.HandlerFunc {
	return protect(svc, true)
}

func protect(svc *Service, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			c.Next()
			return
		}
		route := c.FullPath()
		if route == "" {
			c.Next() // 未命中路由模式（404 兜底路径）：无端点维度可隔离，不处理
			return
		}
		key := strings.TrimSpace(c.GetHeader(IdempotencyKeyHeader))
		if key == "" {
			if required {
				response.Err(c, response.NewError(ErrKeyRequired, nil))
				c.Abort()
				return
			}
			c.Next()
			return
		}
		uc, ok := auth.CurrentUser(c)
		if !ok {
			response.Err(c, response.NewError(response.CodeUnauthorized, nil))
			c.Abort()
			return
		}

		// 请求体整读 + 回填（hash 与业务 handler 各自可读；LimitReader 防御超限）。
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxCaptureBodyBytes+1))
		if err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "body", "reason": "请求体读取失败",
			}))
			c.Abort()
			return
		}
		if len(raw) > maxCaptureBodyBytes {
			response.Err(c, response.NewError(response.CodePayloadTooLarge, map[string]any{
				"limit_bytes": maxCaptureBodyBytes,
			}))
			c.Abort()
			return
		}
		_ = c.Request.Body.Close()
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))

		lease, err := svc.Acquire(c.Request.Context(), uc.UserID, EndpointOf(c.Request.Method, route), key, HashRequest(raw))
		if err != nil {
			response.Err(c, err)
			c.Abort()
			return
		}
		if snap := lease.Replay(); snap != nil {
			writeReplay(c, snap)
			return
		}

		cw := &captureWriter{ResponseWriter: c.Writer}
		c.Writer = cw
		defer func() {
			if r := recover(); r != nil {
				// 执行中断：释放占用（同键重试重新执行——doc.go 语义），panic 继续
				// 上抛交由 Recovery 统一 500。
				svc.Release(c.Request.Context(), lease)
				panic(r)
			}
			svc.Complete(c.Request.Context(), lease, cw.Status(), cw.buf.Bytes())
		}()
		c.Next()
	}
}

// writeReplay 回放首次响应：HTTP 状态与响应体取首次快照；信封体为 JSON 对象时把
// request_id 改写为当前请求（追踪正确性——首次 request_id 归首次请求日志）。
func writeReplay(c *gin.Context, snap *Snapshot) {
	body := snap.Body
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) == nil {
		if _, has := envelope["request_id"]; has {
			rid, _ := json.Marshal(c.GetString(response.RequestIDKey))
			envelope["request_id"] = rid
			if nb, err := json.Marshal(envelope); err == nil {
				body = json.RawMessage(nb)
			}
		}
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(snap.Status)
	_, _ = c.Writer.Write(body)
	c.Abort()
}

// captureWriter 双写响应捕获（业务写出照常下发客户端，同步缓冲供快照）。
// Status 走 gin.ResponseWriter 内建实现（未显式 WriteHeader 时默认 200）。
type captureWriter struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.buf.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}
