import { Tag } from 'antd'
import { resolveStatus, type StatusSemantic } from '@/types/status'

/**
 * 语义色 → --sf-* Token（frontend.md §24 / 任务书 §19 全局唯一映射）。
 * 不再使用 antd 预设色（gold/orange 等与 --sf-warning #d97706 不一致，且绕过 Token）；
 * 处理中按语义归 info，待处理/警告归 warning。
 * 底色/描边由 color-mix 从同一 Token 派生（12% / 24%），Light/Dark 随 Token 自动切换。
 */
const SEMANTIC_COLOR: Record<StatusSemantic, string> = {
  success: 'var(--sf-success)',
  processing: 'var(--sf-info)',
  pending: 'var(--sf-warning)',
  warning: 'var(--sf-warning)',
  danger: 'var(--sf-danger)',
  neutral: 'var(--sf-text-secondary)',
  disabled: 'var(--sf-text-muted)',
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
    <Tag
      bordered={bordered}
      style={{
        marginInlineEnd: 0,
        color,
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
        borderColor: `color-mix(in srgb, ${color} 24%, transparent)`,
      }}
    >
      {text}
    </Tag>
  )
}
