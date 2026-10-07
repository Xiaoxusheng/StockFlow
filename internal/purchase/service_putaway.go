package purchase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 上架任务业务（business-flow §5、architecture §5.2、plan §6.3/§7）：
// 任务由收货确认生成；PENDING→IN_PROGRESS 原子抢占（UPDATE ... WHERE status='PENDING'，
// 0 行=领取冲突）；IN_PROGRESS→COMPLETED 上架确认，同事务经 StockGateway.Putaway 落账
// （免检直达 available；经检进入待检 pending_inspect——质检单执行事务再转 available/defective），
// 序列号任务逐件 SerialEvent 更新库位；全部任务完成驱动入库单 AWAITING_PUTAWAY→COMPLETED。

// PutawayExecuteInput 上架确认入参（§5.2：数量确认、批次确认、扫码库位=库位改指定）。
type PutawayExecuteInput struct {
	BinID  int64  `json:"bin_id"` // 可选：实际确认库位（缺省=任务目标库位）
	Remark string `json:"remark"`
}

// recommendBin 目标库位解析：显式指定（BinChecker 校验）优先，其次推荐库位服务
// （§5.3 基础规则：同 SKU 集中+剩余容量+库位启用，plan §12 分级交付）；
// 两者皆不可用 → ErrBinRequired（fail-closed，不造假推荐）。
func (s *Service) recommendBin(ctx context.Context, warehouseID, skuID int64, qty stock.Qty, explicitBin int64) (binID, zoneID, shelfID int64, err error) {
	if explicitBin > 0 {
		if s.opt.bins == nil {
			return 0, 0, 0, response.NewError(ErrCheckerMissing, map[string]any{"reason": "库位校验服务未装配"})
		}
		ok, err := s.opt.bins.ExistsActive(ctx, warehouseID, explicitBin)
		if err != nil {
			return 0, 0, 0, response.NewError(response.CodeInternalError, map[string]any{"reason": "库位校验失败", "error": err.Error()})
		}
		if !ok {
			return 0, 0, 0, response.NewError(ErrPutawayBinInvalid, map[string]any{
				"warehouse_id": warehouseID, "bin_id": explicitBin,
			})
		}
		return explicitBin, 0, 0, nil
	}
	if s.opt.recommender == nil {
		return 0, 0, 0, response.NewError(ErrBinRequired, map[string]any{
			"warehouse_id": warehouseID, "sku_id": skuID,
			"reason": "未指定目标库位且推荐库位服务未装配",
		})
	}
	q, _ := strconv.ParseFloat(qty.String(), 64)
	sugs, err := s.opt.recommender.Recommend(ctx, warehouseID, skuID, q)
	if err != nil {
		return 0, 0, 0, response.NewError(response.CodeInternalError, map[string]any{"reason": "推荐库位服务失败", "error": err.Error()})
	}
	if len(sugs) == 0 {
		return 0, 0, 0, response.NewError(ErrBinRequired, map[string]any{
			"warehouse_id": warehouseID, "sku_id": skuID, "reason": "推荐库位服务无可推荐库位",
		})
	}
	return sugs[0].BinID, sugs[0].ZoneID, sugs[0].ShelfID, nil
}

