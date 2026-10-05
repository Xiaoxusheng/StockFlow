import { useMemo, useState } from 'react'
import { Button, Col, Flex, Row, Segmented, Tooltip } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { reportsApi } from '@/api/reports'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfChartCard, SfLineChart } from '@/components/charts'

/** 图表统一高度（紧凑卡片适配，与 DashboardCharts.DASHBOARD_CHART_HEIGHT 同值） */
const CHART_HEIGHT = 280

const RANGE_OPTIONS = [
  { label: '近7天', value: 7 },
  { label: '近30天', value: 30 },
  { label: '近90天', value: 90 },
]

/** EMPTY 卡文案（componentPlan 裁决：端点未立项的槽位走真实空态，禁止伪造数据） */
const PENDING_ENDPOINT_TEXT = '接口未交付，后端立项后自动展示'

/** 入库趋势双序列：数量 + 金额（任务书 §40；色序按主题色板顺序取色，不写死色值） */
const TREND_SERIES = [
  { key: 'qty', name: '入库数量' },
  { key: 'amount', name: '入库金额' },
]

/** 卡片右上刷新（Dashboard RefreshButton 同款：§45 图表卡 extra 槽位） */
function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <Tooltip title="刷新">
      <Button size="small" type="text" icon={<ReloadOutlined />} onClick={onClick} />
    </Tooltip>
  )
}

/**
 * 入库分析（frontend.md §40：入库趋势 / 供应商入库排行 / 入库状态构成）。
 *
 * 图槽位与数据来源（componentPlan 蓝图，逐条核实 internal/reports 路由注册表）：
 * - 入库趋势      折线图 · GET /api/reports/inbound-stats 真实端点（internal/reports/routes.go:47，
 *                权限 reports:report:read）：复用 api/reports.ts inboundStats（本域不建专属 api 文件，
 *                只读共享），FlowStatsPage.items.items 按日 stat_date/order_count/qty/amount 双系列；
 *                金额为估值口径 = Σ|流水量| × SKU 成本价（internal/reports/handler.go flowValuationBasis），
 *                卡副标题如实披露、非订单实付金额。
 * - 供应商入库排行 横向条形图 · EMPTY：采购/入库域无供应商维度聚合端点（internal/purchase/purchase.go
 *                路由注册表逐条核实，仅基础资料 /api/suppliers 列表）。
 * - 入库状态构成   环形图 · EMPTY：无入库单状态占比聚合端点（后端全域无 by_status/status_count 聚合）。
 * 空态一律 SfEmpty 真实占位（requirements.md §10 / 任务书 §52/§54），禁止随机数伪造/前端拼装。
 */
export default function InboundAnalyticsPage() {
  const [days, setDays] = useState(30)

  // 统计窗口（后端 requireRange：time_from/time_to YYYY-MM-DD，上限 366 天——api/reports.ts ReportRangeQuery）
  const range = useMemo(
    () => ({
      time_from: dayjs().subtract(days - 1, 'day').format('YYYY-MM-DD'),
      time_to: dayjs().format('YYYY-MM-DD'),
    }),
    [days],
  )

  // GET /api/reports/inbound-stats（reports:report:read）：行集为按日聚合，pageSize 取天数
  // +2 余量覆盖时区边界；后端按会话仓库范围快照过滤，前端不传范围参数
  const stats = useQuery({
    queryKey: ['inbound-analytics', 'inbound-stats', range],
    queryFn: () => reportsApi.inboundStats({ ...range, page: 1, pageSize: days + 2 }),
  })

  // 按日升序（x 轴时间序；仅展示排序，不改数值）——stat_date 为 JSONTime 字符串
  // 「YYYY-MM-DD HH:mm:ss」，轴标签裁前 10 位日期作展示格式化
  const trendData = useMemo(() => {
    const rows = stats.data?.items?.items ?? []
    return [...rows]
      .sort((a, b) => String(a.stat_date).localeCompare(String(b.stat_date)))
      .map((row) => ({
        date: String(row.stat_date).slice(0, 10),
        qty: row.qty,
        amount: row.amount,
      }))
  }, [stats.data])

  return (
    <div className="sf-page">
      <SfPageHeader title="入库分析" subtitle="入库趋势 / 供应商入库排行 / 入库状态构成" />

      <Row gutter={[16, 16]}>
        {/* 入库趋势（§40 折线图）：真实端点，Loading/Empty/Error 三态由 SfLineChart 内聚处理 */}
        <Col xs={24} lg={16}>
          <SfChartCard
            title="入库趋势"
            subtitle="按日数量 / 金额双序列 · 金额 = Σ 流水量 × SKU 成本价（估值口径，非订单实付金额）"
            extra={
              <Flex gap={8} wrap="wrap" align="center">
                <Segmented
                  size="small"
                  options={RANGE_OPTIONS}
                  value={days}
                  onChange={(v) => setDays(v as number)}
                />
                <RefreshButton onClick={() => void stats.refetch()} />
              </Flex>
            }
          >
            <SfLineChart
              data={trendData}
              xField="date"
              series={TREND_SERIES}
              height={CHART_HEIGHT}
              loading={stats.isPending}
              error={stats.error}
              onRetry={() => void stats.refetch()}
            />
          </SfChartCard>
        </Col>

        {/* 入库状态构成（§40 环形图）：状态占比聚合端点未立项，真实空态占位 */}
        <Col xs={24} lg={8}>
          <SfChartCard title="入库状态构成" subtitle="按入库单状态分布">
            <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
              <SfEmpty description={PENDING_ENDPOINT_TEXT} />
            </Flex>
          </SfChartCard>
        </Col>

        {/* 供应商入库排行（§40 横向条形图）：供应商维度聚合端点未立项，真实空态占位 */}
        <Col xs={24}>
          <SfChartCard title="供应商入库排行" subtitle="按入库数量 TOP 10（横向条形）">
            <Flex align="center" justify="center" style={{ height: CHART_HEIGHT }}>
              <SfEmpty description={PENDING_ENDPOINT_TEXT} />
            </Flex>
          </SfChartCard>
        </Col>
      </Row>
    </div>
  )
}
