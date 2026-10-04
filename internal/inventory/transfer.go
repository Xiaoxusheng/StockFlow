package inventory

// 跨仓调拨原语（backend-m2-plan §8.1，工程师 O 独占新增文件）：
//
//	TransferOut 源仓出库：total/locked 同减（核销调拨预占锁）+ TRANSFER_OUT 流水；
//	TransferIn  目仓入库：total/available 同增（行可创建）+ TRANSFER_IN 流水。
//
// 两原语是调拨出库确认与到货入库确认两个物理时点的落账动作（plan §6.6 状态机
// APPROVED→TRANSFERRING / AWAITING_RECEIPT→COMPLETED 各自调用），不设合并原语——
// 出库与到货之间货计"在途"（在途 = transfer_items 的 qty_out - qty_in 聚合，
// 不入 inventory 六列恒等式，plan §5 000009 注）。实现完全复用包内既有 helper：
// locateRowForUpdate / applyDelta（原子条件 UPDATE）/ consumeLock / buildLedger /
// writeLedger / auditStock / mutate（幂等内核），无旁路 SQL。
//
// TRANSFER_OUT/TRANSFER_IN 已在 000005 change_type CHECK 值域与 changeTypes 映射内
//（service.go），零迁移改动。

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// TransferOut 调拨出库：源仓 locked、total 同减（货离源仓，转入途——不在任何仓库列体现），
// 写 TRANSFER_OUT 流水（status_from/to=locked：扣减发生在锁定库存上）；
// 携带 LockID 时同步核销锁定记录（部分核销保留 ACTIVE，清零转 CONSUMED）。
func (s *Service) TransferOut(ctx context.Context, tx *gorm.DB, op TransferOutOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, false); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		row, err := locateRowForUpdate(tx, op.Key)
		if err != nil {
			return MutationResult{}, err
		}
		if row == nil {
			return MutationResult{}, response.NewError(ErrRecordNotFound, rowKeyDetails(op.Key, "库存行不存在"))
		}
		before := row.State()
		// 业务层校验（inventory-rules §9.2 双重校验之一）。
		if before.Locked.Sub(op.Qty).IsNegative() {
			return MutationResult{}, notEnoughErr(op.Key, "锁定库存不足", op.Qty, before.Locked)
		}
		// 数据层守卫（最后防线）：WHERE locked_qty >= n，影响行数 0 即失败回滚。
		n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, op.Qty.Neg(),
			[]colDelta{{col: ColLocked, delta: op.Qty.Neg()}},
			[]colGuard{{col: ColLocked, min: op.Qty}})
		if err != nil || n == 0 {
			return MutationResult{}, guardFailed(err, op.Key, "锁定库存不足（条件更新未生效）")
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		if op.LockID > 0 {
			if err := s.consumeLock(tx, op.LockID, op.Qty, op.Key, op.Actor); err != nil {
				return MutationResult{}, err
			}
		}
		led := buildLedger(after, "TRANSFER_OUT", op.Source, op.Actor, ColLocked, ColLocked,
			before.Locked, op.Qty.Neg(), idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "transfer-out", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}}, nil
	})
}

// TransferIn 调拨到货：目标仓 available、total 同增（行不存在则创建，
// 创建需 SKU/库位存在性校验），写 TRANSFER_IN 流水（status_from/to=available）。
// 与 TransferOut 分别落账：两端流水齐备（business-flow §10.1 源减目标增）。
func (s *Service) TransferIn(ctx context.Context, tx *gorm.DB, op TransferInOp) (MutationResult, error) {
	if err := validateRowKey(op.Key, true); err != nil {
		return MutationResult{}, err
	}
	if err := validateQty(op.Qty, "qty"); err != nil {
		return MutationResult{}, err
	}
	if err := validateSource(op.Source); err != nil {
		return MutationResult{}, err
	}
	idemKey := strings.TrimSpace(op.IdempotencyKey)
	if err := validateIdempotencyKey(idemKey); err != nil {
		return MutationResult{}, err
	}
	return s.mutate(ctx, tx, idemKey, nil, func(tx *gorm.DB) (MutationResult, error) {
		if err := s.checkSKUAndBin(ctx, op.Key); err != nil {
			return MutationResult{}, err
		}
		row, created, err := ensureRow(tx, op.Key, op.Actor, StockState{})
		if err != nil {
			return MutationResult{}, err
		}
		// 流水三态口径：新建行变更前为全零（与 Putaway 同一约定）。
		before := StockState{}
		if !created {
			before = row.State()
			n, err := applyDelta(tx, row.ID.Int64(), op.Actor.ID, op.Qty,
				[]colDelta{{col: ColAvailable, delta: op.Qty}}, nil)
			if err != nil || n == 0 {
				return MutationResult{}, guardFailed(err, op.Key, "库存行更新未生效")
			}
		}
		after, err := reloadRow(tx, row.ID.Int64())
		if err != nil {
			return MutationResult{}, err
		}
		led := buildLedger(after, "TRANSFER_IN", op.Source, op.Actor, ColAvailable, ColAvailable,
			before.Available, op.Qty, idemKey, op.Remark, op.SerialNo)
		if err := writeLedger(tx, led); err != nil {
			return MutationResult{}, err
		}
		if err := auditStock(tx, "transfer-in", row.ID.Int64(), op.Actor, op, before, after.State()); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Ledger: LedgerRef{ID: led.ID.Int64(), LedgerNo: led.LedgerNo}}, nil
	})
}
