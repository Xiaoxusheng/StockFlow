/**
 * 上下文导航注册表（作业效率提升层一期 §2.6，frontend.md §10.3/§10.4 下钻先例）。
 *
 * 实体 → 关联业务项：详情页把当前可用的业务标识传入，`SfRelationNav` 消费本表渲染 chip 组，
 * 点击**优先打开 Drawer 内嵌列表**（`list` 配置，不离开当前页——ask 冻结口径）；未配 `list`
 * 或上下文缺必需参数时回退为**带 query 预填跳转**到目标列表页（预填参数一律取**目标列表页
 * 真实支持**的 query 白名单键，不接受「看起来像能过滤」的臆造参数——后端白名单外参数会被
 * 忽略，静默失效即假功能）。
 *
 * 一期不新增后端过滤参数（计划 §2.6 允许的最小追加未触发）：凡目标列表缺按当前实体过滤的
 * 参数者，对应项 `to` 返回 null（不渲染该项），不伪造不可达入口；已确认参数如下（实读各域
 * handler 的 query 白名单，行号见注释）：
 *   inventory/stock     → sku_id / batch_id / warehouse_id / bin_id / zone_id / shelf_id
 *   inventory/ledger    → sku_id / batch_id / bin_id / warehouse_id / serial_no / business_no / change_type（值域经后端校验 internal/inventory/handler.go:352-371）
 *   inventory/batches   → sku_id / supplier_id / batch_no
 *   inventory/serials   → sku_id / batch_id / warehouse_id / bin_id / serial_no
 *   inventory/trace     → sku_id / serial_no / warehouse_id / limit
 *   inbound             → keyword / status / source_type / source_no / warehouse_id（internal/purchase/handler.go:289-309）
 *   receipts            → inbound_no / receipt_no / warehouse_id（internal/purchase/handler.go:436-455——**无 po_no/source_no**，采购单→收货经 ctx.inbound_no 驱动）
 *   purchases           → keyword / status / supplier_id / warehouse_id
 *   purchases/returns   → status / source_no / warehouse_id
 *   quality             → keyword / status / source_type / source_no / warehouse_id（internal/purchase/handler.go:576-596）
 *   outbound            → status / warehouse_id / so_no / outbound_no（internal/sales/handler.go:325-340）
 *   picking/checking/packing/shipment → outbound_no / warehouse_id（picking/checking 另有 status/assignee_id；**无 so_no**——销售单→四作业项经 ctx.outbound_no 驱动）
 *   sales               → so_no / status / customer_id / warehouse_id
 *   sales/returns       → source_no / status / warehouse_id
 *   counts              → warehouse_id / status / count_no
 *   bins                → keyword / warehouseId / zoneId / shelfId / binType / status（internal/warehouse/handler.go，camelCase 白名单）
 *   transfers           → status / type / transfer_no（internal/stockops/handler.go:385-387——**无 sku_id**，SKU→调拨经 ledger 流水 TRANSFER_OUT/TRANSFER_IN 承载）
 *
 * SKU→盘点：count 列表无 sku 过滤参数，经 ledger ADJUST（盘点差异调整落账，business-flow
 * §10.5）承载——展示该 SKU 的盘点调整流水，不伪造盘点单过滤入口。
 */
import type { TableProps } from 'antd'
import type { PageQuery, PageResult } from '@/types/api'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { resolveStatus } from '@/types/status'
import { EMPTY_TEXT, formatDateTime, formatMoney, formatQty } from '@/utils/format'
import {
  inventoryApi,
  type BatchItem,
  type BatchQuery,
  type InventoryChangeType,
  type LedgerItem,
  type LedgerQuery,
  type SerialItem,
  type SerialQuery,
  type StockItem,
  type StockQuery,
} from '@/api/inventory'
import { binApi, type BinItem, type BinQuery } from '@/api/warehouse'
import { inboundApi, type InboundOrder, type InboundOrderQuery } from '@/api/inbound'
import { purchaseApi, type Receipt, type ReceiptQuery } from '@/api/purchase'
import { qualityApi, type QualityInspectionItem, type QualityInspectionQuery } from '@/api/quality'
import {
  outboundApi,
  outboundTaskApi,
  type CheckTask,
  type OutboundOrder,
  type OutboundOrderQuery,
  type PackingRecord,
  type PickTask,
  type Shipment,
} from '@/api/outbound'

