/**
 * SfSparkline 迷你趋势线（任务书 §38）
 *
 * 2026-10-06 由 ECharts 内核改为**纯 SVG** 实现。原因：本组件唯一消费方是 Dashboard
 * KPI 卡（views/dashboard/DashboardKpiCards.tsx），而 Dashboard 是登录后的首屏——
 * 原实现经 components/charts 桶间接依赖 echarts，使 548KB 的 echarts chunk 被拽进首屏
 * （实测 dist/assets/SfChart-*.js）。32px 高的无轴迷你趋势线用 SVG 完全够用，省下整个
 * echarts 首屏加载；视觉对齐原 grid {top:2,bottom:2} 与 1.5px 线宽。
 *
 * 无轴/无网格/无图例/无 tooltip；仅限 KPI 卡内嵌使用。
 * 空数据返回空占位（高度保持，不画 0 也不放 SfEmpty——32px 高度容不下空态插图）。
 *
 * 三态说明：loading/error/onRetry 保留在 Props 中仅为维持与 SfChartStatusProps 的
 * 契约兼容，本组件不渲染三态——KPI 卡的失败/加载由卡片自身处理（见 DashboardKpiCards
 * 的 item.error / item.loading 分支），趋势线在无数据时统一走空占位。
 */
import { useMemo } from 'react'
import {
  SF_CHART_COLOR_KEY_INDEX,
  type SfChartColorKey,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfSparklineProps extends Omit<SfChartStatusProps, 'height'> {
  data: number[]
  /** 语义色键，缺省取主题色板主色 */
  color?: SfChartColorKey
  /** 迷你图高度，默认 32（KPI 卡内嵌） */
  height?: number
}

const SPARKLINE_DEFAULT_HEIGHT = 32
const SPARKLINE_LINE_WIDTH = 1.5
/** 上下留白，对齐原 ECharts grid {top:2, bottom:2} */
const SPARKLINE_PAD_Y = 2
/**
 * 虚拟坐标系宽度：SVG 用 preserveAspectRatio="none" 横向拉伸填满容器（KPI 卡宽度不定），
 * 配合 vector-effect="non-scaling-stroke" 保证线宽不被横向拉伸变粗。
 */
const SPARKLINE_VIEW_WIDTH = 100

/** 归一化到虚拟坐标系（y 轴反转：SVG 原点在左上）；等值序列取中线，不画 0 */
function toPoints(values: number[], height: number): Array<[number, number]> {
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min
  const usable = height - SPARKLINE_PAD_Y * 2
  const step = SPARKLINE_VIEW_WIDTH / (values.length - 1)
  return values.map((v, i) => {
    const ratio = span === 0 ? 0.5 : (v - min) / span
    return [i * step, SPARKLINE_PAD_Y + (1 - ratio) * usable]
  })
}

/**
 * 中点法平滑折线（每个数据点作二次贝塞尔控制点、相邻点中点为锚点，末段直线收尾）——
 * 视觉对齐原 ECharts series.smooth，无外部依赖。
 * n=2 退化为直线，不会产生除零。
 */
function buildSmoothPath(points: Array<[number, number]>): string {
  const n = points.length
  let d = `M ${points[0][0]} ${points[0][1]}`
  for (let i = 1; i < n - 1; i += 1) {
    const [cx, cy] = points[i]
    const [nx, ny] = points[i + 1]
    d += ` Q ${cx} ${cy} ${(cx + nx) / 2} ${(cy + ny) / 2}`
  }
  const [ex, ey] = points[n - 1]
  return `${d} L ${ex} ${ey}`
}

export function SfSparkline({
  data,
  color,
  height = SPARKLINE_DEFAULT_HEIGHT,
  className,
}: SfSparklineProps) {
  const { palette } = useSfChartTheme()
  const values = useMemo(
    () => (Array.isArray(data) ? data.filter((v) => Number.isFinite(v)) : []),
    [data],
  )

  const resolvedColor = useMemo(
    () =>
      color
        ? palette[SF_CHART_COLOR_KEY_INDEX[color] % palette.length]
        : palette[0],
    [color, palette],
  )

  // < 2 点无法成线：保持高度占位（KPI 卡内嵌不放空态插图，也不画 0 线）
  const path = useMemo(
    () => (values.length < 2 ? '' : buildSmoothPath(toPoints(values, height))),
    [values, height],
  )

  if (!path) {
    return <div className={className} style={{ height }} aria-hidden />
  }

  return (
    <svg
      className={className}
      width="100%"
      height={height}
      viewBox={`0 0 ${SPARKLINE_VIEW_WIDTH} ${height}`}
      preserveAspectRatio="none"
      style={{ display: 'block' }}
      aria-hidden
      focusable="false"
    >
      <path
        d={path}
        fill="none"
        stroke={resolvedColor}
        strokeWidth={SPARKLINE_LINE_WIDTH}
        strokeLinecap="round"
        strokeLinejoin="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}
