import type { KeyboardEvent } from 'react'
import type { CountDifference, CountItem, CountOrder, CountScope, CountStatus } from '@/api/count'
import { COUNT_SCOPE_MODE_LABEL } from '@/api/count'
import { toStatusKey } from '@/api/warehouse'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import { formatDateTime, formatNumber } from '@/utils/format'

/**
 * 盘点单五态状态 → SfStatusTag（后端 models.go:143-147 状态机 + types/status.ts 注册表）。
 * 与 api/count.ts CountStatus 同源：DRAFT→草稿 / COUNTING→盘点中 / PENDING_REVIEW→待复核 /
 * COMPLETED→已完成 / CANCELLED→已取消；未知后端值兜底原始文案 + 中性灰（SfStatusTag 内部处理）。
 * 原七态（待执行/待审核）为前端虚构值域已移除（已核实问题 #4：后端只有五态）。
 */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

export function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/**
 * 明细行状态：qty_counted=null=待盘（未登记与「登记为 0」语义不同，inventory-rules.md §9），
 * 有值即已盘。后端 CountItemView 无独立状态字段（handler.go:115-128），前端按口径推导仅用于展示。
 */
export function CountItemStateTag({ item }: { item: Pick<CountItem, 'qty_counted'> }) {
  return item.qty_counted == null ? (
    <SfStatusTag status="pending" label="待盘" semantic="pending" />
  ) : (
    <SfStatusTag status="counted" label="已盘" semantic="success" />
  )
}

/** 差异行状态（db/migrations/000009 chk_count_differences_status：PENDING/APPROVED/REJECTED/EXECUTED，
 * 大写经 toStatusKey 归一后命中注册表 pending/approved/rejected/executed） */
export function CountDiffStatusTag({ status }: { status: string }) {
  return <SfStatusTag status={toStatusKey(status)} />
}

/** 范围 ID → 编码映射（页面经基础资料 options 端点装配；缺失降级 #id，不造假数据） */
export interface ScopeIdMaps {
  zone: Map<string, string>
  shelf: Map<string, string>
  bin: Map<string, string>
  sku: Map<string, string>
}

const SCOPE_ID_LIMIT = 5

/** 范围文案（api/count.ts CountScope.Mode：ALL/ZONE/SHELF/BIN/SKU；明细 ID 经 options 映射编码） */
export function countScopeText(scope: CountScope, maps: ScopeIdMaps): string {
  const label = COUNT_SCOPE_MODE_LABEL[scope.mode] ?? scope.mode
  const ids =
    scope.mode === 'ZONE'
      ? scope.zone_ids
      : scope.mode === 'SHELF'
        ? scope.shelf_ids
        : scope.mode === 'BIN'
          ? scope.bin_ids
          : scope.mode === 'SKU'
            ? scope.sku_ids
            : undefined
  if (!ids || ids.length === 0) return label
  const map =
    scope.mode === 'ZONE'
      ? maps.zone
      : scope.mode === 'SHELF'
        ? maps.shelf
        : scope.mode === 'BIN'
          ? maps.bin
          : maps.sku
  const codes = ids.map((id) => map.get(String(id)) ?? `#${String(id)}`)
  const shown = codes.slice(0, SCOPE_ID_LIMIT).join('、')
  return codes.length > SCOPE_ID_LIMIT ? `${label}：${shown} 等 ${codes.length} 项` : `${label}：${shown}`
}

function pressActivate(onClick: () => void) {
  return (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onClick()
    }
  }
}

export interface CountTaskCardProps {
  order: CountOrder
  /** 仓库编码（options 映射；缺失降级 #id） */
  warehouseCode: string
  /** 范围文案（countScopeText 结果，页面装配） */
  scopeText: string
  /** 横屏左栏当前选中任务高亮（PadPageShell tasksSlot 约定） */
  selected?: boolean
  onClick?: () => void
}

/**
 * 盘点任务卡（frontend.md §20.2 卡片列表 / §20.9 大触摸热区；§10.5 列表字段）：
 * count_no / 仓库 / 范围 / 五态状态 / 冻结时间 / 创建时间。PadTaskCard 原语绑定 TaskItem
 * 类型（api/task.ts），盘点单字段不同，故按同一 pad.css 卡片基元本地实现。
 */
export function CountTaskCard({ order, warehouseCode, scopeText, selected = false, onClick }: CountTaskCardProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={onClick ? pressActivate(onClick) : undefined}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{order.count_no}</span>
        <CountStatusTag status={order.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{warehouseCode}</span>
        <span>{scopeText}</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>冻结：{formatDateTime(order.frozen_at)}</span>
        <span>{formatDateTime(order.created_at)}</span>
      </div>
    </div>
  )
}

export interface CountItemRowProps {
  item: CountItem
  /** SKU 编码（options 映射；缺失降级 SKU #id） */
  skuCode: string
  /** 库位编码（options 映射；缺失降级 #id） */
  binCode: string
  /** 本机暂存草稿数量（仅数量；有则展示草稿角标） */
  draftQty?: number | null
  /** 当前选中行高亮 */
  selected?: boolean
  onClick?: () => void
}

/** 盘点明细行卡：skuCode / binCode / serial_no / qty_system / qty_counted（null=未登记）/ 待盘·已盘 */
export function CountItemRow({ item, skuCode, binCode, draftQty, selected = false, onClick }: CountItemRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={onClick ? pressActivate(onClick) : undefined}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{skuCode}</span>
        <CountItemStateTag item={item} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>库位：{binCode}</span>
        {item.serial_no !== '' && <span>序列号：{item.serial_no}</span>}
        {draftQty != null && <span>草稿 {formatNumber(draftQty)}（未提交）</span>}
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(item.qty_system)}</span>
        <span className="sf-pad-task-card__qty-unit">
          系统数量 · 实盘 {item.qty_counted != null ? formatNumber(item.qty_counted) : '未登记'}
        </span>
      </div>
    </div>
  )
}

export interface CountDifferenceCardProps {
  diff: CountDifference
  /** SKU 编码（options 映射；缺失降级 SKU #id） */
  skuCode: string
  /** 库位编码（options 映射；缺失降级 #id） */
  binCode: string
}

/**
 * 盘点差异行卡（api/count.ts CountDifferenceView；diff_qty 盘盈为正，非零差异以警示色
 * 突出、不做红绿好坏判断，同 PC diffCell 口径）：差异经复核后由系统生成库存调整单
 * （adjust_no 回写），说明随审核意见提交，Pad 端只读展示。
 */
export function CountDifferenceCard({ diff, skuCode, binCode }: CountDifferenceCardProps) {
  const hasDiff = diff.diff_qty !== 0
  return (
    <div className="sf-pad-task-card" style={{ cursor: 'default' }}>
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">
          第 {diff.line_no} 行 · {skuCode}
        </span>
        <CountDiffStatusTag status={diff.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>库位：{binCode}</span>
        {diff.adjust_no !== '' && <span>调整单：{diff.adjust_no}</span>}
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric" style={hasDiff ? { color: 'var(--sf-warning)' } : undefined}>
          {diff.diff_qty > 0 ? '+' : ''}
          {formatNumber(diff.diff_qty)}
        </span>
        <span className="sf-pad-task-card__qty-unit">
          系统 {formatNumber(diff.qty_system)} · 实盘 {formatNumber(diff.qty_counted)}
        </span>
      </div>
      {diff.remark !== '' && <div className="sf-pad-task-card__meta"><span>说明：{diff.remark}</span></div>}
    </div>
  )
}
