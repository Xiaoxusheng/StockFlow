import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type SerialItem, type SerialQuery, type SerialStatus } from '@/api/inventory'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

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

const COLUMNS: ColumnsType<SerialItem> = [
  { title: '序列号', dataIndex: 'serialNo', width: 150, fixed: 'left' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  { title: '仓库', dataIndex: 'warehouseName', width: 100, render: (v?: string) => v ?? '-' },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: SerialStatus) => <SerialStatusTag status={v} />,
  },
  { title: '最近来源类型', dataIndex: 'lastSourceType', width: 110, render: (v?: string) => v ?? '-' },
  { title: '最近来源单号', dataIndex: 'lastSourceNo', width: 160, render: (v?: string) => v ?? '-' },
  {
    title: '最近事件时间',
    dataIndex: 'lastEventAt',
    width: 160,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 序列号（frontend.md §10.1 库存中心模块；菜单 /inventory/serials，config/menu.tsx:59）。
 * GET /api/inventory/serials 为前端先行骨架，M1 不交付该端点（backend-m1-plan.md §13
 * 「批次/序列号查询接口 M1 不交付」；M1 库存 HTTP 面只读且仅冻结 inventory:inventory:list /
 * inventory:ledger:list 两个权限点，internal/auth/permissions.go:116-117）——
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 字段与状态值域对齐 db/migrations/000005 serial_numbers（一物一行，inventory-rules.md §8）。
 */
export default function SerialListPage() {
  const [params, setParams] = useState<SerialQuery>({})
  const list = usePagedList<SerialItem, SerialQuery>({
    queryKey: ['inventory', 'serials'],
    fetch: (q) => inventoryApi.serials(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as SerialQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="序列号"
        subtitle="序列号全生命周期：一物一行，仓库/库位为空表示不在库"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '序列号 / SKU / 商品名称' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
            { name: 'status', label: '状态', control: 'select', options: SERIAL_STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<SerialItem>
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
          scrollX={1480}
        />
      </Card>
    </div>
  )
}
