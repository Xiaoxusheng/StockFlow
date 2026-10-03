import type { KeyboardEvent } from 'react'
import type { TaskItem } from '@/api/task'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

/** 任务类型文案（api/task.ts TaskType 前端先行契约；后端枚举冻结前未知值回退展示原始值，
 * 与 views/task/MyTasksPage.tsx TYPE_LABEL 同源。跨 Pad 卡片复用经 PAD_TASK_TYPE_LABEL 导出） */
export const PAD_TASK_TYPE_LABEL: Record<string, string | undefined> = {
  putaway: '上架任务',
  picking: '拣货任务',
  checking: '复核任务',
  packing: '打包任务',
  moving: '移库任务',
  counting: '盘点任务',
}

export interface PadTaskCardProps {
  task: TaskItem
  /** 横屏左栏当前选中任务高亮 */
  selected?: boolean
  onClick?: () => void
}

/**
 * Pad 任务卡原语（frontend.md §20.2 卡片列表 / §20.9 大触摸热区）：
 * 整卡可点（min-height 64px）、selected 高亮；状态一律经 SfStatusTag（AGENTS.md 规则 5）。
 */
export function PadTaskCard({ task, selected = false, onClick }: PadTaskCardProps) {
  const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!onClick) return
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onClick()
    }
  }

  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={handleKeyDown}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{task.taskNo}</span>
        <SfStatusTag status={task.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{PAD_TASK_TYPE_LABEL[task.taskType] ?? task.taskType}</span>
        <span>{task.warehouseName ?? EMPTY_TEXT}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(task.completedQty)}</span>
        <span className="sf-pad-task-card__qty-unit">/ {formatNumber(task.totalQty)} 已完成</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>{task.assigneeName ?? EMPTY_TEXT}</span>
        <span>{formatDateTime(task.createdAt)}</span>
      </div>
    </div>
  )
}
