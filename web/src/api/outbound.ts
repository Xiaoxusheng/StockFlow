import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { SalesId } from './sales'
import type { BatchResult } from './printing'

// ---------- 出库单（后端 M2 已交付：internal/sales/routes.go:82-87，出参为
// OutboundOrder GORM 模型 snake_case，internal/sales/models.go:54-90） ----------
//
// 出库单详情含任务族（GET /api/outbounds/{no}，handler.go:274-285 返回
// {outbound, items, allocations, picks, checks, packages, shipments}）；
// 四作业任务族列表（拣货/复核/打包/发货，routes.go:94-112）归本文件。
// 状态为大写枚举（models.go:265-275），渲染前经 toStatusKey 归一后走 SfStatusTag。

/** 出库单状态（internal/sales/models.go:265-276 迁移 CHECK 同源） */
export type OutboundOrderStatus =
  | 'PENDING_ALLOCATE'
  | 'ALLOCATED'
  | 'PICKING'
  | 'PICKED'
  | 'CHECKED'
  | 'PACKED'
  | 'PARTIAL_SHIPPED'
  | 'SHIPPED_ALL'
  | 'CANCELLED'
  | 'CLOSED'

/** 出库类型（models.go:344-346 迁移 CHECK：中文值域） */
export type OutboundOrderType = '销售出库' | '生产领料' | '调拨出库' | '其他出库' | '报损出库'

/** 出库单列表筛选（handler.go:253-272：status/warehouse_id/so_no/outbound_no） */
export interface OutboundOrderQuery extends PageQuery {
  status?: OutboundOrderStatus
  warehouse_id?: SalesId
  so_no?: string
  /** 精确出库单号（handler.go:262） */
  outbound_no?: string
}

