package purchase

// 质检与上架任务的 HTTP handler 层表驱动测试（基建见 handler_test.go 头注）。
// 另含详情接口的数据权限 fail-closed 断言（permission.md §4：仓库不在范围内按
// 不存在处理）——该分支依赖 WarehouseScope 传参，在 service 层以真实数据断言
// （HTTP 层非超管用户需 auth 域中间件栈，包内不可装配）。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// seedQCCapable 落一张 AWAITING_QC 且 SKU 已收货的入库单，返回入库单与明细 ID
// （质检创建类用例夹具：CreateQC 校验入库单状态与已收货量，service_quality.go:78-129）。
func (h *httpEnv) seedQCCapable(t *testing.T, status string, received string) (*InboundOrder, int64) {
	t.Helper()
	in := h.repo.seedInbound(status, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
	items, err := h.repo.ListInboundItems(context.Background(), in.ID.Int64())
	require.NoError(t, err)
	require.Len(t, items, 1)
	if received != "" {
		_, err := h.repo.AddInboundItemReceived(context.Background(), nil, items[0].ID.Int64(), qty(t, received), 9)
		require.NoError(t, err)
	}
	return in, items[0].ID.Int64()
}

// ---- 质检：GET /api/quality（handleQCList）----

func TestHandleQCListHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
	qc, err := h.svc.CreateQC(context.Background(), testActor(), QCCreateInput{
		SourceNo: in.InboundNo, InspectionType: InspectionFull,
		Lines: []QCLineInput{{SKUID: 100, QtyInspected: qty(t, "6")}},
	})
	require.NoError(t, err)

	cases := []struct {
		name      string
		query     string
		wantTotal int64
		wantLen   int
	}{
		{"默认分页全量", "", 1, 1},
		{"状态筛选命中", "?status=" + QCStatusPending, 1, 1},
		{"状态筛选无命中", "?status=" + QCStatusCompleted, 0, 0},
		{"来源单号筛选", "?source_no=" + in.InboundNo, 1, 1},
		{"第二页为空", "?page=2", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/quality"+tc.query, nil)
			wantOK(t, call)
			var pg pageData
			decodeData(t, call.env.Data, &pg)
			require.Equal(t, tc.wantTotal, pg.Total)
			var items []QualityOrder
			decodeData(t, pg.Items, &items)
			require.Len(t, items, tc.wantLen)
		})
	}

	t.Run("列表项冻结契约字段", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality", nil)
		wantOK(t, call)
		var pg pageData
		decodeData(t, call.env.Data, &pg)
		var items []QualityOrder
		decodeData(t, pg.Items, &items)
		require.Len(t, items, 1)
		require.Equal(t, qc.QCNo, items[0].QCNo)
		require.Equal(t, QCStatusPending, items[0].Status)
		require.Equal(t, InspectionFull, items[0].InspectionType)
	})
	t.Run("非法参数 page", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/quality?page=0", nil), response.CodeInvalidParam.Code, `"field"`)
	})
}

// ---- 质检：GET /api/quality/trace（handleQCTrace）----
// 成功用例走原生 SQL（service_quality_trace.go:106-139），假驱动恒返回单行 id=1
// （fakedb_test.go fakeRows）——只断言信封结构与 service 编排，不断言业务数据行。

func TestHandleQCTraceHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("默认查询信封结构与 record_type 恒 inspection", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/trace", nil)
		wantOK(t, call)
		var pg pageData
		decodeData(t, call.env.Data, &pg)
		var items []QualityTraceItem
		decodeData(t, pg.Items, &items)
		require.Len(t, items, 1, "假驱动单行；断言编排而非数据")
		require.Equal(t, "inspection", items[0].RecordType, "record_type 恒 inspection（service_quality_trace.go:134-137）")
	})
	t.Run("serial_no 检索显式拒绝 400", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/trace?serial_no=SN-0001", nil)
		wantFailField(t, call, "COMMON_INVALID_PARAM", "serial_no")
	})
	t.Run("非法参数 page", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/quality/trace?page=abc", nil), "COMMON_INVALID_PARAM", `"field"`)
	})
}

