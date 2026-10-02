import { useMemo } from 'react'
import { ConfigProvider, theme as antdTheme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { RouterProvider } from 'react-router'
import { useThemeStore } from '@/stores/theme'
import { router } from '@/router'

/**
 * antd Theme ↔ --sf-* Design Token 映射（frontend.md §2）。
 * 数值与 src/styles/tokens.css 保持一致：修改时两处同步。
 */
export function App() {
  const mode = useThemeStore((s) => s.mode)
  const dark = mode === 'dark'

  const themeConfig = useMemo(
    () => ({
      algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
      token: {
        colorPrimary: dark ? '#3c8dff' : '#1674ff',
        colorInfo: dark ? '#3c8dff' : '#1674ff',
        colorSuccess: dark ? '#6abf3f' : '#389e0d',
        colorWarning: dark ? '#e8b339' : '#d48806',
        colorError: dark ? '#e8564f' : '#cf1322',
        borderRadius: 6,
        colorBgLayout: dark ? '#11151a' : '#f5f6f8',
      },
      components: {
        Layout: {
          headerHeight: 48,
          headerPadding: '0 16px',
        },
        Table: {
          headerBg: dark ? '#1f252e' : '#f7f8fa',
          headerSplitColor: 'transparent',
          cellPaddingBlock: 12,
        },
        Card: {
          paddingLG: 20,
        },
      },
    }),
    [dark],
  )

  return (
    <ConfigProvider locale={zhCN} theme={themeConfig}>
      <RouterProvider router={router} />
    </ConfigProvider>
  )
}
