import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { OnOffStatus } from './user'

/**
 * RBAC / 组织域（M1 契约，backend-m1-plan.md §5.4；JSON tag 以 internal/auth 为准）：
 * /api/roles CRUD + 绑定权限、GET /api/permissions（只读）、/api/departments CRUD（分页平铺 + 前端组树）。
 * roles / departments 无删除接口——一律走 status 停用（§5.4.1 动作清单）。
 *
 * 字段形态约定（与后端序列化一一对应）：
 * - 响应侧 ID 均为字符串（internal/database/model.go:22 MarshalJSON）；
 * - 请求侧 permission_ids/parent_id 为 []int64/*int64（internal/auth/service_rbac.go:646-648/747/810），
 *   必须传 JSON 数字；
 * - roles/departments/permissions 状态枚举为 ENABLED/DISABLED（internal/auth/models.go:27-28，
 *   迁移 000001 chk_*_status）——与 users 的 ACTIVE/DISABLED 不同。
 */

// ---------- 角色 ----------

export interface RoleQuery extends PageQuery {
  keyword?: string
  status?: OnOffStatus
}

/** 角色视图（internal/auth/service_auth.go RoleView：字段名即 JSON tag） */
export interface RoleItem {
  /** 后端 database.ID 序列化为字符串 */
  id: string
  code: string
  name: string
  /** 内置角色（is_system）：禁删禁停用（service_rbac.go:619-621） */
  is_system: boolean
  status: OnOffStatus
  created_at?: string | null
  updated_at?: string | null
}

/** 角色详情：含已绑定权限点 ID 集合与用户数（service_rbac.go GetRoleDetail） */
export interface RoleDetail extends RoleItem {
  permission_ids: string[]
  user_count?: number
}

export interface RoleCreatePayload {
  code: string
  name: string
}

/** 更新角色：code 不可变（RoleUpdateInput 仅收 name，service_rbac.go:568-570） */
export interface RoleUpdatePayload {
  name: string
}

// ---------- 权限（只读） ----------

export type PermissionType = 'MENU' | 'BUTTON' | 'API'

export interface PermissionQuery extends PageQuery {
  keyword?: string
  type?: PermissionType
  status?: OnOffStatus
}

/** 权限点视图（internal/auth/service_auth.go PermissionView——无 created_at/updated_at 字段） */
export interface PermissionItem {
  id: string
  code: string
  name: string
  type: PermissionType
  parent_id?: string | null
  sort: number
  status: OnOffStatus
}

// ---------- 部门（分页平铺 + 前端组树） ----------

export interface DepartmentQuery extends PageQuery {
  keyword?: string
  status?: OnOffStatus
}

/** 部门平铺项（GET /api/departments 分页信封内条目 = DepartmentView，无 children，
 * internal/auth/service_auth.go:130-138 / handler.go:593-615） */
export interface DepartmentItem {
  id: string
  parent_id?: string | null
  code: string
  name: string
  status: OnOffStatus
  created_at?: string | null
  updated_at?: string | null
}

/** 前端按 parent_id 组树后的节点（children 由前端构建，后端不返回） */
export interface DepartmentNode extends DepartmentItem {
  children?: DepartmentNode[]
}

export interface DepartmentCreatePayload {
  /** JSON 数字形态（*int64）；缺省为顶级部门 */
  parent_id?: number
  code: string
  name: string
}

/** 更新部门：code 不可变（DeptUpdateInput 仅收 parent_id/name，service_rbac.go:809-812）；
 * parent_id 三态——缺省不修改、>0 迁移、0 提升为顶级（service_rbac.go:827-868） */
export interface DepartmentUpdatePayload {
  parent_id?: number
  name: string
}

// ---------- 全量拉取与组树辅助 ----------

/** 后端单页上限（internal/response/response.go:47 MaxPageSize） */
export const MAX_PAGE_SIZE = 100