// ---- 质检：GET /api/quality/nonconforming（handleQCNonconforming）----

func TestHandleQCNonconformingHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("destination 检索显式拒绝 400", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/nonconforming?destination=DEFECT_WAREHOUSE", nil)
		wantFailField(t, call, "COMMON_INVALID_PARAM", "destination")
	})
	t.Run("disposition 非六值键拒绝 400", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/nonconforming?disposition=not_a_key", nil)
		wantFailField(t, call, "COMMON_INVALID_PARAM", "disposition")
	})
	t.Run("disposition 六值键之一查询成功", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/nonconforming?disposition=scrap", nil)
		wantOK(t, call)
		var pg pageData
		decodeData(t, call.env.Data, &pg)
		var items []NonconformingItem
		decodeData(t, pg.Items, &items)
		require.Len(t, items, 1, "假驱动单行；断言编排而非数据")
	})
	t.Run("默认查询成功", func(t *testing.T) {
		wantOK(t, h.do(t, http.MethodGet, "/api/quality/nonconforming", nil))
	})
}

// ---- 质检：POST /api/quality（handleQCCreate）----

func TestHandleQCCreateHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()

	t.Run("合法创建（已收货入库单）", func(t *testing.T) {
		in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
		call := h.do(t, http.MethodPost, "/api/quality", map[string]any{
			"source_no": in.InboundNo, "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "6"}},
		})
		wantOK(t, call)
		var created QualityOrder
		decodeData(t, call.env.Data, &created)
		require.True(t, strings.HasPrefix(created.QCNo, "QC-"), "单号冻结规则 QC-: %s", created.QCNo)
		require.Equal(t, QCStatusPending, created.Status)
		require.Equal(t, "6.0000", created.QtyInspected.String())
		rows, err := h.repo.ListQCItems(ctx, created.ID.Int64())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, 1, rows[0].LineNo, "行号从 1 连续编号")
	})

	cases := []struct {
		name     string
		status   string
		received string
		body     map[string]any
		wantCode string
		contains string
	}{
		{"检验方式非法", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": "免检2",
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "1"}}}, ErrQCInspectionTypeInvalid.Code, "",
		},
		{"入库单不存在", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "IN-NOPE", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "1"}}}, ErrInboundNotFound.Code, "inbound_no",
		},
		{"入库单未收齐不可质检", InboundStatusDraft, "", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "1"}}}, ErrInboundStatusNotAllowed.Code, "待质检",
		},
		{"质检量超出已收货", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "11"}}}, ErrQCQtyExceedsReceived.Code, "",
		},
		{"SKU 不在入库单明细", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 999, "qty_inspected": "1"}}}, response.CodeInvalidParam.Code, "lines[0].sku_id",
		},
		{"同 SKU 质检单内重复", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "1"}, {"sku_id": 100, "qty_inspected": "2"}}},
			response.CodeInvalidParam.Code, "lines[1].sku_id",
		},
		{"qty_inspected 为 0", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull,
			"lines": []map[string]any{{"sku_id": 100, "qty_inspected": "0"}}}, response.CodeInvalidParam.Code, "lines[0].qty_inspected",
		},
		{"缺 lines", InboundStatusAwaitingQC, "6", map[string]any{
			"source_no": "@seed", "inspection_type": InspectionFull}, response.CodeInvalidParam.Code, "请求体格式错误",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := h.seedQCCapable(t, tc.status, tc.received)
			if tc.body["source_no"] == "@seed" {
				tc.body["source_no"] = in.InboundNo
			}
			call := h.do(t, http.MethodPost, "/api/quality", tc.body)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}
}

