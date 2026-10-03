import { useMemo, useState } from 'react'
import { Button } from 'antd'
import type { TaskItem, TaskQuery, TaskStatus, TaskType } from '@/api/task'
import { taskApi } from '@/api/task'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { STATUS_META } from '@/types/status'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  PadTaskCard,
  PAD_TASK_TYPE_LABEL,
  usePadOrientation,
} from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

/** 任务类型 chip（api/task.ts TaskType 前端先行契约；文案与 PAD_TASK_TYPE_LABEL 同源） */
const TYPE_OPTIONS: Array<{ label: string; value: TaskType }> = [
  { label: '上架', value: 'putaway' },
  { label: '拣货', value: 'picking' },
  { label: '复核', value: 'checking' },
  { label: '打包', value: 'packing' },
  { label: '移库', value: 'moving' },
  { label: '盘点', value: 'counting' },
]

/** 状态 chip / 分组顺序（TaskStatus 仅 pending/in_progress/completed/cancelled 四值，
 * api/task.ts:20；标签取 types/status.ts 注册表。「异常」分组无契约状态值，待后端任务域
 * 冻结枚举后补，不造假值） */
const STATUS_OPTIONS: Array<{ label: string; value: TaskStatus }> = [
  { label: '待处理', value: 'pending' },
  { label: '进行中', value: 'in_progress' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
]

const statusLabel = (status: TaskStatus) => STATUS_META[status]?.label ?? status

/**
 * Pad 任务页（frontend.md §20.2 卡片列表 / §20.3 横屏三栏 / §20.4 竖屏堆叠）：
 * - taskApi.list（前端先行契约）→ PadTaskCard 分组卡片列表（无状态筛选时按状态分组）；
 * - taskType / status 大触摸 chip 筛选（≥44px）；
 * - 横屏左列表 + 右选中任务详情摘要（PadInfoCard），竖屏单列（详情内联在列表上方）；
 * - 底部 PadActionBar [返回][扫码][异常]（查询类页省略 [暂停][完成]）。
 */
export default function PadTasksPage() {
  const orientation = usePadOrientation()
  const [typeFilter, setTypeFilter] = useState<TaskType | 'all'>('all')
  const [statusFilter, setStatusFilter] = useState<TaskStatus | 'all'>('all')
  const [selected, setSelected] = useState<TaskItem | null>(null)

  const params = useMemo<TaskQuery>(
    () => ({
      taskType: typeFilter === 'all' ? undefined : typeFilter,
      status: statusFilter === 'all' ? undefined : statusFilter,
    }),
    [typeFilter, statusFilter],
  )

  const list = usePagedList<TaskItem, TaskQuery>({
    queryKey: ['pad', 'tasks'],
    fetch: (q) => taskApi.list(q),
    params,
    defaultPageSize: 30,
  })

  const handleTypeFilter = (value: TaskType | 'all') => {
    setTypeFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  const handleStatusFilter = (value: TaskStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  // 无状态筛选时按状态分组展示（组内仍为真实接口数据；每组数量为当前页内计数）
  const groups = useMemo(() => {
    if (statusFilter !== 'all') {
      return [
        {
          key: statusFilter,
          label: statusLabel(statusFilter),
          items: list.items.filter((task) => task.status === statusFilter),
        },
      ]
    }
    return STATUS_OPTIONS.map((option) => ({
      key: option.value,
      label: option.label,
      items: list.items.filter((task) => task.status === option.value),
    })).filter((group) => group.items.length > 0)
  }, [statusFilter, list.items])

  const detailNode = selected ? (
    <PadInfoCard
      title={`任务详情 · ${selected.taskNo}`}
      items={[
        { label: '任务号', value: selected.taskNo },
        { label: '任务类型', value: PAD_TASK_TYPE_LABEL[selected.taskType] ?? selected.taskType },
        { label: '状态', value: <SfStatusTag status={selected.status} /> },
        { label: '仓库', value: selected.warehouseName ?? EMPTY_TEXT },
        { label: '关联单号', value: selected.sourceNo ?? EMPTY_TEXT },
        { label: '负责人', value: selected.assigneeName ?? EMPTY_TEXT },
        { label: '计划数量', value: formatNumber(selected.totalQty), emphasis: true },
        { label: '已完成数量', value: formatNumber(selected.completedQty), emphasis: true },
        { label: '创建时间', value: formatDateTime(selected.createdAt) },
        ...(selected.completedAt
          ? [{ label: '完成时间', value: formatDateTime(selected.completedAt) }]
          : []),
      ]}
    />
  ) : (
    <SfEmpty description="从任务列表选择一张任务卡，此处展示任务详情摘要" />
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="任务类型筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={typeFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleTypeFilter('all')}
          >
            全部
          </Button>
          {TYPE_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={typeFilter === option.value ? 'primary' : 'default'}
              onClick={() => handleTypeFilter(option.value)}
            >
              {option.label}
            </Button>
          ))}
        </div>
        <div className="sf-pad-chip-row" role="group" aria-label="任务状态筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={statusFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleStatusFilter('all')}
          >
            全部
          </Button>
          {STATUS_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={statusFilter === option.value ? 'primary' : 'default'}
              onClick={() => handleStatusFilter(option.value)}
            >
              {option.label}
            </Button>
          ))}
        </div>
      </div>

      {list.isPending ? (
        <SfLoading rows={6} />
      ) : list.error ? (
        <SfError error={list.error} onRetry={() => void list.refetch()} />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有任务，试试切换类型或状态" />
      ) : (
        groups.map((group) => (
          <section key={group.key}>
            <h4 className="sf-pad-group-title">
              {group.label}（{group.items.length}）
            </h4>
            <div className="sf-pad-tasks-cards">
              {group.items.map((task) => (
                <PadTaskCard
                  key={task.id}
                  task={task}
                  selected={selected?.id === task.id}
                  onClick={() => setSelected(task)}
                />
              ))}
            </div>
          </section>
        ))
      )}

      <div className="sf-pad-tasks-pager">
        <span className="sf-pad-info-label">
          共 {list.total} 条 · 第 {list.pagination.current}/{totalPage} 页
        </span>
        <div className="sf-pad-pager-btns">
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={list.pagination.current <= 1}
            onClick={() => list.onPageChange(list.pagination.current - 1, list.pagination.pageSize)}
          >
            上一页
          </Button>
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={list.pagination.current >= totalPage}
            onClick={() => list.onPageChange(list.pagination.current + 1, list.pagination.pageSize)}
          >
            下一页
          </Button>
        </div>
      </div>
    </div>
  )

  return (
    <>
      {orientation === 'landscape' ? (
        // 横屏：左任务列表（46%）+ 右选中任务详情摘要（54%，actionSlot 缺省按剩余比例归一化）
        <PadPageShell ratios={[46, 54, 35]} tasksSlot={listNode} contentSlot={detailNode} />
      ) : (
        // 竖屏：单列堆叠，选中任务详情内联在列表上方；contentSlot 置空由 shell 归一化
        <PadPageShell
          ratios={[46, 54, 35]}
          tasksSlot={
            <>
              {selected && <div className="sf-pad-tasks-detail">{detailNode}</div>}
              {listNode}
            </>
          }
          contentSlot={null}
        />
      )}
      <PadActionBar hideSlots={['pause', 'finish']} />
    </>
  )
}
