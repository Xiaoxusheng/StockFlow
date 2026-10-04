import { useMemo, useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  SALES_ORDER_CREATE_PERMISSION,
  salesApi,
  type SalesOrder,
  type SalesOrderQuery,
  type SalesOrderStatus,
} from '@/api/sales'
import { buildCustomerMaps, buildWarehouseMaps, fetchCustomerOptions, fetchWarehouseOptions, idKey } from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import type { StatusSemantic } from '@/types/status'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatMoney } from '@/utils/format'

const { Link, Text } = Typography

/**
 * 销售订单状态 → SfStatusTag 兜底映射（8 态值域 internal/sales/models.go:240-248，
 * 迁移 CHECK 同源；business-flow.md §6.2 订单 → 审核 → 库存预占 → 出库）。
 * 大写原始值不命中 types/status.ts 注册表（resolveStatus 精确匹配小写键），
 * label/semantic 兜底接管（api/transfer.ts TRANSFER_STATUS_TAG 同口径）。
 */
const SALES_ORDER_STATUS_TAG: Record<SalesOrderStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  REJECTED: { label: '已驳回', semantic: 'danger' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'processing' },
  SHIPPED_ALL: { label: '全部发货', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 状态筛选选项与列内标签同源（值为后端大写枚举，handler.go:106 直接入参） */
const STATUS_OPTIONS = (
  Object.entries(SALES_ORDER_STATUS_TAG) as Array<[SalesOrderStatus, (typeof SALES_ORDER_STATUS_TAG)[SalesOrderStatus]]>
).map(([value, meta]) => ({ label: meta.label, value }))

function SalesOrderStatusTag({ status }: { status: SalesOrderStatus }) {
  const meta = SALES_ORDER_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/** 销售订单列表（/sales；GET /api/sales，后端 M2 已交付 internal/sales/routes.go:73-80）。
 * 搜索参数 so_no/status/warehouse_id/customer_id（handler.go:100-131 实测入参，
 * so_no 精确匹配）；客户/仓库出参为裸 ID（SalesOrder 无联表名称），
 * 经基础资料 options 端点本地映射补充，映射失败降级为 ID，不造假数据。 */
export default function SalesOrderListPage() {
  const [params, setParams] = useState<SalesOrderQuery>({})
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_ORDER_CREATE_PERMISSION)
  const list = usePagedList<SalesOrder, SalesOrderQuery>({
    queryKey: ['sales', 'orders'],
    fetch: (q) => salesApi.orders.list(q),
    params,
  })

  // 客户/仓库 id → 名称映射（options 端点一次取全；失败降级为 ID 显示，不阻塞列表）
  const customersQuery = useQuery({
    queryKey: ['options', 'customers'],
    queryFn: fetchCustomerOptions,
  })
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const customerNames = useMemo(
    () => buildCustomerMaps(customersQuery.data ?? []).name,
    [customersQuery.data],
  )
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SalesOrderQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<SalesOrder> = [
    {
      title: '销售单号',
      dataIndex: 'so_no',
      width: 170,
      fixed: 'left',
      render: (v: string, record: SalesOrder) => (
        <Link onClick={() => navigate(`/sales/${record.id}`)}>{v}</Link>
      ),
    },
    {
      title: '客户',
      dataIndex: 'customer_id',
      width: 180,
      ellipsis: true,
      render: (v: SalesOrder['customer_id']) => {
        const name = customerNames.get(idKey(v)) ?? String(v)
        return <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: name }}>{name}</Text>
      },
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      ellipsis: true,
      render: (v: SalesOrder['warehouse_id']) => warehouseNames.get(idKey(v)) ?? String(v),
    },
    {
      title: '金额',
      dataIndex: 'total_amount',
      width: 120,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (v: SalesOrderStatus) => <SalesOrderStatusTag status={v} />,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: SalesOrder) => (
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
            { name: 'so_no', label: '销售单号', control: 'input', placeholder: '销售单号（精确匹配）' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              placeholder: '请选择仓库',
              options: (warehousesQuery.data ?? []).map((item) => ({
                label: `${item.name}（${item.code}）`,
                value: idKey(item.id),
              })),
            },
            {
              name: 'customer_id',
              label: '客户',
              control: 'select',
              placeholder: '请选择客户',
              options: (customersQuery.data ?? []).map((item) => ({
                label: `${item.name}（${item.code}）`,
                value: idKey(item.id),
              })),
            },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<SalesOrder>
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
          scrollX={900}
        />
      </Card>
    </div>
  )
}
