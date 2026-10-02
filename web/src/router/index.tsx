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

/** 已有真实页面的路由；其余菜单路径统一落到 ModulePlaceholder */
const IMPLEMENTED_PATHS = new Set([
  '/dashboard',
  '/inventory/stock',
  '/inventory/ledger',
  '/inventory/alerts',
  '/inventory/locks',
  '/inventory/adjustments',
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
  // 系统管理
  '/system/users',
  '/system/roles',
  '/system/permissions',
  '/system/departments',
  // 仓储作业 / 采购
  '/inbound',
  '/outbound',
  '/purchases',
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
      {
        path: 'inventory/stock',
        element: <LazyPage><StockListPage /></LazyPage>,
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
      // 仓储作业（M2+ 骨架）
      {
        path: 'inbound',
        element: <LazyPage><InboundPage /></LazyPage>,
      },
      {
        path: 'outbound',
        element: <LazyPage><OutboundPage /></LazyPage>,
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
      // 采购
      {
        path: 'purchases',
        element: <LazyPage><PurchaseListPage /></LazyPage>,
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
