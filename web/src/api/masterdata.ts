import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 基础资料（M1 契约域，backend-m1-plan §5.4：/api/products、/api/skus、
// /api/product-categories、/api/units、/api/suppliers、/api/customers） ----------

/** ID 序列化为字符串是后端 M1 约定（backend-m1-plan §12），保留 number 兼容数值返回 */
export type MasterdataId = number | string

/** 基础资料通用状态字面量（backend-m1-plan §5.4：status(ENABLED/DISABLED)；SKU 为 is_enabled 布尔） */
export type EnabledStatus = 'ENABLED' | 'DISABLED'

/** 后端状态字面量为大写，SfStatusTag 注册表（types/status.ts）为小写 key，渲染前统一转换 */
export function toStatusKey(status?: string | null): string {
  return status?.toLowerCase() ?? ''
}

/** 基础资料分页筛选公共参数：keyword 对编码/名称模糊匹配（筛选参数名为前端先行定义，待后端契约对齐） */
export interface MasterdataQuery extends PageQuery {
  keyword?: string
  status?: EnabledStatus
}

/** 下拉数据源等一次取全场景的页大小（基础资料量级有限；接口失败时调用方降级为空，不阻塞表单） */
export const OPTIONS_PAGE_SIZE = 200

// ---------- 商品分类（/api/product-categories） ----------

export type CategoryQuery = MasterdataQuery

export interface CategoryItem {
  id: MasterdataId
  parentId?: MasterdataId | null
  parentName?: string
  code: string
  name: string
  sort?: number
  status: EnabledStatus
  createdAt?: string
  updatedAt?: string
}

export interface CategorySavePayload {
  parentId?: string | null
  code: string
  name: string
  sort?: number
  status?: EnabledStatus
}

// ---------- 计量单位（/api/units） ----------

export type UnitQuery = MasterdataQuery

export interface UnitItem {
  id: MasterdataId
  code: string
  name: string
  status: EnabledStatus
  createdAt?: string
  updatedAt?: string
}

export interface UnitSavePayload {
  code: string
  name: string
  status?: EnabledStatus
}

// ---------- 商品（/api/products，backend-m1-plan §5.4 字段全量） ----------

export interface ProductQuery extends MasterdataQuery {
  categoryId?: string
}

export interface ProductItem {
  id: MasterdataId
  code: string
  name: string
  shortName?: string
  categoryId?: MasterdataId | null
  categoryName?: string
  brand?: string
  model?: string
  spec?: string
  unitId?: MasterdataId | null
  unitName?: string
  weight?: number
  length?: number
  width?: number
  height?: number
  volume?: number
  description?: string
  remark?: string
  status: EnabledStatus
  createdAt?: string
  updatedAt?: string
}

export interface ProductSavePayload {
  code: string
  name: string
  shortName?: string
  categoryId?: string
  brand?: string
  model?: string
  spec?: string
  unitId?: string
  weight?: number
  length?: number
  width?: number
  height?: number
  volume?: number
  description?: string
  remark?: string
  status?: EnabledStatus
}

// ---------- SKU（/api/skus，backend-m1-plan §5.4 字段全量） ----------

export interface SkuQuery extends MasterdataQuery {
  productId?: string
}

export interface SkuItem {
  id: MasterdataId
  code: string
  productId: MasterdataId
  productName?: string
  /** spec_attrs 为 jsonb（backend-m1-plan §5.4），键结构待后端契约细化，暂不在列表渲染 */
  specAttrs?: Record<string, unknown>
  costPrice?: number
  salePrice?: number
  safetyStock?: number
  maxStock?: number
  minReplenishQty?: number
  isBatchManaged?: boolean
  isExpiryManaged?: boolean
  isSerialManaged?: boolean
  isEnabled?: boolean
  createdAt?: string
  updatedAt?: string
}

export interface SkuSavePayload {
  code: string
  productId: string
  specAttrs?: Record<string, unknown>
  costPrice?: number
  salePrice?: number
  safetyStock?: number
  maxStock?: number
  minReplenishQty?: number
  isBatchManaged?: boolean
  isExpiryManaged?: boolean
  isSerialManaged?: boolean
  isEnabled?: boolean
}

// ---------- 供应商（/api/suppliers） ----------

export type SupplierQuery = MasterdataQuery

export interface SupplierItem {
  id: MasterdataId
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  remark?: string
  status: EnabledStatus
  createdAt?: string
  updatedAt?: string
}

export interface SupplierSavePayload {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  remark?: string
  status?: EnabledStatus
}

// ---------- 客户（/api/customers） ----------

export type CustomerQuery = MasterdataQuery

export interface CustomerItem {
  id: MasterdataId
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  shippingAddress?: string
  status: EnabledStatus
  createdAt?: string
  updatedAt?: string
}

export interface CustomerSavePayload {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  shippingAddress?: string
  status?: EnabledStatus
}

// ---------- API（列表 + CRUD /{id}，任务组对接清单） ----------

export const masterdataApi = {
  products: {
    list: (query: ProductQuery) =>
      http.get<PageResult<ProductItem>>('/api/products', { params: query }),
    create: (payload: ProductSavePayload) => http.post<unknown>('/api/products', payload),
    update: (id: MasterdataId, payload: ProductSavePayload) =>
      http.put<unknown>(`/api/products/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/products/${id}`),
  },
  skus: {
    list: (query: SkuQuery) => http.get<PageResult<SkuItem>>('/api/skus', { params: query }),
    create: (payload: SkuSavePayload) => http.post<unknown>('/api/skus', payload),
    update: (id: MasterdataId, payload: SkuSavePayload) =>
      http.put<unknown>(`/api/skus/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/skus/${id}`),
  },
  categories: {
    list: (query: CategoryQuery) =>
      http.get<PageResult<CategoryItem>>('/api/product-categories', { params: query }),
    create: (payload: CategorySavePayload) => http.post<unknown>('/api/product-categories', payload),
    update: (id: MasterdataId, payload: CategorySavePayload) =>
      http.put<unknown>(`/api/product-categories/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/product-categories/${id}`),
  },
  units: {
    list: (query: UnitQuery) => http.get<PageResult<UnitItem>>('/api/units', { params: query }),
    create: (payload: UnitSavePayload) => http.post<unknown>('/api/units', payload),
    update: (id: MasterdataId, payload: UnitSavePayload) =>
      http.put<unknown>(`/api/units/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/units/${id}`),
  },
  suppliers: {
    list: (query: SupplierQuery) =>
      http.get<PageResult<SupplierItem>>('/api/suppliers', { params: query }),
    create: (payload: SupplierSavePayload) => http.post<unknown>('/api/suppliers', payload),
    update: (id: MasterdataId, payload: SupplierSavePayload) =>
      http.put<unknown>(`/api/suppliers/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/suppliers/${id}`),
  },
  customers: {
    list: (query: CustomerQuery) =>
      http.get<PageResult<CustomerItem>>('/api/customers', { params: query }),
    create: (payload: CustomerSavePayload) => http.post<unknown>('/api/customers', payload),
    update: (id: MasterdataId, payload: CustomerSavePayload) =>
      http.put<unknown>(`/api/customers/${id}`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/customers/${id}`),
  },
}
