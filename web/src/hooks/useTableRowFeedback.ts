import { useCallback, useEffect, useRef, useState } from 'react'
import type { Key } from 'react'
import { SF_MOTION_MS } from '@/styles/motion'

export type SfFeedbackTone = 'success' | 'error'

/**
 * 行反馈 / 删除行动效 hook（frontend.md §31；供阶段B 页面接入）：
 * - trigger：行淡色反馈底（success/error 各 480ms 自动回落），必须在 API onSuccess/onError
 *   之后调用（禁止先动画再请求）；重复调用重置定时器，feedbackCleanup 后自动清除。
 * - triggerRemove：删除行 fade→收缩→隐藏（rowRemoveCollapse 260ms 后由 SfTable 过滤 DOM）；
 *   hook 以 rowRemoveFallback 兜底自清——覆盖 refetch 失败场景（行恢复显示 = 删除未生效，如实回显），
 *   refetch 成功时 SfTable 依 dataSource 变化自愈提前清理。两者均只在视觉层，不阻塞业务操作。
 * - removingRowKeys 为数组（多行并行删除：260ms 动画窗口内连续快速删除逐 key 独立定时，互不中断），
 *   空数组=无；重复对同一 key 调用会重置该 key 的兜底定时器。
 */
export function useTableRowFeedback() {
  const [rowKey, setRowKey] = useState<Key | null>(null)
  const [tone, setTone] = useState<SfFeedbackTone>('success')
  const [removingRowKeys, setRemovingRowKeys] = useState<Key[]>([])
  const feedbackTimerRef = useRef<number>()
  const removeFallbackTimersRef = useRef(new Map<Key, number>())

  useEffect(
    () => () => {
      if (feedbackTimerRef.current !== undefined) window.clearTimeout(feedbackTimerRef.current)
      removeFallbackTimersRef.current.forEach((timer) => window.clearTimeout(timer))
      removeFallbackTimersRef.current.clear()
    },
    [],
  )

  /** 行反馈：仅在 API 成功/失败回调之后调用（key=行 rowKey 值，tone 缺省 success） */
  const trigger = useCallback((key: Key, next: SfFeedbackTone = 'success') => {
    setRowKey(key)
    setTone(next)
    if (feedbackTimerRef.current !== undefined) window.clearTimeout(feedbackTimerRef.current)
    feedbackTimerRef.current = window.setTimeout(() => {
      setRowKey(null)
      feedbackTimerRef.current = undefined
    }, SF_MOTION_MS.feedbackCleanup)
  }, [])

  /** 删除行：在删除 API 成功回调之后调用（多行并行安全，逐 key 独立兜底定时）；
   * 2.5s 兜底自清（正常路径由 SfTable 依 refetch 自愈提前清） */
  const triggerRemove = useCallback((key: Key) => {
    setRemovingRowKeys((prev) => (prev.includes(key) ? prev : [...prev, key]))
    const timers = removeFallbackTimersRef.current
    const existing = timers.get(key)
    if (existing !== undefined) window.clearTimeout(existing)
    timers.set(
      key,
      window.setTimeout(() => {
        timers.delete(key)
        setRemovingRowKeys((prev) => {
          const next = prev.filter((k) => k !== key)
          return next.length === prev.length ? prev : next
        })
      }, SF_MOTION_MS.rowRemoveFallback),
    )
  }, [])

  return { rowKey, tone, removingRowKeys, trigger, triggerRemove }
}
