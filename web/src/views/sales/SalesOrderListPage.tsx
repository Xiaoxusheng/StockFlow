import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  SALES_ORDER_CREATE_PERMISSION,
  salesApi,
  type SalesOrderItem,
  type SalesOrderQuery,
  type SalesOrderStatus,
} from '@/api/sales'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 状态选项与 business-flow.md §6.2 销售订单流程（订单 → 审核 → 库存预占 → 出库）一致 */
const STATUS_OPTIONS: Array<{ label: string; value: SalesOrderStatus }> = [
  { label: '草稿', value: 'draft' },
  { label: '待审核', value: 'pending_review' },
  { label: '已审核', value: 'approved' },
  { label: '处理中', value: 'processing' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

/** 销售订单列表（/sales；GET /api/sales-orders 前端先行骨架，后端未交付呈统一错误态） */
export default function SalesOrderListPage() {
  const [params, setParams] = useState<SalesOrderQuery>({})
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_ORDER_CREATE_PERMISSION)
  const list = usePagedList<SalesOrderItem, SalesOrderQuery>({
    queryKey: ['sales', 'orders'],
    fetch: (q) => salesApi.orders.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SalesOrderQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<SalesOrderItem> = [
    {
      title: '销售单号',
      dataIndex: 'soNo',
      width: 170,
      fixed: 'left',
      render: (v: string, record: SalesOrderItem) => (
        <Link onClick={() => navigate(`/sales/${record.id}`)}>{v}</Link>
      ),
    },
    {
      title: '客户',
      dataIndex: 'customerName',
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
      title: '金额',
      dataIndex: 'totalAmount',
      width: 120,
      align: 'right',
      render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
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
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: SalesOrderItem) => (
        <Link onClick={() => navigate(`/sales/${record.id}`)} style={{ whiteSpace: 'nowrap' }}>
          详情
        </Link>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="销售订单"
        subtitle="订单 → 审核 → 库存预占 → 出库"
        extra={
          canCreate ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/sales/new')}
            >
              新建销售订单
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '销售单号 / 客户' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<SalesOrderItem>
          storageKey="sales-orders"
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
          emptyText="当前筛选条件下没有销售订单"
          scrollX={1020}
        />
      </Card>
    </div>
  )
}
