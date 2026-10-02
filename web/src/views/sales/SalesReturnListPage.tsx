import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  salesApi,
  type SalesReturnItem,
  type SalesReturnQuery,
  type SalesReturnStatus,
} from '@/api/sales'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 business-flow.md §9.1 销售退货流程（退货申请 → 审核 → 收货 → 质检 → 正常库存/不良品）一致 */
const STATUS_OPTIONS: Array<{ label: string; value: SalesReturnStatus }> = [
  { label: '草稿', value: 'draft' },
  { label: '待审核', value: 'pending_review' },
  { label: '已审核', value: 'approved' },
  { label: '收货中', value: 'receiving' },
  { label: '待质检', value: 'pending_inspection' },
  { label: '已质检', value: 'inspected' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<SalesReturnItem> = [
  { title: '退货单号', dataIndex: 'returnNo', width: 170, fixed: 'left' },
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
    title: '退货数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已收货',
    dataIndex: 'receivedQty',
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

/** 销售退货列表（/sales/returns；GET /api/sales/returns 前端先行骨架，后端未交付呈统一错误态）。
 * 退货创建页本批不交付（退货单创建入口随退货域后端契约冻结后统一建设），不保留无行为的假入口。 */
export default function SalesReturnListPage() {
  const [params, setParams] = useState<SalesReturnQuery>({})
  const list = usePagedList<SalesReturnItem, SalesReturnQuery>({
    queryKey: ['sales', 'returns'],
    fetch: (q) => salesApi.returns.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SalesReturnQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="销售退货"
        subtitle="退货申请 → 审核 → 收货 → 质检"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '退货单号 / 销售单号 / 客户' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<SalesReturnItem>
          storageKey="sales-returns"
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
          emptyText="当前筛选条件下没有销售退货单"
          scrollX={1070}
        />
      </Card>
    </div>
  )
}
