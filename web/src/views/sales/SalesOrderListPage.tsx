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
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import { buildCustomerMaps, buildWarehouseMaps, fetchCustomerOptions, fetchWarehouseOptions, idKey, WAREHOUSE_OPTIONS_KEY } from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SALES_ORDER_STATUS_TAG } from './salesStatusMeta'
import { formatMoney } from '@/utils/format'

const { Link, Text } = Typography

/** 状态筛选选项与列内标签同源（值为后端大写枚举，handler.go:106 直接入参） */
const STATUS_OPTIONS = (
  Object.entries(SALES_ORDER_STATUS_TAG) as Array<[SalesOrderStatus, (typeof SALES_ORDER_STATUS_TAG)[SalesOrderStatus]]>
).map(([value, meta]) => ({ label: meta.label, value }))

/** 状态标签：大写枚举经 toStatusKey 归一后注册表优先，未注册键以域内映射兜底（salesStatusMeta.ts） */
function SalesOrderStatusTag({ status }: { status: SalesOrderStatus }) {
  const meta = SALES_ORDER_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 销售订单列表（/sales；GET /api/sales，后端 M2 已交付 internal/sales/routes.go:73-80）。
 * 搜索参数 so_no/status/warehouse_id/customer_id（handler.go:100-131 实测入参，
 * so_no 精确匹配）；客户/仓库出参为裸 ID（SalesOrder 无联表名称），
 * 经基础资料 options 端点本地映射补充，映射失败降级为 ID，不造假数据。 */
export default function SalesOrderListPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_ORDER_CREATE_PERMISSION)
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<SalesOrder, SalesOrderQuery>({
    queryKey: ['sales', 'orders'],
    fetch: (q) => salesApi.orders.list(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  // 客户/仓库 id → 名称映射（options 端点一次取全；失败降级为 ID 显示，不阻塞列表）
  const customersQuery = useQuery({
    queryKey: ['options', 'customers'],
    queryFn: fetchCustomerOptions,
  })
  const warehousesQuery = useQuery({
    queryKey: WAREHOUSE_OPTIONS_KEY,
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
      render: (v: string) => <DateCell value={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: SalesOrder) => (
        <Button type="link" size="small" onClick={() => navigate(`/sales/${record.id}`)}>
          详情
        </Button>
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
            /* 时间范围：后端 GET /api/sales 支持 created_from/created_to
               （internal/sales/handler.go:121-127，成对参数） */
            {
              name: 'created',
              label: '创建时间',
              control: 'dateRange',
              rangeKeys: ['created_from', 'created_to'],
            },
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
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图（§2.2）：经 extraActions 与查询/重置**同行**渲染——
             不再挂 SfTable 工具栏（那会独占一行，与搜索框分离） */
          extraActions={
            <SfViewBar
              pageKey="sales.order"
              mode="url"
              paged={{ applyFilters: list.applyFilters, onPageChange: list.onPageChange }}
              appliedFilters={list.params as unknown as Record<string, unknown>}
              currentFilters={list.formValues}
              currentPageSize={list.pagination.pageSize}
              currentHiddenColumns={hiddenColumns}
              onHiddenColumnsChange={setHiddenColumns}
            />
          }
        />
        <SfTable<SalesOrder>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
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
          emptyAction={
            canCreate ? (
              <Button type="primary" onClick={() => navigate('/sales/new')}>
                新建销售订单
              </Button>
            ) : undefined
          }
          scrollX={900}
        />
      </Card>
    </div>
  )
}
