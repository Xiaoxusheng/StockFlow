/**
 * SfChart —— 唯一渲染内核（业务页不直接使用，经 SfLineChart 等业务组件消费）
 *
 * 职责（任务书 §29/§46–§53）：
 * - 容器 div + @ant-design/plots 动态 import（React.lazy，plots 库单 chunk 首图加载）；
 * - 统一注入主题对象、autoFit、动画（420ms 入场 / 300ms 更新 / outCubic，
 *   prefers-reduced-motion 时整体关闭）；
 * - 容器 ResizeObserver：侧边栏折叠 / 抽屉 / 窗口变化时 chart.forceFit()
 *   （plots 内部 autoFit 亦自管 resize，此处兜底容器消隐场景，§48）；
 * - 三态内置：loading → Skeleton 保高（§51）、error → SfError + 重试（§53）、
 *   空数据 → SfEmpty（§52，不画 0）。
 */
import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type ComponentType,
  type LazyExoticComponent,
} from 'react'
import { Skeleton } from 'antd'
import type { AreaConfig, BarConfig, Chart, ColumnConfig, GaugeConfig, LineConfig, PieConfig } from '@ant-design/plots'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SF_CHART_DEFAULT_HEIGHT } from './types'
import { useSfChartTheme } from './useSfChartTheme'
import { SF_CHART_EASING, SF_CHART_ENTER_MS, SF_CHART_EXIT_MS, SF_CHART_UPDATE_MS } from './sfChartTheme'

/** 图型名 → plots 内核映射（componentPlan，API 已对 plots 2.6.8 实测） */
export type SfChartType = 'Line' | 'Area' | 'Column' | 'Bar' | 'Pie' | 'Gauge'

interface PlotConfigMap {
  Line: LineConfig
  Area: AreaConfig
  Column: ColumnConfig
  Bar: BarConfig
  Pie: PieConfig
  Gauge: GaugeConfig
}

type AnyPlotComponent = ComponentType<Record<string, unknown>>

/** 动态 import plots 主包并按图型名取组件（模块缓存复用，全站单 chunk）。
 *  运行时 props 形状由 SfChartProps 泛型（PlotConfigMap）在业务侧保证。 */
async function loadPlotComponent(key: SfChartType): Promise<{ default: AnyPlotComponent }> {
  const plots = await import('@ant-design/plots')
  return { default: plots[key] as unknown as AnyPlotComponent }
}

const PLOT_COMPONENTS: Record<SfChartType, LazyExoticComponent<AnyPlotComponent>> = {
  Line: lazy(() => loadPlotComponent('Line')),
  Area: lazy(() => loadPlotComponent('Area')),
  Column: lazy(() => loadPlotComponent('Column')),
  Bar: lazy(() => loadPlotComponent('Bar')),
  Pie: lazy(() => loadPlotComponent('Pie')),
  Gauge: lazy(() => loadPlotComponent('Gauge')),
}

export interface SfChartProps<T extends SfChartType = SfChartType> {
  /** plots 图型（内核映射：Line/Area/Column/Bar/Pie/Gauge） */
  type: T
  /** 该图型的 plots options（业务组件已含数据与编码；theme/animate/autoFit 由内核注入） */
  options: PlotConfigMap[T]
  /** 容器高度（px），默认 260 */
  height?: number
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  empty?: boolean
  emptyText?: string
  className?: string
}

function ChartSkeleton({ height }: { height: number }) {
  return (
    <div className="sf-chart__state sf-chart__state--skeleton" style={{ height }} aria-busy>
      <Skeleton active title={false} paragraph={{ rows: 3 }} style={{ width: '100%' }} />
    </div>
  )
}

export function SfChart<T extends SfChartType>({
  type,
  options,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading = false,
  error,
  onRetry,
  empty = false,
  emptyText,
  className,
}: SfChartProps<T>) {
  const { theme, reducedMotion } = useSfChartTheme()
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<Chart | null>(null)

  const handleReady = useCallback((chart: Chart) => {
    chartRef.current = chart
  }, [])

  // 容器尺寸变化（侧边栏折叠/抽屉/窗口/消隐重现）→ forceFit 兜底（§48，rAF 节流）
  useEffect(() => {
    const el = wrapRef.current
    if (!el || typeof ResizeObserver === 'undefined') return undefined
    let raf = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        const chart = chartRef.current
        if (chart && typeof chart.forceFit === 'function') chart.forceFit()
      })
    })
    observer.observe(el)
    return () => {
      cancelAnimationFrame(raf)
      observer.disconnect()
      chartRef.current = null
    }
  }, [])

  const finalOptions = useMemo<Record<string, unknown>>(() => {
    // Spec 级 Theme 类型未含 tooltip 键，但 G2 运行时支持（@antv/g2/lib/theme/create.js 实测），
    // 此处显式放宽
    const themeValue = theme as unknown as PlotConfigMap[T]['theme']
    const animate = reducedMotion
      ? false
      : {
          enter: { duration: SF_CHART_ENTER_MS, easing: SF_CHART_EASING },
          update: { duration: SF_CHART_UPDATE_MS, easing: SF_CHART_EASING },
          exit: { duration: SF_CHART_EXIT_MS, easing: SF_CHART_EASING },
        }
    return { ...options, autoFit: true, theme: themeValue, animate, onReady: handleReady }
  }, [options, theme, reducedMotion, handleReady])

  if (loading) {
    return (
      <div className={className}>
        <ChartSkeleton height={height} />
      </div>
    )
  }
  if (error !== undefined && error !== null && error !== false) {
    return (
      <div className={className}>
        <div className="sf-chart__state" style={{ height }}>
          <SfError error={error} onRetry={onRetry} />
        </div>
      </div>
    )
  }
  if (empty) {
    return (
      <div className={className}>
        <div className="sf-chart__state" style={{ height }}>
          <SfEmpty description={emptyText} />
        </div>
      </div>
    )
  }

  const Plot = PLOT_COMPONENTS[type] as LazyExoticComponent<AnyPlotComponent>
  return (
    <div ref={wrapRef} className={[className, 'sf-chart'].filter(Boolean).join(' ')} style={{ height }}>
      <Suspense fallback={<ChartSkeleton height={height} />}>
        <Plot {...finalOptions} />
      </Suspense>
    </div>
  )
}
