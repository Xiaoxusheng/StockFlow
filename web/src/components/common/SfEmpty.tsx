import { Empty, theme } from 'antd'

export interface SfEmptyProps {
  /** 上下文空态描述（frontend.md §9.2：不要只显示“暂无数据”） */
  description?: string
}

/** 统一空态：必须由调用方提供上下文描述 */
export function SfEmpty({ description = '暂无数据' }: SfEmptyProps) {
  const { token } = theme.useToken()
  return (
    <Empty
      image={Empty.PRESENTED_IMAGE_SIMPLE}
      description={<span style={{ color: token.colorTextSecondary }}>{description}</span>}
      style={{ padding: '24px 0' }}
    />
  )
}
