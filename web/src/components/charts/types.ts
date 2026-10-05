/**
 * 图表基座公共类型（components/charts 内部 + 业务组件 props 复用）
 */

/** 图表状态 props：三态在图表组件内处理，脱离 SfChartCard 也可独立使用 */
export interface SfChartStatusProps {
  /** 图表高度（px），默认 260 */
  height?: number
  /** 加载态：Skeleton 保高（任务书 §51） */
  loading?: boolean
  /** 错误态：SfError + 重试（任务书 §53） */
  error?: unknown
  /** 错误重试回调 */
  onRetry?: () => void
  /** 空态描述（任务书 §52：不画 0） */
  emptyText?: string
  className?: string
}

/**
 * 图表语义色键：业务组件只允许传语义键取主题色板，
 * 禁止业务页出现任何图表色值字面量（themeContract / componentPlan 红线）。
 */
export type SfChartColorKey = 'primary' | 'secondary' | 'success' | 'warning' | 'danger' | 'muted'

/** 语义色键 → 主题色板（--sf-chart-* 顺序）下标 */
export const SF_CHART_COLOR_KEY_INDEX: Record<SfChartColorKey, number> = {
  primary: 0,
  secondary: 1,
  success: 2,
  warning: 3,
  danger: 4,
  muted: 5,
}

/** 折线/柱状多序列描述：key 为 data 字段名，name 为图例与 tooltip 展示名 */
export interface SfChartSeries {
  key: string
  name: string
  /** 语义色键，缺省按主题色板顺序取色（任务书 §31） */
  color?: SfChartColorKey
}

/** 默认图表高度（任务书 §45 紧凑卡片适配） */
export const SF_CHART_DEFAULT_HEIGHT = 260

/** 仪表盘默认量程上限 */
export const SF_GAUGE_DEFAULT_MAX = 100

/** 环形图最大分片数，超出折叠为「其他」（任务书 §36） */
export const SF_DONUT_MAX_SLICES = 6

/** 横向排行默认 TOP N（任务书 §35） */
export const SF_HBAR_DEFAULT_TOP_N = 10

/** 长类目名截断长度（超出省略，任务书 §35） */
export const SF_HBAR_LABEL_MAX_CHARS = 8

/** 多序列宽表→长表转换的内部字段名（避免与业务字段冲突） */
export const SF_LONG_X = '__sf_x'
export const SF_LONG_SERIES = '__sf_series'
export const SF_LONG_VALUE = '__sf_value'
