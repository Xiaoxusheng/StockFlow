import { useMutation } from '@tanstack/react-query'
import type { UseMutationOptions, UseMutationResult } from '@tanstack/react-query'
import type { AxiosRequestConfig } from 'axios'
import { newIdempotencyKey } from '@/utils/idempotency'

/**
 * 幂等提交 mutation 包装（计划 §2.10，docs/plans/2026-10-06-efficiency-layer-phase1.md）。
 *
 * 每次 mutate 自动生成新幂等键并传给 mutationFn，调用方以 axios config 形态附到请求头：
 *
 *   const m = useIdempotentMutation({
 *     mutationFn: (vars, config) => http.post('/api/receipts', vars, config),
 *   })
 *
 * 约定与边界：
 * - `Idempotency-Key` 头的服务端消费面 = 收货（internal/purchase/handler.go:502）、
 *   打包/发货（internal/sales/handler.go:730,783）+ stockops 产生 ledger 流水的写路径
 *   （头键作行级键前缀合成，§2.10）——这些提交点重放=回读既有结果，真实幂等；
 * - 上架 execute/拣货确认/复核/批量领取/盘点登记**不读头键**（防重来源=派生键通式或
 *   状态机原子抢占）——提交点仍接本 hook，收益仅为 isPending 期间按钮防双击；
 * - 每次 mutate 新键（同一次提交的重试共享 mutate 内同键）：键随 attempt 生成于
 *   mutationFn 入口，同次 attempt 内多请求不应复用（本 hook 面向单请求提交点）；
 * - isPending 期间消费方以 `m.isPending` 驱动按钮 loading+disabled（§2.10 约定）。
 */

/** 携带 Idempotency-Key 头的 axios 请求配置（调用方原样传给 http.* 的 config 参数） */
export type IdempotencyRequestConfig = Pick<AxiosRequestConfig, 'headers' | 'timeout'> & {
  headers: { 'Idempotency-Key': string }
}

type IdempotentMutationFn<TData, TVariables> = (
  variables: TVariables,
  config: IdempotencyRequestConfig,
) => Promise<TData>

type IdempotentMutationOptions<TData, TVariables, TContext> = Omit<
  UseMutationOptions<TData, Error, TVariables, TContext>,
  'mutationFn'
> & {
  mutationFn: IdempotentMutationFn<TData, TVariables>
}

export function useIdempotentMutation<TData = unknown, TVariables = void, TContext = unknown>(
  options: IdempotentMutationOptions<TData, TVariables, TContext>,
): UseMutationResult<TData, Error, TVariables, TContext> {
  const { mutationFn, ...rest } = options
  return useMutation<TData, Error, TVariables, TContext>({
    ...rest,
    mutationFn: (variables) => {
      const config: IdempotencyRequestConfig = {
        headers: { 'Idempotency-Key': newIdempotencyKey() },
      }
      return mutationFn(variables, config)
    },
  })
}
