import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DISPOSITION_TAG_FALLBACK,
  qualityApi,
  type NonconformingItem,
  type NonconformingQuery,
  type QualityDisposition,
} from '@/api/quality'
import { DateCell } from '@/components/table/cells'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

const DISPOSITION_OPTIONS: Array<{ label: string; value: QualityDisposition }> = [
  { label: '退供应商', value: 'return_supplier' },
  { label: '报废', value: 'scrap' },
  { label: '返工', value: 'rework' },
  { label: '降级', value: 'downgrade' },
  { label: '转不良品仓', value: 'to_defective_warehouse' },
  { label: '特批放行', value: 'special_release' },
]

// 去向（destination）筛选与列已摘除：quality_orders 无「处置去向」列，后端对非空
// destination 检索显式 400、记录亦不下发（service_quality_trace.go:145-149 + 文件头
// 「已知边界」）——提供即假筛选/恒空列，禁止（requirements.md §10）。

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
  { title: '质检单号', dataIndex: 'qc_no', width: 150, fixed: 'left' },
  { title: '来源单据', dataIndex: 'source_no', width: 160, render: (v?: string) => v ?? '-' },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'product_name',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '批次号', dataIndex: 'batch_no', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '不合格数量',
    dataIndex: 'qty',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  // 「不合格原因」（reason）列已摘除：后端 NonconformingItem 无该字段（不合格成因由
  // 质检单 result 九值承载）——消费不存在字段恒显示 '-' 属拿错数据，禁止。
  // 「去向」（destination）列已摘除：同上无字段（service_quality_trace.go:71-83）。
  {
    title: '处理结果',
    dataIndex: 'disposition',
    width: 130,
    render: (v?: QualityDisposition) => <DispositionTag value={v} />,
  },
  { title: '处理人', dataIndex: 'handler_name', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '处理时间',
    dataIndex: 'handled_at',
    width: 160,
    render: (v?: string) => <DateCell value={v} />,
  },
  {
    title: '记录时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <DateCell value={v} />,
  },
]

/**
 * 不合格品列表（frontend.md §9.1 质量中心模块；菜单 /quality/nonconforming，config/menu.tsx:107）。
 * GET /api/quality/nonconforming 后端 2026-10-04 已交付（internal/purchase/service_quality_trace.go，
 * 原「端点未立项呈统一错误态」披露废止）。
 * 处理结果六值（退供应商/报废/返工/降级/转不良品仓/特批放行）对齐 business-flow.md §4.3，
 * 经 SfStatusTag 呈现；去向（destination）与不合格原因（reason）无后端字段，检索与列已摘除。
 * 单据锚点为关联质检单号 QC-（§13.1），转不良品仓须产生库存变动与流水（inventory-rules）。
 */
export default function NonconformingListPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<NonconformingItem, NonconformingQuery>({
    queryKey: ['quality', 'nonconforming'],
    fetch: (q) => qualityApi.nonconforming(q),
    urlSync: true,
  })

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
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
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
          scrollX={1420}
        />
      </Card>
    </div>
  )
}
