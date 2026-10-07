import { useMemo, useState } from 'react'
import { Button, Space, Tooltip } from 'antd'
import { useNavigate } from 'react-router'
import {
  RELATIONS,
  type RelationContext,
  type RelationEntity,
  type RelationItem,
  type RelationListSpec,
} from '@/config/relations'
import { SfRelationListDrawer } from '@/components/common/SfRelationListDrawer'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'

export interface SfRelationNavProps {
  /** 实体类型（RELATIONS 注册表键） */
  entity: RelationEntity
  /**
   * 当前详情页可用业务标识（如 {sku_id, sku_code}）。
   * 注册表项 `to(ctx)`/`list(ctx)` 返回 null 的（缺必需参数）不渲染——不造假入口。
   */
  context: RelationContext
  /** 页面特有关联项（追加到注册表之后，同受权限过滤） */
  extra?: RelationItem[]
  /** 全部关联项均不可达时的提示（默认中性文案） */
  emptyText?: string
}

/**
 * 详情页关联业务导航（计划 §2.6，frontend.md §10.3/§10.4）：
 * 于 SfDetailSection extra 位渲染 chip 组。点击**优先打开 Drawer 内嵌列表**
 * （`list` 配置——不离开当前页，ask 冻结口径）；未配 list 或上下文缺必需参数时
 * 回退为带 query 预填跳转目标列表页（URL 承载筛选，可直接回退）。
 * 权限经 canAccess fail-closed 过滤——无权限项不渲染（不置灰：置灰会暴露权限模型
 * 且与菜单过滤口径不一致）。
 *
 * 一期口径：内嵌/跳转均落既有列表端点与既有列表路由（不新建聚合端点，计划 §2.6 明确禁止）；
 * 目标列表是否支持该过滤参数由注册表逐项核实（relations.tsx 顶部注释列了实读白名单）。
 */
export function SfRelationNav({ entity, context, extra, emptyText = '暂无可跳转的关联业务' }: SfRelationNavProps) {
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  // 当前打开的内嵌列表（null=Drawer 关闭）；spec 由点击时工厂求值固化——
  // 关闭再点其他项经 key 重挂载重置分页（SfRelationListDrawer 约定）
  const [active, setActive] = useState<{ key: string; spec: RelationListSpec } | null>(null)

  const resolved = useMemo(() => {
    const definitions = [...(RELATIONS[entity] ?? []), ...(extra ?? [])]
    return definitions
      .filter((item) => canAccess(user, item.permission))
      .map((item) => ({ item, to: item.to(context), list: item.list?.(context) ?? null }))
      .filter((entry) => entry.to !== null || entry.list !== null)
  }, [entity, context, extra, user])

  if (resolved.length === 0) {
    return (
      <span style={{ color: 'var(--sf-text-muted)', fontSize: 'var(--sf-font-size-caption)' }}>
        {emptyText}
      </span>
    )
  }

  // 关联按钮配色（用户口径：不同颜色边框区分业务，与系统按钮同尺寸）：
  // 色板轮转——同页按钮颜色错开、同一业务入口颜色稳定（index 由注册表顺序决定）；
  // 边框/文字同色、白底，不引入底色填充（克制口径）。
  const PALETTE = [
    'var(--sf-primary)',
    'var(--sf-success)',
    'var(--sf-info)',
    'var(--sf-warning)',
    'var(--sf-danger)',
  ]

  return (
    <>
      <Space size={8} wrap>
        {resolved.map(({ item, to, list }, index) => {
          const color = PALETTE[index % PALETTE.length]
          return (
            <Tooltip key={item.key} title={item.hint}>
              <Button
                style={{ borderColor: color, color, background: 'transparent' }}
                onClick={() => {
                  if (list) {
                    // 优先 Drawer 内嵌：不离开当前页（ask 冻结口径）
                    setActive({ key: item.key, spec: list })
                    return
                  }
                  if (to) navigate(to)
                }}
              >
                {item.label}
              </Button>
            </Tooltip>
          )
        })}
      </Space>
      <SfRelationListDrawer
        key={active?.key ?? 'closed'}
        open={active !== null}
        spec={active?.spec ?? null}
        onClose={() => setActive(null)}
      />
    </>
  )
}
