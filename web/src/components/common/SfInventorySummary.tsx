import { Card, Flex, Skeleton, Statistic } from 'antd'
import type { CSSProperties } from 'react'
import { SfError } from './SfError'
import { formatNumber } from '@/utils/format'

/** 语义色调（一律走 --sf-* Design Token，禁止页面写死色值） */
export type SfInventorySummaryTone =
  | 'default'
  | 'primary'
  | 'success'
  | 'info'
  | 'warning'
  | 'danger'

const TONE_COLOR: Record<SfInventorySummaryTone, string> = {
  default: 'var(--sf-text)',
  primary: 'var(--sf-primary)',
  success: 'var(--sf-success)',
  info: 'var(--sf-info)',
  warning: 'var(--sf-warning)',
  danger: 'var(--sf-danger)',
}

export interface SfInventorySummaryItem {
  label: string
  value?: number
  /** 语义色调：可用=success、锁定=info、冻结=warning、临期/异常=danger 等；缺省主文字色 */
  tone?: SfInventorySummaryTone
  /**
   * 数值为 0 时弱化为 muted（「非零着色、零值退后」）：0 的语义位没有信息量，
   * 着色只会制造假警；语义统计位建议开启，身份指标（SKU 数/总量）不开。
   */
  mutedWhenZero?: boolean
}

export interface SfInventorySummaryProps {
  /** 统计项清单（数值由页面从真实 API 下发，组件不做任何加工口径） */
  items: SfInventorySummaryItem[]
  loading?: boolean
  error?: unknown
  onRetry?: () => void
  style?: CSSProperties
}

/**
 * 库存域统计条（frontend.md §10.2 统计行 / §5「不做巨大 KPI 卡片」）：
 * loading → 骨架、error → 统一错误态、就绪 → 分隔式 Statistic；数字统一 formatNumber。
 * 颜色层级：语义位按 tone 着色 + mutedWhenZero 零值弱化，形成「主指标黑 / 正常绿 /
 * 占用蓝 / 风险橙红 / 零值灰」的阅读层级，克制不彩条（Neutral ≥85% 原则）。
 */
export function SfInventorySummary({ items, loading = false, error, onRetry, style }: SfInventorySummaryProps) {
  return (
    <Card size="small" styles={{ body: { padding: '12px 16px' } }} style={style}>
      {loading ? (
        <Flex gap={32}>
          {items.map((item) => (
            <Skeleton.Node active key={item.label} style={{ width: 64, height: 40 }} />
          ))}
        </Flex>
      ) : error ? (
        <SfError error={error} onRetry={onRetry} />
      ) : (
        <Flex wrap="wrap" style={{ width: '100%' }}>
          {items.map((item, index) => {
            const tone = item.tone ?? 'default'
            const muted = item.mutedWhenZero && item.value === 0
            const isFirst = index === 0
            const isLast = index === items.length - 1
            return (
              <Statistic
                key={item.label}
                title={item.label}
                value={formatNumber(item.value ?? 0)}
                valueStyle={{
                  // 数字 tabular-nums：多指标并排时位数/小数点成列对齐，扫读更快
                  fontSize: 20,
                  fontWeight: 600,
                  lineHeight: '28px',
                  fontVariantNumeric: 'tabular-nums',
                  color: muted ? 'var(--sf-text-muted)' : TONE_COLOR[tone],
                }}
                style={{
                  // 等分铺满整行（消除右半边空白）；首尾项贴边，与卡片内容区左/右缘对齐
                  flex: '1 1 0',
                  minWidth: 104,
                  paddingLeft: isFirst ? 0 : 16,
                  paddingRight: isLast ? 0 : 16,
                  borderRight: isLast ? undefined : '1px solid var(--sf-border-subtle)',
                }}
              />
            )
          })}
        </Flex>
      )}
    </Card>
  )
}
