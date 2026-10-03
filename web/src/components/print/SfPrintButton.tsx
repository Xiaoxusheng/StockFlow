import { Button } from 'antd'
import { PrinterOutlined } from '@ant-design/icons'
import { useReactToPrint } from 'react-to-print'
import type { RefObject } from 'react'

export interface SfPrintButtonProps {
  /** 打印目标容器（react-to-print 克隆到打印 iframe 的节点） */
  contentRef: RefObject<HTMLDivElement>
  /** @page 页面样式（按纸张规格注入 size/margin，printing.md §6 多纸张） */
  pageStyle?: string
  documentTitle?: string
  disabled?: boolean
  buttonText?: string
}

/**
 * react-to-print 统一封装（frontend.md §13 / §30.1：禁止 window.print 直打当前网页）。
 * 打印目标为独立渲染层的纸张节点，与系统页面样式隔离（printing.md §6）。
 */
export function SfPrintButton({ contentRef, pageStyle, documentTitle, disabled, buttonText = '打印' }: SfPrintButtonProps) {
  const handlePrint = useReactToPrint({ contentRef, pageStyle, documentTitle })

  return (
    <Button icon={<PrinterOutlined />} disabled={disabled} onClick={() => handlePrint()}>
      {buttonText}
    </Button>
  )
}
