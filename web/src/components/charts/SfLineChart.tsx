/**
 * SfLineChart 折线图（任务书 §32）
 *
 * 线宽固定 2px；默认不显示常驻节点，hover 经 tooltip 交互自带节点 marker
 * （@antv/g2 interaction/tooltip.js 的 marker 默认开启，实测）；
 * 多序列宽表→长表转换，色序按主题色板（§31）。
 */
import { useMemo } from 'react'
import type { LineConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import { dayLabel, SF_CHART_LINE_WIDTH, type SfChartTheme } from './sfChartTheme'
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

export interface SfLineChartProps extends SfChartStatusProps {
  data: Array<Record<string, unknown>>
  xField: string
  /** 至少一条序列；空数组视为空数据 */
  series: SfChartSeries[]
  /** 平滑曲线，默认 false（任务书 §32） */
  smooth?: boolean
}

const DEFAULT_EMPTY_TEXT = '当前时间范围内没有可展示的数据'

/** 语义色键 → 色板值；未指定时按色板顺序循环取色（§31） */
function seriesColor(series: SfChartSeries, index: number, palette: string[]): string {
  if (!series.color) return palette[index % palette.length]
  return palette[SF_CHART_COLOR_KEY_INDEX[series.color] % palette.length]
}

export function buildLineOptions(params: {
  data: SfLineChartProps['data']
  xField: string
  series: SfChartSeries[]
  smooth: boolean
  palette: string[]
  theme: SfChartTheme
}): LineConfig {
  const { data, xField, series, smooth, palette, theme } = params

  const axis = {
    // X 轴按天粒度："2026-10-04 00:00:00" 形态的时间标签收敛为日期（JSONTime 序列化尾巴）
    x: { grid: false, title: false, labelFormatter: dayLabel },
    y: { grid: true, title: false },
  }

  if (series.length === 1) {
    const [only] = series
    return {
      data,
      xField,
      yField: only.key,
      shapeField: smooth ? 'smooth' : undefined,
      legend: false,
      axis,
      style: { lineWidth: SF_CHART_LINE_WIDTH, stroke: seriesColor(only, 0, palette) },
      theme,
    }
  }

  // 多序列：宽表 → 长表
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
    style: { lineWidth: SF_CHART_LINE_WIDTH },
    theme,
  }
}

export function SfLineChart({
  data,
  xField,
  series,
  smooth = false,
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
    () => buildLineOptions({ data, xField, series, smooth, palette, theme }),
    [data, xField, series, smooth, palette, theme],
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
