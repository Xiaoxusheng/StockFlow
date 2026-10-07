package returns

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 销售退货（business-flow §9.1：退货申请 → 审核 → 收货 → 质检 → 正常库存/不良品）
// 与退货单共享动作（提交/审批/取消，business-flow §12.1 退货必审批）。
//
// 库存动作（plan §6.9/§7 冻结映射）：
//   - 收货：逐行 Putaway(RequireInspect=true)（total↑+pending_inspect↑，入待检）+
//     序列号 SKU 逐件 SerialEvent(RETURNED)，幂等键 putaway:{return_no}:{line}:五维:{qty}
//     （或 Idempotency-Key 头透传，plan §5 事件型收货口径）；
//   - 质检：经 QCCreator 窄接口创建质检单（purchase 实现，禁止本域另造）；
//     结果应用触发 InspectResult（合格→available，不合格→defective，§9.1 质检决定去向），
//     幂等键 inspect:{qc_no}:{line_no}:{pass|defect}。
// 全部与单据状态迁移同事务（architecture §4）。

// 单据类型（document_approvals.target_type / 审计 object_type）。
const docTypeReturnOrder = "return_order"

// ---- 入参 ----

// SalesReturnLineInput 销售退货创建行（qty_return ≤ 原销售单行已发货量 − 已退量）。
type SalesReturnLineInput struct {
	LineNo    int64  `json:"line_no"`
	SKUID     int64  `json:"sku_id"`
	QtyReturn string `json:"qty_return"`
	Reason    string `json:"reason"`
	Remark    string `json:"remark"`
}

// SalesReturnCreateInput 创建销售退货入参。
type SalesReturnCreateInput struct {
	SONo        string                 `json:"so_no"`
	CustomerID  int64                  `json:"customer_id"`
	WarehouseID int64                  `json:"warehouse_id"`
	Remark      string                 `json:"remark"`
	Lines       []SalesReturnLineInput `json:"lines"`
}

// ReceiveLineInput 退货收货行（库位四维 + 批次 + 数量 + 序列号逐件采集）。
type ReceiveLineInput struct {
	LineNo  int64    `json:"line_no"`
	ZoneID  int64    `json:"zone_id"`
	ShelfID int64    `json:"shelf_id"`
	BinID   int64    `json:"bin_id"`
	BatchNo string   `json:"batch_no"`
	Qty     string   `json:"qty"`
	Serials []string `json:"serials"`
}

// ReceiveInput 退货收货入参（销售退货；IdempotencyKey 为可选 HTTP 头透传，
// plan §5 幂等口径：事件型收货透传给原语）。
type ReceiveInput struct {
	Lines          []ReceiveLineInput `json:"lines"`
	IdempotencyKey string             `json:"-"`
}

// SubmitQCInput 提交质检入参（全部明细收齐后触发；质检单经 QCCreator 窄接口创建）。
type SubmitQCInput struct {
	Remark string `json:"remark"`
}

// QCResultLineInput 质检结果行（合格/不合格拆分；inspect 的目标行必须与收货行一致——
// 原语以待检量守卫，定位错误会显式失败而不是错账）。
type QCResultLineInput struct {
	LineNo       int64  `json:"line_no"`
	ZoneID       int64  `json:"zone_id"`
	ShelfID      int64  `json:"shelf_id"`
	BinID        int64  `json:"bin_id"`
	BatchID      int64  `json:"batch_id"`
	QtyQualified string `json:"qty_qualified"`
	QtyDefective string `json:"qty_defective"`
	// SerialsPassed 质检合格逐件序列号（可选，序列号 SKU 逐件回 IN_STOCK；
	// 不良件保持 RETURNED 待处置）。
	SerialsPassed []string `json:"serials_passed"`
}

// QCResultInput 质检结果应用入参（qc_no 来自 QCCreator 创建结果，plan §7 inspect 键构成）。
type QCResultInput struct {
	QCNo  string              `json:"qc_no"`
	Lines []QCResultLineInput `json:"lines"`
}

// ApproveInput 审批入参（通过/驳回共用资源点，plan §9.1 动作词 approve）。
type ApproveInput struct {
	Approved bool   `json:"approved"`
	Opinion  string `json:"opinion"`
}

// CancelInput 取消入参。
type CancelInput struct {
	Reason string `json:"reason"`
}

// ---- 视图 ----

