import { Typography } from 'antd'
import { SfStatusTag } from '@/components/common/SfStatusTag'

const { Text } = Typography

export interface SfDeviceStatusProps {
  /** 设备在线布尔值（DeviceItem.online）；undefined/null 渲染占位符 */
  online?: boolean | null
}

/**
 * 设备在线状态标签（frontend.md §14.1 在线状态列 / §14.2 在线状态区）：
 * online/offline 已在 types/status.ts:130-131 注册（在线=success、离线=neutral），
 * 一律经 SfStatusTag 全局语义映射渲染，禁止页面自造状态色。
 */
export function SfDeviceStatus({ online }: SfDeviceStatusProps) {
  if (online === undefined || online === null) {
    return <Text type="secondary">-</Text>
  }
  return <SfStatusTag status={online ? 'online' : 'offline'} />
}
