import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type BatchItem, type BatchQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<BatchItem> = [
  { title: '批次号', dataIndex: 'batch_no', width: 140, fixed: 'left' },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '供应商 ID', dataIndex: 'supplier_id', width: 110, render: (v: BatchItem['supplier_id']) => (String(v) === '0' ? '-' : String(v)) },
  { title: '生产日期', dataIndex: 'production_date', width: 110, render: (v?: string | null) => formatDate(v) },
  { title: '入库日期', dataIndex: 'inbound_date', width: 110, render: (v?: string | null) => formatDate(v) },
  { title: '效期', dataIndex: 'expiry_date', width: 110, render: (v?: string | null) => formatDate(v) },
  {
    title: '成本价',
    dataIndex: 'cost_price',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 180,
    ellipsis: true,
    render: (v: string) => (v ? <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 批次库存（frontend.md §10.1 库存中心模块；菜单 /inventory/batches，config/menu.tsx:58）。
 * GET /api/batches 后端 T5 已交付（internal/inventory/inventory.go:55，权限点 inventory:batch:list），
 * 字段对齐 BatchView（internal/inventory/handler.go:107-119）；批次维度库存聚合后端 M1 未下发，
 * 到货后随入库域冻结回补（backend-m1-plan.md §13）。
 */
export default function BatchListPage() {
  const [params, setParams] = useState<BatchQuery>({})
  const list = usePagedList<BatchItem, BatchQuery>({
    queryKey: ['inventory', 'batches'],
    fetch: (q) => inventoryApi.batches(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as BatchQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="批次库存"
        subtitle="启用批次管理 SKU 的批次台账（FIFO/FEFO 命中维度）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'batch_no', label: '批次号', control: 'input', placeholder: '批次号' },
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'supplier_id', label: '供应商 ID', control: 'input', placeholder: '供应商 ID（0=未指定）' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<BatchItem>
          storageKey="inventory-batches"
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
          emptyText="当前筛选条件下没有批次库存"
          scrollX={1430}
        />
      </Card>
    </div>
  )
}
