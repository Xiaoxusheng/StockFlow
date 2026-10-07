package sales

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// 出库执行链 Service（business-flow §7.2/§8、plan §6.5）：
// 分配后出库单（ALLOCATED）→ 生成拣货任务（PICKING）→ 领取（原子抢占）→ 拣货确认
// → 复核（领取为原子指派）→ 打包（幂等键）→ 发货确认（单事务：Deduct 核销预占 +
// 序列号逐件 OUTBOUND + shipments + 状态推进；幂等键防重复扣减）。
// 序列号 SKU 的逐序列号操作落位：拣货确认采集并校验序列号 → 复核逐件确认（一行一件）
// → 发货逐件 SerialEvent(OUTBOUND)（inventory-rules §8.2"出库必须逐个序列号操作"）。

// outForUpdate 按单号取出库单并加行锁（nil = 不存在）。
func (s *Service) outForUpdate(tx *gorm.DB, no string) (*OutboundOrder, error) {
	o, err := s.repo.GetOutboundOrderByNo(tx, no)
	if err != nil || o == nil {
		return nil, err
	}
	return s.repo.GetOutboundOrderForUpdate(tx, o.ID.Int64())
}

// ---- 出库单：查询 ----

// GetOutboundDetail 出库单详情（单据 + 明细 + 分配 + 任务族；行级数据权限）。
func (s *Service) GetOutboundDetail(ctx context.Context, no string, scope Scope) (*OutboundOrder, []OutboundItem, []AllocationRecord, []PickTask, []CheckTask, []PackingRecord, []Shipment, error) {
	var (
		o        *OutboundOrder
		items    []OutboundItem
		allocs   []AllocationRecord
		picks    []PickTask
		checks   []CheckTask
		packages []PackingRecord
		ships    []Shipment
		err      error
	)
	err = s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err = s.repo.GetOutboundOrderByNo(tx, no)
		if err != nil || o == nil {
			return err
		}
		if !scope.visible(o.WarehouseID) {
			o = nil
			return nil
		}
		if items, err = s.repo.ListOutboundItems(tx, o.ID.Int64()); err != nil {
			return err
		}
		if allocs, err = s.repo.ListAllocationsByOutbound(tx, no); err != nil {
			return err
		}
		if picks, err = s.repo.ListPickTasksByOutbound(tx, no); err != nil {
			return err
		}
		if checks, err = s.repo.ListCheckTasksByOutbound(tx, no); err != nil {
			return err
		}
		if packages, err = s.repo.ListPackagesByOutbound(tx, no); err != nil {
			return err
		}
		ships, err = s.repo.ListShipmentsByOutbound(tx, no)
		return err
	})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	if o == nil {
		return nil, nil, nil, nil, nil, nil, nil, response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": no})
	}
	return o, items, allocs, picks, checks, packages, ships, nil
}

// ListOutboundOrders 出库单列表。
func (s *Service) ListOutboundOrders(ctx context.Context, q OutboundQuery) ([]OutboundOrder, int64, error) {
	return s.repo.ListOutboundOrders(ctx, q)
}

// ListAllocations 分配记录列表。
func (s *Service) ListAllocations(ctx context.Context, q AllocationQuery) ([]AllocationRecord, int64, error) {
	return s.repo.ListAllocationsPage(ctx, q)
}

// ---- 拣货：生成任务（ALLOCATED→PICKING） ----

