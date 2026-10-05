import { useState, type ReactNode } from 'react'
import { App, Tooltip } from 'antd'
import { CheckOutlined, CopyOutlined } from '@ant-design/icons'
import {
  EMPTY_TEXT,
  formatDate,
  formatDateTime,
  formatMoney,
  formatNumber,
  formatQty,
} from '@/utils/format'

/**
 * 统一表格单元格（frontend.md §6.2 / 任务书 §35）：
 * NumberCell=数字右对齐配 sf-num；CodeCell=编码省略+hover 复制；
 * ProductCell=主名+弱化编码；DateCell=统一时间弱化。
 * 列声明职责不变：数字列仍需 align:'right'、状态列仍走 SfStatusTag。
 */

type NumberFormat = (value: number | string | null | undefined) => string

export interface NumberCellProps {
  value: number | string | null | undefined
  /** 默认千分位整数；数量/金额传 formatQty / formatMoney */
  format?: NumberFormat
  /** 核心数字（如可用库存、单号下的主数量）加 500 字重（任务书 §13） */
  strong?: boolean
}

/** 数字单元格：tabular-nums 纵向对齐，配合列 align:'right' 使用 */
export function NumberCell({ value, format = formatNumber, strong = false }: NumberCellProps) {
  return (
    <span className="sf-num" style={{ fontWeight: strong ? 500 : undefined }}>
      {format(value)}
    </span>
  )
}

export interface CodeCellProps {
  value?: string | null
  /** 复制提示宾语（任务书 §12：已复制商品编码/入库单号…） */
  label?: string
}

/** 编码单元格：ellipsis + Tooltip 完整值 + hover 显复制，不放大按钮 */
export function CodeCell({ value, label = '编码' }: CodeCellProps) {
  const { message } = App.useApp()
  const [copied, setCopied] = useState(false)
  if (!value) return <span>{EMPTY_TEXT}</span>
  const copy = () => {
    void navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true)
        message.success(`已复制${label}`)
        window.setTimeout(() => setCopied(false), 1500)
      })
      .catch(() => {
        // 剪贴板不可用（非安全上下文）：Tooltip 内已有完整值，静默降级
      })
  }
  return (
    <Tooltip title={value}>
      <span className="sf-code-cell">
        <span className="sf-code-cell__text">{value}</span>
        {copied ? (
          <CheckOutlined className="sf-code-cell__copy sf-code-cell__copy--done" />
        ) : (
          <CopyOutlined className="sf-code-cell__copy" onClick={copy} />
        )}
      </span>
    </Tooltip>
  )
}

export interface ProductCellProps {
  /** 主名（商品名称/主数据），500 字重 */
  name?: ReactNode
  /** 辅助编码（SKU/商品编码），弱化 12px */
  code?: string | null
}

/** 主名+辅助编码双行单元格（任务书 §35 ProductCell） */
export function ProductCell({ name, code }: ProductCellProps) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontWeight: 500 }}>{name ?? EMPTY_TEXT}</div>
      {code ? (
        <div
          className="sf-num"
          style={{
            fontSize: 'var(--sf-font-size-caption)',
            color: 'var(--sf-text-muted)',
          }}
        >
          {code}
        </div>
      ) : null}
    </div>
  )
}

export interface DateCellProps {
  value?: string | number | Date | null
  /** 默认含时间（YYYY-MM-DD HH:mm）；false 仅日期 */
  withTime?: boolean
}

/** 时间单元格：统一分钟精度 + 次要色弱化（任务书 §36） */
export function DateCell({ value, withTime = true }: DateCellProps) {
  return (
    <span style={{ color: 'var(--sf-text-secondary)', whiteSpace: 'nowrap' }}>
      {withTime ? formatDateTime(value) : formatDate(value)}
    </span>
  )
}

export interface SfRowActionsProps {
  children: ReactNode
}

/**
 * 行操作区显式包裹（frontend.md §31 #2）：hover/focus-within 渐显、触摸端恒显。
 * 「操作」列 td 已由 SfTable 自动注入 sf-table-actions-cell；本组件供页面在
 * 非「操作」列名场景或需要显式声明时使用，不包裹时自动注入兜底。
 */
export function SfRowActions({ children }: SfRowActionsProps) {
  return <div className="sf-table-actions">{children}</div>
}

export { formatMoney, formatNumber, formatQty }
