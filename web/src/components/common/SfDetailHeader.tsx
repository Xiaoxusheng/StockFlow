import { Flex, Typography } from 'antd'
import type { ReactNode } from 'react'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'

const { Title } = Typography

export interface SfDetailHeaderProps {
  /** 单号 / 业务主键（frontend.md §7 Header 主标题） */
  code: ReactNode
  /** 业务状态 key（types/status.ts 注册表，经 SfStatusTag 展示） */
  status?: string
  /** 状态未注册时的兜底文案 */
  statusLabel?: string
  /** 状态未注册时的兜底语义 */
  statusSemantic?: StatusSemantic
  /** 关键指标插槽（配合 SfSummaryBar，frontend.md §7 顶部指标行） */
  summary?: ReactNode
  /** 关键操作插槽（仅放真实可用操作，frontend.md §7） */
  actions?: ReactNode
  /** 返回回调（详情页 → 列表页） */
  onBack?: () => void
}

/**
 * 统一详情页头（frontend.md §7 / §23）：
 * 单号 + 状态 + 关键操作 + 关键指标；紧凑单行结构，状态用 SfStatusTag，禁止巨大 Banner。
 */
export function SfDetailHeader({
  code,
  status,
  statusLabel,
  statusSemantic,
  summary,
  actions,
  onBack,
}: SfDetailHeaderProps) {
  return (
    <Flex vertical gap={8} style={{ marginBottom: 16 }}>
      <Flex align="center" justify="space-between" gap={16} wrap="wrap">
        <Flex align="center" gap={12} style={{ minWidth: 0 }}>
          {onBack && (
            <Typography.Link onClick={onBack} style={{ whiteSpace: 'nowrap' }}>
              返回
            </Typography.Link>
          )}
          <Title level={4} style={{ margin: 0 }}>
            {code}
          </Title>
          {(status || statusLabel) && (
            <SfStatusTag status={status} label={statusLabel} semantic={statusSemantic} />
          )}
        </Flex>
        {actions && <Flex align="center" gap={8}>{actions}</Flex>}
      </Flex>
      {summary}
    </Flex>
  )
}
