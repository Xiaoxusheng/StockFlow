import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Card, Flex, Skeleton, Statistic } from 'antd'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type StockItem, type StockQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfError } from '@/components/common/SfError'
import { SfExportButton } from '@/components/common/SfExportButton'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

/** §26.3 页面状态保留：筛选 + 分页持久化 key（详情返回后恢复） */
const LIST_STATE_KEY = 'sf.page.inventory-stock'
const DEFAULT_PAGE_SIZE = 20

interface StockListState {
  params: StockQuery
  page: number
  pageSize: number
}

function readListState(): StockListState | null {
  try {
    const raw = sessionStorage.getItem(LIST_STATE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as StockListState
    if (!parsed || typeof parsed !== 'object' || typeof parsed.page !== 'number') return null
    return parsed
  } catch {
    return null
  }
}

/** 数量列渲染（后端 Qty 为裸数字，identity.go:71） */
function renderQty(value: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/** 可选维度 ID（zone/shelf/batch）0 值显示占位符（handler.go 注释：0=未指定/非批次） */
function renderIdOrZero(value: StockItem['zone_id']): string {
  return String(value) === '0' ? '-' : String(value)
}

/** 库存行列（InventoryView 仅含五维定位 ID + 六状态数量，handler.go:30-41；文本联表待后端聚合字段下发） */
const COLUMNS: ColumnsType<StockItem> = [
  { title: 'SKU ID', dataIndex: 'sku_id', width: 120, fixed: 'left' },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 110 },
  { title: '库区 ID', dataIndex: 'zone_id', width: 100, render: renderIdOrZero },
  { title: '货架 ID', dataIndex: 'shelf_id', width: 100, render: renderIdOrZero },
  { title: '库位 ID', dataIndex: 'bin_id', width: 110 },
  { title: '批次 ID', dataIndex: 'batch_id', width: 110, render: renderIdOrZero },
  { title: '总库存', dataIndex: 'total_qty', width: 90, align: 'right', render: renderQty },
  { title: '可用', dataIndex: 'available_qty', width: 90, align: 'right', render: renderQty },
  { title: '锁定', dataIndex: 'locked_qty', width: 90, align: 'right', render: renderQty },
  { title: '冻结', dataIndex: 'frozen_qty', width: 90, align: 'right', render: renderQty },
  { title: '待检', dataIndex: 'pending_inspect_qty', width: 90, align: 'right', render: renderQty },
  { title: '不良', dataIndex: 'defective_qty', width: 90, align: 'right', render: renderQty },
  {
    title: '更新时间',
    dataIndex: 'updated_at',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 实时库存（frontend.md §10.2）：统计 → 筛选 → 库存表格；点击行进入库存行详情（§10.3） */
export default function StockListPage() {
  const navigate = useNavigate()
  // §26.3：进入详情再返回时恢复离开前的筛选与分页
  const savedState = useRef<StockListState | null>(readListState())
  const [params, setParams] = useState<StockQuery>(() => savedState.current?.params ?? {})
  const list = usePagedList<StockItem, StockQuery>({
    queryKey: ['inventory', 'stock'],
    fetch: (q) => inventoryApi.stock(q),
    params,
  })
  const summary = useQuery({
    queryKey: ['inventory', 'stock', 'summary'],
    queryFn: inventoryApi.stockSummary,
  })

  // §26.3：恢复离开前的分页（筛选已由 params 初始值恢复；只跑一次）
  const restoredRef = useRef(false)
  const onPageChange = list.onPageChange
  useEffect(() => {
    if (restoredRef.current) return
    restoredRef.current = true
    const state = savedState.current
    if (state && (state.page !== 1 || state.pageSize !== DEFAULT_PAGE_SIZE)) {
      onPageChange(state.page, state.pageSize)
    }
  }, [onPageChange])

  // §26.3：筛选 / 分页变化即持久化，供详情返回后恢复
  useEffect(() => {
    try {
      sessionStorage.setItem(
        LIST_STATE_KEY,
        JSON.stringify({
          params,
          page: list.pagination.current,
          pageSize: list.pagination.pageSize,
        } satisfies StockListState),
      )
    } catch {
      // 存储不可用时降级为不保留
    }
  }, [params, list.pagination])

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as StockQuery)
    list.resetToFirstPage()
  }

  const summaryItems = [
    { label: 'SKU 数', value: summary.data?.skuCount },
    { label: '库存总量', value: summary.data?.totalQty },
    { label: '可用', value: summary.data?.availableQty },
    { label: '锁定', value: summary.data?.lockedQty },
    { label: '冻结', value: summary.data?.frozenQty },
    { label: '临期', value: summary.data?.nearExpiryQty, danger: true },
    { label: '异常', value: summary.data?.abnormalQty, danger: true },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="实时库存"
        subtitle="SKU / 库位 / 批次维度的实时库存"
        extra={
          <SfExportButton
            module="INVENTORY"
            permission="inventory:inventory:list"
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

      <Card size="small" styles={{ body: { padding: '12px 16px' } }} style={{ marginBottom: 16 }}>
        {summary.isPending ? (
          <Flex gap={32}>
            {summaryItems.map((item) => (
              <Skeleton.Node active key={item.label} style={{ width: 64, height: 40 }} />
            ))}
          </Flex>
        ) : summary.error ? (
          <SfError error={summary.error} onRetry={summary.refetch} />
        ) : (
          <Flex gap={0} wrap="wrap">
            {summaryItems.map((item, index) => (
              <Statistic
                key={item.label}
                title={item.label}
                value={formatNumber(item.value ?? 0)}
                valueStyle={{
                  fontSize: 18,
                  color: item.danger ? 'var(--sf-danger)' : undefined,
                }}
                style={{ padding: '0 24px', borderRight: index < summaryItems.length - 1 ? '1px solid var(--sf-border-subtle)' : undefined }}
              />
            ))}
          </Flex>
        )}
      </Card>

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
        <SfTable<StockItem>
          storageKey="inventory-stock"
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
