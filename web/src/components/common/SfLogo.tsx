/**
 * StockFlow 品牌 logo（前端唯一定义点：原 LoginPage/PcLayout/ModulePlaceholder 三份 SVG
 * 拷贝收敛于此，frontend.md §26.1 禁止重复组件）。色值一律走 Token：
 * - primary 变体：底 --sf-primary、描边 --sf-on-primary（主色底对比前景，AGENTS.md 规则 3）；
 * - muted 变体：底 --sf-border、描边 --sf-text-muted（占位页弱化态，ModulePlaceholder 用）。
 */
export interface SfLogoProps {
  /** 渲染边长 px（viewBox 恒 32×32，圆角/描边比例不变） */
  size?: number
  variant?: 'primary' | 'muted'
}

export function SfLogo({ size = 24, variant = 'primary' }: SfLogoProps) {
  const box = variant === 'muted' ? 'var(--sf-border)' : 'var(--sf-primary)'
  const stroke = variant === 'muted' ? 'var(--sf-text-muted)' : 'var(--sf-on-primary)'
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
      <rect width="32" height="32" rx="7" fill={box} />
      <path
        d="M9 11h10a3 3 0 0 1 0 6H11a3 3 0 0 0 0 6h12"
        stroke={stroke}
        strokeWidth="2.6"
        fill="none"
        strokeLinecap="round"
      />
    </svg>
  )
}
