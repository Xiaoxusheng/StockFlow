package stockops

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// qtyUnit 一个完整单位（stock.Qty 内部以 0.0001 计——经 ParseQty("1") 推导，
// 不重复定义标度常量；序列号管理 SKU 逐件作业要求整数量）。
var qtyUnit = func() stock.Qty {
	q, err := stock.ParseQty("1")
	if err != nil {
		panic(err)
	}
	return q
}()

// qtyIsInteger 数量是否为整数个单位（序列号 SKU 逐件作业前置校验）。
func qtyIsInteger(q stock.Qty) bool { return q%qtyUnit == 0 }

// qtyUnits 数量的整数件数（调用方保证 qtyIsInteger）。
func qtyUnits(q stock.Qty) int64 { return int64(q / qtyUnit) }

// itoa 行号/ID 拼接幂等键用（int64 → 十进制）。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// 调拨单服务（business-flow §10.1、backend-m2-plan §6.6/§7）。
//
// 状态机（全部经守卫 UPDATE 迁移，禁止跳状态；重复提交同迁移 = 幂等重放）：
//
//	DRAFT --submit--> PENDING_APPROVAL --approve--> APPROVED(待出库，源仓逐行 Lock 预占)
//	APPROVED --outbound--> TRANSFERRING（逐行 TransferOut + 序列号转在途，qty_out 落行）
//	TRANSFERRING --arrive--> AWAITING_RECEIPT（到货登记，无库存动作）
//	AWAITING_RECEIPT --receive--> COMPLETED（逐行 TransferIn + 序列号回位，qty_in 落行）
//	DRAFT/PENDING_APPROVAL/APPROVED --cancel--> CANCELLED（APPROVED 先释放全部预占锁）
//	TRANSFERRING/AWAITING_RECEIPT 取消被拒（business-flow §13.3：只能反向调拨冲正）。
//
// 事务边界（architecture §4，plan §10.1）：每个动作 = 单外层事务：状态守卫迁移 →
// 库存原语（同事务组合）→ 审批记录 → operation_logs → COMMIT；任一失败整体回滚。

// TransferLoc 调拨库位定位（四维：仓库/区/架/位）。
type TransferLoc struct {
	WarehouseID int64 `json:"warehouse_id"`
	ZoneID      int64 `json:"zone_id"`
	ShelfID     int64 `json:"shelf_id"`
	BinID       int64 `json:"bin_id"`
}

// TransferLineInput 调拨明细输入。
type TransferLineInput struct {
	SKUID   int64       `json:"sku_id"`
	BatchID int64       `json:"batch_id"` // 0=非批次 SKU
	From    TransferLoc `json:"from"`
	To      TransferLoc `json:"to"`
	Qty     stock.Qty   `json:"qty"`
}

// TransferInput 创建/修改调拨单输入。
type TransferInput struct {
	Type            string              `json:"type"`
	FromWarehouseID int64               `json:"from_warehouse_id"`
	ToWarehouseID   int64               `json:"to_warehouse_id"`
	Remark          string              `json:"remark"`
	Lines           []TransferLineInput `json:"lines"`
}

// TransferDetail 调拨单详情（明细含在途进度）。
type TransferDetail struct {
	Order TransferOrder  `json:"order"`
	Items []TransferItem `json:"items"`
}

// rowKey 调拨明细 → 库存原语五维定位键（needZoneShelf：目标行创建必须携带区/架）。
func transferKey(loc TransferLoc, skuID, batchID int64, needZoneShelf bool) stock.RowKey {
	return stock.RowKey{
		WarehouseID: loc.WarehouseID, ZoneID: loc.ZoneID, ShelfID: loc.ShelfID,
		BinID: loc.BinID, SKUID: skuID, BatchID: batchID,
	}
}