/** 关联上下文：详情页传入的业务标识（值一律可字符串化——用于 URL query 预填） */
export interface RelationContext {
  [key: string]: string | number | undefined | null
}

/**
 * Drawer 内嵌列表配置（ask 冻结口径「优先 Drawer 内嵌，不强制离开当前页」）：
 * `SfRelationListDrawer` 消费——受控分页直查目标列表端点，复用 SfTable nested 形态
 * （统一 Loading/Empty/Error/密度，禁止第二套表格）。
 * any 为受控逃逸口：各列定义保留具体 Item 类型（TableProps<StockItem> 等），仅在契约
 * 边界（本接口 + Drawer 内部组件引用）收拢，避免多形态列表引入第二层泛型体操。
 */
/* eslint-disable @typescript-eslint/no-explicit-any */
export interface RelationListSpec {
  /** Drawer 标题（如「实时库存 · 按 SKU 过滤」） */
  title: string
  /** 分页查询：过滤参数由工厂闭包按 ctx 预填，分页参数由 Drawer 注入 */
  fetch: (query: PageQuery) => Promise<PageResult<any>>
  columns: TableProps<any>['columns']
}
/* eslint-enable @typescript-eslint/no-explicit-any */

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
  /**
   * Drawer 内嵌列表工厂；返回 null = 当前上下文缺必需参数 → 回退跳转（`to`）。
   * 配置优先级：list 可用 → Drawer 内嵌；list 缺参/未配 → navigate(to)。
   */
  list?: (ctx: RelationContext) => RelationListSpec | null
  /** 悬停说明（如「按 SKU 过滤」「该列表无按单号过滤参数，落地后需自行检索」） */
  hint?: string
}

export type RelationEntity =
  | 'sku'
  | 'stock'
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

// ---------- 内嵌列表渲染辅助（与既有列表页同口径；注册表唯一数据源） ----------

/** ID 列渲染：0=空外键（batch_id 0=非批次 SKU 等，api/inventory.ts 注释口径） */
function renderIdOrDash(v: unknown): string {
  const n = Number(v)
  return v === null || v === undefined || v === '' || n === 0 ? EMPTY_TEXT : String(v)
}

/** 业务状态列：types/status.ts STATUS_META 注册表 + SfStatusTag（frontend.md §24） */
function renderStatus(v: string): React.ReactNode {
  const meta = resolveStatus(v)
  return <SfStatusTag label={meta?.label ?? v} semantic={meta?.semantic ?? 'neutral'} />
}

/** 流水类型文案（与 LedgerPage/StockDetailPage CHANGE_TYPE_LABEL 同源——
 * 值域=inventory_ledgers CHECK，internal/inventory/service.go:116-130；三处注册表待后续收敛） */
const CHANGE_TYPE_LABEL: Record<InventoryChangeType, string> = {
  INBOUND: '入库',
  OUTBOUND: '出库',
  TRANSFER_OUT: '调拨出库',
  TRANSFER_IN: '调拨入库',
  LOCK: '锁定',
  RELEASE: '释放',
  MOVE: '移库',
  INSPECT_PASS: '质检合格',
  INSPECT_DEFECTIVE: '质检不合格',
  ADJUST: '调整',
}

