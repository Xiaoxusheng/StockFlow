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
const LoginPageLazy = lazy(() => import('@/views/login/LoginPage').then((m) => ({ default: m.LoginPage })))

/** 已有真实页面的路由；其余菜单路径统一落到 ModulePlaceholder */
const IMPLEMENTED_PATHS = new Set(['/dashboard', '/inventory/stock', '/inventory/ledger', '/inventory/alerts'])

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
