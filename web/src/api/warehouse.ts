import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 仓库空间（契约唯一来源：internal/warehouse/dto.go / service_map.go / handler.go） ----------
// 视图 Item 的 ID/FK 为 database.ID（internal/database/model.go:22：JSON 序列化为字符串，
// 防 JS 2^53 精度丢失）；创建入参关联 ID 为 int64（dto.go:170/188/207 binding:required），
// 必须以 number 提交——encoding/json 不接受字符串，否则 400。
// status 一律 ENABLED / DISABLED（db/migrations/000004 同构）；warehouses / bins 为软删除
// 对象（database.md §5.1）提供 delete；zones / shelves 走 status 停用，无删除接口。
// 后端视图不做联表：不含 warehouse_code / warehouse_name / zone_code / shelf_code 等字段。
// 列表筛选参数名为 camelCase（handler.go:261/361/465：warehouseId / zoneId / shelfId 等）。

/** 视图 ID（后端 database.ID → JSON 字符串；保留 number 兼容，与 api/masterdata.ts 同约定） */
export type WarehouseSpaceId = number | string

/** 启停动作统一载荷（backend-m1-plan.md §5.4.1：status=ENABLED/DISABLED） */
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

// ---------- 仓库 /api/warehouses（dto.go WarehouseView / WarehouseCreateInput / WarehouseUpdateInput） ----------

export interface WarehouseQuery extends PageQuery {
  keyword?: string
  type?: string
  status?: string
}

export interface WarehouseItem {
  id: WarehouseSpaceId
  code: string
  name: string
  address: string
  contact: string
  phone: string
  area: number
  capacity: number
  type: string
  status: string
  manager_user_id: WarehouseSpaceId
  created_at?: string
  updated_at?: string
}

/** 创建/更新仓库（WarehouseCreateInput 必填 code/name；UpdateInput 同字段集为指针可选语义，
 *  status 不在更新面，走启停接口） */
export interface WarehousePayload {
  code: string
  name: string
  address?: string
  contact?: string
  phone?: string
  area?: number
  capacity?: number
  type?: string
  manager_user_id?: number
}

/** 仓库类型（迁移注释：默认 NORMAL 正常仓，值域开放随业务扩展） */
export const WAREHOUSE_TYPE_LABEL: Record<string, string> = {
  NORMAL: '正常仓',
}

// ---------- 库区 /api/zones（dto.go ZoneView / ZoneCreateInput / ZoneUpdateInput） ----------

export interface ZoneQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneType?: string
  status?: string
}

export interface ZoneItem {
  id: WarehouseSpaceId
  warehouse_id: WarehouseSpaceId
  code: string
  name: string
  zone_type: string
  capacity: number
  status: string
  created_at?: string
  updated_at?: string
}

/** 创建库区（dto.go:168-175：warehouse_id 层级锚点 binding:required，创建后不可变更） */
export interface ZonePayload {
  warehouse_id: number
  code: string
  name: string
  zone_type?: string
  capacity?: number
}

/** 更新库区（dto.go ZoneUpdateInput：warehouse_id 不可变更，更新面不含关联 ID） */
export interface ZoneUpdatePayload {
  code?: string
  name?: string
  zone_type?: string
  capacity?: number
}

/** 库区类型（迁移注释：STORAGE 存储 / PICKING 拣货 / RECEIVING 收货暂存等，值域开放） */
export const ZONE_TYPE_LABEL: Record<string, string> = {
  STORAGE: '存储区',
  PICKING: '拣货区',
  RECEIVING: '收货暂存区',
}

// ---------- 货架 /api/shelves（dto.go ShelfView / ShelfCreateInput / ShelfUpdateInput） ----------

export interface ShelfQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneId?: number | string
  status?: string
}

export interface ShelfItem {
  id: WarehouseSpaceId
  warehouse_id: WarehouseSpaceId
  zone_id: WarehouseSpaceId
  code: string
  layers: number
  columns: number
  capacity: number
  status: string
  created_at?: string
  updated_at?: string
}

