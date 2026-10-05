import { useMemo, useState } from 'react'
import { Button, Col, DatePicker, Flex, Row, Segmented, Typography } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { reportsApi, type FlowStatsPage, type FlowValuationBasis } from '@/api/reports'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfChartCard, SfLineChart } from '@/components/charts'

const { Text } = Typography

/** 图表统一高度（任务书 §45 紧凑卡片适配，DashboardCharts.DASHBOARD_CHART_HEIGHT 同值） */
const CHART_HEIGHT = 280

/** 时间档（后端 requireRange：time_from/time_to 缺省近 30 天、上限 366 天，handler.go:40-74） */
const RANGE_OPTIONS: Array<{ label: string; value: RangeKey }> = [
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '近90天', value: '90d' },
  { label: '自定义', value: 'custom' },
]

type RangeKey = '7d' | '30d' | '90d' | 'custom'

/** 预设档 → [time_from, time_to]（含今天共 N 天，Dashboard sparkWindow 同口径） */
function presetRange(key: Exclude<RangeKey, 'custom'>): [string, string] {
  const days = key === '7d' ? 7 : key === '30d' ? 30 : 90
  return [
    dayjs().subtract(days - 1, 'day').format('YYYY-MM-DD'),
    dayjs().format('YYYY-MM-DD'),
  ]
}

/** 金额估值口径（后端 valuation.basis 下发，handler.go:147-152；ReportFlowStats 同表） */
const VALUATION_LABEL: Record<FlowValuationBasis, string> = {
  cost_price: '按 SKU 成本价估值',
  sale_price: '按 SKU 销售价估值',
}

/** 卡片右上刷新（Dashboard RefreshButton 同款，任务书 §45 图表卡 extra） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} aria-label="刷新" />
  )
}

/** 出库趋势折线数据：按日 stat_date 升序（SfLineChart 按数据顺序绘制，flowSparkSeries 同口径） */
function buildTrendData(page?: FlowStatsPage): Array<{ date: string; qty: number; amount: number }> {
  const rows = page?.items?.items ?? []
  return [...rows]
    .sort((a, b) => String(a.stat_date).localeCompare(String(b.stat_date)))
    .map((row) => ({ date: String(row.stat_date), qty: row.qty, amount: row.amount }))
}

/**
 * 出库分析（任务书 §41：出库趋势 / 商品出库 TOP10 / 订单完成率）——与 Dashboard 范式同构，
 * 只消费 @/components/charts 业务组件，业务页零 echarts 初始化、零图表色值字面量。
 *
 * 三图数据裁决：
 * - 出库趋势   GET /api/reports/outbound-stats（真端点，internal/reports/routes.go:48，
 *              权限 reports:report:read）：按日 qty / amount 双系列折线；金额为流水估值
 *              （Σ|qty_change| × 估值单价），口径以后端响应 valuation.basis 为准并在卡脚披露。
 * - 商品出库 TOP10 横向条形图 EMPTY：后端无商品维度出库聚合端点（internal/sales/routes.go:83-118
 *              出库作业路由逐条核实：outbounds/allocations/picks/checks/packing/shipments
 *              仅列表/详情/动作端点；/api/reports 目录亦无 TOP 类端点）——走真实空态，
 *              禁止前端拼算 / 伪造（AGENTS.md 规则 8 / 任务书 §54）。
 * - 订单完成率 仪表盘 EMPTY：无完成率聚合端点（grep internal/ 零命中），§41 允许 Gauge/KPI
 *              但端点缺位一律 EMPTY（任务书 §54），端点交付后经 SfGaugeChart 接入。
 *
 * 路由 /outbound/analytics、菜单（仓储中心组，permission=reports:report:view）由收口统一登记
 * （sharedChanges）；数据端点与菜单可见性同源（reports:report:list/read 任一动作即放行，
 * types/permission.ts:68-82 matchBackendPermission），菜单可见 ⟺ 数据可达。
 */
