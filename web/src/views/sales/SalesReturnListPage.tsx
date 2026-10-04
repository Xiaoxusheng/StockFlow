import { useMemo, useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import {
  salesApi,
  type SalesReturnOrder,
  type SalesReturnQuery,
  type SalesReturnStatus,
} from '@/api/sales'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import type { StatusSemantic } from '@/types/status'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

/**
 * 退货单状态 → SfStatusTag 兜底映射（8 态值域 internal/returns/models.go:27-36，
 * 迁移 chk_return_orders_status 同源；大写原始值不命中 types/status.ts 注册表
 * （resolveStatus 精确匹配小写键），label/semantic 兜底接管，TRANSFER_STATUS_TAG 同口径）。
 * SHIPPED 为采购退货出库完成态（models.go:32），销售退货流程不产出。
 */
const SALES_RETURN_STATUS_TAG: Record<SalesReturnStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  RECEIVING: { label: '收货中', semantic: 'processing' },
  IN_QC: { label: '质检中', semantic: 'processing' },
  SHIPPED: { label: '已发货', semantic: 'processing' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 状态筛选选项与列内标签同源（值为后端大写枚举，returns/handler.go 直接入参） */
const STATUS_OPTIONS = (
  Object.entries(SALES_RETURN_STATUS_TAG) as Array<
    [SalesReturnStatus, (typeof SALES_RETURN_STATUS_TAG)[SalesReturnStatus]]
  >
).map(([value, meta]) => ({ label: meta.label, value }))

function SalesReturnStatusTag({ status }: { status: SalesReturnStatus }) {
  const meta = SALES_RETURN_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/** 销售退货列表（/sales/returns；GET /api/returns，后端退货域已交付 internal/returns/routes.go:117）。
 * 原骨架漂移调用 GET /api/sales/returns（路由不存在，审计已核实问题 #14），本版切换
 * salesApi.returns.list。搜索参数 source_no/status/warehouse_id（returns/handler.go:154-171
 * 实测入参）。后端列表视图 ReturnOrderView（service_sales.go:124-136）无客户名称/数量汇总列，
 * 原骨架 customerName/totalQty 列如实删除，不造假。退货创建入口随退货域页面统一建设，本页不放假入口。 */
export default function SalesReturnListPage() {
  const [params, setParams] = useState<SalesReturnQuery>({})
  const list = usePagedList<SalesReturnOrder, SalesReturnQuery>({
    queryKey: ['sales', 'returns'],
    fetch: (q) => salesApi.returns.list(q),
    params,
  })

  // 仓库 id → 名称映射（options 端点一次取全；失败降级为 ID 显示，不阻塞列表）
  const warehousesQuery = useQuery({
    queryKey: ['options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehousesQuery.data ?? []).name,
    [warehousesQuery.data],
  )

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SalesReturnQuery)
    list.resetToFirstPage()
  }

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
      render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="销售退货"
        subtitle="退货申请 → 审核 → 收货 → 质检"
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
          onSearch={handleSearch}
        />
        <SfTable<SalesReturnOrder>
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
          scrollX={740}
        />
      </Card>
    </div>
  )
}