/** 出库单（OutboundOrder，models.go:54-71 字段全量；type 为中文值域） */
export interface OutboundOrder {
  id: SalesId
  /** 出库单号（business-flow.md §13.1：OUT-日期-流水） */
  outbound_no: string
  /** 来源销售单号 */
  so_no: string
  type: string
  warehouse_id: number
  status: OutboundOrderStatus
  picked_at: string | null
  checked_at: string | null
  packed_at: string | null
  shipped_at: string | null
  cancelled_at: string | null
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 出库单明细（OutboundItem，models.go:75-90；各环节累计数量支持部分拣货/部分发货） */
export interface OutboundOrderItem {
  id: SalesId
  outbound_id: number
  line_no: number
  sku_id: number
  qty: number
  qty_picked: number
  qty_checked: number
  qty_packed: number
  qty_shipped: number
  remark: string
  created_at: string
  updated_at: string
  /** sales 域 CreatedBy 为裸 int64（models.go:88-89），出参数字 */
  created_by: number
  updated_by: number
}

/** 库存分配记录（AllocationRecord，models.go:94-110） */
export interface OutboundAllocationRecord {
  id: SalesId
  outbound_no: string
  line_no: number
  sku_id: number
  batch_id: number
  warehouse_id: number
  bin_id: number
  qty: number
  /** FIFO/FEFO/指定批次/指定仓库/指定库位（models.go:349-355） */
  strategy: string
  reason: Record<string, unknown>
  /** 回指 inventory_locks（发货核销依据） */
  lock_id: number
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

// ---------- 拣货任务（GET /api/picks，routes.go:94-97；出参 PickTask，models.go:113-139） ----------

/** 拣货任务状态（models.go:294-301 迁移 chk_pick_tasks_status；PICKING 值域保留、状态机不使用） */
export type PickTaskStatus = 'PENDING' | 'CLAIMED' | 'PICKING' | 'PICKED' | 'EXCEPTION' | 'CANCELLED'

/** 拣货任务列表筛选（handler.go:364-385：status/outbound_no/assignee_id/warehouse_id） */
export interface PickTaskQuery extends PageQuery {
  status?: PickTaskStatus
  outbound_no?: string
  assignee_id?: SalesId
  warehouse_id?: SalesId
}

/** 拣货任务（PickTask，models.go:113-139 字段全量） */
export interface PickTask {
  id: SalesId
  /** 拣货任务号（business-flow.md §13.1：PK- 前缀） */
  pick_no: string
  outbound_no: string
  outbound_line_no: number
  sku_id: number
  batch_id: number
  source_warehouse_id: number
  source_zone_id: number
  source_shelf_id: number
  source_bin_id: number
  /** 应拣数量 */
  qty: number
  /** 已拣数量 */
  picked_qty: number
  status: PickTaskStatus
  /** 任务优先级 0–9（效率层一期 B3，迁移 000023；0=默认）——/api/tasks/next 排序层依据，
   * 经 PUT /api/picks/{id}/priority 设置（权限 sales:pick:assign） */
  priority: number
  assignee_id: number
  assignee_name: string
  claimed_at: string | null
  picked_at: string | null
  scanned_code: string
  scan_matched: boolean
  warehouse_id: number
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 拣货确认入参（PickConfirmInput，service_outbound.go:197-201） */
export interface PickConfirmPayload {
  picked_qty: number
  /** 序列号 SKU 必填：逐件采集（inventory-rules.md §8.2） */
  serials?: string[]
  /** 扫码录入值（M2 记录不解析） */
  scanned_code?: string
}

// ---------- 复核任务（GET /api/checks，routes.go:100-103；出参 CheckTask，models.go:143-164） ----------

/** 复核任务状态（models.go:315-319；无 CLAIMED——领取为原子指派不迁移状态） */
export type CheckTaskStatus = 'PENDING' | 'DONE' | 'EXCEPTION'

/** 复核异常类型（models.go:322 五类中文值域；result 为空串 = 复核通过） */
export type CheckResultType = '错货' | '少货' | '多货' | '批次错误' | '序列号错误'

/** 复核任务列表筛选（handler.go:440-461：status/outbound_no/assignee_id/warehouse_id） */
export interface CheckTaskQuery extends PageQuery {
  status?: CheckTaskStatus
  outbound_no?: string
  assignee_id?: SalesId
  warehouse_id?: SalesId
}

/** 复核任务（CheckTask，models.go:143-164 字段全量） */
export interface CheckTask {
  id: SalesId
  check_no: string
  outbound_no: string
  outbound_line_no: number
  sku_id: number
  batch_id: number
  /** 序列号 SKU 一行一件 */
  serial_no: string
  qty: number
  status: CheckTaskStatus
  /** 任务优先级 0–9（效率层一期 B3，迁移 000023；0=默认）——/api/tasks/next 排序层依据，
   * 经 PUT /api/checks/{id}/priority 设置（权限 sales:check:assign） */
  priority: number
  /** 空 = 复核通过；异常为五类中文值域之一 */
  result: string
  assignee_id: number
  assignee_name: string
  claimed_at: string | null
  done_at: string | null
  warehouse_id: number
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 复核确认入参（CheckConfirmInput，service_outbound.go:604-608） */
export interface CheckConfirmPayload {
  pass: boolean
  /** pass=false 时必填：五类异常之一 */
  result?: CheckResultType
  /** 序列号任务必填：重新扫描确认的序列号 */
  serial?: string
}

// ---------- 打包记录（GET/POST /api/packing，routes.go:106-107；出参 PackingRecord，models.go:167-186） ----------

/** 打包记录列表筛选（handler.go:510-527：outbound_no/warehouse_id） */
export interface PackingRecordQuery extends PageQuery {
  outbound_no?: string
  warehouse_id?: SalesId
}

/** 打包记录（PackingRecord，models.go:167-186 字段全量；一个订单允许多个包裹） */
export interface PackingRecord {
  id: SalesId
  package_no: string
  outbound_no: string
  packing_material: string
  length: number
  width: number
  height: number
  weight: number
  volume: number
  carrier: string
  tracking_no: string
  warehouse_id: number
  idempotency_key: string | null
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 打包明细行入参（PackLineInput，service_ship.go 上方定义：line_no + qty） */
export interface PackLineInput {
  line_no: number
  qty: number
}

/** 打包入参（PackInput，service_outbound.go:730-744；幂等键随体或 Idempotency-Key 头） */
export interface PackPayload {
  outbound_no: string
  lines: PackLineInput[]
  packing_material?: string
  length?: number
  width?: number
  height?: number
  weight?: number
  volume?: number
  carrier?: string
  tracking_no?: string
  remark?: string
  idempotency_key?: string
}

/** 打包结果（PackResult，service_outbound.go:747-750；replay=true 为幂等重放） */
export interface PackResult {
  package: PackingRecord
  replay: boolean
}

// ---------- 发货单（GET/POST /api/shipments，routes.go:110-112；出参 Shipment，models.go:204-222） ----------

/** 发货单状态（models.go:325-331；库存正式扣减仅发生在 PENDING→SHIPPED，后续为纯记录流转） */
export type ShipmentStatus = 'PENDING' | 'SHIPPED' | 'IN_TRANSIT' | 'SIGNED' | 'ABNORMAL'

/** 发货单列表筛选（handler.go:548-566：status/outbound_no/warehouse_id） */
export interface ShipmentQuery extends PageQuery {
  status?: ShipmentStatus
  outbound_no?: string
  warehouse_id?: SalesId
}

/** 发货单（Shipment，models.go:204-222 字段全量） */
export interface Shipment {
  id: SalesId
  shipment_no: string
  outbound_no: string
  carrier: string
  tracking_no: string
  warehouse_id: number
  shipper_id: number
  shipper_name: string
  package_count: number
  status: ShipmentStatus
  shipped_at: string | null
  idempotency_key: string | null
  remark: string
  created_at: string
  updated_at: string
  created_by: number
  updated_by: number
}

/** 发货明细行入参（ShipLineInput，service_ship.go:33-35；缺省 = 全部未发货明细行） */
export interface ShipLineInput {
  line_no: number
}

/** 发货确认入参（ShipInput，service_ship.go:36-43） */
export interface ShipPayload {
  outbound_no: string
  lines?: ShipLineInput[]
  carrier?: string
  tracking_no?: string
  remark?: string
  idempotency_key?: string
}

/** 发货结果（ShipResult，service_ship.go:46-50；shipped 为行号 → 本次发货量） */
export interface ShipResult {
  shipment: Shipment
  replay: boolean
  shipped: Record<string, number>
}

// ---------- 库存分配（GET/POST /api/allocations，routes.go:90-91 已注册；
// 出参为 AllocationRecord 裸模型 = OutboundAllocationRecord，models.go:94-110） ----------

/** 分配记录列表筛选（AllocationQuery，repository.go:179-186：outbound_no/strategy/sku_id） */
export interface OutboundAllocationQuery extends PageQuery {
  outbound_no?: string
  /** FIFO/FEFO/指定批次/指定仓库/指定库位（models.go:349-355 中文值域） */
  strategy?: string
  sku_id?: SalesId
}

/** 重新分配入参（ReallocateInput，service_ship.go:513-516；line_no=0 表示整单所有
 * 可重分配行）。后端约束：仅 ALLOCATED/PICKING 状态、未拣货未发货的行可重分配，
 * 释放旧锁 → allocation_records 整组替换 → 未完结拣货任务取消 → 默认策略重新预占。 */
export interface OutboundReallocatePayload {
  outbound_no: string
  line_no: number
}

// ---------- 出库单详情（GET /api/outbounds/{no}，handler.go:274-285 任务族全量） ----------

export interface OutboundOrderDetail {
  outbound: OutboundOrder
  items: OutboundOrderItem[]
  allocations: OutboundAllocationRecord[]
  picks: PickTask[]
  checks: CheckTask[]
  packages: PackingRecord[]
  shipments: Shipment[]
}

// ---------- 权限码（internal/auth/permissions.go:180-212 三段式冻结） ----------

export const OUTBOUND_CREATE_PERMISSION = 'sales:outbound:create'
export const OUTBOUND_CANCEL_PERMISSION = 'sales:outbound:cancel'
export const OUTBOUND_CLOSE_PERMISSION = 'sales:outbound:close'
/** 库存分配（internal/auth/permissions.go:187-190；execute=重新分配） */
export const ALLOCATION_LIST_PERMISSION = 'sales:allocation:list'
export const ALLOCATION_EXECUTE_PERMISSION = 'sales:allocation:execute'
export const PICK_CLAIM_PERMISSION = 'sales:pick:claim'
export const PICK_EXECUTE_PERMISSION = 'sales:pick:execute'
export const CHECK_CLAIM_PERMISSION = 'sales:check:claim'
export const CHECK_EXECUTE_PERMISSION = 'sales:check:execute'
export const PACKING_EXECUTE_PERMISSION = 'sales:packing:execute'
export const SHIPMENT_EXECUTE_PERMISSION = 'sales:shipment:execute'

export const outboundApi = {
  /** 出库单列表（GET /api/outbounds） */
  list: (query: OutboundOrderQuery) =>
    http.get<PageResult<OutboundOrder>>('/api/outbounds', { params: query }),
  /** 出库单详情（GET /api/outbounds/{no}，:no 为出库单号） */
  get: (no: string) => http.get<OutboundOrderDetail>(`/api/outbounds/${encodeURIComponent(no)}`),
  /** 生成拣货任务（POST /api/outbounds/{no}/picks，ALLOCATED→PICKING；返回 {outbound, picks}，handler.go:288-296） */
  createPicks: (no: string) =>
    http.post<{ outbound: OutboundOrder; picks: PickTask[] }>(
      `/api/outbounds/${encodeURIComponent(no)}/picks`,
    ),
  /** 取消出库单（PUT /api/outbounds/{no}/cancel，释放预占） */
  cancel: (no: string, payload?: { reason?: string }) =>
    http.put<OutboundOrder>(`/api/outbounds/${encodeURIComponent(no)}/cancel`, payload),
  /** 差额关闭（PUT /api/outbounds/{no}/close，原因必填） */
  close: (no: string, payload: { reason: string }) =>
    http.put<OutboundOrder>(`/api/outbounds/${encodeURIComponent(no)}/close`, payload),
  /** 库存分配（GET/POST /api/allocations，routes.go:90-91；execute=重新分配） */
  allocations: {
    list: (query: OutboundAllocationQuery) =>
      http.get<PageResult<OutboundAllocationRecord>>('/api/allocations', { params: query }),
    /**
     * 重新分配（POST /api/allocations，sales:allocation:execute；handler.go:466-479）。
     * 释放旧预占锁 → 分配记录整组替换 → 未完结拣货任务取消 → 默认策略重新预占，
     * 同事务生效；危险操作，前端须二次确认（frontend.md §16.2）。
     * 响应为 {allocations: AllocationRecord[]}（非分页信封，handler.go:477）。
     */
    reallocate: (payload: OutboundReallocatePayload) =>
      http.post<{ allocations: OutboundAllocationRecord[] }>('/api/allocations', payload),
  },
}

/** 出库四作业任务族 API（拣货 → 复核 → 打包 → 发货，business-flow.md §8） */
export const outboundTaskApi = {
  picks: {
    list: (query: PickTaskQuery) =>
      http.get<PageResult<PickTask>>('/api/picks', { params: query }),
    /** 领取（PUT /api/picks/{id}/claim，原子抢占） */
    claim: (id: SalesId) => http.put<PickTask>(`/api/picks/${id}/claim`),
    /**
     * 批量领取（POST /api/picks/batch-claim，计划 §2.7；权限复用 sales:pick:claim）：
     * 响应=批量结果契约 BatchResult（api.md §9）——PENDING→success、已被本人领取→skipped、
     * 被他人领取/状态非法→failed(reason)；批量不整体回滚；结果统一经
     * components/batch/BatchResultDrawer 呈现（禁各页自写），重试=仅对 failed ids 再调本端点。
     * 头键说明：批量领取**不消费 Idempotency-Key**（防重=状态机原子抢占），前端附头仅防双击。
     */
    batchClaim: (ids: Array<number | string>) =>
      http.post<BatchResult>('/api/picks/batch-claim', { ids: ids.map((id) => Number(id)) }),
    /**
     * 设置任务优先级（PUT /api/picks/{id}/priority，效率层一期 B3）：值域 0–9
     * （迁移 000023 CHECK 兜底），终态任务后端 409 拒绝；本端点是 /api/tasks/next
     * 「mine > priority > 超时 > created_at」排序层的数据来源（不设置即排序层无意义）。
     * 权限 sales:pick:assign。响应 {id, priority} 供前端就地回显。
     */
    setPriority: (id: SalesId, priority: number) =>
      http.put<{ id: number; priority: number }>(`/api/picks/${id}/priority`, { priority }),
    /** 拣货确认（PUT /api/picks/{id}/confirm，CLAIMED→PICKED，联动创建复核任务） */
    confirm: (id: SalesId, payload: PickConfirmPayload) =>
      http.put<PickTask>(`/api/picks/${id}/confirm`, payload),
    /** 缺货/少货/库位异常上报（PUT /api/picks/{id}/exception） */
    reportException: (id: SalesId, payload: { reason: string }) =>
      http.put<PickTask>(`/api/picks/${id}/exception`, payload),
  },
  checks: {
    list: (query: CheckTaskQuery) =>
      http.get<PageResult<CheckTask>>('/api/checks', { params: query }),
    /** 领取（PUT /api/checks/{id}/claim，原子指派不迁移状态） */
    claim: (id: SalesId) => http.put<CheckTask>(`/api/checks/${id}/claim`),
    /** 批量领取（POST /api/checks/batch-claim，权限复用 sales:check:claim）——
     * 契约与 picks.batchClaim 同形（BatchResult，api.md §9），结果经 BatchResultDrawer 呈现 */
    batchClaim: (ids: Array<number | string>) =>
      http.post<BatchResult>('/api/checks/batch-claim', { ids: ids.map((id) => Number(id)) }),
    /** 设置任务优先级（PUT /api/checks/{id}/priority，值域 0–9，终态 409；权限 sales:check:assign） */
    setPriority: (id: SalesId, priority: number) =>
      http.put<{ id: number; priority: number }>(`/api/checks/${id}/priority`, { priority }),
    /** 复核确认（PUT /api/checks/{id}/confirm，通过/五类异常） */
    confirm: (id: SalesId, payload: CheckConfirmPayload) =>
      http.put<CheckTask>(`/api/checks/${id}/confirm`, payload),
    /** 复核异常重开（PUT /api/checks/{id}/reopen，EXCEPTION→PENDING） */
    reopen: (id: SalesId) => http.put<CheckTask>(`/api/checks/${id}/reopen`),
  },
  packing: {
    list: (query: PackingRecordQuery) =>
      http.get<PageResult<PackingRecord>>('/api/packing', { params: query }),
    /** 打包（POST /api/packing，幂等键；全部明细打包完成推进 CHECKED→PACKED） */
    pack: (payload: PackPayload) => http.post<PackResult>('/api/packing', payload),
  },
  shipments: {
    list: (query: ShipmentQuery) =>
      http.get<PageResult<Shipment>>('/api/shipments', { params: query }),
    /** 发货确认（POST /api/shipments，PENDING→SHIPPED 触发库存正式扣减） */
    ship: (payload: ShipPayload) => http.post<ShipResult>('/api/shipments', payload),
    /** 物流态流转（PUT /api/shipments/{id}/status，SHIPPED 后续：IN_TRANSIT/SIGNED/ABNORMAL） */
    updateStatus: (id: SalesId, payload: { status: ShipmentStatus; remark?: string }) =>
      http.put<Shipment>(`/api/shipments/${id}/status`, payload),
  },
}