// ReturnItemView 退货明细视图。
type ReturnItemView struct {
	ID           database.ID `json:"id"`
	LineNo       int64       `json:"line_no"`
	SKUID        int64       `json:"sku_id"`
	QtyReturn    string      `json:"qty_return"`
	QtyReceived  string      `json:"qty_received"`
	QtyInspected string      `json:"qty_inspected"`
	QtyDefective string      `json:"qty_defective"`
	Reason       string      `json:"reason"`
	Remark       string      `json:"remark"`
}

// ReturnOrderView 退货单视图。
type ReturnOrderView struct {
	ID          database.ID       `json:"id"`
	ReturnNo    string            `json:"return_no"`
	Type        string            `json:"type"`
	SourceNo    string            `json:"source_no"`
	CustomerID  int64             `json:"customer_id"`
	SupplierID  int64             `json:"supplier_id"`
	WarehouseID int64             `json:"warehouse_id"`
	Status      string            `json:"status"`
	Remark      string            `json:"remark"`
	CreatedAt   database.JSONTime `json:"created_at"`
	UpdatedAt   database.JSONTime `json:"updated_at"`
	Items       []ReturnItemView  `json:"items,omitempty"`
}

func newItemView(it *ReturnItem) ReturnItemView {
	return ReturnItemView{
		ID: it.ID, LineNo: it.LineNo, SKUID: it.SKUID,
		QtyReturn: it.QtyReturn.String(), QtyReceived: it.QtyReceived.String(),
		QtyInspected: it.QtyInspected.String(), QtyDefective: it.QtyDefective.String(),
		Reason: it.Reason, Remark: it.Remark,
	}
}

func newOrderView(o *ReturnOrder, items []*ReturnItem) *ReturnOrderView {
	v := &ReturnOrderView{
		ID: o.ID, ReturnNo: o.ReturnNo, Type: o.Type, SourceNo: o.SourceNo,
		CustomerID: o.CustomerID, SupplierID: o.SupplierID, WarehouseID: o.WarehouseID,
		Status: o.Status, Remark: o.Remark, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	for _, it := range items {
		v.Items = append(v.Items, newItemView(it))
	}
	return v
}

// ---- 创建（销售退货）----

// CreateSalesReturn 创建销售退货单（DRAFT）。业务关系校验（api.md §4）经
// SalesOrderReader 窄接口：销售单存在且已发货、退货仓一致、行/SKU 归属一致、
// 逐行退量 ≤ 已发货量 − 本域累计已退量（plan §3.1：退量由 returns 域自己的流水累计比对）。
func (s *Service) CreateSalesReturn(ctx context.Context, actor Actor, in SalesReturnCreateInput) (*ReturnOrderView, error) {
	reader, err := s.requireSalesOrders()
	if err != nil {
		return nil, err
	}
	if err := validateReturnInput(in.SONo, in.WarehouseID, in.Lines); err != nil {
		return nil, err
	}
	if in.CustomerID <= 0 {
		return nil, paramError("customer_id", "销售退货必填客户")
	}
	soID, soWH, soLines, found, err := reader.FindReturnable(ctx, in.SONo)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, response.NewError(ErrSourceOrderNotFound, map[string]any{"so_no": in.SONo})
	}
	if soWH != in.WarehouseID {
		return nil, response.NewError(ErrSourceMismatch, map[string]any{
			"reason": "退货仓必须与原销售单发货仓一致", "so_warehouse_id": soWH, "warehouse_id": in.WarehouseID,
		})
	}
	soLine := lineQtyMap(soLines)

	var view *ReturnOrderView
	err = s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 逐行退量累计（plan §3.1）：同来源同行的未取消退货单明细合计；事务内读取，
		// 与插入同事务。同来源并发创建经事务级咨询锁串行化（修复轮）——后到者等待
		// 先到者提交后再读累计，杜绝双双读到 returned=0 而累计超退。
		if err := s.repo.LockSourceSerial(tx, in.SONo); err != nil {
			return err
		}
		returned, err := s.sumReturned(tx, in.SONo, ReturnTypeSales, 0)
		if err != nil {
			return err
		}
		items, err := buildReturnItems(in.Lines, soLine, returned)
		if err != nil {
			return err
		}
		o := &ReturnOrder{
			Type: ReturnTypeSales, SourceNo: in.SONo, CustomerID: in.CustomerID,
			WarehouseID: in.WarehouseID, Status: ReturnStatusDraft, Remark: in.Remark,
			CreatedAt: database.Now(), UpdatedAt: database.Now(),
			CreatedBy: actor.UserID, UpdatedBy: actor.UserID,
		}
		id, err := s.insertReturnWithNo(ctx, tx, o, items, actor)
		if err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, id, "create"),
			in, map[string]any{"return_no": o.ReturnNo, "status": o.Status, "lines": len(items)}, nil)); err != nil {
			return err
		}
		view = newOrderView(o, items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = soID // 预留：后续需要销售单 ID 级关联时使用（000010 以 source_no 逻辑引用）
	return view, nil
}