// generatePutawayTasks 收货确认事务内生成上架任务（PW 单号；批次 SKU 按 (SKU,批次)
// 一任务、序列号 SKU 逐件一任务 qty=1——inventory-rules §8.2）。
func (s *Service) generatePutawayTasks(ctx context.Context, tx *gorm.DB, actor Actor,
	inbound *InboundOrder, plan *receiptPlan, receiptNo string) ([]string, error) {

	// 按 (sku, batch, from_state, bin) 归并任务需求（同 SKU 多行同目标合并）。
	type taskKey struct {
		sku, batch, bin int64
		serial          string
	}
	type taskDemand struct {
		qty            stock.Qty
		zone, shelf    int64
		requireInspect bool
	}
	demands := map[taskKey]*taskDemand{}
	var order []taskKey

	binResolve := func(lp *receiptLinePlan) (int64, int64, int64, error) {
		if lp.binID > 0 {
			return lp.binID, lp.zoneID, lp.shelfID, nil
		}
		binID, zoneID, shelfID, err := s.recommendBin(ctx, inbound.WarehouseID, lp.input.SKUID, lp.input.QtyGood, lp.input.TargetBinID)
		if err != nil {
			return 0, 0, 0, err
		}
		lp.binID, lp.zoneID, lp.shelfID = binID, zoneID, shelfID
		return binID, zoneID, shelfID, nil
	}

	for _, lp := range plan.lines {
		if lp.input.QtyGood.IsZero() {
			continue // 拒收行不产生上架任务
		}
		binID, zoneID, shelfID, err := binResolve(lp)
		if err != nil {
			return nil, err
		}
		if lp.flags.SerialManaged {
			for _, sn := range lp.input.Serials {
				k := taskKey{sku: lp.input.SKUID, batch: lp.batchID, bin: binID, serial: strings.TrimSpace(sn)}
				d := demands[k]
				if d == nil {
					// 序列号任务单件 qty=1（1.0000；序列号 SKU 按件上架，inventory-rules §8.2）。
					d = &taskDemand{qty: stock.Qty(10000), zone: zoneID, shelf: shelfID, requireInspect: lp.requireInspect}
					demands[k] = d
					order = append(order, k)
				}
			}
			continue
		}
		k := taskKey{sku: lp.input.SKUID, batch: lp.batchID, bin: binID}
		d := demands[k]
		if d == nil {
			d = &taskDemand{zone: zoneID, shelf: shelfID, requireInspect: lp.requireInspect}
			demands[k] = d
			order = append(order, k)
		}
		d.qty = d.qty.Add(lp.input.QtyGood)
	}

	tasks := make([]*PutawayTask, 0, len(order))
	for _, k := range order {
		d := demands[k]
		fromState := FromStatePendingInspect
		if !d.requireInspect {
			fromState = FromStateAvailable
		}
		tasks = append(tasks, &PutawayTask{
			InboundNo:         inbound.InboundNo,
			ReceiptNo:         receiptNo,
			SKUID:             k.sku,
			BatchID:           k.batch,
			SerialNo:          k.serial,
			Qty:               d.qty,
			FromState:         fromState,
			TargetWarehouseID: inbound.WarehouseID,
			TargetZoneID:      d.zone,
			TargetShelfID:     d.shelf,
			TargetBinID:       k.bin,
			Status:            TaskStatusPending,
			Remark:            "收货生成",
			CreatedBy:         database.ID(actor.UserID),
			UpdatedBy:         database.ID(actor.UserID),
		})
	}
	nos := make([]string, 0, len(tasks))
	for _, t := range tasks {
		no, err := docnum.Next(ctx, tx, docRule("PW"))
		if err != nil {
			return nil, err
		}
		t.PutawayNo = no
		nos = append(nos, no)
	}
	if err := s.repo.InsertPutawayTasks(ctx, tx, tasks); err != nil {
		return nil, err
	}
	return nos, nil
}

