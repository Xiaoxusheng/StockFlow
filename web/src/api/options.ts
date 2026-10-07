import {
  OPTIONS_PAGE_SIZE,
  masterdataApi,
  type CategoryItem,
  type CustomerItem,
  type ProductItem,
  type SkuItem,
  type SupplierItem,
  type UnitItem,
} from './masterdata'
import { binApi, warehouseApi, type BinItem, type WarehouseItem } from './warehouse'
import { userApi, type UserItem } from './user'
import { inventoryApi, type BatchItem } from './inventory'
import type { PageResult } from '@/types/api'

// ---------- 基础资料 options 拉取与 ID→编码/名称映射助手 ----------
//
// 后端列表视图（库存/单据/设备等）出参为五维定位 ID（如 InventoryView、TransferItemView），
// 不联表下发编码/名称——页面经本文件一次取全基础资料后本地映射补充，映射失败降级为 ID，
// 不造假数据（对齐 views/pad/inventory/PadInventoryPage.tsx:76-93 既有模式）。
//
// 量级前提：仓库/客户/供应商/SKU/用户量级有限，一页取全（OPTIONS_PAGE_SIZE=100，对齐
// 后端 MaxPageSize，response.go:47）；库位/批次量级可能超一页，分页取全由 fetchAllPages
// 承担（仍受认证与数据权限约束）。

/** 页大小：下拉数据源等一次取全场景（复用 masterdata.ts 冻结常量） */
export const OPTIONS_FETCH_PAGE_SIZE = OPTIONS_PAGE_SIZE

// ---- 主数据全量 options 的规范化 queryKey（f2 修复） ----
//
// 同一 fetcher 全局共享同一缓存键：此前各页面为 fetchSkuOptions/fetchWarehouseOptions/
// fetchBinOptions 自起炉灶（['reports','options','skus'] / ['purchase','options','skus'] /
// ['pad','transfer','sku-options'] 等 17/11/16 个互不共享的 key），跨模块浏览即重复
// 全量拉取。统一为模块级常量后，TanStack Query 以 URL 缓存语义跨页复用——同会话内
// 同一份主数据只拉一遍（staleTime 仍由各 useQuery 自行声明）。

/** SKU 主数据全量缓存键（配 fetchSkuOptions） */
export const SKU_OPTIONS_KEY = ['options', 'skus'] as const
/** 仓库主数据全量缓存键（配 fetchWarehouseOptions） */
export const WAREHOUSE_OPTIONS_KEY = ['options', 'warehouses'] as const
/** 库位主数据全量缓存键（配 fetchBinOptions） */
export const BIN_OPTIONS_KEY = ['options', 'bins'] as const

/** sku_id 集合规范化：去重、trim、过滤空串并排序（批次 options 的 key 与请求共用） */
function normalizeSkuIds(skuIds?: ReadonlyArray<number | string>): string[] {
  return [...new Set((skuIds ?? []).map((v) => String(v).trim()).filter(Boolean))].sort()
}

/**
 * 批次 options 规范化缓存键（配 fetchBatchOptions）：sku_id 集合排序后拼接——
 * 同一集合在不同行序/数组引用下命中同一缓存（此前各列表页把行序敏感的数组直接放进
 * queryKey，翻页/排序即整组重拉；弹窗侧无过滤调用则各页自造 key，同一份全量批次
 * 被重复拉取）。空集合归一为 '' 段（无过滤取全），由调用方 enabled 守卫避免误拉。
 */
export function batchOptionsKey(skuIds?: ReadonlyArray<number | string>): readonly [string, string, string] {
  return ['options', 'batches', normalizeSkuIds(skuIds).join(',')] as const
}

/** 单页上限下的最大拉取页数（防御死循环：100×50=5000 行足够覆盖一页取全场景） */
const MAX_PAGES = 50

/**
 * 分页逐页取全（后端分页统一 page/pageSize/total/items，api.md §2.1）：
 * 直到 items 不足一页或达到 MAX_PAGES 防御上限。
 */
async function fetchAllPages<T>(fetchPage: (page: number) => Promise<PageResult<T>>): Promise<T[]> {
  const first = await fetchPage(1)
  const rows = [...first.items]
  const totalPages = Math.min(Math.ceil(first.total / OPTIONS_FETCH_PAGE_SIZE) || 1, MAX_PAGES)
  for (let page = 2; page <= totalPages; page += 1) {
    const next = await fetchPage(page)
    rows.push(...next.items)
  }
  return rows
}

/** id 字符串化键（Map 键统一 string，与 ID 出参字符串形态对齐） */
export function idKey(id: number | string): string {
  return String(id)
}

// ---- 各域一次取全（失败由调用方呈错误态/降级，本层不吞错） ----

export function fetchWarehouseOptions(): Promise<WarehouseItem[]> {
  return fetchAllPages((page) => warehouseApi.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

/**
 * SKU options 一次取全 + 商品名称装配。
 * 后端 /api/skus 列表契约不返回 product_name（omitempty 仅详情装配，service_sku.go:49、
 * changelog.md:858），而采购/入库/销售/库存等约 30 处消费点依赖 SkuItem.product_name
 * 展示「商品名称」列与下拉文案——此处按既有口径（SkuListPage.tsx:166-170 同源）
 * 用一次取全的商品数据源按 product_id 兜底映射（同一 API 的真实数据，非前端造数）。
 * 商品列表失败时降级为仅 code（消费方均有 product_name 缺省回退），不阻断 SKU options。
 */
export async function fetchSkuOptions(): Promise<SkuItem[]> {
  const [skus, products] = await Promise.all([
    fetchAllPages((page) => masterdataApi.skus.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE })),
    fetchAllPages((page) => masterdataApi.products.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE })).catch(
      () => [] as ProductItem[],
    ),
  ])
  const productNameById = new Map(products.map((p) => [idKey(p.id), p.name]))
  return skus.map((s) =>
    s.product_name ? s : { ...s, product_name: productNameById.get(idKey(s.product_id)) },
  )
}