// insertReturnWithNo 取单号（RT 前缀，business-flow §13.1）并落单据与明细；
// 唯一单号冲突由 docnum.NextWithRetry 取下号重试（历史遗留同格式单号兜底）。
// 返回单据 ID（回调内插入后回填 o.ID）。
func (s *Service) insertReturnWithNo(ctx context.Context, tx *gorm.DB, o *ReturnOrder, items []*ReturnItem, actor Actor) (int64, error) {
	rule, _ := docnum.RuleFor("RT")
	var id int64
	if _, err := docnum.NextWithRetry(ctx, tx, rule, func(tx *gorm.DB, no string) error {
		o.ReturnNo = no
		if err := s.repo.InsertReturnOrder(tx, o); err != nil {
			return err
		}
		id = o.ID.Int64()
		for _, it := range items {
			it.ReturnID = id
			it.CreatedBy = actor.UserID
			it.UpdatedBy = actor.UserID
		}
		return s.repo.InsertReturnItems(tx, items)
	}); err != nil {
		return 0, err
	}
	return id, nil
}

func validateReturnInput(sourceNo string, warehouseID int64, lines []SalesReturnLineInput) error {
	if sourceNo == "" {
		return paramError("source_no", "来源单号必填")
	}
	if warehouseID <= 0 {
		return paramError("warehouse_id", "退货仓必填")
	}
	if len(lines) == 0 {
		return paramError("lines", "退货明细不能为空")
	}
	seen := map[int64]bool{}
	for _, ln := range lines {
		if ln.LineNo <= 0 {
			return paramError("line_no", "必须为正整数")
		}
		if seen[ln.LineNo] {
			return paramError("line_no", fmt.Sprintf("行号 %d 重复", ln.LineNo))
		}
		seen[ln.LineNo] = true
		if ln.SKUID <= 0 {
			return paramError("sku_id", "必须为正整数")
		}
		q, err := stock.ParseQty(ln.QtyReturn)
		if err != nil || !q.IsPositive() {
			return paramError("qty_return", "必须为正数（numeric(18,4)）")
		}
		if ln.Reason == "" {
			return paramError("reason", "退货原因必填（business-flow §9.1）")
		}
	}
	return nil
}

func lineQtyMap(lines []ReturnableLine) map[lineKey]int64 {
	m := make(map[lineKey]int64, len(lines))
	for _, l := range lines {
		m[lineKey{LineNo: l.LineNo, SKUID: l.SKUID}] = l.Qty
	}
	return m
}

// buildReturnItems 校验并构造明细：行必须存在于来源单（行号+SKU 匹配），
// 退量 ≤ 来源量 − 已退量（business-flow §2.3 同族防超量口径）。
func buildReturnItems(in []SalesReturnLineInput, source map[lineKey]int64, returned map[lineKey]stock.Qty) ([]*ReturnItem, error) {
	items := make([]*ReturnItem, 0, len(in))
	for _, ln := range in {
		q, err := stock.ParseQty(ln.QtyReturn)
		if err != nil || !q.IsPositive() {
			return nil, paramError("qty_return", "必须为正数（numeric(18,4)）")
		}
		key := lineKey{LineNo: ln.LineNo, SKUID: ln.SKUID}
		base, ok := source[key]
		if !ok || base <= 0 {
			return nil, response.NewError(ErrLineNotFound, map[string]any{
				"line_no": ln.LineNo, "sku_id": ln.SKUID,
				"reason": "来源单不存在该明细行（行号+SKU 必须与来源单一致）",
			})
		}
		baseQty, perr := stock.ParseQty(fmt.Sprint(base))
		if perr != nil {
			return nil, response.NewError(response.CodeInternalError, map[string]any{"reason": "来源单数量解析失败"})
		}
		already := returned[key]
		if q.Add(already).Sub(baseQty).IsPositive() {
			return nil, response.NewError(ErrQtyExceeded, map[string]any{
				"line_no": ln.LineNo, "sku_id": ln.SKUID,
				"qty_return": q.String(), "source_qty": baseQty.String(), "already_returned": already.String(),
				"reason": "累计退量超出来源单可退量",
			})
		}
		items = append(items, &ReturnItem{
			LineNo: ln.LineNo, SKUID: ln.SKUID, QtyReturn: q,
			Reason: ln.Reason, Remark: ln.Remark,
			CreatedAt: database.Now(), UpdatedAt: database.Now(),
		})
	}
	return items, nil
}

