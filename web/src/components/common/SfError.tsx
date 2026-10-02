import { Alert, Button, Space } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { resolveErrorMessage } from '@/api/client'

export interface SfErrorProps {
  error: unknown
  /** 提供后展示“重试”按钮 */
  onRetry?: () => void
  /** 附加说明（如排查建议） */
  description?: string
}

/** 统一错误态：用户可读信息 + 可选重试（frontend.md §9.1） */
export function SfError({ error, onRetry, description }: SfErrorProps) {
  const message = resolveErrorMessage(error)
  return (
    <Alert
      type="error"
      showIcon
      message={message}
      description={description}
      action={
        onRetry ? (
          <Space direction="vertical">
            <Button size="small" danger ghost icon={<ReloadOutlined />} onClick={onRetry}>
              重试
            </Button>
          </Space>
        ) : undefined
      }
    />
  )
}
