import { useState } from 'react'
import { Card, Typography } from 'antd'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  COUNT_SCOPE_TYPE_LABEL,
  COUNT_TYPE_LABEL,
  countApi,
  type CountId,
  type CountQuery,
  type CountScopeType,
  type CountStatus,
  type CountTaskItem,
  type CountType,
} from '@/api/count'
import type { StatusSemantic } from '@/types/status'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatDateTime } from '@/utils/format'

const { Link, Text } = Typography

/**
 * 盘点单七态状态 → SfStatusTag（frontend.md §10.5 状态机 + types/status.ts 注册表）。
 * 待复核走注册表 pending_recheck；待审核走 pending_review（同 AdjustmentsPage 模式）。
 */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  PENDING_EXECUTE: { key: 'pending_execute', label: '待执行', semantic: 'pending' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  PENDING_APPROVAL: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

const SCOPE_OPTIONS = (Object.entries(COUNT_SCOPE_TYPE_LABEL) as Array<[CountScopeType, string]>).map(
  ([value, label]) => ({ label, value }),
)

const TYPE_OPTIONS = (Object.entries(COUNT_TYPE_LABEL) as Array<[CountType, string]>).map(([value, label]) => ({
  label,
  value,
}))

const STATUS_OPTIONS = (Object.entries(COUNT_STATUS_TAG) as Array<[CountStatus, (typeof COUNT_STATUS_TAG)[CountStatus]]>).map(
  ([value, meta]) => ({ label: meta.label, value }),
)

/** 范围列：全盘不带明细；其余「类型：明细」（business-flow.md §10.2 范围值域） */
function renderScope(record: CountTaskItem): string {
  const label = COUNT_SCOPE_TYPE_LABEL[record.scopeType] ?? record.scopeType
  if (record.scopeType === 'ALL') return label
  return `${label}：${record.scopeValue ?? '-'}`
}

/** 盘点任务列表（/counts，frontend.md §10.5：单号/仓库/范围/类型/负责人/时间/七态状态） */
export default function CountTaskListPage() {
  const [params, setParams] = useState<CountQuery>({})
  const navigate = useNavigate()
  const list = usePagedList<CountTaskItem, CountQuery>({
    queryKey: ['counts', 'tasks'],
    fetch: (q) => countApi.list(q),
    params,
  })

  const handleSearch = (values: Record<string, unknown>) => {
    setParams(values as CountQuery)
    list.resetToFirstPage()
  }

  const goDetail = (id: CountId) => navigate(`/counts/${id}`)

  const columns: ColumnsType<CountTaskItem> = [
    {
      title: '盘点单号',
      dataIndex: 'countNo',
      width: 170,
      fixed: 'left',
      render: (v: string, record: CountTaskItem) => (
        <Link onClick={() => goDetail(record.id)}>{v}</Link>
      ),
    },
    {
      title: '仓库',
      dataIndex: 'warehouseName',
      width: 120,
      ellipsis: true,
      render: (v: string) => <Text style={{ maxWidth: 120 }} ellipsis={{ tooltip: v }}>{v}</Text>,
    },
    {
      title: '范围',
      key: 'scope',
      width: 190,
      ellipsis: true,
      render: (_: unknown, record: CountTaskItem) => renderScope(record),
    },
    {
      title: '类型',
      dataIndex: 'countType',
      width: 100,
      render: (v: CountType) => COUNT_TYPE_LABEL[v] ?? v,
    },
    { title: '负责人', dataIndex: 'ownerName', width: 100, render: (v?: string) => v ?? '-' },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '开始时间',
      dataIndex: 'startedAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '完成时间',
      dataIndex: 'completedAt',
      width: 160,
      render: (v?: string) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: CountStatus) => <CountStatusTag status={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 90,
      render: (_: unknown, record: CountTaskItem) => (
        <Link onClick={() => goDetail(record.id)} style={{ whiteSpace: 'nowrap' }}>
          查看详情
        </Link>
      ),
    },
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="盘点中心"
        subtitle="盘点任务：全盘 / 按仓 / 按库区 / 按货架 / 按库位 / 按 SKU（单号 CK- 前缀）"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '盘点单号' },
            { name: 'warehouseCode', label: '仓库', control: 'input', placeholder: '仓库编码' },
            { name: 'scopeType', label: '范围', control: 'select', options: SCOPE_OPTIONS },
            { name: 'countType', label: '类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
          ]}
          onSearch={handleSearch}
        />
        <SfTable<CountTaskItem>
          storageKey="count-tasks"
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
          emptyText="当前筛选条件下没有盘点任务"
          scrollX={1420}
        />
      </Card>
    </div>
  )
}
