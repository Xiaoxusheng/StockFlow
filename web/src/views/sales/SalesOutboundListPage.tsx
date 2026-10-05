import { useMemo, useState } from 'react'
import { Button, Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  outboundApi,
  type OutboundOrder,
  type OutboundOrderQuery,
  type OutboundOrderStatus,
} from '@/api/outbound'
import { toStatusKey } from '@/api/masterdata'
import { buildWarehouseMaps, fetchWarehouseOptions, idKey } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { OUTBOUND_STATUS_TAG } from './salesStatusMeta'
import { formatDateTime } from '@/utils/format'

const { Link } = Typography

/** 状态筛选选项与列内标签同源（值为后端大写枚举，handler.go:257 直接入参） */
const STATUS_OPTIONS = (
  Object.entries(OUTBOUND_STATUS_TAG) as Array<
    [OutboundOrderStatus, (typeof OUTBOUND_STATUS_TAG)[OutboundOrderStatus]]
  >
).map(([value, meta]) => ({ label: meta.label, value }))

/** 状态标签：大写枚举经 toStatusKey 归一后注册表优先，未注册键以域内映射兜底（salesStatusMeta.ts） */
function OutboundStatusTag({ status }: { status: OutboundOrderStatus }) {
  const meta = OUTBOUND_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 出库单列表（/sales/outbounds；GET /api/outbounds，后端 M2 已交付 internal/sales/routes.go:82-87）。
 * 原骨架漂移调用 GET /api/sales/outbounds（命中 /api/sales/:id 参数错误，审计已核实问题 #13），
 * 本版切换 outboundApi.list。搜索参数 outbound_no/so_no/status/warehouse_id（handler.go:253-272
 * 实测入参，单号均精确匹配）；仓库出参为裸 ID，经 options 端点映射，失败降级 ID。
 * 出库单由销售订单审核后的下游流程（库存分配 → 拣货 → 复核 → 打包 → 发货）生成，
 * 页面不提供手工新建入口。详情跳转与出库域 OutboundPage 同径：/outbound/{outbound_no}
 * （后端 GET /api/outbounds/:no 按单号查询）；本页仍在销售菜单组，菜单归属不动。 */
export default function SalesOutboundListPage() {
  const [params, setParams] = useState<OutboundOrderQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<OutboundOrder, OutboundOrderQuery>({
    queryKey: ['outbound', 'orders'],
    fetch: (q) => outboundApi.list(q),
    params,
    // §26.3：分页经 persistKey 持久化，进详情返回后恢复离开前分页
    persistKey: 'sales-outbounds',
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
    setParams(values as OutboundOrderQuery)
    list.resetToFirstPage()
  }

  const columns: ColumnsType<OutboundOrder> = [
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 170,
      fixed: 'left',
      render: (v: string, record: OutboundOrder) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    {
      title: '销售单号',
      dataIndex: 'so_no',
      width: 170,
      render: (v: string) => v || '-',
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 110,
      render: (v: OutboundOrder['type']) => v || '-',
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      ellipsis: true,
      render: (v: OutboundOrder['warehouse_id']) => warehouseNames.get(idKey(v)) ?? String(v),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: OutboundOrderStatus) => <OutboundStatusTag status={v} />,
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
      render: (_: unknown, record: OutboundOrder) => (
        <Button type="link" size="small" onClick={() => navigate(`/outbound/${record.outbound_no}`)}>
          详情
        </Button>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库"
        subtitle="库存分配 → 拣货 → 复核 → 打包 → 发货"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确匹配）' },
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
          ]}
          onSearch={handleSearch}
        />
        <SfTable<OutboundOrder>
          storageKey="sales-outbounds"
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
          emptyText="当前筛选条件下没有出库单"
          scrollX={930}
        />
      </Card>
    </div>
  )
}
