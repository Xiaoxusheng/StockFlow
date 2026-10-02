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

export const inventoryApi = {
  stock: (query: StockQuery) =>
    http.get<PageResult<StockItem>>('/api/inventory', { params: query }),
  stockSummary: () => http.get<StockSummary>('/api/inventory/summary'),
  ledger: (query: LedgerQuery) =>
    http.get<PageResult<LedgerItem>>('/api/inventory-ledgers', { params: query }),
  alerts: (query: StockAlertQuery) =>
    http.get<PageResult<StockAlertItem>>('/api/inventory/alerts', { params: query }),
}
