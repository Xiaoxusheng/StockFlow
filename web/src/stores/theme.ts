import { create } from 'zustand'

export type ThemeMode = 'light' | 'dark'

// web/index.html <head> 内联脚本镜像了本文件的首帧解析逻辑（防 FOUC 首帧闪白）：
// 修改 STORAGE_KEY / resolveInitialMode / applyToDocument 时必须同 commit 同步该脚本（注释互指）。
const STORAGE_KEY = 'sf.theme'

function resolveInitialMode(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function applyToDocument(mode: ThemeMode) {
  document.documentElement.dataset.theme = mode
}

interface ThemeState {
  mode: ThemeMode
  setMode: (mode: ThemeMode) => void
  toggleMode: () => void
}

/** 主题状态：Light/Dark 全端共用 Token（frontend.md §2.2），持久化到 localStorage */
export const useThemeStore = create<ThemeState>((set, get) => ({
  mode: resolveInitialMode(),
  setMode: (mode) => {
    localStorage.setItem(STORAGE_KEY, mode)
    applyToDocument(mode)
    set({ mode })
  },
  toggleMode: () => get().setMode(get().mode === 'light' ? 'dark' : 'light'),
}))

// 首次加载即同步 <html data-theme>
applyToDocument(resolveInitialMode())
