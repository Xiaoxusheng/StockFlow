package purchase

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// pendingSlot 待检库存分摊槽位（库位+批次维度，容量随两方向分摊递减）。
type pendingSlot struct {
	task      *PutawayTask
	remaining stock.Qty
}

// 质检业务（business-flow §4、plan §6.3/§7）：
// 检验方式免检/抽检/全检（§4.1）；质检记录字段（§4.2：检验/合格/不合格数量、检验人、
// 检验时间、原因、图片附件占位）；处理结果九类（§4.3——库存映射为二元去向：
// 合格数量 → InspectResult(pass) 转 available，不合格数量 → InspectResult(!pass)
// 转 defective；退供应商/报废等处置的业务执行属退货/调整域，质检单记录处置结论）。
// 状态机 PENDING→INSPECTING→COMPLETED（COMPLETED 即处理结果落定并触发库存映射）。
//
// 库存映射前置：InspectResult 作用于 pending_inspect 库存（InspectResult 前置状态），
// 故执行前该入库单相关 SKU 的待检量必须已全部上架（上架任务完成落账）。

// QCCreateInput 质检单创建入参（M2 本域 source_type=INBOUND；RETURN 退货质检由
// returns 域经 QCCreator 窄接口复用本实现——plan §3.1，禁止另造）。
type QCCreateInput struct {
	SourceNo       string        `json:"source_no" binding:"required"` // 入库单号
	InspectionType string        `json:"inspection_type" binding:"required"`
	Lines          []QCLineInput `json:"lines" binding:"required"`
	Remark         string        `json:"remark"`
}

// QCLineInput 质检明细计划行（检验对象：SKU + 批次 + 计划检验数量）。
type QCLineInput struct {
	SKUID        int64     `json:"sku_id" binding:"required"`
	BatchNo      string    `json:"batch_no"`
	QtyInspected stock.Qty `json:"qty_inspected" binding:"required"`
}

// QCExecuteInput 质检结果提交入参（§4.2/§4.3）。
type QCExecuteInput struct {
	Lines     []QCExecuteLine `json:"lines" binding:"required"`
	Result    string          `json:"result" binding:"required"`
	ImageRefs StringList      `json:"image_refs"`
	Remark    string          `json:"remark"`
}

// QCExecuteLine 质检结果行（合格+不合格 必须 = 该行检验数量）。
type QCExecuteLine struct {
	LineNo       int       `json:"line_no" binding:"required"`
	QtyQualified stock.Qty `json:"qty_qualified"`
	QtyDefective stock.Qty `json:"qty_defective"`
	Remark       string    `json:"remark"`
}

var qcResults = map[string]bool{
	"合格": true, "部分合格": true, "不合格": true, "退供应商": true, "报废": true,
	"返工": true, "降级": true, "转不良品仓": true, "特批放行": true,
}

