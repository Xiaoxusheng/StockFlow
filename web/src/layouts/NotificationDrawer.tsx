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

const { Text } = Typography

/** 单页条数（后端 ParsePage 单页上限 100，internal/response/response.go MaxPageSize） */
const PAGE_SIZE = 50

// 通知条目**不使用任何彩色装饰**——无左侧色条、无淡彩底、无彩色图标。
// 层级完全由排版承担：字重（未读 600 / 已读 400）+ 灰度（--sf-text /
// --sf-text-secondary / --sf-text-muted）+ 留白；未读仅以 6px 主色圆点指示。
// 类型识别由标题文案自身承担（「库存异常待处理」「待审批：」等），不额外加图标。
// 规范见 frontend.md §15.3。

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
              const unread = !item.read
              return (
                <List.Item
                  style={{ padding: '12px 0', cursor: unread ? 'pointer' : 'default' }}
                  onClick={unread && !markRead.isPending ? () => markRead.mutate(item.id) : undefined}
                >
                  {/* 列表式条目（frontend.md §15.3）：无卡片、无底色、无彩色条——
                      层级只靠「字重 + 灰度 + 留白」承担，条目间为 antd List 默认细分隔线。
                      未读仅以 6px 主色圆点指示；已读留同宽透明占位，保证标题左缘对齐。 */}
                  <div style={{ display: 'flex', gap: 10, width: '100%', alignItems: 'flex-start' }}>
                    <span
                      aria-hidden
                      style={{
                        flex: '0 0 auto',
                        width: 6,
                        height: 6,
                        marginTop: 7,
                        borderRadius: '50%',
                        background: unread ? 'var(--sf-primary)' : 'transparent',
                      }}
                    />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      {/* 标题独占一行（不截断、不被时间挤折行——单号是定位关键信息） */}
                      <Text
                        strong={unread}
                        type={unread ? undefined : 'secondary'}
                        style={{ display: 'block', fontSize: 14, lineHeight: '20px' }}
                      >
                        {item.title}
                      </Text>
                      <Text
                        type={unread ? undefined : 'secondary'}
                        style={{
                          display: 'block',
                          marginTop: 4,
                          fontSize: 13,
                          lineHeight: 1.6,
                          color: unread ? 'var(--sf-text-secondary)' : 'var(--sf-text-muted)',
                        }}
                      >
                        {item.content}
                      </Text>
                      {/* 时间：末行右对齐作元信息锚点（字号最小、灰度最弱） */}
                      <Text
                        type="secondary"
                        style={{
                          display: 'block',
                          marginTop: 6,
                          textAlign: 'right',
                          fontSize: 12,
                          lineHeight: '18px',
                          color: 'var(--sf-text-muted)',
                        }}
                      >
                        {formatDateTime(item.created_at)}
                      </Text>
                    </div>
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
