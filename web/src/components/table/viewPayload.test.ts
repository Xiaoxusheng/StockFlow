import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { cleanFilters, stableFiltersJson, toApplyValues } from './viewPayload.ts'

// ---------- 保存视图序列化/恢复测试（docs/testing.md §12.2 保存视图自动化） ----------
// 语义来源：SfViewBar（计划 §2.2 B5）——filters_json 序列化为「对账键」：空值剔除后
// String 归一并 stringify，保证「保存时的筛选」与「应用后回读的 appliedFilters」同形可比；
// 恢复 = toApplyValues 展开 filters_json → usePagedList.applyFilters 落 URL。

describe('cleanFilters（空值剔除唯一规则）', () => {
  it('剔除 undefined/null/空串，保留 0 与 false', () => {
    assert.deepEqual(cleanFilters({ a: '1', b: undefined, c: null, d: '', e: 0, f: false, g: 'x' }), {
      a: '1',
      e: 0,
      f: false,
      g: 'x',
    })
  })

  it('空对象剔除后仍为空对象', () => {
    assert.deepEqual(cleanFilters({}), {})
    assert.deepEqual(cleanFilters({ a: undefined, b: null, c: '' }), {})
  })
})

describe('stableFiltersJson（filters_json 序列化）', () => {
  it('空值剔除后 stringify：对象形状与输出一致', () => {
    assert.equal(stableFiltersJson({ a: '1', b: undefined, c: null, d: '', e: 'x' }), '{"a":"1","e":"x"}')
  })

  it('非字符串值 String 归一（数字 20 → "20"，对齐 URL 查询串形态）', () => {
    assert.equal(stableFiltersJson({ page: 20, flag: true }), '{"page":"20","flag":"true"}')
  })

  it('全空筛选序列化为 "{}"', () => {
    assert.equal(stableFiltersJson({ a: undefined, b: null, c: '' }), '{}')
    assert.equal(stableFiltersJson({}), '{}')
  })
})

describe('序列化/恢复闭环（SfViewBar 保存 → 恢复 → 标记对账不变量）', () => {
  it('保存时的筛选与应用后回读的 appliedFilters 序列化结果一致 → 视图标记不被清除', () => {
    // 1. 保存采集（SfViewBar.submitModal）：cleanFilters 生成 filters_json
    const currentFilters = { keyword: 'SKU001', status: 'NORMAL', empty: '' }
    const payloadFilters = cleanFilters(currentFilters) as Record<string, string>

    // 2. 恢复（SfViewBar.applyView）：toApplyValues 展开 → applyFilters 落 URL → appliedFilters 回读
    const view = { filters_json: payloadFilters }
    const appliedFilters = toApplyValues(view)

    // 3. 对账（SfViewBar effect）：stableFiltersJson(appliedFilters) === 保存时序列化键 → 标记保留
    const savedJson = stableFiltersJson(currentFilters)
    assert.equal(stableFiltersJson(appliedFilters), savedJson)
  })

  it('URL 回读带数字形态分页值时归一后仍可比（20 → "20"）', () => {
    const savedJson = stableFiltersJson({ keyword: 'SKU001', pageSize: 20 })
    const appliedFromUrl = { keyword: 'SKU001', pageSize: '20' }
    assert.equal(stableFiltersJson(appliedFromUrl), savedJson)
  })

  it('用户改动任一筛选 → 序列化偏离保存键 → 标记清除（回归「自定义」）', () => {
    const savedJson = stableFiltersJson({ keyword: 'SKU001', status: 'NORMAL' })
    const drifted = stableFiltersJson({ keyword: 'SKU002', status: 'NORMAL' })
    const cleared = stableFiltersJson({})
    assert.notEqual(drifted, savedJson)
    assert.notEqual(cleared, savedJson)
  })

  it('toApplyValues 对空 filters_json 兜底为空对象（防御后端 JSON 列 null）', () => {
    assert.deepEqual(toApplyValues({ filters_json: null as unknown as Record<string, string> }), {})
    assert.deepEqual(toApplyValues({ filters_json: {} }), {})
  })

  it('toApplyValues 返回副本：应用视图不反写视图对象', () => {
    const view = { filters_json: { keyword: 'SKU001' } }
    const values = toApplyValues(view)
    values.keyword = 'MUTATED'
    assert.equal(view.filters_json.keyword, 'SKU001')
  })
})
