/**
 * SfDonutChart 环形图（任务书 §36，plots Pie + innerRadius 固定 donut 形，禁 3D）
 *
 * maxSlices=6，超出按值降序折叠为「其他」；
 * 不做去重/求和兜底——name/value 口径互斥由调用方保证（防双计是数据侧责任）。
 */
import { useMemo } from 'react'
import type { PieConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import type { SfChartTheme } from './sfChartTheme'
import { SF_CHART_DEFAULT_HEIGHT, SF_DONUT_MAX_SLICES, type SfChartStatusProps } from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfDonutDatum {
  name: string
  value: number
}

export interface SfDonutChartProps extends SfChartStatusProps {
  data: SfDonutDatum[]
  /** 最大分片数，默认 6；超出折叠为「其他」（§36） */
  maxSlices?: number
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'
const OTHER_SLICE_NAME = '其他'

/** 内半径比例固定 donut 形；0.66（0.6 厚环显重，0.72 会破坏该 plots 版本布局计算，实测上限） */
const DONUT_INNER_RADIUS = 0.66

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
}): PieConfig {
  const { data, maxSlices, palette, theme } = params
  const chartData = foldDonutData(data, maxSlices)

  return {
    data: chartData,
    angleField: 'value',
    colorField: 'name',
    innerRadius: DONUT_INNER_RADIUS,
    label: false,
    legend: { color: { position: 'bottom' } },
    scale: {
      color: { range: chartData.map((_, i) => palette[i % palette.length]) },
    },
    theme,
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
    />
  )
}
