import { useState } from 'react'
import { Card, Flex, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { Dayjs } from 'dayjs'
import {
  reportsApi,
  type FlowStatRow,
  type FlowValuationBasis,
  type ReportRangeQuery,
} from '@/api/reports'
import { usePagedList } from '@/hooks/usePagedList'
import { SfTable } from '@/components/table/SfTable'
import { formatDate, formatMoney, formatNumber, formatQty } from '@/utils/format'
import { rangeToParams, ReportRangePicker } from './ReportRangePicker'

const { Text } = Typography

const COLUMNS: ColumnsType<FlowStatRow> = [
  {
    title: '统计日',
    dataIndex: 'stat_date',
    width: 120,
    fixed: 'left',
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDate(v)}</span>,
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
 */
export default function ReportFlowStats({ kind }: ReportFlowStatsProps) {
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null)
  const [valuationBasis, setValuationBasis] = useState<FlowValuationBasis | null>(null)

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
    params: rangeToParams(range),
  })

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
