import { Button, Drawer, Flex, List, Typography } from 'antd'
import {
  AuditOutlined,
  CheckOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  InfoCircleOutlined,
  SyncOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { notificationApi, type NotificationItem } from '@/api/notifications'
import { resolveErrorMessage } from '@/api/client'
import { formatDateTime } from '@/utils/format'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { STATUS_SEMANTIC_COLOR } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'

const { Text } = Typography

/** 单页条数（后端 ParsePage 单页上限 100，internal/response/response.go MaxPageSize） */
const PAGE_SIZE = 50

// 通知类型 → 语义色：键为后端值域大写枚举（db/migrations/000014 chk_notifications_type：
// SYSTEM/APPROVAL/STOCK_ALERT/EXPIRY_ALERT/EXCEPTION/TASK，api/notifications.ts
// NotificationType）。§24 状态色统一：语义 → SfStatusTag 经 --sf-* Token 派生，
// 禁 antd 预设色（orange/gold 与 --sf-warning、red 与 --sf-danger 不同相）；
// 未知类型中性兜底。
const NOTIFICATION_TYPE_SEMANTIC: Record<string, StatusSemantic> = {
  APPROVAL: 'processing',
  STOCK_ALERT: 'warning',
  EXPIRY_ALERT: 'warning',
  EXCEPTION: 'danger',
  TASK: 'processing',
  SYSTEM: 'neutral',
}

/**
 * 通知类型 → 图标（frontend.md §15.3：审批 / 库存预警 / 效期预警 / 异常 / 任务 / 系统）。
 * 图标承担「类型识别」职责，颜色取自 STATUS_SEMANTIC_COLOR——与 SfStatusTag 共用同一
 * Token 色源（§24），不另造第二套语义色；未知类型中性兜底。
 */
const NOTIFICATION_TYPE_ICON: Record<string, ReactNode> = {
  APPROVAL: <AuditOutlined />,
  STOCK_ALERT: <WarningOutlined />,
  EXPIRY_ALERT: <ClockCircleOutlined />,
  EXCEPTION: <CloseCircleOutlined />,
  TASK: <SyncOutlined />,
  SYSTEM: <InfoCircleOutlined />,
}

export interface NotificationDrawerProps {
  open: boolean
  onClose: () => void
}

/** Header 通知抽屉（frontend.md §15.3）：数据来自 GET /api/notifications（分页信封
 * items/total）。历史通知经「加载更多」逐页拉取——此前写死 page=1 导致超 50 条不可达，
 * 现以信封 total 判断翻页进度；点击未读项标记单条已读（POST /api/notifications/:id/read），
 * 并联动 Header 未读数 */
export function NotificationDrawer({ open, onClose }: NotificationDrawerProps) {
  const queryClient = useQueryClient()
  const query = useInfiniteQuery({
    queryKey: ['notifications', 'list'],
    queryFn: ({ pageParam }) => notificationApi.list({ page: pageParam, pageSize: PAGE_SIZE }),
    initialPageParam: 1,
    getNextPageParam: (lastPage, allPages) => {
      const loaded = allPages.reduce((sum, page) => sum + page.items.length, 0)
      // 以信封 total 为准判断是否还有下一页；空页兜底防死循环
      return loaded < lastPage.total && lastPage.items.length > 0 ? lastPage.page + 1 : undefined
    },
    enabled: open,
  })

  // 后端空页 items 为 null（response.Page.Items any 序列化 nil slice），必须兜底空数组
  const items = useMemo(
    () => query.data?.pages.flatMap((page) => page.items ?? []) ?? [],
    [query.data],
  )
  const total = query.data?.pages[0]?.total ?? 0

  // 已读动作后刷新列表 + 未读数（PcLayout Badge 的 queryKey 同在 'notifications' 前缀下）
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['notifications'] })
  }

  const markRead = useMutation({
    mutationFn: (id: NotificationItem['id']) => notificationApi.markRead(id),
    onSuccess: refresh,
    // 失败信息经 markRead.error 在抽屉底部统一呈现
  })

  const markAll = useMutation({
    mutationFn: notificationApi.markAllRead,
    onSuccess: refresh,
  })

  return (
    <Drawer
      title="通知"
      size={380}
      open={open}
      onClose={onClose}
      extra={
        <Button
          size="small"
          icon={<CheckOutlined />}
          onClick={() => markAll.mutate()}
          disabled={!query.data || items.length === 0}
          loading={markAll.isPending}
        >
          全部已读
        </Button>
      }
    >
      {/* 首屏失败（data 为空）→ 整页错误；翻页失败（已有 data）→ 列表保留 + 底部错误可重试 */}
      {query.isPending || !query.data ? (
        query.error ? (
          <SfError error={query.error} onRetry={() => query.refetch()} />
        ) : (
          <SfLoading rows={6} />
        )
      ) : items.length === 0 ? (
        <SfEmpty description="暂无通知" />
      ) : (
        <>
          <List
            dataSource={items}
            renderItem={(item) => {
              const semantic = NOTIFICATION_TYPE_SEMANTIC[item.type] ?? 'neutral'
              const accent = STATUS_SEMANTIC_COLOR[semantic]
              const unread = !item.read
              return (
                <List.Item
                  style={{ padding: 0, border: 'none', cursor: unread ? 'pointer' : 'default' }}
                  onClick={unread && !markRead.isPending ? () => markRead.mutate(item.id) : undefined}
                >
                  {/* 通知卡片（frontend.md §15.3）三级层次：
                      ① 标题行 = 类型图标 + 标题 + 时间（右对齐弱化）
                      ② 正文（次级色、行高 1.65，与标题拉开权重）
                      未读 = 左侧语义色条 + 微染底色 + 标题加粗；已读整卡降级为 muted，
                      一眼可分且无需额外「未读」文字标签。 */}
                  <div
                    style={{
                      position: 'relative',
                      width: '100%',
                      marginBottom: 'var(--sf-space-2)',
                      padding: 'var(--sf-space-3) var(--sf-space-3) var(--sf-space-3) 16px',
                      border: '1px solid var(--sf-border-subtle)',
                      borderRadius: 'var(--sf-radius-md)',
                      background: unread
                        ? `color-mix(in srgb, ${accent} 6%, var(--sf-surface))`
                        : 'var(--sf-surface)',
                      overflow: 'hidden',
                    }}
                  >
                    {unread && (
                      <span
                        aria-hidden
                        style={{
                          position: 'absolute',
                          insetBlock: 0,
                          insetInlineStart: 0,
                          width: 3,
                          background: accent,
                        }}
                      />
                    )}
                    <Flex align="flex-start" gap="var(--sf-space-2)">
                      <span
                        aria-hidden
                        style={{
                          flex: '0 0 auto',
                          marginTop: 1,
                          fontSize: 15,
                          lineHeight: '20px',
                          color: accent,
                        }}
                      >
                        {NOTIFICATION_TYPE_ICON[item.type] ?? <InfoCircleOutlined />}
                      </span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        {/* ① 标题：完整展示不截断（单号是定位通知的关键信息），未读加粗 */}
                        <Text
                          strong={unread}
                          type={unread ? undefined : 'secondary'}
                          style={{ display: 'block', fontSize: 14, lineHeight: '20px' }}
                        >
                          {item.title}
                        </Text>
                        {/* ② 正文：次级色 + 舒展行高，与标题拉开权重差 */}
                        <Text
                          type={unread ? undefined : 'secondary'}
                          style={{
                            display: 'block',
                            marginTop: 4,
                            fontSize: 13,
                            lineHeight: 1.65,
                            color: unread ? 'var(--sf-text-secondary)' : 'var(--sf-text-muted)',
                          }}
                        >
                          {item.content}
                        </Text>
                        {/* ③ 时间：右对齐作元信息锚点（字号最小、色最弱），不占标题宽度 */}
                        <Text
                          type="secondary"
                          style={{
                            display: 'block',
                            marginTop: 6,
                            textAlign: 'right',
                            fontSize: 12,
                            color: 'var(--sf-text-muted)',
                          }}
                        >
                          {formatDateTime(item.created_at)}
                        </Text>
                      </div>
                    </Flex>
                  </div>
                </List.Item>
              )
            }}
          />
          {/* 分页加载进度：已显示 / 共 N 条（total 消费自分页信封） */}
          <Flex vertical align="center" gap={8} style={{ marginTop: 12 }}>
            {query.hasNextPage && (
              <Button
                size="small"
                block
                onClick={() => void query.fetchNextPage()}
                loading={query.isFetchingNextPage}
              >
                加载更多
              </Button>
            )}
            <Text type="secondary" style={{ fontSize: 12 }}>
              已显示 {items.length} / 共 {total} 条
            </Text>
            {query.error && query.data && (
              <Flex align="center" gap={8}>
                <Text type="danger" style={{ fontSize: 12 }}>
                  加载更多失败：{resolveErrorMessage(query.error)}
                </Text>
                <Button type="link" size="small" onClick={() => void query.fetchNextPage()}>
                  重试
                </Button>
              </Flex>
            )}
          </Flex>
        </>
      )}
      {markRead.error && (
        <Text type="danger" style={{ fontSize: 12 }}>
          标记已读失败：{resolveErrorMessage(markRead.error)}
        </Text>
      )}
    </Drawer>
  )
}
