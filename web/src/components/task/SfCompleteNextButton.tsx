import { useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { Button, Space, message } from 'antd'
import { CheckOutlined, FastForwardOutlined, UnorderedListOutlined } from '@ant-design/icons'
import { taskApi, type NextTaskQuery, type TaskItem } from '@/api/task'
import { resolveErrorMessage } from '@/api/client'

export interface SfCompleteNextButtonProps {
  /**
   * 完成动作（既有 onComplete 提交回调）：resolve 即视为完成成功，随后触发进入下一条；
   * reject 则停留在当前条（错误提示由调用方提交链路负责，组件不重复弹错）。
   */
  onComplete: () => Promise<unknown> | unknown
  /**
   * 下一条查询参数（推荐接法，任务 ask 冻结口径）：提供后组件在**完成动作成功后**
   * 调 GET /api/tasks/next 重新查询（绝不前端数组推算）——
   * has_next=true → 领取+切换任务上下文；has_next=false → 提示「没有更多任务」并返回列表；
   * 查询失败 → 提示错误并停留当前页（当前任务已完成，不误导为提交失败）。
   */
  nextQuery?: NextTaskQuery
  /**
   * 兼容接法（既有消费点）：外部预取的下一条任务（useNextTask().data?.task）。
   * 未传 nextQuery 时使用；传了 nextQuery 则以完成后重新查询的结果为准。
   */
  nextTask?: TaskItem | null
  /**
   * 进入下一条前的领取动作（既有 claim 端点，调用方提供）——
   * claim 命中「已被领取」冲突时视为可进入（调用方在 onClaimNext 内吞掉冲突错误）。
   * receipt 分支无领取语义（api.md §9），调用方不传即跳过。
   */
  onClaimNext?: (task: TaskItem) => Promise<unknown> | unknown
  /** 进入下一条导航（页面接线：Pad 作业页/PC 作业页各自路由形态） */
  onNavigateNext?: (task: TaskItem) => void
  /**
   * 返回列表动作：【返回列表】按钮与「无下一条自动回列表」共用。
   * 缺省 = 路由历史回退（navigate(-1)）。
   */
  onBackList?: () => void
  /** 【返回列表】按钮文案（缺省「返回列表」） */
  backText?: string
  /** 隐藏【完成】按钮（部分作业页仅保留主按钮+返回列表二态） */
  hideCompleteOnly?: boolean
  /** 完成动作进行中（页面提交 mutation 的 isPending）——提交期三按钮全部禁用 */
  completing?: boolean
  disabled?: boolean
  children?: React.ReactNode
}

/**
 * 任务完成按钮组（计划 §2.4；ask 交付名 TaskNextButton，导出别名同款）：
 * 【完成并处理下一条】（primary，默认推荐）/【完成】/【返回列表】。
 *
 * 主按钮链路：onComplete（提交）→ GET /api/tasks/next 重新查询（后端真实 SQL 排序，
 * 前端零推算）→ 有下一条：claim（可选）+ 切换任务上下文；无下一条：提示并回列表；
 * 查询失败：提示并停留（提交已成功）。提交/推进全程 loading+disabled 防双击。
 * 组件只做编排不做接线：Pad 收货/上架与 PC 作业页各自提供 onComplete/onClaimNext/onNavigateNext。
 */
export function SfCompleteNextButton({
  onComplete,
  nextQuery,
  nextTask,
  onClaimNext,
  onNavigateNext,
  onBackList,
  backText = '返回列表',
  hideCompleteOnly,
  completing,
  disabled,
  children,
}: SfCompleteNextButtonProps) {
  const navigate = useNavigate()
  const [advancing, setAdvancing] = useState(false)
  // 完成动作的提交链路（提交→查询下一条→claim→导航）进行中防双击：覆盖 onComplete 与领取段
  const busyRef = useRef(false)

  const goBackList = () => {
    if (onBackList) onBackList()
    else navigate(-1)
  }

  /** 主按钮：完成 → 查询下一条（提供 nextQuery 时完成后重新查询）→ 领取 → 切换/回列表 */
  const handleCompleteAndNext = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setAdvancing(true)
    try {
      try {
        await onComplete()
      } catch {
        // 提交失败：错误提示由调用方提交链路负责（组件不重复弹错），停留在当前条
        return
      }
      let next: TaskItem | null | undefined
      if (nextQuery) {
        try {
          // 完成后再查（ask 冻结口径：完成后调 GET /api/tasks/next 重新查询，绝不前端数组推算）
          const res = await taskApi.next(nextQuery)
          next = res.has_next ? res.task : null
        } catch (e) {
          // 查询失败 ≠ 提交失败：当前任务已完成，提示后停留，由用户手动决定去向
          message.error(`当前任务已完成；获取下一条任务失败：${resolveErrorMessage(e)}`)
          return
        }
        if (!next) {
          // Empty 态：没有更多候选任务——提示并返回列表（不造假任务）
          message.info('当前任务已完成，没有更多待处理任务')
          goBackList()
          return
        }
      } else {
        // 兼容既有接法：外部预取结果（useNextTask）；无预取则仅完成
        next = nextTask
        if (!next) return
      }
      if (onClaimNext) await onClaimNext(next)
      onNavigateNext?.(next)
    } finally {
      busyRef.current = false
      setAdvancing(false)
    }
  }

  /** 【完成】：仅提交，不查询/不切换（成功反馈与页面刷新由调用方提交链路负责） */
  const handleCompleteOnly = async () => {
    if (busyRef.current) return
    busyRef.current = true
    setAdvancing(true)
    try {
      await onComplete()
    } catch {
      // 提交失败：错误提示由调用方提交链路负责
    } finally {
      busyRef.current = false
      setAdvancing(false)
    }
  }

  // 提交期（页面 completing 或组件推进中）三按钮全部禁用 + 主按钮 loading
  const busy = Boolean(completing) || advancing

  return (
    <Space size={8} wrap>
      <Button
        type="primary"
        icon={<FastForwardOutlined />}
        loading={busy}
        disabled={disabled}
        onClick={() => {
          void handleCompleteAndNext()
        }}
      >
        {children ?? '完成并处理下一条'}
      </Button>
      {!hideCompleteOnly && (
        <Button
          icon={<CheckOutlined />}
          disabled={disabled || busy}
          onClick={() => {
            void handleCompleteOnly()
          }}
        >
          完成
        </Button>
      )}
      <Button
        icon={<UnorderedListOutlined />}
        disabled={busy}
        onClick={goBackList}
      >
        {backText}
      </Button>
    </Space>
  )
}

/** ask 交付名别名：TaskNextButton = SfCompleteNextButton（同一实现，禁止第二套任务按钮） */
export { SfCompleteNextButton as TaskNextButton }
