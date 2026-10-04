import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 库存域（后端 T5 已交付只读查询面：GET /api/inventory、GET /api/inventory/:id、
// GET /api/inventory-ledgers、GET /api/batches、GET /api/serials，internal/inventory/inventory.go:52-56；
// 字段逐一对齐 internal/inventory/handler.go 各 View 的 JSON tag） ----------

/** 后端 database.ID JSON 序列化为字符串（backend-m1-plan §1），保留 number 兼容 */
export type InventoryId = number | string

/** 实时库存筛选（handler.go:230-245：ID 过滤全为 snake_case 正整数，0=不限） */
export interface StockQuery extends PageQuery {
  warehouse_id?: InventoryId
  zone_id?: InventoryId
  shelf_id?: InventoryId
  bin_id?: InventoryId
  sku_id?: InventoryId
  /** 批次过滤（handler.go:241-245 parseBatchIDQuery；0=非批次/不限） */
  batch_id?: InventoryId
}

/**
 * 实时库存行（InventoryView，handler.go:30-41）：五维定位 ID + 六状态数量
 * （StockState，identity.go:18-25；恒等式 total = available + locked + frozen
 * + pending_inspect + defective 由后端 CHECK 约束保证）。
 */
export interface StockItem {
  id: InventoryId
  warehouse_id: InventoryId
  zone_id: InventoryId
  shelf_id: InventoryId
  bin_id: InventoryId
  sku_id: InventoryId
  /** 0=非批次 SKU（handler.go:37） */
  batch_id: InventoryId
  total_qty: number
  available_qty: number
  locked_qty: number
  frozen_qty: number
  pending_inspect_qty: number
  defective_qty: number
  created_at: string
  updated_at: string
}

/**
 * 库存统计（GET /api/inventory/summary，reports 实现、inventory 前缀挂载：
 * internal/reports/routes.go:47 + DashboardSummary，repository.go:364-378 snake_case 输出；
 * 权限点 reports:report:read）。abnormal_qty = 冻结 + 残次（待检属正常流转态不计入）；
 * near_expiry_qty 含已过期批次。数值为 float8 聚合。
 */
export interface StockSummary {
  sku_count: number
  total_qty: number
  available_qty: number
  locked_qty: number
  frozen_qty: number
  /** 临期/已过期库存量（效期 ≤ 阈值天数的批次现存量，含已过期） */
  near_expiry_qty: number
  /** 异常库存量 = 冻结 + 残次（待检属正常流转态，不计入异常） */
  abnormal_qty: number
}

// ---------- 库存流水（GET /api/inventory-ledgers，后端 T5 已交付） ----------

/** 库存流水筛选（handler.go:285-323，参数名以 handler 读取的 query key 为准） */
export interface LedgerQuery extends PageQuery {
  warehouse_id?: InventoryId
  sku_id?: InventoryId
  bin_id?: InventoryId
  batch_id?: InventoryId
  /** 流水类型（inventory_ledgers CHECK 值域，handler.go:300-308 非法值直接报参数错误） */
  change_type?: InventoryChangeType
  business_no?: string
  serial_no?: string
  /** 时间范围：YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD（handler.go:197-214） */
  created_from?: string
  created_to?: string
}

/** 库存流水行（LedgerView，handler.go:54-78；append-only 只读） */
export interface LedgerItem {
  id: InventoryId
  ledger_no: string
  sku_id: InventoryId
  warehouse_id: InventoryId
  zone_id?: InventoryId | null
  shelf_id?: InventoryId | null
  bin_id: InventoryId
  batch_id: InventoryId
  serial_no: string
  change_type: InventoryChangeType
  business_type: string
  business_no: string
  status_from: string
  status_to: string
  qty_before: number
  /** 变更数量：正=入库、负=出库（后端无独立 direction 字段） */
  qty_change: number
  qty_after: number
  idempotency_key: string
  operator_id: InventoryId
  operator_name: string
  request_id: string
  remark: string
  created_at: string
}

// ---------- 库存预警（GET /api/inventory/alerts，reports 实现、inventory 前缀挂载：
// internal/reports/routes.go:48 + AlertItem，repository.go:407-421 snake_case 输出） ----------

