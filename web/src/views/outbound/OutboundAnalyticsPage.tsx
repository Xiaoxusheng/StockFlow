import { useMemo, useState } from 'react'
import { Button, Col, DatePicker, Flex, Progress, Row, Segmented, Skeleton, theme, Typography } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { analyticsApi } from '@/api/analytics'
import { reportsApi, type FlowStatsPage, type FlowValuationBasis } from '@/api/reports'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfChartCard, SfHBarChart, SfLineChart } from '@/components/charts'
import { formatNumber } from '@/utils/format'

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

/** 完成率环形（antd Progress dashboard 型）：品牌色，达 100% 转成功色（Token 随主题） */
function CompletionRing({ rate }: { rate: number }) {
  const { token } = theme.useToken()
  const done = rate >= 100
  return (
    <Progress
      type="dashboard"
      percent={rate}
      size={160}
      strokeColor={done ? token.colorSuccess : token.colorPrimary}
      format={(p) => `${p ?? 0}%`}
    />
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
 * 三图数据裁决（聚合批 2026-10-05 交付后原两处 EMPTY 槽位点亮，统一经 api/analytics.ts 消费）：
 * - 出库趋势   GET /api/reports/outbound-stats（真端点，internal/reports/routes.go:48，
 *              权限 reports:report:read）：按日 qty / amount 双系列折线；金额为流水估值
 *              （Σ|qty_change| × 估值单价），口径以后端响应 valuation.basis 为准并在卡脚披露。
 * - 订单完成率 仪表盘 · GET /api/outbounds/completion-rate（workbench.go，reports:report:read，
 *              与趋势端点同权限面）：比率与口径切片均由后端计算（分母=非取消出库单、
 *              分子=SHIPPED_ALL+CLOSED 差额关闭视为完成），前端只呈现不拼算；
 *              窗口无出库单（order_total=0）走真实空态，不画 0%（§52）。
 * - 商品出库 TOP10 横向条形图 · GET /api/outbounds/product-rank（workbench.go，reports:report:read）：
 *              OUTBOUND 流水按 SKU 聚合（含采购退货出库等一切出库扣减，与 outbound-stats
 *              同口径可对账），后端按出库量降序 limit=10；金额估值口径随响应 valuation.basis
 *              卡脚披露（docs/api.md §9 收口披露节②）。
 *
 * 路由 /outbound/analytics、菜单（仓储中心组，permission=reports:report:view）由收口统一登记
 * （sharedChanges）；数据端点与菜单可见性同源（reports:report:list/read 任一动作即放行，
 * types/permission.ts:68-82 matchBackendPermission），菜单可见 ⟺ 数据可达。
 */
export default function OutboundAnalyticsPage() {
  const [range, setRange] = useState<RangeKey>('30d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)
  // 联动批次二 L14：SKU 出库排行点击 → /inventory/ledger?sku_id=&change_type=OUTBOUND&同范围
  // （frontend.md §33.4；ledger 白名单 sku_id/change_type/created_from/created_to——口径与卡脚一致）
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canDrillLedger = canAccess(user, 'inventory:stock:view')
  // 估值口径（outbound-stats 响应 valuation.basis 随查询回写——trend queryFn :119；
  // 未响应前置 null，:207 卡脚走「金额估值口径随响应披露」兜底文案）
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

  // GET /api/outbounds/completion-rate（reports:report:read）：免分页直出
  // completionRateDTO（workbench.go:78-87），完成率 0~100
  const completion = useQuery({
    queryKey: ['outbound', 'analytics', 'completion-rate', timeFrom, timeTo],
    queryFn: () => analyticsApi.outboundCompletionRate({ time_from: timeFrom, time_to: timeTo }),
    enabled: ready,
  })

  // GET /api/outbounds/product-rank（reports:report:read）：免分页直出
  // outboundProductRankDTO（workbench.go:100-103，valuation.basis 随响应披露），limit 1-50
  const productRank = useQuery({
    queryKey: ['outbound', 'analytics', 'product-rank', timeFrom, timeTo],
    queryFn: () => analyticsApi.outboundProductRank({ time_from: timeFrom, time_to: timeTo, limit: 10 }),
    enabled: ready,
  })

  const trendData = useMemo(() => buildTrendData(trend.data), [trend.data])

  // 商品排行 → 横向条形：行对象重映射为图表入参（后端已按 outbound_qty 降序，
  // SfHBarChart 内置降序仅作展示兜底）
  const productRankData = useMemo(
    () =>
      (productRank.data?.items ?? []).map((row) => ({
        name: row.sku_name || row.sku_code || `SKU#${row.sku_id}`,
        qty: row.outbound_qty,
      })),
    [productRank.data],
  )

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库分析"
        subtitle="出库趋势 / 商品排行 / 订单完成率"
      />
      <Flex vertical gap={16}>
        {/* L1 出库趋势（§41 Line）+ 订单完成率（§41 Gauge） */}
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
              subtitle="分母 = 非取消出库单 · 分子 = 全部发货 + 差额关闭"
            >
              {/*
                完成率 0~100 由后端计算（completionRateDTO），前端不拼算；
                呈现用 antd Progress dashboard 环形（品牌色/完成即成功色）——取代
                针式仪表盘（§37：比率呈现 Progress 优先，红橙绿分段表盘视觉重）。
                窗口无出库单（order_total=0）不画 0% 走真实空态（§52），
                自定义档未选定范围时不发请求走提示占位。
              */}
              {!ready ? (
                <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
                  <SfEmpty description="请先选择时间范围" />
                </Flex>
              ) : completion.isPending ? (
                <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
                  <Skeleton.Node active style={{ width: 160, height: 160 }} />
                </Flex>
              ) : completion.error ? (
                <SfError error={completion.error} onRetry={() => void completion.refetch()} />
              ) : completion.data && completion.data.order_total === 0 ? (
                <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
                  <SfEmpty description="统计窗口内没有出库单" />
                </Flex>
              ) : (
                <Flex vertical align="center" justify="center" gap={12} style={{ minHeight: CHART_HEIGHT }}>
                  <CompletionRing rate={completion.data?.completion_rate ?? 0} />
                  {completion.data ? (
                    <Flex justify="space-between" wrap="wrap">
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        全部发货 {formatNumber(completion.data.shipped_all)} · 差额关闭{' '}
                        {formatNumber(completion.data.closed)} · 进行中{' '}
                        {formatNumber(completion.data.in_progress)}（取消{' '}
                        {formatNumber(completion.data.cancelled)} 张不计分母）
                      </Text>
                    </Flex>
                  ) : null}
                </Flex>
              )}
            </SfChartCard>
          </Col>
        </Row>
        {/* L2 商品出库 TOP10（§35 横向 Bar）：GET /api/outbounds/product-rank */}
        <Row gutter={[16, 16]}>
          <Col xs={24}>
            <SfChartCard
              title="商品出库 TOP10"
              subtitle="按出库量排行（OUTBOUND 流水口径）"
              extra={ready ? <RefreshButton onClick={() => void productRank.refetch()} /> : undefined}
            >
              {/*
                自定义档未选定范围时不发请求（enabled=false），走提示占位防常驻骨架屏；
                空数据由 SfHBarChart 内聚真实空态。
              */}
              {!ready ? (
                <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
                  <SfEmpty description="请先选择时间范围" />
                </Flex>
              ) : (
                <SfHBarChart
                  data={productRankData}
                  categoryField="name"
                  valueField="qty"
                  topN={10}
                  height={CHART_HEIGHT}
                  loading={productRank.isPending}
                  error={productRank.error}
                  onRetry={() => void productRank.refetch()}
                  emptyText="所选时间范围内没有出库流水落账"
                  onPointClick={
                    canDrillLedger
                      ? ({ name }) => {
                          const hit = (productRank.data?.items ?? []).find(
                            (row) => (row.sku_name || row.sku_code) === name,
                          )
                          if (!hit) return
                          navigate(
                            `/inventory/ledger?sku_id=${hit.sku_id}&change_type=OUTBOUND&created_from=${encodeURIComponent(timeFrom)}&created_to=${encodeURIComponent(timeTo)}`,
                          )
                        }
                      : undefined
                  }
                />
              )}
              {/* 卡脚口径披露（§54）：流水含一切 OUTBOUND 扣减；估值口径随响应披露（api.md §9 节②） */}
              <Flex justify="space-between" wrap="wrap">
                <Text type="secondary" style={{ fontSize: 12 }}>
                  口径：OUTBOUND 流水（含采购退货出库等一切出库扣减）；
                  {productRank.data
                    ? `金额${VALUATION_LABEL[productRank.data.valuation.basis]}（非订单实收）`
                    : '金额估值口径随响应披露'}
                </Text>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  时间范围 {ready && timeFrom ? `${timeFrom} ~ ${timeTo}` : '待选择'}（上限 366 天）
                </Text>
              </Flex>
            </SfChartCard>
          </Col>
        </Row>
      </Flex>
    </div>
  )
}
