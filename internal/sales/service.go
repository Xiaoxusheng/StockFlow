package sales

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
)

// Service 销售出库域业务层（architecture.md §1）：状态机守卫、事务编排、库存原语组合
// 与跨域校验的唯一入口。事务边界在本层（plan §2.3 判据 6）：每个业务动作 = 单外层事务
// （Repo.Tx）——单据状态迁移（守卫 UPDATE）→ 库存原语（StockGateway，传 tx）→
// document_approvals/异常联动 → middleware.Audit → COMMIT，任一步失败整体回滚。
type Service struct {
	repo Repo

	stock      StockGateway
	skus       SKUAttrReader
	customers  CustomerChecker
	bins       BinChecker
	exceptions ExceptionCreator

	audit  AuditFunc
	nextNo NumberIssuer
}

// NewService 构造销售域 Service。缺省绑定：审计 = middleware.Audit（业务事务内写
// operation_logs，plan §2.3 判据 6）；单号 = internal/docnum（business-flow §13.1，
// plan §2.3 判据 5 禁止绕过）。跨域必需依赖（库存网关/SKU 开关/客户/库位校验）由
// router 经 Option 注入，RegisterRoutes 对缺位启动期 fail-fast（plan §3.1 规则①）；
// Service 方法内再做 nil 防线（fail-closed，杜绝静默跳过校验）。
func NewService(repo Repo, opts ...Option) *Service {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	svc := &Service{
		repo:       repo,
		stock:      o.stock,
		skus:       o.skus,
		customers:  o.customers,
		bins:       o.bins,
		exceptions: o.exceptions,
		audit:      o.audit,
		nextNo:     o.nextNo,
	}
	if svc.audit == nil {
		svc.audit = middleware.Audit
	}
	if svc.nextNo == nil {
		svc.nextNo = docnumIssuer
	}
	return svc
}

// docnumIssuer 生产单号发放（internal/docnum 冻结注册表；发放与单据创建同事务）。
func docnumIssuer(ctx context.Context, tx *gorm.DB, prefix string) (string, error) {
	rule, ok := docnum.RuleFor(prefix)
	if !ok {
		return "", response.NewError(response.CodeInternalError, map[string]any{
			"reason": "未知单号前缀（编程错误，注册表见 internal/docnum）", "prefix": prefix,
		})
	}
	return docnum.Next(ctx, tx, rule)
}

// ---- fail-closed 依赖防线 ----

func (s *Service) depsReady() error {
	if s.stock == nil || s.skus == nil || s.customers == nil || s.bins == nil {
		return response.NewError(ErrCheckerMissing, nil)
	}
	return nil
}

func (s *Service) requireExceptions() error {
	if s.exceptions == nil {
		return response.NewError(ErrExceptionsNotWired, map[string]any{
			"reason": "异常中心创建接口未注入（router 装配 WithExceptions，plan §3.1）",
		})
	}
	return nil
}

// auditEntry 业务审计条目（含 before/after 状态快照，business-flow §13.2）。
func (s *Service) auditEntry(tx *gorm.DB, module, objectType, action string, objectID int64,
	actor Actor, request any, before, after any) error {
	return s.audit(tx, middleware.AuditEntry{
		Module: module, ObjectType: objectType, ObjectID: objectID, Action: action,
		OperatorID: actor.ID, OperatorName: actor.Name,
		RequestID: actor.RequestID, IP: actor.IP, UserAgent: actor.UserAgent,
		Method: actor.Method, Path: actor.Path,
		Success: true, Request: request, Before: before, After: after,
	})
}

// ---- 入参与结果 ----

// OrderItemInput 销售订单明细入参。
type OrderItemInput struct {
	LineNo int64  `json:"line_no"`
	SKUID  int64  `json:"sku_id"`
	Qty    Qty    `json:"qty"`
	Price  Qty    `json:"price"`
	Remark string `json:"remark"`
}

