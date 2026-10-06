import { useEffect, useRef } from 'react'
import { usePreferences, useSetPreference } from '@/hooks/usePreferences'

// ---------- 仓库偏好：默认仓库/最近仓库 selector hooks + 默认值注入辅助（计划 §2.3） ----------
//
// 偏好 key 属服务端白名单（api/userpref.ts PREFERENCE_KEYS）：default_warehouse_id /
// last_warehouse_id。本文件只提供 selector 与注入辅助，**页面接线由页面波次完成**：
//   - urlSync 页：inject 的 apply 传 list.applyFilters、filters 传 list.formValues——URL 为
//     筛选唯一事实源，注入即经 usePagedList 公开 API 展开写入 URL（刷新/分享可还原），
//     对齐 SfViewBar B5 裁决「禁止绕过 hook 直改 URL 或页面 state」；
//   - 旧形态页（无 urlSync）：apply 传页面写入 params 的回调（需自行保证回到第 1 页，
//     与 SfSearchForm onSearch 同语义）、filters 传当前 params。
//
// 边界：表格密度/分页大小**不走服务端偏好**——SfTable storageKey（localStorage）已内建
// 持久化（SfTable.tsx:190-194 density/hidden-columns），计划 §1.2 明确「不为单点偏好新建
// 服务端体系」，故本文件不提供 density/pageSize 偏好 selector。

/** 偏好值规整：jsonb 无 schema，仅接受非空 string/number（防御异常写入），统一 string 形态 */
function normalizeWarehouseId(value: unknown): string | null {
  if (typeof value === 'string' && value.trim() !== '') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return null
}

/** 默认仓库 selector（SfPreferenceDrawer 写入的 default_warehouse_id；未设置返回 null） */
export function useDefaultWarehouseId(): string | null {
  const { data } = usePreferences()
  return normalizeWarehouseId(data.default_warehouse_id)
}

/** 最近仓库 selector（useTrackLastWarehouse 写入的 last_warehouse_id；未产生过返回 null） */
export function useLastWarehouseId(): string | null {
  const { data } = usePreferences()
  return normalizeWarehouseId(data.last_warehouse_id)
}

export interface WarehouseInjectionOptions {
  /**
   * 当前生效筛选：urlSync 页传 list.formValues（URL 原始字符串形态）、旧形态页传 params。
   * 仅用于判定仓库字段是否已被占用（URL/显式筛选已带仓库 → 不注入，尊重用户现状）。
   */
  filters: Record<string, unknown>
  /**
   * 注入出口（合并语义）：urlSync 页传 list.applyFilters（展开写入 URL）、
   * 旧形态页传写入 params 的回调（应含回到第 1 页语义）。
   */
  apply: (values: Record<string, unknown>) => void
  /** 仓库筛选字段名（缺省 'warehouse_id'，与既有列表页筛选键同名） */
  field?: string
  /**
   * 合法仓库 id 集合（页面仓库下拉候选，['options','warehouses'] 同源）：默认仓库不在集合内
   * （已删除/超出数据权限）→ 放弃注入，防打出空结果页。不传则不做该校验。
   */
  validIds?: ReadonlyArray<string | number>
  /**
   * 就绪开关（缺省 true）：建议传「偏好与仓库候选均已就绪」（如 !prefs.isLoading && warehouses.isSuccess）。
   * 偏好读有 localStorage 先行渲染镜像（usePreferences initialData），页面不传 ready 时
   * 首屏即可用本地镜像值注入；服务端刷新出不同默认后**不重注入**（一次性语义）。
   */
  ready?: boolean
}

/**
 * 默认仓库一次性注入（页面波次接线用）：挂载后若仓库筛选为空且偏好已设默认仓库，
 * 经 apply 展开注入一次。页面生命周期内只决策一次——仓库字段已被显式筛选占用、默认仓库
 * 不在 validIds、或本无默认，任一发生即收束，绝不与用户后续改动抢写。
 */
export function useInjectDefaultWarehouse({
  filters,
  apply,
  field = 'warehouse_id',
  validIds,
  ready = true,
}: WarehouseInjectionOptions): void {
  const { data } = usePreferences()
  const doneRef = useRef(false)

  useEffect(() => {
    if (doneRef.current || !ready) return
    const current = filters[field]
    if (current !== undefined && current !== null && String(current).trim() !== '') {
      doneRef.current = true // 页面/URL 已带仓库：尊重现状，永不注入
      return
    }
    const defaultId = normalizeWarehouseId(data.default_warehouse_id)
    if (!defaultId) return // 偏好未就绪/未设默认：等待（data 变化后 effect 重试）
    if (validIds && !validIds.some((id) => String(id) === defaultId)) {
      doneRef.current = true // 默认仓库无效（已删除/无数据权限）：放弃，防空结果页
      return
    }
    doneRef.current = true
    apply({ ...filters, [field]: defaultId })
  }, [ready, filters, field, apply, validIds, data])
}

export interface LastWarehouseTrackOptions {
  /** 当前生效筛选（跟踪其仓库字段变化；urlSync 页传 list.params） */
  filters: Record<string, unknown>
  /** 仓库筛选字段名（缺省 'warehouse_id'） */
  field?: string
  /** 缺省 true；偏好未就绪等场景可传 false 暂停 */
  enabled?: boolean
}

/**
 * 最近仓库跟踪（页面波次接线用）：生效筛选中的仓库字段变化即写 last_warehouse_id
 * （写路径复用 useSetPreference——本地缓存/localStorage 即时生效 + 800ms 防抖合并 PUT）。
 * 清除仓库筛选不清最近仓库（「最近使用」为历史语义）；同值不重写防抖动。
 */
export function useTrackLastWarehouse({ filters, field = 'warehouse_id', enabled = true }: LastWarehouseTrackOptions): void {
  const setPreference = useSetPreference()
  const lastWrittenRef = useRef<string | null>(null)

  useEffect(() => {
    if (!enabled) return
    const id = normalizeWarehouseId(filters[field])
    if (!id || id === lastWrittenRef.current) return
    lastWrittenRef.current = id
    setPreference('last_warehouse_id', id)
  }, [enabled, filters, field, setPreference])
}
