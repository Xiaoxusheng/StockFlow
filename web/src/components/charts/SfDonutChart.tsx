/**
 * SfDonutChart 环形图（任务书 §36，ECharts Pie；细环 0.52~0.72，禁 3D）
 *
 * maxSlices=6，超出按值降序折叠为「其他」；
 * 不做去重/求和兜底——name/value 口径互斥由调用方保证（防双计是数据侧责任）。
 */
import { useMemo } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { SfChart, type SfChartProps } from './SfChart'
import { type SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_DEFAULT_HEIGHT,
  SF_DONUT_MAX_SLICES,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfDonutDatum {
  name: string
  value: number
}

export interface SfDonutChartProps extends SfChartStatusProps {
  data: SfDonutDatum[]
  /** 最大分片数，默认 6；超出折叠为「其他」（§36） */
  maxSlices?: number
  /** 图元点击（frontend.md §33.4 图表下钻，透传内核） */
  onPointClick?: SfChartProps['onPointClick']
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'
const OTHER_SLICE_NAME = '其他'

export function foldDonutData(data: SfDonutDatum[], maxSlices: number): SfDonutDatum[] {
  const sorted = [...data].sort((a, b) => b.value - a.value)
  if (sorted.length <= maxSlices) return sorted
  const kept = sorted.slice(0, maxSlices - 1)
  const restSum = sorted.slice(maxSlices - 1).reduce((sum, d) => sum + d.value, 0)
  return [...kept, { name: OTHER_SLICE_NAME, value: restSum }]
}

export function buildDonutOptions(params: {
  data: SfDonutDatum[]
  maxSlices: number
  palette: string[]
  theme: SfChartTheme
}): EChartsCoreOption {
  const { data, maxSlices, palette, theme } = params
  const chartData = foldDonutData(data, maxSlices)

  return {
    tooltip: { trigger: 'item' },
    legend: { bottom: 0, icon: 'rect', itemWidth: 12, itemHeight: 8, textStyle: { color: theme.legendText, fontSize: 12 } },
    series: [
      {
        type: 'pie',
        radius: ['52%', '72%'],
        center: ['50%', '44%'],
        avoidLabelOverlap: true,
        label: { show: false },
        itemStyle: { borderColor: 'transparent', borderWidth: 2 },
        data: chartData.map((d, i) => ({
          name: d.name,
          value: d.value,
          itemStyle: { color: palette[i % palette.length] },
        })),
      },
    ],
  }
}

export function SfDonutChart({
  data,
  maxSlices = SF_DONUT_MAX_SLICES,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
  onPointClick,
}: SfDonutChartProps) {
  const { theme, palette } = useSfChartTheme()
  // 全 0 或空数据不画 0（§52）；plots Pie 对全 0 会兜底画 1，此处先行拦截
  const total = Array.isArray(data) ? data.reduce((sum, d) => sum + (d?.value ?? 0), 0) : 0
  const empty = !Array.isArray(data) || data.length === 0 || total <= 0
  const options = useMemo(
    () => buildDonutOptions({ data, maxSlices, palette, theme }),
    [data, maxSlices, palette, theme],
  )
  return (
    <SfChart
      type="Pie"
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
