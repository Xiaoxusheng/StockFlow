import { useMemo, useState } from 'react'
import { Button, Col, DatePicker, Flex, Row, Segmented, Typography } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { analyticsApi } from '@/api/analytics'
import type { SalesOrderStatus } from '@/api/sales'
import { SALES_ORDER_STATUS_TAG } from './salesStatusMeta'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import {
  SfChartCard,
  SfDonutChart,
  SfHBarChart,
  SfLineChart,
  type SfDonutDatum,
} from '@/components/charts'

const { Text } = Typography

/** 图表统一高度（紧凑卡片适配，与 DashboardCharts.DASHBOARD_CHART_HEIGHT 同值） */
const CHART_HEIGHT = 280

/** 时间档（后端 requireRange：time_from/time_to 缺省近 30 天、上限 366 天，
 * api/reports.ts ReportRangeQuery 同源校验） */
const RANGE_OPTIONS: Array<{ label: string; value: RangeKey }> = [
  { label: '近7天', value: '7d' },
  { label: '近30天', value: '30d' },
  { label: '近90天', value: '90d' },
  { label: '自定义', value: 'custom' },
]

type RangeKey = '7d' | '30d' | '90d' | 'custom'

/** 预设档 → [time_from, time_to]（含今天共 N 天，OutboundAnalyticsPage.presetRange 同口径） */
function presetRange(key: Exclude<RangeKey, 'custom'>): [string, string] {
  const days = key === '7d' ? 7 : key === '30d' ? 30 : 90
  return [
    dayjs().subtract(days - 1, 'day').format('YYYY-MM-DD'),
    dayjs().format('YYYY-MM-DD'),
  ]
}

/** 商品排行维度（真实端点参数 sort：qty=Σ 明细下单量 / amount=Σ 明细行金额） */
const SORT_OPTIONS: Array<{ label: string; value: RankSort }> = [
  { label: '按销量', value: 'qty' },
  { label: '按金额', value: 'amount' },
]

type RankSort = 'qty' | 'amount'

/** 销售订单状态 → 图例中文名（域内唯一来源 salesStatusMeta.SALES_ORDER_STATUS_TAG，
 * internal/sales/models.go:240-248 八态；未注册值兜底展示原始文案，不阻塞页面） */
function salesStatusName(status: string): string {
  return SALES_ORDER_STATUS_TAG[status as SalesOrderStatus]?.label ?? status
}

/** 销售趋势双序列：订单数 + 订单金额（InboundAnalyticsPage.TREND_SERIES 同款范式） */
const TREND_SERIES = [
  { key: 'order_count', name: '订单数' },
  { key: 'amount', name: '订单金额' },
]

/** 卡片右上刷新（Dashboard RefreshButton 同款：§45 图表卡 extra 槽位） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} aria-label="刷新" />
  )
}

/** 趋势折线数据：按日 date 升序（SfLineChart 按数据顺序绘制） */
function buildTrendData(page?: { items: Array<{ date: string; order_count: number; amount: number }> }) {
  const rows = page?.items ?? []
  return [...rows]
    .sort((a, b) => a.date.localeCompare(b.date))
    .map((row) => ({ date: row.date, order_count: row.order_count, amount: row.amount }))
}

/**
 * 销售分析（三图，任务书销售域蓝图）：销售趋势 / 商品销售排行 / 销售单状态构成。
 *
 * 数据契约（本域不建专属 api 文件，只消费收口统一接线文件 api/analytics.ts——
 * InboundAnalyticsPage.tsx:44 先例，AGENTS.md 规则 6；端点均于 2026-10-05 以 dev_viewer
 * 实测通过，字段与 internal/reports/workbench.go JSON tag 逐字段对齐）：
 * - 销售趋势       折线图 · GET /api/sales/analytics/trend（analyticsApi.salesTrend，
 *                  sales:sales:list）：{metric:'order_amount', items:[{date,order_count,amount}]}；
 *                  订单金额口径 Σ total_amount（不含 DRAFT/REJECTED/CANCELLED），≠ 出入库
 *                  流水估值——卡脚如实披露（api.md §9 收口披露节③）。
 * - 商品销售排行    横向条形图 · GET /api/sales/product-rank（analyticsApi.salesProductRank，
 *                  sales:sales:list）：行 {sku_name, order_count, qty, amount}；sort 参数
 *                  （qty/amount）真实下发，前端 TOP10 展示；类目拼 SKU 编码保证唯一
 *                  （实测同名不同码 SKU 并存，如「智能温控器 STC-2000」三码）。
 * - 销售单状态构成  环形图 · GET /api/sales/status-composition（analyticsApi.salesStatusComposition，
 *                  sales:sales:list）：{total, items:[{status,count}]}，items 仅含 count>0 态；
 *                  全量现状分布、不限时间范围，总数直接消费后端 total 字段（前端不求和）。
 *
 * 时间档共享：趋势/排行两查询共用页级 range 状态（自定义档选定后 enabled 才发请求）；
 * 路由 /sales/analytics 与菜单项由收口统一接线（router/index.tsx:51-53、menu.tsx:132
 * 注释占位——菜单码 sales:sales:list 同源，菜单可见 ⟺ 数据可达）。
 */
