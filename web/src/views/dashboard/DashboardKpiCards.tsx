/**
 * Dashboard 顶部 KPI 卡（任务书 §17 / §27 第一层）
 *
 * 形态：标签 13px + 数字 26px（--sf-font-size-kpi，tabular-nums）+ 底部固定高度信息区
 * （SfSparkline 真实迷你趋势 或 真实次要指标）。颜色只用于趋势与状态（danger），
 * 不伪造「较昨日 ↑x%」——后端未下发同比/环比 delta（todayMetrics 无此类字段），
 * 按 §54 真实数据原则不做前端拼装。
 *
 * 三态：加载中数字占位「-」；块级错误块内展示「加载失败 + 重试」（§53，不拖垮整排）；
 * Sparkline 数据 < 2 点由组件自身空占位（§52 不画 0）。
 */
import { Button, Col, Row } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import type { ReactNode } from 'react'
import { SfSparkline } from '@/components/charts'
import { formatNumber } from '@/utils/format'
import './dashboard.css'

export interface DashboardKpiItem {
  key: string
  label: string
  /** undefined → 展示「-」（加载中 / 字段未下发）；数字一律真实接口值 */
  value?: number
  /** 数字格式化（默认 formatNumber 千分位；库存量等小数用 formatQty 由调用方传入） */
  format?: (value: number) => string
  /** 底部真实迷你趋势（如近 7 日量），缺省不渲染 */
  spark?: number[]
  /** 底部真实次要指标说明（spark 存在时让位于 spark） */
  sub?: ReactNode
  /** 状态色（§17：颜色只用于状态）——库存预警等风险指标 */
  danger?: boolean
  /** 真实路由（config/menu.tsx 注册路径），缺省不可点击 */
  link?: string
  loading?: boolean
  error?: unknown
  onRetry?: () => void
}

function KpiCard({ item }: { item: DashboardKpiItem }) {
  const navigate = useNavigate()
  const link = item.link
  const clickable = Boolean(link)
  const valueText =
    item.loading || item.value === undefined || item.value === null
      ? '-'
      : (item.format ?? formatNumber)(item.value)

  return (
    <div
      className={['sf-kpi-card', clickable ? 'sf-kpi-card--link' : ''].filter(Boolean).join(' ')}
      onClick={link ? () => navigate(link) : undefined}
      role={clickable ? 'button' : undefined}
    >
      <div className="sf-kpi-card__label">{item.label}</div>
      <div className={['sf-kpi-card__value', item.danger ? 'sf-kpi-card__value--danger' : '']
        .filter(Boolean)
        .join(' ')}
      >
        {valueText}
      </div>
      {item.error ? (
        <div className="sf-kpi-card__error">
          <span>加载失败</span>
          {item.onRetry && (
            <Button type="link" size="small" icon={<ReloadOutlined />} onClick={item.onRetry}>
              重试
            </Button>
          )}
        </div>
      ) : item.spark && item.spark.length > 0 ? (
        <div className="sf-kpi-card__footer sf-kpi-card__footer--spark">
          <SfSparkline data={item.spark} className="sf-kpi-card__spark" />
        </div>
      ) : item.sub !== undefined ? (
        <div className="sf-kpi-card__footer">
          <span className="sf-kpi-card__sub">{item.sub}</span>
        </div>
      ) : (
        /* 无趋势 / 无次要信息：保留等高占位，保证同排 KPI 卡对齐 */
        <div className="sf-kpi-card__footer" aria-hidden />
      )}
    </div>
  )
}

/** KPI 卡排（§27 顶部 4 KPI；响应式 xs=1 sm=2 lg=4 列，任务书 §60） */
export function DashboardKpiCards({ items }: { items: DashboardKpiItem[] }) {
  return (
    <Row gutter={[16, 16]}>
      {items.map((item) => (
        <Col key={item.key} xs={24} sm={12} lg={6}>
          <KpiCard item={item} />
        </Col>
      ))}
    </Row>
  )
}