// ---- 质检：GET /api/quality/:id（handleQCDetail）----

func TestHandleQCDetailHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
	qc, err := h.svc.CreateQC(context.Background(), testActor(), QCCreateInput{
		SourceNo: in.InboundNo, InspectionType: InspectionSample,
		Lines: []QCLineInput{{SKUID: 100, BatchNo: "B1", QtyInspected: qty(t, "3")}},
	})
	require.NoError(t, err)

	t.Run("详情含明细", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/quality/"+idPath(qc.ID), nil)
		wantOK(t, call)
		var detail QCDetail
		decodeData(t, call.env.Data, &detail)
		require.Equal(t, qc.QCNo, detail.Order.QCNo)
		require.Len(t, detail.Items, 1)
		require.Equal(t, "3.0000", detail.Items[0].QtyInspected.String())
		require.Equal(t, "B1", detail.Items[0].BatchNo)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/quality/999", nil), ErrQCNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/quality/abc", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 质检：POST /api/quality/:id/start（handleQCStart）----

func TestHandleQCStartHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()

	t.Run("开始检验记录检验人", func(t *testing.T) {
		in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
		qc, err := h.svc.CreateQC(ctx, testActor(), QCCreateInput{
			SourceNo: in.InboundNo, InspectionType: InspectionFull,
			Lines: []QCLineInput{{SKUID: 100, QtyInspected: qty(t, "3")}},
		})
		require.NoError(t, err)
		call := h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/start", qc.ID.Int64()), nil)
		wantOK(t, call)
		var got QualityOrder
		decodeData(t, call.env.Data, &got)
		require.Equal(t, QCStatusInspecting, got.Status)
		require.Equal(t, int64(9), h.repo.qcs[qc.ID.Int64()].InspectorID, "记录检验人（business-flow §4.2）")
	})

	t.Run("已完成单再开始 409", func(t *testing.T) {
		in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
		qc, err := h.svc.CreateQC(ctx, testActor(), QCCreateInput{
			SourceNo: in.InboundNo, InspectionType: InspectionFull,
			Lines: []QCLineInput{{SKUID: 100, QtyInspected: qty(t, "3")}},
		})
		require.NoError(t, err)
		_, err = h.repo.UpdateQCStatus(ctx, nil, qc.ID.Int64(), QCStatusPending, QCStatusCompleted, 9)
		require.NoError(t, err)
		wantFail(t, h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/start", qc.ID.Int64()), nil), ErrQCStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/quality/999/start", nil), ErrQCNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/quality/0/start", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 质检：POST /api/quality/:id/execute（handleQCExecute）----
// 成功路径走完整业务链（收货→上架→质检，service_test.go TestQualityExecuteMapsInspectResult
// 的 HTTP 版），覆盖 handler 绑定与信封；库存映射键构成已由 service 层测试覆盖，不重复。

func TestHandleQCExecuteHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()
	actor := testActor()

	t.Run("完整链路执行成功", func(t *testing.T) {
		_, in := h.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{101: qty(t, "5")})
		_, err := h.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
			InboundNo: in.InboundNo,
			Lines: []ReceiptLineInput{{
				SKUID: 101, QtyGood: qty(t, "5"), BatchNo: "B-HTTP-01", ExpiryDate: dateOf(t, "2027-12-31"),
			}},
		}, "op-qc-http")
		require.NoError(t, err)
		tasks, _, err := h.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		_, err = h.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
		require.NoError(t, err)
		_, err = h.svc.ExecutePutawayTask(ctx, actor, tasks[0].ID.Int64(), PutawayExecuteInput{})
		require.NoError(t, err)

		// HTTP 创建 → start → execute。
		call := h.do(t, http.MethodPost, "/api/quality", map[string]any{
			"source_no": in.InboundNo, "inspection_type": InspectionSample,
			"lines": []map[string]any{{"sku_id": 101, "batch_no": "B-HTTP-01", "qty_inspected": "5"}},
		})
		wantOK(t, call)
		var qc QualityOrder
		decodeData(t, call.env.Data, &qc)
		require.Equal(t, QCStatusPending, qc.Status)
		call = h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/start", qc.ID.Int64()), nil)
		wantOK(t, call)
		call = h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/execute", qc.ID.Int64()), map[string]any{
			"lines":  []map[string]any{{"line_no": 1, "qty_qualified": "3", "qty_defective": "2"}},
			"result": "部分合格",
		})
		wantOK(t, call)
		var done QualityOrder
		decodeData(t, call.env.Data, &done)
		require.Equal(t, QCStatusCompleted, done.Status)
		require.Equal(t, "3.0000", done.QtyQualified.String())
		require.Equal(t, "2.0000", done.QtyDefective.String())
		// 全部明细处理完成 + 任务全部完成 → 入库单 COMPLETED（plan §6.2）。
		fresh, err := h.svc.GetInbound(ctx, in.ID.Int64(), WarehouseScope{All: true})
		require.NoError(t, err)
		require.Equal(t, InboundStatusCompleted, fresh.Order.Status)
	})

	t.Run("PENDING 状态直接执行 409", func(t *testing.T) {
		in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
		qc, err := h.svc.CreateQC(ctx, testActor(), QCCreateInput{
			SourceNo: in.InboundNo, InspectionType: InspectionFull,
			Lines: []QCLineInput{{SKUID: 100, QtyInspected: qty(t, "3")}},
		})
		require.NoError(t, err)
		call := h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/execute", qc.ID.Int64()), map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "3"}}, "result": "合格",
		})
		wantFail(t, call, ErrQCStatusNotAllowed.Code)
	})

	seedInspecting := func(t *testing.T) *QualityOrder {
		t.Helper()
		in, _ := h.seedQCCapable(t, InboundStatusAwaitingQC, "6")
		qc, err := h.svc.CreateQC(ctx, testActor(), QCCreateInput{
			SourceNo: in.InboundNo, InspectionType: InspectionFull,
			Lines: []QCLineInput{{SKUID: 100, QtyInspected: qty(t, "3")}},
		})
		require.NoError(t, err)
		_, err = h.svc.StartQC(ctx, testActor(), qc.ID.Int64())
		require.NoError(t, err)
		return qc
	}

	cases := []struct {
		name     string
		body     map[string]any
		wantCode string
		contains string
	}{
		{"处理结果非法", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "3"}}, "result": "还行"}, ErrQCResultInvalid.Code, "",
		},
		{"明细行不存在", map[string]any{
			"lines": []map[string]any{{"line_no": 99, "qty_qualified": "3"}}, "result": "合格"}, response.CodeInvalidParam.Code, "99",
		},
		{"合格+不等于检验数量", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "2", "qty_defective": "2"}}, "result": "合格"}, ErrQCQtyInvalid.Code, "",
		},
		{"合格+不良为 0", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "0", "qty_defective": "0"}}, "result": "合格"}, ErrQCQtyInvalid.Code, "",
		},
		{"缺 lines", map[string]any{"result": "合格"}, response.CodeInvalidParam.Code, "请求体格式错误"},
		{"缺 result", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "3"}}}, response.CodeInvalidParam.Code, "请求体格式错误",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qc := seedInspecting(t)
			call := h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/execute", qc.ID.Int64()), tc.body)
			wantFailField(t, call, tc.wantCode, tc.contains)
		})
	}

	t.Run("未提交全部明细行拒绝", func(t *testing.T) {
		// 双 SKU 入库单（同 SKU 重复行会被创建校验拒绝，故多行场景须两 SKU 构造）。
		in := h.repo.seedInbound(InboundStatusAwaitingQC, 1, SourceTypeOther, "",
			map[int64]stock.Qty{100: qty(t, "10"), 101: qty(t, "10")})
		items, err := h.repo.ListInboundItems(ctx, in.ID.Int64())
		require.NoError(t, err)
		require.Len(t, items, 2)
		for _, it := range items {
			_, err := h.repo.AddInboundItemReceived(ctx, nil, it.ID.Int64(), qty(t, "10"), 9)
			require.NoError(t, err)
		}
		qc, err := h.svc.CreateQC(ctx, testActor(), QCCreateInput{
			SourceNo: in.InboundNo, InspectionType: InspectionFull,
			Lines: []QCLineInput{
				{SKUID: 100, QtyInspected: qty(t, "1")},
				{SKUID: 101, QtyInspected: qty(t, "2")},
			},
		})
		require.NoError(t, err)
		_, err = h.svc.StartQC(ctx, testActor(), qc.ID.Int64())
		require.NoError(t, err)
		call := h.do(t, http.MethodPost, fmt.Sprintf("/api/quality/%d/execute", qc.ID.Int64()), map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "1"}}, "result": "合格",
		})
		wantFailField(t, call, response.CodeInvalidParam.Code, "全部明细行")
	})

	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/quality/999/execute", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "1"}}, "result": "合格",
		}), ErrQCNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/quality/abc/execute", map[string]any{
			"lines": []map[string]any{{"line_no": 1, "qty_qualified": "1"}}, "result": "合格",
		}), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 上架任务：GET /api/putaway（handleTaskList）----

func TestHandleTaskListHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusPending, 1, 101)
	h.repo.seedTask("IN-TEST-000001", 101, 0, qty(t, "3"), FromStatePendingInspect, TaskStatusInProgress, 1, 102)
	h.repo.seedTask("IN-TEST-000002", 100, 0, qty(t, "1"), FromStateAvailable, TaskStatusCompleted, 2, 201)

	cases := []struct {
		name      string
		query     string
		wantTotal int64
		wantLen   int
	}{
		{"默认分页全量", "", 3, 3},
		{"状态筛选", "?status=" + TaskStatusPending, 1, 1},
		{"入库单号筛选", "?inbound_no=IN-TEST-000001", 2, 2},
		{"SKU 筛选", "?sku_id=100", 2, 2},
		{"来源状态筛选", "?from_state=" + FromStatePendingInspect, 1, 1},
		{"仓库筛选", "?warehouse_id=2", 1, 1},
		{"第二页截取", "?page=2&pageSize=2", 3, 1},
		{"条件组合无命中", "?status=PAUSED", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := h.do(t, http.MethodGet, "/api/putaway"+tc.query, nil)
			wantOK(t, call)
			var pg pageData
			decodeData(t, call.env.Data, &pg)
			require.Equal(t, tc.wantTotal, pg.Total)
			var items []PutawayTask
			decodeData(t, pg.Items, &items)
			require.Len(t, items, tc.wantLen)
		})
	}
	for _, tc := range []struct{ name, query string }{
		{"sku_id 非整数", "?sku_id=abc"},
		{"sku_id 负数", "?sku_id=-1"},
		{"page 为 0", "?page=0"},
	} {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			wantFailField(t, h.do(t, http.MethodGet, "/api/putaway"+tc.query, nil), response.CodeInvalidParam.Code, `"field"`)
		})
	}
}

