import { Card } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { DateCell } from '@/components/table/cells'
import { taskApi, type TaskItem, type TaskQuery, type TaskStatus, type TaskType } from '@/api/task'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

/** 任务类型文案（api/task.ts TaskType 三值；未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  putaway: '上架任务',
  picking: '拣货任务',
  checking: '复核任务',
}

/** 类型筛选选项（后端仅映射 putaway/picking/checking 三任务表——packing/moving/counting
 * 传值 400，api.md §9 平台批） */
const TYPE_OPTIONS: Array<{ label: string; value: TaskType }> = [
  { label: '上架任务', value: 'putaway' },
  { label: '拣货任务', value: 'picking' },
  { label: '复核任务', value: 'checking' },
]

/** 状态选项（统一五值；exception 为第五统一值——picking/checking 原态 EXCEPTION 映射） */
const STATUS_OPTIONS: Array<{ label: string; value: TaskStatus }> = [
  { label: '待处理', value: 'pending' },
  { label: '进行中', value: 'in_progress' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
  { label: '异常', value: 'exception' },
]

const COLUMNS: ColumnsType<TaskItem> = [
  { title: '任务号', dataIndex: 'task_no', width: 150, fixed: 'left' },
  {
    title: '任务类型',
    dataIndex: 'task_type',
    width: 110,
    render: (v: string) => TYPE_LABEL[v] ?? v,
  },
  { title: '关联单号', dataIndex: 'source_no', width: 150, render: (v?: string) => v ?? '-' },
  { title: '仓库', dataIndex: 'warehouse_name', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '计划数量',
    dataIndex: 'total_qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已完成',
    dataIndex: 'completed_qty',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 100,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  { title: '负责人', dataIndex: 'assignee_name', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 170,
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '完成时间',
    dataIndex: 'completed_at',
    width: 170,
    render: (v?: string) => <DateCell value={v} />,
  },
]

/** 我的任务（/tasks，menu.tsx 仓储中心子菜单；GET /api/tasks 2026-10-05 平台批已交付，
 * 行列含 raw_status 原表态）。筛选仅后端契约参数 task_type/status/warehouse_code
 * （workbench.go:865-891 仅读三键）——keyword 后端不消费，假筛选项已摘除（api.md §9 平台批）。 */
export default function MyTasksPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const list = usePagedList<TaskItem, TaskQuery>({
    queryKey: ['task', 'my'],
    fetch: (q) => taskApi.list(q),
    urlSync: true,
  })

  return (
    <div className="sf-page">
      <SfPageHeader title="我的任务" subtitle="上架 / 拣货 / 复核 作业任务" />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'task_type', label: '任务类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouse_code', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
        />
        <SfTable<TaskItem>
          storageKey="my-tasks"
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
          emptyText="当前筛选条件下没有任务"
          scrollX={1360}
        />
      </Card>
    </div>
  )
}
