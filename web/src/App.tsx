import { useMemo, useState } from 'react'
import { App as AntdApp, ConfigProvider, theme as antdTheme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { RouterProvider } from 'react-router'
import { useThemeStore } from '@/stores/theme'
import { router } from '@/router'

/**
 * antd Theme ↔ --sf-* Design Token 映射（frontend.md §2）。
 * 数值与 src/styles/tokens.css 保持一致：修改时两处必须同 commit 同步。
 */
export function App() {
  const mode = useThemeStore((s) => s.mode)
  const dark = mode === 'dark'
  // 系统减少动效偏好（任务书 §59）：挂载时读一次——OS 偏好极少会话中途切换，不做监听，
  // 中途变更刷新页面生效（图表侧 useSfChartTheme 独立实时读取，不依赖本值）。
  const [reducedMotion] = useState(
    () => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches,
  )

  const themeConfig = useMemo(
    () => ({
      algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
      // 系统减少动效：antd 官方开关（seed.motion；alias.js motion:false → Fast/Mid/Slow
      // 三档时长覆写 0s，且优先于下方显式时长）——Modal/Drawer/Menu 等组件内部动效全站归零，
      // 与 global.css 文尾 token 驱动的 reduced-motion 归零同口径。
      motion: !reducedMotion,
      token: {
        // 状态色 ↔ --sf-primary/-info/-success/-warning/-danger
        colorPrimary: dark ? '#4c7ef0' : '#2563eb',
        colorInfo: dark ? '#22b8cf' : '#0891b2',
        // 链接色显式对齐品牌主色：antd colorLink 缺省取 colorInfo（青蓝 #0891b2），
        // 与主按钮 #2563eb 异色显割裂——表格操作列/文字链接全站与主色同源；
        // Dark 直接复用暗色主色档（对 #171b21 底对比 ≈4.6:1 达 AA）
        colorLink: dark ? '#4c7ef0' : '#2563eb',
        colorSuccess: dark ? '#3fb950' : '#16a34a',
        colorWarning: dark ? '#e8a33d' : '#d97706',
        colorError: dark ? '#e8564f' : '#dc2626',
        // 表面 ↔ --sf-bg / --sf-surface（2026-10-07 三次调整：Light 页面背景**纯白**
        // （用户口径「页面更白更干净」），卡片层次靠 --sf-shadow-card 阴影 + 边框区分；
        // Dark 页面 #101418 与卡片白两级分层不变）
        colorBgLayout: dark ? '#101418' : '#ffffff',
        colorBgContainer: dark ? '#171b21' : '#ffffff',
        // 边框 ↔ --sf-border / --sf-border-subtle
        colorBorder: dark ? 'rgba(255, 255, 255, 0.08)' : '#e5e7eb',
        colorBorderSecondary: dark ? 'rgba(255, 255, 255, 0.05)' : '#f1f3f5',
        // 文字三档 ↔ --sf-text / -secondary / -muted
        colorText: dark ? 'rgba(255, 255, 255, 0.9)' : '#111827',
        colorTextSecondary: dark ? 'rgba(255, 255, 255, 0.6)' : '#64748b',
        colorTextTertiary: dark ? 'rgba(255, 255, 255, 0.38)' : '#94a3b8',
        // 控件级圆角 = --sf-radius-sm；字号/字体 ↔ --sf-font-*（保证浮层字体一致）
        borderRadius: 6,
        // 控件统一高度下限（任务书 §24：Form 控件 32~36px）
        controlHeight: 32,
        // 动效三档时长 ↔ --sf-motion-fast/-normal/-slow（120/180/220ms；antd 默认由
        // motionUnit 0.1s 派生 0.1/0.2/0.3s 与 Design Token 不一致，显式对齐——
        // Button/Modal 等组件内部动效经 motionDurationMid 等消费）
        motionDurationFast: '0.12s',
        motionDurationMid: '0.18s',
        motionDurationSlow: '0.22s',
        fontSize: 14,
        fontFamily:
          "Inter, 'PingFang SC', 'Microsoft YaHei', 'Noto Sans SC', system-ui, -apple-system, 'Segoe UI', sans-serif",
      },
      components: {
        Layout: {
          headerHeight: 48,
          headerPadding: '0 16px',
        },
        Descriptions: {
          // 详情摘要标签（采购单号/仓库/供应商等）升到正文色（2026-10-07 用户口径：
          // 标签灰阶层次信息密度不突出——antd 6 缺省 labelColor 取 colorTextTertiary
          // #94a3b8，比次要灰还浅一档；改为与正文同色后标签/值仅靠底色区分）。
          // Dark 同步升到正文档 rgba(255,255,255,0.9)，与 --sf-text 映射同源。
          labelColor: dark ? 'rgba(255, 255, 255, 0.9)' : '#111827',
        },
        Table: {
          headerBg: dark ? '#1d232b' : '#f8fafc',
          headerSplitColor: 'transparent',
          // 表头文字比正文弱一档（--sf-table-header-text）
          headerColor: dark ? 'rgba(255, 255, 255, 0.72)' : '#334155',
          rowHoverBg: dark ? '#232a33' : '#f8fafc',
          // 选中行=主题色低透明（任务书 §17 alpha 0.04~0.08，禁止整行深蓝）：
          // Dark 取区间上限 0.08（0.06 在暗底对比不足），hover 同相 +0.04 与 Light 节奏一致
          rowSelectedBg: dark ? 'rgba(76, 126, 240, 0.08)' : 'rgba(37, 99, 235, 0.06)',
          rowSelectedHoverBg: dark ? 'rgba(76, 126, 240, 0.12)' : 'rgba(37, 99, 235, 0.1)',
          cellPaddingBlock: 12,
          // 全站表格统一 13px 密度字号（antd 同步派生 cellFontSize，表头/单元格/汇总行一致）
          fontSize: 13,
        },
        Card: {
          paddingLG: 20,
          // antd 6.6.5：Card 消费全局 borderRadiusLG（es/card/style/index.js:27,217），
          // 组件级覆盖 = --sf-radius-md 8px（任务书 §10）
          borderRadiusLG: 8,
        },
        Modal: {
          // antd 6.6.5：Modal 消费全局 borderRadiusLG（es/modal/style/index.js:111,127,175），
          // 组件级覆盖 = --sf-radius-lg 10px（任务书 §25）
          borderRadiusLG: 10,
        },
      },
    }),
    [dark, reducedMotion],
  )

  return (
    <ConfigProvider locale={zhCN} theme={themeConfig}>
      {/* component={false}：仅提供 App.useApp() 上下文（message/modal 静态替代），
          不渲染包裹 DOM，避免破坏既有布局结构 */}
      <AntdApp component={false}>
        <RouterProvider router={router} />
      </AntdApp>
    </ConfigProvider>
  )
}
