/**
 * SfGaugeChart 仪表盘（任务书 §37，仅用于利用率/完成率等比率场景）
 *
 * 数据形态经 node 实测（@antv/g2/lib/mark/gauge.js）：
 * - { target, total }：两段弧（target 色 + 剩余 muted）；
 * - { target, total, thresholds: number[] }：按累计边界分段着色，
 *   分段弧只画到 thresholds 末位——故末位必须补 max。
 * 色值经语义键取主题色板，业务页零色值字面量。
 */
import { useMemo } from 'react'
import type { GaugeConfig } from '@ant-design/plots'
import { SfChart } from './SfChart'
import type { SfChartTheme } from './sfChartTheme'
import {
  SF_CHART_COLOR_KEY_INDEX,
  SF_CHART_DEFAULT_HEIGHT,
  SF_GAUGE_DEFAULT_MAX,
  type SfChartColorKey,
  type SfChartStatusProps,
} from './types'
import { useSfChartTheme } from './useSfChartTheme'

export interface SfGaugeThreshold {
  /** 分段起始值（升序；第一段从 0 起，最后一段自动延伸到 max） */
  from: number
  /** 语义色键 */
  color: SfChartColorKey
}

export interface SfGaugeChartProps extends SfChartStatusProps {
  value: number
  /** 量程上限，默认 100 */
  max?: number
  /** 数值单位（如 %） */
  unit?: string
  /** 利用率分段色（§37），缺省单段主色 + muted 剩余 */
  thresholds?: SfGaugeThreshold[]
}

const DEFAULT_EMPTY_TEXT = '暂无利用率数据'

export function buildGaugeOptions(params: {
  value: number
  max: number
  unit: string
  thresholds?: SfGaugeThreshold[]
  palette: string[]
  theme: SfChartTheme
}): GaugeConfig {
  const { value, max, unit, thresholds, palette, theme } = params
  const clamped = Math.min(Math.max(value, 0), max)

  // 分段：按 from 升序取边界（去掉第一段的 0 起点），末位补 max
  const sorted = [...(thresholds ?? [])].sort((a, b) => a.from - b.from)
  const hasThresholds = sorted.length > 0
  const bounds = hasThresholds
    ? [...sorted.slice(1).map((t) => Math.min(t.from, max)), max]
    : undefined
  const range = hasThresholds
    ? sorted.map((t) => palette[SF_CHART_COLOR_KEY_INDEX[t.color] % palette.length])
    : [palette[0], palette[SF_CHART_COLOR_KEY_INDEX.muted]]

  const gaugeData: Record<string, unknown> = { target: clamped, total: max }
  if (bounds) gaugeData.thresholds = bounds

  return {
    data: gaugeData as GaugeConfig['data'],
    // scale.color 按分段序号取色（实测：thresholds 段 color 为段下标）
    scale: { color: { range } },
    // 中心数值文本（G2 gauge 默认 fill '#888' 在暗色下不可读，经 --sf-chart-axis 覆盖）
    style: {
      text: {
        content: (target: number) => `${Number.isFinite(target) ? target : 0}${unit}`,
        fontSize: 22,
        fontWeight: 600,
        fill: theme.axis.labelFill,
      },
    },
    legend: false,
    theme,
  }
}

export function SfGaugeChart({
  value,
  max = SF_GAUGE_DEFAULT_MAX,
  unit = '',
  thresholds,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading,
  error,
  onRetry,
  emptyText = DEFAULT_EMPTY_TEXT,
  className,
}: SfGaugeChartProps) {
  const { theme, palette } = useSfChartTheme()
  const empty = !Number.isFinite(value) || max <= 0
  const options = useMemo(
    () => buildGaugeOptions({ value, max, unit, thresholds, palette, theme }),
    [value, max, unit, thresholds, palette, theme],
  )
  return (
    <SfChart
      type="Gauge"
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
