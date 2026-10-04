import { useEffect, useMemo, useState } from 'react'
import { Button, Input, Modal, Select, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckOutlined,
  MinusOutlined,
  PauseOutlined,
  PlusOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { resolveErrorMessage } from '@/api/client'
import {
  INSPECTION_METHOD_LABEL,
  QUALITY_RESULT_TAG_META,
  qualityApi,
  type QualityExecutePayload,
  type QualityInspectionItem,
  type QualityInspectionLine,
  type QualityInspectionQuery,
  type QualityOrderStatus,
  type QualityResult,
} from '@/api/quality'
import {
  fetchFileObjectUrl,
  fileApi,
  resolveFileDownloadPath,
  type FileItem,
} from '@/api/file'
import { buildIdItemMap, buildSkuMaps, fetchSkuOptions } from '@/api/options'
import type { SkuItem } from '@/api/masterdata'
import type { SalesId } from '@/api/sales'
import { SfAttachment, type AttachmentItem } from '@/components/common/SfAttachment'
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
 * 质检单状态 → SfStatusTag 文案/语义（frontend.md §24：颜色统一经 SfStatusTag）。
 * 后端大写枚举 PENDING/INSPECTING/COMPLETED（models.go:36-38），文案对齐
 * types/status.ts 注册表（质检中/已完成），PENDING 以质检语境「待质检」显式指定。
 */
const QC_STATUS_TAG_META: Record<QualityOrderStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待质检', semantic: 'pending' },
  INSPECTING: { label: '质检中', semantic: 'processing' },
  COMPLETED: { label: '已完成', semantic: 'success' },
}

function QualityStatusTag({ status }: { status: QualityOrderStatus }) {
  const meta = QC_STATUS_TAG_META[status]
  return <SfStatusTag label={meta?.label ?? status} semantic={meta?.semantic ?? 'neutral'} />
}

/** 任务源状态 chip（默认筛「待质检」= 待质检任务；审计已核实问题 #5：原 /api/quality/inspections 为前端先行契约） */
const STATUS_OPTIONS: Array<{ label: string; value: QualityOrderStatus }> = (
  ['PENDING', 'INSPECTING', 'COMPLETED'] as const
).map((value) => ({ value, label: QC_STATUS_TAG_META[value].label }))

/** 判定快捷值（backend qcResults 值域前三；service_quality.go:64-67） */
const JUDGE_RESULTS: Array<'合格' | '部分合格' | '不合格'> = ['合格', '部分合格', '不合格']

/** 处置下拉九中文值（检验结论与处理结果共用 result 字段，api/quality.ts 值域注释） */
const DISPOSITION_OPTIONS: Array<{ label: string; value: QualityResult }> = (
  Object.keys(QUALITY_RESULT_TAG_META) as QualityResult[]
).map((value) => ({ value, label: QUALITY_RESULT_TAG_META[value].label }))

/** 结果标签：中文值域不与 types/status.ts 注册表相交，经 label + semantic 显式指定 */
function ResultTag({ result }: { result: string }) {
  const meta = QUALITY_RESULT_TAG_META[result as QualityResult]
  return (
    <SfStatusTag
      label={meta?.label ?? (result || EMPTY_TEXT)}
      semantic={meta?.semantic ?? 'neutral'}
    />
  )
}

/** 明细行判定录入态：合格数量步进，不合格 = 检验数量 − 合格数量（行和恒等于检验数量，
 * 与 service_quality.go:243-248「合格+不合格 必须 = 该行检验数量」同口径） */
interface QcLineState {
  qtyQualified: number
}