// validateTransferInput 创建/修改入参完整校验（api.md §4：后端完整校验，含业务关系）。
// 序列号管理 SKU 额外约束：数量为正整数、整单仅一行（逐件作业与到货回件定位，
// inventory-rules §8.2）。返回按输入序排列的明细行（行号已编）。
func (s *Service) validateTransferInput(ctx context.Context, in TransferInput) ([]TransferItem, error) {
	if !validTransferTypes[in.Type] {
		return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
			"field": "type", "value": in.Type, "allowed": []string{TransferTypeWarehouse, TransferTypeBin},
		})
	}
	if in.FromWarehouseID <= 0 || in.ToWarehouseID <= 0 {
		return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
			"field": "from_warehouse_id/to_warehouse_id", "reason": "必须为正整数",
		})
	}
	if in.Type == TransferTypeWarehouse && in.FromWarehouseID == in.ToWarehouseID {
		return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
			"reason": "跨仓调拨的源仓与目标仓必须不同（仓内移库属库位间调拨/移库域）",
		})
	}
	if in.Type == TransferTypeBin && in.FromWarehouseID != in.ToWarehouseID {
		return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
			"reason": "库位间调拨两端必须同仓",
		})
	}
	if len(in.Lines) == 0 {
		return nil, response.NewError(ErrTransferLineInvalid, map[string]any{"reason": "明细不能为空"})
	}
	seenSKU := map[int64]bool{}
	items := make([]TransferItem, 0, len(in.Lines))
	for i, ln := range in.Lines {
		lineNo := i + 1
		if ln.SKUID <= 0 {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{"line_no": lineNo, "field": "sku_id"})
		}
		if ln.BatchID < 0 {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{"line_no": lineNo, "field": "batch_id"})
		}
		if !ln.Qty.IsPositive() {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{"line_no": lineNo, "field": "qty", "reason": "必须为正数"})
		}
		if ln.From.WarehouseID != in.FromWarehouseID || ln.To.WarehouseID != in.ToWarehouseID {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
				"line_no": lineNo, "reason": "明细两端仓库必须与单据一致",
			})
		}
		if ln.From.BinID <= 0 || ln.To.BinID <= 0 {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
				"line_no": lineNo, "reason": "源/目标库位必填（库存按库位定位，inventory-rules §3）",
			})
		}
		if ln.To.ZoneID <= 0 || ln.To.ShelfID <= 0 {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
				"line_no": lineNo, "reason": "目标 zone_id/shelf_id 必填（目标行创建五维要求）",
			})
		}
		if in.Type == TransferTypeBin && ln.From.BinID == ln.To.BinID {
			return nil, response.NewError(ErrTransferLineInvalid, map[string]any{
				"line_no": lineNo, "reason": "库位间调拨源库位与目标库位必须不同",
			})
		}
		// 业务关系校验：SKU 启用、源/目标库位存在且归属对应仓库。
		if err := s.checkSKU(ctx, ln.SKUID); err != nil {
			return nil, err
		}
		if err := s.checkBin(ctx, ln.From.WarehouseID, ln.From.BinID, lineNo, "from_bin_id"); err != nil {
			return nil, err
		}
		if err := s.checkBin(ctx, ln.To.WarehouseID, ln.To.BinID, lineNo, "to_bin_id"); err != nil {
			return nil, err
		}
		// 序列号管理 SKU 分支（fail-closed：开关读取缺位即拒绝，plan §4.3 规则①）。
		flags, err := s.skuFlags(ctx, ln.SKUID)
		if err != nil {
			return nil, err
		}
		if flags.SerialManaged {
			if !qtyIsInteger(ln.Qty) {
				return nil, response.NewError(ErrSerialQtyInvalid, map[string]any{
					"line_no": lineNo, "sku_id": ln.SKUID, "qty": ln.Qty.String(),
				})
			}
			if seenSKU[ln.SKUID] {
				return nil, response.NewError(ErrSerialLineDup, map[string]any{"line_no": lineNo, "sku_id": ln.SKUID})
			}
			seenSKU[ln.SKUID] = true
		}
		items = append(items, TransferItem{
			LineNo: lineNo, SKUID: ln.SKUID, BatchID: ln.BatchID,
			FromWarehouseID: ln.From.WarehouseID, FromZoneID: ln.From.ZoneID,
			FromShelfID: ln.From.ShelfID, FromBinID: ln.From.BinID,
			ToWarehouseID: ln.To.WarehouseID, ToZoneID: ln.To.ZoneID,
			ToShelfID: ln.To.ShelfID, ToBinID: ln.To.BinID,
			Qty: ln.Qty,
		})
	}
	return items, nil
}

