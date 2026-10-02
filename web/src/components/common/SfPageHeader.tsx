import { Flex, Typography } from 'antd'
import type { ReactNode } from 'react'

const { Title, Text } = Typography

export interface SfPageHeaderProps {
  title: ReactNode
  subtitle?: ReactNode
  /** 页面级操作区（主按钮 + 更多） */
  extra?: ReactNode
  onBack?: () => void
}

/**
 * 统一页面头（frontend.md §4.1 Page Header）
 * 紧凑单行结构：标题 + 副标题 + 右侧操作，不做大 Banner。
 */
export function SfPageHeader({ title, subtitle, extra, onBack }: SfPageHeaderProps) {
  return (
    <Flex
      align="center"
      justify="space-between"
      gap={16}
      style={{ marginBottom: 16 }}
      wrap="wrap"
    >
      <Flex align="center" gap={12} style={{ minWidth: 0 }}>
        {onBack && (
          <Typography.Link onClick={onBack} style={{ whiteSpace: 'nowrap' }}>
            返回
          </Typography.Link>
        )}
        <Title level={4} style={{ margin: 0, whiteSpace: 'nowrap' }}>
          {title}
        </Title>
        {subtitle && (
          <Text type="secondary" style={{ whiteSpace: 'nowrap' }}>
            {subtitle}
          </Text>
        )}
      </Flex>
      {extra && <Flex align="center" gap={8}>{extra}</Flex>}
    </Flex>
  )
}
