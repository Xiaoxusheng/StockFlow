import type { KeyboardEvent, ReactNode } from 'react'
import { Button, message } from 'antd'
import type { ExceptionItem, ExceptionStatus } from '@/api/exception'
import { EXCEPTION_STATUS_TAG } from '@/api/exception'
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

/** 异常状态 → SfStatusTag（六态大写枚举经 EXCEPTION_STATUS_TAG 显式指定 label/semantic，
 * OPEN/PENDING_REVIEW 注册表不命中按覆盖路径生效，见 api/exception.ts 注释） */
export function ExceptionStatusTag({ status }: { status: ExceptionStatus }) {
  const meta = EXCEPTION_STATUS_TAG[status]
  return <SfStatusTag status={status} label={meta?.label} semantic={meta?.semantic} />
}

/**
 * 异常卡（frontend.md §20.2 卡片列表 / §20.9 大触摸热区；business-flow.md §11.2）：
 * exception_no / type / source_type / detail / source_no / status / created_at
 * （ExceptionView snake_case，internal/returns/service_exception.go:64-88——无仓库/标题联表
 * 字段，禁止假造）。异常类型是分类不是状态，按普通文本渲染（api/exception.ts 约定）；
 * 生命周期六态经 SfStatusTag（未知值兜底原始文案）。
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
        <span className="sf-pad-task-card__no">{item.exception_no}</span>
        <ExceptionStatusTag status={item.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{item.type}</span>
        <span>{item.source_type || EMPTY_TEXT}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span style={{ overflowWrap: 'anywhere' }}>{item.detail || EMPTY_TEXT}</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>来源：{item.source_no || EMPTY_TEXT}</span>
        <span>{formatDateTime(item.created_at)}</span>
      </div>
    </div>
  )
}

export interface PadExceptionActionProps {
  label: string
  icon?: ReactNode
  /** 给定即禁用并说明原因（权限缺失 / 状态机前置态不满足），§21.7 口径 */
  disabledReason?: string
  loading?: boolean
  onClick?: () => void
}

/**
 * Pad 异常守卫动作按钮（死按钮门禁口径，同 PadTransferPage TransferAction 模式）：
 * disabledReason 缺省时为真实提交按钮；给定时外包 span 接管点按把原因以 Toast 送达。
 */
export function PadExceptionAction({ label, icon, disabledReason, loading = false, onClick }: PadExceptionActionProps) {
  const [messageApi, contextHolder] = message.useMessage()
  if (!disabledReason) {
    return (
      <Button
        block
        size="large"
        icon={icon}
        loading={loading}
        onClick={onClick}
        style={{ height: 'var(--sf-pad-touch-min)' }}
      >
        {label}
      </Button>
    )
  }
  return (
    <>
      {contextHolder}
      <span
        role="button"
        tabIndex={0}
        aria-label={`${label}（不可用：${disabledReason}）`}
        style={{ display: 'block', cursor: 'not-allowed' }}
        onClick={() => messageApi.warning(disabledReason)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            messageApi.warning(disabledReason)
          }
        }}
      >
        <Button block size="large" icon={icon} disabled style={{ height: 'var(--sf-pad-touch-min)' }}>
          {label}
        </Button>
      </span>
    </>
  )
}
