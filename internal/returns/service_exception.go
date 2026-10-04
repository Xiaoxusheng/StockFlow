package returns

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/database"
	"github.com/stockflow/server/internal/docnum"
	"github.com/stockflow/server/internal/middleware"
	"github.com/stockflow/server/internal/response"
	"github.com/stockflow/server/internal/stock"
)

// 异常中心（business-flow §11.2：统一处理九类异常；plan §6.10 生命周期与异常冻结联动）。
//
// 生命周期：OPEN→ASSIGNED（分派）→PROCESSING→PENDING_REVIEW（待复核）→RESOLVED→CLOSED；
// 处理记录追加式（jsonb 数组，不覆盖历史，business-flow §11.2）。
//
// 异常冻结联动（inventory-rules §4：异常冻结 EXCEPTION_FREEZE→frozen 状态列）：
//   - 创建时可对定位到的库存行 Lock(EXCEPTION_FREEZE)（可选；与异常单创建同事务），
//     freeze_lock_id 回指锁记录；
//   - RESOLVED/CLOSED 时必须 ReleaseLock（inventory-rules §4.2：释放由明确业务动作触发并
//     生成流水）。释放量取自创建期冻结处理记录（HandleRecord{Action:"freeze", LockID, Qty}），
//     释放后追加 release 记录并清空 freeze_lock_id——append-only 记录即冻结台账。
//
// 跨域入口（plan §3.1 ExceptionCreator 行）：CreateException 为导出实现，签名只含内建
// 类型与本包值结构，purchase/sales/stockops 经各自消费接口由 router 闭包桥接调用；
// tx 透传支持与调用方业务同事务（architecture §4）。

// ExceptionCreateInput 异常单创建入参（HTTP 面）。
type ExceptionCreateInput struct {
	Type       string `json:"type"`        // 九类中文值域（business-flow §11.2）
	SourceType string `json:"source_type"` // 产生环节的单据类型（收货/拣货/复核/盘点等）
	SourceNo   string `json:"source_no"`
	SKUID      int64  `json:"sku_id"` // 可空定位：0=未定位
	BinID      int64  `json:"bin_id"` // 可空定位：0=未定位
	SerialNo   string `json:"serial_no"`
	OwnerID    int64  `json:"owner_id"`
	OwnerName  string `json:"owner_name"`
	Detail     string `json:"detail"`
	Remark     string `json:"remark"`
	// 冻结选项：定位到具体库存行时才可冻结（仓库+库位+SKU 定位，inventory-rules §4.1）；
	// FreezeQty 为冻结量（解冻释放取量依据）。
	FreezeEnabled     bool   `json:"freeze_enabled"`
	FreezeWarehouseID int64  `json:"freeze_warehouse_id"`
	FreezeBatchID     int64  `json:"freeze_batch_id"` // 0=非批次 SKU
	FreezeQty         string `json:"freeze_qty"`
}

// ExceptionAssignInput 分派入参。
type ExceptionAssignInput struct {
	AssigneeID   int64  `json:"assignee_id"`
	AssigneeName string `json:"assignee_name"`
}

// ExceptionNoteInput 处理动作备注（开始处理/提交复核/解决/关闭）。
type ExceptionNoteInput struct {
	Note string `json:"note"`
}

// ExceptionView 异常单视图。
type ExceptionView struct {
	IDInt         database.ID       `json:"id"`
	ExceptionNo   string            `json:"exception_no"`
	Type          string            `json:"type"`
	SourceType    string            `json:"source_type"`
	SourceNo      string            `json:"source_no"`
	SKUID         int64             `json:"sku_id"`
	BinID         int64             `json:"bin_id"`
	SerialNo      string            `json:"serial_no"`
	Status        string            `json:"status"`
	Detail        string            `json:"detail"`
	AssigneeID    int64             `json:"assignee_id"`
	AssigneeName  string            `json:"assignee_name"`
	OwnerID       int64             `json:"owner_id"`
	OwnerName     string            `json:"owner_name"`
	HandleRecords []HandleRecord    `json:"handle_records"`
	ImageRefs     []string          `json:"image_refs"`
	FreezeLockID  int64             `json:"freeze_lock_id"` // 0=未冻结
	AssignedAt    database.JSONTime `json:"assigned_at"`
	ResolvedAt    database.JSONTime `json:"resolved_at"`
	ClosedAt      database.JSONTime `json:"closed_at"`
	Remark        string            `json:"remark"`
	CreatedAt     database.JSONTime `json:"created_at"`
	UpdatedAt     database.JSONTime `json:"updated_at"`
}

