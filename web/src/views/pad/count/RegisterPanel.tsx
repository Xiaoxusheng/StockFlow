import { Button, Input, InputNumber } from 'antd'
import { CameraOutlined } from '@ant-design/icons'
import type { CountItem, CountTaskItem } from '@/api/count'
import { SfEmpty } from '@/components/common/SfEmpty'
import { PadScanStub } from '@/layouts/pad'
import { EMPTY_TEXT, formatNumber } from '@/utils/format'

/** 实盘录入表单值（页面持有 state 以支持 PadActionBar [暂停] 草稿暂存） */
export interface RegisterFormValue {
  countedQty: number | null
  reason: string
  remark: string
}

export const EMPTY_REGISTER_FORM: RegisterFormValue = { countedQty: null, reason: '', remark: '' }

export interface RegisterPanelProps {
  task: CountTaskItem | null
  item: CountItem | null
  value: RegisterFormValue
  onChange: (value: RegisterFormValue) => void
  submitting: boolean
  onSubmit: () => void
  /** 拍照占位 / 扫码手输等本地反馈（页面级 message 实例） */
  notify: (message: string) => void
  /** 扫码 / 手输兜底编码（仅本地匹配明细，不接真实扫码链路） */
  onScanSubmit: (code: string) => void
}

/**
 * 盘点操作区（frontend.md §10.5 盘点扫码页 / §20.3 右栏）：
 * 实盘数量大录入位 + 差异（实盘-系统，前端计算展示，api/count.ts 契约不重复下发 diffQty）
 * + 差异原因/备注（差异必走原因/备注流程，devices.md §10.3）+ [登记实盘] 主按钮
 * + 盘点差异拍照入口占位（frontend.md §20.6，同收货/质检口径）+ PadScanStub。
 * 禁止在前端算库存：差异仅为行内展示，库存调整由后端走调整单 + 审批（business-flow.md §10.2）。
 */
export function RegisterPanel({
  task,
  item,
  value,
  onChange,
  submitting,
  onSubmit,
  notify,
  onScanSubmit,
}: RegisterPanelProps) {
  const disabledReason = !task
    ? '请先在左侧选择一张盘点任务'
    : !item
      ? '请先在明细中选择要登记实盘的商品行'
      : task.status !== 'COUNTING'
        ? '当前盘点单不在「盘点中」状态，不能登记实盘（business-flow.md §10.2：冻结后实盘）'
        : null
  const hasDiff = item != null && value.countedQty != null && value.countedQty !== item.systemQty

  return (
    <section className="sf-pad-card" aria-label="实盘登记操作区">
      <h3 className="sf-pad-card-title">实盘登记</h3>
      {!task || !item ? (
        <SfEmpty description="选择盘点任务并点选明细行后，在此录入实盘数量" />
      ) : (
        <div style={{ display: 'grid', gap: 'var(--sf-space-3)' }}>
          <p className="sf-pad-muted-note" style={{ margin: 0 }}>
            当前明细：{item.skuCode} · 库位 {item.binCode || EMPTY_TEXT} · 批次{' '}
            {item.batchNo || EMPTY_TEXT} · 系统数量 {formatNumber(item.systemQty)}
          </p>

          <label className="sf-pad-info-item">
            <span className="sf-pad-info-label">实盘数量（{item.productName}）</span>
            <InputNumber
              size="large"
              min={0}
              value={value.countedQty}
              style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
              onChange={(next) => onChange({ ...value, countedQty: typeof next === 'number' ? next : null })}
            />
          </label>

          <div className="sf-pad-info-item">
            <span className="sf-pad-info-label">差异（实盘 - 系统）</span>
            {value.countedQty == null ? (
              <span className="sf-pad-info-value">录入实盘数量后自动计算</span>
            ) : (
              <span
                className="sf-pad-metric"
                style={{
                  color:
                    value.countedQty === item.systemQty
                      ? undefined
                      : value.countedQty > item.systemQty
                        ? 'var(--sf-success)'
                        : 'var(--sf-danger)',
                }}
              >
                {value.countedQty === item.systemQty
                  ? '无差异'
                  : `${value.countedQty > item.systemQty ? '盘盈 +' : '盘亏 '}${formatNumber(
                      value.countedQty - item.systemQty,
                    )}`}
              </span>
            )}
          </div>

          <label className="sf-pad-info-item">
            <span className="sf-pad-info-label">差异原因{hasDiff ? '（差异必填）' : ''}</span>
            <Input
              size="large"
              value={value.reason}
              placeholder="如：损耗 / 破损 / 录入差异…"
              style={{ height: 'var(--sf-pad-touch-min)' }}
              onChange={(e) => onChange({ ...value, reason: e.target.value })}
            />
          </label>

          <label className="sf-pad-info-item">
            <span className="sf-pad-info-label">备注{hasDiff ? '（差异必填）' : ''}</span>
            <Input
              size="large"
              value={value.remark}
              placeholder="补充说明（展示于盘点差异复核）"
              style={{ height: 'var(--sf-pad-touch-min)' }}
              onChange={(e) => onChange({ ...value, remark: e.target.value })}
            />
          </label>

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
            onClick={() =>
              notify('盘点差异拍照属 Pad 拍照链路（frontend.md §20.6 盘点差异场景），本轮为占位入口，待设备/文件域交付后接线')
            }
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                notify('盘点差异拍照属 Pad 拍照链路（frontend.md §20.6 盘点差异场景），本轮为占位入口，待设备/文件域交付后接线')
              }
            }}
          >
            <Button block size="large" icon={<CameraOutlined />} disabled style={{ height: 'var(--sf-pad-touch-min)' }}>
              盘点差异拍照（占位）
            </Button>
          </span>

          <PadScanStub
            onSubmit={onScanSubmit}
            placeholder="手输 SKU / 库位编码，定位明细行"
          />
        </div>
      )}
    </section>
  )
}
