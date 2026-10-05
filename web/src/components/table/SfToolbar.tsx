import type { ReactNode } from 'react'

export interface SfToolbarProps {
  /** 左侧：标题或操作按钮组 */
  title?: ReactNode
  /** 右侧：视图工具（刷新/密度/列设置/全屏等） */
  extra?: ReactNode
  children?: ReactNode
}

/**
 * 统一列表工具栏容器：左侧动作、右侧工具（frontend.md §6.3）。
 * 使用原生 flex 布局：antd Flex 对空内容不产生盒模型，无法承担占位职责。
 */
export function SfToolbar({ title, extra, children }: SfToolbarProps) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--sf-space-3)',
        flexWrap: 'wrap',
        marginBottom: 'var(--sf-space-3)',
      }}
    >
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
      {extra && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 2 }}>{extra}</div>
      )}
    </div>
  )
}
