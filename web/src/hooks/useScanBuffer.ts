import { useCallback, useRef } from 'react'

/** 扫码输入模式（frontend.md §22 作业效率提升层一期） */
export type ScanInputMode = 'normal' | 'fast' | 'continuous'

/** 一次扫入的节奏元数据（页面据此决定是否自动推进下一步） */
export interface ScanMeta {
  /** 整段输入判定为扫码枪连发（首末字符间隔 ≤ GUN_INPUT_MS；人工逐字输入无法达到） */
  viaGun: boolean
  /** 处于连扫节奏（本次枪扫距上次枪扫 < FAST_WINDOW_MS，仅 mode='fast' 有意义） */
  fastRhythm: boolean
  /** continuous 模式下同码窗口内的累计次数（1 = 窗口内首次；人工输入恒为 1） */
  repeatCount: number
}

/** 扫码枪整段输入的首末字符间隔上限（ms）——HID 枪扫通常 <50ms，人工 ≥300ms */
const GUN_INPUT_MS = 150
/** 人工停顿视为新一轮输入的间隔（ms）：超过后首字符时间戳重置 */
const NEW_ROUND_MS = 1000
/** fast 模式连扫节奏窗口（ms）——与 devices 2s 去重窗口同量级 */
const FAST_WINDOW_MS = 2000
/** continuous 模式同码累计窗口（ms） */
const REPEAT_WINDOW_MS = 4000

export interface UseScanBufferOptions {
  mode: ScanInputMode
}

/**
 * HID 键盘模拟扫码的节奏判定缓冲（frontend.md §22 / scanner.md §2.3）：
 * - 输入缓冲解析限定在组件自身受控输入焦点内承接（一期口径，不做页面级 keydown 监听）；
 * - registerKey 在受控 Input 的 onChange 里登记字符时间戳（只保留首/尾，停顿超
 *   NEW_ROUND_MS 自动开启新一轮，不无限累积）；
 * - judge 在 Enter 提交时判定节奏元数据（枪扫/连扫/同码累计）；
 * - 判定只读不阻断：viaGun=false 的手工输入照常回调（manualInput 兜底，§3.3）。
 */
export function useScanBuffer({ mode }: UseScanBufferOptions) {
  const firstKeyAtRef = useRef<number>(0)
  const lastKeyAtRef = useRef<number>(0)
  const lastGunAtRef = useRef<number>(0)
  const lastGunCodeRef = useRef<string>('')
  const repeatCountRef = useRef<number>(0)

  /** 受控 Input onChange 时登记（每次字符变化调用） */
  const registerKey = useCallback(() => {
    const now = Date.now()
    if (now - lastKeyAtRef.current > NEW_ROUND_MS || firstKeyAtRef.current === 0) {
      firstKeyAtRef.current = now
    }
    lastKeyAtRef.current = now
  }, [])

  /** Enter 提交时判定本次扫入的节奏元数据 */
  const judge = useCallback(
    (code: string): ScanMeta => {
      const now = Date.now()
      const span = lastKeyAtRef.current - firstKeyAtRef.current
      // 单字符输入无法区分枪扫与人工，保守判人工（单字符码场景由手输兜底覆盖）
      const viaGun = span >= 0 && span <= GUN_INPUT_MS && firstKeyAtRef.current !== lastKeyAtRef.current

      let fastRhythm = false
      let repeatCount = 1
      if (viaGun) {
        fastRhythm = mode === 'fast' && now - lastGunAtRef.current < FAST_WINDOW_MS
        if (mode === 'continuous' && code === lastGunCodeRef.current && now - lastGunAtRef.current < REPEAT_WINDOW_MS) {
          repeatCount = repeatCountRef.current + 1
        }
        lastGunAtRef.current = now
        lastGunCodeRef.current = code
        repeatCountRef.current = repeatCount
      } else {
        // 人工输入打断节奏与累计
        repeatCountRef.current = 0
        lastGunCodeRef.current = ''
      }
      return { viaGun, fastRhythm, repeatCount }
    },
    [mode],
  )

  /** 提交后复位输入节奏缓冲（不清 lastGun 系列：连扫节奏跨条码保留） */
  const reset = useCallback(() => {
    firstKeyAtRef.current = 0
    lastKeyAtRef.current = 0
  }, [])

  return { registerKey, judge, reset }
}
