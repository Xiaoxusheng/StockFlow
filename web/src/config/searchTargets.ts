import type { SearchHitItem } from '@/api/search'

// ---------- 全局搜索跳转映射（计划 §2.1） ----------
// 后端不编码前端路由，type+payload→路由由前端映射；映射经 canAccess 权限码校验
// （permission.md §5：前端权限是体验优化，后端必须校验），无权限项置灰不可点。
// 权限码取 MENU_TREE 对应菜单项编码（config/menu.tsx 同源），不另造码。

export interface SearchTargetRoute {
  path: string
  /** 菜单权限点编码（三段冻结码，types/permission.ts canAccess 消费） */
  permission?: string
}

/**
 * 单据类型（doc 组为采购/入库/销售/出库/调拨/盘点/异常七类单号混合，
 * 后端按 ILIKE 前缀优先返回编码——按单号前缀映射路由。
 * 前缀枚举与 internal/search/repository.go 单号列一一对应：
 * PO=purchase_orders / IN=inbound_orders / SO=sales_orders / OUT=outbound_orders /
 * TRF=transfer_orders / CT=count_orders / EX=exceptions。
 */
const DOC_PREFIX_TARGETS: ReadonlyArray<{ prefix: string; path: string; permission: string }> = [
  { prefix: 'PO', path: '/purchases', permission: 'purchase:view' },
  { prefix: 'IN', path: '/inbound', permission: 'inbound:view' },
  { prefix: 'SO', path: '/sales', permission: 'sales:view' },
  { prefix: 'OUT', path: '/outbound', permission: 'outbound:view' },
  { prefix: 'TRF', path: '/transfers', permission: 'transfer:view' },
  { prefix: 'CT', path: '/counts', permission: 'count:view' },
  { prefix: 'EX', path: '/exceptions', permission: 'exception:view' },
]

/** type → 路由 + 权限码映射表（权限码与 MENU_TREE 菜单项同源） */
const TYPE_TARGETS: Record<string, { path: string; permission: string }> = {
  sku: { path: '/skus', permission: 'sku:view' },
  product: { path: '/products', permission: 'product:view' },
  barcode: { path: '/skus', permission: 'sku:view' },
  batch: { path: '/inventory/batches', permission: 'inventory:batch:view' },
  serial: { path: '/inventory/serials', permission: 'inventory:serial:view' },
  bin: { path: '/bins', permission: 'warehouse:bin:view' },
  warehouse: { path: '/warehouses', permission: 'warehouse:view' },
  customer: { path: '/customers', permission: 'customer:view' },
  supplier: { path: '/suppliers', permission: 'supplier:view' },
  logistics: { path: '/shipment', permission: 'shipment:view' },
}

/**
 * 搜索命中项 → 跳转路由。未知 type 或无法判定路由返回 null（调用方不渲染跳转态）。
 * 列表页无详情动态段的类型直达列表页（/skus、/bins 等无 :id 详情路由，router 冻结不动）；
 * 命中参数（code/id）一期不带入 URL，列表页可自行按关键词检索。
 */
export function resolveSearchTarget(item: SearchHitItem): SearchTargetRoute | null {
  if (item.type === 'doc') {
    const code = item.code.trim().toUpperCase()
    const hit = DOC_PREFIX_TARGETS.find((t) => code.startsWith(t.prefix))
    return hit ? { path: hit.path, permission: hit.permission } : null
  }
  const target = TYPE_TARGETS[item.type]
  return target ? { path: target.path, permission: target.permission } : null
}
