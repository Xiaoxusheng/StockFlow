import { useMemo, useState } from 'react'
import { Button, Col, DatePicker, Flex, Row, Segmented, Typography } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { analyticsApi, type OrderAmountTrendPage } from '@/api/analytics'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
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

/** 采购订单状态 → 图例中文名（值域 db/migrations/000007 chk_purchase_orders_status 七态；
 * 文案与 purchaseStatusMeta.ts PO_STATUS_TAG 逐键同值——环形图图例只取 label 不涉状态色，
 * 未注册值兜底展示原始文案，后端新增状态不阻塞页面） */
const PO_STATUS_LABELS: Record<string, string> = {
  DRAFT: '草稿',
  PENDING_APPROVAL: '待审核',
  APPROVED: '已审核',
  PARTIAL_RECEIVED: '部分到货',
  RECEIVED_ALL: '到货完成',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
}

function poStatusName(status: string): string {
  return PO_STATUS_LABELS[status] ?? status
}

/** 采购趋势双序列：订单数 + 订单金额（InboundAnalyticsPage.TREND_SERIES 同款范式） */
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
function buildTrendData(page?: OrderAmountTrendPage): Array<{ date: string; order_count: number; amount: number }> {
  const rows = page?.items ?? []
  return [...rows]
    .sort((a, b) => a.date.localeCompare(b.date))
    .map((row) => ({ date: row.date, order_count: row.order_count, amount: row.amount }))
}

/**
 * 采购分析（三图，任务书采购域蓝图）：采购趋势 / 供应商采购排行 / 采购单状态构成。
 *
 * 数据契约（本域不建专属 api 文件，只消费收口统一接线文件 api/analytics.ts——
 * InboundAnalyticsPage.tsx:44 先例，AGENTS.md 规则 6；端点均于 2026-10-05 以 dev_viewer
 * 实测通过，字段与 internal/reports/workbench.go JSON tag 逐字段对齐）：
 * - 采购趋势       折线图 · GET /api/purchases/analytics/trend（analyticsApi.purchaseTrend，
 *                  purchase:purchase:list）：{metric:'order_amount', items:[{date,order_count,amount}]}；
 *                  订单金额口径 Σ total_amount（不含 DRAFT/CANCELLED），≠ 出入库流水估值——
 *                  卡脚如实披露（api.md §9 收口披露节③）。
 * - 供应商采购排行  横向条形图 · GET /api/purchases/supplier-rank（analyticsApi.purchaseSupplierRank，
 *                  purchase:purchase:list）：行 {supplier_name, order_count, total_amount}，
 *                  后端按 total_amount DESC 排序，前端 TOP10 展示；类目拼供应商编码保证唯一。
 * - 采购单状态构成  环形图 · GET /api/purchases/status-composition（analyticsApi.purchaseStatusComposition，
 *                  purchase:purchase:list）：{total, items:[{status,count}]}，items 仅含 count>0 态；
 *                  全量现状分布、不限时间范围，总数直接消费后端 total 字段（前端不求和）。
 *
 * 时间档共享：趋势/排行两查询共用页级 range 状态（自定义档选定后 enabled 才发请求）；
 * 路由 /purchases/analytics 与菜单项由收口统一接线（router/index.tsx:51-53、menu.tsx:119-121
 * 注释占位——菜单码 purchase:purchase:list 同源，菜单可见 ⟺ 数据可达）。
 */
