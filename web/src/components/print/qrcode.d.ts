/**
 * qrcode@1.5.4 未随包分发类型声明，@types/qrcode 未安装且本组不得改动 package.json；
 * 按前端实际使用面（toString 生成 SVG 矢量码）最小化声明（frontend.md §26.1 严格类型）。
 */
declare module 'qrcode' {
  export interface QrCodeToStringOptions {
    type?: 'svg' | 'utf8' | 'terminal'
    /** 输出尺寸 px（SVG 宽高，矢量渲染不受影响） */
    width?: number
    /** 静区（模块数） */
    margin?: number
    errorCorrectionLevel?: 'L' | 'M' | 'Q' | 'H'
    color?: { dark?: string; light?: string }
  }

  const QRCode: {
    toString(text: string, options?: QrCodeToStringOptions): Promise<string>
  }

  export default QRCode
}
