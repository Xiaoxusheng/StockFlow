import { useState } from 'react'
import { Button, Input } from 'antd'
import { ScanOutlined } from '@ant-design/icons'

export interface PadScanStubProps {
  /** 手输编码提交回调（本轮仅本地处理，不接任何真实扫码链路） */
  onSubmit?: (code: string) => void
  /** 手输输入框占位文案 */
  placeholder?: string
}

/**
 * 全 Pad 统一扫码入口占位原语（frontend.md §19.2/§22 硬性边界）：
 * 实体 / 摄像头扫码能力属 StockFlow Scan 端与 F16，Web/Pad 不假装拥有原生扫码能力。
 * 提供固定说明 + 手工输入兜底输入框；禁止在本轮接任何真实扫码实现。
 */
export function PadScanStub({ onSubmit, placeholder }: PadScanStubProps) {
  const [code, setCode] = useState('')

  const submit = () => {
    const value = code.trim()
    if (!value) return
    onSubmit?.(value)
    setCode('')
  }

  return (
    <div className="sf-pad-scan-stub">
      <div className="sf-pad-scan-stub__zone" aria-hidden>
        <ScanOutlined className="sf-pad-scan-stub__icon" />
        <span>扫码入口占位</span>
      </div>
      <p className="sf-pad-scan-stub__note">
        实体 / 摄像头扫码属 StockFlow Scan 端与 F16（frontend.md §19.2 边界），Pad 端不接入真实扫码。
      </p>
      <div className="sf-pad-scan-stub__input">
        <Input
          size="large"
          value={code}
          placeholder={placeholder ?? '手工输入条码 / 单号兜底'}
          onChange={(e) => setCode(e.target.value)}
          onPressEnter={submit}
          allowClear
        />
        <Button size="large" type="primary" disabled={!code.trim()} onClick={submit}>
          提交
        </Button>
      </div>
    </div>
  )
}
