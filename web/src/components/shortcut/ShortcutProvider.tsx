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
import { GlobalSearchModal } from '@/components/search/GlobalSearchModal'
import { ShortcutHelpDrawer } from './ShortcutHelpDrawer'
import {
  dispatchShortcut,
  isEditableTarget,
  type PendingChord,
  type ShortcutDeps,
  type ShortcutItem,
} from './shortcutMatcher'

// ---------- 全站唯一 keydown 监听点（计划 §2.9 / scanner.md §3.1 PC 落地） ----------
// Context 注册表 + 统一分发；页面/组件禁止自建 window keydown 监听。
// 冲突矩阵/combo 匹配/分发规划等纯逻辑在 shortcutMatcher.ts（测试加固抽取，node:test 直测）；
// 输入态自动抑制：target 为 INPUT/TEXTAREA/SELECT/isContentEditable 或带
// [data-sf-scan-input]（scanner 组件标记，ScannerManager 消费同一约定）时页面级快捷键禁用，
// global 级（Ctrl/Cmd+K、Ctrl/Cmd+R）仍生效。
// G 系 chord：800ms 序列窗口（G+I/P/S/T）。Esc 不在此处理：antd Modal/Drawer 的 Esc
// 归 antd 自身（GlobalSearchModal/ShortcutHelpDrawer 均为 antd 容器），不双抢。

export type { ShortcutScope, ShortcutDeps, ShortcutItem } from './shortcutMatcher'
export { isShortcutEnabled } from './shortcutMatcher'

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

      // chord 窗口消费即清（fire/start-chord/none 三分支都在 dispatchShortcut 规划内）；
      // 判定纯逻辑在 shortcutMatcher.dispatchShortcut——这里只落地副作用
      const pending = pendingChordRef.current
      if (pending) pendingChordRef.current = null
      const outcome = dispatchShortcut({
        items: list,
        event: e,
        editable,
        pending,
        now: performance.now(),
      })
      if (outcome.kind === 'fire') {
        e.preventDefault()
        outcome.item.handler(e, currentDeps)
        return
      }
      if (outcome.kind === 'start-chord') {
        pendingChordRef.current = { group: outcome.group, until: outcome.until }
        e.preventDefault()
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
