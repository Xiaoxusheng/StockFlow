import { Alert, Button, Drawer, Space, Table, Typography } from 'antd'
import { RedoOutlined } from '@ant-design/icons'
import type { TableProps } from 'antd'
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

/** 逐条结果列（对象 ID + 三态标签；完整原因经行展开查看） */
const RESULT_COLUMNS: TableProps<BatchResultItemShape>['columns'] = [
  {
    title: '对象',
    dataIndex: 'id',
    ellipsis: { showTitle: false },
    render: (id: string) => (
      <Text style={{ fontFamily: 'var(--sf-font-family-mono, monospace)' }} ellipsis={{ tooltip: id }}>
        {id}
      </Text>
    ),
  },
  {
    title: '结果',
    dataIndex: 'status',
    width: 88,
    align: 'right',
    render: (_: unknown, record) => {
      const meta = ITEM_STATUS_META[record.status]
      return <SfStatusTag label={meta.label} semantic={meta.semantic} />
    },
  },
]

/** 行形状（BatchResultItem 同构；就地声明避免与 api 类型循环依赖的导出面扩大） */
type BatchResultItemShape = BatchResult['results'][number]

/**
 * 统一批量结果抽屉（§2.7）：计数条 + 逐条列表 + SfStatusTag 三态 + 【仅重试失败】。
 * 失败行（含带原因的跳过行）**可展开查看完整原因**（错误码支持复制，便于对照错误码表排查）。
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
              message="存在失败项：点击行首箭头展开查看原因；重试仅重新提交失败对象，成功项绝不重跑"
            />
          )}

          {/* 逐条列表（后端 results 全量返回；失败/带原因行可展开看完整 reason——ask 验收口径） */}
          <Table<BatchResultItemShape>
            size="small"
            rowKey="id"
            columns={RESULT_COLUMNS}
            dataSource={result.results}
            pagination={
              result.results.length > 20
                ? { pageSize: 20, size: 'small', showSizeChanger: false, showTotal: (t) => `共 ${t} 条` }
                : false
            }
            expandable={{
              // 失败行必可展开；skipped 亦可能带原因（幂等命中/已处目标态说明）
              rowExpandable: (record) => record.status === 'failed' || Boolean(record.reason),
              expandedRowRender: (record) =>
                record.reason ? (
                  <Typography.Paragraph
                    copyable={{ text: record.reason, tooltips: ['复制原因', '已复制'] }}
                    style={{ margin: 0, fontSize: 12 }}
                    type={record.status === 'failed' ? 'danger' : undefined}
                  >
                    {record.reason}
                  </Typography.Paragraph>
                ) : (
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    无原因信息
                  </Text>
                ),
            }}
          />
        </Space>
      )}
    </Drawer>
  )
}
