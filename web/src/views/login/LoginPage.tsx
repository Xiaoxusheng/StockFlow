import { Alert, Button, Card, Flex, Form, Input, Typography } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { authApi, type LoginPayload } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { resolveErrorMessage } from '@/api/client'
import type { UserInfo } from '@/types/permission'

const { Title, Text } = Typography

/** 开发模式旁路：仅 DEV 构建且显式配置 VITE_AUTH_BYPASS=1 时可用（AGENTS.md） */
const DEV_BYPASS = import.meta.env.DEV && import.meta.env.VITE_AUTH_BYPASS === '1'

export function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const setSession = useAuthStore((s) => s.setSession)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const redirect = searchParams.get('redirect') ?? '/dashboard'
  const safeRedirect = redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/dashboard'

  const enter = (session: { token: string; user: UserInfo }) => {
    setSession(session)
    navigate(safeRedirect, { replace: true })
  }

  const handleLogin = async (values: LoginPayload) => {
    setLoading(true)
    setError(null)
    try {
      const result = await authApi.login(values)
      enter(result)
    } catch (err) {
      setError(resolveErrorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Flex
      align="center"
      justify="center"
      style={{ minHeight: '100vh', background: 'var(--sf-bg)', padding: 24 }}
    >
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
                  user: {
                    id: 'dev',
                    username: 'dev',
                    realName: '开发模式',
                    roles: [],
                    permissions: [],
                  },
                })
              }
            >
              开发模式进入
            </Button>
          </>
        )}
      </Card>
    </Flex>
  )
}
