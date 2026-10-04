import type { KeyboardEvent } from 'react'
import { ArrowRightOutlined } from '@ant-design/icons'
import type { TransferOrder, TransferStatus } from '@/api/transfer'
import { TRANSFER_STATUS_TAG } from '@/api/transfer'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'

export interface TransferDocCardProps {
  /** 调拨单（TransferOrderView，internal/stockops/handler.go:27-42：明细走详情接口） */
  item: TransferOrder
  /** 仓库 id → 名称（列表出参无联表名称，经基础资料 options 映射；失败降级为 #id） */
  fromWarehouseName: string
  toWarehouseName: string
  /** 横屏左栏当前选中调拨单高亮 */
  selected?: boolean
  onClick?: () => void
}

/** 调拨状态 → SfStatusTag（七态大写枚举经 TRANSFER_STATUS_TAG 显式指定 label/semantic，
 * 注册表 approved/transfer 调拨语境文案覆盖见 api/transfer.ts 注释） */
export function TransferStatusTag({ status }: { status: TransferStatus }) {
  const meta = TRANSFER_STATUS_TAG[status]
  return <SfStatusTag label={meta?.label ?? status} semantic={meta?.semantic ?? 'neutral'} />
}

/** 类型文案（models.go:153-154：WAREHOUSE 跨仓 / BIN 库位间） */
export const TRANSFER_TYPE_LABEL: Record<TransferOrder['type'], string> = {
  WAREHOUSE: '跨仓调拨',
  BIN: '库位调拨',
}

/**
 * Pad 调拨单卡片（frontend.md §20.2 卡片列表 / §20.9 大触摸热区）。
 * 未复用 PadTaskCard：其 props 契约为 api/task.ts TaskItem（totalQty/completedQty 等），
 * 与调拨单 TransferOrder 字段不兼容，硬套会造字段假映射；本卡沿用同一视觉形态
 * （sf-pad-tr-card 对齐 pad.css 任务卡规格），状态一律经 SfStatusTag（AGENTS.md 规则 5）。
 * 出参为 ID + 状态 + 时间的单据头（无 SKU/数量联表，明细走 GET /api/transfers/{id}），
 * 禁止页面假造 SKU/数量字段。
 */
export function TransferDocCard({
  item,
  fromWarehouseName,
  toWarehouseName,
  selected = false,
  onClick,
}: TransferDocCardProps) {
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
        <span className="sf-pad-tr-card__no">{item.transfer_no}</span>
        <TransferStatusTag status={item.status} />
      </div>
      <div className="sf-pad-tr-card__route">
        <span>{fromWarehouseName}</span>
        <ArrowRightOutlined aria-hidden />
        <span>{toWarehouseName}</span>
      </div>
      <div className="sf-pad-tr-card__route">
        <span>{TRANSFER_TYPE_LABEL[item.type] ?? item.type}</span>
        {item.remark && <span>备注 {item.remark}</span>}
      </div>
      <div className="sf-pad-tr-card__foot">
        <span>创建 {formatDateTime(item.created_at)}</span>
        <span>{item.received_at ? `入库 ${formatDateTime(item.received_at)}` : EMPTY_TEXT}</span>
      </div>
    </div>
  )
}