// GeneratePickTasks 生成拣货任务：每个分配记录（行×批次×库位）一张任务，
// 来源四维取自分配记录（zone/shelf 经库存行只读定位回填）——business-flow §8.2
// 任务内容"SKU→来源库位→数量"；plan §6.5"整单一张或多行任务"。幂等性由
// ALLOCATED→PICKING 状态守卫保证（行锁 + 守卫 UPDATE，重复调用 409）。
func (s *Service) GeneratePickTasks(ctx context.Context, actor Actor, outboundNo string) (*OutboundOrder, []PickTask, error) {
	if err := s.depsReady(); err != nil {
		return nil, nil, err
	}
	var (
		out   *OutboundOrder
		tasks []PickTask
	)
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
		if o.Status != OBStatusAllocated {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": outboundNo, "from": o.Status, "to": OBStatusPicking,
				"reason": "仅已分配（ALLOCATED）状态可生成拣货任务",
			})
		}
		recs, err := s.repo.ListAllocationsByOutbound(tx, outboundNo)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			return response.NewError(response.CodeConflict, map[string]any{"reason": "出库单无分配记录，禁止生成拣货任务"})
		}
		for _, rec := range recs {
			zoneID, shelfID, found, err := s.repo.ReadBinLocation(tx, rec.WarehouseID, rec.BinID, rec.SKUID, rec.BatchID)
			if err != nil {
				return err
			}
			if !found {
				return response.NewError(response.CodeConflict, map[string]any{
					"reason": "分配库位库存行不存在", "bin_id": rec.BinID, "sku_id": rec.SKUID,
				})
			}
			pickNo, err := s.nextNo(ctx, tx, "PK")
			if err != nil {
				return err
			}
			tasks = append(tasks, PickTask{
				PickNo: pickNo, OutboundNo: outboundNo, OutboundLineNo: rec.LineNo,
				SKUID: rec.SKUID, BatchID: rec.BatchID,
				SourceWarehouseID: rec.WarehouseID, SourceZoneID: zoneID,
				SourceShelfID: shelfID, SourceBinID: rec.BinID,
				Qty: rec.Qty, Status: PickStatusPending,
				WarehouseID: rec.WarehouseID, CreatedBy: actor.ID, UpdatedBy: actor.ID,
			})
		}
		if err := s.repo.InsertPickTasks(tx, tasks); err != nil {
			return err
		}
		if n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), OBStatusAllocated, OBStatusPicking, StatusStamp{By: actor.ID}); err != nil {
			return err
		} else if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"outbound_no": outboundNo, "from": o.Status, "to": OBStatusPicking})
		}
		o.Status = OBStatusPicking
		out = o
		return s.auditEntry(tx, "sales", "outbound_order", "release-to-pick", o.ID.Int64(), actor,
			map[string]any{"task_count": len(tasks)}, OBStatusAllocated, OBStatusPicking)
	})
	if err != nil {
		return nil, nil, err
	}
	return out, tasks, nil
}

// ---- 拣货：领取（原子抢占） ----

// ClaimPickTask 领取拣货任务（architecture.md §5.2：原子抢占，0 行 = 409 领取冲突）。
func (s *Service) ClaimPickTask(ctx context.Context, actor Actor, taskID int64) (*PickTask, error) {
	var out *PickTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetPickTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		n, err := s.repo.ClaimPickTask(tx, taskID, actor.ID, actor.Name)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrClaimConflict, map[string]any{"pick_task_id": taskID, "status": t.Status})
		}
		t.Status, t.AssigneeID, t.AssigneeName = PickStatusClaimed, actor.ID, actor.Name
		out = t
		return s.auditEntry(tx, "sales", "pick_task", "claim", taskID, actor, nil, PickStatusPending, PickStatusClaimed)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PickConfirmInput 拣货确认入参。
type PickConfirmInput struct {
	PickedQty   Qty      `json:"picked_qty"`
	Serials     []string `json:"serials"`      // 序列号 SKU 必填：逐件采集（inventory-rules §8.2）
	ScannedCode string   `json:"scanned_code"` // 扫码录入值（M2 记录不解析，plan §12 扫码不做）
}

