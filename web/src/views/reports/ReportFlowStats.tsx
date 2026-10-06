import { useMemo, useState } from 'react'
import { Card, Flex, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { Dayjs } from 'dayjs'
import { useQuery } from '@tanstack/react-query'
import {
  reportsApi,
  type FlowStatRow,
  type FlowValuationBasis,
  type ReportRangeQuery,
} from '@/api/reports'
import { DateCell } from '@/components/table/cells'
import { usePagedList } from '@/hooks/usePagedList'
import { SfLineChart } from '@/components/charts'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfTable } from '@/components/table/SfTable'
import { formatMoney, formatNumber, formatQty } from '@/utils/format'
import { rangeToParams, ReportRangePicker } from './ReportRangePicker'

const { Text } = Typography

/** 趋势图单次拉取行数 = 后端 MaxPageSize（internal/response/response.go:47） */
const TREND_PAGE_SIZE = 100

/** 图表高度：与六域分析页 CHART_HEIGHT=280 同档（任务书 §45 紧凑卡片适配） */
const TREND_CHART_HEIGHT = 280

const COLUMNS: ColumnsType<FlowStatRow> = [
  {
    title: '统计日',
    dataIndex: 'stat_date',
    width: 120,
    fixed: 'left',
    render: (v: string) => <DateCell value={v} withTime={false} />,
  },
  { title: '单据数', dataIndex: 'order_count', width: 110, align: 'right', render: count },
  { title: '数量', dataIndex: 'qty', width: 120, align: 'right', render: qty },
  { title: '金额（估值）', dataIndex: 'amount', width: 150, align: 'right', render: amount },
]

function count(v: number) {
  return <span className="sf-num">{formatNumber(v)}</span>
}

function qty(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

function amount(v: number) {
  return <span className="sf-num">{formatMoney(v)}</span>
}

/** 金额估值口径（后端 valuation.basis 下发，handler.go:147-152） */
const VALUATION_LABEL: Record<FlowValuationBasis, string> = {
  cost_price: '按 SKU 成本价估值',
  sale_price: '按 SKU 销售价估值',
}

interface ReportFlowStatsProps {
  kind: 'inbound' | 'outbound'
}

/**
 * 出入库统计报表（reports:report:read）：
 * - inbound → GET /api/reports/inbound-stats（INBOUND 流水落账，金额按成本价估值）
 * - outbound → GET /api/reports/outbound-stats（OUTBOUND 流水落账，金额按销售价估值）
 * 按日聚合（FlowStatRow，repository.go:115-123）；时间范围 time_from/time_to（上限 366 天），
 * 估值口径以响应 valuation.basis 为准（现场聚合无订单金额列，后端口径显式披露）。
 * 图表叠加（任务书 §63 P2）：同一端点的按日行集叠 SfLineChart 趋势（数量/金额双系列），
 * 表格保留分页明细；行集 > 单页上限时趋势图如实降级为说明空态，不画残缺折线（§54）。
 */
export default function ReportFlowStats({ kind }: ReportFlowStatsProps) {
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null)
  const [valuationBasis, setValuationBasis] = useState<FlowValuationBasis | null>(null)

  const rangeParams = rangeToParams(range)

  const list = usePagedList<FlowStatRow, ReportRangeQuery>({
    queryKey: ['reports', kind === 'inbound' ? 'inbound-stats' : 'outbound-stats'],
    fetch: (q) => {
      const request = kind === 'inbound' ? reportsApi.inboundStats(q) : reportsApi.outboundStats(q)
      // 出入库统计的分页信封 items 为 {items,total,valuation} 二层包装（handler.go:139-143），
      // 估值口径随真实响应记录，适配 usePagedList 的 PageResult 形状
      return request.then((page) => {
        setValuationBasis(page.items.valuation.basis)
        return { ...page, items: page.items.items }
      })
    },
    params: rangeParams,
  })

  // 趋势数据与表格同端点同范围：按日行集一次取满（行数 ≤ 范围天数，30/90 天窗口
  // 单页即全量）；range 缺省走后端近 30 天缺省口径（rangeToParams 返回空对象）
  const trend = useQuery({
    queryKey: ['reports', kind, 'stats-trend', rangeParams.time_from ?? '', rangeParams.time_to ?? ''],
    queryFn: () => {
      const query = { ...rangeParams, page: 1, pageSize: TREND_PAGE_SIZE }
      return kind === 'inbound' ? reportsApi.inboundStats(query) : reportsApi.outboundStats(query)
    },
  })

  const trendRows = useMemo<FlowStatRow[]>(() => trend.data?.items.items ?? [], [trend.data])
  const trendTotal = trend.data?.items.total ?? 0
  /** 行集超出单页上限 → 折线只是残缺窗口，如实降级说明（§54 不画残缺数据） */
  const trendTruncated = trendTotal > trendRows.length
  const trendData = useMemo(
    () =>
      [...trendRows]
        .sort((a, b) => String(a.stat_date).localeCompare(String(b.stat_date)))
        .map((row) => ({ date: String(row.stat_date), 数量: row.qty, 金额: row.amount })),
    [trendRows],
  )

  const handleRangeChange = (value: [Dayjs, Dayjs] | null) => {
    setRange(value)
    list.resetToFirstPage()
  }

  return (
    <Card
      size="small"
      title={kind === 'inbound' ? '入库统计' : '出库统计'}
      extra={
        <Flex gap={8} align="center">
          <ReportRangePicker value={range} onChange={handleRangeChange} />
        </Flex>
      }
    >
      <Flex vertical gap={8}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          口径：{kind === 'inbound' ? 'INBOUND' : 'OUTBOUND'} 流水落账；
          {valuationBasis ? `金额${VALUATION_LABEL[valuationBasis]}` : '金额估值口径随首屏响应披露'}
          ；时间范围缺省近 30 天，上限 366 天
        </Text>
        {/* 按日趋势折线（§32/§63 P2）：与表格同端点同范围，Loading/Empty/Error 三态内聚；
            行集超过单页上限时不画残缺折线，降级为说明空态（§54） */}
        {trendTruncated ? (
          <Flex align="center" justify="center" style={{ height: TREND_CHART_HEIGHT }}>
            <SfEmpty
              description={`所选范围含 ${formatNumber(trendTotal)} 个统计日，超过单次拉取上限 ${TREND_PAGE_SIZE}，趋势图无法完整覆盖；请缩小时间范围后查看趋势`}
            />
          </Flex>
        ) : (
          <SfLineChart
            data={trendData}
            xField="date"
            series={[
              { key: '数量', name: '数量' },
              { key: '金额', name: '金额（估值）', color: 'secondary' },
            ]}
            height={TREND_CHART_HEIGHT}
            loading={trend.isPending}
            error={trend.error}
            onRetry={() => void trend.refetch()}
            emptyText={
              range
                ? '所选时间范围内没有出入库流水落账'
                : '近 30 天没有出入库流水落账，可选择其他时间范围'
            }
          />
        )}
        <SfTable<FlowStatRow>
          storageKey={`report-${kind}-stats`}
          rowKey={(record) => record.stat_date}
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText={
            range
              ? '所选时间范围内没有出入库流水落账'
              : '近 30 天没有出入库流水落账，可选择其他时间范围'
          }
          scrollX={520}
        />
      </Flex>
    </Card>
  )
}
