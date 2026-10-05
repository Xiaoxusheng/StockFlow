import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 任务中心（F8：/workbench + /tasks；后端 2026-10-05 平台批已交付，契约对齐
// docs/api.md §9「平台批」节 + internal/reports/workbench_summary.go/workbench.go JSON tag） ----------
// 响应字段 snake_case（后端 DTO JSON tag 惯例）；列表筛选 query 参数 snake_case；
// 分页除外：page/pageSize 双端一致（internal/response/response.go ParsePage 读
// "page"/"pageSize"，types/api.ts PageQuery/PageResult 同形）；
// 枚举值域已冻结：task_type=putaway|picking|checking（packing/moving/counting 无独立
// 任务表不映射，传值 400）、status 统一五值 pending|in_progress|completed|cancelled|
// exception（picking/checking 的 EXCEPTION 原态映射第五值，raw_status 随行下发原态）。

/**
 * 任务类型（后端冻结三值：GET /api/tasks UNION 三任务表 putaway/pick/check——
 * internal/reports/workbench.go；packing/moving/counting 无独立任务表不映射，传值 400）
 */
export type TaskType = 'putaway' | 'picking' | 'checking'

/** 任务状态（统一五值，api.md §9 平台批；exception 为第五值——picking/checking 原态
 * EXCEPTION 映射；types/status.ts 注册表 exception 键同源） */
export type TaskStatus = 'pending' | 'in_progress' | 'completed' | 'cancelled' | 'exception'

export interface TaskQuery extends PageQuery {
  keyword?: string
  task_type?: TaskType
  status?: TaskStatus
  warehouse_code?: string
}

export interface TaskItem {
  id: number | string
  /** 任务号（编号规则待后端冻结） */
  task_no: string
  task_type: string
  /** 关联单据号（入库单 / 出库单 / 盘点单等） */
  source_no?: string
  warehouse_name?: string
  /** 计划数量 */
  total_qty: number
  /** 已完成数量（作业支持部分完成，business-flow.md §3.3/§8.2） */
  completed_qty: number
  status: string
  /** 原表态（putaway_tasks/pick_tasks/check_tasks 的 status 原值，随行下发——
   * 统一五值 status 之外的明细依据，2026-10-05 平台批） */
  raw_status: string
  /** 负责人（我的任务 = 指派给当前用户的任务子集） */
  assignee_name?: string
  created_at: string
  completed_at?: string
}

/**
 * 我的工作台入口计数（frontend.md §15.1 四块；2026-10-05 平台批交付，与
 * workbench_summary.go JSON tag 逐字段对齐）。共享裁决：四块互斥，
 * Σ(todo+approval+task+exception) ≠ Dashboard pendingTaskCount——pendingTask
 * （dashboard.go:613）= receive + 六作业块 + exception 不含 approval，即
 * Σ = pendingTaskCount + approval_count；前端 tooltip 须如实披露（api.md §9 收口披露节④）。
 */
export interface WorkbenchSummary {
  /** 我的待办 */
  todo_count: number
  /** 我的审批 */
  approval_count: number
  /** 我的任务 */
  task_count: number
  /** 我的异常 */
  exception_count: number
}

export const taskApi = {
  /** 工作台四块入口计数（预置端点，后端未就绪时页面呈现统一错误态） */
  summary: () => http.get<WorkbenchSummary>('/api/workbench/summary'),
  /** 我的任务列表（统一分页信封 page/pageSize/total/items，docs/api.md §2.1） */
  list: (query: TaskQuery) => http.get<PageResult<TaskItem>>('/api/tasks', { params: query }),
}
