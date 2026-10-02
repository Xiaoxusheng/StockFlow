import { Alert, Button, Card, Flex, Form, Input, Typography, message } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { authApi, PASSWORD_RULE, type LoginPayload } from '@/api/auth'
import { useAuthStore, type SessionInput } from '@/stores/auth'
import { resolveErrorMessage } from '@/api/client'
import type { UserInfo } from '@/types/permission'

const { Title, Text } = Typography

/** 开发模式旁路：仅 DEV 构建且显式配置 VITE_AUTH_BYPASS=1 时可用（AGENTS.md） */
const DEV_BYPASS = import.meta.env.DEV && import.meta.env.VITE_AUTH_BYPASS === '1'

interface ForceChangeFormValues {
  old_password: string
  new_password: string
  confirm_password: string
}

/** 首登强改阶段的会话锚点：token 已落库（改密接口需 Bearer），改密成功后凭此拉 me 进入系统 */
interface PendingForceChange {
  token: string
  refreshToken: string | null
  initialPassword: string
}

export function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const setSession = useAuthStore((s) => s.setSession)
  const [messageApi, contextHolder] = message.useMessage()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingForceChange | null>(null)

  const redirect = searchParams.get('redirect') ?? '/dashboard'
  const safeRedirect = redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/dashboard'

  const enter = (session: SessionInput) => {
    setSession(session)
    navigate(safeRedirect, { replace: true })
  }

  /** 权限快照以 GET /api/auth/me 为准（LoginResult.user 不含 permissions 权限点集） */
  const enterWithMe = async (token: string, refreshToken: string | null) => {
    const me = await authApi.me()
    enter({
      token,
      refreshToken,
      user: me.user,
      permissions: me.permissions,
      isSuper: me.is_super,
      mustChangePassword: me.must_change_password,
    })
  }

  const handleLogin = async (values: LoginPayload) => {
    setLoading(true)
    setError(null)
    try {
      const result = await authApi.login(values)
      if (result.must_change_password) {
        // 首登强制改密（plan §7.3）：先落会话（middleware.go:101 白名单放行 PUT /api/auth/password）
        setSession({
          token: result.access_token,
          refreshToken: result.refresh_token,
          user: result.user,
          mustChangePassword: true,
        })
        setPending({
          token: result.access_token,
          refreshToken: result.refresh_token,
          initialPassword: values.password,
        })
        return
      }
      await enterWithMe(result.access_token, result.refresh_token)
    } catch (err) {
      setError(resolveErrorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  const handleForceChange = async (values: ForceChangeFormValues) => {
    if (!pending) return
    setLoading(true)
    setError(null)
    try {
      await authApi.changePassword({
        old_password: values.old_password,
        new_password: values.new_password,
      })
      messageApi.success('密码修改成功')
      // 后端已复位本会话 must_change_password 并踢其余会话（service_auth.go:486-491），
      // 刷新 me 取最新权限快照后进入系统
      await enterWithMe(pending.token, pending.refreshToken)
    } catch (err) {
      setError(resolveErrorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  const forceChange = pending !== null

  return (
    <Flex
      align="center"
      justify="center"
      style={{ minHeight: '100vh', background: 'var(--sf-bg)', padding: 24 }}
    >
      {contextHolder}
      <Card
        styles={{ body: { padding: '32px 32px 24px' } }}
        style={{ width: 400, boxShadow: 'var(--sf-shadow-md)' }}
      >
        <Flex vertical gap={4} style={{ marginBottom: 24 }}>
          <Flex align="center" gap={10}>
            <svg width="30" height="30" viewBox="0 0 32 32" aria-hidden>
              <rect width="32" height="32" rx="7" fill="var(--sf-primary)" />
              <path
                d="M9 11h10a3 3 0 0 1 0 6H11a3 3 0 0 0 0 6h12"
                stroke="#fff"
                strokeWidth="2.6"
                fill="none"
                strokeLinecap="round"
              />
            </svg>
            <Title level={4} style={{ margin: 0 }}>StockFlow</Title>
          </Flex>
          <Text type="secondary">库流智能仓储管理系统</Text>
        </Flex>

        {error && (
          <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />
        )}

        {forceChange ? (
          <>
            <Alert
              type="warning"
              showIcon
              message="首次登录请修改初始密码"
              description="为保障账户安全，修改密码后方可进入系统。"
              style={{ marginBottom: 16 }}
            />
            <Form<ForceChangeFormValues>
              layout="vertical"
              requiredMark={false}
              initialValues={{ old_password: pending.initialPassword }}
              onFinish={handleForceChange}
            >
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
                <Input.Password prefix={<LockOutlined />} placeholder="至少 8 位，含字母与数字" autoComplete="new-password" />
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
              <Button type="primary" htmlType="submit" block loading={loading}>
                修改密码并进入系统
              </Button>
            </Form>
          </>
        ) : (
          <>
            <Form<LoginPayload> layout="vertical" onFinish={handleLogin} requiredMark={false}>
              <Form.Item
                name="username"
                label="用户名"
                rules={[{ required: true, message: '请输入用户名' }]}
              >
                <Input prefix={<UserOutlined />} placeholder="用户名" autoFocus autoComplete="username" />
              </Form.Item>
              <Form.Item
                name="password"
                label="密码"
                rules={[{ required: true, message: '请输入密码' }]}
              >
                <Input.Password
                  prefix={<LockOutlined />}
                  placeholder="密码"
                  autoComplete="current-password"
                />
              </Form.Item>
              <Button type="primary" htmlType="submit" block loading={loading}>
                登录
              </Button>
            </Form>

            {DEV_BYPASS && (
              <>
                <Alert
                  type="warning"
                  showIcon
                  message="后端服务未接入"
                  description="可使用开发模式进入查看页面骨架；所有数据页将显示真实接口错误状态。生产构建无此入口。"
                  style={{ marginTop: 16 }}
                />
                <Button
                  block
                  style={{ marginTop: 12 }}
                  onClick={() =>
                    enter({
                      token: 'dev-bypass',
                      refreshToken: null,
                      user: {
                        id: 'dev',
                        username: 'dev',
                        real_name: '开发模式',
                        is_super: true,
                        roles: ['super_admin'],
                        permissions: ['*'],
                      } satisfies UserInfo,
                      permissions: ['*'],
                      isSuper: true,
                    })
                  }
                >
                  开发模式进入
                </Button>
              </>
            )}
          </>
        )}
      </Card>
    </Flex>
  )
}