/** 预警级别（handler.go:197-202 白名单校验值域） */
export type StockAlertLevel = 'low_stock' | 'overstock' | 'near_expiry' | 'expired' | 'slow_moving'

export interface StockAlertQuery extends PageQuery {
  level?: StockAlertLevel
  keyword?: string
}

/** 库存预警行（AlertItem，repository.go:407-421 字段全量；无 id/时间字段——
 * 效期类携带 batch_no，slow_moving 携带 last_moved_at，message 由后端模板组装） */
export interface StockAlertItem {
  level: StockAlertLevel
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_id: number
  sku_code: string
  sku_name: string
  current_qty: number
  threshold: number
  /** 批次号（效期类预警携带；其余为空） */
  batch_no?: string
  /** 末次移动时间（slow_moving 携带；其余为空） */
  last_moved_at?: string
  /** 预警说明（携计算依据） */
  message: string
}

// ---------- 库存锁定（GET /api/inventory/locks，后端已交付：internal/inventory/handler.go:479-516；
// 出参为 LockView snake_case（handler.go:421-438），仅裸 ID 无联表编码/操作人字段——
// 页面经 api/options.ts 一次取全基础资料后本地映射补充；qty 为 stock.Qty 裸数字出参
//（numeric(18,4)，internal/stock/qty.go:72-74 MarshalJSON 直出小数字面量） ----------

export type InventoryLockStatus = 'ACTIVE' | 'RELEASED' | 'CONSUMED'

export type InventoryLockType =
  | 'ORDER_HOLD'
  | 'COUNT_FREEZE'
  | 'QC_FREEZE'
  | 'MANUAL_FREEZE'
  | 'EXCEPTION_FREEZE'

/** 锁定记录筛选（handler.go:479-516 读参：warehouse_id/sku_id/lock_type/status/source_type/source_no） */
export interface InventoryLockQuery extends PageQuery {
  warehouse_id?: InventoryId
  sku_id?: InventoryId
  lock_type?: InventoryLockType
  status?: InventoryLockStatus
  /** 来源单据类型（与 source_no 配对，inventory-rules.md §4 规则 1） */
  source_type?: string
  source_no?: string
}

/** 锁定记录（LockView，handler.go:421-438 字段全量；released_at 零值为 null） */
export interface InventoryLockItem {
  id: InventoryId
  warehouse_id: InventoryId
  bin_id: InventoryId
  sku_id: InventoryId
  /** 0 = 非批次 */
  batch_id: InventoryId
  lock_type: InventoryLockType
  source_type: string
  source_no: string
  qty: number
  status: InventoryLockStatus
  released_at: string | null
  released_by: InventoryId
  remark: string
  created_at: string
  updated_at: string
}

// ---------- 库存调整（GET /api/inventory/adjustments，后端已交付：internal/inventory/handler.go:524-556；
// 出参为 AdjustmentView snake_case（handler.go:451-466），仅裸 ID 无联表编码/申请人字段；
// 后端无 approved_at——执行信息仅 executed_at/executed_by（handler.go:451-466 字段全量）；
// qty 为 stock.Qty 裸数字出参（numeric(18,4)，internal/stock/qty.go:72-74） ----------

export type InventoryAdjustmentStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'REJECTED'
  | 'EXECUTED'
  | 'CANCELLED'

/** 调整类型（business-flow.md §11.1 中文值域，handler.go:540 allowed 清单同源） */
export type InventoryAdjustmentType = '盘盈' | '盘亏' | '损耗' | '报废' | '其他'

/** 调整单筛选（handler.go:524-556 读参：warehouse_id/sku_id/adjust_type/status） */
export interface InventoryAdjustmentQuery extends PageQuery {
  warehouse_id?: InventoryId
  sku_id?: InventoryId
  adjust_type?: InventoryAdjustmentType
  status?: InventoryAdjustmentStatus
}

