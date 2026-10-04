package stockops

import (
	"context"
	"sort"

	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 盘点单服务（business-flow §10.2、backend-m2-plan §6.7/§10.3）。
//
// 状态机（全部经守卫 UPDATE 迁移；差异必须走调整单链路，禁止直接改库存——§10.2 硬性规则）：
//
//	DRAFT --start--> COUNTING（范围冻结：逐行 Lock(COUNT_FREEZE) available→frozen +
//	                 qty_system 快照；序列号 SKU 逐件建档明细；范围互斥，architecture §5）
//	COUNTING --register(PUT, 幂等覆盖)--> 实盘登记（只读库存，不动账）
//	COUNTING --finish--> PENDING_REVIEW（系统对比生成 count_differences，行级汇总差异）
//	PENDING_REVIEW --complete--> COMPLETED（单事务逐行：解冻 → Adjust 盘盈/盘亏 →
//	                         序列号联动 → 差异 EXECUTED + adjust_no 回写）
//	PENDING_REVIEW --reject--> CANCELLED（仅解冻 + 差异 REJECTED，不调整）
//	DRAFT/COUNTING --cancel--> CANCELLED（解冻，不出差异）
//
// 多人同范围盘点互斥（architecture §5）：冻结以 COUNT_FREEZE 占住范围内可用库存，
// 第二张盘点单冻结同行时 available 守卫必然不足，或命中他单 ACTIVE COUNT_FREEZE
// （FindRowLocks 检查）→ 整单回滚；盘点期间并发销售预占同被 available 守卫拒绝。
//
// 冻结期行总量恒定（business-flow §10.2"冻结范围"）：inventory 原语层对持有
// ACTIVE COUNT_FREEZE 的库存行拒绝一切改变 total 的变更（Deduct/Putaway/Adjust/
// MoveBin → ErrRowCountFrozen），因此差异 = 实盘 - 冻结快照 即真实盘盈/盘亏；
// 锁定（ORDER_HOLD）库存冻结期间不可发货扣减，解冻后差异调整守卫必然可满足。
//
// 明细双层结构（迁移 000009 唯一键 count_id+inventory_row_id+serial_no）：
//   - 行级汇总行（serial_no=''，qty_system=冻结时行总量）——差异生成与库存调整依据；
//   - 序列号 SKU 逐件行（qty_system=1，inventory-rules §8.2"盘点必须逐个序列号操作"）
//     ——序列号台账联动（缺失核销 OUTBOUND / 多出建档 IN_STOCK）依据。

// CountInput 创建盘点单输入。
type CountInput struct {
	WarehouseID int64      `json:"warehouse_id"`
	Scope       CountScope `json:"scope"`
	Remark      string     `json:"remark"`
}

// CountDetail 盘点单详情。
type CountDetail struct {
	Order       CountOrder        `json:"order"`
	Items       []CountItem       `json:"items"`
	Differences []CountDifference `json:"differences"`
}

// CountRegistrationInput 实盘登记输入（PUT 幂等：同 (inventory_row_id, serial_no) 覆盖）。
type CountRegistrationInput struct {
	Registrations []CountRegistration `json:"items"`
}

// CreateCount 创建盘点单（DRAFT；范围声明校验，不动库存）。
func (s *Service) CreateCount(ctx context.Context, actor stock.Actor, in CountInput) (*CountDetail, error) {
	if in.WarehouseID <= 0 {
		return nil, response.NewError(ErrScopeInvalid, map[string]any{"field": "warehouse_id"})
	}
	if err := in.Scope.validate(); err != nil {
		return nil, err
	}
	detail := &CountDetail{}
	_, err := s.issueInTx(ctx, countNoRule, func(t Tx, no string) error {
		o := &CountOrder{
			CountNo:     no,
			WarehouseID: in.WarehouseID,
			Scope:       in.Scope,
			Status:      CountDraft,
			Remark:      in.Remark,
			CreatedBy:   actor.ID, UpdatedBy: actor.ID,
		}
		if err := t.InsertCount(ctx, o); err != nil {
			return err
		}
		if err := t.Audit(countAudit(actor, "create", o, nil, o)); err != nil {
			return err
		}
		detail.Order = *o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// StartCount 开始盘点（DRAFT→COUNTING）：范围快照落 scope（创建时已快照）→ 范围内
// 库存行按 id 升序逐行 Lock(COUNT_FREEZE)（available→frozen）+ qty_system 快照 +
// 序列号 SKU 逐件明细；任一行失败整体回滚（plan §6.7）。
func (s *Service) StartCount(ctx context.Context, actor stock.Actor, id int64) (*CountDetail, bool, error) {
	replay := false
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if o.Status == CountCounting {
			replay = true
			items, ierr := t.ListCountItems(ctx, id)
			if ierr != nil {
				return ierr
			}
			detail.Order, detail.Items = *o, items
			return nil
		}
		if o.Status != CountDraft {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅草稿状态可开始盘点", "current_status": o.Status,
			})
		}
		// 范围枚举（只读；按 id 升序——多行加锁顺序，plan §10.1）。
		rows, err := t.FindInventoryRows(ctx, o.WarehouseID, o.Scope)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return response.NewError(ErrScopeEmpty, map[string]any{"count_no": o.CountNo})
		}
		items := make([]CountItem, 0, len(rows))
		for _, row := range rows {
			// 冻结该行可用部分（available→frozen）。available=0 的行无可用可冻
			// （行仍入盘点范围——空行盘盈照样可登记）。锁定失败（如并发出库后
			// available 不足）即整体回滚——并发互斥的第一道防线。
			if row.Available.IsPositive() {
				if _, err := s.gateway.Lock(ctx, t.GormDB(), stock.LockOp{
					Key: row.Key(), Qty: row.Available, LockType: "COUNT_FREEZE",
					Source:         stock.Source{Type: sourceCount, No: o.CountNo},
					Actor:          actor,
					IdempotencyKey: "cfreeze:" + o.CountNo + ":" + itoa(row.ID),
					Remark:         "盘点范围冻结",
				}); err != nil {
					return err
				}
			}
			// 范围互斥（architecture §5）：同行已有他单 ACTIVE COUNT_FREEZE 即冲突。
			// 本单刚创建的锁按单号排除。行锁已被上方 Lock 持有（serial 化），检查
			// 在锁内执行——并发第二单必然在其 Lock 或本检查处失败。
			foreign, err := t.FindRowLocks(ctx, row, "COUNT_FREEZE")
			if err != nil {
				return err
			}
			for _, l := range foreign {
				if l.SourceNo != o.CountNo {
					return response.NewError(ErrScopeFrozen, map[string]any{
						"inventory_row_id": row.ID, "frozen_by": l.SourceNo,
					})
				}
			}
			// 行级汇总行：qty_system = 冻结时行总量（差异生成与调整依据）。
			items = append(items, CountItem{
				InventoryRowID: row.ID, SKUID: row.SKUID,
				WarehouseID: row.WarehouseID, ZoneID: row.ZoneID,
				ShelfID: row.ShelfID, BinID: row.BinID,
				QtySystem: row.Total, SerialNo: "",
				CreatedBy: actor.ID, UpdatedBy: actor.ID,
			})
			// 序列号管理 SKU：逐件建档明细（inventory-rules §8.2；fail-closed）。
			flags, err := s.skuFlags(ctx, row.SKUID)
			if err != nil {
				return err
			}
			if flags.SerialManaged {
				serials, err := t.FindSerialsForRow(ctx, row)
				if err != nil {
					return err
				}
				for _, sr := range serials {
					items = append(items, CountItem{
						InventoryRowID: row.ID, SKUID: row.SKUID,
						WarehouseID: row.WarehouseID, ZoneID: row.ZoneID,
						ShelfID: row.ShelfID, BinID: row.BinID,
						QtySystem: qtyUnit, SerialNo: sr.SerialNo,
						CreatedBy: actor.ID, UpdatedBy: actor.ID,
					})
					// 逐件冻结序列号台账（与行冻结同事务，plan §6.7）。
					if _, _, err := s.gateway.SerialEvent(ctx, t.GormDB(), stock.SerialOp{
						SerialNo: sr.SerialNo, SKUID: row.SKUID, BatchID: sr.BatchID,
						WarehouseID: row.WarehouseID, BinID: row.BinID, Status: "FROZEN",
						Source: stock.Source{Type: sourceCount, No: o.CountNo},
						Actor:  actor, Remark: "盘点冻结",
					}); err != nil {
						return err
					}
				}
			}
		}
		if _, err := t.UpdateCountStatus(ctx, id, CountDraft, CountCounting,
			CountStamps{Frozen: true}, actor.ID); err != nil {
			return err
		}
		if err := t.ReplaceCountItems(ctx, id, items); err != nil {
			return err
		}
		o.Status = CountCounting
		if err := t.Audit(countAudit(actor, "start", o,
			map[string]any{"status": CountDraft}, map[string]any{"status": CountCounting, "frozen_rows": len(rows)})); err != nil {
			return err
		}
		detail.Order, detail.Items = *o, items
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// RegisterCountings 实盘登记（COUNTING；PUT 幂等覆盖——重复提交同值不产生副作用，
// architecture §3.2）。只读库存：登记只写 count_items，不动任何库存账。
func (s *Service) RegisterCountings(ctx context.Context, actor stock.Actor, id int64, in CountRegistrationInput) (*CountDetail, error) {
	if len(in.Registrations) == 0 {
		return nil, response.NewError(response.CodeInvalidParam, map[string]any{"field": "items", "reason": "不能为空"})
	}
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if o.Status != CountCounting {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅盘点中状态可登记实盘", "current_status": o.Status,
			})
		}
		existing, err := t.ListCountItems(ctx, id)
		if err != nil {
			return err
		}
		rowLines := map[int64]bool{} // 范围内行
		lineByKey := map[string]bool{}
		for _, it := range existing {
			rowLines[it.InventoryRowID] = true
			lineByKey[itemKey(it.InventoryRowID, it.SerialNo)] = true
		}
		for _, reg := range in.Registrations {
			if reg.InventoryRowID <= 0 {
				return response.NewError(ErrCountItemForeign, map[string]any{"inventory_row_id": reg.InventoryRowID})
			}
			if !rowLines[reg.InventoryRowID] {
				return response.NewError(ErrCountItemForeign, map[string]any{
					"inventory_row_id": reg.InventoryRowID, "reason": "登记行不属于该盘点单范围",
				})
			}
			if reg.Qty.IsNegative() {
				return response.NewError(ErrCountQtyInvalid, map[string]any{
					"inventory_row_id": reg.InventoryRowID, "qty": reg.Qty.String(),
				})
			}
			isSerial := reg.SerialNo != ""
			if isSerial {
				// 逐件登记：一件一行，数量仅 0（缺失）/1（在库）（inventory-rules §8.2）。
				if !qtyIsInteger(reg.Qty) || qtyUnits(reg.Qty) > 1 {
					return response.NewError(ErrCountQtyInvalid, map[string]any{
						"inventory_row_id": reg.InventoryRowID, "serial_no": reg.SerialNo,
						"qty": reg.Qty.String(), "reason": "序列号行数量仅允许 0 或 1",
					})
				}
				// 盘盈多出件（范围内无此 (行, 序列号) 明细）：建档前校验序列号归属。
				if !lineByKey[itemKey(reg.InventoryRowID, reg.SerialNo)] {
					sr, err := t.FindSerialByNo(ctx, reg.SerialNo)
					if err != nil {
						return err
					}
					if sr != nil && sr.SKUID != skuIDOfRow(existing, reg.InventoryRowID) {
						return response.NewError(ErrSerialSKUMismatch, map[string]any{
							"serial_no": reg.SerialNo, "existing_sku_id": sr.SKUID,
						})
					}
				}
			} else if !lineByKey[itemKey(reg.InventoryRowID, "")] {
				return response.NewError(ErrCountItemForeign, map[string]any{
					"inventory_row_id": reg.InventoryRowID,
					"reason":           "行级汇总明细缺失（冻结时未建行）",
				})
			}
		}
		items, err := t.UpsertCountRegistrations(ctx, id, actor, in.Registrations)
		if err != nil {
			return err
		}
		if err := t.Audit(countAudit(actor, "register", o,
			nil, map[string]any{"registered": len(in.Registrations)})); err != nil {
			return err
		}
		detail.Order, detail.Items = *o, items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// FinishCount 完成实盘（COUNTING→PENDING_REVIEW）：全部明细必须已登记（实盘 0
// 也必须显式登记）；系统对比生成 count_differences（行级汇总差异：差异 = 汇总行
// qty_counted - qty_system，盘盈为正；无差异行不生成）。
func (s *Service) FinishCount(ctx context.Context, actor stock.Actor, id int64) (*CountDetail, bool, error) {
	replay := false
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		items, err := t.ListCountItems(ctx, id)
		if err != nil {
			return err
		}
		if o.Status == CountReview {
			replay = true
			diffs, derr := t.ListCountDifferences(ctx, id)
			if derr != nil {
				return derr
			}
			detail.Order, detail.Items, detail.Differences = *o, items, diffs
			return nil
		}
		if o.Status != CountCounting {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅盘点中状态可完成实盘", "current_status": o.Status,
			})
		}
		// 守卫：全部登记（未登记 ≠ 0——防"漏盘当盘亏"造假差异）。
		unregistered := 0
		firstRow := int64(0)
		for _, it := range items {
			if !it.QtyCounted.Valid {
				unregistered++
				if firstRow == 0 {
					firstRow = it.InventoryRowID
				}
			}
		}
		if unregistered > 0 {
			return response.NewError(ErrCountingIncomplete, map[string]any{
				"unregistered": unregistered, "first_inventory_row_id": firstRow,
			})
		}
		// 行级汇总差异：以 serial_no='' 汇总行为准（冻结总量快照 vs 实盘总量）。
		diffs := make([]CountDifference, 0, 8)
		line := 0
		for _, it := range items {
			if it.SerialNo != "" {
				continue
			}
			diff := it.QtyCounted.Qty.Sub(it.QtySystem)
			if diff.IsZero() {
				continue
			}
			line++
			diffs = append(diffs, CountDifference{
				LineNo: line, SKUID: it.SKUID, WarehouseID: it.WarehouseID,
				BinID: it.BinID, BatchID: rowBatchOf(ctx, t, it.InventoryRowID),
				QtySystem: it.QtySystem, QtyCounted: it.QtyCounted.Qty, DiffQty: diff,
				Status: DiffPending, CreatedBy: actor.ID, UpdatedBy: actor.ID,
			})
		}
		if _, err := t.UpdateCountStatus(ctx, id, CountCounting, CountReview,
			CountStamps{}, actor.ID); err != nil {
			return err
		}
		if err := t.ReplaceCountDifferences(ctx, id, diffs); err != nil {
			return err
		}
		o.Status = CountReview
		if err := t.Audit(countAudit(actor, "finish", o,
			map[string]any{"status": CountCounting},
			map[string]any{"status": CountReview, "differences": len(diffs)})); err != nil {
			return err
		}
		detail.Order, detail.Items, detail.Differences = *o, items, diffs
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// CompleteCount 差异审核通过（PENDING_REVIEW→COMPLETED，plan §6.7 单事务逐行）：
// 先 ReleaseLock（释放该行全部盘点冻结）→ Adjust 盘盈/盘亏（经 §11.1 审批链：
// 调整单直接 EXECUTED，adjust_no 回写差异行）→ 序列号联动（缺失核销 OUTBOUND /
// 多出建档 IN_STOCK，source=调整单）；差异为 0 的行只解冻不调整；序列号台账
// 无差异自愈行 source=盘点单。任一失败整体回滚——解冻与调整原子，中间态不可见。
func (s *Service) CompleteCount(ctx context.Context, actor stock.Actor, id int64, opinion string) (*CountDetail, bool, error) {
	replay := false
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if o.Status == CountCompleted {
			replay = true
			return loadCountDetail(ctx, t, id, detail)
		}
		if o.Status != CountReview {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅待审核状态可完成差异处理", "current_status": o.Status,
			})
		}
		items, err := t.ListCountItems(ctx, id)
		if err != nil {
			return err
		}
		diffs, err := t.ListCountDifferences(ctx, id)
		if err != nil {
			return err
		}
		// 活跃冻结锁按来源回查（盘点解冻依据；只读 SELECT，见 repo_gorm.go 说明）。
		locks, err := t.FindSourceLocks(ctx, sourceCount, o.CountNo, "COUNT_FREEZE")
		if err != nil {
			return err
		}
		// 行分组（按 inventory_row_id 升序 = 冻结顺序）。
		rowIDs := distinctRowIDs(items)
		adjustNos := map[int64]string{}
		for _, rowID := range rowIDs {
			row, err := t.GetInventoryRow(ctx, rowID)
			if err != nil {
				return err
			}
			if row == nil {
				return response.NewError(response.CodeInternalError, map[string]any{
					"reason": "库存行缺失", "inventory_row_id": rowID,
				})
			}
			// 1) 解冻该行（frozen→available；inventory-rules §4.2 释放须业务动作触发）。
			for _, l := range locks {
				if l.WarehouseID == row.WarehouseID && l.BinID == row.BinID &&
					l.SKUID == row.SKUID && l.BatchID == row.BatchID {
					if _, err := s.gateway.ReleaseLock(ctx, t.GormDB(), stock.ReleaseLockOp{
						LockID: l.ID, Qty: l.Qty,
						Source:         stock.Source{Type: sourceCount, No: o.CountNo},
						Actor:          actor,
						IdempotencyKey: "release:" + o.CountNo + ":" + itoa(l.ID),
						Remark:         "盘点结束解冻",
					}); err != nil {
						return err
					}
				}
			}
			// 行差异定位：差异行的 sku/bin/batch 与行五维匹配。
			var diff *CountDifference
			for i := range diffs {
				d := &diffs[i]
				if d.SKUID == row.SKUID && d.BinID == row.BinID &&
					d.WarehouseID == row.WarehouseID && d.BatchID == row.BatchID {
					diff = d
					break
				}
			}
			// 2) 差异调整（解冻后同事务；可用量已恢复，盘亏守卫可满足）。冻结期间
			// 行 total 恒定（inventory 层盘点冻结守卫 ErrRowCountFrozen 拒绝
			// Deduct/Putaway/Adjust/MoveBin——business-flow §10.2"冻结范围"），
			// 差异 = 实盘 - 冻结快照 即真实盘盈/盘亏，不存在"冻结期间变动被二次计账"。
			adjustNo := ""
			if diff != nil && !diff.DiffQty.IsZero() {
				adjQty := diff.DiffQty
				if adjQty.IsNegative() {
					adjQty = adjQty.Neg()
				}
				op := stock.AdjustOp{
					Key: row.Key(), Qty: adjQty,
					Reason:         "盘点差异调整：盘点单 " + o.CountNo + " 差异行 " + itoa(int64(diff.LineNo)),
					Source:         stock.Source{Type: sourceCount, No: o.CountNo},
					Actor:          actor,
					IdempotencyKey: "adjust:" + o.CountNo + ":" + itoa(int64(diff.LineNo)),
					Remark:         "盘点差异执行",
				}
				if diff.DiffQty.IsPositive() {
					op.AdjustType = "盘盈"
				} else {
					op.AdjustType = "盘亏"
				}
				res, err := s.gateway.Adjust(ctx, t.GormDB(), op)
				if err != nil {
					return err
				}
				// adjust_no 回写：Adjust 原语返回值（MutationResult.AdjustmentNo，
				// plan §8.3 条 3）。空串仅可能是并发同键重放（前一事务已整体提交、
				// 本单应早已 COMPLETED 的矛盾请求），兜底按行五维+类型+数量精确回查。
				adjustNo = res.AdjustmentNo
				if adjustNo == "" {
					no, ferr := t.FindAdjustmentNo(ctx, row.WarehouseID, row.BinID, row.SKUID, row.BatchID, op.AdjustType, op.Qty)
					if ferr != nil {
						return ferr
					}
					adjustNo = no
				}
				adjustNos[diff.ID.Int64()] = adjustNo
			}
			// 3) 序列号联动（inventory-rules §8.2）。
			if err := s.settleSerialPieces(ctx, t, actor, o, row, items, diff, adjustNo); err != nil {
				return err
			}
		}
		if err := t.SettleCountDifferences(ctx, id, DiffExecuted, adjustNos); err != nil {
			return err
		}
		if _, err := t.UpdateCountStatus(ctx, id, CountReview, CountCompleted,
			CountStamps{Reviewed: true, Completed: true}, actor.ID); err != nil {
			return err
		}
		o.Status = CountCompleted
		if err := t.InsertApproval(ctx, ApprovalRecord{
			TargetType: sourceCount, TargetNo: o.CountNo, Action: "APPROVE", Result: "APPROVED",
			Opinion: opinion, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
		}); err != nil {
			return err
		}
		if err := t.Audit(countAudit(actor, "complete", o,
			map[string]any{"status": CountReview}, map[string]any{"status": CountCompleted, "adjusted": len(adjustNos)})); err != nil {
			return err
		}
		if err := loadCountDetail(ctx, t, id, detail); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// RejectCount 差异驳回（PENDING_REVIEW→CANCELLED）：仅解冻（序列号件回正常）+
