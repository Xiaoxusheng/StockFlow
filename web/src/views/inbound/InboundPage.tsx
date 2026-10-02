import { useState } from 'react'
import { Button, Card, Typography } from 'antd'
import { ExportOutlined, PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router'
import {
  inboundApi,
  type InboundItem,
  type InboundQuery,
  type InboundStatus,
  type InboundType,
} from '@/api/inbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 入库类型文案（business-flow.md §3.1；后端枚举冻结前未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  purchase: '采购入库',
  production: '生产入库',
  sales_return: '销售退货入库',
  transfer: '调拨入库',
  other: '其他入库',
}

const TYPE_OPTIONS: Array<{ label: string; value: InboundType }> = [
  { label: '采购入库', value: 'purchase' },
  { label: '生产入库', value: 'production' },
  { label: '销售退货入库', value: 'sales_return' },
  { label: '调拨入库', value: 'transfer' },
  { label: '其他入库', value: 'other' },
]

/** 状态选项与 types/status.ts 入库状态注册表一致 */
const STATUS_OPTIONS: Array<{ label: string; value: InboundStatus }> = [
  { label: '草稿', value: 'draft' },
  { label: '待收货', value: 'pending_receipt' },
  { label: '收货中', value: 'receiving' },
  { label: '已收货', value: 'received' },
  { label: '待质检', value: 'pending_inspection' },
  { label: '待上架', value: 'pending_putaway' },
  { label: '已上架', value: 'putaway_completed' },
  { label: '已完成', value: 'completed' },
  { label: '已关闭', value: 'closed' },
  { label: '已取消', value: 'cancelled' },
]

const COLUMNS: ColumnsType<InboundItem> = [
  { title: '入库单号', dataIndex: 'inboundNo', width: 160, fixed: 'left' },
  {
    title: '入库类型',
    dataIndex: 'inboundType',
    width: 110,
    render: (v: string) => TYPE_LABEL[v] ?? v,
  },
  { title: '来源单号', dataIndex: 'sourceNo', width: 150, render: (v?: string) => v ?? '-' },
  {
    title: '供应商',
    dataIndex: 'supplierName',
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
    title: '计划数量',
    dataIndex: 'totalQty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已收数量',
    dataIndex: 'receivedQty',
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

/** 入库管理（frontend.md F7 入库/出库流程；GET /api/inbounds，子路径未冻结） */
export default function InboundPage() {
  const [params, setParams] = useState<InboundQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<InboundItem, InboundQuery>({
    queryKey: ['inbound', 'orders'],
    fetch: (q) => inboundApi.list(q),
    params,
  })

  // 既有列保持不变，仅追加「详情」行入口（/inbound/:id，无菜单路由）
  const columns: ColumnsType<InboundItem> = [
    ...COLUMNS,
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 80,
      render: (_: unknown, record: InboundItem) => (
        <Button type="link" size="small" onClick={() => navigate(`/inbound/${record.id}`)}>
          详情
        </Button>
      ),
    },
  ]

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as InboundQuery)
    list.resetToFirstPage()
  }

  return (
    <div className="sf-page">
      <SfPageHeader
        title="入库管理"
        subtitle="收货 → 质检 → 上架（支持部分收货）"
        extra={
          <>
            <Button icon={<ExportOutlined />}>导出</Button>
            <Button type="primary" icon={<PlusOutlined />}>
              新建入库单
            </Button>
          </>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '入库单号 / 来源单号 / 供应商' },
            { name: 'inboundType', label: '入库类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<InboundItem>
          storageKey="inbound-list"
          rowKey="id"
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有入库单"
          scrollX={1330}
        />
      </Card>
    </div>
  )
}
