import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { Alert, Button, Card, Input, Modal, Select, Tag, Typography, message } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  SHIPMENT_EXECUTE_PERMISSION,
  outboundApi,
  outboundTaskApi,
  type OutboundOrderItem,
  type Shipment,
  type ShipmentQuery,
  type ShipmentStatus,
} from '@/api/outbound'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import { buildSkuMaps, buildWarehouseMaps, fetchSkuOptions, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 发货单状态 → SfStatusTag（models.go:325-331 五值；ABNORMAL 注册表暂无键，以 label/semantic 兜底） */
const SHIP_STATUS_TAG: Record<ShipmentStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待发货', semantic: 'pending' },
  SHIPPED: { label: '已发货', semantic: 'processing' },
  IN_TRANSIT: { label: '运输中', semantic: 'processing' },
  SIGNED: { label: '已签收', semantic: 'success' },
  ABNORMAL: { label: '发货异常', semantic: 'danger' },
}

function ShipStatusTag({ status }: { status: ShipmentStatus }) {
  const meta = SHIP_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listShipments：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: ShipmentStatus }> = [
  { label: '待发货', value: 'PENDING' },
  { label: '已发货', value: 'SHIPPED' },
  { label: '运输中', value: 'IN_TRANSIT' },
  { label: '已签收', value: 'SIGNED' },
  { label: '发货异常', value: 'ABNORMAL' },
]

/** 出库单状态中文（发货弹窗提示用；完整状态列见 OutboundPage OB_STATUS_TAG） */
const OB_STATUS_LABEL: Record<string, string> = {
  PENDING_ALLOCATE: '待分配',
  ALLOCATED: '已分配',
  PICKING: '拣货中',
  PICKED: '已拣货',
  CHECKED: '已复核',
  PACKED: '已打包',
  PARTIAL_SHIPPED: '部分发货',
  SHIPPED_ALL: '全部发货',
  CANCELLED: '已取消',
  CLOSED: '已关闭',
}

/**
 * 物流态人工流转允许的目标（models.go:334-340 shipTransitions 同源；PENDING→SHIPPED
 * 由发货确认完成，本表仅覆盖 SHIPPED 之后的纯记录流转）。
 */
const SHIP_NEXT_STATUS: Partial<Record<ShipmentStatus, ShipmentStatus[]>> = {
  SHIPPED: ['IN_TRANSIT', 'ABNORMAL'],
  IN_TRANSIT: ['SIGNED', 'ABNORMAL'],
}

/** 流转目标状态中文 */
const NEXT_STATUS_LABEL: Partial<Record<ShipmentStatus, string>> = {
  IN_TRANSIT: '运输中',
  SIGNED: '已签收',
  ABNORMAL: '发货异常',
}

/**
 * 发货管理（GET /api/shipments，后端 M2 已交付）：列表列回对 Shipment 裸模型——
 * 物流信息与签收跟踪（business-flow.md §8.5），发货完成触发库存正式扣减。
 * 发货确认（POST /api/shipments，sales:shipment:execute）：出库单须 PACKED（首批）/
 * PARTIAL_SHIPPED（续批，service_ship.go:88）；lines 只传行号 = 发该行全部剩余量
 * （ShipLineInput 无数量字段），缺省 = 全部未发货明细行。幂等键 randomUUID 防双击重提。
 * 物流态流转（PUT /api/shipments/{id}/status）：SHIPPED→IN_TRANSIT/ABNORMAL、
 * IN_TRANSIT→SIGNED/ABNORMAL（shipTransitions），纯记录不触发库存。
 * 仓库 ID 经基础资料 options 本地映射，失败降级为 ID。
 */
