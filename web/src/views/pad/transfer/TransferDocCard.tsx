import type { KeyboardEvent } from 'react'
import { ArrowRightOutlined } from '@ant-design/icons'
import type { TransferItem } from '@/api/transfer'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

export interface TransferDocCardProps {
  item: TransferItem
  /** 横屏左栏当前选中调拨单高亮 */
  selected?: boolean
  onClick?: () => void
}

/**
 * Pad 调拨单卡片（frontend.md §20.2 卡片列表 / §20.9 大触摸热区）。
 * 未复用 PadTaskCard：其 props 契约为 api/task.ts TaskItem（totalQty/completedQty 等），
 * 与调拨单 TransferItem 字段不兼容，硬套会造字段假映射；本卡沿用同一视觉形态
 * （sf-pad-tr-card 对齐 pad.css 任务卡规格），状态一律经 SfStatusTag（AGENTS.md 规则 5）。
 */
export function TransferDocCard({ item, selected = false, onClick }: TransferDocCardProps) {
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
      className={`sf-pad-tr-card${selected ? ' sf-pad-tr-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={handleKeyDown}
    >
      <div className="sf-pad-tr-card__head">
        <span className="sf-pad-tr-card__no">{item.transferNo}</span>
        <SfStatusTag status={item.status} />
      </div>
      <div className="sf-pad-tr-card__route">
        <span>{item.sourceWarehouseName}</span>
        <ArrowRightOutlined aria-hidden />
        <span>{item.targetWarehouseName}</span>
      </div>
      <div>
        {item.skuCode} · {item.productName}
      </div>
      <div className="sf-pad-tr-card__qty">
        <span className="sf-pad-metric">{formatNumber(item.qty)}</span>
        <span className="sf-pad-tr-card__qty-unit">
          {item.transferType === 'warehouse' ? '跨仓调拨' : '库位调拨'}
          {item.batchNo ? ` · 批次 ${item.batchNo}` : ''}
        </span>
      </div>
      <div className="sf-pad-tr-card__foot">
        <span>{item.createdByName ?? EMPTY_TEXT}</span>
        <span>{formatDateTime(item.createdAt)}</span>
      </div>
    </div>
  )
}
