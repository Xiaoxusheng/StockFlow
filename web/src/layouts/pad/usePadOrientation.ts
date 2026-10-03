import { useSyncExternalStore } from 'react'

export type PadOrientation = 'landscape' | 'portrait'

const LANDSCAPE_QUERY = '(orientation: landscape)'

function subscribe(onChange: () => void): () => void {
  const mql = window.matchMedia(LANDSCAPE_QUERY)
  mql.addEventListener('change', onChange)
  return () => mql.removeEventListener('change', onChange)
}

function getSnapshot(): PadOrientation {
  return window.matchMedia(LANDSCAPE_QUERY).matches ? 'landscape' : 'portrait'
}

/** 首帧兜底：横屏优先（frontend.md §20.2），防首帧闪烁 */
function getServerSnapshot(): PadOrientation {
  return 'landscape'
}

/**
 * Pad 横竖屏状态（frontend.md §20.8）：matchMedia('(orientation: landscape)') 监听，
 * 经 useSyncExternalStore 返回 'landscape' | 'portrait'。
 *
 * 分工约定：
 * - JS 状态驱动**结构性切换**——PadPageShell 横屏渲染三栏、竖屏渲染堆叠（DOM 不同，非压缩同一布局）；
 * - 纯视觉差异（栏间距/字号微调）由 pad.css 的 @media (orientation: ...) 双轨兜底，
 *   PadLayout 根节点同时挂 data-orientation 属性。
 * 只判 orientation，不写死具体像素宽高（frontend.md §19.1 五断点要求）。
 */
export function usePadOrientation(): PadOrientation {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
