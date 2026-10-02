import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 实时库存（docs/api.md：/api/inventory） ----------

export interface StockQuery extends PageQuery {
  keyword?: string
  warehouseCode?: string
  binCode?: string
  status?: string
}

export interface StockItem {
  id: number | string
  skuCode: string
  productName: string
  barcode?: string
  warehouseCode: string
  warehouseName: string
  zoneCode?: string
  binCode?: string
  batchNo?: string
  totalQty: number
  availableQty: number
  lockedQty: number
  frozenQty: number
  expiryDate?: string
  status: string
  updatedAt: string
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

// ---------- 库存流水（/api/inventory-ledgers） ----------

export interface LedgerQuery extends PageQuery {
  keyword?: string
  warehouseCode?: string
  bizType?: string
  direction?: 'in' | 'out'
  beginDate?: string
  endDate?: string
}

export interface LedgerItem {
  id: number | string
  createdAt: string
  bizType: string
  bizNo: string
  skuCode: string
  productName: string
  warehouseName: string
  binCode?: string
  batchNo?: string
  direction: 'in' | 'out'
  qty: number
  beforeQty: number
  afterQty: number
  operatorName: string
  remark?: string
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

// ---------- 批次库存（/api/inventory/batches；前端先行骨架，M1 不交付：
// backend-m1-plan.md §13「/api/batches、/api/serials 查询接口 M1 不交付」，M1 仅建 batches 表支撑库存维度；
// M1 库存 HTTP 面只读且仅 inventory:inventory:list / inventory:ledger:list 两个权限点
// （internal/auth/permissions.go），后端就绪前页面呈统一错误态。
// 字段对齐 db/migrations/000005 batches 表 + inventory（batch_id 维度聚合）） ----------

export interface BatchQuery extends PageQuery {
  keyword?: string
  batchNo?: string
  warehouseCode?: string
}

export interface BatchItem {
  id: number | string
  batchNo: string
  skuCode: string
  productName: string
  /** 供应商（batches.supplier_id 逻辑引用 masterdata，跨域不建 FK；0=未指定） */
  supplierName?: string
  productionDate?: string
  inboundDate?: string
  expiryDate?: string
  costPrice?: number
  /** 批次维度库存聚合（inventory 按 batch_id 汇总；契约未冻结，随 M2 入库域对齐） */
  totalQty: number
  availableQty: number
  remark?: string
  updatedAt: string
}

// ---------- 序列号（/api/inventory/serials；前端先行骨架，M1 不交付：理由同批次；
// 字段与状态值域对齐 db/migrations/000005 serial_numbers + inventory-rules.md §8） ----------

/** 序列号状态（serial_numbers CHECK 约束；inventory-rules.md §8 全生命周期） */
export type SerialStatus = 'IN_STOCK' | 'LOCKED' | 'OUTBOUND' | 'RETURNED' | 'FROZEN'

export interface SerialQuery extends PageQuery {
  keyword?: string
  warehouseCode?: string
  status?: SerialStatus
}

export interface SerialItem {
  id: number | string
  serialNo: string
  skuCode: string
  productName: string
  /** 批次（serial_numbers.batch_id 逻辑引用 batches；0=非批次 SKU） */
  batchNo?: string
  /** 仓库 / 库位（serial_numbers 0=不在库，inventory-rules.md §8） */
  warehouseName?: string
  binCode?: string
  status: SerialStatus
  /** 最近一次状态变化来源单据（与 lastSourceNo 配对构成追溯指针） */
  lastSourceType?: string
  lastSourceNo?: string
  lastEventAt?: string
  createdAt: string
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
    http.get<PageResult<BatchItem>>('/api/inventory/batches', { params: query }),
  serials: (query: SerialQuery) =>
    http.get<PageResult<SerialItem>>('/api/inventory/serials', { params: query }),
  transfers: (query: InventoryTransferQuery) =>
    http.get<PageResult<InventoryTransferItem>>('/api/inventory/transfers', { params: query }),
  trace: (query: TraceQuery) =>
    http.get<PageResult<TraceItem>>('/api/inventory/trace', { params: query }),
}

// 注：库存分析（/inventory/analytics，config/menu.tsx inventory:analytics:view）本轮预算裁剪不做，待 M2+ 补齐；
//     库存盘点（/inventory/count）按约束 5 禁做（盘点由盘点中心 /counts 统一承载），本组不涉及。
