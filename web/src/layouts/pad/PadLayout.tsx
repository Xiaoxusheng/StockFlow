import { useSyncExternalStore } from 'react'
import type { ReactNode } from 'react'
import { Button } from 'antd'
import {
  ArrowUpOutlined,
  ArrowsAltOutlined,
  CalculatorOutlined,
  DatabaseOutlined,
  HomeOutlined,
  InboxOutlined,
  LogoutOutlined,
  MoonOutlined,
  ProfileOutlined,
  SafetyCertificateOutlined,
  SunOutlined,
  TransactionOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { Outlet, useLocation, useNavigate } from 'react-router'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { authApi } from '@/api/auth'
import { usePadOrientation } from './usePadOrientation'
import './pad.css'

/**
 * /pad/* 十页快捷导航（frontend.md §20.1 定位）：
 * Header 下横滑 chip 为第一入口，首页宫格（§20.5）为第二入口，两者共用本配置。
 */
export const PAD_NAV_ITEMS: Array<{ path: string; label: string; icon: ReactNode }> = [
  { path: '/pad/home', label: '首页', icon: <HomeOutlined /> },
  { path: '/pad/tasks', label: '任务', icon: <ProfileOutlined /> },
  { path: '/pad/receive', label: '收货', icon: <InboxOutlined /> },
  { path: '/pad/quality', label: '质检', icon: <SafetyCertificateOutlined /> },
  { path: '/pad/putaway', label: '上架', icon: <ArrowUpOutlined /> },
  { path: '/pad/inventory', label: '库存', icon: <DatabaseOutlined /> },
  { path: '/pad/count', label: '盘点', icon: <CalculatorOutlined /> },
  { path: '/pad/stockmove', label: '移库', icon: <ArrowsAltOutlined /> },
  { path: '/pad/transfer', label: '调拨', icon: <TransactionOutlined /> },
  { path: '/pad/exception', label: '异常', icon: <WarningOutlined /> },
]

function useOnlineStatus(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      window.addEventListener('online', onChange)
      window.addEventListener('offline', onChange)
      return () => {
        window.removeEventListener('online', onChange)
        window.removeEventListener('offline', onChange)
      }
    },
    () => navigator.onLine,
    () => true,
  )
}

/**
 * Pad 端总布局（frontend.md §20，F14）：
 * usePadOrientation 根状态 + data-orientation 根属性 + PadHeader（端标识 / 当前仓库 /
 * 在线状态点 / 主题切换 / 退出）+ 10 页快捷导航条 + Outlet。
 * 页面组只经 <Outlet/> 消费本布局，不直接引 Header 内部。
 */
export function PadLayout() {
  const orientation = usePadOrientation()
  const location = useLocation()
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  const clearSession = useAuthStore((s) => s.clearSession)
  const { mode, toggleMode } = useThemeStore()
  const online = useOnlineStatus()

  // devices.md §6.3 设备绑定仓库自动取值：设备域未交付，本轮展示「默认仓库」文案，不做仓库切换
  const handleLogout = () => {
    clearSession()
    void authApi.logout().catch(() => {
      // 后端未接入时本地登出即可，不阻塞（与 PcLayout 同口径）
    })
    navigate('/login', { replace: true })
  }

  return (
    <div className="sf-pad-root" data-orientation={orientation}>
      <header className="sf-pad-header">
        <span className="sf-pad-header-brand">StockFlow Pad</span>
        <span
          className="sf-pad-header-warehouse"
          title="设备绑定仓库（devices.md §6.3）待设备域交付后自动取值"
        >
          <DatabaseOutlined />
          默认仓库
        </span>
        <span className="sf-pad-header-user">{user?.real_name ?? user?.username ?? '未登录'}</span>
        <span className="sf-pad-header-spacer" />
        <span
          className={`sf-pad-dot${online ? ' sf-pad-dot--online' : ''}`}
          aria-hidden
        />
        <span className="sf-pad-header-online">{online ? '在线' : '离线'}</span>
        <Button
          type="text"
          size="large"
          icon={mode === 'light' ? <MoonOutlined /> : <SunOutlined />}
          onClick={toggleMode}
          aria-label={mode === 'light' ? '切换深色模式' : '切换浅色模式'}
        />
        <Button
          type="text"
          size="large"
          danger
          icon={<LogoutOutlined />}
          onClick={handleLogout}
          aria-label="退出登录"
        />
      </header>

      <nav className="sf-pad-navbar" aria-label="Pad 快捷导航">
        {PAD_NAV_ITEMS.map((item) => {
          const active = location.pathname === item.path || location.pathname.startsWith(`${item.path}/`)
          return (
            <Button
              key={item.path}
              type={active ? 'primary' : 'default'}
              icon={item.icon}
              className="sf-pad-nav-chip"
              onClick={() => navigate(item.path)}
            >
              {item.label}
            </Button>
          )
        })}
      </nav>

      <main className="sf-pad-main">
        <Outlet />
      </main>
    </div>
  )
}
