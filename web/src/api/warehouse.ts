import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 仓库空间（docs/api.md §1、backend-m1-plan.md §5.4） ----------
// 值域来源：db/migrations/000004_create_warehouse_tables.up.sql
//   * status 一律 ENABLED / DISABLED（warehouses/zones/shelves/bins 同构）；
//   * warehouses / bins 为软删除对象（database.md §5.1），提供 delete；
//     zones / shelves 走 status 停用，无删除接口。

/** 启停动作统一载荷（backend-m1-plan.md §5.4.1：status=启用/停用） */
export type ResourceStatus = 'ENABLED' | 'DISABLED'

/** 后端状态枚举为大写（迁移 000004），前端状态注册表为小写键（types/status.ts）：
 *  展示层统一转小写交给 SfStatusTag，颜色仍由全局注册表唯一决定。 */
export function toStatusKey(status?: string | null): string | undefined {
  return status ? status.toLowerCase() : undefined
}

/** 状态筛选/标签通用选项（大写值为后端枚举） */
export const RESOURCE_STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

// ---------- 仓库 /api/warehouses ----------

export interface WarehouseQuery extends PageQuery {
  keyword?: string
  type?: string
  status?: string
}

export interface WarehouseItem {
  id: number | string
  code: string
  name: string
  type?: string
  address?: string
  contact?: string
  phone?: string
  area?: number
  capacity?: number
  managerUserId?: number
  status: string
  createdAt?: string
  updatedAt?: string
}

export interface WarehousePayload {
  code: string
  name: string
  type?: string
  address?: string
  contact?: string
  phone?: string
  area?: number
  capacity?: number
  managerUserId?: number
}

/** 仓库类型（迁移注释：默认 NORMAL 正常仓，值域随业务扩展由应用层校验） */
export const WAREHOUSE_TYPE_LABEL: Record<string, string> = {
  NORMAL: '正常仓',
}

// ---------- 库区 /api/zones ----------

export interface ZoneQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneType?: string
  status?: string
}

export interface ZoneItem {
  id: number | string
  warehouseId?: number | string
  warehouseCode?: string
  warehouseName?: string
  code: string
  name: string
  zoneType?: string
  capacity?: number
  status: string
  createdAt?: string
  updatedAt?: string
}

export interface ZonePayload {
  warehouseId: number | string
  code: string
  name: string
  zoneType?: string
  capacity?: number
}

/** 库区类型（迁移注释：STORAGE 存储 / PICKING 拣货 / RECEIVING 收货暂存等） */
export const ZONE_TYPE_LABEL: Record<string, string> = {
  STORAGE: '存储区',
  PICKING: '拣货区',
  RECEIVING: '收货暂存区',
}

// ---------- 货架 /api/shelves ----------

export interface ShelfQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneId?: number | string
  status?: string
}

export interface ShelfItem {
  id: number | string
  warehouseId?: number | string
  warehouseCode?: string
  warehouseName?: string
  zoneId?: number | string
  zoneCode?: string
  zoneName?: string
  code: string
  layers?: number
  columns?: number
  capacity?: number
  status: string
  createdAt?: string
  updatedAt?: string
}

export interface ShelfPayload {
  warehouseId: number | string
  zoneId: number | string
  code: string
  layers?: number
  columns?: number
  capacity?: number
}

// ---------- 库位 /api/bins ----------

export interface BinQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneId?: number | string
  shelfId?: number | string
  binType?: string
  status?: string
}

export interface BinItem {
  id: number | string
  warehouseId?: number | string
  warehouseCode?: string
  warehouseName?: string
  zoneId?: number | string
  zoneCode?: string
  shelfId?: number | string
  shelfCode?: string
  layer?: number
  columnNo?: number
  code: string
  binType?: string
  maxCapacity?: number
  currentCapacity?: number
  status: string
  createdAt?: string
  updatedAt?: string
}

export interface BinPayload {
  warehouseId: number | string
  zoneId: number | string
  shelfId: number | string
  layer?: number
  columnNo?: number
  code: string
  binType?: string
  maxCapacity?: number
}

