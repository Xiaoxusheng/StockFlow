import { useMemo } from 'react'
import { Button, Descriptions, Flex, Typography } from 'antd'
import { ArrowLeftOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  inboundApi,
  type InboundOrder,
  type InboundOrderItem,
  type InboundOrderStatus,
  type InboundSourceType,
} from '@/api/inbound'
import { buildSkuMaps, buildWarehouseMaps, fetchSkuOptions, fetchWarehouseOptions } from '@/api/options'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfTable } from '@/components/table/SfTable'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import { InboundOrderActions } from './InboundOrderActions'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 入库来源类型文案（api/inbound.ts InboundSourceType＝迁移 CHECK 值域；与列表页同源，本单元仅两文件故各自维护） */
const SOURCE_TYPE_LABEL: Record<InboundSourceType, string> = {
  PURCHASE: '采购入库',
  OTHER: '其他入库',
}

/**
 * 入库单七态 → SfStatusTag（models.go:27-33；待质检/待上架未入全局注册表，
 * 经 label/semantic 兜底传参，与列表页/盘点中心 CountStatusTag 同模式）。
 */
const INBOUND_STATUS_TAG: Record<InboundOrderStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  RECEIVING: { key: 'receiving', label: '收货中', semantic: 'processing' },
  AWAITING_QC: { key: 'awaiting_qc', label: '待质检', semantic: 'pending' },
  AWAITING_PUTAWAY: { key: 'awaiting_putaway', label: '待上架', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
  CLOSED: { key: 'closed', label: '已关闭', semantic: 'neutral' },
}

/**
 * 单据状态 → Timeline 当前所处环节（models.go:83-91 状态机：
 * DRAFT→RECEIVING→AWAITING_QC→AWAITING_PUTAWAY→COMPLETED；CANCELLED 仅自 DRAFT、
 * CLOSED 仅自 RECEIVING，故取消/差额关闭都停在收货环节）。
 * 时间列落库口径见 purchase/repository.go:431-433：received_at=收货完成、
 * inspected_at=质检完成、putaway_at/completed_at=上架/单据完成。
 */
const STATUS_NODE_STATE: Record<InboundOrderStatus, { node: string; status?: string; label?: string; semantic?: StatusSemantic }> = {
  DRAFT: { node: 'create', status: 'draft' },
  RECEIVING: { node: 'receive', status: 'receiving' },
  AWAITING_QC: { node: 'inspect', label: '待质检', semantic: 'pending' },
  AWAITING_PUTAWAY: { node: 'putaway', label: '待上架', semantic: 'pending' },
  COMPLETED: { node: 'complete', status: 'completed' },
  CANCELLED: { node: 'receive', label: '已取消', semantic: 'neutral' },
  CLOSED: { node: 'receive', label: '已关闭', semantic: 'neutral' },
}

/** 明细行数量合计（裸模型无单据级汇总列，仅作展示聚合，业务口径以后端为准） */
function sumQty(items: InboundOrderItem[], key: 'qty' | 'qty_received'): number {
  return items.reduce((acc, item) => acc + Number(item[key] ?? 0), 0)
}

/** Timeline 节点组装：有时间的环节视为已完成，当前环节展示单据状态，其余未开始 */
function buildSteps(order: InboundOrder, totals: { qty: number; received: number }): SfTimelineStep[] {
  const current = STATUS_NODE_STATE[order.status]
  const nodes: Array<{ key: string; title: string; time?: string | null; remark?: string }> = [
    { key: 'create', title: '创建', time: order.created_at },
    {
      key: 'receive',
      title: '收货',
      time: order.received_at,
      // 部分收货进度（business-flow.md §3.3：每次收货累计，收齐或关闭为止）
      remark: `已收货 ${formatNumber(totals.received)} / 计划 ${formatNumber(totals.qty)}`,
    },
    { key: 'inspect', title: '质检', time: order.inspected_at },
    { key: 'putaway', title: '上架', time: order.putaway_at },
    { key: 'complete', title: '完成', time: order.completed_at },
  ]
  return nodes.map((node) => {
    const isCurrent = current?.node === node.key && !node.time
    return {
      key: node.key,
      title: node.title,
      time: node.time,
      remark: node.remark,
      status: node.time ? 'completed' : isCurrent ? current?.status : undefined,
      statusLabel: node.time || !isCurrent ? undefined : current?.label,
      statusSemantic: node.time || !isCurrent ? undefined : current?.semantic,
    }
  })
}

/** 入库单详情（/inbound/:id，frontend.md §7 结构；GET /api/inbounds/{id} 返回 {order,items}，
 * service_inbound.go:336-356；明细为 InboundItem 裸模型 snake_case，models.go:187-199）。
 * Header 操作区（编辑入口/取消/差额关闭）由 InboundOrderActions 承载，
 * 成功后 refetch 详情并失效列表缓存。 */
