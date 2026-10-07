import { Flex, Progress, Typography } from 'antd'
import { useNavigate } from 'react-router'
import type { DashboardAlertItem, DashboardTaskItem, DashboardWarehouseStock } from '@/api/dashboard'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

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
 * 级别标签统一走 SfStatusTag（types/status.ts 已注册五级预警语义，frontend.md §24）。
 * 联动批次二 L8：行点击 → /inventory/alerts?level=&keyword=<SKU 编码> 预筛
 * （frontend.md §33.4；inventory:stock:view 门控，无权限不可点）。 */
export function AlertList({ items }: { items: DashboardAlertItem[] }) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const clickable = canAccess(user, 'inventory:stock:view')
  return (
    <Flex vertical>
      {items.slice(0, 8).map((item) => (
        <Flex
          key={item.id}
          vertical
          gap={2}
          style={{
            padding: '8px 4px',
            borderBottom: '1px solid var(--sf-border-subtle)',
            cursor: clickable ? 'pointer' : 'default',
          }}
          onClick={() => {
            if (!clickable) return
            navigate(`/inventory/alerts?level=${item.level}&keyword=${encodeURIComponent(item.sku_code)}`)
          }}
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

/**
 * 库位利用率（§37 利用率只用 Gauge/Progress；仓库多时列表比单 Gauge 信息密度高）。
 *
 * 2026-10-06 由 DashboardCharts.tsx 移入：本组件只用 antd Progress、不依赖 echarts，
 * 而 DashboardCharts 已整体改为 React.lazy 懒加载（让 556KB 的 echarts 不进首屏）。
 * 留在那边会被一并延迟渲染，故归入同属「非图表列表块」的 DashboardLists。
 */
export function BinUtilizationList({ items }: { items: DashboardWarehouseStock[] }) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  // 联动批次二 L11：行点击 → /bins?warehouseId=<id> 按仓库收敛（frontend.md §33.4；
  // warehouse:bin:view 门控，无权限不可点）。
  const clickable = canAccess(user, 'warehouse:bin:view')
  if (items.length === 0) {
    return <Text type="secondary">暂无仓库数据</Text>
  }
  return (
    <Flex vertical gap={12}>
      {items.map((w) => (
        <Flex
          key={w.warehouse_code}
          align="center"
          gap={12}
          style={{ cursor: clickable ? 'pointer' : 'default' }}
          onClick={() => {
            if (!clickable) return
            navigate(`/bins?warehouseId=${w.warehouse_id}`)
          }}
        >
          <Text style={{ width: 80 }} ellipsis>
            {w.warehouse_name}
          </Text>
          <Progress
            percent={w.bin_utilization}
            size="small"
            style={{ flex: 1, marginBottom: 0 }}
            format={(p) => formatPercent(p ?? 0, 0)}
          />
          <Text type="secondary" className="sf-num" style={{ fontSize: 12 }}>
            {formatNumber(w.sku_count)} SKU
          </Text>
        </Flex>
      ))}
    </Flex>
  )
}
