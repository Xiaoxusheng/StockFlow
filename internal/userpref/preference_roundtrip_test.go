package userpref

// preference_roundtrip_test.go 偏好「写入值原样读回」往返 + 偏好维度用户隔离单测
// （2026-10-06 测试加固轮补缺口，ask 第 3 项「用户偏好读写」）。
//
// 既有 TestT6PreferenceWhitelist（service_test.go:217）断言白名单 400 / keys= 过滤
// 计数 / >16KB 400，TestT6bRecentVisitsTrimmed 断言裁剪——均未断言「读回的值等于写入
// 的值」与「keys= 过滤返回的是指定键本身（而非恰好同数量）」，也未覆盖偏好维度的
// 跨用户隔离（T4 仅覆盖视图维度）。本文件补齐：
//
//	1. PUT 三个白名单键（数值/字符串/对象三种 JSON 形态）→ GET ?keys= 只回指定键
//	   且 value 与写入值逐字节一致（JSON canonical 比较）；
//	2. GET 缺省只回已写键（无默认值伪造——未写键不出现），键集精确匹配；
//	3. 同键二次 PUT 覆盖（复合主键 upsert），读回为新值；
//	4. 用户 B 的 GET（含缺省全量）看不到用户 A 的任何键——偏好按 user_id 隔离，
//	   与 T4 视图隔离同口径（fakePrefRepo 按 (user_id, pref_key) 复合主键存储）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// prefItem GET 响应 items 的解码形态（models.go Preference json tag：key/value）。
type prefItem struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// compact JSON canonical 形态（去除空白差异后比较字节）。
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("value 非法 JSON: %v（%s）", err, raw)
	}
	return buf.String()
}

// mustPrefs 解码偏好列表信封。
func mustPrefs(t *testing.T, code int, body map[string]any) []prefItem {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d: %v", code, body)
	}
	raw, err := json.Marshal(dataArr(t, body))
	if err != nil {
		t.Fatalf("items 重编码失败: %v", err)
	}
	var items []prefItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("items 解码失败: %v（%s）", err, raw)
	}
	return items
}

// putPref 写一个偏好键（值任意 JSON 形态）。
func putPref(t *testing.T, e *env, key string, value any) {
	t.Helper()
	if code, body := e.do(t, http.MethodPut, "/api/user/preferences/"+key, value); code != http.StatusOK {
		t.Fatalf("PUT %s 应 200，实际 %d: %v", key, code, body)
	}
}

// getPrefs 按可选 keys= 过滤读偏好。
func getPrefs(t *testing.T, e *env, keysQuery string) []prefItem {
	t.Helper()
	path := "/api/user/preferences"
	if keysQuery != "" {
		path += "?keys=" + keysQuery
	}
	code, body := e.do(t, http.MethodGet, path, nil)
	return mustPrefs(t, code, body)
}

// prefIndex 按 key 建索引。
func prefIndex(t *testing.T, items []prefItem) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, it := range items {
		if _, dup := m[it.Key]; dup {
			t.Fatalf("key %s 重复返回: %v", it.Key, items)
		}
		m[it.Key] = compact(t, it.Value)
	}
	return m
}

func TestPreferenceRoundTripAndIsolation(t *testing.T) {
	a := newEnv(501)

	// 三种 JSON 形态写入（避开 recent_visits 的 20 条裁剪语义——T6b 已覆盖）。
	putPref(t, a, "default_warehouse_id", map[string]any{"warehouse_id": 3})
	putPref(t, a, "last_printer_id", "PRT-01")
	putPref(t, a, "recent_filters", map[string]any{"page_key": "stock.list", "hidden": []string{"qty"}})

	// 1. keys= 过滤：只回指定键，value 逐字节往返一致。
	items := getPrefs(t, a, "last_printer_id,recent_filters")
	if len(items) != 2 {
		t.Fatalf("keys= 过滤应恰 2 项，实际 %d: %v", len(items), items)
	}
	idx := prefIndex(t, items)
	if _, ok := idx["default_warehouse_id"]; ok {
		t.Fatalf("keys= 未请求的键不应返回: %v", idx)
	}
	if idx["last_printer_id"] != `"PRT-01"` {
		t.Fatalf("last_printer_id 往返不一致: %s", idx["last_printer_id"])
	}
	wantFilters := `{"hidden":["qty"],"page_key":"stock.list"}`
	if idx["recent_filters"] != wantFilters {
		t.Fatalf("recent_filters 往返不一致: %s（期望 %s）", idx["recent_filters"], wantFilters)
	}

	// 2. 缺省全量：只回已写 3 键（无默认值伪造），未写键不出现。
	all := prefIndex(t, getPrefs(t, a, ""))
	if len(all) != 3 {
		t.Fatalf("缺省应只回已写 3 键（无默认值伪造），实际 %d: %v", len(all), all)
	}
	for _, k := range []string{"default_warehouse_id", "last_printer_id", "recent_filters"} {
		if _, ok := all[k]; !ok {
			t.Fatalf("已写键 %s 缺失: %v", k, all)
		}
	}

	// 3. 同键二次 PUT 覆盖（upsert），读回为新值。
	putPref(t, a, "last_printer_id", "PRT-02")
	again := prefIndex(t, getPrefs(t, a, "last_printer_id"))
	if again["last_printer_id"] != `"PRT-02"` {
		t.Fatalf("upsert 后应读回新值，实际 %s", again["last_printer_id"])
	}

	// 4. 用户 B 隔离：缺省全量与 keys= 定向读取均看不到 A 的任何键。
	b := newEnv(502)
	if items := getPrefs(t, b, ""); len(items) != 0 {
		t.Fatalf("B 缺省读取应为空，实际 %v", items)
	}
	if items := getPrefs(t, b, "last_printer_id,recent_filters"); len(items) != 0 {
		t.Fatalf("B 定向读取应为空，实际 %v", items)
	}
}
