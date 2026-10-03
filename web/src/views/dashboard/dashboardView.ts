import type { DashboardView } from '@/api/dashboard'
import { canAccess, type UserInfo } from '@/types/permission'

/**
 * 管理层职能资源（fail-closed 白名单）：报表中心 / 基础资料域任一 view 权限。
 * 码表对齐 config/menu.tsx 两段菜单码与后端冻结码「资源段归一」匹配（types/permission.ts）。
 */
const MANAGEMENT_CODES = [
  'report:view',
  'product:view',
  'sku:view',
  'category:view',
  'unit:view',
  'supplier:view',
  'customer:view',
] as const

/**
 * Dashboard 视图判定（requirements.md §2.1 两套指标分列）：
 * - 超管 / 通配 '*' / 持有管理层职能资源任一权限 → 管理层视图（跨域经营指标）；
 * - 其余一律落仓库人员视图（fail-closed：未登录 / 空权限 / 仅作业域权限都不给聚合经营指标，
 *   聚合视角必须显式授权）。
 */
export function resolveDashboardView(user: UserInfo | null): DashboardView {
  return MANAGEMENT_CODES.some((code) => canAccess(user, code)) ? 'management' : 'operator'
}
