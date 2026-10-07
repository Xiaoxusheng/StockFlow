/**
 * SfChart —— 唯一渲染内核（ECharts 版，2026-10-05 全站图表内核由 @ant-design/plots 换为 echarts；
 * 业务页不直接使用，经 SfLineChart 等业务组件消费；组件 Props 契约不变，页面零改动）
 *
 * 职责（任务书 §29/§46–§53）：
 * - 容器 div + echarts/core 按需注册（Line/Bar/Pie + Grid/Tooltip/Legend + CanvasRenderer）；
 * - setOption 整体替换（notMerge：系列数量变化安全）；reduced-motion 时 animation 关闭；
 * - 容器 ResizeObserver → chart.resize()（侧边栏折叠/抽屉/窗口变化，§48）；
 * - 三态内置：loading → Skeleton 保高（§51）、error → SfError + 重试（§53）、
 *   空数据 → SfEmpty（§52，不画 0）。
 */
import { useEffect, useMemo, useRef } from 'react'
import { Skeleton } from 'antd'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SF_CHART_DEFAULT_HEIGHT } from './types'
import { useSfChartTheme } from './useSfChartTheme'

echarts.use([LineChart, BarChart, PieChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

/** 图型语义标记（echarts 单实例通吃，type 仅保留业务可读性，不再分发内核） */
export type SfChartType = 'Line' | 'Area' | 'Column' | 'Bar' | 'Pie'

export interface SfChartPoint {
  /** 类目名（echarts params.name：柱/条类目、饼片名、折线数据点 x 值） */
  name: string
  /** 序列名（echarts params.seriesName，单序列图为序列 key） */
  seriesName: string
}

export interface SfChartProps {
  /** 图型语义标记（Line/Area/Column/Bar/Pie） */
  type: SfChartType
  /** 该图型的 ECharts option（业务组件已含数据与编码；动画/主题由内核注入） */
  options: EChartsCoreOption
  /** 容器高度（px），默认 260 */
  height?: number
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  empty?: boolean
  emptyText?: string
  className?: string
  /** 图元点击（frontend.md §33.4 图表下钻）：echarts click 事件收敛为 {name, seriesName}；
   * 内核仅注册一次，回调经 ref 取最新值（切换跳转目标无需重建实例） */
  onPointClick?: (params: SfChartPoint) => void
}

export function SfChart({
  type,
  options,
  height = SF_CHART_DEFAULT_HEIGHT,
  loading = false,
  error,
  onRetry,
  empty = false,
  emptyText,
  className,
  onPointClick,
}: SfChartProps) {
  void type
  const { reducedMotion } = useSfChartTheme()
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const chartElRef = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  // 点击回调走 ref：实例只注册一次监听，页面侧跳转目标变化无需摘挂重注册
  const onPointClickRef = useRef<SfChartProps['onPointClick']>(undefined)
  onPointClickRef.current = onPointClick

  // 实例生命周期：init 一次，dispose 清理（§48）。
  // 关键：echarts 挂在专用子 div（chartElRef）——React 从不管理它的子节点，
  // 避免与三态覆盖层的声明式子节点冲突（removeChild DOM 归属错误实录）。
  useEffect(() => {
    const el = chartElRef.current
    if (!el) return undefined
    const chart = echarts.init(el)
    chartRef.current = chart
    // init 后下一帧强制对齐一次容器尺寸：懒加载图表（React.lazy 分包晚于首帧）与
    // 网格布局未及算好时 echarts 会按 0 宽回退 100px 画布，而 ResizeObserver 只在
    // 「变化」时触发——init 后若无尺寸变化则永远停在回退宽度（2026-10-07 Dashboard
    // 实测 625px 容器挂 100px 画布）。一次 resize 对齐即恢复，观察器仍管后续变化。
    requestAnimationFrame(() => chartRef.current?.resize())
    chart.on('click', (params) => {
      const cb = onPointClickRef.current
      if (!cb) return
      cb({ name: String(params.name ?? ''), seriesName: String(params.seriesName ?? '') })
    })
    // 容器尺寸变化（侧边栏折叠/抽屉/窗口/消隐重现）→ resize 兜底（rAF 节流）
    let raf = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => chart.resize())
    })
    observer.observe(el)
    return () => {
      cancelAnimationFrame(raf)
      observer.disconnect()
      chart.dispose()
      chartRef.current = null
    }
  }, [])

  // 三态判定（在 effect 之前声明）：loading/错误/空数据时用覆盖层遮挡，不 setOption
  const showError = error !== undefined && error !== null && error !== false
  // option 更新：notMerge 整体替换（系列数量/类型变化安全）；reduced-motion 动画关闭（§59）
  const merged = useMemo<EChartsCoreOption>(
    () => ({ ...options, animation: !reducedMotion }),
    [options, reducedMotion],
  )
  useEffect(() => {
    if (loading || showError || empty) return
    chartRef.current?.setOption(merged, { notMerge: true })
  }, [merged, loading, showError, empty])

  // 外层 wrap 由 React 管理（含三态覆盖层）；chartEl 专用于 echarts，子节点归 echarts。
  return (
    <div
      ref={wrapRef}
      className={[className, 'sf-chart'].filter(Boolean).join(' ')}
      style={{ height, position: 'relative' }}
    >
      <div ref={chartElRef} style={{ width: '100%', height: '100%' }} />
      {loading ? (
        <div
          className="sf-chart__state sf-chart__state--skeleton"
          style={{ position: 'absolute', inset: 0, background: 'var(--sf-surface)' }}
          aria-busy
        >
          <Skeleton active title={false} paragraph={{ rows: 3 }} style={{ width: '100%' }} />
        </div>
      ) : showError ? (
        <div
          className="sf-chart__state"
          style={{ position: 'absolute', inset: 0, height, background: 'var(--sf-surface)' }}
        >
          <SfError error={error} onRetry={onRetry} />
        </div>
      ) : empty ? (
        <div
          className="sf-chart__state"
          style={{ position: 'absolute', inset: 0, height, background: 'var(--sf-surface)' }}
        >
          <SfEmpty description={emptyText} />
        </div>
      ) : null}
    </div>
  )
}
