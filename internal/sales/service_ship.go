package sales

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// 发货确认 / 取消 / 关闭 / 重新分配 / 物流态流转（business-flow §8.5、plan §6.5/§7）。
//
// 发货确认事务边界（architecture.md §4、plan §6.5"发货确认 = 标准事务"）：
// shipments 落库（幂等键）→ 逐行 Deduct（携带 LockID 核销预占锁定，正式扣减）→
// 序列号 SKU 逐件 SerialEvent(OUTBOUND, 仓/位=0) → 出库单/销售订单明细 qty_shipped
// 与状态推进 → 审计，同一事务；任一步失败整体回滚。
// 幂等防重复扣减（三层）：① shipments.idempotency_key 部分唯一索引 + 预检重放；
// ② 出库单状态守卫（PACKED→SHIPPED_* 只能发生一次，重放请求无可发明细 → 拒绝）；
// ③ Deduct 原语幂等键 ship:{outbound_no}:{line_no}:{bin}:{sku}:{batch}（plan §7 冻结
// 构成，inventory_ledgers 唯一索引兜底）。
// 部分发货口径：发货按"行"取整行剩余量（一次发货取整行），未选行留待后续发货单
// ——出库单 PARTIAL_SHIPPED、销售订单 PARTIAL_SHIPPED 承载中间态（business-flow §14
// "部分发货"）；同行拆多次发运不在 M2 键构成下支持（见域 risks 注记）。

// ShipLineInput 发货行（只选行号——发货量恒为该行已打包未发货的剩余量）。
type ShipLineInput struct {
	LineNo int64 `json:"line_no"`
}

