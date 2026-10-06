package idempotency

// 中间件 HTTP 层单测（效率层一期 ask 测试矩阵的 HTTP 形态）：
//   - 首次真实执行 + 重复提交回放首次结果（执行探针计数=1）；
//   - 并发双请求（同键同体同时到达）恰一 200，余者 409 IDEMPOTENCY_IN_PROGRESS；
//   - 缺键（required）/键非法/换载荷/跨用户维度行为；
//   - panic 释放占用（同键重试不永久卡死）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
	"github.com/stockflow/server/internal/response"
)

// sfAuthUserKey auth.CurrentUser 上下文键（auth.ctxUserKey 为包私有，测试侧
// 字面量注入——datax/handler_test.go 同款口径）。
const sfAuthUserKey = "sf_auth_user"

// mwEngine 装配 gin 引擎：注入认证上下文 + request_id 桩 + 幂等中间件 + 探针端点。
// probe 原子递增并返回执行序号（信封 data.n），用于断言「只执行一次」。
func mwEngine(t *testing.T, svc *Service, required bool, probe *atomic.Int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery()) // 承接幂等中间件 panic 释放路径的再上抛
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op", IsSuper: true})
		c.Set(response.RequestIDKey, "req-current")
		c.Next()
	})
	api := r.Group("/api")
	h := func(c *gin.Context) {
		n := probe.Add(1)
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "message": "ok",
			"data":       gin.H{"n": n},
			"request_id": c.GetString(response.RequestIDKey),
		})
	}
	if required {
		api.POST("/receipts", RequiredMiddleware(svc), h)
	} else {
		api.POST("/receipts", Middleware(svc), h)
	}
	return r
}

func mwDo(t *testing.T, r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/receipts", strings.NewReader(body))
	if key != "" {
		req.Header.Set(IdempotencyKeyHeader, key)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestMiddlewareFirstExecutesSecondReplays 重复提交返回首次结果（执行探针恒 1），
// 回放体改写当前 request_id。
func TestMiddlewareFirstExecutesSecondReplays(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), false, &probe)
	body := `{"receipt_no":"REC-1","qty":1}`

	rec1 := mwDo(t, r, tKey, body)
	if rec1.Code != http.StatusOK || probe.Load() != 1 {
		t.Fatalf("首次应真实执行: code=%d probe=%d", rec1.Code, probe.Load())
	}
	if !strings.Contains(rec1.Body.String(), `"n":1`) {
		t.Fatalf("首次响应不符: %s", rec1.Body.String())
	}

	rec2 := mwDo(t, r, tKey, body)
	if probe.Load() != 1 {
		t.Fatalf("重复提交不得再执行（幂等只执行一次）: probe=%d", probe.Load())
	}
	if rec2.Body.String() != rec1.Body.String() {
		t.Fatalf("回放应返回首次结果: %s vs %s", rec2.Body.String(), rec1.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"request_id":"req-current"`) {
		t.Fatalf("回放体 request_id 应改写为当前请求: %s", rec2.Body.String())
	}
}

// TestMiddlewareConcurrentDoubleSubmitOneExecution 并发双确认只成功一次（ask 硬性
// 测试项）：同键同体两请求同时到达。恒真不变量 = 业务探针恰 1（只执行一次）；
// 每路响应必为二者之一：409 IDEMPOTENCY_IN_PROGRESS（Acquire 先于胜者收尾）或
// 200 回放体=首次响应（Acquire 晚于收尾——快照已就绪，仍不执行业务）。
func TestMiddlewareConcurrentDoubleSubmitOneExecution(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), false, &probe)
	body := `{"receipt_no":"REC-9","qty":3}`

	start := make(chan struct{})
	var wg sync.WaitGroup
	type outcome struct {
		code int
		body string
	}
	outcomes := make([]outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/receipts", strings.NewReader(body))
			req.Header.Set(IdempotencyKeyHeader, tKey)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			outcomes[i] = outcome{code: rec.Code, body: rec.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()

	if probe.Load() != 1 {
		t.Fatalf("并发双请求只允许一个执行，探针=%d", probe.Load())
	}
	valid := func(o outcome) bool {
		if o.code == http.StatusConflict && strings.Contains(o.body, "IDEMPOTENCY_IN_PROGRESS") {
			return true
		}
		// 回放路径：200 且响应体与首次真实执行一致（探针=1 时首次响应 n=1）。
		return o.code == http.StatusOK && strings.Contains(o.body, `"n":1`)
	}
	if !valid(outcomes[0]) || !valid(outcomes[1]) {
		t.Fatalf("并发双请求响应不合法（应 409 冲突或 200 回放）: %+v", outcomes)
	}
	if outcomes[0].code == http.StatusOK && outcomes[1].code == http.StatusOK &&
		outcomes[0].body != outcomes[1].body {
		t.Fatalf("双 200 时响应体必须一致（回放首次结果）: %+v", outcomes)
	}
}

// TestMiddlewareBodyMismatch 同键换载荷 → 409 IDEMPOTENCY_REQUEST_MISMATCH。
func TestMiddlewareBodyMismatch(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), false, &probe)
	if rec := mwDo(t, r, tKey, `{"a":1}`); rec.Code != http.StatusOK {
		t.Fatalf("首次应 200: %d", rec.Code)
	}
	rec := mwDo(t, r, tKey, `{"a":2}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_REQUEST_MISMATCH") {
		t.Fatalf("换载荷应 409 IDEMPOTENCY_REQUEST_MISMATCH: %d %s", rec.Code, rec.Body.String())
	}
	if probe.Load() != 1 {
		t.Fatalf("换载荷请求不得执行业务: probe=%d", probe.Load())
	}
}

// TestMiddlewareUserIsolation 同键不同用户各自执行（user_id 维度隔离）。
func TestMiddlewareUserIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewService(newFakeRepo(), nil)
	var probe atomic.Int64
	r := gin.New()
	r.Use(func(c *gin.Context) {
		uid := int64(7)
		if c.GetHeader("X-Test-User") == "b" {
			uid = 8
		}
		c.Set(sfAuthUserKey, auth.UserContext{UserID: uid, Username: "op"})
		c.Next()
	})
	r.POST("/api/receipts", Middleware(svc), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "n": probe.Add(1)})
	})
	if rec := mwDo(t, r, tKey, `{"a":1}`); rec.Code != http.StatusOK {
		t.Fatalf("用户 A 首次应 200: %d", rec.Code)
	}
	// 用户 B 同键同体：独立占用执行（探针=2）。
	req := httptest.NewRequest(http.MethodPost, "/api/receipts", strings.NewReader(`{"a":1}`))
	req.Header.Set(IdempotencyKeyHeader, tKey)
	req.Header.Set("X-Test-User", "b")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || probe.Load() != 2 {
		t.Fatalf("用户 B 应独立执行: code=%d probe=%d", rec.Code, probe.Load())
	}
	// 用户 A 再次提交：回放（探针不变）。
	if rec := mwDo(t, r, tKey, `{"a":1}`); rec.Code != http.StatusOK || probe.Load() != 2 {
		t.Fatalf("用户 A 重复提交应回放: code=%d probe=%d", rec.Code, probe.Load())
	}
}

