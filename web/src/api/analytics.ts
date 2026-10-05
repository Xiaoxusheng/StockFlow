import { http } from './client'
import type { FlowStatRow, FlowValuationBasis, ReportRangeQuery } from './reports'

// ---------- 聚合分析端点批（2026-10-05 分析卡片轮 14 端点统一收口文件，六域并行工程师
// 只消费此文件、不散落各域 api 文件——InboundAnalyticsPage.tsx:44『本域不建专属 api 文件，
// 只读共享』先例；消费点禁直连 http，统一走 analyticsApi，AGENTS.md 规则 6）。
// 字段与响应形状逐字段对齐 internal/reports/analytics.go / workbench.go 各 DTO 的
// JSON tag（snake_case）；权限挂载见 internal/reports/routes.go:88-108（inventory
// 前缀挂 inventory:inventory:list、reports 前缀挂 reports:report:read、采购/销售六端点
// 挂域列表码 purchase:purchase:list / sales:sales:list——立项若裁决改挂 reports:report:read
// 可平移不改契约形状，docs/api.md §9 收口披露节①）。
// 免分页直出端点（TopN≤50 / 趋势≤366 行）不走分页信封，response.OK 直出裸数组或对象；
// ReportRangeQuery 沿用 reports.ts（time_from/time_to 缺省近 30 天、上限 366 天，
// requireRange 校验），其 page/pageSize 字段对免分页端点被后端忽略、仅类型复用。 ----------

/** SKU 库存 TOP N 行（skuTopRow，analytics.go:30-39；HAVING SUM(total_qty)>0 只看在库 SKU） */
export interface InventorySkuTopRow {
  sku_id: number
  sku_code: string
  sku_name: string
  /** 现存量合计 */
  total_qty: number
  /** 库存金额（成本价口径：批次成本价优先，非批次 SKU 用 SKU 成本价——与 inventory-summary 同源） */
  stock_value: number
}

/** 库存周转趋势行（turnoverTrendRow，analytics.go:43-53；日粒度连续序列，缺口后端补零） */
export interface InventoryTurnoverTrendRow {
  /** 统计日 YYYY-MM-DD（字符串形态，dashboardTrendRepo 先例） */
  date: string
  /** 当日 Σ|OUTBOUND 流水量| */
  outbound_qty: number
  /** 日末现存量（现存量锚点回推，同 dashboard/trend 口径） */
  end_qty: number
  /** 平均库存 =（日初 + 日末）/2；日初 = 日末 − 当日净变化 */
  avg_inventory: number
  /** 周转率 = outbound_qty / avg_inventory（avg≤0 记 0——不造假分母） */
  turnover_rate: number
}

/** 状态构成响应（statusCompositionDTO，workbench.go:61-64；items 仅含 count>0 态，
 * total=全量单据数，排序 count DESC——图表友好） */
export interface StatusComposition {
  total: number
  items: Array<{ status: string; count: number }>
}

/** 供应商入库排行行（inboundSupplierRankRow，workbench.go:68-74；仅 source_type='PURCHASE'
 * 经 source_no=po_no 关联 PO→供应商，OTHER 来源不入榜） */
export interface InboundSupplierRankRow {
  supplier_id: number
  supplier_code: string
  supplier_name: string
  inbound_count: number
  /** received_qty = Σ inbound_items.qty_received */
  received_qty: number
}

/** 出库订单完成率（completionRateDTO，workbench.go:78-87；分母=status≠CANCELLED 出库单数，
 * 分子=SHIPPED_ALL+CLOSED（差额关闭视为完成出库流程），分母 0→0） */
export interface OutboundCompletionRate {
  order_total: number
  cancelled: number
  denominator: number
  shipped_all: number
  closed: number
  in_progress: number
  completion_rate: number
}

/** 商品出库排行行（outboundRankRow，workbench.go:90-96；OUTBOUND 流水 GROUP BY sku——
 * inventory Deduct 恒写 OUTBOUND，含采购退货出库等一切出库扣减，与 outbound-stats
 * 同口径可对账；前端卡片副标题须如实披露估值口径，docs/api.md §9 收口披露节②） */
export interface OutboundProductRankRow {
  sku_id: number
  sku_code: string
  sku_name: string
  outbound_qty: number
  /** 金额 = Σ|qty_change| × SKU sale_price（售价估值，非订单实收） */
  amount: number
}

/** 商品出库排行响应（outboundProductRankDTO，workbench.go:100-103；valuation.basis=sale_price
 * 随响应显式披露） */
export interface OutboundProductRankPage {
  valuation: { basis: FlowValuationBasis }
  items: OutboundProductRankRow[]
}

/** 仓库作业量行（warehouseWorkloadRow，workbench.go:106-114；可见仓全集 LEFT JOIN，
 * 零作业仓返回零行；排序两 qty 之和 DESC） */
export interface WarehouseWorkloadRow {
  warehouse_id: number
  warehouse_code: string
  warehouse_name: string
  /** COUNT(DISTINCT business_no) 口径 */
  inbound_order_count: number
  outbound_order_count: number
  /** Σ|qty_change|（INBOUND/OUTBOUND 流水） */
  inbound_qty: number
  outbound_qty: number
}

/** 采购/销售订单金额趋势行（orderTrendRow，workbench.go:117-121） */
export interface OrderAmountTrendRow {
  /** 统计日 YYYY-MM-DD（字符串形态，to_char 落库口径） */
  date: string
  order_count: number
  amount: number
}

/** 订单金额趋势响应（orderTrendDTO，workbench.go:125-128；metric=order_amount 显式披露
 * ≠ inbound/outbound-stats 流水估值——销售页须标注「订单金额口径，非流水估值」，
 * docs/api.md §9 收口披露节③） */
