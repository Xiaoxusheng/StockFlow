import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

/**
 * 列表页增删改按钮权限（permission.md §5：前端权限是体验优化，后端必须校验）。
 *
 * 传入资源段（如 'product'），内部按两段码 `${resource}:{action}` 判断——经
 * types/permission.ts 的 matchBackendPermission「资源段归一 + 动作映射」命中后端三段冻结码：
 *   product:create   → masterdata:product:create
 *   warehouse:update → warehouse:warehouse:update
 *   department:update→ auth:dept:update（RESOURCE_ALIASES department → ['department','dept']）
 *
 * 后端无该动作码时 fail-closed 返回 false，按钮自动隐藏，页面无需特判
 * （如 category / unit 后端只冻结了 list/read/create/update/status，无 delete，
 *  则 canDelete 恒 false，删除入口自然消失）。
 */
export interface CrudPermissions {
  canCreate: boolean
  canUpdate: boolean
  canDelete: boolean
  canStatus: boolean
}

export function useCrudPermissions(resource: string): CrudPermissions {
  const user = useAuthStore((s) => s.user)
  return {
    canCreate: canAccess(user, `${resource}:create`),
    canUpdate: canAccess(user, `${resource}:update`),
    canDelete: canAccess(user, `${resource}:delete`),
    canStatus: canAccess(user, `${resource}:status`),
  }
}