// CreateQC 创建质检单（PENDING）。入库单须处于 AWAITING_QC；检验数量不得超过
// 该 SKU 已收货量（收齐才可质检——plan §6.2 RECEIVING→AWAITING_QC 前置）。
func (s *Service) CreateQC(ctx context.Context, actor Actor, in QCCreateInput) (*QualityOrder, error) {
	if !qcInspectionTypes[in.InspectionType] {
		return nil, response.NewError(ErrQCInspectionTypeInvalid, map[string]any{"inspection_type": in.InspectionType})
	}
	if len(in.Lines) == 0 {
		return nil, invalidParam("lines", "质检单必须至少一行明细")
	}
	o, err := s.repo.FindInboundByNo(ctx, strings.TrimSpace(in.SourceNo))
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, response.NewError(ErrInboundNotFound, map[string]any{"inbound_no": in.SourceNo})
	}
	if o.Status != InboundStatusAwaitingQC && o.Status != InboundStatusAwaitingPutaway {
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{
			"status": o.Status, "reason": "入库单未收齐进入待质检，不能创建质检单",
		})
	}
	// 已质检处理量按 SKU 汇总（免检直通行在收货时计入 qty_inspected；多质检单分摊累计）。
	items, err := s.repo.ListInboundItems(ctx, o.ID.Int64())
	if err != nil {
		return nil, err
	}
	receivedBySKU := map[int64]stock.Qty{}
	settledBySKU := map[int64]stock.Qty{}
	for _, it := range items {
		receivedBySKU[it.SKUID] = it.QtyReceived
		settledBySKU[it.SKUID] = it.QtyInspected
	}
	qcItems := make([]*QualityItem, 0, len(in.Lines))
	skuSeen := map[int64]bool{}
	var planned stock.Qty
	for i, li := range in.Lines {
		field := fmt.Sprintf("lines[%d]", i)
		if li.SKUID <= 0 {
			return nil, invalidParam(field+".sku_id", "必须为正整数")
		}
		if !li.QtyInspected.IsPositive() {
			return nil, invalidParam(field+".qty_inspected", "必须为正数")
		}
		if _, known := receivedBySKU[li.SKUID]; !known {
			return nil, invalidParam(field+".sku_id", fmt.Sprintf("SKU %d 不在该入库单明细中", li.SKUID))
		}
		if skuSeen[li.SKUID] {
			return nil, invalidParam(field+".sku_id", "同一 SKU 在质检单内重复")
		}
		skuSeen[li.SKUID] = true
		// 计划检验量累计不得超过该 SKU 未处理余量（已收 - 已处理）。
		remaining := receivedBySKU[li.SKUID].Sub(settledBySKU[li.SKUID])
		if remaining.Sub(li.QtyInspected).IsNegative() {
			return nil, response.NewError(ErrQCQtyExceedsReceived, map[string]any{
				"line": i + 1, "sku_id": li.SKUID,
				"qty_received": receivedBySKU[li.SKUID].String(),
				"qty_settled":  settledBySKU[li.SKUID].String(),
				"this_time":    li.QtyInspected.String(),
			})
		}
		settledBySKU[li.SKUID] = settledBySKU[li.SKUID].Add(li.QtyInspected)
		planned = planned.Add(li.QtyInspected)
		qcItems = append(qcItems, &QualityItem{
			LineNo:       len(qcItems) + 1,
			SKUID:        li.SKUID,
			BatchNo:      strings.TrimSpace(li.BatchNo),
			QtyInspected: li.QtyInspected,
		})
	}
	qc := &QualityOrder{
		SourceType:     QCSourceInbound,
		SourceNo:       o.InboundNo,
		WarehouseID:    o.WarehouseID,
		InspectionType: in.InspectionType,
		Status:         QCStatusPending,
		QtyInspected:   planned,
		ImageRefs:      StringList{},
		Remark:         strings.TrimSpace(in.Remark),
		CreatedBy:      database.ID(actor.UserID),
		UpdatedBy:      database.ID(actor.UserID),
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		no, err := docnum.NextWithRetry(ctx, tx, docRule("QC"), func(tx *gorm.DB, no string) error {
			qc.QCNo = no
			return s.repo.InsertQC(ctx, tx, qc, qcItems)
		})
		if err != nil {
			return err
		}
		qc.QCNo = no
		e := actor.auditEntry("quality_order", qc.ID.Int64(), "create")
		e.After = qc
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindQCByID(ctx, qc.ID.Int64())
}

var qcInspectionTypes = map[string]bool{InspectionExempt: true, InspectionSample: true, InspectionFull: true}

// parseQtyText 十进制数量文本解析（QCCreator 跨域线格式入参）。
func parseQtyText(s string) (stock.Qty, error) {
	return stock.ParseQty(s)
}

