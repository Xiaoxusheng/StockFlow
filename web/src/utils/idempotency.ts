/**
 * 幂等键生成（计划 §2.10，docs/plans/2026-10-06-efficiency-layer-phase1.md）：
 * crypto.randomUUID → crypto.getRandomValues → 时间戳+计数器 三级回退
 * （PadReceivePage.tsx:127 先例收编为全站唯一工具函数）。
 *
 * 服务端消费面（§2.10）：经 useIdempotentMutation 附 `Idempotency-Key` 头后，仅
 * 收货/打包/发货 + stockops 产生 ledger 流水的写路径真实消费（重放=回读既有结果）；
 * 上架 execute/拣货确认/复核/批量领取/盘点登记不读头键——其防重来源为派生键通式
 * 或状态机原子抢占，前端附头仅获得按钮防双击收益。
 */

/** 模块级计数器：时间戳同毫秒内兜底去重 */
let fallbackCounter = 0

export function newIdempotencyKey(): string {
  // 一级：crypto.randomUUID（现代浏览器/安全上下文）
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    try {
      return crypto.randomUUID()
    } catch {
      // 非安全上下文可能抛错，降级下一级
    }
  }
  // 二级：crypto.getRandomValues 拼 UUID v4 形态
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  // 三级：时间戳 + 计数器 + 随机尾（无 crypto 的极端环境）
  fallbackCounter += 1
  return `sf-${Date.now().toString(36)}-${fallbackCounter.toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
