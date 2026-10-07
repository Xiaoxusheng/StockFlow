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

// 入库单与收货业务（business-flow §3.2–§3.4、plan §6.2/§6.3）：
//   - 入库单：DRAFT→RECEIVING→AWAITING_QC→AWAITING_PUTAWAY→COMPLETED（CLOSED 差额关闭）；
//   - 收货确认：事件型幂等落库（幂等键 + uk_receipts_idempotency 部分唯一索引兜底），
//     §2.3 四量强校验（累计收货 ≤ 订单量，超量整体回滚），批次/效期/序列号三开关采集，
//     异常收货经异常中心窄接口同事务登记，并按行生成上架任务
//     （require_inspect=false 免检直达 available；默认进入待检 pending_inspect——
//     InspectResult 的前置状态，由质检单执行事务转换）。

// InboundItemInput 入库单明细入参。
type InboundItemInput struct {
	SKUID  int64     `json:"sku_id" binding:"required"`
	Qty    stock.Qty `json:"qty" binding:"required"`
	Remark string    `json:"remark"`
}

// InboundCreateInput 入库单创建入参。
type InboundCreateInput struct {
	SourceType  string             `json:"source_type" binding:"required"`
	SourceNo    string             `json:"source_no"`
	WarehouseID int64              `json:"warehouse_id" binding:"required"`
	Remark      string             `json:"remark"`
	Items       []InboundItemInput `json:"items" binding:"required"`
}

// InboundUpdateInput 入库单修改入参（仅草稿，明细整单替换）。
type InboundUpdateInput struct {
	Remark string             `json:"remark"`
	Items  []InboundItemInput `json:"items"`
}

// InboundCloseInput 差额关闭入参（RECEIVING→CLOSED，原因必填——plan §6.2）。
type InboundCloseInput struct {
	Reason string `json:"reason" binding:"required"`
}

// ReceiptLineInput 收货行入参。
type ReceiptLineInput struct {
	SKUID          int64             `json:"sku_id" binding:"required"`
	QtyGood        stock.Qty         `json:"qty_good"`
	QtyRejected    stock.Qty         `json:"qty_rejected"`
	RequireInspect *bool             `json:"require_inspect"` // nil=默认 true（合格品进入待检）
	BatchNo        string            `json:"batch_no"`
	ExpiryDate     database.JSONTime `json:"expiry_date"`
	ProductionDate database.JSONTime `json:"production_date"`
	Serials        []string          `json:"serials"`
	// 异常收货（§3.4：少货/多货/错货/破损/包装异常/批次异常/效期异常；拍照/附件随
	// 异常中心承载——文件中心阶段 14 前不提供上传，plan §12）
	ExceptionType string `json:"exception_type"`
	ExceptionNote string `json:"exception_note"`
	TargetBinID   int64  `json:"target_bin_id"` // 手动指定目标库位（§5.2）
	Remark        string `json:"remark"`
}

// ReceiptInput 收货确认入参（幂等键取请求体或 Idempotency-Key 头，头优先——plan §7）。
type ReceiptInput struct {
	InboundNo      string             `json:"inbound_no" binding:"required"`
	Lines          []ReceiptLineInput `json:"lines" binding:"required"`
	IdempotencyKey string             `json:"idempotency_key"`
	Remark         string             `json:"remark"`
}

// ReceiptResult 收货确认结果（重放时 Replay=true 且返回既有单号）。
type ReceiptResult struct {
	ReceiptNo     string   `json:"receipt_no"`
	Replay        bool     `json:"replay"`
	InboundNo     string   `json:"inbound_no"`
	InboundStatus string   `json:"inbound_status"`
	PONo          string   `json:"po_no,omitempty"`
	POStatus      string   `json:"po_status,omitempty"`
	PutawayTasks  []string `json:"putaway_task_nos"`
}

// exceptionTypeReceiving 异常中心"收货异常"类目（§11.2 九类之一；§3.4 子型入 detail）。
const exceptionTypeReceiving = "收货异常"

