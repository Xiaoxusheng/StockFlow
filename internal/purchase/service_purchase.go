package purchase

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 采购订单业务（business-flow §2、plan §6.1）：
// 状态机 DRAFT→PENDING_APPROVAL→APPROVED→(PARTIAL_RECEIVED→)RECEIVED_ALL→COMPLETED，
// 任意未收货前置态可 CANCELLED（§2.3 四量约束的收货强校验在 service_receipt.go）。

// POItemInput 采购订单明细入参。
type POItemInput struct {
	SKUID  int64     `json:"sku_id" binding:"required"`
	Qty    stock.Qty `json:"qty" binding:"required"`
	Price  stock.Qty `json:"price"`
	Remark string    `json:"remark"`
}

// POCreateInput 采购订单创建入参。
type POCreateInput struct {
	SupplierID  int64         `json:"supplier_id" binding:"required"`
	WarehouseID int64         `json:"warehouse_id" binding:"required"`
	Remark      string        `json:"remark"`
	Items       []POItemInput `json:"items" binding:"required"`
}

// POUpdateInput 采购订单修改入参（仅草稿；明细整单替换）。
type POUpdateInput struct {
	SupplierID  *int64        `json:"supplier_id"`
	WarehouseID *int64        `json:"warehouse_id"`
	Remark      *string       `json:"remark"`
	Items       []POItemInput `json:"items"`
}

// POApproveInput 审核入参（approve 权限点：通过/驳回共用资源点，操作语义由请求区分——
// plan §9.1；驳回必须附意见，business-flow §12.2 审批意见入审计记录）。
type POApproveInput struct {
	Approved bool   `json:"approved" binding:"required_without=Opinion"`
	Opinion  string `json:"opinion"`
}

// POCloseInput 差额关闭入参（上架未全部完成时原因必填——plan §6.1）。
type POCloseInput struct {
	Reason string `json:"reason"`
}

// buildPOItems 校验并构造订单明细（行号从 1 连续编号、SKU 不重复、数量>0、单价≥0、
// 金额服务端计算——api.md §4；同 SKU 多行合并为单行以支撑收货按 SKU 精确匹配）。
func (s *Service) buildPOItems(ctx context.Context, in []POItemInput, by int64) ([]*PurchaseOrderItem, stock.Qty, error) {
	if len(in) == 0 {
		return nil, 0, response.NewError(ErrPOLinesRequired, nil)
	}
	items := make([]*PurchaseOrderItem, 0, len(in))
	skuSeen := map[int64]int{}
	var total stock.Qty
	for i, li := range in {
		field := fmt.Sprintf("items[%d]", i)
		if li.SKUID <= 0 {
			return nil, 0, invalidParam(field+".sku_id", "必须为正整数")
		}
		if !li.Qty.IsPositive() {
			return nil, 0, invalidParam(field+".qty", "必须为正数")
		}
		if li.Price.IsNegative() {
			return nil, 0, invalidParam(field+".price", "不能为负数")
		}
		if prev, dup := skuSeen[li.SKUID]; dup {
			return nil, 0, response.NewError(ErrPOLineDuplicated, map[string]any{
				"sku_id": li.SKUID, "lines": []int{prev, i + 1},
			})
		}
		skuSeen[li.SKUID] = i + 1
		f, err := s.skuFlagsOf(ctx, li.SKUID)
		if err != nil {
			return nil, 0, err
		}
		if !f.Enabled {
			return nil, 0, invalidParam(field+".sku_id", fmt.Sprintf("SKU %d 已停用", li.SKUID))
		}
		amount, ok := mulAmount(li.Qty, li.Price)
		if !ok {
			return nil, 0, invalidParam(field+".price", "数量×单价超出金额列可表示范围")
		}
		total = total.Add(amount)
		items = append(items, &PurchaseOrderItem{
			LineNo:     len(items) + 1,
			SKUID:      li.SKUID,
			QtyOrdered: li.Qty,
			Price:      li.Price,
			Amount:     amount,
			Remark:     strings.TrimSpace(li.Remark),
		})
	}
	return items, total, nil
}

