import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { ExportOutlined, PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundApi,
  type OutboundItem,
  type OutboundQuery,
  type OutboundStatus,
  type OutboundType,
} from '@/api/outbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 出库类型文案（business-flow.md §7.1；后端枚举冻结前未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  sales: '销售出库',
  production: '生产领料',
  transfer: '调拨出库',
  other: '其他出库',
  loss: '报损出库',
}

const TYPE_OPTIONS: Array<{ label: string; value: OutboundType }> = [
  { label: '销售出库', value: 'sales' },
  { label: '生产领料', value: 'production' },
  { label: '调拨出库', value: 'transfer' },
  { label: '其他出库', value: 'other' },
  { label: '报损出库', value: 'loss' },
]

/** 状态选项与 types/status.ts 出库状态注册表一致 */
const STATUS_OPTIONS: Array<{ label: string; value: OutboundStatus }> = [
  { label: '待分配', value: 'pending_allocate' },
  { label: '已分配', value: 'allocated' },
  { label: '待拣货', value: 'pending_pick' },
  { label: '拣货中', value: 'picking' },
  { label: '已拣货', value: 'picked' },
  { label: '待复核', value: 'pending_check' },
  { label: '待打包', value: 'pending_pack' },
  { label: '待发货', value: 'pending_shipment' },
  { label: '已发货', value: 'shipped' },
  { label: '发货异常', value: 'shipment_exception' },
  { label: '已完成', value: 'completed' },
  { label: '已关闭', value: 'closed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<OutboundItem> = [
  { title: '出库单号', dataIndex: 'outboundNo', width: 160, fixed: 'left' },
  {
    title: '出库类型',
    dataIndex: 'outboundType',
    width: 110,
    render: (v: string) => TYPE_LABEL[v] ?? v,
  },
  { title: '来源单号', dataIndex: 'sourceNo', width: 150, render: (v?: string) => v ?? '-' },
  {
    title: '客户',
    dataIndex: 'customerName',
    width: 160,
    ellipsis: true,
    render: (v?: string) => (
      <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>
        {v ?? '-'}
      </Text>
    ),
  },
  { title: '仓库', dataIndex: 'warehouseName', width: 100 },
  {
    title: '需求数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已拣数量',
    dataIndex: 'pickedQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 100,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  { title: '创建人', dataIndex: 'operatorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 170,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 出库管理（frontend.md F7 入库/出库流程；GET /api/outbounds，子路径未冻结） */
export default function OutboundPage() {
  const [params, setParams] = useState<OutboundQuery>({})
  const list = usePagedList<OutboundItem, OutboundQuery>({
    queryKey: ['outbound', 'orders'],
    fetch: (q) => outboundApi.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as OutboundQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="出库管理"
        subtitle="分配 → 拣货 → 复核 → 打包 → 发货"
        extra={
          <>
            <Button icon={<ExportOutlined />}>导出</Button>
            <Button type="primary" icon={<PlusOutlined />}>
              新建出库单
            </Button>
          </>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '出库单号 / 来源单号 / 客户' },
            { name: 'outboundType', label: '出库类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<OutboundItem>
          storageKey="outbound-list"
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
          emptyText="当前筛选条件下没有出库单"
          scrollX={1250}
        />
      </Card>
    </div>
  )
}
