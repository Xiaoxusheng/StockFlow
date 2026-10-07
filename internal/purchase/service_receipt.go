package purchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 收货确认（business-flow §3.2/§3.3/§3.4、plan §6.1 收货行 / §7 幂等键构成）。
//
// 单事务内容：RC 单号发放 → 收货单与明细落库（幂等键部分唯一索引兜底）→ 异常收货
// 同事务登记异常中心 → 批次采集 EnsureBatch / 序列号采集 SerialEvent → 入库单/采购订单
// 四量累计（条件 UPDATE 守卫，累计收货 ≤ 原始数量）→ 入库单与采购订单状态推进
// （守卫迁移）→ 逐行生成上架任务（免检直达 available / 经检进入待检 pending_inspect）
// → 同事务审计。任何步骤失败整体回滚（architecture.md §4）。

// resolveIdempotencyKey 幂等键归一（HTTP 头优先于请求体；≤128 字符——plan §7）。
func resolveIdempotencyKey(header, body string) (string, error) {
	key := strings.TrimSpace(header)
	if key == "" {
		key = strings.TrimSpace(body)
	}
	if len(key) > 128 {
		return "", invalidParam("idempotency_key", "长度不能超过 128")
	}
	return key, nil
}

// receiptDemand 收货需求按 SKU 聚合（同 SKU 多行合并；含合格+拒收——§2.3
// "累计收货数量（含合格+不合格待定）不得超过原始数量"，拒收单独记录）。
type receiptDemand struct {
	skuID       int64
	qtyGood     stock.Qty
	qtyRejected stock.Qty
	lineIdxs    []int
}

// ConfirmReceipt 收货确认（事件型，幂等；重放返回既有结果不重复累计）。
func (s *Service) ConfirmReceipt(ctx context.Context, actor Actor, in ReceiptInput, idemHeader string) (*ReceiptResult, error) {
	key, err := resolveIdempotencyKey(idemHeader, in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if len(in.Lines) == 0 {
		return nil, invalidParam("lines", "收货必须至少一行明细")
	}
	// 归一请求体键为已解析键（头优先——plan §7），后续落库与重放统一使用。
	in.IdempotencyKey = key
	// 快路径：幂等键重放（事务外预检，plan §8.5 同款两段式；最终准绳是部分唯一索引）。
	if key != "" {
		if rc, err := s.repo.FindReceiptByIdempotencyKey(ctx, key); err != nil {
			return nil, err
		} else if rc != nil {
			return s.replayReceipt(ctx, rc)
		}
	}

	inbound, err := s.repo.FindInboundByNo(ctx, strings.TrimSpace(in.InboundNo))
	if err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, response.NewError(ErrInboundNotFound, map[string]any{"inbound_no": in.InboundNo})
	}
	// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与 GetInbound 详情同口径）。
	if !actor.canAccessWarehouse(inbound.WarehouseID) {
		return nil, response.NewError(ErrInboundNotFound, map[string]any{"inbound_no": in.InboundNo})
	}
	if inbound.Status != InboundStatusDraft && inbound.Status != InboundStatusReceiving {
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{
			"status": inbound.Status, "reason": "入库单当前状态不允许收货",
		})
	}
	inboundItems, err := s.repo.ListInboundItems(ctx, inbound.ID.Int64())
	if err != nil {
		return nil, err
	}

	// —— 校验阶段（事务外只读，精确 4xx + details 行号，api.md §4）——
	plan, err := s.validateReceiptLines(ctx, actor, inbound, inboundItems, in.Lines)
	if err != nil {
		return nil, err
	}

	var po *PurchaseOrder
	if inbound.SourceType == SourceTypePurchase {
		po, err = s.repo.FindPOByNo(ctx, inbound.SourceNo)
		if err != nil {
			return nil, err
		}
		if po == nil {
			return nil, response.NewError(ErrInboundSourceInvalid, map[string]any{"source_no": inbound.SourceNo})
		}
	}
	poItemPlan, err := s.validatePOQuantities(ctx, po, plan)
	if err != nil {
		return nil, err
	}

	// —— 事务阶段 ——
	result := &ReceiptResult{}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		return s.confirmReceiptTx(ctx, tx, actor, inbound, in, plan, po, poItemPlan, result)
	})
	if err != nil {
		if key != "" && errors.Is(err, errReplayConflict) {
			// 并发同键：本事务已回滚，返回既有结果（plan §8.5）。
			rc, ferr := s.repo.FindReceiptByIdempotencyKey(ctx, key)
			if ferr == nil && rc != nil {
				return s.replayReceipt(ctx, rc)
			}
		}
		return nil, err
	}
	return result, nil
}

