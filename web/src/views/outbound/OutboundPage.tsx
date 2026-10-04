import { useMemo, useState } from 'react'
import { Card, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
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
import type { StatusSemantic } from '@/types/status'
import { formatDateTime } from '@/utils/format'

const { Link, Text } = Typography

/** 出库单状态 → SfStatusTag（frontend.md §24：颜色统一走注册表语义）。
 * 后端大写枚举（internal/sales/models.go:265-276）经 toStatusKey 归一后命中
 * types/status.ts 注册表（pending_allocate/…/cancelled/closed）；
 * PARTIAL_SHIPPED/SHIPPED_ALL 注册表暂无键，以 label/semantic 兜底，注册表补键后自动切换。 */
const OB_STATUS_TAG: Record<OutboundOrderStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING_ALLOCATE: { label: '待分配', semantic: 'pending' },
  ALLOCATED: { label: '已分配', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  CHECKED: { label: '已复核', semantic: 'success' },
  PACKED: { label: '已打包', semantic: 'success' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'warning' },
  SHIPPED_ALL: { label: '已发货', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
  CLOSED: { label: '已关闭', semantic: 'neutral' },
}

function ObStatusTag({ status }: { status: OutboundOrderStatus }) {
  const meta = OB_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listOutbounds：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: OutboundOrderStatus }> = [
  { label: '待分配', value: 'PENDING_ALLOCATE' },
  { label: '已分配', value: 'ALLOCATED' },
  { label: '拣货中', value: 'PICKING' },
  { label: '已拣货', value: 'PICKED' },
  { label: '已复核', value: 'CHECKED' },
  { label: '已打包', value: 'PACKED' },
  { label: '部分发货', value: 'PARTIAL_SHIPPED' },
  { label: '已发货', value: 'SHIPPED_ALL' },
  { label: '已取消', value: 'CANCELLED' },
  { label: '已关闭', value: 'CLOSED' },
]

/**
 * 出库管理（GET /api/outbounds，后端 M2 已交付）：列表列回对 OutboundOrder 裸模型
 * （无联表编码/名称，仓库 ID 经基础资料 options 本地映射，失败降级为 ID）。
 * 详情跳转按出库单号 /outbound/{outbound_no}——后端 GET /api/outbounds/:no 按单号查询
 * （修复审计问题 #17：传主键 id 必查空）。
 */
export default function OutboundPage() {
  const [params, setParams] = useState<OutboundOrderQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<OutboundOrder, OutboundOrderQuery>({
    queryKey: ['outbound', 'orders'],
    fetch: (q) => outboundApi.list(q),
    params,
    // §26.3：分页经 persistKey 持久化，进详情返回后恢复离开前分页
    persistKey: 'outbound-orders',
  })

  // 仓库 ID → 名称（options.ts：一次取全基础资料，映射失败降级为 ID，不造假数据）
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
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
    // type 为中文值域（models.go:344-346 迁移 CHECK：销售出库/生产领料/调拨出库/其他出库/报损出库）
    { title: '出库类型', dataIndex: 'type', width: 110, render: (v: string) => v || '-' },
    { title: '来源销售单号', dataIndex: 'so_no', width: 160, render: (v: string) => v || '-' },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 130,
      render: (v: number) => {
        const name = warehouseNames.get(idKey(v)) ?? idKey(v)
        return (
          <Text style={{ maxWidth: 120 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (v: OutboundOrderStatus) => <ObStatusTag status={v} />,
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
        <Link
          onClick={() => navigate(`/outbound/${record.outbound_no}`)}
          style={{ whiteSpace: 'nowrap' }}
        >
          详情
        </Link>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库管理"
        subtitle="分配 → 拣货 → 复核 → 打包 → 发货"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
            { name: 'so_no', label: '来源销售单号', control: 'input' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: idKey(w.id),
              })),
            },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<OutboundOrder>
          storageKey="outbound-orders"
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
          scrollX={940}
        />
      </Card>
    </div>
  )
}
