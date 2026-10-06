import { Drawer, Tag, Typography } from 'antd'
import { isShortcutEnabled, type ShortcutItem } from './ShortcutProvider'

// ---------- 快捷键帮助面板（计划 §2.9 / frontend.md §32.4） ----------
// 按注册表驱动渲染，零手写文档；Esc 归 antd Drawer 自身（ShortcutProvider 不双抢）。
// 被禁用（enabled=false 或求值为 false）的键位不展示——面板与实际行为一致（§32.4）。
// 样式走 --sf-* Token，动效仅 antd Drawer 自带进出场（frontend.md §25/§31）。

const SCOPE_TITLES: Record<ShortcutItem['scope'], string> = {
  global: '全局（输入框内同样生效）',
  page: '页面（输入框内自动停用）',
}

function ComboTag({ combo }: { combo: string }) {
  return (
    <Tag
      bordered
      style={{
        marginInlineEnd: 0,
        fontFamily: 'monospace',
        color: 'var(--sf-text)',
        backgroundColor: 'var(--sf-surface-hover)',
        borderColor: 'var(--sf-border)',
      }}
    >
      {combo}
    </Tag>
  )
}

export function ShortcutHelpDrawer({
  open,
  items,
  onClose,
}: {
  open: boolean
  items: readonly ShortcutItem[]
  onClose: () => void
}) {
  const scopes: ShortcutItem['scope'][] = ['global', 'page']
  return (
    <Drawer
      title="键盘快捷键"
      open={open}
      onClose={onClose}
      width={360}
      // Esc 由 antd Drawer 自身处理（计划 §2.9：不双抢）
      maskClosable
    >
      {scopes.map((scope) => {
        const group = items.filter((it) => it.scope === scope && isShortcutEnabled(it))
        if (group.length === 0) return null
        return (
          <section key={scope} style={{ marginBottom: 'var(--sf-space-4)' }}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {SCOPE_TITLES[scope]}
            </Typography.Text>
            <ul style={{ listStyle: 'none', margin: 'var(--sf-space-2) 0 0', padding: 0 }}>
              {group.map((it) => (
                <li
                  key={`${it.combo}-${it.description}`}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--sf-space-2)',
                    padding: 'var(--sf-space-1) 0',
                  }}
                >
                  <ComboTag combo={it.combo} />
                  <span style={{ color: 'var(--sf-text)', textAlign: 'right' }}>{it.description}</span>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        G 系跳转：先按 G，800ms 内按第二个键（I / P / S / T）。
      </Typography.Text>
    </Drawer>
  )
}