// receiptPlan 校验后的收货执行计划（校验阶段产出、事务阶段消费，保证两阶段口径一致）。
type receiptPlan struct {
	lines []*receiptLinePlan // 按入参行序
	bySKU map[int64]*receiptDemand
}

type receiptLinePlan struct {
	input           ReceiptLineInput
	flags           SKUFlags
	inboundItemID   int64
	batchID         int64 // 批次采集后的批次 ID（非批次 SKU=0）
	requireInspect  bool
	binID           int64
	zoneID, shelfID int64
	exceptionNo     string
}

// validateReceiptLines 行级校验：SKU 三开关分支、数量正性、序列号件数、批次/效期必填、
// 入库单余量（§3.3 部分收货余量跟踪）、目标库位解析（手动指定校验 / 推荐服务，§5.2/§5.3）。
func (s *Service) validateReceiptLines(ctx context.Context, actor Actor, inbound *InboundOrder, inboundItems []*InboundItem, lines []ReceiptLineInput) (*receiptPlan, error) {
	itemBySKU := make(map[int64]*InboundItem, len(inboundItems))
	for _, it := range inboundItems {
		itemBySKU[it.SKUID] = it
	}
	demandBySKU := map[int64]*receiptDemand{}
	plan := &receiptPlan{lines: make([]*receiptLinePlan, 0, len(lines)), bySKU: demandBySKU}
	serialSeen := map[string]bool{} // 序列号去重（请求内）

	for i, li := range lines {
		field := fmt.Sprintf("lines[%d]", i)
		if li.SKUID <= 0 {
			return nil, invalidParam(field+".sku_id", "必须为正整数")
		}
		total := li.QtyGood.Add(li.QtyRejected)
		if !total.IsPositive() {
			return nil, response.NewError(ErrReceiptQtyInvalid, map[string]any{"line": i + 1})
		}
		item, known := itemBySKU[li.SKUID]
		if !known {
			return nil, response.NewError(ErrReceiptInboundLineUnknown, map[string]any{"line": i + 1, "sku_id": li.SKUID})
		}
		flags, err := s.skuFlagsOf(ctx, li.SKUID)
		if err != nil {
			return nil, err
		}
		if !flags.Enabled {
			return nil, invalidParam(field+".sku_id", fmt.Sprintf("SKU %d 已停用", li.SKUID))
		}
		lp := &receiptLinePlan{input: li, flags: flags, inboundItemID: item.ID.Int64()}
		// 免检判定（§5.2：收货环节声明；nil=默认进入待检 pending_inspect——
		// InspectResult 的前置状态，免检行在收货时即计入质检处理量）。
		lp.requireInspect = li.RequireInspect == nil || *li.RequireInspect

		// 批次/效期采集校验（inventory-rules §6：批次管理 SKU 必须采集批次号；
		// 效期管理 SKU 必须采集效期——效期随批次台账承载）。
		if flags.BatchManaged {
			if strings.TrimSpace(li.BatchNo) == "" {
				return nil, response.NewError(ErrBatchRequired, map[string]any{"line": i + 1, "sku_id": li.SKUID})
			}
			if flags.ExpiryManaged && li.ExpiryDate.IsZero() {
				return nil, response.NewError(ErrExpiryRequired, map[string]any{"line": i + 1, "sku_id": li.SKUID})
			}
		}
		// 序列号采集（inventory-rules §8：序列号 SKU 按件收货——合格件逐件采集，
		// 件数与合格数量一致、请求内不重复；拒收件未入库不采集）。
		if flags.SerialManaged {
			if len(li.Serials) == 0 {
				return nil, response.NewError(ErrSerialRequired, map[string]any{"line": i + 1, "sku_id": li.SKUID})
			}
			if !isIntegerQty(li.QtyGood) {
				return nil, response.NewError(ErrSerialQtyMismatch, map[string]any{
					"line": i + 1, "reason": "序列号 SKU 合格数量必须为整数（按件收货）",
				})
			}
			units := int(int64(li.QtyGood) / 10000)
			if len(li.Serials) != units {
				return nil, response.NewError(ErrSerialQtyMismatch, map[string]any{
					"line": i + 1, "serials": len(li.Serials), "qty_good": li.QtyGood.String(),
				})
			}
			for _, sn := range li.Serials {
				sn = strings.TrimSpace(sn)
				if sn == "" {
					return nil, response.NewError(ErrSerialRequired, map[string]any{"line": i + 1})
				}
				if serialSeen[sn] {
					return nil, response.NewError(ErrSerialRequired, map[string]any{
						"line": i + 1, "serial_no": sn, "reason": "序列号请求内重复",
					})
				}
				serialSeen[sn] = true
			}
		}

		// 入库单余量（§3.3：部分收货累计跟踪，禁止超量）。
		d := demandBySKU[li.SKUID]
		if d == nil {
			d = &receiptDemand{skuID: li.SKUID}
			demandBySKU[li.SKUID] = d
		}
		d.qtyGood = d.qtyGood.Add(li.QtyGood)
		d.qtyRejected = d.qtyRejected.Add(li.QtyRejected)
		d.lineIdxs = append(d.lineIdxs, i+1)
		plan.lines = append(plan.lines, lp)
	}

	// 入库单余量总校验（聚合后判定，行号进 details）。
	for _, d := range demandBySKU {
		item := itemBySKU[d.skuID]
		remaining := item.Qty.Sub(item.QtyReceived)
		if remaining.Sub(d.qtyGood).Sub(d.qtyRejected).IsNegative() {
			return nil, response.NewError(ErrOverReceipt, map[string]any{
				"source": "inbound", "inbound_no": inbound.InboundNo, "sku_id": d.skuID,
				"inbound_line_no": item.LineNo, "qty": item.Qty.String(),
				"qty_received": item.QtyReceived.String(),
				"this_time":    d.qtyGood.Add(d.qtyRejected).String(),
				"lines":        d.lineIdxs,
			})
		}
	}

	// 目标库位解析与批次预检放事务内（需推荐服务/BinChecker 与 EnsureBatch），
	// 此处仅做参数形状校验。
	return plan, nil
}

