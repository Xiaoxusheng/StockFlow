import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import type { MessageInstance } from 'antd/es/message/interface'
import {
  CHORD_WINDOW_MS,
  dispatchShortcut,
  isEditableElement,
  type ShortcutDeps,
  type ShortcutItem,
  type ShortcutScope,
} from './shortcutMatcher.ts'

// ---------- 快捷键纯逻辑测试（docs/testing.md §12.2 快捷键与输入框冲突矩阵自动化） ----------
// 规则冻结来源：效率层一期计划 §2.9 / scanner.md §3.1——
// 焦点在输入框/文本域/下拉/可编辑区时页面级快捷键全部禁用；global 级仍生效；
// 扫码输入框（[data-sf-scan-input]）内击键不触发快捷键；Esc 不双抢 antd Modal/Drawer。

function mkItem(over: Partial<ShortcutItem> & { combo: string; scope: ShortcutScope }): ShortcutItem {
  return {
    description: over.description ?? '',
    handler: over.handler ?? (() => {}),
    ...over,
  }
}

const noopDeps: ShortcutDeps = {
  openSearch: () => {},
  openHelp: () => {},
  message: {} as unknown as MessageInstance,
}

function evt(over: Partial<{ key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; shiftKey: boolean }>) {
  return {
    key: 'x',
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    ...over,
  }
}

describe('isEditableElement（输入态冲突矩阵判定核心）', () => {
  it('INPUT/TEXTAREA/SELECT 视为输入态', () => {
    assert.equal(isEditableElement({ tagName: 'INPUT', closest: () => null }), true)
    assert.equal(isEditableElement({ tagName: 'TEXTAREA', closest: () => null }), true)
    assert.equal(isEditableElement({ tagName: 'SELECT', closest: () => null }), true)
  })

  it('isContentEditable 可编辑区视为输入态', () => {
    assert.equal(isEditableElement({ tagName: 'DIV', isContentEditable: true, closest: () => null }), true)
  })

  it('扫码输入框标记 [data-sf-scan-input]（含祖先命中）视为输入态', () => {
    assert.equal(isEditableElement({ tagName: 'INPUT', closest: () => ({}) }), true)
    assert.equal(isEditableElement({ tagName: 'DIV', closest: (sel) => (sel === '[data-sf-scan-input]' ? {} : null) }), true)
  })

  it('普通元素（BUTTON/DIV 且无扫码标记）不是输入态', () => {
    assert.equal(isEditableElement({ tagName: 'BUTTON', closest: () => null }), false)
    assert.equal(isEditableElement({ tagName: 'DIV', closest: () => null }), false)
  })
})

