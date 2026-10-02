import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router'
import { purchaseApi, type PurchaseItem, type PurchaseQuery, type PurchaseStatus } from '@/api/purchase'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDate, formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 business-flow.md §2.2 采购订单状态机一致 */
const STATUS_OPTIONS: Array<{ label: string; value: PurchaseStatus }> = [
  { label: '草稿', value: 'draft' },
  { label: '待审核', value: 'pending_review' },
  { label: '已审核', value: 'approved' },
  { label: '部分到货', value: 'partially_received' },
  { label: '到货完成', value: 'received' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<PurchaseItem> = [
  { title: '采购单号', dataIndex: 'poNo', width: 170, fixed: 'left' },
  {
    title: '供应商',
    dataIndex: 'supplierName',
    width: 180,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text>,
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
    title: '已收货',
    dataIndex: 'receivedQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '金额',
    dataIndex: 'totalAmount',
    width: 120,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '预计到货',
    dataIndex: 'expectedArrivalDate',
    width: 110,
    render: (v?: string) => formatDate(v),
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 170,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 采购订单列表（frontend.md 采购中心 /purchases；GET /api/purchases，子路径未冻结） */
export default function PurchaseListPage() {
  const [params, setParams] = useState<PurchaseQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<PurchaseItem, PurchaseQuery>({
    queryKey: ['purchase', 'orders'],
    fetch: (q) => purchaseApi.list(q),
    params,
  })

  // 既有列保持不变，仅追加「详情」行入口（/purchases/:id，无菜单路由）
  const columns: ColumnsType<PurchaseItem> = [
    ...COLUMNS,
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: PurchaseItem) => (
        <Button type="link" size="small" onClick={() => navigate(`/purchases/${record.id}`)}>
          详情
        </Button>
      ),
    },
  ]

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as PurchaseQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="采购订单"
        subtitle="草稿 → 待审核 → 已审核 → 到货 → 完成"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '采购单号 / 供应商' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<PurchaseItem>
          storageKey="purchase-orders"
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
          emptyText="当前筛选条件下没有采购订单"
          scrollX={1220}
        />
      </Card>
    </div>
  )
}
