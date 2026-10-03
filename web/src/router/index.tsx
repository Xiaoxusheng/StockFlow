import { Suspense, lazy } from 'react'
import { createBrowserRouter, Navigate, useLocation } from 'react-router'
import type { ReactNode } from 'react'
import { Spin } from 'antd'
import { MENU_TREE } from '@/config/menu'
import { useAuthStore } from '@/stores/auth'
import { PcLayout } from '@/layouts/PcLayout'
import { RouteError } from './RouteError'
import { ErrorPage } from '@/views/error/ErrorPage'
import { ModulePlaceholder } from '@/views/placeholder/ModulePlaceholder'

const DashboardPage = lazy(() => import('@/views/dashboard/DashboardPage'))
const StockListPage = lazy(() => import('@/views/inventory/StockListPage'))
const LedgerPage = lazy(() => import('@/views/inventory/LedgerPage'))
const AlertsPage = lazy(() => import('@/views/inventory/AlertsPage'))
const LocksPage = lazy(() => import('@/views/inventory/LocksPage'))
const AdjustmentsPage = lazy(() => import('@/views/inventory/AdjustmentsPage'))
const LoginPageLazy = lazy(() => import('@/views/login/LoginPage').then((m) => ({ default: m.LoginPage })))

// 基础资料（F6 基础资料 M1 契约域）
const ProductListPage = lazy(() => import('@/views/masterdata/ProductListPage'))
const SkuListPage = lazy(() => import('@/views/masterdata/SkuListPage'))
const CategoryListPage = lazy(() => import('@/views/masterdata/CategoryListPage'))
const UnitListPage = lazy(() => import('@/views/masterdata/UnitListPage'))
const SupplierListPage = lazy(() => import('@/views/masterdata/SupplierListPage'))
const CustomerListPage = lazy(() => import('@/views/masterdata/CustomerListPage'))

// 仓库中心（F7 仓库中心 M1 契约域）
const WarehouseListPage = lazy(() => import('@/views/warehouse/WarehouseListPage'))
const ZoneListPage = lazy(() => import('@/views/warehouse/ZoneListPage'))
const ShelfListPage = lazy(() => import('@/views/warehouse/ShelfListPage'))
const BinListPage = lazy(() => import('@/views/warehouse/BinListPage'))
const WarehouseMapPage = lazy(() => import('@/views/warehouse/WarehouseMapPage'))

// 系统管理（F13 系统管理 M1 契约域）
const UserListPage = lazy(() => import('@/views/system/UserListPage'))
const RoleListPage = lazy(() => import('@/views/system/RoleListPage'))
const PermissionListPage = lazy(() => import('@/views/system/PermissionListPage'))
const DepartmentPage = lazy(() => import('@/views/system/DepartmentPage'))

// 仓储作业 / 采购（M2+ 骨架，后端契约冻结前呈统一错误态）
const InboundPage = lazy(() => import('@/views/inbound/InboundPage'))
const OutboundPage = lazy(() => import('@/views/outbound/OutboundPage'))
const PurchaseListPage = lazy(() => import('@/views/purchase/PurchaseListPage'))

// 任务中心（F8：工作台四计数 + 我的任务；后端任务域未交付，呈统一错误态）
const WorkbenchPage = lazy(() => import('@/views/workbench/WorkbenchPage'))
const MyTasksPage = lazy(() => import('@/views/task/MyTasksPage'))

// 库存中心余量（F6：批次/序列号/转移/追溯；端点 M1 不交付，呈统一错误态）
const BatchListPage = lazy(() => import('@/views/inventory/BatchListPage'))
const SerialListPage = lazy(() => import('@/views/inventory/SerialListPage'))
const TransferPage = lazy(() => import('@/views/inventory/TransferPage'))
const TracePage = lazy(() => import('@/views/inventory/TracePage'))

