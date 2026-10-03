/**
 * Pad 布局原语统一出口（frontend.md §20 / §20.2 独立设计要求）。
 *
 * 页面组约定：
 * - 一律从 '@/layouts/pad' import，禁止复用 PcLayout / SfTable 密表格 / SfSearchForm（§20.2/§1.3）；
 * - pad.css（--sf-pad-* 局部 Token）由 PadLayout 引入一次，页面无需重复 import；
 * - 页面根容器：三栏作业页用 PadPageShell，首页用 .sf-pad-home（pad.css @media 控制横竖屏）。
 */
export { PadLayout, PAD_NAV_ITEMS } from './PadLayout'
export { PadPageShell, type PadPageShellProps } from './PadPageShell'
export {
  PadActionBar,
  type PadActionBarAction,
  type PadActionBarProps,
  type PadActionBarSlotKey,
} from './PadActionBar'
export { PadTaskCard, PAD_TASK_TYPE_LABEL, type PadTaskCardProps } from './PadTaskCard'
export { PadInfoCard, type PadInfoCardProps, type PadInfoItem } from './PadInfoCard'
export { PadScanStub, type PadScanStubProps } from './PadScanStub'
export { usePadOrientation, type PadOrientation } from './usePadOrientation'
