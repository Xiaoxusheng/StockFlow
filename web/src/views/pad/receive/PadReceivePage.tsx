import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, DatePicker, Input, Modal, Select, Switch, message } from 'antd'
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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Dayjs } from 'dayjs'
import { useNavigate } from 'react-router'
import { resolveErrorMessage } from '@/api/client'
import {
  buildIdItemMap,
  buildWarehouseMaps,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import type { SkuItem } from '@/api/masterdata'
import type { BinItem } from '@/api/warehouse'
import type {
  InboundOrder,
  InboundOrderItem,
  InboundOrderQuery,
  InboundOrderStatus,
} from '@/api/inbound'
import { inboundApi } from '@/api/inbound'
import type { ReceiptLineInput } from '@/api/purchase'
import { purchaseApi } from '@/api/purchase'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  PadScanStub,
  usePadOrientation,
} from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

/**
 * 入库单状态 → SfStatusTag 文案/语义（frontend.md §24：颜色统一经 SfStatusTag）。
 * AWAITING_QC / AWAITING_PUTAWAY 为后端大写枚举（models.go:27-33），未注册于
 * types/status.ts（不在本单元文件清单内），与 api/quality.ts QUALITY_RESULT_TAG_META
 * 同口径以 label + semantic 显式指定，文案对齐注册表既有词条。
 */
const INBOUND_STATUS_TAG_META: Record<InboundOrderStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  RECEIVING: { label: '收货中', semantic: 'processing' },
  AWAITING_QC: { label: '待质检', semantic: 'pending' },
  AWAITING_PUTAWAY: { label: '待上架', semantic: 'pending' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
  CLOSED: { label: '已关闭', semantic: 'neutral' },
}

function InboundStatusTag({ status }: { status: InboundOrderStatus }) {
  const meta = INBOUND_STATUS_TAG_META[status]
  return <SfStatusTag label={meta?.label ?? status} semantic={meta?.semantic ?? 'neutral'} />
}

/** 任务源状态 chip（默认筛「收货中」= 待收任务；DRAFT 亦允许收货，service_receipt.go:104） */
const STATUS_OPTIONS: Array<{ label: string; value: InboundOrderStatus }> = (
  ['RECEIVING', 'DRAFT', 'AWAITING_QC', 'AWAITING_PUTAWAY', 'COMPLETED'] as const
).map((value) => ({ value, label: INBOUND_STATUS_TAG_META[value].label }))

/** 异常收货子型（§3.4：service_inbound.go:62-63 注释值域；拍照/附件随异常中心承载） */
const EXCEPTION_TYPE_OPTIONS = ['少货', '多货', '错货', '破损', '包装异常', '批次异常', '效期异常'].map(
  (value) => ({ label: value, value }),
)

/** 明细行收货录入态（一行一态；qty_good/qty_rejected 步进 + 批次/效期/序列号/库位/异常） */
interface ReceiveLineState {
  qtyGood: number
  qtyRejected: number
  batchNo: string
  expiry: Dayjs | null
  productionDate: Dayjs | null
  /** 序列号逐件录入：每行一个序列号（service_receipt.go:190-215 件数=合格数量、请求内不重复） */
  serialsText: string
  /** 免检开关：开 = require_inspect:false（合格品直达 available），关 = 缺省进待检 */
  noInspect: boolean
  /** 手动指定目标库位（§5.2 可选；缺省走后端推荐/报错语义） */
  binId?: string
  exceptionType?: string
  exceptionNote: string
}

function initLineState(item: InboundOrderItem): ReceiveLineState {
  return {
    qtyGood: Math.max(0, item.qty - item.qty_received),
    qtyRejected: 0,
    batchNo: '',
    expiry: null,
    productionDate: null,
    serialsText: '',
    noInspect: false,
    binId: undefined,
    exceptionType: undefined,
    exceptionNote: '',
  }
}

