import { useState } from 'react'
import { Button, Card, Flex, Skeleton, Statistic, Typography } from 'antd'
import { ExportOutlined, PlusOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type StockItem, type StockQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfError } from '@/components/common/SfError'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDate, formatNumber } from '@/utils/format'

const { Text } = Typography

const STATUS_OPTIONS = [
  { label: '正常', value: 'normal' },
  { label: '锁定', value: 'locked' },
  { label: '冻结', value: 'frozen' },
]

const COLUMNS: ColumnsType<StockItem> = [
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130, fixed: 'left' },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库区', dataIndex: 'zoneCode', width: 90, render: (v?: string) => v ?? '-' },
  { title: '库位', dataIndex: 'binCode', width: 110 },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '总库存',
    dataIndex: 'totalQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '可用',
    dataIndex: 'availableQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '锁定',
    dataIndex: 'lockedQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '冻结',
    dataIndex: 'frozenQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '效期', dataIndex: 'expiryDate', width: 110, render: (v?: string) => formatDate(v) },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  {
    title: '更新时间',
    dataIndex: 'updatedAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDate(v)}</span>,
  },
]

/** 实时库存（frontend.md §10.2）：统计 → 筛选 → 库存表格 */
export default function StockListPage() {
  const [params, setParams] = useState<StockQuery>({})
  const list = usePagedList<StockItem, StockQuery>({
    queryKey: ['inventory', 'stock'],
    fetch: (q) => inventoryApi.stock(q),
    params,
  })
  const summary = useQuery({
    queryKey: ['inventory', 'stock', 'summary'],
    queryFn: inventoryApi.stockSummary,
  })

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
          <>
            <Button icon={<ExportOutlined />}>导出</Button>
            <Button type="primary" icon={<PlusOutlined />}>
              库存调整
            </Button>
          </>
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
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU / 商品名称 / 条码' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
            { name: 'binCode', label: '库位', control: 'input' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
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
          scrollX={1460}
        />
      </Card>
    </div>
  )
}
