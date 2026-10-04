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
  { path: '/workbench', label: '我的工作台', icon: <CarryOutOutlined />, permission: 'workbench:view' },
  {
    path: '/warehouse-ops',
    label: '仓储中心',
    icon: <InboxOutlined />,
    children: [
      { path: '/tasks', label: '我的任务', permission: 'task:view' },
      { path: '/inbound', label: '入库管理', permission: 'inbound:view' },
      { path: '/outbound', label: '出库管理', permission: 'outbound:view' },
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
      { path: '/inventory/alerts', label: '库存预警', permission: 'inventory:alert:view' },
      { path: '/inventory/trace', label: '库存追溯', permission: 'inventory:trace:view' },
      { path: '/inventory/analytics', label: '库存分析', permission: 'inventory:analytics:view' },
    ],
  },
  {
    path: '/warehouse-center',
    label: '仓库中心',
    icon: <HomeOutlined />,
    children: [
      { path: '/warehouses', label: '仓库', permission: 'warehouse:view' },
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
      { path: '/system/notifications', label: '通知', permission: 'system:notification:view' },
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
