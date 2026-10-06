package reports

// 工作台汇总端点（2026-10-05 平台批，docs/api.md §9 契约先行；web/src/api/task.ts
// WorkbenchSummary 前端先行契约——该契约注释明示"端点交付时以 Go JSON tag 复核"，本文件即交付）：
//
//	GET /api/workbench/summary → { todo_count, approval_count, task_count, exception_count,
//	                               mine_count, timeout_count, today_completed_count }
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
//   - mine_count            待我处理（2026-10-06 效率层一期 B3 增量）：本人统一态 in_progress
//     任务数（putaway IN_PROGRESS/PAUSED + pick CLAIMED/PICKING，与 /api/tasks?status=
//     in_progress 同一口径；check 无 CLAIMED 进行中态不计入）。
//   - timeout_count         超时：上述本人进行中任务里 created_at 早于超时截止线的行数
//     （putaway/picking 两类——阈值 system_configs task.timeout.putaway_hours/pick_hours，
//     缺省 4 小时，sysops/configs.go 冻结 seed 键；本包只读直查同值；checking 无超时层）。
//   - today_completed_count 今日已完成：本人今日完成的任务数（putaway COMPLETED×completed_at、
//     pick PICKED×picked_at、check DONE×done_at，均 >= 本地零点）。
//
// 三计数均为 additive 增量（键名对齐既有 snake_case 命名），一次 SQL 标量子查询聚合；
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
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/stockflow/server/internal/auth"
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
	// MineCount 待我处理（2026-10-06 效率层一期 B3 增量）：本人统一态 in_progress 任务
	// （putaway IN_PROGRESS/PAUSED + pick CLAIMED/PICKING；/api/tasks?status=in_progress 同口径）。
	MineCount int64 `json:"mine_count"`
	// TimeoutCount 超时：本人进行中且 created_at 早于超时截止线的任务数
	// （putaway/picking，阈值 task.timeout.* 可配，缺省 4 小时；checking 无超时层）。
	TimeoutCount int64 `json:"timeout_count"`
	// TodayCompletedCount 今日已完成：本人今日完成任务数
	// （putaway COMPLETED×completed_at + pick PICKED×picked_at + check DONE×done_at）。
	TodayCompletedCount int64 `json:"today_completed_count"`
}

// myTaskCountsRow 三增量计数行（单条 SQL 标量子查询聚合；gorm 列名映射）。
type myTaskCountsRow struct {
	MineCount           int64 `gorm:"column:mine_count"`
	TimeoutCount        int64 `gorm:"column:timeout_count"`
	TodayCompletedCount int64 `gorm:"column:today_completed_count"`
}

// myTaskCountsBase 三增量计数（putaway/scope 列=target_warehouse_id、pick/check=warehouse_id；
// 超时截止线与今日零点为参数；%1-%6 为 Scope 片段——代码常量拼接面，归因占位符为字面 ?）。
const myTaskCountsBase = `
SELECT
  (SELECT COUNT(*) FROM putaway_tasks pt WHERE pt.claimed_by = ? AND pt.status IN ('IN_PROGRESS','PAUSED') AND %s) +
  (SELECT COUNT(*) FROM pick_tasks pk WHERE pk.assignee_id = ? AND pk.status IN ('CLAIMED','PICKING') AND %s)
  AS mine_count,
  (SELECT COUNT(*) FROM putaway_tasks pt WHERE pt.claimed_by = ? AND pt.status IN ('IN_PROGRESS','PAUSED') AND pt.created_at <= ? AND %s) +
  (SELECT COUNT(*) FROM pick_tasks pk WHERE pk.assignee_id = ? AND pk.status IN ('CLAIMED','PICKING') AND pk.created_at <= ? AND %s)
  AS timeout_count,
  (SELECT COUNT(*) FROM putaway_tasks pt WHERE pt.claimed_by = ? AND pt.status = 'COMPLETED' AND pt.completed_at >= ? AND %s) +
  (SELECT COUNT(*) FROM pick_tasks pk WHERE pk.assignee_id = ? AND pk.status = 'PICKED' AND pk.picked_at >= ? AND %s) +
  (SELECT COUNT(*) FROM check_tasks ck WHERE ck.assignee_id = ? AND ck.status = 'DONE' AND ck.done_at >= ? AND %s)
  AS today_completed_count`

