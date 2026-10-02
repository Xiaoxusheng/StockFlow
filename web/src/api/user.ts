import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

/**
 * 用户域（M1 契约，backend-m1-plan.md §5.4 / §6）：
 * /api/users CRUD + 启停 / 重置密码 / 解锁 / 绑定角色。
 * M1 不提供用户删除接口（软删除仅保留 schema 能力，停用即离岗路径）。
 */

/** 通用启停状态（users / roles / departments 一致，M1 迁移 000001） */
export type CommonStatus = 'ACTIVE' | 'DISABLED'

/** 数据权限范围（backend-m1-plan.md §7.4） */
export type DataScope = 'ALL' | 'SPECIFIED_WAREHOUSE' | 'DEPARTMENT' | 'SELF' | 'SELF_IN_CHARGE'

export interface UserQuery extends PageQuery {
  /** 关键词：用户名 / 姓名 / 手机号 */
  keyword?: string
  status?: CommonStatus
  departmentId?: string
}

/** 用户绑定的角色摘要 */
export interface UserRoleBrief {
  id: number | string
  code: string
  name: string
}

export interface UserItem {
  id: number | string
  username: string
  realName?: string
  phone?: string
  email?: string
  departmentId?: number | string
  departmentName?: string
  dataScope: DataScope
  status: CommonStatus
  /** 账户锁定截止时间（登录保护；存在即处于锁定态，可解锁） */
  lockedUntil?: string
  lastLoginAt?: string
  lastLoginIp?: string
  roles: UserRoleBrief[]
  createdAt?: string
  updatedAt?: string
}

export interface UserCreatePayload {
  username: string
  /** 初始密码（强密码策略：长度 ≥ 8 且含字母 + 数字，backend-m1-plan.md §7.2） */
  password: string
  realName?: string
  phone?: string
  email?: string
  departmentId?: string
  dataScope: DataScope
}

export interface UserUpdatePayload {
  realName?: string
  phone?: string
  email?: string
  departmentId?: string
  dataScope: DataScope
}

export const userApi = {
  users: (query: UserQuery) => http.get<PageResult<UserItem>>('/api/users', { params: query }),
  user: (id: string) => http.get<UserItem>(`/api/users/${id}`),
  createUser: (payload: UserCreatePayload) => http.post<UserItem>('/api/users', payload),
  updateUser: (id: string, payload: UserUpdatePayload) => http.put<void>(`/api/users/${id}`, payload),
  /** 启用 / 停用（权限点 auth:user:status） */
  setUserStatus: (id: string, status: CommonStatus) =>
    http.put<void>(`/api/users/${id}/status`, { status }),
  /** 重置密码（权限点 auth:user:reset-password） */
  resetPassword: (id: string, newPassword: string) =>
    http.put<void>(`/api/users/${id}/reset-password`, { newPassword }),
  /** 账户解锁（权限点 auth:user:unlock，backend-m1-plan.md §7.2） */
  unlock: (id: string) => http.put<void>(`/api/users/${id}/unlock`),
  /** 绑定角色（权限点 auth:user:assign-role） */
  assignRoles: (id: string, roleIds: string[]) =>
    http.put<void>(`/api/users/${id}/roles`, { roleIds }),
}
