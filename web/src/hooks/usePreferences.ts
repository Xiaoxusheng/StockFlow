import { useCallback, useEffect, useRef } from 'react'
import { useLocation } from 'react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { userprefApi, PREFERENCE_KEYS, type PreferenceKey, type PreferenceMap, type RecentVisit } from '@/api/userpref'
import { resolveMenuTrail } from '@/config/menu'

// ---------- 用户偏好 hook（计划 §2.3） ----------
// 读：TanStack Query 缓存（queryKey ['user','preferences']）；localStorage 先行渲染防闪变
// （initialData 来自本地镜像，随后被服务端结果覆盖）。写：本地缓存与 localStorage 即时更新
// + 防抖 PUT（合并窗口内只发最后一次），失败静默保留本地值并在控制台留痕。
// 仅写白名单 7 键（PREFERENCE_KEYS，白名单外后端 400——前端也不产生）。

/** localStorage 镜像统一前缀（键名 = sf-pref-{key}） */
const LS_PREFIX = 'sf-pref-'

/** 防抖写合并窗口（ms）：快速连续修改只发最后一次 PUT */
const WRITE_DEBOUNCE_MS = 800

/** 最近访问防抖（ms，计划 §2.3 冻结 3s）：路由快速切换只记停留过的页面 */
const RECENT_VISIT_DELAY_MS = 3000

/** 最近访问条数上限（服务端同样裁 20，前端对齐防抖动） */
const RECENT_VISITS_LIMIT = 20

function readLocalPref(key: PreferenceKey): unknown | undefined {
  try {
    const raw = window.localStorage.getItem(LS_PREFIX + key)
    return raw === null ? undefined : (JSON.parse(raw) as unknown)
  } catch {
    return undefined
  }
}

function writeLocalPref(key: PreferenceKey, value: unknown): void {
  try {
    window.localStorage.setItem(LS_PREFIX + key, JSON.stringify(value))
  } catch {
    // 隐私模式等 localStorage 不可用场景：降级为纯内存态，不阻塞
  }
}

function buildInitialData(): PreferenceMap {
  const data: PreferenceMap = {}
  for (const key of PREFERENCE_KEYS) {
    const value = readLocalPref(key)
    if (value !== undefined) data[key] = value
  }
  return data
}

/** 全量偏好读取（组件树内多处消费共用一个缓存条目） */
export function usePreferences(): {
  data: PreferenceMap
  isLoading: boolean
} {
  const query = useQuery({
    queryKey: ['user', 'preferences'],
    queryFn: () => userprefApi.getPreferences(),
    initialData: buildInitialData,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
  return { data: query.data ?? {}, isLoading: query.isLoading }
}

/** 结构化取值：类型断言由调用点负责（偏好值 jsonb 无统一 schema） */
export function usePreferenceValue<T>(key: PreferenceKey, fallback: T): T {
  const { data } = usePreferences()
  const value = data[key]
  return (value === undefined || value === null ? fallback : value) as T
}

/**
 * 偏好写入器：本地缓存 + localStorage 即时更新（防闪变），PUT 防抖合并。
 * 返回的 setter 引用稳定。
 */
export function useSetPreference(): (key: PreferenceKey, value: unknown) => void {
  const queryClient = useQueryClient()
  const timersRef = useRef(new Map<PreferenceKey, ReturnType<typeof setTimeout>>())

  useEffect(() => {
    const timers = timersRef.current
    return () => {
      timers.forEach((t) => clearTimeout(t))
      timers.clear()
    }
  }, [])

  return useCallback(
    (key: PreferenceKey, value: unknown) => {
      // 1. 本地即时生效：query 缓存 + localStorage（渲染不等待网络）
      writeLocalPref(key, value)
      queryClient.setQueryData<PreferenceMap>(['user', 'preferences'], (prev) => ({
        ...buildInitialData(),
        ...prev,
        [key]: value,
      }))
      // 2. 防抖 PUT：窗口内同 key 重复写只发最后一次
      const timers = timersRef.current
      const prev = timers.get(key)
      if (prev) clearTimeout(prev)
      timers.set(
        key,
        setTimeout(() => {
          timers.delete(key)
          userprefApi.setPreference(key, value).catch((err: unknown) => {
            // 写失败不回滚本地值（离线/后端重启场景下保持可用），控制台留痕供排查
            console.warn(`[usePreferences] 写入偏好 ${key} 失败`, err)
          })
        }, WRITE_DEBOUNCE_MS),
      )
    },
    [queryClient],
  )
}

/**
 * 最近访问写入器（计划 §2.3/§2.5）：路由变化防抖 3s 写 recent_visits。
 * 标题取 resolveMenuTrail 既有函数；菜单外路径（无 trail 命中）跳过——Dashboard、
 * 详情衍生页等无菜单语义的路径不强求记录。同 path 再访问置顶去重，上限 20 条。
 */
export function useRecentVisits(): void {
  const location = useLocation()
  const setPreference = useSetPreference()
  const { data } = usePreferences()
  // 定时器回调读 ref 快照（重渲染不重置 3s 防抖计时）
  const listRef = useRef<RecentVisit[]>([])
  useEffect(() => {
    const raw = data.recent_visits
    listRef.current = Array.isArray(raw) ? (raw as RecentVisit[]) : []
  }, [data])
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    const path = location.pathname
    timerRef.current = setTimeout(() => {
      const trail = resolveMenuTrail(path)
      const title = trail.page?.label ?? trail.group?.label
      if (!title) return // 菜单外路径跳过（Dashboard 等）
      const rest = listRef.current.filter((v) => v.path !== path)
      const next = [{ path, title, visited_at: dayjs().format('YYYY-MM-DD HH:mm:ss') }, ...rest]
      setPreference('recent_visits', next.slice(0, RECENT_VISITS_LIMIT))
    }, RECENT_VISIT_DELAY_MS)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [location.pathname, setPreference])
}

/** 读最近访问列表（RecentVisitsDropdown 消费；未写入过返回空数组） */
export function useRecentVisitsList(): RecentVisit[] {
  const { data } = usePreferences()
  const raw = data.recent_visits
  return Array.isArray(raw) ? (raw as RecentVisit[]) : []
}

/**
 * 清除最近数据（SfPreferenceDrawer『清除最近访问/最近筛选』按钮）：
 * recent_visits 与 recent_filters 一并清空（本地即时 + 防抖 PUT）。
 */
export function useClearRecentData(): () => void {
  const setPreference = useSetPreference()
  return useCallback(() => {
    setPreference('recent_visits', [])
    setPreference('recent_filters', [])
  }, [setPreference])
}
