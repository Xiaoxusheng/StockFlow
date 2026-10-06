import { useState, type ReactNode } from 'react'
import { Button, Modal, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router'
import { ScanInput } from '@/components/scanner/ScanInput'
import { useSmartScanNext } from '@/hooks/useSmartScanNext'

export type PadActionBarSlotKey = 'back' | 'scan' | 'exception' | 'pause' | 'finish'

export interface PadActionBarAction {
  key: string
  label: string
  icon?: ReactNode
  onClick?: () => void
  loading?: boolean
  disabled?: boolean
  /** 死按钮门禁（frontend.md §9.1 Disabled）：disabled 必须给原因文案，点按 Toast 提示 */
  disabledReason?: string
  /** primary = 主操作（完成），danger = 危险操作（异常） */
  variant?: 'primary' | 'danger' | 'default'
}

export interface PadActionBarProps {
  /** 自定义槽位：传入后完全替换默认槽位（扫码槽需自行挂扫码输入） */
  actions?: PadActionBarAction[]
  /** 隐藏默认槽位：查询类页（首页/任务/库存/异常列表）省略 ['pause', 'finish']；
   * 仅在未传 actions 时生效 */
  hideSlots?: PadActionBarSlotKey[]
  /** 内置 [暂停] 回调：作业页传入后渲染该槽（查询类页不传即不渲染） */
  onPause?: () => void
  /** 内置 [完成] 回调：作业页传入后渲染该槽（查询类页不传即不渲染） */
  onFinish?: () => void
  /**
   * 内置 [扫码] 槽回调：页面自有扫码上下文处理（定位明细行等）。不传时走
   * 智能下一步全局路由（resolve → 按识别对象进入对应作业页）。
   * 两种形态扫码后均不自动关弹窗（扫→判→继续，扫码作业优化 2026-10-06）。
   */
  onScanSubmit?: (code: string) => void
}

/**
 * Pad 底部操作栏原语（frontend.md §20.7）：固定视口底部，padding-bottom 含
 * env(safe-area-inset-bottom) 不挡系统手势区；默认五槽 [返回][扫码][异常][暂停][完成]，
 * 按钮高 52px。暂停/完成需页面显式传入回调才渲染；禁用必须带 disabledReason。
 * 扫码弹窗统一托管 ScanInput 三模式输入（扫码作业优化 2026-10-06）：页面传
 * onScanSubmit 时按页面上下文处理；未传时走 useSmartScanNext('global')——识别命中
 * 后直接进入对应作业页（收货任务→收货确认 / 上架→上架 / 拣货→拣货 / SKU·库位→库存页定位）。
 */
export function PadActionBar({
  actions,
  hideSlots = [],
  onPause,
  onFinish,
  onScanSubmit,
}: PadActionBarProps) {
  const navigate = useNavigate()
  const [scanOpen, setScanOpen] = useState(false)
  const [messageApi, contextHolder] = message.useMessage()
  const { resolveNext } = useSmartScanNext({ context: 'global', page: '/pad' })

  const defaultActions: PadActionBarAction[] = [
    { key: 'back', label: '返回', icon: <ArrowLeftOutlined />, onClick: () => navigate(-1) },
    { key: 'scan', label: '扫码', icon: <ScanOutlined />, onClick: () => setScanOpen(true) },
    {
      key: 'exception',
      label: '异常',
      icon: <WarningOutlined />,
      variant: 'danger',
      onClick: () => navigate('/pad/exception'),
    },
    ...(onPause ? [{ key: 'pause', label: '暂停', icon: <PauseOutlined />, onClick: onPause }] : []),
    ...(onFinish
      ? [{ key: 'finish', label: '完成', icon: <CheckOutlined />, variant: 'primary' as const, onClick: onFinish }]
      : []),
  ]

  // hideSlots 仅对默认槽生效；页面传入 actions 时完全自定义
  const finalActions =
    actions ?? defaultActions.filter((action) => !(hideSlots as string[]).includes(action.key))

  // 全部槽位被隐藏时不渲染空栏（如纯展示页），modal 随之无入口
  if (finalActions.length === 0) return null

  const handleDisabledTap = (action: PadActionBarAction) => {
    messageApi.warning(action.disabledReason ?? '该操作当前不可用')
  }

  /** 全局智能下一步：resolve 命中 → 路由到对应作业页并收起弹窗；未命中/失败 → 弹窗内
   * 如实提示继续扫（不伪造成功，不静默关窗） */
  const handleGlobalScan = async (code: string) => {
    const { directive } = await resolveNext(code)
    if (directive.kind === 'navigate') {
      messageApi.success(directive.message)
      setScanOpen(false)
      navigate(directive.path)
      return
    }
    messageApi.warning(directive.message)
  }

  return (
    <>
      {contextHolder}
      <div className="sf-pad-actionbar" role="toolbar" aria-label="Pad 底部操作栏">
        {finalActions.map((action) =>
          action.disabled ? (
            // antd 禁用按钮吞点击：外包一层接管点按，把 disabledReason 以 Toast 送达（死按钮门禁）
            <span
              key={action.key}
              className="sf-pad-actionbar-wrap"
              onClick={() => handleDisabledTap(action)}
            >
              <Button
                block
                size="large"
                className="sf-pad-action-btn"
                icon={action.icon}
                loading={action.loading}
                disabled
              >
                {action.label}
              </Button>
            </span>
          ) : (
            <Button
              key={action.key}
              block
              size="large"
              className="sf-pad-action-btn"
              type={action.variant === 'primary' || action.variant === 'danger' ? 'primary' : 'default'}
              danger={action.variant === 'danger'}
              icon={action.icon}
              loading={action.loading}
              onClick={() => action.onClick?.()}
            >
              {action.label}
            </Button>
          ),
        )}
      </div>
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <ScanInput
          autoFocus={false}
          placeholder="扫入条码 / 单号（HID 扫码枪或手工输入）"
          onScan={(code) => {
            if (onScanSubmit) {
              // 页面自有上下文处理（定位明细行等）：弹窗保持打开，页面 Toast 反馈后可直接连扫
              onScanSubmit(code)
              return
            }
            void handleGlobalScan(code)
          }}
        />
      </Modal>
    </>
  )
}