// ConfirmPick 拣货确认（CLAIMED→PICKED）：数量守卫（≤ 任务量）、序列号 SKU 逐件校验
// （台账 IN_STOCK 且位于来源库位）、联动创建复核任务（序列号 SKU 一件一行）、
// 出库单明细拣货进度汇总与 PICKING→PICKED 推进（全部任务 PICKED 时）。
func (s *Service) ConfirmPick(ctx context.Context, actor Actor, taskID int64, in PickConfirmInput) (*PickTask, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	if !in.PickedQty.IsPositive() {
		return nil, errInvalidParam("picked_qty", "必须为正数")
	}
	var out *PickTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetPickTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		if t.Status != PickStatusClaimed {
			return response.NewError(ErrStateConflict, map[string]any{
				"pick_no": t.PickNo, "from": t.Status, "to": PickStatusPicked,
			})
		}
		if in.PickedQty.Sub(t.Qty).IsPositive() {
			return response.NewError(ErrPickExceed, map[string]any{
				"pick_no": t.PickNo, "task_qty": t.Qty.String(), "picked_qty": in.PickedQty.String(),
			})
		}
		flags, err := s.checkSKUEnabled(ctx, t.SKUID)
		if err != nil {
			return err
		}
		// 序列号 SKU：逐序列号校验（不存在/不属于该 SKU/不在库/不在来源库位 → 拒绝）。
		if flags.SerialManaged {
			if err := s.validateSerialsForPick(tx, t, in.Serials, in.PickedQty); err != nil {
				return err
			}
		} else if len(in.Serials) > 0 {
			return errInvalidParam("serials", "非序列号 SKU 不接受序列号")
		}
		scanned := strings.TrimSpace(in.ScannedCode)
		n, err := s.repo.MarkPickTaskStatus(tx, taskID, PickStatusClaimed, PickStatusPicked, PickStamp{
			By: actor.ID, PickedQty: &in.PickedQty, ScannedCode: &scanned, StampPickedAt: true,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"pick_no": t.PickNo, "from": t.Status, "to": PickStatusPicked})
		}
		// 短拣重同步（M2 修复轮）：实拣 < 任务量时同步收缩该任务的预占分配——
		// 释放差额锁 + 分配记录数量改写为实拣量 + 销售订单行 qty_allocated 收缩。
		// 否则发货确认按 allocation_records 全额逐条 Deduct（service_ship.go），短拣
		// 差额会被二次扣减、与 qty_shipped 口径互相矛盾（inventory-rules §9.2 账实一致）。
		if in.PickedQty.Sub(t.Qty).IsNegative() {
			if err := s.resyncAllocationAfterShortPick(ctx, tx, actor, t, in.PickedQty); err != nil {
				return err
			}
		}
		// 联动创建复核任务（§8.3：序列号 SKU 逐件一行一件；普通 SKU 每拣货任务一张）。
		if err := s.createCheckTasksForPick(ctx, tx, actor, t, in.PickedQty, in.Serials); err != nil {
			return err
		}
		if err := s.refreshOutboundAfterTask(tx, actor, t.OutboundNo, t.OutboundLineNo, "picked"); err != nil {
			return err
		}
		t.Status, t.PickedQty, t.ScannedCode = PickStatusPicked, in.PickedQty, scanned
		out = t
		return s.auditEntry(tx, "sales", "pick_task", "confirm", taskID, actor, in, PickStatusClaimed, PickStatusPicked)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// validateSerialsForPick 拣货序列号校验：件数 = 拣货量（序列号 SKU 单件 qty=1），
// 逐件核对台账（IN_STOCK + 位置匹配来源库位，inventory-rules §8）。
func (s *Service) validateSerialsForPick(tx *gorm.DB, t *PickTask, serials []string, picked Qty) error {
	clean := make([]string, 0, len(serials))
	seen := map[string]bool{}
	for _, sn := range serials {
		sn = strings.TrimSpace(sn)
		if sn == "" {
			return errInvalidParam("serials", "序列号不能为空串")
		}
		if seen[sn] {
			return errInvalidParam("serials", "序列号重复")
		}
		seen[sn] = true
		clean = append(clean, sn)
	}
	if int64(len(clean)) != int64(picked)/qtyScale {
		return response.NewError(ErrSerialsMissing, map[string]any{
			"pick_no": t.PickNo, "picked_qty": picked.String(), "serial_count": len(clean),
			"reason": "序列号 SKU 拣货必须逐件采集序列号（件数与数量一致）",
		})
	}
	states, err := s.repo.ReadSerialStates(tx, t.SKUID, clean)
	if err != nil {
		return err
	}
	byNo := map[string]SerialState{}
	for _, st := range states {
		byNo[st.SerialNo] = st
	}
	for _, sn := range clean {
		st, ok := byNo[sn]
		if !ok {
			return response.NewError(ErrSerialStateInvalid, map[string]any{
				"serial_no": sn, "sku_id": t.SKUID, "reason": "序列号不存在或不属于该 SKU",
			})
		}
		if st.Status != "IN_STOCK" || st.WarehouseID != t.SourceWarehouseID || st.BinID != t.SourceBinID {
			return response.NewError(ErrSerialStateInvalid, map[string]any{
				"serial_no": sn, "status": st.Status,
				"warehouse_id": st.WarehouseID, "bin_id": st.BinID,
				"reason": "序列号不在来源库位的在库状态，不可拣货",
			})
		}
	}
	return nil
}

