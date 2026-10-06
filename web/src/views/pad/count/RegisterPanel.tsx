import { Button, InputNumber } from 'antd'
import { CameraOutlined } from '@ant-design/icons'
import type { CountItem, CountOrder } from '@/api/count'
import { SfEmpty } from '@/components/common/SfEmpty'
import { ScanInput } from '@/components/scanner/ScanInput'
import { EMPTY_TEXT, formatNumber } from '@/utils/format'

/** 实盘录入表单值：仅数量（差异原因/备注已移除——后端 CountRegistration 无此字段
 * （internal/stockops/store.go:204-208），差异说明随 PC 端差异审核意见提交） */
export interface RegisterFormValue {
  countedQty: number | null
}

export const EMPTY_REGISTER_FORM: RegisterFormValue = { countedQty: null }

export interface RegisterPanelLabels {
  skuCode: string
  productName: string
  binCode: string
}

export interface RegisterPanelProps {
  /** 当前盘点单（登记门禁：仅 COUNTING 态可实盘） */
  order: CountOrder | null
  /** 当前选中明细行 */
  item: CountItem | null
  /** 行编码标签（options 映射结果，页面装配） */
  labels: RegisterPanelLabels | null
  value: RegisterFormValue
  onChange: (value: RegisterFormValue) => void
  submitting: boolean
  onSubmit: () => void
  /** 拍照占位等本地反馈（页面级 message 实例） */
  notify: (message: string) => void
  /** 扫码 / 手输兜底编码（仅本地匹配明细行，不接真实扫码链路） */
  onScanSubmit: (code: string) => void
}

/**
 * 盘点操作区（frontend.md §10.5 盘点扫码页 / §20.3 右栏）：
 * 实盘数量大录入位 + 差异（实盘-系统，前端计算仅为录入预览，同 PC diffCell 口径）
 * + [登记实盘]（批量幂等 PUT /api/counts/{id}/items，items 数组按
 * (InventoryRowID, SerialNo) 提交，登记 0 必须显式提交，inventory-rules.md §9）
 * + 盘点差异拍照入口占位（frontend.md §20.6，同收货/质检口径）+ ScanInput 扫码定位明细行
 * （扫码作业优化 2026-10-06：三模式可切换，仅定位不登记——盘盈亏属高风险，登记必须显式提交）。
 * 禁止在前端算库存：差异仅为行内展示，调整由后端差异审核后走库存调整单（business-flow.md §10.2）。
 */
export function RegisterPanel({
  order,
  item,
  labels,
  value,
  onChange,
  submitting,
  onSubmit,
  notify,
  onScanSubmit,
}: RegisterPanelProps) {
  const disabledReason = !order
    ? '请先选择一张盘点任务'
    : !item
      ? '请先在明细中选择要登记实盘的商品行'
      : order.status !== 'COUNTING'
        ? '仅「盘点中」状态可登记实盘（business-flow.md §10.2：冻结快照后实盘）'
        : null
  const isSerialRow = item != null && item.serial_no !== ''
  const counted = value.countedQty

  const photoPlaceholder = '盘点差异拍照属 Pad 拍照链路（frontend.md §20.6 盘点差异场景），本轮为占位入口，待设备/文件域交付后接线'

  return (
    <section className="sf-pad-card" aria-label="实盘登记操作区">
      <h3 className="sf-pad-card-title">实盘登记</h3>
      {!order || !item || !labels ? (
        <SfEmpty description="选择盘点任务并点选明细行后，在此录入实盘数量" />
      ) : (
        <div style={{ display: 'grid', gap: 'var(--sf-space-3)' }}>
          <p className="sf-pad-muted-note" style={{ margin: 0 }}>
            当前明细：{labels.skuCode} · 库位 {labels.binCode} · 系统数量{' '}
            {formatNumber(item.qty_system)}
            {isSerialRow ? ` · 序列号 ${item.serial_no}` : ''}
          </p>

          <label className="sf-pad-info-item">
            <span className="sf-pad-info-label">
              实盘数量（{labels.productName !== EMPTY_TEXT ? labels.productName : labels.skuCode}）
            </span>
            <InputNumber
              size="large"
              min={0}
              precision={0}
              value={value.countedQty}
              style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
              onChange={(next) => onChange({ countedQty: typeof next === 'number' ? next : null })}
            />
          </label>
          {isSerialRow && (
            <p className="sf-pad-muted-note" style={{ margin: 0 }}>
              序列号 SKU 逐件一行：数量仅允许 0（缺失）/ 1（在库），登记 0 必须显式提交（inventory-rules.md §8.2/§9）
            </p>
          )}

          <div className="sf-pad-info-item">
            <span className="sf-pad-info-label">差异（实盘 - 系统）</span>
            {counted == null ? (
              <span className="sf-pad-info-value">录入实盘数量后自动预览</span>
            ) : counted === item.qty_system ? (
              <span className="sf-pad-metric">无差异</span>
            ) : (
              <span className="sf-pad-metric" style={{ color: 'var(--sf-warning)' }}>
                {counted > item.qty_system ? '盘盈 +' : '盘亏 '}
                {formatNumber(counted - item.qty_system)}
              </span>
            )}
          </div>

          <p className="sf-pad-muted-note" style={{ margin: 0 }}>
            登记仅提交数量（批量幂等 PUT，同「库存行 + 序列号」重复提交为覆盖）；差异说明不在登记时提交，
            随 PC 端差异审核意见提交（后端 CountRegistration 无差异原因 / 备注字段）
          </p>

          <Button
            block
            size="large"
            type="primary"
            loading={submitting}
            disabled={!!disabledReason}
            style={{ height: 'var(--sf-pad-touch-min)' }}
            onClick={onSubmit}
          >
            登记实盘
          </Button>
          {disabledReason && <p className="sf-pad-muted-note" style={{ margin: 0 }}>{disabledReason}</p>}

          <span
            role="button"
            tabIndex={0}
            style={{ display: 'block', cursor: 'not-allowed' }}
            onClick={() => notify(photoPlaceholder)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                notify(photoPlaceholder)
              }
            }}
          >
            <Button block size="large" icon={<CameraOutlined />} disabled style={{ height: 'var(--sf-pad-touch-min)' }}>
              盘点差异拍照（占位）
            </Button>
          </span>

          <ScanInput
            autoFocus={false}
            placeholder="扫入 SKU / 库位编码，定位明细行"
            hint="扫码仅定位明细行；登记实盘（盘盈亏落账）必须显式按 [登记实盘] 提交"
            onScan={onScanSubmit}
          />
        </div>
      )}
    </section>
  )
}
