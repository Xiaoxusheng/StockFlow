import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { CommonStatus } from './user'

/**
 * RBAC / 组织域（M1 契约，backend-m1-plan.md §5.4）：
 * /api/roles CRUD + 绑定权限、GET /api/permissions（只读）、/api/departments CRUD（树形）。
 * roles / departments 无删除接口——一律走 status 停用（§5.4.1 动作清单）。
 */

// ---------- 角色 ----------

export interface RoleQuery extends PageQuery {
  keyword?: string
  status?: CommonStatus
}

export interface RoleItem {
  id: number | string
  code: string
  name: string
  /** 内置角色（is_system）：禁删除 */
  isSystem: boolean
  status: CommonStatus
  createdAt?: string
  updatedAt?: string
}

/** 角色详情：含已绑定权限点 ID 集合 */
export interface RoleDetail extends RoleItem {
  permissionIds: Array<number | string>
}

export interface RolePayload {
  code: string
  name: string
}

// ---------- 权限（只读） ----------

export type PermissionType = 'MENU' | 'BUTTON' | 'API'

export interface PermissionQuery extends PageQuery {
  keyword?: string
  type?: PermissionType
}

export interface PermissionItem {
  id: number | string
  code: string
  name: string
  type: PermissionType
  parentId?: number | string
  sort: number
  status: CommonStatus
  updatedAt?: string
}

// ---------- 部门（树形） ----------

export interface DepartmentNode {
  id: number | string
  parentId?: number | string
  code: string
  name: string
  status: CommonStatus
  children?: DepartmentNode[]
}

export interface DepartmentPayload {
  parentId?: string
  code: string
  name: string
}

export const rbacApi = {
  // 角色
  roles: (query: RoleQuery) => http.get<PageResult<RoleItem>>('/api/roles', { params: query }),
  role: (id: string) => http.get<RoleDetail>(`/api/roles/${id}`),
  createRole: (payload: RolePayload) => http.post<RoleItem>('/api/roles', payload),
  updateRole: (id: string, payload: RolePayload) => http.put<void>(`/api/roles/${id}`, payload),
  setRoleStatus: (id: string, status: CommonStatus) =>
    http.put<void>(`/api/roles/${id}/status`, { status }),
  /** 绑定权限（权限点 auth:role:assign-permission） */
  assignRolePermissions: (id: string, permissionIds: string[]) =>
    http.put<void>(`/api/roles/${id}/permissions`, { permissionIds }),

  // 权限（只读列表）
  permissions: (query: PermissionQuery) =>
    http.get<PageResult<PermissionItem>>('/api/permissions', { params: query }),

  // 部门（全量树）
  departmentTree: () => http.get<DepartmentNode[]>('/api/departments'),
  createDepartment: (payload: DepartmentPayload) =>
    http.post<DepartmentNode>('/api/departments', payload),
  updateDepartment: (id: string, payload: DepartmentPayload) =>
    http.put<void>(`/api/departments/${id}`, payload),
  setDepartmentStatus: (id: string, status: CommonStatus) =>
    http.put<void>(`/api/departments/${id}/status`, { status }),
}