export interface OrderAmountTrendPage {
  metric: 'order_amount'
  items: OrderAmountTrendRow[]
}

/** 供应商采购排行行（purchaseSupplierRankRow，workbench.go:131-137；状态过滤
 * NOT IN ('DRAFT','CANCELLED')，排序 total_amount DESC） */
export interface PurchaseSupplierRankRow {
  supplier_id: number
  supplier_code: string
  supplier_name: string
  order_count: number
  total_amount: number
}

/** 商品销售排行行（salesProductRankRow，workbench.go:141-148；qty=Σ items.qty 下单量、
 * amount=Σ items.amount 行金额（下单金额非估值）） */
export interface SalesProductRankRow {
  sku_id: number
  sku_code: string
  sku_name: string
  order_count: number
  qty: number
  amount: number
}

/** 出入库流水趋势响应（flowTrendDTO，analytics.go:62-65；items 复用 reports.ts FlowStatRow——
 * 与既有 inbound/outbound-stats 行形逐字段一致，stat_date JSONTime 收敛口径不变；
 * 估值口径 inbound→cost_price、outbound→sale_price 与既有出入库统计一致） */
export interface FlowTrendPage {
  valuation: { basis: FlowValuationBasis }
  items: FlowStatRow[]
}

export const analyticsApi = {
  /** SKU 库存 TOP N：GET /api/inventory/sku-top（inventory:inventory:list；
   * metric 缺省 qty、limit 缺省 10（1–50）、warehouse_id 缺省不过滤） */
  inventorySkuTop: (query: { metric?: 'qty' | 'value'; limit?: number; warehouse_id?: number | string }) =>
    http.get<InventorySkuTopRow[]>('/api/inventory/sku-top', { params: query }),
  /** 库存周转趋势：GET /api/inventory/turnover-trend（inventory:inventory:list；
   * days 缺省 30（1–366）） */
  inventoryTurnoverTrend: (query: { days?: number }) =>
    http.get<InventoryTurnoverTrendRow[]>('/api/inventory/turnover-trend', { params: query }),
  /** 入库单状态构成：GET /api/inbounds/status-composition（reports:report:read；无参数全量现状分布） */
  inboundStatusComposition: () => http.get<StatusComposition>('/api/inbounds/status-composition'),
  /** 供应商入库排行：GET /api/inbounds/supplier-rank（reports:report:read；limit 缺省 10（1–50）） */
  inboundSupplierRank: (query: ReportRangeQuery & { limit?: number }) =>
    http.get<InboundSupplierRankRow[]>('/api/inbounds/supplier-rank', { params: query }),
  /** 出库订单完成率：GET /api/outbounds/completion-rate（reports:report:read） */
  outboundCompletionRate: (query: ReportRangeQuery) =>
    http.get<OutboundCompletionRate>('/api/outbounds/completion-rate', { params: query }),
  /** 商品出库排行：GET /api/outbounds/product-rank（reports:report:read；估值口径披露见
   * OutboundProductRankPage/OutboundProductRankRow——api.md §9 收口披露节②） */
  outboundProductRank: (query: ReportRangeQuery & { limit?: number }) =>
    http.get<OutboundProductRankPage>('/api/outbounds/product-rank', { params: query }),
  /** 仓库作业量：GET /api/warehouses/workload（inventory:inventory:list——仓库分析页
   * warehouse-stock 同码） */
  warehouseWorkload: (query: ReportRangeQuery) =>
    http.get<WarehouseWorkloadRow[]>('/api/warehouses/workload', { params: query }),
  /** 出入库流水趋势：GET /api/reports/flow-trend（reports:report:read；type 必填
   * inbound|outbound——ReportFlowStats 趋势换端点，去分页销 report-flowstats-trend-cap） */
  flowTrend: (query: { type: 'inbound' | 'outbound'; time_from?: string; time_to?: string }) =>
    http.get<FlowTrendPage>('/api/reports/flow-trend', { params: query }),
  /** 采购订单金额趋势：GET /api/purchases/analytics/trend（purchase:purchase:list；
   * 订单金额口径 Σ total_amount，status NOT IN ('DRAFT','CANCELLED')） */
  purchaseTrend: (query: ReportRangeQuery) =>
    http.get<OrderAmountTrendPage>('/api/purchases/analytics/trend', { params: query }),
  /** 供应商采购排行：GET /api/purchases/supplier-rank（purchase:purchase:list） */
  purchaseSupplierRank: (query: ReportRangeQuery & { limit?: number }) =>
    http.get<PurchaseSupplierRankRow[]>('/api/purchases/supplier-rank', { params: query }),
  /** 采购单状态构成：GET /api/purchases/status-composition（purchase:purchase:list） */
  purchaseStatusComposition: () => http.get<StatusComposition>('/api/purchases/status-composition'),
  /** 销售订单金额趋势：GET /api/sales/analytics/trend（sales:sales:list；订单金额口径
   * status NOT IN ('DRAFT','REJECTED','CANCELLED')——页面须标注「订单金额口径，非流水估值」） */
  salesTrend: (query: ReportRangeQuery) =>
    http.get<OrderAmountTrendPage>('/api/sales/analytics/trend', { params: query }),
  /** 商品销售排行：GET /api/sales/product-rank（sales:sales:list；sort 缺省 qty） */
  salesProductRank: (query: ReportRangeQuery & { sort?: 'qty' | 'amount'; limit?: number }) =>
    http.get<SalesProductRankRow[]>('/api/sales/product-rank', { params: query }),
  /** 销售单状态构成：GET /api/sales/status-composition（sales:sales:list） */
  salesStatusComposition: () => http.get<StatusComposition>('/api/sales/status-composition'),
}