// buildInboundItems 校验并构造入库单明细（SKU 不重复、数量>0；同 SKU 合并单行
// 以支撑收货按 SKU 精确匹配与累计守卫）。
func (s *Service) buildInboundItems(ctx context.Context, in []InboundItemInput, by int64) ([]*InboundItem, error) {
	if len(in) == 0 {
		return nil, invalidParam("items", "入库单必须至少包含一行明细")
	}
	items := make([]*InboundItem, 0, len(in))
	skuSeen := map[int64]int{}
	for i, li := range in {
		field := fmt.Sprintf("items[%d]", i)
		if li.SKUID <= 0 {
			return nil, invalidParam(field+".sku_id", "必须为正整数")
		}
		if !li.Qty.IsPositive() {
			return nil, invalidParam(field+".qty", "必须为正数")
		}
		if prev, dup := skuSeen[li.SKUID]; dup {
			return nil, response.NewError(ErrPOLineDuplicated, map[string]any{
				"sku_id": li.SKUID, "lines": []int{prev, i + 1}, "reason": "入库单明细 SKU 重复",
			})
		}
		skuSeen[li.SKUID] = i + 1
		f, err := s.skuFlagsOf(ctx, li.SKUID)
		if err != nil {
			return nil, err
		}
		if !f.Enabled {
			return nil, invalidParam(field+".sku_id", fmt.Sprintf("SKU %d 已停用", li.SKUID))
		}
		items = append(items, &InboundItem{
			LineNo: len(items) + 1,
			SKUID:  li.SKUID,
			Qty:    li.Qty,
			Remark: strings.TrimSpace(li.Remark),
		})
	}
	return items, nil
}

// validateInboundSource 校验来源单据（PURCHASE：采购订单须已审核且仓库一致——
// api.md §4 业务关系；OTHER：来源号可空）。
func (s *Service) validateInboundSource(ctx context.Context, sourceType, sourceNo string, warehouseID int64) (*PurchaseOrder, error) {
	switch sourceType {
	case SourceTypePurchase:
		if strings.TrimSpace(sourceNo) == "" {
			return nil, invalidParam("source_no", "采购入库必须携带采购订单号")
		}
		po, err := s.repo.FindPOByNo(ctx, strings.TrimSpace(sourceNo))
		if err != nil {
			return nil, err
		}
		if po == nil {
			return nil, response.NewError(ErrInboundSourceInvalid, map[string]any{"source_no": sourceNo})
		}
		if po.Status != POStatusApproved && po.Status != POStatusPartialReceived && po.Status != POStatusReceivedAll {
			return nil, response.NewError(ErrInboundSourceInvalid, map[string]any{
				"source_no": sourceNo, "status": po.Status, "reason": "采购订单未审核或已终态，不能创建入库单",
			})
		}
		if po.WarehouseID != warehouseID {
			return nil, invalidParam("warehouse_id", "入库仓必须与采购订单收货仓一致")
		}
		return po, nil
	case SourceTypeOther:
		return nil, nil
	}
	return nil, invalidParam("source_type", "必须为 PURCHASE 或 OTHER")
}

