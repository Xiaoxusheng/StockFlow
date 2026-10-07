import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { UserInfo } from '@/types/permission'

export interface LoginPayload {
  username: string
  password: string
  /** 图像验证码 id（GET /api/auth/captcha 签发，一次性消费） */
  captcha_id: string
  /** 图像验证码答案（不区分大小写，后端规范化比对） */
  captcha_code: string
}

/** GET /api/auth/captcha 响应（后端 CaptchaChallenge，internal/auth/captcha.go） */
export interface CaptchaChallenge {
  captcha_id: string
  /** data:image/svg+xml;base64,...，<img src> 直用；点击刷新重新签发 */
  image: string
  /** 有效期（秒），默认 180 */
  expires_in: number
}

/** 登录/刷新结果（后端 LoginResult，internal/auth/service_auth.go：access_token/refresh_token
 * 首次登录与静默续期共用同一结构，刷新时 refresh_token 轮换回存） */
export interface LoginResult {
  access_token: string
  token_type: string
  /** Access Token 有效期（秒） */
  expires_in: number
  refresh_token: string
  must_change_password: boolean
  user: UserInfo | null
}

/** GET /api/auth/me 响应（后端 MeResult，internal/auth/service_auth.go） */
export interface MeResult {
  user: UserInfo | null
  is_super: boolean
  data_scope: string
  warehouse_ids: number[]
  dept_id: number
  must_change_password: boolean
  /** 三段冻结权限码集（internal/auth/permissions.go），前端菜单/按钮过滤数据源 */
  permissions: string[]
}

/** 修改本人密码入参（后端 ChangePasswordRequest，internal/auth/handler.go） */
export interface ChangePasswordPayload {
  old_password: string
  new_password: string
}

/** 密码策略固定文案（与后端 AUTH_PASSWORD_WEAK 默认文案同口径，internal/auth/errors.go） */
export const PASSWORD_POLICY_MESSAGE =
  '密码不满足安全策略：长度至少 8 位且必须同时包含字母与数字（上限 72 字节）'

/**
 * 密码策略前端镜像（体验校验，以后端 ValidatePasswordStrength 为准：
 * internal/auth/password.go——RuneCount ≥ 8、UTF-8 字节 ≤ 72、至少一个
 * Unicode 字母（unicode.IsLetter ↔ \p{L}）与一个 Unicode 数字（unicode.IsDigit ↔ \p{Nd}）。
 * 用 validator 而非 pattern：JS 正则按 UTF-16 码元计数，与 Go 的 rune 计数在增补平面
 * 字符（emoji 等）上不一致；代码点计数 + TextEncoder 字节数可精确镜像后端口径。
 * 早年仅认 ASCII 字母的正则已放宽——「密码12」这类合法密码不再被前端误拒。
 * 共用方：改密弹窗（PcLayout）、登录强改（LoginPage）、用户表单（UserListPage）。
 */
export const PASSWORD_RULE = {
  validator: (_rule: unknown, value: string | undefined) => {
    const pw = value ?? ''
    const ok =
      [...pw].length >= 8 && // 代码点数 ≥ 8（等价 Go utf8.RuneCountInString）
      new TextEncoder().encode(pw).length <= 72 && // UTF-8 字节 ≤ 72（bcrypt 输入上限）
      /\p{L}/u.test(pw) && // Unicode 字母
      /\p{Nd}/u.test(pw) // Unicode 数字
    return ok ? Promise.resolve() : Promise.reject(new Error(PASSWORD_POLICY_MESSAGE))
  },
} as const

/**
 * 在线会话视图（后端 SessionView，internal/auth/service_auth.go）。
 * 管理员可经 DELETE /api/auth/sessions/{id} 强制下线（权限点 auth:session:kick）。
 */
export interface AuthSession {
  session_id: string
  user_id: number | string
  username: string
  is_super: boolean
  ip: string
  user_agent: string
  login_at: string | null
  last_active_at: string | null
  /** 是否为当前请求会话（handleSessionList 内标记，便于前端展示「本机」） */
  current: boolean
}

/**
 * 认证域（后端已交付，internal/auth/handler.go）：
 * - 公开：GET /api/auth/captcha（登录验证码签发）、POST /api/auth/login（携带 captcha_id/
 *   captcha_code，人机闸先于凭据校验，internal/auth/captcha.go）、POST /api/auth/refresh
 *   （静默续期入口，client.ts 401 拦截单飞调用）
 * - 受保护：POST /api/auth/logout、GET /api/auth/me、PUT /api/auth/password、
 *   GET/DELETE /api/auth/sessions
 */
export const authApi = {
  /** GET /api/auth/captcha（登录验证码签发，答案存 Redis TTL 默认 3m，一次性消费） */
  captcha: () => http.get<CaptchaChallenge>('/api/auth/captcha'),
  /** POST /api/auth/login */
  login: (payload: LoginPayload) => http.post<LoginResult>('/api/auth/login', payload),
  /** POST /api/auth/logout */
  logout: () => http.post<void>('/api/auth/logout'),
  /** GET /api/auth/me（当前用户 + is_super + permissions 权限点集） */
  me: () => http.get<MeResult>('/api/auth/me'),
  /**
   * PUT /api/auth/password（修改本人密码；后端成功后复位 must_change_password
   * 并强制下线本人其余会话，internal/auth/service_auth.go ChangePassword）
   */
  changePassword: (payload: ChangePasswordPayload) => http.put<void>('/api/auth/password', payload),
  /** POST /api/auth/refresh（请求体 {refresh_token}，后端 RefreshRequest） */
  refresh: (payload: { refresh_token: string }) =>
    http.post<LoginResult>('/api/auth/refresh', payload),
  /** GET /api/auth/sessions（分页信封 {items,page,pageSize,total}，后端 handleSessionList） */
  sessions: (params?: PageQuery) =>
    http.get<PageResult<AuthSession>>('/api/auth/sessions', { params }),
  /** DELETE /api/auth/sessions/{id}（强制下线指定会话，权限点 auth:session:kick） */
  kickSession: (id: string) => http.delete<void>(`/api/auth/sessions/${id}`),
}