// 差异行 REJECTED，不调整库存（plan §6.7）。
func (s *Service) RejectCount(ctx context.Context, actor stock.Actor, id int64, opinion string) (*CountDetail, bool, error) {
	replay := false
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if o.Status == CountCancelled {
			replay = true
			return loadCountDetail(ctx, t, id, detail)
		}
		if o.Status != CountReview {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅待审核状态可驳回", "current_status": o.Status,
			})
		}
		items, err := t.ListCountItems(ctx, id)
		if err != nil {
			return err
		}
		if err := s.unfreezeAll(ctx, t, actor, o, items); err != nil {
			return err
		}
		if err := t.SettleCountDifferences(ctx, id, DiffRejected, nil); err != nil {
			return err
		}
		if _, err := t.UpdateCountStatus(ctx, id, CountReview, CountCancelled,
			CountStamps{Reviewed: true, Cancelled: true}, actor.ID); err != nil {
			return err
		}
		o.Status = CountCancelled
		if err := t.InsertApproval(ctx, ApprovalRecord{
			TargetType: sourceCount, TargetNo: o.CountNo, Action: "REJECT", Result: "REJECTED",
			Opinion: opinion, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
		}); err != nil {
			return err
		}
		if err := t.Audit(countAudit(actor, "reject", o,
			map[string]any{"status": CountReview}, map[string]any{"status": CountCancelled})); err != nil {
			return err
		}
		if err := loadCountDetail(ctx, t, id, detail); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// CancelCount 取消（DRAFT/COUNTING→CANCELLED）：COUNTING 解冻（含序列号件回正常），