// myWorkbenchCounts 本人任务三增量计数（单次往返；软删：任务表无软删列——000016 注）。
func (r *repository) myWorkbenchCounts(ctx context.Context, sc Scope, userID int64, pickCutoff, putawayCutoff, todayStart time.Time) (*myTaskCountsRow, error) {
	putCond, putArgs := sc.cond("pt.target_warehouse_id")
	pickCond, pickArgs := sc.cond("pk.warehouse_id")
	checkCond, checkArgs := sc.cond("ck.warehouse_id")
	sql := fmt.Sprintf(myTaskCountsBase,
		putCond, pickCond, // mine
		putCond, pickCond, // timeout
		putCond, pickCond, checkCond) // today completed
	args := []any{userID}
	args = append(args, putArgs...)
	args = append(args, userID)
	args = append(args, pickArgs...)
	args = append(args, userID, putawayCutoff)
	args = append(args, putArgs...)
	args = append(args, userID, pickCutoff)
	args = append(args, pickArgs...)
	args = append(args, userID, todayStart)
	args = append(args, putArgs...)
	args = append(args, userID, todayStart)
	args = append(args, pickArgs...)
	args = append(args, userID, todayStart)
	args = append(args, checkArgs...)
	row := &myTaskCountsRow{}
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(row).Error; err != nil {
		return nil, fmt.Errorf("reports: 工作台个人计数查询失败: %w", err)
	}
	return row, nil
}

// WorkbenchSummary 工作台入口计数（既有四块复用 dashboardTodayRepo 同源聚合；三增量
// 计数走 myWorkbenchCounts 单查询——mine/timeout/today_completed）。
func (s *Service) WorkbenchSummary(ctx context.Context, sc Scope, userID int64) (*workbenchSummaryDTO, error) {
	t, err := s.repo.dashboardTodayRepo(ctx, sc)
	if err != nil {
		return nil, err
	}
	pickCutoff, putawayCutoff, err := s.repo.taskTimeoutCutoffs(ctx, s.now())
	if err != nil {
		return nil, err
	}
	mine, err := s.repo.myWorkbenchCounts(ctx, sc, userID, pickCutoff, putawayCutoff, todayStart(s.now()))
	if err != nil {
		return nil, err
	}
	return &workbenchSummaryDTO{
		TodoCount:           t.receive,
		ApprovalCount:       t.approval,
		TaskCount:           t.putaway + t.pick + t.check + t.pack + t.ship + t.count_,
		ExceptionCount:      t.exception,
		MineCount:           mine.MineCount,
		TimeoutCount:        mine.TimeoutCount,
		TodayCompletedCount: mine.TodayCompletedCount,
	}, nil
}

// todayStart 本地零点（今日已完成口径）。
func todayStart(now time.Time) time.Time {
	y, m, d := now.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// workbenchSummary GET /api/workbench/summary。
//
// @Summary GET /api/workbench/summary
// @Tags 报表
// @Produce json
// @Success 200 {object} response.Envelope "统一响应信封"
// @Router /api/workbench/summary [get]
func (h *handler) workbenchSummary(c *gin.Context) {
	uc, ok := auth.CurrentUser(c)
	if !ok {
		// RequirePerm 已保证认证上下文，缺失为编程错误防御分支 fail-closed（myTasks 同口径）。
		response.Err(c, response.NewError(response.CodeUnauthorized, nil))
		return
	}
	dto, err := h.svc.WorkbenchSummary(c.Request.Context(), scopeOf(c), uc.UserID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, dto)
}
