import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 盘点中心（后端 M2 已交付：internal/stockops/routes.go:63-71，/api/counts） ----------
//
// 出参为 stockops 视图 snake_case（CountOrderView/CountItemView/CountDifferenceView，
// internal/stockops/handler.go:91-174；ID 出参 database.ID 序列化为字符串）。
// 业务依据：business-flow.md §10.2（差异必须走库存调整单 + 审批链路）、§13.1 单号规则
// （CK-日期-流水）、frontend.md §10.5。

/** ID 序列化：后端 database.ID 出参字符串，保留 number 兼容 */
export type CountId = number | string

/** 盘点范围模式（CountScope.Mode，internal/stockops/models.go:163：ALL/ZONE/SHELF/BIN/SKU；
 * 按仓 = 单据 warehouse_id 本身，无 WAREHOUSE 模式） */
export type CountScopeMode = 'ALL' | 'ZONE' | 'SHELF' | 'BIN' | 'SKU'

/** 盘点范围声明（CountScope，models.go:162-168 快照 jsonb；各维度 ID 列表按 mode 携带） */
export interface CountScope {
  mode: CountScopeMode
  zone_ids?: number[]
  shelf_ids?: number[]
  bin_ids?: number[]
  sku_ids?: number[]
}

/** 盘点单五态状态机（models.go:143-147 迁移 chk_count_orders_status 同源；
 * 渲染经 toStatusKey 归一后走 SfStatusTag：DRAFT→草稿 / COUNTING→盘点中 /
 * PENDING_REVIEW→待复核 / COMPLETED→已完成 / CANCELLED→已取消） */
export type CountStatus = 'DRAFT' | 'COUNTING' | 'PENDING_REVIEW' | 'COMPLETED' | 'CANCELLED'

/** 差异行状态（db/migrations/000009 chk_count_differences_status：PENDING/APPROVED/REJECTED/EXECUTED） */
export type CountDiffStatus = 'PENDING' | 'APPROVED' | 'REJECTED' | 'EXECUTED'

export const COUNT_SCOPE_MODE_LABEL: Record<CountScopeMode, string> = {
  ALL: '全盘',
  ZONE: '按库区',
  SHELF: '按货架',
  BIN: '按库位',
  SKU: '按 SKU',
}

/** 盘点单列表筛选（handler.go:451-465：warehouse_id/status/count_no） */
export interface CountQuery extends PageQuery {
  warehouse_id?: CountId
  status?: CountStatus
  /** 精确盘点单号（handler.go:465） */
  count_no?: string
}

/** 盘点单（CountOrderView，handler.go:91-104 字段全量） */
export interface CountOrder {
  id: CountId
  /** 盘点单号（CK-日期-流水，business-flow.md §13.1） */
  count_no: string
  warehouse_id: CountId
  scope: CountScope
  status: CountStatus
  frozen_at: string | null
  reviewed_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
}

/** 盘点明细行（CountItemView，handler.go:115-128；qty_system 冻结快照，
 * qty_counted null=未登记——与「登记为 0」语义不同，inventory-rules.md §9） */
export interface CountItem {
  id: CountId
  /** 库存行 ID（实盘登记按 (inventory_row_id, serial_no) 幂等覆盖，store.go:204-208） */
  inventory_row_id: CountId
  sku_id: CountId
  warehouse_id: CountId
  zone_id: CountId
  shelf_id: CountId
  bin_id: CountId
  qty_system: number
  qty_counted: number | null
  counted_by: CountId
  counted_at: string | null
  /** 序列号 SKU 逐件一行 qty=1，非序列号 SKU 单行 serial_no=''（models.go:80-81） */
  serial_no: string
}

/** 盘点差异行（CountDifferenceView，handler.go:145-158；diff_qty 盘盈为正，
 * adjust_no 为差异批准后执行的调整单单号回写） */
export interface CountDifference {
  id: CountId
  line_no: number
  sku_id: CountId
  warehouse_id: CountId
  bin_id: CountId
  batch_id: CountId
  qty_system: number
  qty_counted: number
  diff_qty: number
  adjust_no: string
  status: CountDiffStatus
  remark: string
}

/** 盘点单详情（GET /api/counts/{id}，handler.go:170-174 返回 {order, items, differences}） */
export interface CountDetail {
  order: CountOrder
  items: CountItem[]
  differences: CountDifference[]
}

// ---------- 创建 / 实盘登记 / 状态流转 ----------

/** 创建入参（CountInput，internal/stockops/count.go:40-43；范围声明校验 models.go:180-207） */
export interface CountCreatePayload {
  warehouse_id: number
  scope: CountScope
  remark?: string
}

/**
 * 批量实盘登记行（CountRegistration，internal/stockops/store.go:204-208 为无 json tag 的
 * Go 结构体——键即 Go 字段名 InventoryRowID/SerialNo/Qty；Go 反序列化大小写不敏感，
 * 服务端两种键均接受，此处按后端字段名提交）。Qty 为 stock.Qty（裸数字）；
 * 登记为 0 必须显式提交（inventory-rules.md §9）。PUT 幂等：同 (row, serial) 覆盖。
 */
export interface CountRegistrationInput {
  InventoryRowID: number
  SerialNo: string
  Qty: number
}

/** 批量实盘登记载荷（CountRegistrationInput.Registrations，count.go:54-56，JSON 键 items） */
export interface CountRegisterPayload {
  items: CountRegistrationInput[]
}

// ---------- 权限码（internal/auth/permissions.go:226-232 三段式冻结） ----------

export const COUNT_CREATE_PERMISSION = 'stockops:count:create'
export const COUNT_EXECUTE_PERMISSION = 'stockops:count:execute'
export const COUNT_APPROVE_PERMISSION = 'stockops:count:approve'
export const COUNT_CANCEL_PERMISSION = 'stockops:count:cancel'

export const countApi = {
  list: (query: CountQuery) => http.get<PageResult<CountOrder>>('/api/counts', { params: query }),
  create: (payload: CountCreatePayload) => http.post<CountDetail>('/api/counts', payload),
  detail: (id: CountId) => http.get<CountDetail>(`/api/counts/${id}`),
  /** 开始盘点（POST /api/counts/{id}/start，DRAFT→COUNTING + 冻结快照） */
  start: (id: CountId) => http.post<CountDetail>(`/api/counts/${id}/start`),
  /** 批量实盘登记（PUT /api/counts/{id}/items，COUNTING 态；PUT 幂等覆盖） */
  register: (id: CountId, payload: CountRegisterPayload) =>
    http.put<CountDetail>(`/api/counts/${id}/items`, payload),
  /** 完成实盘（POST /api/counts/{id}/finish，COUNTING→PENDING_REVIEW + 差异生成） */
  finish: (id: CountId) => http.post<CountDetail>(`/api/counts/${id}/finish`),
  /** 差异审核通过（POST /api/counts/{id}/complete，stockops:count:approve；差异走调整单） */
  complete: (id: CountId, payload?: { opinion?: string }) =>
    http.post<CountDetail>(`/api/counts/${id}/complete`, payload),
  /** 差异审核驳回（POST /api/counts/{id}/reject，stockops:count:approve；
   * PENDING_REVIEW→CANCELLED：差异行落 REJECTED、解冻，count.go:546-598） */
  reject: (id: CountId, payload?: { opinion?: string }) =>
    http.post<CountDetail>(`/api/counts/${id}/reject`, payload),
  /** 取消（POST /api/counts/{id}/cancel，stockops:count:cancel） */
  cancel: (id: CountId, payload?: { reason?: string }) =>
    http.post<CountDetail>(`/api/counts/${id}/cancel`, payload),
}
