import type { KeyboardEvent, ReactNode } from 'react'
import { Button } from 'antd'
import type { ExceptionItem } from '@/api/exception'
import { EXCEPTION_TYPE_LABEL } from '@/api/exception'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'

function pressActivate(onClick: () => void) {
  return (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onClick()
    }
  }
}

export interface ExceptionCardProps {
  item: ExceptionItem
  /** 当前选中异常高亮（横屏左栏 / 竖屏顶部） */
  selected?: boolean
  onClick?: () => void
}

/**
 * 异常卡（frontend.md §20.2 卡片列表 / §20.9 大触摸热区；business-flow.md §11.2）：
 * exceptionNo / exceptionType / title / warehouseName / skuCode / bizNo / status / discoveredAt。
 * 异常类型是分类不是状态，按普通文本渲染（api/exception.ts 约定）；
 * 生命周期七态经 SfStatusTag（types/status.ts 注册表，未知值兜底原始文案）。
 */
export function ExceptionCard({ item, selected = false, onClick }: ExceptionCardProps) {
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
        <span className="sf-pad-task-card__no">{item.exceptionNo}</span>
        <SfStatusTag status={item.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{EXCEPTION_TYPE_LABEL[item.exceptionType] ?? item.exceptionType}</span>
        <span>{item.warehouseName || EMPTY_TEXT}</span>
        {item.skuCode ? <span>SKU {item.skuCode}</span> : null}
      </div>
      <div className="sf-pad-task-card__qty">
        <span style={{ overflowWrap: 'anywhere' }}>{item.title}</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>来源：{item.bizNo || EMPTY_TEXT}</span>
        <span>{formatDateTime(item.discoveredAt)}</span>
      </div>
    </div>
  )
}

export interface ActionPlaceholderProps {
  label: string
  icon?: ReactNode
  /** 禁用原因（死按钮门禁：disabled 必须给原因，点按 Toast 送达） */
  reason: string
  notify: (message: string) => void
}

/**
 * 动作占位按钮（frontend.md §9.1 Disabled 口径，同地基 PadActionBar 禁用包装模式）：
 * 端点未交付的动作（认领/处理/关闭/拍照）disabled + 点按 Toast 原因，不假装可用。
 */
export function ActionPlaceholder({ label, icon, reason, notify }: ActionPlaceholderProps) {
  return (
    <span
      role="button"
      tabIndex={0}
      aria-label={`${label}（不可用：${reason}）`}
      style={{ display: 'block', cursor: 'not-allowed' }}
      onClick={() => notify(reason)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          notify(reason)
        }
      }}
    >
      <Button block size="large" icon={icon} disabled style={{ height: 'var(--sf-pad-touch-min)' }}>
        {label}
      </Button>
    </span>
  )
}