// CreateOrderInput 销售订单创建/编辑入参（business-flow §6.1 字段清单）。
type CreateOrderInput struct {
	CustomerID      int64            `json:"customer_id"`
	WarehouseID     int64            `json:"warehouse_id"`
	ShippingAddress string           `json:"shipping_address"`
	DeliveryMethod  string           `json:"delivery_method"`
	Remark          string           `json:"remark"`
	Items           []OrderItemInput `json:"items"`
}

// CancelInput 取消入参（原因留痕审计与审批记录）。
type CancelInput struct {
	Reason string `json:"reason"`
}

// CloseInput 差额关闭入参（原因必填，business-flow §13.3）。
type CloseInput struct {
	Reason string `json:"reason"`
}

// ApproveInput 审核入参（通过/驳回共用资源点，操作语义由 Action 区分——plan §9.1）。
type ApproveInput struct {
	Action  string `json:"action"` // APPROVE / REJECT
	Opinion string `json:"opinion"`
}

// errInvalidParam 入参错误快捷构造。
func errInvalidParam(field, reason string) error {
	return response.NewError(response.CodeInvalidParam, map[string]any{"field": field, "reason": reason})
}

// validateItems 明细业务校验（api.md §4 后端完整校验）：数量为正、单价非负、
// SKU 必填、行号重复检测；行号缺省按顺序补齐。返回规范化明细与明细总额。
func validateItems(in []OrderItemInput) ([]SalesOrderItem, Qty, error) {
	if len(in) == 0 {
		return nil, 0, errInvalidParam("items", "至少一行明细")
	}
	seen := map[int64]bool{}
	items := make([]SalesOrderItem, 0, len(in))
	var total Qty
	for i, it := range in {
		line := it.LineNo
		if line == 0 {
			line = int64(i + 1)
		}
		if seen[line] {
			return nil, 0, errInvalidParam("items", "行号重复")
		}
		seen[line] = true
		if it.SKUID <= 0 {
			return nil, 0, errInvalidParam("items", "sku_id 必须为正整数")
		}
		if !it.Qty.IsPositive() {
			return nil, 0, errInvalidParam("items", "数量必须为正数")
		}
		if it.Price.IsNegative() {
			return nil, 0, errInvalidParam("items", "单价不能为负数")
		}
		// 金额 = 数量 × 单价（Qty 标度 1e-4：乘积除回标度，全整数运算无浮点误差）。
		amount := Qty(int64(it.Qty) * int64(it.Price) / qtyScale)
		total = total.Add(amount)
		items = append(items, SalesOrderItem{
			LineNo: line, SKUID: it.SKUID, Qty: it.Qty, Price: it.Price,
			Amount: amount, Remark: it.Remark,
		})
	}
	return items, total, nil
}

// checkSKUEnabled SKU 启用校验（SKUAttrReader 缺位 fail-closed）。
func (s *Service) checkSKUEnabled(ctx context.Context, skuID int64) (SKUFlags, error) {
	if s.skus == nil {
		return SKUFlags{}, response.NewError(ErrCheckerMissing, nil)
	}
	flags, err := s.skus.GetFlags(ctx, skuID)
	if err != nil {
		return SKUFlags{}, response.NewError(response.CodeInternalError, map[string]any{
			"reason": "SKU 开关读取失败", "sku_id": skuID, "error": err.Error(),
		})
	}
	if !flags.Enabled {
		return flags, response.NewError(ErrSKUNotFound, map[string]any{"sku_id": skuID})
	}
	return flags, nil
}

// ---- 销售订单：创建（DRAFT） ----

