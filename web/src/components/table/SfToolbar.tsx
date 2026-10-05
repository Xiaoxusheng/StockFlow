import type { ReactNode } from 'react'
import { SfBatchBar } from './SfBatchBar'

export interface SfToolbarProps {
  /** 左侧：标题或操作按钮组 */
  title?: ReactNode
  /** 右侧：视图工具（刷新/密度/列设置/全屏等） */
  extra?: ReactNode
  children?: ReactNode
  /** 批量态（frontend.md §31 #4）：选中行数 >0 时根节点加 sf-table-toolbar--selected */
  selectedCount?: number
  /** 批量操作面板内容：传入后左区升级为「常规 ⇄ 已选择 N 项」grid 双面板（切换动效由 CSS 承担） */
  bulkActions?: ReactNode
  /** 批量面板「清空」回调（SfTable 默认调 rowSelection.onChange([], [], { type: 'none' })） */
  onClearSelection?: () => void
}

/** SfBatchBar onClear 为必填；仅当外部直用 SfToolbar 传 bulkActions 却漏传 onClearSelection 时兜底（SfTable 场景恒传真实现） */
const noopClear = () => undefined

/**
 * 统一列表工具栏容器：左侧动作、右侧工具（frontend.md §6.3）。
 * 使用原生 flex 布局：antd Flex 对空内容不产生盒模型，无法承担占位职责。
 * 批量态（frontend.md §31 #4）：仅当传入 bulkActions 时左区渲染
 * .sf-table-toolbar__panes 双面板（常规/批量恒占 grid-area 1/1，容器高度=max(两态) 不跳变），
 * selectedRowKeys>0 由 SfTable 自动驱动切换，业务页只传 bulkActions；
 * 不传 bulkActions 时结构与此前的单面板完全一致（独立使用本组件的页面零改动）。
 */
export function SfToolbar({ title, extra, children, selectedCount, bulkActions, onClearSelection }: SfToolbarProps) {
  const selected = typeof selectedCount === 'number' && selectedCount > 0
  const rootClassName = ['sf-table-toolbar', selected ? 'sf-table-toolbar--selected' : '']
    .filter(Boolean)
    .join(' ')
  return (
    <div
      className={rootClassName}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--sf-space-3)',
        flexWrap: 'wrap',
        marginBottom: 'var(--sf-space-3)',
      }}
    >
      {bulkActions != null ? (
        <div className="sf-table-toolbar__panes">
          <div
            className={
              selected
                ? 'sf-table-toolbar__pane sf-table-toolbar__pane--hidden'
                : 'sf-table-toolbar__pane'
            }
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--sf-space-2)',
              flexWrap: 'wrap',
            }}
          >
            {title}
            {children}
          </div>
          <div
            className={
              selected
                ? 'sf-table-toolbar__pane'
                : 'sf-table-toolbar__pane sf-table-toolbar__pane--hidden'
            }
            style={{ display: 'flex', alignItems: 'center' }}
          >
            <SfBatchBar
              className="sf-table-toolbar__batch"
              selectedCount={selectedCount ?? 0}
              onClear={onClearSelection ?? noopClear}
            >
              {bulkActions}
            </SfBatchBar>
          </div>
        </div>
      ) : (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--sf-space-2)',
            flexWrap: 'wrap',
            flex: 1,
            minWidth: 0,
          }}
        >
          {title}
          {children}
        </div>
      )}
      {extra && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 2 }}>{extra}</div>
      )}
    </div>
  )
}
