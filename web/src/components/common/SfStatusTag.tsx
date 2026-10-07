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
/**
 * 统一状态标签（frontend.md §24，2026-10-06 样式改版）：**色点 + 文字**式——
 * 无底色、无边框（原彩色 chip 底色/描边样式废弃，用户口径：装饰性彩色底太重）。
 * 语义仍由「圆点颜色 + 文字颜色」承担（同一 SEMANTIC_COLOR 色源），全站状态观感统一克制。
 * bordered prop 保留签名兼容（改版后无描边概念，内部忽略）。
 */
export function SfStatusTag({ status, label, semantic, bordered: _bordered }: SfStatusTagProps) {
  const meta = resolveStatus(status)
  const text = meta?.label ?? label ?? status ?? '-'
  const color = SEMANTIC_COLOR[meta?.semantic ?? semantic ?? 'neutral']
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        fontSize: 13,
        lineHeight: '20px',
        whiteSpace: 'nowrap',
      }}
    >
      <span
        aria-hidden
        style={{
          width: 6,
          height: 6,
          borderRadius: '50%',
          background: color,
          flex: '0 0 auto',
        }}
      />
      <span style={{ color }}>{text}</span>
    </span>
  )
}
