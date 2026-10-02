import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 采购订单（docs/api.md §1：领域前缀 /api/purchases，子路径未冻结） ----------

/** 采购订单状态（business-flow.md §2.2 状态机；后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type PurchaseStatus =
  | 'draft'
  | 'pending_review'
  | 'approved'
  | 'partially_received'
  | 'received'
  | 'completed'
  | 'cancelled'

export interface PurchaseQuery extends PageQuery {
  keyword?: string
  status?: PurchaseStatus
  warehouseCode?: string
}

export interface PurchaseItem {
  id: number | string
  /** 采购单号（business-flow.md §13.1：PO-日期-流水） */
  poNo: string
  supplierCode?: string
  supplierName: string
  warehouseCode?: string
  warehouseName: string
  /** 原始数量合计（business-flow.md §2.3 四数量字段之一，明细级校验收货时做） */
  totalQty: number
  /** 已收货数量合计 */
  receivedQty: number
  /** 金额合计；草稿单可能缺失 */
  totalAmount?: number
  expectedArrivalDate?: string
  status: string
  createdAt: string
}

export const purchaseApi = {
  list: (query: PurchaseQuery) =>
    http.get<PageResult<PurchaseItem>>('/api/purchases', { params: query }),
}
