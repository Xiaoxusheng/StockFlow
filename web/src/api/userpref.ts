import { http } from './client'

// ---------- 用户偏好（计划 §2.3 / api.md §9 效率层节 / apidocs/swagger.json /api/user/preferences） ----------
// 统一信封 {code,message,data,request_id}；业务 ID 字符串；时间 YYYY-MM-DD HH:mm:ss。
//
// 归属收敛（2026-10-06，本文件原注释「views/preferences 共存」裁定废止）：保存视图 API
// 单一来源为 **api/savedViews.ts**（F2 交付，useSavedViews/SfViewBar 消费）——本文件原并列的
// views CRUD（listViews/createView/updateView/deleteView）零消费方，按 savedViews.ts 头注
// 「勿双轨长期并存」移除，避免同一端点双类型双轨。「最近操作」单一来源为 api/task.ts
// taskApi.recentOperations（GET /api/workbench/recent-operations）；「最近访问」不设独立
// activities 端点（计划 §2.5 裁决：属导航态，存偏好 recent_visits，结构见 RecentVisit）。

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

/** 后端 GET 实际出参行（Preference，internal/userpref/models.go:74-76：value 为原始 jsonb） */
export interface PreferenceRow {
  key: string
  value: unknown
  created_at?: string
  updated_at?: string
}

export const userprefApi = {
  /**
   * 读取偏好：keys 缺省返回全部白名单键。
   * GET /api/user/preferences?keys=a,b
   *
   * 后端响应为 Preference 行数组（internal/userpref/routes.go:250-258 `response.OK(c, rows)`，
   * rows: []Preference），此处归约为 {key: value} 映射——消费方 usePreferences/
   * usePreferenceValue 按键取值（hooks/usePreferences.ts:70 `data[key]`），
   * 行数组直出会因数组按键索引恒 undefined 使偏好读取全失效。
   */
  getPreferences(keys?: readonly PreferenceKey[]): Promise<PreferenceMap> {
    return http
      .get<PreferenceRow[]>('/api/user/preferences', {
        params: keys && keys.length > 0 ? { keys: keys.join(',') } : undefined,
      })
      .then((rows) => {
        const map: PreferenceMap = {}
        for (const row of rows ?? []) {
          if (row && typeof row.key === 'string') map[row.key as PreferenceKey] = row.value
        }
        return map
      })
  },

  /**
   * 写入偏好：请求体为**原始 JSON 值**（不是 {value:...} 包装，api.md §9 冻结契约）。
   * 白名单外 key 后端 400。
   */
  setPreference(key: PreferenceKey, value: unknown): Promise<void> {
    return http.put<void>(`/api/user/preferences/${key}`, value)
  },
}
