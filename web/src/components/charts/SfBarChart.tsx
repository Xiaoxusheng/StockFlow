/**
 * SfBarChart 纵向柱状图（任务书 §34，ECharts 内核）
 *
 * 柱圆角 3px、barMaxWidth 限宽；多序列并列（echarts 多 series 天然 dodge）。
 */
import { useMemo } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { SfChart, type SfChartProps } from './SfChart'
import { dayLabel, SF_CHART_BAR_RADIUS, type SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_COLOR_KEY_INDEX,
  SF_CHART_DEFAULT_HEIGHT,
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
  /** 柱最大宽度，默认 32px */
  barMaxWidth?: number
  /** 图元点击（frontend.md §33.4 图表下钻，透传内核） */
  onPointClick?: SfChartProps['onPointClick']
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
}): EChartsCoreOption {
  const { data, xField, yField, series, barMaxWidth, palette, theme } = params
  const categories = data.map((row) => String(row[xField] ?? ''))
  const activeSeries =
    !series || series.length === 0
      ? [{ key: yField, name: yField } as SfChartSeries]
      : series

  const chartSeries = activeSeries.map((s, i) => ({
    type: 'bar' as const,
    name: s.name,
    data: data.map((row) => row[s.key] ?? null),
    barMaxWidth,
    itemStyle: {
      color: seriesColor(s, i, palette),
      borderRadius: [SF_CHART_BAR_RADIUS, SF_CHART_BAR_RADIUS, 0, 0],
    },
  }))

  return {
    grid: { left: 8, right: 16, top: activeSeries.length > 1 ? 30 : 16, bottom: 4, containLabel: true },
    legend:
      activeSeries.length > 1
        ? { top: 0, left: 0, icon: 'rect', itemWidth: 12, itemHeight: 8, itemGap: 16, textStyle: { color: theme.legendText, fontSize: 12 } }
        : undefined,
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: categories,
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
  onPointClick,
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
      onPointClick={onPointClick}
    />
  )
}
