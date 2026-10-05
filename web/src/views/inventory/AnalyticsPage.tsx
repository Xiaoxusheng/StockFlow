import { useState } from 'react'
import {
  Button,
  Card,
  Col,
  Flex,
  Row,
  Segmented,
  Skeleton,
  Statistic,
  Tooltip,
  Typography,
} from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { dashboardApi, type DashboardWarehouseStock } from '@/api/dashboard'
import { inventoryApi, type StockSummary } from '@/api/inventory'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import {
  SfAreaChart,
  SfBarChart,
  SfChartCard,
  SfDonutChart,
  SfHBarChart,
  type SfDonutDatum,
} from '@/components/charts'
import { formatMoney, formatNumber, formatPercent, formatQty } from '@/utils/format'

const { Text } = Typography

const RANGE_OPTIONS = [
  { label: '近7天', value: '7' },
  { label: '近30天', value: '30' },
  { label: '近90天', value: '90' },
]

const DEFAULT_RANGE = '30'

/** 图表统一高度（§45 紧凑卡片适配，与 Dashboard 图表块一致） */
const CHART_HEIGHT = 280

/** 卡片右上刷新（§45 图表卡 extra：时间档 / 刷新） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Tooltip title="刷新">
      <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} />
    </Tooltip>
  )
}

/**
 * 库存状态构成三片（v2 修订互斥口径）：可用 available_qty / 锁定 locked_qty / 异常
 * abnormal_qty，全部为 GET /api/inventory/summary（routes.go:64）后端直出字段——
 * 后端冻结定义 abnormal = 冻结 + 残次（repository.go DashboardSummary「异常库存量 =
 * 冻结 + 残次」）。禁止四片 available/locked/frozen/abnormal（frozen 双计、四片之和
 * ≠ total）；三片之和 = total − 待检（inventory-rules.md:39 恒等式含待检，而
 * dashboardSummary 无待检字段），缺口由卡副标题显式披露，不做任何前端衍生
 * （AGENTS.md 规则 8：前端不算库存）。
 */
function buildStockStatusData(summary: StockSummary): SfDonutDatum[] {
  return [
    { name: '可用', value: summary.available_qty },
    { name: '锁定', value: summary.locked_qty },
    { name: '异常', value: summary.abnormal_qty },
  ]
}

/** 仓库库存排行数据映射（GET /api/reports/dashboard/warehouse-stock，routes.go:60） */
function buildWarehouseRankData(items: DashboardWarehouseStock[]) {
  return items.map((w) => ({ name: w.warehouse_name, qty: w.total_qty }))
}

/**
 * 库存分析（frontend.md §10.1 口径：库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势）。
 *
 * 数据全部来自真实端点（internal/reports/routes.go，权限均挂 inventory:inventory:list）：
 * - GET /api/inventory/analytics（routes.go:67，2026-10-05 已交付）：指标条 + ABC + 趋势
 * - GET /api/inventory/summary（routes.go:64）：库存状态构成 Donut
 * - GET /api/reports/dashboard/warehouse-stock（routes.go:60）：仓库库存排行横向 Bar
 *
 * §39 补图挂账（无真实数据不伪造，§54）：
 * - SKU 库存 TOP10：/api/reports/inventory-summary 为分页明细且无排序参数
 *   （internal/reports/handler.go:76-95 仅 page/pageSize/warehouse_id/sku_id），
 *   前端跨页取 TOP 属拼装数据——走 SfEmpty 挂账后端排序聚合参数或专用端点
 * - 库存周转趋势：/api/reports/inventory-turnover 为按仓 + SKU 统计快照行集
 *   （repository.go TurnoverRow）非时序，不能画时序 Line——走 SfEmpty 挂账后端时序端点
 */
