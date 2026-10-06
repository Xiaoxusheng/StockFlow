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
import { SfViewBar } from '@/components/table/SfViewBar'

/** 实时库存（frontend.md §10.2）：统计 → 筛选 → 库存表格；点击行进入库存行详情（§10.3） */
export default function StockListPage() {
  const navigate = useNavigate()
  // 筛选与分页同步到 URL（?sku_id=..&warehouse_id=..&page=2）：刷新 / 分享链接 / 前进后退均可还原，
  // 故不再自持 useState，也不再需要 persistKey——URL 已完整承载状态
  const list = usePagedList<StockItem, StockQuery>({
    queryKey: ['inventory', 'stock'],
    fetch: (q) => inventoryApi.stock(q),
    urlSync: true,
  })
  // 汇总条：GET /api/inventory/summary 为 reports 实现、inventory 前缀挂载
  // （router.go:211 + internal/reports/routes.go:47；权限点 reports:report:read），
  // 响应为 snake_case DashboardSummary（repository.go:364-378），键名对齐 api/inventory.ts StockSummary
  const summary = useQuery({
    queryKey: ['inventory', 'stock', 'summary'],
    queryFn: inventoryApi.stockSummary,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined = 非受控（沿用 localStorage 列偏好）；
   * 应用视图后置为视图的 hidden 列键数组，用户手动改列经 onChange 回写（不禁用本地持久化） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  // 键名取 snake_case 汇总字段（StockSummary；口径见 api/inventory.ts 注释：
  // abnormal = 冻结+残次、near_expiry 含已过期）。
  // 颜色层级（「非零着色、零值退后」）：身份指标（SKU 数/总量）主文字色；
  // 可用=success、锁定=info、冻结=warning、临期/异常=danger，全部 mutedWhenZero。
  const summaryItems = [
    { label: 'SKU 数', value: summary.data?.sku_count },
    { label: '库存总量', value: summary.data?.total_qty },
    { label: '可用', value: summary.data?.available_qty, tone: 'success' as const, mutedWhenZero: true },
    { label: '锁定', value: summary.data?.locked_qty, tone: 'info' as const, mutedWhenZero: true },
    { label: '冻结', value: summary.data?.frozen_qty, tone: 'warning' as const, mutedWhenZero: true },
    { label: '临期', value: summary.data?.near_expiry_qty, tone: 'danger' as const, mutedWhenZero: true },
    { label: '异常', value: summary.data?.abnormal_qty, tone: 'danger' as const, mutedWhenZero: true },
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
              warehouse_id: list.params.warehouse_id,
              zone_id: list.params.zone_id,
              shelf_id: list.params.shelf_id,
              bin_id: list.params.bin_id,
              sku_id: list.params.sku_id,
              batch_id: list.params.batch_id,
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
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图（§2.2，验收场景 2）：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <SfViewBar
              pageKey="inventory.stock"
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
        <SfInventoryTable
          storageKey="inventory-stock"
          rowKey="id"
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
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
