package returns

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 采购退货（business-flow §9.2：退货申请 → 审核 → 退货出库 → 供应商）。
//
// 库存动作（plan §6.9/§7 冻结映射）：退货出库 = 单事务 Lock(ORDER_HOLD)→Deduct
// （business_type=PURCHASE_RETURN 口径的来源归因 return_order），不建 outbound_order——
// 采购退货闭环在本域，追溯经库存流水 business_no（plan §6.9，回写 §13）。
// 幂等键两段：prt:{return_no}:{line}:{zone}:{shelf}:{bin}:{sku}:{batch}:lock / :deduct
// （序列号件逐件追加 ":sn:{serial_no}" 第三段——修复轮随逐件核销补齐，重放不触发）。
// 序列号管理 SKU 出库必须逐件采集并核销台账（inventory-rules §8.2，修复轮补齐：
// SerialStates 事务内校验 → SerialEvent(OUTBOUND)——消费 StockGateway 窄接口）。
//
// 出库粒度约束（frozen DDL 000010 无逐行已出量列）：整单一次出库——SHIP 请求必须
// 覆盖全部明细行且每行数量=qty_return；部分退货在创建期表达（qty_return < 已收量），
// 行级分批出库需要迁移加列后开放（列入 DomainResult 风险）。

// PurchaseReturnLineInput 采购退货创建行（qty_return ≤ 原采购单行已收货量 − 已退量）。
type PurchaseReturnLineInput struct {
	LineNo    int64  `json:"line_no"`
	SKUID     int64  `json:"sku_id"`
	QtyReturn string `json:"qty_return"`
	Reason    string `json:"reason"`
	Remark    string `json:"remark"`
}

// PurchaseReturnCreateInput 创建采购退货入参。
type PurchaseReturnCreateInput struct {
	PONo        string                    `json:"po_no"`
	SupplierID  int64                     `json:"supplier_id"`
	WarehouseID int64                     `json:"warehouse_id"`
	Remark      string                    `json:"remark"`
	Lines       []PurchaseReturnLineInput `json:"lines"`
}

// ShipLineInput 采购退货出库行（源库位定位 + 批次 + 数量；序列号管理 SKU 必须逐件
// 采集 Serials——inventory-rules §8.2"出库必须逐个序列号操作"，修复轮补齐）。
type ShipLineInput struct {
	LineNo  int64    `json:"line_no"`
	ZoneID  int64    `json:"zone_id"`
	ShelfID int64    `json:"shelf_id"`
	BinID   int64    `json:"bin_id"`
	BatchID int64    `json:"batch_id"`
	Qty     string   `json:"qty"`
	Serials []string `json:"serials"`
}

// ShipInput 采购退货出库入参。
type ShipInput struct {
	Lines []ShipLineInput `json:"lines"`
}

// CreatePurchaseReturn 创建采购退货单（DRAFT）。业务关系校验（api.md §4）经
// PurchaseOrderReader 窄接口：采购单存在且已收货、退货仓一致、行/SKU 归属一致、
// 逐行退量 ≤ 已收货量 − 本域累计已退量（plan §3.1：退量 ≤ 收量）。
func (s *Service) CreatePurchaseReturn(ctx context.Context, actor Actor, in PurchaseReturnCreateInput) (*ReturnOrderView, error) {
	reader, err := s.requirePurchaseOrders()
	if err != nil {
		return nil, err
	}
	if err := validateReturnInput(in.PONo, in.WarehouseID, salesLinesOf(in.Lines)); err != nil {
		return nil, err
	}
	if in.SupplierID <= 0 {
		return nil, paramError("supplier_id", "采购退货必填供应商")
	}
	poID, poWH, poLines, found, err := reader.FindReturnable(ctx, in.PONo)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, response.NewError(ErrSourceOrderNotFound, map[string]any{"po_no": in.PONo})
	}
	if poWH != in.WarehouseID {
		return nil, response.NewError(ErrSourceMismatch, map[string]any{
			"reason": "退货仓必须与原采购单收货仓一致", "po_warehouse_id": poWH, "warehouse_id": in.WarehouseID,
		})
	}
	poLine := lineQtyMap(poLines)

	var view *ReturnOrderView
	err = s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 同来源并发创建串行化（修复轮，见 CreateSalesReturn 同款注记）。
		if err := s.repo.LockSourceSerial(tx, in.PONo); err != nil {
			return err
		}
		returned, err := s.sumReturned(tx, in.PONo, ReturnTypePurchase, 0)
		if err != nil {
			return err
		}
		items, err := buildReturnItems(salesLinesOf(in.Lines), poLine, returned)
		if err != nil {
			return err
		}
		o := &ReturnOrder{
			Type: ReturnTypePurchase, SourceNo: in.PONo, SupplierID: in.SupplierID,
			WarehouseID: in.WarehouseID, Status: ReturnStatusDraft, Remark: in.Remark,
			CreatedAt: database.Now(), UpdatedAt: database.Now(),
			CreatedBy: actor.UserID, UpdatedBy: actor.UserID,
		}
		if _, err := s.insertReturnWithNo(ctx, tx, o, items, actor); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "create"),
			in, map[string]any{"return_no": o.ReturnNo, "status": o.Status, "lines": len(items)}, nil)); err != nil {
			return err
		}
		view = newOrderView(o, items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = poID // 预留：后续需要采购单 ID 级关联时使用（000010 以 source_no 逻辑引用）
	return view, nil
}

