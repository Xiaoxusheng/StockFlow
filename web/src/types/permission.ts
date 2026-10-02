/**
 * 用户与权限模型（docs/permission.md）
 * 前端权限仅是体验优化，一切以后端校验为准。
 */

export interface UserInfo {
  id: number | string
  username: string
  realName?: string
  avatarUrl?: string
  /** 角色编码，如 super_admin / warehouse_manager */
  roles: string[]
  /** 权限点编码，如 inventory:stock:view */
  permissions: string[]
}

/**
 * 权限判断：
 * - 超级管理员 / 通配权限直接放行；
 * - 权限点未下发（空列表）时放行——后端未接入期间保持基础菜单可见；
 *   后端接入后该策略由真实权限数据替代。
 */
export function canAccess(user: UserInfo | null, code?: string): boolean {
  if (!code) return true
  if (!user) return false
  if (user.roles.includes('super_admin')) return true
  if (user.permissions.includes('*')) return true
  if (user.permissions.length === 0) return true
  return user.permissions.includes(code)
}
