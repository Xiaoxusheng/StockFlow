import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { UserInfo } from '@/types/permission'

export interface LoginPayload {
  username: string
  password: string
}

/** 登录/刷新结果（后端 LoginResult，internal/auth/service_auth.go:176-183） */
export interface LoginResult {
  access_token: string
  token_type: string
  /** Access Token 有效期（秒） */
  expires_in: number
  refresh_token: string
  must_change_password: boolean
  user: UserInfo | null
}

/** GET /api/auth/me 响应（后端 MeResult，internal/auth/service_auth.go:402-410） */
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

/** 修改本人密码入参（后端 ChangePasswordRequest，internal/auth/handler.go:157-160） */
export interface ChangePasswordPayload {
  old_password: string
  new_password: string
}

/** 密码策略前端镜像（体验校验，以后端 ValidatePassword 为准：internal/auth/password.go:54-75） */
export const PASSWORD_RULE = {
  pattern: /^(?=.*[A-Za-z])(?=.*\d).{8,}$/,
  message: '密码不满足安全策略：长度至少 8 位且必须同时包含字母与数字',
} as const

/**
 * 在线会话视图（后端 SessionView，internal/auth/service_auth.go:514-524）。
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
  /** 是否为当前请求会话（handler.go:212-219 标记，便于前端展示「本机」） */
  current: boolean
}

/**
 * 认证域（后端已交付，internal/auth/handler.go）：
 * - 公开：POST /api/auth/login、POST /api/auth/refresh
 * - 受保护：POST /api/auth/logout、GET /api/auth/me、PUT /api/auth/password、
 *   GET/DELETE /api/auth/sessions
 */
export const authApi = {
  /** POST /api/auth/login */
  login: (payload: LoginPayload) => http.post<LoginResult>('/api/auth/login', payload),
  /** POST /api/auth/logout */
  logout: () => http.post<void>('/api/auth/logout'),
  /** GET /api/auth/me（当前用户 + is_super + permissions 权限点集） */
  me: () => http.get<MeResult>('/api/auth/me'),
  /**
   * PUT /api/auth/password（修改本人密码；后端成功后复位 must_change_password
   * 并强制下线本人其余会话，internal/auth/service_auth.go:443-492）
   */
  changePassword: (payload: ChangePasswordPayload) => http.put<void>('/api/auth/password', payload),
  /** POST /api/auth/refresh（请求体 {refresh_token}，internal/auth/handler.go:90-92） */
  refresh: (payload: { refresh_token: string }) =>
    http.post<LoginResult>('/api/auth/refresh', payload),
  /** GET /api/auth/sessions（分页信封 {items,page,pageSize,total}，internal/auth/handler.go:186-221） */
  sessions: (params?: PageQuery) =>
    http.get<PageResult<AuthSession>>('/api/auth/sessions', { params }),
  /** DELETE /api/auth/sessions/{id}（强制下线指定会话，权限点 auth:session:kick） */
  kickSession: (id: string) => http.delete<void>(`/api/auth/sessions/${id}`),
}
