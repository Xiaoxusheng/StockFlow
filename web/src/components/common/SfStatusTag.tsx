import { Tag } from 'antd'
import { resolveStatus, type StatusSemantic } from '@/types/status'

/** 语义色 → antd Tag 预设色（frontend.md §24 全局唯一映射） */
const SEMANTIC_COLOR: Record<StatusSemantic, string> = {
  success: 'success',
  processing: 'processing',
  pending: 'gold',
  warning: 'orange',
  danger: 'error',
  neutral: 'default',
  disabled: 'default',
}

export interface SfStatusTagProps {
  /** 业务状态 key（types/status.ts 注册表） */
  status?: string
  /** 未注册状态直接指定文案 */
  label?: string
  /** 未注册状态直接指定语义 */
  semantic?: StatusSemantic
  bordered?: boolean
}

/**
 * 统一状态标签：所有业务状态一律走这里，禁止页面自己决定颜色。
 * 未知状态兜底为中性灰 + 原始文案，后端新增状态不阻塞页面。
 */
export function SfStatusTag({ status, label, semantic, bordered }: SfStatusTagProps) {
  const meta = resolveStatus(status)
  const text = meta?.label ?? label ?? status ?? '-'
  const color = SEMANTIC_COLOR[meta?.semantic ?? semantic ?? 'neutral']
  return (
    <Tag color={color} bordered={bordered} style={{ marginInlineEnd: 0 }}>
      {text}
    </Tag>
  )
}
