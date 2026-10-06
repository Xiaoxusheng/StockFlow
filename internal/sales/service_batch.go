package sales

// 批量结果中心·领取批 + 任务优先级（2026-10-06 效率层一期 B3，docs/api.md §9 契约）：
//
//	POST /api/picks/batch-claim   批量领取拣货任务（sales:pick:claim）
//	POST /api/checks/batch-claim  批量领取复核任务（sales:check:claim）
//	PUT  /api/picks/:id/priority  拣货任务优先级（sales:pick:assign）
//	PUT  /api/checks/:id/priority 复核任务优先级（sales:check:assign）
//
// 批量语义（§2.7 冻结）：逐条调用既有 claim 服务函数（逐条独立事务 + 逐条审计），
// PENDING→success；已被本人领取→skipped（幂等命中/已处目标态）；被他人领取/状态非法→
// failed(reason=既有错误码字符串)；**批量不整体回滚**。响应契约
// {total, success_count, failed_count, skipped_count, results:[{id, status, reason}]}。
// 优先级语义（§2.4）：单列守卫 UPDATE + 终态守卫 409 + 值域 0-9（服务端校验 + 迁移
// 000023 CHECK 兜底）+ middleware.Audit 同事务审计。

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// ---- 批量结果契约（§2.7 冻结形状；printing 侧演进归 B4，本域仅领取批） ----

// BatchResultItem 批量结果逐条项（status ∈ success|failed|skipped；reason 仅 failed
// 携既有错误码字符串，skipped/failed 之外恒空）。
type BatchResultItem struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// BatchResult 批量结果契约（total=len(results)=success+failed+skipped）。
type BatchResult struct {
	Total        int               `json:"total"`
	SuccessCount int               `json:"success_count"`
	FailedCount  int               `json:"failed_count"`
	SkippedCount int               `json:"skipped_count"`
	Results      []BatchResultItem `json:"results"`
}

// BatchClaimInput 批量领取入参（ids 至少一项且逐项为正整数，否则 400 invalidParam）。
type BatchClaimInput struct {
	IDs []int64 `json:"ids"`
}

// TaskPriorityInput 优先级设置入参（必填 0-9；指针形态区分缺省与 0 值）。
type TaskPriorityInput struct {
	Priority *int `json:"priority"`
}

// 批量结果三态（§2.7 冻结字面量）。
const (
	batchStatusSuccess = "success"
	batchStatusFailed  = "failed"
	batchStatusSkipped = "skipped"
)

// validateBatchIDs 入参校验：ids 非空且逐项为正整数。
func validateBatchIDs(ids []int64) error {
	if len(ids) == 0 {
		return errInvalidParam("ids", "至少一项任务 ID")
	}
	for _, id := range ids {
		if id <= 0 {
			return errInvalidParam("ids", "任务 ID 必须为正整数")
		}
	}
	return nil
}

// validatePriority 值域校验（0-9；与迁移 000023 CHECK chk_*_tasks_priority 同值域）。
func validatePriority(in *TaskPriorityInput) (int, error) {
	if in == nil || in.Priority == nil {
		return 0, errInvalidParam("priority", "必填")
	}
	p := *in.Priority
	if p < 0 || p > 9 {
		return 0, errInvalidParam("priority", "必须为 0-9 的整数")
	}
	return p, nil
}

// errorCodeOf 业务错误 → 错误码字符串（failed.reason=既有冲突码；非业务错误收敛
// COMMON_INTERNAL_ERROR。response.Error 的 code 字段未导出，经 Error() 文本前缀提取）。
func errorCodeOf(err error) string {
	var e *response.Error
	if !errors.As(err, &e) {
		return "COMMON_INTERNAL_ERROR"
	}
	msg := e.Error()
	if idx := strings.Index(msg, ": "); idx > 0 {
		return msg[:idx]
	}
	return msg
}

// normalizeBatchResult 计数汇总（results 保持入参顺序，逐条对账）。
func normalizeBatchResult(ids []int64, items []BatchResultItem) *BatchResult {
	out := &BatchResult{Total: len(ids), Results: items}
	for _, it := range items {
		switch it.Status {
		case batchStatusSuccess:
			out.SuccessCount++
		case batchStatusFailed:
			out.FailedCount++
		case batchStatusSkipped:
			out.SkippedCount++
		}
	}
	return out
}

// ---- 拣货任务批量领取 ----

// BatchClaimPicks 批量领取拣货任务：逐条走既有 ClaimPickTask（原子抢占 + 同事务审计），
// 冲突后回读归属分类 skipped/failed；任一条失败不影响其余条目（不整体回滚）。
func (s *Service) BatchClaimPicks(ctx context.Context, actor Actor, in BatchClaimInput) (*BatchResult, error) {
	if err := validateBatchIDs(in.IDs); err != nil {
		return nil, err
	}
	items := make([]BatchResultItem, 0, len(in.IDs))
	for _, id := range in.IDs {
		it := BatchResultItem{ID: id, Status: batchStatusSuccess}
		if _, err := s.ClaimPickTask(ctx, actor, id); err != nil {
			it.Status, it.Reason = s.classifyPickClaimConflict(ctx, actor, id, err)
		}
		items = append(items, it)
	}
	return normalizeBatchResult(in.IDs, items), nil
}

