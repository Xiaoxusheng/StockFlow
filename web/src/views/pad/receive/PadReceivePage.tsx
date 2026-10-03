import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, DatePicker, Input, Modal, message } from 'antd'
import {
  ArrowLeftOutlined,
  CameraOutlined,
  CheckOutlined,
  MinusOutlined,
  PauseOutlined,
  PlusOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import type { Dayjs } from 'dayjs'
import { useNavigate } from 'react-router'
import type { ReceiptItem, ReceiptQuery, ReceiptStatus } from '@/api/purchase'
import { purchaseApi } from '@/api/purchase'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { STATUS_META } from '@/types/status'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  PadScanStub,
  usePadOrientation,
} from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

/** 收货单状态 chip（ReceiptStatus 值域子集，文案取 types/status.ts 注册表，颜色经 SfStatusTag §24） */
const STATUS_OPTIONS: Array<{ label: string; value: ReceiptStatus }> = (
  ['receiving', 'pending_inspection', 'inspected', 'pending_putaway'] as const
).map((value) => ({ value, label: STATUS_META[value]?.label ?? value }))

/** 收货单任务卡：ReceiptItem 卡片（receiptNo/poNo/供应商/仓库/应收/已收/状态），
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9） */
function ReceiptTaskCard({
  receipt,
  selected = false,
  onClick,
}: {
  receipt: ReceiptItem
  selected?: boolean
  onClick?: () => void
}) {
  return (
    <div
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
      aria-pressed={onClick ? selected : undefined}
      className={`sf-pad-task-card${selected ? ' sf-pad-task-card--selected' : ''}`}
      onClick={onClick}
      onKeyDown={(e) => {
        if (!onClick) return
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick()
        }
      }}
    >
      <div className="sf-pad-task-card__head">
        <span className="sf-pad-task-card__no">{receipt.receiptNo}</span>
        <SfStatusTag status={receipt.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        {receipt.poNo && <span>PO {receipt.poNo}</span>}
        <span>{receipt.supplierName}</span>
        <span>{receipt.warehouseName}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(receipt.receivedQty)}</span>
        <span className="sf-pad-task-card__qty-unit">/ {formatNumber(receipt.totalQty)} 已收</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>应收 {formatNumber(receipt.totalQty)}</span>
        <span>{formatDateTime(receipt.createdAt)}</span>
      </div>
    </div>
  )
}

/** 禁用动作按钮（死按钮门禁口径，frontend.md §9.1）：antd 禁用按钮吞点击，
 * 外包 span 接管点按，把 disabledReason 以 Toast 送达 */
function DisabledAction({
  label,
  reason,
  icon,
  variant,
}: {
  label: string
  reason: string
  icon?: ReactNode
  variant?: 'primary'
}) {
  const [messageApi, contextHolder] = message.useMessage()
  return (
    <>
      {contextHolder}
      <span style={{ display: 'block' }} onClick={() => messageApi.warning(reason)}>
        <Button
          block
          size="large"
          type={variant === 'primary' ? 'primary' : 'default'}
          icon={icon}
          disabled
          style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
        >
          {label}
        </Button>
      </span>
    </>
  )
}

/**
 * Pad 收货页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §2.3 / §21.4 收货要点）：
 * - 左栏：purchaseApi.receipts.list（前端先行契约）→ ReceiptTaskCard 列表 + 状态 chip 筛选；
 * - 中栏：选中收货单 PadInfoCard（应收 / 已收 / 待收 = 应收 − 已收，前端计算，metric 大字）；
 * - 右栏：操作区 = 数量步进大按钮 + 批次 / 效期录入 + [确认收货]（端点未冻结，disabled 占位） +
 *   PadScanStub 扫码占位 + 异常拍照入口占位（§20.6）；
 * - 竖屏：顶部当前收货任务卡 → 商品信息 → 任务列表滚动区 → 底部 PadActionBar。
 * 收货确认 / 批次效期登记端点 M2 采购域冻结后接线，主按钮 disabled 并注明原因（无死按钮）。
 */