// ---- 上架任务：GET /api/putaway/recommend（handleTaskRecommend）----

func TestHandleTaskRecommendHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("推荐库位成功", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/putaway/recommend?warehouse_id=1&sku_id=100", nil)
		wantOK(t, call)
		var res struct {
			Suggestions []BinSuggestion `json:"suggestions"`
		}
		decodeData(t, call.env.Data, &res)
		require.Len(t, res.Suggestions, 1)
		require.Equal(t, int64(101), res.Suggestions[0].BinID)
		require.Equal(t, "同 SKU 集中", res.Suggestions[0].Reason)
	})
	t.Run("qty 缺省 1 透传推荐服务", func(t *testing.T) {
		_ = h.do(t, http.MethodGet, "/api/putaway/recommend?warehouse_id=1&sku_id=100", nil)
		require.Equal(t, "1:100:1", h.rec.calls[len(h.rec.calls)-1])
	})
	t.Run("qty 透传推荐服务", func(t *testing.T) {
		_ = h.do(t, http.MethodGet, "/api/putaway/recommend?warehouse_id=1&sku_id=100&qty=2.5", nil)
		require.Equal(t, "1:100:2.5", h.rec.calls[len(h.rec.calls)-1])
	})
	t.Run("无可推荐库位返回空集", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/putaway/recommend?warehouse_id=2&sku_id=100", nil)
		wantOK(t, call)
		var res struct {
			Suggestions []BinSuggestion `json:"suggestions"`
		}
		decodeData(t, call.env.Data, &res)
		require.Empty(t, res.Suggestions)
	})

	cases := []struct {
		name     string
		query    string
		contains string
	}{
		{"缺 warehouse_id", "", "warehouse_id"},
		{"warehouse_id 为 0", "?warehouse_id=0&sku_id=100", "warehouse_id"},
		{"缺 sku_id", "?warehouse_id=1", "sku_id"},
		{"sku_id 为 0", "?warehouse_id=1&sku_id=0", "sku_id"},
		{"qty 为 0", "?warehouse_id=1&sku_id=100&qty=0", "qty"},
		{"qty 负数", "?warehouse_id=1&sku_id=100&qty=-1", "qty"},
		{"qty 非数字", "?warehouse_id=1&sku_id=100&qty=abc", "qty"},
	}
	for _, tc := range cases {
		t.Run("非法参数 "+tc.name, func(t *testing.T) {
			wantFailField(t, h.do(t, http.MethodGet, "/api/putaway/recommend"+tc.query, nil), response.CodeInvalidParam.Code, tc.contains)
		})
	}
}

