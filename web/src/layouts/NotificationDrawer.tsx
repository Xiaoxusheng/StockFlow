import { Button, Drawer, Flex, List, Tag, Typography } from 'antd'
import { CheckOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { notificationApi } from '@/api/notifications'
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

/** Header 通知抽屉（frontend.md §15.3）：数据来自 /api/notifications */
export function NotificationDrawer({ open, onClose }: NotificationDrawerProps) {
  const query = useQuery({
    queryKey: ['notifications', { page: 1, pageSize: 50 }],
    queryFn: () => notificationApi.list({ page: 1, pageSize: 50 }),
    enabled: open,
  })

  const markAll = () => {
    void notificationApi.markAllRead().then(() => query.refetch())
  }

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
          onClick={markAll}
          disabled={!query.data || query.data.items.length === 0}
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
            <List.Item style={{ padding: '10px 0' }}>
              <Flex vertical gap={4} style={{ width: '100%' }}>
                <Flex align="center" gap={8}>
                  <Tag color={NOTIFICATION_TYPE_COLOR[item.type] ?? 'default'} style={{ marginInlineEnd: 0 }}>
                    {item.title}
                  </Tag>
                  {!item.read && <Text type="danger" style={{ fontSize: 12 }}>未读</Text>}
                </Flex>
                <Text style={{ fontSize: 13 }}>{item.content}</Text>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {formatDateTime(item.createdAt)}
                </Text>
              </Flex>
            </List.Item>
          )}
        />
      )}
    </Drawer>
  )
}