// salesLinesOf 复用行校验（两类型行结构字段一致，仅来源语义不同）。
func salesLinesOf(in []PurchaseReturnLineInput) []SalesReturnLineInput {
	out := make([]SalesReturnLineInput, len(in))
	for i, l := range in {
		out[i] = SalesReturnLineInput{
			LineNo: l.LineNo, SKUID: l.SKUID, QtyReturn: l.QtyReturn,
			Reason: l.Reason, Remark: l.Remark,
		}
	}
	return out
}

// ShipPurchaseReturn 退货出库（APPROVED→SHIPPED）：单事务逐行 Lock(ORDER_HOLD)→Deduct
// （正式扣减，business-flow §7.2 口径），预占与扣减同事务、失败整体回滚。
// 幂等：两段键重放（plan §7 "prt:… 两段"）；整单重放返回当前状态，部分重放整体回滚。
func (s *Service) ShipPurchaseReturn(ctx context.Context, actor Actor, id int64, in ShipInput) (*ReturnOrderView, error) {
	gw, err := s.requireStock()
	if err != nil {
		return nil, err
	}
	flagReader, err := s.requireSKUFlagReader()
	if err != nil {
		return nil, err
	}
	if len(in.Lines) == 0 {
		return nil, paramError("lines", "出库明细不能为空")
	}
	src := stock.Source{Type: "return_order"}
	var view *ReturnOrderView
	err = s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := s.repo.FindReturnOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if o.Type != ReturnTypePurchase {
			return response.NewError(ErrSourceMismatch, map[string]any{
				"return_id": id, "reason": "采购退货出库接口不适用于销售退货单",
			})
		}
		// 幂等重放窗口：APPROVED（正常执行）与 SHIPPED（已出库后的整单重试——
		// 两段键命中 ledger 重放，不重复扣减；新行扣减在 SHIPPED 态仍被拒绝）。
		if o.Status != ReturnStatusApproved && o.Status != ReturnStatusShipped {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": id, "return_no": o.ReturnNo, "from": o.Status,
				"reason": "仅已审核（APPROVED）可退货出库",
			})
		}
		src.No = o.ReturnNo
		items, err := s.repo.ListReturnItemsForUpdate(tx, id)
		if err != nil {
			return err
		}
		itemByLine := make(map[int64]*ReturnItem, len(items))
		for _, it := range items {
			itemByLine[it.LineNo] = it
		}
		// 整单一次出库守卫：每行恰一条出库行且数量=qty_return（见包级粒度约束说明）。
		seen := map[int64]bool{}
		for _, ln := range in.Lines {
			it := itemByLine[ln.LineNo]
			if it == nil {
				return response.NewError(ErrLineNotFound, map[string]any{"line_no": ln.LineNo, "return_id": id})
			}
			if seen[ln.LineNo] {
				return paramError("line_no", fmt.Sprintf("行 %d 重复", ln.LineNo))
			}
			seen[ln.LineNo] = true
			qty, err := parseQty("qty", ln.Qty)
			if err != nil {
				return err
			}
			if qty.Sub(it.QtyReturn).IsNegative() || it.QtyReturn.Sub(qty).IsNegative() {
				return response.NewError(ErrQtyExceeded, map[string]any{
					"line_no": ln.LineNo, "ship_qty": qty.String(), "qty_return": it.QtyReturn.String(),
					"reason": "退货出库为整单一次出库：每行数量必须等于退货量",
				})
			}
		}
		if len(seen) != len(items) {
			return response.NewError(ErrQtyExceeded, map[string]any{
				"ship_lines": len(seen), "item_lines": len(items),
				"reason": "退货出库必须覆盖全部明细行",
			})
		}
		before := snapshotOrder(o)
		ia := actor.inventoryActor()
		newLines, replayLines := 0, 0
		for _, ln := range in.Lines {
			it := itemByLine[ln.LineNo]
			qty, _ := stock.ParseQty(ln.Qty)
			key := stock.RowKey{
				WarehouseID: o.WarehouseID, ZoneID: ln.ZoneID, ShelfID: ln.ShelfID,
				BinID: ln.BinID, SKUID: it.SKUID, BatchID: ln.BatchID,
			}
			lockKey := fmtKey("prt", o.ReturnNo, ln.LineNo, ln.ZoneID, ln.ShelfID, ln.BinID, it.SKUID, ln.BatchID, "lock")
			deductKey := fmtKey("prt", o.ReturnNo, ln.LineNo, ln.ZoneID, ln.ShelfID, ln.BinID, it.SKUID, ln.BatchID, "deduct")
			lockRes, err := gw.Lock(ctx, tx, stock.LockOp{
				Key: key, Qty: qty, LockType: "ORDER_HOLD", // 退货出库预占（plan §7 采购退货出库）
				Source: src, Actor: ia, IdempotencyKey: lockKey, Remark: "采购退货出库预占",
			})
			if err != nil {
				return err
			}
			deductRes, err := gw.Deduct(ctx, tx, stock.DeductOp{
				Key: key, Qty: qty, LockID: lockRes.LockID, // 核销预占，正式扣减
				Source: src, Actor: ia, IdempotencyKey: deductKey, Remark: "采购退货出库扣减",
			})
			if err != nil {
				return err
			}
			switch {
			case !lockRes.Replay && !deductRes.Replay:
				newLines++
				// 序列号管理 SKU 逐件核销台账（inventory-rules §8.2"出库必须逐个
				// 序列号操作"；修复轮补齐——此前退货出库不动台账，序列号件在台账
				// 仍 IN_STOCK 于原库位而库存已扣，后续拣货按库位校验会命中不存在
				// 的台账件）。重实行跳过（台账已核销，整单重放语义）。
				flags, err := flagReader.GetFlags(ctx, it.SKUID)
				if err != nil {
					return err
				}
				if flags.SerialManaged {
					// 出库方向：序列号管理 SKU 必须逐件采集（收货侧 validateSerialBatch
					// 的空集放行是"可选采集"语义，出库不适用——inventory-rules §8.2）。
					if len(ln.Serials) == 0 {
						return response.NewError(ErrSerialMismatch, map[string]any{
							"line_no": ln.LineNo, "sku_id": it.SKUID, "qty": qty.String(),
							"reason": "序列号管理 SKU 退货出库必须逐件采集序列号",
						})
					}
					if err := validateSerialBatch(qty, ln.Serials); err != nil {
						return err
					}
					if err := s.shipPurchaseReturnSerials(ctx, tx, gw, ia, o, key, ln, qty, deductKey); err != nil {
						return err
					}
				} else if len(ln.Serials) > 0 {
					return paramError("serials", "非序列号 SKU 不接受序列号")
				}
			case lockRes.Replay && deductRes.Replay:
				replayLines++
			default:
				return response.NewError(ErrPartialReplay, map[string]any{
					"return_id": id, "line_no": ln.LineNo,
					"lock_replay": lockRes.Replay, "deduct_replay": deductRes.Replay,
				})
			}
		}
		switch {
		case replayLines > 0 && newLines == 0:
			// 整单幂等重放：无任何变更。
			fresh, ferr := s.repo.ListReturnItemsForUpdate(tx, id)
			if ferr != nil {
				return ferr
			}
			view = newOrderView(o, fresh)
			return nil
		case replayLines > 0 && newLines > 0:
			return response.NewError(ErrPartialReplay, map[string]any{
				"return_id": id, "new_lines": newLines, "replayed_lines": replayLines,
			})
		}
		if o.Status != ReturnStatusApproved {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": id, "return_no": o.ReturnNo, "from": o.Status,
				"reason": "已出库的退货单不允许再次扣减库存（重复出库应整单重试命中幂等重放）",
			})
		}
		if err := s.guardReturnStatus(tx, o, ReturnStatusShipped, "", actor.UserID); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "ship"),
			map[string]any{"lines": in.Lines},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
			return err
		}
		fresh, err := s.repo.ListReturnItemsForUpdate(tx, id)
		if err != nil {
			return err
		}
		view = newOrderView(o, fresh)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// shipPurchaseReturnSerials 采购退货出库序列号逐件核销（ShipPurchaseReturn 同事务内
