import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 任务中心（F8：/workbench + /tasks；后端任务域 M1 未交付，以下为前端先行契约，
// 字段名待后端交付时以 Go JSON tag 为准对齐，对齐机制同 docs/tasks/current.md 遗留清单） ----------

/**
 * 任务类型（业务依据：上架 business-flow.md §5、拣货 §8.2、复核 §8.3、打包 §7.2、
 * 移库 frontend.md §21、盘点 frontend.md §10.5；后端枚举冻结前仅用于筛选传参，展示走页面标签映射兜底）
 */
export type TaskType =
  | 'putaway' // 上架任务
  | 'picking' // 拣货任务
  | 'checking' // 复核任务
  | 'packing' // 打包任务
  | 'moving' // 移库任务
  | 'counting' // 盘点任务

/** 任务状态（与 types/status.ts 注册表一致；上架等子流程状态由各域页面表达） */
export type TaskStatus = 'pending' | 'in_progress' | 'completed' | 'cancelled'

export interface TaskQuery extends PageQuery {
  keyword?: string
  taskType?: TaskType
  status?: TaskStatus
  warehouseCode?: string
}

export interface TaskItem {
  id: number | string
  /** 任务号（编号规则待后端冻结） */
  taskNo: string
  taskType: string
  /** 关联单据号（入库单 / 出库单 / 盘点单等） */
  sourceNo?: string
  warehouseName?: string
  /** 计划数量 */
  totalQty: number
  /** 已完成数量（作业支持部分完成，business-flow.md §3.3/§8.2） */
  completedQty: number
  status: string
  /** 负责人（我的任务 = 指派给当前用户的任务子集） */
  assigneeName?: string
  createdAt: string
  completedAt?: string
}

/** 我的工作台入口计数（frontend.md §15.1 四块；前端先行契约，后端交付时对齐字段名） */
export interface WorkbenchSummary {
  /** 我的待办 */
  todoCount: number
  /** 我的审批 */
  approvalCount: number
  /** 我的任务 */
  taskCount: number
  /** 我的异常 */
  exceptionCount: number
}

export const taskApi = {
  /** 工作台四块入口计数（预置端点，后端未就绪时页面呈现统一错误态） */
  summary: () => http.get<WorkbenchSummary>('/api/workbench/summary'),
  /** 我的任务列表（统一分页信封 page/pageSize/total/items，docs/api.md §2.1） */
  list: (query: TaskQuery) => http.get<PageResult<TaskItem>>('/api/tasks', { params: query }),
}
