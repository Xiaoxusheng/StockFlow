import { useMemo, useState } from 'react'
import { Button, Col, Flex, Row, Segmented, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import dayjs from 'dayjs'
import { analyticsApi } from '@/api/analytics'
import { reportsApi } from '@/api/reports'
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

const RANGE_OPTIONS = [
  { label: '近7天', value: 7 },
  { label: '近30天', value: 30 },
  { label: '近90天', value: 90 },
]

/** 入库趋势双序列：数量 + 金额（任务书 §40；色序按主题色板顺序取色，不写死色值） */
const TREND_SERIES = [
  { key: 'qty', name: '入库数量' },
  { key: 'amount', name: '入库金额' },
]

/** 入库单七态 → 中文文案（models.go:27-33 CHECK 值域，与 InboundPage INBOUND_STATUS_TAG
 * 同口径；状态构成返回原始大写枚举，未知值兜底展示原文——后端新增状态不阻塞页面） */
const INBOUND_STATUS_LABEL: Record<string, string> = {
  DRAFT: '草稿',
  RECEIVING: '收货中',
  AWAITING_QC: '待质检',
  AWAITING_PUTAWAY: '待上架',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
  CLOSED: '已关闭',
}

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
 * 图槽位与数据来源（componentPlan 蓝图，逐条核实 internal/reports 路由注册表；
 * 聚合批 2026-10-05 交付后原 EMPTY 槽位点亮，统一经 api/analytics.ts 消费，不建本域 api 文件）：
 * - 入库趋势      折线图 · GET /api/reports/inbound-stats 真实端点（internal/reports/routes.go:47，
 *                权限 reports:report:read）：复用 api/reports.ts inboundStats（本域不建专属 api 文件，
 *                只读共享），FlowStatsPage.items.items 按日 stat_date/order_count/qty/amount 双系列；
 *                金额为估值口径 = Σ|流水量| × SKU 成本价（internal/reports/handler.go flowValuationBasis），
 *                卡副标题如实披露、非订单实付金额。
 * - 入库状态构成   环形图 · GET /api/inbounds/status-composition（workbench.go，reports:report:read，
 *                与趋势端点同权限面）：全量现状分布（无时间窗口），items 仅含 count>0 态、
 *                total=全量单据数；原始大写状态经 INBOUND_STATUS_LABEL 映射文案，未知值兜底原文。
 * - 供应商入库排行 横向条形图 · GET /api/inbounds/supplier-rank（workbench.go，reports:report:read）：
 *                窗口按入库单 created_at 与页顶时间档联动；仅 PURCHASE 来源经采购单关联供应商
 *                （OTHER 来源无供应商不入榜），后端按实收数量降序、limit=10。
 * 空态一律 SfChart 内聚真实空态 / SfEmpty（requirements.md §10 / 任务书 §52/§54），
 * 禁止随机数伪造/前端拼装（AGENTS.md 规则 8）。
 */
export default function InboundAnalyticsPage() {
  const [days, setDays] = useState(30)
  // 联动批次二 L14：图表图元点击下钻（frontend.md §33.4）
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canDrillInbound = canAccess(user, 'inbound:view')
  const canDrillPurchase = canAccess(user, 'purchase:view')

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

  // GET /api/inbounds/status-composition（reports:report:read）：全量现状分布，
  // 无时间窗口参数；免分页直出（workbench.go statusCompositionDTO）
  const composition = useQuery({
    queryKey: ['inbound-analytics', 'status-composition'],
    queryFn: () => analyticsApi.inboundStatusComposition(),
  })

  // GET /api/inbounds/supplier-rank（reports:report:read）：时间档与趋势卡共用 days；
  // 免分页端点，limit 1-50（workbench.go parseLimitQuery）
  const supplierRank = useQuery({
    queryKey: ['inbound-analytics', 'supplier-rank', range],
    queryFn: () => analyticsApi.inboundSupplierRank({ ...range, limit: 10 }),
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

  // 状态构成 → 环形图分片：原始大写状态映射中文文案（未知值兜底原文）；
  // 仅映射不改数值，count 求和/占比由图表层呈现
  const compositionData = useMemo<SfDonutDatum[]>(
    () =>
      (composition.data?.items ?? []).map((item) => ({
        name: INBOUND_STATUS_LABEL[item.status] ?? item.status,
        value: item.count,
      })),
    [composition.data],
  )

  // 供应商排行 → 横向条形：行对象重映射为图表入参（后端已按 received_qty 降序，
  // SfHBarChart 内置降序仅作展示兜底）
  const supplierRankData = useMemo(
    () =>
      (supplierRank.data ?? []).map((row) => ({
        name: row.supplier_name || row.supplier_code || `供应商#${row.supplier_id}`,
        qty: row.received_qty,
      })),
    [supplierRank.data],
  )

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

        {/* 入库状态构成（§40 环形图）：GET /api/inbounds/status-composition 全量现状分布 */}
        <Col xs={24} lg={8}>
          <SfChartCard
            title="入库状态构成"
            subtitle="按入库单状态分布（全量现状，无时间窗口）"
            extra={<RefreshButton onClick={() => void composition.refetch()} />}
          >
            <SfDonutChart
              data={compositionData}
              height={CHART_HEIGHT}
              loading={composition.isPending}
              error={composition.error}
              onRetry={() => void composition.refetch()}
              emptyText="当前没有入库单"
              onPointClick={
                canDrillInbound
                  ? ({ name }) => {
                      // 图例中文名反查原始状态码（INBOUND_STATUS_LABEL 的逆映射）
                      const status = (composition.data?.items ?? []).find(
                        (item) => (INBOUND_STATUS_LABEL[item.status] ?? item.status) === name,
                      )?.status
                      if (status) navigate(`/inbound?status=${status}`)
                    }
                  : undefined
              }
            />
            {composition.data && composition.data.total > 0 ? (
              <Flex justify="space-between" wrap="wrap">
                <Text type="secondary" style={{ fontSize: 12 }}>
                  共 {composition.data.total} 张入库单（仅展示 count&gt;0 状态）
                </Text>
              </Flex>
            ) : null}
          </SfChartCard>
        </Col>

        {/* 供应商入库排行（§40 横向条形图）：GET /api/inbounds/supplier-rank，与趋势卡共用时间档 */}
        <Col xs={24}>
          <SfChartCard
            title="供应商入库排行"
            subtitle={`按实收数量 TOP 10 · 仅采购来源入库入榜 · 近${days}天`}
            extra={<RefreshButton onClick={() => void supplierRank.refetch()} />}
          >
            <SfHBarChart
              data={supplierRankData}
              categoryField="name"
              valueField="qty"
              topN={10}
              height={CHART_HEIGHT}
              loading={supplierRank.isPending}
              error={supplierRank.error}
              onRetry={() => void supplierRank.refetch()}
              emptyText="所选时间范围内没有采购来源入库"
              onPointClick={
                canDrillPurchase
                  ? ({ name }) => {
                      // 供应商入库的来源是采购单 → 落点 /purchases?supplier_id=（入库列表无 supplier 参数）
                      const hit = (supplierRank.data ?? []).find((row) => (row.supplier_name || row.supplier_code) === name)
                      if (hit) navigate(`/purchases?supplier_id=${hit.supplier_id}`)
                    }
                  : undefined
              }
            />
            <Flex justify="space-between" wrap="wrap">
              <Text type="secondary" style={{ fontSize: 12 }}>
                口径：入库单经采购单（source_no=po_no）关联供应商，OTHER 来源不入榜；数量 = Σ 明细实收量
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                时间范围 {range.time_from} ~ {range.time_to}（上限 366 天）
              </Text>
            </Flex>
          </SfChartCard>
        </Col>
      </Row>
    </div>
  )
}
