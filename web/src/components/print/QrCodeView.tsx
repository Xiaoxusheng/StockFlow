import { useEffect, useState, type CSSProperties } from 'react'
import QRCode from 'qrcode'

export interface QrCodeViewProps {
  value: string
  /** 输出尺寸 px（SVG 矢量渲染，打印放大不失真，printing.md §6） */
  size?: number
  /** 纠错级别（标签磨损场景建议 M/H；SKU 标签按纸张尺寸自动升降，见 labelSheet.labelQrLevel） */
  level?: 'L' | 'M' | 'Q' | 'H'
  /** 静区模块数，默认 4（qr-code.md §7.3 Quiet Zone：margin=4 全站统一，qr-code 库参数） */
  margin?: number
  /** 渲染形态：preview 屏幕预览（默认）/ print 打印渲染层（输出强制 print-color-adjust:exact，
   * 保证白底黑码在打印层不被省色优化，约束 9） */
  variant?: 'preview' | 'print'
}

/**
 * 二维码渲染（qrcode → SVG 矢量字符串，printing.md §6「矢量或高分辨率渲染」）：
 * 全站统一出口——库位二维码 / 箱码 / 托盘码 / 单据二维码 / SKU SFQR 码共用（printing.md §4.2）。
 * 黑码白底、Quiet Zone（margin 默认 4）、SVG 矢量、不持久化（QR=身份协议，qr-code.md §1）。
 */
export function QrCodeView({ value, size = 96, level = 'M', margin = 4, variant = 'preview' }: QrCodeViewProps) {
  const [svg, setSvg] = useState('')
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    setFailed(false)
    if (!value) {
      setSvg('')
      return
    }
    QRCode.toString(value, {
      type: 'svg',
      width: size,
      margin,
      errorCorrectionLevel: level,
      color: { dark: '#000000', light: '#ffffff' },
    })
      .then((out) => {
        if (!cancelled) setSvg(out)
      })
      .catch(() => {
        if (!cancelled) {
          setSvg('')
          setFailed(true)
        }
      })
    return () => {
      cancelled = true
    }
  }, [value, size, level, margin])

  const frameStyle: CSSProperties = {
    display: 'inline-block',
    width: size,
    height: size,
    lineHeight: 0,
    ...(variant === 'print' ? { printColorAdjust: 'exact', WebkitPrintColorAdjust: 'exact' } : {}),
  }

  if (!value || failed) {
    return <span style={{ color: '#000000', fontSize: 12 }}>{value || '-'}</span>
  }

  return (
    <span
      style={frameStyle}
      // qrcode toString 输出的为本组件生成的静态 SVG 字符串（非用户 HTML），注入渲染为矢量码
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
