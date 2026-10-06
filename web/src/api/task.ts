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
  /** 超时任务数（2026-10-06 效率层一期 additive 增量；键名对齐 workbench_summary.go） */
  timeout_count?: number
  /** 指派给我且进行中的任务数（同上前缀增量） */
  mine_count?: number
  /** 今日已完成任务数（同上） */
  today_completed_count?: number
}

/**
 * 最近操作行（GET /api/workbench/recent-operations，api.md §9 效率层节）：
 * 读本人 operation_logs 尾 N 条——写入链路零新增（仍仅 middleware.Audit），
 * 「最近访问」属导航态走 user_preferences.recent_visits，不入本表（避免污染 append-only 审计）。
 */
export interface RecentOperationItem {
  /** YYYY-MM-DD HH:mm:ss */
  time: string
  action: string
  module: string
  object_type?: string
  object_id?: string
  success: boolean
  error_code?: string
  request_id?: string
}

/**
 * 自动下一条任务类型（GET /api/tasks/next 白名单五值，api.md §9 效率层节）：
 * 前三者复用 /api/tasks 三分支字段映射与五值映射；receipt→inbound_orders 状态
 * RECEIVING 候选池、exception→exceptions 状态 OPEN/处理中候选池（二者无独立任务表、
 * 无 priority 列，排序仅 created_at ASC——api.md §9 口径披露）。
 */
export type NextTaskType = TaskType | 'receipt' | 'exception'

export interface NextTaskQuery {
  task_type: NextTaskType
  /** 与数据权限 scopeOf 求交收窄（exceptions 表无仓库列，对该分支不生效——api.md §9 明示） */
  warehouse_id?: number | string
  /** 当前任务 ID：结果恒排除（防「下一条 = 自己」） */
  current_task_id?: number | string
}

/**
 * 自动下一条响应（NextTaskResult，api.md §9）：
 * has_next=false 时 task 为 null（无候选 → 页面给出「暂无待处理任务」真实反馈，不造假任务）。
 */
export interface NextTaskResult {
  has_next: boolean
  task: TaskItem | null
}

/**
 * 任务优先级选项（0–9，效率层一期 B3 迁移 000023 CHECK 值域；0=默认/不优先）。
 * 消费方：拣货/复核/上架列表的「优先级」列行内设置（PUT …/priority）。
 */
export const TASK_PRIORITY_OPTIONS = Array.from({ length: 10 }, (_, i) => ({
  label: String(i),
  value: i,
}))

/**
 * 优先处理明细行（GET /api/workbench/priorities，internal/reports/workbench_priority.go
 * priorityItem JSON tag 同源）：四组统一行形状，Detail 为后端组装的人读文本；
 * Qty 数量类明细（近效期现存量）缺省不下发；Status 仅异常组携原态
 * （OPEN/ASSIGNED/PROCESSING/PENDING_REVIEW，EXCEPTION_STATUS_TAG 渲染）；
 * Time 时间锚点（近效期行无自然时间锚点为 null）。
 */
export interface WorkbenchPriorityItem {
  id: number | string
  title: string
  subtitle: string
  detail: string
  qty?: number
  status?: string
  time?: string | null
}

/** 优先处理单组：count 全量计数 + items 尾 N 条（TopN 截断，count ≠ items.length） */
export interface WorkbenchPriorityGroup {
  count: number
  items: WorkbenchPriorityItem[]
}

/**
 * 优先处理四组统计（GET /api/workbench/priorities?limit=，口径见后端文件头注——
 * 超时收货阈值 task.timeout.receive_hours 族缺省 4h；库位异常不受仓库范围过滤；
 * 临期窗口 = inventory.alert.expiry_days 最大档，与 /inventory/alerts?level=near_expiry
 * 同构；待复核订单 = 存在 PENDING 复核任务的出库单去重计数）。
 */
export interface WorkbenchPriorities {
  overdue_receipts: WorkbenchPriorityGroup
  bin_exceptions: WorkbenchPriorityGroup
  near_expiry_stock: WorkbenchPriorityGroup
  pending_checks: WorkbenchPriorityGroup
}

export const taskApi = {
  /** 工作台四块入口计数（预置端点，后端未就绪时页面呈现统一错误态） */
  summary: () => http.get<WorkbenchSummary>('/api/workbench/summary'),
  /** 我的任务列表（统一分页信封 page/pageSize/total/items，docs/api.md §2.1） */
  list: (query: TaskQuery) => http.get<PageResult<TaskItem>>('/api/tasks', { params: query }),
  /**
   * 下一条任务（GET /api/tasks/next，计划 §2.4；排序由后端真实 SQL ORDER BY 承担，
   * 严禁前端推算；候选池=(本人已领取且进行中) OR (PENDING 未领取)——B7 裁决）
   */
  next: (query: NextTaskQuery) => http.get<NextTaskResult>('/api/tasks/next', { params: query }),
  /** 我的最近操作（GET /api/workbench/recent-operations，本人 operation_logs 尾 N 条，只读） */
  recentOperations: (limit?: number) =>
    http.get<{ items: RecentOperationItem[] }>('/api/workbench/recent-operations', {
      params: limit ? { limit } : undefined,
    }),
  /** 优先处理四组统计（GET /api/workbench/priorities，limit=每组明细条数 缺省 5 上限 20） */
  priorities: (limit?: number) =>
    http.get<WorkbenchPriorities>('/api/workbench/priorities', {
      params: limit ? { limit } : undefined,
    }),
}
