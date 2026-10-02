import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type BatchItem, type BatchQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDate, formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<BatchItem> = [
  { title: '批次号', dataIndex: 'batchNo', width: 130, fixed: 'left' },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '供应商', dataIndex: 'supplierName', width: 110, render: (v?: string) => v ?? '-' },
  { title: '生产日期', dataIndex: 'productionDate', width: 110, render: (v?: string) => formatDate(v) },
  { title: '入库日期', dataIndex: 'inboundDate', width: 110, render: (v?: string) => formatDate(v) },
  { title: '效期', dataIndex: 'expiryDate', width: 110, render: (v?: string) => formatDate(v) },
  {
    title: '成本价',
    dataIndex: 'costPrice',
    width: 110,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatMoney(v)}</span>,
  },
  {
    title: '库存总量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '可用',
    dataIndex: 'availableQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '备注',
    dataIndex: 'remark',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
  },
  {
    title: '更新时间',
    dataIndex: 'updatedAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/**
 * 批次库存（frontend.md §10.1 库存中心模块；菜单 /inventory/batches，config/menu.tsx:58）。
 * GET /api/inventory/batches 为前端先行骨架，M1 不交付该端点（backend-m1-plan.md §13
 * 「批次/序列号查询接口 M1 不交付」；M1 库存 HTTP 面只读且仅冻结 inventory:inventory:list /
 * inventory:ledger:list 两个权限点，internal/auth/permissions.go:116-117）——
 * 后端就绪前页面呈统一错误态（SfTable error 兜底），属预期行为，禁止 mock（约束 6）。
 * 字段对齐 db/migrations/000005 batches 表 + inventory 批次维度聚合。
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
        subtitle="启用批次管理 SKU 的批次维度库存（FIFO/FEFO 命中维度）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU / 商品名称' },
            { name: 'batchNo', label: '批次号', control: 'input', placeholder: '批次号' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
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
          scrollX={1530}
        />
      </Card>
    </div>
  )
}
