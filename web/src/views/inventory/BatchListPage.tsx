import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { inventoryApi, type BatchItem, type BatchQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfTable } from '@/components/table/SfTable'
import { formatDate, formatMoney } from '@/utils/format'

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
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    width: 160,
    render: (v: string) => <DateCell value={v} />,
  },
]

/**
 * 批次库存（frontend.md §10.1 库存中心模块；菜单 /inventory/batches，config/menu.tsx:58）。
 * GET /api/batches 后端 T5 已交付（internal/inventory/inventory.go:55，权限点 inventory:batch:list），
 * 字段对齐 BatchView（internal/inventory/handler.go:107-119）；批次维度库存聚合后端 M1 未下发，
 * 到货后随入库域冻结回补（backend-m1-plan.md §13）。
 */
export default function BatchListPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<BatchItem, BatchQuery>({
    queryKey: ['inventory', 'batches'],
    fetch: (q) => inventoryApi.batches(q),
    urlSync: true,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

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
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <SfViewBar
              pageKey="inventory.batches"
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
        <SfTable<BatchItem>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
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
