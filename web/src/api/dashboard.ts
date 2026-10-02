import { http } from './client'

/** Dashboard 今日业务指标（requirements.md §2.1 / frontend.md §5 第一层） */
export interface DashboardTodayMetrics {
  todayInboundCount: number
  todayOutboundCount: number
  pendingTaskCount: number
  stockAlertCount: number
}

export type TrendRange = '7d' | '30d' | '90d'

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

export interface DashboardAlertItem {
  id: number | string
  type: string
  level: string
  skuCode: string
  productName: string
  message: string
  createdAt: string
}

/** 仓库库存分析（frontend.md §5 第四层） */
export interface DashboardWarehouseStock {
  warehouseCode: string
  warehouseName: string
  skuCount: number
  totalQty: number
  binUtilization: number
}

export const dashboardApi = {
  todayMetrics: () => http.get<DashboardTodayMetrics>('/api/reports/dashboard/today'),
  trend: (range: TrendRange) =>
    http.get<TrendPoint[]>('/api/reports/dashboard/trend', { params: { range } }),
  tasks: () => http.get<DashboardTaskItem[]>('/api/reports/dashboard/tasks'),
  alerts: () => http.get<DashboardAlertItem[]>('/api/reports/dashboard/alerts'),
  warehouseStock: () => http.get<DashboardWarehouseStock[]>('/api/reports/dashboard/warehouse-stock'),
}
