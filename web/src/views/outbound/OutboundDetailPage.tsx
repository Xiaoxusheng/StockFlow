import { useMemo } from 'react'
import { Button, Descriptions, Flex, Typography, message } from 'antd'
import { RetweetOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate, useParams } from 'react-router'
import {
  ALLOCATION_EXECUTE_PERMISSION,
  outboundApi,
  type CheckTask,
  type CheckTaskStatus,
  type OutboundAllocationRecord,
  type OutboundOrder,
  type OutboundOrderItem,
  type OutboundOrderStatus,
  type OutboundReallocatePayload,
  type PackingRecord,
  type PickTask,
  type PickTaskStatus,
  type Shipment,
  type ShipmentStatus,
} from '@/api/outbound'
import { DateCell } from '@/components/table/cells'
import { resolveErrorMessage } from '@/api/client'
import { toStatusKey } from '@/api/masterdata'
import {
  buildIdItemMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey, SKU_OPTIONS_KEY, WAREHOUSE_OPTIONS_KEY, BIN_OPTIONS_KEY } from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfDetailHeader } from '@/components/common/SfDetailHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfRelationNav } from '@/components/common/SfRelationNav'
import { SfTable } from '@/components/table/SfTable'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTimeline, type SfTimelineStep } from '@/components/common/SfTimeline'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 出库单状态 → SfStatusTag（与 OutboundPage 同映射；PARTIAL_SHIPPED/SHIPPED_ALL 注册表
 * 暂无键，以 label/semantic 兜底） */
