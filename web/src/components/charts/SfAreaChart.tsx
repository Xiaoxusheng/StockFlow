/**
 * SfAreaChart 面积图（任务书 §33，ECharts 内核；SfLineChart 的 area 默认开启形态）
 *
 * 面积透明度固定 0.12（0.08~0.15 区间内），同色纯色低透明填充——禁高饱和渐变（§33/§64）。
 */
import { useMemo } from 'react'
import { SfChart, type SfChartProps } from './SfChart'
import { SF_CHART_AREA_OPACITY, type SfChartTheme } from './sfChartTheme'
import { SF_CHART_DEFAULT_HEIGHT, type SfChartSeries, type SfChartStatusProps } from './types'
import { useSfChartTheme } from './useSfChartTheme'
import { buildLineOptions, type SfLineChartProps } from './SfLineChart'

export interface SfAreaChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  xField: string
  series: SfChartSeries[]
  /** 平滑曲线，默认 true（面积图常用于趋势带，§33） */
  smooth?: boolean
  /** 图元点击（frontend.md §33.4 图表下钻，透传内核） */
  onPointClick?: SfChartProps['onPointClick']
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

export function buildAreaOptions(params: {
  data: SfLineChartProps['data']
  xField: string
  series: SfChartSeries[]
  smooth: boolean
  palette: string[]
  theme: SfChartTheme
}): Record<string, unknown> {
  const base = buildLineOptions({ ...params, area: true }) as Record<string, unknown>
  const series = base.series as Array<{ areaStyle?: Record<string, unknown> }>
  for (const s of series) {
    s.areaStyle = { opacity: SF_CHART_AREA_OPACITY }
  }
  return base
}

export function SfAreaChart({
  data,
  xField,
  series,
  smooth = true,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
  onPointClick,
}: SfAreaChartProps) {
  const { theme, palette } = useSfChartTheme()
  const empty = !Array.isArray(data) || data.length === 0 || series.length === 0
  const options = useMemo(
    () => buildAreaOptions({ data, xField, series, smooth, palette, theme }),
    [data, xField, series, smooth, palette, theme],
  )
  return (
    <SfChart
      type="Area"
      options={options as never}
      height={height}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty={empty}
      emptyText={emptyText}
      className={className}
      onPointClick={onPointClick}
    />
  )
}
