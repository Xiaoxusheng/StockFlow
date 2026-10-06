import { create } from 'zustand'
import type { ScanInputMode } from '@/hooks/useScanBuffer'

const STORAGE_KEY = 'sf.scan.mode'

function resolveInitialMode(): ScanInputMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'normal' || saved === 'fast' || saved === 'continuous') return saved
  return 'normal'
}

interface ScanState {
  /** 扫码三模式（scanner.md §6.4 / frontend.md §22）：normal 常规 / fast 快速 / continuous 连续。
   * 仅决定输入节奏与自动推进幅度，业务提交一律显式按键（requirements.md §2.10 禁止自动提交） */
  mode: ScanInputMode
  setMode: (mode: ScanInputMode) => void
}

/**
 * 扫码模式全局偏好（扫码作业优化，2026-10-06）：三模式为使用者偏好而非页面写死——
 * 持久化到 localStorage（theme.ts 同款模式），ScanInput 未传 mode prop 时读写本 store，
 * 同一作业员跨页面保持一致；页面显式传 mode 时为受控口径（覆盖 store）。
 */
export const useScanStore = create<ScanState>((set) => ({
  mode: resolveInitialMode(),
  setMode: (mode) => {
    localStorage.setItem(STORAGE_KEY, mode)
    set({ mode })
  },
}))