// CreateSalesOrder 创建草稿销售订单（business-flow §6.1/§6.2 流程起点）。
func (s *Service) CreateSalesOrder(ctx context.Context, actor Actor, in CreateOrderInput) (*SalesOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	if in.CustomerID <= 0 {
		return nil, errInvalidParam("customer_id", "必须为正整数")
	}
	if in.WarehouseID <= 0 {
		return nil, errInvalidParam("warehouse_id", "必须为正整数")
	}
	items, total, err := validateItems(in.Items)
	if err != nil {
		return nil, err
	}
	ok, err := s.customers.ExistsActive(ctx, in.CustomerID)
	if err != nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{"reason": "客户校验失败", "error": err.Error()})
	}
	if !ok {
		return nil, response.NewError(ErrCustomerNotFound, map[string]any{"customer_id": in.CustomerID})
	}
	for _, it := range items {
		if _, err := s.checkSKUEnabled(ctx, it.SKUID); err != nil {
			return nil, err
		}
	}

	var out *SalesOrder
	err = s.repo.Tx(ctx, func(tx *gorm.DB) error {
		soNo, err := s.nextNo(ctx, tx, "SO")
		if err != nil {
			return err
		}
		o := &SalesOrder{
			SoNo: soNo, CustomerID: in.CustomerID, WarehouseID: in.WarehouseID,
			ShippingAddress: in.ShippingAddress, DeliveryMethod: in.DeliveryMethod,
			TotalAmount: total, Status: SOStatusDraft, Remark: in.Remark,
			CreatedBy: actor.ID, UpdatedBy: actor.ID,
		}
		if err := s.repo.InsertSalesOrder(tx, o, items); err != nil {
			if pgUniqueViolation(err, "uk_sales_orders_no") {
				return response.NewError(response.CodeConflict, map[string]any{"reason": "单号冲突，请重试"})
			}
			return err
		}
		out = o
		return s.auditEntry(tx, "sales", "sales_order", "create", o.ID.Int64(), actor, in, nil, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSalesOrderDraft 草稿编辑（仅 DRAFT；提交后明细不可再改——business-flow §6.2）。
func (s *Service) UpdateSalesOrderDraft(ctx context.Context, actor Actor, id int64, in CreateOrderInput) (*SalesOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	items, total, err := validateItems(in.Items)
	if err != nil {
		return nil, err
	}
	ok, err := s.customers.ExistsActive(ctx, in.CustomerID)
	if err != nil {
		return nil, response.NewError(response.CodeInternalError, map[string]any{"reason": "客户校验失败", "error": err.Error()})
	}
	if !ok {
		return nil, response.NewError(ErrCustomerNotFound, map[string]any{"customer_id": in.CustomerID})
	}
	for _, it := range items {
		if _, err := s.checkSKUEnabled(ctx, it.SKUID); err != nil {
			return nil, err
		}
	}
	var out *SalesOrder
	err = s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOrderNotFound, map[string]any{"id": id})
		}
		before := *o
		o.CustomerID, o.WarehouseID = in.CustomerID, in.WarehouseID
		o.ShippingAddress, o.DeliveryMethod = in.ShippingAddress, in.DeliveryMethod
		o.Remark, o.TotalAmount, o.UpdatedBy = in.Remark, total, actor.ID
		if err := s.repo.UpdateSalesOrderDraft(tx, o, items); err != nil {
			return err
		}
		out = o
		return s.auditEntry(tx, "sales", "sales_order", "update", id, actor, in, before, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 销售订单：查询 ----

// GetSalesOrderDetail 订单详情（行级数据权限：Scope.visible）。
func (s *Service) GetSalesOrderDetail(ctx context.Context, id int64, scope Scope) (*SalesOrder, []SalesOrderItem, error) {
	var (
		o     *SalesOrder
		items []SalesOrderItem
		err   error
	)
	err = s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err = s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil || o == nil {
			return err
		}
		items, err = s.repo.ListSalesOrderItems(tx, id)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if o == nil || !scope.visible(o.WarehouseID) {
		return nil, nil, response.NewError(ErrOrderNotFound, map[string]any{"id": id})
	}
	return o, items, nil
}

// ListSalesOrders 订单列表（强制分页 + 仓库数据权限过滤）。
func (s *Service) ListSalesOrders(ctx context.Context, q SalesOrderQuery) ([]SalesOrder, int64, error) {
	return s.repo.ListSalesOrders(ctx, q)
}

// ---- 销售订单：提交（DRAFT→PENDING_APPROVAL） ----

// SubmitSalesOrder 提交审核：客户启用、SKU 启用、数量/价格复验（plan §6.4 守卫列）。
func (s *Service) SubmitSalesOrder(ctx context.Context, actor Actor, id int64) (*SalesOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	var out *SalesOrder
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOrderNotFound, map[string]any{"id": id})
		}
		before := *o
		items, err := s.repo.ListSalesOrderItems(tx, id)
		if err != nil {
			return err
		}
		ok, err := s.customers.ExistsActive(ctx, o.CustomerID)
		if err != nil {
			return response.NewError(response.CodeInternalError, map[string]any{"reason": "客户校验失败", "error": err.Error()})
		}
		if !ok {
			return response.NewError(ErrCustomerNotFound, map[string]any{"customer_id": o.CustomerID})
		}
		for _, it := range items {
			if _, err := s.checkSKUEnabled(ctx, it.SKUID); err != nil {
				return err
			}
		}
		n, err := s.repo.MarkSalesOrderStatus(tx, id, SOStatusDraft, SOStatusPendingApproval, StatusStamp{By: actor.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"so_no": o.SoNo, "from": before.Status, "to": SOStatusPendingApproval})
		}
		if err := s.repo.InsertApproval(tx, "sales_order", o.SoNo, "SUBMIT", "", "", actor); err != nil {
			return err
		}
		o.Status = SOStatusPendingApproval
		out = o
		return s.auditEntry(tx, "sales", "sales_order", "submit", id, actor, nil, before, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 销售订单：审核（PENDING_APPROVAL→APPROVED，审核即预占） ----

// ApproveResult 审核结果（预占/建单联动产物回执）。
type ApproveResult struct {
	Order      *SalesOrder `json:"order"`
	OutboundNo string      `json:"outbound_no"`
	LockCount  int         `json:"lock_count"` // 预占锁行数（= 分配记录数）
	LockedQty  Qty         `json:"locked_qty"`
}

// ApproveSalesOrder 审核（plan §6.4"审核即预占"）：
// 默认策略分配（批次 SKU 走 inventory.AllocateFEFO/FIFO 纯函数，逐 bin 选行）→
// 逐行 Lock(ORDER_HOLD, source=sales_order:{so_no}) → allocation_records 落分配理由 →
// 联动创建出库单并推进至 ALLOCATED → 订单 APPROVED + 审批记录 + 审计，同一事务。
// 任一行可用不足 → 整体回滚，订单停留 PENDING_APPROVAL，返回 INVENTORY_NOT_ENOUGH
// （business-flow §6.2"预占失败则订单无法进入出库"）。
func (s *Service) ApproveSalesOrder(ctx context.Context, actor Actor, id int64, in ApproveInput) (*ApproveResult, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	action := strings.ToUpper(strings.TrimSpace(in.Action))
	if action != "APPROVE" && action != "REJECT" {
		return nil, errInvalidParam("action", "必须为 APPROVE 或 REJECT")
	}
	var res *ApproveResult
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOrderNotFound, map[string]any{"id": id})
		}
		if action == "REJECT" {
			r, err := s.rejectOrder(tx, actor, o, in.Opinion)
			if err != nil {
				return err
			}
			res = &ApproveResult{Order: r}
			return nil
		}
		r, err := s.approveOrder(ctx, tx, actor, o)
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// approveOrder 审核通过主体（调用方已持订单行锁）。
func (s *Service) approveOrder(ctx context.Context, tx *gorm.DB, actor Actor, o *SalesOrder) (*ApproveResult, error) {
	before := *o
	if o.Status != SOStatusPendingApproval {
		return nil, response.NewError(ErrStateConflict, map[string]any{
			"so_no": o.SoNo, "from": o.Status, "to": SOStatusApproved,
		})
	}
	items, err := s.repo.ListSalesOrderItems(tx, o.ID.Int64())
	if err != nil {
		return nil, err
	}
	// 1. 联动创建出库单（PENDING_ALLOCATE，审核事务内完成分配后推进 ALLOCATED——plan §6.5 首行）。
	outboundNo, err := s.nextNo(ctx, tx, "OUT")
	if err != nil {
		return nil, err
	}
	ob := &OutboundOrder{
		OutboundNo: outboundNo, SoNo: o.SoNo, Type: "销售出库",
		WarehouseID: o.WarehouseID, Status: OBStatusPendingAllocate,
		CreatedBy: actor.ID, UpdatedBy: actor.ID,
	}
	obItems := make([]OutboundItem, 0, len(items))
	for _, it := range items {
		obItems = append(obItems, OutboundItem{
			LineNo: it.LineNo, SKUID: it.SKUID, Qty: it.Qty,
			Remark: it.Remark, CreatedBy: actor.ID, UpdatedBy: actor.ID,
		})
	}
	if err := s.repo.InsertOutboundOrder(tx, ob, obItems); err != nil {
		if pgUniqueViolation(err, "uk_outbound_orders_no") {
			return nil, response.NewError(response.CodeConflict, map[string]any{"reason": "单号冲突，请重试"})
		}
		return nil, err
	}

	// 2. 逐行默认策略分配 + 预占（锁定量 = 分配量；任一行失败整体回滚）。
	res := &ApproveResult{Order: o, OutboundNo: outboundNo}
	var lockedTotal Qty
	for _, it := range items {
		flags, err := s.checkSKUEnabled(ctx, it.SKUID)
		if err != nil {
			return nil, err
		}
		ar, err := s.allocateLine(ctx, tx, actor, o.WarehouseID, o.SoNo, outboundNo, it, flags)
		if err != nil {
			return nil, err
		}
		if err := s.repo.InsertAllocations(tx, ar.Records); err != nil {
			return nil, err
		}
		var lineAlloc Qty
		for _, rec := range ar.Records {
			lineAlloc = lineAlloc.Add(rec.Qty)
			if rec.LockID > 0 {
				res.LockCount++
			}
		}
		lockedTotal = lockedTotal.Add(lineAlloc)
		if err := s.repo.UpdateSalesOrderItemProgress(tx, o.ID.Int64(), it.LineNo, &lineAlloc, nil); err != nil {
			return nil, err
		}
	}
	res.LockedQty = lockedTotal

	// 3. 出库单 PENDING_ALLOCATE→ALLOCATED（同事务，plan §6.5）。
	if n, err := s.repo.MarkOutboundStatus(tx, ob.ID.Int64(), OBStatusPendingAllocate, OBStatusAllocated, StatusStamp{By: actor.ID}); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, response.NewError(ErrStateConflict, map[string]any{"outbound_no": outboundNo, "from": OBStatusPendingAllocate, "to": OBStatusAllocated})
	}

	// 4. 订单 PENDING_APPROVAL→APPROVED + 审批记录 + 审计（business-flow §12.2/§13.2）。
	if n, err := s.repo.MarkSalesOrderStatus(tx, o.ID.Int64(), SOStatusPendingApproval, SOStatusApproved, StatusStamp{By: actor.ID, Approved: true}); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, response.NewError(ErrStateConflict, map[string]any{"so_no": o.SoNo, "from": before.Status, "to": SOStatusApproved})
	}
	if err := s.repo.InsertApproval(tx, "sales_order", o.SoNo, "APPROVE", "APPROVED", "", actor); err != nil {
		return nil, err
	}
	o.Status = SOStatusApproved
	if err := s.auditEntry(tx, "sales", "sales_order", "approve", o.ID.Int64(), actor,
		map[string]any{"outbound_no": outboundNo, "locked_qty": lockedTotal.String()}, before, o); err != nil {
		return nil, err
	}
	if err := s.auditEntry(tx, "sales", "outbound_order", "create", ob.ID.Int64(), actor,
		map[string]any{"so_no": o.SoNo}, nil, ob); err != nil {
		return nil, err
	}
	return res, nil
}

