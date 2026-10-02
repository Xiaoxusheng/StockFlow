import { http } from './client'
import type { UserInfo } from '@/types/permission'

export interface LoginPayload {
  username: string
  password: string
}

export interface LoginResult {
  token: string
  user: UserInfo
}

export interface ChangePasswordPayload {
  oldPassword: string
  newPassword: string
}

/** 在线会话（GET /api/auth/sessions；管理员可经 DELETE 踢下线，M1 契约 §7） */
export interface AuthSession {
  id: string
  username?: string
  ip?: string
  userAgent?: string
  createdAt?: string
  lastActiveAt?: string
  /** 是否为当前登录会话 */
  current?: boolean
}

/**
 * 认证域（M1 契约，backend-m1-plan.md §5.4）：
 * - 公开：POST /login、POST /refresh
 * - 受保护：POST /logout、GET /me、PUT /password、GET/DELETE /sessions
 */
export const authApi = {
  login: (payload: LoginPayload) => http.post<LoginResult>('/api/auth/login', payload),
  logout: () => http.post<void>('/api/auth/logout'),
  /** 当前登录用户（修正：原 profile 路径 → GET /api/auth/me） */
  me: () => http.get<UserInfo>('/api/auth/me'),
  /** 修改本人密码（修正：原 change-password 的 POST /api/auth/change-password → PUT /api/auth/password） */
  changePassword: (payload: ChangePasswordPayload) => http.put<void>('/api/auth/password', payload),
  /** 刷新会话 token（公开路由） */
  refresh: (payload: { refreshToken: string }) =>
    http.post<LoginResult>('/api/auth/refresh', payload),
  /** 会话列表（权限点 auth:session:list） */
  sessions: () => http.get<AuthSession[]>('/api/auth/sessions'),
  /** 强制下线指定会话（权限点 auth:session:kick，DELETE /api/auth/sessions/{id}） */
  kickSession: (id: string) => http.delete<void>(`/api/auth/sessions/${id}`),
}