// createCheckTasksForPick 拣货确认后联动创建复核任务：
//   - 序列号 SKU：每件一行（serial_no 落列，qty=1，复核逐件确认 §8.3）；
//   - 普通 SKU：每拣货任务一张（qty=拣货量，复核 SKU/条码/数量/批次）。
func (s *Service) createCheckTasksForPick(ctx context.Context, tx *gorm.DB, actor Actor, t *PickTask, picked Qty, serials []string) error {
	var tasks []CheckTask
	if len(serials) > 0 {
		for _, sn := range serials {
			checkNo, err := s.nextNo(ctx, tx, "CH")
			if err != nil {
				return err
			}
			tasks = append(tasks, CheckTask{
				CheckNo: checkNo, OutboundNo: t.OutboundNo, OutboundLineNo: t.OutboundLineNo,
				SKUID: t.SKUID, BatchID: t.BatchID, SerialNo: sn, Qty: qtyOne,
				Status: CheckStatusPending, WarehouseID: t.WarehouseID,
				CreatedBy: actor.ID, UpdatedBy: actor.ID,
			})
		}
	} else {
		checkNo, err := s.nextNo(ctx, tx, "CH")
		if err != nil {
			return err
		}
		tasks = append(tasks, CheckTask{
			CheckNo: checkNo, OutboundNo: t.OutboundNo, OutboundLineNo: t.OutboundLineNo,
			SKUID: t.SKUID, BatchID: t.BatchID, Qty: picked,
			Status: CheckStatusPending, WarehouseID: t.WarehouseID,
			CreatedBy: actor.ID, UpdatedBy: actor.ID,
		})
	}
	return s.repo.InsertCheckTasks(tx, tasks)
}

// qtyOne 序列号件单件数量（Qty 标度 1e-4，即 1.0000）。
var qtyOne = Qty(10000)

