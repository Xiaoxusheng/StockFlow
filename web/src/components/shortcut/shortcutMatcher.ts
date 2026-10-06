// ---------- 快捷键纯逻辑层（冲突矩阵 / combo 匹配 / 分发规划） ----------
// 自 ShortcutProvider.tsx 抽出（2026-10-06 测试加固，docs/testing.md §12.2）：
// 本文件零运行时依赖（antd 等仅 import type，运行时擦除），node:test 可直接加载测试；
// ShortcutProvider 只保留 React 壳（注册表 + window 监听 + handler 调用）。
// 规则冻结来源：docs/plans/2026-10-06-efficiency-layer-phase1.md §2.9 / scanner.md §3.1 PC 落地。

import type { MessageInstance } from 'antd/es/message/interface'

export type ShortcutScope = 'global' | 'page'

/** 快捷键分发时的环境依赖（由 Provider 注入，注册方不感知 UI 挂载点） */
export interface ShortcutDeps {
  /** 打开全局搜索面板（Header 搜索按钮同款入口） */
  openSearch: () => void
  /** 打开快捷键帮助面板 */
  openHelp: () => void
  /** antd message（App.useApp 上下文实例） */
  message: MessageInstance
}

/** 单条快捷键注册项（shortcuts.ts 集中注册 + 帮助面板按此渲染，零手写文档） */
export interface ShortcutItem {
  /** 展示用键位文案（如 'Ctrl/Cmd+K'、'G I'、'?'） */
  combo: string
  /** chord 分组标识（如 'g'）：声明后按序列窗口匹配，combo 仅作展示 */
  chordGroup?: string
  /** chord 第二键（小写单字符，如 'i'） */
  chordKey?: string
  /** global=输入态仍生效；page=输入态自动抑制 */
  scope: ShortcutScope
  description: string
  handler: (e: KeyboardEvent, deps: ShortcutDeps) => void
  /** 可选开关（如任务作业页经 useNextTask 注册的 Alt 系条目按页面条件启停） */
  enabled?: boolean | (() => boolean)
}

/** chord 序列窗口时长（ms，计划 §2.9 冻结 800ms） */
export const CHORD_WINDOW_MS = 800

// ---------- combo 解析与匹配 ----------

interface ParsedCombo {
  /** Ctrl（Windows/Linux）或 Cmd（macOS）二选一 */
  mod?: boolean
  ctrl?: boolean
  shift?: boolean
  alt?: boolean
  /** 主键（小写） */
  key: string
}

/** 键名别名归一：注册表用展示形态（→/←/↑/↓），KeyboardEvent.key 为 ArrowRight 等 */
const KEY_ALIASES: Readonly<Record<string, string>> = {
  arrowright: 'right',
  arrowleft: 'left',
  arrowup: 'up',
  arrowdown: 'down',
  '→': 'right',
  '←': 'left',
  '↑': 'up',
  '↓': 'down',
}

function normalizeKey(key: string): string {
  const lower = key.toLowerCase()
  return KEY_ALIASES[lower] ?? lower
}

function parseCombo(combo: string): ParsedCombo {
  const parts = combo.split('+').map((s) => s.trim().toLowerCase())
  const key = parts[parts.length - 1] ?? ''
  const mods = parts.slice(0, -1)
  return {
    mod: mods.some((m) => m === 'ctrl/cmd' || m === 'mod' || m === 'cmd/ctrl'),
    ctrl: mods.includes('ctrl'),
    shift: mods.includes('shift'),
    alt: mods.includes('alt'),
    key,
  }
}

function matchesCombo(item: ShortcutItem, e: ShortcutKeyEventLike): boolean {
  // chord 条目走序列窗口匹配，不在普通分支
  if (item.chordGroup && item.chordKey) return false
  const p = parseCombo(item.combo)
  if (p.mod && !(e.ctrlKey || e.metaKey)) return false
  if (p.ctrl && !e.ctrlKey) return false
  if (p.shift && !e.shiftKey) return false
  if (p.alt && !e.altKey) return false
  if (!p.mod && !p.ctrl && !p.shift && !p.alt && (e.ctrlKey || e.metaKey || e.altKey)) return false
  return normalizeKey(e.key) === normalizeKey(p.key)
}

/** 分发用键事件结构子集（真实 KeyboardEvent 天然满足；node 测试可构造纯对象） */
export interface ShortcutKeyEventLike {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  shiftKey: boolean
}

