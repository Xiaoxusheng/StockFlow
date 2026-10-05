import { Button, Tag } from 'antd'
import { ClearOutlined } from '@ant-design/icons'
import type { ReactNode } from 'react'

export interface SfBatchBarProps {
  selectedCount: number
  onClear: () => void
  /** 批量操作按钮组 */
  children: ReactNode
  /** 追加类名（SfToolbar 批量面板语境传 sf-table-toolbar__batch 归零底距） */
  className?: string
}

/**
 * 批量操作提示条（frontend.md §16.1：必须显示“已选择 N 条”）。
 * 危险批量操作请在 children 中使用带二次确认的按钮。
 * 底距由 global.css .sf-batch-bar 承担（原内联 8px 迁出，值不变）。
 */
export function SfBatchBar({ selectedCount, onClear, children, className }: SfBatchBarProps) {
  return (
    <div
      className={className ? `sf-batch-bar ${className}` : 'sf-batch-bar'}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        flexWrap: 'wrap',
        padding: '6px 12px',
        background: 'var(--sf-surface-elevated)',
        border: '1px solid var(--sf-border)',
        borderRadius: 'var(--sf-radius-md)',
      }}
    >
      <span>
        已选择 <Tag color="blue" style={{ marginInlineEnd: 4 }}>{selectedCount}</Tag> 条
      </span>
      {children}
      <Button
        type="text"
        size="small"
        icon={<ClearOutlined />}
        onClick={onClear}
      >
        清空
      </Button>
    </div>
  )
}