// TestMiddlewareNoKeyOptionalPassThrough 键可选模式：无键请求不拦截（每次执行）。
func TestMiddlewareNoKeyOptionalPassThrough(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), false, &probe)
	for i := 1; i <= 2; i++ {
		if rec := mwDo(t, r, "", `{"a":1}`); rec.Code != http.StatusOK {
			t.Fatalf("无键请求应放行: %d", rec.Code)
		}
	}
	if probe.Load() != 2 {
		t.Fatalf("无键请求每次执行: probe=%d", probe.Load())
	}
}

// TestRequiredMiddlewareMissingKey 键必需模式：缺头 400（fail-closed）。
func TestRequiredMiddlewareMissingKey(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), true, &probe)
	rec := mwDo(t, r, "", `{"a":1}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("缺键应 400 IDEMPOTENCY_KEY_REQUIRED: %d %s", rec.Code, rec.Body.String())
	}
	if probe.Load() != 0 {
		t.Fatalf("缺键请求不得触达业务: probe=%d", probe.Load())
	}
}

// TestRequiredMiddlewareInvalidKey 键非法 400（值域与 000024 CHECK 同式）。
func TestRequiredMiddlewareInvalidKey(t *testing.T) {
	var probe atomic.Int64
	r := mwEngine(t, NewService(newFakeRepo(), nil), true, &probe)
	rec := mwDo(t, r, "bad key!", `{"a":1}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_INVALID") {
		t.Fatalf("非法键应 400: %d %s", rec.Code, rec.Body.String())
	}
}

