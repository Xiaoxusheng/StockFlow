package reports

// 工作台汇总端点（2026-10-05 平台批，docs/api.md §9 契约先行；web/src/api/task.ts
// WorkbenchSummary 前端先行契约——该契约注释明示"端点交付时以 Go JSON tag 复核"，本文件即交付）：
//
//	GET /api/workbench/summary → { todo_count, approval_count, task_count, exception_count }
//
// 口径（蓝图裁决：复用 dashboardTodayRepo 同源聚合 dashboard.go:148-214，一次仓储查询
// 组装，dashboardTodayBundle 模式——与 GET /api/reports/dashboard/tasks 计数逐块一致）：
//   - todo_count      我的待办：采购单待收货（APPROVED/PARTIAL_RECEIVED 单据型待办）；
//   - approval_count  我的审批：五单据待审聚合（purchase_orders/sales_orders/transfer_orders/
//     inventory_adjustments PENDING_APPROVAL + count_orders PENDING_REVIEW）；
//   - task_count      我的任务：六作业块活动任务（putaway PENDING/IN_PROGRESS/PAUSED +
//     pick ALLOCATED/PICKING + check PICKED + pack CHECKED + ship PACKED + count
//     COUNTING/PENDING_REVIEW）；
//   - exception_count 我的异常：未闭环（NOT IN ('RESOLVED','CLOSED')；exceptions 无仓库列，
//     全量未闭环口径）。
// 四块互斥拆分，Σ≠Dashboard pendingTaskCount——dashboard.go:613 的 pendingTask 不含
// approval（= receive+六作业块+exception），Σ 比 pendingTaskCount 多 approval_count
// 一项（2026-10-05 收口位真库复算：四计数 3/3/2/0，Σ=8、pendingTaskCount=5）——
// 前端 tooltip 如实披露。
// 软删一致性：purchase_orders 内嵌 gorm.DeletedAt（迁移 000016），dashboardTodayRepo 的
//   count 计数走 GORM 模型自动过滤，与列表页可见集对齐。
// 只读约束：全部参数化 SELECT 零写语句（guard-readonly 红线，同 repository.go）。
// 权限：auth.PermInventoryList（GET /reports/dashboard/tasks 计数版已在同码暴露同一
//   信息面，汇总不抬高敏感面——routes.go:58 同码先例）。
// 数据范围：Scope 会话仓库快照（scopeOf），禁收前端范围参数；fail-closed 空集四计数为 0。

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/response"
)

// workbenchSummaryDTO 工作台四块入口计数（snake_case 契约）。
type workbenchSummaryDTO struct {
	// TodoCount 我的待办：采购单待收货（APPROVED/PARTIAL_RECEIVED 单据型待办）。
	TodoCount int64 `json:"todo_count"`
	// ApprovalCount 我的审批：五单据待审聚合（purchase/sales/transfer/inventory_
	// adjustments PENDING_APPROVAL + count_orders PENDING_REVIEW）。
	ApprovalCount int64 `json:"approval_count"`
	// TaskCount 我的任务：六作业块活动任务（putaway/pick/check/pack/ship/count）。
	TaskCount int64 `json:"task_count"`
	// ExceptionCount 我的异常：未闭环异常（NOT IN ('RESOLVED','CLOSED')；
	// exceptions 无仓库列，全量未闭环口径——dashboardTodayRepo 同源）。
	ExceptionCount int64 `json:"exception_count"`
}

// WorkbenchSummary 工作台四块入口计数（复用 dashboardTodayRepo 同源聚合——一次仓储
// 查询组装，dashboardTodayBundle 模式；计数口径与 GET /api/reports/dashboard/tasks 逐块一致）。
func (s *Service) WorkbenchSummary(ctx context.Context, sc Scope) (*workbenchSummaryDTO, error) {
	t, err := s.repo.dashboardTodayRepo(ctx, sc)
	if err != nil {
		return nil, err
	}
	return &workbenchSummaryDTO{
		TodoCount:      t.receive,
		ApprovalCount:  t.approval,
		TaskCount:      t.putaway + t.pick + t.check + t.pack + t.ship + t.count_,
		ExceptionCount: t.exception,
	}, nil
}

// workbenchSummary GET /api/workbench/summary。
//
// @Summary GET /api/workbench/summary
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/workbench/summary [get]
func (h *handler) workbenchSummary(c *gin.Context) {
	dto, err := h.svc.WorkbenchSummary(c.Request.Context(), scopeOf(c))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}