// rejectOrder 驳回（PENDING_APPROVAL→REJECTED + 审批记录）。
func (s *Service) rejectOrder(tx *gorm.DB, actor Actor, o *SalesOrder, opinion string) (*SalesOrder, error) {
	before := *o
	n, err := s.repo.MarkSalesOrderStatus(tx, o.ID.Int64(), SOStatusPendingApproval, SOStatusRejected, StatusStamp{By: actor.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, response.NewError(ErrStateConflict, map[string]any{"so_no": o.SoNo, "from": before.Status, "to": SOStatusRejected})
	}
	if err := s.repo.InsertApproval(tx, "sales_order", o.SoNo, "REJECT", "REJECTED", opinion, actor); err != nil {
		return nil, err
	}
	o.Status = SOStatusRejected
	if err := s.auditEntry(tx, "sales", "sales_order", "reject", o.ID.Int64(), actor,
		map[string]any{"opinion": opinion}, before, o); err != nil {
		return nil, err
	}
	return o, nil
}

// ---- 销售订单：取消（释放全部 ORDER_HOLD，inventory-rules §4.2） ----

// CancelSalesOrder 取消销售订单：
//   - DRAFT/PENDING_APPROVAL：无预占，直接取消；
//   - APPROVED：未发货（qty_shipped=0）时取消——ReleaseLock 释放全部 ORDER_HOLD +
//     出库单联动 CANCELLED + 拣货任务取消，同事务（plan §6.4；任一锁已核销即拒绝，
//     走差额关闭）。
func (s *Service) CancelSalesOrder(ctx context.Context, actor Actor, id int64, in CancelInput) (*SalesOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	var out *SalesOrder
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOrderNotFound, map[string]any{"id": id})
		}
		before := *o
		switch o.Status {
		case SOStatusDraft, SOStatusPendingApproval:
			// 无库存动作。
		case SOStatusApproved:
			obs, err := s.repo.ListOutboundOrdersBySO(tx, o.SoNo)
			if err != nil {
				return err
			}
			for _, ob := range obs {
				if err := s.cancelOutboundInTx(ctx, tx, actor, &ob, "sales_order:"+o.SoNo); err != nil {
					return err
				}
			}
			// 明细预占清零（锁已全部释放，预占进度归零）。
			items, err := s.repo.ListSalesOrderItems(tx, o.ID.Int64())
			if err != nil {
				return err
			}
			zero := Qty(0)
			for _, it := range items {
				if err := s.repo.UpdateSalesOrderItemProgress(tx, o.ID.Int64(), it.LineNo, &zero, nil); err != nil {
					return err
				}
			}
		default:
			return response.NewError(ErrStateConflict, map[string]any{
				"so_no": o.SoNo, "from": o.Status, "to": SOStatusCancelled,
				"reason": "已发货/已完结订单不可取消，走差额关闭（business-flow §13.3）",
			})
		}
		n, err := s.repo.MarkSalesOrderStatus(tx, o.ID.Int64(), before.Status, SOStatusCancelled, StatusStamp{By: actor.ID, Cancelled: true})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"so_no": o.SoNo, "from": before.Status, "to": SOStatusCancelled})
		}
		if err := s.repo.InsertApproval(tx, "sales_order", o.SoNo, "CANCEL", "CANCELLED", in.Reason, actor); err != nil {
			return err
		}
		o.Status = SOStatusCancelled
		out = o
		return s.auditEntry(tx, "sales", "sales_order", "cancel", o.ID.Int64(), actor, in, before, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 销售订单：差额关闭（PARTIAL_SHIPPED→COMPLETED） ----

// CloseSalesOrder 差额关闭：未发货行放弃，剩余预占锁全部释放；每个出库单按其状态
// 联动 CLOSED（已部分发货）或 CANCELLED（未发货），同事务（business-flow §13.3）。
func (s *Service) CloseSalesOrder(ctx context.Context, actor Actor, id int64, in CloseInput) (*SalesOrder, error) {
	if err := s.depsReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, response.NewError(ErrCloseReasonRequired, nil)
	}
	var out *SalesOrder
	err := s.repo.Tx(ctx, func(tx *gorm.DB) error {
		o, err := s.repo.GetSalesOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return response.NewError(ErrOrderNotFound, map[string]any{"id": id})
		}
		// 状态机守卫（business-flow §13.2）：soTransitions 仅允许 PARTIAL_SHIPPED→
		// COMPLETED（差额关闭）——DRAFT/PENDING_APPROVAL/APPROVED 订单禁止经关闭接口
		// 一步跳到 COMPLETED（M2 修复轮补齐，与 ClosePO/CancelPO 的 canTransition 对齐）。
		if !canTransition(soTransitions, o.Status, SOStatusCompleted) {
			return response.NewError(ErrStateConflict, map[string]any{
				"so_no": o.SoNo, "from": o.Status, "to": SOStatusCompleted,
				"reason": "仅部分发货（PARTIAL_SHIPPED）订单可差额关闭（business-flow §13.3）",
			})
		}
		before := *o
		obs, err := s.repo.ListOutboundOrdersBySO(tx, o.SoNo)
		if err != nil {
			return err
		}
		for _, ob := range obs {
			items, err := s.repo.ListOutboundItems(tx, ob.ID.Int64())
			if err != nil {
				return err
			}
			shippedAny := false
			for _, it := range items {
				if it.QtyShipped.IsPositive() {
					shippedAny = true
					break
				}
			}
			target := OBStatusCancelled
			if shippedAny {
				target = OBStatusClosed
			}
			if ob.Status == target {
				continue
			}
			if !canTransition(obTransitions, ob.Status, target) {
				return response.NewError(ErrStateConflict, map[string]any{
					"outbound_no": ob.OutboundNo, "from": ob.Status, "to": target,
				})
			}
			if err := s.releaseOutboundLocks(ctx, tx, actor, &ob, items, "sales_order:"+o.SoNo); err != nil {
				return err
			}
			stamp := StatusStamp{By: actor.ID}
			if target == OBStatusCancelled {
				stamp.Cancelled = true
				if err := s.repo.CancelPickTasks(tx, ob.OutboundNo, 0); err != nil {
					return err
				}
			}
			if n, err := s.repo.MarkOutboundStatus(tx, ob.ID.Int64(), ob.Status, target, stamp); err != nil {
				return err
			} else if n == 0 {
				return response.NewError(ErrStateConflict, map[string]any{"outbound_no": ob.OutboundNo, "from": ob.Status, "to": target})
			}
		}
		n, err := s.repo.MarkSalesOrderStatus(tx, o.ID.Int64(), before.Status, SOStatusCompleted, StatusStamp{By: actor.ID, Completed: true})
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{"so_no": o.SoNo, "from": before.Status, "to": SOStatusCompleted})
		}
		o.Status = SOStatusCompleted
		out = o
		return s.auditEntry(tx, "sales", "sales_order", "close", o.ID.Int64(), actor, in, before, o)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// releaseOutboundLocks 释放出库单未发货行的 ORDER_HOLD 锁（inventory-rules §4.2：
