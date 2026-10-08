/**
 * 幂等键生成（计划 §2.10，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * crypto.randomUUID → crypto.getRandomValues → 时间戳+计数器 三级回退
 * （PadReceivePage.tsx:127 先例收编为全站唯一工具函数）。
 *
 * 服务端消费面（§2.10）：经 useIdempotentMutation 附 `Idempotency-Key` 头后，仅
 * 收货/打包/发货 + stockops 产生 ledger 流水的写路径真实消费（重放=回读既有结果）；
 * 上架 execute/拣货确认/复核/批量领取/盘点登记不读头键——其防重来源为派生键通式
 * 或状态机原子抢占，前端附头仅获得按钮防双击收益。
 *
 * **长度上限 32 字符（api.md §7 冻结）**：stockops 流水路径（调拨 approve/outbound/
 * arrive/receive、移库、盘点 complete）以头键做行级键前缀合成
 * `{头键}:{原行级通式}`，其 handler 对超过 32 字符的头键直接 400 invalidParam
 * （internal/stockops/handler.go:234 maxIdemHeaderLen，列宽 varchar(128) 反推）。
 * 因此**不得直接输出 36 字符的 UUID 带连字符形态**——那是本工具函数曾经的形态，
 * 一旦被接到 stockops 端点上必 400（2026-10-08 实测：33 字符键被拒）。统一输出
 * ≤32 字符：UUID 去连字符恰 32 字符；其余分支同样收敛在该上限内。
 * 下限更严的服务端（收货/打包/发货 ≤128、幂等中间件键值域 ≤64）对更短的键无差别接受。
 */

/** 服务端 Idempotency-Key 头长度上限（api.md §7 冻结，stockops handler 同源） */
export const IDEMPOTENCY_KEY_MAX_LEN = 32

/** 模块级计数器：时间戳同毫秒内兜底去重 */
let fallbackCounter = 0

export function newIdempotencyKey(): string {
  // 一级：crypto.randomUUID（现代浏览器/安全上下文）——去连字符 = 32 字符，恰在上限内
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    try {
      return crypto.randomUUID().replace(/-/g, '')
    } catch {
      // 非安全上下文可能抛错，降级下一级
    }
  }
  // 二级：crypto.getRandomValues 随机十六进制（16 字节 → 32 字符，不再插连字符）
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  }
  // 三级：时间戳 + 计数器 + 随机尾（无 crypto 的极端环境；约 23 字符）
  fallbackCounter += 1
  return `sf-${Date.now().toString(36)}-${fallbackCounter.toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
