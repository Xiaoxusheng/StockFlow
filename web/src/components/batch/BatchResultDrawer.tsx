import { Alert, Button, Drawer, Space, Typography } from 'antd'
import { RedoOutlined } from '@ant-design/icons'
import type { BatchResult, BatchResultItemStatus } from '@/api/printing'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Text } = Typography

// ---------- 批量结果统一抽屉（计划 §2.7，docs/plans/2026-10-06-efficiency-layer-phase1.md） ----------
// 契约：{total, success_count, failed_count, skipped_count, results:[{id, status, reason}]}
// （类型定义在 api/printing.ts BatchResult——全站唯一契约源，api.md §9 冻结）。
// ⚠️ 全站唯一批量结果抽屉：打印/批量领取等所有批量提交点一律复用本组件，禁止各页自写结果弹窗。

export interface BatchResultDrawerProps {
  open: boolean
  /** 批量结果（null 时不渲染内容） */
  result: BatchResult | null
  /** 重试回调：仅传失败项 id（成功项绝不重跑；skipped 不重跑）——重试语义=调用方以失败 ids 重新发起同一批量端点 */
  onRetry?: (failedIds: string[]) => void
  /** 重试进行中（按钮 loading+disabled） */
  retrying?: boolean
  onClose: () => void
}

/** 逐条结果状态 → SfStatusTag（三态语义色，frontend.md §24；skipped=幂等命中/已处目标态） */
const ITEM_STATUS_META: Record<BatchResultItemStatus, { label: string; semantic: 'success' | 'danger' | 'warning' }> = {
  success: { label: '成功', semantic: 'success' },
  failed: { label: '失败', semantic: 'danger' },
  skipped: { label: '已跳过', semantic: 'warning' },
}

/**
 * 统一批量结果抽屉（§2.7）：计数条 + 逐条列表 + SfStatusTag 三态 + 【仅重试失败】。
 * 验收场景 4（批量打印 100→96/3/1，仅重试失败 3 张）的统一承载面；
 * 批量领取（putaway/picks/checks batch-claim）接线复用同一组件。
 */
export function BatchResultDrawer({ open, result, onRetry, retrying, onClose }: BatchResultDrawerProps) {
  const failedIds = (result?.results ?? [])
    .filter((item) => item.status === 'failed')
    .map((item) => item.id)

  return (
    <Drawer
      title="批量处理结果"
      open={open}
      onClose={onClose}
      width={520}
      destroyOnHidden
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          {onRetry && failedIds.length > 0 && (
            <Button
              type="primary"
              icon={<RedoOutlined />}
              loading={retrying}
              onClick={() => onRetry(failedIds)}
            >
              仅重试失败（{failedIds.length}）
            </Button>
          )}
        </div>
      }
    >
      {result && (
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          {/* 计数条：成功 / 失败 / 跳过 三态对账（Σ=total，api.md §9 批量契约） */}
          <div
            style={{
              display: 'flex',
              gap: 'var(--sf-space-4)',
              alignItems: 'center',
              padding: 'var(--sf-space-2) var(--sf-space-3)',
              background: 'var(--sf-bg-layout, rgba(0,0,0,0.02))',
              borderRadius: 'var(--sf-radius-md)',
            }}
          >
            <Text>共 {result.total} 条</Text>
            <span><SfStatusTag label={`成功 ${result.success_count}`} semantic="success" /></span>
            <span><SfStatusTag label={`失败 ${result.failed_count}`} semantic="danger" /></span>
            <span><SfStatusTag label={`跳过 ${result.skipped_count}`} semantic="warning" /></span>
          </div>

          {result.failed_count > 0 && (
            <Alert
              type="warning"
              showIcon
              message="存在失败项：重试仅重新提交失败对象，成功项绝不重跑"
            />
          )}

          {/* 逐条列表（后端 results 全量返回；500 行内逐条渲染，超长由滚动承载） */}
          <div style={{ maxHeight: 420, overflowY: 'auto' }}>
            {result.results.map((item, index) => {
              const meta = ITEM_STATUS_META[item.status]
              return (
                <div
                  key={`${item.id}-${index}`}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--sf-space-2)',
                    padding: '6px 0',
                    borderBottom: '1px solid var(--sf-border-subtle)',
                  }}
                >
                  <Text style={{ minWidth: 0 }} ellipsis={{ tooltip: item.id }}>
                    {item.id}
                  </Text>
                  <Space size={8} style={{ flexShrink: 0 }}>
                    {item.reason && (
                      <Text type="secondary" style={{ fontSize: 12 }} ellipsis={{ tooltip: item.reason }}>
                        {item.reason}
                      </Text>
                    )}
                    <SfStatusTag label={meta.label} semantic={meta.semantic} />
                  </Space>
                </div>
              )
            })}
          </div>
        </Space>
      )}
    </Drawer>
  )
}
