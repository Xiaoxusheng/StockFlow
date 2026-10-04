import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 销售订单 /api/sales（后端 M2 已交付：internal/sales/routes.go:73-80） ----------
//
// 出参为销售域 GORM 模型裸形态（internal/sales/models.go:14-51 JSON tag 全量 snake_case；
// ID 出参 database.ID 序列化为字符串，保留 number 兼容；数量/金额 stock.Qty 裸数字）。
// 状态为大写枚举（models.go:240-248），渲染前经 masterdata.ts toStatusKey 归一后走
// SfStatusTag 注册表（types/status.ts）。

/** ID 序列化约定对齐其他单据域：后端 database.ID 出参字符串，保留 number 兼容 */
export type SalesId = number | string

/** 销售订单状态（internal/sales/models.go:240-248 迁移 CHECK 同源） */
export type SalesOrderStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'REJECTED'
  | 'PARTIAL_SHIPPED'
  | 'SHIPPED_ALL'
  | 'COMPLETED'
  | 'CANCELLED'

/** 销售订单列表筛选（handler.go:100-131：status/customer_id/warehouse_id/so_no/created_from/created_to） */
export interface SalesOrderQuery extends PageQuery {
  status?: SalesOrderStatus
  customer_id?: SalesId
  warehouse_id?: SalesId
  /** 精确销售单号（handler.go:114） */
  so_no?: string
  /** 时间范围：YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD（handler.go:70-90） */
  created_from?: string
  created_to?: string
}

