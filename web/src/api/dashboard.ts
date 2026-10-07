import { http } from './client'

/**
 * Dashboard 视图（requirements.md §2.1：管理层 / 仓库人员两套指标分列）。
 * 仅用于前端按权限 fail-closed 选择指标面板（views/dashboard/dashboardView.ts），
 * 不作为请求参数下发——数据范围以会话仓库权限快照为准，后端禁止接受前端范围参数
 * （internal/reports/handler.go:19-24）。
 */
export type DashboardView = 'management' | 'operator'

/**
 * Dashboard 端点时间参数（对齐后端报表域冻结口径：requireRange，handler.go:26-53——
 * time_from/time_to，YYYY-MM-DD，缺省近 30 天，上限 366 天）。禁止 from/to 拼写。
 * 五个 /api/reports/dashboard/* 端点已于 2026-10-05 联调轮注册
 * （internal/reports/routes.go:56-60，权限 inventory:inventory:list）。
 */
export interface DashboardTimeParams {
  /** 自定义时间窗起（YYYY-MM-DD）；仅 trend 图表时间段切换时下发 */
  time_from?: string
  /** 自定义时间窗结束（YYYY-MM-DD） */
  time_to?: string
}

/**
 * 今日业务指标（requirements.md §2.1 / frontend.md §5 第一层）。
 * 字段名为前端先行契约（camelCase），/api/reports/dashboard/today 立项后按后端
 * JSON tag 回对（同 DashboardAlertItem/DashboardWarehouseStock 的 snake_case 口径）。
 */
export interface DashboardTodayMetrics {
  /** —— 两视图公共 —— */
  todayInboundCount: number
  todayOutboundCount: number
  pendingTaskCount: number
  stockAlertCount: number
  /** —— 管理层视图（requirements.md §2.1）—— */
  warehouseCount?: number
  skuCount?: number
  totalQty?: number
  /** 库存金额（口径以后端契约为准） */
  stockValue?: number
  orderCount?: number
  nearExpiryQty?: number
  slowMovingQty?: number
  pendingApprovalCount?: number
  pendingExceptionCount?: number
  /** —— 仓库人员视图（requirements.md §2.1）—— */
  pendingReceiveCount?: number
  pendingPutawayCount?: number
  pendingPickCount?: number
  pendingCheckCount?: number
  pendingPackCount?: number
  pendingShipmentCount?: number
  pendingCountCount?: number
}

/** 趋势时间档：预设档 + custom（time_from/time_to 真实传参，requirements.md §2.1「自定义时间」） */
export type TrendRange = '7d' | '30d' | '90d' | 'custom'

export interface TrendPoint {
  date: string
  inbound: number
  outbound: number
  stockQty: number
}

/** 任务与预警条目（frontend.md §5 第三层） */
export interface DashboardTaskItem {
  type: string
  label: string
  count: number
  link: string
}

/** 库存预警条目（字段按后端 snake_case 口径对齐，dashboard.go:94-102 dashboardAlertRowDTO 同源：
 * created_at 为 *database.JSONTime，种子流水无发生时间时实测为 null——formatDateTime 空
 * 值统一占位「-」） */
export interface DashboardAlertItem {
  id: number | string
  type: string
  level: string
  sku_code: string
  product_name: string
  message: string
  created_at: string | null
}

/** 仓库库存分析（frontend.md §5 第四层；字段按后端 snake_case 口径对齐）。
 * warehouse_id 供图表/列表下钻预筛（frontend.md §33.4，2026-10-07 联动批次二）。 */
export interface DashboardWarehouseStock {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_count: number
  total_qty: number
  bin_utilization: number
}

/** Dashboard 五端点（routes.go:56-60 已注册，DTO 见 internal/reports/dashboard.go:37-102——
 * JSON tag 已逐字段核实与本文件类型一致）。本文件只承载 /api/reports/dashboard/* 五端点；
 * Dashboard 页其余块消费既有封装：inventoryApi.stockSummary/analytics（GET /api/inventory/summary|
 * analytics，routes.go:64,67）、reportsApi.inboundStats/outboundStats（KPI 迷你趋势）、
 * inventoryApi.ledger（最近库存异动表）——不改其他 api 文件。 */
export const dashboardApi = {
  todayMetrics: (params?: DashboardTimeParams) =>
    http.get<DashboardTodayMetrics>('/api/reports/dashboard/today', { params }),
  trend: (range: TrendRange, params?: DashboardTimeParams) =>
    http.get<TrendPoint[]>('/api/reports/dashboard/trend', { params: { range, ...params } }),
  tasks: (params?: DashboardTimeParams) =>
    http.get<DashboardTaskItem[]>('/api/reports/dashboard/tasks', { params }),
  alerts: (params?: DashboardTimeParams) =>
    http.get<DashboardAlertItem[]>('/api/reports/dashboard/alerts', { params }),
  warehouseStock: (params?: DashboardTimeParams) =>
    http.get<DashboardWarehouseStock[]>('/api/reports/dashboard/warehouse-stock', { params }),
}
