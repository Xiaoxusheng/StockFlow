import { Card, Flex, Skeleton, Statistic } from 'antd'
import type { CSSProperties } from 'react'
import { SfError } from './SfError'
import { formatNumber } from '@/utils/format'

/** 统计条单项：danger=true 时数值用语义危险色（--sf-danger Design Token） */
export interface SfInventorySummaryItem {
  label: string
  value?: number
  danger?: boolean
}

export interface SfInventorySummaryProps {
  /** 统计项清单（数值由页面从真实 API 下发，组件不做任何加工口径） */
  items: SfInventorySummaryItem[]
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  style?: CSSProperties
}

/**
 * 库存域统计条（frontend.md §10.2 统计行 / §5「不做巨大 KPI 卡片」）：
 * loading → 骨架、error → 统一错误态、就绪 → 分隔式 Statistic；数字统一 formatNumber。
 */
export function SfInventorySummary({ items, loading = false, error, onRetry, style }: SfInventorySummaryProps) {
  return (
    <Card size="small" styles={{ body: { padding: '12px 16px' } }} style={style}>
      {loading ? (
        <Flex gap={32}>
          {items.map((item) => (
            <Skeleton.Node active key={item.label} style={{ width: 64, height: 40 }} />
          ))}
        </Flex>
      ) : error ? (
        <SfError error={error} onRetry={onRetry} />
      ) : (
        <Flex gap={0} wrap="wrap">
          {items.map((item, index) => (
            <Statistic
              key={item.label}
              title={item.label}
              value={formatNumber(item.value ?? 0)}
              valueStyle={{
                fontSize: 18,
                color: item.danger ? 'var(--sf-danger)' : undefined,
              }}
              style={{
                padding: '0 24px',
                borderRight:
                  index < items.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined,
              }}
            />
          ))}
        </Flex>
      )}
    </Card>
  )
}