/** 循环拉取全量平铺数据（下拉/组树/选项等一次取全场景；上限 100 页兜底，防脏数据死循环）。
 * 导出供其他域模块「取全」场景复用（如用户表单的仓库绑定选项——单页 100 不保证取全） */
export async function fetchAllPaged<T extends { id: string | number }>(
  fetchPage: (page: number) => Promise<PageResult<T>>,
): Promise<T[]> {
  const all: T[] = []
  for (let page = 1; page <= 100; page += 1) {
    const result = await fetchPage(page)
    all.push(...result.items)
    if (all.length >= result.total || result.items.length === 0) return all
  }
  return all
}

/** 平铺部门 → 树：按 parent_id 归组；父级缺失（隔离脏数据）时按根节点处理 */
function buildDepartmentTree(items: DepartmentItem[]): DepartmentNode[] {
  const nodes = new Map<string, DepartmentNode>()
  for (const item of items) nodes.set(item.id, { ...item })
  const roots: DepartmentNode[] = []
  for (const item of items) {
    const node = nodes.get(item.id)
    if (!node) continue
    const parent = item.parent_id ? nodes.get(item.parent_id) : undefined
    if (parent && parent !== node) {
      if (parent.children) parent.children.push(node)
      else parent.children = [node]
    } else {
      roots.push(node)
    }
  }
  return roots
}

export const rbacApi = {
  // 角色
  roles: (query: RoleQuery) => http.get<PageResult<RoleItem>>('/api/roles', { params: query }),
  /** 角色全量（分配角色弹窗选项 / 角色 ID→名称映射等一次取全场景；
   * 角色数可超单页上限 100，单页拉取会静默截断） */
  rolesAll: (): Promise<RoleItem[]> =>
    fetchAllPaged((page) => rbacApi.roles({ page, pageSize: MAX_PAGE_SIZE })),
  role: (id: string) => http.get<RoleDetail>(`/api/roles/${id}`),
  createRole: (payload: RoleCreatePayload) => http.post<RoleItem>('/api/roles', payload),
  updateRole: (id: string, payload: RoleUpdatePayload) => http.put<RoleItem>(`/api/roles/${id}`, payload),
  setRoleStatus: (id: string, status: OnOffStatus) =>
    http.put<void>(`/api/roles/${id}/status`, { status }),
  /** 绑定权限（权限点 auth:role:assign-permission；入参 AssignPermissionsInput permission_ids，
   * 全量替换语义——ReplaceRolePermissions，service_rbac.go:645-690） */
  assignRolePermissions: (id: string, permissionIds: number[]) =>
    http.put<void>(`/api/roles/${id}/permissions`, { permission_ids: permissionIds }),

  // 权限（只读分页列表）
  permissions: (query: PermissionQuery) =>
    http.get<PageResult<PermissionItem>>('/api/permissions', { params: query }),
  /** 权限点全量（权限树等一次取全场景；M1 种子 106 点 > 单页上限 100） */
  permissionsAll: (): Promise<PermissionItem[]> =>
    fetchAllPaged((page) => rbacApi.permissions({ page, pageSize: MAX_PAGE_SIZE })),

  // 部门（分页平铺）
  departments: (query: DepartmentQuery) =>
    http.get<PageResult<DepartmentItem>>('/api/departments', { params: query }),
  /** 全量部门组树：循环拉取分页信封后按 parent_id 组树（部门为组织级小数据量） */
  departmentTree: (): Promise<DepartmentNode[]> =>
    fetchAllPaged((page) => rbacApi.departments({ page, pageSize: MAX_PAGE_SIZE })).then(buildDepartmentTree),

  createDepartment: (payload: DepartmentCreatePayload) =>
    http.post<DepartmentItem>('/api/departments', payload),
  updateDepartment: (id: string, payload: DepartmentUpdatePayload) =>
    http.put<DepartmentItem>(`/api/departments/${id}`, payload),
  setDepartmentStatus: (id: string, status: OnOffStatus) =>
    http.put<void>(`/api/departments/${id}/status`, { status }),
}