// 出库作业（P0：拣货/复核/打包/发货；后端 outbound 域未交付，呈统一错误态）
const PickingPage = lazy(() => import('@/views/outbound/PickingPage'))
const CheckingPage = lazy(() => import('@/views/outbound/CheckingPage'))
const PackingPage = lazy(() => import('@/views/outbound/PackingPage'))
const ShipmentPage = lazy(() => import('@/views/outbound/ShipmentPage'))

// 单据域（P0：销售三单 + 采购收货/退货；契约 M2 冻结，呈统一错误态）
const SalesOrderListPage = lazy(() => import('@/views/sales/SalesOrderListPage'))
const SalesOutboundListPage = lazy(() => import('@/views/sales/SalesOutboundListPage'))
const SalesReturnListPage = lazy(() => import('@/views/sales/SalesReturnListPage'))
// 组5（第四批）销售订单新建（/sales/new）与详情（/sales/:id），无菜单路径
const SalesOrderCreatePage = lazy(() => import('@/views/sales/SalesOrderCreatePage'))
const SalesOrderDetailPage = lazy(() => import('@/views/sales/SalesOrderDetailPage'))
const ReceiptListPage = lazy(() => import('@/views/purchase/ReceiptListPage'))
const PurchaseReturnListPage = lazy(() => import('@/views/purchase/PurchaseReturnListPage'))

// 单据详情（入库/出库/采购订单动态段；后端单据域未交付，呈统一错误态）
const InboundDetailPage = lazy(() => import('@/views/inbound/InboundDetailPage'))
const OutboundDetailPage = lazy(() => import('@/views/outbound/OutboundDetailPage'))
const PurchaseOrderDetailPage = lazy(() => import('@/views/purchase/PurchaseOrderDetailPage'))

// 质量中心（前端先行契约：后端质量域未交付，呈统一错误态）
const QualityInspectionListPage = lazy(() => import('@/views/quality/QualityInspectionListPage'))
const NonconformingListPage = lazy(() => import('@/views/quality/NonconformingListPage'))
const QualityTracePage = lazy(() => import('@/views/quality/QualityTracePage'))

// 调拨单（仓库中心，/api/transfers 单据流，与 /inventory/transfers 库存转移作业语义区分）+ 异常中心
const TransferListPage = lazy(() => import('@/views/warehouse/TransferListPage'))
const ExceptionCenterPage = lazy(() => import('@/views/exception/ExceptionCenterPage'))

// 库存中心（库存分析 + SKU 库存详情动态段；分析端点 M2+，详情页部分页签呈统一错误态）
const AnalyticsPage = lazy(() => import('@/views/inventory/AnalyticsPage'))
const StockDetailPage = lazy(() => import('@/views/inventory/StockDetailPage'))

// 盘点中心（F9；后端盘点域未交付，呈统一错误态）
const CountTaskListPage = lazy(() => import('@/views/count/CountTaskListPage'))
const CountDetailPage = lazy(() => import('@/views/count/CountDetailPage'))
// 组5（第四批）盘点新建（/counts/new），无菜单路径
const CountCreatePage = lazy(() => import('@/views/count/CountCreatePage'))

// 数据中心（F11：Excel 导入/导出；后端数据域未交付，呈统一错误态）
const ImportWizardPage = lazy(() => import('@/views/data/ImportWizardPage'))
const ExportTaskPage = lazy(() => import('@/views/data/ExportTaskPage'))

// 报表中心（前端先行契约：后端报表域阶段 17–18 交付，目录呈统一错误态；页面为已交付真实页面聚合入口）
const ReportsPage = lazy(() => import('@/views/reports/ReportsPage'))

