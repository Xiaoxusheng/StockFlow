package userpref

// 服务/端点级单测（计划 §7.1 T4/T5/T6，HTTP 链路直驱：内存仓储 + gin + 用户上下文
// 注入键 "sf_auth_user"，internal/auth/middleware.go ctxUserKey 冻结口径——
// sysops/masterdata 同款先例）。同包串行，禁用 t.Parallel。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
)

// ctxUserKeyMirror 镜像 internal/auth/middleware.go ctxUserKey（"sf_auth_user"）。
const ctxUserKeyMirror = "sf_auth_user"

// env 构造以指定用户身份发请求的引擎（认证即可用端点的单测替身）。
type env struct {
	t      *gin.Engine
	views  *fakeViewRepo
	prefs  *fakePrefRepo
	userID int64
}

func newEnv(userID int64) *env {
	gin.SetMode(gin.TestMode)
	e := &env{
		views:  newFakeViewRepo(),
		prefs:  newFakePrefRepo(),
		userID: userID,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(ctxUserKeyMirror, auth.UserContext{UserID: userID, Username: "tester"})
		c.Next()
	})
	RegisterRoutes(r.Group("/api"), nil, WithService(NewService(e.views, e.prefs)))
	e.t = r
	return e
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	payload := []byte(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("构造请求体失败: %v", err)
		}
		payload = b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.t.ServeHTTP(rec, req)
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

// dataObj 取信封 data 字段（对象形态）。
func dataObj(env map[string]any) map[string]any {
	d, _ := env["data"].(map[string]any)
	return d
}

// dataArr 取信封 data 字段（数组形态）。
func dataArr(t *testing.T, env map[string]any) []any {
	t.Helper()
	d, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("data 应为数组: %v", env["data"])
	}
	return d
}

func codeOf(t *testing.T, env map[string]any) string {
	t.Helper()
	code, _ := env["code"].(string)
	return code
}

// ---- T4 视图用户隔离：A 建视图，B GET/PUT/DELETE → 404；A 列表不见 B 的行 ----

func TestT4ViewUserIsolation(t *testing.T) {
	a := newEnv(101)
	b := newEnv(102)

	// A 建视图。
	code, body := a.do(t, http.MethodPost, "/api/user/views", map[string]any{
		"page_key": "stock.list", "name": "我的视图",
	})
	if code != http.StatusOK {
		t.Fatalf("A 建视图应 200，实际 %d: %v", code, body)
	}
	viewID, _ := dataObj(body)["id"].(string)
	if viewID == "" {
		t.Fatalf("创建结果应含字符串形态 id: %v", dataObj(body))
	}

	// B 按 (id, user_id) 访问 → 一律 404 防探测（PUT/DELETE 走归属检查）。
	for _, tc := range []struct{ m, p string }{
		{http.MethodPut, "/api/user/views/" + viewID},
		{http.MethodDelete, "/api/user/views/" + viewID},
	} {
		var payload any
		if tc.m == http.MethodPut {
			payload = map[string]any{"name": "抢占"}
		}
		code, body := b.do(t, tc.m, tc.p, payload)
		if code != http.StatusNotFound {
			t.Fatalf("B %s %s 应 404，实际 %d: %v", tc.m, tc.p, code, body)
		}
		if code := codeOf(t, body); code != "USERPREF_VIEW_NOT_FOUND" {
			t.Fatalf("应返回 USERPREF_VIEW_NOT_FOUND，实际 %s", code)
		}
	}

	// A 列表恰 1 行（自己的），B 列表为空数组（items 空为 []）。
	code, body = a.do(t, http.MethodGet, "/api/user/views?page_key=stock.list", nil)
	if code != http.StatusOK {
		t.Fatalf("A 列表应 200，实际 %d", code)
	}
	if items := dataArr(t, body); len(items) != 1 {
		t.Fatalf("A 列表应恰 1 行，实际 %d", len(items))
	}
	code, body = b.do(t, http.MethodGet, "/api/user/views?page_key=stock.list", nil)
	if code != http.StatusOK {
		t.Fatalf("B 列表应 200，实际 %d", code)
	}
	if items := dataArr(t, body); len(items) != 0 {
		t.Fatalf("B 列表应为空数组，实际 %v", items)
	}
}

// ---- T5 默认唯一：设新默认后旧默认自动清除；任一时刻同页签至多一个默认 ----

