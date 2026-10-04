package inventory

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/response"
)

// 幂等去重判定、入参校验、编号生成等 Service 层纯逻辑单元测试（不依赖 PG/Redis/网络）。

func TestPgUniqueViolation(t *testing.T) {
	idem := &pgconn.PgError{Code: "23505", ConstraintName: "uk_inventory_ledgers_idempotency_key"}
	require.True(t, pgUniqueViolation(idem, "uk_inventory_ledgers_idempotency_key"))
	// 不同约束不误判。
	require.False(t, pgUniqueViolation(idem, "uk_inventory_ledgers_ledger_no"))
	// 非唯一冲突不误判。
	require.False(t, pgUniqueViolation(&pgconn.PgError{Code: "23503"}, "uk_inventory_ledgers_idempotency_key"))
	// 普通错误不误判（errors.As 不命中）。
	require.False(t, pgUniqueViolation(errors.New("boom"), ""))
	// 不指定约束名时仅按 23505 判定。
	require.True(t, pgUniqueViolation(idem, ""))
}

// replayDecision 幂等内核的判定输入（模拟 mutate 的决策面）：
// 预检命中 → 重放；未命中 → 执行；执行遇幂等键唯一冲突 → 回滚后重放。
func replayDecision(preFound bool, execErr error) (executed, replayed bool, err error) {
	switch {
	case preFound:
		return false, true, nil
	case execErr != nil && pgUniqueViolation(execErr, "uk_inventory_ledgers_idempotency_key"):
		return false, true, execErr // 事务已回滚 → 转重放
	default:
		return execErr == nil, false, execErr
	}
}

func TestIdempotencyReplayDecision(t *testing.T) {
	// 首次执行：预检未命中、无冲突 → 执行且不重放。
	executed, replayed, err := replayDecision(false, nil)
	require.NoError(t, err)
	require.True(t, executed)
	require.False(t, replayed)

	// 重复提交（预检命中）：不执行、只重放——重复请求不重复扣减（architecture.md §3.2）。
	executed, replayed, err = replayDecision(true, nil)
	require.NoError(t, err)
	require.False(t, executed)
	require.True(t, replayed)

	// 并发同键：唯一冲突 → 回滚转重放（plan §8.5，唯一索引为准绳）。
	dup := &pgconn.PgError{Code: "23505", ConstraintName: "uk_inventory_ledgers_idempotency_key"}
	executed, replayed, err = replayDecision(false, dup)
	require.Error(t, err)
	require.False(t, executed)
	require.True(t, replayed)

	// 其他错误：失败且不重放（如实上抛）。
	boom := errors.New("connection reset")
	executed, replayed, err = replayDecision(false, boom)
	require.ErrorIs(t, err, boom)
	require.False(t, executed)
	require.False(t, replayed)
}

func TestBusinessNoDocnumContract(t *testing.T) {
	// LED/ADJ 单号经 internal/docnum 统一编号引擎发放（business-flow §13.1、
	// plan §4.1/§8.3 条 2——M1"日期+8位随机后缀"过渡实现已删除，判据 5 收口）：
	// 规则 = 日期段 + 独立流水、ResetAll 永不重置（M1 存量 LED-*/ADJ-* 承接口径）。
	for _, prefix := range []string{"LED", "ADJ"} {
		rule, ok := docnum.RuleFor(prefix)
		require.True(t, ok, prefix)
		require.Equal(t, docnum.ResetAll, rule.Reset, prefix)
		require.True(t, rule.DateSeg, prefix)
	}
}

