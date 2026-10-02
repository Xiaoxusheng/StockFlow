import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DISPOSITION_TAG_FALLBACK,
  INSPECTION_METHOD_LABEL,
  qualityApi,
  type InspectionMethod,
  type QualityDisposition,
  type QualityInspectionItem,
  type QualityInspectionQuery,
  type QualityResult,
} from '@/api/quality'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const METHOD_OPTIONS: Array<{ label: string; value: InspectionMethod }> = [
  { label: '免检', value: 'NONE' },
  { label: '抽检', value: 'SAMPLING' },
  { label: '全检', value: 'FULL' },
]

const RESULT_OPTIONS: Array<{ label: string; value: QualityResult }> = [
  { label: '合格', value: 'qualified' },
  { label: '部分合格', value: 'partially_qualified' },
  { label: '不合格', value: 'unqualified' },
]

const DISPOSITION_OPTIONS: Array<{ label: string; value: QualityDisposition }> = [
  { label: '合格', value: 'qualified' },
  { label: '部分合格', value: 'partially_qualified' },
  { label: '不合格', value: 'unqualified' },
  { label: '退供应商', value: 'return_supplier' },
  { label: '报废', value: 'scrap' },
  { label: '返工', value: 'rework' },
  { label: '降级', value: 'downgrade' },
  { label: '转不良品仓', value: 'to_defective_warehouse' },
  { label: '特批放行', value: 'special_release' },
]

/**
 * 处理结果 → SfStatusTag：qualified/partially_qualified/unqualified 命中
 * types/status.ts 注册表，其余六种处置值走 DISPOSITION_TAG_FALLBACK 的 label/semantic 兜底；
 * 后端返回未知值时兜底中性灰 + 原始文案，不崩溃。
 */
function DispositionTag({ value }: { value?: QualityDisposition }) {
  if (!value) return '-'
  const fallback = DISPOSITION_TAG_FALLBACK[value]
  return <SfStatusTag status={value} label={fallback?.label} semantic={fallback?.semantic} />
}

const COLUMNS: ColumnsType<QualityInspectionItem> = [
  { title: '质检单号', dataIndex: 'inspectionNo', width: 150, fixed: 'left' },
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
    title: '检验方式',
    dataIndex: 'inspectionMethod',
    width: 90,
    render: (v: InspectionMethod) => INSPECTION_METHOD_LABEL[v] ?? v,
  },
  {
    title: '检验数量',
    dataIndex: 'inspectQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '合格数量',
    dataIndex: 'qualifiedQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '不合格数量',
    dataIndex: 'unqualifiedQty',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '检验结果',
    dataIndex: 'result',
    width: 100,
    render: (v: QualityResult) => <SfStatusTag status={v} />,
  },
  {
    title: '处理结果',
    dataIndex: 'disposition',
    width: 130,
    render: (v?: QualityDisposition) => <DispositionTag value={v} />,
  },
  {
    title: '原因',
    dataIndex: 'reason',
    width: 140,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 140 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  { title: '检验人', dataIndex: 'inspectorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '检验时间',
    dataIndex: 'inspectedAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 质检单列表（frontend.md §9.1 质量中心模块；菜单 /quality/inspections，config/menu.tsx:106）。
 * GET /api/quality/inspections 为前端先行契约：M1 后端未交付质量域（backend-m1-plan.md §13），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 列与筛选值域对齐 business-flow.md §4.1–§4.3：检验方式（免检/抽检/全检）、
 * 检验数量/合格数量/不合格数量、检验结果、处理结果九值、检验人、检验时间；
 * 单号 QC- 前缀（business-flow.md §13.1）。
 */
export default function QualityInspectionListPage() {
  const [params, setParams] = useState<QualityInspectionQuery>({})
  const list = usePagedList<QualityInspectionItem, QualityInspectionQuery>({
    queryKey: ['quality', 'inspections'],
    fetch: (q) => qualityApi.inspections(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as QualityInspectionQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="质检"
        subtitle="质检单：免检 / 抽检 / 全检，处理结果九值（business-flow.md §4）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '质检单号 / SKU / 来源单据' },
            { name: 'inspectionMethod', label: '检验方式', control: 'select', options: METHOD_OPTIONS },
            { name: 'result', label: '检验结果', control: 'select', options: RESULT_OPTIONS },
            { name: 'disposition', label: '处理结果', control: 'select', options: DISPOSITION_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<QualityInspectionItem>
          storageKey="quality-inspections"
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
          emptyText="当前筛选条件下没有质检单"
          scrollX={1760}
        />
      </Card>
    </div>
  )
}
