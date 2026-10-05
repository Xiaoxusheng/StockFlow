/**
 * SKU 标签纸张布局纯函数（docs/qr-code.md §7.3「纸张布局规范」与本文件同文；
 * printing.md §6 注记同源）。只做尺寸/分片/换算计算，不做渲染：
 * A4/A5 网格分片渲染见 PrintLabelGrid，预览分页编排见 PrintPreviewPage。
 */
import { resolvePaper, type PrintContentRow } from '@/api/printing'

/** A4/A5 网格分片的最小标签单元（mm）。预制模切纸（如 3×8 排列 70×36mm）按纸样调整此常量，
 * 实纸验证口径见 docs/testing.md §6（qr-code.md §7.3） */
export const LABEL_CELL_MM = { widthMm: 50, heightMm: 30 } as const

/** 网格容量算法页边距（mm）：cols=floor((纸宽−12)/50)、rows=floor((纸高−12)/30) */
export const LABEL_PAGE_MARGIN_MM = 12

/** mm → px 换算（CSS 96dpi：1mm≈3.7795px；标签内 QR 等按 mm 设计后转 px 渲染，qr-code.md §7.3） */
export const MM_TO_PX = 3.7795

/** 毫米转像素（四舍五入到整 px，避免亚像素抖动） */
export function mmToPx(mm: number): number {
  return Math.round(mm * MM_TO_PX)
}

/** 是否 A4/A5 网格纸（标签类分片布局适用纸张；热敏纸一律一码一页） */
export function isGridLabelPaper(paper: string | null | undefined): boolean {
  return paper === 'A4' || paper === 'A5'
}

/** QR 印刷尺寸按纸张计算（mm；qr-code.md §7.3 表格逐字：40×30→20、60×40→28、100×50→34、A4/A5 网格单元→22） */
export function labelQrSizeMm(paper: string | null | undefined): number {
  switch (paper) {
    case 'THERMAL_40_30':
      return 20
    case 'THERMAL_60_40':
      return 28
    case 'THERMAL_100_50':
      return 34
    default:
      return 22
  }
}

/** 纠错级别：QR 尺寸 <24mm 升 Q，否则 M（qr-code.md §7.3；约束 9「默认 M、小尺寸自动升 Q」） */
export function labelQrLevel(qrSizeMm: number): 'M' | 'Q' {
  return qrSizeMm < 24 ? 'Q' : 'M'
}

/** 标签纸内边距（mm）：热敏窄边距 2mm；A4/A5 网格 6mm（四边合计 12mm，与容量算法页边距对应）；
 * 单据类沿用 6mm 宽边距（既有口径不变） */
export function labelSheetPaddingMm(paper: string | null | undefined): number {
  if (paper === 'THERMAL_40_30' || paper === 'THERMAL_60_40' || paper === 'THERMAL_100_50') return 2
  return 6
}

/** 网格容量：cols×rows 与单片张数 */
export interface LabelGridCapacity {
  cols: number
  rows: number
  perSheet: number
}

/** A4/A5 网格容量：A4=3×9=27 张/页、A5=2×6=12 张/页；热敏纸/未知纸 1×1=1（一码一页，qr-code.md §7.3） */
export function labelGridCapacity(paper: string | null | undefined): LabelGridCapacity {
  const spec = resolvePaper(paper)
  if (!spec || !isGridLabelPaper(spec.key)) return { cols: 1, rows: 1, perSheet: 1 }
  const cols = Math.max(Math.floor((spec.widthMm - LABEL_PAGE_MARGIN_MM) / LABEL_CELL_MM.widthMm), 1)
  const rows = Math.max(Math.floor((spec.heightMm - LABEL_PAGE_MARGIN_MM) / LABEL_CELL_MM.heightMm), 1)
  return { cols, rows, perSheet: cols * rows }
}

/** 内容行分片：A4/A5 网格纸按容量切片（每片一张纸，分页不截断标签）；热敏纸每行一片（一码一页） */
export function chunkLabelRows(rows: PrintContentRow[], paper: string | null | undefined): PrintContentRow[][] {
  const { perSheet } = labelGridCapacity(paper)
  if (perSheet <= 1) return rows.map((row) => [row])
  const chunks: PrintContentRow[][] = []
  for (let index = 0; index < rows.length; index += perSheet) {
    chunks.push(rows.slice(index, index + perSheet))
  }
  return chunks
}