// resyncAllocationAfterShortPick 短拣分配重同步（ConfirmPick 事务内调用，调用方持有
// 任务行锁）：
//  1. 释放差额预占（ReleaseLock 部分释放，锁记录保留 ACTIVE、qty 递减——inventory
//     service.go ReleaseLock 拆分语义），幂等键 shortpick:{outbound_no}:{pick_no}:{lock_id}
//     （与取消/关闭的 release:{outbound_no}:{lock_id} 区分：本释放只发生一次，
//     任务 CLAIMED→PICKED 状态守卫保证；后续整单释放释放的是余量）；
//  2. 分配记录数量改写为实拣量（reason 留短拣注记，审计经操作日志）；
//  3. 销售订单行 qty_allocated 同步收缩（预占进度与实际持有锁一致）。
func (s *Service) resyncAllocationAfterShortPick(ctx context.Context, tx *gorm.DB, actor Actor, t *PickTask, picked Qty) error {
	recs, err := s.repo.ListAllocations(tx, t.OutboundNo, t.OutboundLineNo)
	if err != nil {
		return err
	}
	var rec *AllocationRecord
	for i := range recs {
		r := &recs[i]
		// 分配记录与拣货任务一一对应（GeneratePickTasks/createPickTasksForRecords：
		// 每记录一张任务），按记录维度四元组 + 数量精确匹配。
		if r.SKUID == t.SKUID && r.BatchID == t.BatchID &&
			r.WarehouseID == t.SourceWarehouseID && r.BinID == t.SourceBinID &&
			r.Qty == t.Qty {
			rec = r
			break
		}
	}
	if rec == nil {
		return response.NewError(response.CodeConflict, map[string]any{
			"reason":  "短拣重同步失败：未找到与拣货任务对应的分配记录",
			"pick_no": t.PickNo, "outbound_no": t.OutboundNo,
			"line_no": t.OutboundLineNo, "sku_id": t.SKUID, "bin_id": t.SourceBinID,
		})
	}
	excess := t.Qty.Sub(picked)
	if excess.IsPositive() && rec.LockID > 0 {
		if _, err := s.stock.ReleaseLock(ctx, tx, ReleaseLockOp{
			LockID: rec.LockID, Qty: excess,
			Source:         Source{Type: "outbound_order", No: t.OutboundNo},
			Actor:          actor,
			IdempotencyKey: "shortpick:" + t.OutboundNo + ":" + t.PickNo + ":" + strconv.FormatInt(rec.LockID, 10),
			Remark:         "拣货短拣释放差额预占（实拣 " + picked.String() + " / 任务 " + t.Qty.String() + "）",
		}); err != nil {
			return err
		}
	}
	if err := s.repo.UpdateAllocationQty(tx, rec.ID.Int64(), picked, actor.ID, map[string]any{
		"short_pick": map[string]any{
			"pick_no":      t.PickNo,
			"task_qty":     t.Qty.String(),
			"picked_qty":   picked.String(),
			"released_qty": excess.String(),
		},
	}); err != nil {
		return err
	}
	rec.Qty = picked
	// 销售订单行预占进度收缩（so_no 逻辑引用定位，与 cancelOutboundInTx 同口径）。
	o, err := s.repo.GetOutboundOrderByNo(tx, t.OutboundNo)
	if err != nil {
		return err
	}
	if o != nil {
		so, err := s.repo.GetSalesOrderByNo(tx, o.SoNo)
		if err != nil {
			return err
		}
		if so != nil {
			soItems, err := s.repo.ListSalesOrderItems(tx, so.ID.Int64())
			if err != nil {
				return err
			}
			for _, it := range soItems {
				if it.LineNo == t.OutboundLineNo {
					allocated := it.QtyAllocated.Sub(excess)
					if err := s.repo.UpdateSalesOrderItemProgress(tx, so.ID.Int64(), it.LineNo, &allocated, nil); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	return nil
}

// ---- 拣货：异常上报（CLAIMED→EXCEPTION，联动异常中心） ----

// ReportPickException 缺货/少货/库位异常上报（business-flow §8.2）：任务转 EXCEPTION
// 并登记异常中心（该行后续可重新分配——plan §6.5）。
func (s *Service) ReportPickException(ctx context.Context, actor Actor, taskID int64, reason string) (*PickTask, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, errInvalidParam("reason", "异常原因必填")
	}
	var out *PickTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetPickTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": taskID})
		}
		n, err := s.repo.MarkPickTaskStatus(tx, taskID, PickStatusClaimed, PickStatusException, PickStamp{By: actor.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"pick_no": t.PickNo, "from": t.Status, "to": PickStatusException})
		}
		if err := s.requireExceptions(); err != nil {
			return err
		}
		if _, err := s.exceptions.Create(ctx, tx, "拣货", "outbound_order", t.OutboundNo, ExceptionDetail{
			SKUID: t.SKUID, BinID: t.SourceBinID, BatchID: t.BatchID,
			LineNo: t.OutboundLineNo, Reason: reason,
		}); err != nil {
			return err
		}
		t.Status = PickStatusException
		out = t
		return s.auditEntry(tx, "sales", "pick_task", "report-exception", taskID, actor,
			map[string]any{"reason": reason}, PickStatusClaimed, PickStatusException)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// refreshOutboundAfterTask 任务落定后刷新出库单：明细进度汇总 + 阶段完成状态推进。
// kind: picked（拣货）→ 全部任务 PICKED 时 PICKING→PICKED；
//
//	checked（复核）→ 全部任务 DONE 且无 EXCEPTION 时 PICKED→CHECKED。
func (s *Service) refreshOutboundAfterTask(tx *gorm.DB, actor Actor, outboundNo string, lineNo int64, kind string) error {
	o, err := s.repo.GetOutboundOrderByNo(tx, outboundNo)
	if err != nil {
		return err
	}
	if o == nil {
		return response.NewError(ErrOutboundNotFound, map[string]any{"outbound_no": outboundNo})
	}
	switch kind {
	case "picked":
		tasks, err := s.repo.ListPickTasksByOutbound(tx, outboundNo)
		if err != nil {
			return err
		}
		var lineQty Qty
		for _, t := range tasks {
			if t.OutboundLineNo == lineNo && t.Status == PickStatusPicked {
				lineQty = lineQty.Add(t.PickedQty)
			}
		}
		if err := s.repo.UpdateOutboundItemProgress(tx, o.ID.Int64(), lineNo, OutboundItemProgress{Picked: &lineQty}); err != nil {
			return err
		}
		allPicked := len(tasks) > 0
		for _, t := range tasks {
			if t.Status != PickStatusPicked {
				allPicked = false
				break
			}
		}
		if allPicked && o.Status == OBStatusPicking {
			n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), OBStatusPicking, OBStatusPicked, StatusStamp{By: actor.ID, Picked: true})
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrStateConflict, map[string]any{"outbound_no": outboundNo, "from": o.Status, "to": OBStatusPicked})
			}
		}
	case "checked":
		tasks, err := s.repo.ListCheckTasksByOutbound(tx, outboundNo)
		if err != nil {
			return err
		}
		var lineQty Qty
		for _, t := range tasks {
			if t.OutboundLineNo == lineNo && t.Status == CheckStatusDone {
				lineQty = lineQty.Add(t.Qty)
			}
		}
		if err := s.repo.UpdateOutboundItemProgress(tx, o.ID.Int64(), lineNo, OutboundItemProgress{Checked: &lineQty}); err != nil {
			return err
		}
		allDone := len(tasks) > 0
		for _, t := range tasks {
			if t.Status != CheckStatusDone {
				allDone = false
				break
			}
		}
		if allDone && o.Status == OBStatusPicked {
			n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), OBStatusPicked, OBStatusChecked, StatusStamp{By: actor.ID, Checked: true})
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrStateConflict, map[string]any{"outbound_no": outboundNo, "from": o.Status, "to": OBStatusChecked})
			}
		}
	}
	return nil
}