// validatePOQuantities 采购订单四量校验（business-flow §2.3 / plan §6.1 收货强校验：
// 累计收货（合格+拒收）本次之后不得超过原始数量；拒收单独累计记录）。
func (s *Service) validatePOQuantities(ctx context.Context, po *PurchaseOrder, plan *receiptPlan) (map[int64]*PurchaseOrderItem, error) {
	if po == nil {
		return nil, nil
	}
	poItems, err := s.repo.ListPOItems(ctx, po.ID.Int64())
	if err != nil {
		return nil, err
	}
	bySKU := make(map[int64]*PurchaseOrderItem, len(poItems))
	for _, it := range poItems {
		bySKU[it.SKUID] = it
	}
	for skuID, d := range plan.bySKU {
		poItem, ok := bySKU[skuID]
		if !ok {
			return nil, response.NewError(ErrReceiptInboundLineUnknown, map[string]any{
				"sku_id": skuID, "reason": "SKU 不在采购订单明细中",
			})
		}
		remaining := poItem.QtyOrdered.Sub(poItem.QtyReceived)
		if remaining.Sub(d.qtyGood).Sub(d.qtyRejected).IsNegative() {
			return nil, response.NewError(ErrOverReceipt, map[string]any{
				"source": "purchase_order", "po_no": po.PONo, "sku_id": skuID,
				"po_line_no": poItem.LineNo, "qty_ordered": poItem.QtyOrdered.String(),
				"qty_received": poItem.QtyReceived.String(),
				"this_time":    d.qtyGood.Add(d.qtyRejected).String(),
				"lines":        d.lineIdxs,
			})
		}
	}
	return bySKU, nil
}

