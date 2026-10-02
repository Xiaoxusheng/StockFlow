import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 销售域（前端先行骨架：backend-m1-plan「阶段 9–13」行——单据域契约 M2 冻结；
// 端点为任务组指定路径，后端未交付时页面呈统一错误态） ----------

/** ID 序列化约定对齐 purchase.ts：保留 number 兼容字符串返回 */
export type SalesId = number | string

/** 销售订单状态（business-flow.md §6.2 订单 → 审核 → 库存预占 → 出库；
 * 后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type SalesOrderStatus =
  | 'draft'
  | 'pending_review'
  | 'approved'
  | 'processing'
  | 'completed'
  | 'cancelled'

export interface SalesOrderQuery extends PageQuery {
  keyword?: string
  status?: SalesOrderStatus
  warehouseCode?: string
}

export interface SalesOrderItem {
  id: SalesId
  /** 销售单号（business-flow.md §13.1 编号规则引擎；SO 前缀前端先行提案，待后端冻结） */
  soNo: string
  customerCode?: string
  customerName: string
  warehouseCode?: string
  warehouseName: string
  /** 商品数量合计 */
  totalQty: number
  /** 金额合计（business-flow.md §6.1 字段：数量/单价/金额）；草稿单可能缺失 */
  totalAmount?: number
  status: string
  createdAt: string
}

/** 销售出库单状态（business-flow.md §7.2 销售订单 → 库存分配 → 拣货 → 复核 → 打包 → 发货；
 * 后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type SalesOutboundStatus =
  | 'pending_allocate'
  | 'allocated'
  | 'picking'
  | 'picked'
  | 'checking'
  | 'packing'
  | 'packed'
  | 'shipped'
  | 'completed'
  | 'cancelled'

export interface SalesOutboundQuery extends PageQuery {
  keyword?: string
  status?: SalesOutboundStatus
  warehouseCode?: string
}

export interface SalesOutboundItem {
  id: SalesId
  /** 出库单号（business-flow.md §13.1：OUT-日期-流水） */
  outNo: string
  /** 关联销售单号 */
  soNo?: string
  customerName?: string
  warehouseCode?: string
  warehouseName: string
  totalQty: number
  /** 已发货数量合计（库存正式扣减发生在发货完成时，business-flow.md §7.2） */
  shippedQty?: number
  status: string
  createdAt: string
}

/** 销售退货单状态（business-flow.md §9.1 退货申请 → 审核 → 收货 → 质检 → 正常库存/不良品；
 * 后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type SalesReturnStatus =
  | 'draft'
  | 'pending_review'
  | 'approved'
  | 'receiving'
  | 'pending_inspection'
  | 'inspected'
  | 'completed'
  | 'cancelled'

export interface SalesReturnQuery extends PageQuery {
  keyword?: string
  status?: SalesReturnStatus
  warehouseCode?: string
}

export interface SalesReturnItem {
  id: SalesId
  /** 退货单号（business-flow.md §13.1 前缀枚举未含退货，字段名前端先行提案） */
  returnNo: string
  /** 关联销售单号 */
  soNo?: string
  customerName?: string
  warehouseCode?: string
  warehouseName: string
  totalQty: number
  /** 已收货数量合计（质检决定入库去向：合格入正常库存 / 不合格入不良品） */
  receivedQty?: number
  status: string
  createdAt: string
}

// ---------- 销售订单创建 / 详情（前端先行契约：POST /api/sales-orders、GET /api/sales-orders/{id}；
// 后端单据域 M2 冻结后回对。字段依据 business-flow.md §6.1：客户、商品、数量、单价、金额、仓库、收货地址、配送方式、备注） ----------

/** 新建销售订单按钮权限码（前端先行：资源段对齐 config/menu.tsx「sales:view」，动作词对齐
 * internal/auth/permissions.go 动作枚举 create；后端销售域权限点冻结后回对，未持有点 fail-closed 隐藏） */
export const SALES_ORDER_CREATE_PERMISSION = 'sales:create'

/** 明细行（创建载荷）：商品标识前端先行采用 SKU 编码（/api/skus M1 冻结契约），后端冻结后若改 skuId 在此回对 */
export interface SalesOrderLineInput {
  skuCode: string
  qty: number
  unitPrice: number
  remark?: string
}

export interface SalesOrderCreatePayload {
  /** 客户编码（/api/customers M1 冻结契约） */
  customerCode: string
  /** 仓库编码（/api/warehouses M1 冻结契约） */
  warehouseCode: string
  /** 收货地址（business-flow.md §6.1） */
  shippingAddress?: string
  /** 配送方式（business-flow.md §6.1；值域后端冻结前为前端先行提案） */
  deliveryType?: string
  lines: SalesOrderLineInput[]
  remark?: string
}

/** 明细行（详情返回）：金额 = 数量 × 单价，以后端计算为准 */
export interface SalesOrderLine {
  id: SalesId
  skuCode: string
  productName?: string
  unitName?: string
  qty: number
  unitPrice?: number
  amount?: number
  remark?: string
}

/** 销售订单详情（头 + 明细 + 状态）；时间字段命名对齐 purchase.ts PurchaseDetail 模式（§13.4 业务时间字段） */
export interface SalesOrderDetail extends SalesOrderItem {
  customerCode?: string
  shippingAddress?: string
  deliveryType?: string
  /** 创建人 */
  operatorName?: string
  /** 审核时间 / 审核人（business-flow.md §6.2：订单 → 审核 → 库存预占 → 出库） */
  reviewedAt?: string
  reviewedBy?: string
  /** 库存预占时间（审核通过即触发预占，inventory-rules.md §4） */
  allocatedAt?: string
  completedAt?: string
  cancelledAt?: string
  remark?: string
  items?: SalesOrderLine[]
}

export const salesApi = {
  orders: {
    list: (query: SalesOrderQuery) =>
      http.get<PageResult<SalesOrderItem>>('/api/sales-orders', { params: query }),
    /** 创建（前端先行：后端单据域未交付时页面呈统一错误态） */
    create: (payload: SalesOrderCreatePayload) =>
      http.post<unknown>('/api/sales-orders', payload),
    /** 详情（头 + 明细 + 状态，前端先行契约） */
    detail: (id: SalesId) => http.get<SalesOrderDetail>(`/api/sales-orders/${id}`),
  },
  outbounds: {
    list: (query: SalesOutboundQuery) =>
      http.get<PageResult<SalesOutboundItem>>('/api/sales/outbounds', { params: query }),
  },
  returns: {
    list: (query: SalesReturnQuery) =>
      http.get<PageResult<SalesReturnItem>>('/api/sales/returns', { params: query }),
  },
}