func (s *Service) sumReturned(q *gorm.DB, sourceNo, orderType string, excludeReturnID int64) (map[lineKey]stock.Qty, error) {
	rows, err := s.repo.SumReturnedBySource(q, sourceNo, orderType, excludeReturnID)
	if err != nil {
		return nil, err
	}
	return returnedBySourceMap(rows), nil
}

// ---- 共享动作：提交 / 审批 / 取消（business-flow §12.1 退货必审批）----

// SubmitReturn 提交审核（DRAFT→PENDING_APPROVAL），落 SUBMIT 审批记录。
func (s *Service) SubmitReturn(ctx context.Context, actor Actor, id int64) (*ReturnOrderView, error) {
	var view *ReturnOrderView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, items, err := s.loadOrderWithItems(tx, id)
		if err != nil {
			return err
		}
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if len(items) == 0 {
			return paramError("lines", "退货明细不能为空")
		}
		for _, it := range items {
			if it.Reason == "" {
				return paramError("reason", fmt.Sprintf("行 %d 退货原因必填（business-flow §9.1/§12.1）", it.LineNo))
			}
		}
		before := snapshotOrder(o)
		if err := s.guardReturnStatus(tx, o, ReturnStatusPendingApproval, "", actor.UserID); err != nil {
			return err
		}
		if err := s.repo.InsertApproval(tx, approvalRecord(docTypeReturnOrder, o.ReturnNo,
			ApprovalActionSubmit, "", "", actor)); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "submit"),
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

// ApproveReturn 审批（PENDING_APPROVAL→APPROVED / 驳回→DRAFT），落审批记录与审计。
func (s *Service) ApproveReturn(ctx context.Context, actor Actor, id int64, in ApproveInput) (*ReturnOrderView, error) {
	var view *ReturnOrderView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, items, err := s.loadOrderWithItems(tx, id)
		if err != nil {
			return err
		}
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		before := snapshotOrder(o)
		target := ReturnStatusApproved
		recordAction, result := ApprovalActionApprove, "APPROVED"
		auditAction := "approve" // plan §9.1 动作词（operation_logs.action）
		if !in.Approved {
			target = ReturnStatusDraft
			recordAction, result, auditAction = ApprovalActionReject, "REJECTED", "reject"
		}
		// 审批迁移携带 approved_by/at（tsCol 白名单 approved_at，business-flow §13.4）。
		if !returnTransitionAllowed(o.Type, o.Status, target) {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": o.ID.Int64(), "return_no": o.ReturnNo,
				"from": o.Status, "to": target, "reason": "非法状态迁移",
			})
		}
		n, err := s.repo.UpdateReturnStatus(tx, o.ID.Int64(), []string{o.Status}, target, "approved_at", actor.UserID,
			map[string]any{"approved_by": actor.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": o.ID.Int64(), "from": o.Status, "to": target, "reason": "状态已被并发变更",
			})
		}
		o.Status = target
		if err := s.repo.InsertApproval(tx, approvalRecord(docTypeReturnOrder, o.ReturnNo,
			recordAction, result, in.Opinion, actor)); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), auditAction),
			map[string]any{"approved": in.Approved, "opinion": in.Opinion},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
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

// CancelReturn 取消（未收货/未出库前，business-flow §13.3：已影响库存不能取消，
// 只能反向冲正）；落 CANCEL 审批记录。
func (s *Service) CancelReturn(ctx context.Context, actor Actor, id int64, in CancelInput) (*ReturnOrderView, error) {
	var view *ReturnOrderView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, items, err := s.loadOrderWithItems(tx, id)
		if err != nil {
			return err
		}
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if o.Type == ReturnTypeSales {
			for _, it := range items {
				if it.QtyReceived.IsPositive() {
					return response.NewError(ErrStatusConflict, map[string]any{
						"return_id": o.ID.Int64(), "line_no": it.LineNo,
						"reason": "已有收货，不能取消（business-flow §13.3：只能反向冲正）",
					})
				}
			}
		}
		before := snapshotOrder(o)
		if err := s.guardReturnStatus(tx, o, ReturnStatusCancelled, "cancelled_at", actor.UserID); err != nil {
			return err
		}
		if err := s.repo.InsertApproval(tx, approvalRecord(docTypeReturnOrder, o.ReturnNo,
			ApprovalActionCancel, "CANCELLED", in.Reason, actor)); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "cancel"),
			map[string]any{"reason": in.Reason},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
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

