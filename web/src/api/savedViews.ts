import { http } from './client'

// ---------- 保存视图（计划 §2.2，docs/plans/2026-10-06-efficiency-layer-phase1.md；
// 迁移 000020 user_saved_views，internal/userpref 包，/api/user/views 端点，认证即可无权限点） ----------
//
// ⚠️ 归属说明：本文件为 F2 波次自建（api/userpref.ts 归 F1 并行开发，禁碰）。
// F3 前由集成者将本文件与 F1 的 api/userpref.ts 收敛为单文件，勿双轨长期并存。

/**
 * 保存视图（GET/POST/PUT /api/user/views 出入参；ID 为字符串形态，api.md §2 冻结口径）。
 * filters_json=SfSearchForm cleanValues 产物（string 值对象）；columns_json=hidden 列键数组
 * （对齐 SfTable storageKey 'hidden-columns' localStorage 形态，SfTable.tsx:179）；
 * sort_json 一期恒 '{}'（B6 裁决：排序通道预留不采集，api.md/frontend.md 披露）。
 */
export interface SavedView {
  id: string
  page_key: string
  name: string
  filters_json: Record<string, string>
  /** 排序通道预留：一期恒 '{}' */
  sort_json: Record<string, unknown>
  /** hidden 列键数组（SfTable 受控列 hiddenColumns 同形态） */
  columns_json: string[]
  /** 1–100（后端 CHECK 约束对齐 API 分页上限，000020 chk_user_saved_views_page_size） */
  page_size: number
  is_default: boolean
  created_at: string
  updated_at: string
}

export interface SavedViewCreatePayload {
  page_key: string
  name: string
  filters_json: Record<string, string>
  columns_json: string[]
  page_size: number
  is_default?: boolean
}

/** PUT /api/user/views/{id}：改名/改内容/is_default 部分更新（非本人 404 防探测） */
export interface SavedViewUpdatePayload {
  name?: string
  filters_json?: Record<string, string>
  columns_json?: string[]
  page_size?: number
  is_default?: boolean
}

export const savedViewsApi = {
  /** GET /api/user/views?page_key=（page_key 服务端正则 ^[a-z0-9._-]{1,64}$） */
  list: (pageKey: string) => http.get<SavedView[]>('/api/user/views', { params: { page_key: pageKey } }),
  /** POST /api/user/views（重名 409——(user_id,page_key,name) 唯一索引） */
  create: (payload: SavedViewCreatePayload) => http.post<SavedView>('/api/user/views', payload),
  update: (id: string, payload: SavedViewUpdatePayload) => http.put<SavedView>(`/api/user/views/${id}`, payload),
  remove: (id: string) => http.delete<void>(`/api/user/views/${id}`),
}
