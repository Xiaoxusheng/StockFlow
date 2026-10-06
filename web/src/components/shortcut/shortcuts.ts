import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { App } from 'antd'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { useShortcutRegister, type ShortcutItem } from './ShortcutProvider'

// ---------- 全站集中快捷键注册表（计划 §2.9） ----------
// 由 PcLayout 内 <ShortcutBuiltinRegistrar/> 挂载一次性注册；帮助面板（ShortcutHelpDrawer）
// 按同一注册表驱动渲染，零手写文档。除本文件与后续 useNextTask 等经 register API 的注册点外，
// 任何组件禁止自建 window keydown 监听（scanner.md §3.1「业务页禁自监听键盘」PC 落地）。
// 匹配与分发规则（输入态抑制/chord 窗口/preventDefault）由 ShortcutProvider 统一承担。

export type { ShortcutItem }

/**
 * 全站内置快捷键（计划 §2.9 冻结清单）：
 * - Ctrl/Cmd+K 打开全局搜索（global：输入态仍生效）
 * - Ctrl/Cmd+R 刷新当前页活跃查询（TanStack Query active refetch，接管浏览器刷新）
 * - ? 打开快捷键帮助（page：输入态禁用——输入 ? 是打字不是指令）
 * - Ctrl/Cmd+F 聚焦当前列表搜索表单首输入框（page）
 * - G 系 chord 跳转（page）：G+I→/inventory/stock、G+P→/purchases、G+S→/sales、
 *   G+T→/tasks；跳前 canAccess 校验，无权限 message 提示不跳转
 * - Esc 不在此注册：antd Modal/Drawer 的 Esc 归 antd 自身，不双抢（计划 §2.9）
 */
export function ShortcutBuiltinRegistrar(): null {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const register = useShortcutRegister()

  useEffect(() => {
    // user 从 zustand 快照读取（会话快照仅在登录/自愈时变化；store 订阅会导致
    // 注册表频繁重建——快照变化伴随整页会话重建，取渲染时刻值即可）
    const user = useAuthStore.getState().user

    // G 系跳转目标（计划 §2.9 冻结）；/tasks 菜单码与侧边栏同源（inventory:stock:view，
    // 经 RESOURCE_ALIASES 归一命中，见 config/menu.tsx 注释）
    const gTargets: ReadonlyArray<{ key: string; path: string; permission?: string; name: string }> = [
      { key: 'i', path: '/inventory/stock', permission: 'inventory:stock:view', name: '实时库存' },
      { key: 'p', path: '/purchases', permission: 'purchase:view', name: '采购订单' },
      { key: 's', path: '/sales', permission: 'sales:view', name: '销售订单' },
      { key: 't', path: '/tasks', permission: 'inventory:stock:view', name: '我的任务' },
    ]

    const unregisterFns = [
      register({
        combo: 'Ctrl/Cmd+K',
        scope: 'global',
        description: '打开全局搜索',
        handler: (_e, deps) => deps.openSearch(),
      }),
      register({
        combo: 'Ctrl/Cmd+R',
        scope: 'global',
        description: '刷新当前页面数据',
        handler: () => {
          void queryClient.invalidateQueries({ refetchType: 'active' })
        },
      }),
      register({
        combo: '?',
        scope: 'page',
        description: '打开快捷键帮助',
        handler: (_e, deps) => deps.openHelp(),
      }),
      register({
        combo: 'Ctrl/Cmd+F',
        scope: 'page',
        description: '聚焦列表搜索',
        handler: (_e, deps) => {
          // SfSearchForm 渲染于 .sf-page 内首个 antd Form 的首输入框；无表单页给出真实反馈
          const input = document.querySelector<HTMLInputElement>(
            '.sf-page form .ant-form-item input',
          )
          if (input) {
            input.focus()
            input.select()
          } else {
            deps.message.warning('当前页面没有搜索表单')
          }
        },
      }),
      ...gTargets.map((t) =>
        register({
          combo: `G ${t.key.toUpperCase()}`,
          chordGroup: 'g',
          chordKey: t.key,
          scope: 'page',
          description: `跳转到${t.name}`,
          handler: (_e, deps) => {
            if (!canAccess(user, t.permission)) {
              deps.message.warning(`没有访问「${t.name}」的权限`)
              return
            }
            navigate(t.path)
          },
        }),
      ),
    ]
    return () => unregisterFns.forEach((fn) => fn())
    // user 取 zustand 渲染时刻快照（非 hook 返回值，不入 deps）；navigate/queryClient/
    // message/register 均为稳定引用——全量列出后 disable 指令不再需要
  }, [navigate, queryClient, message, register])

  return null
}
