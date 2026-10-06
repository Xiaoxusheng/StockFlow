import { useMemo } from 'react'
import { Button, Card, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import {
  INBOUND_CREATE_PERMISSION,
  inboundApi,
  type InboundOrder,
  type InboundOrderQuery,
  type InboundOrderStatus,
  type InboundSourceType,
} from '@/api/inbound'
import { DateCell } from '@/components/table/cells'
import { buildWarehouseMaps, fetchWarehouseOptions } from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'

const { Text } = Typography

/** 入库来源类型文案（api/inbound.ts InboundSourceType＝迁移 CHECK 值域：PURCHASE/OTHER） */
const SOURCE_TYPE_LABEL: Record<InboundSourceType, string> = {
  PURCHASE: '采购入库',
  OTHER: '其他入库',
}

/**
 * 入库单七态 → SfStatusTag（models.go:27-33 迁移 CHECK 同源）。
 * awaiting_qc/awaiting_putaway 尚未入 types/status.ts 全局注册表，
 * 经 label/semantic 兜底传参（SfStatusTag 支持未注册状态直接指定），与盘点中心 CountStatusTag 同模式。
 */
const INBOUND_STATUS_TAG: Record<InboundOrderStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  RECEIVING: { key: 'receiving', label: '收货中', semantic: 'processing' },
  AWAITING_QC: { key: 'awaiting_qc', label: '待质检', semantic: 'pending' },
  AWAITING_PUTAWAY: { key: 'awaiting_putaway', label: '待上架', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
  CLOSED: { key: 'closed', label: '已关闭', semantic: 'neutral' },
}

function InboundStatusTag({ status }: { status: InboundOrderStatus }) {
  const meta = INBOUND_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

const STATUS_OPTIONS = (
  Object.entries(INBOUND_STATUS_TAG) as Array<
    [InboundOrderStatus, (typeof INBOUND_STATUS_TAG)[InboundOrderStatus]]
  >
).map(([value, meta]) => ({ label: meta.label, value }))

const SOURCE_TYPE_OPTIONS = (Object.entries(SOURCE_TYPE_LABEL) as Array<[InboundSourceType, string]>).map(
  ([value, label]) => ({ label, value }),
)

/** 入库单列表（frontend.md F7 入库/出库流程；GET /api/inbounds，出参为 InboundOrder 裸模型
 * snake_case，internal/purchase/models.go:164-178；仓库/来源类型经筛选回传
 * keyword/status/source_type/source_no/warehouse_id，purchase/handler.go:220-243） */
export default function InboundPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, INBOUND_CREATE_PERMISSION)

  // 仓库 options（GET /api/warehouses）：仓库筛选下拉 + warehouse_id→名称本地映射；
  // 拉取失败降级为 ID 展示 / 空下拉，不阻塞列表（api/options.ts 约定）
  const warehouseOptions = useQuery({
    queryKey: ['inbound', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseMaps = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []),
    [warehouseOptions.data],
  )

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<InboundOrder, InboundOrderQuery>({
    queryKey: ['inbound', 'orders'],
    fetch: (q) => inboundApi.list(q),
    urlSync: true,
  })

  const columns: ColumnsType<InboundOrder> = [
    { title: '入库单号', dataIndex: 'inbound_no', width: 170, fixed: 'left' },
    {
      title: '入库类型',
      dataIndex: 'source_type',
      width: 110,
      render: (v: InboundSourceType) => SOURCE_TYPE_LABEL[v] ?? v,
    },
    {
      title: '来源单号',
      dataIndex: 'source_no',
      width: 160,
      render: (v: string) =>
        v ? (
          <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>
            {v}
          </Text>
        ) : (
          '-'
        ),
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      ellipsis: true,
      render: (v: number) => warehouseMaps.name.get(String(v)) ?? `#${String(v)}`,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: InboundOrderStatus) => <InboundStatusTag status={v} />,
    },
    {
      title: '收货完成时间',
      dataIndex: 'received_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
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
      render: (_: unknown, record: InboundOrder) => (
        <Button type="link" size="small" onClick={() => navigate(`/inbound/${record.id}`)}>
          详情
        </Button>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="入库管理"
        subtitle="收货 → 质检 → 上架（支持部分收货）"
        extra={
          canCreate ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/inbound/new')}
            >
              新建入库单
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '入库单号 / 来源单号' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'source_type', label: '入库类型', control: 'select', options: SOURCE_TYPE_OPTIONS },
            { name: 'source_no', label: '来源单号', control: 'input', placeholder: '按来源单号精确匹配' },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: String(w.id),
              })),
            },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<InboundOrder>
          storageKey="inbound-list"
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
          emptyText="当前筛选条件下没有入库单"
          emptyAction={
            canCreate ? (
              <Button type="primary" onClick={() => navigate('/inbound/new')}>
                新建入库单
              </Button>
            ) : undefined
          }
          scrollX={1080}
        />
      </Card>
    </div>
  )
}