func (s *Service) checkSKU(ctx context.Context, skuID int64) error {
	if s.skus == nil {
		return response.NewError(ErrReaderMissing, map[string]any{"reason": "SKU 存在性校验未装配（WithSKUChecker）"})
	}
	ok, err := s.skus.ExistsActive(ctx, skuID)
	if err != nil {
		return response.NewError(response.CodeInternalError, map[string]any{"reason": "SKU 校验失败", "error": err.Error()})
	}
	if !ok {
		return response.NewError(response.CodeInvalidParam, map[string]any{"reason": "SKU 不存在或已停用", "sku_id": skuID})
	}
	return nil
}

func (s *Service) checkBin(ctx context.Context, warehouseID, binID int64, lineNo int, field string) error {
	if s.bins == nil {
		return response.NewError(ErrReaderMissing, map[string]any{"reason": "库位存在性校验未装配（WithBinChecker）"})
	}
	ok, err := s.bins.ExistsActive(ctx, warehouseID, binID)
	if err != nil {
		return response.NewError(response.CodeInternalError, map[string]any{"reason": "库位校验失败", "error": err.Error()})
	}
	if !ok {
		return response.NewError(response.CodeInvalidParam, map[string]any{
			"line_no": lineNo, "field": field, "warehouse_id": warehouseID, "bin_id": binID,
			"reason": "库位不存在、已停用或不属于该仓库",
		})
	}
	return nil
}

// skuFlags SKU 开关读取（fail-closed）。
func (s *Service) skuFlags(ctx context.Context, skuID int64) (SKUFlags, error) {
	if s.flags == nil {
		return SKUFlags{}, response.NewError(ErrReaderMissing, map[string]any{
			"reason": "SKU 开关读取未装配（WithSKUFlagReader，序列号分支判定的前置）",
		})
	}
	f, err := s.flags.GetFlags(ctx, skuID)
	if err != nil {
		return SKUFlags{}, response.NewError(response.CodeInternalError, map[string]any{"reason": "SKU 开关读取失败", "error": err.Error()})
	}
	return f, nil
}

