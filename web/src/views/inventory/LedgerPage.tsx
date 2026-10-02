import { useState } from 'react'
import { Card, Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { inventoryApi, type LedgerItem, type LedgerQuery } from '@/api/inventory'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

const BIZ_TYPE_OPTIONS = [
  { label: '采购入库', value: 'purchase_in' },
  { label: '销售出库', value: 'sale_out' },
  { label: '调拨', value: 'transfer' },
  { label: '盘点调整', value: 'count_adjust' },
  { label: '其他', value: 'other' },
]

const DIRECTION_OPTIONS = [
  { label: '入库', value: 'in' },
  { label: '出库', value: 'out' },
]

const COLUMNS: ColumnsType<LedgerItem> = [
  {
    title: '时间',
    dataIndex: 'createdAt',
    width: 160,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  { title: '单据编号', dataIndex: 'bizNo', width: 150, fixed: 'left' },
  { title: '业务类型', dataIndex: 'bizType', width: 100 },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'productName',
    width: 200,
    ellipsis: true,
    render: (v: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: v }}>{v}</Text>,
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  { title: '库位', dataIndex: 'binCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '方向',
    dataIndex: 'direction',
    width: 90,
    render: (v: LedgerItem['direction']) =>
      v === 'in' ? (
        <Text style={{ color: 'var(--sf-success)' }}>
          <ArrowDownOutlined /> 入库
        </Text>
      ) : (
        <Text style={{ color: 'var(--sf-danger)' }}>
          <ArrowUpOutlined /> 出库
        </Text>
      ),
  },
  {
    title: '数量',
    dataIndex: 'qty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动前',
    dataIndex: 'beforeQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '变动后',
    dataIndex: 'afterQty',
    width: 90,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  { title: '操作人', dataIndex: 'operatorName', width: 100 },
]

/** 库存流水：所有库存变化必须可追溯（inventory-rules.md，禁止前端拼装数据） */
export default function LedgerPage() {
  const [params, setParams] = useState<LedgerQuery>({})
  const list = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['inventory', 'ledger'],
    fetch: (q) => inventoryApi.ledger(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as LedgerQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader title="库存流水" subtitle="库存变化完整轨迹，与库存保持一致" />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: 'SKU / 单据编号 / 商品名称' },
            { name: 'bizType', label: '业务类型', control: 'select', options: BIZ_TYPE_OPTIONS },
            { name: 'direction', label: '方向', control: 'select', options: DIRECTION_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<LedgerItem>
          storageKey="inventory-ledger"
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
          emptyText="当前筛选条件下没有库存流水"
          scrollX={1560}
        />
      </Card>
    </div>
  )
}
