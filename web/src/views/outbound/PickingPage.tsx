import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundApi,
  type PickingTaskItem,
  type PickingTaskQuery,
  type PickingTaskStatus,
} from '@/api/outbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 types/status.ts 出库状态注册表一致（business-flow.md §8.2） */
const STATUS_OPTIONS: Array<{ label: string; value: PickingTaskStatus }> = [
  { label: '待拣货', value: 'pending_pick' },
  { label: '拣货中', value: 'picking' },
  { label: '已拣货', value: 'picked' },
]

const COLUMNS: ColumnsType<PickingTaskItem> = [
  { title: '拣货任务号', dataIndex: 'pickTaskNo', width: 160, fixed: 'left' },
  { title: '出库单号', dataIndex: 'outboundNo', width: 160 },
  { title: 'SKU 编码', dataIndex: 'skuCode', width: 130 },
  {
    title: '商品名称',
    dataIndex: 'skuName',
    width: 180,
    ellipsis: true,
    render: (v?: string) =>
      v ? (
        <Text style={{ maxWidth: 180 }} ellipsis={{ tooltip: v }}>
          {v}
        </Text>
      ) : (
        '-'
      ),
  },
  { title: '来源库位', dataIndex: 'fromBinCode', width: 110, render: (v?: string) => v ?? '-' },
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '应拣数量',
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
  { title: '操作人', dataIndex: 'operatorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '完成时间',
    dataIndex: 'completedAt',
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

/** 拣货管理（/picking，business-flow.md §8.2；GET /api/outbound/picking-tasks，后端未交付呈统一错误态） */
export default function PickingPage() {
  const [params, setParams] = useState<PickingTaskQuery>({})
  const list = usePagedList<PickingTaskItem, PickingTaskQuery>({
    queryKey: ['outbound', 'picking-tasks'],
    fetch: (q) => outboundApi.pickingTasks(q),
    params,
  })

  return (
    <div className="sf-page">
      <SfPageHeader
        title="拣货管理"
        subtitle="拣货任务：SKU → 来源库位 → 数量 → 操作人 → 完成时间"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            {
              name: 'keyword',
              label: '关键词',
              control: 'input',
              placeholder: '拣货任务号 / 出库单号 / SKU',
            },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={(values) => {
            setParams(values as PickingTaskQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<PickingTaskItem>
          storageKey="outbound-picking"
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
          emptyText="当前筛选条件下没有拣货任务"
          scrollX={1590}
        />
      </Card>
    </div>
  )
}
