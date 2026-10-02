import { Button, Descriptions, Flex, Table } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  salesApi,
  type SalesOrderDetail,
  type SalesOrderLine,
} from '@/api/sales'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

/**
 * 销售订单状态 → SfStatusTag 三参兜底（key + label + semantic，frontend.md §24；
 * 不改 types/status.ts，键对齐 api/sales.ts SalesOrderStatus 前端先行枚举，后端冻结后回对）。
 */
const SALES_ORDER_STATUS_TAG: Record<string, { key: string; label: string; semantic: StatusSemantic }> = {
  draft: { key: 'draft', label: '草稿', semantic: 'neutral' },
  pending_review: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  approved: { key: 'approved', label: '已审核', semantic: 'success' },
  processing: { key: 'processing', label: '处理中', semantic: 'processing' },
  completed: { key: 'completed', label: '已完成', semantic: 'success' },
  cancelled: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

/** 单据状态 → 当前所处环节（business-flow.md §6.2：订单 → 审核 → 库存预占 → 出库） */
const STATUS_NODE_STATE: Record<string, { node: string; status?: string; label?: string; semantic?: StatusSemantic }> = {
  draft: { node: 'create', status: 'draft' },
  pending_review: { node: 'review', status: 'pending_review' },
  approved: { node: 'allocate', label: '待预占', semantic: 'pending' },
  processing: { node: 'complete', label: '出库中', semantic: 'processing' },
  completed: { node: 'complete', status: 'completed' },
  cancelled: { node: 'cancel', status: 'cancelled' },
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始（§13.4 业务时间字段） */
function buildSteps(detail: SalesOrderDetail): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[detail.status]
  const nodes: Array<{ key: string; title: string; time?: string; operator?: string; remark?: string }> = [
    { key: 'create', title: '创建', time: detail.createdAt, operator: detail.operatorName },
    { key: 'review', title: '审核', time: detail.reviewedAt, operator: detail.reviewedBy },
    {
      key: 'allocate',
      title: '库存预占',
      time: detail.allocatedAt,
      remark: '审核通过即触发库存预占（锁定库存，inventory-rules.md §4）',
    },
    { key: 'complete', title: '出库', time: detail.completedAt },
  ]
  if (detail.status === 'cancelled') {
    nodes.push({ key: 'cancel', title: '取消', time: detail.cancelledAt })
  }
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

const ITEM_COLUMNS: ColumnsType<SalesOrderLine> = [
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 140 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 180,
    ellipsis: true,
    render: (v?: string) => v ?? '-',
  },
  { title: '单位', dataIndex: 'unitName', width: 70, render: (v?: string) => v ?? '-' },
  {
    title: '数量',
    dataIndex: 'qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
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
  { title: '行备注', dataIndex: 'remark', width: 160, ellipsis: true, render: (v?: string) => v ?? '-' },
]

/** 销售订单详情（/sales/:id，GET /api/sales-orders/{id} 前端先行契约；后端单据域未交付时呈统一错误态） */
export default function SalesOrderDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['sales', 'orders', id],
    queryFn: () => salesApi.orders.detail(id as string),
    enabled: Boolean(id),
  })

  if (!id) {
    return (
      <div className="sf-page">
        <SfError error={new Error('URL 缺少销售单号')} />
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
          description="销售订单详情接口不可用：GET /api/sales-orders/{id}（前端先行契约，后端单据域 M2 冻结后回对字段）"
        />
      </div>
    )
  }

  const detail = query.data
  const lines = detail.items ?? []
  const statusMeta = SALES_ORDER_STATUS_TAG[detail.status]

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={detail.soNo}
        status={statusMeta?.key ?? detail.status}
        statusLabel={statusMeta?.label}
        statusSemantic={statusMeta?.semantic}
        summary={
          <SfSummaryBar
            items={[
              { label: '客户', value: detail.customerName },
              { label: '仓库', value: detail.warehouseName },
              { label: '总数量', value: formatNumber(detail.totalQty) },
              { label: '金额', value: formatMoney(detail.totalAmount) },
              { label: 'SKU 数', value: formatNumber(lines.length) },
            ]}
          />
        }
        actions={
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/sales')}>
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
              { key: 'soNo', label: '销售单号', children: detail.soNo },
              {
                key: 'customer',
                label: '客户',
                children: detail.customerCode
                  ? `${detail.customerName}（${detail.customerCode}）`
                  : detail.customerName,
              },
              {
                key: 'warehouse',
                label: '仓库',
                children: detail.warehouseCode
                  ? `${detail.warehouseName}（${detail.warehouseCode}）`
                  : detail.warehouseName,
              },
              { key: 'deliveryType', label: '配送方式', children: detail.deliveryType ?? '-' },
              {
                key: 'shippingAddress',
                label: '收货地址',
                children: detail.shippingAddress ?? '-',
                span: 2,
              },
              { key: 'operator', label: '创建人', children: detail.operatorName ?? '-' },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(detail.createdAt) },
              {
                key: 'reviewed',
                label: '审核',
                children: detail.reviewedAt
                  ? `${formatDateTime(detail.reviewedAt)}（${detail.reviewedBy ?? '-'}）`
                  : '-',
              },
              { key: 'allocatedAt', label: '库存预占时间', children: formatDateTime(detail.allocatedAt) },
              { key: 'completedAt', label: '完成时间', children: formatDateTime(detail.completedAt) },
              { key: 'remark', label: '备注', children: detail.remark ?? '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<SalesOrderLine>
            size="small"
            rowKey="id"
            columns={ITEM_COLUMNS}
            dataSource={lines}
            pagination={false}
            scroll={{ x: 900 }}
            locale={{ emptyText: () => <SfEmpty description="该销售订单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(detail)} emptyText="该销售订单暂无流程节点记录" />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
