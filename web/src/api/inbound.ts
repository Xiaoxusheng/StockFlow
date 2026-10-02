import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 入库管理（docs/api.md §1：领域前缀 /api/inbounds，子路径未冻结） ----------

/** 入库类型（business-flow.md §3.1；后端枚举冻结前仅用于筛选传参，展示走页面标签映射兜底） */
export type InboundType =
  | 'purchase' // 采购入库
  | 'production' // 生产入库
  | 'sales_return' // 销售退货入库
  | 'transfer' // 调拨入库
  | 'other' // 其他入库

/** 入库单状态主流程（business-flow.md §3.2 收货 → 质检 → 上架；后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type InboundStatus =
  | 'draft'
  | 'pending_receipt'
  | 'receiving'
  | 'received'
  | 'pending_inspection'
  | 'pending_putaway'
  | 'putaway_completed'
  | 'completed'
  | 'closed'
  | 'cancelled'

export interface InboundQuery extends PageQuery {
  keyword?: string
  inboundType?: InboundType
  status?: InboundStatus
  warehouseCode?: string
}

export interface InboundItem {
  id: number | string
  /** 入库单号（business-flow.md §13.1：IN-日期-流水） */
  inboundNo: string
  inboundType: string
  /** 来源单号（采购单 / 退货单 / 调拨单等） */
  sourceNo?: string
  /** 供应商（仅采购入库等有来源场景返回） */
  supplierName?: string
  warehouseCode?: string
  warehouseName: string
  /** 计划数量合计 */
  totalQty: number
  /** 已收货数量合计（business-flow.md §3.3 支持部分收货） */
  receivedQty: number
  status: string
  operatorName?: string
  createdAt: string
}

// ---------- 入库单详情（GET /api/inbounds/{id}，前端先行契约：后端入库单据域未交付，冻结后回对字段） ----------

/** 入库单商品明细行 */
export interface InboundDetailItem {
  id: number | string
  skuCode: string
  skuName?: string
  /** 计量单位 */
  unitName?: string
  /** 计划数量 */
  totalQty: number
  /** 已收货数量（business-flow.md §3.3 支持部分收货） */
  receivedQty?: number
  /** 批次 */
  batchNo?: string
  /** 目的库位 */
  binCode?: string
  status?: string
  remark?: string
}

/** 入库单详情：含明细与流程节点时间（business-flow.md §13.4） */
export interface InboundDetail {
  id: number | string
  inboundNo: string
  inboundType: string
  /** 来源单号（采购单 / 退货单 / 调拨单等） */
  sourceNo?: string
  supplierName?: string
  warehouseCode?: string
  warehouseName: string
  /** 计划数量合计 */
  totalQty: number
  /** 已收货数量合计 */
  receivedQty?: number
  status: string
  /** 创建人 */
  operatorName?: string
  /** 创建时间 */
  createdAt: string
  /** 审核时间 */
  reviewedAt?: string
  /** 审核人 */
  reviewedBy?: string
  /** 收货完成时间 */
  receivedAt?: string
  /** 收货人 */
  receivedBy?: string
  /** 质检完成时间 */
  inspectedAt?: string
  /** 质检人 */
  inspectedBy?: string
  /** 上架完成时间 */
  putawayAt?: string
  /** 上架人 */
  putawayBy?: string
  /** 备注 */
  remark?: string
  /** 商品明细 */
  items: InboundDetailItem[]
}

export const inboundApi = {
  /** 入库单列表（预置端点，后端未就绪时页面呈现统一错误态） */
  list: (query: InboundQuery) =>
    http.get<PageResult<InboundItem>>('/api/inbounds', { params: query }),
  /** 入库单详情（前端先行契约，后端未交付时页面呈现统一错误态） */
  get: (id: InboundItem['id']) =>
    http.get<InboundDetail>(`/api/inbounds/${id}`),
}
