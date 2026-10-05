/**
 * SfSparkline 迷你趋势线（任务书 §38）
 *
 * 无轴/无网格/无图例/无 tooltip；仅限 KPI 卡内嵌使用。
 * 空数据返回空占位（高度保持，不画 0 也不放 SfEmpty——32px 高度容不下空态插图）。
 */
import { useMemo } from 'react'
import type { LineConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import type { SfChartTheme } from './sfChartTheme'
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

export function buildSparklineOptions(params: {
  data: number[]
  color: string
  theme: SfChartTheme
}): LineConfig {
  const { data, color, theme } = params
  return {
    data: data.map((value, index) => ({ x: index, y: value })),
    xField: 'x',
    yField: 'y',
    axis: false,
    legend: false,
    tooltip: false,
    interaction: { tooltip: false },
    padding: 0,
    scale: { color: { range: [color] } },
    style: { lineWidth: SPARKLINE_LINE_WIDTH, stroke: color },
    theme,
  }
}

export function SfSparkline({
  data,
  color,
  height = SPARKLINE_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  className,
}: SfSparklineProps) {
  const { theme, palette } = useSfChartTheme()
  const values = useMemo(
    () => (Array.isArray(data) ? data.filter((v) => Number.isFinite(v)) : []),
    [data],
  )
  const empty = values.length < 2

  const resolvedColor = useMemo(
    () =>
      color
        ? palette[SF_CHART_COLOR_KEY_INDEX[color] % palette.length]
        : palette[0],
    [color, palette],
  )

  const options = useMemo(
    () => buildSparklineOptions({ data: values, color: resolvedColor, theme }),
    [values, resolvedColor, theme],
  )

  // 空数据：保持高度占位（KPI 卡内嵌不放空态插图，也不画 0 线）
  if (empty) {
    return <div className={className} style={{ height }} aria-hidden />
  }

  return (
    <SfChart
      type="Line"
      options={options}
      height={height}
      loading={loading}
      error={error}
      onRetry={onRetry}
      className={className}
    />
  )
}