export default function AnalyticsPage() {
  const [range, setRange] = useState(DEFAULT_RANGE)
  const analytics = useQuery({
    queryKey: ['inventory', 'analytics', range],
    queryFn: () => inventoryApi.analytics({ days: Number(range) }),
  })
  const summary = useQuery({
    queryKey: ['inventory', 'summary'],
    queryFn: () => inventoryApi.stockSummary(),
  })
  const warehouseStock = useQuery({
    queryKey: ['inventory', 'warehouse-stock'],
    queryFn: () => dashboardApi.warehouseStock(),
  })

  const data = analytics.data
  const trend = data?.trend ?? []
  const abc = data?.abc ?? []

  return (
    <div className="sf-page">
      <SfPageHeader title="库存分析" subtitle="库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势" />

      <Flex vertical gap={16}>
        {/* 指标条：库存金额 / 库存总量 / SKU 数 / 周转率 / 周转天数（保留） */}
        <Card size="small" styles={{ body: { padding: '12px 16px' } }}>
          {analytics.isPending ? (
            <Flex gap={32}>
              {['库存金额', '库存总量', 'SKU 数', '周转率', '周转天数'].map((label) => (
                <Skeleton.Node active key={label} style={{ width: 72, height: 40 }} />
              ))}
            </Flex>
          ) : analytics.error ? (
            <SfError error={analytics.error} onRetry={analytics.refetch} description="库存分析数据加载失败，请稍后重试" />
          ) : (
            <Flex gap={0} wrap="wrap">
              {[
                { label: '库存金额', value: formatMoney(data?.total_stock_value) },
                { label: '库存总量', value: formatQty(data?.total_qty) },
                { label: 'SKU 数', value: formatNumber(data?.total_sku_count) },
                { label: '周转率', value: `${formatNumber(data?.turnover_rate, 2)} 次` },
                { label: '周转天数', value: `${formatNumber(data?.turnover_days, 1)} 天` },
              ].map((item, index, arr) => (
                <Statistic
                  key={item.label}
                  title={item.label}
                  value={item.value}
                  valueStyle={{ fontSize: 18 }}
                  style={{
                    padding: '0 24px',
                    borderRight: index < arr.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined,
                  }}
                />
              ))}
            </Flex>
          )}
        </Card>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={10}>
            {/* ABC 分析（§34 柱状图）：分档由后端计算（80/15/5），前端只呈现 */}
            <SfChartCard title="ABC 分析（按库存金额）" subtitle="各档位金额占比">
              <Flex vertical gap={12}>
                <SfBarChart
                  data={abc.map((item) => ({
                    grade: `${item.grade} 类`,
                    value_percent: item.value_percent,
                  }))}
                  xField="grade"
                  yField="value_percent"
                  height={220}
                  loading={analytics.isPending}
                  error={analytics.error}
                  onRetry={() => void analytics.refetch()}
                  emptyText="暂无 ABC 分析数据"
                />
                {!analytics.isPending && !analytics.error && abc.length > 0 ? (
                  <Flex vertical>
                    {abc.map((item) => (
                      <Flex
                        key={item.grade}
                        justify="space-between"
                        gap={12}
                        style={{
                          padding: '6px 4px',
                          borderBottom: '1px solid var(--sf-border-subtle)',
                        }}
                      >
                        <Text strong>{item.grade} 类</Text>
                        <Flex gap={16}>
                          <Text type="secondary">SKU {formatNumber(item.sku_count)}</Text>
                          <Text type="secondary">金额 {formatMoney(item.value_amount)}</Text>
                          <Text className="sf-num">占比 {formatPercent(item.value_percent)}</Text>
                        </Flex>
                      </Flex>
                    ))}
                  </Flex>
                ) : null}
              </Flex>
            </SfChartCard>
          </Col>
          <Col xs={24} lg={14}>
            {/* 库存趋势（§33 面积图）：数量 / 金额双系列，trend 为后端日末口径 */}
            <SfChartCard
              title="库存趋势"
              subtitle="库存数量 / 库存金额（按日）"
              extra={
                <Flex gap={8} align="center">
                  <Segmented
                    size="small"
                    options={RANGE_OPTIONS}
                    value={range}
                    onChange={(v) => setRange(v as string)}
                  />
                  <RefreshButton onClick={() => void analytics.refetch()} />
                </Flex>
              }
            >
              <SfAreaChart
                data={trend.map((point) => ({
                  date: point.date,
                  total_qty: point.total_qty,
                  stock_value: point.stock_value,
                }))}
                xField="date"
                series={[
                  { key: 'total_qty', name: '库存数量' },
                  { key: 'stock_value', name: '库存金额' },
                ]}
                height={CHART_HEIGHT}
                loading={analytics.isPending}
                error={analytics.error}
                onRetry={() => void analytics.refetch()}
                emptyText="所选时间范围内暂无库存趋势数据"
              />
            </SfChartCard>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          <Col xs={24} lg={8}>
            {/* 库存状态构成（§36 环形图）：三片互斥口径，副标题披露口径缺口（见 buildStockStatusData 注） */}
            <SfChartCard
              title="库存状态构成"
              subtitle="可用 / 锁定 / 异常（异常 = 冻结 + 残次，后端冻结口径；待检库存未计入构成）"
              extra={<RefreshButton onClick={() => void summary.refetch()} />}
            >
              <SfDonutChart
                data={summary.data ? buildStockStatusData(summary.data) : []}
                height={CHART_HEIGHT}
                loading={summary.isPending}
                error={summary.error}
                onRetry={() => void summary.refetch()}
                emptyText="当前没有库存数据"
              />
            </SfChartCard>
          </Col>
          <Col xs={24} lg={16}>
            {/* 仓库库存排行（§35 排行榜首选横向 Bar）：dashboardApi.warehouseStock 复用 */}
            <SfChartCard
              title="仓库库存排行"
              subtitle="按现存量 TOP 10"
              extra={<RefreshButton onClick={() => void warehouseStock.refetch()} />}
            >
              <SfHBarChart
                data={buildWarehouseRankData(warehouseStock.data ?? [])}
                categoryField="name"
                valueField="qty"
                topN={10}
                height={CHART_HEIGHT}
                loading={warehouseStock.isPending}
                error={warehouseStock.error}
                onRetry={() => void warehouseStock.refetch()}
                emptyText="当前没有仓库库存数据"
              />
            </SfChartCard>
          </Col>
        </Row>

        <Row gutter={[16, 16]}>
          {/* §39 挂账空态：无真实数据不伪造（§54），后端立项后接入 */}
          <Col xs={24} lg={12}>
            <SfChartCard title="SKU 库存 TOP 10" subtitle="按现存量排行">
              <SfEmpty description="后端暂无 SKU 库存排序聚合端点（/api/reports/inventory-summary 为分页明细、无排序参数，前端跨页取 TOP 属拼装数据），待后端补充排序聚合端点后接入" />
            </SfChartCard>
          </Col>
          <Col xs={24} lg={12}>
            <SfChartCard title="库存周转趋势" subtitle="按日周转率 / 周转天数">
              <SfEmpty description="GET /api/reports/inventory-turnover 为按仓 + SKU 的统计快照行集，非时序数据，无法绘制周转趋势，待后端时序端点交付后接入" />
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