/** 创建货架（dto.go:185-194：zone_id 层级锚点 binding:required；warehouse_id 可选交叉校验） */
export interface ShelfPayload {
  zone_id: number
  warehouse_id?: number
  code: string
  layers?: number
  columns?: number
  capacity?: number
}

/** 更新货架（dto.go ShelfUpdateInput：zone_id/warehouse_id 不可变更） */
export interface ShelfUpdatePayload {
  code?: string
  layers?: number
  columns?: number
  capacity?: number
}

// ---------- 库位 /api/bins（dto.go BinView / BinCreateInput / BinUpdateInput） ----------

export interface BinQuery extends PageQuery {
  keyword?: string
  warehouseId?: number | string
  zoneId?: number | string
  shelfId?: number | string
  binType?: string
  status?: string
}

export interface BinItem {
  id: WarehouseSpaceId
  warehouse_id: WarehouseSpaceId
  zone_id: WarehouseSpaceId
  shelf_id: WarehouseSpaceId
  layer: number
  column_no: number
  code: string
  bin_type: string
  max_capacity: number
  current_capacity: number
  status: string
  created_at?: string
  updated_at?: string
	/** 存量展示聚合（ListBins 装配）：非零存量 SKU 数 / 总量 / 首个（量最大）SKU 名 */
	stock_sku_count: number
	stock_total_qty: number
	stock_sku_name: string
}

/** 创建库位（dto.go:204-215：shelf_id 层级锚点 binding:required；zone_id/warehouse_id 可选交叉校验；
 *  current_capacity 由上架/移库业务维护，禁止直改） */
export interface BinPayload {
  shelf_id: number
  zone_id?: number
  warehouse_id?: number
  code: string
  bin_type?: string
  layer?: number
  column_no?: number
  max_capacity?: number
}

/** 更新库位（dto.go BinUpdateInput：zone_id/shelf_id/warehouse_id/current_capacity 不可变更） */
export interface BinUpdatePayload {
  code?: string
  bin_type?: string
  layer?: number
  column_no?: number
  max_capacity?: number
}

/** 库位类型（迁移注释：PICK 拣货位 / STORAGE 存储位 / RECEIVE 收货位等，值域开放） */
export const BIN_TYPE_LABEL: Record<string, string> = {
  PICK: '拣货位',
  STORAGE: '存储位',
  RECEIVE: '收货位',
}

// ---------- 库位地图 GET /api/warehouses/{id}/map（service_map.go:30-53） ----------
// 输出为全量层级树 { warehouse, zones: [{ zone, shelves: [{ shelf, bins }] }] }；
// 占用经 BinOccupancyReader 由库存域聚合，occupancy_status 为四值占用状态
// （锁定优先于容量状态），quantity/locked_quantity 仅在占用数据可用时返回。

/** 库位占用显示状态（service_map.go:21-26：IDLE 空闲 / PARTIAL 部分占用 / FULL 满载 /
 *  LOCKED 存在锁定中的库存） */
export type BinOccupancyStatus = 'IDLE' | 'PARTIAL' | 'FULL' | 'LOCKED'

/** 地图库位格（service_map.go:30 BinMapCell = BinView 展平 + 占用状态） */
export interface BinMapCell extends BinItem {
  occupancy_status: BinOccupancyStatus
  quantity?: number
  locked_quantity?: number
}

/** 地图货架节点（service_map.go:38 ShelfMapNode） */
export interface ShelfMapNode {
  shelf: ShelfItem
  bins: BinMapCell[]
}

/** 地图库区节点（service_map.go:44 ZoneMapNode） */
export interface ZoneMapNode {
  zone: ZoneItem
  shelves: ShelfMapNode[]
}

/** 仓库地图（service_map.go:50 WarehouseMapView） */
export interface WarehouseMap {
  warehouse: WarehouseItem
  zones: ZoneMapNode[]
}

// ---------- API 模块 ----------

