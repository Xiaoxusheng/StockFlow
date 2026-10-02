import { lazy, Suspense, useState } from 'react'
import { Card, Col, Flex, Progress, Row, Skeleton, Segmented, Statistic, Typography } from 'antd'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { DashboardTaskItem, TrendPoint } from '@/api/dashboard'
import { dashboardApi, type TrendRange } from '@/api/dashboard'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

const Line = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Line })))
const Bar = lazy(() => import('@ant-design/plots').then((m) => ({ default: m.Bar })))

const { Text } = Typography

const RANGE_OPTIONS = [
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '近90天', value: '90d' },
]

/**
 * Dashboard（frontend.md §5 四层结构，不做巨大 KPI 卡片）：
 * L1 今日业务指标 → L2 入库/出库/库存趋势 → L3 任务+预警 → L4 仓库与库存分析
 */
export default function DashboardPage() {
  const [range, setRange] = useState<TrendRange>('7d')

  const today = useQuery({ queryKey: ['dashboard', 'today'], queryFn: dashboardApi.todayMetrics })
  const trend = useQuery({
    queryKey: ['dashboard', 'trend', range],
    queryFn: () => dashboardApi.trend(range),
  })
  const tasks = useQuery({ queryKey: ['dashboard', 'tasks'], queryFn: dashboardApi.tasks })
  const alerts = useQuery({ queryKey: ['dashboard', 'alerts'], queryFn: dashboardApi.alerts })
  const warehouseStock = useQuery({
    queryKey: ['dashboard', 'warehouse-stock'],
    queryFn: dashboardApi.warehouseStock,
  })

  return (
    <div className="sf-page">
      <SfPageHeader title="Dashboard" subtitle="今日业务与库存总览" />

      <Flex vertical gap={16}>
        {/* L1 今日业务指标 */}
        <MetricStrip
          loading={today.isPending}
          error={today.error}
          onRetry={today.refetch}
          metrics={[
            { label: '今日入库单', value: today.data?.todayInboundCount, link: '/inbound' },
            { label: '今日出库单', value: today.data?.todayOutboundCount, link: '/outbound' },
            { label: '待处理任务', value: today.data?.pendingTaskCount, link: '/tasks' },
            { label: '库存预警', value: today.data?.stockAlertCount, link: '/inventory/alerts', danger: true },
          ]}
        />

        {/* L2 趋势 */}
        <Card
          size="small"
          title="业务趋势"
          extra={
            <Segmented size="small" options={RANGE_OPTIONS} value={range} onChange={(v) => setRange(v as TrendRange)} />
          }
        >
          {trend.isPending ? (
            <Skeleton active paragraph={{ rows: 5 }} />
          ) : trend.error ? (
            <SfError error={trend.error} onRetry={trend.refetch} />
          ) : (
            <Suspense fallback={<Skeleton active paragraph={{ rows: 5 }} />}>
              <TrendCharts points={trend.data ?? []} />
            </Suspense>
          )}
        </Card>

        {/* L3 任务 + 预警 */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={10}>
            <Card size="small" title="任务概览">
              {tasks.isPending ? (
                <Skeleton active paragraph={{ rows: 4 }} />
              ) : tasks.error ? (
                <SfError error={tasks.error} onRetry={tasks.refetch} />
              ) : (
                <TaskList items={tasks.data ?? []} />
              )}
            </Card>
          </Col>
          <Col xs={24} lg={14}>
            <Card size="small" title="库存预警">
              {alerts.isPending ? (
                <Skeleton active paragraph={{ rows: 4 }} />
              ) : alerts.error ? (
                <SfError error={alerts.error} onRetry={alerts.refetch} />
              ) : (alerts.data?.length ?? 0) === 0 ? (
                <Text type="secondary">当前没有库存预警</Text>
              ) : (
                <AlertList items={alerts.data ?? []} />
              )}
            </Card>
          </Col>
        </Row>

        {/* L4 仓库 + 库存分析 */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={14}>
            <Card size="small" title="仓库库存分布">
              {warehouseStock.isPending ? (
                <Skeleton active paragraph={{ rows: 4 }} />
              ) : warehouseStock.error ? (
                <SfError error={warehouseStock.error} onRetry={warehouseStock.refetch} />
              ) : (
                <Suspense fallback={<Skeleton active paragraph={{ rows: 4 }} />}>
                  <WarehouseBar items={warehouseStock.data ?? []} />
                </Suspense>
              )}
            </Card>
          </Col>
          <Col xs={24} lg={10}>
            <Card size="small" title="库位利用率">
              {warehouseStock.isPending ? (
                <Skeleton active paragraph={{ rows: 4 }} />
              ) : warehouseStock.error ? (
                <SfError error={warehouseStock.error} onRetry={warehouseStock.refetch} />
              ) : (warehouseStock.data?.length ?? 0) === 0 ? (
                <Text type="secondary">暂无仓库数据</Text>
              ) : (
                <Flex vertical gap={12}>
                  {(warehouseStock.data ?? []).map((w) => (
                    <Flex key={w.warehouseCode} align="center" gap={12}>
                      <Text style={{ width: 80 }} ellipsis>
                        {w.warehouseName}
                      </Text>
                      <Progress
                        percent={w.binUtilization}
                        size="small"
                        style={{ flex: 1, marginBottom: 0 }}
                        format={(p) => formatPercent(p ?? 0, 0)}
                      />
                    </Flex>
                  ))}
                </Flex>
              )}
            </Card>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}

function MetricStrip({
  metrics,
  loading,
  error,
  onRetry,
}: {
  metrics: Array<{ label: string; value?: number; link: string; danger?: boolean }>
  loading: boolean
  error: unknown
  onRetry: () => void
}) {
  const navigate = useNavigate()
  return (
    <Card size="small" styles={{ body: { padding: '12px 8px' } }}>
      {error ? (
        <SfError error={error} onRetry={onRetry} />
      ) : (
        <Row gutter={8}>
          {metrics.map((metric, index) => (
            <Col key={metric.label} xs={12} md={6}>
              <Flex
                vertical
                align="center"
                gap={2}
                style={{ cursor: 'pointer', borderRight: index < metrics.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined }}
                onClick={() => navigate(metric.link)}
              >
                <Statistic
                  title={<Text type="secondary" style={{ fontSize: 13 }}>{metric.label}</Text>}
                  value={loading ? '-' : formatNumber(metric.value ?? 0)}
                  valueStyle={metric.danger ? { color: 'var(--sf-danger)' } : undefined}
                />
              </Flex>
            </Col>
          ))}
        </Row>
      )}
    </Card>
  )
}

function TrendCharts({ points }: { points: TrendPoint[] }) {
  const bizData = points.flatMap((p) => [
    { date: p.date, type: '入库', qty: p.inbound },
    { date: p.date, type: '出库', qty: p.outbound },
  ])
  const stockData = points.map((p) => ({ date: p.date, qty: p.stockQty }))

  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} lg={12}>
        <Text type="secondary">入库 / 出库趋势</Text>
        <Line
          data={bizData}
          xField="date"
          yField="qty"
          colorField="type"
          shapeField="smooth"
          height={220}
          style={{ maxWidth: '100%' }}
        />
      </Col>
      <Col xs={24} lg={12}>
        <Text type="secondary">库存趋势</Text>
        <Line
          data={stockData}
          xField="date"
          yField="qty"
          shapeField="smooth"
          height={220}
          style={{ maxWidth: '100%' }}
        />
      </Col>
    </Row>
  )
}

function TaskList({ items }: { items: DashboardTaskItem[] }) {
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

function AlertList({ items }: { items: Array<{ id: number | string; skuCode: string; productName: string; message: string; createdAt: string }> }) {
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
          <Flex justify="space-between" gap={12}>
            <Text strong style={{ fontSize: 13 }}>
              {item.skuCode} · {item.productName}
            </Text>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {formatDateTime(item.createdAt)}
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

function WarehouseBar({ items }: { items: Array<{ warehouseCode: string; warehouseName: string; totalQty: number }> }) {
  if (items.length === 0) {
    return <Text type="secondary">暂无仓库数据</Text>
  }
  return (
    <Bar
      data={items.map((w) => ({ warehouse: w.warehouseName, 库存量: w.totalQty }))}
      xField="warehouse"
      yField="库存量"
      height={240}
      style={{ maxWidth: '100%' }}
    />
  )
}
