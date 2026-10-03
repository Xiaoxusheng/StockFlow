import type { CSSProperties, ReactNode } from 'react'
import { resolvePaper, type PrintPaperSpec } from '@/api/printing'

export interface PrintPaperSheetProps {
  /** PRINT_PAPERS 键或规格对象 */
  paper: string | PrintPaperSpec
  children: ReactNode
  /** 内边距（mm），标签纸窄边距、单据纸宽边距由调用方给定 */
  paddingMm?: number
  /** 打印分页：非末页输出 break-after，避免末尾空白页 */
  pageBreak?: boolean
}

/**
 * 模板纸张渲染器（printing.md §2/§6）：按 A4/A5/热敏规格以毫米精确铺纸。
 *
 * 样式隔离（printing.md §6「独立打印渲染层，与系统页面样式隔离」）：
 * 内容全部内联样式 + 独立字体栈，不依赖 --sf-* Token 与全局 CSS；
 * 下方 <style> 仅供本组件屏幕态阴影使用，@media print 中归零，
 * 不影响宿主页面，也保证打印输出为纯净白底黑字。
 */
export function PrintPaperSheet({ paper, children, paddingMm = 6, pageBreak = false }: PrintPaperSheetProps) {
  const spec = typeof paper === 'string' ? resolvePaper(paper) : paper
  if (!spec) {
    return <span style={{ color: '#000000' }}>未知纸张规格</span>
  }

  const sheetStyle: CSSProperties = {
    width: `${spec.widthMm}mm`,
    minHeight: `${spec.heightMm}mm`,
    boxSizing: 'border-box',
    padding: `${paddingMm}mm`,
    background: '#ffffff',
    color: '#000000',
    fontFamily:
      "'Microsoft YaHei', 'PingFang SC', 'Helvetica Neue', Arial, sans-serif",
    overflow: 'hidden',
    breakAfter: pageBreak ? 'page' : 'auto',
  }

  return (
    <div className="sf-print-sheet" style={sheetStyle}>
      <style>{`
.sf-print-sheet { box-shadow: 0 2px 8px rgba(0,0,0,.15); }
@media print { .sf-print-sheet { box-shadow: none; } }
      `}</style>
      {children}
    </div>
  )
}