// ---- 上架任务：GET /api/putaway/:id（handleTaskDetail）----

func TestHandleTaskDetailHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusPending, 1, 101)

	t.Run("详情", func(t *testing.T) {
		call := h.do(t, http.MethodGet, "/api/putaway/"+idPath(task.ID), nil)
		wantOK(t, call)
		var got PutawayTask
		decodeData(t, call.env.Data, &got)
		require.Equal(t, task.PutawayNo, got.PutawayNo)
		require.Equal(t, TaskStatusPending, got.Status)
		require.Equal(t, "5.0000", got.Qty.String())
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodGet, "/api/putaway/999", nil), ErrPutawayTaskNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodGet, "/api/putaway/abc", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 上架任务：POST /api/putaway/:id/claim（handleTaskClaim）----

func TestHandleTaskClaimHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("待领取任务领取成功", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusPending, 1, 101)
		call := h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/claim", nil)
		wantOK(t, call)
		var got PutawayTask
		decodeData(t, call.env.Data, &got)
		require.Equal(t, TaskStatusInProgress, got.Status)
		require.Equal(t, int64(9), got.ClaimedBy, "领取人=当前用户")
	})
	t.Run("已被领取 409", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusInProgress, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/claim", nil), ErrPutawayClaimConflict.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/999/claim", nil), ErrPutawayTaskNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/putaway/abc/claim", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 上架任务：POST /api/putaway/:id/pause（handleTaskPause）----

func TestHandleTaskPauseHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("上架中任务暂停成功", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusInProgress, 1, 101)
		call := h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/pause", nil)
		wantOK(t, call)
		var got PutawayTask
		decodeData(t, call.env.Data, &got)
		require.Equal(t, TaskStatusPaused, got.Status)
	})
	t.Run("待领取任务不能暂停 409", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusPending, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/pause", nil), ErrPutawayStatusNotAllowed.Code)
	})
	t.Run("已完成任务不能暂停 409", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusCompleted, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/pause", nil), ErrPutawayStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/999/pause", nil), ErrPutawayTaskNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/putaway/abc/pause", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 上架任务：POST /api/putaway/:id/resume（handleTaskResume）----

