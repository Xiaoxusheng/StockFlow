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
import { analyticsApi, type InventorySkuTopRow } from '@/api/analytics'
import { dashboardApi, type DashboardWarehouseStock } from '@/api/dashboard'
import { inventoryApi, type StockSummary } from '@/api/inventory'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import {
  SfAreaChart,
  SfBarChart,
  SfChartCard,
  SfDonutChart,
  SfHBarChart,
  SfLineChart,
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

/** SKU TOP 排序指标（GET /api/inventory/sku-top metric=qty|value，后端 ORDER BY 二选一白名单） */
type SkuTopMetric = 'qty' | 'value'

const SKU_METRIC_OPTIONS = [
  { label: '按数量', value: 'qty' },
  { label: '按金额', value: 'value' },
]

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
 * SKU TOP 排行数据映射（GET /api/inventory/sku-top，analytics.go:30-39：后端
 * ORDER BY total_qty/stock_value DESC + HAVING SUM(total_qty)>0 只看在库 SKU，
 * 前端保持原序、不重排不拼算）。类目轴取 sku_code——sku_name 同商品多 SKU 会重名
 * （实测 SKU-D003-01/02 同名「Type-C 数据线」），code 为唯一键；两值字段随 metric
 * 二选一作 valueField（均后端直出）。
 */
function buildSkuTopData(rows: InventorySkuTopRow[]) {
  return rows.map((row) => ({
    sku_code: row.sku_code,
    total_qty: row.total_qty,
    stock_value: row.stock_value,
  }))
}

/**
 * 库存分析（frontend.md §10.1 口径：库存金额 / 周转率 / 周转天数 / ABC 分析 / 库存趋势）。
 *
 * 数据全部来自真实端点（internal/reports/routes.go，权限均挂 inventory:inventory:list）：
 * - GET /api/inventory/analytics（routes.go:67，2026-10-05 已交付）：指标条 + ABC + 趋势
 * - GET /api/inventory/summary（routes.go:64）：库存状态构成 Donut
 * - GET /api/reports/dashboard/warehouse-stock（routes.go:60）：仓库库存排行横向 Bar
 * - GET /api/inventory/sku-top（routes.go:88，2026-10-05 分析卡片轮已交付）：
 *   SKU 库存 TOP10 横向 Bar（metric=qty|value 由卡右上 Segmented 切换排序口径，
 *   api/analytics.ts inventorySkuTop）——销 §39 旧挂账「inventory-summary 无排序参数」项
 * - GET /api/inventory/turnover-trend（routes.go:89，同轮已交付）：库存周转趋势 Line
 *   （days 与页面时间档同窗，日粒度连续序列后端补零，api/analytics.ts inventoryTurnoverTrend）
 *   ——销 §39 旧挂账「inventory-turnover 非时序」项
 */
export default function AnalyticsPage() {
  const [range, setRange] = useState(DEFAULT_RANGE)
  const [skuMetric, setSkuMetric] = useState<SkuTopMetric>('qty')
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
  // SKU TOP10（metric 切换排序口径；limit=10 与 topN 一致，后端 1–50 白名单）
  const skuTop = useQuery({
    queryKey: ['inventory', 'sku-top', skuMetric],
    queryFn: () => analyticsApi.inventorySkuTop({ metric: skuMetric, limit: 10 }),
  })
  // 库存周转趋势（与页面时间档同窗——库存趋势卡 Segmented 驱动，窗口在卡副标题披露）
  const turnoverTrend = useQuery({
    queryKey: ['inventory', 'turnover-trend', range],
    queryFn: () => analyticsApi.inventoryTurnoverTrend({ days: Number(range) }),
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
          {/* SKU TOP10（§35 排行榜横向 Bar）：GET /api/inventory/sku-top 后端排序聚合直出，
              metric Segmented 切换 qty（现存量）/ value（库存金额，成本价口径——与
              inventory-summary 同源披露）；类目取 sku_code 唯一键（见 buildSkuTopData 注） */}
          <Col xs={24} lg={12}>
            <SfChartCard
              title="SKU 库存 TOP 10"
              subtitle={skuMetric === 'qty' ? '按现存量排行（在库 SKU）' : '按库存金额排行（成本价口径）'}
              extra={
                <Flex gap={8} align="center">
                  <Segmented
                    size="small"
                    options={SKU_METRIC_OPTIONS}
                    value={skuMetric}
                    onChange={(v) => setSkuMetric(v as SkuTopMetric)}
                  />
                  <RefreshButton onClick={() => void skuTop.refetch()} />
                </Flex>
              }
            >
              <SfHBarChart
                data={buildSkuTopData(skuTop.data ?? [])}
                categoryField="sku_code"
                valueField={skuMetric === 'qty' ? 'total_qty' : 'stock_value'}
                topN={10}
                height={CHART_HEIGHT}
                loading={skuTop.isPending}
                error={skuTop.error}
                onRetry={() => void skuTop.refetch()}
                emptyText="当前没有在库 SKU 库存数据"
              />
            </SfChartCard>
          </Col>
          {/* 库存周转趋势（§32 折线图）：GET /api/inventory/turnover-trend 日粒度连续序列，
              turnover_rate = 当日出库量 / 平均库存（avg≤0 后端记 0，不造假分母）；
              与页面时间档同窗（库存趋势卡 Segmented 驱动），窗口在副标题披露 */}
          <Col xs={24} lg={12}>
            <SfChartCard
              title="库存周转趋势"
              subtitle={`按日周转率 = 当日出库量 / 平均库存 · 近 ${range} 天`}
              extra={<RefreshButton onClick={() => void turnoverTrend.refetch()} />}
            >
              <SfLineChart
                data={(turnoverTrend.data ?? []).map((row) => ({
                  date: row.date,
                  turnover_rate: row.turnover_rate,
                }))}
                xField="date"
                series={[{ key: 'turnover_rate', name: '周转率' }]}
                height={CHART_HEIGHT}
                loading={turnoverTrend.isPending}
                error={turnoverTrend.error}
                onRetry={() => void turnoverTrend.refetch()}
                emptyText="所选时间范围内暂无库存周转数据"
              />
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
