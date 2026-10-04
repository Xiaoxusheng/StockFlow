import { useMemo } from 'react'
import { Button, Descriptions, Flex, Table } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  salesApi,
  type SalesOrder,
  type SalesOrderItem,
  type SalesOrderStatus,
} from '@/api/sales'
import {
  buildCustomerMaps,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchCustomerOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { EMPTY_TEXT, formatDateTime, formatMoney, formatNumber } from '@/utils/format'

/**
 * 销售订单状态 → SfStatusTag 兜底映射（8 态值域 internal/sales/models.go:240-248，
 * 迁移 CHECK 同源；与 SalesOrderListPage 同口径：大写原始值不命中 types/status.ts
 * 注册表（resolveStatus 精确匹配小写键），label/semantic 兜底接管；不改注册表）。
 */
const SALES_ORDER_STATUS_TAG: Record<SalesOrderStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  REJECTED: { label: '已驳回', semantic: 'danger' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'processing' },
  SHIPPED_ALL: { label: '全部发货', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 单据状态 → Timeline 当前环节（soTransitions 状态机 models.go:250-260；
 * APPROVED=已审核待出库，COMPLETED 经差额关闭达成 service.go:648-706） */
const STATUS_NODE_STATE: Partial<
  Record<SalesOrderStatus, { node: string; status?: SalesOrderStatus; label?: string; semantic?: StatusSemantic }>
> = {
  DRAFT: { node: 'create', status: 'DRAFT' },
  PENDING_APPROVAL: { node: 'review', status: 'PENDING_APPROVAL' },
  APPROVED: { node: 'ship', label: '待发货', semantic: 'pending' },
  PARTIAL_SHIPPED: { node: 'ship', status: 'PARTIAL_SHIPPED' },
  SHIPPED_ALL: { node: 'ship', status: 'SHIPPED_ALL' },
  COMPLETED: { node: 'complete', status: 'COMPLETED' },
  REJECTED: { node: 'review', status: 'REJECTED' },
  CANCELLED: { node: 'cancel', status: 'CANCELLED' },
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始
 * （时间字段 models.go:14-33：created_at/approved_at/shipped_at/completed_at/cancelled_at；
 * shipped_at 首次发货迁移即落，service_ship.go:267） */
function buildSteps(order: SalesOrder): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[order.status]
  const nodes: Array<{ key: string; title: string; time: string | null; remark?: string }> = [
    { key: 'create', title: '创建', time: order.created_at },
    { key: 'review', title: '审核', time: order.approved_at },
    {
      key: 'ship',
      title: '发货',
      time: order.shipped_at,
      remark: '审核通过即触发库存预占（inventory-rules.md §4）；允许部分发货，进度见明细行已发货数量',
    },
    { key: 'complete', title: '完成', time: order.completed_at },
  ]
  if (order.status === 'CANCELLED') {
    nodes.push({ key: 'cancel', title: '取消', time: order.cancelled_at })
  }
  return nodes.map((node) => {
    const isCurrent = current?.node === node.key && !node.time
    const currentTag = current?.status ? SALES_ORDER_STATUS_TAG[current.status] : undefined
    return {
      key: node.key,
      title: node.title,
      time: node.time,
      remark: node.remark,
      // 已发生环节走注册表 completed 键（SfTimeline 直传 status 命中 types/status.ts）；
      // 当前环节大写枚举不命中注册表，改走 statusLabel/statusSemantic 兜底通道（SfTimeline.tsx:69-73）
      status: node.time ? 'completed' : undefined,
      statusLabel: !isCurrent ? undefined : current?.label ?? currentTag?.label,
      statusSemantic: !isCurrent ? undefined : current?.semantic ?? currentTag?.semantic,
    }
  })
}

/** 销售订单详情（/sales/:id，GET /api/sales/{id}，internal/sales/handler.go:156-166 返回嵌套 {order, items}）。
 * 出参为裸 ID 与 sku_id（无联表编码/名称），经基础资料 options 端点本地映射补充，
 * 映射失败降级为 ID /「-」，不造假数据（api/options.ts 既有模式）。 */
export default function SalesOrderDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['sales', 'orders', id],
    queryFn: () => salesApi.orders.detail(id as string),
    enabled: Boolean(id),
  })

  // sku_id → 编码/名称、customer_id/warehouse_id → 名称映射（options 一次取全，失败降级不阻塞详情）
  const customersQuery = useQuery({
    queryKey: ['options', 'customers'],
    queryFn: fetchCustomerOptions,
  })
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skusQuery = useQuery({
    queryKey: ['options', 'skus'],
    queryFn: fetchSkuOptions,
  })
  const customerMaps = useMemo(
    () => buildCustomerMaps(customersQuery.data ?? []),
    [customersQuery.data],
  )
  const warehouseMaps = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []),
    [warehousesQuery.data],
  )
  const skuMaps = useMemo(() => buildSkuMaps(skusQuery.data ?? []), [skusQuery.data])

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
          description="销售订单详情接口不可用：GET /api/sales/{id}"
        />
      </div>
    )
  }

  const { order, items } = query.data
  const statusMeta = SALES_ORDER_STATUS_TAG[order.status]

  /** 明细行 sku_id → 编码/名称（options 映射，失败降级 SKU #ID /「-」） */
  const skuCodeOf = (item: SalesOrderItem): string =>
    skuMaps.code.get(idKey(item.sku_id)) ?? `SKU #${String(item.sku_id)}`
  const skuNameOf = (item: SalesOrderItem): string =>
    skuMaps.name.get(idKey(item.sku_id)) ?? EMPTY_TEXT

  const columns: ColumnsType<SalesOrderItem> = [
    { title: '行号', dataIndex: 'line_no', width: 60 },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 140,
      render: (_: unknown, record: SalesOrderItem) => skuCodeOf(record),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: SalesOrderItem) => skuNameOf(record),
    },
    {
      title: '数量',
      dataIndex: 'qty',
      width: 100,
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
    {
      title: '已预占',
      dataIndex: 'qty_allocated',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已发货',
      dataIndex: 'qty_shipped',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '行备注',
      dataIndex: 'remark',
      width: 160,
      ellipsis: true,
      render: (v: string) => v || EMPTY_TEXT,
    },
  ]

  const customerName = customerMaps.name.get(idKey(order.customer_id)) ?? String(order.customer_id)
  const customerCode = customerMaps.code.get(idKey(order.customer_id))
  const warehouseName = warehouseMaps.name.get(idKey(order.warehouse_id)) ?? String(order.warehouse_id)
  const warehouseCode = warehouseMaps.code.get(idKey(order.warehouse_id))

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={order.so_no}
        status={order.status}
        statusLabel={statusMeta?.label}
        statusSemantic={statusMeta?.semantic}
        summary={
          <SfSummaryBar
            items={[
              { label: '客户', value: customerName },
              { label: '仓库', value: warehouseName },
              { label: '明细行数', value: formatNumber(items.length) },
              { label: '金额', value: formatMoney(order.total_amount) },
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
              { key: 'soNo', label: '销售单号', children: order.so_no },
              {
                key: 'customer',
                label: '客户',
                children: customerCode ? `${customerName}（${customerCode}）` : customerName,
              },
              {
                key: 'warehouse',
                label: '仓库',
                children: warehouseCode ? `${warehouseName}（${warehouseCode}）` : warehouseName,
              },
              { key: 'deliveryMethod', label: '配送方式', children: order.delivery_method || EMPTY_TEXT },
              {
                key: 'shippingAddress',
                label: '收货地址',
                children: order.shipping_address || EMPTY_TEXT,
                span: 2,
              },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(order.created_at) },
              { key: 'approvedAt', label: '审核时间', children: formatDateTime(order.approved_at) },
              { key: 'shippedAt', label: '发货时间', children: formatDateTime(order.shipped_at) },
              { key: 'completedAt', label: '完成时间', children: formatDateTime(order.completed_at) },
              { key: 'cancelledAt', label: '取消时间', children: formatDateTime(order.cancelled_at) },
              { key: 'remark', label: '备注', children: order.remark || EMPTY_TEXT, span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <Table<SalesOrderItem>
            size="small"
            rowKey="id"
            columns={columns}
            dataSource={items}
            pagination={false}
            scroll={{ x: 1070 }}
            locale={{ emptyText: () => <SfEmpty description="该销售订单暂无商品明细" /> }}
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(order)} emptyText="该销售订单暂无流程节点记录" />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
