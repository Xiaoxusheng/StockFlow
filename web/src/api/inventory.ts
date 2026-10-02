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
}