// ---- 复核 ----

// CheckClaimInput 复核任务领取（原子指派：值域无 CLAIMED，不迁移状态）。
func (s *Service) ClaimCheckTask(ctx context.Context, actor Actor, taskID int64) (*CheckTask, error) {
	var out *CheckTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetCheckTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		n, err := s.repo.AssignCheckTask(tx, taskID, actor.ID, actor.Name)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrClaimConflict, map[string]any{"check_no": t.CheckNo, "status": t.Status})
		}
		t.AssigneeID, t.AssigneeName = actor.ID, actor.Name
		out = t
		return s.auditEntry(tx, "sales", "check_task", "claim", taskID, actor, nil, nil, actor.ID)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckConfirmInput 复核确认入参：Pass=true 复核通过；false 时 Result 必须为
// business-flow §8.3 五类异常之一（错货/少货/多货/批次错误/序列号错误）。
type CheckConfirmInput struct {
	Pass   bool   `json:"pass"`
	Result string `json:"result"`
	Serial string `json:"serial"` // 序列号任务必填：重新扫描确认的序列号（§8.3 逐件确认）
}

// ConfirmCheck 复核确认（PENDING→DONE | PENDING→EXCEPTION）：通过时序列号任务必须
// 逐件核对一致；异常时联动异常中心并保持出库单 PICKED 等待处理。
func (s *Service) ConfirmCheck(ctx context.Context, actor Actor, taskID int64, in CheckConfirmInput) (*CheckTask, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	var out *CheckTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetCheckTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		if t.Status != CheckStatusPending {
			return response.NewError(ErrStateConflict, map[string]any{"check_no": t.CheckNo, "from": t.Status})
		}
		in.Serial = strings.TrimSpace(in.Serial)
		if in.Pass {
			if t.SerialNo != "" && in.Serial != t.SerialNo {
				return errInvalidParam("serial", "复核通过时序列号必须与任务序列号一致（不一致请走异常上报）")
			}
			n, err := s.repo.MarkCheckTaskStatus(tx, taskID, CheckStatusPending, CheckStatusDone, "")
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrClaimConflict, map[string]any{"check_no": t.CheckNo, "reason": "任务已被处理"})
			}
			if err := s.refreshOutboundAfterTask(tx, actor, t.OutboundNo, t.OutboundLineNo, "checked"); err != nil {
				return err
			}
			t.Status = CheckStatusDone
			out = t
			return s.auditEntry(tx, "sales", "check_task", "confirm", taskID, actor, in, CheckStatusPending, CheckStatusDone)
		}
		// 异常路径：result 必须为五类之一，登记异常中心（fail-closed）。
		if !checkResults[in.Result] {
			return errInvalidParam("result", "复核异常必须为：错货/少货/多货/批次错误/序列号错误")
		}
		if t.SerialNo != "" && in.Result == "序列号错误" && in.Serial != "" && in.Serial != t.SerialNo {
			// 扫描值与任务序列号不同属"序列号错误"的佐证，记录进异常详情。
			_ = in.Serial
		}
		n, err := s.repo.MarkCheckTaskStatus(tx, taskID, CheckStatusPending, CheckStatusException, in.Result)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrClaimConflict, map[string]any{"check_no": t.CheckNo, "reason": "任务已被处理"})
		}
		if err := s.requireExceptions(); err != nil {
			return err
		}
		if _, err := s.exceptions.Create(ctx, tx, "复核", "outbound_order", t.OutboundNo, ExceptionDetail{
			SKUID: t.SKUID, BatchID: t.BatchID, SerialNo: t.SerialNo,
			LineNo: t.OutboundLineNo, Reason: in.Result, Extra: map[string]any{"scanned": in.Serial},
		}); err != nil {
			return err
		}
		t.Status, t.Result = CheckStatusException, in.Result
		out = t
		return s.auditEntry(tx, "sales", "check_task", "report-exception", taskID, actor, in, CheckStatusPending, CheckStatusException)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReopenCheckTask 复核异常重开（EXCEPTION→PENDING，sales:check:execute）：异常处置
