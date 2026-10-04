import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 基础资料（M1 契约域，backend-m1-plan §5.4：/api/products、/api/skus、
// /api/product-categories、/api/units、/api/suppliers、/api/customers） ----------
//
// 字段名与后端 View/Input JSON tag 全量对齐（internal/masterdata/service_*.go）：
//   - Item 视图 id/category_id/unit_id/product_id/parent_id 为 database.ID，
//     JSON 出参为字符串（internal/database/model.go:22 MarshalJSON）；
//   - Create/Update 入参外键为 *int64（service_product.go:104 等），提交必须用
//     number（传字符串会 400 COMMON_INVALID_PARAM），故 SavePayload 关联 ID 一律 number。

/** 视图出参 ID（后端序列化为字符串，保留 number 兼容） */
export type MasterdataId = number | string

/** 基础资料通用状态字面量（models.go:21-24：ENABLED/DISABLED；SKU 为 is_enabled 布尔） */
export type EnabledStatus = 'ENABLED' | 'DISABLED'

/** 后端状态字面量为大写，SfStatusTag 注册表（types/status.ts）为小写 key，渲染前统一转换 */
export function toStatusKey(status?: string | null): string {
  return status?.toLowerCase() ?? ''
}

/** 通用启停请求体（internal/masterdata/handler.go:148-150 StatusRequest） */
export interface StatusPayload {
  status: EnabledStatus
}

/** SKU 启停请求体（handler.go:184-186 SKUStatusRequest：enabled 布尔） */
export interface SkuStatusPayload {
  enabled: boolean
}

/** 基础资料分页筛选公共参数：keyword 对编码/名称模糊匹配，status 为 ENABLED/DISABLED
 * （handler.go:81-102/294-326/389-405/468-484/559-575 列表入参） */
export interface MasterdataQuery extends PageQuery {
  keyword?: string
  status?: EnabledStatus
}

/**
 * 下拉数据源等一次取全场景的页大小（基础资料量级有限；接口失败时调用方降级为空，不阻塞表单）。
 * 上限对齐后端 response.ParsePage 的 MaxPageSize=100（internal/response/response.go:47，
 * 越界直接报参数错误而非 clamp——200 会 400），需要更多行由调用方分页取全（api/options.ts fetchAllPages）。
 */
export const OPTIONS_PAGE_SIZE = 100

// ---------- 商品分类（/api/product-categories，service_category.go:24-33 CategoryView） ----------

export interface CategoryQuery extends MasterdataQuery {
  /** 上级筛选：0=仅顶级（parent_id IS NULL），>0=该分类下（handler.go:305-319） */
  parent_id?: number | string
}