/** 调整单（AdjustmentView，handler.go:451-466 字段全量；executed_at 零值为 null） */
export interface InventoryAdjustmentItem {
  id: InventoryId
  adjustment_no: string
  warehouse_id: InventoryId
  sku_id: InventoryId
  bin_id: InventoryId
  /** 0 = 非批次 */
  batch_id: InventoryId
  adjust_type: InventoryAdjustmentType
  qty: number
  /** 申请必填原因（business-flow.md §11.1） */
  reason: string
  status: InventoryAdjustmentStatus
  executed_by: InventoryId
  executed_at: string | null
  created_at: string
  updated_at: string
}

// ---------- 批次台账（GET /api/batches，后端 T5 已交付：inventory.go:55；
// 字段对齐 BatchView，handler.go:107-119；权限点 inventory:batch:list） ----------

/** 批次筛选（handler.go:344-362） */
export interface BatchQuery extends PageQuery {
  sku_id?: InventoryId
  supplier_id?: InventoryId
  batch_no?: string
  /** 效期范围：YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD（handler.go:356-361） */
  expiry_from?: string
  expiry_to?: string
  /** 'expiry' = 按效期升序（FEFO 审阅视图，handler.go:362） */
  order?: 'expiry'
}

/** 批次（BatchView，inventory-rules §6 字段清单；批次维度库存聚合后端 M1 未下发） */
export interface BatchItem {
  id: InventoryId
  sku_id: InventoryId
  batch_no: string
  /** 供应商（batches.supplier_id 逻辑引用 masterdata，跨域不建 FK；0=未指定） */
  supplier_id: InventoryId
  production_date?: string | null
  inbound_date?: string | null
  expiry_date?: string | null
  cost_price: number
  remark: string
  created_at: string
  updated_at: string
}

// ---------- 序列号（GET /api/serials，后端 T5 已交付：inventory.go:56；
// 字段对齐 SerialView，handler.go:131-144；权限点 inventory:serial:list） ----------

/** 序列号状态（serial_numbers CHECK 约束；inventory-rules.md §8 全生命周期） */
export type SerialStatus = 'IN_STOCK' | 'LOCKED' | 'OUTBOUND' | 'RETURNED' | 'FROZEN'

/** 序列号筛选（handler.go:383-398） */
export interface SerialQuery extends PageQuery {
  warehouse_id?: InventoryId
  bin_id?: InventoryId
  sku_id?: InventoryId
  batch_id?: InventoryId
  serial_no?: string
  status?: SerialStatus
}

/** 序列号（SerialView，inventory-rules §8：一物一行 + 最近状态变化追溯指针） */
export interface SerialItem {
  id: InventoryId
  serial_no: string
  sku_id: InventoryId
  /** 0=非批次 SKU */
  batch_id: InventoryId
  /** 0=不在库（inventory-rules.md §8） */
  warehouse_id: InventoryId
  bin_id: InventoryId
  status: SerialStatus
  /** 最近一次状态变化来源单据（与 last_source_no 配对构成追溯指针） */
  last_source_type: string
  last_source_no: string
  last_event_at?: string | null
  created_at: string
  updated_at: string
}

// ---------- 库存追溯（GET /api/inventory/trace，后端已交付：returns 域实现、inventory 前缀挂载，
// internal/returns/handler.go:669-696 + service_trace.go 查询编排。查询主键为 sku_id /
// serial_no（至少其一），响应为单个非分页 TraceResult 对象（response.OK，无 items/total）；
// 各 qty 字段为 stock.Qty 字符串化 numeric(18,4) 出参，internal/stock/qty.go:72-74） ----------

/** 库存变更类型（inventory_ledgers CHECK 约束） */
export type InventoryChangeType =
  | 'INBOUND'
  | 'OUTBOUND'
  | 'TRANSFER_OUT'
  | 'TRANSFER_IN'
  | 'LOCK'
  | 'RELEASE'
  | 'MOVE'
  | 'INSPECT_PASS'
  | 'INSPECT_DEFECTIVE'
  | 'ADJUST'

/** 追溯查询入参（handler.go:675-696 读参：serial_no/sku_id/warehouse_id/limit） */
export interface TraceQuery {
  /** SKU ID（与 serial_no 至少其一，service_trace.go:92-94） */
  sku_id?: InventoryId
  /** 序列号（与 sku_id 至少其一；命中后自动定位 SKU，service_trace.go:134-147） */
  serial_no?: string
  /** 仓库 ID（0/缺省 = 数据权限内全部；多仓范围必须显式指定，service_trace.go:113-131） */
  warehouse_id?: InventoryId
  /** 追溯链上限（1-500，缺省 100，service_trace.go:87-90） */
  limit?: number
}

