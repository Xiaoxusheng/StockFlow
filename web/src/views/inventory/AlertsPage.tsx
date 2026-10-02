import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { ExportOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type StockAlertItem, type StockAlertLevel, type StockAlertQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const LEVEL_OPTIONS: Array<{ label: string; value: StockAlertLevel }> = [
  { label: '低库存', value: 'low_stock' },
  { label: '超储', value: 'overstock' },
  { label: '临期', value: 'near_expiry' },
  { label: '过期', value: 'expired' },
  { label: '积压', value: 'slow_moving' },
]

const COLUMNS: ColumnsType<StockAlertItem> = [
  {
    title: '预警类型',
    dataIndex: 'level',
    width: 100,
    render: (v: StockAlertLevel) => <SfStatusTag status={v} />,
  },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130, fixed: 'left' },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '当前库存',
    dataIndex: 'currentQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '阈值',
    dataIndex: 'threshold',
    width: 90,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{v === undefined || v === null ? '-' : formatNumber(v)}</span>,
  },
  {
    title: '提示',
    dataIndex: 'message',
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 320 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 库存预警（低库存 / 超储 / 临期 / 过期 / 积压，frontend.md §10.1） */
export default function AlertsPage() {
  const [params, setParams] = useState<StockAlertQuery>({})
  const list = usePagedList<StockAlertItem, StockAlertQuery>({
    queryKey: ['inventory', 'alerts'],
    fetch: (q) => inventoryApi.alerts(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as StockAlertQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="库存预警"
        subtitle="低库存 / 超储 / 临期 / 过期 / 积压"
        extra={<Button icon={<ExportOutlined />}>导出</Button>}
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU / 商品名称' },
            { name: 'level', label: '预警类型', control: 'select', options: LEVEL_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<StockAlertItem>
          storageKey="inventory-alerts"
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
          emptyText="当前没有库存预警"
          scrollX={1100}
        />
      </Card>
    </div>
  )
}
