/**
 * SfHBarChart 横向排行图（任务书 §35，plots Bar = interval + coordinate transpose，
 * 已实测 node_modules/@ant-design/plots/es/core/plots/bar/index.js）
 *
 * 内置按值降序 + topN 截断；长类目名自动省略（§35）。
 */
import { useMemo } from 'react'
import type { BarConfig } from '@ant-design/plots'
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
}): BarConfig {
  const { data, categoryField, valueField, topN, color, palette, theme } = params

  const ranked = [...data]
    .sort((a, b) => Number(b?.[valueField] ?? 0) - Number(a?.[valueField] ?? 0))
    .slice(0, Math.max(1, topN))

  const resolvedColor = color
    ? palette[SF_CHART_COLOR_KEY_INDEX[color] % palette.length]
    : palette[0]

  return {
    data: ranked,
    // G2 标准：transpose 坐标下 xField=类目（渲染于左侧）、yField=数值（渲染于底部）。
    // 此前 x=value/y=category 的写法导致数值轴立左、类目轴落底（轴义反转）；2026-10-05
    // 曾据 5178 混乱现场判"换轴后条形消失"而回滚——该观察不可信，干净实例实测换轴正常。
    xField: categoryField,
    yField: valueField,
    legend: false,
    axis: {
      x: {
        grid: false,
        title: false,
        labelFormatter: (value: unknown) => truncateLabel(value),
      },
      y: { grid: true, title: false },
    },
    scale: { color: { range: [resolvedColor] } },
    style: { radius: SF_CHART_BAR_RADIUS, maxWidth: 28 },
    theme,
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
