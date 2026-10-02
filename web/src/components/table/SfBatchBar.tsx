import { Button, Tag } from 'antd'
import { ClearOutlined } from '@ant-design/icons'
import type { ReactNode } from 'react'

export interface SfBatchBarProps {
  selectedCount: number
  onClear: () => void
  /** 批量操作按钮组 */
  children: ReactNode
}

/**
 * 批量操作提示条（frontend.md §16.1：必须显示“已选择 N 条”）。
 * 危险批量操作请在 children 中使用带二次确认的按钮。
 */
export function SfBatchBar({ selectedCount, onClear, children }: SfBatchBarProps) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        flexWrap: 'wrap',
        padding: '6px 12px',
        marginBottom: 8,
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
