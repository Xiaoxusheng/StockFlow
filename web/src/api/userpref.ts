import { http } from './client'

// ---------- 用户偏好与保存视图（计划 §2.2/§2.3 / api.md §9 效率层节 / apidocs/swagger.json /api/user/*） ----------
// 统一信封 {code,message,data,request_id}；业务 ID 字符串；时间 YYYY-MM-DD HH:mm:ss。

/**
 * 偏好 key 服务端白名单（internal/userpref 冻结清单）：白名单外后端 400 invalidParam。
 * 前端只写这 7 个 key，读取允许全量/按 keys。
 */
export const PREFERENCE_KEYS = [
  'default_warehouse_id',
  'last_warehouse_id',
  'last_business_type',
  'last_printer_id',
  'last_print_template_id',
  'recent_visits',
  'recent_filters',
] as const

export type PreferenceKey = (typeof PREFERENCE_KEYS)[number]

/** key → 值类型映射（value 为 jsonb；recent_visits 有结构约束） */
export interface RecentVisit {
  /** 路由 path（如 /inventory/stock/SKU001） */
  path: string
  /** 展示标题（取菜单名或详情页语义） */
  title: string
  /** 访问时间 YYYY-MM-DD HH:mm:ss */
  visited_at: string
}

/** 偏好读出参：{key: value} 映射（value 为原始 jsonb，白名单内键均可缺省） */
export type PreferenceMap = Partial<Record<PreferenceKey, unknown>>

/**
 * 保存视图（计划 §2.2，GET/POST /api/user/views、PUT/DELETE /api/user/views/{id}）。
 * filters_json/columns_json 为 jsonb 原样存取；非本人 404、重名 409（resolveErrorMessage 展示）。
 */
export interface SavedView {
  id: string
  /** 绑定页面键（如 'inventory.stock'，与列表页 storageKey 同源口径） */
  page_key: string
  name: string
  filters_json: unknown
  columns_json: unknown
  page_size: number
  is_default: boolean
  created_at?: string
  updated_at?: string
}

export interface SavedViewPayload {
  page_key: string
  name: string
  filters_json: unknown
  columns_json: unknown
  page_size: number
  /** 设为该页默认视图（同页互斥由后端保证） */
  is_default?: boolean
}

export const userprefApi = {
  /**
   * 读取偏好：keys 缺省返回全部白名单键。
   * GET /api/user/preferences?keys=a,b
   */
  getPreferences(keys?: readonly PreferenceKey[]): Promise<PreferenceMap> {
    return http.get<PreferenceMap>('/api/user/preferences', {
      params: keys && keys.length > 0 ? { keys: keys.join(',') } : undefined,
    })
  },

  /**
   * 写入偏好：请求体为**原始 JSON 值**（不是 {value:...} 包装，api.md §9 冻结契约）。
   * 白名单外 key 后端 400。
   */
  setPreference(key: PreferenceKey, value: unknown): Promise<void> {
    return http.put<void>(`/api/user/preferences/${key}`, value)
  },

  /** 保存视图列表（page_key 必填） */
  listViews(pageKey: string): Promise<SavedView[]> {
    return http.get<SavedView[]>('/api/user/views', { params: { page_key: pageKey } })
  },

  /** 新建保存视图（重名 409） */
  createView(payload: SavedViewPayload): Promise<SavedView> {
    return http.post<SavedView>('/api/user/views', payload)
  },

  /** 更新保存视图（非本人 404、重名 409） */
  updateView(id: string, payload: SavedViewPayload): Promise<SavedView> {
    return http.put<SavedView>(`/api/user/views/${id}`, payload)
  },

  /** 删除保存视图（非本人 404） */
  deleteView(id: string): Promise<void> {
    return http.delete<void>(`/api/user/views/${id}`)
  },
}
