import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import type { BatchResult, BatchResultItem } from '@/api/printing'
import { pickFailedIds } from './batchRetry.ts'

// ---------- 批量结果「仅重试失败项」测试（docs/testing.md §12.2 批量打印自动化） ----------
// 契约：api.md §9 批量结果三态（success/failed/skipped，批量不整体回滚）；
// 验收场景 4（效率层一期计划 §9）：批量打印 100 → 96 成功 / 3 失败 / 1 跳过，仅重试失败 3 张。

function item(id: string, status: BatchResultItem['status'], reason?: string): BatchResultItem {
  return reason === undefined ? { id, status } : { id, status, reason }
}

function batch(results: BatchResultItem[]): BatchResult {
  return {
    total: results.length,
    success_count: results.filter((r) => r.status === 'success').length,
    failed_count: results.filter((r) => r.status === 'failed').length,
    skipped_count: results.filter((r) => r.status === 'skipped').length,
    results,
  }
}

describe('pickFailedIds（BatchResultDrawer 仅重试失败项）', () => {
  it('result 为 null 时返回空数组（不渲染重试按钮）', () => {
    assert.deepEqual(pickFailedIds(null), [])
  })

  it('results 为空时返回空数组', () => {
    assert.deepEqual(pickFailedIds(batch([])), [])
  })

  it('混合批次：仅取 failed 项，保持 results 原顺序', () => {
    const result = batch([
      item('a-1', 'success'),
      item('a-2', 'failed', 'PRINT_SKU_DISABLED'),
      item('a-3', 'skipped', '幂等命中'),
      item('a-4', 'failed', 'STOCK_INSUFFICIENT'),
    ])
    assert.deepEqual(pickFailedIds(result), ['a-2', 'a-4'])
  })

  it('skipped（幂等命中/已处目标态）不重跑', () => {
    const result = batch([item('s-1', 'skipped', 'DUPLICATE_DATA_ID'), item('s-2', 'failed')])
    assert.deepEqual(pickFailedIds(result), ['s-2'])
  })

  it('成功项绝不重跑', () => {
    const result = batch([item('ok-1', 'success'), item('ok-2', 'success'), item('bad-1', 'failed')])
    assert.deepEqual(pickFailedIds(result), ['bad-1'])
  })

  it('全部失败：逐条 id 全量返回', () => {
    const result = batch([item('f-1', 'failed'), item('f-2', 'failed'), item('f-3', 'failed')])
    assert.deepEqual(pickFailedIds(result), ['f-1', 'f-2', 'f-3'])
  })

  it('验收场景 4：100 张批量打印 96/3/1 → 重试集合恰为 3 张失败且与 failed_count 对账', () => {
    const results: BatchResultItem[] = []
    for (let i = 1; i <= 96; i += 1) results.push(item(`print-${i}`, 'success'))
    results.push(item('print-97', 'failed', 'PRINT_SKU_DISABLED'))
    results.push(item('print-98', 'failed', 'STOCK_LOCKED'))
    results.push(item('print-99', 'failed', 'PRINT_SKU_DISABLED'))
    results.push(item('print-100', 'skipped', '幂等命中'))
    const result = batch(results)

    const failedIds = pickFailedIds(result)
    assert.equal(result.total, 100)
    assert.equal(result.success_count, 96)
    assert.equal(result.failed_count, 3)
    assert.equal(result.skipped_count, 1)
    assert.equal(failedIds.length, 3)
    assert.deepEqual(failedIds, ['print-97', 'print-98', 'print-99'])
    assert.equal(failedIds.length, result.failed_count)
  })
})
