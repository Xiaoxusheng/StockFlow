import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { reportsApi, type InventorySummaryQuery, type InventorySummaryRow } from '@/api/reports'
import { fetchSkuOptions, fetchWarehouseOptions } from '@/api/options'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { usePagedList } from '@/hooks/usePagedList'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatMoney, formatQty } from '@/utils/format'

const { Text } = Typography

const COLUMNS: ColumnsType<InventorySummaryRow> = [
  {
    title: '仓库',
    dataIndex: 'warehouse_code',
    width: 160,
    fixed: 'left',
    render: (v: string, record: InventorySummaryRow) => `${record.warehouse_name}（${v}）`,
  },
  { title: 'SKU 编码', dataIndex: 'sku_code', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'sku_name',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '总库存', dataIndex: 'total_qty', width: 100, align: 'right', render: num },
  { title: '可用', dataIndex: 'available_qty', width: 100, align: 'right', render: num },
  { title: '锁定', dataIndex: 'locked_qty', width: 90, align: 'right', render: num },
  { title: '冻结', dataIndex: 'frozen_qty', width: 90, align: 'right', render: num },
  { title: '待检', dataIndex: 'pending_inspect_qty', width: 90, align: 'right', render: num },
  { title: '残次', dataIndex: 'defective_qty', width: 90, align: 'right', render: num },
  { title: '库存金额', dataIndex: 'stock_value', width: 130, align: 'right', render: money },
]

function num(v: number) {
  return <span className="sf-num">{formatQty(v)}</span>
}

function money(v: number) {
  return <span className="sf-num">{formatMoney(v)}</span>
}

/**
 * 库存汇总报表（GET /api/reports/inventory-summary，reports:report:read）：
 * 按仓库/SKU 分组的库存状态与金额汇总；行结构 InventorySummaryRow（repository.go:61-76）。
 * 仓库/SKU 维度过滤（handler.go:82-89），数据权限以后端会话仓库范围快照为准。
 */
export default function ReportInventorySummary() {
  const [params, setParams] = useState<InventorySummaryQuery>({})
  // 联动批次三 L15：行点击 → /inventory/stock?sku_id=&warehouse_id= 预筛
  // （frontend.md §33.4；inventory:stock:view 门控，无权限不可点）
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const canDrillStock = canAccess(user, 'inventory:stock:view')
  const warehouseOptions = useQuery({
    queryKey: ['reports', 'options', 'warehouses'],
    queryFn: fetchWarehouseOptions,
  })
  const skuOptions = useQuery({ queryKey: ['reports', 'options', 'skus'], queryFn: fetchSkuOptions })

  const list = usePagedList<InventorySummaryRow, InventorySummaryQuery>({
    queryKey: ['reports', 'inventory-summary'],
    fetch: (q) => reportsApi.inventorySummary(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as InventorySummaryQuery)
    list.resetToFirstPage()
  }

  return (
    <Card size="small">
      <SfSearchForm
        loading={list.isFetching}
        fields={[
          {
            name: 'warehouse_id',
            label: '仓库',
            control: 'select',
            options: (warehouseOptions.data ?? []).map((w) => ({
              label: `${w.name}（${w.code}）`,
              value: String(w.id),
            })),
          },
          {
            name: 'sku_id',
            label: 'SKU',
            control: 'select',
            options: (skuOptions.data ?? []).map((s) => ({
              label: s.product_name ? `${s.code} ${s.product_name}` : s.code,
              value: String(s.id),
            })),
          },
        ]}
        onSearch={handleSearch}
      />
      <SfTable<InventorySummaryRow>
        storageKey="report-inventory-summary"
        // 行键用 code 对组合键（历史遗留写法；sku_id 后端显式 tag 修复后实测有值，预筛直接用）
        rowKey={(record) => `${record.warehouse_code}-${record.sku_code}`}
        columns={COLUMNS}
        dataSource={list.items}
        loading={list.isFetching}
        error={list.error}
        onRetry={list.refetch}
        onRefresh={list.refetch}
        pagination={list.pagination}
        total={list.total}
        onPageChange={list.onPageChange}
        emptyText="当前筛选条件下没有库存汇总数据"
        scrollX={1180}
        onRow={
          canDrillStock
            ? (record) => ({
                onClick: () =>
                  navigate(
                    `/inventory/stock?sku_id=${record.sku_id}&warehouse_id=${record.warehouse_id}`,
                  ),
                style: { cursor: 'pointer' },
              })
            : undefined
        }
      />
    </Card>
  )
}
