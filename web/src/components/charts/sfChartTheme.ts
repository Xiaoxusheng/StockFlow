/**
 * 图表主题与公共常量（ECharts 内核版，2026-10-05 全站图表内核由 @ant-design/plots 换为 echarts）
 *
 * 色值一律读 tokens.css 的 --sf-chart-*（唯一真相），Light/Dark 自动跟随；
 * 本文件不定义任何色值字面量。
 */

/** 默认高度（任务书 §45 紧凑卡片适配） */
export const SF_CHART_DEFAULT_HEIGHT = 260

/** 面积透明度（任务书 §33：0.08~0.15 区间，禁高饱和渐变） */
export const SF_CHART_AREA_OPACITY = 0.12

/** 柱圆角（任务书 §34：3–4px） */
export const SF_CHART_BAR_RADIUS = 3

/** 折线宽度 1.5px（现代细线：低线宽 + 低透明面积，视觉轻；2px 在高密度趋势图上过重） */
export const SF_CHART_LINE_WIDTH = 1.5

/** 轴标签按天收敛："2026-10-04 00:00:00"/ISO 形态截取日期段，其余原样（趋势图 X 轴日粒度统一口径） */
export function dayLabel(value: unknown): string {
  const text = String(value ?? '')
  return /^\d{4}-\d{2}-\d{2}/.test(text) ? text.slice(0, 10) : text
}

function readChartVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

/** 主题色板顺序（任务书 §31：primary→secondary→success→warning→danger→muted） */
const SF_CHART_PALETTE_VARS = [
  '--sf-chart-primary',
  '--sf-chart-secondary',
  '--sf-chart-success',
  '--sf-chart-warning',
  '--sf-chart-danger',
  '--sf-chart-muted',
] as const

/** ECharts 公共视觉片段（各 build 函数拼 option 时展开；mode 变化即重算，Light/Dark 跟随） */
export interface SfEChartsBase {
  /** 系列色板（§31 同页最多 4–5 主色，超量循环复用 §64） */
  palette: string[]
  /** 轴/图例文字色 */
  axisText: string
  /** 轴线色 */
  axisLine: string
  /** 网格线色 */
  splitLine: string
  /** 图例文字色 */
  legendText: string
}

export function buildEChartsBase(_mode?: string): SfEChartsBase {
  void _mode
  return {
    palette: SF_CHART_PALETTE_VARS.map((v) => readChartVar(v)),
    axisText: readChartVar('--sf-chart-axis'),
    axisLine: readChartVar('--sf-chart-axis'),
    splitLine: readChartVar('--sf-chart-grid'),
    legendText: readChartVar('--sf-chart-legend-text'),
  }
}

/** 兼容旧引用名（useSfChartTheme 返回 base） */
export type SfChartTheme = SfEChartsBase