// ---- 收货与质检（销售退货专属）----

// ReceiveSalesReturn 退货收货（APPROVED→RECEIVING 首次，之后停留 RECEIVING 支持部分多次收货）。
//
// 幂等语义（plan §7/§8.5）：逐行 Putaway 幂等键（确定性拼接或 Idempotency-Key 头透传）
// 命中 inventory_ledgers 部分唯一索引重放——整单重放返回当前状态不做任何变更；
// 部分行重放为矛盾请求，整体回滚（ErrPartialReplay）。数量守恒：qty_received 累计
// 带 WHERE 守卫，超量收货整体回滚（business-flow §2.3 同族，plan §6.1 超量 4xx）。
func (s *Service) ReceiveSalesReturn(ctx context.Context, actor Actor, id int64, in ReceiveInput) (*ReturnOrderView, error) {
	gw, err := s.requireStock()
	if err != nil {
		return nil, err
	}
	if len(in.Lines) == 0 {
		return nil, paramError("lines", "收货明细不能为空")
	}
	if in.IdempotencyKey != "" {
		if err := validateIdempotencyKey(in.IdempotencyKey); err != nil {
			return nil, err
		}
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
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if o.Type != ReturnTypeSales {
			return response.NewError(ErrSourceMismatch, map[string]any{
				"return_id": id, "reason": "销售退货收货接口不适用于采购退货单",
			})
		}
		if o.Status != ReturnStatusApproved && o.Status != ReturnStatusReceiving {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": id, "return_no": o.ReturnNo, "from": o.Status,
				"reason": "仅已审核（APPROVED）或收货中（RECEIVING）可收货",
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
		before := snapshotOrder(o)
		ia := actor.inventoryActor()
		newLines, replayLines := 0, 0
		for _, ln := range in.Lines {
			it := itemByLine[ln.LineNo]
			if it == nil {
				return response.NewError(ErrLineNotFound, map[string]any{"line_no": ln.LineNo, "return_id": id})
			}
			qty, err := parseQty("qty", ln.Qty)
			if err != nil {
				return err
			}
			if err := validateSerialBatch(qty, ln.Serials); err != nil {
				return err
			}
			// 批次采集（inventory-rules §6）：携带批次号时经原语建/取批次（幂等）。
			batchID := int64(0)
			if ln.BatchNo != "" {
				batchID, _, err = gw.EnsureBatch(ctx, tx, stock.BatchOp{
					SKUID: it.SKUID, BatchNo: ln.BatchNo, Actor: ia,
				})
				if err != nil {
					return err
				}
			}
			key := stock.RowKey{
				WarehouseID: o.WarehouseID, ZoneID: ln.ZoneID, ShelfID: ln.ShelfID,
				BinID: ln.BinID, SKUID: it.SKUID, BatchID: batchID,
			}
			idemKey := receiveKey(in.IdempotencyKey, o.ReturnNo, ln.LineNo, key, qty)
			res, err := gw.Putaway(ctx, tx, stock.PutawayOp{
				Key: key, Qty: qty, RequireInspect: true, // 退货收货入待检（plan §6.9）
				Source: src, Actor: ia, IdempotencyKey: idemKey, Remark: "销售退货收货（待检）",
			})
			if err != nil {
				return err
			}
			if res.Replay {
				replayLines++
				continue // 重放行：库存与累计已在此前同事务生效，不重复累计
			}
			newLines++
			for _, sn := range ln.Serials {
				if _, _, err := gw.SerialEvent(ctx, tx, stock.SerialOp{
					SerialNo: sn, SKUID: it.SKUID, BatchID: batchID,
					WarehouseID: o.WarehouseID, BinID: ln.BinID, Status: "RETURNED", // plan §6.9
					Source: src, Actor: ia, IdempotencyKey: idemKey + ":sn:" + sn,
					Remark: "销售退货收货",
				}); err != nil {
					return err
				}
			}
			n, err := s.repo.AddItemReceived(tx, it.ID.Int64(), qty, actor.UserID)
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrQtyExceeded, map[string]any{
					"line_no": ln.LineNo, "receive_qty": qty.String(),
					"qty_return": it.QtyReturn.String(), "qty_received": it.QtyReceived.String(),
					"reason": "累计收货量超出退货量",
				})
			}
		}
		switch {
		case replayLines > 0 && newLines == 0:
			// 整单幂等重放：无任何变更，返回当前状态（plan §8.5）。
			fresh, ferr := s.repo.ListReturnItemsForUpdate(tx, id)
			if ferr != nil {
				return ferr
			}
			view = newOrderView(o, fresh)
			return nil
		case replayLines > 0 && newLines > 0:
			// 部分重放=矛盾请求：真实重试必然整单重放（同键确定性），整体回滚。
			return response.NewError(ErrPartialReplay, map[string]any{
				"return_id": id, "new_lines": newLines, "replayed_lines": replayLines,
			})
		}
		if o.Status == ReturnStatusApproved {
			if err := s.guardReturnStatus(tx, o, ReturnStatusReceiving, "received_at", actor.UserID); err != nil {
				return err
			}
		}
		fresh, err := s.repo.ListReturnItemsForUpdate(tx, id)
		if err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "receive"),
			map[string]any{"lines": in.Lines, "idempotency_key": in.IdempotencyKey},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
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

