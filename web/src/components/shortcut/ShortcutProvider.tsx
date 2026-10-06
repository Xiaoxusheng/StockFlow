import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { App } from 'antd'
import type { MessageInstance } from 'antd/es/message/interface'
import { GlobalSearchModal } from '@/components/search/GlobalSearchModal'
import { ShortcutHelpDrawer } from './ShortcutHelpDrawer'

// ---------- 全站唯一 keydown 监听点（计划 §2.9 / scanner.md §3.1 PC 落地） ----------
// Context 注册表 + 统一分发；页面/组件禁止自建 window keydown 监听。
// 输入态自动抑制：target 为 INPUT/TEXTAREA/SELECT/isContentEditable 或带
// [data-sf-scan-input]（scanner 组件标记，ScannerManager 消费同一约定）时页面级快捷键禁用，
// global 级（Ctrl/Cmd+K、Ctrl/Cmd+R）仍生效。
// G 系 chord：800ms 序列窗口（G+I/P/S/T）。Esc 不在此处理：antd Modal/Drawer 的 Esc
// 归 antd 自身（GlobalSearchModal/ShortcutHelpDrawer 均为 antd 容器），不双抢。

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
const CHORD_WINDOW_MS = 800

/** 注册函数：返回注销函数（useEffect cleanup 消费） */
export type RegisterShortcut = (item: ShortcutItem) => () => void

interface ShortcutContextValue {
  register: RegisterShortcut
  /** 打开全局搜索（Header 搜索按钮 / 输入框形态入口复用） */
  openSearch: () => void
}

const ShortcutContext = createContext<ShortcutContextValue | null>(null)

/** 取注册 API（自定义快捷键注册点；内置注册表见 shortcuts.ts） */
export function useShortcutRegister(): RegisterShortcut {
  const ctx = useContext(ShortcutContext)
  if (!ctx) throw new Error('useShortcutRegister 必须在 <ShortcutProvider> 内使用')
  return ctx.register
}

/** 取全局搜索入口（PcLayout Header 搜索按钮调用，与 Ctrl/Cmd+K 同源） */
export function useSearchOpener(): () => void {
  const ctx = useContext(ShortcutContext)
  if (!ctx) throw new Error('useSearchOpener 必须在 <ShortcutProvider> 内使用')
  return ctx.openSearch
}

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

function matchesCombo(item: ShortcutItem, e: KeyboardEvent): boolean {
  // chord 条目走序列窗口匹配，不在普通分支
  if (item.chordGroup && item.chordKey) return false
  const p = parseCombo(item.combo)
  if (p.mod && !(e.ctrlKey || e.metaKey)) return false
  if (p.ctrl && !e.ctrlKey) return false
  if (p.shift && !e.shiftKey) return false
  if (p.alt && !e.altKey) return false
  if (!p.mod && !p.ctrl && !p.shift && !p.alt && (e.ctrlKey || e.metaKey || e.altKey)) return false
  return e.key.toLowerCase() === p.key
}

/** 输入态判定（计划 §2.9 冻结规则）：INPUT/TEXTAREA/SELECT/isContentEditable 或扫码输入标记 */
function isEditableTarget(target: EventTarget | null): boolean {
  const el = target instanceof Element ? target : null
  if (!el) return false
  if (el instanceof HTMLElement && el.isContentEditable) return true
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return !!el.closest('[data-sf-scan-input]')
}

function isItemEnabled(item: ShortcutItem): boolean {
  return typeof item.enabled === 'function' ? item.enabled() : item.enabled !== false
}

interface PendingChord {
  group: string
  /** 窗口截止时间戳（performance.now 口径） */
  until: number
}

/**
 * 快捷键中枢：包裹 PcLayout 内容树（仅 PC；Pad 布局不挂——Pad 卡片可达性层
 * 已有自身 keydown，一期不动，计划 §2.9）。内部承载 GlobalSearchModal 与帮助面板。
 */
export function ShortcutProvider({ children }: { children: ReactNode }) {
  const { message } = App.useApp()
  const [searchOpen, setSearchOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  /** 帮助面板渲染源（注册表快照） */
  const [items, setItems] = useState<ShortcutItem[]>([])
  /** 分发路径读 ref（避免每帧重建监听器） */
  const itemsRef = useRef<ShortcutItem[]>([])
  const pendingChordRef = useRef<PendingChord | null>(null)
  /** 自增注册 ID（Map 键） */
  const seqRef = useRef(0)
  /** id → item：注册表唯一存储 */
  const registryRef = useRef(new Map<number, ShortcutItem>())

  const deps = useMemo<ShortcutDeps>(
    () => ({
      openSearch: () => setSearchOpen(true),
      openHelp: () => setHelpOpen(true),
      message,
    }),
    [message],
  )
  /** 监听闭包统一走 depsRef：deps 变化不重建监听器 */
  const depsRef = useRef(deps)
  useEffect(() => {
    depsRef.current = deps
  }, [deps])

  const syncItems = useCallback(() => {
    const list = [...registryRef.current.values()]
    itemsRef.current = list
    setItems(list)
  }, [])

  const register = useCallback<RegisterShortcut>(
    (item) => {
      const id = ++seqRef.current
      registryRef.current.set(id, item)
      syncItems()
      return () => {
        registryRef.current.delete(id)
        syncItems()
      }
    },
    [syncItems],
  )

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.repeat) return
      const currentDeps = depsRef.current
      const editable = isEditableTarget(e.target)
      const list = itemsRef.current

      // 1. chord 序列窗口：pending 期间仅匹配同组 chordKey，其余键清窗不拦截
      const pending = pendingChordRef.current
      if (pending) {
        pendingChordRef.current = null
        if (performance.now() <= pending.until && !editable) {
          const hit = list.find(
            (it) =>
              it.chordGroup === pending.group &&
              it.chordKey === e.key.toLowerCase() &&
              isItemEnabled(it),
          )
          if (hit) {
            e.preventDefault()
            hit.handler(e, currentDeps)
            return
          }
        }
        // 未命中/过期/输入态：清窗后事件照常流转（不吞键）
      }

      // 2. 进入 chord 窗口：裸 'g' 键（无修饰、非输入态）启动 800ms 序列窗口
      if (!editable && !e.ctrlKey && !e.metaKey && !e.altKey && e.key.toLowerCase() === 'g') {
        if (list.some((it) => it.chordGroup && isItemEnabled(it))) {
          pendingChordRef.current = { group: 'g', until: performance.now() + CHORD_WINDOW_MS }
          e.preventDefault()
          return
        }
      }

      // 3. 普通条目匹配：输入态仅 global 生效（页面级快捷键全部禁用）
      for (const item of list) {
        if (!isItemEnabled(item)) continue
        if (editable && item.scope !== 'global') continue
        if (!matchesCombo(item, e)) continue
        e.preventDefault()
        item.handler(e, currentDeps)
        return
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  const contextValue = useMemo(() => ({ register, openSearch: deps.openSearch }), [register, deps])

  return (
    <ShortcutContext.Provider value={contextValue}>
      {children}
      <GlobalSearchModal open={searchOpen} onClose={() => setSearchOpen(false)} />
      <ShortcutHelpDrawer open={helpOpen} items={items} onClose={() => setHelpOpen(false)} />
    </ShortcutContext.Provider>
  )
}