// CreatePO 创建采购订单（草稿）。校验供应商/收货仓存在且启用（api.md §4 业务关系）。
func (s *Service) CreatePO(ctx context.Context, actor Actor, in POCreateInput) (*PurchaseOrder, error) {
	if err := s.requireChecker(); err != nil {
		return nil, err
	}
	ok, err := s.opt.suppliers.ExistsActive(ctx, in.SupplierID)
	if err != nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{"reason": "供应商校验失败", "error": err.Error()})
	}
	if !ok {
		return nil, invalidParam("supplier_id", fmt.Sprintf("供应商 %d 不存在或已停用", in.SupplierID))
	}
	ok, err = s.opt.warehouses.ExistsActive(ctx, in.WarehouseID)
	if err != nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{"reason": "仓库校验失败", "error": err.Error()})
	}
	if !ok {
		return nil, invalidParam("warehouse_id", fmt.Sprintf("仓库 %d 不存在或已停用", in.WarehouseID))
	}
	items, total, err := s.buildPOItems(ctx, in.Items, actor.UserID)
	if err != nil {
		return nil, err
	}
	po := &PurchaseOrder{
		SupplierID:  in.SupplierID,
		WarehouseID: in.WarehouseID,
		TotalAmount: total,
		Status:      POStatusDraft,
		Remark:      strings.TrimSpace(in.Remark),
		CreatedBy:   database.ID(actor.UserID),
		UpdatedBy:   database.ID(actor.UserID),
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		no, err := docnum.NextWithRetry(ctx, tx, docRule("PO"), func(tx *gorm.DB, no string) error {
			po.PONo = no
			return s.repo.InsertPO(ctx, tx, po)
		})
		if err != nil {
			return err
		}
		po.PONo = no
		if err := s.repo.ReplacePOItems(ctx, tx, po.ID.Int64(), items, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("purchase_order", po.ID.Int64(), "create")
		e.After = po
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return po, nil
}

// UpdatePO 修改采购订单（仅草稿；明细整单替换，business-flow §13.2 状态守卫）。
func (s *Service) UpdatePO(ctx context.Context, actor Actor, id int64, in POUpdateInput) (*PurchaseOrder, error) {
	if err := s.requireChecker(); err != nil {
		return nil, err
	}
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if po == nil {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	if po.Status != POStatusDraft {
		return nil, response.NewError(ErrPOStatusNotAllowed, map[string]any{"status": po.Status, "reason": "仅草稿可修改"})
	}
	var items []*PurchaseOrderItem
	if in.Items != nil {
		items, _, err = s.buildPOItems(ctx, in.Items, actor.UserID)
		if err != nil {
			return nil, err
		}
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		cols := map[string]any{"updated_at": database.Now(), "updated_by": actor.UserID}
		if in.SupplierID != nil {
			if *in.SupplierID <= 0 {
				return invalidParam("supplier_id", "必须为正整数")
			}
			ok, err := s.opt.suppliers.ExistsActive(ctx, *in.SupplierID)
			if err != nil {
				return response.NewError(response.CodeInternalError, map[string]any{"reason": "供应商校验失败", "error": err.Error()})
			}
			if !ok {
				return invalidParam("supplier_id", fmt.Sprintf("供应商 %d 不存在或已停用", *in.SupplierID))
			}
			cols["supplier_id"] = *in.SupplierID
		}
		if in.WarehouseID != nil {
			if *in.WarehouseID <= 0 {
				return invalidParam("warehouse_id", "必须为正整数")
			}
			ok, err := s.opt.warehouses.ExistsActive(ctx, *in.WarehouseID)
			if err != nil {
				return response.NewError(response.CodeInternalError, map[string]any{"reason": "仓库校验失败", "error": err.Error()})
			}
			if !ok {
				return invalidParam("warehouse_id", fmt.Sprintf("仓库 %d 不存在或已停用", *in.WarehouseID))
			}
			cols["warehouse_id"] = *in.WarehouseID
		}
		if in.Remark != nil {
			cols["remark"] = strings.TrimSpace(*in.Remark)
		}
		before := *po
		if err := s.repo.UpdatePOCols(ctx, tx, id, cols); err != nil {
			return err
		}
		if items != nil {
			if err := s.repo.ReplacePOItems(ctx, tx, id, items, actor.UserID); err != nil {
				return err
			}
		}
		fresh, err := s.repo.FindPOByID(ctx, id)
		if err != nil || fresh == nil {
			return err
		}
		e := actor.auditEntry("purchase_order", id, "update")
		e.Before, e.After = &before, fresh
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindPOByID(ctx, id)
}

// SubmitPO 提交审核（DRAFT→PENDING_APPROVAL）：明细 SKU 启用复检 + SUBMIT 审批记录
// （plan §6.1 事务内容）。
func (s *Service) SubmitPO(ctx context.Context, actor Actor, id int64) (*PurchaseOrder, error) {
	if err := s.requireChecker(); err != nil {
		return nil, err
	}
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if po == nil {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	if !canTransition(purchaseTransitions, po.Status, POStatusPendingApproval) {
		return nil, response.NewError(ErrPOStatusNotAllowed, map[string]any{
			"status": po.Status, "to": POStatusPendingApproval,
		})
	}
	items, err := s.repo.ListPOItems(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, response.NewError(ErrPOLinesRequired, nil)
	}
	for _, it := range items {
		f, err := s.skuFlagsOf(ctx, it.SKUID)
		if err != nil {
			return nil, err
		}
		if !f.Enabled {
			return nil, invalidParam("items", fmt.Sprintf("明细行 %d：SKU %d 已停用，不能提交", it.LineNo, it.SKUID))
		}
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdatePOStatus(ctx, tx, id, po.Status, POStatusPendingApproval, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		if err := s.insertApproval(ctx, tx, "purchase_order", po.PONo, ApprovalActionSubmit, "", "", actor); err != nil {
			return err
		}
		e := actor.auditEntry("purchase_order", id, "submit")
		e.Before = map[string]any{"status": po.Status}
		e.After = map[string]any{"status": POStatusPendingApproval}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindPOByID(ctx, id)
}

// ApprovePO 审核（PENDING_APPROVAL→APPROVED 或驳回回 DRAFT）：APPROVE/REJECT 审批记录
// （business-flow §12.2：审批人/时间/意见/结果落 document_approvals，不可修改删除）。
func (s *Service) ApprovePO(ctx context.Context, actor Actor, id int64, in POApproveInput) (*PurchaseOrder, error) {
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if po == nil {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	if po.Status != POStatusPendingApproval {
		return nil, response.NewError(ErrPOStatusNotAllowed, map[string]any{"status": po.Status})
	}
	to, action, result := POStatusApproved, ApprovalActionApprove, "APPROVED"
	if !in.Approved {
		to, action, result = POStatusDraft, ApprovalActionReject, "REJECTED"
		if strings.TrimSpace(in.Opinion) == "" {
			return nil, invalidParam("opinion", "驳回必须填写审批意见")
		}
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdatePOStatus(ctx, tx, id, po.Status, to, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		if err := s.insertApproval(ctx, tx, "purchase_order", po.PONo, action, result, strings.TrimSpace(in.Opinion), actor); err != nil {
			return err
		}
		e := actor.auditEntry("purchase_order", id, action)
		e.Before = map[string]any{"status": po.Status}
		e.After = map[string]any{"status": to, "opinion": strings.TrimSpace(in.Opinion)}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindPOByID(ctx, id)
}

// CancelPO 取消（DRAFT/PENDING_APPROVAL/APPROVED→CANCELLED）：无任何收货方可取消
// （plan §6.1——有收货即拒绝，只能走关闭/反向冲正，business-flow §13.3）。
// 事务外校验仅作快速失败；事务内持 po 行锁重读复查 qty_received（修复轮：与
// ConfirmReceipt 并发时，重读在锁内看到对方已提交的收货即拒绝——收货侧则在
// confirmReceiptTx 锁内校验状态，两方向均不产生"已取消单据继续进库存"）。
func (s *Service) CancelPO(ctx context.Context, actor Actor, id int64) (*PurchaseOrder, error) {
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if po == nil {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	if !canTransition(purchaseTransitions, po.Status, POStatusCancelled) {
		return nil, response.NewError(ErrPOStatusNotAllowed, map[string]any{
			"status": po.Status, "to": POStatusCancelled,
		})
	}
	items, err := s.repo.ListPOItems(ctx, id)
	if err != nil {
		return nil, err
	}
	var received stock.Qty
	for _, it := range items {
		received = received.Add(it.QtyReceived)
	}
	if received.IsPositive() {
		return nil, response.NewError(ErrPOHasReceipts, map[string]any{"qty_received": received.String()})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		// 行锁重读（SELECT ... FOR UPDATE）：与 ConfirmReceipt 的 po 行锁互斥，
		// 锁内复查收货量与状态——并发收货已提交则此处必然拒绝。
		fresh, err := s.repo.FindPOByIDForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if fresh == nil {
			return response.NewError(ErrPONotFound, nil)
		}
		freshItems, err := s.repo.ListPOItems(ctx, id)
		if err != nil {
			return err
		}
		var freshReceived stock.Qty
		for _, it := range freshItems {
			freshReceived = freshReceived.Add(it.QtyReceived)
		}
		if freshReceived.IsPositive() {
			return response.NewError(ErrPOHasReceipts, map[string]any{"qty_received": freshReceived.String()})
		}
		n, err := s.repo.UpdatePOStatus(ctx, tx, id, fresh.Status, POStatusCancelled, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		if err := s.insertApproval(ctx, tx, "purchase_order", po.PONo, ApprovalActionCancel, "CANCELLED", "", actor); err != nil {
			return err
		}
		e := actor.auditEntry("purchase_order", id, "cancel")
		e.Before = map[string]any{"status": po.Status}
		e.After = map[string]any{"status": POStatusCancelled}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindPOByID(ctx, id)
}

// ClosePO 关闭/完成（RECEIVED_ALL|PARTIAL_RECEIVED→COMPLETED）：
// 上架任务全部完成（明细 qty_putaway ≥ qty_received）或差额关闭填原因（plan §6.1）。
func (s *Service) ClosePO(ctx context.Context, actor Actor, id int64, in POCloseInput) (*PurchaseOrder, error) {
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if po == nil {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	if !canTransition(purchaseTransitions, po.Status, POStatusCompleted) {
		return nil, response.NewError(ErrPOStatusNotAllowed, map[string]any{
			"status": po.Status, "to": POStatusCompleted,
		})
	}
	items, err := s.repo.ListPOItems(ctx, id)
	if err != nil {
		return nil, err
	}
	shortage := false
	for _, it := range items {
		if it.QtyPutaway.Sub(it.QtyReceived).IsNegative() {
			shortage = true
			break
		}
	}
	reason := strings.TrimSpace(in.Reason)
	if shortage && reason == "" {
		return nil, invalidParam("reason", "存在未上架完成的数量，差额关闭必须填写原因")
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdatePOStatus(ctx, tx, id, po.Status, POStatusCompleted, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		e := actor.auditEntry("purchase_order", id, "close")
		e.Before = map[string]any{"status": po.Status}
		e.After = map[string]any{"status": POStatusCompleted, "shortage_close": shortage, "reason": reason}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindPOByID(ctx, id)
}

// GetPO 详情（含明细）。
type PODetail struct {
	Order *PurchaseOrder       `json:"order"`
	Items []*PurchaseOrderItem `json:"items"`
}

func (s *Service) GetPO(ctx context.Context, id int64, scope WarehouseScope) (*PODetail, error) {
	po, err := s.repo.FindPOByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：仓库不在范围内按不存在处理，
	// 与 sales/stockops/returns 详情接口同口径（plan §10.5）。
	if po == nil || !scope.visible(po.WarehouseID) {
		return nil, response.NewError(ErrPONotFound, nil)
	}
	items, err := s.repo.ListPOItems(ctx, id)
	if err != nil {
		return nil, err
	}
	return &PODetail{Order: po, Items: items}, nil
}

// ListPO 列表（分页强制 + 仓库数据权限过滤，permission.md §4）。
func (s *Service) ListPO(ctx context.Context, f POListFilter) ([]*PurchaseOrder, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	return s.repo.ListPOs(ctx, f)
}

// insertApproval 写审批记录（business-flow §12.2；append-only，与业务同事务）。
func (s *Service) insertApproval(ctx context.Context, tx *gorm.DB, targetType, targetNo, action, result, opinion string, actor Actor) error {
	return s.repo.InsertApproval(ctx, tx, &DocumentApproval{
		TargetType:   targetType,
		TargetNo:     targetNo,
		Action:       action,
		Result:       result,
		Opinion:      opinion,
		OperatorID:   actor.UserID,
		OperatorName: actor.Username,
		RequestID:    actor.RequestID,
	})
}

// middlewareAudit 审计写入薄封装（同事务；审计失败即业务失败——architecture.md §4）。
func middlewareAudit(tx *gorm.DB, e middleware.AuditEntry) error {
	return middleware.Audit(tx, e)
}