// confirmReceiptTx 收货确认事务体（顺序即依赖序；任何失败整体回滚）。
func (s *Service) confirmReceiptTx(ctx context.Context, tx *gorm.DB, actor Actor,
	inbound *InboundOrder, in ReceiptInput, plan *receiptPlan, po *PurchaseOrder,
	poItems map[int64]*PurchaseOrderItem, result *ReceiptResult) error {

	// 0) 关联采购订单行锁重读与状态校验（修复轮：取消/收货并发互斥）。
	// 校验阶段（事务外）读到的 po.Status 到事务执行之间可能已被 CancelPO 迁移——
	// 事务内 FOR UPDATE 重读锁定 po 行后校验，保证：a) 取消先提交 → 此处读到
	// CANCELLED 拒绝收货；b) 收货先持锁 → CancelPO 的守卫 UPDATE（WHERE status=from）
	// 在锁释放后必然 n=0 回滚，两方向都不产生"已取消单据继续收货/上架"（business-flow
	// §13.2 状态守卫；plan §2.3 判据 4）。
	if po != nil {
		fresh, err := s.repo.FindPOByIDForUpdate(ctx, tx, po.ID.Int64())
		if err != nil {
			return err
		}
		if fresh == nil {
			return response.NewError(ErrInboundSourceInvalid, map[string]any{"source_no": po.PONo})
		}
		switch fresh.Status {
		case POStatusApproved, POStatusPartialReceived:
		default:
			return response.NewError(ErrPOStatusNotAllowed, map[string]any{
				"po_no": fresh.PONo, "status": fresh.Status,
				"reason": "仅已审核（APPROVED）或部分收货（PARTIAL_RECEIVED）采购订单可收货",
			})
		}
		*po = *fresh // 后续四量累计/状态推进以锁内最新值为准（含明细行号稳定性校验）
	}

	// 1) 异常收货登记（§3.4 → 异常中心 §11.2，同事务联动 plan §10；服务缺位 fail-closed）。
	for i, lp := range plan.lines {
		if strings.TrimSpace(lp.input.ExceptionType) == "" {
			continue
		}
		if s.opt.exceptions == nil {
			return response.NewError(ErrExceptionServiceMissing, nil)
		}
		detail, _ := json.Marshal(map[string]any{
			"sub_type":     strings.TrimSpace(lp.input.ExceptionType),
			"sku_id":       lp.input.SKUID,
			"qty_good":     lp.input.QtyGood.String(),
			"qty_rejected": lp.input.QtyRejected.String(),
			"description":  strings.TrimSpace(lp.input.ExceptionNote),
			"line":         i + 1,
		})
		excNo, err := s.opt.exceptions.Create(ctx, tx, exceptionTypeReceiving, "inbound_order", inbound.InboundNo, string(detail))
		if err != nil {
			return err
		}
		lp.exceptionNo = excNo
	}

	// 2) 收货单落库（RC 单号 + 幂等键；唯一索引兜底并发同键 → 哨兵上抛转重放）。
	rc := &Receipt{
		InboundNo:    inbound.InboundNo,
		WarehouseID:  inbound.WarehouseID,
		BatchNo:      firstBatchNo(plan),
		OperatorID:   actor.UserID,
		OperatorName: actor.Username,
		Remark:       strings.TrimSpace(in.Remark),
		CreatedBy:    database.ID(actor.UserID),
		UpdatedBy:    database.ID(actor.UserID),
	}
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		k := key
		rc.IdempotencyKey = &k
	}
	items := make([]*ReceiptItem, 0, len(plan.lines))
	for i, lp := range plan.lines {
		items = append(items, &ReceiptItem{
			LineNo:       i + 1,
			SKUID:        lp.input.SKUID,
			QtyGood:      lp.input.QtyGood,
			QtyRejected:  lp.input.QtyRejected,
			ExceptionRef: lp.exceptionNo,
			Remark:       strings.TrimSpace(lp.input.Remark),
		})
	}
	rcNo, err := docnum.NextWithRetry(ctx, tx, docRule("RC"), func(tx *gorm.DB, no string) error {
		rc.ReceiptNo = no
		rerr := s.repo.InsertReceipt(ctx, tx, rc, items)
		if rerr != nil && rc.IdempotencyKey != nil && isUniqueViolationErr(rerr) {
			return errReplayConflict // uk_receipts_idempotency 兜底（architecture §3.2）
		}
		return rerr
	})
	if err != nil {
		return err
	}
	rc.ReceiptNo = rcNo
	result.ReceiptNo = rcNo
	result.InboundNo = inbound.InboundNo

	// 3) 批次采集与序列号采集（批次台账/序列号台账写入口仅限库存原语，plan §4.2 判据 3）。
	if err := s.captureBatchAndSerials(ctx, tx, actor, inbound, po, plan, rcNo); err != nil {
		return err
	}

	// 4) 四量累计（数据层条件 UPDATE 守卫：累计 ≤ 上限；守卫未生效=并发竞争）。
	for _, d := range plan.bySKU {
		item, err := s.repo.FindInboundItemBySku(ctx, inbound.ID.Int64(), d.skuID)
		if err != nil || item == nil {
			return response.NewError(ErrReceiptQtyMismatch, map[string]any{"sku_id": d.skuID})
		}
		n, err := s.repo.AddInboundItemReceived(ctx, tx, item.ID.Int64(), d.qtyGood.Add(d.qtyRejected), actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrOverReceipt, map[string]any{
				"sku_id": d.skuID, "reason": "入库单余量守卫未生效（并发收货竞争）",
			})
		}
		if po != nil {
			poItem := poItems[d.skuID]
			n, err := s.repo.AddPOItemReceived(ctx, tx, poItem.ID.Int64(), d.qtyGood, d.qtyRejected, actor.UserID)
			if err := guardRows(n, err); err != nil {
				return response.NewError(ErrOverReceipt, map[string]any{
					"po_no": po.PONo, "po_line_no": poItem.LineNo, "sku_id": d.skuID,
					"reason": "采购订单累计收货守卫未生效（并发收货竞争）",
				})
			}
		}
	}
	// 免检直通行（require_inspect=false）收货即计入质检处理量——AWAITING_QC→
	// AWAITING_PUTAWAY 的"全部明细质检完成或免检（免检直通）"判定数据面（plan §6.2）。
	exempt := map[int64]stock.Qty{}
	for _, lp := range plan.lines {
		if !lp.requireInspect && lp.input.QtyGood.IsPositive() {
			exempt[lp.input.SKUID] = exempt[lp.input.SKUID].Add(lp.input.QtyGood)
		}
	}
	for skuID, q := range exempt {
		if q.IsZero() {
			continue
		}
		item, err := s.repo.FindInboundItemBySku(ctx, inbound.ID.Int64(), skuID)
		if err != nil || item == nil {
			return response.NewError(ErrReceiptQtyMismatch, map[string]any{"sku_id": skuID})
		}
		n, err := s.repo.AddInboundItemInspected(ctx, tx, item.ID.Int64(), q, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrReceiptQtyMismatch, map[string]any{
				"sku_id": skuID, "reason": "免检直通处理量守卫未生效",
			})
		}
	}

	// 5) 上架任务生成（免检 available / 默认 pending_inspect；§5.2 手动指定或 §5.3 推荐）
	// ——必须在状态推进之前：推进判定读取"任务是否存在进行中"的完成态。
	taskNos, err := s.generatePutawayTasks(ctx, tx, actor, inbound, plan, rcNo)
	if err != nil {
		return err
	}
	result.PutawayTasks = taskNos

	// 6) 入库单与采购订单状态推进（守卫迁移 + §13.2 审计）。
	if err := s.progressAfterReceipt(ctx, tx, actor, inbound); err != nil {
		return err
	}
	if po != nil {
		if err := s.progressPOAfterReceipt(ctx, tx, actor, po); err != nil {
			return err
		}
		result.PONo = po.PONo
	}
	// 回读最终状态（收货确认结果展示；plan §6.1/§6.2 迁移留审计）。
	// 必须经 tx 读：回读发生在推进之后、提交之前，r.db 连接 READ COMMITTED 下
	// 只见旧值（DRAFT/APPROVED），响应状态字段会失真（2026-10-07 链路实测修复）。
	if fresh, err := s.repo.FindInboundByNoTx(ctx, tx, inbound.InboundNo); err == nil && fresh != nil {
		result.InboundStatus = fresh.Status
	}
	if po != nil {
		if fresh, err := s.repo.FindPOByNoTx(ctx, tx, po.PONo); err == nil && fresh != nil {
			result.POStatus = fresh.Status
		}
	}

	// 7) 审计（收货事件本身；状态迁移审计已在推进方法内写）。
	e := actor.auditEntry("receipt", rc.ID.Int64(), "receive")
	e.Request = in
	e.After = map[string]any{
		"receipt_no": rc.ReceiptNo, "inbound_no": inbound.InboundNo,
		"lines": len(plan.lines),
	}
	return middlewareAudit(tx, e)
}

