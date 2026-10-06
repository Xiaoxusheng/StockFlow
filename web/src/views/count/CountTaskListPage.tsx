import { useMemo } from 'react'
import { Button, Card, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import {
  COUNT_CREATE_PERMISSION,
  COUNT_SCOPE_MODE_LABEL,
  countApi,
  type CountId,
  type CountOrder,
  type CountQuery,
  type CountScope,
  type CountStatus,
} from '@/api/count'
import { DateCell } from '@/components/table/cells'
import { masterdataApi, OPTIONS_PAGE_SIZE } from '@/api/masterdata'
import { binApi, shelfApi, warehouseApi, zoneApi } from '@/api/warehouse'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Link, Text } = Typography

/**
 * 盘点单五态状态 → SfStatusTag（internal/stockops/models.go:143-147 迁移 CHECK 同源；
 * 经 toStatusKey 归一复用 types/status.ts 注册表：PENDING_REVIEW 走 pending_recheck「待复核」，
 * 原前端虚构的 pending_execute / pending_approval 值域已随后端契约冻结移除）。
 */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

const STATUS_OPTIONS = (
  Object.entries(COUNT_STATUS_TAG) as Array<[CountStatus, (typeof COUNT_STATUS_TAG)[CountStatus]]>
).map(([value, meta]) => ({ label: meta.label, value }))

const LIST_OPTIONS = { page: 1, pageSize: OPTIONS_PAGE_SIZE } as const

/**
 * ID → 文案映射表（模式对齐 PadInventoryPage 的 options 映射：基础资料 / 仓库空间
 * options 端点一次取全本地映射，映射失败降级为 `#id` 展示，不造假数据）。
 */
interface ScopeLabelMaps {
  warehouseMap: Map<string, string>
  zoneMap: Map<string, string>
  shelfMap: Map<string, string>
  binMap: Map<string, string>
  skuMap: Map<string, string>
}

function labelOrId(map: Map<string, string>, id: CountId): string {
  return map.get(String(id)) ?? `#${String(id)}`
}

/** 范围文本：mode 中文 + 按 mode 携带的 ID 列表映射为编码（COUNT_SCOPE_MODE_LABEL，api/count.ts） */
function scopeText(scope: CountScope, maps: ScopeLabelMaps): string {
  const label = COUNT_SCOPE_MODE_LABEL[scope.mode] ?? scope.mode
  switch (scope.mode) {
    case 'ALL':
      return label
    case 'ZONE':
      return `${label}：${(scope.zone_ids ?? []).map((id) => labelOrId(maps.zoneMap, id)).join('、')}`
    case 'SHELF':
      return `${label}：${(scope.shelf_ids ?? []).map((id) => labelOrId(maps.shelfMap, id)).join('、')}`
    case 'BIN':
      return `${label}：${(scope.bin_ids ?? []).map((id) => labelOrId(maps.binMap, id)).join('、')}`
    case 'SKU':
      return `${label}：${(scope.sku_ids ?? []).map((id) => labelOrId(maps.skuMap, id)).join('、')}`
    default:
      return label
  }
}

/**
 * 盘点中心列表（/counts，frontend.md §10.5；后端实测筛选仅 warehouse_id/status/count_no，
 * internal/stockops/handler.go:451-486）。类型 / 负责人列后端 CountOrderView 无对应字段，
 * 待后端补充后再行展示（见 frontend.md §10.5 缺口说明）。
 */
