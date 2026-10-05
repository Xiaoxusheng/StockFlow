/**
 * SfHBarChart 横向排行图（任务书 §35，ECharts 内核）
 *
 * 内置按值降序 + topN 截断；长类目名自动省略（§35）。
 * echarts：yAxis 类目（反转让最大值在顶）、xAxis 数值——横向排行天然直白。
 */
import { useMemo } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { SfChart } from './SfChart'
import { SF_CHART_BAR_RADIUS, type SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_COLOR_KEY_INDEX,
  SF_CHART_DEFAULT_HEIGHT,
  SF_HBAR_DEFAULT_TOP_N,
  SF_HBAR_LABEL_MAX_CHARS,
  type SfChartColorKey,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfHBarChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  categoryField: string
  valueField: string
  /** 内置降序后截断条数，默认 10（TOP 10） */
  topN?: number
  /** 单序列语义色键，缺省取主题色板主色 */
  color?: SfChartColorKey
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

function truncateLabel(value: unknown): string {
  const text = String(value ?? '')
  return text.length > SF_HBAR_LABEL_MAX_CHARS ? `${text.slice(0, SF_HBAR_LABEL_MAX_CHARS)}…` : text
}

export function buildHBarOptions(params: {
  data: SfHBarChartProps['data']
  categoryField: string
  valueField: string
  topN: number
  color: SfChartColorKey | undefined
  palette: string[]
  theme: SfChartTheme
}): EChartsCoreOption {
  const { data, categoryField, valueField, topN, color, palette, theme } = params

  const ranked = [...data]
    .sort((a, b) => Number(b?.[valueField] ?? 0) - Number(a?.[valueField] ?? 0))
    .slice(0, Math.max(1, topN))

  const resolvedColor = color
    ? palette[SF_CHART_COLOR_KEY_INDEX[color] % palette.length]
    : palette[0]

  // echarts 类目轴自下而上：反转让最大值在顶部
  const ascending = [...ranked].reverse()
  const categories = ascending.map((row) => truncateLabel(row[categoryField]))
  const values = ascending.map((row) => Number(row[valueField] ?? 0))

  return {
    grid: { left: 8, right: 24, top: 12, bottom: 4, containLabel: true },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: theme.splitLine } },
      axisLabel: { color: theme.axisText, fontSize: 12 },
    },
    yAxis: {
      type: 'category',
      data: categories,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: theme.axisText, fontSize: 12, width: 88, overflow: 'truncate' },
    },
    series: [
      {
        type: 'bar',
        data: values,
        barMaxWidth: 22,
        itemStyle: { color: resolvedColor, borderRadius: [0, SF_CHART_BAR_RADIUS, SF_CHART_BAR_RADIUS, 0] },
      },
    ],
  }
}

export function SfHBarChart({
  data,
  categoryField,
  valueField,
  topN = SF_HBAR_DEFAULT_TOP_N,
  color,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
}: SfHBarChartProps) {
  const { theme, palette } = useSfChartTheme()
  const empty = !Array.isArray(data) || data.length === 0
  const options = useMemo(
    () => buildHBarOptions({ data, categoryField, valueField, topN, color, palette, theme }),
    [data, categoryField, valueField, topN, color, palette, theme],
  )
  return (
    <SfChart
      type="Bar"
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