// StartQC 开始检验（PENDING→INSPECTING，记录检验人）。
func (s *Service) StartQC(ctx context.Context, actor Actor, id int64) (*QualityOrder, error) {
	qc, err := s.repo.FindQCByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if qc == nil {
		return nil, response.NewError(ErrQCNotFound, nil)
	}
	if !canTransition(qcTransitions, qc.Status, QCStatusInspecting) {
		return nil, response.NewError(ErrQCStatusNotAllowed, map[string]any{
			"status": qc.Status, "to": QCStatusInspecting,
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdateQCStatus(ctx, tx, id, qc.Status, QCStatusInspecting, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "质检单状态并发变化"})
		}
		if err := s.repo.UpdateQCCols(ctx, tx, id, map[string]any{
			"inspector_id": actor.UserID, "inspector_name": actor.Username,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("quality_order", id, "start")
		e.Before, e.After = map[string]any{"status": qc.Status}, map[string]any{"status": QCStatusInspecting}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindQCByID(ctx, id)
}

// ExecuteQC 提交质检结果（INSPECTING→COMPLETED）：
//  1. 逐行校验 合格+不合格=检验数量、结果值域（§4.3 九类）；
//  2. 入库单明细 qty_inspected 累计（守卫 ≤ 已收货）；
//  3. 库存映射：待检库存（已完成上架任务的 pending_inspect 数量）按库位确定性分摊，
//     逐库位调 StockGateway.InspectResult（pass → available / defect → defective），
//     幂等键 plan §7 构成律"动作:单号:行:方向:五维尾缀"：
//     inspect:{qc_no}:{line_no}:{pass|defect}:{bin}:{sku}:{batch}；
//  4. 待检量未全部上架 → ErrPendingNotPutaway（先完成上架再提交）；
//  5. 全部明细处理完成 → 入库单 AWAITING_QC→AWAITING_PUTAWAY 推进判定。
func (s *Service) ExecuteQC(ctx context.Context, actor Actor, id int64, in QCExecuteInput) (*QualityOrder, error) {
	qc, err := s.repo.FindQCByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if qc == nil {
		return nil, response.NewError(ErrQCNotFound, nil)
	}
	if qc.Status != QCStatusInspecting {
		return nil, response.NewError(ErrQCStatusNotAllowed, map[string]any{
			"status": qc.Status, "to": QCStatusCompleted, "reason": "请先开始检验",
		})
	}
	if !qcResults[in.Result] {
		return nil, response.NewError(ErrQCResultInvalid, map[string]any{"result": in.Result})
	}
	qcItems, err := s.repo.ListQCItems(ctx, id)
	if err != nil {
		return nil, err
	}
	itemByLine := map[int]*QualityItem{}
	for _, it := range qcItems {
		itemByLine[it.LineNo] = it
	}
	byLineIdx := map[int]*QCExecuteLine{}
	var totalQualified, totalDefective stock.Qty
	for _, l := range in.Lines {
		it, ok := itemByLine[l.LineNo]
		if !ok {
			return nil, invalidParam("lines", fmt.Sprintf("明细行 %d 不存在", l.LineNo))
		}
		if prev, dup := byLineIdx[l.LineNo]; dup {
			return nil, invalidParam("lines", fmt.Sprintf("明细行 %d 重复（行 %d）", l.LineNo, prev.LineNo))
		}
		byLineIdx[l.LineNo] = &l
		sum := l.QtyQualified.Add(l.QtyDefective)
		if !sum.IsPositive() || sum.Sub(it.QtyInspected).IsNegative() || it.QtyInspected.Sub(sum).IsNegative() {
			return nil, response.NewError(ErrQCQtyInvalid, map[string]any{
				"line_no": l.LineNo, "qty_inspected": it.QtyInspected.String(),
				"qty_qualified": l.QtyQualified.String(), "qty_defective": l.QtyDefective.String(),
			})
		}
		totalQualified = totalQualified.Add(l.QtyQualified)
		totalDefective = totalDefective.Add(l.QtyDefective)
	}
	if len(byLineIdx) != len(qcItems) {
		return nil, invalidParam("lines", "必须提交全部明细行的检验结果")
	}
	inbound, err := s.repo.FindInboundByNo(ctx, qc.SourceNo)
	if err != nil {
		return nil, err
	}
	if inbound == nil {
		return nil, response.NewError(ErrInboundNotFound, map[string]any{"inbound_no": qc.SourceNo})
	}
	if err := s.requireStock(); err != nil {
		return nil, err
	}
	sa := actor.stockActor()
	source := stock.Source{Type: "quality_order", No: qc.QCNo}

	err = s.tx(ctx, func(tx *gorm.DB) error {
		// 待检库存分摊：已完成上架任务（from_state=pending_inspect）即待检库存所在库位；
		// 按 (bin_id, batch_id) 升序确定性分摊，合格先于不良（重试路径键稳定）。
		// 待检库存分摊槽位：已完成上架任务（from_state=pending_inspect）即待检库存所在
		// 库位；槽位容量 = 任务数量，合格与不良共用同一份容量（先合格后不良），避免
		// 同一库位被两方向重复分摊（真实库存层 pending_inspect 守卫同样兜底）。
		slotsBySKU := map[int64][]*pendingSlot{}
		for _, it := range qcItems {
			l := byLineIdx[it.LineNo]
			need := l.QtyQualified.Add(l.QtyDefective)
			if need.IsZero() {
				continue
			}
			tasks, err := s.repo.ListCompletedPendingTasksBySKU(ctx, qc.SourceNo, it.SKUID)
			if err != nil {
				return err
			}
			var pending stock.Qty
			slots := make([]*pendingSlot, 0, len(tasks))
			for _, t := range tasks {
				pending = pending.Add(t.Qty)
				slots = append(slots, &pendingSlot{task: t, remaining: t.Qty})
			}
			if pending.Sub(need).IsNegative() {
				return response.NewError(ErrPendingNotPutaway, map[string]any{
					"line_no": it.LineNo, "sku_id": it.SKUID,
					"pending_putaway": pending.String(), "need": need.String(),
				})
			}
			slotsBySKU[it.SKUID] = slots
		}
		// 库存映射（InspectResult 逐库位；幂等键按 plan §7 构成律
		// "动作:单号:行:方向:五维尾缀" 确定性拼接——重试必命中同键）。
		for _, it := range qcItems {
			l := byLineIdx[it.LineNo]
			allocate := func(qty stock.Qty, pass bool) error {
				remaining := qty
				for _, sl := range slotsBySKU[it.SKUID] {
					if remaining.IsZero() {
						break
					}
					take := sl.remaining
					if take.Sub(remaining).IsPositive() {
						take = remaining
					}
					if take.IsZero() {
						continue
					}
					t := sl.task
					dir := "defect"
					if pass {
						dir = "pass"
					}
					idem := fmt.Sprintf("inspect:%s:%d:%s:%d:%d:%d",
						qc.QCNo, it.LineNo, dir, t.TargetBinID, it.SKUID, t.BatchID)
					if _, err := s.opt.stock.InspectResult(ctx, tx, stock.InspectResultOp{
						Key: stock.RowKey{
							WarehouseID: t.TargetWarehouseID, ZoneID: t.TargetZoneID, ShelfID: t.TargetShelfID,
							BinID: t.TargetBinID, SKUID: it.SKUID, BatchID: t.BatchID,
						},
						Qty: take, Pass: pass,
						Source: source, Actor: sa,
						IdempotencyKey: idem,
						Remark:         "质检结果 " + qc.QCNo,
					}); err != nil {
						return err
					}
					sl.remaining = sl.remaining.Sub(take)
					remaining = remaining.Sub(take)
				}
				if remaining.IsPositive() {
					return response.NewError(ErrPendingNotPutaway, map[string]any{
						"line_no": it.LineNo, "sku_id": it.SKUID,
						"reason": "待检库存分摊不足", "undelivered": remaining.String(),
					})
				}
				return nil
			}
			if err := allocate(l.QtyQualified, true); err != nil {
				return err
			}
			if err := allocate(l.QtyDefective, false); err != nil {
				return err
			}
			// 入库单明细已质检累计（守卫：累计 ≤ 已收货；覆盖免检直通与多质检单分摊）。
			item, err := s.repo.FindInboundItemBySku(ctx, inbound.ID.Int64(), it.SKUID)
			if err != nil || item == nil {
				return response.NewError(ErrReceiptQtyMismatch, map[string]any{"sku_id": it.SKUID})
			}
			n, err := s.repo.AddInboundItemInspected(ctx, tx, item.ID.Int64(),
				l.QtyQualified.Add(l.QtyDefective), actor.UserID)
			if err := guardRows(n, err); err != nil {
				return response.NewError(ErrQCQtyExceedsReceived, map[string]any{
					"sku_id": it.SKUID, "reason": "已质检处理量超出已收货量（并发质检竞争）",
				})
			}
			// 质检明细结果落列。
			if err := s.repo.UpdateQCItemCols(ctx, tx, it.ID.Int64(), map[string]any{
				"qty_qualified": l.QtyQualified,
				"qty_defective": l.QtyDefective,
				"remark":        strings.TrimSpace(l.Remark),
				"updated_at":    database.Now(),
				"updated_by":    actor.UserID,
			}); err != nil {
				return err
			}
		}
		// 质检单守卫迁移 + 汇总落列。
		n, err := s.repo.UpdateQCStatus(ctx, tx, id, qc.Status, QCStatusCompleted, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "质检单状态并发变化"})
		}
		if err := s.repo.UpdateQCCols(ctx, tx, id, map[string]any{
			"qty_qualified": totalQualified, "qty_defective": totalDefective,
			"result": in.Result, "image_refs": in.ImageRefs,
			"inspector_id": actor.UserID, "inspector_name": actor.Username,
			"remark": strings.TrimSpace(in.Remark), "updated_at": database.Now(), "updated_by": actor.UserID,
		}); err != nil {
			return err
		}
		e := actor.auditEntry("quality_order", id, "execute")
		e.Request = in
		e.Before = map[string]any{"status": qc.Status}
		e.After = map[string]any{
			"status": QCStatusCompleted, "result": in.Result,
			"qty_qualified": totalQualified.String(), "qty_defective": totalDefective.String(),
		}
		if err := middlewareAudit(tx, e); err != nil {
			return err
		}
		// 全部明细质检完成（或免检直通）→ AWAITING_QC→AWAITING_PUTAWAY 推进判定。
		return s.progressInboundAfterQC(ctx, tx, actor, inbound)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindQCByID(ctx, id)
}

// progressInboundAfterQC 全部明细"质检完成或免检"判定（plan §6.2）：
// 每行 qty_inspected（质检处理量，含免检直通）≥ qty_received 时推进 AWAITING_QC→AWAITING_PUTAWAY，
// 并即时检查全部任务已完成 → COMPLETED（免检直通先完成的场景，瞬时中间态留审计轨迹）。
func (s *Service) progressInboundAfterQC(ctx context.Context, tx *gorm.DB, actor Actor, inbound *InboundOrder) error {
	items, err := s.repo.ListInboundItems(ctx, inbound.ID.Int64())
	if err != nil {
		return err
	}
	settled := true
	for _, it := range items {
		if it.QtyInspected.Sub(it.QtyReceived).IsNegative() {
			settled = false
			break
		}
	}
	if !settled || inbound.Status != InboundStatusAwaitingQC {
		return nil
	}
	n, err := s.repo.UpdateInboundStatus(ctx, tx, inbound.ID.Int64(), InboundStatusAwaitingQC, InboundStatusAwaitingPutaway, actor.UserID)
	if err := guardRows(n, err); err != nil {
		if isGuardMiss(err) {
			return nil
		}
		return err
	}
	inbound.Status = InboundStatusAwaitingPutaway
	e := actor.auditEntry("inbound_order", inbound.ID.Int64(), "status")
	e.Before, e.After = map[string]any{"status": InboundStatusAwaitingQC}, map[string]any{"status": InboundStatusAwaitingPutaway}
	if err := middlewareAudit(tx, e); err != nil {
		return err
	}
	return s.progressInboundAfterTask(ctx, tx, actor, inbound)
}

// isGuardMiss 守卫未命中判定（errGuardMiss 哨兵）。
func isGuardMiss(err error) bool { return err == errGuardMiss }

// QC / QCItem 查询视图。
type QCDetail struct {
	Order *QualityOrder  `json:"order"`
	Items []*QualityItem `json:"items"`
}

func (s *Service) GetQC(ctx context.Context, id int64, scope WarehouseScope) (*QCDetail, error) {
	qc, err := s.repo.FindQCByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：仓库不在范围内按不存在处理，
	// 与 sales/stockops/returns 详情接口同口径（plan §10.5）。
	if qc == nil || !scope.visible(qc.WarehouseID) {
		return nil, response.NewError(ErrQCNotFound, nil)
	}
	items, err := s.repo.ListQCItems(ctx, id)
	if err != nil {
		return nil, err
	}
	return &QCDetail{Order: qc, Items: items}, nil
}

func (s *Service) ListQC(ctx context.Context, f QCListFilter) ([]*QualityOrder, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)
	f.Page, f.PageSize = page, size
	return s.repo.ListQCs(ctx, f)
}

// ---- returns 域复用入口（plan §3.1 QCCreator：退货质检复用 purchase 质检单一套实现）----

// QCCreateLine QCCreator 导出的行结构（内建类型字段，router 闭包桥接 returns 消费接口）。
type QCCreateLine struct {
	LineNo       int    `json:"line_no"`
	SKUID        int64  `json:"sku_id"`
	BatchNo      string `json:"batch_no"`
	QtyInspected string `json:"qty_inspected"` // 十进制文本（numeric(18,4) 线格式）
	Remark       string `json:"remark"`
}

// QCCreatorService 退货质检创建入口（router 装配注入 returns 域的 QCCreator 消费接口）。
type QCCreatorService struct{ svc *Service }

// NewQCCreator 构造（router：returns.WithQCCreator(purchase.NewQCCreator(svc))）。
func NewQCCreator(svc *Service) *QCCreatorService { return &QCCreatorService{svc: svc} }

// CreateQC 创建来源质检单（sourceType=INBOUND/RETURN；RETURN 跳过入库单状态校验——
// 退货单据归 returns 域守卫，本入口只校验来源单号非空与数量值域）。
func (c *QCCreatorService) CreateQC(ctx context.Context, operatorID int64, operatorName, sourceType, sourceNo, qcType string, lines []QCCreateLine) (string, error) {
	if sourceType != QCSourceInbound && sourceType != QCSourceReturn {
		return "", invalidParam("source_type", "必须为 INBOUND 或 RETURN")
	}
	if sourceType == QCSourceReturn && strings.TrimSpace(sourceNo) == "" {
		return "", invalidParam("source_no", "退货质检必须携带退货单号")
	}
	in := QCCreateInput{SourceNo: strings.TrimSpace(sourceNo), InspectionType: qcType}
	for _, l := range lines {
		q, err := parseQtyText(l.QtyInspected)
		if err != nil {
			return "", invalidParam("lines[].qty_inspected", err.Error())
		}
		in.Lines = append(in.Lines, QCLineInput{SKUID: l.SKUID, BatchNo: l.BatchNo, QtyInspected: q})
	}
	var qcNo string
	err := c.svc.tx(ctx, func(tx *gorm.DB) error {
		qc := &QualityOrder{
			SourceType: sourceType, SourceNo: strings.TrimSpace(sourceNo),
			WarehouseID:    0, // 退货质检作业仓由 returns 域在来源单据上守卫；本表列随来源单补录
			InspectionType: qcType, Status: QCStatusPending, ImageRefs: StringList{},
			CreatedBy: database.ID(operatorID), UpdatedBy: database.ID(operatorID),
		}
		items := make([]*QualityItem, 0, len(in.Lines))
		var planned stock.Qty
		for i, li := range in.Lines {
			planned = planned.Add(li.QtyInspected)
			items = append(items, &QualityItem{
				LineNo: i + 1, SKUID: li.SKUID, BatchNo: strings.TrimSpace(li.BatchNo),
				QtyInspected: li.QtyInspected,
			})
		}
		qc.QtyInspected = planned
		no, err := docnum.NextWithRetry(ctx, tx, docRule("QC"), func(tx *gorm.DB, no string) error {
			qc.QCNo = no
			return c.svc.repo.InsertQC(ctx, tx, qc, items)
		})
		if err != nil {
			return err
		}
		qc.QCNo = no
		qcNo = no
		e := Actor{UserID: operatorID, Username: operatorName}.auditEntry("quality_order", qc.ID.Int64(), "create")
		e.After = qc
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return "", err
	}
	return qcNo, nil
}
