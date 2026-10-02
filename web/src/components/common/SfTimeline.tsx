import { Flex, Timeline, Typography } from 'antd'
import type { ReactNode } from 'react'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { resolveStatus, type StatusSemantic } from '@/types/status'
import { formatDateTime } from '@/utils/format'

const { Text } = Typography

export interface SfTimelineStep {
  /** 节点唯一标识 */
  key: string | number
  /** 环节名（如「创建」「收货」，frontend.md §7） */
  title: ReactNode
  /** 环节状态 key（types/status.ts 注册表，经 SfStatusTag 展示） */
  status?: string
  /** 状态未注册时的兜底文案 */
  statusLabel?: string
  /** 状态未注册时的兜底语义 */
  statusSemantic?: StatusSemantic
  /** 环节时间（utils/format 统一格式化；未完成为空） */
  time?: string | number | null
  /** 操作人 */
  operator?: ReactNode
  /** 备注 */
  remark?: ReactNode
}

export interface SfTimelineProps {
  steps: SfTimelineStep[]
  /** 步骤为空时的空态文案（frontend.md §9.2） */
  emptyText?: string
}

/** 状态语义 → Timeline 圆点色（与 SfStatusTag §24 语义色一一对应） */
const SEMANTIC_DOT: Record<StatusSemantic, string> = {
  success: 'green',
  processing: 'blue',
  pending: 'gold',
  warning: 'orange',
  danger: 'red',
  neutral: 'gray',
  disabled: 'gray',
}

/**
 * 统一业务时间线（frontend.md §7 / §23）：
 * 每步展示 状态 + 时间 + 操作人 + 备注；状态一律经 SfStatusTag，时间统一走 utils/format。
 * 节点数据由调用方按业务流程组装（入库：创建→审核→收货→质检→上架；出库：分配→拣货→…）。
 */
export function SfTimeline({ steps, emptyText = '暂无流程节点记录' }: SfTimelineProps) {
  if (steps.length === 0) {
    return <SfEmpty description={emptyText} />
  }
  return (
    <Timeline
      items={steps.map((step) => {
        const semantic: StatusSemantic =
          (step.status ? resolveStatus(step.status)?.semantic : undefined) ??
          step.statusSemantic ??
          'neutral'
        return {
          key: step.key,
          color: SEMANTIC_DOT[semantic],
          children: (
            <Flex vertical gap={2}>
              <Flex align="center" gap={8} wrap="wrap">
                <Text strong>{step.title}</Text>
                {step.status ? (
                  <SfStatusTag status={step.status} />
                ) : (
                  <SfStatusTag label={step.statusLabel ?? '未开始'} semantic={semantic} />
                )}
              </Flex>
              <Flex gap={12} wrap="wrap">
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {formatDateTime(step.time)}
                </Text>
                {step.operator != null && step.operator !== '' && (
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    操作人：{step.operator}
                  </Text>
                )}
              </Flex>
              {step.remark != null && step.remark !== '' && (
                <Text type="secondary" style={{ fontSize: 12 }}>
                  备注：{step.remark}
                </Text>
              )}
            </Flex>
          ),
        }
      })}
    />
  )
}