// receiveKey 收货幂等键：头透传优先（事件型收货，plan §5），否则确定性拼接
// （plan §7 键构成：动作:单号:行:五维尾缀+数量——重试必然命中同键）。
func receiveKey(httpKey, returnNo string, lineNo int64, key stock.RowKey, qty stock.Qty) string {
	if httpKey != "" {
		return fmtKey(httpKey, lineNo)
	}
	return fmtKey("putaway", returnNo, lineNo, key.ZoneID, key.ShelfID, key.BinID, key.SKUID, key.BatchID, qty.String())
}

// validateSerialBatch 序列号逐件校验：采集数必须等于数量（数量须为整数件）且不重复
// （inventory-rules §8.2：启用序列号管理的 SKU 必须逐个序列号操作）。
// Qty 无导出整数判定（qty.go 值类型面冻结），经 4 位小数文本判定整件并还原件数。
func validateSerialBatch(qty stock.Qty, serials []string) error {
	if len(serials) == 0 {
		return nil
	}
	s := qty.String()
	if !strings.HasSuffix(s, ".0000") {
		return response.NewError(ErrSerialMismatch, map[string]any{
			"qty": s, "reason": "序列号逐件收货要求整件数量",
		})
	}
	units, err := strconv.ParseInt(strings.TrimSuffix(s, ".0000"), 10, 64)
	if err != nil {
		return response.NewError(ErrSerialMismatch, map[string]any{"qty": s, "reason": "件数解析失败"})
	}
	if int64(len(serials)) != units {
		return response.NewError(ErrSerialMismatch, map[string]any{
			"qty": s, "serial_count": len(serials),
			"reason": "序列号采集数必须等于收货数量",
		})
	}
	seen := map[string]bool{}
	for _, sn := range serials {
		if sn == "" {
			return paramError("serials", "序列号不能为空串")
		}
		if seen[sn] {
			return response.NewError(ErrSerialMismatch, map[string]any{
				"serial_no": sn, "reason": "序列号重复采集",
			})
		}
		seen[sn] = true
	}
	return nil
}

// SubmitSalesQC 提交质检（RECEIVING→IN_QC）：全部明细收齐后经 QCCreator 窄接口创建
// 质检单（purchase 实现，plan §3.1），返回质检单号。
func (s *Service) SubmitSalesQC(ctx context.Context, actor Actor, id int64, in SubmitQCInput) (*ReturnOrderView, string, error) {
	qc, err := s.requireQC()
	if err != nil {
		return nil, "", err
	}
	var view *ReturnOrderView
	qcNo := ""
	err = s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, items, err := s.loadOrderWithItems(tx, id)
		if err != nil {
			return err
		}
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if o.Type != ReturnTypeSales {
			return response.NewError(ErrSourceMismatch, map[string]any{"return_id": id, "reason": "仅销售退货需要质检"})
		}
		lines := make([]QCLine, 0, len(items))
		for _, it := range items {
			if it.QtyReceived.Sub(it.QtyReturn).IsNegative() {
				return response.NewError(ErrQtyExceeded, map[string]any{
					"line_no":      it.LineNo,
					"qty_received": it.QtyReceived.String(), "qty_return": it.QtyReturn.String(),
					"reason": "存在未收齐明细，不能提交质检（business-flow §9.1 收货→质检顺序）",
				})
			}
			lines = append(lines, QCLine{LineNo: it.LineNo, SKUID: it.SKUID, QtyInspected: it.QtyReceived.String()})
		}
		before := snapshotOrder(o)
		qcNo, err = qc.CreateQC(ctx, "RETURN", o.ReturnNo, "全检", o.WarehouseID, lines)
		if err != nil {
			return err
		}
		if err := s.guardReturnStatus(tx, o, ReturnStatusInQC, "qc_at", actor.UserID); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "submit-qc"),
			map[string]any{"qc_no": qcNo, "remark": in.Remark},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
			return err
		}
		view = newOrderView(o, items)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return view, qcNo, nil
}