// 释放必须由明确业务动作触发并生成流水）。已发货行的锁已被 Deduct 核销（CONSUMED），
// 依"整行一次发货"口径跳过。trigger 为触发释放的业务单据标识（审计与 Release source）。
func (s *Service) releaseOutboundLocks(ctx context.Context, tx *gorm.DB, actor Actor,
	ob *OutboundOrder, items []OutboundItem, trigger string) error {
	recs, err := s.repo.ListAllocationsByOutbound(tx, ob.OutboundNo)
	if err != nil {
		return err
	}
	shippedLine := map[int64]bool{}
	for _, it := range items {
		if it.QtyShipped.IsPositive() {
			shippedLine[it.LineNo] = true
		}
	}
	for _, rec := range recs {
		if shippedLine[rec.LineNo] || rec.LockID <= 0 {
			continue
		}
		// 幂等键构成（plan §7 订单取消）：release:{source_no}:{lock_id}。
		if _, err := s.stock.ReleaseLock(ctx, tx, ReleaseLockOp{
			LockID:         rec.LockID,
			Qty:            rec.Qty,
			Source:         Source{Type: "outbound_order", No: ob.OutboundNo},
			Actor:          actor,
			IdempotencyKey: "release:" + ob.OutboundNo + ":" + strconv.FormatInt(rec.LockID, 10),
			Remark:         "出库链释放预占（" + trigger + "）",
		}); err != nil {
			return err
		}
	}
	return nil
}
