import { useMemo } from 'react'
import { Card, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundTaskApi,
  type Shipment,
  type ShipmentQuery,
  type ShipmentStatus,
} from '@/api/outbound'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
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

/**
 * 发货管理（GET /api/shipments，后端 M2 已交付）：列表列回对 Shipment 裸模型——
 * 物流信息与签收跟踪（business-flow.md §8.5），发货完成触发库存正式扣减。
 * 仓库 ID 经基础资料 options 本地映射，失败降级为 ID；
 * 发货确认/物流态流转写端点（POST /api/shipments、PUT /api/shipments/{id}/status）
 * 已注册，交互设计不在本轮范围，列表只读。
 */
export default function ShipmentPage() {
  const navigate = useNavigate()
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
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="发货管理"
        subtitle="发货单：物流信息与签收跟踪（发货完成触发库存正式扣减）"
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
          scrollX={1380}
        />
      </Card>
    </div>
  )
}