export default function OutboundAnalyticsPage() {
  const [range, setRange] = useState<RangeKey>('30d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)
  // 估值口径随真实响应记录（后端 handler.go:147-152 下发，前端不假设）
  const [valuationBasis, setValuationBasis] = useState<FlowValuationBasis | null>(null)

  // 自定义档：RangePicker 选定后才发请求（Dashboard L3 同款交互），time_from/time_to 真实传参；
  // 未就绪时以空串占位（请求 enabled=false 不发出，卡脚显示「待选择」）
  const ready = range !== 'custom' || Boolean(customRange?.[0] && customRange?.[1])
  const [timeFrom, timeTo] = useMemo<[string, string]>(() => {
    if (range === 'custom') {
      if (customRange?.[0] && customRange?.[1]) {
        return [customRange[0].format('YYYY-MM-DD'), customRange[1].format('YYYY-MM-DD')]
      }
      return ['', '']
    }
    return presetRange(range)
  }, [range, customRange])

  const trend = useQuery({
    queryKey: ['outbound', 'analytics', 'outbound-stats', timeFrom, timeTo],
    queryFn: () =>
      reportsApi
        .outboundStats({ time_from: timeFrom, time_to: timeTo, page: 1, pageSize: 100 })
        // 分页信封 items 为 {items,total,valuation} 二层包装（handler.go:139-143），
        // 估值口径随响应记录；pageSize 取后端 MaxPageSize=100（response.go:47），
        // 按日聚合 7/30/90 天窗口行数 ≤ 窗口天数，一次取全无需翻页
        .then((page) => {
          setValuationBasis(page.items.valuation.basis)
          return page
        }),
    enabled: ready,
  })

  const trendData = useMemo(() => buildTrendData(trend.data), [trend.data])

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库分析"
        subtitle="出库趋势 / 商品排行 / 订单完成率"
      />
      <Flex vertical gap={16}>
        {/* L1 出库趋势（§41 Line）+ 订单完成率（§41 Gauge，端点缺位真实空态） */}
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={16}>
            <SfChartCard
              title="出库趋势"
              subtitle="按日出库量 / 出库金额（估值）"
              extra={
                <Flex gap={8} wrap="wrap" align="center">
                  <Segmented
                    size="small"
                    options={RANGE_OPTIONS}
                    value={range}
                    onChange={(v) => setRange(v as RangeKey)}
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
                  {ready && <RefreshButton onClick={() => void trend.refetch()} />}
                </Flex>
              }
            >
              <SfLineChart
                data={trendData}
                xField="date"
                series={[
                  { key: 'qty', name: '出库数量' },
                  { key: 'amount', name: '出库金额' },
                ]}
                height={CHART_HEIGHT}
                loading={trend.isPending}
                error={trend.error}
                onRetry={() => void trend.refetch()}
                emptyText="所选时间范围内没有出库流水落账"
              />
              {/* 卡脚口径披露（§54）：金额为 OUTBOUND 流水估值，口径以后端 valuation.basis 为准 */}
              <Flex justify="space-between" wrap="wrap">
                <Text type="secondary" style={{ fontSize: 12 }}>
                  口径：OUTBOUND 流水落账；
                  {valuationBasis ? `金额${VALUATION_LABEL[valuationBasis]}` : '金额估值口径随响应披露'}
                </Text>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  时间范围 {ready && timeFrom ? `${timeFrom} ~ ${timeTo}` : '待选择'}（上限 366 天）
                </Text>
              </Flex>
            </SfChartCard>
          </Col>
          <Col xs={24} lg={8}>
            <SfChartCard
              title="订单完成率"
              subtitle="出库订单完成情况（端点缺位）"
            >
              {/* §41 允许 Gauge/KPI；无完成率聚合端点，走真实空态，禁止前端拼算（§54） */}
              <SfEmpty description="后端暂无出库订单完成率聚合端点，暂无法展示；端点交付后经统一仪表盘组件接入" />
            </SfChartCard>
          </Col>
        </Row>
        {/* L2 商品出库 TOP10（§35 横向 Bar；端点缺位真实空态） */}
        <Row gutter={[16, 16]}>
          <Col xs={24}>
            <SfChartCard
              title="商品出库 TOP10"
              subtitle="按出库量排行（端点缺位）"
            >
              {/* 后端无商品维度出库聚合端点（sales 出库作业路由 + reports 目录逐条核实），
                  走真实空态；禁止取 /api/outbounds 明细在前端聚合排行（§54） */}
              <SfEmpty description="后端暂无商品维度的出库聚合端点，暂无法展示商品出库排行；端点交付后经统一横向条形图接入" />
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