// 完成后允许重新复核——出库单停留 PICKED，全部任务 DONE 后照常推进 CHECKED。
// 守卫迁移（WHERE status='EXCEPTION'）+ 审计；没有该恢复通路，复核异常将把出库单
// 永久卡死在 PICKED（唯一出路整单取消，而取消后 SO 停留 APPROVED 无重发入口）。
func (s *Service) ReopenCheckTask(ctx context.Context, actor Actor, taskID int64) (*CheckTask, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	var out *CheckTask
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetCheckTask(tx, taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与列表行级过滤同口径）。
		if !actor.CanAccess(t.WarehouseID) {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": taskID})
		}
		n, err := s.repo.MarkCheckTaskStatus(tx, taskID, CheckStatusException, CheckStatusPending, "")
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{
				"check_no": t.CheckNo, "from": t.Status, "to": CheckStatusPending,
				"reason": "仅异常（EXCEPTION）任务可重开",
			})
		}
		if err := s.refreshOutboundAfterTask(tx, actor, t.OutboundNo, t.OutboundLineNo, "checked"); err != nil {
			return err
		}
		t.Status, t.Result = CheckStatusPending, ""
		out = t
		return s.auditEntry(tx, "sales", "check_task", "reopen", taskID, actor, nil, CheckStatusException, CheckStatusPending)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 打包（CHECKED 窗口内多包裹，幂等键防重复提交） ----

// PackLineInput 包裹×明细行。
type PackLineInput struct {
	LineNo int64 `json:"line_no"`
	Qty    Qty   `json:"qty"`
}

// PackInput 打包入参（business-flow §8.4 全列；一个订单允许多个包裹——多对多拆包）。
type PackInput struct {
	OutboundNo      string          `json:"outbound_no"`
	Lines           []PackLineInput `json:"lines"`
	PackingMaterial string          `json:"packing_material"`
	Length          Qty             `json:"length"`
	Width           Qty             `json:"width"`
	Height          Qty             `json:"height"`
	Weight          Qty             `json:"weight"`
	Volume          Qty             `json:"volume"`
	Carrier         string          `json:"carrier"`
	TrackingNo      string          `json:"tracking_no"`
	Remark          string          `json:"remark"`
	IdempotencyKey  string          `json:"idempotency_key"`
}

// PackResult 打包结果（Replay=true 幂等重放：返回既有包裹，不重复落库）。
type PackResult struct {
	Package *PackingRecord `json:"package"`
	Replay  bool           `json:"replay"`
}