/** 追溯链流水（TraceLedger，internal/returns/ports.go:97-118；append-only 主轴，时间正序） */
export interface TraceLedgerItem {
  id: InventoryId
  ledger_no: string
  sku_id: InventoryId
  warehouse_id: InventoryId
  bin_id: InventoryId
  batch_id: InventoryId
  serial_no?: string
  change_type: InventoryChangeType
  business_type: string
  business_no: string
  status_from: string
  status_to: string
  qty_before: string
  qty_change: string
  qty_after: string
  operator_name: string
  request_id?: string
  remark?: string
  /** YYYY-MM-DD HH:mm:ss（ports.go:117 CreatedAtStr） */
  created_at: string
}

/** 追溯当前库存行（TraceStockRow，ports.go:129-144；六状态数量为字符串化 numeric(18,4)） */
export interface TraceStockRow {
  warehouse_id: InventoryId
  zone_id: InventoryId
  shelf_id: InventoryId
  bin_id: InventoryId
  sku_id: InventoryId
  batch_id: InventoryId
  total_qty: string
  available_qty: string
  locked_qty: string
  frozen_qty: string
  pending_inspect_qty: string
  defective_qty: string
  updated_at: string
}

/** 序列号当前台账（TraceSerialRow，ports.go:148-159；last_source_* 为最近状态变化追溯指针） */
export interface TraceSerialRow {
  serial_no: string
  sku_id: InventoryId
  batch_id: InventoryId
  warehouse_id: InventoryId
  bin_id: InventoryId
  status: SerialStatus
  last_source_type: string
  last_source_no: string
  last_event_at: string
}

/** 追溯来源单据行证据（TraceDocLine，service_trace.go:50-54） */
export interface TraceDocumentLine {
  line_no: number
  sku_id: InventoryId
  qty: string
}

/** 追溯来源单据富化（TraceDocument，service_trace.go:44-48；found=false 为正常情形，不视为错误） */
export interface TraceDocumentItem {
  /** sales_order / purchase_order */
  type: string
  no: string
  found: boolean
  warehouse_id: InventoryId
  lines?: TraceDocumentLine[]
}

/** 追溯关联操作日志（TraceOperation，service_trace.go:57-69；operation_logs 只读投影，
 * 经流水 request_id 关联） */
export interface TraceOperationItem {
  id: InventoryId
  request_id: string
  module: string
  object_type: string
  object_id: InventoryId
  action: string
  operator_id: InventoryId
  operator_name: string
  success: boolean
  created_at: string
}

/** 追溯编排结果（TraceResult，service_trace.go:72-84；非分页单对象） */
export interface TraceResult {
  sku_id: InventoryId
  serial_no?: string
  /** 0 = 数据权限内全部仓库 */
  warehouse_id: InventoryId
  stock_rows: TraceStockRow[]
  serial?: TraceSerialRow
  chain: TraceLedgerItem[]
  chain_truncated: boolean
  documents: TraceDocumentItem[]
  operations: TraceOperationItem[]
}

// ---------- 库存详情（frontend.md §10.3；后端 T5 已交付 GET /api/inventory/{id}：
// :id 为库存行 int64 id（handler.go:260-275 parseIDParam），非 SKU 编码；返回同 InventoryView） ----------

/** 库存行详情 = InventoryView 形态（handler.go:274 newInventoryView） */
export type InventoryDetail = StockItem

// ---------- 库存分布（frontend.md §10.4 层级视图：仓库 → 库区 → 库位，支持点击下钻；
// 前端先行契约（后端 M1 库存查询面未含层级分布），字段名按后端 snake_case JSON tag 惯例预对齐） ----------

/**
 * 分布层级节点：仓库层（children=库区或库位）/ 库区层（children=库位）/ 库位层（叶子）。
 * 仓库未划库区时允许后端直接返回「仓库 → 库位」两层（§10.4 示例即此形态）。
 */