// classifyPickClaimConflict 领取失败归类：任务不存在→原错误码；已被本人领取→skipped
// （幂等命中）；其余（被他人领取/状态非法）→failed(reason=既有错误码)。
func (s *Service) classifyPickClaimConflict(ctx context.Context, actor Actor, id int64, err error) (string, string) {
	var t *PickTask
	_ = s.repo.Tx(ctx, func(tx *gorm.DB) error { // 只读回读，Tx 仅为复用仓储签名
		t, _ = s.repo.GetPickTask(tx, id)
		return nil
	})
	if t == nil {
		return batchStatusFailed, errorCodeOf(err) // ErrTaskNotFound → SALES_TASK_NOT_FOUND
	}
	if t.AssigneeID == actor.ID {
		return batchStatusSkipped, ""
	}
	return batchStatusFailed, errorCodeOf(err)
}

// ---- 复核任务批量领取 ----

// BatchClaimChecks 批量领取复核任务：领取=原子指派不迁移状态（值域无 CLAIMED），
// 复核重复指派会静默成功，故先回读归属——已被本人领取（PENDING 且 assignee=本人）→
// skipped，不再触发指派；其余走既有 ClaimCheckTask（逐条独立事务 + 逐条审计）。
func (s *Service) BatchClaimChecks(ctx context.Context, actor Actor, in BatchClaimInput) (*BatchResult, error) {
	if err := validateBatchIDs(in.IDs); err != nil {
		return nil, err
	}
	items := make([]BatchResultItem, 0, len(in.IDs))
	for _, id := range in.IDs {
		it := BatchResultItem{ID: id, Status: batchStatusSuccess}
		t := s.readCheckTask(ctx, id)
		switch {
		case t == nil:
			it.Status, it.Reason = batchStatusFailed, "SALES_TASK_NOT_FOUND"
		case t.Status == CheckStatusPending && t.AssigneeID == actor.ID:
			it.Status = batchStatusSkipped // 幂等命中：已被本人领取
		default:
			if _, err := s.ClaimCheckTask(ctx, actor, id); err != nil {
				it.Status, it.Reason = batchStatusFailed, errorCodeOf(err)
			}
		}
		items = append(items, it)
	}
	return normalizeBatchResult(in.IDs, items), nil
}

// readCheckTask 只读回读（Tx 内调用仓储签名，仅取数不做写）。
func (s *Service) readCheckTask(ctx context.Context, id int64) *CheckTask {
	var t *CheckTask
	_ = s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, _ = s.repo.GetCheckTask(tx, id)
		return nil
	})
	return t
}

// ---- 任务优先级 ----

// 终态守卫值域（完成/取消态拒绝，§2.4「状态守卫 409」；各任务表状态机终态）：
// pick 终态=PICKED/CANCELLED（EXCEPTION 为异常处理中态，仍可调整优先级引导重拣）；
// check 终态=DONE/EXCEPTION。
func pickPriorityGuarded(status string) bool {
	return status == PickStatusPicked || status == PickStatusCancelled
}

func checkPriorityGuarded(status string) bool {
	return status == CheckStatusDone || status == CheckStatusException
}

// SetPickTaskPriority 拣货任务优先级（单列守卫 UPDATE + 终态 409 + 审计；值域 0-9 已在
// validatePriority 前置校验，列 CHECK 兜底）。
func (s *Service) SetPickTaskPriority(ctx context.Context, actor Actor, id int64, in TaskPriorityInput) error {
	priority, err := validatePriority(&in)
	if err != nil {
		return err
	}
	return s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetPickTask(tx, id)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"pick_task_id": id})
		}
		if pickPriorityGuarded(t.Status) {
			return response.NewError(ErrStateConflict, map[string]any{
				"pick_no": t.PickNo, "status": t.Status,
				"reason": "已完成/已取消任务不可调整优先级",
			})
		}
		n, err := s.repo.SetPickTaskPriority(tx, id, priority, actor.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{
				"pick_no": t.PickNo, "reason": "任务状态并发变化，请刷新重试",
			})
		}
		return s.auditEntry(tx, "sales", "pick_task", "priority", id, actor,
			map[string]any{"priority": priority}, nil,
			map[string]any{"priority": priority, "status": t.Status})
	})
}

// SetCheckTaskPriority 复核任务优先级（语义同 SetPickTaskPriority）。
func (s *Service) SetCheckTaskPriority(ctx context.Context, actor Actor, id int64, in TaskPriorityInput) error {
	priority, err := validatePriority(&in)
	if err != nil {
		return err
	}
	return s.repo.Tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.GetCheckTask(tx, id)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrTaskNotFound, map[string]any{"check_task_id": id})
		}
		if checkPriorityGuarded(t.Status) {
			return response.NewError(ErrStateConflict, map[string]any{
				"check_no": t.CheckNo, "status": t.Status,
				"reason": "已完成/已取消任务不可调整优先级",
			})
		}
		n, err := s.repo.SetCheckTaskPriority(tx, id, priority, actor.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return response.NewError(ErrStateConflict, map[string]any{
				"check_no": t.CheckNo, "reason": "任务状态并发变化，请刷新重试",
			})
		}
		return s.auditEntry(tx, "sales", "check_task", "priority", id, actor,
			map[string]any{"priority": priority}, nil,
			map[string]any{"priority": priority, "status": t.Status})
	})
}
