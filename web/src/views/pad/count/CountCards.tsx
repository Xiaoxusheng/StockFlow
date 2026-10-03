import type { KeyboardEvent } from 'react'
import type { CountItem, CountItemStatus, CountStatus, CountTaskItem } from '@/api/count'
import { COUNT_SCOPE_TYPE_LABEL, COUNT_TYPE_LABEL } from '@/api/count'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

/** 盘点单七态状态 → SfStatusTag（frontend.md §10.5 状态机 + types/status.ts 注册表）。
 * 与 PC 端 views/count/CountTaskListPage.tsx COUNT_STATUS_TAG 同源：待复核走 pending_recheck、
 * 待审核走 pending_review；未知后端值兜底原始文案 + 中性灰（SfStatusTag 内部处理）。 */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  PENDING_EXECUTE: { key: 'pending_execute', label: '待执行', semantic: 'pending' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  PENDING_APPROVAL: { key: 'pending_review', label: '待审核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

export function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 明细行状态 → SfStatusTag（pending/counted 均已注册：待盘、已盘） */
const COUNT_ITEM_STATUS_TAG: Record<CountItemStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  PENDING: { key: 'pending', label: '待盘', semantic: 'pending' },
  COUNTED: { key: 'counted', label: '已盘', semantic: 'success' },
}

export function CountItemStatusTag({ status }: { status: CountItemStatus }) {
  const meta = COUNT_ITEM_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 范围文案：全盘不带明细；其余「类型：明细」（business-flow.md §10.2 范围值域，同 PC 列表口径） */
export function countScopeText(task: Pick<CountTaskItem, 'scopeType' | 'scopeValue'>): string {
  const label = COUNT_SCOPE_TYPE_LABEL[task.scopeType] ?? task.scopeType
  if (task.scopeType === 'ALL') return label
  return `${label}：${task.scopeValue ?? EMPTY_TEXT}`
}

function pressActivate(onClick: () => void) {
  return (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onClick()
    }
  }
}

export interface CountTaskCardProps {
  task: CountTaskItem
  /** 横屏左栏当前选中任务高亮（PadPageShell tasksSlot 约定） */
  selected?: boolean
  onClick?: () => void
}

/**
 * 盘点任务卡（frontend.md §20.2 卡片列表 / §20.9 大触摸热区；frontend.md §10.5 列表字段）：
 * countNo / 仓库 / 范围 / 类型 / 负责人 / 七态状态。PadTaskCard 原语绑定 TaskItem 类型
 * （api/task.ts），盘点单字段不同，故按同一 pad.css 卡片基元本地实现。
 */
export function CountTaskCard({ task, selected = false, onClick }: CountTaskCardProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={onClick ? pressActivate(onClick) : undefined}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{task.countNo}</span>
        <CountStatusTag status={task.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{countScopeText(task)}</span>
        <span>{COUNT_TYPE_LABEL[task.countType] ?? task.countType}</span>
        <span>{task.warehouseName || EMPTY_TEXT}</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>负责人：{task.ownerName || EMPTY_TEXT}</span>
        <span>{formatDateTime(task.createdAt)}</span>
      </div>
    </div>
  )
}

export interface CountItemRowProps {
  item: CountItem
  /** 当前库位商品（横屏中栏 / 竖屏录入区对象）高亮 */
  selected?: boolean
  onClick?: () => void
}

/** 盘点明细行卡：skuCode / binCode / batchNo / systemQty / countedQty / status（frontend.md §10.5 明细） */
export function CountItemRow({ item, selected = false, onClick }: CountItemRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={onClick ? pressActivate(onClick) : undefined}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{item.skuCode}</span>
        <CountItemStatusTag status={item.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>库位：{item.binCode || EMPTY_TEXT}</span>
        <span>批次：{item.batchNo || EMPTY_TEXT}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(item.systemQty)}</span>
        <span className="sf-pad-task-card__qty-unit">
          系统数量 · 实盘 {item.countedQty != null ? formatNumber(item.countedQty) : '未登记'}
        </span>
      </div>
    </div>
  )
}
