package batchresult

// 计数收敛单测（效率层一期计划 §7.1 T8 口径：计数与 results 逐条对账）。

import (
	"encoding/json"
	"testing"
)

// TestBuilderCounts 对账：total = success+failed+skipped = len(results)，顺序保持。
func TestBuilderCounts(t *testing.T) {
	b := NewBuilder(4)
	b.Success("1").Failed("2", "PRINT_SKU_DISABLED").Skipped("3").Success("4")
	got := b.Result()

	want := Result{
		Total: 4, SuccessCount: 2, FailedCount: 1, SkippedCount: 1,
		Results: []Item{
			{ID: "1", Status: StatusSuccess},
			{ID: "2", Status: StatusFailed, Reason: "PRINT_SKU_DISABLED"},
			{ID: "3", Status: StatusSkipped},
			{ID: "4", Status: StatusSuccess},
		},
	}
	if got.Total != want.Total || got.SuccessCount != want.SuccessCount ||
		got.FailedCount != want.FailedCount || got.SkippedCount != want.SkippedCount ||
		len(got.Results) != len(want.Results) {
		t.Fatalf("计数对账不符:\n got=%+v\nwant=%+v", got, want)
	}
	for i, it := range got.Results {
		if it != want.Results[i] {
			t.Fatalf("results[%d] 对账不符:\n got=%+v\nwant=%+v", i, it, want.Results[i])
		}
		if it.ID != want.Results[i].ID {
			t.Fatalf("results 应保持入参顺序: [%d]=%s", i, it.ID)
		}
	}
}

// TestBuilderEmpty 空批量：results=[] 非 null（api.md §2 items 空为数组口径）。
func TestBuilderEmpty(t *testing.T) {
	got := NewBuilder(0).Result()
	if got.Total != 0 || got.Results == nil || len(got.Results) != 0 {
		t.Fatalf("空批量 results 应为 [] 非 null: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if string(raw) != `{"total":0,"success_count":0,"failed_count":0,"skipped_count":0,"results":[]}` {
		t.Fatalf("JSON 形状不符（§2.7 冻结契约）: %s", raw)
	}
}

// TestBuilderReasonOmitted success/skipped 不出现 reason 字段（omitempty 语义冻结）。
func TestBuilderReasonOmitted(t *testing.T) {
	raw, err := json.Marshal(NewBuilder(2).Success("1").Skipped("2").Result().Results)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if string(raw) != `[{"id":"1","status":"success"},{"id":"2","status":"skipped"}]` {
		t.Fatalf("success/skipped 不应携带 reason: %s", raw)
	}
}

// TestSummaryOf 域包自有逐条项切片的收敛路径（既有批量端点接入辅助）。
func TestSummaryOf(t *testing.T) {
	got := SummaryOf([]Item{
		{ID: "a", Status: StatusSuccess},
		{ID: "b", Status: StatusSkipped},
		{ID: "c", Status: StatusFailed, Reason: "DATAX_STATUS_CONFLICT"},
	})
	if got.Total != 3 || got.SuccessCount != 1 || got.SkippedCount != 1 || got.FailedCount != 1 {
		t.Fatalf("SummaryOf 计数不符: %+v", got)
	}
}

// TestStatusLiterals 三态字面量与计划 §2.7 冻结值逐字一致（api.md 同源）。
func TestStatusLiterals(t *testing.T) {
	if StatusSuccess != "success" || StatusFailed != "failed" || StatusSkipped != "skipped" {
		t.Fatalf("状态字面量漂移: %s/%s/%s", StatusSuccess, StatusFailed, StatusSkipped)
	}
}
