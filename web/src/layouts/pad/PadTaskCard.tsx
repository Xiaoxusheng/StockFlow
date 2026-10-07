import { useState } from 'react'
import type { KeyboardEvent, MouseEvent } from 'react'
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
  // 联动批次三 L12（frontend.md §33.4）：任务卡展示关联单号并提供复制——PC 端经既有
  // 全局搜索单号直达详情承接（searchTargets 前缀映射已存在，不自建跨端深链通道）。
  const [copied, setCopied] = useState(false)

  const handleCopy = async (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    if (!task.source_no) return
    try {
      await navigator.clipboard.writeText(task.source_no)
    } catch {
      // 非安全上下文（http）回退 execCommand
      const ta = document.createElement('textarea')
      ta.value = task.source_no
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

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
        <span className="sf-pad-task-card__no">{task.task_no}</span>
        <SfStatusTag status={task.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{PAD_TASK_TYPE_LABEL[task.task_type] ?? task.task_type}</span>
        <span>{task.warehouse_name ?? EMPTY_TEXT}</span>
      </div>
      {task.source_no ? (
        <div className="sf-pad-task-card__src">
          <span className="sf-pad-task-card__src-no">{task.source_no}</span>
          <button
            type="button"
            className={`sf-pad-task-card__copy${copied ? ' sf-pad-task-card__copy--done' : ''}`}
            onClick={handleCopy}
          >
            {copied ? '已复制' : '复制单号'}
          </button>
        </div>
      ) : null}
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(task.completed_qty)}</span>
        <span className="sf-pad-task-card__qty-unit">/ {formatNumber(task.total_qty)} 已完成</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>{task.assignee_name ?? EMPTY_TEXT}</span>
        <span>{formatDateTime(task.created_at)}</span>
      </div>
    </div>
  )
}
