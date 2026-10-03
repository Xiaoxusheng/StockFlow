import { useEffect, useState } from 'react'
import QRCode from 'qrcode'

export interface QrCodeViewProps {
  value: string
  /** 输出尺寸 px（SVG 矢量渲染，打印放大不失真，printing.md §6） */
  size?: number
  /** 纠错级别（标签磨损场景建议 M/H） */
  level?: 'L' | 'M' | 'Q' | 'H'
}

/**
 * 二维码渲染（qrcode → SVG 矢量字符串，printing.md §6「矢量或高分辨率渲染」）。
 * 库位二维码 / 箱码 / 托盘码 / 单据二维码共用（printing.md §4.2）。
 */
export function QrCodeView({ value, size = 96, level = 'M' }: QrCodeViewProps) {
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
      margin: 0,
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
  }, [value, size, level])

  if (!value || failed) {
    return <span style={{ color: '#000000', fontSize: 12 }}>{value || '-'}</span>
  }

  return (
    <span
      style={{ display: 'inline-block', width: size, height: size, lineHeight: 0 }}
      // qrcode toString 输出的为本组件生成的静态 SVG 字符串（非用户 HTML），注入渲染为矢量码
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
