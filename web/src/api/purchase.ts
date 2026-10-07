import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { SalesId, ReturnApprovePayload } from './sales'

// ---------- 采购订单（后端 M2 已交付：internal/purchase/purchase.go:40-47，
// 出参为 PurchaseOrder GORM 模型 snake_case，internal/purchase/models.go:120-135） ----------
//
// 收货单走 /api/receipts（purchase.go:58-61）、采购退货走 /api/purchase-returns
// （internal/returns/handler.go:127-135，退货域承载）。状态为大写枚举
// （models.go:17-23），渲染前经 toStatusKey 归一后走 SfStatusTag。

/** 采购订单状态（internal/purchase/models.go:17-23 迁移 CHECK 同源） */
export type PurchaseStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'PARTIAL_RECEIVED'
  | 'RECEIVED_ALL'
  | 'COMPLETED'
  | 'CANCELLED'

/** 采购订单列表筛选（handler.go:87-113：keyword/status/supplier_id/warehouse_id） */
export interface PurchaseQuery extends PageQuery {
  keyword?: string
  status?: PurchaseStatus
  supplier_id?: SalesId
  warehouse_id?: SalesId
}

/** 采购订单（PurchaseOrder，models.go:120-135 字段全量） */
export interface PurchaseOrder {
  id: SalesId
  /** 采购单号（business-flow.md §13.1：PO-日期-流水） */
  po_no: string
  supplier_id: number
  warehouse_id: number
  total_amount: number
  status: PurchaseStatus
  approved_by: number
  approved_at: string | null
  received_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
  /** database.ID 序列化为字符串（BaseModel，database/model.go:122-128） */
  created_by: string
  updated_by: string
}