export interface CategoryItem {
  id: MasterdataId
  parent_id?: MasterdataId | null
  code: string
  name: string
  sort: number
  status: EnabledStatus
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（CategoryCreateInput/CategoryUpdateInput，service_category.go:72-77/134-138；
 * 后端无 status 字段——创建恒 ENABLED、启停走专用接口，编码创建后不可改） */
export interface CategorySavePayload {
  /** 创建：null=顶级；更新：0=提升为顶级 / >0=换上级 / null=不修改（service_category.go:133 三态） */
  parent_id?: number | null
  code: string
  name: string
  sort?: number
}

// ---------- 计量单位（/api/units，service_category.go:49-56 UnitView） ----------

export type UnitQuery = MasterdataQuery

export interface UnitItem {
  id: MasterdataId
  code: string
  name: string
  status: EnabledStatus
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（UnitCreateInput/UnitUpdateInput，service_category.go:320-323/356-358；
 * 后端无 status 字段——创建恒 ENABLED、启停走专用接口，编码创建后不可改） */
export interface UnitSavePayload {
  code: string
  name: string
}

// ---------- 商品（/api/products，service_product.go:23-46 ProductView 字段全量） ----------

export interface ProductQuery extends MasterdataQuery {
  /** 分类筛选（handler.go:86 queryInt64("category_id")） */
  category_id?: string
}

export interface ProductItem {
  id: MasterdataId
  code: string
  name: string
  short_name?: string
  category_id?: MasterdataId | null
  /** 详情装配字段（列表 omitempty 不返回，service_product.go:29） */
  category_name?: string
  brand?: string
  model?: string
  spec?: string
  unit_id?: MasterdataId | null
  /** 详情装配字段（列表 omitempty 不返回，service_product.go:34） */
  unit_name?: string
  weight?: number
  length?: number
  width?: number
  height?: number
  volume?: number
  /** 图片 URL 列表（jsonb；上传接口随阶段 14 文件中心交付，backend-m1-plan §9） */
  image_urls?: string[]
  description?: string
  remark?: string
  status: EnabledStatus
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（ProductCreateInput/ProductUpdateInput，service_product.go:100-136；
 * 后端无 status/code 修改字段——创建恒 ENABLED、启停走专用接口，编码创建后不可改） */
export interface ProductSavePayload {
  code: string
  name: string
  short_name?: string
  /** *int64：提交 number；null/省略=创建无分类 / 更新不修改（service_product.go:123 指针三态） */
  category_id?: number | null
  brand?: string
  model?: string
  spec?: string
  /** *int64：提交 number；null/省略=创建无单位 / 更新不修改 */
  unit_id?: number | null
  weight?: number
  length?: number
  width?: number
  height?: number
  volume?: number
  image_urls?: string[]
  description?: string
  remark?: string
}

// ---------- SKU（/api/skus，service_sku.go:31-58 BarcodeView/SKUView 字段全量） ----------

/** SKU 条码视图（BarcodeView，service_sku.go:31-36；列表批量装配返回） */
export interface SkuBarcodeItem {
  id: MasterdataId
  barcode: string
  code_type: string
  is_primary: boolean
}

/** 条码入参（BarcodeInput，service_sku.go:139-143；code_type 缺省 CODE128） */
export interface SkuBarcodePayload {
  barcode: string
  code_type?: string
  is_primary?: boolean
}

export interface SkuQuery extends MasterdataQuery {
  /** 所属商品筛选（handler.go:193 queryInt64("product_id")） */
  product_id?: string
  /** 启停筛选（handler.go:203-212，true/false） */
  enabled?: boolean
}

export interface SkuItem {
  id: MasterdataId
  code: string
  product_id: MasterdataId
  /** 详情装配字段（列表 omitempty 不返回，service_sku.go:43-44） */
  product_code?: string
  product_name?: string
  /** spec_attrs 为 jsonb 对象（backend-m1-plan §5.4），键结构待后端契约细化，暂不在列表渲染 */
  spec_attrs?: Record<string, unknown>
  cost_price?: number
  sale_price?: number
  safety_stock?: number
  max_stock?: number
  min_replenish_qty?: number
  is_batch_managed: boolean
  is_expiry_managed: boolean
  is_serial_managed: boolean
  is_enabled: boolean
  /** 条码列表（列表/详情均批量装配，service_sku.go:92-134） */
  barcodes: SkuBarcodeItem[]
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（SKUCreateInput/SKUUpdateInput，service_sku.go:146-176；
 * is_enabled 布尔开关；barcodes 提供即全量替换、省略不修改） */
export interface SkuSavePayload {
  code: string
  /** int64 必填正整数（service_sku.go:265-267） */
  product_id: number
  spec_attrs?: Record<string, unknown>
  cost_price?: number
  sale_price?: number
  safety_stock?: number
  max_stock?: number
  min_replenish_qty?: number
  is_batch_managed?: boolean
  is_expiry_managed?: boolean
  is_serial_managed?: boolean
  is_enabled?: boolean
  barcodes?: SkuBarcodePayload[]
}

// ---------- 供应商（/api/suppliers，service_partner.go:23-35 SupplierView） ----------

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
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（SupplierCreateInput/SupplierUpdateInput，service_partner.go:87-105；
 * 后端无 status 字段——创建恒 ENABLED、启停走专用接口，编码创建后不可改） */
export interface SupplierSavePayload {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  remark?: string
}

// ---------- 客户（/api/customers，service_partner.go:54-66 CustomerView） ----------

export type CustomerQuery = MasterdataQuery

export interface CustomerItem {
  id: MasterdataId
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  shipping_address?: string
  status: EnabledStatus
  created_at?: string
  updated_at?: string
}

/** 创建/更新入参（CustomerCreateInput/CustomerUpdateInput，service_partner.go:108-126；
 * 后端无 status 字段——创建恒 ENABLED、启停走专用接口，编码创建后不可改） */
export interface CustomerSavePayload {
  code: string
  name: string
  contact?: string
  phone?: string
  email?: string
  address?: string
  shipping_address?: string
}

// ---------- API（列表 + CRUD /{id} + 启停 /status，internal/masterdata/masterdata.go:46-89） ----------

export const masterdataApi = {
  products: {
    list: (query: ProductQuery) =>
      http.get<PageResult<ProductItem>>('/api/products', { params: query }),
    create: (payload: ProductSavePayload) => http.post<unknown>('/api/products', payload),
    update: (id: MasterdataId, payload: ProductSavePayload) =>
      http.put<unknown>(`/api/products/${id}`, payload),
    /** PUT /api/products/:id/status（masterdata.go:50）；停用级联停用其启用中的 SKU */
    setStatus: (id: MasterdataId, payload: StatusPayload) =>
      http.put<{ status: EnabledStatus; cascade_disabled_skus?: number }>(
        `/api/products/${id}/status`,
        payload,
      ),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/products/${id}`),
  },
  skus: {
    list: (query: SkuQuery) => http.get<PageResult<SkuItem>>('/api/skus', { params: query }),
    create: (payload: SkuSavePayload) => http.post<unknown>('/api/skus', payload),
    update: (id: MasterdataId, payload: SkuSavePayload) =>
      http.put<unknown>(`/api/skus/${id}`, payload),
    /** PUT /api/skus/:id/status（masterdata.go:58）：{enabled} 布尔开关 */
    setStatus: (id: MasterdataId, payload: SkuStatusPayload) =>
      http.put<{ is_enabled: boolean }>(`/api/skus/${id}/status`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/skus/${id}`),
  },
  categories: {
    list: (query: CategoryQuery) =>
      http.get<PageResult<CategoryItem>>('/api/product-categories', { params: query }),
    create: (payload: CategorySavePayload) => http.post<unknown>('/api/product-categories', payload),
    update: (id: MasterdataId, payload: CategorySavePayload) =>
      http.put<unknown>(`/api/product-categories/${id}`, payload),
    /** PUT /api/product-categories/:id/status（masterdata.go:66）；无 DELETE 路由（停用即终点） */
    setStatus: (id: MasterdataId, payload: StatusPayload) =>
      http.put<{ status: EnabledStatus }>(`/api/product-categories/${id}/status`, payload),
  },
  units: {
    list: (query: UnitQuery) => http.get<PageResult<UnitItem>>('/api/units', { params: query }),
    create: (payload: UnitSavePayload) => http.post<unknown>('/api/units', payload),
    update: (id: MasterdataId, payload: UnitSavePayload) =>
      http.put<unknown>(`/api/units/${id}`, payload),
    /** PUT /api/units/:id/status（masterdata.go:73）；无 DELETE 路由（停用即终点） */
    setStatus: (id: MasterdataId, payload: StatusPayload) =>
      http.put<{ status: EnabledStatus }>(`/api/units/${id}/status`, payload),
  },
  suppliers: {
    list: (query: SupplierQuery) =>
      http.get<PageResult<SupplierItem>>('/api/suppliers', { params: query }),
    create: (payload: SupplierSavePayload) => http.post<unknown>('/api/suppliers', payload),
    update: (id: MasterdataId, payload: SupplierSavePayload) =>
      http.put<unknown>(`/api/suppliers/${id}`, payload),
    /** PUT /api/suppliers/:id/status（masterdata.go:80） */
    setStatus: (id: MasterdataId, payload: StatusPayload) =>
      http.put<{ status: EnabledStatus }>(`/api/suppliers/${id}/status`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/suppliers/${id}`),
  },
  customers: {
    list: (query: CustomerQuery) =>
      http.get<PageResult<CustomerItem>>('/api/customers', { params: query }),
    create: (payload: CustomerSavePayload) => http.post<unknown>('/api/customers', payload),
    update: (id: MasterdataId, payload: CustomerSavePayload) =>
      http.put<unknown>(`/api/customers/${id}`, payload),
    /** PUT /api/customers/:id/status（masterdata.go:88） */
    setStatus: (id: MasterdataId, payload: StatusPayload) =>
      http.put<{ status: EnabledStatus }>(`/api/customers/${id}/status`, payload),
    remove: (id: MasterdataId) => http.delete<unknown>(`/api/customers/${id}`),
  },
}
