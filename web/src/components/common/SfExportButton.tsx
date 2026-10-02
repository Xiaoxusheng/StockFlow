import { useMemo } from 'react'
import { Button } from 'antd'
import { ExportOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

export interface SfExportButtonProps {
  /**
   * 按钮级权限码（三段冻结码）：经 types/permission canAccess 消费会话权限快照过滤，
   * fail-closed——未登录 / 权限快照为空 / 未命中一律不渲染（permission.md §5：
   * 前端权限仅是体验优化，后端仍须校验）。
   */
  permission?: string
  /** 导出业务模块（api/data.ts EXPORT_MODULE_OPTIONS 值集，如 INVENTORY） */
  module: string
  /**
   * 范围参数：列表当前筛选（warehouse_id / sku_id 等后端认领参数），
   * 跳转 /data/exports 时并入 URL query，由导出任务页读取并预填导出表单。
   * 仅写入 string/number 值；undefined / null / 空串的项不写入。
   */
  scopeParams?: Record<string, unknown>
  /** 按钮文案，默认「导出」 */
  label?: string
}

function buildExportSearch(module: string, scopeParams: SfExportButtonProps['scopeParams']): string {
  const search = new URLSearchParams()
  search.set('type', module)
  for (const [key, value] of Object.entries(scopeParams ?? {})) {
    if (typeof value !== 'string' && typeof value !== 'number') continue
    const text = String(value).trim()
    if (text === '') continue
    search.set(key, text)
  }
  return search.toString()
}

/**
 * 统一导出按钮（frontend.md §5 按钮级权限的首次真实消费）：
 * - 权限过滤：permission 经 canAccess（stores/auth 会话 user + 冻结权限快照）fail-closed；
 * - 真实行为：点击跳转数据中心导出任务页 /data/exports，query 携带 type（业务模块）
 *   与 scopeParams（范围参数），由 ExportTaskPage 预填导出表单——不弹 Toast、不造假入口；
 * - 仅在列表有真实数据源（端点已交付）时接入：后端无导出契约的按钮位应直接移除。
 */
export function SfExportButton({ permission, module, scopeParams, label = '导出' }: SfExportButtonProps) {
  const navigate = useNavigate()
  const user = useAuthStore((state) => state.user)
  const allowed = useMemo(() => canAccess(user, permission), [user, permission])

  if (!allowed) return null

  const search = buildExportSearch(module, scopeParams)
  return (
    <Button icon={<ExportOutlined />} onClick={() => navigate(`/data/exports?${search}`)}>
      {label}
    </Button>
  )
}
