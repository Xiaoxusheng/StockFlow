package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTestContext(t *testing.T, url string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, url, nil)
	return c, rec
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	return m
}

func TestOKEnvelope(t *testing.T) {
	c, rec := newTestContext(t, "/")
	c.Set(RequestIDKey, "req-1")
	OK(c, gin.H{"hello": "world"})

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, float64(0), body["code"]) // 成功 code=0（数字，api.md §2.2）
	require.Equal(t, "ok", body["message"])
	require.Equal(t, "req-1", body["request_id"])
	require.Equal(t, map[string]any{"hello": "world"}, body["data"])
	require.NotContains(t, body, "details") // 成功无 details（backend-m1-plan §1）
}

func TestOKNilDataStillHasField(t *testing.T) {
	c, rec := newTestContext(t, "/")
	OK(c, nil)
	body := decode(t, rec.Body.String())
	require.Contains(t, body, "data")
	require.Nil(t, body["data"])
}

func TestErrBusinessErrorWithDetails(t *testing.T) {
	code := Register("TEST_SAMPLE_NOT_FOUND", "示例不存在", http.StatusNotFound)
	c, rec := newTestContext(t, "/")
	c.Set(RequestIDKey, "req-2")
	Err(c, NewError(code, gin.H{"id": "7"}))

	require.Equal(t, http.StatusNotFound, rec.Code) // HTTP 状态来自注册值
	body := decode(t, rec.Body.String())
	require.Equal(t, "TEST_SAMPLE_NOT_FOUND", body["code"]) // 失败 code 为字符串错误码
	require.Equal(t, "示例不存在", body["message"])
	require.Equal(t, "req-2", body["request_id"])
	require.Equal(t, map[string]any{"id": "7"}, body["details"]) // details 结构化补充（api.md §4）
}

func TestErrUnknownErrorHidesInternals(t *testing.T) {
	c, rec := newTestContext(t, "/")
	Err(c, assertNotImplementedErr())

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decode(t, rec.Body.String())
	require.Equal(t, "COMMON_INTERNAL_ERROR", body["code"])
	require.NotContains(t, rec.Body.String(), "db-secret") // 内部细节不外泄（architecture.md §6）
	require.Equal(t, "系统内部错误，请稍后重试", body["message"])
}

type sentinelErr struct{}

func (sentinelErr) Error() string { return "db-secret should not leak" }

func assertNotImplementedErr() error { return sentinelErr{} }

func TestAsError(t *testing.T) {
	code := Register("TEST_AS_ERROR", "业务错误", http.StatusBadRequest)
	require.Equal(t, "TEST_AS_ERROR", AsError(NewError(code, nil)).code.Code)
	require.Equal(t, "COMMON_INTERNAL_ERROR", AsError(sentinelErr{}).code.Code)
	require.Equal(t, "COMMON_INTERNAL_ERROR", AsError(nil).code.Code)
}

func TestErrorWithMessage(t *testing.T) {
	code := Register("TEST_WITH_MSG", "默认文案", http.StatusBadRequest)
	e := NewError(code, nil).WithMessage("第 3 行 SKU 不存在")
	require.Equal(t, "第 3 行 SKU 不存在", e.Message())
	require.Contains(t, e.Error(), "TEST_WITH_MSG")
}

func TestRegisterDuplicatePanics(t *testing.T) {
	require.NotPanics(t, func() {
		Register("TEST_DUP_SAME", "完全相同重复注册幂等", http.StatusBadRequest)
		Register("TEST_DUP_SAME", "完全相同重复注册幂等", http.StatusBadRequest)
	})
	require.Panics(t, func() {
		Register("TEST_DUP_DIFF", "第一次", http.StatusBadRequest)
		Register("TEST_DUP_DIFF", "第二次", http.StatusUnauthorized)
	})
}

func TestRegisterInvalidArgs(t *testing.T) {
	require.Panics(t, func() { Register("", "空码", http.StatusBadRequest) })
	require.Panics(t, func() { Register("TEST_EMPTY_MSG", "", http.StatusBadRequest) })
	require.Panics(t, func() { Register("TEST_BAD_STATUS", "状态越界", http.StatusOK) })
}

func TestLookup(t *testing.T) {
	Register("TEST_LOOKUP", "可查询", http.StatusConflict)
	c, ok := Lookup("TEST_LOOKUP")
	require.True(t, ok)
	require.Equal(t, http.StatusConflict, c.HTTPStatus)

	_, ok = Lookup("TEST_NOT_REGISTERED")
	require.False(t, ok)
}

func TestParsePage(t *testing.T) {
	t.Run("默认值", func(t *testing.T) {
		c, _ := newTestContext(t, "/items")
		page, pageSize, err := ParsePage(c)
		require.NoError(t, err)
		require.Equal(t, DefaultPage, page)
		require.Equal(t, DefaultPageSize, pageSize)
	})
	t.Run("合法入参", func(t *testing.T) {
		c, _ := newTestContext(t, "/items?page=2&pageSize=50")
		page, pageSize, err := ParsePage(c)
		require.NoError(t, err)
		require.Equal(t, 2, page)
		require.Equal(t, 50, pageSize)
	})
	t.Run("page 非法", func(t *testing.T) {
		c, _ := newTestContext(t, "/items?page=0")
		_, _, err := ParsePage(c)
		require.Error(t, err)
	})
	t.Run("pageSize 超上限", func(t *testing.T) {
		c, _ := newTestContext(t, "/items?pageSize=101")
		_, _, err := ParsePage(c)
		require.Error(t, err)
	})
}

func TestOKPageShape(t *testing.T) {
	c, rec := newTestContext(t, "/items")
	OKPage(c, []string{"a"}, 2, 50, 101)

	body := decode(t, rec.Body.String())
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	// 出参 {page,pageSize,total,items}（api.md §2.1）
	require.Equal(t, float64(2), data["page"])
	require.Equal(t, float64(50), data["pageSize"])
	require.Equal(t, float64(101), data["total"])
	require.Equal(t, []any{"a"}, data["items"])
}
