import { useMemo, useState } from 'react'
import { Button, Col, DatePicker, Flex, Row, Segmented, Tag, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import dayjs, { type Dayjs } from 'dayjs'
import { dashboardApi, type TrendRange } from '@/api/dashboard'
import { inventoryApi } from '@/api/inventory'
import { reportsApi, type FlowStatsPage } from '@/api/reports'
import { useAuthStore } from '@/stores/auth'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfChartCard } from '@/components/charts'
import { formatMoney, formatQty } from '@/utils/format'
import { resolveDashboardView } from './dashboardView'
import { DashboardKpiCards, type DashboardKpiItem } from './DashboardKpiCards'
import {
  BinUtilizationList,
  FlowTrendChart,
  StockStatusDonut,
  StockTrendChart,
  WarehouseRankChart,
  type StockTrendMetric,
} from './DashboardCharts'
import { AlertList, TaskList } from './DashboardLists'
import { DashboardMovements } from './DashboardMovements'

const { Text } = Typography

const RANGE_OPTIONS: Array<{ label: string; value: TrendRange }> = [
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '近90天', value: '90d' },
  { label: '自定义', value: 'custom' },
]

const DAYS_OPTIONS = [
  { label: '7天', value: 7 },
  { label: '30天', value: 30 },
  { label: '90天', value: 90 },
]

const METRIC_OPTIONS = [
  { label: '数量', value: 'qty' },
  { label: '金额', value: 'value' },
]

/** 卡片右上刷新（§45 图表卡 extra：时间档 / 刷新） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Tooltip title="刷新">
      <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} />
    </Tooltip>
  )
}

/** 出入库统计 → KPI 迷你趋势序列（近 7 日按日 qty 升序；真实接口值，无数据返回空由
 * SfSparkline 自行空占位，不画 0 不伪造——§38/§52/§54） */
function flowSparkSeries(page?: FlowStatsPage): number[] {
  const rows = page?.items?.items ?? []
  return [...rows]
    .sort((a, b) => String(a.stat_date).localeCompare(String(b.stat_date)))
    .map((row) => row.qty)
}

/**
 * Dashboard（任务书 §27：高信息密度仓储数据工作台，不做数据大屏）：
 * L1 顶部 4 KPI（§17 紧凑指标卡，管理层/仓库人员两套按 auth fail-closed 分列，
 *    requirements.md §2.1）→ L2 库存趋势 Area + 库存状态 Donut → L3 出入库趋势 Bar +
 *    仓库库存排行横向 Bar → L4 任务 + 预警 feed → L5 最近库存异动 Table + 库位利用率。
 * 全部块独立 Loading/Empty/Error 三态（§51-53：单块失败不拖垮整页），数据全部真实端点，
 * 禁止随机数伪造/写死（AGENTS.md 规则 8 / 任务书 §54）。
 */
