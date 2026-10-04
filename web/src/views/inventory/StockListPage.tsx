import { useState } from 'react'
import { Card } from 'antd'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { inventoryApi, type StockItem, type StockQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfExportButton } from '@/components/common/SfExportButton'
import { SfInventorySummary } from '@/components/common/SfInventorySummary'
import { SfInventoryTable } from '@/components/common/SfInventoryTable'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'

/** 实时库存（frontend.md §10.2）：统计 → 筛选 → 库存表格；点击行进入库存行详情（§10.3） */
export default function StockListPage() {
  const navigate = useNavigate()
  const [params, setParams] = useState<StockQuery>({})
  // §26.3：分页经 persistKey 持久化，进入详情再返回时恢复离开前分页
  const list = usePagedList<StockItem, StockQuery>({
    queryKey: ['inventory', 'stock'],
    fetch: (q) => inventoryApi.stock(q),
    params,
    persistKey: 'inventory-stock',
  })
  // 汇总条：GET /api/inventory/summary 为 reports 实现、inventory 前缀挂载
  // （router.go:211 + internal/reports/routes.go:47；权限点 reports:report:read），
  // 响应为 snake_case DashboardSummary（repository.go:364-378），键名对齐 api/inventory.ts StockSummary
  const summary = useQuery({
    queryKey: ['inventory', 'stock', 'summary'],
    queryFn: inventoryApi.stockSummary,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as StockQuery)
    list.resetToFirstPage()
  }

  // 键名取 snake_case 汇总字段（StockSummary；口径见 api/inventory.ts 注释：
  // abnormal = 冻结+残次、near_expiry 含已过期）
  const summaryItems = [
    { label: 'SKU 数', value: summary.data?.sku_count },
    { label: '库存总量', value: summary.data?.total_qty },
    { label: '可用', value: summary.data?.available_qty },
    { label: '锁定', value: summary.data?.locked_qty },
    { label: '冻结', value: summary.data?.frozen_qty },
    { label: '临期', value: summary.data?.near_expiry_qty, danger: true },
    { label: '异常', value: summary.data?.abnormal_qty, danger: true },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="实时库存"
        subtitle="SKU / 库位 / 批次维度的实时库存"
        extra={
          <SfExportButton
            module="INVENTORY"
            /* 按钮级权限对齐创建导出任务的真实权限点 datax:export:create
               （internal/auth/permissions.go:307）——持列表权限而无导出权限者不渲染该按钮 */
            permission="datax:export:create"
            scopeParams={{
              warehouse_id: params.warehouse_id,
              zone_id: params.zone_id,
              shelf_id: params.shelf_id,
              bin_id: params.bin_id,
              sku_id: params.sku_id,
              batch_id: params.batch_id,
            }}
          />
        }
      />

      <SfInventorySummary
        style={{ marginBottom: 16 }}
        items={summaryItems}
        loading={summary.isPending}
        error={summary.error}
        onRetry={summary.refetch}
      />

      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'sku_id', label: 'SKU ID', control: 'input', placeholder: 'SKU ID（正整数）' },
            { name: 'warehouse_id', label: '仓库 ID', control: 'input', placeholder: '仓库 ID（正整数）' },
            { name: 'bin_id', label: '库位 ID', control: 'input', placeholder: '库位 ID（正整数）' },
            { name: 'batch_id', label: '批次 ID', control: 'input', placeholder: '批次 ID（0=非批次）' },
          ]}
          onSearch={handleSearch}
        />
        <SfInventoryTable
          storageKey="inventory-stock"
          rowKey="id"
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有库存"
          scrollX={1520}
          onRow={(record: StockItem) => ({
            onClick: () => navigate(`/inventory/stock/${encodeURIComponent(String(record.id))}`),
            style: { cursor: 'pointer' },
          })}
        />
      </Card>
    </div>
  )
}
