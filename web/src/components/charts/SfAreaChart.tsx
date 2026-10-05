/**
 * SfAreaChart 面积图（任务书 §33）
 *
 * 与折线同构（plots Area 内核），面积透明度固定 0.12（0.08~0.15 区间内），
 * 同色纯色低透明填充——禁高饱和渐变（§33/§64）。
 */
import { useMemo } from 'react'
import type { AreaConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import { SF_CHART_AREA_OPACITY, SF_CHART_LINE_WIDTH, dayLabel } from './sfChartTheme'
import { SF_CHART_DEFAULT_HEIGHT, SF_LONG_SERIES, SF_LONG_VALUE, SF_LONG_X, type SfChartSeries, type SfChartStatusProps, SF_CHART_COLOR_KEY_INDEX } from './types'
import { useSfChartTheme } from './useSfChartTheme'
import type { SfChartTheme } from './sfChartTheme'
import type { SfLineChartProps } from './SfLineChart'

export interface SfAreaChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  xField: string
  series: SfChartSeries[]
  /** 平滑曲线，默认 true（面积图常用于趋势带，§33） */
  smooth?: boolean
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

function seriesColor(series: SfChartSeries, index: number, palette: string[]): string {
  if (!series.color) return palette[index % palette.length]
  return palette[SF_CHART_COLOR_KEY_INDEX[series.color] % palette.length]
}

export function buildAreaOptions(params: {
  data: SfLineChartProps['data']
  xField: string
  series: SfChartSeries[]
  smooth: boolean
  palette: string[]
  theme: SfChartTheme
}): AreaConfig {
  const { data, xField, series, smooth, palette, theme } = params

  const axis = {
    // X 轴按天粒度：时间标签收敛为日期（同 SfLineChart，JSONTime 尾巴不进图）
    x: { grid: false, title: false, labelFormatter: dayLabel },
    y: { grid: true, title: false },
  }
  // 面积透明度固定低值，同色渐隐不使用渐变（§33）
  const areaStyle = { fillOpacity: SF_CHART_AREA_OPACITY }

  if (series.length === 1) {
    const [only] = series
    return {
      data,
      xField,
      yField: only.key,
      shapeField: smooth ? 'smooth' : undefined,
      legend: false,
      axis,
      style: { lineWidth: SF_CHART_LINE_WIDTH, stroke: seriesColor(only, 0, palette), ...areaStyle },
      theme,
    }
  }

  const longData = data.flatMap((row) =>
    series.map((s) => ({
      [SF_LONG_X]: row[xField],
      [SF_LONG_SERIES]: s.name,
      [SF_LONG_VALUE]: row[s.key],
    })),
  )
  const range = series.map((s, i) => seriesColor(s, i, palette))
  return {
    data: longData,
    xField: SF_LONG_X,
    yField: SF_LONG_VALUE,
    seriesField: SF_LONG_SERIES,
    colorField: SF_LONG_SERIES,
    shapeField: smooth ? 'smooth' : undefined,
    legend: { color: { position: 'top' } },
    axis,
    scale: { color: { range } },
    style: { lineWidth: SF_CHART_LINE_WIDTH, ...areaStyle },
    theme,
  }
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
      options={options}
      height={height}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty={empty}
      emptyText={emptyText}
      className={className}
    />
  )
}
