import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 任务中心（F8：/workbench + /tasks；后端任务域 M1/M2 未交付，以下为前端先行契约） ----------
// 命名已对齐后端惯例（无实际错位，端点未上线故调用 404 → 页面统一错误态，requirements.md §10）：
// - 响应字段 snake_case（后端 DTO JSON tag 惯例，grep internal/ 实测 source_no/serial_no 等）；
// - 列表筛选 query 参数 snake_case（后端 c.Query 惯例：warehouse_id/source_no 等，grep 实测）；
// - 分页除外：page/pageSize 双端一致（internal/response/response.go:54-62 ParsePage 读
//   "page"/"pageSize"，types/api.ts PageQuery/PageResult 同形，共享文件本轮不动）；
// - 枚举值域后端 dto/迁移 CHECK 冻结前保持现值，未知值由页面回退展示原始值；
//   端点交付时以 Go JSON tag / CHECK 逐字段复核（对齐机制同 docs/tasks/current.md 遗留清单）。

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
  /** 负责人（我的任务 = 指派给当前用户的任务子集） */
  assignee_name?: string
  created_at: string
  completed_at?: string
}

/** 我的工作台入口计数（frontend.md §15.1 四块；前端先行契约，端点交付时以 Go JSON tag 复核） */
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
