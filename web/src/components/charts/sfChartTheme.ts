/**
 * SfChart 主题纯函数（themeContract 唯一取值落点）
 *
 * - 色值唯一真相是 web/src/styles/tokens.css 的 --sf-chart-* 变量；
 *   本文件经 getComputedStyle 读取，不出现任何色值字面量。
 * - Dark 值由 html[data-theme='dark'] 选择器在读取时已生效（stores/theme.ts
 *   的 setMode 先 applyToDocument 再 set({mode})，订阅者读到 mode 新值时
 *   DOM 属性必已就位，getComputedStyle 拿到的就是目标主题值）。
 * - 输出为 @ant-design/plots 2.6.8（G2 v5）部分主题对象，键名经
 *   node_modules/@antv/g2/lib/theme/create.js + light.js 实测核实：
 *   category10/category20（色板）、enter/update/exit（动画时长）、
 *   line/point/area/interval（mark 造型）、axis、legendCategory、tooltip.css。
 */
import type { ThemeMode } from '@/stores/theme'

/** G2 主题键的本地结构化类型（Spec 级 Theme 类型未含 tooltip 键但运行时支持，
 *  故内核注入处做一次显式 cast，见 SfChart.tsx） */
export interface SfChartTheme {
  color: string
  category10: string[]
  category20: string[]
  enter: { duration: number; fill: string }
  update: { duration: number; fill: string }
  exit: { duration: number; fill: string }
  line: { line: { lineWidth: number; lineCap: string } }
  point: { point: { r: number; lineWidth: number } }
  area: { area: { fillOpacity: number; lineWidth: number } }
  interval: { rect: { fillOpacity: number; radius: number } }
  axis: {
    line: boolean
    lineStroke: string
    lineStrokeOpacity: number
    gridStroke: string
    gridLineWidth: number
    gridLineDash: number[] | null
    labelFill: string
    labelOpacity: number
    tickLength: number
    tickStroke: string
    titleFill: string
    titleOpacity: number
  }
  legendCategory: {
    itemLabelFill: string
    itemLabelFontSize: number
    itemMarkerSize: number
  }
  tooltip: {
    css: Record<string, Record<string, string | number>>
  }
}

/** 动画造型常量（任务书 §32/§47：300–500ms，update 300ms） */
export const SF_CHART_ENTER_MS = 420
export const SF_CHART_UPDATE_MS = 300
export const SF_CHART_EXIT_MS = 160
/**
 * 缓动实测结论：G2 v5 底层 @antv/g 的 getEasingFunction 会把 camelCase 经
 * convertToDash 转换后在 EasingFunctions 注册表查键，'cubicOut' 会转成
 * 'cubic-out' 而注册表键是 'out-cubic'（查不到则静默回退 linear）；
 * 故本库 cubicOut 语义的正确写法是 'outCubic'（@antv/g/dist/index.esm.js 实测）。
 */
export const SF_CHART_EASING = 'outCubic'

/** 面积透明度固定区间中值（任务书 §33：0.08~0.15，禁高饱和渐变） */
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

/**
 * 构建 plots 主题对象（两套主题同构）。useMemo([mode]) 消费，mode 变化即重算。
 */
export function buildChartTheme(mode: ThemeMode): SfChartTheme {
  void mode
  const palette = SF_CHART_PALETTE_VARS.map((v) => readChartVar(v))
  const axis = readChartVar('--sf-chart-axis')
  const grid = readChartVar('--sf-chart-grid')
  const legendText = readChartVar('--sf-chart-legend-text')

  return {
    // 系列色板：同页最多 4–5 主色（§31）；超出色板数量时循环复用而不是彩虹色（§64）
    color: palette[0],
    category10: [...palette, ...palette],
    category20: [...palette, ...palette, ...palette, ...palette],
    // 动画默认时长（§47）；easing 在 mark 级 animate 注入（G2Theme 类型不含 easing）
    enter: { duration: SF_CHART_ENTER_MS, fill: 'both' },
    update: { duration: SF_CHART_UPDATE_MS, fill: 'both' },
    exit: { duration: SF_CHART_EXIT_MS, fill: 'both' },
    line: { line: { lineWidth: SF_CHART_LINE_WIDTH, lineCap: 'round' } },
    point: { point: { r: 3, lineWidth: 0 } },
    area: { area: { fillOpacity: SF_CHART_AREA_OPACITY, lineWidth: 0 } },
    interval: { rect: { fillOpacity: 0.95, radius: SF_CHART_BAR_RADIUS } },
    axis: {
      line: true,
      lineStroke: axis,
      lineStrokeOpacity: 0.35,
      gridStroke: grid,
      gridLineWidth: 1,
      gridLineDash: null,
      labelFill: axis,
      labelOpacity: 1,
      tickLength: 0,
      tickStroke: axis,
      titleFill: axis,
      titleOpacity: 0.9,
    },
    legendCategory: {
      itemLabelFill: legendText,
      itemLabelFontSize: 12,
      itemMarkerSize: 8,
    },
    tooltip: {
      // tooltip 是 DOM 浮层：色值直接引用 var()，主题切换永远同步（§46/§50）
      css: {
        '.g2-tooltip': {
          'border-radius': '8px',
          padding: '8px 12px',
          'background-color': 'var(--sf-chart-tooltip-bg)',
          border: '1px solid var(--sf-chart-tooltip-border)',
          color: 'var(--sf-chart-tooltip-text)',
          'font-family': 'var(--sf-font-family)',
          'font-size': '12px',
          'line-height': '20px',
          'box-shadow': 'var(--sf-shadow-md)',
          'min-width': '96px',
        },
        '.g2-tooltip-title': {
          color: 'var(--sf-chart-tooltip-text)',
          'font-weight': 600,
          'margin-bottom': '4px',
        },
        '.g2-tooltip-list-item': {
          color: 'var(--sf-chart-tooltip-text)',
        },
        '.g2-tooltip-list-item-name': {
          color: 'var(--sf-chart-tooltip-text)',
        },
        '.g2-tooltip-list-item-value': {
          color: 'var(--sf-chart-tooltip-text)',
          'font-weight': 600,
        },
      },
    },
  }
}
