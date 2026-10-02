import { useState } from 'react'
import { Card, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  outboundApi,
  type CheckingTaskItem,
  type CheckingTaskQuery,
  type CheckingTaskStatus,
} from '@/api/outbound'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 状态选项与 types/status.ts 出库状态注册表一致（business-flow.md §8.3） */
const STATUS_OPTIONS: Array<{ label: string; value: CheckingTaskStatus }> = [
  { label: '待复核', value: 'pending_check' },
  { label: '复核中', value: 'checking' },
  { label: '已复核', value: 'checked' },
]

const COLUMNS: ColumnsType<CheckingTaskItem> = [
  { title: '复核任务号', dataIndex: 'checkTaskNo', width: 160, fixed: 'left' },
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
  { title: '批次', dataIndex: 'batchNo', width: 110, render: (v?: string) => v ?? '-' },
  {
    title: '复核数量',
    dataIndex: 'totalQty',
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
  { title: '复核人', dataIndex: 'operatorName', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '复核时间',
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

/** 复核管理（/checking，business-flow.md §8.3；GET /api/outbound/checking-tasks，后端未交付呈统一错误态） */
export default function CheckingPage() {
  const [params, setParams] = useState<CheckingTaskQuery>({})
  const list = usePagedList<CheckingTaskItem, CheckingTaskQuery>({
    queryKey: ['outbound', 'checking-tasks'],
    fetch: (q) => outboundApi.checkingTasks(q),
    params,
  })

  return (
    <div className="sf-page">
      <SfPageHeader
        title="复核管理"
        subtitle="复核确认：SKU / 条码 / 数量 / 批次 / 序列号 / 订单"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            {
              name: 'keyword',
              label: '关键词',
              control: 'input',
              placeholder: '复核任务号 / 出库单号 / SKU',
            },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          onSearch={(values) => {
            setParams(values as CheckingTaskQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<CheckingTaskItem>
          storageKey="outbound-checking"
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
          emptyText="当前筛选条件下没有复核任务"
          scrollX={1380}
        />
      </Card>
    </div>
  )
}