// 调用；调用方已完成 validateSerialBatch 的件数/去重校验）。逐件校验台账
// （存在 + 属该 SKU + IN_STOCK + 位于该行指定仓库/库位——与销售拣货同口径，
// inventory-rules §8）后 SerialEvent(OUTBOUND, 仓/位=0)（与 Deduct 同事务，
// 失败整体回滚）。幂等键 = 行扣减键 + ":sn:" + 序列号（重试整单重放时不触发）。
// 批次以台账当前值为准传回（SerialEvent 会覆盖写 BatchID，避免把采集方未知的
// 批次改写进台账）。
func (s *Service) shipPurchaseReturnSerials(ctx context.Context, tx *gorm.DB, gw StockGateway,
	ia stock.Actor, o *ReturnOrder, key stock.RowKey, ln ShipLineInput, qty stock.Qty, deductKey string) error {
	states, err := gw.SerialStates(ctx, tx, key.SKUID, ln.Serials)
	if err != nil {
		return err
	}
	byNo := make(map[string]stock.SerialState, len(states))
	for _, st := range states {
		byNo[st.SerialNo] = st
	}
	for _, sn := range ln.Serials {
		st, ok := byNo[sn]
		if !ok {
			return response.NewError(ErrSerialStateInvalid, map[string]any{
				"serial_no": sn, "sku_id": key.SKUID,
				"reason": "序列号不存在或不属于该 SKU",
			})
		}
		if st.Status != "IN_STOCK" || st.WarehouseID != key.WarehouseID || st.BinID != key.BinID {
			return response.NewError(ErrSerialStateInvalid, map[string]any{
				"serial_no": sn, "status": st.Status,
				"warehouse_id": st.WarehouseID, "bin_id": st.BinID,
				"reason": "序列号不在指定库位的在库状态，不可退货出库",
			})
		}
		if _, _, err := gw.SerialEvent(ctx, tx, stock.SerialOp{
			SerialNo: sn, SKUID: key.SKUID, BatchID: st.BatchID,
			WarehouseID: 0, BinID: 0, Status: "OUTBOUND", // 脱离仓库/库位（销售发货同口径）
			Source:         stock.Source{Type: "return_order", No: o.ReturnNo},
			Actor:          ia,
			IdempotencyKey: deductKey + ":sn:" + sn,
			Remark:         "采购退货出库序列号核销",
		}); err != nil {
			return err
		}
	}
	return nil
}

// CompletePurchaseReturn 完成确认（SHIPPED→COMPLETED，business-flow §9.2 退货出库闭环）。
func (s *Service) CompletePurchaseReturn(ctx context.Context, actor Actor, id int64) (*ReturnOrderView, error) {
	var view *ReturnOrderView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, items, err := s.loadOrderWithItems(tx, id)
		if err != nil {
			return err
		}
		if o.Type != ReturnTypePurchase {
			return response.NewError(ErrSourceMismatch, map[string]any{"return_id": id, "reason": "仅采购退货有出库完成态"})
		}
		before := snapshotOrder(o)
		if err := s.guardReturnStatus(tx, o, ReturnStatusCompleted, "completed_at", actor.UserID); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "complete"),
			nil, map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
			return err
		}
		view = newOrderView(o, items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}
