import { useEffect, useRef, useState } from 'react'
import JsBarcode from 'jsbarcode'
import type { BarcodeSymbology } from '@/api/printing'

export interface BarcodeViewProps {
  value: string
  /** 码制（printing.md §4.1：Code128 默认推荐；与扫码解析端码制范围一致） */
  format?: BarcodeSymbology
  /** 模块宽度 px（打印场景用高分辨率 px 保证扫码枪识别，printing.md §6） */
  moduleWidth?: number
  /** 条高 px */
  height?: number
  /** 是否显示人眼可读文本 */
  displayValue?: boolean
  fontSize?: number
}

/**
 * 条码渲染（JsBarcode → SVG 矢量，printing.md §6「矢量或高分辨率渲染」）。
 * 数据不满足码制校验时降级渲染原文文本并保持占位，不渲染空白误导打印。
 */
export function BarcodeView({
  value,
  format = 'CODE128',
  moduleWidth = 2,
  height = 48,
  displayValue = true,
  fontSize = 12,
}: BarcodeViewProps) {
  const svgRef = useRef<SVGSVGElement>(null)
  const [valid, setValid] = useState(true)

  useEffect(() => {
    if (!svgRef.current || !value) return
    setValid(true)
    try {
      JsBarcode(svgRef.current, value, {
        format,
        width: moduleWidth,
        height,
        displayValue,
        fontSize,
        margin: 0,
        lineColor: '#000000',
        background: '#ffffff',
        valid: (isValid: boolean) => {
          if (!isValid) setValid(false)
        },
      })
    } catch {
      setValid(false)
    }
  }, [value, format, moduleWidth, height, displayValue, fontSize])

  if (!value) {
    return <span style={{ color: '#000000' }}>-</span>
  }

  return (
    <span style={{ display: 'inline-block', lineHeight: 0 }}>
      {/* svg 常驻挂载：JsBarcode 直接向其绘制；无效数据时隐藏并降级为文本 */}
      <svg ref={svgRef} style={{ display: valid ? 'block' : 'none' }} />
      {!valid && (
        <span style={{ display: 'inline-block', color: '#000000', fontSize, lineHeight: 1.2 }}>{value}</span>
      )}
    </span>
  )
}