func newExceptionView(e *Exception) *ExceptionView {
	v := &ExceptionView{
		IDInt: e.ID, ExceptionNo: e.ExceptionNo, Type: e.Type,
		SourceType: e.SourceType, SourceNo: e.SourceNo,
		SKUID: e.SKUID, BinID: e.BinID, SerialNo: e.SerialNo,
		Status: e.Status, Detail: e.Detail,
		AssigneeID: e.AssigneeID, AssigneeName: e.AssigneeName,
		OwnerID: e.OwnerID, OwnerName: e.OwnerName,
		HandleRecords: e.HandleRecordList(), ImageRefs: e.ImageRefList(),
		AssignedAt: e.AssignedAt, ResolvedAt: e.ResolvedAt, ClosedAt: e.ClosedAt,
		Remark: e.Remark, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	if e.FreezeLockID != nil {
		v.FreezeLockID = *e.FreezeLockID
	}
	return v
}

// CreateException 创建异常单（OPEN），可选触发异常冻结。
//
// 冻结（plan §6.10：创建时可对定位到的库存行 Lock(EXCEPTION_FREEZE)）：
// 与异常单创建同事务；定位信息不足显式失败（fail-closed，不放行无定位的冻结）。
// 幂等键 efreeze:{exception_no}:{bin}:{sku}:{batch}（plan §7 键构成规则）。
// 该方法同时是 plan §3.1 ExceptionCreator 窄接口的导出实现（内建类型签名）。
func (s *Service) CreateException(ctx context.Context, tx *gorm.DB, op CreateExceptionOp) (string, error) {
	if !exceptionTypes[op.Type] {
		return "", response.NewError(ErrExceptionTypeInvalid, map[string]any{
			"type": op.Type, "reason": "必须为九类异常之一（business-flow §11.2）",
		})
	}
	if op.Freeze {
		if op.FreezeWarehouseID <= 0 || op.BinID <= 0 || op.SKUID <= 0 {
			return "", response.NewError(ErrFreezeTargetRequired, map[string]any{
				"freeze_warehouse_id": op.FreezeWarehouseID, "bin_id": op.BinID, "sku_id": op.SKUID,
				"reason": "异常冻结需要仓库/库位/SKU 定位到库存行（inventory-rules §4.1）",
			})
		}
		// 冻结量必填：解冻释放取量依据（拒绝"冻结无量"导致后续无法合规释放）。
		if q, err := stock.ParseQty(op.FreezeQty); err != nil || !q.IsPositive() {
			return "", paramError("freeze_qty", "冻结量必填且为正数（numeric(18,4) 文本）")
		}
	}
	var exceptionNo string
	run := func(tx *gorm.DB) error {
		rule, _ := docnum.RuleFor("EX")
		e := &Exception{
			Type: op.Type, SourceType: op.SourceType, SourceNo: op.SourceNo,
			SKUID: op.SKUID, BinID: op.BinID, SerialNo: op.SerialNo,
			Status: ExceptionStatusOpen, Detail: op.Detail,
			OwnerID: op.OwnerID, OwnerName: op.OwnerName,
			HandleRecords: marshalJSONB([]HandleRecord{}), ImageRefs: marshalJSONB([]string{}),
			CreatedAt: database.Now(), UpdatedAt: database.Now(),
			CreatedBy: op.Actor.UserID, UpdatedBy: op.Actor.UserID,
		}
		if _, err := docnum.NextWithRetry(ctx, tx, rule, func(tx *gorm.DB, no string) error {
			e.ExceptionNo = no
			return s.repo.InsertException(tx, e)
		}); err != nil {
			return err
		}
		exceptionNo = e.ExceptionNo
		if op.Freeze {
			gw, err := s.requireStock()
			if err != nil {
				return err
			}
			key := stock.RowKey{
				WarehouseID: op.FreezeWarehouseID, BinID: op.BinID,
				SKUID: op.SKUID, BatchID: op.FreezeBatchID,
			}
			ia := op.Actor.inventoryActor()
			lockQty, perr := stock.ParseQty(op.FreezeQty)
			if perr != nil || !lockQty.IsPositive() {
				return paramError("freeze_qty", "冻结量必须为正数（numeric(18,4) 文本）")
			}
			res, err := gw.Lock(ctx, tx, stock.LockOp{
				Key: key, Qty: lockQty, LockType: "EXCEPTION_FREEZE", // 异常冻结→frozen（plan §7 映射）
				Source: stock.Source{Type: "exception", No: e.ExceptionNo},
				Actor:  ia, IdempotencyKey: fmtKey("efreeze", e.ExceptionNo, op.BinID, op.SKUID, op.FreezeBatchID),
				Remark: "异常单冻结（EXCEPTION_FREEZE）",
			})
			if err != nil {
				return err
			}
			lockID := res.LockID
			e.FreezeLockID = &lockID
			if err := s.repo.SetExceptionFreezeLock(tx, e.ID.Int64(), &lockID); err != nil {
				return err
			}
			// 冻结台账：处理记录携带锁 ID 与冻结量（解冻释放取量依据，append-only）。
			if err := s.repo.AppendHandleRecord(tx, e.ID.Int64(), HandleRecord{
				At: database.Now(), Action: "freeze",
				ByID: op.Actor.UserID, ByName: op.Actor.Username,
				Note:   "异常冻结 EXCEPTION_FREEZE（inventory-rules §4.2）",
				LockID: lockID, Qty: op.FreezeQty,
			}); err != nil {
				return err
			}
		}
		if err := middleware.Audit(tx, withSnapshots(op.Actor.auditEntry("exception", e.ID.Int64(), "create"),
			map[string]any{"type": op.Type, "source_type": op.SourceType, "source_no": op.SourceNo,
				"freeze": op.Freeze, "freeze_warehouse_id": op.FreezeWarehouseID, "freeze_batch_id": op.FreezeBatchID},
			map[string]any{"exception_no": e.ExceptionNo, "status": e.Status,
				"freeze_lock_id": e.FreezeLockID}, nil)); err != nil {
			return err
		}
		return nil
	}
	if tx != nil {
		if err := run(tx); err != nil {
			return "", err
		}
	} else {
		if err := s.repo.DB().WithContext(ctx).Transaction(run); err != nil {
			return "", err
		}
	}
	return exceptionNo, nil
}

// CreateExceptionOp 跨域创建入参（ExceptionCreator 导出实现的入参结构；全部内建类型，
// 调用方域经 router 闭包桥接构造，plan §3.1）。
type CreateExceptionOp struct {
	Type       string // 九类中文值域（business-flow §11.2）
	SourceType string
	SourceNo   string
	Detail     string
	SKUID      int64  // 0=未定位
	BinID      int64  // 0=未定位
	SerialNo   string // 空=非序列号问题
	// Freeze 冻结选项：需 FreezeWarehouseID/BinID/SKUID 定位到库存行；
	// FreezeBatchID 0=非批次 SKU；FreezeQty 为冻结量（numeric(18,4) 文本，解冻释放取量依据）。
	Freeze            bool
	FreezeWarehouseID int64
	FreezeBatchID     int64
	FreezeQty         string
	OwnerID           int64 // 责任人（可空，business-flow §11.2）
	OwnerName         string
	Actor             Actor
}

// exceptionDetailPayload detail JSON 载荷（键与 sales.ExceptionDetail JSON 标签对齐——
// sku_id/bin_id/batch_id/serial_no/line_no/reason/extra；freeze_* 为冻结扩展位，
// router 桥接 sales/purchase 的 ExceptionCreator 时逐字段构造）。
type exceptionDetailPayload struct {
	SKUID             int64          `json:"sku_id"`
	BinID             int64          `json:"bin_id"`
	BatchID           int64          `json:"batch_id"`
	SerialNo          string         `json:"serial_no"`
	LineNo            int64          `json:"line_no"`
	Reason            string         `json:"reason"`
	Freeze            bool           `json:"freeze"`
	FreezeWarehouseID int64          `json:"freeze_warehouse_id"`
	FreezeQty         string         `json:"freeze_qty"`
	Extra             map[string]any `json:"extra,omitempty"`
}

// Create 异常登记（plan §3.1 ExceptionCreator 导出实现的扁平内建类型形态——与
// purchase.ExceptionCreator 签名逐字一致，router 可直接结构化注入；sales 等携带
// 域类型 detail 的接口经 router 闭包逐字段桥接）。detail 为 JSON 文本：
// {"sku_id","bin_id","batch_id","serial_no","line_no","reason","freeze",
//
//	"freeze_warehouse_id","freeze_qty",...}。tx 非 nil 时与调用方业务同事务
//
// （architecture §4：收货/拣货等业务回滚则异常登记一并回滚）。
func (s *Service) Create(ctx context.Context, tx *gorm.DB, excType, sourceType, sourceNo, detail string) (exceptionNo string, err error) {
	var p exceptionDetailPayload
	if detail != "" {
		if err := json.Unmarshal([]byte(detail), &p); err != nil {
			return "", paramError("detail", "异常定位信息必须为 JSON 文本")
		}
	}
	return s.CreateException(ctx, tx, CreateExceptionOp{
		Type: excType, SourceType: sourceType, SourceNo: sourceNo, Detail: detail,
		SKUID: p.SKUID, BinID: p.BinID, SerialNo: p.SerialNo,
		Freeze:            p.Freeze,
		FreezeWarehouseID: p.FreezeWarehouseID,
		FreezeBatchID:     p.BatchID,
		FreezeQty:         p.FreezeQty,
	})
}

// AssignException 分派（OPEN→ASSIGNED，business-flow §11.2 分派环节；plan §9.1 assign）。
func (s *Service) AssignException(ctx context.Context, actor Actor, id int64, in ExceptionAssignInput) (*ExceptionView, error) {
	if in.AssigneeID <= 0 {
		return nil, paramError("assignee_id", "处理人必填")
	}
	var view *ExceptionView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e, err := s.repo.FindExceptionForUpdate(tx, id)
		if err != nil {
			return err
		}
		if e == nil {
			return response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
		}
		before := snapshotException(e)
		if err := s.guardExceptionStatus(tx, e, ExceptionStatusAssigned, "assigned_at", actor.UserID); err != nil {
			return err
		}
		if err := s.repo.UpdateExceptionAssignee(tx, id, in.AssigneeID, in.AssigneeName, actor.UserID); err != nil {
			return err
		}
		e.AssigneeID = in.AssigneeID
		e.AssigneeName = in.AssigneeName
		if err := s.repo.AppendHandleRecord(tx, id, HandleRecord{
			At: database.Now(), Action: "assign",
			ByID: actor.UserID, ByName: actor.Username,
			Note: "分派给 " + in.AssigneeName,
		}); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry("exception", id, "assign"),
			map[string]any{"assignee_id": in.AssigneeID, "assignee_name": in.AssigneeName},
			map[string]any{"before": before, "after": snapshotException(e)}, nil)); err != nil {
			return err
		}
		view = newExceptionView(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// StartException 开始处理（ASSIGNED→PROCESSING）。
func (s *Service) StartException(ctx context.Context, actor Actor, id int64, in ExceptionNoteInput) (*ExceptionView, error) {
	return s.transitionException(ctx, actor, id, ExceptionStatusProcessing, "", "start", in.Note)
}

// ReviewException 提交复核（PROCESSING→PENDING_REVIEW）。
func (s *Service) ReviewException(ctx context.Context, actor Actor, id int64, in ExceptionNoteInput) (*ExceptionView, error) {
	return s.transitionException(ctx, actor, id, ExceptionStatusPendingReview, "", "review", in.Note)
}

// ResolveException 解决（PENDING_REVIEW→RESOLVED）：按需释放异常冻结（inventory-rules
// §4.2——RESOLVED/CLOSED 必须释放，同事务并生成 RELEASE 流水）。
func (s *Service) ResolveException(ctx context.Context, actor Actor, id int64, in ExceptionNoteInput) (*ExceptionView, error) {
	var view *ExceptionView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e, err := s.repo.FindExceptionForUpdate(tx, id)
		if err != nil {
			return err
		}
		if e == nil {
			return response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
		}
		before := snapshotException(e)
		if err := s.guardExceptionStatus(tx, e, ExceptionStatusResolved, "resolved_at", actor.UserID); err != nil {
			return err
		}
		if err := s.releaseFreeze(ctx, tx, e, actor); err != nil {
			return err
		}
		if err := s.repo.AppendHandleRecord(tx, id, HandleRecord{
			At: database.Now(), Action: "resolve",
			ByID: actor.UserID, ByName: actor.Username, Note: in.Note,
		}); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry("exception", id, "resolve"),
			map[string]any{"note": in.Note},
			map[string]any{"before": before, "after": snapshotException(e)}, nil)); err != nil {
			return err
		}
		view = newExceptionView(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// CloseException 关闭（RESOLVED→CLOSED）；防御性释放：若冻结仍挂（异常路径）在此补释放。
func (s *Service) CloseException(ctx context.Context, actor Actor, id int64, in ExceptionNoteInput) (*ExceptionView, error) {
	var view *ExceptionView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e, err := s.repo.FindExceptionForUpdate(tx, id)
		if err != nil {
			return err
		}
		if e == nil {
			return response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
		}
		before := snapshotException(e)
		if err := s.guardExceptionStatus(tx, e, ExceptionStatusClosed, "closed_at", actor.UserID); err != nil {
			return err
		}
		if err := s.releaseFreeze(ctx, tx, e, actor); err != nil {
			return err
		}
		if err := s.repo.AppendHandleRecord(tx, id, HandleRecord{
			At: database.Now(), Action: "close",
			ByID: actor.UserID, ByName: actor.Username, Note: in.Note,
		}); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry("exception", id, "close"),
			map[string]any{"note": in.Note},
			map[string]any{"before": before, "after": snapshotException(e)}, nil)); err != nil {
			return err
		}
		view = newExceptionView(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// transitionException 纯状态迁移动作（开始处理/提交复核）。
func (s *Service) transitionException(ctx context.Context, actor Actor, id int64, to, tsCol, action, note string) (*ExceptionView, error) {
	var view *ExceptionView
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e, err := s.repo.FindExceptionForUpdate(tx, id)
		if err != nil {
			return err
		}
		if e == nil {
			return response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
		}
		before := snapshotException(e)
		if err := s.guardExceptionStatus(tx, e, to, tsCol, actor.UserID); err != nil {
			return err
		}
		if err := s.repo.AppendHandleRecord(tx, id, HandleRecord{
			At: database.Now(), Action: action,
			ByID: actor.UserID, ByName: actor.Username, Note: note,
		}); err != nil {
			return err
		}
		if err := middleware.Audit(tx, withSnapshots(actor.auditEntry("exception", id, action),
			map[string]any{"note": note},
			map[string]any{"before": before, "after": snapshotException(e)}, nil)); err != nil {
			return err
		}
		view = newExceptionView(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// releaseFreeze 释放异常冻结（inventory-rules §4.2：由明确业务动作触发并生成流水）。
// 释放量取自冻结处理记录（freeze 台账，见 CreateException）；无冻结为 no-op；
// 释放后追加 release 记录并清空 freeze_lock_id。
func (s *Service) releaseFreeze(ctx context.Context, tx *gorm.DB, e *Exception, actor Actor) error {
	if e.FreezeLockID == nil || *e.FreezeLockID <= 0 {
		return nil
	}
	gw, err := s.requireStock()
	if err != nil {
		return err
	}
	lockID := *e.FreezeLockID
	qtyStr, err := freezeQtyOf(e, lockID)
	if err != nil {
		return err
	}
	qty, err := stock.ParseQty(qtyStr)
	if err != nil || !qty.IsPositive() {
		return response.NewError(response.CodeInternalError, map[string]any{
			"exception_id": e.ID.Int64(), "lock_id": lockID,
			"reason": "冻结量台账缺失或非法，无法释放（拒绝静默全量释放）",
		})
	}
	if _, err := gw.ReleaseLock(ctx, tx, stock.ReleaseLockOp{
		LockID: lockID, Qty: qty,
		Source:         stock.Source{Type: "exception", No: e.ExceptionNo},
		Actor:          actor.inventoryActor(),
		IdempotencyKey: fmtKey("release", e.ExceptionNo, lockID), // plan §7 release:{source_no}:{lock_id}
		Remark:         "异常解除释放 EXCEPTION_FREEZE",
	}); err != nil {
		return err
	}
	if err := s.repo.AppendHandleRecord(tx, e.ID.Int64(), HandleRecord{
		At: database.Now(), Action: "release",
		ByID: actor.UserID, ByName: actor.Username,
		Note: "释放异常冻结", LockID: lockID, Qty: qtyStr,
	}); err != nil {
		return err
	}
	if err := s.repo.SetExceptionFreezeLock(tx, e.ID.Int64(), nil); err != nil {
		return err
	}
	e.FreezeLockID = nil
	return nil
}

// freezeQtyOf 从处理记录取冻结量（最后一次对该锁的 freeze 记录且其后无 release 记录）。
func freezeQtyOf(e *Exception, lockID int64) (string, error) {
	records := e.HandleRecordList()
	qty := ""
	for _, r := range records {
		if r.LockID != lockID {
			continue
		}
		switch r.Action {
		case "freeze":
			qty = r.Qty
		case "release":
			qty = "" // 已释放过：不允许二次释放（防御）
		}
	}
	if qty == "" {
		return "", response.NewError(response.CodeInternalError, nil)
	}
	return qty, nil
}

// GetException 异常单详情。
func (s *Service) GetException(ctx context.Context, id int64) (*ExceptionView, error) {
	e, err := s.repo.FindException(ctx, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, response.NewError(ErrExceptionNotFound, map[string]any{"exception_id": id})
	}
	return newExceptionView(e), nil
}

// ListExceptions 分页列表（f 由 handler 组装）。
func (s *Service) ListExceptions(ctx context.Context, f ExceptionFilter) ([]*ExceptionView, int64, error) {
	rows, total, err := s.repo.ListExceptions(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*ExceptionView, 0, len(rows))
	for _, e := range rows {
		views = append(views, newExceptionView(e))
	}
	return views, total, nil
}

func snapshotException(e *Exception) map[string]any {
	lockID := int64(0)
	if e.FreezeLockID != nil {
		lockID = *e.FreezeLockID
	}
	return map[string]any{
		"id": e.ID.Int64(), "exception_no": e.ExceptionNo, "type": e.Type,
		"source_type": e.SourceType, "source_no": e.SourceNo,
		"status": e.Status, "assignee_id": e.AssigneeID, "freeze_lock_id": lockID,
	}
}
