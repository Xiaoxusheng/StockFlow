import { Alert, AutoComplete, Avatar, Badge, Breadcrumb, ConfigProvider, Dropdown, Flex, Form, Input, Layout, Menu, Modal, Tooltip, message } from 'antd'
import {
  BellOutlined,
  DownOutlined,
  LockOutlined,
  LogoutOutlined,
  MoonOutlined,
  SearchOutlined,
  SunOutlined,
  UnorderedListOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router'
import type { AutoCompleteProps, MenuProps, ThemeConfig } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { MENU_TREE, resolveMenuTrail, type MenuItem } from '@/config/menu'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { useUiStore } from '@/stores/ui'
import { canAccess } from '@/types/permission'
import { authApi, PASSWORD_RULE, type ChangePasswordPayload } from '@/api/auth'
import { notificationApi } from '@/api/notifications'
import { SfLogo } from '@/components/common/SfLogo'
import { matchFieldErrors, resolveErrorMessage } from '@/api/client'
import { NotificationDrawer } from './NotificationDrawer'

const { Header, Sider, Content } = Layout

/** Sidebar 菜单规格（任务书 §13）：
 * - 菜单高 38px（36~40）、条目间距 2px（2~4）、圆角 6 = --sf-radius-sm；
 * - Active = 淡蓝背景 + 主色文字（color-mix 从 --sf-primary 派生，Light/Dark 随 Token 自动切换），
 *   左侧 3px 指示条由下方 .sf-pc-sider 样式块补充——antd Menu 无 inline 模式指示条 token；
 * - 图标统一 16px（展开/收起同尺寸，颜色继承条目色，不逐项配色）。
 * 色值一律引用 --sf-* Token；嵌套 ConfigProvider 与 App.tsx 主题按组件合并（算法/暗色继承）。 */
const SIDER_MENU_THEME: ThemeConfig = {
  components: {
    Menu: {
      itemHeight: 38,
      itemMarginBlock: 2,
      itemMarginInline: 8,
      itemBorderRadius: 6,
      itemColor: 'var(--sf-text-secondary)',
      itemHoverColor: 'var(--sf-text)',
      itemHoverBg: 'var(--sf-surface-hover)',
      itemActiveBg: 'var(--sf-surface-hover)',
      itemSelectedColor: 'var(--sf-primary)',
      itemSelectedBg: 'color-mix(in srgb, var(--sf-primary) 12%, transparent)',
      iconSize: 16,
      collapsedIconSize: 16,
    },
  },
}

/** PcLayout 局部样式（仅骨架自身需要、antd token 覆盖不到的部分）：
 * 菜单选中左侧 3px 指示条 + Header 图标按钮的 hover/focus/active（任务书 §13/§58）。
 * Reduced Motion（任务书 §59）：关闭按钮过渡与按压缩放。 */
const PC_LAYOUT_CSS = `
.sf-pc-sider .ant-menu-item-selected::before{content:'';position:absolute;inset-block:6px;inset-inline-start:0;width:3px;border-radius:2px;background:var(--sf-primary);}
.sf-header-icon-btn{transition:background var(--sf-motion-fast) var(--sf-motion-ease),color var(--sf-motion-fast) var(--sf-motion-ease);}
.sf-header-icon-btn:hover{background:var(--sf-border-subtle);}
.sf-header-icon-btn:focus-visible{outline:2px solid var(--sf-primary);outline-offset:1px;}
.sf-header-icon-btn:active{transform:scale(.97);}
@media (prefers-reduced-motion:reduce){.sf-header-icon-btn{transition:none;}.sf-header-icon-btn:active{transform:none;}}
`

function buildMenuItems(items: MenuItem[]): NonNullable<MenuProps['items']> {
  return items.map((item) => ({
    key: item.path,
    icon: item.icon,
    label: item.label,
    children: item.children ? buildMenuItems(item.children) : undefined,
  }))
}

/** MENU_TREE 取叶子：分组容器在侧边栏点击是展开而非跳转，不作为搜索直达目标 */
function flattenMenuLeaves(items: MenuItem[]): MenuItem[] {
  return items.flatMap((item) => (item.children && item.children.length > 0 ? item.children : [item]))
}

/** PC Layout（frontend.md §4）：固定 Sidebar + 紧凑 Header + 面包屑 + 内容区 */
export function PcLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { siderCollapsed, toggleSider } = useUiStore()
  const { mode, toggleMode } = useThemeStore()
  const user = useAuthStore((s) => s.user)
  const clearSession = useAuthStore((s) => s.clearSession)
  /** 首登强制改密门禁（client.ts 403 AUTH_PASSWORD_CHANGE_REQUIRED 特判置位）：无条件弹出改密 Modal */
  const mustChangePassword = useAuthStore((s) => s.mustChangePassword)
  /** 会话自愈：首登强制改密分支落的会话不含 is_super/权限集（LoginPage 精简 setSession 形态），
   * 持久化后经刷新复用会让 canAccess fail-closed 把全部带码子菜单滤空（菜单"点不动"）。
   * 挂载时检测残缺会话并回拉 /api/auth/me 补全（条件翻转后自动停止，无循环）。 */
  const token = useAuthStore((s) => s.token)
  const isSuper = useAuthStore((s) => s.isSuper)
  const permissions = useAuthStore((s) => s.permissions)
  const setSession = useAuthStore((s) => s.setSession)
  const sessionIncomplete =
    !!token && !mustChangePassword && (!isSuper || !permissions || permissions.length === 0)
  useEffect(() => {
    if (!sessionIncomplete) return
    let cancelled = false
    authApi
      .me()
      .then((me) => {
        if (cancelled) return
        const current = useAuthStore.getState()
        if (!current.token) return
        setSession({
          token: current.token,
          refreshToken: current.refreshToken,
          user: me.user,
          permissions: me.permissions,
          isSuper: me.is_super,
          mustChangePassword: me.must_change_password,
        })
      })
      .catch(() => undefined) // 401 由 client.ts 统一处理跳登录
    return () => {
      cancelled = true
    }
  }, [sessionIncomplete, setSession])
  const [notificationOpen, setNotificationOpen] = useState(false)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [messageApi, contextHolder] = message.useMessage()
  /** 窄屏（<992px）下自动折叠侧边栏（frontend.md §19.1 平板竖屏适配） */
  const [broken, setBroken] = useState(false)
  const collapsed = siderCollapsed || broken

  /** 通知未读数（GET /api/notifications/unread-count；后端通知域 M3 已交付，
   * internal/sysops/routes.go 个人收件箱路由组）：请求失败时 Badge 无 count 自动隐藏
   * （错误隐藏方案，不阻塞布局） */
  const unreadCount = useQuery({
    queryKey: ['notifications', 'unread-count'],
    queryFn: notificationApi.unreadCount,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 30_000,
  })

  /** 可见菜单（与 sidebar 同一 canAccess fail-closed 过滤），同时驱动渲染与全局搜索 */
  const visibleMenu = useMemo(
    () =>
      MENU_TREE.filter((group) => canAccess(user, group.permission)).map((group) => ({
        ...group,
        children: group.children?.filter((child) => canAccess(user, child.permission)),
      })),
    [user],
  )
  const menuItems = useMemo(() => buildMenuItems(visibleMenu), [visibleMenu])
  /** 全局搜索候选源：菜单叶子（与侧边栏可点击行为一致，占位叶子跳占位页亦为真实反馈） */
  const searchSource = useMemo(() => flattenMenuLeaves(visibleMenu), [visibleMenu])

  const trail = resolveMenuTrail(location.pathname)
  const openKey = useMemo(
    () =>
      MENU_TREE.find((group) => group.children?.some((child) => child.path === location.pathname))
        ?.path,
    [location.pathname],
  )
  const [openKeys, setOpenKeys] = useState<string[]>(openKey ? [openKey] : [])
  // 跨分组跳转时自动展开目标分组。必须挂在路由变化上而非渲染期：渲染期每帧断言
  // 会把用户刚点收起的当前分组立刻弹回（onOpenChange 置空后下一帧又被塞回），菜单表现为"收不回去"。
  useEffect(() => {
    if (openKey) {
      setOpenKeys((prev) => (prev.includes(openKey) ? prev : [...prev, openKey]))
    }
  }, [openKey])

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
    { key: 'change-password', icon: <LockOutlined />, label: '修改密码' },
    { type: 'divider' },
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
  ]

  const handleUserMenu: MenuProps['onClick'] = ({ key }) => {
    if (key === 'change-password') {
      setPasswordOpen(true)
    }
    if (key === 'logout') {
      handleLogout()
    }
  }

  /** 搜索候选：菜单标签（含分组）包含关键字即命中 */
  const searchOptions: AutoCompleteProps['options'] = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return []
    return searchSource
      .filter((item) => item.label.toLowerCase().includes(kw))
      .map((item) => ({ value: item.path, label: item.label }))
  }, [keyword, searchSource])

  /** 候选点选 / 下拉高亮回车：真实导航到对应菜单路由 */
  const handleSearchSelect = (path: string) => {
    navigate(path)
    setKeyword('')
  }

  /** 直接回车（下拉无高亮项时兜底）：取第一个标签命中项直达，未命中给出真实反馈 */
  const handleSearchEnter = () => {
    const kw = keyword.trim()
    if (!kw) return
    const hit = searchSource.find((item) => item.label.toLowerCase().includes(kw.toLowerCase()))
    if (hit) {
      handleSearchSelect(hit.path)
    } else {
      messageApi.warning(`未找到与「${kw}」匹配的菜单`)
    }
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      {contextHolder}
      <style>{PC_LAYOUT_CSS}</style>
      <Sider
        className="sf-pc-sider"
        width="var(--sf-sider-width)"
        collapsedWidth="var(--sf-sider-collapsed-width)"
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
          gap="var(--sf-space-2)"
          style={{ height: 'var(--sf-header-height)', overflow: 'hidden' }}
        >
          <SfLogo size={24} />
          {!collapsed && (
            <span style={{ fontWeight: 600, fontSize: 15, whiteSpace: 'nowrap' }}>StockFlow</span>
          )}
        </Flex>
        <ConfigProvider theme={SIDER_MENU_THEME}>
          <Menu
            mode="inline"
            items={menuItems}
            selectedKeys={[location.pathname]}
            openKeys={collapsed ? [] : openKeys}
            onOpenChange={(keys) => setOpenKeys(keys)}
            style={{ borderInlineEnd: 'none', background: 'transparent' }}
            onClick={({ key }) => navigate(key)}
          />
        </ConfigProvider>
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
            padding: '0 var(--sf-space-4)',
            height: 'var(--sf-header-height)',
            lineHeight: 'normal',
            background: 'var(--sf-surface)',
            borderBottom: '1px solid var(--sf-border)',
          }}
        >
          <Flex align="center" gap="var(--sf-space-3)" style={{ minWidth: 0 }}>
            <ButtonGhost
              title={collapsed ? '展开菜单' : '收起菜单'}
              icon={<UnorderedListOutlined />}
              onClick={toggleSider}
            />
            <Breadcrumb items={breadcrumbItems} style={{ whiteSpace: 'nowrap' }} />
          </Flex>

          <Flex align="center" gap="var(--sf-space-2)">
            <AutoComplete
              value={keyword}
              options={searchOptions}
              onChange={(value) => setKeyword(value)}
              onSelect={handleSearchSelect}
              popupMatchSelectWidth={280}
              style={{ width: 220 }}
            >
              {/* 高度取全局 controlHeight 32（任务书 §24 控件 32~36），与页面表单控件一致 */}
              <Input
                allowClear
                onPressEnter={handleSearchEnter}
                prefix={<SearchOutlined style={{ color: 'var(--sf-text-muted)' }} />}
                placeholder="搜索菜单名称，回车直达"
                style={{ width: 220 }}
              />
            </AutoComplete>
            <Tooltip title={mode === 'light' ? '切换深色模式' : '切换浅色模式'}>
              <ButtonGhost
                title="主题"
                icon={mode === 'light' ? <MoonOutlined /> : <SunOutlined />}
                onClick={toggleMode}
              />
            </Tooltip>
            <Tooltip title="通知">
              <Badge count={unreadCount.data} size="small" offset={[3, -1]}>
                <ButtonGhost
                  title="通知"
                  icon={<BellOutlined />}
                  onClick={() => setNotificationOpen(true)}
                />
              </Badge>
            </Tooltip>
            <Dropdown menu={{ items: userMenu, onClick: handleUserMenu }} trigger={['click']}>
              <Flex
                align="center"
                gap="var(--sf-space-2)"
                style={{ cursor: 'pointer', padding: 'var(--sf-space-1) var(--sf-space-2)', borderRadius: 'var(--sf-radius-md)' }}
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
                  {user?.real_name ?? user?.username ?? '未登录'}
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

      <NotificationDrawer
        open={notificationOpen}
        onClose={() => {
          setNotificationOpen(false)
          // 关闭抽屉时刷新未读数（抽屉内可能已标记已读）
          void unreadCount.refetch()
        }}
      />

      <ChangePasswordModal
        open={passwordOpen || mustChangePassword}
        force={mustChangePassword}
        onClose={() => setPasswordOpen(false)}
      />
    </Layout>
  )
}

