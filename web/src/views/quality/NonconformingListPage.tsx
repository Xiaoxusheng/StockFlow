import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DESTINATION_LABEL,
  DISPOSITION_TAG_FALLBACK,
  qualityApi,
  type NonconformingDestination,
  type NonconformingItem,
  type NonconformingQuery,
  type QualityDisposition,
} from '@/api/quality'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const DISPOSITION_OPTIONS: Array<{ label: string; value: QualityDisposition }> = [
  { label: '退供应商', value: 'return_supplier' },
  { label: '报废', value: 'scrap' },
  { label: '返工', value: 'rework' },
  { label: '降级', value: 'downgrade' },
  { label: '转不良品仓', value: 'to_defective_warehouse' },
  { label: '特批放行', value: 'special_release' },
]

const DESTINATION_OPTIONS: Array<{ label: string; value: NonconformingDestination }> = [
  { label: '退供应商', value: 'supplier' },
  { label: '报废区', value: 'scrap_area' },
  { label: '返工区', value: 'rework_area' },
  { label: '降级库位', value: 'downgrade_bin' },
  { label: '不良品仓', value: 'defective_warehouse' },
  { label: '放行', value: 'released' },
]

/**
 * 处理结果 → SfStatusTag：六种处置值均不在 types/status.ts 注册表，
 * 走 DISPOSITION_TAG_FALLBACK 的 label/semantic 兜底；
 * 后端返回未知值时兜底中性灰 + 原始文案，不崩溃。
 */
function DispositionTag({ value }: { value?: QualityDisposition }) {
  if (!value) return '-'
  const fallback = DISPOSITION_TAG_FALLBACK[value]
  return <SfStatusTag status={value} label={fallback?.label} semantic={fallback?.semantic} />
}

const COLUMNS: ColumnsType<NonconformingItem> = [
  { title: '质检单号', dataIndex: 'qcNo', width: 150, fixed: 'left' },
  { title: '来源单据', dataIndex: 'sourceNo', width: 160, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '批次号', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '不合格数量',
    dataIndex: 'qty',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '不合格原因',
    dataIndex: 'reason',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  {
    title: '处理结果',
    dataIndex: 'disposition',
    width: 130,
    render: (v?: QualityDisposition) => <DispositionTag value={v} />,
  },
  {
    title: '去向',
    dataIndex: 'destination',
    width: 110,
    render: (v?: NonconformingDestination) => (v ? (DESTINATION_LABEL[v] ?? v) : '-'),
  },
  { title: '处理人', dataIndex: 'handlerName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '处理时间',
    dataIndex: 'handledAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '记录时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 不合格品列表（frontend.md §9.1 质量中心模块；菜单 /quality/nonconforming，config/menu.tsx:107）。
 * GET /api/quality/nonconforming 为前端先行契约：M1 后端未交付质量域（backend-m1-plan.md §13），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 处理结果与去向（退供应商/报废/返工/降级/转不良品仓/特批放行）对齐 business-flow.md §4.3；
 * 单据锚点为关联质检单号 QC-（§13.1），转不良品仓须产生库存变动与流水（inventory-rules）。
 */
export default function NonconformingListPage() {
  const [params, setParams] = useState<NonconformingQuery>({})
  const list = usePagedList<NonconformingItem, NonconformingQuery>({
    queryKey: ['quality', 'nonconforming'],
    fetch: (q) => qualityApi.nonconforming(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as NonconformingQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="不合格品"
        subtitle="不合格品处置：退供应商 / 报废 / 返工 / 降级 / 转不良品仓 / 特批放行（business-flow.md §4.3）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '质检单号 / SKU / 来源单据' },
            { name: 'disposition', label: '处理结果', control: 'select', options: DISPOSITION_OPTIONS },
            { name: 'destination', label: '去向', control: 'select', options: DESTINATION_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<NonconformingItem>
          storageKey="quality-nonconforming"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有不合格品记录"
          scrollX={1690}
        />
      </Card>
    </div>
  )
}
