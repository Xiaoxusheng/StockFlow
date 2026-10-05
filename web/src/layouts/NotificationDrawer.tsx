import { Button, Drawer, Flex, List, Typography } from 'antd'
import { CheckOutlined } from '@ant-design/icons'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import { notificationApi, type NotificationItem } from '@/api/notifications'
import { resolveErrorMessage } from '@/api/client'
import { formatDateTime } from '@/utils/format'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
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
            renderItem={(item) => (
              <List.Item
                style={{ padding: '10px 0', cursor: item.read ? 'default' : 'pointer' }}
                onClick={
                  item.read || markRead.isPending ? undefined : () => markRead.mutate(item.id)
                }
              >
                <Flex vertical gap={4} style={{ width: '100%' }}>
                  <Flex align="center" gap={8}>
                    <SfStatusTag
                      label={item.title}
                      semantic={NOTIFICATION_TYPE_SEMANTIC[item.type] ?? 'neutral'}
                    />
                    {!item.read && <Text type="danger" style={{ fontSize: 12 }}>未读</Text>}
                  </Flex>
                  {/* 未读内容加粗、已读弱化，视觉一眼可分 */}
                  <Text
                    strong={!item.read}
                    type={item.read ? 'secondary' : undefined}
                    style={{ fontSize: 13 }}
                  >
                    {item.content}
                  </Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {formatDateTime(item.created_at)}
                  </Text>
                </Flex>
              </List.Item>
            )}
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
