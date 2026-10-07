import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DISPOSITION_TAG_FALLBACK,
  INSPECTION_METHOD_LABEL,
  QUALITY_RESULT_TAG_META,
  qualityApi,
  type InspectionMethod,
  type QualityDisposition,
  type QualityResult,
  type QualityTraceItem,
  type QualityTraceQuery,
  type QualityRecordType,
  RECORD_TYPE_LABEL,
} from '@/api/quality'
import { DateCell } from '@/components/table/cells'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Text } = Typography

/**
 * 处理结果（disposition 处置六英文键）→ SfStatusTag：注册表（types/status.ts 质检处置段）
 * 与 DISPOSITION_TAG_FALLBACK 的 label/semantic 逐键一致，未知值兜底中性灰 + 原始文案，不崩溃。
 */
function DispositionTag({ value }: { value?: QualityDisposition }) {
  if (!value) return '-'
  const fallback = DISPOSITION_TAG_FALLBACK[value]
  return <SfStatusTag label={fallback?.label ?? value} semantic={fallback?.semantic ?? 'neutral'} />
}

const COLUMNS: ColumnsType<QualityTraceItem> = [
  {
    title: '记录时间',
    dataIndex: 'occurred_at',
    width: 160,
    fixed: 'left',
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '记录类型',
    dataIndex: 'record_type',
    width: 110,
    render: (v: QualityRecordType) => RECORD_TYPE_LABEL[v] ?? v,
  },
  { title: '质检单号', dataIndex: 'qc_no', width: 150, render: (v?: string) => v ?? '-' },
  { title: '来源业务', dataIndex: 'biz_type', width: 100, render: (v?: string) => v ?? '-' },
  { title: '单据号', dataIndex: 'biz_no', width: 160, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'product_name',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '批次号', dataIndex: 'batch_no', width: 110, render: (v?: string) => v ?? '-' },
  // 序列号列已摘除：质检链无序列号台账，后端 QualityTraceItem 无 serial_no 字段
  // （service_quality_trace.go:43-58），恒空列属「拿错数据」，禁止（requirements.md §10）。
  {
    title: '检验方式',
    dataIndex: 'inspection_method',
    width: 90,
    render: (v?: InspectionMethod) => (v ? (INSPECTION_METHOD_LABEL[v] ?? v) : '-'),
  },
  {
    title: '检验结果',
    dataIndex: 'result',
    width: 100,
    // 九类中文值域（迁移 000007:248 chk_quality_orders_result，与质检单 result 同域），
    // 不命中 types/status.ts 小写注册表，经 QUALITY_RESULT_TAG_META 显式指定 label/semantic
    render: (v?: string) => {
      if (!v) return '-'
      const meta = QUALITY_RESULT_TAG_META[v as QualityResult]
      return <SfStatusTag label={meta?.label ?? v} semantic={meta?.semantic ?? 'neutral'} />
    },
  },
  {
    title: '处理结果',
    dataIndex: 'disposition',
    width: 130,
    render: (v?: QualityDisposition) => <DispositionTag value={v} />,
  },
  { title: '检验人', dataIndex: 'inspector_name', width: 100, render: (v?: string) => v ?? '-' },
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
 * GET /api/quality/trace 后端 2026-10-04 已交付（internal/purchase/service_quality_trace.go，
 * 原「端点未立项呈统一错误态」披露废止）。按 SKU / 批次号 / 单据号 三维检索质量记录链
 * （检验 → 处置），走既有列表模式；序列号（serial_no）检索维度已摘除——质检链未记录
 * 序列号，后端对非空 serial_no 显式 400（service_quality_trace.go:107-111）。
 */
export default function QualityTracePage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<QualityTraceItem, QualityTraceQuery>({
    queryKey: ['quality', 'trace'],
    fetch: (q) => qualityApi.trace(q),
    urlSync: true,
  })

  return (
    <div className="sf-page">
      <SfPageHeader
        title="质量追溯"
        subtitle="按 SKU / 批次号 / 单据号检索质量记录链（检验 → 处置）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'sku_code', label: 'SKU', control: 'input', placeholder: 'SKU 编码' },
            { name: 'batch_no', label: '批次号', control: 'input', placeholder: '批次号' },
            { name: 'biz_no', label: '单据号', control: 'input', placeholder: '质检单号 / 业务单据号' },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
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
          emptyText="请输入 SKU / 批次号 / 单据号进行质量追溯"
          scrollX={1670}
        />
      </Card>
    </div>
  )
}
