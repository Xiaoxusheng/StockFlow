import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DISPOSITION_TAG_FALLBACK,
  INSPECTION_METHOD_LABEL,
  qualityApi,
  type InspectionMethod,
  type QualityDisposition,
  type QualityResult,
  type QualityTraceItem,
  type QualityTraceQuery,
  type QualityRecordType,
  RECORD_TYPE_LABEL,
} from '@/api/quality'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

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

const COLUMNS: ColumnsType<QualityTraceItem> = [
  {
    title: '记录时间',
    dataIndex: 'occurredAt',
    width: 160,
    fixed: 'left',
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '记录类型',
    dataIndex: 'recordType',
    width: 110,
    render: (v: QualityRecordType) => RECORD_TYPE_LABEL[v] ?? v,
  },
  { title: '质检单号', dataIndex: 'qcNo', width: 150, render: (v?: string) => v ?? '-' },
  { title: '来源业务', dataIndex: 'bizType', width: 100, render: (v?: string) => v ?? '-' },
  { title: '单据号', dataIndex: 'bizNo', width: 160, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '批次号', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  { title: '序列号', dataIndex: 'serialNo', width: 130, render: (v?: string) => v ?? '-' },
  {
    title: '检验方式',
    dataIndex: 'inspectionMethod',
    width: 90,
    render: (v?: InspectionMethod) => (v ? (INSPECTION_METHOD_LABEL[v] ?? v) : '-'),
  },
  {
    title: '检验结果',
    dataIndex: 'result',
    width: 100,
    render: (v?: QualityResult) => (v ? <SfStatusTag status={v} /> : '-'),
  },
  {
    title: '处理结果',
    dataIndex: 'disposition',
    width: 130,
    render: (v?: QualityDisposition) => <DispositionTag value={v} />,
  },
  { title: '检验人', dataIndex: 'inspectorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
]

/**
 * 质量追溯（frontend.md §9.1 质量中心模块；菜单 /quality/trace，config/menu.tsx:108）。
 * GET /api/quality/trace 为前端先行契约：M1 后端未交付质量域（backend-m1-plan.md §13），
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 按 SKU / 批次号 / 序列号 / 单据号 四维检索质量记录链（检验 → 处置），走既有列表模式。
 */
export default function QualityTracePage() {
  const [params, setParams] = useState<QualityTraceQuery>({})
  const list = usePagedList<QualityTraceItem, QualityTraceQuery>({
    queryKey: ['quality', 'trace'],
    fetch: (q) => qualityApi.trace(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as QualityTraceQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="质量追溯"
        subtitle="按 SKU / 批次号 / 序列号 / 单据号检索质量记录链（检验 → 处置）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'skuCode', label: 'SKU', control: 'input', placeholder: 'SKU 编码' },
            { name: 'batchNo', label: '批次号', control: 'input', placeholder: '批次号' },
            { name: 'serialNo', label: '序列号 SN', control: 'input', placeholder: '序列号' },
            { name: 'bizNo', label: '单据号', control: 'input', placeholder: '质检单号 / 业务单据号' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<QualityTraceItem>
          storageKey="quality-trace"
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
          emptyText="请输入 SKU / 批次号 / 序列号 / 单据号进行质量追溯"
          scrollX={1800}
        />
      </Card>
    </div>
  )
}
