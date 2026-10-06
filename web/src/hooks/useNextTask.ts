import { useQuery } from '@tanstack/react-query'
import { taskApi, type NextTaskQuery, type NextTaskResult } from '@/api/task'

/**
 * 「完成后自动下一条」查询（计划 §2.4，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * queryFn 调 GET /api/tasks/next（task_type 白名单五值；候选池=(本人已领取且进行中)
 * OR (PENDING 未领取)——B7 裁决；排序全在后端 SQL，前端零推算）。
 *
 * 流程约定：本 hook 只负责查询；返回 task 后由调用方 claim（既有领取端点——
 * putaway POST /api/putaway/{id}/claim、picks/checks PUT claim），claim 命中
 * 「已被领取」冲突时视为可进入（调用方吞掉该冲突错误后继续导航）；
 * SfCompleteNextButton 消费本 hook 结果完成「完成→领取→进入」链路。
 */
export function useNextTask(params: NextTaskQuery & { enabled?: boolean }) {
  const { enabled = true, ...query } = params
  return useQuery<NextTaskResult>({
    queryKey: ['tasks', 'next', query],
    queryFn: () => taskApi.next(query),
    enabled,
  })
}