func TestT5DefaultUniqueness(t *testing.T) {
	e := newEnv(201)

	// 视图 1 设为默认。
	code, body := e.do(t, http.MethodPost, "/api/user/views", map[string]any{
		"page_key": "stock.list", "name": "v1", "is_default": true,
	})
	if code != http.StatusOK {
		t.Fatalf("建默认视图应 200，实际 %d: %v", code, body)
	}
	id1, _ := dataObj(body)["id"].(string)
	assertExactlyOneDefault(t, e, "stock.list", id1)

	// 视图 2 再设默认 → 单事务内清除旧默认。
	code, body = e.do(t, http.MethodPost, "/api/user/views", map[string]any{
		"page_key": "stock.list", "name": "v2", "is_default": true,
	})
	if code != http.StatusOK {
		t.Fatalf("建第二个默认视图应 200，实际 %d: %v", code, body)
	}
	id2, _ := dataObj(body)["id"].(string)
	assertExactlyOneDefault(t, e, "stock.list", id2)

	// PUT 恢复视图 1 为默认 → 视图 2 被清除。
	code, _ = e.do(t, http.MethodPut, "/api/user/views/"+id1, map[string]any{"is_default": true})
	if code != http.StatusOK {
		t.Fatalf("PUT 设默认应 200，实际 %d", code)
	}
	assertExactlyOneDefault(t, e, "stock.list", id1)

	// 「恢复默认」= PUT is_default=false → 该页签无默认（合法态）。
	code, _ = e.do(t, http.MethodPut, "/api/user/views/"+id1, map[string]any{"is_default": false})
	if code != http.StatusOK {
		t.Fatalf("PUT 取消默认应 200，实际 %d", code)
	}
	for _, it := range dataArr(t, mustList(t, e, "stock.list")) {
		if m, _ := it.(map[string]any)["is_default"].(bool); m {
			t.Fatalf("取消后不应残留默认视图: %v", it)
		}
	}
}

// mustList 拉取页签视图列表。
func mustList(t *testing.T, e *env, pageKey string) map[string]any {
	t.Helper()
	code, body := e.do(t, http.MethodGet, "/api/user/views?page_key="+pageKey, nil)
	if code != http.StatusOK {
		t.Fatalf("列表应 200，实际 %d: %v", code, body)
	}
	return body
}

