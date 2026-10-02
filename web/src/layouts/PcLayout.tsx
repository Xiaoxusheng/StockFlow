import { Avatar, Breadcrumb, Dropdown, Flex, Input, Layout, Menu, Tooltip } from 'antd'
import {
  BellOutlined,
  DownOutlined,
  LogoutOutlined,
  MoonOutlined,
  SearchOutlined,
  SunOutlined,
  UnorderedListOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router'
import type { MenuProps } from 'antd'
import { MENU_TREE, resolveMenuTrail, type MenuItem } from '@/config/menu'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useUiStore } from '@/stores/ui'
import { canAccess } from '@/types/permission'
import { authApi } from '@/api/auth'
import { NotificationDrawer } from './NotificationDrawer'

const { Header, Sider, Content } = Layout

function buildMenuItems(items: MenuItem[]): NonNullable<MenuProps['items']> {
  return items.map((item) => ({
    key: item.path,
    icon: item.icon,
    label: item.label,
    children: item.children ? buildMenuItems(item.children) : undefined,
  }))
}

/** PC Layout（frontend.md §4）：固定 Sidebar + 紧凑 Header + 面包屑 + 内容区 */
export function PcLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { siderCollapsed, toggleSider } = useUiStore()
  const { mode, toggleMode } = useThemeStore()
  const user = useAuthStore((s) => s.user)
  const clearSession = useAuthStore((s) => s.clearSession)
  const [notificationOpen, setNotificationOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  /** 窄屏（<992px）下自动折叠侧边栏（frontend.md §19.1 平板竖屏适配） */
  const [broken, setBroken] = useState(false)
  const collapsed = siderCollapsed || broken

  const menuItems = useMemo(
    () =>
      buildMenuItems(
        MENU_TREE.filter((group) => canAccess(user, group.permission)).map((group) => ({
          ...group,
          children: group.children?.filter((child) => canAccess(user, child.permission)),
        })),
      ),
    [user],
  )

  const trail = resolveMenuTrail(location.pathname)
  const openKey = useMemo(
    () =>
      MENU_TREE.find((group) => group.children?.some((child) => child.path === location.pathname))
        ?.path,
    [location.pathname],
  )
  const [openKeys, setOpenKeys] = useState<string[]>(openKey ? [openKey] : [])
  // 跨分组跳转时自动展开目标分组
  if (openKey && !openKeys.includes(openKey)) {
    setOpenKeys([...openKeys, openKey])
  }

  const breadcrumbItems = useMemo(() => {
    if (trail.group && trail.page && trail.group !== trail.page) {
      return [
        { title: trail.group.label },
        { title: trail.page.label },
      ]
    }
    return trail.page ? [{ title: trail.page.label }] : []
  }, [trail])

  const handleLogout = () => {
    clearSession()
    void authApi.logout().catch(() => {
      // 后端未接入时本地登出即可，不阻塞
    })
    navigate('/login', { replace: true })
  }

  const userMenu: MenuProps['items'] = [
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
  ]

  const handleUserMenu: MenuProps['onClick'] = ({ key }) => {
    if (key === 'logout') {
      handleLogout()
    }
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        width={216}
        collapsedWidth={48}
        breakpoint="lg"
        onBreakpoint={setBroken}
        collapsed={collapsed}
        theme="light"
        style={{
          borderRight: '1px solid var(--sf-border)',
          background: 'var(--sf-surface)',
          overflow: 'auto',
          height: '100vh',
          position: 'sticky',
          top: 0,
        }}
      >
        <Flex
          align="center"
          justify="center"
          gap={8}
          style={{ height: 'var(--sf-header-height)', overflow: 'hidden' }}
        >
          <svg width="24" height="24" viewBox="0 0 32 32" aria-hidden>
            <rect width="32" height="32" rx="7" fill="var(--sf-primary)" />
            <path
              d="M9 11h10a3 3 0 0 1 0 6H11a3 3 0 0 0 0 6h12"
              stroke="#fff"
              strokeWidth="2.6"
              fill="none"
              strokeLinecap="round"
            />
          </svg>
          {!collapsed && (
            <span style={{ fontWeight: 600, fontSize: 15, whiteSpace: 'nowrap' }}>StockFlow</span>
          )}
        </Flex>
        <Menu
          mode="inline"
          items={menuItems}
          selectedKeys={[location.pathname]}
          openKeys={collapsed ? [] : openKeys}
          onOpenChange={(keys) => setOpenKeys(keys)}
          style={{ borderInlineEnd: 'none', background: 'transparent' }}
          onClick={({ key }) => navigate(key)}
        />
      </Sider>

      <Layout>
        <Header
          style={{
            position: 'sticky',
            top: 0,
            zIndex: 10,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '0 16px',
            height: 'var(--sf-header-height)',
            lineHeight: 'normal',
            background: 'var(--sf-surface)',
            borderBottom: '1px solid var(--sf-border)',
          }}
        >
          <Flex align="center" gap={12} style={{ minWidth: 0 }}>
            <ButtonGhost
              title={collapsed ? '展开菜单' : '收起菜单'}
              icon={<UnorderedListOutlined />}
              onClick={toggleSider}
            />
            <Breadcrumb items={breadcrumbItems} style={{ whiteSpace: 'nowrap' }} />
          </Flex>

          <Flex align="center" gap={8}>
            <Input
              size="small"
              allowClear
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              onPressEnter={() => setKeyword('')}
              prefix={<SearchOutlined style={{ color: 'var(--sf-text-muted)' }} />}
              placeholder="搜索 SKU / 单据 / 库位 / SN"
              style={{ width: 220 }}
            />
            <Tooltip title={mode === 'light' ? '切换深色模式' : '切换浅色模式'}>
              <ButtonGhost
                title="主题"
                icon={mode === 'light' ? <MoonOutlined /> : <SunOutlined />}
                onClick={toggleMode}
              />
            </Tooltip>
            <Tooltip title="通知">
              <ButtonGhost
                title="通知"
                icon={<BellOutlined />}
                onClick={() => setNotificationOpen(true)}
              />
            </Tooltip>
            <Dropdown menu={{ items: userMenu, onClick: handleUserMenu }} trigger={['click']}>
              <Flex
                align="center"
                gap={6}
                style={{ cursor: 'pointer', padding: '4px 8px', borderRadius: 'var(--sf-radius-md)' }}
              >
                <Avatar size={26} icon={<UserOutlined />} />
                <span
                  style={{
                    maxWidth: 120,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {user?.realName ?? user?.username ?? '未登录'}
                </span>
                <DownOutlined style={{ fontSize: 10, color: 'var(--sf-text-muted)' }} />
              </Flex>
            </Dropdown>
          </Flex>
        </Header>

        <Content style={{ minHeight: 'calc(100vh - var(--sf-header-height))' }}>
          <Outlet />
        </Content>
      </Layout>

      <NotificationDrawer open={notificationOpen} onClose={() => setNotificationOpen(false)} />
    </Layout>
  )
}

function ButtonGhost({
  icon,
  title,
  onClick,
}: {
  icon: ReactNode
  title: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-label={title}
      onClick={onClick}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 30,
        height: 30,
        border: 'none',
        borderRadius: 'var(--sf-radius-md)',
        background: 'transparent',
        color: 'var(--sf-text-secondary)',
        cursor: 'pointer',
      }}
      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--sf-border-subtle)')}
      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
    >
      {icon}
    </button>
  )
}
