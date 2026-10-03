/**
 * qrcode@1.5.4 局部类型声明。
 * 依据：package.json dependencies 已含 qrcode ^1.5.4（本组不得改 package.json），
 * 但未安装 @types/qrcode 且该包无自带类型，故在本组独占目录内声明本组用到的方法。
 * 按 qrcode README：不传回调时返回 Promise。
 */
declare module 'qrcode' {
  export interface QRCodeRenderOptions {
    /** 输出图片宽度（px） */
    width?: number
    /** 静区边距（模块数） */
    margin?: number
    errorCorrectionLevel?: 'L' | 'M' | 'Q' | 'H'
    color?: { dark?: string; light?: string }
  }

  /** 生成 PNG data URL（浏览器端渲染 <img> 用） */
  export function toDataURL(text: string, options?: QRCodeRenderOptions): Promise<string>
  /** 生成字符串形态（utf8/svg，调试用） */
  export function toString(text: string, options?: QRCodeRenderOptions): Promise<string>
  /** 绘制到 canvas 元素 */
  export function toCanvas(
    canvasElement: HTMLCanvasElement,
    text: string,
    options?: QRCodeRenderOptions,
  ): Promise<void>
}
