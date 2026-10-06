import { Button, Flex, Space } from 'antd'
import type { ReactNode } from 'react'

export interface SfToolbarProps {
  /** 左侧：标题或操作按钮组 */
  title?: ReactNode
  /** 右侧：视图工具（刷新/密度/列设置/全屏等） */
  extra?: ReactNode
  children?: ReactNode
  /** 选中行数>0 时，bulkActions 按钮**追加**在左区既有按钮之后（同一行） */
  selectedCount?: number
  /** 批量操作按钮组（选中时显示） */
  bulkActions?: ReactNode
  /** 清空选中（右区「已选择 N 条 · 清空」处） */
  onClearSelection?: () => void
}

/**
 * 统一列表工具栏（frontend.md §6.3 / §16.1，2026-10-06 批量交互改版）：
 *
 * **布局（用户口径）**：批量操作按钮**与其他按钮同一行**（追加在左区既有按钮之后，
 * 不再单独占一条批量面板）；「已选择 N 条 · 清空」计数**另显示在右端**
 * （视图工具旁，不与按钮同排）。
 *
 * 旧实现为「双面板 grid 叠加」：选中后整个左区被「已选择 N 条 + 按钮 + 清空」
 * 面板替换，批量按钮独占一条且与页面上其他按钮分离——已废弃。
 */
export function SfToolbar({
  title,
  extra,
  children,
  selectedCount,
  bulkActions,
  onClearSelection,
}: SfToolbarProps) {
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
      {/* 左区：标题 + 既有动作 + 批量操作按钮（选中时**追加同行**，不替换原按钮） */}
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
        {selected && bulkActions != null && (
          <Space size={4} style={{ marginInlineStart: 'var(--sf-space-2)' }}>
            {bulkActions}
          </Space>
        )}
      </div>
      {/* 右区：「已选择 N 条 · 清空」（选中时，用户口径：计数另显示、不与按钮同排）+ 视图工具 */}
      <Flex align="center" gap="var(--sf-space-3)" style={{ flex: '0 0 auto' }}>
        {selected && bulkActions != null && (
          <span
            style={{ fontSize: 'var(--sf-font-size-caption)', color: 'var(--sf-text-secondary)' }}
          >
            已选择 {selectedCount} 条
            <Button
              type="link"
              size="small"
              style={{ paddingInline: 4 }}
              onClick={onClearSelection ?? (() => undefined)}
            >
              清空
            </Button>
          </span>
        )}
        {extra}
      </Flex>
    </div>
  )
}