export const warehouseApi = {
  list: (query: WarehouseQuery) =>
    http.get<PageResult<WarehouseItem>>('/api/warehouses', { params: query }),
  create: (payload: WarehousePayload) =>
    http.post<WarehouseItem>('/api/warehouses', payload),
  /** 详情（GET /api/warehouses/:id，handler.go:38 → getWarehouse，WarehouseView 同列表行） */
  detail: (id: WarehouseSpaceId) => http.get<WarehouseItem>(`/api/warehouses/${id}`),
  update: (id: WarehouseSpaceId, payload: WarehousePayload) =>
    http.put<WarehouseItem>(`/api/warehouses/${id}`, payload),
  /** 软删除（warehouses 为 database.md §5.1 软删除对象；级联校验由后端执行） */
  remove: (id: WarehouseSpaceId) =>
    http.delete<void>(`/api/warehouses/${id}`),
  setStatus: (id: WarehouseSpaceId, status: ResourceStatus) =>
    // 后端 handler.go:236 返回 {status}，非 Item 视图
    http.put<{ status: ResourceStatus }>(`/api/warehouses/${id}/status`, { status }),
  map: (id: WarehouseSpaceId) =>
    http.get<WarehouseMap>(`/api/warehouses/${id}/map`),
}

export const zoneApi = {
  list: (query: ZoneQuery) =>
    http.get<PageResult<ZoneItem>>('/api/zones', { params: query }),
  create: (payload: ZonePayload) =>
    http.post<ZoneItem>('/api/zones', payload),
  /** 详情（GET /api/zones/:id，handler.go:47 → getZone，ZoneView 同列表行） */
  detail: (id: WarehouseSpaceId) => http.get<ZoneItem>(`/api/zones/${id}`),
  update: (id: WarehouseSpaceId, payload: ZoneUpdatePayload) =>
    http.put<ZoneItem>(`/api/zones/${id}`, payload),
  /** zones 无删除接口：停用即下线（backend-m1-plan.md §5.4.1） */
  setStatus: (id: WarehouseSpaceId, status: ResourceStatus) =>
    // 后端 handler.go:346 返回 {status}，非 Item 视图
    http.put<{ status: ResourceStatus }>(`/api/zones/${id}/status`, { status }),
}

export const shelfApi = {
  list: (query: ShelfQuery) =>
    http.get<PageResult<ShelfItem>>('/api/shelves', { params: query }),
  create: (payload: ShelfPayload) =>
    http.post<ShelfItem>('/api/shelves', payload),
  /** 详情（GET /api/shelves/:id，handler.go:54 → getShelf，ShelfView 同列表行） */
  detail: (id: WarehouseSpaceId) => http.get<ShelfItem>(`/api/shelves/${id}`),
  update: (id: WarehouseSpaceId, payload: ShelfUpdatePayload) =>
    http.put<ShelfItem>(`/api/shelves/${id}`, payload),
  /** shelves 无删除接口：停用即下线（backend-m1-plan.md §5.4.1） */
  setStatus: (id: WarehouseSpaceId, status: ResourceStatus) =>
    // 后端 handler.go:446 返回 {status}，非 Item 视图
    http.put<{ status: ResourceStatus }>(`/api/shelves/${id}/status`, { status }),
}

export const binApi = {
  list: (query: BinQuery) =>
    http.get<PageResult<BinItem>>('/api/bins', { params: query }),
  create: (payload: BinPayload) =>
    http.post<BinItem>('/api/bins', payload),
  /** 详情（GET /api/bins/:id，handler.go:61 → getBin，BinView 不含 map 聚合的占用字段） */
  detail: (id: WarehouseSpaceId) => http.get<BinItem>(`/api/bins/${id}`),
  update: (id: WarehouseSpaceId, payload: BinUpdatePayload) =>
    http.put<BinItem>(`/api/bins/${id}`, payload),
  /** 软删除（bins 为 database.md §5.1 软删除对象；级联校验由后端执行） */
  remove: (id: WarehouseSpaceId) =>
    http.delete<void>(`/api/bins/${id}`),
  setStatus: (id: WarehouseSpaceId, status: ResourceStatus) =>
    // 后端 handler.go:565 返回 {status}，非 Item 视图
    http.put<{ status: ResourceStatus }>(`/api/bins/${id}/status`, { status }),
}
