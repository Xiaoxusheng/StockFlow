import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 上架任务（后端已交付：GET /api/putaway，internal/purchase/purchase.go:71-75；
// 路由：POST /{id}/claim 领取原子抢占（service_putaway.go:171-201）、POST /{id}/execute
// IN_PROGRESS→COMPLETED 触发 Putaway 落账（service_putaway.go:207-343）） ----------
//
// 出参为 PutawayTask GORM 模型 snake_case（models.go:246-267）；主键 database.ID 序列化为
// 字符串，FK（sku_id/batch_id/target_*_id/claimed_by）为 int64 裸数字，qty 为 stock.Qty
// 裸数字（numeric(18,4)，internal/stock/qty.go:72-74 MarshalJSON 直出小数字面量）。
// 自 views/pad/putaway/PadPutawayPage.tsx 页内封装原样迁出（AGENTS.md 规则 6 API 层统一）。

export type PutawayTaskId = number | string

/** 上架任务状态（models.go:40-44 迁移 chk_putaway_tasks_status 同源；
 * PENDING→IN_PROGRESS 领取原子抢占，IN_PROGRESS→COMPLETED 上架确认，putawayTransitions） */
export type PutawayTaskStatus = 'PENDING' | 'IN_PROGRESS' | 'COMPLETED' | 'CANCELLED'

/** from_state（models.go:60-63：available 免检直通 / pending_inspect 经检待检，
 * 决定执行时 PutawayOp.RequireInspect） */
export type PutawayFromState = 'available' | 'pending_inspect'

/** 上架任务（PutawayTask，models.go:246-267 字段全量） */
export interface PutawayTask {
  id: PutawayTaskId
  /** 上架单号（PW-日期-流水，business-flow.md §13.1） */
  putaway_no: string
  inbound_no: string
  receipt_no: string
  sku_id: number
  /** 0=非批次 SKU */
  batch_id: number
  /** 序列号 SKU 单件任务 qty=1（inventory-rules.md §8.2） */
  serial_no: string
  qty: number
  from_state: PutawayFromState
  target_warehouse_id: number
  target_zone_id: number
  target_shelf_id: number
  target_bin_id: number
  status: PutawayTaskStatus
  claimed_by: number
  /** JSONTime：零值为 null */
  claimed_at: string | null
  completed_at: string | null
  remark: string
  /** 归因操作者（models.go:265-266，database.ID → JSON 字符串；系统操作为 "0"） */
  created_by: string
  updated_by: string
  created_at: string
  updated_at: string
}

/** 列表筛选（handler.go:464-491 query key：keyword/status/inbound_no/sku_id/from_state/warehouse_id） */
export interface PutawayTaskQuery extends PageQuery {
  keyword?: string
  status?: PutawayTaskStatus
  inbound_no?: string
  sku_id?: number
  from_state?: PutawayFromState
  warehouse_id?: number
}

/** 上架确认入参（PutawayExecuteInput，service_putaway.go:25-28：bin_id 可选=实际确认库位改指定） */
export interface PutawayExecutePayload {
  bin_id?: number
  remark?: string
}

/** 权限码（internal/auth/permissions.go:157-161 冻结三段式；前端仅体验优化，后端强校验） */
export const PUTAWAY_CLAIM_PERMISSION = 'purchase:putaway:claim'
export const PUTAWAY_EXECUTE_PERMISSION = 'purchase:putaway:execute'

export const putawayApi = {
  /** 上架任务列表（GET /api/putaway） */
  list: (query: PutawayTaskQuery) =>
    http.get<PageResult<PutawayTask>>('/api/putaway', { params: query }),
  /** 上架任务详情（GET /api/putaway/{id}） */
  detail: (id: PutawayTaskId) =>
    http.get<PutawayTask>(`/api/putaway/${encodeURIComponent(String(id))}`),
  /** 领取（POST /api/putaway/{id}/claim，PENDING→IN_PROGRESS 原子抢占） */
  claim: (id: PutawayTaskId) =>
    http.post<PutawayTask>(`/api/putaway/${encodeURIComponent(String(id))}/claim`),
  /** 上架确认（POST /api/putaway/{id}/execute，IN_PROGRESS→COMPLETED + 库存落账） */
  execute: (id: PutawayTaskId, payload: PutawayExecutePayload) =>
    http.post<PutawayTask>(`/api/putaway/${encodeURIComponent(String(id))}/execute`, payload),
}
