import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'
import type { SalesId } from './sales'

// ---------- 调拨单（后端 M2 已交付：internal/stockops/routes.go:46-56，/api/transfers） ----------
//
// 出参为 TransferOrderView/TransferItemView snake_case（internal/stockops/handler.go:27-84；
// ID 出参 database.ID 序列化为字符串，数量为 numeric 裸数字，在途 in_transit_qty 由后端
// 计算——前端禁止算库存）。与 api/inventory.ts 的库存转移（前端先行骨架）是两套语义：
//   /api/transfers        调拨单据流（仓库中心菜单组 /transfers，7 态状态机走单）
//   /api/inventory/moves  仓内移库作业（MoveBin 原语 HTTP 化，routes.go:58-60）

/** 调拨类型（internal/stockops/models.go:153-154：WAREHOUSE 跨仓 / BIN 库位间，库位间必须同仓） */
export type TransferType = 'WAREHOUSE' | 'BIN'

/** 调拨单状态机（models.go:134-142 迁移 chk_transfer_orders_status 七态同源：
 * 草稿 → 待审核 → 待出库（APPROVED，审核通过即源仓预占）→ 调拨中 → 待入库
 * （AWAITING_RECEIPT）→ 已完成；DRAFT/PENDING_APPROVAL/APPROVED 可取消） */
export type TransferStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'TRANSFERRING'
  | 'AWAITING_RECEIPT'
  | 'COMPLETED'
  | 'CANCELLED'

/**
 * 调拨单状态 → SfStatusTag：注册表（types/status.ts）已收录 draft/pending_approval/
 * approved/completed/cancelled（approved 通用文案「已审核」，调拨语境「待出库」经
 * label 覆盖）；后端返回未知值时映射缺失，SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
export const TRANSFER_STATUS_TAG: Record<TransferStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '待出库', semantic: 'pending' },
  TRANSFERRING: { label: '调拨中', semantic: 'processing' },
  AWAITING_RECEIPT: { label: '待入库', semantic: 'pending' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 调拨单列表筛选（handler.go:294-314：warehouse_id/to_warehouse_id/status/type/transfer_no；
 * 数据权限：from/to 任一端在范围内） */
export interface TransferQuery extends PageQuery {
  warehouse_id?: SalesId
  to_warehouse_id?: SalesId
  status?: TransferStatus
  type?: TransferType
  /** 精确调拨单号（handler.go:314） */
  transfer_no?: string
}

/** 调拨单（TransferOrderView，handler.go:27-42 字段全量；明细走详情接口） */
export interface TransferOrder {
  id: SalesId
  /** 调拨单号（business-flow.md §13.1：TR-日期-流水） */
  transfer_no: string
  type: TransferType
  from_warehouse_id: SalesId
  to_warehouse_id: SalesId
  status: TransferStatus
  approved_by: SalesId
  approved_at: string | null
  outbound_at: string | null
  received_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
}

/** 调拨明细（TransferItemView，handler.go:54-72；两端四维定位 ID + 在途进度） */
export interface TransferLine {
  id: SalesId
  line_no: number
  sku_id: SalesId
  /** 0=非批次 SKU */
  batch_id: SalesId
  from_warehouse_id: SalesId
  from_zone_id: SalesId
  from_shelf_id: SalesId
  from_bin_id: SalesId
  to_warehouse_id: SalesId
  to_zone_id: SalesId
  to_shelf_id: SalesId
  to_bin_id: SalesId
  qty: number
  qty_out: number
  qty_in: number
  /** 在途 = qty_out - qty_in（后端计算，前端禁止算库存） */
  in_transit_qty: number
  remark: string
}

/** 调拨单详情（GET /api/transfers/{id}，handler.go:86-89 返回 {order, items}） */
export interface TransferDetail {
  order: TransferOrder
  items: TransferLine[]
}

// ---------- 创建 / 修改入参（TransferInput，internal/stockops/transfer.go:66-71） ----------

/** 调拨库位定位（TransferLoc，transfer.go:46-51：四维） */
export interface TransferLocInput {
  warehouse_id: number
  zone_id?: number
  shelf_id?: number
  bin_id: number
}

/** 调拨明细行入参（TransferLineInput，transfer.go:57-63） */
export interface TransferLineInput {
  sku_id: number
  /** 0=非批次 SKU */
  batch_id?: number
  from: TransferLocInput
  to: TransferLocInput
  qty: number
}