// captureBatchAndSerials 批次/序列号采集（必须在四量累计之后、任务生成之前——
// 任务需要 batchID；序列号收货时在库未上架，库位随上架事件更新，inventory-rules §8）。
func (s *Service) captureBatchAndSerials(ctx context.Context, tx *gorm.DB, actor Actor,
	inbound *InboundOrder, po *PurchaseOrder, plan *receiptPlan, receiptNo string) error {
	if err := s.requireStock(); err != nil {
		return err
	}
	sa := actor.stockActor()
	batchDone := map[string]int64{}
	for _, lp := range plan.lines {
		if lp.flags.BatchManaged {
			dedupe := fmt.Sprintf("%d:%s", lp.input.SKUID, strings.TrimSpace(lp.input.BatchNo))
			if id, done := batchDone[dedupe]; done {
				lp.batchID = id
				continue
			}
			var supplierID int64
			if po != nil {
				supplierID = po.SupplierID
			}
			batchID, _, err := s.opt.stock.EnsureBatch(ctx, tx, stock.BatchOp{
				SKUID:          lp.input.SKUID,
				BatchNo:        strings.TrimSpace(lp.input.BatchNo),
				SupplierID:     supplierID,
				ProductionDate: lp.input.ProductionDate,
				InboundDate:    database.Now(),
				ExpiryDate:     lp.input.ExpiryDate,
				Actor:          sa,
				Remark:         "收货采集 " + receiptNo,
			})
			if err != nil {
				return err
			}
			batchDone[dedupe] = batchID
			lp.batchID = batchID
		}
		if lp.flags.SerialManaged {
			for _, sn := range lp.input.Serials {
				if _, _, err := s.opt.stock.SerialEvent(ctx, tx, stock.SerialOp{
					SerialNo: strings.TrimSpace(sn), SKUID: lp.input.SKUID, BatchID: lp.batchID,
					WarehouseID: inbound.WarehouseID, BinID: 0,
					Status: SerialStatusInStock,
					Source: stock.Source{Type: "receipt", No: receiptNo},
					Actor:  sa,
					Remark: "收货采集",
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// firstBatchNo 收货单批次冗余列（多批次收货取首个非空批次；明细级批次见上架任务与批次台账）。
func firstBatchNo(plan *receiptPlan) string {
	for _, lp := range plan.lines {
		if b := strings.TrimSpace(lp.input.BatchNo); b != "" {
			return b
		}
	}
	return ""
}

// progressAfterReceipt 入库单状态推进：DRAFT→RECEIVING（首次收货）→ 全部收齐
// RECEIVING→AWAITING_QC（plan §6.2，守卫迁移 + 审计）；收齐后即时做"质检完成或免检"
// 判定（全免检单直通 AWAITING_PUTAWAY，见 progressInboundAfterQC）。
func (s *Service) progressAfterReceipt(ctx context.Context, tx *gorm.DB, actor Actor, inbound *InboundOrder) error {
	if inbound.Status == InboundStatusDraft {
		n, err := s.repo.UpdateInboundStatus(ctx, tx, inbound.ID.Int64(), InboundStatusDraft, InboundStatusReceiving, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "入库单状态并发变化"})
		}
		e := actor.auditEntry("inbound_order", inbound.ID.Int64(), "status")
		e.Before, e.After = map[string]any{"status": inbound.Status}, map[string]any{"status": InboundStatusReceiving}
		if err := middlewareAudit(tx, e); err != nil {
			return err
		}
		inbound.Status = InboundStatusReceiving
	}
	items, err := s.repo.ListInboundItemsTx(ctx, tx, inbound.ID.Int64())
	if err != nil {
		return err
	}
	allReceived := true
	for _, it := range items {
		if it.QtyReceived.Sub(it.Qty).IsNegative() {
			allReceived = false
			break
		}
	}
	if allReceived {
		n, err := s.repo.UpdateInboundStatus(ctx, tx, inbound.ID.Int64(), InboundStatusReceiving, InboundStatusAwaitingQC, actor.UserID)
		if err := guardRows(n, err); err != nil {
			if errors.Is(err, errGuardMiss) {
				return nil // 非 RECEIVING（并发已推进）不视为错误
			}
			return err
		}
		e := actor.auditEntry("inbound_order", inbound.ID.Int64(), "status")
		e.Before, e.After = map[string]any{"status": InboundStatusReceiving}, map[string]any{"status": InboundStatusAwaitingQC}
		if err := middlewareAudit(tx, e); err != nil {
			return err
		}
		inbound.Status = InboundStatusAwaitingQC
		// 全部明细免检直通的场景：收齐即"质检完成或免检"，直通 AWAITING_PUTAWAY。
		return s.progressInboundAfterQC(ctx, tx, actor, inbound)
	}
	return nil
}

// progressPOAfterReceipt 采购订单状态推进（plan §6.1）：
// APPROVED→PARTIAL_RECEIVED（首收）→RECEIVED_ALL（全部明细收齐）。
func (s *Service) progressPOAfterReceipt(ctx context.Context, tx *gorm.DB, actor Actor, po *PurchaseOrder) error {
	items, err := s.repo.ListPOItemsTx(ctx, tx, po.ID.Int64())
	if err != nil {
		return err
	}
	anyReceived, allReceived := false, true
	for _, it := range items {
		if it.QtyReceived.IsPositive() {
			anyReceived = true
		}
		if it.QtyReceived.Sub(it.QtyOrdered).IsNegative() {
			allReceived = false
		}
	}
	if !anyReceived {
		return nil
	}
	to := POStatusPartialReceived
	if allReceived {
		to = POStatusReceivedAll
	}
	if po.Status == to {
		return nil
	}
	if !canTransition(purchaseTransitions, po.Status, to) {
		return nil // 并发已推进到兼容态，不阻断收货主流程
	}
	n, err := s.repo.UpdatePOStatus(ctx, tx, po.ID.Int64(), po.Status, to, actor.UserID)
	if err := guardRows(n, err); err != nil {
		return response.NewError(ErrStatusConflict, map[string]any{"reason": "采购订单状态并发变化"})
	}
	e := actor.auditEntry("purchase_order", po.ID.Int64(), "status")
	e.Before, e.After = map[string]any{"status": po.Status}, map[string]any{"status": to}
	return middlewareAudit(tx, e)
}

// replayReceipt 幂等重放结果（返回既有单号，不重复累计——plan §8.5）。
func (s *Service) replayReceipt(ctx context.Context, rc *Receipt) (*ReceiptResult, error) {
	inbound, err := s.repo.FindInboundByNo(ctx, rc.InboundNo)
	if err != nil {
		return nil, err
	}
	res := &ReceiptResult{
		ReceiptNo: rc.ReceiptNo, Replay: true, InboundNo: rc.InboundNo,
	}
	if inbound != nil {
		res.InboundStatus = inbound.Status
		res.PONo = inbound.SourceNo
		if inbound.SourceType == SourceTypePurchase {
			if po, err := s.repo.FindPOByNo(ctx, inbound.SourceNo); err == nil && po != nil {
				res.POStatus = po.Status
			}
		}
	}
	tasks, _, err := s.repo.ListTasks(ctx, TaskListFilter{
		InboundNo: rc.InboundNo, Page: 1, PageSize: 1 << 20,
		Scope: WarehouseScope{All: true}, // 单据联动查询不受列表数据权限过滤
	})
	if err == nil {
		for _, t := range tasks {
			if t.ReceiptNo == rc.ReceiptNo {
				res.PutawayTasks = append(res.PutawayTasks, t.PutawayNo)
			}
		}
	}
	return res, nil
}

// isUniqueViolationErr PostgreSQL 唯一约束冲突（23505）判定（inventory/masterdata 同款）。
func isUniqueViolationErr(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