export interface StockDistributionNode {
  warehouse_code: string
  warehouse_name?: string
  zone_code?: string
  bin_code?: string
  total_qty: number
  available_qty?: number
  children?: StockDistributionNode[]
}

// ---------- 库存分析（frontend.md §10.1 口径：库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势；
// 前端先行契约：分析域后端 M2+ 交付（backend-m1-plan.md §13），就绪前页面呈统一错误态；
// 字段名按后端 snake_case JSON tag 惯例预对齐，后端立项时以实际 dto 为准复核） ----------

export interface InventoryAnalyticsQuery {
  /** 趋势窗口天数（默认 30；仅影响 trend 字段） */
  days?: number
}

/** ABC 分类项（按库存金额 80/15/5 阈值分档由后端计算，前端不得自行分档） */
export interface AnalyticsAbcItem {
  grade: 'A' | 'B' | 'C'
  sku_count: number
  value_amount: number
  /** 金额占比 0~100 */
  value_percent: number
}

/** 库存趋势点（date 为 YYYY-MM-DD） */
export interface AnalyticsTrendPoint {
  date: string
  total_qty: number
  stock_value: number
}

export interface InventoryAnalytics {
  /** 库存金额（∑ 数量 × 成本价，口径以后端契约为准） */
  total_stock_value: number
  total_sku_count: number
  total_qty: number
  /** 库存周转率（次 / 统计周期） */
  turnover_rate: number
  /** 库存周转天数（天） */
  turnover_days: number
  abc: AnalyticsAbcItem[]
  trend: AnalyticsTrendPoint[]
}

export const inventoryApi = {
  stock: (query: StockQuery) =>
    http.get<PageResult<StockItem>>('/api/inventory', { params: query }),
  stockSummary: () => http.get<StockSummary>('/api/inventory/summary'),
  ledger: (query: LedgerQuery) =>
    http.get<PageResult<LedgerItem>>('/api/inventory-ledgers', { params: query }),
  alerts: (query: StockAlertQuery) =>
    http.get<PageResult<StockAlertItem>>('/api/inventory/alerts', { params: query }),
  locks: (query: InventoryLockQuery) =>
    http.get<PageResult<InventoryLockItem>>('/api/inventory/locks', { params: query }),
  adjustments: (query: InventoryAdjustmentQuery) =>
    http.get<PageResult<InventoryAdjustmentItem>>('/api/inventory/adjustments', { params: query }),
  batches: (query: BatchQuery) =>
    http.get<PageResult<BatchItem>>('/api/batches', { params: query }),
  serials: (query: SerialQuery) =>
    http.get<PageResult<SerialItem>>('/api/serials', { params: query }),
  /** 库存追溯（GET /api/inventory/trace，returns 域编排：sku_id/serial_no 至少其一；
   * 响应为单个 TraceResult 非分页对象，无 items/total） */
  trace: (query: TraceQuery) =>
    http.get<TraceResult>('/api/inventory/trace', { params: query }),
  /** 库存行详情：GET /api/inventory/{id}（:id 为库存行 int64 id，handler.go:260-275） */
  stockDetail: (id: InventoryId) =>
    http.get<InventoryDetail>(`/api/inventory/${encodeURIComponent(String(id))}`),
  /** 库存层级分布（frontend.md §10.4 仓库 → 库区 → 库位；前端先行契约，路径段语义同详情） */
  stockDistribution: (id: InventoryId) =>
    http.get<StockDistributionNode[]>(
      `/api/inventory/${encodeURIComponent(String(id))}/distribution`,
    ),
  /** 库存分析汇总（frontend.md §10.1 口径；前端先行契约，后端 M2+ 交付） */
  analytics: (query?: InventoryAnalyticsQuery) =>
    http.get<InventoryAnalytics>('/api/inventory/analytics', { params: query }),
}

// 注：库存详情（/inventory/stock/:id）为无菜单动态段路由（frontend.md §10.2「点击行进入库存详情」），
//     路径段承载库存行 id（GET /api/inventory/{id} 的 :id，router 沿用旧段名注册，本文件只负责 API 契约）。
//     库存盘点（/inventory/count）按既有裁决由盘点中心 /counts 统一承载，不做库存盘点页签（frontend.md §10.5）。