// ApplySalesQCResult 质检结果应用（IN_QC，部分质检支持）：合格 pending_inspect→available、
// 不良 pending_inspect→defective（§9.1 质检决定去向，plan §6.9）；全部明细检完自动
// IN_QC→COMPLETED。幂等键 inspect:{qc_no}:{line_no}:{pass|defect}（plan §7）。
func (s *Service) ApplySalesQCResult(ctx context.Context, actor Actor, id int64, in QCResultInput) (*ReturnOrderView, error) {
	gw, err := s.requireStock()
	if err != nil {
		return nil, err
	}
	if len(in.Lines) == 0 {
		return nil, paramError("lines", "质检结果明细不能为空")
	}
	if in.QCNo == "" {
		return nil, paramError("qc_no", "质检单号必填（plan §7 inspect 幂等键构成）")
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
		// 数据权限 fail-closed（f16）：越仓退货单按不存在处理（与详情接口同口径）。
		if !actor.canAccessWarehouse(o.WarehouseID) {
			return response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
		}
		if o.Type != ReturnTypeSales {
			return response.NewError(ErrSourceMismatch, map[string]any{"return_id": id, "reason": "仅销售退货需要质检"})
		}
		// 幂等重放窗口：IN_QC（正常执行）与 COMPLETED（全部检完后的整单重试——
		// inspect 键命中 ledger 重放，不重复变更）。
		if o.Status != ReturnStatusInQC && o.Status != ReturnStatusCompleted {
			return response.NewError(ErrStatusConflict, map[string]any{
				"return_id": id, "return_no": o.ReturnNo, "from": o.Status,
				"reason": "仅质检中（IN_QC）可应用质检结果",
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
		before := snapshotOrder(o)
		ia := actor.inventoryActor()
		newLines, replayLines := 0, 0
		for _, ln := range in.Lines {
			it := itemByLine[ln.LineNo]
			if it == nil {
				return response.NewError(ErrLineNotFound, map[string]any{"line_no": ln.LineNo, "return_id": id})
			}
			q, err := parseQtyNonNeg("qty_qualified", ln.QtyQualified)
			if err != nil {
				return err
			}
			d, err := parseQtyNonNeg("qty_defective", ln.QtyDefective)
			if err != nil {
				return err
			}
			if !q.Add(d).IsPositive() {
				return paramError("qty_qualified", "合格量与不良量之和必须为正数")
			}
			if err := validateSerialBatch(q, ln.SerialsPassed); err != nil {
				return err
			}
			key := stock.RowKey{
				WarehouseID: o.WarehouseID, ZoneID: ln.ZoneID, ShelfID: ln.ShelfID,
				BinID: ln.BinID, SKUID: it.SKUID, BatchID: ln.BatchID,
			}
			lineReplay := 0
			if q.IsPositive() {
				res, err := gw.InspectResult(ctx, tx, stock.InspectResultOp{
					Key: key, Qty: q, Pass: true, Source: src, Actor: ia,
					IdempotencyKey: fmtKey("inspect", in.QCNo, ln.LineNo, "pass"), // plan §7
					Remark:         "退货质检合格→available",
				})
				if err != nil {
					return err
				}
				if res.Replay {
					lineReplay++
				}
			}
			if d.IsPositive() {
				res, err := gw.InspectResult(ctx, tx, stock.InspectResultOp{
					Key: key, Qty: d, Pass: false, Source: src, Actor: ia,
					IdempotencyKey: fmtKey("inspect", in.QCNo, ln.LineNo, "defect"), // plan §7
					Remark:         "退货质检不良→defective",
				})
				if err != nil {
					return err
				}
				if res.Replay {
					lineReplay++
				}
			}
			expect := 0
			if q.IsPositive() {
				expect++
			}
			if d.IsPositive() {
				expect++
			}
			switch {
			case lineReplay == 0:
				newLines++
			case lineReplay == expect:
				replayLines++
			default:
				// 同一行一份合格/不良重放一份新——矛盾请求，整体回滚。
				return response.NewError(ErrPartialReplay, map[string]any{
					"return_id": id, "line_no": ln.LineNo, "qc_no": in.QCNo,
				})
			}
			if lineReplay > 0 {
				continue // 重放行不重复累计
			}
			for _, sn := range ln.SerialsPassed {
				if _, _, err := gw.SerialEvent(ctx, tx, stock.SerialOp{
					SerialNo: sn, SKUID: it.SKUID, BatchID: ln.BatchID,
					WarehouseID: o.WarehouseID, BinID: ln.BinID, Status: "IN_STOCK",
					Source: src, Actor: ia, IdempotencyKey: fmtKey("inspect", in.QCNo, ln.LineNo, "pass", "sn", sn),
					Remark: "退货质检合格回库",
				}); err != nil {
					return err
				}
			}
			n, err := s.repo.AddItemInspected(tx, it.ID.Int64(), q, d, actor.UserID)
			if err != nil {
				return err
			}
			if n == 0 {
				return response.NewError(ErrQtyExceeded, map[string]any{
					"line_no": ln.LineNo, "qualified": q.String(), "defective": d.String(),
					"qty_received": it.QtyReceived.String(), "qty_inspected": it.QtyInspected.String(),
					"reason": "累计质检量超出收货量",
				})
			}
		}
		switch {
		case replayLines > 0 && newLines == 0:
			fresh, ferr := s.repo.ListReturnItemsForUpdate(tx, id)
			if ferr != nil {
				return ferr
			}
			view = newOrderView(o, fresh)
			return nil
		case replayLines > 0 && newLines > 0:
			return response.NewError(ErrPartialReplay, map[string]any{
				"return_id": id, "qc_no": in.QCNo, "new_lines": newLines, "replayed_lines": replayLines,
			})
		}
		// 全部检完 → COMPLETED（部分质检停留 IN_QC，business-flow §14 部分质检）。
		// COMPLETED 态进入的整单重试在上方 all-replay 分支已返回，不会到达此处。
		fresh, err := s.repo.ListReturnItemsForUpdate(tx, id)
		if err != nil {
			return err
		}
		allInspected := true
		for _, it := range fresh {
			if it.QtyInspected.Sub(it.QtyReceived).IsNegative() {
				allInspected = false
				break
			}
		}
		if allInspected {
			if err := s.guardReturnStatus(tx, o, ReturnStatusCompleted, "completed_at", actor.UserID); err != nil {
				return err
			}
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry(docTypeReturnOrder, o.ID.Int64(), "apply-qc"),
			map[string]any{"qc_no": in.QCNo, "lines": in.Lines},
			map[string]any{"before": before, "after": snapshotOrder(o)}, nil)); err != nil {
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

// ---- 查询 ----

// GetReturn 退货单详情。
func (s *Service) GetReturn(ctx context.Context, id int64) (*ReturnOrderView, error) {
	o, err := s.loadReturnOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListReturnItemsForUpdate(s.repo.DB().WithContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return newOrderView(o, items), nil
}

// ListReturns 分页列表（f.WarehouseIDs 由 handler 按数据权限组装）。
func (s *Service) ListReturns(ctx context.Context, f ReturnOrderFilter) ([]*ReturnOrderView, int64, error) {
	rows, total, err := s.repo.ListReturnOrders(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*ReturnOrderView, 0, len(rows))
	for _, o := range rows {
		views = append(views, newOrderView(o, nil))
	}
	return views, total, nil
}

// ---- 内部助手 ----

func (s *Service) loadOrderWithItems(tx *gorm.DB, id int64) (*ReturnOrder, []*ReturnItem, error) {
	o, err := s.repo.FindReturnOrderForUpdate(tx, id)
	if err != nil {
		return nil, nil, err
	}
	if o == nil {
		return nil, nil, response.NewError(ErrReturnNotFound, map[string]any{"return_id": id})
	}
	items, err := s.repo.ListReturnItemsForUpdate(tx, id)
	if err != nil {
		return nil, nil, err
	}
	return o, items, nil
}

// snapshotOrder 单据状态快照（business-flow §13.2：变化前后状态+操作者+时间入审计）。
func snapshotOrder(o *ReturnOrder) map[string]any {
	return map[string]any{
		"id": o.ID.Int64(), "return_no": o.ReturnNo, "type": o.Type,
		"source_no": o.SourceNo, "warehouse_id": o.WarehouseID, "status": o.Status,
	}
}