/** 序列号文本 → 逐件数组（每行一个，去空行） */
function parseSerials(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

/** 新幂等键（service_inbound.go:69-74：同一次收货意图重试复用同键，成功后作废） */
function newIdempotencyKey(): string {
  return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `pad-receive-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/** 数量步进器（≥ 触摸最小尺寸大按钮，frontend.md §20.9） */
function QtyStepper({
  label,
  value,
  max,
  onChange,
}: {
  label: string
  value: number
  max: number
  onChange: (value: number) => void
}) {
  return (
    <div
      role="group"
      aria-label={label}
      style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 'var(--sf-space-2)' }}
    >
      <Button
        size="large"
        icon={<MinusOutlined />}
        aria-label={`${label}减 1`}
        disabled={value <= 0}
        onClick={() => onChange(Math.max(0, value - 1))}
        style={{
          width: 'var(--sf-pad-touch-min)',
          height: 'var(--sf-pad-touch-min)',
          paddingInline: 0,
        }}
      />
      <div style={{ flex: 1, textAlign: 'center' }}>
        <span className="sf-pad-metric">{formatNumber(value)}</span>
        <div className="sf-pad-info-label">{label}</div>
      </div>
      <Button
        size="large"
        icon={<PlusOutlined />}
        aria-label={`${label}加 1`}
        disabled={value >= max}
        onClick={() => onChange(Math.min(max, value + 1))}
        style={{
          width: 'var(--sf-pad-touch-min)',
          height: 'var(--sf-pad-touch-min)',
          paddingInline: 0,
        }}
      />
    </div>
  )
}

/** 入库单任务卡：InboundOrder 卡片（入库单号/来源单号/仓库/状态/创建时间），
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9）；
 * 计划/已收数量在明细层（InboundOrderItem），列表卡不含数量大字 */
function InboundTaskCard({
  order,
  warehouseName,
  selected = false,
  onClick,
}: {
  order: InboundOrder
  /** 仓库名（基础资料 options 本地映射，失败降级 #id） */
  warehouseName: string
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
        <span className="sf-pad-task-card__no">{order.inbound_no}</span>
        <InboundStatusTag status={order.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        {order.source_no && <span>来源 {order.source_no}</span>}
        <span>{warehouseName}</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>{order.received_at ? `收货于 ${formatDateTime(order.received_at)}` : EMPTY_TEXT}</span>
        <span>{formatDateTime(order.created_at)}</span>
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
 * Pad 收货页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §2.3 / §3 / §21.4 收货要点）：
 * - 左栏：任务源 = GET /api/inbounds（inboundApi.list，默认筛 RECEIVING 待收状态；
 *   审计已核实问题 #3：原 GET /api/purchases/receipts 为漂移端点）→ InboundTaskCard 列表 + 状态 chip；
 * - 中栏：选中入库单 GET /api/inbounds/{id}（inboundApi.detail）→ PadInfoCard 渲染
 *   入库单号 / 来源单号 / 明细行应收·已收·待收（InboundOrderItem.qty / qty_received）；
 * - 右栏：逐明细行收货录入（合格/拒收数量步进 + 批次 / 效期 / 生产日期 + 序列号逐件录入 +
 *   免检开关 + 目标库位可选 + 异常收货类型/说明），[确认收货] 接线
 *   POST /api/receipts（purchaseApi.receipts.confirm，幂等键重试复用，service_receipt.go）；
 *   [拍照登记收货异常] 保持 disabled（依赖 /api/files 文件上传与 Scan 端拍照集成）；
 * - 竖屏：顶部当前任务卡 → 信息卡 → 收货操作 → 任务列表滚动区 → 底部 PadActionBar。
 * 批次/效期/序列号前端仅做同口径预检，最终以后端强校验为准（service_receipt.go:141-247）。
 */
export default function PadReceivePage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState<InboundOrderStatus | 'all'>('RECEIVING')
  const [selected, setSelected] = useState<InboundOrder | null>(null)
  const [scanOpen, setScanOpen] = useState(false)
  const [remark, setRemark] = useState('')
  /** 明细行录入态，键 = 行 id 字符串（String(item.id)） */
  const [lineStates, setLineStates] = useState<Record<string, ReceiveLineState>>({})
  /** 已初始化录入态的入库单 id（防窗口聚焦重取覆盖录入中内容） */
  const [loadedForId, setLoadedForId] = useState<string | null>(null)
  /** 幂等键（同单重试复用，成功后作废——plan §7） */
  const idemKeyRef = useRef<{ inboundId: string; key: string } | null>(null)

  const params = useMemo<InboundOrderQuery>(
    () => ({ status: statusFilter === 'all' ? undefined : statusFilter }),
    [statusFilter],
  )

  const list = usePagedList<InboundOrder, InboundOrderQuery>({
    queryKey: ['pad', 'receive', 'inbounds'],
    fetch: (q) => inboundApi.list(q),
    params,
    defaultPageSize: 30,
  })

  // 选中入库单明细（GET /api/inbounds/{id} → {order, items}，service_inbound.go:341-356）
  const detailQuery = useQuery({
    queryKey: ['pad', 'receive', 'inbound', String(selected?.id ?? '')],
    queryFn: () => {
      if (!selected) throw new Error('未选择入库单')
      return inboundApi.detail(selected.id)
    },
    enabled: selected != null,
  })

  // 基础资料 options（PadInventoryPage 同范本）：SKU 管控开关 + 仓库名 + 目标库位下拉
  const skuOptions = useQuery({ queryKey: ['pad', 'options', 'sku'], queryFn: fetchSkuOptions })
  const warehouseOptions = useQuery({
    queryKey: ['pad', 'options', 'warehouse'],
    queryFn: fetchWarehouseOptions,
  })
  const binOptions = useQuery({ queryKey: ['pad', 'options', 'bin'], queryFn: fetchBinOptions })

  const skuItemMap = useMemo(
    () => buildIdItemMap(skuOptions.data ?? [], (s) => s.id),
    [skuOptions.data],
  )
  const warehouseNameMap = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const binItems = useMemo(() => binOptions.data ?? [], [binOptions.data])
  // 目标库位下拉限定当前入库单仓库（BinItem.warehouse_id 锚点），避免跨仓手选
  const binSelectOptions = useMemo<Array<{ label: string; value: string }>>(
    () =>
      selected
        ? binItems
            .filter((bin: BinItem) => String(bin.warehouse_id) === String(selected.warehouse_id))
            .map((bin) => ({ label: bin.code, value: String(bin.id) }))
        : [],
    [binItems, selected],
  )

  const detailItems: InboundOrderItem[] = detailQuery.data?.items ?? []

  // 明细到达后初始化逐行录入态（仅切换单据时一次；成功后置空重建）
  useEffect(() => {
    if (!selected || !detailQuery.data) return
    const inboundId = String(selected.id)
    if (loadedForId === inboundId) return
    const next: Record<string, ReceiveLineState> = {}
    for (const item of detailQuery.data.items) next[String(item.id)] = initLineState(item)
    setLineStates(next)
    setLoadedForId(inboundId)
  }, [detailQuery.data, selected, loadedForId])

  const patchLine = (lineId: string, patch: Partial<ReceiveLineState>) => {
    setLineStates((prev) =>
      prev[lineId] ? { ...prev, [lineId]: { ...prev[lineId], ...patch } } : prev,
    )
  }

  const skuOf = (skuId: number): SkuItem | undefined => skuItemMap.get(String(skuId))
  const skuLabel = (skuId: number): string => {
    const sku = skuOf(skuId)
    return sku ? sku.code : `SKU #${String(skuId)}`
  }
  const skuNameOf = (skuId: number): string => {
    const sku = skuOf(skuId)
    return sku?.product_name ?? EMPTY_TEXT
  }
  const warehouseNameOf = (warehouseId: number): string =>
    warehouseNameMap.get(String(warehouseId)) ?? `#${String(warehouseId)}`
  const lineRemaining = (item: InboundOrderItem): number =>
    Math.max(0, item.qty - item.qty_received)

  /** 是否已录入至少一行本次收货数量（主按钮可用条件） */
  const hasInput = useMemo(
    () =>
      detailItems.some((item) => {
        const state = lineStates[String(item.id)]
        return !!state && (state.qtyGood > 0 || state.qtyRejected > 0)
      }),
    [detailItems, lineStates],
  )

  const confirmMutation = useMutation({
    mutationFn: purchaseApi.receipts.confirm,
    onSuccess: (result) => {
      const replayNote = result.replay ? '（幂等重放，未重复累计）' : ''
      const putawayNote = result.putaway_task_nos.length
        ? `，已生成 ${result.putaway_task_nos.length} 个上架任务`
        : ''
      messageApi.success(`收货确认成功：${result.receipt_no}${replayNote}${putawayNote}`)
      idemKeyRef.current = null
      setRemark('')
      // 保留选中单据：明细重取后按剩余待收重建录入态
      setLineStates({})
      setLoadedForId(null)
      void queryClient.invalidateQueries({ queryKey: ['pad', 'receive'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  /**
   * 确认收货：逐行收集 + 同口径预检（数量正性 / 累计余量 / 批次效期必填 /
   * 序列号件数=合格数量且请求内不重复），全部通过后 POST /api/receipts。
   * 序列号管控开关、批次/效期必填口径与 service_receipt.go:141-247 一致；
   * SKU 管控开关未加载时跳过前端预检，由后端校验兜底（不造假放行）。
   */
  const handleConfirm = () => {
    if (!selected || !detailQuery.data || confirmMutation.isPending) return
    const problems: string[] = []
    const serialLineByNo = new Map<string, number>()
    const lines: ReceiptLineInput[] = []

    for (const item of detailItems) {
      const state = lineStates[String(item.id)]
      if (!state) continue
      if (state.qtyGood <= 0 && state.qtyRejected <= 0) continue

      const label = `行${item.line_no} · ${skuLabel(item.sku_id)}`
      const remaining = lineRemaining(item)
      const sku = skuOf(item.sku_id)
      const batchManaged = sku?.is_batch_managed === true
      const expiryManaged = sku?.is_batch_managed === true && sku?.is_expiry_managed === true
      const serialManaged = sku?.is_serial_managed === true
      const serials = parseSerials(state.serialsText)

      if (state.qtyGood + state.qtyRejected > remaining) {
        problems.push(`${label}：本次收货 ${state.qtyGood + state.qtyRejected} 超出待收 ${remaining}`)
      }
      if (batchManaged && !state.batchNo.trim()) {
        problems.push(`${label}：批次管理 SKU 必须录入批次号`)
      }
      if (expiryManaged && !state.expiry) {
        problems.push(`${label}：效期管理 SKU 必须录入效期`)
      }
      if (serialManaged) {
        if (!Number.isInteger(state.qtyGood) || state.qtyGood <= 0) {
          problems.push(`${label}：序列号 SKU 按件收货，合格数量须为正整数`)
        } else if (serials.length !== state.qtyGood) {
          problems.push(`${label}：序列号 ${serials.length} 件与合格数量 ${state.qtyGood} 件不一致`)
        }
        const seen = new Set<string>()
        for (const sn of serials) {
          if (seen.has(sn)) {
            problems.push(`${label}：序列号 ${sn} 行内重复`)
            break
          }
          seen.add(sn)
          const prevLine = serialLineByNo.get(sn)
          if (prevLine !== undefined && prevLine !== item.line_no) {
            problems.push(`${label}：序列号 ${sn} 与行 ${prevLine} 重复`)
            break
          }
          serialLineByNo.set(sn, item.line_no)
        }
      }

      const line: ReceiptLineInput = {
        sku_id: Number(item.sku_id),
        qty_good: state.qtyGood,
        qty_rejected: state.qtyRejected,
      }
      if (state.noInspect) line.require_inspect = false
      if (state.batchNo.trim()) line.batch_no = state.batchNo.trim()
      if (state.expiry) line.expiry_date = state.expiry.format('YYYY-MM-DD')
      if (state.productionDate) line.production_date = state.productionDate.format('YYYY-MM-DD')
      if (serialManaged && serials.length > 0) line.serials = serials
      if (state.exceptionType) {
        line.exception_type = state.exceptionType
        if (state.exceptionNote.trim()) line.exception_note = state.exceptionNote.trim()
      }
      if (state.binId) line.target_bin_id = Number(state.binId)
      lines.push(line)
    }

    if (lines.length === 0) {
      messageApi.warning('请先在明细行录入本次收货数量')
      return
    }
    if (problems.length > 0) {
      messageApi.error(problems.slice(0, 2).join('；') + (problems.length > 2 ? ` 等 ${problems.length} 项` : ''))
      return
    }

    // 幂等键：同单重试复用同键（重放返回既有结果不重复累计），切单 / 成功后作废（plan §7）
    const inboundId = String(selected.id)
    const idempotencyKey =
      idemKeyRef.current?.inboundId === inboundId ? idemKeyRef.current.key : newIdempotencyKey()
    idemKeyRef.current = { inboundId, key: idempotencyKey }
    confirmMutation.mutate({
      inbound_no: selected.inbound_no,
      lines,
      idempotency_key: idempotencyKey,
      remark: remark.trim() || undefined,
    })
  }

  const handleSelect = (order: InboundOrder) => {
    setSelected(order)
    setRemark('')
  }

  const handleFilter = (value: InboundOrderStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    setLineStates({})
    setLoadedForId(null)
    list.resetToFirstPage()
  }

  const handleScanSubmit = (code: string) => {
    // 扫码链路属 Scan 端 / F16（frontend.md §19.2）：本轮仅本地接收手输编码
    messageApi.info(`已接收手输编码：${code}（扫码直达收货属 Scan 端通用扫码中心）`)
    setScanOpen(false)
  }

  const totalOrdered = detailItems.reduce((sum, item) => sum + item.qty, 0)
  const totalReceived = detailItems.reduce((sum, item) => sum + item.qty_received, 0)
  const totalRemaining = detailItems.reduce((sum, item) => sum + lineRemaining(item), 0)

  const infoNode = !selected ? (
    <SfEmpty description="从收货任务列表选择一张入库单卡，此处展示明细行应收 / 已收 / 待收数量" />
  ) : detailQuery.isPending ? (
    <SfLoading rows={4} />
  ) : detailQuery.error ? (
    <SfError
      error={detailQuery.error}
      description="入库单明细加载失败（GET /api/inbounds/{id}）"
      onRetry={() => void detailQuery.refetch()}
    />
  ) : (
    <>
      <PadInfoCard
        title={`入库单 · ${selected.inbound_no}`}
        items={[
          { label: '入库单号', value: selected.inbound_no },
          { label: '来源单号', value: selected.source_no || EMPTY_TEXT },
          { label: '状态', value: <InboundStatusTag status={selected.status} /> },
          { label: '仓库', value: warehouseNameOf(selected.warehouse_id) },
          { label: '明细行数', value: `${detailItems.length} 行` },
          { label: '创建时间', value: formatDateTime(selected.created_at) },
          { label: '应收数量', value: formatNumber(totalOrdered), emphasis: true },
          { label: '已收数量', value: formatNumber(totalReceived), emphasis: true },
          { label: '待收数量', value: formatNumber(totalRemaining), emphasis: true },
          ...detailItems.map((item) => ({
            label: `行${item.line_no} · ${skuLabel(item.sku_id)}`,
            value: `${skuNameOf(item.sku_id)}｜应收 ${formatNumber(item.qty)} · 已收 ${formatNumber(item.qty_received)} · 待收 ${formatNumber(lineRemaining(item))}`,
          })),
        ]}
      />
      <p className="sf-pad-muted-note">
        待收 = 应收 − 已收；累计收货（含合格 + 不合格待定）不得超过原始数量（business-flow.md §2.3），
        超量由后端强校验
      </p>
    </>
  )

  const actionNode = (
    <>
      <section className="sf-pad-card" aria-label="收货操作">
        <h3 className="sf-pad-card-title">收货操作</h3>
        {!selected ? (
          <SfEmpty description="选择入库单后按明细行步进本次收货数量并录入批次 / 效期" />
        ) : detailQuery.isPending ? (
          <SfLoading rows={4} />
        ) : detailQuery.error ? (
          <SfError error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
        ) : detailItems.length === 0 ? (
          <SfEmpty description="该入库单暂无明细行" />
        ) : (
          <>
            {detailItems.map((item) => {
              const state = lineStates[String(item.id)]
              if (!state) return null
              const remaining = lineRemaining(item)
              const sku = skuOf(item.sku_id)
              const batchManaged = sku?.is_batch_managed === true
              const expiryManaged = sku?.is_batch_managed === true && sku?.is_expiry_managed === true
              const serialManaged = sku?.is_serial_managed === true
              const serialCount = parseSerials(state.serialsText).length
              return (
                <div
                  key={String(item.id)}
                  role="group"
                  aria-label={`收货行 ${item.line_no}`}
                  style={{
                    display: 'grid',
                    gap: 'var(--sf-space-3)',
                    borderTop: '1px solid var(--sf-border)',
                    paddingTop: 'var(--sf-space-3)',
                  }}
                >
                  <div>
                    <span className="sf-pad-info-value">
                      行{item.line_no} · {skuLabel(item.sku_id)} · {skuNameOf(item.sku_id)}
                    </span>
                    <div className="sf-pad-info-label">
                      待收 {formatNumber(remaining)}
                      {batchManaged ? ' · 批次必填' : ''}
                      {expiryManaged ? ' · 效期必填' : ''}
                      {serialManaged ? ' · 序列号按件' : ''}
                      {sku === undefined ? ' · SKU 管控开关未加载，校验由后端执行' : ''}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: 'var(--sf-space-3)' }}>
                    <QtyStepper
                      label="本次合格"
                      value={state.qtyGood}
                      max={remaining}
                      onChange={(value) => patchLine(String(item.id), { qtyGood: value })}
                    />
                    <QtyStepper
                      label="本次拒收"
                      value={state.qtyRejected}
                      max={remaining}
                      onChange={(value) => patchLine(String(item.id), { qtyRejected: value })}
                    />
                  </div>
                  <Input
                    size="large"
                    placeholder={batchManaged ? '批次号录入（必填）' : '批次号录入（可选）'}
                    value={state.batchNo}
                    onChange={(e) => patchLine(String(item.id), { batchNo: e.target.value })}
                    allowClear
                    style={{ height: 'var(--sf-pad-touch-min)' }}
                  />
                  <DatePicker
                    size="large"
                    placeholder="效期（到期日）"
                    value={state.expiry}
                    onChange={(value) => patchLine(String(item.id), { expiry: value })}
                    style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
                  />
                  <DatePicker
                    size="large"
                    placeholder="生产日期（可选）"
                    value={state.productionDate}
                    onChange={(value) => patchLine(String(item.id), { productionDate: value })}
                    style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
                  />
                  {serialManaged && (
                    <>
                      <Input.TextArea
                        size="large"
                        rows={4}
                        placeholder={'序列号逐件录入：每行一个序列号\n（件数须与本次合格数量一致）'}
                        value={state.serialsText}
                        onChange={(e) => patchLine(String(item.id), { serialsText: e.target.value })}
                      />
                      <p className="sf-pad-muted-note">
                        序列号已录 {serialCount} 件 · 须与本次合格 {state.qtyGood} 件一致，请求内不得重复
                      </p>
                    </>
                  )}
                  <Select
                    size="large"
                    placeholder="目标库位（可选，缺省由后端推荐）"
                    value={state.binId}
                    onChange={(value) => patchLine(String(item.id), { binId: value })}
                    options={binSelectOptions}
                    loading={binOptions.isPending}
                    allowClear
                    showSearch
                    optionFilterProp="label"
                    style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
                  />
                  <div
                    role="group"
                    aria-label="异常收货登记"
                    style={{ display: 'grid', gap: 'var(--sf-space-2)' }}
                  >
                    <Select
                      size="large"
                      placeholder="异常收货类型（可选：少货/多货/错货/破损…）"
                      value={state.exceptionType}
                      onChange={(value) => patchLine(String(item.id), { exceptionType: value })}
                      options={EXCEPTION_TYPE_OPTIONS}
                      allowClear
                      style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
                    />
                    {state.exceptionType && (
                      <Input.TextArea
                        size="large"
                        rows={2}
                        placeholder="异常说明（随异常收货同事务登记异常中心）"
                        value={state.exceptionNote}
                        onChange={(e) => patchLine(String(item.id), { exceptionNote: e.target.value })}
                      />
                    )}
                  </div>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      gap: 'var(--sf-space-3)',
                    }}
                  >
                    <span className="sf-pad-info-label">免检（合格品直达可用，不进待检）</span>
                    <Switch
                      size="default"
                      checked={state.noInspect}
                      onChange={(checked) => patchLine(String(item.id), { noInspect: checked })}
                      aria-label={`行 ${item.line_no} 免检开关`}
                    />
                  </div>
                </div>
              )
            })}
            <Input
              size="large"
              placeholder="收货备注（可选，随收货单落库）"
              value={remark}
              onChange={(e) => setRemark(e.target.value)}
              allowClear
              style={{ height: 'var(--sf-pad-touch-min)' }}
            />
            <Button
              block
              size="large"
              type="primary"
              icon={<CheckOutlined />}
              loading={confirmMutation.isPending}
              disabled={!hasInput}
              onClick={handleConfirm}
              style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
            >
              确认收货
            </Button>
            <p className="sf-pad-muted-note">
              确认后单事务生效：收货单落库 → 异常同事务登记异常中心（§3.4）→ 批次/序列号采集 →
              累计与状态推进 → 逐行生成上架任务；幂等键防重，重试不重复累计（plan §7）
            </p>
          </>
        )}
      </section>

      <section className="sf-pad-card" aria-label="扫码占位">
        <h3 className="sf-pad-card-title">扫码</h3>
        <PadScanStub
          placeholder="手工输入采购单 / 入库单 / SKU 条码兜底"
          onSubmit={(code) =>
            messageApi.info(`已接收手输编码：${code}（扫码直达收货属 Scan 端通用扫码中心）`)
          }
        />
      </section>

      <section className="sf-pad-card" aria-label="收货异常拍照">
        <h3 className="sf-pad-card-title">收货异常</h3>
        <DisabledAction
          label="拍照登记收货异常"
          icon={<CameraOutlined />}
          reason="拍照上传依赖 /api/files 文件端点与 Scan 端拍照集成（business-flow.md §3.4 / frontend.md §20.6），本轮仅支持文本异常：可在明细行选择异常类型与说明，随收货确认同事务登记"
        />
      </section>
    </>
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="入库单状态筛选">
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
          description="入库单列表加载失败（GET /api/inbounds）"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有收货任务，试试切换状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((order) => (
            <InboundTaskCard
              key={String(order.id)}
              order={order}
              warehouseName={warehouseNameOf(order.warehouse_id)}
              selected={String(selected?.id ?? '') === String(order.id)}
              onClick={() => handleSelect(order)}
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
        // 竖屏（§20.4）：顶部当前任务卡 → 信息卡 → 收货操作 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {selected && <InboundTaskCard order={selected} warehouseName={warehouseNameOf(selected.warehouse_id)} selected />}
              {selected && <div className="sf-pad-tasks-detail">{infoNode}</div>}
              {selected && actionNode}
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
            loading: confirmMutation.isPending,
            disabled: !selected || !hasInput,
            disabledReason: '先选择入库单任务卡并录入本次收货数量，再确认收货',
            onClick: handleConfirm,
          },
        ]}
      />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <PadScanStub onSubmit={handleScanSubmit} placeholder="手工输入采购单 / 入库单 / SKU 条码兜底" />
      </Modal>
    </>
  )
}