// ShipInput 发货确认入参（business-flow §8.5：物流公司/物流单号/发货人/包裹数；
// IdempotencyKey 来自 HTTP Idempotency-Key 头，缺省依赖状态守卫防重）。
type ShipInput struct {
	OutboundNo     string          `json:"outbound_no"`
	Lines          []ShipLineInput `json:"lines"` // 缺省 = 全部未发货明细行
	Carrier        string          `json:"carrier"`
	TrackingNo     string          `json:"tracking_no"`
	Remark         string          `json:"remark"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// ShipResult 发货结果（Replay=true 幂等重放：返回既有发货单，未再次扣减）。
type ShipResult struct {
	Shipment *Shipment     `json:"shipment"`
	Replay   bool          `json:"replay"`
	Shipped  map[int64]Qty `json:"shipped"` // 行号 → 本次发货量
}

// Ship 发货确认。出库单须为 PACKED（首批）或 PARTIAL_SHIPPED（续批）。
func (s *Service) Ship(ctx context.Context, actor Actor, in ShipInput) (*ShipResult, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	in.OutboundNo = strings.TrimSpace(in.OutboundNo)
	if in.OutboundNo == "" {
		return nil, errInvalidParam("outbound_no", "必填")
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 128 {
		return nil, errInvalidParam("idempotency_key", "长度不能超过 128")
	}
	var res *ShipResult
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		// 幂等第一层：显式幂等键重放返回既有发货单（不重复扣减，plan §11.4）。
		if key != "" {
			existing, err := s.repo.FindShipmentByIdempotencyKey(tx, key)
			if err != nil {
				return err
			}
			if existing != nil {
				res = &ShipResult{Shipment: existing, Replay: true, Shipped: map[int64]Qty{}}
				return nil
			}
		}
		o, err := s.outForUpdate(tx, in.OutboundNo)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": in.OutboundNo})
		}
		// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与详情接口同口径）。
		if !actor.CanAccess(o.WarehouseID) {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": in.OutboundNo})
		}
		if o.Status != OBStatusPacked && o.Status != OBStatusPartialShipped {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": in.OutboundNo, "from": o.Status,
				"reason": "仅已打包（PACKED）/部分发货（PARTIAL_SHIPPED）状态可确认发货",
			})
		}
		items, err := s.repo.ListOutboundItems(tx, o.ID.Int64())
		if err != nil {
			return err
		}
		byLine := map[int64]OutboundItem{}
		for _, it := range items {
			byLine[it.LineNo] = it
		}
		// 发货行确定：请求指定行（去重校验）或全部未发货行；发货量 = 整行剩余量。
		shipLines := map[int64]bool{}
		if len(in.Lines) > 0 {
			for _, l := range in.Lines {
				if _, ok := byLine[l.LineNo]; !ok {
					return response.NewError(ErrShipQtyInvalid, map[string]any{"line_no": l.LineNo, "reason": "行号不存在"})
				}
				if shipLines[l.LineNo] {
					return errInvalidParam("lines", "行号重复")
				}
				shipLines[l.LineNo] = true
			}
		} else {
			for _, it := range items {
				if it.QtyPacked.Sub(it.QtyShipped).IsPositive() {
					shipLines[it.LineNo] = true
				}
			}
		}
		if len(shipLines) == 0 {
			return response.NewError(ErrNothingToShip, map[string]any{"outbound_no": in.OutboundNo})
		}
		allocs, err := s.repo.ListAllocationsByOutbound(tx, in.OutboundNo)
		if err != nil {
			return err
		}
		allocByLine := map[int64][]AllocationRecord{}
		for _, rec := range allocs {
			allocByLine[rec.LineNo] = append(allocByLine[rec.LineNo], rec)
		}
		shipmentNo, err := s.nextNo(ctx, tx, "SH")
		if err != nil {
			return err
		}
		pkgCount, err := s.repo.CountPackagesByOutbound(tx, in.OutboundNo)
		if err != nil {
			return err
		}
		sh := &Shipment{
			ShipmentNo: shipmentNo, OutboundNo: in.OutboundNo,
			Carrier: in.Carrier, TrackingNo: in.TrackingNo,
			WarehouseID: o.WarehouseID, ShipperID: actor.ID, ShipperName: actor.Name,
			PackageCount: pkgCount, Status: ShipStatusPending, Remark: in.Remark,
			CreatedBy: actor.ID, UpdatedBy: actor.ID,
		}
		if key != "" {
			k := key
			sh.IdempotencyKey = &k
		}
		if err := s.repo.InsertShipment(tx, sh); err != nil {
			if pgUniqueViolation(err, "uk_shipments_idempotency") {
				existing, ferr := s.repo.FindShipmentByIdempotencyKey(tx, key)
				if ferr == nil && existing != nil {
					res = &ShipResult{Shipment: existing, Replay: true, Shipped: map[int64]Qty{}}
					return nil
				}
			}
			return err
		}

		shipped := map[int64]Qty{}
		// 固定行号升序扣减（plan §10.1：多行库存变更按行 id 升序，防死锁与分配抖动）。
		lineOrder := make([]int64, 0, len(shipLines))
		for lineNo, ship := range shipLines {
			if ship {
				lineOrder = append(lineOrder, lineNo)
			}
		}
		sort.Slice(lineOrder, func(i, j int) bool { return lineOrder[i] < lineOrder[j] })
		for _, lineNo := range lineOrder {
			it := byLine[lineNo]
			qty := it.QtyPacked.Sub(it.QtyShipped)
			if !qty.IsPositive() {
				return response.NewError(ErrShipQtyInvalid, map[string]any{
					"line_no": lineNo, "reason": "该行无已打包未发货数量",
				})
			}
			flags, err := s.checkSKUEnabled(ctx, it.SKUID)
			if err != nil {
				return err
			}
			// 正式扣减：逐分配记录 Deduct（整行一次发货口径下每记录恰好核销一次）。
			recs := allocByLine[lineNo]
			var allocTotal Qty
			for _, rec := range recs {
				allocTotal = allocTotal.Add(rec.Qty)
			}
			if allocTotal.Sub(qty).IsNegative() {
				return response.NewError(ErrShipQtyInvalid, map[string]any{
					"line_no": lineNo, "reason": "分配量不足（预占记录缺失或已释放）",
					"ship_qty": qty.String(), "allocated_qty": allocTotal.String(),
				})
			}
			// 分配量超出发货量同样拒绝（M2 修复轮）：逐记录全额 Deduct 的口径下，
			// allocTotal > qty 意味着按分配全额扣减而 qty_shipped 只记发货量——多扣
			// 部分从账面消失（短拣差额已由 ConfirmPick 的分配重同步收缩，正常路径
			// 恒等；此处为数据不一致时的显式失败防线，inventory-rules §9.2）。
			if allocTotal.Sub(qty).IsPositive() {
				return response.NewError(ErrShipQtyInvalid, map[string]any{
					"line_no":  lineNo,
					"reason":   "分配量超出发货量（预占记录与实拣/实包不一致），拒绝按分配全额扣减",
					"ship_qty": qty.String(), "allocated_qty": allocTotal.String(),
				})
			}
			for _, rec := range recs {
				// 幂等键构成冻结于 plan §7：ship:{outbound_no}:{line_no}:{bin}:{sku}:{batch}。
				if _, err := s.stock.Deduct(ctx, tx, DeductOp{
					Key: RowKey{
						WarehouseID: rec.WarehouseID, BinID: rec.BinID,
						SKUID: rec.SKUID, BatchID: rec.BatchID,
					},
					Qty:    rec.Qty,
					LockID: rec.LockID,
					Source: Source{Type: "outbound_order", No: in.OutboundNo},
					Actor:  actor,
					IdempotencyKey: "ship:" + in.OutboundNo + ":" + strconv.FormatInt(lineNo, 10) +
						":" + strconv.FormatInt(rec.BinID, 10) + ":" + strconv.FormatInt(rec.SKUID, 10) +
						":" + strconv.FormatInt(rec.BatchID, 10),
					Remark: "销售发货正式扣减（核销 ORDER_HOLD）",
				}); err != nil {
					return err
				}
			}
			// 序列号 SKU：逐件核销台账（OUTBOUND，脱离仓库/库位，inventory-rules §8.2）。
			if flags.SerialManaged {
				if err := s.shipSerials(ctx, tx, actor, o, lineNo, it.SKUID, qty); err != nil {
					return err
				}
			}
			if err := s.repo.UpdateOutboundItemProgress(tx, o.ID.Int64(), lineNo, OutboundItemProgress{Shipped: &it.QtyPacked}); err != nil {
				return err
			}
			shipped[lineNo] = qty
		}
		// 销售订单明细进度：经 so_no 定位（UpdateSalesOrderItemProgress 需 so_id）。
		so, err := s.repo.GetSalesOrderByNo(tx, o.SoNo)
		if err != nil {
			return err
		}
		if so != nil {
			for lineNo := range shipped {
				q := byLine[lineNo].QtyPacked
				if err := s.repo.UpdateSalesOrderItemProgress(tx, so.ID.Int64(), lineNo, nil, &q); err != nil {
					return err
				}
			}
			// 销售订单状态推进（发货事件，plan §6.4）。
			soItems, err := s.repo.ListSalesOrderItems(tx, so.ID.Int64())
			if err != nil {
				return err
			}
			allShipped := true
			for _, it := range soItems {
				if it.QtyShipped.Sub(it.Qty).IsNegative() {
					allShipped = false
					break
				}
			}
			target := SOStatusPartialShipped
			if allShipped {
				target = SOStatusShippedAll
			}
			if so.Status != target {
				if !canTransition(soTransitions, so.Status, target) {
					return response.NewError(ErrStateConflict, map[string]any{
						"so_no": so.SoNo, "from": so.Status, "to": target,
					})
				}
				// shipped_at = 首次发货迁移即落（business-flow §13.4 发货时间）。
				n, err := s.repo.MarkSalesOrderStatus(tx, so.ID.Int64(), so.Status, target, StatusStamp{By: actor.ID, Shipped: true})
				if err != nil {
					return err
				}
				if n == 0 {
					return response.NewError(ErrStateConflict, map[string]any{"so_no": so.SoNo, "from": so.Status, "to": target})
				}
			}
		}
		// 出库单状态推进（PACKED→PARTIAL_SHIPPED/SHIPPED_ALL 或 PARTIAL_SHIPPED→SHIPPED_ALL）。
		allShipped := true
		for _, it := range items {
			if shipLines[it.LineNo] {
				continue
			}
			if it.QtyPacked.Sub(it.QtyShipped).IsPositive() {
				allShipped = false
				break
			}
		}
		obTarget := OBStatusPartialShipped
		if allShipped {
			obTarget = OBStatusShippedAll
		}
		if !canTransition(obTransitions, o.Status, obTarget) {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": in.OutboundNo, "from": o.Status, "to": obTarget,
			})
		}
		if n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), o.Status, obTarget, StatusStamp{By: actor.ID, Shipped: allShipped}); err != nil {
			return err
		} else if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"outbound_no": in.OutboundNo, "from": o.Status, "to": obTarget})
		}
		// 发货单 PENDING→SHIPPED（库存动作发生在本迁移，plan §6.5；shipped_at 落列）。
		if n, err := s.repo.MarkShipmentStatus(tx, sh.ID.Int64(), ShipStatusPending, ShipStatusShipped, StatusStamp{By: actor.ID, Shipped: true}); err != nil {
			return err
		} else if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"shipment_no": sh.ShipmentNo, "from": ShipStatusPending, "to": ShipStatusShipped})
		}
		sh.Status = ShipStatusShipped
		res = &ShipResult{Shipment: sh, Shipped: shipped}
		if err := s.auditEntry(tx, "sales", "shipment", "ship", sh.ID.Int64(), actor, in, nil, sh); err != nil {
			return err
		}
		return s.auditEntry(tx, "sales", "outbound_order", "ship", o.ID.Int64(), actor,
			map[string]any{"shipment_no": sh.ShipmentNo, "shipped_lines": len(shipped)}, o.Status, obTarget)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// shipSerials 发货序列号逐件核销：序列号集合 = 该行已完成的序列号复核任务（复核通过
// 即实物确认），逐件校验台账状态后 SerialEvent(OUTBOUND)（与 Deduct 同事务，
// inventory service.go:1136-1137 组合契约）。
func (s *Service) shipSerials(ctx context.Context, tx *gorm.DB, actor Actor, o *OutboundOrder, lineNo int64, skuID int64, qty Qty) error {
	checks, err := s.repo.ListCheckTasksByOutbound(tx, o.OutboundNo)
	if err != nil {
		return err
	}
	serials := make([]string, 0, int64(qty)/int64(qtyScale))
	for _, c := range checks {
		if c.OutboundLineNo == lineNo && c.SKUID == skuID && c.Status == CheckStatusDone && c.SerialNo != "" {
			serials = append(serials, c.SerialNo)
		}
	}
	if int64(len(serials)) != int64(qty)/qtyScale {
		return response.NewError(ErrSerialsMissing, map[string]any{
			"outbound_no": o.OutboundNo, "line_no": lineNo,
			"ship_qty": qty.String(), "confirmed_serials": len(serials),
			"reason": "序列号 SKU 发货必须逐件序列号核销（件数与已复核序列号一致）",
		})
	}
	states, err := s.repo.ReadSerialStates(tx, skuID, serials)
	if err != nil {
		return err
	}
	byNo := map[string]SerialState{}
	for _, st := range states {
		byNo[st.SerialNo] = st
	}
	for _, sn := range serials {
		st, ok := byNo[sn]
		if !ok {
			return response.NewError(ErrSerialStateInvalid, map[string]any{"serial_no": sn, "reason": "序列号不存在或不属于该 SKU"})
		}
		if st.Status != "IN_STOCK" {
			return response.NewError(ErrSerialStateInvalid, map[string]any{
				"serial_no": sn, "status": st.Status, "reason": "序列号不在库，不可出库核销",
			})
		}
		if _, _, err := s.stock.SerialEvent(ctx, tx, SerialOp{
			SerialNo: sn, SKUID: skuID, BatchID: st.BatchID,
			WarehouseID: 0, BinID: 0, Status: "OUTBOUND",
			Source:         Source{Type: "outbound_order", No: o.OutboundNo},
			Actor:          actor,
			IdempotencyKey: "shipserial:" + o.OutboundNo + ":" + strconv.FormatInt(lineNo, 10) + ":" + sn,
			Remark:         "销售发货序列号出库",
		}); err != nil {
			return err
		}
	}
	return nil
}

// ---- 出库单：取消（PENDING_ALLOCATE..PACKED，未发货；释放全部锁） ----

// CancelOutbound 取消出库单：全部明细未发货时取消——ReleaseLock 释放全部预占、
// 拣货任务取消、销售订单行预占进度归零，同事务（plan §6.5；已发货不可取消走关闭）。
// 说明：取消后销售订单停留 APPROVED（状态机无 APPROVED 回退迁移），
// 如需重发出库属新单据需求（见域交付 risks）。
func (s *Service) CancelOutbound(ctx context.Context, actor Actor, outboundNo string, in CancelInput) (*OutboundOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	var out *OutboundOrder
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.outForUpdate(tx, outboundNo)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": outboundNo})
		}
		// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与详情接口同口径）。
		if !actor.CanAccess(o.WarehouseID) {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": outboundNo})
		}
		if err := s.cancelOutboundInTx(ctx, tx, actor, o, in.Reason); err != nil {
			return err
		}
		o.Status = OBStatusCancelled
		out = o
		return s.auditEntry(tx, "sales", "outbound_order", "cancel", o.ID.Int64(), actor, in, nil, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// cancelOutboundInTx 取消单个出库单的全部库存/任务联动（调用方持有行锁与守卫迁移责任）。
// reason 为取消原因（审计与操作日志留痕，business-flow §13.2/§13.3）。
func (s *Service) cancelOutboundInTx(ctx context.Context, tx *gorm.DB, actor Actor, ob *OutboundOrder, reason string) error {
	if !canTransition(obTransitions, ob.Status, OBStatusCancelled) {
		return response.NewError(ErrStateConflict, map[string]any{
			"outbound_no": ob.OutboundNo, "from": ob.Status, "to": OBStatusCancelled,
			"reason": "已发货/已完结出库单不可取消（business-flow §13.3）",
		})
	}
	items, err := s.repo.ListOutboundItems(tx, ob.ID.Int64())
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.QtyShipped.IsPositive() {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": ob.OutboundNo, "reason": "存在已发货数量，不可取消，走差额关闭",
			})
		}
	}
	if err := s.releaseOutboundLocks(ctx, tx, actor, ob, items, "outbound_order:"+ob.OutboundNo); err != nil {
		return err
	}
	if err := s.repo.CancelPickTasks(tx, ob.OutboundNo, 0); err != nil {
		return err
	}
	// 销售订单行预占进度归零（锁已释放；so_no 逻辑引用定位）。
	so, err := s.repo.GetSalesOrderByNo(tx, ob.SoNo)
	if err != nil {
		return err
	}
	if so != nil {
		zero := Qty(0)
		for _, it := range items {
			if err := s.repo.UpdateSalesOrderItemProgress(tx, so.ID.Int64(), it.LineNo, &zero, nil); err != nil {
				return err
			}
		}
	}
	before := ob.Status
	n, err := s.repo.MarkOutboundStatus(tx, ob.ID.Int64(), before, OBStatusCancelled, StatusStamp{By: actor.ID, Cancelled: true})
	if err != nil {
		return err
	}
	if n == 0 {
		return response.NewError(ErrStateConflict, map[string]any{"outbound_no": ob.OutboundNo, "from": before, "to": OBStatusCancelled})
	}
	ob.Status = OBStatusCancelled
	return s.auditEntry(tx, "sales", "outbound_order", "cancel", ob.ID.Int64(), actor,
		map[string]any{"reason": reason}, before, ob.Status)
}

// ---- 出库单：差额关闭（PARTIAL_SHIPPED→CLOSED，释放剩余锁） ----

// CloseOutbound 差额关闭：未发货行放弃，剩余预占锁全部释放（inventory-rules §4.2），
// 原因必填留审计（business-flow §13.3）。
func (s *Service) CloseOutbound(ctx context.Context, actor Actor, outboundNo string, in CloseInput) (*OutboundOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, response.NewError(ErrCloseReasonRequired, nil)
	}
	var out *OutboundOrder
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.outForUpdate(tx, outboundNo)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": outboundNo})
		}
		// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与详情接口同口径）。
		if !actor.CanAccess(o.WarehouseID) {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": outboundNo})
		}
		before := o.Status
		if !canTransition(obTransitions, before, OBStatusClosed) {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": outboundNo, "from": before, "to": OBStatusClosed,
			})
		}
		items, err := s.repo.ListOutboundItems(tx, o.ID.Int64())
		if err != nil {
			return err
		}
		if err := s.releaseOutboundLocks(ctx, tx, actor, o, items, "outbound_order:"+outboundNo); err != nil {
			return err
		}
		n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), before, OBStatusClosed, StatusStamp{By: actor.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"outbound_no": outboundNo, "from": before, "to": OBStatusClosed})
		}
		o.Status = OBStatusClosed
		out = o
		return s.auditEntry(tx, "sales", "outbound_order", "close", o.ID.Int64(), actor, in, before, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 重新分配（sales:allocation:execute） ----

// ReallocateInput 重新分配入参：LineNo=0 表示整单所有可重分配行；仅 ALLOCATED/PICKING
// 状态、未拣货未发货的行可重分配（plan §6.5：ReleaseLock(旧)+Lock(新)+allocation_records
// 替换，同事务）。
type ReallocateInput struct {
	OutboundNo string `json:"outbound_no"`
	LineNo     int64  `json:"line_no"`
}

// Reallocate 重新分配：旧行锁全部释放 → allocation_records 整组替换 → 未完结拣货任务
// 取消 → 按默认策略重新分配预占（relock 幂等键，plan §7）→ PICKING 状态下为新区
// 补建拣货任务。
func (s *Service) Reallocate(ctx context.Context, actor Actor, in ReallocateInput) ([]AllocationRecord, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	in.OutboundNo = strings.TrimSpace(in.OutboundNo)
	if in.OutboundNo == "" {
		return nil, errInvalidParam("outbound_no", "必填")
	}
	var (
		outRecs []AllocationRecord
	)
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.outForUpdate(tx, in.OutboundNo)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": in.OutboundNo})
		}
		// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与详情接口同口径）。
		if !actor.CanAccess(o.WarehouseID) {
			return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": in.OutboundNo})
		}
		if o.Status != OBStatusAllocated && o.Status != OBStatusPicking {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": in.OutboundNo, "from": o.Status,
				"reason": "仅 ALLOCATED/PICKING 状态可重新分配",
			})
		}
		items, err := s.repo.ListOutboundItems(tx, o.ID.Int64())
		if err != nil {
			return err
		}
		byLine := map[int64]OutboundItem{}
		for _, it := range items {
			byLine[it.LineNo] = it
		}
		if in.LineNo > 0 {
			if _, ok := byLine[in.LineNo]; !ok {
				return response.NewError(ErrShipQtyInvalid, map[string]any{"line_no": in.LineNo, "reason": "行号不存在"})
			}
		}
		// 可重分配行：未拣货且未发货（已拣实物不可"纸面重分配"）。
		eligible := map[int64]OutboundItem{}
		for lineNo, it := range byLine {
			if in.LineNo > 0 && lineNo != in.LineNo {
				continue
			}
			if it.QtyPicked.IsPositive() || it.QtyShipped.IsPositive() {
				return response.NewError(ErrReallocForbidden, map[string]any{
					"line_no": lineNo, "qty_picked": it.QtyPicked.String(), "qty_shipped": it.QtyShipped.String(),
				})
			}
			eligible[lineNo] = it
		}
		if len(eligible) == 0 {
			return response.NewError(ErrReallocForbidden, map[string]any{"reason": "无符合条件的行"})
		}
		outRecs = nil
		for lineNo, it := range eligible {
			old, err := s.repo.ListAllocations(tx, in.OutboundNo, lineNo)
			if err != nil {
				return err
			}
			// 释放旧锁（触发源 = 出库单重新分配动作）。
			for _, rec := range old {
				if rec.LockID <= 0 {
					continue
				}
				if _, err := s.stock.ReleaseLock(ctx, tx, ReleaseLockOp{
					LockID: rec.LockID, Qty: rec.Qty,
					Source:         Source{Type: "outbound_order", No: in.OutboundNo},
					Actor:          actor,
					IdempotencyKey: "release:" + in.OutboundNo + ":" + strconv.FormatInt(rec.LockID, 10),
					Remark:         "重新分配释放旧预占",
				}); err != nil {
					return err
				}
			}
			if err := s.repo.DeleteAllocations(tx, in.OutboundNo, lineNo); err != nil {
				return err
			}
			if err := s.repo.CancelPickTasks(tx, in.OutboundNo, lineNo); err != nil {
				return err
			}
			// 重新分配预占：relock 幂等键（plan §7 冻结构成 relock:{so_no}:{line_no}:{seq}，
			// seq 取纳秒时间戳——分配与释放同事务，重试路径不会命中半程键）。
			flags, err := s.checkSKUEnabled(ctx, it.SKUID)
			if err != nil {
				return err
			}
			seq := strconv.FormatInt(time.Now().UnixNano(), 10)
			lineNo := it.LineNo
			si := SalesOrderItem{LineNo: it.LineNo, SKUID: it.SKUID, Qty: it.Qty}
			ar, err := s.allocateLineWithKey(ctx, tx, actor, o.WarehouseID, o.SoNo, in.OutboundNo, si, flags,
				func(binID, batchID int64) string {
					return "relock:" + o.SoNo + ":" + strconv.FormatInt(lineNo, 10) + ":" + seq
				})
			if err != nil {
				return err
			}
			if err := s.repo.InsertAllocations(tx, ar.Records); err != nil {
				return err
			}
			lineAlloc := Qty(0)
			for _, rec := range ar.Records {
				lineAlloc = lineAlloc.Add(rec.Qty)
			}
			if lineAlloc.Sub(it.Qty).IsNegative() {
				return response.NewError(ErrReallocForbidden, map[string]any{
					"line_no": lineNo, "reason": "重分配结果数量少于明细数量",
				})
			}
			outRecs = append(outRecs, ar.Records...)
			// PICKING 状态下为新区补建拣货任务（ALLOCATED 状态待 release-to-pick 统一生成）。
			if o.Status == OBStatusPicking {
				if err := s.createPickTasksForRecords(ctx, tx, actor, in.OutboundNo, ar.Records); err != nil {
					return err
				}
			}
		}
		return s.auditEntry(tx, "sales", "allocation", "reallocate", o.ID.Int64(), actor, in, nil, len(outRecs))
	})
	if err != nil {
		return nil, err
	}
	return outRecs, nil
}

// createPickTasksForRecords 为重分配产生的分配记录补建拣货任务（PICKING 场景）。
func (s *Service) createPickTasksForRecords(ctx context.Context, tx *gorm.DB, actor Actor, outboundNo string, recs []AllocationRecord) error {
	for _, rec := range recs {
		zoneID, shelfID, found, err := s.repo.ReadBinLocation(tx, rec.WarehouseID, rec.BinID, rec.SKUID, rec.BatchID)
		if err != nil {
			return err
		}
		if !found {
			return response.NewError(response.CodeConflict, map[string]any{"reason": "分配库位库存行不存在", "bin_id": rec.BinID})
		}
		pickNo, err := s.nextNo(ctx, tx, "PK")
		if err != nil {
			return err
		}
		if err := s.repo.InsertPickTasks(tx, []PickTask{{
			PickNo: pickNo, OutboundNo: outboundNo, OutboundLineNo: rec.LineNo,
			SKUID: rec.SKUID, BatchID: rec.BatchID,
			SourceWarehouseID: rec.WarehouseID, SourceZoneID: zoneID,
			SourceShelfID: shelfID, SourceBinID: rec.BinID,
			Qty: rec.Qty, Status: PickStatusPending,
			WarehouseID: rec.WarehouseID, Remark: "重新分配补建",
			CreatedBy: actor.ID, UpdatedBy: actor.ID,
		}}); err != nil {
			return err
		}
	}
	return nil
}

// ---- 发货单物流态流转（纯记录，无库存动作——plan §6.5） ----

// ShipStatusInput 物流态流转入参。
type ShipStatusInput struct {
	Status string `json:"status"` // SHIPPED 后续：IN_TRANSIT / SIGNED / ABNORMAL
	Remark string `json:"remark"`
}

// UpdateShipmentStatus 物流态人工流转（PENDING→SHIPPED 由 Ship 事务内完成，本接口
// 只接受其后的物流态：IN_TRANSIT / SIGNED / ABNORMAL）。
func (s *Service) UpdateShipmentStatus(ctx context.Context, actor Actor, shipmentID int64, in ShipStatusInput) (*Shipment, error) {
	target := strings.ToUpper(strings.TrimSpace(in.Status))
	switch target {
	case ShipStatusInTransit, ShipStatusSigned, ShipStatusAbnormal:
	default:
		return nil, errInvalidParam("status", "仅支持 IN_TRANSIT / SIGNED / ABNORMAL（PENDING→SHIPPED 由发货确认完成）")
	}
	var out *Shipment
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		sh, err := s.repo.GetShipment(tx, shipmentID)
		if err != nil {
			return err
		}
		if sh == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"shipment_id": shipmentID})
		}
		// 数据权限 fail-closed（f16）：越仓发货单按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(sh.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"shipment_id": shipmentID})
		}
		if !canTransition(shipTransitions, sh.Status, target) {
			return response.NewError(ErrStateConflict, map[string]any{
				"shipment_no": sh.ShipmentNo, "from": sh.Status, "to": target,
			})
		}
		n, err := s.repo.MarkShipmentStatus(tx, shipmentID, sh.Status, target, StatusStamp{By: actor.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"shipment_no": sh.ShipmentNo, "from": sh.Status, "to": target})
		}
		before := sh.Status
		sh.Status = target
		out = sh
		return s.auditEntry(tx, "sales", "shipment", "update-status", shipmentID, actor, in, before, target)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListPickTasks / ListCheckTasks / ListPackages / ListShipments 列表透传。
func (s *Service) ListPickTasks(ctx context.Context, q PickQuery) ([]PickTask, int64, error) {
	return s.repo.ListPickTasks(ctx, q)
}

func (s *Service) ListCheckTasks(ctx context.Context, q CheckQuery) ([]CheckTask, int64, error) {
	return s.repo.ListCheckTasks(ctx, q)
}

func (s *Service) ListPackages(ctx context.Context, q PackingQuery) ([]PackingRecord, int64, error) {
	return s.repo.ListPackages(ctx, q)
}

func (s *Service) ListShipments(ctx context.Context, q ShipmentQuery) ([]Shipment, int64, error) {
	return s.repo.ListShipments(ctx, q)
}
