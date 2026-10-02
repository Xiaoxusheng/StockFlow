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

export const inboundApi = {
  /** 入库单列表（预置端点，后端未就绪时页面呈现统一错误态） */
  list: (query: InboundQuery) =>
    http.get<PageResult<InboundItem>>('/api/inbounds', { params: query }),
}
