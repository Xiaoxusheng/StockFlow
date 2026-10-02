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

// ---------- 收货单（/api/purchases/receipts，前端先行骨架：backend-m1-plan 单据域契约 M2 冻结） ----------

/** 收货单状态（business-flow.md §2.1/§3.2 到货 → 收货 → 质检 → 上架 → 入库完成；
 * 后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type ReceiptStatus =
  | 'receiving'
  | 'pending_inspection'
  | 'inspecting'
  | 'inspected'
  | 'pending_putaway'
  | 'putaway_completed'
  | 'cancelled'

export interface ReceiptQuery extends PageQuery {
  keyword?: string
  status?: ReceiptStatus
  warehouseCode?: string
}

export interface ReceiptItem {
  id: number | string
  /** 收货单号（business-flow.md §13.1 前缀枚举未含收货，字段名前端先行提案） */
  receiptNo: string
  /** 关联采购单号（business-flow.md §13.1：PO-日期-流水） */
  poNo?: string
  supplierName: string
  warehouseCode?: string
  warehouseName: string
  /** 应收数量合计（business-flow.md §2.3：累计收货不得超过原始数量） */
  totalQty: number
  /** 已收数量合计（含合格 + 不合格待定） */
  receivedQty: number
  status: string
  createdAt: string
}

// ---------- 采购退货单（/api/purchases/returns，前端先行骨架：契约 M2 冻结） ----------

/** 采购退货单状态（business-flow.md §9.2 退货申请 → 审核 → 退货出库 → 供应商；
 * 后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type PurchaseReturnStatus =
  | 'draft'
  | 'pending_review'
  | 'approved'
  | 'processing'
  | 'completed'
  | 'cancelled'

export interface PurchaseReturnQuery extends PageQuery {
  keyword?: string
  status?: PurchaseReturnStatus
  warehouseCode?: string
}

export interface PurchaseReturnItem {
  id: number | string
  /** 退货单号（business-flow.md §13.1 前缀枚举未含退货，字段名前端先行提案） */
  returnNo: string
  /** 关联采购单号 */
  poNo?: string
  supplierName: string
  warehouseCode?: string
  warehouseName: string
  /** 退货数量合计 */
  totalQty: number
  /** 已退货数量合计 */
  returnedQty?: number
  status: string
  createdAt: string
}

export const purchaseApi = {
  list: (query: PurchaseQuery) =>
    http.get<PageResult<PurchaseItem>>('/api/purchases', { params: query }),
  receipts: {
    list: (query: ReceiptQuery) =>
      http.get<PageResult<ReceiptItem>>('/api/purchases/receipts', { params: query }),
  },
  returns: {
    list: (query: PurchaseReturnQuery) =>
      http.get<PageResult<PurchaseReturnItem>>('/api/purchases/returns', { params: query }),
  },
}
