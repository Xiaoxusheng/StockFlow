import { http } from './client'

// ---------- 全局业务搜索（计划 §2.1 / api.md §9 效率层节 / apidocs/swagger.json /api/search） ----------
// 端点只挂认证，组内按用户权限集逐 type 过滤（无对应 list 码的 type 不查询不出组）；
// q < 2 后端直接返回空 groups 不做 SQL；q 上限 64；limit 缺省 5 上限 20。
// 前端契约：q ≥ 2 字符才发请求 + 300ms 防抖（GlobalSearchModal 承担）。

/**
 * 跳转标识（api.md §9 效率层节 B2 补录）：后端不编码前端路由，kind+id 为稳定实体标识，
 * 路由映射由 config/searchTargets.ts 消费（navigation 优先，type/单号前缀为回退）。
 * kind 权威值：普通 type 与 type 本值同值（sku/product/barcode/batch/serial/bin/
 * warehouse/customer/supplier）；doc 组为具体单据 kind（purchase_order/inbound_order/
 * sales_order/outbound_order/transfer_order/count_order/exception）；logistics→shipment。
 */
export interface SearchNavigation {
  kind: string
  /** 业务 ID（字符串形态，api.md §2） */
  id: string
}

/** 搜索命中项（后端 SearchItem，database.ID 序列化为字符串） */
export interface SearchHitItem {
  id: string
  /** 类型域：sku/product/barcode/batch/serial/bin/warehouse/customer/supplier/doc/logistics */
  type: string
  title: string
  /** 业务编码（单据为单号、SKU 为 code 等） */
  code: string
  /** 业务状态原始值（展示经 SfStatusTag，未注册状态兜底中性灰） */
  status?: string
  /** 补充摘要（如商品名/仓库名） */
  summary?: string
  /** YYYY-MM-DD HH:mm:ss */
  updated_at?: string
  /** 跳转标识（additive 契约字段；后端恒返回，前端 navigation 优先消费） */
  navigation?: SearchNavigation
}

/** 按类型分组的结果组（后端 SearchGroup） */
export interface SearchHitGroup {
  type: string
  /** 分组标题（后端中文文案，如「商品」「采购单」） */
  title: string
  count: number
  items: SearchHitItem[]
}

export interface SearchResult {
  groups: SearchHitGroup[]
}

export interface SearchParams {
  /** 关键词（2-64 字符，前端保证 ≥2 才发） */
  q: string
  /** 类型过滤（逗号分隔，缺省全部） */
  types?: string
  /** 仓库收窄过滤器（与数据权限求交，越界按无结果处理） */
  warehouse_id?: number | string
  /** 每组条数（缺省 5，上限 20） */
  limit?: number
}

export const searchApi = {
  search(params: SearchParams): Promise<SearchResult> {
    return http.get<SearchResult>('/api/search', { params })
  },
}
