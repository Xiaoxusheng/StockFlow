import { useRef, useState } from 'react'
import { Button } from 'antd'
import { FastForwardOutlined } from '@ant-design/icons'
import type { TaskItem } from '@/api/task'

export interface SfCompleteNextButtonProps {
  /**
   * 完成动作（既有 onComplete 提交回调）：resolve 即视为完成成功，随后触发进入下一条；
   * reject 则停留在当前条（不取下一条）。
   */
  onComplete: () => Promise<unknown> | unknown
  /** 下一条任务（useNextTask().data?.task；undefined=查询未就绪/无下一条，仅完成不跳转） */
  nextTask?: TaskItem | null
  /**
   * 进入下一条前的领取动作（既有 claim 端点，调用方提供）——
   * claim 命中「已被领取」冲突时视为可进入（调用方在 onClaimNext 内吞掉冲突错误）
   */
  onClaimNext?: (task: TaskItem) => Promise<unknown> | unknown
  /** 进入下一条导航（页面接线：Pad 作业页/PC 作业页各自路由形态） */
  onNavigateNext?: (task: TaskItem) => void
  /** 完成动作进行中（页面提交 mutation 的 isPending） */
  completing?: boolean
  disabled?: boolean
  children?: React.ReactNode
}

/**
 * 「完成并处理下一条」primary 按钮（计划 §2.4，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * 与既有确认按钮并排，props 兼容既有 onComplete 回调——回调成功后依次执行
 * 领取下一条（可选，claim 冲突视为可进入）→ 导航下一条。
 * 组件只做编排不做接线：Pad 收货/上架（并行会话）与 PC 作业页（F3）各自提供
 * onComplete/onClaimNext/onNavigateNext；无下一条时仅完成当前条（不弹假提示）。
 */
export function SfCompleteNextButton({
  onComplete,
  nextTask,
  onClaimNext,
  onNavigateNext,
  completing,
  disabled,
  children,
}: SfCompleteNextButtonProps) {
  const [advancing, setAdvancing] = useState(false)
  // 完成动作的提交链路（claim→导航）进行中防双击：advancing 覆盖 onComplete 与领取段
  const busyRef = useRef(false)

  const handleClick = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setAdvancing(true)
    try {
      await onComplete()
      if (!nextTask) return
      if (onClaimNext) await onClaimNext(nextTask)
      onNavigateNext?.(nextTask)
    } finally {
      busyRef.current = false
      setAdvancing(false)
    }
  }

  return (
    <Button
      type="primary"
      icon={<FastForwardOutlined />}
      loading={completing || advancing}
      disabled={disabled}
      onClick={() => {
        void handleClick()
      }}
    >
      {children ?? '完成并处理下一条'}
    </Button>
  )
}
