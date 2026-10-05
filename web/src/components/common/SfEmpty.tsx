import { Empty, theme } from 'antd'
import type { ReactNode } from 'react'

export interface SfEmptyProps {
  /** 上下文空态描述（frontend.md §9.2：不要只显示"暂无数据"） */
  description?: string
  /** 空态标题（frontend.md §31 #8：图标+标题+说明+操作；可选，渲染在描述上方） */
  title?: ReactNode
  /** 空态 CTA（frontend.md §31 #8；可选，经 antd Empty footer 区渲染在描述下方） */
  action?: ReactNode
  /** 透传到 Empty 根节点（SfTable 传 sf-table-empty 启用进场动效） */
  className?: string
}

/** 统一空态：必须由调用方提供上下文描述 */
export function SfEmpty({ description = '暂无数据', title, action, className }: SfEmptyProps) {
  const { token } = theme.useToken()
  return (
    <Empty
      image={Empty.PRESENTED_IMAGE_SIMPLE}
      className={className}
      description={
        // title 缺省时 fragment 内仅剩原 span，渲染树与既有版本完全一致（Pad 端 11 处引用零影响）
        <>
          {title != null && <div className="sf-empty__title">{title}</div>}
          <span style={{ color: token.colorTextSecondary }}>{description}</span>
        </>
      }
      style={{ padding: '24px 0' }}
    >
      {action}
    </Empty>
  )
}