// 未出差异（差异生成于 finish）；DRAFT 仅状态迁移（plan §6.7）。
func (s *Service) CancelCount(ctx context.Context, actor stock.Actor, id int64, reason string) (*CountDetail, bool, error) {
	replay := false
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if o.Status == CountCancelled {
			replay = true
			return loadCountDetail(ctx, t, id, detail)
		}
		fromStatus := o.Status
		switch o.Status {
		case CountDraft:
		case CountCounting:
			items, err := t.ListCountItems(ctx, id)
			if err != nil {
				return err
			}
			if err := s.unfreezeAll(ctx, t, actor, o, items); err != nil {
				return err
			}
		default:
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅草稿/盘点中状态可取消（待审核差异走驳回）", "current_status": o.Status,
			})
		}
		if _, err := t.UpdateCountStatus(ctx, id, fromStatus, CountCancelled,
			CountStamps{Cancelled: true}, actor.ID); err != nil {
			return err
		}
		o.Status = CountCancelled
		if err := t.InsertApproval(ctx, ApprovalRecord{
			TargetType: sourceCount, TargetNo: o.CountNo, Action: "CANCEL", Result: "CANCELLED",
			Opinion: reason, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
		}); err != nil {
			return err
		}
		if err := t.Audit(countAudit(actor, "cancel", o,
			map[string]any{"status": fromStatus}, map[string]any{"status": CountCancelled})); err != nil {
			return err
		}
		if err := loadCountDetail(ctx, t, id, detail); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// ---- 查询 ----