// CreateTransfer 创建调拨单（DRAFT）。编号经 internal/docnum（TR 前缀，§13.1），
// 取号与创建同事务。
func (s *Service) CreateTransfer(ctx context.Context, actor stock.Actor, in TransferInput) (*TransferDetail, error) {
	items, err := s.validateTransferInput(ctx, in)
	if err != nil {
		return nil, err
	}
	detail := &TransferDetail{}
	_, err = s.issueInTx(ctx, transferNoRule, func(t Tx, no string) error {
		o := &TransferOrder{
			TransferNo:      no,
			Type:            in.Type,
			FromWarehouseID: in.FromWarehouseID,
			ToWarehouseID:   in.ToWarehouseID,
			Status:          TransferDraft,
			Remark:          in.Remark,
			CreatedBy:       actor.ID, UpdatedBy: actor.ID,
		}
		for i := range items {
			items[i].CreatedBy, items[i].UpdatedBy = actor.ID, actor.ID
		}
		if err := t.InsertTransfer(ctx, o, items); err != nil {
			return err
		}
		if err := t.Audit(transferAudit(actor, "create", o, nil, o)); err != nil {
			return err
		}
		detail.Order = *o
		detail.Items = items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// issueInTx 编号发放 + insert 回调同事务（内部经 NumberIssuer → docnum.NextWithRetry：
// 撞历史遗留单号唯一约束自动取下一号重试，business-flow §13.1）。
func (s *Service) issueInTx(ctx context.Context, rule docnum.Rule, insert func(t Tx, no string) error) (string, error) {
	var issued string
	err := s.store.WithinTx(ctx, func(t Tx) error {
		no, err := s.issuer.Issue(ctx, t.GormDB(), rule, func(_ *gorm.DB, no string) error {
			return insert(t, no)
		})
		if err != nil {
			return err
		}
		issued = no
		return nil
	})
	return issued, err
}

// UpdateTransfer 修改调拨单（仅 DRAFT；整单替换明细，行号重编；非 DRAFT 一律拒绝——
// 明细已被后续环节消费，business-flow §13.3 不可变语义）。
func (s *Service) UpdateTransfer(ctx context.Context, actor stock.Actor, id int64, in TransferInput) (*TransferDetail, error) {
	items, err := s.validateTransferInput(ctx, in)
	if err != nil {
		return nil, err
	}
	detail := &TransferDetail{}
	err = s.store.WithinTx(ctx, func(t Tx) error {
		o, err := t.GetTransfer(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrTransferNotFound, map[string]any{"id": id})
		}
		if o.Status != TransferDraft {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅草稿状态可修改调拨单", "current_status": o.Status,
			})
		}
		o.Type, o.FromWarehouseID, o.ToWarehouseID = in.Type, in.FromWarehouseID, in.ToWarehouseID
		o.Remark = in.Remark
		for i := range items {
			items[i].CreatedBy, items[i].UpdatedBy = actor.ID, actor.ID
		}
		if err := t.ReplaceTransferItems(ctx, id, items); err != nil {
			return err
		}
		if err := t.Audit(transferAudit(actor, "update", o, nil, o)); err != nil {
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

// SubmitTransfer 提交审核（DRAFT→PENDING_APPROVAL；守卫：目标库位存在，plan §6.6）。
func (s *Service) SubmitTransfer(ctx context.Context, actor stock.Actor, id int64) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o.Status == TransferPending {
			replay = true
			detail.Order, detail.Items = *o, items
			return nil
		}
		if o.Status != TransferDraft {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅草稿可提交", "current_status": o.Status,
			})
		}
		// 守卫：两端库位仍然有效（创建后库位可能被停用）。
		if err := s.checkTransferBins(ctx, items); err != nil {
			return err
		}
		n, err := t.UpdateTransferStatus(ctx, id, TransferDraft, TransferPending, TransferStamps{}, actor.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "单据状态已变化，请刷新重试"})
		}
		if err := t.InsertApproval(ctx, ApprovalRecord{
			TargetType: sourceTransfer, TargetNo: o.TransferNo, Action: "SUBMIT",
			OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
		}); err != nil {
			return err
		}
		o.Status = TransferPending
		if err := t.Audit(transferAudit(actor, "submit", o,
			map[string]any{"status": TransferDraft}, map[string]any{"status": TransferPending})); err != nil {
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

// ApproveTransferInput 审核输入（通过/驳回共用资源点，操作语义由请求区分，plan §9.1）。
type ApproveTransferInput struct {
	Action  string `json:"action"` // approve / reject
	Opinion string `json:"opinion"`
}

// ApproveTransfer 审核：通过 = PENDING_APPROVAL→APPROVED + 源仓逐行 Lock(ORDER_HOLD)
// 预占（任一行可用不足整体回滚，单据留待审核，plan §6.6）；驳回 = →CANCELLED
// （迁移 CHECK 无 REJECTED 值，驳回终态与 §12.2 审批记录并存）。
func (s *Service) ApproveTransfer(ctx context.Context, actor stock.Actor, id int64, in ApproveTransferInput) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		switch in.Action {
		case "reject":
			if o.Status == TransferCancelled {
				replay = true
				detail.Order, detail.Items = *o, items
				return nil
			}
			if o.Status != TransferPending {
				return response.NewError(ErrStatusConflict, map[string]any{
					"reason": "仅待审核状态可驳回", "current_status": o.Status,
				})
			}
			if _, err := t.UpdateTransferStatus(ctx, id, TransferPending, TransferCancelled,
				TransferStamps{Cancelled: true}, actor.ID); err != nil {
				return err
			}
			o.Status = TransferCancelled
			if err := t.InsertApproval(ctx, ApprovalRecord{
				TargetType: sourceTransfer, TargetNo: o.TransferNo, Action: "REJECT", Result: "REJECTED",
				Opinion: in.Opinion, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
			}); err != nil {
				return err
			}
			if err := t.Audit(transferAudit(actor, "reject", o,
				map[string]any{"status": TransferPending}, map[string]any{"status": TransferCancelled})); err != nil {
				return err
			}
			detail.Order, detail.Items = *o, items
			return nil
		case "approve":
			if o.Status == TransferApproved {
				replay = true
				detail.Order, detail.Items = *o, items
				return nil
			}
			if o.Status != TransferPending {
				return response.NewError(ErrStatusConflict, map[string]any{
					"reason": "仅待审核状态可审核", "current_status": o.Status,
				})
			}
			// 状态先迁移（守卫 UPDATE 串行化并发审核），任一锁定失败整体回滚。
			if _, err := t.UpdateTransferStatus(ctx, id, TransferPending, TransferApproved,
				TransferStamps{Approve: true, ApprovedBy: actor.ID}, actor.ID); err != nil {
				return err
			}
			for _, it := range items {
				key := transferKey(TransferLoc{
					WarehouseID: it.FromWarehouseID, ZoneID: it.FromZoneID,
					ShelfID: it.FromShelfID, BinID: it.FromBinID,
				}, it.SKUID, it.BatchID, false)
				if _, err := s.gateway.Lock(ctx, t.GormDB(), stock.LockOp{
					Key: key, Qty: it.Qty, LockType: "ORDER_HOLD",
					Source:         stock.Source{Type: sourceTransfer, No: o.TransferNo},
					Actor:          actor,
					IdempotencyKey: transferIdem("lock", o.TransferNo, it.LineNo, it.FromBinID, it.SKUID, it.BatchID),
					Remark:         "调拨审核预占",
				}); err != nil {
					return err
				}
			}
			o.Status = TransferApproved
			if err := t.InsertApproval(ctx, ApprovalRecord{
				TargetType: sourceTransfer, TargetNo: o.TransferNo, Action: "APPROVE", Result: "APPROVED",
				Opinion: in.Opinion, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
			}); err != nil {
				return err
			}
			if err := t.Audit(transferAudit(actor, "approve", o,
				map[string]any{"status": TransferPending}, map[string]any{"status": TransferApproved})); err != nil {
				return err
			}
			detail.Order, detail.Items = *o, items
			return nil
		default:
			return response.NewError(response.CodeInvalidParam, map[string]any{
				"field": "action", "value": in.Action, "allowed": []string{"approve", "reject"},
			})
		}
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// OutboundTransfer 调拨出库确认（APPROVED→TRANSFERRING）：逐行 TransferOut（源仓
// total/locked 同减 + TRANSFER_OUT 流水 + 核销预占锁）+ 序列号逐件转在途（wh=0）+
// qty_out 落行；任一失败整体回滚，单据留 APPROVED（plan §6.6）。在途 = qty_out - qty_in。
func (s *Service) OutboundTransfer(ctx context.Context, actor stock.Actor, id int64) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o.Status == TransferMoving {
			replay = true
			detail.Order, detail.Items = *o, items
			return nil
		}
		if o.Status != TransferApproved {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅待出库（已审核）状态可出库", "current_status": o.Status,
			})
		}
		if _, err := t.UpdateTransferStatus(ctx, id, TransferApproved, TransferMoving,
			TransferStamps{Outbound: true}, actor.ID); err != nil {
			return err
		}
		// 预占锁回查（transfer_items 无 lock_id 冻结列——锁由 inventory_locks 持有，
		// 按来源单据 + 行维度定位；只读 SELECT，见 repo_gorm.go 编排说明）。
		locks, err := t.FindSourceLocks(ctx, sourceTransfer, o.TransferNo, "ORDER_HOLD")
		if err != nil {
			return err
		}
		lockLookup := func(it TransferItem) *ActiveLockRef {
			for i := range locks {
				l := &locks[i]
				if l.SKUID == it.SKUID && l.BatchID == it.BatchID &&
					l.WarehouseID == it.FromWarehouseID && l.BinID == it.FromBinID {
					return l
				}
			}
			return nil
		}
		for _, it := range items {
			remaining := it.Qty.Sub(it.QtyOut)
			if !remaining.IsPositive() {
				continue
			}
			lock := lockLookup(it)
			if lock == nil {
				return response.NewError(ErrLockMissing, map[string]any{
					"line_no": it.LineNo, "sku_id": it.SKUID, "reason": "出库前必须存在该行预占锁",
				})
			}
			// 序列号管理 SKU：逐件转在途（wh=0，inventory-rules §8 生命周期"调拨"环节）。
			flags, err := s.skuFlags(ctx, it.SKUID)
			if err != nil {
				return err
			}
			if flags.SerialManaged {
				pieces := qtyUnits(remaining)
				serials, err := t.FindSerialsInBin(ctx, it.FromWarehouseID, it.FromBinID, it.SKUID, it.BatchID, pieces)
				if err != nil {
					return err
				}
				if int64(len(serials)) != pieces {
					return response.NewError(ErrSerialShortage, map[string]any{
						"line_no": it.LineNo, "sku_id": it.SKUID,
						"need_pieces": pieces, "found_pieces": len(serials),
					})
				}
				for _, sr := range serials {
					if _, _, err := s.gateway.SerialEvent(ctx, t.GormDB(), stock.SerialOp{
						SerialNo: sr.SerialNo, SKUID: it.SKUID, BatchID: it.BatchID,
						WarehouseID: 0, BinID: 0, Status: "OUTBOUND",
						Source: stock.Source{Type: sourceTransfer, No: o.TransferNo},
						Actor:  actor, Remark: "调拨出库转在途",
					}); err != nil {
						return err
					}
				}
			}
			if _, err := s.gateway.TransferOut(ctx, t.GormDB(), stock.TransferOutOp{
				Key: transferKey(TransferLoc{
					WarehouseID: it.FromWarehouseID, ZoneID: it.FromZoneID,
					ShelfID: it.FromShelfID, BinID: it.FromBinID,
				}, it.SKUID, it.BatchID, false),
				Qty: remaining, LockID: lock.ID,
				Source: stock.Source{Type: sourceTransfer, No: o.TransferNo},
				Actor:  actor,
				IdempotencyKey: "trout:" + o.TransferNo + ":" + itoa(int64(it.LineNo)) +
					":" + itoa(it.FromBinID) + ":" + itoa(it.SKUID) + ":" + itoa(it.BatchID),
				Remark: "调拨出库（转在途）",
			}); err != nil {
				return err
			}
			if err := t.BumpTransferItemOut(ctx, it.ID.Int64(), remaining); err != nil {
				return err
			}
		}
		o.Status = TransferMoving
		fresh, freshItems, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if err := t.Audit(transferAudit(actor, "outbound", o,
			map[string]any{"status": TransferApproved}, map[string]any{"status": TransferMoving})); err != nil {
			return err
		}
		detail.Order, detail.Items = *fresh, freshItems
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// ArriveTransfer 到货登记（TRANSFERRING→AWAITING_RECEIPT）：货已抵达目标仓、
// 待收货确认落账——纯单据流转，无库存动作（plan §6.6 两态区分口径）。
func (s *Service) ArriveTransfer(ctx context.Context, actor stock.Actor, id int64) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o.Status == TransferAwaiting {
			replay = true
			detail.Order, detail.Items = *o, items
			return nil
		}
		if o.Status != TransferMoving {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅调拨中状态可登记到货", "current_status": o.Status,
			})
		}
		for _, it := range items {
			if it.QtyOut.Sub(it.QtyIn).IsNegative() || it.Qty.Sub(it.QtyOut).IsPositive() {
				return response.NewError(ErrStatusConflict, map[string]any{
					"reason": "存在未完成出库的明细", "line_no": it.LineNo,
				})
			}
		}
		if _, err := t.UpdateTransferStatus(ctx, id, TransferMoving, TransferAwaiting,
			TransferStamps{}, actor.ID); err != nil {
			return err
		}
		o.Status = TransferAwaiting
		if err := t.Audit(transferAudit(actor, "arrive", o,
			map[string]any{"status": TransferMoving}, map[string]any{"status": TransferAwaiting})); err != nil {
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

// ReceiveTransfer 收货完成（AWAITING_RECEIPT→COMPLETED）：逐行 TransferIn（目标仓
// total/available 同增 + TRANSFER_IN 流水）+ 序列号逐件回位于目标库位 + qty_in 落行；
// 任一失败整体回滚（货仍计在途，plan §6.6）。
func (s *Service) ReceiveTransfer(ctx context.Context, actor stock.Actor, id int64) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o.Status == TransferCompleted {
			replay = true
			detail.Order, detail.Items = *o, items
			return nil
		}
		if o.Status != TransferAwaiting {
			return response.NewError(ErrStatusConflict, map[string]any{
				"reason": "仅待入库状态可收货完成", "current_status": o.Status,
			})
		}
		if _, err := t.UpdateTransferStatus(ctx, id, TransferAwaiting, TransferCompleted,
			TransferStamps{Received: true}, actor.ID); err != nil {
			return err
		}
		for _, it := range items {
			remaining := it.Qty.Sub(it.QtyIn)
			if !remaining.IsPositive() {
				continue
			}
			if _, err := s.gateway.TransferIn(ctx, t.GormDB(), stock.TransferInOp{
				Key: transferKey(TransferLoc{
					WarehouseID: it.ToWarehouseID, ZoneID: it.ToZoneID,
					ShelfID: it.ToShelfID, BinID: it.ToBinID,
				}, it.SKUID, it.BatchID, true),
				Qty:    remaining,
				Source: stock.Source{Type: sourceTransfer, No: o.TransferNo},
				Actor:  actor,
				IdempotencyKey: "trin:" + o.TransferNo + ":" + itoa(int64(it.LineNo)) +
					":" + itoa(it.ToBinID) + ":" + itoa(it.SKUID) + ":" + itoa(it.BatchID),
				Remark: "调拨到货入库",
			}); err != nil {
				return err
			}
			// 序列号管理 SKU：在途件逐件回位目标库位（单行约束保证回件归属无歧义）。
			flags, err := s.skuFlags(ctx, it.SKUID)
			if err != nil {
				return err
			}
			if flags.SerialManaged {
				pieces := qtyUnits(remaining)
				serials, err := t.FindSerialsInTransit(ctx, o.TransferNo, it.SKUID)
				if err != nil {
					return err
				}
				if int64(len(serials)) != pieces {
					return response.NewError(ErrSerialShortage, map[string]any{
						"line_no": it.LineNo, "sku_id": it.SKUID,
						"need_pieces": pieces, "found_pieces": len(serials),
					})
				}
				for _, sr := range serials {
					if _, _, err := s.gateway.SerialEvent(ctx, t.GormDB(), stock.SerialOp{
						SerialNo: sr.SerialNo, SKUID: it.SKUID, BatchID: it.BatchID,
						WarehouseID: it.ToWarehouseID, BinID: it.ToBinID, Status: "IN_STOCK",
						Source: stock.Source{Type: sourceTransfer, No: o.TransferNo},
						Actor:  actor, Remark: "调拨到货回位",
					}); err != nil {
						return err
					}
				}
			}
			if err := t.BumpTransferItemIn(ctx, it.ID.Int64(), remaining); err != nil {
				return err
			}
		}
		o.Status = TransferCompleted
		fresh, freshItems, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if err := t.Audit(transferAudit(actor, "receive", o,
			map[string]any{"status": TransferAwaiting}, map[string]any{"status": TransferCompleted})); err != nil {
			return err
		}
		detail.Order, detail.Items = *fresh, freshItems
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return detail, replay, nil
}

