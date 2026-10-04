import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

/**
 * 用户域（M1 契约，backend-m1-plan.md §5.4 / §6；JSON tag 以 internal/auth 为准）：
 * /api/users CRUD + 启停 / 重置密码 / 解锁 / 绑定角色。
 * M1 不提供用户删除接口（软删除仅保留 schema 能力，停用即离岗路径）。
 *
 * 字段形态约定（与后端序列化一一对应）：
 * - 响应侧 ID 均为字符串（internal/database/model.go:22 MarshalJSON）；
 * - 请求侧 department_id/role_ids/warehouse_ids 为 *int64/[]int64（internal/auth/service_rbac.go:46-67），
 *   必须传 JSON 数字——Go int64 反序列化不接受字符串形态；
 * - 响应字段名与 UserView JSON tag 全量对齐（internal/auth/service_auth.go:70-87）。
 */

/** 用户状态（users 表 chk_users_status，迁移 000001：ACTIVE/DISABLED） */
export type UserStatus = 'ACTIVE' | 'DISABLED'

/** 通用启停状态（roles/departments/permissions 表 chk_*_status：ENABLED/DISABLED，
 * 与 users 的 ACTIVE/DISABLED 是不同枚举，internal/auth/models.go:19-29） */
export type OnOffStatus = 'ENABLED' | 'DISABLED'

/** 数据权限范围（backend-m1-plan.md §7.4） */
export type DataScope = 'ALL' | 'SPECIFIED_WAREHOUSE' | 'DEPARTMENT' | 'SELF' | 'SELF_IN_CHARGE'

export interface UserQuery extends PageQuery {
  /** 关键词：用户名 / 姓名 / 手机号 */
  keyword?: string
  status?: UserStatus
  /** 部门筛选（internal/auth/handler.go:256 c.Query("department_id")） */
  department_id?: string
}

/** 用户视图（internal/auth/service_auth.go UserView：字段名即 JSON tag） */
export interface UserItem {
  /** 后端 database.ID 序列化为字符串 */
  id: string
  username: string
  real_name?: string
  phone?: string
  email?: string
  /** 可空部门引用：null 或 ID 字符串 */
  department_id?: string | null
  data_scope: DataScope
  status: UserStatus
  /** 重置密码后为 true，登录时强制改密 */
  must_change_password?: boolean
  /** 账户锁定截止时间（存在即处于锁定态，可解锁）；JSONTime 零值序列化为 null */
  locked_until?: string | null
  last_login_at?: string | null
  last_login_ip?: string
  /** 仅 GET /api/users/:id 详情返回（service_rbac.go GetUserDetail）——列表项不含该字段 */
  role_ids?: string[]
  /** 仅详情返回（同上）；数据范围=SPECIFIED_WAREHOUSE 时由表单显式绑定 */
  warehouse_ids?: string[]
  created_at?: string | null
  updated_at?: string | null
}

/** 创建用户入参（internal/auth/service_rbac.go UserCreateInput） */
export interface UserCreatePayload {
  username: string
  /** 初始密码（强密码策略：长度 ≥ 8 且含字母 + 数字，backend-m1-plan.md §7.2） */
  password: string
  real_name?: string
  phone?: string
  email?: string
  /** JSON 数字形态（*int64）；缺省不绑部门 */
  department_id?: number
  /** 缺省 SELF 最小授权（service_rbac.go:84-86） */
  data_scope?: DataScope
  /** 全量绑定；缺省不绑角色 */
  role_ids?: number[]
  /** 数据范围为 SPECIFIED_WAREHOUSE 时必须至少一个仓库（service_rbac.go:105-109） */
  warehouse_ids?: number[]
}

/** 更新用户入参（internal/auth/service_rbac.go UserUpdateInput：指针三态——缺省不修改） */
export interface UserUpdatePayload {
  real_name?: string
  phone?: string
  email?: string
  /** 三态：缺省不修改；>0 设置；0 显式清空部门（service_rbac.go:219-225） */
  department_id?: number
  data_scope?: DataScope
  /** 全量替换语义：传即整体替换（service_rbac.go:235-242） */
  role_ids?: number[]
  warehouse_ids?: number[]
}

export const userApi = {
  users: (query: UserQuery) => http.get<PageResult<UserItem>>('/api/users', { params: query }),
  user: (id: string) => http.get<UserItem>(`/api/users/${id}`),
  createUser: (payload: UserCreatePayload) => http.post<UserItem>('/api/users', payload),
  updateUser: (id: string, payload: UserUpdatePayload) => http.put<UserItem>(`/api/users/${id}`, payload),
  /** 启用 / 停用（权限点 auth:user:status） */
  setUserStatus: (id: string, status: UserStatus) =>
    http.put<void>(`/api/users/${id}/status`, { status }),
  /** 重置密码（权限点 auth:user:reset-password；入参 ResetPasswordInput new_password，service_rbac.go:314-317） */
  resetPassword: (id: string, newPassword: string) =>
    http.put<void>(`/api/users/${id}/reset-password`, { new_password: newPassword }),
  /** 账户解锁（权限点 auth:user:unlock，backend-m1-plan.md §7.2） */
  unlock: (id: string) => http.put<void>(`/api/users/${id}/unlock`),
  /** 绑定角色（权限点 auth:user:assign-role；入参 AssignRolesInput role_ids，
   * 全量替换语义——DeleteUserRoles 后 InsertUserRoles，service_rbac.go:379-413） */
  assignRoles: (id: string, roleIds: number[]) =>
    http.put<void>(`/api/users/${id}/roles`, { role_ids: roleIds }),
}