export function fetchProductOptions(): Promise<ProductItem[]> {
  return fetchAllPages((page) => masterdataApi.products.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

export function fetchCategoryOptions(): Promise<CategoryItem[]> {
  return fetchAllPages((page) =>
    masterdataApi.categories.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }),
  )
}

export function fetchUnitOptions(): Promise<UnitItem[]> {
  return fetchAllPages((page) => masterdataApi.units.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

export function fetchCustomerOptions(): Promise<CustomerItem[]> {
  return fetchAllPages((page) => masterdataApi.customers.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

export function fetchSupplierOptions(): Promise<SupplierItem[]> {
  return fetchAllPages((page) => masterdataApi.suppliers.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

export function fetchBinOptions(): Promise<BinItem[]> {
  return fetchAllPages((page) => binApi.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

/**
 * 批次 id → batch_no 映射数据源（GET /api/batches；锁定/调整/追溯行仅下发 batch_id 裸 ID）。
 *
 * 行数据均携带 sku_id（InventoryLockItem/InventoryAdjustmentItem/Trace* 行），优先按调用方
 * 当前行去重后的 sku_id 集合逐个过滤拉取（GET /api/batches 支持 sku_id 过滤，inventory/
 * handler.go:366-372；PadStockMovePage.tsx:220 同模式）——批次台账量级可能远超一页取全上限
 * （MAX_PAGES=50 页×100 行），无过滤全量拉取超限后 batch_id→batch_no 映射降级裸 ID。
 */
export async function fetchBatchOptions(skuIds?: ReadonlyArray<number | string>): Promise<BatchItem[]> {
  const ids = normalizeSkuIds(skuIds)
  if (ids.length === 0) {
    // 无 sku_id 可按（页面尚未查询/行数据为空）：退化为无过滤取全，仍受 MAX_PAGES 防御约束
    return fetchAllPages((page) => inventoryApi.batches({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
  }
  const groups = await Promise.all(
    ids.map((id) =>
      fetchAllPages((page) =>
        inventoryApi.batches({ sku_id: id, page, pageSize: OPTIONS_FETCH_PAGE_SIZE }),
      ),
    ),
  )
  const byId = new Map<string, BatchItem>()
  for (const rows of groups) {
    for (const batch of rows) byId.set(idKey(batch.id), batch)
  }
  return [...byId.values()]
}

export function fetchUserOptions(): Promise<UserItem[]> {
  return fetchAllPages((page) => userApi.users({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE }))
}

// ---- Map 构建助手（泛型，键为 id 字符串） ----

/**
 * 构建 id → 提取值 的 Map（如 sku_id → sku.code / warehouse_id → warehouse.name）。
 * 后端 ID 出参为字符串，Map 键统一 string 化。
 */
export function buildIdMap<T>(items: T[], idOf: (item: T) => number | string, valueOf: (item: T) => string): Map<string, string> {
  const map = new Map<string, string>()
  for (const item of items) map.set(idKey(idOf(item)), valueOf(item))
  return map
}

/**
 * 构建 id → 整行的 Map（需要多个字段的场景，如 SKU 的 code + product_name）。
 * 同 id 后到覆盖先到（基础资料 ID 唯一，仅防御异常数据）。
 */
export function buildIdItemMap<T>(items: T[], idOf: (item: T) => number | string): Map<string, T> {
  const map = new Map<string, T>()
  for (const item of items) map.set(idKey(idOf(item)), item)
  return map
}

// ---- 领域便捷映射（编码/名称一次成型；查询失败抛 ApiError 由调用方降级为 ID） ----

/** 仓库 id → code/name */
export function buildWarehouseMaps(items: WarehouseItem[]): { code: Map<string, string>; name: Map<string, string> } {
  return {
    code: buildIdMap(items, (w) => w.id, (w) => w.code),
    name: buildIdMap(items, (w) => w.id, (w) => w.name),
  }
}

/** SKU id → {code, product_name}（列表无联表编码，PadInventoryPage.tsx:88-91 同口径） */
export function buildSkuMaps(items: SkuItem[]): { code: Map<string, string>; name: Map<string, string> } {
  return {
    code: buildIdMap(items, (s) => s.id, (s) => s.code),
    name: buildIdMap(items, (s) => s.id, (s) => s.product_name ?? s.code),
  }
}

/** 客户 id → code/name */
export function buildCustomerMaps(items: CustomerItem[]): { code: Map<string, string>; name: Map<string, string> } {
  return {
    code: buildIdMap(items, (c) => c.id, (c) => c.code),
    name: buildIdMap(items, (c) => c.id, (c) => c.name),
  }
}

/** 供应商 id → code/name */
export function buildSupplierMaps(items: SupplierItem[]): { code: Map<string, string>; name: Map<string, string> } {
  return {
    code: buildIdMap(items, (s) => s.id, (s) => s.code),
    name: buildIdMap(items, (s) => s.id, (s) => s.name),
  }
}

/** 库位 id → code（BinView 含 warehouse_id/zone_id/shelf_id 层级锚点，可再自行映射） */
export function buildBinCodeMap(items: BinItem[]): Map<string, string> {
  return buildIdMap(items, (b) => b.id, (b) => b.code)
}

/** 用户 id → 展示名（real_name 优先，缺省回退 username） */
export function buildUserNameMap(items: UserItem[]): Map<string, string> {
  return buildIdMap(items, (u) => u.id, (u) => u.real_name || u.username)
}