const STOCK_COLUMNS: TableProps<StockItem>['columns'] = [
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 100 },
  { title: '库位 ID', dataIndex: 'bin_id', width: 110, render: renderIdOrDash },
  { title: '批次 ID', dataIndex: 'batch_id', width: 110, render: renderIdOrDash },
  { title: '账面数量', dataIndex: 'total_qty', width: 92, align: 'right', render: formatQty },
  { title: '可用', dataIndex: 'available_qty', width: 80, align: 'right', render: formatQty },
  { title: '锁定', dataIndex: 'locked_qty', width: 80, align: 'right', render: formatQty },
]

const LEDGER_COLUMNS: TableProps<LedgerItem>['columns'] = [
  { title: '流水号', dataIndex: 'ledger_no', width: 150 },
  { title: '单据编号', dataIndex: 'business_no', width: 150, render: (v: string) => v || EMPTY_TEXT },
  {
    title: '变更类型',
    dataIndex: 'change_type',
    width: 100,
    render: (v: InventoryChangeType) => CHANGE_TYPE_LABEL[v] ?? v,
  },
  { title: '数量', dataIndex: 'qty_change', width: 88, align: 'right', render: formatQty },
  { title: '发生时间', dataIndex: 'created_at', width: 150, render: (v: string) => formatDateTime(v) },
]

const BATCH_COLUMNS: TableProps<BatchItem>['columns'] = [
  { title: '批次号', dataIndex: 'batch_no', width: 160 },
  { title: '供应商 ID', dataIndex: 'supplier_id', width: 110, render: renderIdOrDash },
  { title: '生产日期', dataIndex: 'production_date', width: 110, render: (v: string | null) => v || EMPTY_TEXT },
  { title: '到期日', dataIndex: 'expiry_date', width: 110, render: (v: string | null) => v || EMPTY_TEXT },
  { title: '成本价', dataIndex: 'cost_price', width: 90, align: 'right', render: (v: number) => formatMoney(v) },
]

const SERIAL_COLUMNS: TableProps<SerialItem>['columns'] = [
  { title: '序列号', dataIndex: 'serial_no', width: 170 },
  { title: '状态', dataIndex: 'status', width: 100, render: renderStatus },
  { title: '仓库 ID', dataIndex: 'warehouse_id', width: 100, render: renderIdOrDash },
  { title: '库位 ID', dataIndex: 'bin_id', width: 110, render: renderIdOrDash },
]

const BIN_COLUMNS: TableProps<BinItem>['columns'] = [
  { title: '库位编码', dataIndex: 'code', width: 140 },
  { title: '类型', dataIndex: 'bin_type', width: 90, render: (v: string) => v || EMPTY_TEXT },
  { title: '状态', dataIndex: 'status', width: 90, render: renderStatus },
  { title: '存量 SKU 数', dataIndex: 'stock_sku_count', width: 100, align: 'right' },
  { title: '存量总量', dataIndex: 'stock_total_qty', width: 92, align: 'right', render: formatQty },
]

const INBOUND_COLUMNS: TableProps<InboundOrder>['columns'] = [
  { title: '入库单号', dataIndex: 'inbound_no', width: 160 },
  { title: '来源单号', dataIndex: 'source_no', width: 160, render: (v: string) => v || EMPTY_TEXT },
  { title: '状态', dataIndex: 'status', width: 110, render: renderStatus },
  { title: '创建时间', dataIndex: 'created_at', width: 150, render: (v: string) => formatDateTime(v) },
]

const RECEIPT_COLUMNS: TableProps<Receipt>['columns'] = [
  { title: '收货单号', dataIndex: 'receipt_no', width: 160 },
  { title: '入库单号', dataIndex: 'inbound_no', width: 160 },
  { title: '操作员', dataIndex: 'operator_name', width: 100, render: (v: string) => v || EMPTY_TEXT },
  { title: '收货时间', dataIndex: 'created_at', width: 150, render: (v: string) => formatDateTime(v) },
]

