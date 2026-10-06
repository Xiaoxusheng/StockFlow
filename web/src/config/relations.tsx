/**
 * 上下文导航注册表（作业效率提升层一期 §2.6，frontend.md §10.3/§10.4 下钻先例）。
 *
 * 实体 → 关联业务项：详情页把当前可用的业务标识传入，`SfRelationNav` 消费本表渲染 chip 组，
 * 点击 = 带 query 预填跳转到目标列表页（预填参数一律取**目标列表页真实支持**的 query 白名单键，
 * 不接受「看起来像能过滤」的臆造参数——后端白名单外参数会被忽略，静默失效即假功能）。
 *
 * 一期不新增后端过滤参数（计划 §2.6 允许的最小追加未触发）：凡目标列表缺按当前实体过滤的
 * 参数者，对应项 `to` 返回 null（不渲染该项），不伪造不可达入口；已确认参数如下（本会话实读）：
 *   inventory/stock     → sku_id / batch_id / warehouse_id / bin_id / zone_id / shelf_id
 *   inventory/ledger    → sku_id / batch_id / bin_id / warehouse_id / serial_no / business_no / change_type
 *   inventory/batches   → sku_id / supplier_id / batch_no
 *   inventory/serials   → sku_id / batch_id / warehouse_id / bin_id / serial_no
 *   inventory/trace     → sku_id / serial_no / warehouse_id / limit
 *   inbound             → keyword / status / source_type / source_no / warehouse_id
 *   purchases           → keyword / status / supplier_id / warehouse_id
 *   purchases/receipts  → inbound_no / warehouse_id
 *   purchases/returns   → status / source_no / warehouse_id
 *   outbound            → status / warehouse_id / so_no / outbound_no
 *   picking/checking/packing/shipment → outbound_no / warehouse_id（picking/checking 另有 status）
 *   sales               → so_no / status / customer_id / warehouse_id
 *   sales/returns       → source_no / status / warehouse_id
 *   counts              → warehouse_id / status / count_no
 */

/** 关联上下文：详情页传入的业务标识（值一律可字符串化——用于 URL query 预填） */
export interface RelationContext {
  [key: string]: string | number | undefined | null
}

export interface RelationItem {
  /** 稳定键（同实体下唯一，用作 React key 与测试锚点） */
  key: string
  label: string
  /**
   * 按钮级权限码（types/permission canAccess fail-closed 过滤；
   * 缺省 = 认证即可——与菜单码同一套 RESOURCE_ALIASES 归一规则）。
   */
  permission?: string
  /** 目标地址（含 query 预填）；返回 null = 当前上下文缺必需参数 → 该项不渲染 */
  to: (ctx: RelationContext) => string | null
  /** 悬停说明（如「按 SKU 过滤」「该列表无按单号过滤参数，落地后需自行检索」） */
  hint?: string
}

export type RelationEntity =
  | 'sku'
  | 'inbound'
  | 'purchase_order'
  | 'outbound'
  | 'sales_order'
  | 'count'

/** 目标地址构造：空值跳过，输出 `/path?a=1&b=2`（无有效参数时返回裸 path） */
function target(path: string, params: Record<string, unknown>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    search.set(key, String(value))
  }
  const qs = search.toString()
  return qs ? `${path}?${qs}` : path
}

/** 取上下文值并保证非空（空值 → undefined，交由调用方决定是否可渲染） */
function pick(ctx: RelationContext, key: string): string | undefined {
  const value = ctx[key]
  if (value === undefined || value === null || value === '') return undefined
  return String(value)
}

/**
 * 注册表（唯一数据源，禁止页面内联散写关联逻辑）。
 * 每一项都对应**真实可达**的目标；数据端点为前端先行契约者，页面呈统一错误态（预期行为）。
 */