func TestHandleTaskResumeHTTP(t *testing.T) {
	h := newHTTPEnv(t)

	t.Run("已暂停任务恢复成功", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusPaused, 1, 101)
		call := h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/resume", nil)
		wantOK(t, call)
		var got PutawayTask
		decodeData(t, call.env.Data, &got)
		require.Equal(t, TaskStatusInProgress, got.Status)
	})
	t.Run("上架中任务不能恢复 409", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000001", 100, 0, qty(t, "5"), FromStateAvailable, TaskStatusInProgress, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/"+idPath(task.ID)+"/resume", nil), ErrPutawayStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/999/resume", nil), ErrPutawayTaskNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/putaway/abc/resume", nil), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 上架任务：POST /api/putaway/:id/execute（handleTaskExecute）----

func TestHandleTaskExecuteHTTP(t *testing.T) {
	h := newHTTPEnv(t)
	ctx := context.Background()
	actor := testActor()

	t.Run("上架确认成功（免检直通、改指定库位）", func(t *testing.T) {
		h2 := newHTTPEnv(t) // 独立环境避免用例间库存/单据串扰
		_, in := h2.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "6")})
		_, err := h2.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
			InboundNo: in.InboundNo,
			Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "6"), RequireInspect: boolPtr(false)}},
		}, "op-exe-http")
		require.NoError(t, err)
		tasks, _, err := h2.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		taskID := tasks[0].ID.Int64()

		call := h2.do(t, http.MethodPost, fmt.Sprintf("/api/putaway/%d/claim", taskID), nil)
		wantOK(t, call)
		call = h2.do(t, http.MethodPost, fmt.Sprintf("/api/putaway/%d/execute", taskID), map[string]any{"bin_id": 102})
		wantOK(t, call)
		var got PutawayTask
		decodeData(t, call.env.Data, &got)
		require.Equal(t, TaskStatusCompleted, got.Status)
		require.Equal(t, int64(102), got.TargetBinID, "执行时改指定库位（§5.2）")
		require.Len(t, h2.stock.putaways, 1)
		require.False(t, h2.stock.putaways[0].RequireInspect, "免检任务 Putaway 直达 available")
		require.Equal(t, int64(102), h2.stock.putaways[0].Key.BinID)
		fresh, err := h2.svc.GetInbound(ctx, in.ID.Int64(), WarehouseScope{All: true})
		require.NoError(t, err)
		require.Equal(t, InboundStatusCompleted, fresh.Order.Status, "全部任务完成驱动入库单完成")
	})

	t.Run("目标库位不存在 400", func(t *testing.T) {
		_, in := h.seedApprovedChain(t, SourceTypePurchase, "", map[int64]stock.Qty{100: qty(t, "2")})
		_, err := h.svc.ConfirmReceipt(ctx, actor, ReceiptInput{
			InboundNo: in.InboundNo,
			Lines:     []ReceiptLineInput{{SKUID: 100, QtyGood: qty(t, "2"), RequireInspect: boolPtr(false)}},
		}, "op-exe-bin")
		require.NoError(t, err)
		tasks, _, _ := h.repo.ListTasks(ctx, TaskListFilter{InboundNo: in.InboundNo, Page: 1, PageSize: 10, Scope: WarehouseScope{All: true}})
		_, err = h.svc.ClaimPutawayTask(ctx, actor, tasks[0].ID.Int64())
		require.NoError(t, err)
		call := h.do(t, http.MethodPost, fmt.Sprintf("/api/putaway/%d/execute", tasks[0].ID.Int64()), map[string]any{"bin_id": 999})
		wantFail(t, call, ErrPutawayBinInvalid.Code)
	})
	t.Run("待领取任务不能执行 409", func(t *testing.T) {
		task := h.repo.seedTask("IN-TEST-000009", 100, 0, qty(t, "1"), FromStateAvailable, TaskStatusPending, 1, 101)
		wantFail(t, h.do(t, http.MethodPost, fmt.Sprintf("/api/putaway/%d/execute", task.ID.Int64()), map[string]any{}),
			ErrPutawayStatusNotAllowed.Code)
	})
	t.Run("不存在 404", func(t *testing.T) {
		wantFail(t, h.do(t, http.MethodPost, "/api/putaway/999/execute", map[string]any{}), ErrPutawayTaskNotFound.Code)
	})
	t.Run("非法路径 id", func(t *testing.T) {
		wantFailField(t, h.do(t, http.MethodPost, "/api/putaway/abc/execute", map[string]any{}), response.CodeInvalidParam.Code, `"id"`)
	})
}