// CreateInbound 创建入库单（草稿）。
func (s *Service) CreateInbound(ctx context.Context, actor Actor, in InboundCreateInput) (*InboundOrder, error) {
	if err := s.requireChecker(); err != nil {
		return nil, err
	}
	po, err := s.validateInboundSource(ctx, in.SourceType, in.SourceNo, in.WarehouseID)
	if err != nil {
		return nil, err
	}
	items, err := s.buildInboundItems(ctx, in.Items, actor.UserID)
	if err != nil {
		return nil, err
	}
	o := &InboundOrder{
		SourceType:  in.SourceType,
		SourceNo:    strings.TrimSpace(in.SourceNo),
		WarehouseID: in.WarehouseID,
		Status:      InboundStatusDraft,
		Remark:      strings.TrimSpace(in.Remark),
		CreatedBy:   database.ID(actor.UserID),
		UpdatedBy:   database.ID(actor.UserID),
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		no, err := docnum.NextWithRetry(ctx, tx, docRule("IN"), func(tx *gorm.DB, no string) error {
			o.InboundNo = no
			return s.repo.InsertInbound(ctx, tx, o)
		})
		if err != nil {
			return err
		}
		o.InboundNo = no
		if err := s.repo.ReplaceInboundItems(ctx, tx, o.ID.Int64(), items, actor.UserID); err != nil {
			return err
		}
		e := actor.auditEntry("inbound_order", o.ID.Int64(), "create")
		e.After = o
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	_ = po // 来源校验通过即落 biz 关系；PO 引用走 source_no 逻辑单号（不建 FK）
	return s.repo.FindInboundByID(ctx, o.ID.Int64())
}

// UpdateInbound 修改入库单（仅草稿）。
func (s *Service) UpdateInbound(ctx context.Context, actor Actor, id int64, in InboundUpdateInput) (*InboundOrder, error) {
	o, err := s.repo.FindInboundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与 GetInbound 详情同口径）。
	if !actor.canAccessWarehouse(o.WarehouseID) {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	if o.Status != InboundStatusDraft {
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{"status": o.Status, "reason": "仅草稿可修改"})
	}
	var items []*InboundItem
	if in.Items != nil {
		items, err = s.buildInboundItems(ctx, in.Items, actor.UserID)
		if err != nil {
			return nil, err
		}
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		cols := map[string]any{"updated_at": database.Now(), "updated_by": actor.UserID, "remark": strings.TrimSpace(in.Remark)}
		before := *o
		if err := s.repo.UpdateInboundCols(ctx, tx, id, cols); err != nil {
			return err
		}
		if items != nil {
			if err := s.repo.ReplaceInboundItems(ctx, tx, id, items, actor.UserID); err != nil {
				return err
			}
		}
		e := actor.auditEntry("inbound_order", id, "update")
		e.Before, e.After = &before, o
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindInboundByID(ctx, id)
}

// CancelInbound 取消入库单（仅 DRAFT，无收货——plan §6.2）。
// 取消原因 in.Reason 可选：落审计快照（对齐 CancelPO/sales CancelInput 口径）。
func (s *Service) CancelInbound(ctx context.Context, actor Actor, id int64, in CancelInput) (*InboundOrder, error) {
	o, err := s.repo.FindInboundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与 GetInbound 详情同口径）。
	if !actor.canAccessWarehouse(o.WarehouseID) {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	if !canTransition(inboundTransitions, o.Status, InboundStatusCancelled) {
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{
			"status": o.Status, "to": InboundStatusCancelled,
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		n, err := s.repo.UpdateInboundStatus(ctx, tx, id, o.Status, InboundStatusCancelled, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		e := actor.auditEntry("inbound_order", id, "cancel")
		e.Before = map[string]any{"status": o.Status}
		e.After = map[string]any{"status": InboundStatusCancelled, "reason": strings.TrimSpace(in.Reason)}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindInboundByID(ctx, id)
}

// CloseInbound 差额关闭（RECEIVING→CLOSED，原因必填；进行中的上架任务必须先完结，
// 待领取任务联动取消——plan §6.2/§6.3）。
func (s *Service) CloseInbound(ctx context.Context, actor Actor, id int64, in InboundCloseInput) (*InboundOrder, error) {
	o, err := s.repo.FindInboundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	// 数据权限 fail-closed（f16）：越仓单据按不存在处理（与 GetInbound 详情同口径）。
	if !actor.canAccessWarehouse(o.WarehouseID) {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	if !canTransition(inboundTransitions, o.Status, InboundStatusClosed) {
		return nil, response.NewError(ErrInboundStatusNotAllowed, map[string]any{
			"status": o.Status, "to": InboundStatusClosed,
		})
	}
	counts, err := s.repo.CountTasksByInbound(ctx, o.InboundNo)
	if err != nil {
		return nil, err
	}
	// PAUSED 属未完成活动态，与 IN_PROGRESS 一并阻断差额关闭（迁移 000017）。
	if counts[TaskStatusInProgress] > 0 || counts[TaskStatusPaused] > 0 {
		return nil, response.NewError(ErrInboundHasActiveTasks, map[string]any{
			"in_progress": counts[TaskStatusInProgress], "paused": counts[TaskStatusPaused],
		})
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		// 待领取任务联动取消（plan §6.3：→CANCELLED 入库单取消联动）。
		pending, _, err := s.repo.ListTasks(ctx, TaskListFilter{
			InboundNo: o.InboundNo, Status: TaskStatusPending, Page: 1, PageSize: 1 << 20,
			Scope: WarehouseScope{All: true}, // 关闭联动查询不受列表数据权限过滤
		})
		if err != nil {
			return err
		}
		for _, t := range pending {
			n, err := s.repo.UpdatePutawayTaskStatus(ctx, tx, t.ID.Int64(), TaskStatusPending, TaskStatusCancelled, actor.UserID)
			if err := guardRows(n, err); err != nil {
				return err
			}
			e := actor.auditEntry("putaway_task", t.ID.Int64(), "cancel")
			e.Before = map[string]any{"status": TaskStatusPending}
			e.After = map[string]any{"status": TaskStatusCancelled, "reason": "入库单差额关闭联动"}
			if err := middlewareAudit(tx, e); err != nil {
				return err
			}
		}
		n, err := s.repo.UpdateInboundStatus(ctx, tx, id, o.Status, InboundStatusClosed, actor.UserID)
		if err := guardRows(n, err); err != nil {
			return response.NewError(ErrStatusConflict, map[string]any{"reason": "状态并发变化，请刷新重试"})
		}
		e := actor.auditEntry("inbound_order", id, "close")
		e.Before = map[string]any{"status": o.Status}
		e.After = map[string]any{"status": InboundStatusClosed, "reason": strings.TrimSpace(in.Reason)}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.FindInboundByID(ctx, id)
}

// InboundDetail 入库单详情（含明细）。
type InboundDetail struct {
	Order *InboundOrder  `json:"order"`
	Items []*InboundItem `json:"items"`
}

func (s *Service) GetInbound(ctx context.Context, id int64, scope WarehouseScope) (*InboundDetail, error) {
	o, err := s.repo.FindInboundByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：仓库不在范围内按不存在处理，
	// 与 sales/stockops/returns 详情接口同口径（plan §10.5）。
	if o == nil || !scope.visible(o.WarehouseID) {
		return nil, response.NewError(ErrInboundNotFound, nil)
	}
	items, err := s.repo.ListInboundItems(ctx, id)
	if err != nil {
		return nil, err
	}
	return &InboundDetail{Order: o, Items: items}, nil
}

func (s *Service) ListInbound(ctx context.Context, f InboundListFilter) ([]*InboundOrder, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)
	f.Page, f.PageSize = page, size
	return s.repo.ListInbounds(ctx, f)
}

func (s *Service) ListReceipt(ctx context.Context, f ReceiptListFilter) ([]*Receipt, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)
	f.Page, f.PageSize = page, size
	return s.repo.ListReceipts(ctx, f)
}

// GetReceipt 收货记录详情（含明细）。
type ReceiptDetail struct {
	Receipt *Receipt       `json:"receipt"`
	Items   []*ReceiptItem `json:"items"`
}

func (s *Service) GetReceiptByID(ctx context.Context, id int64, scope WarehouseScope) (*ReceiptDetail, error) {
	rc, err := s.repo.FindReceiptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：仓库不在范围内按不存在处理。
	if rc == nil || !scope.visible(rc.WarehouseID) {
		return nil, response.NewError(ErrReceiptNotFound, nil)
	}
	items, err := s.repo.ListReceiptItems(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ReceiptDetail{Receipt: rc, Items: items}, nil
}

// GetReceiptByNo 按单号查询收货记录详情。
func (s *Service) GetReceiptByNo(ctx context.Context, no string, scope WarehouseScope) (*ReceiptDetail, error) {
	rc, err := s.repo.FindReceiptByNo(ctx, no)
	if err != nil {
		return nil, err
	}
	// 数据权限 fail-closed（permission.md §4）：仓库不在范围内按不存在处理。
	if rc == nil || !scope.visible(rc.WarehouseID) {
		return nil, response.NewError(ErrReceiptNotFound, nil)
	}
	items, err := s.repo.ListReceiptItems(ctx, rc.ID.Int64())
	if err != nil {
		return nil, err
	}
	return &ReceiptDetail{Receipt: rc, Items: items}, nil
}