const OB_STATUS_TAG: Record<OutboundOrderStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING_ALLOCATE: { label: '待分配', semantic: 'pending' },
  ALLOCATED: { label: '已分配', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  CHECKED: { label: '已复核', semantic: 'success' },
  PACKED: { label: '已打包', semantic: 'success' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'warning' },
  SHIPPED_ALL: { label: '已发货', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
  CLOSED: { label: '已关闭', semantic: 'neutral' },
}

/** 拣货任务状态 → SfStatusTag（models.go:294-301；PICKING 值域保留、状态机不使用） */
const PICK_STATUS_TAG: Record<PickTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待领取', semantic: 'pending' },
  CLAIMED: { label: '已领取', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  EXCEPTION: { label: '拣货异常', semantic: 'danger' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

function PickStatusTag({ status }: { status: PickTaskStatus }) {
  const meta = PICK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 复核任务状态 → SfStatusTag（models.go:315-319：PENDING/DONE/EXCEPTION，无 CLAIMED——领取为原子指派） */
const CHECK_STATUS_TAG: Record<CheckTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待复核', semantic: 'pending' },
  DONE: { label: '已复核', semantic: 'success' },
  EXCEPTION: { label: '复核异常', semantic: 'danger' },
}

function CheckStatusTag({ status }: { status: CheckTaskStatus }) {
  const meta = CHECK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 发货单状态 → SfStatusTag（models.go:325-331：PENDING→SHIPPED 正式扣减库存，后续为纯记录流转） */
const SHIPMENT_STATUS_TAG: Record<ShipmentStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待发货', semantic: 'pending' },
  SHIPPED: { label: '已发货', semantic: 'success' },
  IN_TRANSIT: { label: '运输中', semantic: 'processing' },
  SIGNED: { label: '已签收', semantic: 'success' },
  ABNORMAL: { label: '异常', semantic: 'danger' },
}

function ShipmentStatusTag({ status }: { status: ShipmentStatus }) {
  const meta = SHIPMENT_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/**
 * 单据状态 → 当前所处环节（business-flow.md §8 分配→拣货→复核→打包→发货）。
 * 单据仅存各环节完成时间（picked_at/checked_at/packed_at/shipped_at），有时间的环节视为
 * 已完成；「已分配/已拣货/已复核/已打包」表示上一环节刚完成，映射为下一环节的待处理状态；
 * CANCELLED/CLOSED 无当前环节，未完成环节由 SfTimeline 兜底为「未开始」。
 */
const CURRENT_NODE: Partial<Record<OutboundOrderStatus, { node: string; status: string }>> = {
  PENDING_ALLOCATE: { node: 'pick', status: 'pending_pick' },
  ALLOCATED: { node: 'pick', status: 'pending_pick' },
  PICKING: { node: 'pick', status: 'picking' },
  PICKED: { node: 'check', status: 'pending_check' },
  CHECKED: { node: 'pack', status: 'pending_pack' },
  PACKED: { node: 'ship', status: 'pending_shipment' },
}

/** Timeline 节点组装：有时间的环节已完成，当前环节展示单据状态，其余未开始 */
function buildSteps(outbound: OutboundOrder): SfTimelineStep[] {
  const current = CURRENT_NODE[outbound.status]
  const nodes: Array<{ key: string; title: string; time: string | null }> = [
    { key: 'pick', title: '拣货', time: outbound.picked_at },
    { key: 'check', title: '复核', time: outbound.checked_at },
    { key: 'pack', title: '打包', time: outbound.packed_at },
    { key: 'ship', title: '发货', time: outbound.shipped_at },
  ]
  return nodes.map((node) => {
    const isCurrent = current?.node === node.key && !node.time
    return {
      key: node.key,
      title: node.title,
      time: node.time,
      status: node.time ? 'completed' : isCurrent ? current?.status : undefined,
    }
  })
}

function renderQty(value: number) {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/**
 * 出库单详情（/outbound/:no，GET /api/outbounds/{no} 后端 M2 已交付）。
 * 路由参数名 :id（router/index.tsx），#17 修复后跳转传入的值为出库单号。
 * 响应为任务族全量 {outbound, items, allocations, picks, checks, packages, shipments}
 * （internal/sales/handler.go getOutbound）：Header/时间线用单据各环节时间，
 * 明细区渲染出库明细行（各环节累计进度）+ 分配记录（重新分配经 POST /api/allocations）
 * + 拣货/复核任务与包裹/发货记录（frontend.md §7）。
 */
export default function OutboundDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)

  const query = useQuery({
    queryKey: ['outbound', 'detail', id],
    queryFn: () => outboundApi.get(id as string),
    enabled: Boolean(id),
  })

  // SKU / 仓库 ID → 编码/名称（拣货任务为裸模型无联表，映射失败降级为 ID）
  const skuOptions = useQuery({
    queryKey: SKU_OPTIONS_KEY,
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: WAREHOUSE_OPTIONS_KEY,
    queryFn: fetchWarehouseOptions,
  })
  // 库位 ID → 编码（分配记录 bin_id 为裸 ID；失败降级为 #ID，不阻塞详情）
  const binOptions = useQuery({
    queryKey: BIN_OPTIONS_KEY,
    queryFn: fetchBinOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const binItems = useMemo(
    () => buildIdItemMap(binOptions.data ?? [], (b) => b.id),
    [binOptions.data],
  )

  // 重新分配（POST /api/allocations，sales:allocation:execute；整单 line_no=0）。
  // 后端约束：仅 ALLOCATED/PICKING 且未拣货未发货的行可重分配（service_ship.go:531-555），
  // 权限/状态不满足时按钮不出现；已拣行后端仍会拒绝（前端权限仅是体验优化）。
  const reallocateMutation = useMutation({
    mutationFn: (payload: OutboundReallocatePayload) =>
      outboundApi.allocations.reallocate(payload),
    onSuccess: (res) => {
      messageApi.success(`重新分配完成，本次生成 ${res.allocations.length} 条分配记录`)
      queryClient.invalidateQueries({ queryKey: ['outbound', 'detail', id] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  if (!id) {
    return (
      <div className="sf-page">
        <SfError error={new Error('URL 缺少出库单号')} />
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
          description="出库单详情接口不可用：GET /api/outbounds/{出库单号}"
        />
      </div>
    )
  }

  const detail = query.data
  const outbound = detail.outbound
  const items = detail.items ?? []
  const allocations = detail.allocations ?? []
  const picks = detail.picks ?? []
  const checks = detail.checks ?? []
  const packages = detail.packages ?? []
  const shipments = detail.shipments ?? []
  const obMeta = OB_STATUS_TAG[outbound.status]
  const warehouseName =
    warehouseNames.get(idKey(outbound.warehouse_id)) ?? idKey(outbound.warehouse_id)
  // 重新分配仅对 ALLOCATED/PICKING 开放（后端 service_ship.go:531-535 同口径）
  const canReallocate =
    canAccess(user, ALLOCATION_EXECUTE_PERMISSION) &&
    (outbound.status === 'ALLOCATED' || outbound.status === 'PICKING')

  const pickColumns: ColumnsType<PickTask> = [
    { title: '拣货任务号', dataIndex: 'pick_no', width: 160 },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: PickTask) => {
        const name = skuMaps.name.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    { title: '应拣数量', dataIndex: 'qty', width: 100, align: 'right', render: renderQty },
    { title: '已拣数量', dataIndex: 'picked_qty', width: 100, align: 'right', render: renderQty },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: PickTaskStatus) => <PickStatusTag status={v} />,
    },
    { title: '领取人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '拣货时间',
      dataIndex: 'picked_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
    },
  ]

  const itemColumns: ColumnsType<OutboundOrderItem> = [
    { title: '行号', dataIndex: 'line_no', width: 70 },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: OutboundOrderItem) => {
        const name = skuMaps.name.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    { title: '应出数量', dataIndex: 'qty', width: 100, align: 'right', render: renderQty },
    { title: '已拣', dataIndex: 'qty_picked', width: 90, align: 'right', render: renderQty },
    { title: '已复核', dataIndex: 'qty_checked', width: 90, align: 'right', render: renderQty },
    { title: '已打包', dataIndex: 'qty_packed', width: 90, align: 'right', render: renderQty },
    { title: '已发货', dataIndex: 'qty_shipped', width: 90, align: 'right', render: renderQty },
    {
      title: '备注',
      dataIndex: 'remark',
      width: 160,
      ellipsis: true,
      render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
    },
  ]

  // 分配记录列（AllocationRecord 裸模型：batch_id/bin_id 为裸 ID，批次无 options 端点
  // 降级 #ID，库位经 bin options 映射编码；reason 为后端分配理由 JSON 不直接渲染）
  const allocationColumns: ColumnsType<OutboundAllocationRecord> = [
    { title: '行号', dataIndex: 'line_no', width: 70, align: 'right', render: renderQty },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: OutboundAllocationRecord) => {
        const name = skuMaps.name.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    { title: '分配数量', dataIndex: 'qty', width: 100, align: 'right', render: renderQty },
    { title: '分配策略', dataIndex: 'strategy', width: 110, render: (v: string) => v || '-' },
    {
      title: '批次',
      dataIndex: 'batch_id',
      width: 100,
      render: (v: number) => `#${String(v)}`,
    },
    {
      title: '库位',
      dataIndex: 'bin_id',
      width: 130,
      render: (v: number) => binItems.get(idKey(v))?.code ?? `#${String(v)}`,
    },
    {
      title: '分配时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
  ]

  const checkColumns: ColumnsType<CheckTask> = [    { title: '复核任务号', dataIndex: 'check_no', width: 160 },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    { title: '数量', dataIndex: 'qty', width: 90, align: 'right', render: renderQty },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: CheckTaskStatus) => <CheckStatusTag status={v} />,
    },
    {
      title: '复核结果',
      dataIndex: 'result',
      width: 110,
      // result 为空串 = 复核通过；异常为五类中文值域之一（models.go:322）
      render: (v: string) => v || '通过',
    },
    { title: '复核人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '完成时间',
      dataIndex: 'done_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
    },
  ]

  const packageColumns: ColumnsType<PackingRecord> = [
    { title: '包裹号', dataIndex: 'package_no', width: 160 },
    { title: '包材', dataIndex: 'packing_material', width: 120, render: (v: string) => v || '-' },
    {
      title: '尺寸（长×宽×高 mm）',
      key: 'size',
      width: 170,
      render: (_: unknown, record: PackingRecord) => `${record.length} × ${record.width} × ${record.height}`,
    },
    { title: '重量 (kg)', dataIndex: 'weight', width: 100, align: 'right', render: renderQty },
    { title: '承运商', dataIndex: 'carrier', width: 120, render: (v: string) => v || '-' },
    { title: '运单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || '-' },
    {
      title: '打包时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
  ]

  const shipmentColumns: ColumnsType<Shipment> = [
    { title: '发货单号', dataIndex: 'shipment_no', width: 160 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: ShipmentStatus) => <ShipmentStatusTag status={v} />,
    },
    { title: '承运商', dataIndex: 'carrier', width: 120, render: (v: string) => v || '-' },
    { title: '运单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || '-' },
    { title: '包裹数', dataIndex: 'package_count', width: 90, align: 'right', render: renderQty },
    { title: '发货人', dataIndex: 'shipper_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '发货时间',
      dataIndex: 'shipped_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfDetailHeader
        code={outbound.outbound_no}
        status={toStatusKey(outbound.status)}
        statusLabel={obMeta?.label}
        statusSemantic={obMeta?.semantic}
        summary={
          <SfSummaryBar
            items={[
              { label: '出库类型', value: outbound.type || '-' },
              { label: '仓库', value: warehouseName },
              { label: '明细行', value: `${items.length} 行` },
              { label: '拣货任务', value: `${picks.length} 条` },
            ]}
          />
        }
        onBack={() => navigate('/outbound')}
      />
      <Flex vertical gap={16}>
        <SfDetailSection title="基础信息">
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, md: 2, xl: 3 }}
            items={[
              { key: 'outboundNo', label: '出库单号', children: outbound.outbound_no },
              { key: 'soNo', label: '来源销售单号', children: outbound.so_no || '-' },
              { key: 'type', label: '出库类型', children: outbound.type || '-' },
              { key: 'warehouse', label: '仓库', children: warehouseName },
              { key: 'createdAt', label: '创建时间', children: formatDateTime(outbound.created_at) },
              { key: 'cancelledAt', label: '取消时间', children: formatDateTime(outbound.cancelled_at) },
              { key: 'remark', label: '备注', children: outbound.remark || '-', span: 2 },
            ]}
          />
        </SfDetailSection>
                {/* §2.6 上下文导航：出库单 → 拣货 / 复核 / 打包 / 发货 / 来源销售单 / 库存 */}
        <SfDetailSection title="关联业务">
          <SfRelationNav
            entity="outbound"
            context={{
              outbound_no: outbound.outbound_no,
              so_no: outbound.so_no,
              warehouse_id: outbound.warehouse_id,
            }}
          />
        </SfDetailSection>
        <SfDetailSection title="出库明细">
          <SfTable<OutboundOrderItem>
            variant="nested"
            rowKey="id"
            columns={itemColumns}
            dataSource={items}
            scroll={{ x: 1180 }}
            emptyText="该出库单暂无明细行"
          />
        </SfDetailSection>
        <SfDetailSection
          title="分配记录"
          extra={
            canReallocate ? (
              <SfConfirm
                okText="重新分配"
                confirming={reallocateMutation.isPending}
                title="确认重新分配整单库存？"
                description="将释放本单全部旧预占锁并按默认策略重新分配，未完结的拣货任务会被取消；已拣货/已发货的行无法重分配（后端校验）。"
                onConfirm={() =>
                  reallocateMutation.mutate({ outbound_no: outbound.outbound_no, line_no: 0 })
                }
              >
                <Button danger icon={<RetweetOutlined />} size="small">
                  重新分配
                </Button>
              </SfConfirm>
            ) : undefined
          }
        >
          <SfTable<OutboundAllocationRecord>
            variant="nested"
            rowKey="id"
            columns={allocationColumns}
            dataSource={allocations}
            scroll={{ x: 1060 }}
            emptyText="该出库单暂无分配记录（审核预占后生成）"
          />
        </SfDetailSection>
        <SfDetailSection title="拣货任务">
          <SfTable<PickTask>
            variant="nested"
            rowKey="id"
            columns={pickColumns}
            dataSource={picks}
            scroll={{ x: 1050 }}
            emptyText="该出库单暂无拣货任务（分配生成后展示）"
          />
        </SfDetailSection>
        <SfDetailSection title="复核记录">
          <SfTable<CheckTask>
            variant="nested"
            rowKey="id"
            columns={checkColumns}
            dataSource={checks}
            scroll={{ x: 1010 }}
            emptyText="该出库单暂无复核任务（拣货确认后生成）"
          />
        </SfDetailSection>
        <SfDetailSection title="包裹记录">
          <SfTable<PackingRecord>
            variant="nested"
            rowKey="id"
            columns={packageColumns}
            dataSource={packages}
            scroll={{ x: 1170 }}
            emptyText="该出库单暂无打包记录"
          />
        </SfDetailSection>
        <SfDetailSection title="发货记录">
          <SfTable<Shipment>
            variant="nested"
            rowKey="id"
            columns={shipmentColumns}
            dataSource={shipments}
            scroll={{ x: 1060 }}
            emptyText="该出库单暂无发货单"
          />
        </SfDetailSection>
        <SfDetailSection title="业务流程">
          <SfTimeline steps={buildSteps(outbound)} emptyText="该出库单暂无流程节点记录" />
        </SfDetailSection>
      </Flex>
    </div>
  )
}