export default function ShipmentPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const canShip = canAccess(user, SHIPMENT_EXECUTE_PERMISSION)

  /** 发货确认弹窗开关 / 选中的出库单号 / 勾选的发行（line_no 集合） */
  const [shipOpen, setShipOpen] = useState(false)
  const [shipNo, setShipNo] = useState<string>()
  const [shipLines, setShipLines] = useState<Set<number>>(new Set())
  const [carrier, setCarrier] = useState('')
  const [trackingNo, setTrackingNo] = useState('')
  const [remark, setRemark] = useState('')

  /** 物流态流转弹窗：当前发货单 / 目标状态 */
  const [statusTarget, setStatusTarget] = useState<Shipment | null>(null)
  const [nextStatus, setNextStatus] = useState<ShipmentStatus>()
  const [statusRemark, setStatusRemark] = useState('')

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<Shipment, ShipmentQuery>({
    queryKey: ['outbound', 'shipments'],
    fetch: (q) => outboundTaskApi.shipments.list(q),
    urlSync: true,
  })

  // 仓库 ID → 名称（options.ts：一次取全基础资料，失败降级为 ID）
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])

  /** 可发货出库单（PACKED 首批 / PARTIAL_SHIPPED 续批；合并两组各取前 25） */
  const shippable = useQuery({
    queryKey: ['outbound', 'shippable-orders'],
    queryFn: async () => {
      const [packed, partial] = await Promise.all([
        outboundApi.list({ status: 'PACKED', page: 1, pageSize: 25 }),
        outboundApi.list({ status: 'PARTIAL_SHIPPED', page: 1, pageSize: 25 }),
      ])
      return [...packed.items, ...partial.items]
    },
    enabled: canShip,
  })

  /** 选中出库单的详情（items 供勾选发行） */
  const detail = useQuery({
    queryKey: ['outbound', 'ship-detail', shipNo],
    queryFn: () => outboundApi.get(shipNo!),
    enabled: !!shipNo,
  })
  const detailItems: OutboundOrderItem[] = detail.data?.items ?? []
  const detailStatus = detail.data?.outbound.status
  const shippableOrder = detailStatus === 'PACKED' || detailStatus === 'PARTIAL_SHIPPED'
  /** 可发行 = 尚有剩余量 */
  const shippableLines = detailItems.filter((it) => it.qty_shipped < it.qty)

  /** 详情到位 → 默认勾选全部可发行（整单发货为最常见操作） */
  useEffect(() => {
    if (!detail.data) return
    setShipLines(new Set(detailItems.filter((it) => it.qty_shipped < it.qty).map((it) => it.line_no)))
    // eslint 依赖以 detailItems 派生即可（detail.data 变化 ⇒ items 变化）
  }, [detail.data])

  const resetShipModal = () => {
    setShipNo(undefined)
    setShipLines(new Set())
    setCarrier('')
    setTrackingNo('')
    setRemark('')
  }

  const shipMutation = useMutation({
    mutationFn: (payload: Parameters<typeof outboundTaskApi.shipments.ship>[0]) =>
      outboundTaskApi.shipments.ship(payload),
    onSuccess: (r) => {
      message.success(
        r.replay
          ? `发货请求为幂等重放，发货单 ${r.shipment.shipment_no} 已存在`
          : `发货确认完成：${r.shipment.shipment_no}，本次发货 ${Object.keys(r.shipped).length} 行（库存已扣减）`,
      )
      queryClient.invalidateQueries({ queryKey: ['outbound', 'shipments'] })
      queryClient.invalidateQueries({ queryKey: ['outbound', 'shippable-orders'] })
      setShipOpen(false)
      resetShipModal()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status, remark: r }: { id: Shipment['id']; status: ShipmentStatus; remark?: string }) =>
      outboundTaskApi.shipments.updateStatus(id, { status, ...(r?.trim() ? { remark: r.trim() } : {}) }),
    onSuccess: (sh) => {
      message.success(`发货单 ${sh.shipment_no} 已更新为「${SHIP_STATUS_TAG[sh.status]?.label ?? sh.status}」`)
      queryClient.invalidateQueries({ queryKey: ['outbound', 'shipments'] })
      setStatusTarget(null)
      setNextStatus(undefined)
      setStatusRemark('')
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  const submitShip = () => {
    if (!shipNo) return
    if (shipLines.size === 0) {
      message.warning('请至少勾选一行发货明细')
      return
    }
    shipMutation.mutate({
      outbound_no: shipNo,
      lines: [...shipLines].sort((a, b) => a - b).map((line_no) => ({ line_no })),
      ...(carrier.trim() ? { carrier: carrier.trim() } : {}),
      ...(trackingNo.trim() ? { tracking_no: trackingNo.trim() } : {}),
      ...(remark.trim() ? { remark: remark.trim() } : {}),
      idempotency_key: crypto.randomUUID(),
    })
  }

  const columns: ColumnsType<Shipment> = [
    { title: '发货单号', dataIndex: 'shipment_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: Shipment) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    { title: '物流公司', dataIndex: 'carrier', width: 120, render: (v: string) => v || '-' },
    { title: '物流单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || '-' },
    {
      title: '发货仓',
      dataIndex: 'warehouse_id',
      width: 120,
      render: (v: number) => {
        const name = warehouseNames.get(idKey(v)) ?? idKey(v)
        return (
          <Text style={{ maxWidth: 110 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '包裹数量',
      dataIndex: 'package_count',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    { title: '发货人', dataIndex: 'shipper_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '发货时间',
      dataIndex: 'shipped_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (v: ShipmentStatus) => <ShipStatusTag status={v} />,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    // 物流态流转（纯记录）：仅 SHIPPED/IN_TRANSIT 有后继态（shipTransitions），且须持有 execute 权限
    ...(canShip
      ? [
          {
            title: '操作',
            key: 'actions',
            fixed: 'right' as const,
            width: 100,
            render: (_: unknown, record: Shipment) =>
              (SHIP_NEXT_STATUS[record.status]?.length ?? 0) > 0 ? (
                <Button
                  type="link"
                  size="small"
                  onClick={() => {
                    setStatusTarget(record)
                    setNextStatus(undefined)
                    setStatusRemark('')
                  }}
                >
                  物流态
                </Button>
              ) : (
                <Text type="secondary">-</Text>
              ),
          },
        ]
      : []),
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="发货管理"
        subtitle="发货单：物流信息与签收跟踪（发货完成触发库存正式扣减）"
        extra={
          canShip ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setShipOpen(true)}>
              发货确认
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: idKey(w.id),
              })),
            },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<Shipment>
          storageKey="outbound-shipments"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有发货单"
          emptyAction={
            /* 空状态 CTA：发货确认由本页发起（选 PACKED/PARTIAL_SHIPPED 出库单），有权限时引导直达 */
            canShip ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setShipOpen(true)}>
                发货确认
              </Button>
            ) : undefined
          }
          scrollX={1480}
        />
      </Card>

      {/* 发货确认弹窗：选 PACKED/PARTIAL_SHIPPED 出库单 → 勾选发行（默认全选剩余行）→ 可选物流字段。
          发货按行取全部剩余量（ShipLineInput 仅 line_no），库存正式扣减发生在 PENDING→SHIPPED 事务 */}
      <Modal
        open={shipOpen}
        title="发货确认"
        okText="确认发货"
        okButtonProps={{ disabled: !!shipNo && !shippableOrder, loading: shipMutation.isPending }}
        onCancel={() => {
          setShipOpen(false)
          resetShipModal()
        }}
        onOk={submitShip}
        width={720}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="发货确认将核销库存预占并正式扣减库存，提交前请核对明细"
        />
        <div style={{ marginBottom: 12 }}>
          <div style={{ marginBottom: 4 }}>出库单（已打包 / 部分发货）</div>
          <Select
            showSearch
            placeholder="选择出库单"
            style={{ width: '100%' }}
            value={shipNo}
            loading={shippable.isFetching}
            filterOption={(input, option) =>
              String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
            }
            onChange={(v: string) => {
              setShipNo(v)
              setShipLines(new Set())
            }}
            options={(shippable.data ?? []).map((o) => ({
              label: `${o.outbound_no} · ${OB_STATUS_LABEL[o.status] ?? o.status}（${o.so_no || '-'}）`,
              value: o.outbound_no,
            }))}
            notFoundContent={
              shippable.isError
                ? `出库单列表加载失败：${resolveErrorMessage(shippable.error)}（需 sales:outbound:list）`
                : shippable.isFetching
                  ? '加载中…'
                  : '没有可发货的出库单'
            }
          />
        </div>

        {shipNo && detail.isFetching && <div style={{ padding: '16px 0' }}>明细加载中…</div>}
        {shipNo && detail.isError && (
          <Alert
            type="error"
            showIcon
            style={{ marginBottom: 12 }}
            message="出库单明细加载失败"
            description={`${resolveErrorMessage(detail.error)}（需出库单查看权限 sales:outbound:read）`}
          />
        )}
        {shipNo && detailStatus && !shippableOrder && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message={`出库单当前为「${OB_STATUS_LABEL[detailStatus] ?? detailStatus}」，仅已打包（PACKED）/ 部分发货（PARTIAL_SHIPPED）状态可确认发货`}
          />
        )}
        {shipNo && detailStatus && shippableOrder && (
          <>
            <div style={{ marginBottom: 8, display: 'flex', gap: 8, alignItems: 'center' }}>
              <Tag color="processing">{OB_STATUS_LABEL[detailStatus]}</Tag>
              <Text type="secondary">发货按行取全部剩余量；已勾选 {shipLines.size} / {shippableLines.length} 行</Text>
            </div>
            <div style={{ maxHeight: 260, overflow: 'auto', border: '1px solid var(--sf-border, #f0f0f0)', borderRadius: 6 }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr style={{ textAlign: 'left', color: 'var(--sf-text-secondary, #888)' }}>
                    <th style={{ ...shipThStyle, width: 40 }}></th>
                    <th style={shipThStyle}>行号</th>
                    <th style={shipThStyle}>SKU</th>
                    <th style={{ ...shipThStyle, textAlign: 'right' }}>应发 / 已发</th>
                    <th style={{ ...shipThStyle, textAlign: 'right' }}>本次发货</th>
                  </tr>
                </thead>
                <tbody>
                  {detailItems.map((it) => {
                    const remain = it.qty - it.qty_shipped
                    const disabled = remain <= 0
                    return (
                      <tr key={it.line_no}>
                        <td style={shipTdStyle}>
                          <input
                            type="checkbox"
                            disabled={disabled}
                            checked={shipLines.has(it.line_no)}
                            onChange={(e) => {
                              setShipLines((prev) => {
                                const next = new Set(prev)
                                if (e.target.checked) next.add(it.line_no)
                                else next.delete(it.line_no)
                                return next
                              })
                            }}
                          />
                        </td>
                        <td style={shipTdStyle}>{it.line_no}</td>
                        <td style={shipTdStyle}>{skuMaps.code.get(idKey(it.sku_id)) ?? idKey(it.sku_id)}</td>
                        <td style={{ ...shipTdStyle, textAlign: 'right' }} className="sf-num">
                          {formatNumber(it.qty)} / {formatNumber(it.qty_shipped)}
                        </td>
                        <td style={{ ...shipTdStyle, textAlign: 'right' }} className="sf-num">
                          {disabled ? '-' : formatNumber(remain)}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 12 }}>
              <Input placeholder="物流公司（可选）" value={carrier} onChange={(e) => setCarrier(e.target.value)} />
              <Input placeholder="物流单号（可选）" value={trackingNo} onChange={(e) => setTrackingNo(e.target.value)} />
            </div>
            <Input.TextArea
              rows={2}
              style={{ marginTop: 12 }}
              placeholder="备注（可选）"
              value={remark}
              onChange={(e) => setRemark(e.target.value)}
            />
          </>
        )}
      </Modal>

      {/* 物流态流转弹窗（纯记录，不触发库存）：目标按 shipTransitions 过滤，ABNORMAL 需备注留痕 */}
      <Modal
        open={!!statusTarget}
        title={`物流态流转 · ${statusTarget?.shipment_no ?? ''}`}
        okText="确认流转"
        okButtonProps={{ disabled: !nextStatus, loading: statusMutation.isPending }}
        onCancel={() => setStatusTarget(null)}
        onOk={() => {
          if (!statusTarget || !nextStatus) return
          statusMutation.mutate({ id: statusTarget.id, status: nextStatus, remark: statusRemark })
        }}
      >
        {statusTarget && (
          <>
            <div style={{ marginBottom: 12 }}>
              当前状态：
              <ShipStatusTag status={statusTarget.status} />
            </div>
            <div style={{ marginBottom: 4 }}>流转到</div>
            <Select
              style={{ width: '100%' }}
              placeholder="选择目标状态"
              value={nextStatus}
              onChange={(v: ShipmentStatus) => setNextStatus(v)}
              options={(SHIP_NEXT_STATUS[statusTarget.status] ?? []).map((s) => ({
                label: NEXT_STATUS_LABEL[s] ?? s,
                value: s,
              }))}
            />
            <Input.TextArea
              rows={2}
              style={{ marginTop: 12 }}
              placeholder={nextStatus === 'ABNORMAL' ? '异常说明（建议填写，留痕异常中心）' : '备注（可选）'}
              value={statusRemark}
              onChange={(e) => setStatusRemark(e.target.value)}
            />
          </>
        )}
      </Modal>
    </div>
  )
}

const shipThStyle: CSSProperties = { padding: '8px 12px', borderBottom: '1px solid var(--sf-border, #f0f0f0)', position: 'sticky', top: 0, background: 'var(--sf-bg-container, #fff)' }
const shipTdStyle: CSSProperties = { padding: '6px 12px', borderBottom: '1px solid var(--sf-border, #f0f0f0)' }