export interface TransferCreatePayload {
  type: TransferType
  from_warehouse_id: number
  to_warehouse_id: number
  remark?: string
  lines: TransferLineInput[]
}

/** 审核入参（ApproveTransferInput，transfer.go:377-380；通过 = PENDING_APPROVAL→APPROVED +
 * 源仓逐行预占，驳回 = →CANCELLED，plan §6.6） */
export interface TransferApprovePayload {
  action: 'approve' | 'reject'
  opinion?: string
}

// ---------- 仓内移库（POST /api/inventory/moves，stockops/routes.go:58-60） ----------

/** 移库定位键（MoveKeyInput，internal/stockops/moves.go:24-30：五维） */
export interface MoveKeyInput {
  warehouse_id: number
  zone_id?: number
  shelf_id?: number
  bin_id: number
  sku_id: number
  /** 0=非批次 SKU */
  batch_id?: number
}

/** 移库入参（MoveInput，moves.go:35-41；qty 为 numeric 文本，source_no 作业依据号必填） */
export interface MoveBinPayload {
  from: MoveKeyInput
  to: MoveKeyInput
  qty: string
  source_no: string
  remark?: string
}

/** 库存原语变更结果（stock.MutationResult，internal/stock/result.go:10-21） */
export interface StockMutationResult {
  /** true=幂等重放（未再次变更库存） */
  replay: boolean
  ledger: { id: number; ledger_no: string }
  lock_id?: number
  adjustment_no?: string
}

// ---------- 权限码（internal/auth/permissions.go:215-224/243-244 三段式冻结） ----------

export const TRANSFER_CREATE_PERMISSION = 'stockops:transfer:create'
export const TRANSFER_UPDATE_PERMISSION = 'stockops:transfer:update'
export const TRANSFER_SUBMIT_PERMISSION = 'stockops:transfer:submit'
export const TRANSFER_APPROVE_PERMISSION = 'stockops:transfer:approve'
export const TRANSFER_EXECUTE_PERMISSION = 'stockops:transfer:execute'
export const TRANSFER_CANCEL_PERMISSION = 'stockops:transfer:cancel'
export const MOVE_EXECUTE_PERMISSION = 'stockops:move:execute'

export const transferApi = {
  list: (query: TransferQuery) =>
    http.get<PageResult<TransferOrder>>('/api/transfers', { params: query }),
  create: (payload: TransferCreatePayload) => http.post<TransferDetail>('/api/transfers', payload),
  detail: (id: SalesId) => http.get<TransferDetail>(`/api/transfers/${id}`),
  /** 草稿编辑（PUT /api/transfers/{id}，仅 DRAFT） */
  update: (id: SalesId, payload: TransferCreatePayload) =>
    http.put<TransferDetail>(`/api/transfers/${id}`, payload),
  /** 提交审核（POST /api/transfers/{id}/submit，DRAFT→PENDING_APPROVAL） */
  submit: (id: SalesId) => http.post<TransferDetail>(`/api/transfers/${id}/submit`),
  /** 审核（POST /api/transfers/{id}/approve：approve 通过即预占 / reject 驳回取消） */
  approve: (id: SalesId, payload: TransferApprovePayload) =>
    http.post<TransferDetail>(`/api/transfers/${id}/approve`, payload),
  /** 调拨出库（POST /api/transfers/{id}/outbound，APPROVED→TRANSFERRING + 源仓扣减） */
  outbound: (id: SalesId) => http.post<TransferDetail>(`/api/transfers/${id}/outbound`),
  /** 到货登记（POST /api/transfers/{id}/arrive，TRANSFERRING→AWAITING_RECEIPT，无库存动作） */
  arrive: (id: SalesId) => http.post<TransferDetail>(`/api/transfers/${id}/arrive`),
  /** 收货入库（POST /api/transfers/{id}/receive，AWAITING_RECEIPT→COMPLETED + 逐行 TransferIn） */
  receive: (id: SalesId) => http.post<TransferDetail>(`/api/transfers/${id}/receive`),
  /** 取消（POST /api/transfers/{id}/cancel；TRANSFERRING/AWAITING_RECEIPT 取消被拒） */
  cancel: (id: SalesId, payload?: { reason?: string }) =>
    http.post<TransferDetail>(`/api/transfers/${id}/cancel`, payload),
  /** 仓内移库（POST /api/inventory/moves，stockops:move:execute；同仓库位间可用库存移动） */
  moveBin: (payload: MoveBinPayload) =>
    http.post<StockMutationResult>('/api/inventory/moves', payload),
}
