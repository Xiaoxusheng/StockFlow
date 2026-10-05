/**
 * 用户与权限模型（docs/permission.md）
 * 前端权限仅是体验优化，一切以后端校验为准（permission.md §5）。
 *
 * 字段对齐后端冻结契约（JSON tag 为准，禁止前端自造 snake_case 之外的拼写）：
 * - 用户视图：internal/auth/service_auth.go UserView（database.ID 序列化为字符串）；
 * - 权限点集：GET /api/auth/me 的 MeResult.permissions（service_auth.go MeResult），
 *   编码为三段冻结码「域:资源:动作」（internal/auth/permissions.go）。
 */
export interface UserInfo {
  /** 后端 database.ID JSON 为字符串形态（internal/database/model.go:22-24） */
  id: number | string
  username: string
  real_name?: string
  phone?: string
  email?: string
  department_id?: number | string | null
  data_scope?: string
  status?: string
  must_change_password?: boolean
  /** JSONTime 序列化 "YYYY-MM-DD HH:mm:ss"，零值为 null（internal/database/model.go JSONTime.MarshalJSON） */
  locked_until?: string | null
  last_login_at?: string | null
  last_login_ip?: string
  created_at?: string | null
  updated_at?: string | null
  /** 超级管理员（MeResult.is_super，service_auth.go MeResult 顶层字段）：canAccess 直通的真实数据源 */
  is_super?: boolean
  /** 角色编码（如 super_admin）。/api/auth/me 不返回角色，仅 DEV 旁路与扩展使用 */
  roles?: string[]
  /** 权限点集（三段冻结码，permissions.go）；随 /api/auth/me 组装进会话 */
  permissions?: string[]
}

/**
 * 菜单码资源段 → 后端冻结码资源段别名（M1 冻结清单 internal/auth/permissions.go）。
 * 菜单两段/三段码与后端三段码的域前缀暂不一致（如 system:user:view ↔ auth:user:list、
 * inventory:stock:view ↔ inventory:inventory:list），M1 清单内资源段全局唯一，
 * 故按「资源段归一 + 动作映射」匹配；菜单编码统一对齐冻结码后此表即可删除。
 */
const RESOURCE_ALIASES: Record<string, readonly string[]> = {
  stock: ['stock', 'inventory'],
  department: ['department', 'dept'],
}

/**
 * 跨域同名资源段的域段限定（matchBackendPermission 忽略域段的补充校验）：
 * 「资源:动作」两段匹配建立在 M1 清单资源段全局唯一的假设上；当同一资源段出现在
 * 多个后端域、或菜单指向的资源域尚未立项时，必须限定允许的后端域段，否则会误亮。
 * - task 条目已摘除（2026-10-05 平台批）：GET /api/tasks 交付并挂 inventory:inventory:list
 *   （internal/reports/routes.go:115），菜单 /tasks 改挂 inventory:stock:view——经
 *   RESOURCE_ALIASES stock→['stock','inventory'] 归一命中 inventory 域冻结码，原
 *   「task 域未立项」的 fail-closed 限定失义；task 资源段在 M1 清单仍属 printing 域
 *   （printing:task:*），但已无菜单码指向该资源段，无误亮面。后续如再出现跨域同名
 *   资源段，按原规则补条目即可。
 * - 既有别名映射（stock/department）资源段全局唯一，不受影响。
 */
const DOMAIN_BOUND_RESOURCES: Record<string, readonly string[]> = {}

/**
 * 菜单码与后端权限码的归一匹配（集中在本文件，不改 config/menu.tsx）：
 * - 取菜单码末两段为「资源段:动作段」（两段/三段码均适用，域前缀忽略）；
 * - 权限码必须为三段且资源段命中（含别名）；
 * - 动作段 view 为「资源可见」语义：持有该资源任意动作（list/read/create/…）即放行，
 *   避免「仅持 create 的人看不到菜单入口」的体验断裂；非 view 动作精确匹配。
 */
function matchBackendPermission(perms: string[], menuCode: string): boolean {
  const segs = menuCode.split(':')
  if (segs.length < 2) return false
  const resource = segs[segs.length - 2]
  const action = segs[segs.length - 1]
  const candidates = RESOURCE_ALIASES[resource] ?? [resource]
  const allowedDomains = DOMAIN_BOUND_RESOURCES[resource]
  return perms.some((perm) => {
    const parts = perm.split(':')
    if (parts.length !== 3) return false
    if (allowedDomains && !allowedDomains.includes(parts[0])) return false
    if (!candidates.includes(parts[1])) return false
    return action === 'view' ? true : parts[2] === action
  })
}

/**
 * 权限判断（真实权限过滤）：
 * - 无权限要求的菜单（code 缺省，如 Dashboard）放行；
 * - 超级管理员（is_super / super_admin 角色）与通配权限 '*' 直通；
 * - 权限点集为空一律拒绝（fail-closed：后端接入后空权限 = 未授权任何资源）；
 * - 其余按「资源段归一 + 动作映射」对三段冻结码匹配（matchBackendPermission）。
 */
export function canAccess(user: UserInfo | null, code?: string): boolean {
  if (!code) return true
  if (!user) return false
  if (user.is_super === true || user.roles?.includes('super_admin')) return true
  const perms = user.permissions ?? []
  if (perms.includes('*')) return true
  if (perms.length === 0) return false
  if (perms.includes(code)) return true
  return matchBackendPermission(perms, code)
}