// 打印中心（F12 前端先行契约；react-to-print 独立渲染层 + 独立预览页）
const PrintingCenterPage = lazy(() => import('@/views/printing/PrintingCenterPage'))
const PrintPreviewPage = lazy(() => import('@/views/printing/PrintPreviewPage'))
// 文件中心（F11 余量：附件上传/预览/下载/删除）
const FileCenterPage = lazy(() => import('@/views/data/FileCenterPage'))
// 设备中心（四类型列表复用同一组件，deviceType 由 pathname 解析，见 api/device.ts DEVICE_TYPE_BY_PATH）
const DeviceListPage = lazy(() => import('@/views/device/DeviceListPage'))
const DeviceCreatePage = lazy(() => import('@/views/device/DeviceCreatePage'))
const DeviceDetailPage = lazy(() => import('@/views/device/DeviceDetailPage'))
// 系统管理余量（日志/定时任务/系统配置/系统监控）
const LogPage = lazy(() => import('@/views/system/LogPage'))
const JobPage = lazy(() => import('@/views/system/JobPage'))
const SettingsPage = lazy(() => import('@/views/system/SettingsPage'))
const MonitorPage = lazy(() => import('@/views/system/MonitorPage'))

/** 已有真实页面的路由；其余菜单路径统一落到 ModulePlaceholder */
const IMPLEMENTED_PATHS = new Set([
  '/dashboard',
  // 任务中心（F8）
  '/workbench',
  '/tasks',
  '/inventory/stock',
  '/inventory/ledger',
  '/inventory/alerts',
  '/inventory/locks',
  '/inventory/adjustments',
  // 库存中心余量（F6）
  '/inventory/batches',
  '/inventory/serials',
  '/inventory/transfers',
  '/inventory/trace',
  // 库存分析（F10）
  '/inventory/analytics',
  // 库存详情 /inbound/:id、/outbound/:id、/purchases/:id、/counts/:id、/sales/:id、/devices/:id、/inventory/stock/:skuCode
  // 为动态段路由，无菜单路径，不参与占位匹配，不加入本 Set
  // /sales/new、/counts/new、/devices/new、/data/printing/preview 为新建页/预览页的无菜单静态路径，
  // 同样不参与占位匹配、不加入本 Set
  // 基础资料
  '/products',
  '/skus',
  '/categories',
  '/units',
  '/suppliers',
  '/customers',
  // 仓库中心
  '/warehouses',
  '/zones',
  '/shelves',
  '/bins',
  '/warehouse-map',
  // 调拨单（仓库中心；与库存转移 /inventory/transfers 语义区分）
  '/transfers',
  // 系统管理
  '/system/users',
  '/system/roles',
  '/system/permissions',
  '/system/departments',
  // 仓储作业 / 采购
  '/inbound',
  '/outbound',
  '/purchases',
  // 异常中心
  '/exceptions',
  // 出库作业（P0）
  '/picking',
  '/checking',
  '/packing',
  '/shipment',
  // 采购收货 / 退货（P0 单据域）
  '/purchases/receipts',
  '/purchases/returns',
  // 销售三单（P0 单据域）
  '/sales',
  '/sales/outbounds',
  '/sales/returns',
  // 质量中心（前端先行契约）
  '/quality/inspections',
  '/quality/nonconforming',
  '/quality/trace',
  // 盘点中心（F9）
  '/counts',
  // 报表中心（前端先行契约）
  '/reports',
  // 数据中心（F11：Excel 导入/导出）
  '/data/imports',
  '/data/exports',
  // 打印中心（F12）
  '/data/printing',
  // 文件中心（F11 余量）
  '/data/files',
  // 设备中心（F13：四类型列表）
  '/devices/scanners',
  '/devices/pda',
  '/devices/pads',
  '/devices/printers',
  // 系统管理余量（日志/定时任务/系统配置/系统监控）
  '/system/logs',
  '/system/jobs',
  '/system/settings',
  '/system/monitor',
])

function menuPaths(): string[] {
  return MENU_TREE.flatMap((group) => [group, ...(group.children ?? [])]).map((item) => item.path)
}

function placeholderRoutes() {
  return menuPaths()
    .filter((path) => !IMPLEMENTED_PATHS.has(path))
    .map((path) => ({
      path: path.slice(1),
      element: <ModulePlaceholder />,
    }))
}