export default function DashboardPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  // 视图判定 fail-closed：无管理层职能权限一律落仓库人员视图（dashboardView.ts）。
  // 视图仅决定指标面板选择，不作为请求参数下发（数据范围以后端会话仓库权限快照为准）
  const view = resolveDashboardView(user)

  // 库存趋势（GET /api/inventory/analytics）：口径切换（数量/金额）× 窗口（7/30/90 天）
  const [metric, setMetric] = useState<StockTrendMetric>('qty')
  const [days, setDays] = useState(30)
  // 出入库趋势（GET /api/reports/dashboard/trend）：预设档 + 自定义时间段（后端冻结契约）
  const [range, setRange] = useState<TrendRange>('7d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)

  // 自定义档：RangePicker 选定后才发请求，time_from/time_to 真实传参（YYYY-MM-DD）
  const trendFrom = range === 'custom' ? customRange?.[0]?.format('YYYY-MM-DD') : undefined
  const trendTo = range === 'custom' ? customRange?.[1]?.format('YYYY-MM-DD') : undefined
  const trendReady = range !== 'custom' || Boolean(trendFrom && trendTo)

  const today = useQuery({
    queryKey: ['dashboard', 'today'],
    queryFn: () => dashboardApi.todayMetrics(),
  })
  const summary = useQuery({
    queryKey: ['dashboard', 'summary'],
    queryFn: () => inventoryApi.stockSummary(),
  })
  const analytics = useQuery({
    queryKey: ['dashboard', 'analytics', days],
    queryFn: () => inventoryApi.analytics({ days }),
  })
  const trend = useQuery({
    queryKey: ['dashboard', 'trend', range, trendFrom, trendTo],
    queryFn: () => dashboardApi.trend(range, { time_from: trendFrom, time_to: trendTo }),
    enabled: trendReady,
  })
  const warehouseStock = useQuery({
    queryKey: ['dashboard', 'warehouse-stock'],
    queryFn: () => dashboardApi.warehouseStock(),
  })
  const tasks = useQuery({
    queryKey: ['dashboard', 'tasks'],
    queryFn: () => dashboardApi.tasks(),
  })
  const alerts = useQuery({
    queryKey: ['dashboard', 'alerts'],
    queryFn: () => dashboardApi.alerts(),
  })

  // KPI 迷你趋势数据源（GET /api/reports/inbound-stats|outbound-stats，perm reports:report:read）：
  // 仅管理层视图启用（仓库人员视图 KPI 无 spark 槽位，且避免无报表权限角色的必败请求；
  // 403 时 spark 数据为空 → SfSparkline 空占位，不影响 KPI 数字本体）
  const sparkWindow = useMemo(
    () => ({
      time_from: dayjs().subtract(6, 'day').format('YYYY-MM-DD'),
      time_to: dayjs().format('YYYY-MM-DD'),
    }),
    [],
  )
  const inboundStats = useQuery({
    queryKey: ['dashboard', 'inbound-stats', 'spark', sparkWindow],
    queryFn: () => reportsApi.inboundStats({ ...sparkWindow, page: 1, pageSize: 31 }),
    enabled: view === 'management',
  })
  const outboundStats = useQuery({
    queryKey: ['dashboard', 'outbound-stats', 'spark', sparkWindow],
    queryFn: () => reportsApi.outboundStats({ ...sparkWindow, page: 1, pageSize: 31 }),
    enabled: view === 'management',
  })

  const stockSpark = (analytics.data?.trend ?? []).map((p) => p.total_qty)
  const inboundSpark = flowSparkSeries(inboundStats.data)
  const outboundSpark = flowSparkSeries(outboundStats.data)

  // —— L1 KPI（§27 顶部 4 卡；值/趋势均为真实端点字段，无同比 delta 不伪造 §54）——
  const kpis: DashboardKpiItem[] =
    view === 'management'
      ? [
          {
            key: 'sku-count',
            label: 'SKU 总数',
            value: summary.data?.sku_count,
            loading: summary.isPending,
            error: summary.error,
            onRetry: summary.refetch,
            link: '/skus',
            sub: <>库存金额 {formatMoney(analytics.data?.total_stock_value)}</>,
          },
          {
            key: 'stock-qty',
            label: '库存总量',
            value: summary.data?.total_qty,
            format: formatQty,
            loading: summary.isPending,
            error: summary.error,
            onRetry: summary.refetch,
            link: '/inventory/stock',
            spark: stockSpark,
          },
          {
            key: 'today-inbound',
            label: '今日入库',
            value: today.data?.todayInboundCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            link: '/inbound',
            spark: inboundSpark,
          },
          {
            key: 'today-outbound',
            label: '今日出库',
            value: today.data?.todayOutboundCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            link: '/outbound',
            spark: outboundSpark,
          },
        ]
      : [
          {
            key: 'today-inbound',
            label: '今日入库',
            value: today.data?.todayInboundCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            link: '/inbound',
          },
          {
            key: 'today-outbound',
            label: '今日出库',
            value: today.data?.todayOutboundCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            link: '/outbound',
          },
          {
            key: 'pending-tasks',
            label: '待办任务',
            value: today.data?.pendingTaskCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            link: '/tasks',
          },
          {
            key: 'stock-alerts',
            label: '库存预警',
            value: today.data?.stockAlertCount,
            loading: today.isPending,
            error: today.error,
            onRetry: today.refetch,
            danger: true,
            link: '/inventory/alerts',
          },
        ]

  // 视图徽标强调色走 Token（管理层=主色 / 仓库人员=成功色）：非业务状态不经 SfStatusTag
  // 语义集（其无 primary 语义），但禁 antd 预设色（geekblue≠--sf-primary、green≠--sf-success），
  // 底色/描边按 SfStatusTag 同款 color-mix 12%/24% 公式从 Token 派生（AGENTS.md 规则 3）。
  const viewAccent = view === 'management' ? 'var(--sf-primary)' : 'var(--sf-success)'

  return (
    <div className="sf-page">
      <SfPageHeader
        title="Dashboard"
        subtitle="今日业务与库存总览"
        extra={
          <Tag
            style={{
              marginInlineEnd: 0,
              color: viewAccent,
              backgroundColor: `color-mix(in srgb, ${viewAccent} 12%, transparent)`,
              borderColor: `color-mix(in srgb, ${viewAccent} 24%, transparent)`,
            }}
          >
            {view === 'management' ? '管理层视图' : '仓库人员视图'}
          </Tag>
        }
      />

      <Flex vertical gap={16}>
        {/* L1 顶部 4 KPI（§17/§27） */}
        <DashboardKpiCards items={kpis} />

        {/* L2 库存趋势（Area）+ 库存状态（Donut） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={16}>
            <SfChartCard
              title="库存趋势"
              subtitle="现存数量 / 库存金额（按日）"
              extra={
                <Flex gap={8} wrap="wrap" align="center">
                  <Segmented
                    size="small"
                    options={METRIC_OPTIONS}
                    value={metric}
                    onChange={(v) => setMetric(v as StockTrendMetric)}
                  />
                  <Segmented
                    size="small"
                    options={DAYS_OPTIONS}
                    value={days}
                    onChange={(v) => setDays(v as number)}
                  />
                  <RefreshButton onClick={() => void analytics.refetch()} />
                </Flex>
              }
            >
              <StockTrendChart
                points={analytics.data?.trend ?? []}
                metric={metric}
                loading={analytics.isPending}
                error={analytics.error}
                onRetry={() => void analytics.refetch()}
              />
            </SfChartCard>
          </Col>
          <Col xs={24} lg={8}>
            <SfChartCard title="库存状态" subtitle="可用 / 锁定 / 冻结 / 待检·残次（余量）">
              <StockStatusDonut
                summary={summary.data}
                loading={summary.isPending}
                error={summary.error}
                onRetry={() => void summary.refetch()}
              />
            </SfChartCard>
          </Col>
        </Row>

        {/* L3 出入库趋势（Bar）+ 仓库库存排行（横向 Bar） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={16}>
            <SfChartCard
              title="出入库趋势"
              subtitle="按日入库 / 出库量"
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
                  {trendReady && <RefreshButton onClick={() => void trend.refetch()} />}
                </Flex>
              }
            >
              {!trendReady ? (
                <Text type="secondary">请选择自定义时间段后查看趋势</Text>
              ) : (
                <FlowTrendChart
                  points={trend.data ?? []}
                  loading={trend.isPending}
                  error={trend.error}
                  onRetry={() => void trend.refetch()}
                />
              )}
            </SfChartCard>
          </Col>
          <Col xs={24} lg={8}>
            <SfChartCard title="仓库库存排行" subtitle="按现存量 TOP 10">
              <WarehouseRankChart
                items={warehouseStock.data ?? []}
                loading={warehouseStock.isPending}
                error={warehouseStock.error}
                onRetry={() => void warehouseStock.refetch()}
              />
            </SfChartCard>
          </Col>
        </Row>

        {/* L4 任务 + 预警（frontend.md §5 第三层，真实 feed） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={10}>
            <SfChartCard
              title="任务概览"
              subtitle="待办任务直达入口"
              loading={tasks.isPending}
              extra={<RefreshButton onClick={() => void tasks.refetch()} />}
            >
              {tasks.error ? (
                <SfError error={tasks.error} onRetry={() => void tasks.refetch()} />
              ) : (
                <TaskList items={tasks.data ?? []} />
              )}
            </SfChartCard>
          </Col>
          <Col xs={24} lg={14}>
            <SfChartCard
              title="库存预警"
              subtitle="最新预警（点击进入预警中心）"
              loading={alerts.isPending}
              extra={<RefreshButton onClick={() => void alerts.refetch()} />}
            >
              {alerts.error ? (
                <SfError error={alerts.error} onRetry={() => void alerts.refetch()} />
              ) : (alerts.data?.length ?? 0) === 0 ? (
                <SfEmpty description="当前没有库存预警" />
              ) : (
                <AlertList items={alerts.data ?? []} />
              )}
            </SfChartCard>
          </Col>
        </Row>

        {/* L5 最近库存异动（Table）+ 库位利用率（§37 利用率只用 Progress/Gauge） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={16}>
            <SfChartCard
              title="最近库存异动"
              subtitle="库存流水 · 最新在前"
              extra={
                <Button type="link" size="small" onClick={() => navigate('/inventory/ledger')}>
                  查看全部
                </Button>
              }
            >
              <DashboardMovements />
            </SfChartCard>
          </Col>
          <Col xs={24} lg={8}>
            <SfChartCard
              title="库位利用率"
              subtitle="各仓库库位占用"
              loading={warehouseStock.isPending}
            >
              {warehouseStock.error ? (
                <SfError error={warehouseStock.error} onRetry={() => void warehouseStock.refetch()} />
              ) : (
                <BinUtilizationList items={warehouseStock.data ?? []} />
              )}
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
