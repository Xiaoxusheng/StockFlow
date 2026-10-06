// ---------- 批量结果重试纯逻辑（自 BatchResultDrawer.tsx 抽出，2026-10-06 测试加固） ----------
// 契约：api.md §9 批量结果 {total, success_count, failed_count, skipped_count, results}
// （api/printing.ts BatchResult 全站唯一契约源）。本文件零运行时依赖（BatchResult 为
// import type，运行时擦除），node:test 可直接加载测试。

import type { BatchResult } from '@/api/printing'

/**
 * 仅重试失败项：status === 'failed' 的对象 id 列表（保持 results 原顺序）。
 * 成功项绝不重跑；skipped（幂等命中/已处目标态）不重跑——api.md §9 批量契约冻结语义。
 */
export function pickFailedIds(result: BatchResult | null): string[] {
  return (result?.results ?? [])
    .filter((item) => item.status === 'failed')
    .map((item) => item.id)
}
