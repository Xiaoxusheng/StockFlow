import type { SearchHitItem } from '@/api/search'

// ---------- 全局搜索跳转映射（计划 §2.1 / api.md §9 效率层节 B2 补录） ----------
// 后端不编码前端路由，返回 items.navigation:{kind,id} 稳定实体标识；本文件承担
// kind+id → 前端路由映射。映射经 canAccess 权限码校验（permission.md §5：前端权限是
// 体验优化，后端必须校验），无权限项置灰不可点。权限码取 MENU_TREE 对应菜单项编码
// （config/menu.tsx 同源），不另造码。
// 回退链：navigation.kind 未知（前后端版本漂移）→ doc 单号前缀映射（PO/IN/SO/OUT/
// TRF/CT/EX）→ type 直映表。前缀映射保留为回退是 api.md 披露口径。

export interface SearchTargetRoute {
  path: string
  /** 菜单权限点编码（三段冻结码，types/permission.ts canAccess 消费） */
  permission?: string
}

/** kind 路由项：list=列表页；detail=详情路由模板（:id 以 navigation.id 实例化） */
interface KindTarget {
  list: string
  detail?: string
  permission: string
}

/**
 * navigation.kind 权威映射（api.md B2 补录枚举一一对应；detail 路由以
 * router/index.tsx 实有动态段为准：purchases/:id、inbound/:id、sales/:id、
 * outbound/:id、counts/:id——transfer_order/exception 无 :id 详情路由，直达列表页）。
 */
const KIND_TARGETS: Record<string, KindTarget> = {
  sku: { list: '/skus', permission: 'sku:view' },
  product: { list: '/products', permission: 'product:view' },
  barcode: { list: '/skus', permission: 'sku:view' },
  batch: { list: '/inventory/batches', permission: 'inventory:batch:view' },
  serial: { list: '/inventory/serials', permission: 'inventory:serial:view' },
  bin: { list: '/bins', permission: 'warehouse:bin:view' },
  warehouse: { list: '/warehouses', permission: 'warehouse:view' },
  customer: { list: '/customers', permission: 'customer:view' },
  supplier: { list: '/suppliers', permission: 'supplier:view' },
  shipment: { list: '/shipment', permission: 'shipment:view' },
  purchase_order: { list: '/purchases', detail: '/purchases/:id', permission: 'purchase:view' },
  inbound_order: { list: '/inbound', detail: '/inbound/:id', permission: 'inbound:view' },
  sales_order: { list: '/sales', detail: '/sales/:id', permission: 'sales:view' },
  outbound_order: { list: '/outbound', detail: '/outbound/:id', permission: 'outbound:view' },
  transfer_order: { list: '/transfers', permission: 'transfer:view' },
  count_order: { list: '/counts', detail: '/counts/:id', permission: 'count:view' },
  exception: { list: '/exceptions', permission: 'exception:view' },
}

/**
 * 回退：doc 组七类单号混合，按单号前缀映射路由（后端按 ILIKE 前缀优先返回编码）。
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

/** 回退：type → 路由 + 权限码映射表（与 KIND_TARGETS 普通 type 同源同值） */
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
 * 搜索命中项 → 跳转路由（navigation.kind 权威优先：有详情路由的单据直达详情，
 * 其余直达列表页）。未知 kind 或无法判定路由时回退 type/前缀映射，仍失败返回 null
 * （调用方不渲染跳转态）。无详情路由的实体（sku/batch/serial/bin/warehouse 等）
 * 直达列表页——命中参数一期不带入列表 URL，列表页可自行按关键词检索。
 */
export function resolveSearchTarget(item: SearchHitItem): SearchTargetRoute | null {
  const navigation = item.navigation
  if (navigation?.kind) {
    const target = KIND_TARGETS[navigation.kind]
    if (target) {
      if (!target.detail) return { path: target.list, permission: target.permission }
      return {
        path: target.detail.replace(':id', encodeURIComponent(navigation.id)),
        permission: target.permission,
      }
    }
  }
  // 回退 1：doc 单号前缀（navigation 缺失/未知 kind 时的旧映射，api.md 披露保留）
  if (item.type === 'doc') {
    const code = item.code.trim().toUpperCase()
    const hit = DOC_PREFIX_TARGETS.find((t) => code.startsWith(t.prefix))
    return hit ? { path: hit.path, permission: hit.permission } : null
  }
  // 回退 2：type 直映表
  const target = TYPE_TARGETS[item.type]
  return target ? { path: target.path, permission: target.permission } : null
}
