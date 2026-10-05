import type { ReactNode } from 'react'
import {
  AppstoreOutlined,
  AuditOutlined,
  BarChartOutlined,
  CarryOutOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  FileSyncOutlined,
  HomeOutlined,
  InboxOutlined,
  MobileOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  ShoppingOutlined,
  ShoppingCartOutlined,
} from '@ant-design/icons'

/**
 * Sidebar 菜单树（frontend.md §4.2，唯一数据源）
 * 同时驱动：侧边栏渲染、面包屑、页面标题、占位页。
 * permission：权限点编码（permission.md §5），后端接入后用于菜单过滤。
 */
export interface MenuItem {
  path: string
  label: string
  icon?: ReactNode
  permission?: string
  children?: MenuItem[]
}

export const MENU_TREE: MenuItem[] = [
  { path: '/dashboard', label: 'Dashboard', icon: <DashboardOutlined /> },
  // 我的工作台：个人汇总页无资源域语义，与 Dashboard 同性质不带码（canAccess 对缺省码放行，
  // types/permission.ts:75）。原 workbench:view 在后端权限清单零命中（grep internal/ db/ 零输出，
  // M1–M3 冻结码无 workbench 域），fail-closed 会导致非超管永远看不到入口；任务域码立项前
  // 端点未交付时页面呈统一错误态（requirements.md §10 预期行为）。
  { path: '/workbench', label: '我的工作台', icon: <CarryOutOutlined /> },
  {
    path: '/warehouse-ops',
    label: '仓储中心',
    icon: <InboxOutlined />,
    children: [
      // 我的任务（2026-10-05 平台批：GET /api/tasks 交付并挂 inventory:inventory:list——
      // internal/reports/routes.go:115；菜单码 task:view 后端零命中 + DOMAIN_BOUND_RESOURCES
      // 限定，端点交付后仍 fail-closed 恒隐身，故归一为 inventory:stock:view 经
      // RESOURCE_ALIASES stock→['stock','inventory'] 命中（types/permission.ts），与
      // 库存分析/仓库分析同款修法）
      { path: '/tasks', label: '我的任务', permission: 'inventory:stock:view' },
      { path: '/inbound', label: '入库管理', permission: 'inbound:view' },
      // 入/出库分析菜单码 reports:report:view：数据端点挂 reports:report:read
      // （internal/reports/routes.go inbound-stats/outbound-stats），matchBackendPermission
      // 取末两段 report:view 命中后端冻结码 reports:report:list/read（permissions.go:343-344，
      // :report: 资源段全局唯一）任一动作即放行——菜单可见 ⟺ 数据可达；
      // 域码（inbound:view 等）无报表读权限角色进页 403、造 analytics 假码则恒隐身。
      { path: '/inbound/analytics', label: '入库分析', permission: 'reports:report:view' },
      { path: '/outbound', label: '出库管理', permission: 'outbound:view' },
      { path: '/outbound/analytics', label: '出库分析', permission: 'reports:report:view' },
      { path: '/picking', label: '拣货管理', permission: 'picking:view' },
      { path: '/checking', label: '复核管理', permission: 'checking:view' },
      { path: '/packing', label: '打包管理', permission: 'packing:view' },
      { path: '/shipment', label: '发货管理', permission: 'shipment:view' },
      { path: '/exceptions', label: '异常中心', permission: 'exception:view' },
    ],
  },
  {
    path: '/inventory-center',
    label: '库存中心',
    icon: <DatabaseOutlined />,
    children: [
      { path: '/inventory/stock', label: '实时库存', permission: 'inventory:stock:view' },
      { path: '/inventory/ledger', label: '库存流水', permission: 'inventory:ledger:view' },
      { path: '/inventory/locks', label: '库存锁定', permission: 'inventory:lock:view' },
      { path: '/inventory/batches', label: '批次库存', permission: 'inventory:batch:view' },
      { path: '/inventory/serials', label: '序列号', permission: 'inventory:serial:view' },
      { path: '/inventory/adjustments', label: '库存调整', permission: 'inventory:adjustment:view' },
      { path: '/inventory/transfers', label: '库存转移', permission: 'inventory:transfer:view' },
      // 库存预警菜单码归一为 inventory:stock:view（matchBackendPermission 经 RESOURCE_ALIASES
      // stock→['stock','inventory'] 命中后端冻结码 inventory:inventory:list）——端点
      // GET /api/inventory/alerts 自 d314103 起挂 inventory:inventory:list（internal/reports/
      // routes.go:56，库存域读口径裁决），与实时库存（:55）同码同源：凡可见实时库存者即可见
      // 预警且不会 403。原 inventory:alert:view 资源段 alert 在后端权限清单零命中
      // （grep -rn ':alert:' internal/ db/ 零输出），非超管用户看不到菜单入口。
      { path: '/inventory/alerts', label: '库存预警', permission: 'inventory:stock:view' },
      { path: '/inventory/trace', label: '库存追溯', permission: 'inventory:trace:view' },
      // 库存分析菜单码归一为 inventory:stock:view（同上方库存预警修法）：原
      // inventory:analytics:view 资源段 analytics 在后端冻结权限码零命中
      // （grep -rn "analytics:view|inventory:analytics" internal/ db/ 零输出），
      // matchBackendPermission 按末两段匹配永不命中 → 非超管恒隐身（fail-closed）；
      // /inventory/analytics 页三数据端点挂 inventory:inventory:list（internal/reports/
      // routes.go:63-67），stock 经 RESOURCE_ALIASES 命中，与实时库存同码同源。
      { path: '/inventory/analytics', label: '库存分析', permission: 'inventory:stock:view' },
    ],
  },
  {
    path: '/warehouse-center',
    label: '仓库中心',
    icon: <HomeOutlined />,
    children: [
      { path: '/warehouses', label: '仓库', permission: 'warehouse:view' },
      // 仓库分析菜单码 inventory:stock:view：数据端点 /api/reports/warehouse-stock 挂
      // inventory:inventory:list（internal/reports/routes.go:60），与实时库存/库存预警同码同源。
      { path: '/warehouses/analytics', label: '仓库分析', permission: 'inventory:stock:view' },
      { path: '/zones', label: '库区', permission: 'warehouse:zone:view' },
      { path: '/shelves', label: '货架', permission: 'warehouse:shelf:view' },
      { path: '/bins', label: '库位', permission: 'warehouse:bin:view' },
      { path: '/warehouse-map', label: '库位地图', permission: 'warehouse:map:view' },
      { path: '/transfers', label: '调拨', permission: 'transfer:view' },
    ],
  },
  {
    path: '/purchase-center',
    label: '采购中心',
    icon: <ShoppingCartOutlined />,
    children: [
      { path: '/purchases', label: '采购订单', permission: 'purchase:view' },
      { path: '/purchases/receipts', label: '收货', permission: 'purchase:receipt:view' },
      { path: '/purchases/returns', label: '采购退货', permission: 'purchase:return:view' },
      // 采购分析（分析卡片轮立项：/api/purchases/analytics/trend、supplier-rank、
      // status-composition 三端点挂 purchase:purchase:list——internal/reports/routes.go:103-105；
      // 菜单码 purchase:view 取末两段，持采购域任意动作可见，与组内既有条目同规则；
      // 页面消费 api/analytics.ts）
      { path: '/purchases/analytics', label: '采购分析', permission: 'purchase:view' },
    ],
  },
  {
    path: '/sales-center',
    label: '销售中心',
    icon: <ShoppingOutlined />,
    children: [
      { path: '/sales', label: '销售订单', permission: 'sales:view' },
      { path: '/sales/outbounds', label: '出库', permission: 'sales:outbound:view' },
      { path: '/sales/returns', label: '销售退货', permission: 'sales:return:view' },
      // 销售分析（同采购分析：三端点挂 sales:sales:list——internal/reports/routes.go:106-108；
      // 销售金额趋势为订单金额口径非流水估值，页面须标注——api.md §9 收口披露节③）
      { path: '/sales/analytics', label: '销售分析', permission: 'sales:view' },
    ],
  },
  {
    path: '/quality-center',
    label: '质量中心',
    icon: <SafetyCertificateOutlined />,
    children: [
      // 质量域菜单码归一为两段 quality:view（matchBackendPermission 取末两段：资源段 quality
      // 命中后端冻结码 purchase:quality:*，view 动作 = 持任意动作即放行）——
      // 原 quality:inspection:view 等三段码资源段 inspection/nonconforming/trace 在后端
      // 权限清单（internal/auth/permissions.go:163-167）不存在，非超管用户将看不到菜单入口
      { path: '/quality/inspections', label: '质检', permission: 'quality:view' },
      { path: '/quality/nonconforming', label: '不合格品', permission: 'quality:view' },
      { path: '/quality/trace', label: '质量追溯', permission: 'quality:view' },
    ],
  },
  { path: '/counts', label: '盘点中心', icon: <AuditOutlined />, permission: 'count:view' },
  {
    path: '/master-data',
    label: '基础资料',
    icon: <AppstoreOutlined />,
    children: [
      { path: '/products', label: '商品', permission: 'product:view' },
      { path: '/skus', label: 'SKU', permission: 'sku:view' },
      // 商品二维码中心（SFQR 闭环，qr-code.md §10：权限复用 SKU 码，不新增 qr:*）
      { path: '/qr-codes', label: '商品二维码', permission: 'sku:view' },
      { path: '/categories', label: '分类', permission: 'category:view' },
      { path: '/units', label: '单位', permission: 'unit:view' },
      { path: '/suppliers', label: '供应商', permission: 'supplier:view' },
      { path: '/customers', label: '客户', permission: 'customer:view' },
    ],
  },
  { path: '/reports', label: '报表中心', icon: <BarChartOutlined />, permission: 'report:view' },
  {
    path: '/data-center',
    label: '数据中心',
    icon: <FileSyncOutlined />,
    children: [
      { path: '/data/imports', label: 'Excel 导入', permission: 'data:import:view' },
      { path: '/data/exports', label: 'Excel 导出', permission: 'data:export:view' },
      { path: '/data/printing', label: '打印中心', permission: 'print:view' },
      { path: '/data/files', label: '文件中心', permission: 'file:view' },
    ],
  },
  {
    path: '/device-center',
    label: '设备中心',
    icon: <MobileOutlined />,
    children: [
      { path: '/devices/scanners', label: '扫码设备', permission: 'device:scanner:view' },
      { path: '/devices/pda', label: 'PDA 设备', permission: 'device:pda:view' },
      { path: '/devices/pads', label: 'Pad 设备', permission: 'device:pad:view' },
      { path: '/devices/printers', label: '打印设备', permission: 'device:printer:view' },
    ],
  },
  {
    path: '/system-center',
    label: '系统管理',
    icon: <SettingOutlined />,
    children: [
      { path: '/system/users', label: '用户', permission: 'system:user:view' },
      { path: '/system/roles', label: '角色', permission: 'system:role:view' },
      { path: '/system/permissions', label: '权限', permission: 'system:permission:view' },
      { path: '/system/departments', label: '部门', permission: 'system:department:view' },
      // 备份：system:backup:view 经 matchBackendPermission 命中后端冻结码 system:backup:list/
      // read/create（internal/auth/permissions.go:362-365，view 动作 = 持任意动作即放行）；
      // 通知为个人收件箱、认证即可用且无权限点（permissions.go:295-296），真实入口为顶栏
      // NotificationDrawer——原「系统管理-通知」菜单项已移除（path 无路由点入 404、
      // 权限码 system:notification:view 后端不存在非超管恒隐藏，双重假入口）。
      { path: '/system/backups', label: '备份', permission: 'system:backup:view' },
      { path: '/system/logs', label: '日志', permission: 'system:log:view' },
      { path: '/system/jobs', label: '定时任务', permission: 'system:job:view' },
      { path: '/system/settings', label: '系统配置', permission: 'system:config:view' },
      { path: '/system/monitor', label: '系统监控', permission: 'system:monitor:view' },
    ],
  },
]

export interface MenuTrail {
  group?: MenuItem
  page?: MenuItem
}

/** 根据路径解析「所属分组 + 页面」，供面包屑与占位页标题使用 */
export function resolveMenuTrail(pathname: string): MenuTrail {
  for (const group of MENU_TREE) {
    if (group.path === pathname) return { group, page: group }
    for (const child of group.children ?? []) {
      if (child.path === pathname) return { group, page: child }
    }
  }
  return {}
}

export function findMenuItem(pathname: string): MenuItem | undefined {
  return resolveMenuTrail(pathname).page
}