/** 可编辑性探测结构（真实 DOM Element 结构子集；node 测试可传纯对象，不依赖 DOM 全局） */
export interface EditableElementProbe {
  tagName: string
  isContentEditable?: boolean
  closest(selector: string): unknown | null
}

/** 可编辑元素判定核心（计划 §2.9 冻结规则）：INPUT/TEXTAREA/SELECT/isContentEditable 或扫码输入标记 */
export function isEditableElement(el: EditableElementProbe): boolean {
  if (el.isContentEditable) return true
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return !!el.closest('[data-sf-scan-input]')
}

/** 输入态判定（计划 §2.9 冻结规则）：INPUT/TEXTAREA/SELECT/isContentEditable 或扫码输入标记 */
export function isEditableTarget(target: EventTarget | null): boolean {
  const el = target instanceof Element ? target : null
  if (!el) return false
  return isEditableElement(el)
}

/** 条目是否启用（enabled 支持布尔或求值函数；分发与帮助面板共用同一判定） */
export function isShortcutEnabled(item: ShortcutItem): boolean {
  return typeof item.enabled === 'function' ? item.enabled() : item.enabled !== false
}

/** 挂起的 chord 序列窗口 */
export interface PendingChord {
  group: string
  /** 窗口截止时间戳（performance.now 口径） */
  until: number
}

/** dispatchShortcut 的判定结果（Provider 按 kind 落地 preventDefault / handler / 窗口状态） */
export type ShortcutDispatchOutcome =
  | { kind: 'fire'; item: ShortcutItem }
  | { kind: 'start-chord'; group: string; until: number }
  | { kind: 'none' }

export interface ShortcutDispatchInput {
  /** 注册表快照（Provider 的 itemsRef 当前值） */
  items: ShortcutItem[]
  /** 键事件（真实 KeyboardEvent 或测试纯对象） */
  event: ShortcutKeyEventLike
  /** 输入态（isEditableTarget 结果）：true 时 page 级全部禁用、chord 不启动/不续 */
  editable: boolean
  /** 当前挂起的 chord 窗口（无则 null）；调用方在本调用前把旧窗口置空 */
  pending: PendingChord | null
  /** 当前时间戳（performance.now 口径，注入保证纯函数可测） */
  now: number
}

/**
 * 一次 keydown 的完整分发规划（ShortcutProvider useEffect 的纯逻辑抽取，行为逐步对齐原实现）：
 * 1. chord 序列窗口：pending 期间仅匹配同组 chordKey，其余键清窗不拦截（过期/输入态同样清窗流转）；
 * 2. 进入 chord 窗口：裸 'g' 键（无修饰、非输入态）启动 800ms 序列窗口；
 * 3. 普通条目匹配：输入态仅 global 生效（页面级快捷键全部禁用）。
 * Esc 无注册条目即无匹配 → none，不双抢 antd Modal/Drawer。
 */
export function dispatchShortcut(input: ShortcutDispatchInput): ShortcutDispatchOutcome {
  const { items, event, editable, pending, now } = input

  // 1. chord 序列窗口：pending 期间仅匹配同组 chordKey，其余键清窗不拦截
  if (pending) {
    if (now <= pending.until && !editable) {
      const hit = items.find(
        (it) =>
          it.chordGroup === pending.group &&
          it.chordKey === event.key.toLowerCase() &&
          isShortcutEnabled(it),
      )
      if (hit) return { kind: 'fire', item: hit }
    }
    // 未命中/过期/输入态：清窗后事件照常流转（不吞键）
  }

  // 2. 进入 chord 窗口：裸 'g' 键（无修饰、非输入态）启动 800ms 序列窗口
  if (!editable && !event.ctrlKey && !event.metaKey && !event.altKey && event.key.toLowerCase() === 'g') {
    if (items.some((it) => it.chordGroup && isShortcutEnabled(it))) {
      return { kind: 'start-chord', group: 'g', until: now + CHORD_WINDOW_MS }
    }
  }

  // 3. 普通条目匹配：输入态仅 global 生效（页面级快捷键全部禁用）
  for (const item of items) {
    if (!isShortcutEnabled(item)) continue
    if (editable && item.scope !== 'global') continue
    if (!matchesCombo(item, event)) continue
    return { kind: 'fire', item }
  }
  return { kind: 'none' }
}
