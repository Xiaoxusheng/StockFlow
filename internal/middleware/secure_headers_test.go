package middleware

// SecureHeaders 单测：全局安全响应头必须逐项落位（安全渗透修复）。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecureHeadersSetsAllHeaders(t *testing.T) {
	r := newGin()
	r.Use(SecureHeaders())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"))
}

func TestSecureHeadersAppliesToErrorResponse(t *testing.T) {
	// 错误响应同样必须携带安全头（全局中间件，不区分成功/失败路径）。
	r := newGin()
	r.Use(SecureHeaders())
	r.GET("/boom", func(c *gin.Context) { c.AbortWithStatus(http.StatusInternalServerError) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"))
}