function initLineState(item: QualityInspectionLine): QcLineState {
  return { qtyQualified: item.qty_inspected }
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

/** 质检单任务卡：qc_no / 状态 / 来源单号 / 检验方式 / 检验·合格·不合格 / 检验人，
 * 字段回对 QualityOrder GORM 出参（api/quality.ts QualityInspectionItem），
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9） */
function QcTaskCard({
  order,
  selected = false,
  onClick,
}: {
  order: QualityInspectionItem
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
        <span className="sf-pad-task-card__no">{order.qc_no}</span>
        <QualityStatusTag status={order.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>来源 {order.source_no}</span>
        <span>{INSPECTION_METHOD_LABEL[order.inspection_type] ?? order.inspection_type}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(order.qty_inspected)}</span>
        <span className="sf-pad-task-card__qty-unit">检验</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>
          合格 {formatNumber(order.qty_qualified)} · 不良 {formatNumber(order.qty_defective)}
        </span>
        <span>{formatDateTime(order.created_at)}</span>
      </div>
    </div>
  )
}

/**
 * Pad 质检页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §4 / §21.4 质检要点）：
 * - 左栏：任务源 = GET /api/quality（qualityApi.inspections，默认筛 PENDING 待质检）→
 *   QcTaskCard 列表 + 状态 chip（审计已核实问题 #5）；
 * - 中栏：选中质检单 PadInfoCard（qc_no / inspection_type / qty_qualified / qty_defective /
 *   inspector_name 回对 QualityOrder 出参）+ 明细行检验数量；
 * - 右栏：质检判定 = [开始质检]（POST /api/quality/{id}/start，PENDING→INSPECTING）→
 *   逐行合格数量步进（不合格自动补齐）→ [合格]/[部分合格]/[不合格] 快捷判定与
 *   处置九中文值下拉（检验结论与处理结果共用 result 字段），统一提交
 *   POST /api/quality/{id}/execute（service_quality.go:49-67，结果值为中文）；
 *   [拍照留证] 真实接线（POST /api/files 已交付，internal/datax/handler.go:123-128）：
 *   经 SfAttachment 上传（module=QUALITY、business_no=质检单号），文件引用随 execute
 *   image_refs 落质检单（QCExecuteInput.ImageRefs，service_quality.go:52）；Pad 原生相机 /
 *   Scan 端拍照集成仍待 Scan 应用交付，Web Pad 经文件选择/系统相机入口完成；
 * - 竖屏：顶部当前质检任务卡 → 信息卡 → 质检判定 → 任务列表滚动区 → 底部 PadActionBar。
 * 结果 / 处置展示一律经 SfStatusTag（§24），不自定颜色。
 */
export default function PadQualityPage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState<QualityOrderStatus | 'all'>('PENDING')
  const [selected, setSelected] = useState<QualityInspectionItem | null>(null)
  const [scanOpen, setScanOpen] = useState(false)
  const [remark, setRemark] = useState('')
  /** 处置九中文值（result 字段；快捷判定按钮自带结果，不依赖该下拉） */
  const [resultChoice, setResultChoice] = useState<QualityResult | undefined>(undefined)
  /** 明细行判定录入态，键 = 行 id 字符串（String(item.id)） */
  const [lineStates, setLineStates] = useState<Record<string, QcLineState>>({})
  /** 已初始化判定态的质检单 id（防窗口聚焦重取覆盖录入中内容） */
  const [loadedForId, setLoadedForId] = useState<string | null>(null)
  /** 拍照留证：本单已上传、待随 execute image_refs 提交的文件（POST /api/files 返回 FileItem） */
  const [photos, setPhotos] = useState<FileItem[]>([])
  const [photoUploading, setPhotoUploading] = useState(false)

  const params = useMemo<QualityInspectionQuery>(
    () => ({ status: statusFilter === 'all' ? undefined : statusFilter }),
    [statusFilter],
  )

  const list = usePagedList<QualityInspectionItem, QualityInspectionQuery>({
    queryKey: ['pad', 'quality', 'orders'],
    fetch: (q) => qualityApi.inspections(q),
    params,
    defaultPageSize: 30,
  })

  // 选中质检单明细（GET /api/quality/{id} → {order, items}，service_quality.go:457-460）
  const detailQuery = useQuery({
    queryKey: ['pad', 'quality', 'detail', String(selected?.id ?? '')],
    queryFn: () => {
      if (!selected) throw new Error('未选择质检单')
      return qualityApi.detail(selected.id)
    },
    enabled: selected != null,
  })

  // SKU 编码/名称映射（options 一次取全；失败降级为 ID 展示，不阻塞列表）
  const skuOptions = useQuery({ queryKey: ['pad', 'options', 'sku'], queryFn: fetchSkuOptions })
  const skuItemMap = useMemo(
    () => buildIdItemMap(skuOptions.data ?? [], (s) => s.id),
    [skuOptions.data],
  )
  const skuCodeMap = useMemo(() => buildSkuMaps(skuOptions.data ?? []).code, [skuOptions.data])

  const detailItems: QualityInspectionLine[] = detailQuery.data?.items ?? []
  const detailOrder = detailQuery.data?.order ?? null
  const inspecting = detailOrder?.status === 'INSPECTING'
  const completed = detailOrder?.status === 'COMPLETED'

  // 明细到达后初始化逐行判定态（仅切换单据时一次；提交成功后重建）
  useEffect(() => {
    if (!selected || !detailQuery.data) return
    const qcId = String(selected.id)
    if (loadedForId === qcId) return
    const next: Record<string, QcLineState> = {}
    for (const item of detailQuery.data.items) next[String(item.id)] = initLineState(item)
    setLineStates(next)
    setLoadedForId(qcId)
  }, [detailQuery.data, selected, loadedForId])

  const patchLine = (lineId: string, patch: Partial<QcLineState>) => {
    setLineStates((prev) =>
      prev[lineId] ? { ...prev, [lineId]: { ...prev[lineId], ...patch } } : prev,
    )
  }

  const skuLabel = (skuId: number): string =>
    skuCodeMap.get(String(skuId)) ?? `SKU #${String(skuId)}`
  const skuNameOf = (skuId: number): string => {
    const sku: SkuItem | undefined = skuItemMap.get(String(skuId))
    return sku?.product_name ?? EMPTY_TEXT
  }
  /** 行实时合格/不良：质检中取录入态（缺省全合格），已完成取后端落列结果 */
  const lineQty = (item: QualityInspectionLine): { qualified: number; defective: number } => {
    if (completed) return { qualified: item.qty_qualified, defective: item.qty_defective }
    const qualified = lineStates[String(item.id)]?.qtyQualified ?? item.qty_inspected
    return { qualified, defective: Math.max(0, item.qty_inspected - qualified) }
  }

  const startMutation = useMutation({
    mutationFn: (id: SalesId) => qualityApi.start(id),
    onSuccess: (order) => {
      messageApi.success(`质检已开始：${order.qc_no}（PENDING → INSPECTING）`)
      setSelected(order)
      void queryClient.invalidateQueries({ queryKey: ['pad', 'quality'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const executeMutation = useMutation({
    mutationFn: ({ id, payload }: { id: SalesId; payload: QualityExecutePayload }) =>
      qualityApi.execute(id, payload),
    onSuccess: (order) => {
      messageApi.success(`质检结果已提交：${order.qc_no} · 处理结果「${order.result}」`)
      setSelected(order)
      setPhotos([])
      void queryClient.invalidateQueries({ queryKey: ['pad', 'quality'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // ---------- 拍照留证（POST /api/files 已交付：internal/datax/handler.go:123-128；
  // 上传引用随 execute image_refs 落质检单，QCExecuteInput.ImageRefs service_quality.go:52） ----------

  /** FileItem → SfAttachment 条目（fileName 形状适配；FileItem 为 snake_case 出参） */
  const photoItems: AttachmentItem[] = useMemo(
    () =>
      photos.map((file) => ({
        id: file.id,
        fileName: file.file_name,
        fileType: file.mime_type,
        size: file.size_bytes,
        uploader: file.uploader_name,
        uploadedAt: file.created_at,
      })),
    [photos],
  )
  const photoFileMap = useMemo(() => new Map(photos.map((file) => [String(file.id), file])), [photos])

  /** 上传：逐文件 POST /api/files（module=QUALITY、business_no=质检单号，文件中心按单据可检索）；
   * 部分失败不阻塞其余文件，失败原因呈可读信封信息（权限/白名单由后端权威校验） */
  const handlePhotoUpload = async (files: File[]) => {
    if (!selected || files.length === 0 || photoUploading) return
    setPhotoUploading(true)
    try {
      const results = await Promise.allSettled(
        files.map((file) => fileApi.upload({ file, module: 'QUALITY', businessNo: selected.qc_no })),
      )
      const uploaded: FileItem[] = []
      let firstError: unknown
      for (const result of results) {
        if (result.status === 'fulfilled') uploaded.push(result.value)
        else firstError ??= result.reason
      }
      if (uploaded.length > 0) {
        setPhotos((prev) => [...prev, ...uploaded])
        messageApi.success(`已上传 ${uploaded.length} 张照片，随质检结果提交关联`)
      }
      if (uploaded.length < results.length) {
        messageApi.error(
          `${results.length - uploaded.length} 个文件上传失败：${resolveErrorMessage(firstError)}`,
        )
      }
    } finally {
      setPhotoUploading(false)
    }
  }

  /** 从待提交引用移除（不删除文件中心记录：避免 datax:file:delete 依赖与误删已留存文件） */
  const removePhoto = (item: AttachmentItem) => {
    setPhotos((prev) => prev.filter((file) => String(file.id) !== String(item.id)))
  }

  /** 照片预览：认证流经后端下发地址取文件流转 objectUrl（SfAttachment 关闭时统一释放） */
  const previewPhoto = async (item: AttachmentItem): Promise<string | void> => {
    const file = photoFileMap.get(String(item.id))
    if (!file) return undefined
    try {
      return await fetchFileObjectUrl(resolveFileDownloadPath(file))
    } catch (error) {
      messageApi.error(`照片预览加载失败：${resolveErrorMessage(error)}`)
      return undefined
    }
  }

  /**
   * 提交质检结果（POST /api/quality/{id}/execute）：
   * - 逐行 qty_qualified 录入、qty_defective = 检验数量 − 合格数量（行和恒等，后端强校验同口径）；
   * - 快捷判定「合格/部分合格/不合格」与数量口径一致性预检（后端不阻断矛盾组合，
   *   前端按 business-flow §4.2 语义拦截，避免落库矛盾记录）；
   * - 处置九中文值（退供应商/报废/返工/降级/转不良品仓/特批放行）直接作为 result 提交。
   */
  const handleExecute = (result: QualityResult) => {
    if (!selected || !detailQuery.data || executeMutation.isPending) return
    // 行判定态未就绪（切单后缓存明细先于初始化 effect 渲染的一帧窗口）时拦截，
    // 防止以缺省 0 合格静默提交"全部不良"（对齐收货页缺行状态即安全拦截口径）
    if (detailItems.some((item) => !lineStates[String(item.id)])) {
      messageApi.warning('明细行判定未就绪，请稍候重试')
      return
    }
    const problems: string[] = []
    let totalQualified = 0
    let totalDefective = 0
    const lines = detailItems.map((item) => {
      const state = lineStates[String(item.id)]
      const qtyQualified = state?.qtyQualified ?? 0
      const qtyDefective = Math.max(0, item.qty_inspected - qtyQualified)
      totalQualified += qtyQualified
      totalDefective += qtyDefective
      return { line_no: item.line_no, qty_qualified: qtyQualified, qty_defective: qtyDefective }
    })
    if (lines.length === 0) {
      problems.push('该质检单无明细行，无法提交')
    }
    if (result === '合格' && totalDefective > 0) {
      problems.push('存在不合格数量：请将各行合格数量调满再判定「合格」，或改选「部分合格」')
    }
    if (result === '不合格' && totalQualified > 0) {
      problems.push('存在合格数量：请将各行合格数量调零再判定「不合格」，或改选「部分合格」')
    }
    if (result === '部分合格' && (totalQualified <= 0 || totalDefective <= 0)) {
      problems.push(
        totalQualified <= 0 ? '当前无合格数量：请判定「不合格」' : '当前无不合格数量：请判定「合格」',
      )
    }
    if (problems.length > 0) {
      messageApi.error(problems.join('；'))
      return
    }
    executeMutation.mutate({
      id: selected.id,
      payload: {
        lines,
        result,
        image_refs: photos.length > 0 ? photos.map((file) => String(file.id)) : undefined,
        remark: remark.trim() || undefined,
      },
    })
  }

  const handleSelect = (order: QualityInspectionItem) => {
    setSelected(order)
    setRemark('')
    setResultChoice(undefined)
    setPhotos([])
  }

  const handleFilter = (value: QualityOrderStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    setLineStates({})
    setLoadedForId(null)
    setPhotos([])
    list.resetToFirstPage()
  }

  const handleScanSubmit = (code: string) => {
    // 扫码链路属 Scan 端 / F16（frontend.md §19.2）：本轮仅本地接收手输编码
    messageApi.info(`已接收手输编码：${code}（扫码直达质检属 Scan 端通用扫码中心）`)
    setScanOpen(false)
  }

  const totalInspected = detailItems.reduce((sum, item) => sum + item.qty_inspected, 0)
  // 质检中展示逐行实时录入汇总；待质检 / 已完成展示后端落列数值（不前端编造）
  const liveTotals = detailItems.reduce(
    (acc, item) => {
      const { qualified, defective } = lineQty(item)
      return { qualified: acc.qualified + qualified, defective: acc.defective + defective }
    },
    { qualified: 0, defective: 0 },
  )

  const infoNode = !selected ? (
    <SfEmpty description="从质检任务列表选择一张任务卡，此处展示质检单号 / 检验方式 / 检验·合格·不合格数量" />
  ) : detailQuery.isPending ? (
    <SfLoading rows={4} />
  ) : detailQuery.error ? (
    <SfError
      error={detailQuery.error}
      description="质检单明细加载失败（GET /api/quality/{id}）"
      onRetry={() => void detailQuery.refetch()}
    />
  ) : (
    <>
      <PadInfoCard
        title={`质检单 · ${selected.qc_no}`}
        items={[
          { label: '质检单号', value: selected.qc_no },
          { label: '状态', value: <QualityStatusTag status={selected.status} /> },
          { label: '处理结果', value: selected.result ? <ResultTag result={selected.result} /> : EMPTY_TEXT },
          {
            label: '检验方式',
            value: INSPECTION_METHOD_LABEL[selected.inspection_type] ?? selected.inspection_type,
          },
          { label: '来源单号', value: selected.source_no || EMPTY_TEXT },
          { label: '检验人', value: selected.inspector_name || EMPTY_TEXT },
          {
            label: '检验时间',
            value: selected.inspected_at ? formatDateTime(selected.inspected_at) : EMPTY_TEXT,
          },
          { label: '创建时间', value: formatDateTime(selected.created_at) },
          { label: '检验数量', value: formatNumber(totalInspected), emphasis: true },
          {
            label: completed ? '合格数量' : '已录合格',
            value: formatNumber(completed ? selected.qty_qualified : liveTotals.qualified),
            emphasis: true,
          },
          {
            label: completed ? '不合格数量' : '已录不合格',
            value: formatNumber(completed ? selected.qty_defective : liveTotals.defective),
            emphasis: true,
          },
          ...detailItems.map((item) => {
            const { qualified, defective } = lineQty(item)
            return {
              label: `行${item.line_no} · ${skuLabel(item.sku_id)}`,
              value: `${skuNameOf(item.sku_id)}｜检验 ${formatNumber(item.qty_inspected)} · 合格 ${formatNumber(qualified)} · 不良 ${formatNumber(defective)}`,
            }
          }),
        ]}
      />
      <p className="sf-pad-muted-note">
        行合格 + 不良 = 检验数量（business-flow.md §4.2）；提交后合格品转入可用、
        不良品转不良品仓并产生库存流水（§4.3），待检库存须已完成上架（service_quality.go 前置）
      </p>
    </>
  )

  const actionNode = (
    <>
      <section className="sf-pad-card" aria-label="质检判定">
        <h3 className="sf-pad-card-title">质检判定</h3>
        {!selected ? (
          <SfEmpty description="选择质检单后先开始质检，再逐行录入合格数量并判定结果" />
        ) : detailQuery.isPending ? (
          <SfLoading rows={4} />
        ) : detailQuery.error ? (
          <SfError error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
        ) : selected.status === 'PENDING' ? (
          <>
            <Button
              block
              size="large"
              type="primary"
              icon={<CheckOutlined />}
              loading={startMutation.isPending}
              onClick={() => startMutation.mutate(selected.id)}
              style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
            >
              开始质检
            </Button>
            <p className="sf-pad-muted-note">
              开始质检后 PENDING → INSPECTING（models.go:36-38），判定与处置录入随之开放
            </p>
          </>
        ) : completed ? (
          <SfEmpty description="该质检单已完成：处理结果已落定并触发库存映射，可在信息卡查看结果" />
        ) : (
          <>
            {detailItems.map((item) => {
              const state = lineStates[String(item.id)]
              if (!state) return null
              const defective = Math.max(0, item.qty_inspected - state.qtyQualified)
              return (
                <div
                  key={String(item.id)}
                  role="group"
                  aria-label={`质检行 ${item.line_no}`}
                  style={{
                    display: 'grid',
                    gap: 'var(--sf-space-2)',
                    borderTop: '1px solid var(--sf-border)',
                    paddingTop: 'var(--sf-space-2)',
                  }}
                >
                  <div>
                    <span className="sf-pad-info-value">
                      行{item.line_no} · {skuLabel(item.sku_id)} · {skuNameOf(item.sku_id)}
                    </span>
                    <div className="sf-pad-info-label">
                      批次 {item.batch_no || EMPTY_TEXT} · 检验 {formatNumber(item.qty_inspected)}
                    </div>
                  </div>
                  <QtyStepper
                    label="合格数量"
                    value={state.qtyQualified}
                    max={item.qty_inspected}
                    onChange={(value) => patchLine(String(item.id), { qtyQualified: value })}
                  />
                  <p className="sf-pad-muted-note">
                    不合格 {formatNumber(defective)}（= 检验数量 − 合格数量，行和须等于检验数量）
                  </p>
                </div>
              )
            })}
            <div role="group" aria-label="快捷判定" style={{ display: 'grid', gap: 'var(--sf-space-2)' }}>
              {JUDGE_RESULTS.map((result) => (
                <Button
                  key={result}
                  block
                  size="large"
                  loading={executeMutation.isPending}
                  onClick={() => handleExecute(result)}
                  style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
                >
                  {result}
                </Button>
              ))}
            </div>
            <p className="sf-pad-muted-note">
              快捷判定按 business-flow.md §4.2 语义与行数量一致性预检；提交后 INSPECTING →
              COMPLETED 并触发库存映射
            </p>
          </>
        )}
      </section>

      <section className="sf-pad-card" aria-label="处置与原因录入">
        <h3 className="sf-pad-card-title">处置与原因</h3>
        <Select
          size="large"
          placeholder="处理结果（合格/部分合格/不合格/退供应商/报废/返工/降级/转不良品仓/特批放行）"
          value={resultChoice}
          onChange={(value) => setResultChoice(value)}
          options={DISPOSITION_OPTIONS}
          disabled={!inspecting}
          style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
        />
        <Input.TextArea
          size="large"
          rows={3}
          placeholder="不合格原因 / 备注（随质检结果提交落库）"
          value={remark}
          onChange={(e) => setRemark(e.target.value)}
          disabled={!inspecting}
          style={{ minHeight: 'var(--sf-pad-touch-min)' }}
        />
        <Button
          block
          size="large"
          type="primary"
          loading={executeMutation.isPending}
          disabled={!inspecting || !resultChoice}
          onClick={() => resultChoice && handleExecute(resultChoice)}
          style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
        >
          提交处置结果
        </Button>
        <p className="sf-pad-muted-note">
          处置九中文值域同 service_quality.go:64-67（检验结论与处理结果共用 result 字段）；
          转不良品仓将产生对应库存变动与流水（business-flow.md §4.3）
        </p>
      </section>

      <section className="sf-pad-card" aria-label="扫码占位">
        <h3 className="sf-pad-card-title">扫码</h3>
        <PadScanStub
          placeholder="手工输入质检单 / SKU 条码兜底"
          onSubmit={(code) =>
            messageApi.info(`已接收手输编码：${code}（扫码直达质检属 Scan 端通用扫码中心）`)
          }
        />
      </section>

      <section className="sf-pad-card" aria-label="拍照留证">
        <h3 className="sf-pad-card-title">拍照留证</h3>
        {!selected ? (
          <SfEmpty description="选择质检单后可拍照留证，照片随质检结果一并提交" />
        ) : completed ? (
          <p className="sf-pad-muted-note">
            {detailOrder?.image_refs && detailOrder.image_refs.length > 0
              ? `已关联 ${detailOrder.image_refs.length} 张照片（文件中心按业务单号 ${detailOrder.qc_no} 检索）`
              : '该质检单无现场照片'}
          </p>
        ) : (
          <>
            <SfAttachment
              items={photoItems}
              title={`待提交照片（${photoItems.length}）`}
              uploading={photoUploading}
              uploadAccept="image/*"
              uploadMultiple
              uploadLabel="拍照 / 选择图片"
              onUpload={(files) => void handlePhotoUpload(files)}
              onDelete={removePhoto}
              onPreview={previewPhoto}
              emptyText="暂无照片：拍摄不合格现场照片，提交质检结果时随 image_refs 关联"
            />
            <p className="sf-pad-muted-note">
              照片经 POST /api/files 上传（module=QUALITY · business_no={selected.qc_no}），
              提交质检结果时随 image_refs 落质检单；「删除」仅从本次提交引用剔除，
              文件保留在文件中心
            </p>
          </>
        )}
      </section>
    </>
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="质检单状态筛选">
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
          description="质检单列表加载失败（GET /api/quality）"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有质检任务，试试切换状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((order) => (
            <QcTaskCard
              key={String(order.id)}
              order={order}
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
        // 竖屏（§20.4）：顶部当前质检任务卡 → 信息卡 → 质检判定 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {selected && <QcTaskCard order={selected} selected />}
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
            loading: executeMutation.isPending || startMutation.isPending,
            disabled: !selected || selected.status !== 'INSPECTING' || !resultChoice,
            disabledReason:
              selected?.status === 'PENDING'
                ? '先「开始质检」进入质检中，再提交判定 / 处置结果'
                : selected?.status === 'COMPLETED'
                  ? '该质检单已完成，处理结果已落定'
                  : '先选择处理结果（九类值域）后提交',
            onClick: () => resultChoice && handleExecute(resultChoice),
          },
        ]}
      />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <PadScanStub onSubmit={handleScanSubmit} placeholder="手工输入质检单 / SKU 条码兜底" />
      </Modal>
    </>
  )
}
