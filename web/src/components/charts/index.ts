/**
 * StockFlow 图表基座桶导出（与 components/common|table|print|device 平级）
 *
 * 内核（SfChart / useSfChartTheme / sfChartTheme）不对业务页导出使用方式——
 * 业务页只消费以下业务组件与类型（componentPlan 红线）。
 *
 * ⚠️ 首屏页面（Dashboard 等）如需 SfSparkline，请**直连 `./SfSparkline`** 而非本桶：
 * 桶内 SfLineChart 等在模块顶层执行 echarts.use()，属静态不可判定的副作用，
 * 从桶导入会把 echarts chunk 一并拉进首屏（2026-10-06 拆包时实测确认）。
 */
import '@/styles/chart.css'

export { SfLineChart, type SfLineChartProps } from './SfLineChart'
export { SfAreaChart, type SfAreaChartProps } from './SfAreaChart'
export { SfBarChart, type SfBarChartProps } from './SfBarChart'
export { SfHBarChart, type SfHBarChartProps } from './SfHBarChart'
export { SfDonutChart, type SfDonutChartProps, type SfDonutDatum } from './SfDonutChart'
export { SfSparkline, type SfSparklineProps } from './SfSparkline'
export { SfChartCard, type SfChartCardProps } from './SfChartCard'
export {
  SF_CHART_COLOR_KEY_INDEX,
  type SfChartColorKey,
  type SfChartSeries,
  type SfChartStatusProps,
} from './types'
