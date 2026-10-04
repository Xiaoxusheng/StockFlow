import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 报表域（后端 M3 已交付：GET /api/reports 目录 + 六个报表端点，
// internal/reports/routes.go:38-44；行结构逐字段对齐 internal/reports/repository.go
// 各 Row 的 JSON tag——snake_case。数据权限以会话仓库范围快照为准，前端不传范围参数，
// handler.go:19-24） ----------

/** 报表目录项（reportCatalogItem，catalog.go:8-13） */
export interface ReportCatalogItem {
  key: string
  name: string
  /** 报表分类：后端冻结目录仅「库存/单据/分析」三类（catalog.go:15-17；
   * requirements §2.4 所列「效率」类因仓库/人员效率无流水语义不进目录，偏差已由后端显式披露） */
  category: string
  description?: string
}

/**
 * 报表域时间范围参数（requireRange，handler.go:26-53；validateRange，calc.go:24-40）：
 * time_from/time_to 接受 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss；均缺省时取近 30 天，
 * 范围上限 366 天（超限报 REPORT_RANGE_TOO_LARGE）。参数名以后端冻结为准，禁止 from/to。
 */
export interface ReportRangeQuery extends PageQuery {
  time_from?: string
  time_to?: string
}

/** 库存汇总筛选（handler.go:82-89：可选 warehouse_id / sku_id，整数） */
export interface InventorySummaryQuery extends PageQuery {
  warehouse_id?: number | string
  sku_id?: number | string
}

/** 库存汇总行（InventorySummaryRow，repository.go:61-76；按仓 + SKU 分组） */
export interface InventorySummaryRow {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_id: number
  sku_code: string
  sku_name: string
  total_qty: number
  available_qty: number
  locked_qty: number
  frozen_qty: number
  pending_inspect_qty: number
  defective_qty: number
  /** 库存金额 = Σ 数量 × 成本价（批次成本价优先，非批次 SKU 用 SKU 成本价） */
  stock_value: number
}

/** 出入库统计行（FlowStatRow，repository.go:115-123；按日聚合） */
export interface FlowStatRow {
  /** 统计日（created_at::date，数据库会话时区） */
  stat_date: string
  /** 当日 DISTINCT 业务单号数 */
  order_count: number
  qty: number
  /** 金额估值 = Σ|qty_change| × 估值单价（口径见 FlowStatsPage.valuation） */
  amount: number
}

/** 金额估值口径（handler.go:147-152：现场聚合无订单金额列，按 SKU 单价估值——口径显式披露） */
export type FlowValuationBasis = 'cost_price' | 'sale_price'

/**
 * 出入库统计响应（handler.go:139-143：行集与估值口径再包一层 items 对象，
 * 外层仍为 {page,pageSize,total,items} 分页信封）。
 */
export interface FlowStatsPage {
  page: number
  pageSize: number
  total: number
  items: {
    items: FlowStatRow[]
    total: number
    valuation: { basis: FlowValuationBasis }
  }
}

/** 库存周转行（TurnoverRow，repository.go:148-163；按仓 + SKU） */
export interface TurnoverRow {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_id: number
  sku_code: string
  sku_name: string
  outbound_qty: number
  /** 期初库存 = 期末 − 窗口净变化（流水可重构） */
  start_qty: number
  end_qty: number
  /** 平均库存 =（期初 + 期末）/ 2；周转率 = 出库量/平均库存，天数 = 周期天数/周转率
   * （Service 层纯函数计算，service.go:103-124） */
  avg_inventory: number
  last_moved_at?: string
  turnover_rate: number
  turnover_days: number
}

/** 积压识别行（StagnantRow，repository.go:218-233） */
export interface StagnantRow {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_id: number
  sku_code: string
  sku_name: string
  total_qty: number
  /** 末次移动时间（无任何流水时回退首次入库建账时间） */
  last_moved_at: string
  /** 未动天数（截至查询时点，按自然 24h 折算） */
  idle_days: number
  /** 分档：命中的最大阈值天数（"30"/"60"/"90"，阈值清单可配置——calc.go:91-98） */
  tier: string
}

/** 补货建议筛选（handler.go:210-213：only_shortage=false 返回全部参与计算的行，缺省仅缺货行） */
export interface ReplenishmentQuery extends PageQuery {
  only_shortage?: boolean
}

/** 补货建议行（ReplenishmentSuggestion，service.go:156-163 内嵌 ReplenishmentRow
 * repository.go:276-289 + 依据字段；只读建议，不改库存不生成单据） */
export interface ReplenishmentSuggestion {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  sku_id: number
  sku_code: string
  sku_name: string
  /** 日均销量（近 30 天 OUTBOUND 流水量 / 30） */
  daily_avg_sales: number
  safety_stock: number
  /** 在途 = 采购在途（PO 已审核未收齐）+ 调拨在途 */
  incoming_qty: number
  current_available: number
  /** 目标库存 = max(安全库存, 日均销量 × (采购周期 + 缓冲)) */
  target_stock: number
  /** 建议补货量 = max(0, 目标库存 − 可用 − 在途) */
  suggested_qty: number
  lead_time_days: number
  buffer_days: number
  /** 计算依据说明（公式 + 各输入值，人读） */
  basis_text: string
}

export const reportsApi = {
  /** 报表目录：GET /api/reports（reports:report:list，代码内冻结注册表） */
  catalog: () => http.get<ReportCatalogItem[]>('/api/reports'),
  /** 库存汇总：GET /api/reports/inventory-summary（reports:report:read） */
  inventorySummary: (query: InventorySummaryQuery) =>
    http.get<PageResult<InventorySummaryRow>>('/api/reports/inventory-summary', { params: query }),
  /** 入库统计：GET /api/reports/inbound-stats（reports:report:read；INBOUND 流水落账） */
  inboundStats: (query: ReportRangeQuery) =>
    http.get<FlowStatsPage>('/api/reports/inbound-stats', { params: query }),
  /** 出库统计：GET /api/reports/outbound-stats（reports:report:read；OUTBOUND 流水落账） */
  outboundStats: (query: ReportRangeQuery) =>
    http.get<FlowStatsPage>('/api/reports/outbound-stats', { params: query }),
  /** 库存周转：GET /api/reports/inventory-turnover（reports:report:read） */
  inventoryTurnover: (query: ReportRangeQuery) =>
    http.get<PageResult<TurnoverRow>>('/api/reports/inventory-turnover', { params: query }),
  /** 库存积压：GET /api/reports/stagnant-stock（reports:report:read；30/60/90 天未动清单） */
  stagnantStock: (query: PageQuery) =>
    http.get<PageResult<StagnantRow>>('/api/reports/stagnant-stock', { params: query }),
  /** 智能补货建议：GET /api/reports/replenishment-suggestions（reports:report:read） */
  replenishmentSuggestions: (query: ReplenishmentQuery) =>
    http.get<PageResult<ReplenishmentSuggestion>>('/api/reports/replenishment-suggestions', {
      params: query,
    }),
}
