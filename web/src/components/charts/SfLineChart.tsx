/**
 * SfLineChart 折线图（任务书 §32，ECharts 内核）
 *
 * 细线 1.5px、无 symbol、平滑可选；多序列图例顶部；X 轴日期经 dayLabel 收敛。
 */
import { useMemo } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { SfChart } from './SfChart'
import { dayLabel, SF_CHART_LINE_WIDTH, type SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_COLOR_KEY_INDEX,
  SF_CHART_DEFAULT_HEIGHT,
  type SfChartSeries,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfLineChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  xField: string
  series: SfChartSeries[]
  /** 平滑曲线，默认 false（折线语义 §32） */
  smooth?: boolean
  /** 面积填充（单序列趋势带），默认 false */
  area?: boolean
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

function seriesColor(series: SfChartSeries, index: number, palette: string[]): string {
  if (!series.color) return palette[index % palette.length]
  return palette[SF_CHART_COLOR_KEY_INDEX[series.color] % palette.length]
}

export function buildLineOptions(params: {
  data: SfLineChartProps['data']
  xField: string
  series: SfChartSeries[]
  smooth: boolean
  area: boolean
  palette: string[]
  theme: SfChartTheme
}): EChartsCoreOption {
  const { data, xField, series, smooth, area, palette, theme } = params
  const categories = data.map((row) => String(row[xField] ?? ''))

  const chartSeries = series.map((s, i) => ({
    type: 'line' as const,
    name: s.name,
    data: data.map((row) => row[s.key] ?? null),
    smooth,
    showSymbol: false,
    lineStyle: { width: SF_CHART_LINE_WIDTH, color: seriesColor(s, i, palette) },
    itemStyle: { color: seriesColor(s, i, palette) },
    ...(area ? { areaStyle: { opacity: 0.12, color: seriesColor(s, i, palette) } } : {}),
  }))

  return {
    grid: { left: 8, right: 16, top: series.length > 1 ? 30 : 16, bottom: 4, containLabel: true },
    legend:
      series.length > 1
        ? { top: 0, left: 0, icon: 'rect', itemWidth: 12, itemHeight: 4, itemGap: 16, textStyle: { color: theme.legendText, fontSize: 12 } }
        : undefined,
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: categories,
      boundaryGap: false,
      axisLine: { lineStyle: { color: theme.axisLine } },
      axisTick: { show: false },
      axisLabel: { color: theme.axisText, fontSize: 12, formatter: dayLabel },
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: theme.splitLine } },
      axisLabel: { color: theme.axisText, fontSize: 12 },
    },
    series: chartSeries,
  }
}

export function SfLineChart({
  data,
  xField,
  series,
  smooth = false,
  area = false,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
}: SfLineChartProps) {
  const { theme, palette } = useSfChartTheme()
  const empty = !Array.isArray(data) || data.length === 0 || series.length === 0
  const options = useMemo(
    () => buildLineOptions({ data, xField, series, smooth, area, palette, theme }),
    [data, xField, series, smooth, area, palette, theme],
  )
  return (
    <SfChart
      type="Line"
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
