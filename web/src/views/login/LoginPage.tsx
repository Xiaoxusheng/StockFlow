import { Alert, Button, Form, Input, Typography, message } from 'antd'
import {
  DatabaseOutlined,
  LockOutlined,
  MobileOutlined,
  NodeIndexOutlined,
  ReloadOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { authApi, PASSWORD_RULE, type CaptchaChallenge, type LoginPayload } from '@/api/auth'
import { SfLogo } from '@/components/common/SfLogo'
import { useAuthStore, type SessionInput } from '@/stores/auth'
import { matchFieldErrors, resolveErrorMessage } from '@/api/client'
import type { UserInfo } from '@/types/permission'

const { Text, Title } = Typography

/** 开发模式旁路：仅 DEV 构建且显式配置 VITE_AUTH_BYPASS=1 时可用（AGENTS.md） */
const DEV_BYPASS = import.meta.env.DEV && import.meta.env.VITE_AUTH_BYPASS === '1'

/** 品牌面板价值点（桌面 ≥1024px 展示；文案与 business-flow 主链一致） */
const BRAND_FEATURES = [
  {
    icon: <DatabaseOutlined />,
    title: '统一库存台账',
    desc: '库存 / 批次 / 库位 / 序列号一账贯通，实时可见',
  },
  {
    icon: <NodeIndexOutlined />,
    title: '全链路追溯',
    desc: '收货、质检、上架、出库环环留痕，反向可查',
  },
  {
    icon: <MobileOutlined />,
    title: '多端协同作业',
    desc: 'PC 管理端、Pad 作业端、PDA 扫码同一套实时数据',
  },
] as const

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

/** 品牌面板（桌面端）：深海军蓝固定品牌色域 + 低饱和辉光 + 产品价值三行 */
function BrandPanel() {
  return (
    <aside className="sf-login-brand" aria-hidden>
      <div className="sf-login-brand-head">
        <SfLogo size={30} />
        <span className="sf-login-brand-word">StockFlow</span>
      </div>
      <div className="sf-login-brand-hero">
        <span className="sf-login-brand-accent" />
        <h1 className="sf-login-brand-headline">
          一套系统
          <br />
          管全仓流转
        </h1>
        <p className="sf-login-brand-sub">
          从采购入库到销售出库，批次、效期、库位与库存台账全程贯通，异常可追、责任可溯。
        </p>
        <div className="sf-login-features">
          {BRAND_FEATURES.map((f) => (
            <div key={f.title} className="sf-login-feature">
              <span className="sf-login-feature-icon">{f.icon}</span>
              <div>
                <div className="sf-login-feature-title">{f.title}</div>
                <div className="sf-login-feature-desc">{f.desc}</div>
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="sf-login-brand-foot">库流智能仓储管理系统 · Warehouse Management System</div>
    </aside>
  )
}

export function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [form] = Form.useForm<ForceChangeFormValues>()
  /** 登录表单实例（与强改密 form 分离；验证码输入值清空用） */
  const [loginForm] = Form.useForm<Pick<LoginPayload, 'username' | 'password' | 'captcha_code'>>()
  const setSession = useAuthStore((s) => s.setSession)
  const [messageApi, contextHolder] = message.useMessage()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingForceChange | null>(null)

  // ---- 图像验证码（internal/auth/captcha.go：一次性消费，失败后必须换新） ----
  const [captcha, setCaptcha] = useState<CaptchaChallenge | null>(null)
  const [captchaLoading, setCaptchaLoading] = useState(false)

  const refreshCaptcha = useCallback(async () => {
    setCaptchaLoading(true)
    try {
      const next = await authApi.captcha()
      setCaptcha(next)
      loginForm.setFieldValue('captcha_code', undefined)
    } catch {
      // 验证码签发失败：图片区显示"点击重试"，提交会被后端 400 拦下，如实呈态即可
      setCaptcha(null)
    } finally {
      setCaptchaLoading(false)
    }
  }, [loginForm])

  useEffect(() => {
    void refreshCaptcha()
  }, [refreshCaptcha])

  const redirect = searchParams.get('redirect') ?? '/dashboard'
  const safeRedirect = redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/dashboard'

  const enter = (session: SessionInput) => {
    setSession(session)
    navigate(safeRedirect, { replace: true })
  }

  /** 权限快照以 GET /api/auth/me 为准（LoginResult.user 不含 permissions 权限点集）。
   * 必须先落 token 再调 me：client.ts 请求拦截器从 auth store 取 token，
   * 退出登录后 store 为空，若先 me() 会无 Authorization 裸奔 401，
   * 被"登录已过期"拦截清会话——形成退出后永远登不进的死循环。 */
  const enterWithMe = async (token: string, refreshToken: string | null) => {
    setSession({
      token,
      refreshToken,
      user: null,
      mustChangePassword: false,
    })
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

  const handleLogin = async (values: Pick<LoginPayload, 'username' | 'password' | 'captcha_code'>) => {
    if (!captcha) {
      setError('验证码未加载，请点击验证码图片刷新后重试')
      return
    }
    setLoading(true)
    setError(null)
    try {
      const result = await authApi.login({
        username: values.username,
        password: values.password,
        captcha_id: captcha.captcha_id,
        captcha_code: values.captcha_code,
      })
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
      // 验证码一次性消费（答错/过期均作废）：失败后必须换新图并清空输入
      void refreshCaptcha()
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
      // bind 校验失败（details.fields）：字段级错误落到对应 Form.Item（client.ts
      // matchFieldErrors 归一匹配 Go 字段名小写键），替代固定文案「请求体格式错误」
      const fieldErrors = matchFieldErrors(err, ['old_password', 'new_password'])
      if (fieldErrors) {
        // 键来自上一行字面量白名单，均为 ForceChangeFormValues 合法字段
        form.setFields(
          Object.entries(fieldErrors).map(([name, errors]) => ({
            name: name as keyof ForceChangeFormValues,
            errors: [errors],
          })),
        )
        setError(null)
      } else {
        setError(resolveErrorMessage(err))
      }
    } finally {
      setLoading(false)
    }
  }

  const forceChange = pending !== null

  return (
    <div className="sf-login">
      {contextHolder}
      <BrandPanel />
      <main className="sf-login-main">
        <div className="sf-login-panel">
          <div className="sf-login-mobile-head" aria-hidden>
            <SfLogo size={30} />
            <Text strong style={{ fontSize: 17 }}>StockFlow</Text>
          </div>

          {forceChange ? (
            <>
              <Title level={3} className="sf-login-title">
                修改初始密码
              </Title>
              <Text className="sf-login-subtitle">为保障账户安全，修改密码后方可进入系统</Text>
              {error && (
                <Alert type="error" showIcon message={error} style={{ marginTop: 16 }} />
              )}
              <Alert
                type="warning"
                showIcon
                message="首次登录请修改初始密码"
                style={{ marginTop: 16 }}
              />
              <Form<ForceChangeFormValues>
                form={form}
                layout="vertical"
                requiredMark={false}
                initialValues={{ old_password: pending.initialPassword }}
                onFinish={handleForceChange}
                className="sf-login-form"
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
                <Button type="primary" htmlType="submit" block loading={loading} size="large">
                  修改密码并进入系统
                </Button>
              </Form>
            </>
          ) : (
            <>
              <h2 className="sf-login-title">欢迎回来</h2>
              <p className="sf-login-subtitle">登录 StockFlow，继续你的仓储作业</p>
              {error && (
                <Alert type="error" showIcon message={error} style={{ marginTop: 16 }} />
              )}
              <Form<Pick<LoginPayload, 'username' | 'password' | 'captcha_code'>>
                form={loginForm}
                layout="vertical"
                onFinish={handleLogin}
                requiredMark={false}
                className="sf-login-form"
              >
                <Form.Item
                  name="username"
                  label="用户名"
                  rules={[{ required: true, message: '请输入用户名' }]}
                >
                  <Input
                    prefix={<UserOutlined style={{ color: 'var(--sf-text-muted)' }} />}
                    placeholder="用户名"
                    size="large"
                    autoFocus
                    autoComplete="username"
                  />
                </Form.Item>
                <Form.Item
                  name="password"
                  label="密码"
                  rules={[{ required: true, message: '请输入密码' }]}
                >
                  <Input.Password
                    prefix={<LockOutlined style={{ color: 'var(--sf-text-muted)' }} />}
                    placeholder="密码"
                    size="large"
                    autoComplete="current-password"
                  />
                </Form.Item>
                <Form.Item
                  name="captcha_code"
                  label="验证码"
                  rules={[
                    { required: true, message: '请输入验证码' },
                    { pattern: /^[0-9A-Za-z]{4}$/, message: '验证码为 4 位字母或数字' },
                  ]}
                >
                  <div className="sf-login-captcha-row">
                    <Input
                      prefix={<LockOutlined style={{ color: 'var(--sf-text-muted)' }} />}
                      placeholder="不区分大小写"
                      size="large"
                      maxLength={4}
                      autoComplete="off"
                    />
                    <button
                      type="button"
                      className={`sf-login-captcha${captchaLoading ? ' sf-login-captcha--loading' : ''}`}
                      onClick={() => void refreshCaptcha()}
                      aria-label="验证码图片，点击刷新"
                      title="点击刷新"
                    >
                      {captcha ? (
                        <img src={captcha.image} alt="" draggable={false} data-captcha-id={captcha.captcha_id} />
                      ) : captchaLoading ? (
                        <ReloadOutlined spin />
                      ) : (
                        <span className="sf-login-captcha--empty">加载失败·点击重试</span>
                      )}
                    </button>
                  </div>
                </Form.Item>
                <Form.Item style={{ marginBottom: 0 }}>
                  <Button type="primary" htmlType="submit" block loading={loading} size="large">
                    登录
                  </Button>
                </Form.Item>
              </Form>
            </>
          )}

          {DEV_BYPASS && !forceChange && (
            <>
              <Alert
                type="warning"
                showIcon
                message="后端服务未接入"
                description="可使用开发模式进入查看页面骨架；所有数据页将显示真实接口错误状态。生产构建无此入口。"
                style={{ marginTop: 24 }}
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

          <div className="sf-login-foot">© 2026 StockFlow · 库流智能仓储管理系统</div>
        </div>
      </main>
    </div>
  )
}
