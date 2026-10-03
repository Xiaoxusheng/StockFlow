import { http } from './client'

/** Dashboard 视图（requirements.md §2.1：管理层 / 仓库人员两套指标分列，后端按 view 出数） */
export type DashboardView = 'management' | 'operator'

/** 五个 dashboard 端点的公共参数：视图判定 + 自定义时间段（前端先行契约扩展，后端交付前统一错误态） */
export interface DashboardScopeParams {
  /** 视图由前端按权限 fail-closed 判定（views/dashboard/dashboardView.ts resolveDashboardView） */
  view?: DashboardView
  /** 自定义时间窗起止（YYYY-MM-DD）；仅 trend 图表时间段切换时下发 */
  from?: string
  /** 自定义时间窗结束（YYYY-MM-DD） */
  to?: string
}

/**
 * 今日业务指标（requirements.md §2.1 / frontend.md §5 第一层）。
 * 公共字段两视图均下发；视图附加字段为前端先行契约（可选），后端交付后填充。
 */
export interface DashboardTodayMetrics {
  /** —— 两视图公共 —— */
  todayInboundCount: number
  todayOutboundCount: number
  pendingTaskCount: number
  stockAlertCount: number
  /** —— 管理层视图（view=management，requirements.md §2.1）—— */
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
  /** —— 仓库人员视图（view=operator，requirements.md §2.1）—— */
  pendingReceiveCount?: number
  pendingPutawayCount?: number
  pendingPickCount?: number
  pendingCheckCount?: number
  pendingPackCount?: number
  pendingShipmentCount?: number
  pendingCountCount?: number
}

/** 趋势时间档：预设档 + custom（from/to 真实传参，requirements.md §2.1「自定义时间」） */
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
  todayMetrics: (params?: DashboardScopeParams) =>
    http.get<DashboardTodayMetrics>('/api/reports/dashboard/today', { params }),
  trend: (range: TrendRange, params?: DashboardScopeParams) =>
    http.get<TrendPoint[]>('/api/reports/dashboard/trend', { params: { range, ...params } }),
  tasks: (params?: DashboardScopeParams) =>
    http.get<DashboardTaskItem[]>('/api/reports/dashboard/tasks', { params }),
  alerts: (params?: DashboardScopeParams) =>
    http.get<DashboardAlertItem[]>('/api/reports/dashboard/alerts', { params }),
  warehouseStock: (params?: DashboardScopeParams) =>
    http.get<DashboardWarehouseStock[]>('/api/reports/dashboard/warehouse-stock', { params }),
}
