import { useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { inventoryApi, type SerialItem, type SerialQuery, type SerialStatus } from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'


const SERIAL_STATUS_OPTIONS: Array<{ label: string; value: SerialStatus }> = [
  { label: '在库', value: 'IN_STOCK' },
  { label: '锁定', value: 'LOCKED' },
  { label: '已出库', value: 'OUTBOUND' },
  { label: '已退货', value: 'RETURNED' },
  { label: '冻结', value: 'FROZEN' },
]

/**
 * 序列号状态 → SfStatusTag：值域对齐 db/migrations/000005 serial_numbers CHECK 约束，
 * 注册表未收录的 key 由 label/semantic 兜底（颜色仍由 SfStatusTag 统一映射）。
 */
const SERIAL_STATUS_TAG: Record<SerialStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  IN_STOCK: { key: 'in_stock', label: '在库', semantic: 'success' },
  LOCKED: { key: 'locked', label: '锁定', semantic: 'warning' },
  OUTBOUND: { key: 'outbound', label: '已出库', semantic: 'neutral' },
  RETURNED: { key: 'returned', label: '已退货', semantic: 'neutral' },
  FROZEN: { key: 'frozen', label: '冻结', semantic: 'danger' },
}

function SerialStatusTag({ status }: { status: SerialStatus }) {
  const meta = SERIAL_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 可选维度 ID 0 值显示占位符（0=非批次/不在库） */
function renderIdOrDash(value: SerialItem['batch_id']): string {
  return String(value) === '0' ? '-' : String(value)
}

const COLUMNS: ColumnsType<SerialItem> = [
  { title: '序列号', dataIndex: 'serial_no', width: 150, fixed: 'left' },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '批次 ID', dataIndex: 'batch_id', width: 100, render: renderIdOrDash },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 100, render: renderIdOrDash },
  { title: '库位 ID', dataIndex: 'bin_id', width: 100, render: renderIdOrDash },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: SerialStatus) => <SerialStatusTag status={v} />,
  },
  { title: '最近来源类型', dataIndex: 'last_source_type', width: 110, render: (v: string) => v || '-' },
  { title: '最近来源单号', dataIndex: 'last_source_no', width: 160, render: (v: string) => v || '-' },
  {
    title: '最近事件时间',
    dataIndex: 'last_event_at',
    width: 160,
    render: (v?: string | null) => <DateCell value={v} />,
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <DateCell value={v} />,
  },
]

/**
 * 序列号（frontend.md §10.1 库存中心模块；菜单 /inventory/serials，config/menu.tsx:59）。
 * GET /api/serials 后端 T5 已交付（internal/inventory/inventory.go:56，权限点 inventory:serial:list），
 * 字段对齐 SerialView（internal/inventory/handler.go:131-144，一物一行，inventory-rules.md §8）。
 */
export default function SerialListPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<SerialItem, SerialQuery>({
    queryKey: ['inventory', 'serials'],
    fetch: (q) => inventoryApi.serials(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  return (
    <div className="sf-page">
      <SfPageHeader
        title="序列号"
        subtitle="序列号全生命周期：一物一行，仓库/库位为 0 表示不在库"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'serial_no', label: '序列号', control: 'input', placeholder: '序列号' },
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'warehouse_id', label: '仓库 ID', control: 'input', placeholder: '仓库 ID（0=不在库）' },
            { name: 'status', label: '状态', control: 'select', options: SERIAL_STATUS_OPTIONS },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <SfViewBar
              pageKey="inventory.serials"
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
        <SfTable<SerialItem>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="inventory-serials"
          rowKey="id"
          columns={COLUMNS}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有序列号记录"
          scrollX={1400}
        />
      </Card>
    </div>
  )
}
