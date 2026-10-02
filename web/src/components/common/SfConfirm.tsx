import { Popconfirm } from 'antd'
import type { PopconfirmProps } from 'antd'
import type { ReactNode } from 'react'

export interface SfConfirmProps extends Omit<PopconfirmProps, 'okText' | 'okButtonProps' | 'okType'> {
  /** 确认动作文案（okText），默认「确认」；语义危险时调用方传「删除」「停用」等 */
  okText?: string
  /** 确认中 loading（透传给 okButtonProps.loading，配合 mutation.isPending） */
  confirming?: boolean
  /** 触发确认的子元素（通常是危险样式的 Button） */
  children: ReactNode
}

/**
 * 危险操作二次确认统一封装（requirements.md §10 / frontend.md §8 交互约束）：
 * 删除、停用、强制执行等破坏性动作必须经二次确认，确认键固定 danger 语义。
 * 与 masterdata 列表页既有 Popconfirm 模式同构，收敛为公共组件后新页面不再各自拼装。
 */
export function SfConfirm({ okText = '确认', confirming, children, ...rest }: SfConfirmProps) {
  return (
    <Popconfirm
      okText={okText}
      cancelText="取消"
      okType="primary"
      okButtonProps={{ danger: true, loading: confirming }}
      {...rest}
    >
      {children}
    </Popconfirm>
  )
}
