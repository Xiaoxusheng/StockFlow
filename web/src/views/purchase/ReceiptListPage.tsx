import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  purchaseApi,
  type ReceiptItem,
  type ReceiptQuery,
  type ReceiptStatus,
} from '@/api/purchase'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 business-flow.md §2.1/§3.2 采购到货流程（收货 → 质检 → 上架 → 入库完成）一致 */
const STATUS_OPTIONS: Array<{ label: string; value: ReceiptStatus }> = [
  { label: '收货中', value: 'receiving' },
  { label: '待质检', value: 'pending_inspection' },
  { label: '质检中', value: 'inspecting' },
  { label: '已质检', value: 'inspected' },
  { label: '待上架', value: 'pending_putaway' },
  { label: '已上架', value: 'putaway_completed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<ReceiptItem> = [
  { title: '收货单号', dataIndex: 'receiptNo', width: 170, fixed: 'left' },
  { title: '采购单号', dataIndex: 'poNo', width: 170, render: (v?: string) => v ?? '-' },
  {
    title: '供应商',
    dataIndex: 'supplierName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  {
    title: '应收数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已收数量',
    dataIndex: 'receivedQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
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

/** 收货列表（/purchases/receipts；GET /api/purchases/receipts 前端先行骨架，后端未交付呈统一错误态） */
export default function ReceiptListPage() {
  const [params, setParams] = useState<ReceiptQuery>({})
  const list = usePagedList<ReceiptItem, ReceiptQuery>({
    queryKey: ['purchase', 'receipts'],
    fetch: (q) => purchaseApi.receipts.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as ReceiptQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="收货"
        subtitle="到货 → 收货 → 质检 → 上架 → 入库完成"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '收货单号 / 采购单号 / 供应商' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<ReceiptItem>
          storageKey="purchase-receipts"
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
          emptyText="当前筛选条件下没有收货单"
          scrollX={1090}
        />
      </Card>
    </div>
  )
}