export default function InboundDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: ['inbound', 'detail', id],
    queryFn: () => inboundApi.detail(id as string),
    enabled: Boolean(id),
  })

  /** 操作成功：详情 refetch + 列表缓存失效（usePagedList queryKey 同步刷新） */
  const handleChanged = () => {
    query.refetch()
    queryClient.invalidateQueries({ queryKey: ['inbound', 'orders'] })
  }

  // SKU options（GET /api/skus）：sku_id→编码/商品名称本地映射；仓库 options：warehouse_id→名称。
  // 拉取失败降级为 ID 展示，不阻塞详情（api/options.ts 约定）
  const skuOptions = useQuery({
    queryKey: ['inbound', 'options', 'skus'],
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: ['inbound', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseMaps = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []),
    [warehouseOptions.data],
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
        <SfError error={query.error} onRetry={query.refetch} description="入库单详情加载失败" />
      </div>
    )
  }

  const order = query.data.order
  const lines = query.data.items ?? []
  const totals = { qty: sumQty(lines, 'qty'), received: sumQty(lines, 'qty_received') }
  const statusMeta = INBOUND_STATUS_TAG[order.status]
  const whName = warehouseMaps.name.get(String(order.warehouse_id))
  const whCode = warehouseMaps.code.get(String(order.warehouse_id))
  const warehouseText = whName ? (whCode ? `${whName}（${whCode}）` : whName) : `#${String(order.warehouse_id)}`

  const ITEM_COLUMNS: ColumnsType<InboundOrderItem> = [
    { title: '行号', dataIndex: 'line_no', width: 70 },
    {
      title: 'SKU 编码',
      key: 'sku_code',
      dataIndex: 'sku_id',
      width: 140,
      render: (v: number) => skuMaps.code.get(String(v)) ?? `#${String(v)}`,
    },
    {
      title: '商品名称',
      key: 'sku_name',
      dataIndex: 'sku_id',
      width: 200,
      ellipsis: true,
      render: (v: number) => {
        const name = skuMaps.name.get(String(v)) ?? `#${String(v)}`
        return (
          <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '计划数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已收数量',
      dataIndex: 'qty_received',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已质检数量',
      dataIndex: 'qty_inspected',
      width: 110,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已上架数量',
      dataIndex: 'qty_putaway',
      width: 110,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    { title: '备注', dataIndex: 'remark', width: 140, ellipsis: true, render: (v: string) => v || '-' },
  ]

  return (
    <div className="sf-page">
      <SfDetailHeader
        code={order.inbound_no}
        status={statusMeta?.key}
        statusLabel={statusMeta?.label}
        statusSemantic={statusMeta?.semantic}
        summary={
          <SfSummaryBar
            items={[
              { label: '入库类型', value: SOURCE_TYPE_LABEL[order.source_type] ?? order.source_type },
              { label: '仓库', value: warehouseText },
              { label: '计划数量', value: formatNumber(totals.qty) },
              { label: '已收数量', value: formatNumber(totals.received) },
              { label: 'SKU 数', value: formatNumber(lines.length) },
            ]}
          />
        }
        actions={
          <>
            <InboundOrderActions order={order} onChanged={handleChanged} />
            <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/inbound')}>
              返回列表
            </Button>
          </>
        }
      />
      <Flex vertical gap={16}>
        <SfDetailSection title="基础信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'inboundNo', label: '入库单号', children: order.inbound_no },
              {
                key: 'sourceType',
                label: '入库类型',
                children: SOURCE_TYPE_LABEL[order.source_type] ?? order.source_type,
              },
              { key: 'warehouse', label: '仓库', children: warehouseText },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(order.created_at) },
              { key: 'remark', label: '备注', children: order.remark || '-', span: 2 },
            ]}
          />
        </SfDetailSection>
        <SfDetailSection title="商品明细">
          <SfTable<InboundOrderItem>
            variant="nested"
            rowKey="id"
            columns={ITEM_COLUMNS}
            dataSource={lines}
            scroll={{ x: 970 }}
            emptyText="该入库单暂无商品明细"
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(order, totals)} emptyText="该入库单暂无流程节点记录" />
        </SfDetailSection>
        <SfDetailSection title="关联单据">
          <Descriptions
            bordered
            size="small"
            column={1}
            items={[
              {
                key: 'sourceType',
                label: '来源类型',
                children: SOURCE_TYPE_LABEL[order.source_type] ?? order.source_type,
              },
              {
                key: 'sourceNo',
                label: '来源单号',
                children: order.source_no || '暂无来源单据',
              },
            ]}
          />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
