package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// FEFO/FIFO 出库分配策略纯函数单元测试（inventory-rules §6/§7.2）。不依赖 PG。

func tp(s string) *time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestAllocateFEFO(t *testing.T) {
	// 先到期先出：有效期升序；无效期批次（不会到期）排末位；同效期按入库日升序。
	cands := []AllocationCandidate{
		{BatchID: 3, BatchNo: "B3", ExpiryDate: tp("2027-06-01"), InboundDate: tp("2026-05-01"), AvailableQty: q(10)},
		{BatchID: 1, BatchNo: "B1", ExpiryDate: tp("2026-03-01"), InboundDate: tp("2026-01-01"), AvailableQty: q(5)},
		{BatchID: 4, BatchNo: "B4", ExpiryDate: nil, InboundDate: tp("2026-02-01"), AvailableQty: q(8)},
		{BatchID: 2, BatchNo: "B2", ExpiryDate: tp("2026-03-01"), InboundDate: tp("2025-12-01"), AvailableQty: q(7)},
	}
	steps, err := AllocateFEFO(cands, q(18))
	require.NoError(t, err)
	require.Equal(t, []AllocationStep{
		{BatchID: 2, BatchNo: "B2", Take: q(7)}, // 同效期（2026-03-01）内入库更早（2025-12-01）
		{BatchID: 1, BatchNo: "B1", Take: q(5)}, // 同效期，入库稍晚
		{BatchID: 3, BatchNo: "B3", Take: q(6)}, // 部分取用
	}, steps)

	// 无效期批次确实排末位。
	steps, err = AllocateFEFO(cands, q(25))
	require.NoError(t, err)
	require.Equal(t, []AllocationStep{
		{BatchID: 2, BatchNo: "B2", Take: q(7)},
		{BatchID: 1, BatchNo: "B1", Take: q(5)},
		{BatchID: 3, BatchNo: "B3", Take: q(10)},
		{BatchID: 4, BatchNo: "B4", Take: q(3)},
	}, steps)
}

func TestAllocateFIFO(t *testing.T) {
	// 先进先出：入库日期升序；入库日期未知排末位。
	cands := []AllocationCandidate{
		{BatchID: 2, BatchNo: "B2", InboundDate: tp("2026-03-01"), AvailableQty: q(4)},
		{BatchID: 1, BatchNo: "B1", InboundDate: tp("2026-01-01"), AvailableQty: q(4)},
		{BatchID: 3, BatchNo: "B3", InboundDate: nil, AvailableQty: q(9)},
	}
	steps, err := AllocateFIFO(cands, q(10))
	require.NoError(t, err)
	require.Equal(t, []AllocationStep{
		{BatchID: 1, BatchNo: "B1", Take: q(4)},
		{BatchID: 2, BatchNo: "B2", Take: q(4)},
		{BatchID: 3, BatchNo: "B3", Take: q(2)},
	}, steps)
}

func TestAllocateDeterministicTieBreak(t *testing.T) {
	// 完全并列的候选按 BatchID 升序兜底（确定性全序，杜绝分配抖动）。
	cands := []AllocationCandidate{
		{BatchID: 9, BatchNo: "B9", ExpiryDate: tp("2026-06-01"), InboundDate: tp("2026-01-01"), AvailableQty: q(5)},
		{BatchID: 2, BatchNo: "B2", ExpiryDate: tp("2026-06-01"), InboundDate: tp("2026-01-01"), AvailableQty: q(5)},
	}
	steps, err := AllocateFEFO(cands, q(8))
	require.NoError(t, err)
	require.Equal(t, []AllocationStep{
		{BatchID: 2, BatchNo: "B2", Take: q(5)},
		{BatchID: 9, BatchNo: "B9", Take: q(3)},
	}, steps)
}

func TestAllocateShortage(t *testing.T) {
	cands := []AllocationCandidate{
		{BatchID: 1, AvailableQty: q(4)},
		{BatchID: 2, AvailableQty: q(6)},
	}
	_, err := AllocateFEFO(cands, q(15))
	var shortage *AllocationShortageError
	require.ErrorAs(t, err, &shortage)
	require.Equal(t, q(15), shortage.Needed)
	require.Equal(t, q(10), shortage.Offered) // 含零候选也计入合计；负候选直接报错

	// 恰好满足（边界）。
	steps, err := AllocateFIFO(cands, q(10))
	require.NoError(t, err)
	require.Len(t, steps, 2)
}

func TestAllocateInvalidInput(t *testing.T) {
	// 需求量必须为正。
	_, err := AllocateFEFO(nil, q(0))
	require.Error(t, err)
	_, err = AllocateFIFO(nil, q(-1))
	require.Error(t, err)

	// 负可用量候选：输入非法。
	_, err = AllocateFEFO([]AllocationCandidate{{BatchID: 1, AvailableQty: q(-2)}}, q(1))
	require.Error(t, err)

	// 空候选 + 正需求 → 缺口。
	_, err = AllocateFEFO(nil, q(1))
	var shortage *AllocationShortageError
	require.ErrorAs(t, err, &shortage)
	require.Equal(t, Qty(0), shortage.Offered)
}

func TestAllocateDoesNotMutateInput(t *testing.T) {
	// 纯函数无副作用：排序不得修改调用方切片。
	cands := []AllocationCandidate{
		{BatchID: 2, ExpiryDate: tp("2027-01-01"), AvailableQty: q(1)},
		{BatchID: 1, ExpiryDate: tp("2026-01-01"), AvailableQty: q(1)},
	}
	_, err := AllocateFEFO(cands, q(2))
	require.NoError(t, err)
	require.Equal(t, int64(2), cands[0].BatchID)
}
