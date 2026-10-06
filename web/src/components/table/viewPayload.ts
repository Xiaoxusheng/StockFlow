// ---------- 保存视图序列化/恢复纯逻辑（自 SfViewBar.tsx 抽出，2026-10-06 测试加固） ----------
// 契约来源：api.md §2 / api/savedViews.ts（filters_json = SfSearchForm cleanValues 产物，
// string 值对象；sort_json 一期不采集）。本文件零运行时依赖（SavedView 为 import type，
// 运行时擦除），node:test 可直接加载测试。

import type { SavedView } from '@/api/savedViews'

/** 空值剔除（undefined/null/'' 视为未筛选）：保存采集与序列化共用的唯一剔除规则 */
export function cleanFilters(filters: Record<string, unknown>): Record<string, unknown> {
  const clean: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === null || value === '') continue
    clean[key] = value
  }
  return clean
}

/** filters_json 序列化（对账键；空值剔除后 String 归一并 stringify 保证同形可比） */
export function stableFiltersJson(filters: Record<string, unknown>): string {
  const clean: Record<string, string> = {}
  for (const [key, value] of Object.entries(cleanFilters(filters))) {
    clean[key] = String(value)
  }
  return JSON.stringify(clean)
}

/** 视图 filters_json → applyFilters 入参（SavedView.filters_json 已是 string 值对象；返回副本不改视图） */
export function toApplyValues(view: Pick<SavedView, 'filters_json'>): Record<string, unknown> {
  return { ...(view.filters_json ?? {}) }
}
