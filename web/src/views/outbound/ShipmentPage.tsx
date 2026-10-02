import { useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { outboundApi, type ShipmentItem, type ShipmentQuery, type ShipmentStatus } from '@/api/outbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 状态选项与 types/status.ts 出库状态注册表一致（business-flow.md §8.5：含异常分支） */
const STATUS_OPTIONS: Array<{ label: string; value: ShipmentStatus }> = [
  { label: '待发货', value: 'pending_shipment' },
  { label: '已发货', value: 'shipped' },
  { label: '运输中', value: 'in_transit' },
  { label: '已签收', value: 'signed' },
  { label: '发货异常', value: 'shipment_exception' },
]

const COLUMNS: ColumnsType<ShipmentItem> = [
  { title: '发货单号', dataIndex: 'shipmentNo', width: 160, fixed: 'left' },
  { title: '出库单号', dataIndex: 'outboundNo', width: 160 },
  { title: '物流公司', dataIndex: 'carrierName', width: 120, render: (v?: string) => v ?? '-' },
  { title: '物流单号', dataIndex: 'trackingNo', width: 150, render: (v?: string) => v ?? '-' },
  { title: '发货仓', dataIndex: 'warehouseName', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '包裹数量',
    dataIndex: 'packageCount',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '发货人', dataIndex: 'shipperName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '发货时间',
    dataIndex: 'shippedAt',
    width: 170,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 110,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 170,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 发货管理（/shipment，business-flow.md §8.5；GET /api/outbound/shipments，后端未交付呈统一错误态） */
export default function ShipmentPage() {
  const [params, setParams] = useState<ShipmentQuery>({})
  const list = usePagedList<ShipmentItem, ShipmentQuery>({
    queryKey: ['outbound', 'shipments'],
    fetch: (q) => outboundApi.shipments(q),
    params,
  })

  return (
    <div className="sf-page">
      <SfPageHeader
        title="发货管理"
        subtitle="发货单：物流信息与签收跟踪（发货完成触发库存正式扣减）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            {
              name: 'keyword',
              label: '关键词',
              control: 'input',
              placeholder: '发货单号 / 出库单号 / 物流单号',
            },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={(values) => {
            setParams(values as ShipmentQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<ShipmentItem>
          storageKey="outbound-shipment"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有发货单"
          scrollX={1350}
        />
      </Card>
    </div>
  )
}