const QUALITY_COLUMNS: TableProps<QualityInspectionItem>['columns'] = [
  { title: '质检单号', dataIndex: 'qc_no', width: 160 },
  { title: '来源单号', dataIndex: 'source_no', width: 160 },
  { title: '状态', dataIndex: 'status', width: 110, render: renderStatus },
  { title: '创建时间', dataIndex: 'created_at', width: 150, render: (v: string) => formatDateTime(v) },
]

const OUTBOUND_COLUMNS: TableProps<OutboundOrder>['columns'] = [
  { title: '出库单号', dataIndex: 'outbound_no', width: 160 },
  { title: '来源销售单号', dataIndex: 'so_no', width: 160 },
  { title: '状态', dataIndex: 'status', width: 110, render: renderStatus },
  { title: '发货时间', dataIndex: 'shipped_at', width: 150, render: (v: string | null) => (v ? formatDateTime(v) : EMPTY_TEXT) },
]

const PICK_TASK_COLUMNS: TableProps<PickTask>['columns'] = [
  { title: '拣货任务号', dataIndex: 'pick_no', width: 160 },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '应拣', dataIndex: 'qty', width: 80, align: 'right', render: formatQty },
  { title: '已拣', dataIndex: 'picked_qty', width: 80, align: 'right', render: formatQty },
  { title: '状态', dataIndex: 'status', width: 100, render: renderStatus },
]

const CHECK_TASK_COLUMNS: TableProps<CheckTask>['columns'] = [
  { title: '复核任务号', dataIndex: 'check_no', width: 160 },
  { title: 'SKU ID', dataIndex: 'sku_id', width: 110 },
  { title: '数量', dataIndex: 'qty', width: 80, align: 'right', render: formatQty },
  { title: '状态', dataIndex: 'status', width: 100, render: renderStatus },
  { title: '结果', dataIndex: 'result', width: 110, render: (v: string) => v || EMPTY_TEXT },
]

