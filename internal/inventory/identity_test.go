package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 恒等式校验与可用量计算单元测试（inventory-rules §2：总库存 = 可用+锁定+冻结+待检+不良品
// +其他不可用[M1=0]；backend-m1-plan §8.1 冻结六列口径）。不依赖 PG。

// q 以数量单位构造 Qty（1 单位 = 1.0000；内部 ×10000 标度，与 ParseQty("1") 一致）。
func q(v int64) Qty { return Qty(v * qtyScale) }

func TestValidateIdentityOK(t *testing.T) {
	// 全零行合法（出清后的库存行）。
	require.NoError(t, StockState{}.ValidateIdentity())

	// 任意分布，只要和等于 total。
	states := []StockState{
		{Total: q(100), Available: q(100)},
		{Total: q(100), Available: q(60), Locked: q(40)},
		{Total: q(100), Available: q(50), Locked: q(20), Frozen: q(10), PendingInspect: q(15), Defective: q(5)},
		{Total: q(7), Defective: q(7)},
		{Total: q(7), PendingInspect: q(7)},
	}
	for i, s := range states {
		require.NoError(t, s.ValidateIdentity(), "case %d", i)
	}
}

func TestValidateIdentityBroken(t *testing.T) {
	cases := []StockState{
		{Total: q(100), Available: q(90)},                // 缺少锁定分类
		{Total: q(100), Available: q(60), Locked: q(41)}, // 和不等于 total
		{Total: q(100), Available: q(120)},               // 可用超过 total
		{Available: q(-1)},                               // 负数列
		{Total: q(-5), Available: q(-5)},                 // 全负
		{Total: q(10), Available: q(10), Frozen: q(-1)},  // 隐藏负数
	}
	for i, s := range cases {
		require.Error(t, s.ValidateIdentity(), "case %d 应违反恒等式", i)
	}
}

func TestAvailableOf(t *testing.T) {
	// 可用 = 总 - 锁定 - 冻结 - 待检 - 不良品（inventory-rules §2 公式）。
	s := StockState{
		Total: q(100), Available: q(55), Locked: q(30), Frozen: q(5),
		PendingInspect: q(7), Defective: q(3),
	}
	require.Equal(t, q(55), s.AvailableOf())
	require.NoError(t, s.ValidateIdentity())

	// 与状态列不一致 → 恒等式失败（AvailableOf 是恒等式的推导侧）。
	s.Available = q(50)
	require.Equal(t, q(55), s.AvailableOf())
	require.Error(t, s.ValidateIdentity())

	// 其他不可用：M1 冻结六列、无该列（backend-m1-plan §8.1），恒为 0——
	// 全部分类列之和即 total，AvailableOf 恰等于 Available。
}

func TestStateColumnValues(t *testing.T) {
	// 流水 status_from/status_to 值域与迁移 CHECK 同源
	//（chk_inventory_ledgers_status_from/to：available/locked/frozen/pending_inspect/defective）。
	require.Equal(t, "available", ColAvailable.String())
	require.Equal(t, "locked", ColLocked.String())
	require.Equal(t, "frozen", ColFrozen.String())
	require.Equal(t, "pending_inspect", ColPendingInspect.String())
	require.Equal(t, "defective", ColDefective.String())

	// dbColumn 白名单（applyDelta 拼接安全性的根基）。
	require.Equal(t, "available_qty", ColAvailable.dbColumn())
	require.Equal(t, "locked_qty", ColLocked.dbColumn())
	require.Equal(t, "frozen_qty", ColFrozen.dbColumn())
	require.Equal(t, "pending_inspect_qty", ColPendingInspect.dbColumn())
	require.Equal(t, "defective_qty", ColDefective.dbColumn())

	// getOf 读取对应列。
	s := StockState{Total: q(9), Locked: q(4), Defective: q(5)}
	require.Equal(t, q(4), ColLocked.getOf(s))
	require.Equal(t, q(5), ColDefective.getOf(s))
	require.Equal(t, Qty(0), ColAvailable.getOf(s))
}

func TestLockTargetStateMapping(t *testing.T) {
	// 订单占用 → locked（business-flow §7.2 预占）；四类冻结 → frozen（inventory-rules §2）。
	col, ok := lockTargetState("ORDER_HOLD")
	require.True(t, ok)
	require.Equal(t, ColLocked, col)

	for _, lt := range []string{"COUNT_FREEZE", "QC_FREEZE", "MANUAL_FREEZE", "EXCEPTION_FREEZE"} {
		col, ok := lockTargetState(lt)
		require.True(t, ok, lt)
		require.Equal(t, ColFrozen, col, lt)
	}

	_, ok = lockTargetState("UNKNOWN")
	require.False(t, ok)
}