func TestValidateQtyAndRowKey(t *testing.T) {
	// 数量必须为正（api.md §4 范围校验）。
	require.Error(t, validateQty(q(0), "qty"))
	require.Error(t, validateQty(q(-5), "qty"))
	require.NoError(t, validateQty(q(1), "qty"))

	// 五维键：仓库/库位/SKU 必填，批次 >=0。
	require.NoError(t, validateRowKey(RowKey{WarehouseID: 1, BinID: 2, SKUID: 3}, false))
	require.NoError(t, validateRowKey(RowKey{WarehouseID: 1, BinID: 2, SKUID: 3, BatchID: 9}, false))
	require.Error(t, validateRowKey(RowKey{BinID: 2, SKUID: 3}, false), "缺仓库")
	require.Error(t, validateRowKey(RowKey{WarehouseID: 1, SKUID: 3}, false), "缺库位")
	require.Error(t, validateRowKey(RowKey{WarehouseID: 1, BinID: 2}, false), "缺 SKU")
	require.Error(t, validateRowKey(RowKey{WarehouseID: 1, BinID: 2, SKUID: 3, BatchID: -1}, false))

	// 行创建类必须携带 zone/shelf（inventory-rules §3 五维）。
	require.Error(t, validateRowKey(RowKey{WarehouseID: 1, BinID: 2, SKUID: 3}, true))
	require.NoError(t, validateRowKey(RowKey{WarehouseID: 1, ZoneID: 4, ShelfID: 5, BinID: 2, SKUID: 3}, true))
}

func TestValidateSourceAndIdempotencyKey(t *testing.T) {
	// 来源单据必填（inventory-rules §5：流水必须追溯来源）。
	require.Error(t, validateSource(Source{}))
	require.Error(t, validateSource(Source{Type: "inbound_order"}))
	require.NoError(t, validateSource(Source{Type: "inbound_order", No: "IN-20261002-000001"}))

	// 幂等键长度限制（列 varchar(128)）。
	require.NoError(t, validateIdempotencyKey(strings.Repeat("k", 128)))
	require.Error(t, validateIdempotencyKey(strings.Repeat("k", 129)))
}

func TestAdjustTypesAndDirection(t *testing.T) {
	// business-flow §11.1 值域：盘盈/盘亏/损耗/报废/其他；盘盈为增，其余为减。
	for _, at := range []string{"盘盈", "盘亏", "损耗", "报废", "其他"} {
		require.True(t, adjustTypes[at], at)
	}
	require.False(t, adjustTypes["盘平"])
	require.Equal(t, 1, adjustDirection("盘盈"))
	for _, at := range []string{"盘亏", "损耗", "报废", "其他"} {
		require.Equal(t, -1, adjustDirection(at), at)
	}
}

func TestSerialAndChangeTypeEnums(t *testing.T) {
	// 迁移 chk_serial_numbers_status 值域。
	for _, st := range []string{"IN_STOCK", "LOCKED", "OUTBOUND", "RETURNED", "FROZEN"} {
		require.True(t, serialStatuses[st], st)
	}
	require.False(t, serialStatuses["DESTROYED"])

	// 迁移 chk_inventory_ledgers_change_type 值域（plan §8.4 十类）。
	for _, ct := range []string{
		"INBOUND", "OUTBOUND", "TRANSFER_OUT", "TRANSFER_IN", "LOCK",
		"RELEASE", "MOVE", "INSPECT_PASS", "INSPECT_DEFECTIVE", "ADJUST",
	} {
		require.True(t, changeTypes[ct], ct)
	}
	require.False(t, changeTypes["UNKNOWN"])
}

func TestNotEnoughErrDetails(t *testing.T) {
	// plan §8.3：INVENTORY_NOT_ENOUGH 的 details 必须带具体 SKU/库位/需求量与可用量。
	err := notEnoughErr(RowKey{WarehouseID: 1, BinID: 2, SKUID: 3, BatchID: 4}, "可用库存不足", q(10), q(5))
	var bizErr *response.Error
	require.ErrorAs(t, err, &bizErr)
	require.Contains(t, bizErr.Error(), "INVENTORY_NOT_ENOUGH")
	details, ok := bizErr.Details.(map[string]any)
	require.True(t, ok)
	require.Equal(t, int64(3), details["sku_id"])
	require.Equal(t, int64(2), details["bin_id"])
	require.Equal(t, "10.0000", details["need"])
	require.Equal(t, "5.0000", details["available"])
}
