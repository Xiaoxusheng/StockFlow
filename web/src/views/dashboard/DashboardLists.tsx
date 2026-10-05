import { Flex, Typography } from 'antd'
import { useNavigate } from 'react-router'
import type { DashboardAlertItem, DashboardTaskItem } from '@/api/dashboard'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 第三层任务概览（数据来自 GET /api/reports/dashboard/tasks，跳转链接由后端下发） */
export function TaskList({ items }: { items: DashboardTaskItem[] }) {
  const navigate = useNavigate()
  if (items.length === 0) {
    return <Text type="secondary">当前没有待处理任务</Text>
  }
  return (
    <Flex vertical>
      {items.map((item) => (
        <Flex
          key={item.type}
          align="center"
          justify="space-between"
          style={{ padding: '8px 4px', borderBottom: '1px solid var(--sf-border-subtle)', cursor: 'pointer' }}
          onClick={() => navigate(item.link)}
        >
          <Text>{item.label}</Text>
          <Text strong className="sf-num">
            {formatNumber(item.count)}
          </Text>
        </Flex>
      ))}
    </Flex>
  )
}

/** 第三层库存预警（数据来自 GET /api/reports/dashboard/alerts，前 8 条 feed）；
 * 级别标签统一走 SfStatusTag（types/status.ts 已注册五级预警语义，frontend.md §24） */
export function AlertList({ items }: { items: DashboardAlertItem[] }) {
  const navigate = useNavigate()
  return (
    <Flex vertical>
      {items.slice(0, 8).map((item) => (
        <Flex
          key={item.id}
          vertical
          gap={2}
          style={{ padding: '8px 4px', borderBottom: '1px solid var(--sf-border-subtle)', cursor: 'pointer' }}
          onClick={() => navigate('/inventory/alerts')}
        >
          <Flex justify="space-between" gap={12} align="center">
            <Flex gap={8} align="center" style={{ minWidth: 0 }}>
              <SfStatusTag status={item.level} />
              <Text strong style={{ fontSize: 13 }} ellipsis>
                {item.sku_code} · {item.product_name}
              </Text>
            </Flex>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {formatDateTime(item.created_at)}
            </Text>
          </Flex>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {item.message}
          </Text>
        </Flex>
      ))}
    </Flex>
  )
}
