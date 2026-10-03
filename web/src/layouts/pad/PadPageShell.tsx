import type { CSSProperties, ReactNode } from 'react'
import { usePadOrientation } from './usePadOrientation'

export interface PadPageShellProps {
  /** 左栏：当前任务 / 列表区（横屏第一栏；竖屏堆叠首段） */
  tasksSlot?: ReactNode
  /** 中栏：主内容区（商品信息 / 详情摘要；竖屏堆叠于任务区之后） */
  contentSlot: ReactNode
  /** 右栏：操作区（竖屏堆叠在内容区之后；不传则横屏按剩余比例归一化） */
  actionSlot?: ReactNode
  /** 横屏三栏宽度比例 [tasks, content, action]，默认 [30, 35, 35]；缺失槽位按剩余比例归一化 */
  ratios?: [number, number, number]
  /** 底部是否预留 PadActionBar 高度（默认 true；查询类页无操作栏传 false） */
  reserveActionBar?: boolean
}

/**
 * 页面横竖屏容器原语（frontend.md §20.3/§20.4/§20.8）：
 * - landscape：三栏 DOM（tasks/content/action，宽度比例 ratios），各栏独立滚动；
 * - portrait：堆叠 DOM（任务区 → 内容滚动区 → 操作区），整体单列滚动，
 *   底部预留 PadActionBar 高度（含安全区）。
 * 结构性切换由 usePadOrientation 的 JS 状态驱动（DOM 不同，非压缩同一布局）；
 * 纯视觉差异由 pad.css 的 @media (orientation: ...) 双轨兜底。
 */
export function PadPageShell({
  tasksSlot,
  contentSlot,
  actionSlot,
  ratios = [30, 35, 35],
  reserveActionBar = true,
}: PadPageShellProps) {
  const orientation = usePadOrientation()

  const slots = [
    { key: 'tasks', node: tasksSlot, ratio: ratios[0] },
    { key: 'content', node: contentSlot, ratio: ratios[1] },
    { key: 'action', node: actionSlot, ratio: ratios[2] },
  ].filter((slot) => slot.node != null)
  const ratioSum = slots.reduce((sum, slot) => sum + slot.ratio, 0) || 1

  if (orientation === 'portrait') {
    return (
      <div
        className={`sf-pad-shell sf-pad-shell--portrait${reserveActionBar ? ' sf-pad-shell--reserve' : ''}`}
      >
        {slots.map((slot) => (
          <section key={slot.key} className={`sf-pad-shell-col sf-pad-shell-col--${slot.key}`}>
            {slot.node}
          </section>
        ))}
      </div>
    )
  }

  return (
    <div className={`sf-pad-shell sf-pad-shell--landscape${reserveActionBar ? ' sf-pad-shell--reserve' : ''}`}>
      {slots.map((slot) => {
        const style: CSSProperties = { flexBasis: `${(slot.ratio / ratioSum) * 100}%` }
        return (
          <section key={slot.key} style={style} className={`sf-pad-shell-col sf-pad-shell-col--${slot.key}`}>
            {slot.node}
          </section>
        )
      })}
    </div>
  )
}
