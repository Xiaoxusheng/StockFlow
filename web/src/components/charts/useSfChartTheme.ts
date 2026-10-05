/**
 * 图表主题 hook（themeContract 唯一落点）
 *
 * 唯一信号：useThemeStore((s) => s.mode) —— mode 变化即 useMemo 重算主题
 * 并重渲图表，全程无刷新（任务书 §50）。禁止读 localStorage('sf.theme')、
 * matchMedia 判主题或直接写 dataset.theme（仅 stores/theme.ts 允许）。
 *
 * matchMedia 仅用于 prefers-reduced-motion（任务书 §59 动画降级），
 * 与主题判断无关，不违反 themeContract。
 */
import { useMemo, useSyncExternalStore } from 'react'
import { useThemeStore } from '@/stores/theme'
import { buildChartTheme, type SfChartTheme } from './sfChartTheme'

const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)'

const reducedMotionQuery =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia(REDUCED_MOTION_QUERY)
    : undefined

function subscribeReducedMotion(onChange: () => void): () => void {
  reducedMotionQuery?.addEventListener('change', onChange)
  return () => reducedMotionQuery?.removeEventListener('change', onChange)
}

function getReducedMotion(): boolean {
  return reducedMotionQuery?.matches ?? false
}

export interface SfChartThemeResult {
  mode: 'light' | 'dark'
  /** plots 主题对象（经 SfChart 内核统一注入，业务页零配置） */
  theme: SfChartTheme
  /** 系列色板（按 §31 顺序，业务组件按语义键取用） */
  palette: string[]
  /** 系统减少动效偏好：true 时图表动画关闭（§59） */
  reducedMotion: boolean
}

export function useSfChartTheme(): SfChartThemeResult {
  const mode = useThemeStore((s) => s.mode)
  const theme = useMemo(() => buildChartTheme(mode), [mode])
  const reducedMotion = useSyncExternalStore(subscribeReducedMotion, getReducedMotion, getReducedMotion)
  return { mode, theme, palette: theme.category10, reducedMotion }
}