// Pack 打包：校验各明细行累计打包量 ≤ 已复核量；落 packing_records + packing_items；
// 全部明细打包完成时推进 CHECKED→PACKED（plan §6.5）。幂等：Idempotency-Key 命中
// 既有包裹直接返回（部分唯一索引 uk_packing_records_idempotency 兜底并发）。
func (s *Service) Pack(ctx context.Context, actor Actor, in PackInput) (*PackResult, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	in.OutboundNo = strings.TrimSpace(in.OutboundNo)
	if in.OutboundNo == "" {
		return nil, errInvalidParam("outbound_no", "必填")
	}
	if len(in.Lines) == 0 {
		return nil, errInvalidParam("lines", "至少一行包裹明细")
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 128 {
		return nil, errInvalidParam("idempotency_key", "长度不能超过 128")
	}
	var res *PackResult
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		if key != "" {
			existing, err := s.repo.FindPackageByIdempotencyKey(tx, key)
			if err != nil {
				return err
			}
			if existing != nil {
				res = &PackResult{Package: existing, Replay: true}
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
		if o.Status != OBStatusChecked {
			return response.NewError(ErrStateConflict, map[string]any{
				"outbound_no": in.OutboundNo, "from": o.Status,
				"reason": "仅复核通过（CHECKED）状态可打包；全部明细打包完成即转 PACKED",
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
		packed, err := s.repo.SumPackedByLine(tx, o.ID.Int64())
		if err != nil {
			return err
		}
		seenLine := map[int64]bool{}
		packItems := make([]PackingItem, 0, len(in.Lines))
		for _, l := range in.Lines {
			it, ok := byLine[l.LineNo]
			if !ok {
				return response.NewError(ErrShipQtyInvalid, map[string]any{"line_no": l.LineNo, "reason": "行号不存在"})
			}
			if seenLine[l.LineNo] {
				return errInvalidParam("lines", "行号重复")
			}
			seenLine[l.LineNo] = true
			if !l.Qty.IsPositive() {
				return errInvalidParam("lines", "打包数量必须为正数")
			}
			newTotal := packed[l.LineNo].Add(l.Qty)
			if newTotal.Sub(it.QtyChecked).IsPositive() {
				return response.NewError(ErrPackExceed, map[string]any{
					"line_no": l.LineNo, "checked_qty": it.QtyChecked.String(),
					"packed_qty": packed[l.LineNo].String(), "pack_qty": l.Qty.String(),
				})
			}
			packItems = append(packItems, PackingItem{
				OutboundID: o.ID.Int64(), LineNo: l.LineNo, Qty: l.Qty,
				CreatedBy: actor.ID, UpdatedBy: actor.ID,
			})
		}
		packageNo, err := s.nextNo(ctx, tx, "BP")
		if err != nil {
			return err
		}
		p := &PackingRecord{
			PackageNo: packageNo, OutboundNo: in.OutboundNo,
			PackingMaterial: in.PackingMaterial, Length: in.Length, Width: in.Width,
			Height: in.Height, Weight: in.Weight, Volume: in.Volume,
			Carrier: in.Carrier, TrackingNo: in.TrackingNo,
			WarehouseID: o.WarehouseID, Remark: in.Remark,
			CreatedBy: actor.ID, UpdatedBy: actor.ID,
		}
		if key != "" {
			k := key
			p.IdempotencyKey = &k
		}
		if err := s.repo.InsertPackage(tx, p, packItems); err != nil {
			if pgUniqueViolation(err, "uk_packing_records_idempotency") {
				// 并发同键：整体回滚后回读既有包裹（幂等重放）。
				existing, ferr := s.repo.FindPackageByIdempotencyKey(tx, key)
				if ferr == nil && existing != nil {
					res = &PackResult{Package: existing, Replay: true}
					return nil
				}
			}
			return err
		}
		// 全部明细打包完成 → CHECKED→PACKED（plan §6.5）。
		packed, err = s.repo.SumPackedByLine(tx, o.ID.Int64())
		if err != nil {
			return err
		}
		allPacked := true
		for _, it := range items {
			if packed[it.LineNo].Sub(it.QtyChecked).IsNegative() {
				allPacked = false
				break
			}
		}
		if allPacked {
			n, err := s.repo.MarkOutboundStatus(tx, o.ID.Int64(), OBStatusChecked, OBStatusPacked, StatusStamp{By: actor.ID, Packed: true})
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrStateConflict, map[string]any{"outbound_no": in.OutboundNo, "from": o.Status, "to": OBStatusPacked})
			}
		}
		for i := range items {
			if q := packed[items[i].LineNo]; q.IsPositive() {
				if err := s.repo.UpdateOutboundItemProgress(tx, o.ID.Int64(), items[i].LineNo, OutboundItemProgress{Packed: &q}); err != nil {
					return err
				}
			}
		}
		res = &PackResult{Package: p}
		return s.auditEntry(tx, "sales", "packing_record", "pack", p.ID.Int64(), actor, in, nil, p)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
