import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { SalesId } from './sales'

// ---------- 入库单（后端 M2 已交付：internal/purchase/purchase.go:50-55，
// 出参为 InboundOrder GORM 模型 snake_case，internal/purchase/models.go:164-202） ----------
//
// 状态为大写枚举（models.go:27-33），渲染前经 toStatusKey 归一后走 SfStatusTag。

/** 入库单状态（internal/purchase/models.go:27-33 迁移 CHECK 同源：
 * DRAFT→RECEIVING→AWAITING_QC→AWAITING_PUTAWAY→COMPLETED，差额关闭 CLOSED，取消 CANCELLED） */
export type InboundOrderStatus =
  | 'DRAFT'
  | 'RECEIVING'
  | 'AWAITING_QC'
  | 'AWAITING_PUTAWAY'
  | 'COMPLETED'
  | 'CANCELLED'
  | 'CLOSED'

/** 入库来源类型（models.go:49-50 迁移 CHECK：PURCHASE 采购入库 / OTHER 其他入库） */
export type InboundSourceType = 'PURCHASE' | 'OTHER'

/** 入库单列表筛选（handler.go:220-242：keyword/status/source_type/source_no/warehouse_id） */
export interface InboundOrderQuery extends PageQuery {
  keyword?: string
  status?: InboundOrderStatus
  source_type?: InboundSourceType
  source_no?: string
  warehouse_id?: SalesId
}

/** 入库单（InboundOrder，models.go:164-178 字段全量） */
export interface InboundOrder {
  id: SalesId
  /** 入库单号（business-flow.md §13.1：IN-日期-流水） */
  inbound_no: string
  source_type: InboundSourceType
  /** 来源单号（采购单号等） */
  source_no: string
  warehouse_id: number
  status: InboundOrderStatus
  received_at: string | null
  inspected_at: string | null
  putaway_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
  /** database.ID 序列化为字符串（BaseModel，database/model.go:122-128） */
  created_by: string
  updated_by: string
}

/** 入库单明细（InboundItem，models.go:187-199；qty_received/qty_inspected/qty_putaway
 * 承载收货→质检→上架三段进度，business-flow.md §3.3 支持部分收货） */
export interface InboundOrderItem {
  id: SalesId
  inbound_id: number
  line_no: number
  sku_id: number
  qty: number
  qty_received: number
  qty_inspected: number
  qty_putaway: number
  remark: string
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 入库单详情（GET /api/inbounds/{id}，service_inbound.go:336-339 返回 {order, items}） */
export interface InboundOrderDetail {
  order: InboundOrder
  items: InboundOrderItem[]
}

/** 明细行入参（InboundItemInput，service_inbound.go:25-30：同 SKU 合并单行） */
export interface InboundLineInput {
  sku_id: number
  qty: number
  remark?: string
}

/** 创建入参（InboundCreateInput，service_inbound.go:32-37） */
export interface InboundCreatePayload {
  source_type: InboundSourceType
  source_no?: string
  warehouse_id: number
  remark?: string
  items: InboundLineInput[]
}

/** 草稿编辑入参（InboundUpdateInput，service_inbound.go:39-42：仅 remark + 明细整单替换） */
export interface InboundUpdatePayload {
  remark?: string
  items?: InboundLineInput[]
}

/** 差额关闭入参（InboundCloseInput，service_inbound.go:46-49；RECEIVING→CLOSED，原因必填） */
export interface InboundClosePayload {
  reason: string
}

// ---------- 权限码（internal/auth/permissions.go:145-150 三段式冻结） ----------

export const INBOUND_CREATE_PERMISSION = 'purchase:inbound:create'
export const INBOUND_UPDATE_PERMISSION = 'purchase:inbound:update'
export const INBOUND_CANCEL_PERMISSION = 'purchase:inbound:cancel'
export const INBOUND_CLOSE_PERMISSION = 'purchase:inbound:close'

export const inboundApi = {
  list: (query: InboundOrderQuery) =>
    http.get<PageResult<InboundOrder>>('/api/inbounds', { params: query }),
  create: (payload: InboundCreatePayload) => http.post<InboundOrder>('/api/inbounds', payload),
  detail: (id: SalesId) => http.get<InboundOrderDetail>(`/api/inbounds/${id}`),
  /** 草稿编辑（PUT /api/inbounds/{id}，仅 remark + 明细整单替换） */
  update: (id: SalesId, payload: InboundUpdatePayload) =>
    http.put<InboundOrder>(`/api/inbounds/${id}`, payload),
  cancel: (id: SalesId, payload?: { reason?: string }) =>
    http.post<InboundOrder>(`/api/inbounds/${id}/cancel`, payload),
  /** 差额关闭（RECEIVING→CLOSED，原因必填） */
  close: (id: SalesId, payload: InboundClosePayload) =>
    http.post<InboundOrder>(`/api/inbounds/${id}/close`, payload),
}