export default function PadReceivePage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const [statusFilter, setStatusFilter] = useState<ReceiptStatus | 'all'>('all')
  const [selected, setSelected] = useState<ReceiptItem | null>(null)
  const [inputQty, setInputQty] = useState(0)
  const [batchNo, setBatchNo] = useState('')
  const [expiry, setExpiry] = useState<Dayjs | null>(null)
  const [scanOpen, setScanOpen] = useState(false)

  const params = useMemo<ReceiptQuery>(
    () => ({ status: statusFilter === 'all' ? undefined : statusFilter }),
    [statusFilter],
  )

  const list = usePagedList<ReceiptItem, ReceiptQuery>({
    queryKey: ['pad', 'receipts'],
    fetch: (q) => purchaseApi.receipts.list(q),
    params,
    defaultPageSize: 30,
  })

  // 待收数量 = 应收 − 已收（前端计算）；钳制非负防后端数据异常
  const remaining = selected ? Math.max(0, selected.totalQty - selected.receivedQty) : 0

  const handleSelect = (receipt: ReceiptItem) => {
    setSelected(receipt)
    // 预填本次收货数量 = 待收数量（可步进减少），切换单据清空批次 / 效期录入
    setInputQty(Math.max(0, receipt.totalQty - receipt.receivedQty))
    setBatchNo('')
    setExpiry(null)
  }

  const handleFilter = (value: ReceiptStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  const handleScanSubmit = (code: string) => {
    // 扫码链路属 Scan 端 / F16（frontend.md §19.2）：本轮仅本地接收手输编码
    messageApi.info(`已接收手输编码：${code}（收货扫码校验 M2 采购域冻结后接线）`)
    setScanOpen(false)
  }

  const infoNode = selected ? (
    <>
      <PadInfoCard
        title={`收货单 · ${selected.receiptNo}`}
        items={[
          { label: '收货单号', value: selected.receiptNo },
          { label: '采购单号', value: selected.poNo ?? EMPTY_TEXT },
          { label: '供应商', value: selected.supplierName },
          { label: '仓库', value: selected.warehouseName },
          { label: '状态', value: <SfStatusTag status={selected.status} /> },
          { label: '创建时间', value: formatDateTime(selected.createdAt) },
          { label: '应收数量', value: formatNumber(selected.totalQty), emphasis: true },
          { label: '已收数量', value: formatNumber(selected.receivedQty), emphasis: true },
          { label: '待收数量', value: formatNumber(remaining), emphasis: true },
        ]}
      />
      <p className="sf-pad-muted-note">
        待收数量 = 应收 − 已收（前端计算）；累计收货（含合格 + 不合格待定）不得超过原始数量
        （business-flow.md §2.3），超量收货由后端强校验
      </p>
    </>
  ) : (
    <SfEmpty description="从收货单列表选择一张任务卡，此处展示应收 / 已收 / 待收数量" />
  )

  const actionNode = (
    <>
      <section className="sf-pad-card" aria-label="收货操作">
        <h3 className="sf-pad-card-title">收货操作</h3>
        {selected ? (
          <>
            <div
              role="group"
              aria-label="本次收货数量步进"
              style={{ display: 'flex', alignItems: 'center', gap: 'var(--sf-space-3)' }}
            >
              <Button
                size="large"
                icon={<MinusOutlined />}
                aria-label="本次收货数量减 1"
                disabled={inputQty <= 0}
                onClick={() => setInputQty((q) => Math.max(0, q - 1))}
                style={{
                  width: 'var(--sf-pad-action-height)',
                  height: 'var(--sf-pad-action-height)',
                }}
              />
              <div style={{ flex: 1, textAlign: 'center' }}>
                <span className="sf-pad-metric">{formatNumber(inputQty)}</span>
                <div className="sf-pad-info-label">本次收货数量 · 待收 {formatNumber(remaining)}</div>
              </div>
              <Button
                size="large"
                icon={<PlusOutlined />}
                aria-label="本次收货数量加 1"
                disabled={inputQty >= remaining}
                onClick={() => setInputQty((q) => Math.min(remaining, q + 1))}
                style={{
                  width: 'var(--sf-pad-action-height)',
                  height: 'var(--sf-pad-action-height)',
                }}
              />
            </div>
            <p className="sf-pad-muted-note">
              步进上限 = 待收数量：累计收货不得超过原始数量（business-flow.md §2.3）
            </p>
            <Input
              size="large"
              placeholder="批次号录入"
              value={batchNo}
              onChange={(e) => setBatchNo(e.target.value)}
              allowClear
              style={{ height: 'var(--sf-pad-touch-min)' }}
            />
            <DatePicker
              size="large"
              placeholder="效期（到期日）"
              value={expiry}
              onChange={(value) => setExpiry(value)}
              style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
            />
            <DisabledAction
              label="确认收货"
              variant="primary"
              reason="收货确认 / 批次效期登记端点后端未冻结：M2 采购域冻结后接线"
            />
          </>
        ) : (
          <SfEmpty description="选择收货单后在此步进本次收货数量并录入批次 / 效期" />
        )}
      </section>

      <section className="sf-pad-card" aria-label="扫码占位">
        <h3 className="sf-pad-card-title">扫码</h3>
        <PadScanStub
          placeholder="手工输入采购单 / 收货单 / SKU 条码兜底"
          onSubmit={(code) =>
            messageApi.info(`已接收手输编码：${code}（收货扫码校验 M2 采购域冻结后接线）`)
          }
        />
      </section>

      <section className="sf-pad-card" aria-label="收货异常拍照">
        <h3 className="sf-pad-card-title">收货异常</h3>
        <DisabledAction
          label="拍照登记收货异常"
          icon={<CameraOutlined />}
          reason="异常拍照上传属文件域与 Scan 端集成（business-flow.md §3.4 / frontend.md §20.6），接线前为占位；异常登记可走底部 [异常] 进入异常中心"
        />
      </section>
    </>
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="收货单状态筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={statusFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleFilter('all')}
          >
            全部
          </Button>
          {STATUS_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={statusFilter === option.value ? 'primary' : 'default'}
              onClick={() => handleFilter(option.value)}
            >
              {option.label}
            </Button>
          ))}
        </div>
      </div>

      {list.isPending ? (
        <SfLoading rows={6} />
      ) : list.error ? (
        <SfError
          error={list.error}
          description="收货单列表为前端先行契约（GET /api/purchases/receipts），M2 采购域冻结后可用"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有收货单，试试切换状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((receipt) => (
            <ReceiptTaskCard
              key={receipt.id}
              receipt={receipt}
              selected={selected?.id === receipt.id}
              onClick={() => handleSelect(receipt)}
            />
          ))}
        </div>
      )}

      <div className="sf-pad-tasks-pager">
        <span className="sf-pad-info-label">
          共 {list.total} 条 · 第 {list.pagination.current}/{totalPage} 页
        </span>
        <div className="sf-pad-pager-btns">
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={list.pagination.current <= 1}
            onClick={() => list.onPageChange(list.pagination.current - 1, list.pagination.pageSize)}
          >
            上一页
          </Button>
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={list.pagination.current >= totalPage}
            onClick={() => list.onPageChange(list.pagination.current + 1, list.pagination.pageSize)}
          >
            下一页
          </Button>
        </div>
      </div>
    </div>
  )

  return (
    <>
      {contextHolder}
      {orientation === 'landscape' ? (
        <PadPageShell tasksSlot={listNode} contentSlot={infoNode} actionSlot={actionNode} />
      ) : (
        // 竖屏（§20.4）：顶部当前收货任务卡 → 商品信息 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {selected && <ReceiptTaskCard receipt={selected} selected />}
              {selected && <div className="sf-pad-tasks-detail">{infoNode}</div>}
              {listNode}
            </>
          }
          contentSlot={null}
        />
      )}
      <PadActionBar
        actions={[
          { key: 'back', label: '返回', icon: <ArrowLeftOutlined />, onClick: () => navigate(-1) },
          { key: 'scan', label: '扫码', icon: <ScanOutlined />, onClick: () => setScanOpen(true) },
          {
            key: 'exception',
            label: '异常',
            icon: <WarningOutlined />,
            variant: 'danger',
            onClick: () => navigate('/pad/exception'),
          },
          {
            key: 'pause',
            label: '暂停',
            icon: <PauseOutlined />,
            disabled: true,
            disabledReason: '暂停 / 恢复作业端点未接线（M2 任务域冻结后启用）',
          },
          {
            key: 'finish',
            label: '完成',
            icon: <CheckOutlined />,
            variant: 'primary',
            disabled: true,
            disabledReason: '确认收货端点未冻结：M2 采购域冻结后接线',
          },
        ]}
      />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <PadScanStub onSubmit={handleScanSubmit} placeholder="手工输入采购单 / 收货单 / SKU 条码兜底" />
      </Modal>
    </>
  )
}
