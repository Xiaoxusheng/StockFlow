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

/** 库存统计（frontend.md §10.2 第一行） */
export interface StockSummary {
  skuCount: number
  totalQty: number
  availableQty: number
  lockedQty: number
  frozenQty: number
  nearExpiryQty: number
  abnormalQty: number
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

// ---------- 库存预警（/api/inventory/alerts） ----------

export type StockAlertLevel = 'low_stock' | 'overstock' | 'near_expiry' | 'expired' | 'slow_moving'

export interface StockAlertQuery extends PageQuery {
  level?: StockAlertLevel
  keyword?: string
}

export interface StockAlertItem {
  id: number | string
  level: StockAlertLevel
  skuCode: string
  productName: string
  warehouseName: string
  binCode?: string
  currentQty: number
  threshold?: number
  message: string
  createdAt: string
}

// ---------- 库存锁定（/api/inventory/locks；契约未冻结，值域对齐 db/migrations/000005 inventory_locks + inventory-rules.md §4） ----------

export type InventoryLockStatus = 'ACTIVE' | 'RELEASED' | 'CONSUMED'

export type InventoryLockType =
  | 'ORDER_HOLD'
  | 'COUNT_FREEZE'
  | 'QC_FREEZE'
  | 'MANUAL_FREEZE'
  | 'EXCEPTION_FREEZE'

export interface InventoryLockQuery extends PageQuery {
  keyword?: string
  /** 按 SKU 过滤（库存详情页「库存锁定」页签复用本端点，frontend.md §10.3） */
  skuCode?: string
  warehouseCode?: string
  lockType?: InventoryLockType
  status?: InventoryLockStatus
}

export interface InventoryLockItem {
  id: number | string
  skuCode: string
  productName: string
  warehouseCode?: string
  warehouseName: string
  binCode?: string
  batchNo?: string
  lockType: InventoryLockType
  /** 来源单据类型（与 sourceNo 配对，inventory-rules.md §4 规则 1） */
  sourceType: string
  sourceNo: string
  qty: number
  status: InventoryLockStatus
  remark?: string
  createdByName?: string
  createdAt: string
  releasedByName?: string
  releasedAt?: string
}

// ---------- 库存调整（/api/inventory/adjustments；契约未冻结，值域对齐 db/migrations/000005 inventory_adjustments + business-flow.md §11.1） ----------

export type InventoryAdjustmentStatus =
  | 'DRAFT'
  | 'PENDING_APPROVAL'
  | 'APPROVED'
  | 'REJECTED'
  | 'EXECUTED'
  | 'CANCELLED'

/** 调整类型（business-flow.md §11.1 中文值域，与 DB CHECK 约束一致） */
export type InventoryAdjustmentType = '盘盈' | '盘亏' | '损耗' | '报废' | '其他'

export interface InventoryAdjustmentQuery extends PageQuery {
  keyword?: string
  warehouseCode?: string
  adjustType?: InventoryAdjustmentType
  status?: InventoryAdjustmentStatus
}

export interface InventoryAdjustmentItem {
  id: number | string
  adjustmentNo: string
  skuCode: string
  productName: string
  warehouseCode?: string
  warehouseName: string
  binCode?: string
  batchNo?: string
  adjustType: InventoryAdjustmentType
  qty: number
  /** 申请必填原因（business-flow.md §11.1） */
  reason: string
  status: InventoryAdjustmentStatus
  createdByName?: string
  createdAt: string
  approvedAt?: string
  executedAt?: string
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

// ---------- 库存转移（/api/inventory/transfers；前端先行骨架：调拨单 M1 未建表，
// 维度与状态机对齐 business-flow.md §10.1（仓库→仓库 / 库位→库位），后端冻结时再对齐字段） ----------

/** 调拨维度（business-flow.md §10.1；枚举前端先行，后端冻结前仅用于筛选传参） */
export type InventoryTransferType = 'warehouse' | 'bin'

/** 调拨单状态机（business-flow.md §10.1；枚举前端先行） */
export type InventoryTransferStatus =
  | 'draft'
  | 'pending_review'
  | 'pending_outbound'
  | 'transferring'
  | 'pending_inbound'
  | 'completed'
  | 'cancelled'

export interface InventoryTransferQuery extends PageQuery {
  keyword?: string
  transferType?: InventoryTransferType
  status?: InventoryTransferStatus
}

export interface InventoryTransferItem {
  id: number | string
  /** 调拨单号（business-flow.md §13：TR-日期-流水） */
  transferNo: string
  transferType: InventoryTransferType
  sourceWarehouseName: string
  targetWarehouseName: string
  sourceBinCode?: string
  targetBinCode?: string
  skuCode: string
  productName: string
  qty: number
  status: InventoryTransferStatus
  createdByName?: string
  createdAt: string
  completedAt?: string
}

// ---------- 库存追溯（/api/inventory/trace；前端先行骨架：追溯数据源为 inventory_ledgers
// （append-only，db/migrations/000005），字段对齐其列；查询支持 SKU / 序列号 / 批次号 / 单据号） ----------

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

export interface TraceQuery extends PageQuery {
  skuCode?: string
  serialNo?: string
  batchNo?: string
  bizNo?: string
}

export interface TraceItem {
  id: number | string
  /** 事件时间（inventory_ledgers.created_at；append-only 无 updated_at） */
  occurredAt: string
  changeType: InventoryChangeType
  bizType: string
  bizNo: string
  skuCode: string
  productName: string
  warehouseName: string
  binCode?: string
  batchNo?: string
  serialNo?: string
  /** 状态三态口径（backend-m1-plan.md §8.4：受影响状态列 status_from → status_to） */
  statusFrom?: string
  statusTo?: string
  qtyChange: number
  qtyAfter: number
  operatorName: string
  remark?: string
}

// ---------- 库存详情（frontend.md §10.3；后端 T5 已交付 GET /api/inventory/{id}：
// :id 为库存行 int64 id（handler.go:260-275 parseIDParam），非 SKU 编码；返回同 InventoryView） ----------

/** 库存行详情 = InventoryView 形态（handler.go:274 newInventoryView） */
export type InventoryDetail = StockItem

// ---------- 库存分布（frontend.md §10.4 层级视图：仓库 → 库区 → 库位，支持点击下钻） ----------

/**
 * 分布层级节点：仓库层（children=库区或库位）/ 库区层（children=库位）/ 库位层（叶子）。
 * 仓库未划库区时允许后端直接返回「仓库 → 库位」两层（§10.4 示例即此形态）。
 */
export interface StockDistributionNode {
  warehouseCode: string
  warehouseName?: string
  zoneCode?: string
  binCode?: string
  totalQty: number
  availableQty?: number
  children?: StockDistributionNode[]
}

// ---------- 库存分析（frontend.md §10.1 口径：库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势；
// 前端先行契约：分析域后端 M2+ 交付（backend-m1-plan.md §13），就绪前页面呈统一错误态） ----------

export interface InventoryAnalyticsQuery {
  /** 趋势窗口天数（默认 30；仅影响 trend 字段） */
  days?: number
}

/** ABC 分类项（按库存金额 80/15/5 阈值分档由后端计算，前端不得自行分档） */
export interface AnalyticsAbcItem {
  grade: 'A' | 'B' | 'C'
  skuCount: number
  valueAmount: number
  /** 金额占比 0~100 */
  valuePercent: number
}

/** 库存趋势点（date 为 YYYY-MM-DD） */
export interface AnalyticsTrendPoint {
  date: string
  totalQty: number
  stockValue: number
}

export interface InventoryAnalytics {
  /** 库存金额（∑ 数量 × 成本价，口径以后端契约为准） */
  totalStockValue: number
  totalSkuCount: number
  totalQty: number
  /** 库存周转率（次 / 统计周期） */
  turnoverRate: number
  /** 库存周转天数（天） */
  turnoverDays: number
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
  transfers: (query: InventoryTransferQuery) =>
    http.get<PageResult<InventoryTransferItem>>('/api/inventory/transfers', { params: query }),
  trace: (query: TraceQuery) =>
    http.get<PageResult<TraceItem>>('/api/inventory/trace', { params: query }),
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