describe('dispatchShortcut：输入态下页面级快捷键禁用（冲突矩阵）', () => {
  const pageItem = mkItem({ combo: 'x', scope: 'page' })
  const globalSearch = mkItem({ combo: 'ctrl/cmd+k', scope: 'global' })

  it('非输入态：page 条目正常触发', () => {
    const outcome = dispatchShortcut({
      items: [pageItem],
      event: evt({ key: 'x' }),
      editable: false,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'fire', item: pageItem })
  })

  it('输入态（输入框聚焦）：page 条目禁用 → none', () => {
    const outcome = dispatchShortcut({
      items: [pageItem],
      event: evt({ key: 'x' }),
      editable: true,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })

  it('输入态：global 级（Ctrl+K）仍生效', () => {
    const outcome = dispatchShortcut({
      items: [pageItem, globalSearch],
      event: evt({ key: 'k', ctrlKey: true }),
      editable: true,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'fire', item: globalSearch })
  })

  it('输入态：裸 g 不启动 chord 序列窗口', () => {
    const chord = mkItem({ combo: 'G I', scope: 'page', chordGroup: 'g', chordKey: 'i' })
    const outcome = dispatchShortcut({
      items: [chord],
      event: evt({ key: 'g' }),
      editable: true,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })
})

describe('dispatchShortcut：G 系 chord 序列窗口（800ms）', () => {
  const chord = mkItem({ combo: 'G I', scope: 'page', chordGroup: 'g', chordKey: 'i' })

  it('裸 g（无修饰、非输入态）启动窗口：until = now + 800', () => {
    const outcome = dispatchShortcut({ items: [chord], event: evt({ key: 'g' }), editable: false, pending: null, now: 1000 })
    assert.deepEqual(outcome, { kind: 'start-chord', group: 'g', until: 1000 + CHORD_WINDOW_MS })
  })

  it('窗口内第二键命中同组 chord 条目', () => {
    const outcome = dispatchShortcut({
      items: [chord],
      event: evt({ key: 'i' }),
      editable: false,
      pending: { group: 'g', until: 2000 },
      now: 1500,
    })
    assert.deepEqual(outcome, { kind: 'fire', item: chord })
  })

  it('窗口过期（now > until）不命中', () => {
    const outcome = dispatchShortcut({
      items: [chord],
      event: evt({ key: 'i' }),
      editable: false,
      pending: { group: 'g', until: 2000 },
      now: 2001,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })

  it('输入态下 pending 窗口不命中（清窗流转）', () => {
    const outcome = dispatchShortcut({
      items: [chord],
      event: evt({ key: 'i' }),
      editable: true,
      pending: { group: 'g', until: 2000 },
      now: 1500,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })

  it('无 chord 条目注册时裸 g 不启动窗口、不拦截', () => {
    const pageItem = mkItem({ combo: 'x', scope: 'page' })
    const outcome = dispatchShortcut({ items: [pageItem], event: evt({ key: 'g' }), editable: false, pending: null, now: 0 })
    assert.deepEqual(outcome, { kind: 'none' })
  })
})

describe('dispatchShortcut：combo 匹配与修饰键', () => {
  it('ctrl/cmd 组合：Ctrl 与 Cmd 均可命中，无修饰不命中', () => {
    const item = mkItem({ combo: 'ctrl/cmd+k', scope: 'global' })
    const fire = (event: ReturnType<typeof evt>) =>
      dispatchShortcut({ items: [item], event, editable: false, pending: null, now: 0 })
    assert.equal(fire(evt({ key: 'k', ctrlKey: true })).kind, 'fire')
    assert.equal(fire(evt({ key: 'k', metaKey: true })).kind, 'fire')
    assert.equal(fire(evt({ key: 'k' })).kind, 'none')
  })

  it('裸键位在按下 Ctrl/Alt 时误触被拦（防 Ctrl+K 命中 k 条目）', () => {
    const item = mkItem({ combo: 'k', scope: 'page' })
    const outcome = dispatchShortcut({
      items: [item],
      event: evt({ key: 'k', ctrlKey: true }),
      editable: false,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })

  it('shift 修饰必须匹配', () => {
    const item = mkItem({ combo: 'shift+f', scope: 'page' })
    const run = (shiftKey: boolean) =>
      dispatchShortcut({ items: [item], event: evt({ key: 'f', shiftKey }), editable: false, pending: null, now: 0 })
    assert.equal(run(true).kind, 'fire')
    assert.equal(run(false).kind, 'none')
  })

  it('方向键别名归一：注册 → 匹配 ArrowRight', () => {
    const item = mkItem({ combo: '→', scope: 'page' })
    const outcome = dispatchShortcut({ items: [item], event: evt({ key: 'ArrowRight' }), editable: false, pending: null, now: 0 })
    assert.deepEqual(outcome, { kind: 'fire', item })
  })

  it('注册表无 Esc 条目：Esc 返回 none（Esc 归 antd Modal/Drawer，不双抢）', () => {
    const outcome = dispatchShortcut({
      items: [mkItem({ combo: 'x', scope: 'page' }), mkItem({ combo: 'ctrl/cmd+k', scope: 'global' })],
      event: evt({ key: 'Escape' }),
      editable: false,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'none' })
  })
})

describe('dispatchShortcut：enabled 开关与注册顺序', () => {
  it('enabled:false 条目被跳过，后续匹配条目生效', () => {
    const disabled = mkItem({ combo: 'x', scope: 'page', enabled: false })
    const enabled = mkItem({ combo: 'x', scope: 'page' })
    const outcome = dispatchShortcut({
      items: [disabled, enabled],
      event: evt({ key: 'x' }),
      editable: false,
      pending: null,
      now: 0,
    })
    assert.deepEqual(outcome, { kind: 'fire', item: enabled })
  })

  it('enabled 为求值函数 false 时同样跳过', () => {
    const item = mkItem({ combo: 'x', scope: 'page', enabled: () => false })
    const outcome = dispatchShortcut({ items: [item], event: evt({ key: 'x' }), editable: false, pending: null, now: 0 })
    assert.deepEqual(outcome, { kind: 'none' })
  })

  it('按注册顺序取第一个命中条目', () => {
    const first = mkItem({ combo: 'x', scope: 'page', description: 'first' })
    const second = mkItem({ combo: 'x', scope: 'page', description: 'second' })
    const outcome = dispatchShortcut({ items: [first, second], event: evt({ key: 'x' }), editable: false, pending: null, now: 0 })
    assert.ok(outcome.kind === 'fire' && outcome.item.description === 'first')
  })

  it('chord 条目不参与普通 combo 匹配（仅序列窗口命中）', () => {
    const chord = mkItem({ combo: 'G I', scope: 'page', chordGroup: 'g', chordKey: 'i' })
    const outcome = dispatchShortcut({ items: [chord], event: evt({ key: 'i' }), editable: false, pending: null, now: 0 })
    assert.deepEqual(outcome, { kind: 'none' })
  })
})

describe('dispatchShortcut：handler 与 deps 注入（Provider 落地路径的输入契约）', () => {
  it('fire 携带条目本体供 Provider 调 handler', () => {
    let called = 0
    const item = mkItem({ combo: 'x', scope: 'page', handler: () => { called += 1 } })
    const outcome = dispatchShortcut({ items: [item], event: evt({ key: 'x' }), editable: false, pending: null, now: 0 })
    assert.ok(outcome.kind === 'fire')
    outcome.item.handler({ key: 'x' } as KeyboardEvent, noopDeps)
    assert.equal(called, 1)
  })
})
