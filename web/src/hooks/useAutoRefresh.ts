import { useCallback, useEffect, useMemo, useState } from 'react'

/**
 * 任务页自动刷新（计划 §2.11，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * 产出 TanStack Query `refetchInterval` 函数形态，供 useQuery 直接消费——
 *
 *   const auto = useAutoRefresh()
 *   useQuery({ ..., refetchInterval: auto.refetchInterval })
 *
 * 行为冻结口径：
 * - 档位 关/10/30/60 秒（默认关）；
 * - document.hidden（页签不可见）暂停轮询；
 * - 连续失败 ≥2 次退避停轮（以 query.state.fetchFailureCount 计数，成功后 TanStack 自动归零）；
 * - 可见性恢复后由 observer 重建 interval 自然续轮。
 */

export const AUTO_REFRESH_OPTIONS: Array<{ label: string; value: 0 | 10 | 30 | 60 }> = [
  { label: '关闭', value: 0 },
  { label: '10 秒', value: 10 },
  { label: '30 秒', value: 30 },
  { label: '60 秒', value: 60 },
]

export type AutoRefreshSeconds = (typeof AUTO_REFRESH_OPTIONS)[number]['value']

/** refetchInterval 回调入参形状（TanStack Query 的 Query.state 超集兼容子集） */
interface RefetchIntervalQueryState {
  fetchFailureCount: number
  dataUpdatedAt: number
}

export type RefetchIntervalFn = (query: { state: RefetchIntervalQueryState }) => number | false

/** 连续失败停轮阈值（§2.11：连续失败 ≥2 次退避停轮） */
const FAILURE_STOP_THRESHOLD = 2

export function useAutoRefresh() {
  const [seconds, setSeconds] = useState<AutoRefreshSeconds>(0)
  const [hidden, setHidden] = useState(() =>
    typeof document !== 'undefined' ? document.hidden : false,
  )

  // 页签不可见暂停：监听 visibilitychange（恢复可见后重新计时应答）
  useEffect(() => {
    const onVisibility = () => setHidden(document.hidden)
    document.addEventListener('visibilitychange', onVisibility)
    return () => document.removeEventListener('visibilitychange', onVisibility)
  }, [])

  const refetchInterval = useCallback<RefetchIntervalFn>(
    (query) => {
      if (seconds === 0) return false
      if (hidden) return false
      if (query.state.fetchFailureCount >= FAILURE_STOP_THRESHOLD) return false
      return seconds * 1000
    },
    [seconds, hidden],
  )

  return useMemo(
    () => ({
      /** 当前档位（秒；0=关）——SfAutoRefreshSelect value 直传 */
      seconds,
      /** 档位切换——SfAutoRefreshSelect onChange 直传 */
      setSeconds,
      /** useQuery 的 refetchInterval 选项直传 */
      refetchInterval,
      /** 页签不可见导致的暂停态（消费方可用于提示） */
      pausedByHidden: hidden,
    }),
    [seconds, refetchInterval, hidden],
  )
}
