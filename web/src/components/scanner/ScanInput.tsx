import { useState } from 'react'
import { Button, Input } from 'antd'
import { ScanOutlined } from '@ant-design/icons'
import { useScanBuffer, type ScanInputMode, type ScanMeta } from '@/hooks/useScanBuffer'
import './scan-input.css'

export type { ScanInputMode, ScanMeta } from '@/hooks/useScanBuffer'

export interface ScanInputProps {
  /** 扫码模式（frontend.md §22 三模式，默认 normal） */
  mode?: ScanInputMode
  /**
   * 智能下一步档位：'safe' = 页面声明 onScan 内只做查询/定位类低风险动作，可随连扫
   * 自动推进；'none'（默认）= 页面存在确认/扣减类动作，组件提示「需显式确认」，
   * 业务提交必须显式按键（requirements.md §2.10：禁止自动提交）。
   */
  autoAdvance?: 'none' | 'safe'
  /** 扫入提交回调（Enter 或提交按钮触发；meta 携带节奏判定） */
  onScan: (code: string, meta: ScanMeta) => void
  placeholder?: string
  /** 自动聚焦（承接 USB/蓝牙 HID 扫码枪，scanner.md §2.1 Pad 主要接入方式） */
  autoFocus?: boolean
  /** 手工输入兜底按钮（默认 true；关闭后仍可键入 + Enter 提交） */
  manualInput?: boolean
  maxLength?: number
  /** 扫码后清空输入框（默认 true，§3.3 能力清单） */
  clearAfterScan?: boolean
  disabled?: boolean
  /** 辅助说明（替代场景期望值提示等） */
  hint?: string
  submitText?: string
}

/** 三模式 UI 文案（模式名 + autoAdvance 档位行为说明） */
const MODE_NOTE: Record<ScanInputMode, (autoAdvance: 'none' | 'safe') => string> = {
  normal: () => '常规模式：扫入 / 手输后回车即解析。',
  fast: (aa) =>
    aa === 'safe'
      ? '快速模式：连扫节奏自动推进下一步（页面已声明低风险动作）。'
      : '快速模式：连扫节奏识别中；本页含确认类动作，推进需显式按键。',
  continuous: (aa) =>
    aa === 'safe'
      ? '连续模式：同一编码窗口内重复扫入自动累计计数。'
      : '连续模式：同一编码重复扫入累计计数；提交仍需显式确认。',
}

/**
 * 统一扫码输入原语（frontend.md §22 作业效率提升层一期 / scanner.md §3.3 能力清单）：
 * 受控输入框承接 USB/蓝牙 HID 扫码枪（模拟键盘 + Enter）与手工键入，节奏判定经
 * useScanBuffer（枪扫/连扫/同码累计），onScan 携带 ScanMeta 交页面决定自动推进档位。
 * 一期口径：键盘缓冲解析限定在组件自身输入焦点内（[data-sf-scan-input] 标记并入
 * §32 输入态抑制），不做页面级 keydown 监听；完整 ScannerManager→Event 总线归 F15/F16。
 */
export function ScanInput({
  mode = 'normal',
  autoAdvance = 'none',
  onScan,
  placeholder,
  autoFocus = true,
  manualInput = true,
  maxLength,
  clearAfterScan = true,
  disabled = false,
  hint,
  submitText = '提交',
}: ScanInputProps) {
  const [code, setCode] = useState('')
  const [lastMeta, setLastMeta] = useState<ScanMeta | null>(null)
  const { registerKey, judge, reset } = useScanBuffer({ mode })

  const submit = () => {
    const value = code.trim()
    if (!value) return
    const meta = judge(value)
    setLastMeta(meta)
    onScan(value, meta)
    if (clearAfterScan) {
      setCode('')
      reset()
    }
  }

  return (
    <div className="sf-scan-input" data-sf-scan-mode={mode}>
      <div className="sf-scan-input__zone">
        <ScanOutlined className="sf-scan-input__icon" />
        <span>{MODE_NOTE[mode](autoAdvance).split('：')[0]}</span>
        {mode === 'continuous' && lastMeta && lastMeta.repeatCount > 1 && (
          <span className="sf-scan-input__repeat">×{lastMeta.repeatCount}</span>
        )}
      </div>
      {hint && <p className="sf-scan-input__hint">{hint}</p>}
      <div className="sf-scan-input__row">
        <Input
          size="large"
          data-sf-scan-input
          autoFocus={autoFocus}
          value={code}
          maxLength={maxLength}
          disabled={disabled}
          placeholder={placeholder ?? '扫入条码 / 单号（HID 扫码枪或手工输入）'}
          onChange={(e) => {
            setCode(e.target.value)
            registerKey()
          }}
          onPressEnter={submit}
          allowClear
        />
        {manualInput && (
          <Button
            size="large"
            type="primary"
            disabled={disabled || !code.trim()}
            onClick={submit}
          >
            {submitText}
          </Button>
        )}
      </div>
      <p className="sf-scan-input__note">{MODE_NOTE[mode](autoAdvance)}</p>
    </div>
  )
}
