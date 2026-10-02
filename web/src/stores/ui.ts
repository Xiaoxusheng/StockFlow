import { create } from 'zustand'

interface UiState {
  siderCollapsed: boolean
  toggleSider: () => void
}

/** 界面偏好分片：侧边栏折叠等纯 UI 状态 */
export const useUiStore = create<UiState>((set) => ({
  siderCollapsed: false,
  toggleSider: () => set((s) => ({ siderCollapsed: !s.siderCollapsed })),
}))
