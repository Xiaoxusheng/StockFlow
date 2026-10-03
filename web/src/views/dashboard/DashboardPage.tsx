import { useState } from 'react'
import { Card, Col, DatePicker, Flex, Row, Segmented, Skeleton, Tag, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import type { Dayjs } from 'dayjs'
import { dashboardApi, type DashboardTodayMetrics, type TrendRange } from '@/api/dashboard'
import { useAuthStore } from '@/stores/auth'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { resolveDashboardView } from './dashboardView'
import { DashboardMetricStrip, type DashboardMetric } from './DashboardMetricStrip'
import {
  BinUtilizationList,
  TrendChartSkeleton,
  TrendCharts,
  WarehouseBar,
} from './DashboardCharts'
import { AlertList, TaskList } from './DashboardLists'

const { Text } = Typography

const RANGE_OPTIONS: Array<{ label: string; value: TrendRange }> = [
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '近90天', value: '90d' },
  { label: '自定义', value: 'custom' },
]

/** 管理层视图指标（requirements.md §2.1：仓库/SKU/总库存/金额 + 今日进出 + 订单 + 预警/临期/积压 + 审核与异常） */
function buildManagementMetrics(data: DashboardTodayMetrics | undefined): DashboardMetric[] {
  return [
    { label: '仓库总数', value: data?.warehouseCount, link: '/warehouses' },
    { label: 'SKU 总数', value: data?.skuCount, link: '/skus' },
    { label: '总库存', value: data?.totalQty, link: '/inventory/stock' },
    { label: '库存金额', value: data?.stockValue, link: '/inventory/analytics' },
    { label: '今日入库', value: data?.todayInboundCount, link: '/inbound' },
    { label: '今日出库', value: data?.todayOutboundCount, link: '/outbound' },
    { label: '订单数量', value: data?.orderCount, link: '/sales' },
    { label: '库存预警', value: data?.stockAlertCount, link: '/inventory/alerts', danger: true },
    { label: '临期商品', value: data?.nearExpiryQty, link: '/inventory/batches', danger: true },
    { label: '积压商品', value: data?.slowMovingQty, link: '/inventory/alerts', danger: true },
    { label: '待审核单据', value: data?.pendingApprovalCount, link: '/tasks' },
    { label: '待处理异常', value: data?.pendingExceptionCount, link: '/exceptions', danger: true },
  ]
}

/** 仓库人员视图指标（requirements.md §2.1：收货→上架→拣货→复核→打包→发货→盘点→异常 作业链路） */
function buildOperatorMetrics(data: DashboardTodayMetrics | undefined): DashboardMetric[] {
  return [
    { label: '待收货', value: data?.pendingReceiveCount, link: '/purchases/receipts' },
    { label: '待上架', value: data?.pendingPutawayCount, link: '/inbound' },
    { label: '待拣货', value: data?.pendingPickCount, link: '/picking' },
    { label: '待复核', value: data?.pendingCheckCount, link: '/checking' },
    { label: '待打包', value: data?.pendingPackCount, link: '/packing' },
    { label: '待发货', value: data?.pendingShipmentCount, link: '/shipment' },
    { label: '待盘点', value: data?.pendingCountCount, link: '/counts' },
    { label: '待处理异常', value: data?.pendingExceptionCount, link: '/exceptions', danger: true },
  ]
}

/**
 * Dashboard（frontend.md §5 四层结构，不做巨大 KPI 卡片）：
 * L1 今日业务指标（按 auth 权限 fail-closed 分管理层/仓库人员两套，requirements.md §2.1）
 * → L2 入库/出库/库存趋势（7d/30d/90d/自定义 from→to）→ L3 任务+预警 → L4 仓库与库存分析。
 * 数据全部来自 /api/reports/dashboard/*，禁止写死。
 */
export default function DashboardPage() {
  const user = useAuthStore((s) => s.user)
  // 视图判定 fail-closed：无管理层职能权限一律落仓库人员视图（dashboardView.ts）
  const view = resolveDashboardView(user)
  const scope = { view } as const

  const [range, setRange] = useState<TrendRange>('7d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)

  // 自定义档：RangePicker 选定后才发请求，from/to 真实传参（YYYY-MM-DD）
  const trendFrom = range === 'custom' ? customRange?.[0]?.format('YYYY-MM-DD') : undefined
  const trendTo = range === 'custom' ? customRange?.[1]?.format('YYYY-MM-DD') : undefined
  const trendReady = range !== 'custom' || Boolean(trendFrom && trendTo)

  const today = useQuery({
    queryKey: ['dashboard', 'today', view],
    queryFn: () => dashboardApi.todayMetrics(scope),
  })
  const trend = useQuery({
    queryKey: ['dashboard', 'trend', view, range, trendFrom, trendTo],
    queryFn: () => dashboardApi.trend(range, { ...scope, from: trendFrom, to: trendTo }),
    enabled: trendReady,
  })
  const tasks = useQuery({
    queryKey: ['dashboard', 'tasks', view],
    queryFn: () => dashboardApi.tasks(scope),
  })
  const alerts = useQuery({
    queryKey: ['dashboard', 'alerts', view],
    queryFn: () => dashboardApi.alerts(scope),
  })
  const warehouseStock = useQuery({
    queryKey: ['dashboard', 'warehouse-stock', view],
    queryFn: () => dashboardApi.warehouseStock(scope),
  })

  const metrics =
    view === 'management' ? buildManagementMetrics(today.data) : buildOperatorMetrics(today.data)

  return (
    <div className="sf-page">
      <SfPageHeader
        title="Dashboard"
        subtitle="今日业务与库存总览"
        extra={
          <Tag color={view === 'management' ? 'geekblue' : 'green'} style={{ marginInlineEnd: 0 }}>
            {view === 'management' ? '管理层视图' : '仓库人员视图'}
          </Tag>
        }
      />

      <Flex vertical gap={16}>
        {/* L1 今日业务指标（两套指标按视图分列） */}
        <DashboardMetricStrip
          metrics={metrics}
          loading={today.isPending}
          error={today.error}
          onRetry={today.refetch}
        />

        {/* L2 趋势（预设档 + 自定义时间段） */}
        <Card
          size="small"
          title="业务趋势"
          extra={
            <Flex gap={8} wrap="wrap" align="center">
              <Segmented
                size="small"
                options={RANGE_OPTIONS}
                value={range}
                onChange={(v) => setRange(v as TrendRange)}
              />
              {range === 'custom' && (
                <DatePicker.RangePicker
                  size="small"
                  value={customRange}
                  onChange={(values) =>
                    setCustomRange(values && values[0] && values[1] ? [values[0], values[1]] : null)
                  }
                  allowClear={false}
                  placeholder={['开始日期', '结束日期']}
                />
              )}
            </Flex>
          }
        >
          {!trendReady ? (
            <Text type="secondary">请选择自定义时间段后查看趋势</Text>
          ) : trend.isPending ? (
            <TrendChartSkeleton />
          ) : trend.error ? (
            <SfError error={trend.error} onRetry={trend.refetch} />
          ) : (
            <TrendCharts points={trend.data ?? []} />
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
                <WarehouseBar items={warehouseStock.data ?? []} />
              )}
            </Card>
          </Col>
          <Col xs={24} lg={10}>
            <Card size="small" title="库位利用率">
              {warehouseStock.isPending ? (
                <Skeleton active paragraph={{ rows: 4 }} />
              ) : warehouseStock.error ? (
                <SfError error={warehouseStock.error} onRetry={warehouseStock.refetch} />
              ) : (
                <BinUtilizationList items={warehouseStock.data ?? []} />
              )}
            </Card>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
