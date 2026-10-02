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

export const authApi = {
  login: (payload: LoginPayload) => http.post<LoginResult>('/api/auth/login', payload),
  logout: () => http.post<void>('/api/auth/logout'),
  profile: () => http.get<UserInfo>('/api/auth/profile'),
  changePassword: (payload: { oldPassword: string; newPassword: string }) =>
    http.post<void>('/api/auth/change-password', payload),
}