/** 库位类型（迁移注释：PICK 拣货位 / STORAGE 存储位 / RECEIVE 收货位等） */
export const BIN_TYPE_LABEL: Record<string, string> = {
  PICK: '拣货位',
  STORAGE: '存储位',
  RECEIVE: '收货位',
}

// ---------- 库位地图 GET /api/warehouses/{id}/map ----------
// backend-m1-plan.md §5.4：按仓库返回区/架/位网格与占用状态（占用经 BinOccupancyReader 由库存域聚合）。
// 库位占用状态值域对齐 types/status.ts：idle/partially_occupied/full/locked/frozen/abnormal（frontend.md §11）。

export interface WarehouseMapBin {
  id: number | string
  code: string
  binType?: string
  layer: number
  columnNo: number
  maxCapacity?: number
  currentCapacity?: number
  /** 占用状态；后端未就绪时页面呈现统一错误态 */
  status?: string
}

export interface WarehouseMapShelf {
  id: number | string
  code: string
  layers: number
  columns: number
  bins: WarehouseMapBin[]
}

export interface WarehouseMapZone {
  id: number | string
  code: string
  name: string
  zoneType?: string
  status?: string
  shelves: WarehouseMapShelf[]
}

export interface WarehouseMap {
  warehouse: {
    id: number | string
    code: string
    name: string
  }
  zones: WarehouseMapZone[]
}

// ---------- API 模块 ----------

export const warehouseApi = {
  list: (query: WarehouseQuery) =>
    http.get<PageResult<WarehouseItem>>('/api/warehouses', { params: query }),
  create: (payload: WarehousePayload) =>
    http.post<WarehouseItem>('/api/warehouses', payload),
  update: (id: number | string, payload: WarehousePayload) =>
    http.put<WarehouseItem>(`/api/warehouses/${id}`, payload),
  /** 软删除（warehouses 为 database.md §5.1 软删除对象；级联校验由后端执行） */
  remove: (id: number | string) =>
    http.delete<void>(`/api/warehouses/${id}`),
  setStatus: (id: number | string, status: ResourceStatus) =>
    http.put<WarehouseItem>(`/api/warehouses/${id}/status`, { status }),
  map: (id: number | string) =>
    http.get<WarehouseMap>(`/api/warehouses/${id}/map`),
}

export const zoneApi = {
  list: (query: ZoneQuery) =>
    http.get<PageResult<ZoneItem>>('/api/zones', { params: query }),
  create: (payload: ZonePayload) =>
    http.post<ZoneItem>('/api/zones', payload),
  update: (id: number | string, payload: ZonePayload) =>
    http.put<ZoneItem>(`/api/zones/${id}`, payload),
  /** zones 无删除接口：停用即下线（backend-m1-plan.md §5.4.1） */
  setStatus: (id: number | string, status: ResourceStatus) =>
    http.put<ZoneItem>(`/api/zones/${id}/status`, { status }),
}

export const shelfApi = {
  list: (query: ShelfQuery) =>
    http.get<PageResult<ShelfItem>>('/api/shelves', { params: query }),
  create: (payload: ShelfPayload) =>
    http.post<ShelfItem>('/api/shelves', payload),
  update: (id: number | string, payload: ShelfPayload) =>
    http.put<ShelfItem>(`/api/shelves/${id}`, payload),
  /** shelves 无删除接口：停用即下线（backend-m1-plan.md §5.4.1） */
  setStatus: (id: number | string, status: ResourceStatus) =>
    http.put<ShelfItem>(`/api/shelves/${id}/status`, { status }),
}

export const binApi = {
  list: (query: BinQuery) =>
    http.get<PageResult<BinItem>>('/api/bins', { params: query }),
  create: (payload: BinPayload) =>
    http.post<BinItem>('/api/bins', payload),
  update: (id: number | string, payload: BinPayload) =>
    http.put<BinItem>(`/api/bins/${id}`, payload),
  /** 软删除（bins 为 database.md §5.1 软删除对象；级联校验由后端执行） */
  remove: (id: number | string) =>
    http.delete<void>(`/api/bins/${id}`),
  setStatus: (id: number | string, status: ResourceStatus) =>
    http.put<BinItem>(`/api/bins/${id}/status`, { status }),
}