/** 采购订单明细（PurchaseOrderItem，models.go:143-157；四量约束收货时后端强校验） */
export interface PurchaseOrderItem {
  id: SalesId
  po_id: number
  line_no: number
  sku_id: number
  /** 原始数量 */
  qty_ordered: number
  /** 累计收货数量（含合格 + 不合格待定） */
  qty_received: number
  /** 拒收数量 */
  qty_rejected: number
  /** 已上架数量 */
  qty_putaway: number
  price: number
  amount: number
  remark: string
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 采购订单详情（GET /api/purchases/{id}，service_purchase.go:444-447 返回 {order, items}） */
export interface PurchaseOrderDetail {
  order: PurchaseOrder
  items: PurchaseOrderItem[]
}

/** 采购订单明细行入参（POItemInput，service_purchase.go:22-27） */
export interface PurchaseLineInput {
  sku_id: number
  qty: number
  price?: number
  remark?: string
}

/** 创建入参（POCreateInput，service_purchase.go:30-35） */
export interface PurchaseCreatePayload {
  supplier_id: number
  warehouse_id: number
  remark?: string
  items: PurchaseLineInput[]
}

/** 草稿编辑入参（POUpdateInput，service_purchase.go:37-42：字段可选=不修改，明细提供即整单替换） */
export interface PurchaseUpdatePayload {
  supplier_id?: number
  warehouse_id?: number
  remark?: string
  items?: PurchaseLineInput[]
}

/** 审核入参（POApproveInput，service_purchase.go:40-43；通过/驳回共用资源点） */
export interface PurchaseApprovePayload {
  approved: boolean
  /** 驳回必须附意见（business-flow.md §12.2 审批意见入审计） */
  opinion?: string
}

// ---------- 收货单（GET/POST /api/receipts，purchase.go:58-61；
// 出参为 Receipt GORM 模型 snake_case，internal/purchase/models.go:207-238） ----------
//
// 收货为事件型一次性生效（幂等键防重），无独立状态机——状态语义由入库单承载。

/** 收货单列表筛选（handler.go:320-340：inbound_no/receipt_no + warehouse_id） */
export interface ReceiptQuery extends PageQuery {
  inbound_no?: string
  receipt_no?: string
  warehouse_id?: SalesId
}

/** 收货记录（Receipt，models.go:207-221 字段全量；一次收货一行 + 明细分行） */
export interface Receipt {
  id: SalesId
  receipt_no: string
  inbound_no: string
  warehouse_id: number
  batch_no: string
  expiry_date: string | null
  production_date: string | null
  idempotency_key: string | null
  operator_id: number
  operator_name: string
  remark: string
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 收货明细（ReceiptItem，models.go:228-238；合格/拒收分列，exception_ref 关联异常中心） */
export interface ReceiptItem {
  id: SalesId
  receipt_id: number
  line_no: number
  sku_id: number
  qty_good: number
  qty_rejected: number
  exception_ref: string
  remark: string
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 收货单详情（GET /api/receipts/{id}，service_inbound.go:371-374 返回 {receipt, items}） */
export interface ReceiptDetail {
  receipt: Receipt
  items: ReceiptItem[]
}

/**
 * 收货行入参（ReceiptLineInput，internal/purchase/service_inbound.go:52-66 字段全量）：
 * serials 序列号 SKU 逐件采集；异常收货字段由异常中心承载范围（拍照/附件随异常中心，
 * 文件中心阶段 14 前不提供上传）；target_bin_id 手动指定目标库位（§5.2，可选）。
 */
export interface ReceiptLineInput {
  sku_id: number
  qty_good?: number
  qty_rejected?: number
  /** nil=默认 true（合格品进入待检） */
  require_inspect?: boolean
  batch_no?: string
  expiry_date?: string
  production_date?: string
  serials?: string[]
  /** 异常收货子型（§3.4：少货/多货/错货/破损/包装异常/批次异常/效期异常） */
  exception_type?: string
  exception_note?: string
  target_bin_id?: number
  remark?: string
}

/** 收货确认入参（ReceiptInput，service_inbound.go:69-74；幂等键头优先于体，plan §7） */
export interface ReceiptConfirmPayload {
  inbound_no: string
  lines: ReceiptLineInput[]
  idempotency_key?: string
  remark?: string
}

/** 收货确认结果（ReceiptResult，service_inbound.go:77-84；replay=true 为幂等重放） */
export interface ReceiptConfirmResult {
  receipt_no: string
  replay: boolean
  inbound_no: string
  inbound_status: string
  po_no?: string
  po_status?: string
  putaway_task_nos: string[]
}

// ---------- 采购退货单（GET /api/purchase-returns，internal/returns/handler.go:127-135；
// 出参为退货单视图 ReturnOrderView，internal/returns/service_sales.go:124-136） ----------

/** 退货单状态（internal/returns/models.go:27-36 迁移 chk_return_orders_status 同源） */
export type PurchaseReturnStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'RECEIVING'
  | 'IN_QC'
  | 'SHIPPED'
  | 'COMPLETED'
  | 'CANCELLED'

/** 采购退货明细（ReturnItemView，service_sales.go:111-123；数量为 numeric 文本） */
export interface PurchaseReturnItemView {
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

/** 采购退货单（ReturnOrderView，service_sales.go:124-136；列表 items 省略） */
export interface PurchaseReturnOrder {
  id: SalesId
  return_no: string
  type: string
  source_no: string
  customer_id: number
  supplier_id: number
  warehouse_id: number
  status: PurchaseReturnStatus
  remark: string
  created_at: string
  updated_at: string
  items?: PurchaseReturnItemView[]
}

/** 采购退货列表筛选（returns/handler.go：status/source_no/warehouse_id，type 固定 PURCHASE） */
export interface PurchaseReturnQuery extends PageQuery {
  status?: PurchaseReturnStatus
  source_no?: string
  warehouse_id?: SalesId
}

// ---------- 采购退货创建（POST /api/purchase-returns，internal/returns/handler.go:434-447 已注册） ----------
//
// 入参 PurchaseReturnCreateInput（internal/returns/service_purchase.go:44-50）：后端强校验
// 采购单存在且已收货、退货仓一致、逐行退量 ≤ 已收货量 − 已退量（service_purchase.go:56-64
// CreatePurchaseReturn + service_sales.go:254-283 validateReturnInput 共用）；
// qty_return 为 numeric(18,4) 文本、reason 必填。整单一次出库（frozen DDL 无逐行已出量列，
// service_purchase.go:20-23），部分退货在创建期以 qty_return < 已收量表达。

/** 采购退货行入参（PurchaseReturnLineInput，service_purchase.go:39-45 字段全量） */
export interface PurchaseReturnLineInput {
  line_no: number
  sku_id: number
  /** numeric(18,4) 文本（stock.ParseQty 解析，service_sales.go:276），传正数字符串如 "3" */
  qty_return: string
  /** 必填（business-flow §9.1） */
  reason: string
  remark?: string
}

/** 创建采购退货入参（PurchaseReturnCreateInput，service_purchase.go:44-50 字段全量） */
export interface PurchaseReturnCreatePayload {
  /** 来源采购单号（精确） */
  po_no: string
  supplier_id: number
  warehouse_id: number
  remark?: string
  lines: PurchaseReturnLineInput[]
}

// ---------- 权限码（internal/auth/permissions.go:135-167 三段式冻结） ----------

export const PURCHASE_CREATE_PERMISSION = 'purchase:purchase:create'
export const PURCHASE_UPDATE_PERMISSION = 'purchase:purchase:update'
export const PURCHASE_SUBMIT_PERMISSION = 'purchase:purchase:submit'
export const PURCHASE_APPROVE_PERMISSION = 'purchase:purchase:approve'
export const PURCHASE_CANCEL_PERMISSION = 'purchase:purchase:cancel'
export const PURCHASE_CLOSE_PERMISSION = 'purchase:purchase:close'
export const RECEIPT_EXECUTE_PERMISSION = 'purchase:receipt:execute'
/** 采购退货创建（internal/auth/permissions.go:263 三段式冻结） */
export const PURCHASE_RETURN_CREATE_PERMISSION = 'returns:purchasereturn:create'
/** 采购退货提交审核（internal/auth/permissions.go:267，DRAFT→PENDING_APPROVAL） */
export const PURCHASE_RETURN_SUBMIT_PERMISSION = 'returns:purchasereturn:submit'
/** 采购退货审核（internal/auth/permissions.go:268，通过→APPROVED / 驳回→退回 DRAFT） */
export const PURCHASE_RETURN_APPROVE_PERMISSION = 'returns:purchasereturn:approve'

export const purchaseApi = {
  list: (query: PurchaseQuery) =>
    http.get<PageResult<PurchaseOrder>>('/api/purchases', { params: query }),
  create: (payload: PurchaseCreatePayload) => http.post<PurchaseOrder>('/api/purchases', payload),
  detail: (id: SalesId) => http.get<PurchaseOrderDetail>(`/api/purchases/${id}`),
  /** 草稿编辑（PUT /api/purchases/{id}，字段可选=不修改，明细提供即整单替换） */
  update: (id: SalesId, payload: PurchaseUpdatePayload) =>
    http.put<PurchaseOrder>(`/api/purchases/${id}`, payload),
  /** 提交审核（DRAFT→PENDING_APPROVAL） */
  submit: (id: SalesId) => http.post<PurchaseOrder>(`/api/purchases/${id}/submit`),
  /** 审核（approve 权限点：approved=true/false 通过/驳回） */
  approve: (id: SalesId, payload: PurchaseApprovePayload) =>
    http.post<PurchaseOrder>(`/api/purchases/${id}/approve`, payload),
  cancel: (id: SalesId, payload?: { reason?: string }) =>
    http.post<PurchaseOrder>(`/api/purchases/${id}/cancel`, payload),
  /** 差额关闭（原因必填） */
  close: (id: SalesId, payload: { reason: string }) =>
    http.post<PurchaseOrder>(`/api/purchases/${id}/close`, payload),
  receipts: {
    list: (query: ReceiptQuery) =>
      http.get<PageResult<Receipt>>('/api/receipts', { params: query }),
    detail: (id: SalesId) => http.get<ReceiptDetail>(`/api/receipts/${id}`),
    /** 收货确认（POST /api/receipts，purchase:receipt:execute；幂等键防重） */
    confirm: (payload: ReceiptConfirmPayload) =>
      http.post<ReceiptConfirmResult>('/api/receipts', payload),
  },
  returns: {
    list: (query: PurchaseReturnQuery) =>
      http.get<PageResult<PurchaseReturnOrder>>('/api/purchase-returns', { params: query }),
    /** 创建采购退货（POST /api/purchase-returns，returns:purchasereturn:create；返回 ReturnOrderView） */
    create: (payload: PurchaseReturnCreatePayload) =>
      http.post<PurchaseReturnOrder>('/api/purchase-returns', payload),
    /** 提交审核（POST /api/purchase-returns/{id}/submit，DRAFT→PENDING_APPROVAL） */
    submit: (id: SalesId) => http.post<PurchaseReturnOrder>(`/api/purchase-returns/${id}/submit`),
    /** 审核（POST /api/purchase-returns/{id}/approve，approved=false 驳回退回 DRAFT） */
    approve: (id: SalesId, payload: ReturnApprovePayload) =>
      http.post<PurchaseReturnOrder>(`/api/purchase-returns/${id}/approve`, payload),
  },
}
