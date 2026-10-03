import type { CSSProperties, ReactNode } from 'react'

export interface PadInfoItem {
  label: string
  value: ReactNode
  /** 强调态：数量等关键值放大为 --sf-pad-font-metric（28px）大字 */
  emphasis?: boolean
}

export interface PadInfoCardProps {
  title?: string
  /** 关键信息项（商品 / SKU / 数量 / 库位 / 批次…），value 缺值由调用方传 '-' */
  items: PadInfoItem[]
  /** 值栅格列数，默认 2 */
  columns?: number
}

/**
 * 关键信息卡原语：label + value 大字栅格（frontend.md §20.2 更大字体 / §20.3 商品信息栏）。
 * emphasis 项走 --sf-pad-font-metric 28px 强调数量。
 */
export function PadInfoCard({ title, items, columns = 2 }: PadInfoCardProps) {
  const gridStyle: CSSProperties = { gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }
  return (
    <section className="sf-pad-card">
      {title && <h3 className="sf-pad-card-title">{title}</h3>}
      <div className="sf-pad-info-grid" style={gridStyle}>
        {items.map((item) => (
          <div key={item.label} className="sf-pad-info-item">
            <span className="sf-pad-info-label">{item.label}</span>
            <span className={item.emphasis ? 'sf-pad-metric' : 'sf-pad-info-value'}>{item.value}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
