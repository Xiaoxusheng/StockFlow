package purchase

// 批量结果中心·领取批 + 任务优先级（2026-10-06 效率层一期 B3，docs/api.md §9 契约）：
//
//	POST /api/putaway/batch-claim  批量领取上架任务（purchase:putaway:claim）
//	PUT  /api/putaway/:id/priority 上架任务优先级（purchase:putaway:assign）
//
// 批量语义（§2.7 冻结）：逐条调用既有 ClaimPutawayTask（原子抢占 + 同事务审计），
// PENDING→success；已被本人领取→skipped；被他人领取/状态非法→failed(reason=既有
// 错误码字符串)；**批量不整体回滚**。响应契约
// {total, success_count, failed_count, skipped_count, results:[{id, status, reason}]}。
// 优先级语义（§2.4）：单列守卫 UPDATE + 终态守卫 409 + 值域 0-9（服务端校验 + 迁移
// 000023 CHECK 兜底）+ middleware.Audit 同事务审计。

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/stockflow/server/internal/response"
)

// ---- 批量结果契约（§2.7 冻结形状；sales 域同形副本，域间零 import） ----

// BatchResultItem 批量结果逐条项（status ∈ success|failed|skipped；reason 仅 failed
// 携既有错误码字符串）。ID 按 api.md §2 冻结口径输出 JSON 字符串（构造处
// strconv.FormatInt，与 internal/batchresult.Item、printing 侧一致——原 int64 直出
// 数字与冻结口径不符，2026-10-07 收敛）。
type BatchResultItem struct {
	ID     string `json:"id"`
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

// batchClaimMaxIDs 批量领取单次上限（f17：逐条独立事务串行执行，无上限的 ids 数组
// 可拖垮 worker 与连接池；100 ≈ 列表页最大选择集，超出即 400 拒绝）。
const batchClaimMaxIDs = 100

// validateBatchIDs 入参校验：ids 非空、逐项为正整数且有数量上限。
func validateBatchIDs(ids []int64) error {
	if len(ids) == 0 {
		return invalidParam("ids", "至少一项任务 ID")
	}
	if len(ids) > batchClaimMaxIDs {
		return invalidParam("ids", "单次最多 "+strconv.Itoa(batchClaimMaxIDs)+" 项")
	}
	for _, id := range ids {
		if id <= 0 {
			return invalidParam("ids", "任务 ID 必须为正整数")
		}
	}
	return nil
}

// validatePriority 值域校验（0-9；与迁移 000023 CHECK chk_putaway_tasks_priority 同值域）。
func validatePriority(in *TaskPriorityInput) (int, error) {
	if in == nil || in.Priority == nil {
		return 0, invalidParam("priority", "必填")
	}
	p := *in.Priority
	if p < 0 || p > 9 {
		return 0, invalidParam("priority", "必须为 0-9 的整数")
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

// ---- 上架任务批量领取 ----

// BatchClaimPutawayTasks 批量领取上架任务：逐条走既有 ClaimPutawayTask（PENDING→
// IN_PROGRESS 原子抢占 + 同事务审计），冲突后回读归属分类 skipped/failed；任一条
// 失败不影响其余条目（不整体回滚）。
func (s *Service) BatchClaimPutawayTasks(ctx context.Context, actor Actor, in BatchClaimInput) (*BatchResult, error) {
	if err := validateBatchIDs(in.IDs); err != nil {
		return nil, err
	}
	items := make([]BatchResultItem, 0, len(in.IDs))
	for _, id := range in.IDs {
		it := BatchResultItem{ID: strconv.FormatInt(id, 10), Status: batchStatusSuccess}
		if _, err := s.ClaimPutawayTask(ctx, actor, id); err != nil {
			it.Status, it.Reason = s.classifyPutawayClaimConflict(ctx, actor, id, err)
		}
		items = append(items, it)
	}
	return normalizeBatchResult(in.IDs, items), nil
}

// classifyPutawayClaimConflict 领取失败归类：任务不存在→原错误码；已被本人领取→
// skipped（幂等命中，PAUSED 同属本人进行中）；其余→failed(reason=既有错误码)。
func (s *Service) classifyPutawayClaimConflict(ctx context.Context, actor Actor, id int64, err error) (string, string) {
	t, ferr := s.repo.FindTaskByID(ctx, id)
	if ferr != nil || t == nil {
		return batchStatusFailed, errorCodeOf(err) // PURCHASE_PUTAWAY_TASK_NOT_FOUND 等
	}
	if t.ClaimedBy == actor.UserID {
		return batchStatusSkipped, ""
	}
	return batchStatusFailed, errorCodeOf(err)
}

// ---- 上架任务优先级 ----

// putawayPriorityGuarded 终态守卫（完成/取消态拒绝，§2.4「状态守卫 409」）。
func putawayPriorityGuarded(status string) bool {
	return status == TaskStatusCompleted || status == TaskStatusCancelled
}

// SetPutawayTaskPriority 上架任务优先级（单列守卫 UPDATE + 终态 409 + 审计；值域 0-9
// 已在 validatePriority 前置校验，列 CHECK 兜底）。
func (s *Service) SetPutawayTaskPriority(ctx context.Context, actor Actor, id int64, in TaskPriorityInput) error {
	priority, err := validatePriority(&in)
	if err != nil {
		return err
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		t, err := s.repo.FindTaskByID(ctx, id)
		if err != nil {
			return err
		}
		if t == nil {
			return response.NewError(ErrPutawayTaskNotFound, nil)
		}
		// 数据权限 fail-closed（f16）：越仓任务按不存在处理（与 GetTask 详情同口径）。
		if !actor.canAccessWarehouse(t.TargetWarehouseID) {
			return response.NewError(ErrPutawayTaskNotFound, nil)
		}
		if putawayPriorityGuarded(t.Status) {
			return response.NewError(ErrPutawayStatusNotAllowed, map[string]any{
				"status": t.Status, "reason": "已完成/已取消任务不可调整优先级",
			})
		}
		n, err := s.repo.SetPutawayTaskPriority(ctx, tx, id, priority, actor.UserID)
		if err := guardRows(n, err); err != nil {
			if errors.Is(err, errGuardMiss) {
				return response.NewError(ErrStatusConflict, map[string]any{"reason": "任务状态并发变化，请刷新重试"})
			}
			return err
		}
		e := actor.auditEntry("putaway_task", id, "priority")
		e.After = map[string]any{"priority": priority, "status": t.Status}
		return middlewareAudit(tx, e)
	})
	if err != nil {
		return err
	}
	return nil
}
