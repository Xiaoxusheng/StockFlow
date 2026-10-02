import { useState } from 'react'
import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundApi,
  type PackingTaskItem,
  type PackingTaskQuery,
  type PackingTaskStatus,
} from '@/api/outbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 状态选项与 types/status.ts 出库状态注册表一致（business-flow.md §8.4） */
const STATUS_OPTIONS: Array<{ label: string; value: PackingTaskStatus }> = [
  { label: '待打包', value: 'pending_pack' },
  { label: '打包中', value: 'packing' },
  { label: '已打包', value: 'packed' },
]

const COLUMNS: ColumnsType<PackingTaskItem> = [
  { title: '包裹编号', dataIndex: 'packageNo', width: 160, fixed: 'left' },
  { title: '出库单号', dataIndex: 'outboundNo', width: 160 },
  { title: '包装材料', dataIndex: 'packMaterial', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '重量',
    dataIndex: 'weight',
    width: 90,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v, 2)}</span>,
  },
  {
    title: '体积',
    dataIndex: 'volume',
    width: 90,
    align: 'right',
    render: (v?: number) => <span className="sf-num">{formatNumber(v, 2)}</span>,
  },
  { title: '快递公司', dataIndex: 'carrierName', width: 120, render: (v?: string) => v ?? '-' },
  { title: '快递单号', dataIndex: 'trackingNo', width: 150, render: (v?: string) => v ?? '-' },
  {
    title: '状态',
    dataIndex: 'status',
    width: 100,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  { title: '打包人', dataIndex: 'operatorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '打包时间',
    dataIndex: 'packedAt',
    width: 170,
    render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
  {
    title: '创建时间',
    dataIndex: 'createdAt',
    width: 170,
    render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
  },
]

/** 打包管理（/packing，business-flow.md §8.4；GET /api/outbound/packing-tasks，后端未交付呈统一错误态） */
export default function PackingPage() {
  const [params, setParams] = useState<PackingTaskQuery>({})
  const list = usePagedList<PackingTaskItem, PackingTaskQuery>({
    queryKey: ['outbound', 'packing-tasks'],
    fetch: (q) => outboundApi.packingTasks(q),
    params,
  })

  return (
    <div className="sf-page">
      <SfPageHeader
        title="打包管理"
        subtitle="打包记录：包裹 / 包装材料 / 快递信息（一单允许多包裹）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            {
              name: 'keyword',
              label: '关键词',
              control: 'input',
              placeholder: '包裹编号 / 出库单号 / 快递单号',
            },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={(values) => {
            setParams(values as PackingTaskQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<PackingTaskItem>
          storageKey="outbound-packing"
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
          emptyText="当前筛选条件下没有打包记录"
          scrollX={1420}
        />
      </Card>
    </div>
  )
}