function RequireAuth({ children }: { children: ReactNode }) {
  const token = useAuthStore((s) => s.token)
  const location = useLocation()
  if (!token) {
    const redirect = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?redirect=${redirect}`} replace />
  }
  return children
}

function LazyPage({ children }: { children: ReactNode }) {
  return (
    <Suspense
      fallback={
        <div className="sf-page" style={{ textAlign: 'center', paddingTop: 80 }}>
          <Spin />
        </div>
      }
    >
      {children}
    </Suspense>
  )
}

export const router = createBrowserRouter([
  { path: '/login', element: <LazyPage><LoginPageLazy /></LazyPage> },
  {
    path: '/',
    element: (
      <RequireAuth>
        <PcLayout />
      </RequireAuth>
    ),
    errorElement: <RouteError />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      {
        path: 'dashboard',
        element: <LazyPage><DashboardPage /></LazyPage>,
      },
      // 任务中心（F8）
      {
        path: 'workbench',
        element: <LazyPage><WorkbenchPage /></LazyPage>,
      },
      {
        path: 'tasks',
        element: <LazyPage><MyTasksPage /></LazyPage>,
      },
      {
        path: 'inventory/stock',
        element: <LazyPage><StockListPage /></LazyPage>,
      },
      // SKU 库存详情（frontend.md §10.2：列表点击 SKU 进入；动态段无菜单，与静态段共存静态优先）
      {
        path: 'inventory/stock/:skuCode',
        element: <LazyPage><StockDetailPage /></LazyPage>,
      },
      {
        path: 'inventory/ledger',
        element: <LazyPage><LedgerPage /></LazyPage>,
      },
      {
        path: 'inventory/alerts',
        element: <LazyPage><AlertsPage /></LazyPage>,
      },
      {
        path: 'inventory/locks',
        element: <LazyPage><LocksPage /></LazyPage>,
      },
      {
        path: 'inventory/adjustments',
        element: <LazyPage><AdjustmentsPage /></LazyPage>,
      },
      // 库存中心余量（F6：批次/序列号/转移/追溯）
      {
        path: 'inventory/batches',
        element: <LazyPage><BatchListPage /></LazyPage>,
      },
      {
        path: 'inventory/serials',
        element: <LazyPage><SerialListPage /></LazyPage>,
      },
      {
        path: 'inventory/transfers',
        element: <LazyPage><TransferPage /></LazyPage>,
      },
      {
        path: 'inventory/trace',
        element: <LazyPage><TracePage /></LazyPage>,
      },
      // 库存分析（F10：§10.1 口径；分析端点后端 M2+ 交付，当前呈统一错误态）
      {
        path: 'inventory/analytics',
        element: <LazyPage><AnalyticsPage /></LazyPage>,
      },
      // 仓储作业（M2+ 骨架）
      {
        path: 'inbound',
        element: <LazyPage><InboundPage /></LazyPage>,
      },
      // 入库单详情（列表「详情」列进入；动态段无菜单）
      {
        path: 'inbound/:id',
        element: <LazyPage><InboundDetailPage /></LazyPage>,
      },
      {
        path: 'outbound',
        element: <LazyPage><OutboundPage /></LazyPage>,
      },
      // 出库单详情（动态段无菜单）
      {
        path: 'outbound/:id',
        element: <LazyPage><OutboundDetailPage /></LazyPage>,
      },
      // 异常中心（business-flow.md §11.2 九类异常；前端先行契约）
      {
        path: 'exceptions',
        element: <LazyPage><ExceptionCenterPage /></LazyPage>,
      },
      // 出库作业（P0：拣货/复核/打包/发货）
      {
        path: 'picking',
        element: <LazyPage><PickingPage /></LazyPage>,
      },
      {
        path: 'checking',
        element: <LazyPage><CheckingPage /></LazyPage>,
      },
      {
        path: 'packing',
        element: <LazyPage><PackingPage /></LazyPage>,
      },
      {
        path: 'shipment',
        element: <LazyPage><ShipmentPage /></LazyPage>,
      },
      // 仓库中心
      {
        path: 'warehouses',
        element: <LazyPage><WarehouseListPage /></LazyPage>,
      },
      {
        path: 'zones',
        element: <LazyPage><ZoneListPage /></LazyPage>,
      },
      {
        path: 'shelves',
        element: <LazyPage><ShelfListPage /></LazyPage>,
      },
      {
        path: 'bins',
        element: <LazyPage><BinListPage /></LazyPage>,
      },
      {
        path: 'warehouse-map',
        element: <LazyPage><WarehouseMapPage /></LazyPage>,
      },
      // 调拨单（仓库中心菜单组；两维度 + 7 态状态机，与库存转移 /inventory/transfers 语义区分）
      {
        path: 'transfers',
        element: <LazyPage><TransferListPage /></LazyPage>,
      },
      // 采购
      {
        path: 'purchases',
        element: <LazyPage><PurchaseListPage /></LazyPage>,
      },
      // 采购订单详情（Timeline 含部分收货进度；动态段无菜单，与 receipts/returns 静态段共存静态优先）
      {
        path: 'purchases/:id',
        element: <LazyPage><PurchaseOrderDetailPage /></LazyPage>,
      },
      // 采购收货 / 退货（P0 单据域）
      {
        path: 'purchases/receipts',
        element: <LazyPage><ReceiptListPage /></LazyPage>,
      },
      {
        path: 'purchases/returns',
        element: <LazyPage><PurchaseReturnListPage /></LazyPage>,
      },
      // 销售三单（P0 单据域）
      {
        path: 'sales',
        element: <LazyPage><SalesOrderListPage /></LazyPage>,
      },
      // 销售订单新建（组5；无菜单路径，静态段先于动态段）
      {
        path: 'sales/new',
        element: <LazyPage><SalesOrderCreatePage /></LazyPage>,
      },
      {
        path: 'sales/outbounds',
        element: <LazyPage><SalesOutboundListPage /></LazyPage>,
      },
      {
        path: 'sales/returns',
        element: <LazyPage><SalesReturnListPage /></LazyPage>,
      },
      // 销售订单详情（组5；动态段无菜单，殿后注册）
      {
        path: 'sales/:id',
        element: <LazyPage><SalesOrderDetailPage /></LazyPage>,
      },
      // 基础资料
      {
        path: 'products',
        element: <LazyPage><ProductListPage /></LazyPage>,
      },
      {
        path: 'skus',
        element: <LazyPage><SkuListPage /></LazyPage>,
      },
      {
        path: 'categories',
        element: <LazyPage><CategoryListPage /></LazyPage>,
      },
      {
        path: 'units',
        element: <LazyPage><UnitListPage /></LazyPage>,
      },
      {
        path: 'suppliers',
        element: <LazyPage><SupplierListPage /></LazyPage>,
      },
      {
        path: 'customers',
        element: <LazyPage><CustomerListPage /></LazyPage>,
      },
      // 质量中心（前端先行契约：后端质量域未交付，呈统一错误态）
      {
        path: 'quality/inspections',
        element: <LazyPage><QualityInspectionListPage /></LazyPage>,
      },
      {
        path: 'quality/nonconforming',
        element: <LazyPage><NonconformingListPage /></LazyPage>,
      },
      {
        path: 'quality/trace',
        element: <LazyPage><QualityTracePage /></LazyPage>,
      },
      // 盘点中心（F9：七态状态机 + 实盘登记；详情为动态段无菜单）
      {
        path: 'counts',
        element: <LazyPage><CountTaskListPage /></LazyPage>,
      },
      // 盘点新建（组5；无菜单路径，静态段先于动态段）
      {
        path: 'counts/new',
        element: <LazyPage><CountCreatePage /></LazyPage>,
      },
      {
        path: 'counts/:id',
        element: <LazyPage><CountDetailPage /></LazyPage>,
      },
      // 报表中心（前端先行契约：报表域未交付呈统一错误态；页面聚合已交付真实入口）
      {
        path: 'reports',
        element: <LazyPage><ReportsPage /></LazyPage>,
      },
      // 数据中心（F11：Excel 导入/导出 + 打印中心 + 文件中心）
      {
        path: 'data/imports',
        element: <LazyPage><ImportWizardPage /></LazyPage>,
      },
      {
        path: 'data/exports',
        element: <LazyPage><ExportTaskPage /></LazyPage>,
      },
      // 打印中心（F12：§13 打印前端，react-to-print 独立渲染层，禁止 window.print）
      {
        path: 'data/printing',
        element: <LazyPage><PrintingCenterPage /></LazyPage>,
      },
      // 打印预览（无菜单静态路径，独立 Preview 页：缩放/翻页/打印/下载 PDF）
      {
        path: 'data/printing/preview',
        element: <LazyPage><PrintPreviewPage /></LazyPage>,
      },
      // 文件中心（F11 余量：附件上传/预览/下载/删除）
      {
        path: 'data/files',
        element: <LazyPage><FileCenterPage /></LazyPage>,
      },
      // 设备中心（F13：四类型列表复用 DeviceListPage，deviceType 按 pathname 解析）
      {
        path: 'devices/scanners',
        element: <LazyPage><DeviceListPage /></LazyPage>,
      },
      {
        path: 'devices/pda',
        element: <LazyPage><DeviceListPage /></LazyPage>,
      },
      {
        path: 'devices/pads',
        element: <LazyPage><DeviceListPage /></LazyPage>,
      },
      {
        path: 'devices/printers',
        element: <LazyPage><DeviceListPage /></LazyPage>,
      },
      // 设备新建（无菜单路径，静态段先于动态段）
      {
        path: 'devices/new',
        element: <LazyPage><DeviceCreatePage /></LazyPage>,
      },
      // 设备详情（动态段无菜单，殿后注册）
      {
        path: 'devices/:id',
        element: <LazyPage><DeviceDetailPage /></LazyPage>,
      },
      // 系统管理
      {
        path: 'system/users',
        element: <LazyPage><UserListPage /></LazyPage>,
      },
      {
        path: 'system/roles',
        element: <LazyPage><RoleListPage /></LazyPage>,
      },
      {
        path: 'system/permissions',
        element: <LazyPage><PermissionListPage /></LazyPage>,
      },
      {
        path: 'system/departments',
        element: <LazyPage><DepartmentPage /></LazyPage>,
      },
      // 系统管理余量（日志/定时任务/系统配置/系统监控；/system/notifications 仍占位）
      {
        path: 'system/logs',
        element: <LazyPage><LogPage /></LazyPage>,
      },
      {
        path: 'system/jobs',
        element: <LazyPage><JobPage /></LazyPage>,
      },
      {
        path: 'system/settings',
        element: <LazyPage><SettingsPage /></LazyPage>,
      },
      {
        path: 'system/monitor',
        element: <LazyPage><MonitorPage /></LazyPage>,
      },
      ...placeholderRoutes(),
    ],
  },
  {
    path: '/403',
    element: (
      <ErrorPage
        status="403"
        title="没有权限"
        description="你无权访问该页面，请联系管理员开通相应权限"
      />
    ),
  },
  {
    path: '/500',
    element: (
      <ErrorPage
        status="500"
        title="服务器开小差了"
        description="服务暂时不可用，请稍后重试；若持续出现请联系管理员"
      />
    ),
  },
  {
    path: '*',
    element: (
      <ErrorPage
        status="404"
        title="页面不存在"
        description="请检查地址是否正确，或从菜单进入相应页面"
      />
    ),
  },
])