// ClaimPutawayTask 领取任务（PENDING→IN_PROGRESS 原子抢占，architecture §5.2）：
// 同任务并发领取恰一路成功（0 行 → ErrPutawayClaimConflict）。
func (s *Service) ClaimPutawayTask(ctx context.Context, actor Actor, id int64) (*PutawayTask, error) {
	t, err := s.repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与 GetTask 详情同口径）。
	if !actor.canAccessWarehouse(t.TargetWarehouseID) {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	if t.Status != TaskStatusPending {
		return nil, response.NewError(ErrPutawayClaimConflict, map[string]any{
			"status": t.Status, "claimed_by": t.ClaimedBy,
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.ClaimPutawayTask(ctx, tx, id, actor.UserID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrPutawayClaimConflict, map[string]any{"task_id": id})
		}
		e := actor.auditEntry("putaway_task", id, "claim")
		e.Before = map[string]any{"status": TaskStatusPending}
		e.After = map[string]any{"status": TaskStatusInProgress, "claimed_by": actor.UserID}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindTaskByID(ctx, id)
}

// ExecutePutawayTask 上架确认（IN_PROGRESS→COMPLETED；仅领取人可执行）：
// 库位确认（改指定经 BinChecker 校验）→ StockGateway.Putaway 落账（免检 available /
// 经检 pending_inspect；幂等键 plan §7 `putaway:{inbound_no}:{task_no}:{bin}:{sku}:{batch}`）
// → 序列号任务逐件 SerialEvent 定位库位 → 入库单/采购单累计与状态推进 → 同事务审计。
func (s *Service) ExecutePutawayTask(ctx context.Context, actor Actor, id int64, in PutawayExecuteInput) (*PutawayTask, error) {
	t, err := s.repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与 GetTask 详情同口径）。
	if !actor.canAccessWarehouse(t.TargetWarehouseID) {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	if t.Status != TaskStatusInProgress {
		return nil, response.NewError(ErrPutawayStatusNotAllowed, map[string]any{
			"status": t.Status, "reason": "仅上架中任务可确认",
		})
	}
	if t.ClaimedBy != actor.UserID && !actor.IsSuper {
		return nil, response.NewError(ErrPutawayNotClaimant, map[string]any{
			"claimed_by": t.ClaimedBy, "operator_id": actor.UserID,
		})
	}
	inbound, err := s.repo.FindInboundByNo(ctx, t.InboundNo)
	if err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, response.NewError(ErrInboundNotFound, map[string]any{"inbound_no": t.InboundNo})
	}
	switch inbound.Status {
	case InboundStatusReceiving, InboundStatusAwaitingQC, InboundStatusAwaitingPutaway:
	default:
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{
			"status": inbound.Status, "reason": "入库单当前状态不允许上架确认",
		})
	}
	// 库位确认（§5.2 扫码库位/手动指定：执行时改指定需校验存在性）。
	binID, zoneID, shelfID := t.TargetBinID, t.TargetZoneID, t.TargetShelfID
	if in.BinID > 0 && in.BinID != t.TargetBinID {
		binID, zoneID, shelfID, err = s.recommendBin(ctx, t.TargetWarehouseID, t.SKUID, t.Qty, in.BinID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.requireStock(); err != nil {
		return nil, err
	}
	requireInspect := t.FromState == FromStatePendingInspect
	key := fmt.Sprintf("putaway:%s:%s:%d:%d:%d", t.InboundNo, t.PutawayNo, binID, t.SKUID, t.BatchID)
	sa := actor.stockActor()
	source := stock.Source{Type: "putaway_task", No: t.PutawayNo}

	err = s.tx(ctx, func(tx *gorm.DB) error {
		// 上架落账（行不存在则创建；BinChecker 由库存 Service 经注入的仓库域校验器把关）。
		if _, err := s.opt.stock.Putaway(ctx, tx, stock.PutawayOp{
			Key: stock.RowKey{
				WarehouseID: t.TargetWarehouseID, ZoneID: zoneID, ShelfID: shelfID,
				BinID: binID, SKUID: t.SKUID, BatchID: t.BatchID,
			},
			Qty:            t.Qty,
			RequireInspect: requireInspect,
			Source:         source,
			Actor:          sa,
			IdempotencyKey: key,
			Remark:         "上架确认 " + t.PutawayNo,
		}); err != nil {
			return err
		}
		// 序列号任务逐件定位库位（与 Putaway 同事务，service.go:1131-1137 契约）。
		if t.SerialNo != "" {
			if _, _, err := s.opt.stock.SerialEvent(ctx, tx, stock.SerialOp{
				SerialNo: t.SerialNo, SKUID: t.SKUID, BatchID: t.BatchID,
				WarehouseID: t.TargetWarehouseID, BinID: binID,
				Status: SerialStatusInStock,
				Source: source, Actor: sa,
				Remark: "上架定位",
			}); err != nil {
				return err
			}
		}
		// 入库单明细已上架累计（守卫：累计 ≤ 已收货）。
		item, err := s.repo.FindInboundItemBySku(ctx, inbound.ID.Int64(), t.SKUID)
		if err != nil || item == nil {
			return response.NewError(ErrReceiptQtyMismatch, map[string]any{"sku_id": t.SKUID})
		}
		n, err := s.repo.AddInboundItemPutaway(ctx, tx, item.ID.Int64(), t.Qty, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrReceiptQtyMismatch, map[string]any{
				"sku_id": t.SKUID, "reason": "入库单已上架数量守卫未生效",
			})
		}
		// 采购订单明细已上架累计（RECEIVED_ALL→COMPLETED 判定数据面，plan §6.1）。
		if inbound.SourceType == SourceTypePurchase && inbound.SourceNo != "" {
			if po, err := s.repo.FindPOByNo(ctx, inbound.SourceNo); err != nil {
				return err
			} else if po != nil {
				if poItem, err := s.repo.FindPOItemBySku(ctx, po.ID.Int64(), t.SKUID); err != nil {
					return err
				} else if poItem != nil {
					n, err := s.repo.AddPOItemPutaway(ctx, tx, poItem.ID.Int64(), t.Qty, actor.UserID)
					if err := guardRows(n, err); err != nil {
						return response.NewError(ErrReceiptQtyMismatch, map[string]any{
							"po_no": po.PONo, "sku_id": t.SKUID, "reason": "采购订单已上架数量守卫未生效",
						})
					}
				}
			}
		}
		// 任务状态守卫迁移（IN_PROGRESS→COMPLETED）+ 审计。
		n, err = s.repo.UpdatePutawayTaskStatus(ctx, tx, id, TaskStatusInProgress, TaskStatusCompleted, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrPutawayStatusNotAllowed, map[string]any{"reason": "任务状态并发变化"})
		}
		if binID != t.TargetBinID || zoneID != t.TargetZoneID || shelfID != t.TargetShelfID {
			if err := s.repo.UpdatePutawayTaskCols(ctx, tx, id, map[string]any{
				"target_bin_id": binID, "target_zone_id": zoneID, "target_shelf_id": shelfID,
			}); err != nil {
				return err
			}
		}
		e := actor.auditEntry("putaway_task", id, "putaway")
		e.Request = in
		e.Before = map[string]any{"status": TaskStatusInProgress, "target_bin_id": t.TargetBinID}
		e.After = map[string]any{
			"status": TaskStatusCompleted, "target_bin_id": binID,
			"from_state": t.FromState, "qty": t.Qty.String(), "idempotency_key": key,
		}
		if err := middlewareAudit(tx, e); err != nil {
			return err
		}
		// 入库单收尾推进（全部任务完成 → AWAITING_PUTAWAY→COMPLETED）。
		return s.progressInboundAfterTask(ctx, tx, actor, inbound)
	})
	if err != nil {
		if errors.Is(err, errGuardMiss) {
			return nil, response.NewError(ErrStatusConflict, nil)
		}
		return nil, err
	}
	return s.repo.FindTaskByID(ctx, id)
}

// progressInboundAfterTask 全部上架任务完成后推进入库单（plan §6.2：
// AWAITING_PUTAWAY→COMPLETED 由"全部上架任务完成"触发；PAUSED 属未完成活动态，
// 一并阻断推进——迁移 000017）。
func (s *Service) progressInboundAfterTask(ctx context.Context, tx *gorm.DB, actor Actor, inbound *InboundOrder) error {
	counts, err := s.repo.CountTasksByInboundTx(ctx, tx, inbound.InboundNo)
	if err != nil {
		return err
	}
	if counts[TaskStatusPending] > 0 || counts[TaskStatusInProgress] > 0 || counts[TaskStatusPaused] > 0 {
		return nil
	}
	if inbound.Status != InboundStatusAwaitingPutaway {
		return nil
	}
	n, err := s.repo.UpdateInboundStatus(ctx, tx, inbound.ID.Int64(), InboundStatusAwaitingPutaway, InboundStatusCompleted, actor.UserID)
	if err := guardRows(n, err); err != nil {
		if errors.Is(err, errGuardMiss) {
			return nil // 并发已推进
		}
		return err
	}
	e := actor.auditEntry("inbound_order", inbound.ID.Int64(), "status")
	e.Before, e.After = map[string]any{"status": InboundStatusAwaitingPutaway}, map[string]any{"status": InboundStatusCompleted}
	return middlewareAudit(tx, e)
}

// PausePutawayTask 暂停任务（IN_PROGRESS→PAUSED；仅领取人/超管，迁移 000017）：
// 领取人归属不变（claimed_by 保留），恢复后仍由原领取人继续执行（ExecutePutawayTask
// 的领取人校验口径一致）；数据层 WHERE status='IN_PROGRESS' 为第二道守卫，0 行 =
// 并发状态变化冲突。
func (s *Service) PausePutawayTask(ctx context.Context, actor Actor, id int64) (*PutawayTask, error) {
	t, err := s.repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与 GetTask 详情同口径）。
	if !actor.canAccessWarehouse(t.TargetWarehouseID) {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	if t.Status != TaskStatusInProgress {
		return nil, response.NewError(ErrPutawayStatusNotAllowed, map[string]any{
			"status": t.Status, "reason": "仅上架中任务可暂停",
		})
	}
	if t.ClaimedBy != actor.UserID && !actor.IsSuper {
		return nil, response.NewError(ErrPutawayNotClaimant, map[string]any{
			"claimed_by": t.ClaimedBy, "operator_id": actor.UserID,
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdatePutawayTaskStatus(ctx, tx, id, TaskStatusInProgress, TaskStatusPaused, actor.UserID)
		if err := guardRows(n, err); err != nil {
			if errors.Is(err, errGuardMiss) {
				return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
			}
			return err
		}
		e := actor.auditEntry("putaway_task", id, "pause")
		e.Before = map[string]any{"status": TaskStatusInProgress}
		e.After = map[string]any{"status": TaskStatusPaused, "claimed_by": t.ClaimedBy}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindTaskByID(ctx, id)
}

// ResumePutawayTask 恢复任务（PAUSED→IN_PROGRESS；仅原领取人/超管，迁移 000017）。
func (s *Service) ResumePutawayTask(ctx context.Context, actor Actor, id int64) (*PutawayTask, error) {
	t, err := s.repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与 GetTask 详情同口径）。
	if !actor.canAccessWarehouse(t.TargetWarehouseID) {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	if t.Status != TaskStatusPaused {
		return nil, response.NewError(ErrPutawayStatusNotAllowed, map[string]any{
			"status": t.Status, "reason": "仅已暂停任务可恢复",
		})
	}
	if t.ClaimedBy != actor.UserID && !actor.IsSuper {
		return nil, response.NewError(ErrPutawayNotClaimant, map[string]any{
			"claimed_by": t.ClaimedBy, "operator_id": actor.UserID,
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdatePutawayTaskStatus(ctx, tx, id, TaskStatusPaused, TaskStatusInProgress, actor.UserID)
		if err := guardRows(n, err); err != nil {
			if errors.Is(err, errGuardMiss) {
				return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
			}
			return err
		}
		e := actor.auditEntry("putaway_task", id, "resume")
		e.Before = map[string]any{"status": TaskStatusPaused}
		e.After = map[string]any{"status": TaskStatusInProgress, "claimed_by": t.ClaimedBy}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindTaskByID(ctx, id)
}

// RecommendBins 推荐库位查询（§5.3 基础规则经 BinRecommender；服务未注入 fail-closed）。
func (s *Service) RecommendBins(ctx context.Context, warehouseID, skuID int64, qty float64) ([]BinSuggestion, error) {
	if s.opt.recommender == nil {
		return nil, response.NewError(ErrBinRequired, map[string]any{
			"warehouse_id": warehouseID, "sku_id": skuID,
			"reason": "推荐库位服务未装配",
		})
	}
	return s.opt.recommender.Recommend(ctx, warehouseID, skuID, qty)
}

// GetTask / ListTask 查询面。
func (s *Service) GetTask(ctx context.Context, id int64, scope WarehouseScope) (*PutawayTask, error) {
	t, err := s.repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：目标仓库不在范围内按不存在处理，
	// 与 sales/stockops/returns 详情接口同口径（plan §10.5）。
	if t == nil || !scope.visible(t.TargetWarehouseID) {
		return nil, response.NewError(ErrPutawayTaskNotFound, nil)
	}
	return t, nil
}

func (s *Service) ListTask(ctx context.Context, f TaskListFilter) ([]*PutawayTask, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)
	f.Page, f.PageSize = page, size
	return s.repo.ListTasks(ctx, f)
}
