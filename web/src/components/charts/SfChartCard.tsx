/**
 * SfChartCard 图表卡片壳（任务书 §45）
 *
 * 结构：标题 + 描述 + 右侧操作（Segmented 时间档 / 刷新 / More）+ 图表区；
 * 紧凑 padding 走 --sf-space-4/5。loading 时图表区换 Skeleton 保持卡高
 * （子图自身 loading 透传时此处可省）。
 */
import { Skeleton } from 'antd'
import type { ReactNode } from 'react'

export interface SfChartCardProps {
  title: ReactNode
  subtitle?: ReactNode
  /** 右侧操作区（Segmented 时间档 / 刷新 / More） */
  extra?: ReactNode
  /** 卡片级加载：图表区渲染 Skeleton 保高（§51） */
  loading?: boolean
  /** 骨架高度（与图表高度一致时无布局抖动），默认 260 */
  loadingHeight?: number
  className?: string
  children: ReactNode
}

export function SfChartCard({
  title,
  subtitle,
  extra,
  loading = false,
  loadingHeight = 260,
  className,
  children,
}: SfChartCardProps) {
  const hasHeader = Boolean(title || subtitle || extra)
  return (
    <section className={[className, 'sf-chart-card'].filter(Boolean).join(' ')}>
      {hasHeader ? (
        <header className="sf-chart-card__header">
          <div className="sf-chart-card__titles">
            {title ? <h3 className="sf-chart-card__title">{title}</h3> : null}
            {subtitle ? <p className="sf-chart-card__subtitle">{subtitle}</p> : null}
          </div>
          {extra ? <div className="sf-chart-card__extra">{extra}</div> : null}
        </header>
      ) : null}
      <div className="sf-chart-card__body">
        {loading ? (
          <div className="sf-chart__state sf-chart__state--skeleton" style={{ height: loadingHeight }} aria-busy>
            <Skeleton active title={false} paragraph={{ rows: 3 }} style={{ width: '100%' }} />
          </div>
        ) : (
          children
        )}
      </div>
    </section>
  )
}
