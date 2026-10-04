import { useMemo } from 'react'
import { Descriptions, Flex, Table, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  purchaseApi,
  type PurchaseOrder,
  type PurchaseOrderItem,
  type PurchaseStatus,
} from '@/api/purchase'
import { toStatusKey, type SkuItem } from '@/api/masterdata'
import {
  buildIdItemMap,
  fetchSkuOptions,
  fetchSupplierOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

/**
 * 采购订单状态 → SfStatusTag（internal/purchase/models.go:17-23 七态）。
 * types/status.ts 注册表已收录 draft/pending_approval/approved/completed/cancelled；
 * PARTIAL_RECEIVED/RECEIVED_ALL 为采购语境专有键未注册，经 SfStatusTag 的
 * label/semantic 兜底；后端返回未知值时中性灰 + 原始文案。
 */
const PO_STATUS_TAG: Record<PurchaseStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  PARTIAL_RECEIVED: { label: '部分到货', semantic: 'processing' },
  RECEIVED_ALL: { label: '到货完成', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/**
 * 单据状态 → 当前所处环节的展示（business-flow.md §2.2 状态机）。
 * 键为后端大写枚举；已发生环节由时间列驱动（有时间即已完成），
 * 未发生且非当前环节由 SfTimeline 显示「未开始」。
 */
const STATUS_NODE: Partial<Record<PurchaseStatus, { node: string; label: string; semantic: StatusSemantic }>> = {
  DRAFT: { node: 'review', label: '待提交', semantic: 'pending' },
  PENDING_APPROVAL: { node: 'review', label: '待审核', semantic: 'pending' },
  APPROVED: { node: 'receive', label: '待收货', semantic: 'pending' },
  PARTIAL_RECEIVED: { node: 'receive', label: '部分到货', semantic: 'processing' },
  RECEIVED_ALL: { node: 'complete', label: '待完成', semantic: 'pending' },
}

interface FlowNode {
  key: string
  title: string
  time: string | null
}

/** Timeline 节点组装：有时间的环节视为已完成（approved_at/received_at/completed_at/
 * cancelled_at 由状态机落列，repository.go:318-324），当前环节展示单据状态，
 * 其余未开始（frontend.md §7 各环节时间）。 */
function buildSteps(order: PurchaseOrder): SfTimelineStep[] {
  const current = STATUS_NODE[order.status]
  const nodes: FlowNode[] = [
    { key: 'create', title: '创建', time: order.created_at },
    { key: 'review', title: '审核', time: order.approved_at },
    { key: 'receive', title: '收货', time: order.received_at },
    { key: 'complete', title: '完成', time: order.completed_at },
  ]
  if (order.status === 'CANCELLED') {
    nodes.push({ key: 'cancel', title: '取消', time: order.cancelled_at })
  }
  return nodes.map((node) => {
    if (node.time) {
      return { ...node, status: 'completed' }
    }
    if (current && current.node === node.key) {
      return { ...node, statusLabel: current.label, statusSemantic: current.semantic }
    }
    return { ...node }
  })
}

/** 采购订单明细列（PurchaseOrderItem，models.go:143-157 四量约束：
 * qty_ordered 原始 / qty_received 累计收货 / qty_rejected 拒收 / qty_putaway 已上架）。
 * sku_id 为裸 ID，经 SKU options 本地映射，失败降级为 ID。 */
function buildItemColumns(skuItems: Map<string, SkuItem>): ColumnsType<PurchaseOrderItem> {
  return [
    { title: '行号', dataIndex: 'line_no', width: 60, align: 'right', render: (v: number) => <span className="sf-num">{formatNumber(v)}</span> },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 140,
      render: (_: unknown, record: PurchaseOrderItem) => skuItems.get(String(record.sku_id))?.code ?? `SKU #${record.sku_id}`,
    },
    {
      title: '商品名称',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: PurchaseOrderItem) => {
        const name = skuItems.get(String(record.sku_id))?.product_name
        return name ? (
          <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '原始数量',
      dataIndex: 'qty_ordered',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '累计收货',
      dataIndex: 'qty_received',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '拒收',
      dataIndex: 'qty_rejected',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已上架',
      dataIndex: 'qty_putaway',
      width: 90,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '单价',
      dataIndex: 'price',
      width: 110,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '金额',
      dataIndex: 'amount',
      width: 120,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v: string) => v || '-' },
  ]
}

/** 采购订单详情（/purchases/:id；GET /api/purchases/{id} 返回 PODetail {order,items}，
 * service_purchase.go:444-463；出参为裸模型 snake_case，供应商/仓库/SKU 经
 * 基础资料 options 本地映射，映射失败降级为 ID，不造假数据） */
export default function PurchaseOrderDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['purchase', 'detail', id],
    queryFn: () => purchaseApi.detail(id as string),
    enabled: Boolean(id),
  })

  // 供应商/仓库/SKU options 一次取全（api/options.ts 头注释：映射失败由调用方降级，不阻塞详情）
  const supplierOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'suppliers'],
    queryFn: fetchSupplierOptions,
  })
  const warehouseOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skuOptionsQuery = useQuery({
    queryKey: ['purchase', 'options', 'skus'],
    queryFn: fetchSkuOptions,
  })
  const supplierItems = useMemo(
    () => buildIdItemMap(supplierOptionsQuery.data ?? [], (s) => s.id),
    [supplierOptionsQuery.data],
  )
  const warehouseItems = useMemo(
    () => buildIdItemMap(warehouseOptionsQuery.data ?? [], (w) => w.id),
    [warehouseOptionsQuery.data],
  )
  const skuItems = useMemo(
    () => buildIdItemMap(skuOptionsQuery.data ?? [], (s) => s.id),
    [skuOptionsQuery.data],
  )

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
          description="采购订单详情接口不可用：GET /api/purchases/{id}"
        />
      </div>
    )
  }

  const order = query.data.order
  const items = query.data.items
  const itemColumns = buildItemColumns(skuItems)

  const supplier = supplierItems.get(String(order.supplier_id))
  const supplierText = supplier ? `${supplier.name}（${supplier.code}）` : `供应商 #${order.supplier_id}`
  const warehouse = warehouseItems.get(String(order.warehouse_id))
  const warehouseText = warehouse ? `${warehouse.name}（${warehouse.code}）` : `仓库 #${order.warehouse_id}`
  const statusTag = PO_STATUS_TAG[order.status]

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={order.po_no}
        status={toStatusKey(order.status)}
        statusLabel={statusTag?.label}
        statusSemantic={statusTag?.semantic}
        onBack={() => navigate('/purchases')}
        summary={
          <SfSummaryBar
            items={[
              { label: '供应商', value: supplierText },
              { label: '仓库', value: warehouseText },
              { label: '金额', value: formatMoney(order.total_amount) },
              { label: '明细行数', value: formatNumber(items.length) },
            ]}
          />
        }
      />
      <Flex vertical gap={16}>
        <SfDetailSection title="基础信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'poNo', label: '采购单号', children: order.po_no },
              { key: 'supplier', label: '供应商', children: supplierText },
              { key: 'warehouse', label: '仓库', children: warehouseText },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(order.created_at) },
              { key: 'approvedAt', label: '审核时间', children: formatDateTime(order.approved_at) },
              { key: 'receivedAt', label: '收货完成时间', children: formatDateTime(order.received_at) },
              { key: 'completedAt', label: '完成时间', children: formatDateTime(order.completed_at) },
              { key: 'cancelledAt', label: '取消时间', children: formatDateTime(order.cancelled_at) },
              { key: 'remark', label: '备注', children: order.remark || '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<PurchaseOrderItem>
            size="small"
            rowKey="id"
            columns={itemColumns}
            dataSource={items}
            pagination={false}
            scroll={{ x: 1130 }}
            locale={{ emptyText: () => <SfEmpty description="该采购订单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(order)} emptyText="该采购订单暂无流程节点记录" />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