// assertExactlyOneDefault 断言列表中仅 expectID 为默认。
func assertExactlyOneDefault(t *testing.T, e *env, pageKey, expectID string) {
	t.Helper()
	defaults := 0
	for _, it := range dataArr(t, mustList(t, e, pageKey)) {
		m, _ := it.(map[string]any)
		if m["is_default"] == true {
			defaults++
			if m["id"] != expectID {
				t.Fatalf("默认视图应为 %s，实际 %v", expectID, m["id"])
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("应恰 1 个默认视图，实际 %d", defaults)
	}
}

// ---- T6 偏好白名单：白名单外 400；GET ?keys= 过滤生效；>16KB 400 ----

func TestT6PreferenceWhitelist(t *testing.T) {
	e := newEnv(301)

	// 白名单外 key → 400 COMMON_INVALID_PARAM。
	code, body := e.do(t, http.MethodPut, "/api/user/preferences/hacker_key", map[string]any{"x": 1})
	if code != http.StatusBadRequest || codeOf(t, body) != "COMMON_INVALID_PARAM" {
		t.Fatalf("白名单外 key 应 400 invalidParam，实际 %d %v", code, body)
	}

	// 白名单内 7 键全部可写。
	for _, k := range WhitelistedPrefKeys {
		code, body = e.do(t, http.MethodPut, "/api/user/preferences/"+k, map[string]any{"k": k})
		if code != http.StatusOK {
			t.Fatalf("写白名单 key %s 应 200，实际 %d: %v", k, code, body)
		}
	}

	// GET ?keys= 过滤生效：只返回指定键。
	code, body = e.do(t, http.MethodGet, "/api/user/preferences?keys=last_printer_id,recent_filters", nil)
	if code != http.StatusOK {
		t.Fatalf("keys 过滤应 200，实际 %d %v", code, body)
	}
	if items := dataArr(t, body); len(items) != 2 {
		t.Fatalf("keys 过滤应返回 2 项，实际 %d", len(items))
	}

	// keys 含白名单外键 → 400。
	code, body = e.do(t, http.MethodGet, "/api/user/preferences?keys=recent_visits,evil", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("keys 含白名单外键应 400，实际 %d %v", code, body)
	}

	// 缺省返回白名单全量 7 项。
	code, body = e.do(t, http.MethodGet, "/api/user/preferences", nil)
	if code != http.StatusOK {
		t.Fatalf("缺省全量应 200，实际 %d %v", code, body)
	}
	if items := dataArr(t, body); len(items) != 7 {
		t.Fatalf("缺省应返回 7 项，实际 %d", len(items))
	}

	// >16KB → 400。
	big := make([]string, 2100) // ≈ 27KB
	for i := range big {
		big[i] = strings.Repeat("a", 12)
	}
	code, body = e.do(t, http.MethodPut, "/api/user/preferences/recent_visits", big)
	if code != http.StatusBadRequest || codeOf(t, body) != "COMMON_INVALID_PARAM" {
		t.Fatalf(">16KB 应 400 invalidParam，实际 %d %v", code, body)
	}
}

// TestT6bRecentVisitsTrimmed recent_visits 服务端裁剪至 20 条（保留前 20 条，前端约定最新在前）。
func TestT6bRecentVisitsTrimmed(t *testing.T) {
	e := newEnv(302)

	visits := make([]map[string]any, 25)
	for i := range visits {
		visits[i] = map[string]any{
			"path": fmt.Sprintf("/page/%02d", i), "title": "页", "visited_at": "2026-10-06 10:00:00",
		}
	}
	code, body := e.do(t, http.MethodPut, "/api/user/preferences/recent_visits", visits)
	if code != http.StatusOK {
		t.Fatalf("写 recent_visits 应 200，实际 %d: %v", code, body)
	}
	stored, ok := dataObj(body)["value"].([]any)
	if !ok {
		t.Fatalf("value 应为数组: %v", dataObj(body)["value"])
	}
	if len(stored) != 20 {
		t.Fatalf("recent_visits 应裁剪至 20 条，实际 %d", len(stored))
	}
	first, _ := stored[0].(map[string]any)
	if first["path"] != "/page/00" {
		t.Fatalf("应保留前 20 条，实际首条 %v", first)
	}
}

// ---- 校验面补充：page_key 正则 / 重名 409 / page_size 1-100 / json 列 8KB / 缺省值 ----

func TestViewValidation(t *testing.T) {
	e := newEnv(401)

	// page_key 正则。
	code, body := e.do(t, http.MethodPost, "/api/user/views", map[string]any{"page_key": "Bad Key!", "name": "v"})
	if code != http.StatusBadRequest || codeOf(t, body) != "COMMON_INVALID_PARAM" {
		t.Fatalf("非法 page_key 应 400 invalidParam，实际 %d %v", code, body)
	}

	// 重名 409（含改名撞名）。
	if code, _ := e.do(t, http.MethodPost, "/api/user/views", map[string]any{"page_key": "stock.list", "name": "v"}); code != http.StatusOK {
		t.Fatalf("首建应 200")
	}
	code, body = e.do(t, http.MethodPost, "/api/user/views", map[string]any{"page_key": "stock.list", "name": "v"})
	if code != http.StatusConflict || codeOf(t, body) != "USERPREF_VIEW_NAME_CONFLICT" {
		t.Fatalf("重名应 409，实际 %d %v", code, body)
	}

	// page_size 越界 400；json 列 >8KB 400。
	code, _ = e.do(t, http.MethodPost, "/api/user/views", map[string]any{"page_key": "stock.list", "name": "v3", "page_size": 101})
	if code != http.StatusBadRequest {
		t.Fatalf("page_size 101 应 400，实际 %d", code)
	}
	code, _ = e.do(t, http.MethodPost, "/api/user/views", map[string]any{
		"page_key": "stock.list", "name": "v4", "filters_json": map[string]any{"blob": strings.Repeat("x", 9000)},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("filters_json >8KB 应 400，实际 %d", code)
	}

	// 缺省值：filters/sort='{}'、columns='[]'、page_size=20；id/时间对外形态。
	_, body = e.do(t, http.MethodPost, "/api/user/views", map[string]any{"page_key": "stock.list", "name": "v5"})
	d := dataObj(body)
	fs, _ := json.Marshal(d["filters_json"])
	cs, _ := json.Marshal(d["columns_json"])
	if string(fs) != "{}" || string(cs) != "[]" {
		t.Fatalf("缺省 json 列应为 {}/[]，实际 %s/%s", fs, cs)
	}
	if ps, _ := d["page_size"].(float64); ps != 20 {
		t.Fatalf("缺省 page_size 应 20，实际 %v", d["page_size"])
	}
	if id, _ := d["id"].(string); id == "" {
		t.Fatalf("id 应为字符串形态: %v", d["id"])
	}
	if ts, _ := d["created_at"].(string); !strings.Contains(ts, " ") || len(ts) != 19 {
		t.Fatalf("created_at 应为 YYYY-MM-DD HH:mm:ss，实际 %v", d["created_at"])
	}
}
