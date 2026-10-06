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
            {/* 默认尺寸（32px）：原 small≈28px 在 Pad 上远小于 44px 触摸目标（frontend.md §31），
                错误重试是关键恢复动作，用默认尺寸提升可点性 */}
            <Button danger ghost icon={<ReloadOutlined />} onClick={onRetry}>
              重试
            </Button>
          </Space>
        ) : undefined
      }
    />
  )
}