// GetCountDetail 详情（数据权限 fail-closed：仓库不在范围内按不存在处理）。
func (s *Service) GetCountDetail(ctx context.Context, id int64, scope Scope) (*CountDetail, error) {
	detail := &CountDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetCount(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		if !scopeVisibleCount(scope, *o) {
			return response.NewError(ErrCountNotFound, map[string]any{"id": id})
		}
		return loadCountDetail(ctx, t, id, detail)
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// ListCounts 分页列表。
func (s *Service) ListCounts(ctx context.Context, f CountFilter, page, pageSize int) ([]CountOrder, int64, error) {
	var rows []CountOrder
	var total int64
	err := s.store.WithinTx(ctx, func(t Tx) error {
		var err error
		rows, total, err = t.ListCounts(ctx, f, page, pageSize)
		return err
	})
	return rows, total, err
}

// ---- 内部助手 ----

func loadCountDetail(ctx context.Context, t Tx, id int64, detail *CountDetail) error {
	o, err := t.GetCount(ctx, id)
	if err != nil {
		return err
	}
	items, err := t.ListCountItems(ctx, id)
	if err != nil {
		return err
	}
	diffs, err := t.ListCountDifferences(ctx, id)
	if err != nil {
		return err
	}
	detail.Order, detail.Items, detail.Differences = *o, items, diffs
	return nil
}

// unfreezeAll 释放该盘点单全部冻结锁并将序列号件回正常（reject/cancel 共用；
// 多出件未入台账，无事件）。
func (s *Service) unfreezeAll(ctx context.Context, t Tx, actor stock.Actor, o *CountOrder, items []CountItem) error {
	locks, err := t.FindSourceLocks(ctx, sourceCount, o.CountNo, "COUNT_FREEZE")
	if err != nil {
		return err
	}
	for _, l := range locks {
		if _, err := s.gateway.ReleaseLock(ctx, t.GormDB(), stock.ReleaseLockOp{
			LockID: l.ID, Qty: l.Qty,
			Source:         stock.Source{Type: sourceCount, No: o.CountNo},
			Actor:          actor,
			IdempotencyKey: "release:" + o.CountNo + ":" + itoa(l.ID),
			Remark:         "盘点取消解冻",
		}); err != nil {
			return err
		}
	}
	rowByID := map[int64]InventoryRowRef{}
	for _, it := range items {
		if it.SerialNo == "" {
			continue
		}
		row, ok := rowByID[it.InventoryRowID]
		if !ok {
			r, err := t.GetInventoryRow(ctx, it.InventoryRowID)
			if err != nil {
				return err
			}
			if r == nil {
				continue
			}
			row, rowByID[it.InventoryRowID] = *r, *r
		}
		// 已知件回正常（FROZEN→IN_STOCK，台账恢复冻结前状态）。
		if _, _, err := s.gateway.SerialEvent(ctx, t.GormDB(), stock.SerialOp{
			SerialNo: it.SerialNo, SKUID: it.SKUID, BatchID: row.BatchID,
			WarehouseID: row.WarehouseID, BinID: row.BinID, Status: "IN_STOCK",
			Source: stock.Source{Type: sourceCount, No: o.CountNo},
			Actor:  actor, Remark: "盘点取消解冻回位",
		}); err != nil {
			return err
		}
	}
	return nil
}

// settleSerialPieces 完成时的序列号台账联动：
//   - 已知件（qty_system=1）实盘在库 → IN_STOCK 解冻回位；
//   - 已知件实盘缺失 → OUTBOUND 核销（source=调整单）；
//   - 多出件（qty_system=0，登记期建档行）→ IN_STOCK 建档（source=调整单）；
//   - 无差异行的序列号自愈（缺一件同时多一件）→ source=盘点单。
func (s *Service) settleSerialPieces(ctx context.Context, t Tx, actor stock.Actor,
	o *CountOrder, row *InventoryRowRef, items []CountItem, diff *CountDifference, adjustNo string) error {
	flags, err := s.skuFlags(ctx, row.SKUID)
	if err != nil {
		return err
	}
	if !flags.SerialManaged {
		return nil
	}
	sourceType := sourceCount
	sourceNo := o.CountNo
	if diff != nil && adjustNo != "" {
		sourceType = adjustSourceType
		sourceNo = adjustNo
	}
	for _, it := range items {
		if it.InventoryRowID != row.ID || it.SerialNo == "" {
			continue
		}
		counted := it.QtyCounted.Qty
		status := ""
		switch {
		case it.QtySystem == qtyUnit && counted == qtyUnit:
			status = "IN_STOCK" // 在库：解冻回位
		case it.QtySystem == qtyUnit && counted.IsZero():
			status = "OUTBOUND" // 缺失：核销
		case it.QtySystem.IsZero() && counted == qtyUnit:
			status = "IN_STOCK" // 多出：建档
		default:
			continue // 不构成台账语义的组合（登记校验已拦截其余）
		}
		if _, _, err := s.gateway.SerialEvent(ctx, t.GormDB(), stock.SerialOp{
			SerialNo: it.SerialNo, SKUID: it.SKUID, BatchID: row.BatchID,
			WarehouseID: row.WarehouseID, BinID: row.BinID, Status: status,
			Source: stock.Source{Type: sourceType, No: sourceNo},
			Actor:  actor, Remark: "盘点差异序列号联动",
		}); err != nil {
			return err
		}
	}
	return nil
}

func distinctRowIDs(items []CountItem) []int64 {
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		if !seen[it.InventoryRowID] {
			seen[it.InventoryRowID] = true
			ids = append(ids, it.InventoryRowID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func rowBatchOf(ctx context.Context, t Tx, rowID int64) int64 {
	row, err := t.GetInventoryRow(ctx, rowID)
	if err != nil || row == nil {
		return 0
	}
	return row.BatchID
}

func skuIDOfRow(items []CountItem, rowID int64) int64 {
	for _, it := range items {
		if it.InventoryRowID == rowID {
			return it.SKUID
		}
	}
	return 0
}

// itemKey 盘点明细映射键（行 ID + 序列号；空串即行级汇总行）。
func itemKey(rowID int64, serialNo string) string {
	return itoa(rowID) + "|" + serialNo
}

func scopeVisibleCount(scope Scope, o CountOrder) bool {
	if scope.AllWarehouses {
		return true
	}
	if len(scope.WarehouseIDs) == 0 {
		return false
	}
	for _, id := range scope.WarehouseIDs {
		if id == o.WarehouseID {
			return true
		}
	}
	return false
}

func countAudit(actor stock.Actor, action string, o *CountOrder, before, after any) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module: "stockops", ObjectType: "count_order", ObjectID: o.ID.Int64(),
		Action: action, OperatorID: actor.ID, OperatorName: actor.Name,
		RequestID: actor.RequestID, IP: actor.IP, UserAgent: actor.UserAgent,
		Method: actor.Method, Path: actor.Path,
		Success: true, Request: map[string]any{"count_no": o.CountNo},
		Before: before, After: after,
	}
}
