import { Button, Descriptions, Flex, Table } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import { inboundApi, type InboundDetail, type InboundDetailItem } from '@/api/inbound'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 入库类型文案（business-flow.md §3.1；与列表页一致，未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  purchase: '采购入库',
  production: '生产入库',
  sales_return: '销售退货入库',
  transfer: '调拨入库',
  other: '其他入库',
}

/**
 * 单据状态 → 当前所处环节的展示（business-flow.md §3.2 流程 + types/status.ts 注册表）。
 * 命中环节用映射后的状态标签展示；未命中的未完成环节由 SfTimeline 兜底为「未开始」。
 */
const STATUS_NODE_STATE: Record<string, { node: string; status?: string; label?: string; semantic?: StatusSemantic }> = {
  draft: { node: 'create', status: 'draft' },
  pending_review: { node: 'review', status: 'pending_review' },
  pending_receipt: { node: 'receive', status: 'pending_receipt' },
  receiving: { node: 'receive', status: 'receiving' },
  received: { node: 'receive', status: 'received' },
  pending_inspection: { node: 'inspect', status: 'pending_inspection' },
  inspecting: { node: 'inspect', status: 'inspecting' },
  pending_putaway: { node: 'putaway', status: 'pending_putaway' },
  putaway_in_progress: { node: 'putaway', status: 'putaway_in_progress' },
  putaway_completed: { node: 'putaway', status: 'putaway_completed' },
  completed: { node: 'putaway', status: 'completed' },
  closed: { node: 'putaway', status: 'completed' },
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始（§13.4 各环节时间） */
function buildSteps(detail: InboundDetail): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[detail.status]
  const nodes: Array<{ key: string; title: string; time?: string; operator?: string }> = [
    { key: 'create', title: '创建', time: detail.createdAt, operator: detail.operatorName },
    { key: 'review', title: '审核', time: detail.reviewedAt, operator: detail.reviewedBy },
    { key: 'receive', title: '收货', time: detail.receivedAt, operator: detail.receivedBy },
    { key: 'inspect', title: '质检', time: detail.inspectedAt, operator: detail.inspectedBy },
    { key: 'putaway', title: '上架', time: detail.putawayAt, operator: detail.putawayBy },
  ]
  return nodes.map((node) => {
    const isCurrent = current?.node === node.key && !node.time
    return {
      key: node.key,
      title: node.title,
      time: node.time,
      operator: node.operator,
      status: node.time ? 'completed' : isCurrent ? current?.status : undefined,
      statusLabel: node.time || !isCurrent ? undefined : current?.label,
      statusSemantic: node.time || !isCurrent ? undefined : current?.semantic,
    }
  })
}

const ITEM_COLUMNS: ColumnsType<InboundDetailItem> = [
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 140 },
  {
    title: '商品名称',
    dataIndex: 'skuName',
    width: 180,
    ellipsis: true,
    render: (v?: string) => v ?? '-',
  },
  { title: '单位', dataIndex: 'unitName', width: 70, render: (v?: string) => v ?? '-' },
  {
    title: '计划数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已收数量',
    dataIndex: 'receivedQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '批次', dataIndex: 'batchNo', width: 130, render: (v?: string) => v ?? '-' },
  { title: '目的库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v?: string) => v ?? '-' },
]

/** 入库单详情（/inbound/:id，frontend.md §7 结构；后端入库单据域未交付时呈统一错误态） */
export default function InboundDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['inbound', 'detail', id],
    queryFn: () => inboundApi.get(id as string),
    enabled: Boolean(id),
  })

  if (!id) {
    return (
      <div className="sf-page">
        <SfError error={new Error('URL 缺少单据编号')} />
      </div>
    )
  }
  if (query.status === 'pending') {
    return (
      <div className="sf-page">
        <SfLoading rows={8} />
      </div>
    )
  }
  if (query.status === 'error') {
    return (
      <div className="sf-page">
        <SfError
          error={query.error}
          onRetry={query.refetch}
          description="入库单详情接口不可用：GET /api/inbounds/{id}（后端入库单据域尚未交付，契约冻结后回对字段）"
        />
      </div>
    )
  }

  const detail = query.data
  const lines = detail.items ?? []

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={detail.inboundNo}
        status={detail.status}
        summary={
          <SfSummaryBar
            items={[
              { label: '入库类型', value: TYPE_LABEL[detail.inboundType] ?? detail.inboundType },
              { label: '仓库', value: detail.warehouseName },
              { label: '计划数量', value: formatNumber(detail.totalQty) },
              { label: '已收数量', value: formatNumber(detail.receivedQty) },
              { label: 'SKU 数', value: formatNumber(lines.length) },
            ]}
          />
        }
        actions={
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/inbound')}>
            返回列表
          </Button>
        }
      />
      <Flex vertical gap={16}>
        <SfDetailSection title="基础信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'inboundNo', label: '入库单号', children: detail.inboundNo },
              {
                key: 'inboundType',
                label: '入库类型',
                children: TYPE_LABEL[detail.inboundType] ?? detail.inboundType,
              },
              {
                key: 'warehouse',
                label: '仓库',
                children: detail.warehouseCode
                  ? `${detail.warehouseName}（${detail.warehouseCode}）`
                  : detail.warehouseName,
              },
              { key: 'supplier', label: '供应商', children: detail.supplierName ?? '-' },
              { key: 'operator', label: '创建人', children: detail.operatorName ?? '-' },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(detail.createdAt) },
              { key: 'remark', label: '备注', children: detail.remark ?? '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<InboundDetailItem>
            size="small"
            rowKey="id"
            columns={ITEM_COLUMNS}
            dataSource={lines}
            pagination={false}
            scroll={{ x: 980 }}
            locale={{ emptyText: () => <SfEmpty description="该入库单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(detail)} emptyText="该入库单暂无流程节点记录" />
        </SfDetailSection>
        <SfDetailSection title="关联单据">
          <Descriptions
            bordered
            size="small"
            column={1}
            items={[
              {
                key: 'sourceNo',
                label: '来源单号',
                children: detail.sourceNo ?? '暂无来源单据',
              },
            ]}
          />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
