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

// ---------- 采购订单详情（GET /api/purchases/{id}，前端先行契约：后端采购单据域未交付，冻结后回对字段） ----------

/** 采购订单商品明细行（business-flow.md §2.3：明细保留四数量字段，收货时后端强校验） */
export interface PurchaseDetailItem {
  id: number | string
  skuCode: string
  skuName?: string
  /** 计量单位 */
  unitName?: string
  /** 原始数量 */
  totalQty: number
  /** 累计收货数量（含合格 + 不合格待定，不得超过原始数量） */
  receivedQty?: number
  /** 待到货数量 */
  pendingQty?: number
  /** 拒收数量（单独记录） */
  rejectedQty?: number
  /** 单价 */
  unitPrice?: number
  /** 金额 */
  amount?: number
  remark?: string
}

/** 采购单关联收货单（部分收货进度展示用） */
export interface PurchaseRelatedReceipt {
  id: number | string
  receiptNo: string
  /** 本次收货数量 */
  receivedQty?: number
  status?: string
}

/** 采购订单详情：含明细与流程节点时间（business-flow.md §2.2/§2.3、§13.4） */
export interface PurchaseDetail {
  id: number | string
  poNo: string
  supplierCode?: string
  supplierName: string
  warehouseCode?: string
  warehouseName: string
  /** 原始数量合计 */
  totalQty: number
  /** 已收货数量合计 */
  receivedQty?: number
  /** 金额合计；草稿单可能缺失 */
  totalAmount?: number
  expectedArrivalDate?: string
  status: string
  /** 创建人 */
  operatorName?: string
  /** 创建时间 */
  createdAt: string
  /** 审核时间 */
  reviewedAt?: string
  /** 审核人 */
  reviewedBy?: string
  /** 收货完成时间（部分收货时为最后收货时间） */
  receivedAt?: string
  /** 收货人 */
  receivedBy?: string
  /** 完成时间 */
  completedAt?: string
  /** 完成操作人 */
  completedBy?: string
  /** 备注 */
  remark?: string
  /** 商品明细 */
  items: PurchaseDetailItem[]
  /** 关联收货单（部分收货进度，未产生收货时缺失） */
  receipts?: PurchaseRelatedReceipt[]
}

export const purchaseApi = {
  list: (query: PurchaseQuery) =>
    http.get<PageResult<PurchaseItem>>('/api/purchases', { params: query }),
  /** 采购订单详情（前端先行契约，后端未交付时页面呈现统一错误态） */
  get: (id: PurchaseItem['id']) =>
    http.get<PurchaseDetail>(`/api/purchases/${id}`),
  receipts: {
    list: (query: ReceiptQuery) =>
      http.get<PageResult<ReceiptItem>>('/api/purchases/receipts', { params: query }),
  },
  returns: {
    list: (query: PurchaseReturnQuery) =>
      http.get<PageResult<PurchaseReturnItem>>('/api/purchases/returns', { params: query }),
  },
}