// CancelTransfer 取消（DRAFT/PENDING_APPROVAL/APPROVED→CANCELLED）：APPROVED 先
// 释放全部预占锁（同事务，inventory-rules §4.2）；已出库（TRANSFERRING/AWAITING_RECEIPT）
// 拒绝——只能反向调拨冲正（business-flow §13.3）。
func (s *Service) CancelTransfer(ctx context.Context, actor stock.Actor, id int64, reason string) (*TransferDetail, bool, error) {
	replay := false
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o.Status == TransferCancelled {
			replay = true
			detail.Order, detail.Items = *o, items
			return nil
		}
		fromStatus := o.Status
		switch o.Status {
		case TransferDraft, TransferPending, TransferApproved:
		case TransferMoving, TransferAwaiting:
			return response.NewError(ErrTransferCancelForbidden, map[string]any{
				"current_status": o.Status,
				"reason":         "货已离源仓，禁止直接取消（创建反向调拨单冲正）",
			})
		default:
			return response.NewError(ErrStatusConflict, map[string]any{"current_status": o.Status})
		}
		if _, err := t.UpdateTransferStatus(ctx, id, o.Status, TransferCancelled,
			TransferStamps{Cancelled: true}, actor.ID); err != nil {
			return err
		}
		if o.Status == TransferApproved {
			locks, err := t.FindSourceLocks(ctx, sourceTransfer, o.TransferNo, "ORDER_HOLD")
			if err != nil {
				return err
			}
			for _, l := range locks {
				if _, err := s.gateway.ReleaseLock(ctx, t.GormDB(), stock.ReleaseLockOp{
					LockID: l.ID, Qty: l.Qty,
					Source:         stock.Source{Type: sourceTransfer, No: o.TransferNo},
					Actor:          actor,
					IdempotencyKey: "release:" + o.TransferNo + ":" + itoa(l.ID),
					Remark:         "调拨取消释放预占",
				}); err != nil {
					return err
				}
			}
		}
		o.Status = TransferCancelled
		if err := t.InsertApproval(ctx, ApprovalRecord{
			TargetType: sourceTransfer, TargetNo: o.TransferNo, Action: "CANCEL", Result: "CANCELLED",
			Opinion: reason, OperatorID: actor.ID, OperatorName: actor.Name, RequestID: actor.RequestID,
		}); err != nil {
			return err
		}
		if err := t.Audit(transferAudit(actor, "cancel", o,
			map[string]any{"status": fromStatus}, map[string]any{"status": TransferCancelled})); err != nil {
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

// ---- 查询 ----

// GetTransferDetail 详情（数据权限 fail-closed：两端仓库均不在范围内按不存在处理）。
func (s *Service) GetTransferDetail(ctx context.Context, id int64, scope Scope) (*TransferDetail, error) {
	detail := &TransferDetail{}
	err := s.store.WithinTx(ctx, func(t Tx) error {
		o, items, err := loadTransfer(ctx, t, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrTransferNotFound, map[string]any{"id": id})
		}
		if !scopeVisibleTransfer(scope, *o) {
			return response.NewError(ErrTransferNotFound, map[string]any{"id": id})
		}
		detail.Order, detail.Items = *o, items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// ListTransfers 分页列表。
func (s *Service) ListTransfers(ctx context.Context, f TransferFilter, page, pageSize int) ([]TransferOrder, int64, error) {
	var rows []TransferOrder
	var total int64
	err := s.store.WithinTx(ctx, func(t Tx) error {
		var err error
		rows, total, err = t.ListTransfers(ctx, f, page, pageSize)
		return err
	})
	return rows, total, err
}

// ListInTransit 在途聚合（inventory-rules §2 在途口径）。
func (s *Service) ListInTransit(ctx context.Context, f InTransitFilter, page, pageSize int) ([]InTransitRow, int64, error) {
	var rows []InTransitRow
	var total int64
	err := s.store.WithinTx(ctx, func(t Tx) error {
		var err error
		rows, total, err = t.ListInTransit(ctx, f, page, pageSize)
		return err
	})
	return rows, total, err
}

// ---- 内部助手 ----

func loadTransfer(ctx context.Context, t Tx, id int64) (*TransferOrder, []TransferItem, error) {
	o, err := t.GetTransfer(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if o == nil {
		return nil, nil, response.NewError(ErrTransferNotFound, map[string]any{"id": id})
	}
	items, err := t.ListTransferItems(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return o, items, nil
}

func (s *Service) checkTransferBins(ctx context.Context, items []TransferItem) error {
	seen := map[[2]int64]bool{}
	for _, it := range items {
		for _, pair := range [][2]int64{{it.FromWarehouseID, it.FromBinID}, {it.ToWarehouseID, it.ToBinID}} {
			if seen[pair] {
				continue
			}
			seen[pair] = true
			if err := s.checkBin(ctx, pair[0], pair[1], 0, "bin_id"); err != nil {
				return err
			}
		}
	}
	return nil
}

func scopeVisibleTransfer(scope Scope, o TransferOrder) bool {
	if scope.AllWarehouses {
		return true
	}
	if len(scope.WarehouseIDs) == 0 {
		return false
	}
	for _, id := range scope.WarehouseIDs {
		if id == o.FromWarehouseID || id == o.ToWarehouseID {
			return true
		}
	}
	return false
}

func transferAudit(actor stock.Actor, action string, o *TransferOrder, before, after any) middleware.AuditEntry {
	return middleware.AuditEntry{
		Module: "stockops", ObjectType: "transfer_order", ObjectID: o.ID.Int64(),
		Action: action, OperatorID: actor.ID, OperatorName: actor.Name,
		RequestID: actor.RequestID, IP: actor.IP, UserAgent: actor.UserAgent,
		Method: actor.Method, Path: actor.Path,
		Success: true, Request: map[string]any{"transfer_no": o.TransferNo},
		Before: before, After: after,
	}
}

func transferIdem(prefix, no string, lineNo int, binID, skuID, batchID int64) string {
	return prefix + ":" + no + ":" + itoa(int64(lineNo)) + ":" + itoa(binID) + ":" + itoa(skuID) + ":" + itoa(batchID)
}
