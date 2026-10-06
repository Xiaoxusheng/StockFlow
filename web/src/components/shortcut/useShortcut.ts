import { useEffect, useRef } from 'react'
import { useShortcutRegister, type ShortcutItem } from './ShortcutProvider'

// ---------- 快捷键注册 hook（计划 §2.9 / frontend.md §32.1） ----------
// 「新增快捷键必须入注册表，禁止组件内散写」的声明式入口：业务页/组件经本 hook
// 注册，禁止自建 window keydown 监听（scanner.md §3.1 PC 落地）。匹配、输入态抑制、
// preventDefault、帮助面板渲染全部由 ShortcutProvider 统一承担，本文件只管生命周期。

/**
 * 声明式注册一条快捷键：挂载即注册、卸载即注销。
 * handler/enabled 经 ref 取最新闭包（回调可引用页面最新 state，不触发重注册）；
 * combo/scope/description/chord 字段变化时重注册（注册表与帮助面板同步更新）。
 */
export function useShortcut(item: ShortcutItem): void {
  const register = useShortcutRegister()
  const { combo, chordGroup, chordKey, scope, description } = item
  const dynamicRef = useRef(item)
  dynamicRef.current = item
  useEffect(
    () =>
      register({
        combo,
        chordGroup,
        chordKey,
        scope,
        description,
        enabled: () => {
          const cur = dynamicRef.current
          return typeof cur.enabled === 'function' ? cur.enabled() : cur.enabled !== false
        },
        handler: (e, deps) => dynamicRef.current.handler(e, deps),
      }),
    // handler/enabled 走 ref；静态字段变化才重注册
    [register, combo, chordGroup, chordKey, scope, description],
  )
}

/** Alt+→/Alt+← 任务导航注册参数 */
export interface TaskNavShortcutOptions {
  /** 仅任务作业页 enabled（frontend.md §32.2：仅任务作业页生效；页面卸载自动注销） */
  enabled?: boolean
  /** Alt+→：下一条 */
  onNext?: () => void
  /** Alt+←：上一条 */
  onPrev?: () => void
}

/**
 * 任务作业页「下一条/上一条」语义注册（frontend.md §32.2 冻结键位 Alt+→ / Alt+←）。
 * 声明式注册点：任务作业页传入 onNext/onPrev 与 enabled；未提供回调的一侧不注册
 * （enabled=false，帮助面板同步隐藏，保证面板与实际行为一致）。输入态自动抑制、
 * 浏览器历史导航劫持仅限任务作业页（Provider preventDefault）。
 */
export function useTaskNavShortcuts({
  enabled = true,
  onNext,
  onPrev,
}: TaskNavShortcutOptions): void {
  useShortcut({
    combo: 'Alt+→',
    scope: 'page',
    description: '下一条',
    enabled: enabled && !!onNext,
    handler: () => onNext?.(),
  })
  useShortcut({
    combo: 'Alt+←',
    scope: 'page',
    description: '上一条',
    enabled: enabled && !!onPrev,
    handler: () => onPrev?.(),
  })
}