// TestMiddlewareReplaysErrorEnvelope 首次执行产出业务错误信封（4xx）时同样落快照并
// 回放——「重复提交返回首次结果」含失败结果。
func TestMiddlewareReplaysErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op"})
		c.Next()
	})
	var exec atomic.Int64
	r.POST("/api/receipts", Middleware(NewService(newFakeRepo(), nil)), func(c *gin.Context) {
		exec.Add(1)
		response.Err(c, response.NewError(response.CodeInvalidParam, map[string]any{"reason": "数量必须为正数"}))
	})
	body := `{"qty":-1}`
	rec1 := mwDo(t, r, tKey, body)
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("首次错误响应应 400: %d", rec1.Code)
	}
	rec2 := mwDo(t, r, tKey, body)
	if exec.Load() != 1 {
		t.Fatalf("错误响应亦应幂等回放: exec=%d", exec.Load())
	}
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("回放应保持首次状态码: %d", rec2.Code)
	}
	// 回放经信封体 request_id 改写后重序列化——JSON 对象键序不保证逐字节一致，
	// 以语义等价断言（解析后 DeepEqual）。
	var v1, v2 any
	if err := json.Unmarshal(rec1.Body.Bytes(), &v1); err != nil {
		t.Fatalf("首次响应体非法: %v", err)
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &v2); err != nil {
		t.Fatalf("回放响应体非法: %v", err)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("回放应保持首次错误信封（语义等价）:\n%s\n%s", rec1.Body.String(), rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"request_id":""`) {
		t.Fatalf("无 request_id 桩时回放体保持原值: %s", rec2.Body.String())
	}
}

// TestMiddlewarePanicReleasesKey 执行中断（panic）：占用释放、Recovery 兜底 500、
// 同键重试重新执行（不永久卡 409）。
func TestMiddlewarePanicReleasesKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op"})
		c.Next()
	})
	var exec atomic.Int64
	r.POST("/api/receipts", Middleware(svc), func(c *gin.Context) {
		if exec.Add(1) == 1 {
			panic("模拟执行中断")
		}
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})

	rec1 := mwDo(t, r, tKey, `{"a":1}`)
	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应由 Recovery 兜底 500: %d", rec1.Code)
	}
	rec2 := mwDo(t, r, tKey, `{"a":1}`)
	if rec2.Code != http.StatusOK || exec.Load() != 2 {
		t.Fatalf("panic 释放后同键重试应重新执行: code=%d exec=%d", rec2.Code, exec.Load())
	}
}

// TestMiddlewarePreservesRequestBody 中间件整读请求体计算 hash 后必须回填
// c.Request.Body——业务 handler 仍可完整读取（防"hash 后空体"回归）。
func TestMiddlewarePreservesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op"})
		c.Next()
	})
	var got string
	r.POST("/api/receipts", Middleware(NewService(newFakeRepo(), nil)), func(c *gin.Context) {
		raw, err := c.GetRawData()
		if err != nil {
			response.Err(c, response.NewError(response.CodeInvalidParam, nil))
			return
		}
		got = string(raw)
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	body := `{"receipt_no":"REC-1","qty":1,"memo":"收货确认"}`
	if rec := mwDo(t, r, tKey, body); rec.Code != http.StatusOK {
		t.Fatalf("应 200: %d", rec.Code)
	}
	if got != body {
		t.Fatalf("下游 handler 应读到完整请求体: got=%q", got)
	}
}

// TestMiddlewareUnauthenticated401 未认证请求 fail-closed 401（auth.AuthRequired
// 未挂载的直调路径防御）。
func TestMiddlewareUnauthenticated401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/receipts", Middleware(NewService(newFakeRepo(), nil)), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	req := httptest.NewRequest(http.MethodPost, "/api/receipts", strings.NewReader(`{}`))
	req.Header.Set(IdempotencyKeyHeader, tKey)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401: %d", rec.Code)
	}
}

// TestMiddlewareReadMethodsUntouched 读方法不拦截（计划 §2.10：GET 幂等不做）。
func TestMiddlewareReadMethodsUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/receipts", Middleware(NewService(newFakeRepo(), nil)), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/receipts", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("GET 不应被幂等中间件拦截: %d %s", rec.Code, rec.Body.String())
	}
}

// TestMiddlewareSnapshotGuardByHandlerSize 捕获上限守卫：响应超过快照上限时释放
// 占用（不缓存），下一次同键提交重新执行（诚实降级，日志披露）。
func TestMiddlewareSnapshotGuardByHandlerSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewService(newFakeRepo(), nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(sfAuthUserKey, auth.UserContext{UserID: 7, Username: "op"})
		c.Next()
	})
	var exec atomic.Int64
	r.POST("/api/receipts", Middleware(svc), func(c *gin.Context) {
		exec.Add(1)
		// 捕获缓冲超 maxSnapshotBytes 的响应（合法 JSON，仅体积超限）。
		c.Data(http.StatusOK, "application/json",
			[]byte(fmt.Sprintf(`{"code":0,"data":"%s"}`, strings.Repeat("x", maxSnapshotBytes))))
	})
	if rec := mwDo(t, r, tKey, `{"a":1}`); rec.Code != http.StatusOK {
		t.Fatalf("超大响应首次仍应正常下发: %d", rec.Code)
	}
	rec := mwDo(t, r, tKey, `{"a":1}`)
	if rec.Code != http.StatusOK || exec.Load() != 2 {
		t.Fatalf("超限快照应释放占用（重试重新执行）: code=%d exec=%d", rec.Code, exec.Load())
	}
}