export default function SalesAnalyticsPage() {
  const [range, setRange] = useState<RangeKey>('30d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)
  const [sort, setSort] = useState<RankSort>('qty')

  // 自定义档：RangePicker 选定后才发请求（OutboundAnalyticsPage 同款交互），
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

  // 销售趋势：免分页直出端点，time_from/time_to 真实传参（不传 page/pageSize——
  // 后端对免分页端点忽略之，analytics.ts 头注）
  const trend = useQuery({
    queryKey: ['sales', 'analytics', 'trend', timeFrom, timeTo],
    queryFn: () => analyticsApi.salesTrend({ time_from: timeFrom, time_to: timeTo }),
    enabled: ready,
  })

  // 商品销售排行：sort 真实下发（后端缺省 qty）、TOP10（limit 校验 1–50）
  const rank = useQuery({
    queryKey: ['sales', 'analytics', 'product-rank', timeFrom, timeTo, sort],
    queryFn: () =>
      analyticsApi.salesProductRank({ time_from: timeFrom, time_to: timeTo, limit: 10, sort }),
    enabled: ready,
  })

  // 状态构成：无参数全量现状
  const composition = useQuery({
    queryKey: ['sales', 'analytics', 'status-composition'],
    queryFn: () => analyticsApi.salesStatusComposition(),
  })

  const trendData = useMemo(() => buildTrendData(trend.data), [trend.data])

  // 排行类目：名称（编码）——后端按 sort 维度降序返回，仅展序不改值；
  // 同名不同码 SKU 以编码保证类目唯一（SfHBarChart yField 同名分档会合并）
  const rankData = useMemo(
    () =>
      (rank.data ?? []).map((row) => ({
        name: `${row.sku_name}（${row.sku_code}）`,
        qty: row.qty,
        amount: row.amount,
      })),
    [rank.data],
  )

  // 环形数据：状态码 → 图例中文名；count 为后端原值（超 6 片由 SfDonutChart 折叠「其他」）
  const donutData = useMemo<SfDonutDatum[]>(
    () =>
      (composition.data?.items ?? []).map((item) => ({
        name: salesStatusName(item.status),
        value: item.count,
      })),
    [composition.data],
  )

  const rangeText = ready && timeFrom ? `${timeFrom} ~ ${timeTo}` : '待选择'

  return (
    <div className="sf-page">
      <SfPageHeader title="销售分析" subtitle="销售趋势 / 商品销售排行 / 销售单状态构成" />

      <Row gutter={[16, 16]}>
        {/* L1 销售趋势（折线图，三态 Loading/Empty/Error 由 SfLineChart 内聚处理） */}
        <Col xs={24} lg={16}>
          <SfChartCard
            title="销售趋势"
            subtitle="按日订单数 / 订单金额双序列"
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
              series={TREND_SERIES}
              height={CHART_HEIGHT}
              loading={trend.isPending}
              error={trend.error}
              onRetry={() => void trend.refetch()}
              emptyText="所选时间范围内没有销售订单落账"
            />
            {/* 卡脚口径披露（§54）：订单金额口径 ≠ 流水估值，如实标注 */}
            <Flex justify="space-between" wrap="wrap">
              <Text type="secondary" style={{ fontSize: 12 }}>
                口径：订单金额 Σ total_amount（草稿/已驳回/已取消不计入），非出入库流水估值
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                时间范围 {rangeText}（上限 366 天）
              </Text>
            </Flex>
          </SfChartCard>
        </Col>

        {/* L1 销售单状态构成（环形图，全量现状、不限时间范围） */}
        <Col xs={24} lg={8}>
          <SfChartCard
            title="销售单状态构成"
            subtitle="按销售订单状态分布（全量现状，不限时间范围）"
            extra={<RefreshButton onClick={() => void composition.refetch()} />}
          >
            <SfDonutChart
              data={donutData}
              height={CHART_HEIGHT}
              loading={composition.isPending}
              error={composition.error}
              onRetry={() => void composition.refetch()}
              emptyText="暂无销售单，状态构成不可展示"
            />
            <Flex justify="space-between" wrap="wrap">
              {/* total 为后端 StatusComposition 原值字段（前端不求和，规则 8） */}
              <Text type="secondary" style={{ fontSize: 12 }}>
                销售单总数 {composition.data ? composition.data.total : '…'}
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                仅展示数量 &gt; 0 的状态，超 6 类折叠为「其他」
              </Text>
            </Flex>
          </SfChartCard>
        </Col>

        {/* L2 商品销售排行（横向条形图，TOP10；sort 按销量/按金额真实下发端点） */}
        <Col xs={24}>
          <SfChartCard
            title="商品销售排行"
            subtitle={sort === 'qty' ? '按下单量 TOP 10' : '按下单金额 TOP 10'}
            extra={
              <Flex gap={8} wrap="wrap" align="center">
                <Segmented
                  size="small"
                  options={SORT_OPTIONS}
                  value={sort}
                  onChange={(v) => setSort(v as RankSort)}
                />
                {ready && <RefreshButton onClick={() => void rank.refetch()} />}
              </Flex>
            }
          >
            <SfHBarChart
              data={rankData}
              categoryField="name"
              valueField={sort}
              topN={10}
              height={CHART_HEIGHT}
              loading={rank.isPending}
              error={rank.error}
              onRetry={() => void rank.refetch()}
              emptyText="所选时间范围内没有商品销售记录"
            />
            <Flex justify="space-between" wrap="wrap">
              <Text type="secondary" style={{ fontSize: 12 }}>
                口径：下单口径（qty=Σ 明细下单量 / amount=Σ 明细行金额），非出库流水估值
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                时间范围 {rangeText}（上限 366 天）
              </Text>
            </Flex>
          </SfChartCard>
        </Col>
      </Row>
    </div>
  )
}
