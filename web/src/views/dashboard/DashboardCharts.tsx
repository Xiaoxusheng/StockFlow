/**
 * Dashboard 图表块（任务书 §27 第二/三层）——只消费 @/components/charts 业务组件，
 * 禁止业务页直接 import '@ant-design/plots' / init / resize / theme / tooltip /
 * animation 配置（componentPlan 红线，主题与三态由图表内核统一注入）。
 *
 * 数据全部来自真实端点：
 * - 库存趋势   GET /api/inventory/analytics（trend[{date,total_qty,stock_value}]）
 * - 库存状态   GET /api/inventory/summary（六状态口径的聚合面：available/locked/frozen
 *              + 余量「待检/残次」= total − available − locked − frozen，由库存恒等式
 *              total = available+locked+frozen+pending_inspect+defective 推得，非前端拼装）
 * - 出入库趋势 GET /api/reports/dashboard/trend（{date,inbound,outbound,stockQty}）
 * - 仓库排行   GET /api/reports/dashboard/warehouse-stock（每仓 total_qty）
 * - 库位利用率 GET /api/reports/dashboard/warehouse-stock（bin_utilization 0-100）
 */
import { Flex, Progress, Typography } from 'antd'
import type { AnalyticsTrendPoint, StockSummary } from '@/api/inventory'
import type { DashboardWarehouseStock, TrendPoint } from '@/api/dashboard'
import { formatNumber, formatPercent, formatQty } from '@/utils/format'
import {
  SfAreaChart,
  SfBarChart,
  SfDonutChart,
  SfHBarChart,
  type SfDonutDatum,
} from '@/components/charts'

const { Text } = Typography

/** 图表统一高度（紧凑卡片适配，任务书 §45） */
export const DASHBOARD_CHART_HEIGHT = 280

/** 库存趋势（§27 库存趋势 Line/Area）：数量 / 金额双口径由页面 Segmented 切换，均为真实字段 */
export type StockTrendMetric = 'qty' | 'value'

export function StockTrendChart({
  points,
  metric,
  loading,
  error,
  onRetry,
}: {
  points: AnalyticsTrendPoint[]
  metric: StockTrendMetric
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}) {
  const series =
    metric === 'qty'
      ? [{ key: 'total_qty', name: '现存数量' }]
      : [{ key: 'stock_value', name: '库存金额' }]
  return (
    <SfAreaChart
      data={points.map((p) => ({ date: p.date, total_qty: p.total_qty, stock_value: p.stock_value }))}
      xField="date"
      series={series}
      height={DASHBOARD_CHART_HEIGHT}
      loading={loading}
      error={error}
      onRetry={onRetry}
    />
  )
}

/**
 * 库存状态 Donut（§27 / §36）。分片互斥：可用 / 锁定 / 冻结为后端直出字段，
 * 「待检/残次」为恒等式余量（含 pending_inspect 与 defective），误差阈值外才出片。
 */
const RESIDUAL_EPSILON = 1e-6

export function buildStockStatusData(summary: StockSummary): SfDonutDatum[] {
  const residual =
    summary.total_qty - summary.available_qty - summary.locked_qty - summary.frozen_qty
  const data: SfDonutDatum[] = [
    { name: '可用', value: summary.available_qty },
    { name: '锁定', value: summary.locked_qty },
    { name: '冻结', value: summary.frozen_qty },
  ]
  if (residual > RESIDUAL_EPSILON) {
    data.push({ name: '待检/残次', value: residual })
  }
  return data
}

export function StockStatusDonut({
  summary,
  loading,
  error,
  onRetry,
}: {
  summary?: StockSummary
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}) {
  return (
    <Flex vertical gap={8}>
      <SfDonutChart
        data={summary ? buildStockStatusData(summary) : []}
        height={DASHBOARD_CHART_HEIGHT}
        loading={loading}
        error={error}
        onRetry={onRetry}
        emptyText="当前没有库存数据"
      />
      {/* 卡脚真实指标：临期（含已过期）与异常（冻结+残次）——summary 直出字段 */}
      <Flex justify="space-between">
        <Text type="secondary" style={{ fontSize: 12 }}>
          临期库存：<span className="sf-num">{summary ? formatQty(summary.near_expiry_qty) : '-'}</span>
        </Text>
        <Text type="secondary" style={{ fontSize: 12 }}>
          异常库存：<span className="sf-num">{summary ? formatQty(summary.abnormal_qty) : '-'}</span>
        </Text>
      </Flex>
    </Flex>
  )
}

/**
 * 出入库趋势（§27 出入库趋势）：入库 / 出库按日分组柱（多序列 group）。
 * 注：§27 草图为「Bar + Line（库存量）」复合图，components/charts 现无复合图组件、
 * 业务页禁止直引 plots 自组复合图——库存量趋势已由本页「库存趋势」Area 承载，此处为双序列 Bar。
 */
export function FlowTrendChart({
  points,
  loading,
  error,
  onRetry,
}: {
  points: TrendPoint[]
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}) {
  return (
    <SfBarChart
      data={points.map((p) => ({ date: p.date, inbound: p.inbound, outbound: p.outbound }))}
      xField="date"
      yField="inbound"
      series={[
        { key: 'inbound', name: '入库' },
        { key: 'outbound', name: '出库' },
      ]}
      height={DASHBOARD_CHART_HEIGHT}
      loading={loading}
      error={error}
      onRetry={onRetry}
    />
  )
}

/**
 * 仓库库存排行（§35 排行榜首选横向 Bar；数据 warehouse-stock 每仓现存量）。
 * 注：任务书 §27 草图该槽位为「SKU TOP 10」——后端无 SKU 维度排行聚合端点
 * （逐域 grep 全部 RegisterRoutes 核实；/api/reports/inventory-summary 为分页明细、
 * 无排序参数，跨页取 TOP 属前端拼装，§54 禁止），故此槽位以真实仓库排行呈现。
 */
export function WarehouseRankChart({
  items,
  loading,
  error,
  onRetry,
}: {
  items: DashboardWarehouseStock[]
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}) {
  return (
    <SfHBarChart
      data={items.map((w) => ({ name: w.warehouse_name, qty: w.total_qty }))}
      categoryField="name"
      valueField="qty"
      topN={10}
      height={DASHBOARD_CHART_HEIGHT}
      loading={loading}
      error={error}
      onRetry={onRetry}
      emptyText="当前没有仓库库存数据"
    />
  )
}

/** 库位利用率（§37 利用率只用 Gauge/Progress；仓库多时列表比单 Gauge 信息密度高） */
export function BinUtilizationList({ items }: { items: DashboardWarehouseStock[] }) {
  if (items.length === 0) {
    return <Text type="secondary">暂无仓库数据</Text>
  }
  return (
    <Flex vertical gap={12}>
      {items.map((w) => (
        <Flex key={w.warehouse_code} align="center" gap={12}>
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
