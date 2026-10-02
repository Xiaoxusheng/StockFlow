import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { ExportOutlined, PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import {
  salesApi,
  type SalesOutboundItem,
  type SalesOutboundQuery,
  type SalesOutboundStatus,
} from '@/api/sales'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 business-flow.md §7.2 出库流程（销售订单 → 库存分配 → 拣货 → 复核 → 打包 → 发货）一致 */
const STATUS_OPTIONS: Array<{ label: string; value: SalesOutboundStatus }> = [
  { label: '待分配', value: 'pending_allocate' },
  { label: '已分配', value: 'allocated' },
  { label: '拣货中', value: 'picking' },
  { label: '已拣货', value: 'picked' },
  { label: '复核中', value: 'checking' },
  { label: '打包中', value: 'packing' },
  { label: '已打包', value: 'packed' },
  { label: '已发货', value: 'shipped' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<SalesOutboundItem> = [
  { title: '出库单号', dataIndex: 'outNo', width: 170, fixed: 'left' },
  { title: '销售单号', dataIndex: 'soNo', width: 170, render: (v?: string) => v ?? '-' },
  {
    title: '客户',
    dataIndex: 'customerName',
    width: 160,
    ellipsis: true,
    render: (v?: string) =>
      v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-',
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  {
    title: '总数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已发货',
    dataIndex: 'shippedQty',
    width: 100,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 100,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 170,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 销售出库列表（/sales/outbounds；GET /api/sales/outbounds 前端先行骨架，后端未交付呈统一错误态） */
export default function SalesOutboundListPage() {
  const [params, setParams] = useState<SalesOutboundQuery>({})
  const list = usePagedList<SalesOutboundItem, SalesOutboundQuery>({
    queryKey: ['sales', 'outbounds'],
    fetch: (q) => salesApi.outbounds.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SalesOutboundQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库"
        subtitle="库存分配 → 拣货 → 复核 → 打包 → 发货"
        extra={
          <>
            <Button icon={<ExportOutlined />}>导出</Button>
            <Button type="primary" icon={<PlusOutlined />}>
              新建出库单
            </Button>
          </>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '出库单号 / 销售单号 / 客户' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<SalesOutboundItem>
          storageKey="sales-outbounds"
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
          emptyText="当前筛选条件下没有出库单"
          scrollX={1070}
        />
      </Card>
    </div>
  )
}