export default function CountTaskListPage() {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canCreate = canAccess(user, COUNT_CREATE_PERMISSION)

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<CountOrder, CountQuery>({
    queryKey: ['counts', 'tasks'],
    fetch: (q) => countApi.list(q),
    urlSync: true,
  })

  // 仓库 / 库区 / 货架 / 库位 / SKU options 一次取全，用于 warehouse_id 与 scope ID 本地映射
  const warehouses = useQuery({
    queryKey: ['count', 'warehouse-options'],
    queryFn: () => warehouseApi.list({ ...LIST_OPTIONS }),
  })
  const zones = useQuery({
    queryKey: ['count', 'zone-options'],
    queryFn: () => zoneApi.list({ ...LIST_OPTIONS }),
  })
  const shelves = useQuery({
    queryKey: ['count', 'shelf-options'],
    queryFn: () => shelfApi.list({ ...LIST_OPTIONS }),
  })
  const bins = useQuery({
    queryKey: ['count', 'bin-options'],
    queryFn: () => binApi.list({ ...LIST_OPTIONS }),
  })
  const skus = useQuery({
    queryKey: ['count', 'sku-options'],
    queryFn: () => masterdataApi.skus.list({ ...LIST_OPTIONS }),
  })

  const maps = useMemo<ScopeLabelMaps>(
    () => ({
      warehouseMap: new Map(
        (warehouses.data?.items ?? []).map((w) => [String(w.id), `${w.name}（${w.code}）`]),
      ),
      zoneMap: new Map((zones.data?.items ?? []).map((z) => [String(z.id), z.code])),
      shelfMap: new Map((shelves.data?.items ?? []).map((s) => [String(s.id), s.code])),
      binMap: new Map((bins.data?.items ?? []).map((b) => [String(b.id), b.code])),
      skuMap: new Map(
        (skus.data?.items ?? []).map((s) => [
          String(s.id),
          s.product_name ? `${s.code} ${s.product_name}` : s.code,
        ]),
      ),
    }),
    [warehouses.data, zones.data, shelves.data, bins.data, skus.data],
  )

  const warehouseOptions = (warehouses.data?.items ?? []).map((w) => ({
    label: `${w.name}（${w.code}）`,
    value: String(w.id),
  }))

  const goDetail = (id: CountId) => navigate(`/counts/${id}`)

  const columns: ColumnsType<CountOrder> = [
    {
      title: '盘点单号',
      dataIndex: 'count_no',
      width: 170,
      fixed: 'left',
      render: (v: string, record: CountOrder) => <Link onClick={() => goDetail(record.id)}>{v}</Link>,
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 150,
      ellipsis: true,
      render: (v: CountId) => {
        const label = labelOrId(maps.warehouseMap, v)
        return (
          <Text style={{ maxWidth: 150 }} ellipsis={{ tooltip: label }}>
            {label}
          </Text>
        )
      },
    },
    {
      title: '范围',
      key: 'scope',
      width: 230,
      ellipsis: true,
      render: (_: unknown, record: CountOrder) => {
        const label = scopeText(record.scope, maps)
        return (
          <Text style={{ maxWidth: 230 }} ellipsis={{ tooltip: label }}>
            {label}
          </Text>
        )
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: CountStatus) => <CountStatusTag status={v} />,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v?: string) => <DateCell value={v} />,
    },
    {
      title: '冻结时间',
      dataIndex: 'frozen_at',
      width: 160,
      render: (v?: string | null) => <DateCell value={v} />,
    },
    {
      title: '完成时间',
      dataIndex: 'completed_at',
      width: 160,
      render: (v?: string | null) => <DateCell value={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 90,
      render: (_: unknown, record: CountOrder) => (
        <Link onClick={() => goDetail(record.id)} style={{ whiteSpace: 'nowrap' }}>
          查看详情
        </Link>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="盘点中心"
        subtitle="全盘 / 按库区 / 按货架 / 按库位 / 按 SKU（单号 CK- 前缀，DRAFT→COUNTING→PENDING_REVIEW→COMPLETED）"
        extra={
          canCreate ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/counts/new')}>
              新建盘点单
            </Button>
          ) : undefined
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'count_no', label: '盘点单号', control: 'input', placeholder: '精确盘点单号' },
            { name: 'warehouse_id', label: '仓库', control: 'select', options: warehouseOptions },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<CountOrder>
          storageKey="count-tasks"
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
          emptyText="当前筛选条件下没有盘点任务"
          emptyAction={
            canCreate ? (
              <Button type="primary" onClick={() => navigate('/counts/new')}>
                新建盘点单
              </Button>
            ) : undefined
          }
          scrollX={1180}
        />
      </Card>
    </div>
  )
}
