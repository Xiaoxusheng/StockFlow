import { useMemo, useState } from 'react'
import { Button, Card } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  SALES_RETURN_APPROVE_PERMISSION,
  SALES_RETURN_CREATE_PERMISSION,
  SALES_RETURN_SUBMIT_PERMISSION,
  salesApi,
  type SalesReturnOrder,
  type SalesReturnQuery,
  type SalesReturnStatus,
} from '@/api/sales'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfReturnAuditActions } from '@/components/common/SfReturnAuditActions'
import { SalesReturnCreateDrawer } from './SalesReturnCreateDrawer'
import { SALES_RETURN_STATUS_TAG } from './salesStatusMeta'

/** 状态筛选选项与列内标签同源（值为后端大写枚举，returns/handler.go 直接入参） */
const STATUS_OPTIONS = (
  Object.entries(SALES_RETURN_STATUS_TAG) as Array<
    [SalesReturnStatus, (typeof SALES_RETURN_STATUS_TAG)[SalesReturnStatus]]
  >
).map(([value, meta]) => ({ label: meta.label, value }))

/** 状态标签：大写枚举经 toStatusKey 归一后注册表优先，未注册键以域内映射兜底（salesStatusMeta.ts） */
function SalesReturnStatusTag({ status }: { status: SalesReturnStatus }) {
  const meta = SALES_RETURN_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 销售退货列表（/sales/returns；GET /api/returns，后端退货域已交付 internal/returns/routes.go:117）。
 * 原骨架漂移调用 GET /api/sales/returns（路由不存在，审计已核实问题 #14），本版切换
 * salesApi.returns.list。搜索参数 source_no/status/warehouse_id（returns/handler.go:154-171
 * 实测入参）。后端列表视图 ReturnOrderView（service_sales.go:124-136）无客户名称/数量汇总列，
 * 原骨架 customerName/totalQty 列如实删除，不造假。
 * 创建入口为 SalesReturnCreateDrawer（POST /api/returns，仅持 returns:salesreturn:create
 * 权限可见）。操作列接 SfReturnAuditActions（提交审核/审核通过/驳回，2026-10-07
 * 退货审核 UI 接入），当前用户不持 submit/approve 权限时整列不渲染。 */
export default function SalesReturnListPage() {
  const [createOpen, setCreateOpen] = useState(false)
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, SALES_RETURN_CREATE_PERMISSION)
  const hasAuditActions =
    canAccess(user, SALES_RETURN_SUBMIT_PERMISSION) || canAccess(user, SALES_RETURN_APPROVE_PERMISSION)
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<SalesReturnOrder, SalesReturnQuery>({
    queryKey: ['sales', 'returns'],
    fetch: (q) => salesApi.returns.list(q),
    urlSync: true,
  })
  // 操作/创建成功后统一失效列表缓存重取（与创建抽屉 onCreated 同构）
  const refreshAfterAction = () => {
    queryClient.invalidateQueries({ queryKey: ['sales', 'returns'] })
    list.refetch()
  }

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  // 仓库 id → 名称映射（options 端点一次取全；失败降级为 ID 显示，不阻塞列表）
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )

  const columns: ColumnsType<SalesReturnOrder> = [
    { title: '退货单号', dataIndex: 'return_no', width: 170, fixed: 'left' },
    {
      title: '来源单号',
      dataIndex: 'source_no',
      width: 170,
      render: (v: string) => v || '-',
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      ellipsis: true,
      render: (v: SalesReturnOrder['warehouse_id']) => warehouseNames.get(idKey(v)) ?? String(v),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: SalesReturnStatus) => <SalesReturnStatusTag status={v} />,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    ...(hasAuditActions
      ? [
          {
            title: '操作',
            key: 'actions',
            fixed: 'right' as const,
            width: 170,
            render: (_: unknown, record: SalesReturnOrder) => (
              <span style={{ whiteSpace: 'nowrap' }}>
                <SfReturnAuditActions
                  type="sales"
                  orderId={record.id}
                  status={record.status}
                  onChanged={refreshAfterAction}
                />
              </span>
            ),
          },
        ]
      : []),
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="销售退货"
        subtitle="退货申请 → 审核 → 收货 → 质检"
        extra={
          canCreate ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreateOpen(true)}
            >
              新建销售退货
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'source_no', label: '来源单号', control: 'input', placeholder: '销售单号（精确匹配）' },
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
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图：与查询/重置同行渲染（mode=url，urlSync 页——视图应用经 usePagedList API 写 URL） */
          extraActions={
            <SfViewBar
              pageKey="sales.return"
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
        <SfTable<SalesReturnOrder>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="sales-returns"
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
          emptyText="当前筛选条件下没有销售退货单"
          emptyAction={
            canCreate ? (
              <Button type="primary" onClick={() => setCreateOpen(true)}>
                新建销售退货
              </Button>
            ) : undefined
          }
          scrollX={910}
        />
      </Card>
      <SalesReturnCreateDrawer
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={refreshAfterAction}
      />
    </div>
  )
}