export const RELATIONS: Record<RelationEntity, RelationItem[]> = {
  /** SKU 详情（§10.3：一屏看全该 SKU 的库存与关联业务） */
  sku: [
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '按 SKU 过滤的实时库存',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/stock', { sku_id: skuId }) : null
      },
    },
    {
      key: 'ledger',
      label: '库存流水',
      permission: 'inventory:ledger:view',
      hint: '按 SKU 过滤的出入库流水',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId }) : null
      },
    },
    {
      key: 'batches',
      label: '批次库存',
      permission: 'inventory:batch:view',
      hint: '按 SKU 过滤的批次',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/batches', { sku_id: skuId }) : null
      },
    },
    {
      key: 'serials',
      label: '序列号',
      permission: 'inventory:serial:view',
      hint: '按 SKU 过滤的序列号',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/serials', { sku_id: skuId }) : null
      },
    },
    {
      key: 'trace',
      label: '库存追溯',
      permission: 'inventory:trace:view',
      hint: '该 SKU 的完整追溯链',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/trace', { sku_id: skuId }) : null
      },
    },
    {
      key: 'qrcode',
      label: '商品二维码',
      permission: 'sku:view',
      hint: '打开二维码中心并预置该 SKU（qr-code.md §10 扫码直达同款入口）',
      to: (ctx) => {
        const code = pick(ctx, 'sku_code')
        return code ? target('/qr-codes', { code }) : null
      },
    },
  ],

  /** 入库单详情（business-flow §3：收货 → 质检 → 上架 → 库存） */
  inbound: [
    {
      key: 'receipts',
      label: '收货记录',
      permission: 'purchase:receipt:view',
      hint: '按入库单号过滤的收货明细',
      to: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no ? target('/purchases/receipts', { inbound_no: no }) : null
      },
    },
    {
      key: 'quality',
      label: '质检单',
      permission: 'quality:view',
      hint: '以入库单号作为来源单号的质检单',
      to: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no ? target('/quality/inspections', { source_no: no }) : null
      },
    },
    {
      key: 'putaway',
      label: '上架任务',
      permission: 'inventory:stock:view',
      hint: '任务中心的上架任务列表',
      to: () => target('/tasks', { task_type: 'putaway' }),
    },
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存',
      to: (ctx) => {
        const wid = pick(ctx, 'warehouse_id')
        return target('/inventory/stock', { warehouse_id: wid })
      },
    },
    {
      key: 'purchase',
      label: '来源采购单',
      permission: 'purchase:view',
      hint: '以采购单号检索采购订单',
      to: (ctx) => {
        const no = pick(ctx, 'source_no')
        return no ? target('/purchases', { keyword: no }) : null
      },
    },
  ],

  /** 采购订单详情（business-flow §2：采购 → 入库 → 收货 → 库存） */
  purchase_order: [
    {
      key: 'inbound',
      label: '关联入库单',
      permission: 'inbound:view',
      hint: '以采购单号作为来源单号的入库单',
      to: (ctx) => {
        const no = pick(ctx, 'po_no')
        return no ? target('/inbound', { source_no: no }) : null
      },
    },
    {
      key: 'returns',
      label: '采购退货',
      permission: 'purchase:return:view',
      hint: '以采购单号作为来源单号的退货单',
      to: (ctx) => {
        const no = pick(ctx, 'po_no')
        return no ? target('/purchases/returns', { source_no: no }) : null
      },
    },
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存',
      to: (ctx) => target('/inventory/stock', { warehouse_id: pick(ctx, 'warehouse_id') }),
    },
  ],

  /** 出库单详情（business-flow §8：拣货 → 复核 → 打包 → 发货） */
  outbound: [
    {
      key: 'picking',
      label: '拣货任务',
      permission: 'picking:view',
      hint: '按出库单号过滤的拣货任务',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/picking', { outbound_no: no }) : null
      },
    },
    {
      key: 'checking',
      label: '复核任务',
      permission: 'checking:view',
      hint: '按出库单号过滤的复核任务',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/checking', { outbound_no: no }) : null
      },
    },
    {
      key: 'packing',
      label: '打包记录',
      permission: 'packing:view',
      hint: '按出库单号过滤的包裹记录',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/packing', { outbound_no: no }) : null
      },
    },
    {
      key: 'shipment',
      label: '发货单',
      permission: 'shipment:view',
      hint: '按出库单号过滤的发货单',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/shipment', { outbound_no: no }) : null
      },
    },
    {
      key: 'sales_order',
      label: '来源销售单',
      permission: 'sales:view',
      hint: '以销售单号检索销售订单',
      to: (ctx) => {
        const no = pick(ctx, 'so_no')
        return no ? target('/sales', { so_no: no }) : null
      },
    },
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存',
      to: (ctx) => target('/inventory/stock', { warehouse_id: pick(ctx, 'warehouse_id') }),
    },
  ],

  /** 销售订单详情（business-flow §6/§7：销售 → 出库 → 发货 / 退货） */
  sales_order: [
    {
      key: 'outbound',
      label: '关联出库单',
      permission: 'outbound:view',
      hint: '以销售单号过滤的出库单',
      to: (ctx) => {
        const no = pick(ctx, 'so_no')
        return no ? target('/outbound', { so_no: no }) : null
      },
    },
    {
      key: 'returns',
      label: '销售退货',
      permission: 'sales:return:view',
      hint: '以销售单号作为来源单号的退货单',
      to: (ctx) => {
        const no = pick(ctx, 'so_no')
        return no ? target('/sales/returns', { source_no: no }) : null
      },
    },
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存',
      to: (ctx) => target('/inventory/stock', { warehouse_id: pick(ctx, 'warehouse_id') }),
    },
  ],

  /** 盘点单详情（business-flow §10.2/§10.5：差异 → 库存调整单） */
  count: [
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存（核对账面）',
      to: (ctx) => target('/inventory/stock', { warehouse_id: pick(ctx, 'warehouse_id') }),
    },
    {
      key: 'ledger',
      label: '库存流水',
      permission: 'inventory:ledger:view',
      hint: '本单所属仓库的出入库流水',
      to: (ctx) => target('/inventory/ledger', { warehouse_id: pick(ctx, 'warehouse_id') }),
    },
    {
      key: 'adjustments',
      label: '库存调整单',
      permission: 'inventory:adjustment:view',
      hint: '盘点差异经调整单落账（该列表无来源单号过滤参数，落地后请按单号检索）',
      to: () => '/inventory/adjustments',
    },
  ],
}
