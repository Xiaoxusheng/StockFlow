package middleware

// maxbody.go 测试：Content-Length 预检 413、MaxBytesReader 包装、limit<=0 放行，
// 以及 response.Err 对 *http.MaxBytesError 的 413 归一（安全审查 S6）。

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/response"
)

// postWithBody 构造 POST 请求；chunked=true 时抹去 Content-Length（模拟分块传输）。
func postWithBody(t *testing.T, body string, chunked bool) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString(body))
	if chunked {
		req.ContentLength = -1
	}
	return req
}

func TestMaxBodyContentLengthOverLimitIs413(t *testing.T) {
	r := newGin()
	r.Use(RequestID(), MaxBody(16))
	r.POST("/upload", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postWithBody(t, strings.Repeat("x", 17), false))

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	body := decodeJSON(t, rec.Body.String())
	require.Equal(t, "COMMON_PAYLOAD_TOO_LARGE", body["code"]) // 经 response 注册的统一错误码
	require.NotEmpty(t, body["request_id"])                    // 中间件置于 RequestID 之后，信封带追踪 ID
}

func TestMaxBodyWithinLimitPasses(t *testing.T) {
	r := newGin()
	r.Use(RequestID(), MaxBody(16))
	var got string
	r.POST("/upload", func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		got = string(b)
		c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postWithBody(t, "exactly-16-byte", false))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "exactly-16-byte", got) // 未超限的 body 原样可达 handler
}

// chunked（无 Content-Length）超限：handler 读 body 得到 *http.MaxBytesError，
// 经 response.Err 归一为 413 统一信封（S6 兜底路径）。
func TestMaxBodyChunkedOverflowIs413(t *testing.T) {
	r := newGin()
	r.Use(RequestID(), MaxBody(16))
	r.POST("/upload", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			response.Err(c, err) // 域 handler 的标准错误出口
			return
		}
		c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postWithBody(t, strings.Repeat("x", 64), true))

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	body := decodeJSON(t, rec.Body.String())
	require.Equal(t, "COMMON_PAYLOAD_TOO_LARGE", body["code"])
	require.NotEmpty(t, body["request_id"])
}

func TestMaxBodyZeroLimitIsNoop(t *testing.T) {
	r := newGin()
	r.Use(MaxBody(0)) // <=0：未配置上限，直接放行
	var got string
	r.POST("/upload", func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		got = string(b)
		c.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, postWithBody(t, strings.Repeat("x", 1024), false))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, got, 1024)
}

// response.Err 对 *http.MaxBytesError（含错误链包装）归一 413，且按客户端错误处理
// （文案为"请求体超出大小限制"，不落入内部错误文案）。
func TestErrMapsMaxBytesErrorTo413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set(response.RequestIDKey, "req-413")

	response.Err(c, fmtWrap{err: &http.MaxBytesError{Limit: 1048576}})

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	body := decodeJSON(t, rec.Body.String())
	require.Equal(t, "COMMON_PAYLOAD_TOO_LARGE", body["code"])
	require.Equal(t, float64(1048576), body["details"].(map[string]any)["limit_bytes"])
	require.Equal(t, "req-413", body["request_id"])
	require.NotContains(t, rec.Body.String(), "内部错误")
}

// fmtWrap 以 Unwrap 链包装（验证 errors.As 穿透）。
type fmtWrap struct{ err error }

func (f fmtWrap) Error() string { return "wrap: " + f.err.Error() }
func (f fmtWrap) Unwrap() error { return f.err }
