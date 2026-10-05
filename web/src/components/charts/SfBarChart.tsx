/**
 * SfBarChart 纵向柱状图（任务书 §34，plots 内核映射 Column）
 *
 * 柱圆角固定 3px、限宽 maxWidth（barMaxWidth 语义）；多序列 group（dodgeX）。
 */
import { useMemo } from 'react'
import type { ColumnConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import { SF_CHART_BAR_RADIUS, type SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_COLOR_KEY_INDEX,
  SF_CHART_DEFAULT_HEIGHT,
  SF_LONG_SERIES,
  SF_LONG_VALUE,
  SF_LONG_X,
  type SfChartSeries,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfBarChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  xField: string
  yField: string
  /** 多序列（key 为 data 字段）；缺省单序列直接用 yField */
  series?: SfChartSeries[]
  /** 柱最大宽度（barMaxWidth 语义），默认 32px */
  barMaxWidth?: number
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

function seriesColor(series: SfChartSeries, index: number, palette: string[]): string {
  if (!series.color) return palette[index % palette.length]
  return palette[SF_CHART_COLOR_KEY_INDEX[series.color] % palette.length]
}

export function buildBarOptions(params: {
  data: SfBarChartProps['data']
  xField: string
  yField: string
  series?: SfChartSeries[]
  barMaxWidth: number
  palette: string[]
  theme: SfChartTheme
}): ColumnConfig {
  const { data, xField, yField, series, barMaxWidth, palette, theme } = params

  const axis = {
    x: { grid: false, title: false },
    y: { grid: true, title: false },
  }
  const barStyle = { radius: SF_CHART_BAR_RADIUS, maxWidth: barMaxWidth }

  if (!series || series.length === 0) {
    return { data, xField, yField, legend: false, axis, style: barStyle, theme }
  }
  if (series.length === 1) {
    const [only] = series
    return {
      data,
      xField,
      yField: only.key,
      legend: false,
      axis,
      scale: { color: { range: [seriesColor(only, 0, palette)] } },
      style: barStyle,
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
    group: true,
    legend: { color: { position: 'top' } },
    axis,
    scale: { color: { range } },
    style: barStyle,
    theme,
  }
}

export function SfBarChart({
  data,
  xField,
  yField,
  series,
  barMaxWidth = 32,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
}: SfBarChartProps) {
  const { theme, palette } = useSfChartTheme()
  const empty = !Array.isArray(data) || data.length === 0
  const options = useMemo(
    () => buildBarOptions({ data, xField, yField, series, barMaxWidth, palette, theme }),
    [data, xField, yField, series, barMaxWidth, palette, theme],
  )
  return (
    <SfChart
      type="Column"
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