export default function PurchaseAnalyticsPage() {
  const [range, setRange] = useState<RangeKey>('30d')
  const [customRange, setCustomRange] = useState<[Dayjs, Dayjs] | null>(null)
  // 联动批次二 L14：供应商排行/状态构成点击下钻（frontend.md §33.4）
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canDrillPurchase = canAccess(user, 'purchase:view')

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

  // 采购趋势：免分页直出端点，time_from/time_to 真实传参（不传 page/pageSize——
  // 后端对免分页端点忽略之，analytics.ts 头注）
  const trend = useQuery({
    queryKey: ['purchase', 'analytics', 'trend', timeFrom, timeTo],
    queryFn: () => analyticsApi.purchaseTrend({ time_from: timeFrom, time_to: timeTo }),
    enabled: ready,
  })

  // 供应商采购排行：TOP10（后端 limit 校验 1–50，缺省 10）
  const rank = useQuery({
    queryKey: ['purchase', 'analytics', 'supplier-rank', timeFrom, timeTo],
    queryFn: () =>
      analyticsApi.purchaseSupplierRank({ time_from: timeFrom, time_to: timeTo, limit: 10 }),
    enabled: ready,
  })

  // 状态构成：无参数全量现状
  const composition = useQuery({
    queryKey: ['purchase', 'analytics', 'status-composition'],
    queryFn: () => analyticsApi.purchaseStatusComposition(),
  })

  const trendData = useMemo(() => buildTrendData(trend.data), [trend.data])

  // 排行类目：名称（编码）——后端按 total_amount DESC 返回，仅展序不改值；
  // 同名供应商以编码保证类目唯一（SfHBarChart yField 同名分档会合并）
  const rankData = useMemo(
    () =>
      (rank.data ?? []).map((row) => ({
        name: `${row.supplier_name}（${row.supplier_code}）`,
        total_amount: row.total_amount,
      })),
    [rank.data],
  )

  // 环形数据：状态码 → 图例中文名；count 为后端原值（超 6 片由 SfDonutChart 折叠「其他」）
  const donutData = useMemo<SfDonutDatum[]>(
    () => (composition.data?.items ?? []).map((item) => ({ name: poStatusName(item.status), value: item.count })),
    [composition.data],
  )

  const rangeText = ready && timeFrom ? `${timeFrom} ~ ${timeTo}` : '待选择'

  return (
    <div className="sf-page">
      <SfPageHeader title="采购分析" subtitle="采购趋势 / 供应商采购排行 / 采购单状态构成" />

      <Row gutter={[16, 16]}>
        {/* L1 采购趋势（折线图，三态 Loading/Empty/Error 由 SfLineChart 内聚处理） */}
        <Col xs={24} lg={16}>
          <SfChartCard
            title="采购趋势"
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
              emptyText="所选时间范围内没有采购订单落账"
            />
            {/* 卡脚口径披露（§54）：订单金额口径 ≠ 流水估值，如实标注 */}
            <Flex justify="space-between" wrap="wrap">
              <Text type="secondary" style={{ fontSize: 12 }}>
                口径：订单金额 Σ total_amount（草稿/已取消不计入），非出入库流水估值
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                时间范围 {rangeText}（上限 366 天）
              </Text>
            </Flex>
          </SfChartCard>
        </Col>

        {/* L1 采购单状态构成（环形图，全量现状、不限时间范围） */}
        <Col xs={24} lg={8}>
          <SfChartCard
            title="采购单状态构成"
            subtitle="按采购单状态分布（全量现状，不限时间范围）"
            extra={<RefreshButton onClick={() => void composition.refetch()} />}
          >
            <SfDonutChart
              data={donutData}
              height={CHART_HEIGHT}
              loading={composition.isPending}
              error={composition.error}
              onRetry={() => void composition.refetch()}
              emptyText="暂无采购单，状态构成不可展示"
              onPointClick={
                canDrillPurchase
                  ? ({ name }) => {
                      // 图例中文名反查原始状态码（poStatusName 的逆映射）
                      const status = (composition.data?.items ?? []).find(
                        (item) => poStatusName(item.status) === name,
                      )?.status
                      if (status) navigate(`/purchases?status=${status}`)
                    }
                  : undefined
              }
            />
            <Flex justify="space-between" wrap="wrap">
              {/* total 为后端 StatusComposition 原值字段（前端不求和，规则 8） */}
              <Text type="secondary" style={{ fontSize: 12 }}>
                采购单总数 {composition.data ? composition.data.total : '…'}
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                仅展示数量 &gt; 0 的状态，超 6 类折叠为「其他」
              </Text>
            </Flex>
          </SfChartCard>
        </Col>

        {/* L2 供应商采购排行（横向条形图，TOP10） */}
        <Col xs={24}>
          <SfChartCard
            title="供应商采购排行"
            subtitle="按采购金额 TOP 10"
            extra={
              ready && <RefreshButton onClick={() => void rank.refetch()} />
            }
          >
            <SfHBarChart
              data={rankData}
              categoryField="name"
              valueField="total_amount"
              topN={10}
              height={CHART_HEIGHT}
              loading={rank.isPending}
              error={rank.error}
              onRetry={() => void rank.refetch()}
              emptyText="所选时间范围内没有供应商采购记录"
              onPointClick={
                canDrillPurchase
                  ? ({ name }) => {
                      // rankData.name = `供应商名（编码）`，反查原始行取 supplier_id
                      const hit = (rank.data ?? []).find((row) => `${row.supplier_name}（${row.supplier_code}）` === name)
                      if (hit) navigate(`/purchases?supplier_id=${hit.supplier_id}`)
                    }
                  : undefined
              }
            />
            <Flex justify="space-between" wrap="wrap">
              <Text type="secondary" style={{ fontSize: 12 }}>
                口径：Σ total_amount 降序（草稿/已取消不计入），金额为下单口径非流水估值
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