const PACKAGE_COLUMNS: TableProps<PackingRecord>['columns'] = [
  { title: '包裹号', dataIndex: 'package_no', width: 160 },
  { title: '包材', dataIndex: 'packing_material', width: 110, render: (v: string) => v || EMPTY_TEXT },
  { title: '承运商', dataIndex: 'carrier', width: 110, render: (v: string) => v || EMPTY_TEXT },
  { title: '运单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || EMPTY_TEXT },
]

const SHIPMENT_COLUMNS: TableProps<Shipment>['columns'] = [
  { title: '发货单号', dataIndex: 'shipment_no', width: 160 },
  { title: '承运商', dataIndex: 'carrier', width: 110, render: (v: string) => v || EMPTY_TEXT },
  { title: '运单号', dataIndex: 'tracking_no', width: 150, render: (v: string) => v || EMPTY_TEXT },
  { title: '状态', dataIndex: 'status', width: 100, render: renderStatus },
  { title: '发货时间', dataIndex: 'shipped_at', width: 150, render: (v: string | null) => (v ? formatDateTime(v) : EMPTY_TEXT) },
]

/**
 * 注册表（唯一数据源，禁止页面内联散写关联逻辑）。
 * 每一项都对应**真实可达**的目标；数据端点为前端先行契约者，页面呈统一错误态（预期行为）。
 */
export const RELATIONS: Record<RelationEntity, RelationItem[]> = {
  /** SKU 详情（§10.3：一屏看全该 SKU 的库存与关联业务；ask 冻结清单：库存/批次/序列号/
   * 出入库/调拨/盘点/追溯——调拨/盘点经 ledger 流水类型承载（transfers/count 列表无 SKU
   * 过滤参数，见文件头白名单），Drawer 内嵌优先 */
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
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '实时库存（按 SKU 过滤）',
              fetch: (q: PageQuery) => inventoryApi.stock({ ...q, sku_id: skuId } as StockQuery),
              columns: STOCK_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'ledger',
      label: '出入库流水',
      permission: 'inventory:ledger:view',
      hint: '按 SKU 过滤的出入库流水',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '出入库流水（按 SKU 过滤）',
              fetch: (q: PageQuery) => inventoryApi.ledger({ ...q, sku_id: skuId } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
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
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '批次库存（按 SKU 过滤）',
              fetch: (q: PageQuery) => inventoryApi.batches({ ...q, sku_id: skuId } as BatchQuery),
              columns: BATCH_COLUMNS,
            }
          : null
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
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '序列号（按 SKU 过滤）',
              fetch: (q: PageQuery) => inventoryApi.serials({ ...q, sku_id: skuId } as SerialQuery),
              columns: SERIAL_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'transfer-out',
      label: '调拨出库',
      permission: 'inventory:ledger:view',
      hint: '该 SKU 的调拨出库流水（transfer_no 过滤参数不存在，经流水类型承载）',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId, change_type: 'TRANSFER_OUT' }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '调拨出库流水（按 SKU 过滤）',
              fetch: (q: PageQuery) =>
                inventoryApi.ledger({ ...q, sku_id: skuId, change_type: 'TRANSFER_OUT' } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'transfer-in',
      label: '调拨入库',
      permission: 'inventory:ledger:view',
      hint: '该 SKU 的调拨入库流水',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId, change_type: 'TRANSFER_IN' }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '调拨入库流水（按 SKU 过滤）',
              fetch: (q: PageQuery) =>
                inventoryApi.ledger({ ...q, sku_id: skuId, change_type: 'TRANSFER_IN' } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'count-adjust',
      label: '盘点调整',
      permission: 'inventory:ledger:view',
      hint: '该 SKU 的盘点差异调整流水（盘点差异经调整单落账，business-flow §10.5）',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId, change_type: 'ADJUST' }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '盘点调整流水（按 SKU 过滤）',
              fetch: (q: PageQuery) =>
                inventoryApi.ledger({ ...q, sku_id: skuId, change_type: 'ADJUST' } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'trace',
      label: '库存追溯',
      permission: 'inventory:trace:view',
      hint: '该 SKU 的完整追溯链（聚合视图，跳转追溯页）',
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

  /** 库存行详情（ask 冻结清单：SKU/批次/库位/来源/去向——来源/去向经 ledger INBOUND/
   * OUTBOUND 流水承载；SKU 维度关联业务由同页 entity=sku 区块承担（StockDetailPage 双区块）） */
  stock: [
    {
      key: 'inbound-ledger',
      label: '来源流水',
      permission: 'inventory:ledger:view',
      hint: '该 SKU 在本仓的入库流水（库存来源）',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId, warehouse_id: pick(ctx, 'warehouse_id'), change_type: 'INBOUND' }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '来源流水（入库，按 SKU+仓库过滤）',
              fetch: (q: PageQuery) =>
                inventoryApi.ledger({
                  ...q,
                  sku_id: skuId,
                  warehouse_id: pick(ctx, 'warehouse_id'),
                  change_type: 'INBOUND',
                } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'outbound-ledger',
      label: '去向流水',
      permission: 'inventory:ledger:view',
      hint: '该 SKU 在本仓的出库流水（库存去向）',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/ledger', { sku_id: skuId, warehouse_id: pick(ctx, 'warehouse_id'), change_type: 'OUTBOUND' }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '去向流水（出库，按 SKU+仓库过滤）',
              fetch: (q: PageQuery) =>
                inventoryApi.ledger({
                  ...q,
                  sku_id: skuId,
                  warehouse_id: pick(ctx, 'warehouse_id'),
                  change_type: 'OUTBOUND',
                } as LedgerQuery),
              columns: LEDGER_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'batches',
      label: '批次',
      permission: 'inventory:batch:view',
      hint: '该 SKU 的批次列表',
      to: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId ? target('/inventory/batches', { sku_id: skuId }) : null
      },
      list: (ctx) => {
        const skuId = pick(ctx, 'sku_id')
        return skuId
          ? {
              title: '批次（按 SKU 过滤）',
              fetch: (q: PageQuery) => inventoryApi.batches({ ...q, sku_id: skuId } as BatchQuery),
              columns: BATCH_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'bins',
      label: '库位',
      permission: 'warehouse:bin:list',
      hint: '本仓库位列表（bins 列表无 bin_id 过滤参数，按仓库收敛；camelCase 白名单见文件头）',
      to: () => target('/warehouse/bins', {}),
      list: (ctx) => ({
        title: '库位（本仓）',
        fetch: (q: PageQuery) => binApi.list({ ...q, warehouseId: pick(ctx, 'warehouse_id') } as BinQuery),
        columns: BIN_COLUMNS,
      }),
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
      list: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no
          ? {
              title: `收货记录（${no}）`,
              fetch: (q: PageQuery) => purchaseApi.receipts.list({ ...q, inbound_no: no } as ReceiptQuery),
              columns: RECEIPT_COLUMNS,
            }
          : null
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
      list: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no
          ? {
              title: `质检单（来源 ${no}）`,
              fetch: (q: PageQuery) => qualityApi.inspections({ ...q, source_no: no } as QualityInspectionQuery),
              columns: QUALITY_COLUMNS,
            }
          : null
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
      list: (ctx) => ({
        title: '实时库存（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.stock({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as StockQuery),
        columns: STOCK_COLUMNS,
      }),
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

  /** 采购订单详情（business-flow §2：采购 → 入库 → 收货 → 质检 → 上架 → 库存；
   * ask 冻结清单五项——收货经 ctx.inbound_no 驱动（receipts 列表无 po_no 过滤参数，
   * 采购单详情提供入库单号后自动渲染）） */
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
      list: (ctx) => {
        const no = pick(ctx, 'po_no')
        return no
          ? {
              title: `关联入库单（来源 ${no}）`,
              fetch: (q: PageQuery) => inboundApi.list({ ...q, source_no: no } as InboundOrderQuery),
              columns: INBOUND_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'receipts',
      label: '收货记录',
      permission: 'purchase:receipt:view',
      hint: '按入库单号过滤的收货明细（receipts 列表仅支持 inbound_no/receipt_no 过滤）',
      to: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no ? target('/purchases/receipts', { inbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'inbound_no')
        return no
          ? {
              title: `收货记录（${no}）`,
              fetch: (q: PageQuery) => purchaseApi.receipts.list({ ...q, inbound_no: no } as ReceiptQuery),
              columns: RECEIPT_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'quality',
      label: '质检单',
      permission: 'quality:view',
      hint: '以采购单号作为来源单号的质检单',
      to: (ctx) => {
        const no = pick(ctx, 'po_no')
        return no ? target('/quality/inspections', { source_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'po_no')
        return no
          ? {
              title: `质检单（来源 ${no}）`,
              fetch: (q: PageQuery) => qualityApi.inspections({ ...q, source_no: no } as QualityInspectionQuery),
              columns: QUALITY_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'putaway',
      label: '上架任务',
      permission: 'inventory:stock:view',
      hint: '任务中心的上架任务列表（任务表无按采购单过滤参数）',
      to: () => target('/tasks', { task_type: 'putaway' }),
    },
    {
      key: 'stock',
      label: '实时库存',
      permission: 'inventory:stock:view',
      hint: '本单所属仓库的实时库存',
      to: (ctx) => target('/inventory/stock', { warehouse_id: pick(ctx, 'warehouse_id') }),
      list: (ctx) => ({
        title: '实时库存（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.stock({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as StockQuery),
        columns: STOCK_COLUMNS,
      }),
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
  ],

  /** 出库单详情（business-flow §8：拣货 → 复核 → 打包 → 发货；Drawer 内嵌优先） */
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
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `拣货任务（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.picks.list({ ...q, outbound_no: no }),
              columns: PICK_TASK_COLUMNS,
            }
          : null
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
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `复核任务（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.checks.list({ ...q, outbound_no: no }),
              columns: CHECK_TASK_COLUMNS,
            }
          : null
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
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `打包记录（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.packing.list({ ...q, outbound_no: no }),
              columns: PACKAGE_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'shipment',
      label: '发货/物流',
      permission: 'shipment:view',
      hint: '按出库单号过滤的发货单（物流跟踪同源 shipments）',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/shipment', { outbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `发货/物流（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.shipments.list({ ...q, outbound_no: no }),
              columns: SHIPMENT_COLUMNS,
            }
          : null
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
      list: (ctx) => ({
        title: '实时库存（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.stock({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as StockQuery),
        columns: STOCK_COLUMNS,
      }),
    },
  ],

  /** 销售订单详情（business-flow §6/§7：销售 → 出库 → 拣货/复核/打包/发货 → 退货；
   * ask 冻结清单六项——拣货/复核/打包/发货物流四项经 ctx.outbound_no 驱动（四列表仅支持
   * outbound_no 过滤，无 so_no；销售单详情提供出库单号后自动渲染，缺参不渲染不造假）） */
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
      list: (ctx) => {
        const no = pick(ctx, 'so_no')
        return no
          ? {
              title: `关联出库单（${no}）`,
              fetch: (q: PageQuery) => outboundApi.list({ ...q, so_no: no } as OutboundOrderQuery),
              columns: OUTBOUND_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'picking',
      label: '拣货任务',
      permission: 'picking:view',
      hint: '按出库单号过滤的拣货任务（需先经出库单取得 outbound_no）',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/picking', { outbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `拣货任务（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.picks.list({ ...q, outbound_no: no }),
              columns: PICK_TASK_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'checking',
      label: '复核任务',
      permission: 'checking:view',
      hint: '按出库单号过滤的复核任务（需先经出库单取得 outbound_no）',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/checking', { outbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `复核任务（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.checks.list({ ...q, outbound_no: no }),
              columns: CHECK_TASK_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'packing',
      label: '打包记录',
      permission: 'packing:view',
      hint: '按出库单号过滤的包裹记录（需先经出库单取得 outbound_no）',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/packing', { outbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `打包记录（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.packing.list({ ...q, outbound_no: no }),
              columns: PACKAGE_COLUMNS,
            }
          : null
      },
    },
    {
      key: 'shipment',
      label: '发货/物流',
      permission: 'shipment:view',
      hint: '按出库单号过滤的发货单（需先经出库单取得 outbound_no；物流跟踪同源 shipments）',
      to: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no ? target('/shipment', { outbound_no: no }) : null
      },
      list: (ctx) => {
        const no = pick(ctx, 'outbound_no')
        return no
          ? {
              title: `发货/物流（${no}）`,
              fetch: (q: PageQuery) => outboundTaskApi.shipments.list({ ...q, outbound_no: no }),
              columns: SHIPMENT_COLUMNS,
            }
          : null
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
      list: (ctx) => ({
        title: '实时库存（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.stock({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as StockQuery),
        columns: STOCK_COLUMNS,
      }),
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
      list: (ctx) => ({
        title: '实时库存（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.stock({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as StockQuery),
        columns: STOCK_COLUMNS,
      }),
    },
    {
      key: 'ledger',
      label: '库存流水',
      permission: 'inventory:ledger:view',
      hint: '本单所属仓库的出入库流水',
      to: (ctx) => target('/inventory/ledger', { warehouse_id: pick(ctx, 'warehouse_id') }),
      list: (ctx) => ({
        title: '库存流水（本仓）',
        fetch: (q: PageQuery) =>
          inventoryApi.ledger({ ...q, warehouse_id: pick(ctx, 'warehouse_id') } as LedgerQuery),
        columns: LEDGER_COLUMNS,
      }),
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
