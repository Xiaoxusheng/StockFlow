import { useMemo } from 'react'
import { Button, Space, Tooltip } from 'antd'
import { useNavigate } from 'react-router'
import {
  RELATIONS,
  type RelationContext,
  type RelationEntity,
  type RelationItem,
} from '@/config/relations'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

export interface SfRelationNavProps {
  /** 实体类型（RELATIONS 注册表键） */
  entity: RelationEntity
  /**
   * 当前详情页可用业务标识（如 {sku_id, sku_code}）。
   * 注册表项 `to(ctx)` 返回 null 的（缺必需参数）不渲染——不造假入口。
   */
  context: RelationContext
  /** 页面特有关联项（追加到注册表之后，同受权限过滤） */
  extra?: RelationItem[]
  /** 全部关联项均不可达时的提示（默认中性文案） */
  emptyText?: string
}

/**
 * 详情页关联业务导航（计划 §2.6，frontend.md §10.3/§10.4）：
 * 于 SfDetailSection extra 位渲染 chip 组，点击**带 query 预填跳转**目标列表页，
 * 不强制离开当前页的语义由「新页签式跳转」自然满足（列表页筛选由 URL 承载，
 * 用户可直接回退）。权限经 canAccess fail-closed 过滤——无权限项不渲染（不置灰：
 * 置灰会暴露权限模型且与菜单过滤口径不一致）。
 *
 * 一期口径：跳转均落在既有列表路由（不新建聚合端点，计划 §2.6 明确禁止）；
 * 目标列表是否支持该过滤参数由注册表逐项核实（relations.tsx 顶部注释列了实读白名单）。
 */
export function SfRelationNav({ entity, context, extra, emptyText = '暂无可跳转的关联业务' }: SfRelationNavProps) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)

  const resolved = useMemo(() => {
    const definitions = [...(RELATIONS[entity] ?? []), ...(extra ?? [])]
    return definitions
      .filter((item) => canAccess(user, item.permission))
      .map((item) => ({ item, to: item.to(context) }))
      .filter((entry): entry is { item: RelationItem; to: string } => entry.to !== null)
  }, [entity, context, extra, user])

  if (resolved.length === 0) {
    return (
      <span style={{ color: 'var(--sf-text-muted)', fontSize: 'var(--sf-font-size-caption)' }}>
        {emptyText}
      </span>
    )
  }

  return (
    <Space size={4} wrap>
      {resolved.map(({ item, to }) => (
        <Tooltip key={item.key} title={item.hint}>
          <Button size="small" onClick={() => navigate(to)}>
            {item.label}
          </Button>
        </Tooltip>
      ))}
    </Space>
  )
}
