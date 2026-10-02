import { Button, Descriptions, Flex, Table } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import { outboundApi, type OutboundDetail, type OutboundDetailItem } from '@/api/outbound'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 出库类型文案（business-flow.md §7.1；与列表页一致，未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  sales: '销售出库',
  production: '生产领料',
  transfer: '调拨出库',
  other: '其他出库',
  loss: '报损出库',
}

/**
 * 单据状态 → 当前所处环节的展示（business-flow.md §7.2/§8 分配→拣货→复核→打包→发货）。
 * 「已分配/已拣货/已打包」等表示上一环节刚完成，映射为下一环节的待处理状态；
 * 未命中的未完成环节由 SfTimeline 兜底为「未开始」。
 */
const STATUS_NODE_STATE: Record<string, { node: string; status?: string; label?: string; semantic?: StatusSemantic }> = {
  pending_allocate: { node: 'allocate', status: 'pending_allocate' },
  allocated: { node: 'pick', status: 'pending_pick' },
  pending_pick: { node: 'pick', status: 'pending_pick' },
  picking: { node: 'pick', status: 'picking' },
  picked: { node: 'check', status: 'pending_check' },
  pending_check: { node: 'check', status: 'pending_check' },
  checking: { node: 'check', status: 'checking' },
  pending_pack: { node: 'pack', status: 'pending_pack' },
  packing: { node: 'pack', status: 'packing' },
  pending_shipment: { node: 'ship', status: 'pending_shipment' },
  shipment_exception: { node: 'ship', status: 'shipment_exception' },
  shipped: { node: 'ship', status: 'shipped' },
  completed: { node: 'ship', status: 'completed' },
  closed: { node: 'ship', status: 'completed' },
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始（§13.4 各环节时间） */
function buildSteps(detail: OutboundDetail): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[detail.status]
  const nodes: Array<{ key: string; title: string; time?: string; operator?: string }> = [
    { key: 'allocate', title: '分配', time: detail.allocatedAt, operator: detail.allocatedBy },
    { key: 'pick', title: '拣货', time: detail.pickedAt, operator: detail.pickedBy },
    { key: 'check', title: '复核', time: detail.checkedAt, operator: detail.checkedBy },
    { key: 'pack', title: '打包', time: detail.packedAt, operator: detail.packedBy },
    { key: 'ship', title: '发货', time: detail.shippedAt, operator: detail.shippedBy },
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

const ITEM_COLUMNS: ColumnsType<OutboundDetailItem> = [
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
    title: '需求数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已拣数量',
    dataIndex: 'pickedQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '来源库位', dataIndex: 'fromBinCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 130, render: (v?: string) => v ?? '-' },
  { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v?: string) => v ?? '-' },
]

/** 出库单详情（/outbound/:id，frontend.md §7 结构；后端 outbound 单据域未交付时呈统一错误态） */
export default function OutboundDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['outbound', 'detail', id],
    queryFn: () => outboundApi.get(id as string),
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
          description="出库单详情接口不可用：GET /api/outbounds/{id}（后端 outbound 单据域尚未交付，契约冻结后回对字段）"
        />
      </div>
    )
  }

  const detail = query.data
  const lines = detail.items ?? []

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={detail.outboundNo}
        status={detail.status}
        summary={
          <SfSummaryBar
            items={[
              { label: '出库类型', value: TYPE_LABEL[detail.outboundType] ?? detail.outboundType },
              { label: '仓库', value: detail.warehouseName },
              { label: '需求数量', value: formatNumber(detail.totalQty) },
              { label: '已拣数量', value: formatNumber(detail.pickedQty) },
              { label: 'SKU 数', value: formatNumber(lines.length) },
            ]}
          />
        }
        actions={
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/outbound')}>
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
              { key: 'outboundNo', label: '出库单号', children: detail.outboundNo },
              {
                key: 'outboundType',
                label: '出库类型',
                children: TYPE_LABEL[detail.outboundType] ?? detail.outboundType,
              },
              {
                key: 'warehouse',
                label: '仓库',
                children: detail.warehouseCode
                  ? `${detail.warehouseName}（${detail.warehouseCode}）`
                  : detail.warehouseName,
              },
              { key: 'customer', label: '客户', children: detail.customerName ?? '-' },
              { key: 'operator', label: '创建人', children: detail.operatorName ?? '-' },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(detail.createdAt) },
              { key: 'remark', label: '备注', children: detail.remark ?? '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<OutboundDetailItem>
            size="small"
            rowKey="id"
            columns={ITEM_COLUMNS}
            dataSource={lines}
            pagination={false}
            scroll={{ x: 980 }}
            locale={{ emptyText: () => <SfEmpty description="该出库单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(detail)} emptyText="该出库单暂无流程节点记录" />
        </SfDetailSection>
        <SfDetailSection title="关联单据">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2 }}
            items={[
              {
                key: 'sourceNo',
                label: '来源单号',
                children: detail.sourceNo ?? '暂无来源单据',
              },
              { key: 'carrier', label: '物流公司', children: detail.carrierName ?? '-' },
              { key: 'trackingNo', label: '物流单号', children: detail.trackingNo ?? '-' },
            ]}
          />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
