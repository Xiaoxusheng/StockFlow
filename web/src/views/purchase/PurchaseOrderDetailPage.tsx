import { Button, Descriptions, Flex, Table } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  purchaseApi,
  type PurchaseDetail,
  type PurchaseDetailItem,
  type PurchaseRelatedReceipt,
} from '@/api/purchase'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDate, formatDateTime, formatMoney, formatNumber } from '@/utils/format'

/**
 * 单据状态 → 当前所处环节的展示（business-flow.md §2.2 状态机）。
 * 已审核待到货 / 部分到货落在「收货」环节，未注册的展示文案走 SfTimeline 的 label 兜底。
 */
const STATUS_NODE_STATE: Record<string, { node: string; status?: string; label?: string; semantic?: StatusSemantic }> = {
  draft: { node: 'create', status: 'draft' },
  pending_review: { node: 'review', status: 'pending_review' },
  approved: { node: 'receive', label: '待收货', semantic: 'pending' },
  partially_received: { node: 'receive', label: '部分收货', semantic: 'processing' },
  received: { node: 'receive', status: 'received' },
  completed: { node: 'complete', status: 'completed' },
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始（§13.4 各环节时间） */
function buildSteps(detail: PurchaseDetail): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[detail.status]
  const nodes: Array<{ key: string; title: string; time?: string; operator?: string; remark?: string }> = [
    { key: 'create', title: '创建', time: detail.createdAt, operator: detail.operatorName },
    { key: 'review', title: '审核', time: detail.reviewedAt, operator: detail.reviewedBy },
    {
      key: 'receive',
      title: '收货',
      time: detail.receivedAt,
      operator: detail.receivedBy,
      // 部分收货进度（business-flow.md §2.3：累计收货不得超过原始数量）
      remark:
        detail.receivedQty != null
          ? `已收货 ${formatNumber(detail.receivedQty)} / ${formatNumber(detail.totalQty)}`
          : undefined,
    },
    { key: 'complete', title: '完成', time: detail.completedAt, operator: detail.completedBy },
  ]
  return nodes.map((node) => {
    const isCurrent = current?.node === node.key && !node.time
    return {
      key: node.key,
      title: node.title,
      time: node.time,
      operator: node.operator,
      remark: node.remark,
      status: node.time ? 'completed' : isCurrent ? current?.status : undefined,
      statusLabel: node.time || !isCurrent ? undefined : current?.label,
      statusSemantic: node.time || !isCurrent ? undefined : current?.semantic,
    }
  })
}

const ITEM_COLUMNS: ColumnsType<PurchaseDetailItem> = [
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
    title: '原始数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '累计收货',
    dataIndex: 'receivedQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '待到货',
    dataIndex: 'pendingQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '拒收',
    dataIndex: 'rejectedQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '单价',
    dataIndex: 'unitPrice',
    width: 110,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '金额',
    dataIndex: 'amount',
    width: 120,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v?: string) => v ?? '-' },
]

const RECEIPT_COLUMNS: ColumnsType<PurchaseRelatedReceipt> = [
  { title: '收货单号', dataIndex: 'receiptNo', width: 200 },
  {
    title: '收货数量',
    dataIndex: 'receivedQty',
    width: 120,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 110,
    render: (v?: string) => (v ? <SfStatusTag status={v} /> : '-'),
  },
]

/** 采购订单详情（/purchases/:id，frontend.md §7 结构；后端采购单据域未交付时呈统一错误态） */
export default function PurchaseOrderDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['purchase', 'detail', id],
    queryFn: () => purchaseApi.get(id as string),
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
          description="采购订单详情接口不可用：GET /api/purchases/{id}（后端采购单据域尚未交付，契约冻结后回对字段）"
        />
      </div>
    )
  }

  const detail = query.data
  const lines = detail.items ?? []
  const receipts = detail.receipts ?? []

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={detail.poNo}
        status={detail.status}
        summary={
          <SfSummaryBar
            items={[
              { label: '供应商', value: detail.supplierName },
              { label: '仓库', value: detail.warehouseName },
              { label: '总数量', value: formatNumber(detail.totalQty) },
              { label: '已收货', value: formatNumber(detail.receivedQty) },
              { label: '金额', value: formatMoney(detail.totalAmount) },
              { label: 'SKU 数', value: formatNumber(lines.length) },
            ]}
          />
        }
        actions={
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/purchases')}>
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
              { key: 'poNo', label: '采购单号', children: detail.poNo },
              {
                key: 'supplier',
                label: '供应商',
                children: detail.supplierCode
                  ? `${detail.supplierName}（${detail.supplierCode}）`
                  : detail.supplierName,
              },
              {
                key: 'warehouse',
                label: '仓库',
                children: detail.warehouseCode
                  ? `${detail.warehouseName}（${detail.warehouseCode}）`
                  : detail.warehouseName,
              },
              { key: 'expectedArrivalDate', label: '预计到货', children: formatDate(detail.expectedArrivalDate) },
              { key: 'operator', label: '创建人', children: detail.operatorName ?? '-' },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(detail.createdAt) },
              { key: 'remark', label: '备注', children: detail.remark ?? '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<PurchaseDetailItem>
            size="small"
            rowKey="id"
            columns={ITEM_COLUMNS}
            dataSource={lines}
            pagination={false}
            scroll={{ x: 1180 }}
            locale={{ emptyText: () => <SfEmpty description="该采购订单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(detail)} emptyText="该采购订单暂无流程节点记录" />
        </SfDetailSection>
        <SfDetailSection title="关联单据">
          {receipts.length === 0 ? (
            <SfEmpty description="暂无关联收货单（收货单生成后在此展示）" />
          ) : (
            <Table<PurchaseRelatedReceipt>
              size="small"
              rowKey="id"
              columns={RECEIPT_COLUMNS}
              dataSource={receipts}
              pagination={false}
            />
          )}
        </SfDetailSection>
      </Flex>
    </div>
  )
}
