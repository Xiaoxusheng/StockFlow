package purchase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/stock"
)

// TestQCCreatorCompleteQCWriteback 退货质检单回写收尾（2026-10-07 全流程实测问题 5 回归）。
//
// 背景：returns 域经 QCCreator 窄接口只建单、结果自己应用（库存原语直接落账），
// 质检模块的 QC 单会永远停在 PENDING、qty_qualified=0——与已 COMPLETED 的退货单
// 记录互相矛盾。CompleteQC 把同一业务事实在两处对齐：逐行回写合格/不良量 + 汇总
// 落列（含 result/检验人）+ PENDING→COMPLETED，且幂等（退货链重试可安全重入）。
func TestQCCreatorCompleteQCWriteback(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	creator := NewQCCreator(env.svc)

	qty := func(n int64) stock.Qty { return stock.Qty(n * 10000) }

	// 经 QCCreator 建单（RETURN 来源，退货侧真实路径）。
	qcNo, err := creator.CreateQC(ctx, 9, "测试员", QCSourceReturn, "RT-20261007-000001", "全检",
		[]QCCreateLine{{LineNo: 1, SKUID: 100, QtyInspected: "5.0000"}})
	require.NoError(t, err)
	require.NotEmpty(t, qcNo)

	qc, err := env.repo.FindQCByNo(ctx, qcNo)
	require.NoError(t, err)
	require.Equal(t, QCStatusPending, qc.Status, "新建质检单应为 PENDING（回写前）")

	// 校验栅栏：行不齐套 / 合格+不良 ≠ 检验量 / 单号不存在，均拒绝且单保持 PENDING。
	require.Error(t, creator.CompleteQC(ctx, 9, "测试员", qcNo, nil), "行数不齐套应拒绝")
	require.Error(t, creator.CompleteQC(ctx, 9, "测试员", qcNo, []QCResultLine{
		{LineNo: 1, QtyQualified: "3.0000", QtyDefective: "1.0000"},
	}), "合格+不良 4 ≠ 检验量 5 应拒绝（退货质检为全检口径）")
	require.Error(t, creator.CompleteQC(ctx, 9, "测试员", "QC-NOT-EXIST", []QCResultLine{
		{LineNo: 1, QtyQualified: "1.0000", QtyDefective: "0.0000"},
	}), "质检单号不存在应报错")
	require.Error(t, creator.CompleteQC(ctx, 9, "测试员", "", nil), "单号为空应报错")
	stillPending, err := env.repo.FindQCByNo(ctx, qcNo)
	require.NoError(t, err)
	require.Equal(t, QCStatusPending, stillPending.Status, "校验失败不得推进状态")

	// 正常回写：4 合格 + 1 不良 → COMPLETED，逐行与汇总双落列。
	require.NoError(t, creator.CompleteQC(ctx, 9, "测试员", qcNo, []QCResultLine{
		{LineNo: 1, QtyQualified: "4.0000", QtyDefective: "1.0000"},
	}))
	qc, err = env.repo.FindQCByNo(ctx, qcNo)
	require.NoError(t, err)
	require.Equal(t, QCStatusCompleted, qc.Status, "回写后应推进 COMPLETED")
	require.Equal(t, qty(4), qc.QtyQualified, "汇总合格量应落列")
	require.Equal(t, qty(1), qc.QtyDefective, "汇总不良量应落列")
	require.Equal(t, "部分合格", qc.Result, "既有合格又有不良应判「部分合格」")
	require.Equal(t, int64(9), qc.InspectorID, "检验人应落列")
	require.Equal(t, "测试员", qc.InspectorName)
	items, err := env.repo.ListQCItems(ctx, qc.ID.Int64())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, qty(4), items[0].QtyQualified, "行合格量应回写")
	require.Equal(t, qty(1), items[0].QtyDefective, "行不良量应回写")

	// 幂等重入：已 COMPLETED 直接返回，不得改写已收尾数据。
	require.NoError(t, creator.CompleteQC(ctx, 9, "测试员", qcNo, []QCResultLine{
		{LineNo: 1, QtyQualified: "999.0000", QtyDefective: "0.0000"},
	}))
	again, err := env.repo.FindQCByNo(ctx, qcNo)
	require.NoError(t, err)
	require.Equal(t, qty(4), again.QtyQualified, "幂等重入不得改写已收尾质检单")
	require.Equal(t, qty(1), again.QtyDefective)
	require.Equal(t, "部分合格", again.Result)

	// 全合格 → result 判「合格」（直接注入 PENDING 单，避开单号发放器）。
	manual := &QualityOrder{
		QCNo: "QC-MANUAL-000001", SourceType: QCSourceReturn, SourceNo: "RT-MANUAL",
		InspectionType: "全检", Status: QCStatusPending,
	}
	require.NoError(t, env.repo.InsertQC(ctx, nil, manual, []*QualityItem{
		{LineNo: 1, SKUID: 100, QtyInspected: qty(2)},
	}))
	require.NoError(t, creator.CompleteQC(ctx, 9, "测试员", manual.QCNo, []QCResultLine{
		{LineNo: 1, QtyQualified: "2.0000", QtyDefective: "0.0000"},
	}))
	done, err := env.repo.FindQCByNo(ctx, manual.QCNo)
	require.NoError(t, err)
	require.Equal(t, QCStatusCompleted, done.Status)
	require.Equal(t, "合格", done.Result, "无不良应判「合格」")
}