// ---- 详情接口数据权限 fail-closed（permission.md §4，service 层断言）----
// 单据落在仓库 1：范围 [2] 不可见 → 按不存在处理；范围 [1] 可见。

func TestDetailEndpointsScopeFailClosed(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	po := e.repo.seedPO(POStatusDraft, 1, map[int64]stock.Qty{100: qty(t, "5")})
	in := e.repo.seedInbound(InboundStatusReceiving, 1, SourceTypeOther, "", map[int64]stock.Qty{100: qty(t, "10")})
	rc := &Receipt{ReceiptNo: "RC-TEST-000001", InboundNo: in.InboundNo, WarehouseID: 1}
	require.NoError(t, e.repo.InsertReceipt(ctx, nil, rc, nil))
	qc := &QualityOrder{
		QCNo: "QC-TEST-000001", SourceType: QCSourceInbound, SourceNo: in.InboundNo,
		WarehouseID: 1, InspectionType: InspectionFull, Status: QCStatusPending, ImageRefs: StringList{},
	}
	require.NoError(t, e.repo.InsertQC(ctx, nil, qc, []*QualityItem{{LineNo: 1, SKUID: 100, QtyInspected: qty(t, "1")}}))
	task := e.repo.seedTask(in.InboundNo, 100, 0, qty(t, "2"), FromStateAvailable, TaskStatusPending, 1, 101)

	hidden := WarehouseScope{IDs: []int64{2}}
	visible := WarehouseScope{IDs: []int64{1}}

	cases := []struct {
		name     string
		call     func(sc WarehouseScope) error
		notFound string
	}{
		{"采购订单详情", func(sc WarehouseScope) error { _, err := e.svc.GetPO(ctx, po.ID.Int64(), sc); return err }, ErrPONotFound.Code},
		{"入库单详情", func(sc WarehouseScope) error { _, err := e.svc.GetInbound(ctx, in.ID.Int64(), sc); return err }, ErrInboundNotFound.Code},
		{"收货详情(按ID)", func(sc WarehouseScope) error { _, err := e.svc.GetReceiptByID(ctx, rc.ID.Int64(), sc); return err }, ErrReceiptNotFound.Code},
		{"质检详情", func(sc WarehouseScope) error { _, err := e.svc.GetQC(ctx, qc.ID.Int64(), sc); return err }, ErrQCNotFound.Code},
		{"上架任务详情", func(sc WarehouseScope) error { _, err := e.svc.GetTask(ctx, task.ID.Int64(), sc); return err }, ErrPutawayTaskNotFound.Code},
	}
	for _, tc := range cases {
		t.Run(tc.name+" 范围外按不存在处理", func(t *testing.T) {
			require.Equal(t, tc.notFound, codeOf(t, tc.call(hidden)))
		})
		t.Run(tc.name+" 范围内可见", func(t *testing.T) {
			require.NoError(t, tc.call(visible))
		})
	}
}
