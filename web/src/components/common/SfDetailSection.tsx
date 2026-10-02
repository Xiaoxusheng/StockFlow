import { Card, Flex, Typography } from 'antd'
import type { ReactNode } from 'react'

const { Text } = Typography

export interface SfDetailSectionProps {
  title: ReactNode
  extra?: ReactNode
  children: ReactNode
}

/** 详情页分区（frontend.md §7：禁止把所有信息堆成一个大 Card） */
export function SfDetailSection({ title, extra, children }: SfDetailSectionProps) {
  return (
    <Card
      size="small"
      title={<Text strong>{title}</Text>}
      extra={extra}
      styles={{ body: { padding: 16 } }}
    >
      {children}
    </Card>
  )
}

export interface SfSummaryBarProps {
  items: Array<{ label: ReactNode; value: ReactNode }>
}

/** 详情页头部关键指标条（frontend.md §7：状态 + 关键指标） */
export function SfSummaryBar({ items }: SfSummaryBarProps) {
  return (
    <Flex wrap="wrap" gap={16}>
      {items.map((item) => (
        <Flex key={String(item.label)} gap={6} align="baseline">
          <Text type="secondary">{item.label}</Text>
          <Text strong className="sf-num">
            {item.value}
          </Text>
        </Flex>
      ))}
    </Flex>
  )
}
