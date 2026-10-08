package idempotency

import (
	"encoding/json"
	"testing"
)

// TestJsonbValueNonNull 守住 response_snapshot 的 NOT NULL 落库形态（回归防线）。
//
// 背景（2026-10-07 全流程实测）：首次占用的 PROCESSING 行 Key.ResponseSnapshot 为
// nil，原 Value() 落 NULL → `null value in column "response_snapshot" violates
// not-null constraint`（SQLSTATE 23502），带 Idempotency-Key 的首请求整事务回滚必现
// 500（POST /api/receipts、POST /api/packing 实测命中）。000024 列定义是
// NOT NULL DEFAULT '{}'，故 nil/空一律须落 '{}'（列缺省同形）。
func TestJsonbValueNonNull(t *testing.T) {
	cases := []struct {
		name string
		in   jsonb
		want string
	}{
		{"nil（首次占用 PROCESSING 行）", nil, "{}"},
		{"空切片", jsonb{}, "{}"},
		{"正常快照", jsonb(`{"status":200,"body":{"code":0}}`), `{"status":200,"body":{"code":0}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := tc.in.Value()
			if err != nil {
				t.Fatalf("Value(): %v", err)
			}
			s, ok := v.(string)
			if !ok {
				t.Fatalf("Value() 返回 %T，期望 string", v)
			}
			if s != tc.want {
				t.Errorf("Value() = %q，期望 %q", s, tc.want)
			}
			if v == nil {
				t.Error("Value() 返回 nil（落库即 NULL，违反 NOT NULL 约束）")
			}
		})
	}
}

// TestSnapshotRoundTrip 快照序列化/解析闭环（PROCESSING 占位 '{}' 走损坏自愈路径，
// 不参与回放——parseSnapshot 对空快照报错，Acquire 释放脏行后按 IN_PROGRESS 拒绝）。
func TestSnapshotRoundTrip(t *testing.T) {
	raw, err := json.Marshal(Snapshot{Status: 200, Body: json.RawMessage(`{"code":0,"data":null}`)})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := parseSnapshot(jsonb(raw))
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if snap.Status != 200 {
		t.Errorf("status = %d，期望 200", snap.Status)
	}
	if _, err := parseSnapshot(jsonb("{}")); err == nil {
		t.Error("PROCESSING 占位空快照应被 parseSnapshot 判为损坏")
	}
}