/** 销售订单（SalesOrder，models.go:14-33 字段全量） */
export interface SalesOrder {
  id: SalesId
  /** 销售单号（business-flow.md §13.1：SO-日期-流水） */
  so_no: string
  customer_id: number
  warehouse_id: number
  shipping_address: string
  /** 配送方式（business-flow.md §6.1） */
  delivery_method: string
  /** 金额合计（明细金额 = 数量 × 单价，后端计算） */
  total_amount: number
  status: SalesOrderStatus
  approved_by: number
  approved_at: string | null
  shipped_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 销售订单明细（SalesOrderItem，models.go:36-51；qty_allocated/qty_shipped 承载预占与发货进度） */
export interface SalesOrderItem {
  id: SalesId
  so_id: number
  line_no: number
  sku_id: number
  qty: number
  price: number
  amount: number
  qty_allocated: number
  qty_shipped: number
  remark: string
  created_at: string
  updated_at: string
  /** sales 域 CreatedBy 为裸 int64（models.go:49-50），出参数字 */
  created_by: number
  updated_by: number
}

/** 订单详情（GET /api/sales/{id}，handler.go:156-167 返回 {order, items}） */
export interface SalesOrderDetail {
  order: SalesOrder
  items: SalesOrderItem[]
}

// ---------- 创建 / 编辑入参（CreateOrderInput，internal/sales/service.go:104-121） ----------

/** 明细行入参（OrderItemInput，service.go:105-111；qty/price 为 stock.Qty，接受数字） */
export interface SalesOrderLineInput {
  line_no?: number
  sku_id: number
  qty: number
  price: number
  remark?: string
}

export interface SalesOrderCreatePayload {
  customer_id: number
  warehouse_id: number
  shipping_address?: string
  delivery_method?: string
  remark?: string
  items: SalesOrderLineInput[]
}

/** 审核入参（ApproveInput，service.go:134-137；通过/驳回共用资源点，plan §9.1） */
export interface SalesOrderApprovePayload {
  action: 'APPROVE' | 'REJECT'
  opinion?: string
}

/**
 * 审核结果（ApproveResult，internal/sales/service.go:388-393 实测）：
 * 审核通过即预占并联动建出库单，响应不是裸 SalesOrder 而是
 * {order, outbound_no, lock_count, locked_qty}——驳回时仅含 order（REJECT 分支
 * service.go:423 ApproveResult{Order: r}，其余字段零值省略），故除 order 外均可选。
 */
export interface SalesOrderApproveResult {
  order: SalesOrder | null
  /** 联动创建的出库单号（APPROVE 时返回，service.go:477） */
  outbound_no?: string
  /** 预占锁行数 = 分配记录数（service.go:391） */
  lock_count?: number
  /** 预占数量（stock.Qty 裸数字，service.go:392） */
  locked_qty?: number
}

// ---------- 权限码（internal/auth/permissions.go:170-177 三段式冻结） ----------

export const SALES_ORDER_CREATE_PERMISSION = 'sales:sales:create'
export const SALES_ORDER_UPDATE_PERMISSION = 'sales:sales:update'
export const SALES_ORDER_SUBMIT_PERMISSION = 'sales:sales:submit'
export const SALES_ORDER_APPROVE_PERMISSION = 'sales:sales:approve'
export const SALES_ORDER_CANCEL_PERMISSION = 'sales:sales:cancel'
export const SALES_ORDER_CLOSE_PERMISSION = 'sales:sales:close'
/** 销售退货创建（internal/auth/permissions.go:252 三段式冻结） */
export const SALES_RETURN_CREATE_PERMISSION = 'returns:salesreturn:create'

// ---------- 销售退货（GET /api/returns，后端退货域：internal/returns/handler.go:115-124） ----------
//
// 出参为退货单视图 ReturnOrderView（internal/returns/service_sales.go:111-136 snake_case）；
// 销售退货走 /api/returns，采购退货走 /api/purchase-returns（api/purchase.ts）。

/** 退货单状态（internal/returns/models.go:27-36 迁移 chk_return_orders_status 同源） */
export type SalesReturnStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'RECEIVING'
  | 'IN_QC'
  | 'SHIPPED'
  | 'COMPLETED'
  | 'CANCELLED'

/** 退货明细（ReturnItemView，service_sales.go:111-123；数量为 numeric 文本） */
export interface SalesReturnItemView {
  id: SalesId
  line_no: number
  sku_id: number
  qty_return: string
  qty_received: string
  qty_inspected: string
  qty_defective: string
  reason: string
  remark: string
}

/** 退货单（ReturnOrderView，service_sales.go:124-136；列表 items 省略） */
export interface SalesReturnOrder {
  id: SalesId
  /** 退货单号（RT- 前缀，docnum 冻结规则） */
  return_no: string
  /** SALES / PURCHASE */
  type: string
  /** 来源单号（销售单号 / 采购单号） */
  source_no: string
  customer_id: number
  supplier_id: number
  warehouse_id: number
  status: SalesReturnStatus
  remark: string
  created_at: string
  updated_at: string
  items?: SalesReturnItemView[]
}

/** 销售退货列表筛选（returns/handler.go:154-171：status/source_no/warehouse_id） */
export interface SalesReturnQuery extends PageQuery {
  status?: SalesReturnStatus
  source_no?: string
  warehouse_id?: SalesId
}

// ---------- 销售退货创建（POST /api/returns，internal/returns/handler.go:195-209 已注册） ----------
//
// 入参 SalesReturnCreateInput（internal/returns/service_sales.go:44-50）：后端强校验
// 退货仓与原销售单发货仓一致、逐行退量 ≤ 已发货量 − 已退量（service_sales.go:254-283、
// plan §3.1），qty_return 为 numeric(18,4) 文本（stock.ParseQty 解析）、reason 必填。

/** 销售退货行入参（SalesReturnLineInput，service_sales.go:35-41 字段全量） */
export interface SalesReturnLineInput {
  line_no: number
  sku_id: number
  /** numeric(18,4) 文本（service_sales.go:276 ParseQty），传正数字符串如 "3" */
  qty_return: string
  /** 必填（business-flow §9.1，service_sales.go:280） */
  reason: string
  remark?: string
}

/** 创建销售退货入参（SalesReturnCreateInput，service_sales.go:44-50 字段全量） */
export interface SalesReturnCreatePayload {
  /** 来源销售单号（精确） */
  so_no: string
  customer_id: number
  warehouse_id: number
  remark?: string
  lines: SalesReturnLineInput[]
}

export const salesApi = {
  orders: {
    list: (query: SalesOrderQuery) =>
      http.get<PageResult<SalesOrder>>('/api/sales', { params: query }),
    create: (payload: SalesOrderCreatePayload) => http.post<SalesOrder>('/api/sales', payload),
    detail: (id: SalesId) => http.get<SalesOrderDetail>(`/api/sales/${id}`),
    /** 草稿编辑（PUT /api/sales/{id}，入参与创建同构 CreateOrderInput） */
    update: (id: SalesId, payload: SalesOrderCreatePayload) =>
      http.put<SalesOrder>(`/api/sales/${id}`, payload),
    /** 提交审核（DRAFT→PENDING_APPROVAL） */
    submit: (id: SalesId) => http.put<SalesOrder>(`/api/sales/${id}/submit`),
    /**
     * 审核（APPROVE 通过即预占 / REJECT 驳回）。响应为 ApproveResult 嵌套结构
     * （internal/sales/service.go:388-393），非裸 SalesOrder——消费方取订单状态
     * 须走 res.order?.status，直接取 res.status 会得到 undefined。
     */
    approve: (id: SalesId, payload: SalesOrderApprovePayload) =>
      http.put<SalesOrderApproveResult>(`/api/sales/${id}/approve`, payload),
    /** 取消（释放预占；原因可选） */
    cancel: (id: SalesId, payload?: { reason?: string }) =>
      http.put<SalesOrder>(`/api/sales/${id}/cancel`, payload),
    /** 差额关闭（原因必填，business-flow.md §13.3） */
    close: (id: SalesId, payload: { reason: string }) =>
      http.put<SalesOrder>(`/api/sales/${id}/close`, payload),
  },
  returns: {
    list: (query: SalesReturnQuery) =>
      http.get<PageResult<SalesReturnOrder>>('/api/returns', { params: query }),
    /** 创建销售退货（POST /api/returns，returns:salesreturn:create；返回 ReturnOrderView） */
    create: (payload: SalesReturnCreatePayload) =>
      http.post<SalesReturnOrder>('/api/returns', payload),
  },
}
