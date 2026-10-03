import { Button, Drawer, Flex, List, Tag, Typography } from 'antd'
import { CheckOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { notificationApi, type NotificationItem } from '@/api/notifications'
import { resolveErrorMessage } from '@/api/client'
import { formatDateTime } from '@/utils/format'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'

const { Text } = Typography

const NOTIFICATION_TYPE_COLOR: Record<string, string> = {
  approval: 'blue',
  stock_alert: 'orange',
  expiry_alert: 'gold',
  exception: 'red',
  task: 'cyan',
  system: 'default',
}

export interface NotificationDrawerProps {
  open: boolean
  onClose: () => void
}

/** Header 通知抽屉（frontend.md §15.3）：数据来自 /api/notifications；
 * 点击未读项标记单条已读（POST /api/notifications/:id/read），并联动 Header 未读数 */
export function NotificationDrawer({ open, onClose }: NotificationDrawerProps) {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['notifications', { page: 1, pageSize: 50 }],
    queryFn: () => notificationApi.list({ page: 1, pageSize: 50 }),
    enabled: open,
  })

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
      width={380}
      open={open}
      onClose={onClose}
      extra={
        <Button
          size="small"
          icon={<CheckOutlined />}
          onClick={() => markAll.mutate()}
          disabled={!query.data || query.data.items.length === 0}
          loading={markAll.isPending}
        >
          全部已读
        </Button>
      }
    >
      {query.isPending ? (
        <SfLoading rows={6} />
      ) : query.error ? (
        <SfError error={query.error} onRetry={() => query.refetch()} />
      ) : (query.data?.items.length ?? 0) === 0 ? (
        <SfEmpty description="暂无通知" />
      ) : (
        <List
          dataSource={query.data?.items}
          renderItem={(item) => (
            <List.Item
              style={{ padding: '10px 0', cursor: item.read ? 'default' : 'pointer' }}
              onClick={
                item.read || markRead.isPending ? undefined : () => markRead.mutate(item.id)
              }
            >
              <Flex vertical gap={4} style={{ width: '100%' }}>
                <Flex align="center" gap={8}>
                  <Tag color={NOTIFICATION_TYPE_COLOR[item.type] ?? 'default'} style={{ marginInlineEnd: 0 }}>
                    {item.title}
                  </Tag>
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
                  {formatDateTime(item.createdAt)}
                </Text>
              </Flex>
            </List.Item>
          )}
        />
      )}
      {markRead.error && (
        <Text type="danger" style={{ fontSize: 12 }}>
          标记已读失败：{resolveErrorMessage(markRead.error)}
        </Text>
      )}
    </Drawer>
  )
}