interface ChangePasswordFormValues extends ChangePasswordPayload {
  confirm_password: string
}

/**
 * 修改密码弹窗（PUT /api/auth/password）：
 * - 普通模式（用户菜单入口）：可取消，成功后要求重新登录（其余会话已被后端强制下线）；
 * - 强制模式（首登门禁 mustChangePassword）：不可关闭，成功后刷新 /api/auth/me
 *   补全权限快照并继续当前会话（后端已复位标志，service_auth.go ChangePassword）。
 */
function ChangePasswordModal({
  open,
  force,
  onClose,
}: {
  open: boolean
  force: boolean
  onClose: () => void
}) {
  const [form] = Form.useForm<ChangePasswordFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const clearSession = useAuthStore((s) => s.clearSession)
  const navigate = useNavigate()

  const handleOk = async () => {
    let values: ChangePasswordFormValues
    try {
      values = await form.validateFields()
    } catch {
      return // 表单校验失败：Form.Item 已内联提示
    }
    setSubmitting(true)
    setError(null)
    try {
      await authApi.changePassword({
        old_password: values.old_password,
        new_password: values.new_password,
      })
      if (force) {
        const { token, refreshToken, setSession } = useAuthStore.getState()
        if (!token) {
          setError('登录状态异常，请重新登录')
          return
        }
        const me = await authApi.me()
        setSession({
          token,
          refreshToken,
          user: me.user,
          permissions: me.permissions,
          isSuper: me.is_super,
          mustChangePassword: me.must_change_password,
        })
        messageApi.success('密码修改成功')
        onClose()
      } else {
        messageApi.success('密码修改成功，请使用新密码重新登录')
        onClose()
        clearSession()
        void authApi.logout().catch(() => undefined)
        navigate('/login', { replace: true })
      }
    } catch (err) {
      // bind 校验失败（details.fields）：字段级错误落到对应 Form.Item（client.ts
      // matchFieldErrors 归一匹配 Go 字段名小写键），替代固定文案「请求体格式错误」
      const fieldErrors = matchFieldErrors(err, ['old_password', 'new_password'])
      if (fieldErrors) {
        // 键来自上一行字面量白名单，均为 ChangePasswordFormValues 合法字段
        form.setFields(
          Object.entries(fieldErrors).map(([name, errors]) => ({
            name: name as keyof ChangePasswordFormValues,
            errors: [errors],
          })),
        )
        setError(null)
      } else {
        setError(resolveErrorMessage(err))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      title="修改密码"
      open={open}
      width={420}
      forceRender
      confirmLoading={submitting}
      okText="保存"
      onOk={handleOk}
      onCancel={force ? undefined : onClose}
      closable={!force}
      keyboard={!force}
      maskClosable={false}
      cancelButtonProps={force ? { style: { display: 'none' } } : undefined}
    >
      {contextHolder}
      {force && (
        <Alert
          type="warning"
          showIcon
          message="必须先修改初始密码"
          description="修改密码后方可继续使用系统。"
          style={{ marginBottom: 16 }}
        />
      )}
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
      <Form<ChangePasswordFormValues> form={form} layout="vertical" requiredMark={false}>
        <Form.Item
          name="old_password"
          label="原密码"
          rules={[{ required: true, message: '请输入原密码' }]}
        >
          <Input.Password prefix={<LockOutlined />} autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="new_password"
          label="新密码"
          rules={[{ required: true, message: '请输入新密码' }, PASSWORD_RULE]}
        >
          <Input.Password prefix={<LockOutlined />} placeholder="至少 8 位，含字母与数字（上限 72 字节）" autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm_password"
          label="确认新密码"
          dependencies={['new_password']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('new_password') === value) {
                  return Promise.resolve()
                }
                return Promise.reject(new Error('两次输入的密码不一致'))
              },
            }),
          ]}
        >
          <Input.Password prefix={<LockOutlined />} autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
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
      className="sf-header-icon-btn"
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
    >
      {icon}
    </button>
  )
}
