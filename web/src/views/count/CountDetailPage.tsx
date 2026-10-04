import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  InputNumber,
  Modal,
  Radio,
  Steps,
  Timeline,
  Tooltip,
  Typography,
  message,
} from 'antd'
import { ReloadOutlined, ScanOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import type { TimelineProps } from 'antd'
import {
  COUNT_APPROVE_PERMISSION,
  COUNT_CANCEL_PERMISSION,
  COUNT_EXECUTE_PERMISSION,
  COUNT_SCOPE_MODE_LABEL,
  countApi,
  type CountDetail,
  type CountDiffStatus,
  type CountDifference,
  type CountId,
  type CountItem,
  type CountRegistrationInput,
  type CountScope,
  type CountStatus,
} from '@/api/count'
import { masterdataApi, OPTIONS_PAGE_SIZE, type SkuItem } from '@/api/masterdata'
import { binApi, shelfApi, warehouseApi, zoneApi } from '@/api/warehouse'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfLoading } from '@/components/common/SfLoading'
import { SfError } from '@/components/common/SfError'
import { formatDateTime, formatNumber, formatPercent } from '@/utils/format'

const { Text } = Typography

/** 盘点单五态状态 → SfStatusTag（internal/stockops/models.go:143-147；PENDING_REVIEW 走注册表 pending_recheck） */
const COUNT_STATUS_TAG: Record<CountStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  DRAFT: { key: 'draft', label: '草稿', semantic: 'neutral' },
  COUNTING: { key: 'counting', label: '盘点中', semantic: 'processing' },
  PENDING_REVIEW: { key: 'pending_recheck', label: '待复核', semantic: 'pending' },
  COMPLETED: { key: 'completed', label: '已完成', semantic: 'success' },
  CANCELLED: { key: 'cancelled', label: '已取消', semantic: 'neutral' },
}

function CountStatusTag({ status }: { status: CountStatus }) {
  const meta = COUNT_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 差异行状态（db/migrations/000009 chk_count_differences_status：PENDING/APPROVED/REJECTED/EXECUTED） */
const DIFF_STATUS_TAG: Record<CountDiffStatus, { key: string; label: string; semantic: StatusSemantic }> = {
  PENDING: { key: 'pending', label: '待处理', semantic: 'pending' },
  APPROVED: { key: 'approved', label: '已批准', semantic: 'success' },
  REJECTED: { key: 'rejected', label: '已驳回', semantic: 'danger' },
  EXECUTED: { key: 'executed', label: '已执行', semantic: 'success' },
}

function DiffStatusTag({ status }: { status: CountDiffStatus }) {
  const meta = DIFF_STATUS_TAG[status]
  return <SfStatusTag status={meta?.key ?? status} label={meta?.label} semantic={meta?.semantic} />
}

/** 明细行盘/未盘仅由 qty_counted 是否为 null 推导（null=未登记，与「登记为 0」语义不同，inventory-rules.md §9） */
function isCounted(item: CountItem): boolean {
  return item.qty_counted !== null && item.qty_counted !== undefined
}

/** 状态机进度（五态线性段，models.go:143-147；已取消为终止态单独提示） */
const COUNT_STEPS: Array<{ key: CountStatus; title: string }> = [
  { key: 'DRAFT', title: '草稿' },
  { key: 'COUNTING', title: '盘点中' },
  { key: 'PENDING_REVIEW', title: '待复核' },
  { key: 'COMPLETED', title: '已完成' },
]

/** 流转动作（五态合法迁移，internal/stockops/count.go:116/334/416/563/632 状态守卫同源；
 * 全部为 POST，complete/reject 带 {opinion}、cancel 带 {reason}，internal/stockops/routes.go:66-71） */
type CountFlowAction = 'start' | 'finish' | 'complete' | 'reject' | 'cancel'

const STATUS_ACTIONS: Record<CountStatus, CountFlowAction[]> = {
  DRAFT: ['start', 'cancel'],
  COUNTING: ['finish', 'cancel'],
  PENDING_REVIEW: ['complete', 'reject'],
  COMPLETED: [],
  CANCELLED: [],
}

interface FlowActionMeta {
  label: string
  confirm: string
  danger?: boolean
  permission: string
  /** 需要意见 / 原因输入时的表单标签（start/finish 无输入） */
  inputLabel?: string
  placeholder?: string
}

const FLOW_ACTION_META: Record<CountFlowAction, FlowActionMeta> = {
  start: {
    label: '开始盘点',
    confirm:
      '开始盘点将冻结盘点范围内库存行（COUNT_FREEZE）并按冻结快照生成盘点明细，实盘期间相关库存锁定，直至盘点结束按审核结果解冻。',
    permission: COUNT_EXECUTE_PERMISSION,
  },
  finish: {
    label: '完成实盘',
    confirm:
      '提交后系统对比实盘与冻结快照生成差异，盘点单进入「待复核」。全部明细必须已登记（实盘为 0 也须显式登记）。',
    permission: COUNT_EXECUTE_PERMISSION,
  },
  complete: {
    label: '审核通过',
    confirm:
      '审核通过后盘点完成；差异由系统生成库存调整单并走审批链路，禁止直接修改系统库存（business-flow.md §10.2 硬性规则）。',
    permission: COUNT_APPROVE_PERMISSION,
    inputLabel: '审核意见',
    placeholder: '补充审核意见（选填）；差异说明随审核意见提交',
  },
  reject: {
    label: '驳回',
    confirm:
      '驳回后盘点单取消：差异行落「已驳回」并解冻，不调整库存（internal/stockops/count.go:546-598），该操作不可恢复。',
    danger: true,
    permission: COUNT_APPROVE_PERMISSION,
    inputLabel: '审核意见',
    placeholder: '请填写驳回原因',
  },
  cancel: {
    label: '取消盘点',
    confirm: '取消后盘点流程终止，已冻结范围解冻，该操作不可恢复。',
    danger: true,
    permission: COUNT_CANCEL_PERMISSION,
    inputLabel: '取消原因',
    placeholder: '请填写取消原因（选填）',
  },
}

interface FlowFormValues {
  opinion?: string
}

interface RegisterFormValues {
  /** 汇总行实盘数量（裸数字，登记为 0 须显式提交） */
  qty?: number
  /** 序列号行：'1'=在库 / '0'=缺失（逐件登记数量仅允许 0/1，inventory-rules.md §8.2） */
  serialQty?: string
}

/** ID → 文案映射（模式对齐 PadInventoryPage：options 端点一次取全本地映射，失败降级 `#id`） */
interface LabelMaps {
  warehouseMap: Map<string, string>
  zoneMap: Map<string, string>
  shelfMap: Map<string, string>
  binMap: Map<string, string>
  skuMap: Map<string, SkuItem>
}

function labelOrId(map: Map<string, string>, id: CountId): string {
  return map.get(String(id)) ?? `#${String(id)}`
}

/** SKU 行文案：skuMap 值为 SkuItem（非 string），取编码展示，缺失降级 `#id`（与 900/927 行同口径） */
function skuLabelOrId(map: Map<string, SkuItem>, id: CountId): string {
  return map.get(String(id))?.code ?? `#${String(id)}`
}

function scopeText(scope: CountScope, maps: LabelMaps): string {
  const label = COUNT_SCOPE_MODE_LABEL[scope.mode] ?? scope.mode
  switch (scope.mode) {
    case 'ALL':
      return label
    case 'ZONE':
      return `${label}：${(scope.zone_ids ?? []).map((id) => labelOrId(maps.zoneMap, id)).join('、')}`
    case 'SHELF':
      return `${label}：${(scope.shelf_ids ?? []).map((id) => labelOrId(maps.shelfMap, id)).join('、')}`
    case 'BIN':
      return `${label}：${(scope.bin_ids ?? []).map((id) => labelOrId(maps.binMap, id)).join('、')}`
    case 'SKU':
      return `${label}：${(scope.sku_ids ?? []).map((id) => skuLabelOrId(maps.skuMap, id)).join('、')}`
    default:
      return label
  }
}

/** 明细数量：带符号（差异列 / 差异合计用，盘盈为正） */
function signedNumber(value: number): string {
  return value > 0 ? `+${formatNumber(value)}` : formatNumber(value)
}

/** 服务端已返回全量行，本地切片仅做展示分页（不造假分页） */
function useLocalPaged<T>(rows: T[]) {
  const [state, setState] = useState({ page: 1, pageSize: 10 })
  const maxPage = Math.max(1, Math.ceil(rows.length / state.pageSize))
  const page = Math.min(state.page, maxPage)
  return {
    paged: rows.slice((page - 1) * state.pageSize, page * state.pageSize),
    pagination: { current: page, pageSize: state.pageSize },
    onPageChange: (page: number, pageSize: number) => setState({ page, pageSize }),
  }
}

/** 业务流程 Timeline 项：有真实时间显示时间，仅可确认发生（无落库时间戳）显示「已完成」 */
function flowItem(
  title: string,
  opts: { time?: string | null; done?: boolean; description?: string },
): NonNullable<TimelineProps['items']>[number] {
  const done = opts.time != null || opts.done === true
  return {
    color: done ? 'green' : 'gray',
    children: (
      <>
        <Text strong>{title}</Text>
        <Text type="secondary" style={{ marginLeft: 8 }}>
          {opts.time != null ? formatDateTime(opts.time) : done ? '已完成' : '待进行'}
        </Text>
        {opts.description && (
          <div>
            <Text type="secondary">{opts.description}</Text>
          </div>
        )}
      </>
    ),
  }
}

/**
 * 盘点详情（/counts/:id，frontend.md §10.5「不能做成普通 CRUD 表格」；数据 GET /api/counts/{id}
 * 返回 {order, items, differences}，internal/stockops/handler.go:170-174）：
 * 页头（单号 + 五态状态 + 流转按钮）→ 硬性规则提示 → SfSummaryBar（由 detail.items
 * 展示聚合：已盘/总行、完成率、系统/实盘合计、差异合计——仅对服务端返回数据聚合）
 * → Steps 状态机 → 盘点范围 → 基础信息 → 盘点明细（COUNTING 态经批量幂等
 * PUT /api/counts/{id}/items 登记，载荷 {items:[{InventoryRowID,SerialNo,Qty}]}，
 * store.go:204-208 Go 字段名；差异原因/备注不入登记载荷，随审核意见提交）
 * → 差异表（完成实盘后生成）→ 业务流程 Timeline。
 * 导入/导出/打印属后端缺口不实现（frontend.md §10.5 缺口）。
 */
export default function CountDetailPage() {
  const { id } = useParams<{ id: string }>()
  const countId: CountId = id ?? ''
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const [flowForm] = Form.useForm<FlowFormValues>()
  const [registerForm] = Form.useForm<RegisterFormValues>()
  const [flowAction, setFlowAction] = useState<CountFlowAction | null>(null)
  const [registering, setRegistering] = useState<CountItem | null>(null)
  const [scanValue, setScanValue] = useState('')

  const detailQuery = useQuery({
    queryKey: ['counts', countId, 'detail'],
    queryFn: () => countApi.detail(countId),
    enabled: countId !== '',
  })
  const detail: CountDetail | undefined = detailQuery.data
  const order = detail?.order
  const items = useMemo(() => detail?.items ?? [], [detail])
  const differences = useMemo(() => detail?.differences ?? [], [detail])

  // 基础资料 / 仓库空间 options 一次取全：仓库 / 库区 / 货架 / 库位 / SKU 编码本地映射
  const warehouses = useQuery({
    queryKey: ['count', 'warehouse-options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const zones = useQuery({
    queryKey: ['count', 'zone-options'],
    queryFn: () => zoneApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const shelves = useQuery({
    queryKey: ['count', 'shelf-options'],
    queryFn: () => shelfApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const bins = useQuery({
    queryKey: ['count', 'bin-options'],
    queryFn: () => binApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const skus = useQuery({
    queryKey: ['count', 'sku-options'],
    queryFn: () => masterdataApi.skus.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })

  const maps = useMemo<LabelMaps>(
    () => ({
      warehouseMap: new Map(
        (warehouses.data?.items ?? []).map((w) => [String(w.id), `${w.name}（${w.code}）`]),
      ),
      zoneMap: new Map((zones.data?.items ?? []).map((z) => [String(z.id), z.code])),
      shelfMap: new Map((shelves.data?.items ?? []).map((s) => [String(s.id), s.code])),
      binMap: new Map((bins.data?.items ?? []).map((b) => [String(b.id), b.code])),
      skuMap: new Map((skus.data?.items ?? []).map((s) => [String(s.id), s])),
    }),
    [warehouses.data, zones.data, shelves.data, bins.data, skus.data],
  )

  // 汇总：仅对服务端返回的 detail.items 做展示聚合，不做任何前端业务推导落库
  const summary = useMemo(() => {
    const total = items.length
    let counted = 0
    let systemTotal = 0
    let countedTotal = 0
    for (const item of items) {
      systemTotal += item.qty_system
      if (isCounted(item)) {
        counted += 1
        countedTotal += item.qty_counted as number
      }
    }
    return {
      total,
      counted,
      rate: total > 0 ? (counted / total) * 100 : 0,
      systemTotal,
      countedTotal,
      // 差异合计仅在全部行已盘时成立（finish 前置条件即全量登记，count.go 状态守卫）；
      // 部分已盘时系统合计含未盘行、实盘合计仅含已盘行，二者不可相减，显示 '-' 不造数
      diffTotal: total > 0 && counted === total ? countedTotal - systemTotal : null,
    }
  }, [items])

  // 五态流转（前端仅按权限码过滤显隐，后端仍做最终校验，permission.md §5）
  const user = useAuthStore((s) => s.user)
  const flowMutation = useMutation({
    mutationFn: ({ action, payload }: { action: CountFlowAction; payload?: { opinion?: string; reason?: string } }) => {
      switch (action) {
        case 'start':
          return countApi.start(countId)
        case 'finish':
          return countApi.finish(countId)
        case 'complete':
          return countApi.complete(countId, payload?.opinion ? { opinion: payload.opinion } : undefined)
        case 'reject':
          return countApi.reject(countId, payload?.opinion ? { opinion: payload.opinion } : undefined)
        case 'cancel':
          return countApi.cancel(countId, payload?.reason ? { reason: payload.reason } : undefined)
      }
    },
    onSuccess: (_data, variables) => {
      messageApi.success(`「${FLOW_ACTION_META[variables.action].label}」操作已提交`)
      setFlowAction(null)
      flowForm.resetFields()
      void queryClient.invalidateQueries({ queryKey: ['counts'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const openFlow = (action: CountFlowAction) => {
    flowMutation.reset()
    flowForm.resetFields()
    setFlowAction(action)
  }

  const submitFlow = () => {
    if (!flowAction) return
    flowForm
      .validateFields()
      .then((values) =>
        flowMutation.mutateAsync({
          action: flowAction,
          payload: { opinion: values.opinion?.trim(), reason: values.opinion?.trim() },
        }),
      )
      .then(() => {
        setFlowAction(null)
      })
      .catch(() => {
        // 表单校验失败 / 提交报错：表单内联提示 + message 已提示
      })
  }

  // 实盘登记：批量幂等 PUT，单项提交 {items:[{InventoryRowID, SerialNo, Qty}]}（store.go:204-208）
  const registerMutation = useMutation({
    mutationFn: (payload: { items: CountRegistrationInput[] }) => countApi.register(countId, payload),
    onSuccess: () => {
      messageApi.success('实盘登记已提交（PUT 幂等：同 行+序列号 覆盖原值）')
      setRegistering(null)
      void queryClient.invalidateQueries({ queryKey: ['counts', countId] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const registeringIsSerial = registering != null && (registering.serial_no ?? '') !== ''

  // 实盘预览 watch（Hooks 无条件顶层调用，且必须先于消费它的派生值声明）
  const watchedQty = Form.useWatch('qty', registerForm)
  const watchedSerialQty = Form.useWatch('serialQty', registerForm)

  // 差异实时预览：登记数量与冻结快照不等时给出调整单链路提示（展示计算，不做业务落库）
  const previewRaw = registeringIsSerial ? watchedSerialQty : watchedQty
  const previewQty =
    previewRaw === undefined || previewRaw === null || previewRaw === '' ? null : Number(previewRaw)
  const previewDiff =
    registering && previewQty !== null && !Number.isNaN(previewQty)
      ? previewQty - registering.qty_system
      : null

  const openRegister = (record: CountItem) => {
    registerMutation.reset()
    setScanValue('')
    setRegistering(record)
    registerForm.resetFields()
    // 重复登记（幂等覆盖）预填当前已登记值；未登记行为空，实盘 0 须显式输入
    if (isCounted(record)) {
      if ((record.serial_no ?? '') !== '') {
        registerForm.setFieldValue('serialQty', String(record.qty_counted))
      } else {
        registerForm.setFieldValue('qty', record.qty_counted as number)
      }
    }
  }

  const handleRegister = () => {
    if (!registering) return
    registerForm
      .validateFields()
      .then((values) => {
        const qty = registeringIsSerial ? Number(values.serialQty) : values.qty
        if (typeof qty !== 'number' || Number.isNaN(qty) || qty < 0) return
        registerMutation.mutate({
          items: [
            {
              InventoryRowID: Number(registering.inventory_row_id),
              SerialNo: registering.serial_no,
              Qty: qty,
            },
          ],
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  /** 扫码录入（frontend.md §10.5 连续扫码）：序列号行匹配本行序列号置「在库」；
   * 汇总行匹配 SKU 编码 / 条码则数量 +1；未命中如实提示，不虚构业务结果 */
  const handleScan = () => {
    const value = scanValue.trim()
    if (!value || !registering) return
    const sku = maps.skuMap.get(String(registering.sku_id))
    if (registeringIsSerial) {
      if (value === registering.serial_no) {
        registerForm.setFieldValue('serialQty', '1')
        messageApi.success(`序列号 ${value} 已登记为「在库」`)
        setScanValue('')
      } else {
        messageApi.warning('扫描序列号与该明细行不匹配：序列号明细行只能登记本行序列号')
      }
      return
    }
    const hit = value === sku?.code || (sku?.barcodes ?? []).some((b) => b.barcode === value)
    if (hit) {
      const current = Number(registerForm.getFieldValue('qty')) || 0
      registerForm.setFieldValue('qty', current + 1)
      messageApi.success(`已扫描 1 件，当前录入 ${current + 1}`)
      setScanValue('')
    } else {
      messageApi.warning('未匹配到该行 SKU 的条码 / 编码；如为盘盈序列号件请经 Pad / Scan 端逐件登记')
    }
  }

  const handleRefresh = () => {
    void detailQuery.refetch()
  }

  // Hooks 约束：全部在条件渲染之前调用（预览 watch 已上移至消费点之前；本地展示分页）
  const itemCounts = useLocalPaged(items)
  const diffCounts = useLocalPaged(differences)

  if (countId === '') {
    return (
      <div className="sf-page">
        <SfPageHeader title="盘点详情" onBack={() => navigate('/counts')} />
        <SfError error={new Error('缺少盘点单 ID')} description="请从盘点中心列表进入详情页" />
      </div>
    )
  }

  const allowedActions = order ? (STATUS_ACTIONS[order.status] ?? []) : []

  const renderBody = () => {
    if (detailQuery.isPending) {
      return <SfLoading rows={6} />
    }
    if (detailQuery.isError || !order) {
      return (
        <SfError
          error={detailQuery.error ?? new Error('盘点详情加载失败')}
          onRetry={detailQuery.refetch}
          description={`盘点详情不可用（GET /api/counts/${countId}），请稍后重试或联系管理员`}
        />
      )
    }

    const cancelled = order.status === 'CANCELLED'
    const counting = order.status === 'COUNTING'
    const canRegister = counting && canAccess(user, COUNT_EXECUTE_PERMISSION)
    const stepIndex = COUNT_STEPS.findIndex((step) => step.key === order.status)
    const finishDone =
      order.status === 'PENDING_REVIEW' || order.status === 'COMPLETED' || order.reviewed_at != null
    const showDiffSection =
      differences.length > 0 ||
      order.status === 'PENDING_REVIEW' ||
      order.status === 'COMPLETED' ||
      cancelled

    const itemColumns: ColumnsType<CountItem> = [
      {
        title: '库存行',
        dataIndex: 'inventory_row_id',
        width: 90,
        render: (v: CountId) => (
          <Text type="secondary" className="sf-num">
            #{String(v)}
          </Text>
        ),
      },
      {
        title: 'SKU 编码',
        key: 'sku_code',
        width: 130,
        fixed: 'left',
        render: (_: unknown, record: CountItem) => {
          const sku = maps.skuMap.get(String(record.sku_id))
          const label = sku?.code ?? `#${String(record.sku_id)}`
          const tooltip = sku?.product_name ? `${label} ${sku.product_name}` : label
          return (
            <Text style={{ maxWidth: 130 }} ellipsis={{ tooltip }}>
              {label}
            </Text>
          )
        },
      },
      {
        title: '库位',
        key: 'bin_code',
        width: 110,
        render: (_: unknown, record: CountItem) => labelOrId(maps.binMap, record.bin_id),
      },
      {
        title: '系统数量',
        dataIndex: 'qty_system',
        width: 100,
        align: 'right',
        render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '实盘数量',
        dataIndex: 'qty_counted',
        width: 100,
        align: 'right',
        render: (v?: number | null) =>
          v === null || v === undefined ? '-' : <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '差异',
        key: 'diff',
        width: 90,
        align: 'right',
        render: (_: unknown, record: CountItem) => {
          if (!isCounted(record)) return '-'
          const diff = (record.qty_counted as number) - record.qty_system
          if (diff === 0) return <span className="sf-num">{formatNumber(diff)}</span>
          return (
            <Text type="warning" className="sf-num">
              {signedNumber(diff)}
            </Text>
          )
        },
      },
      {
        title: '序列号',
        dataIndex: 'serial_no',
        width: 150,
        ellipsis: true,
        render: (v?: string) => (v ? <Text copyable={{ text: v }}>{v}</Text> : '-'),
      },
      {
        title: '状态',
        key: 'line_status',
        width: 80,
        // 明细行盘/未盘为前端推导展示态（qty_counted 是否为 null），显式 label + semantic、
        // 不传 status：pending 注册表键为通用「待处理」文案，status 与 label 同传时
        // SfStatusTag meta?.label 优先级更高会覆盖「待盘」盘点口径
        render: (_: unknown, record: CountItem) =>
          isCounted(record) ? (
            <SfStatusTag label="已盘" semantic="success" />
          ) : (
            <SfStatusTag label="待盘" semantic="pending" />
          ),
      },
      {
        title: '实盘时间',
        dataIndex: 'counted_at',
        width: 160,
        render: (v?: string | null) => <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(v)}</span>,
      },
      {
        title: '操作',
        key: 'actions',
        fixed: 'right',
        width: 100,
        render: (_: unknown, record: CountItem) =>
          canRegister ? (
            <Tooltip title="PUT 幂等：重复登记覆盖原值；实盘 0 须显式登记">
              <Button type="link" size="small" onClick={() => openRegister(record)}>
                实盘登记
              </Button>
            </Tooltip>
          ) : (
            <Text type="secondary">-</Text>
          ),
      },
    ]

    const diffColumns: ColumnsType<CountDifference> = [
      { title: '行号', dataIndex: 'line_no', width: 70, render: (v: number) => <span className="sf-num">{v}</span> },
      {
        title: 'SKU 编码',
        key: 'sku_code',
        width: 130,
        render: (_: unknown, record: CountDifference) => {
          const sku = maps.skuMap.get(String(record.sku_id))
          const label = sku?.code ?? `#${String(record.sku_id)}`
          const tooltip = sku?.product_name ? `${label} ${sku.product_name}` : label
          return (
            <Text style={{ maxWidth: 130 }} ellipsis={{ tooltip }}>
              {label}
            </Text>
          )
        },
      },
      {
        title: '库位',
        key: 'bin_code',
        width: 110,
        render: (_: unknown, record: CountDifference) => labelOrId(maps.binMap, record.bin_id),
      },
      {
        title: '系统数量',
        dataIndex: 'qty_system',
        width: 100,
        align: 'right',
        render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '实盘数量',
        dataIndex: 'qty_counted',
        width: 100,
        align: 'right',
        render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
      },
      {
        title: '差异（盘盈为正）',
        dataIndex: 'diff_qty',
        width: 130,
        align: 'right',
        render: (v: number) =>
          v === 0 ? (
            <span className="sf-num">{formatNumber(v)}</span>
          ) : (
            <Text type="warning" className="sf-num">
              {signedNumber(v)}
            </Text>
          ),
      },
      {
        title: '调整单号',
        dataIndex: 'adjust_no',
        width: 170,
        render: (v?: string) => v || '-',
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 90,
        render: (v: CountDiffStatus) => <DiffStatusTag status={v} />,
      },
      {
        title: '备注',
        dataIndex: 'remark',
        width: 160,
        ellipsis: true,
        render: (v?: string) => (v ? <Text style={{ maxWidth: 160 }} ellipsis={{ tooltip: v }}>{v}</Text> : '-'),
      },
    ]

    const timelineItems = [
      flowItem('创建盘点', { time: order.created_at }),
      flowItem('开始盘点（冻结范围）', {
        time: order.frozen_at,
        description: '冻结范围内库存行（COUNT_FREEZE），实盘期间相关库存锁定',
      }),
      flowItem('完成实盘（系统对比生成差异）', {
        done: finishDone,
        description: '全部明细登记完成后提交；实盘为 0 也须显式登记（inventory-rules.md §9）',
      }),
      flowItem('差异审核', {
        time: order.reviewed_at,
        description: '审核通过后由系统生成库存调整单并走审批链路；驳回则差异落「已驳回」并解冻',
      }),
      flowItem('盘点完成', { time: order.completed_at }),
      ...(order.cancelled_at != null
        ? [flowItem('盘点取消', { time: order.cancelled_at, description: '盘点流程终止，已冻结范围解冻' })]
        : []),
    ]

    return (
      <>
        {/* 硬性规则明示（business-flow.md §10.2）：页面不提供任何直接修改系统库存的入口 */}
        <Alert
          type="warning"
          showIcon
          message="盘点差异禁止直接修改系统库存"
          description="差异经审核通过后由系统生成库存调整单并走审批链路落库（business-flow.md §10.2 硬性规则）"
          style={{ marginBottom: 16 }}
        />
        <SfSummaryBar
          items={[
            { label: '明细行', value: formatNumber(summary.total) },
            { label: '已盘', value: formatNumber(summary.counted) },
            { label: '完成率（已盘/总行）', value: formatPercent(summary.rate) },
            { label: '系统数量合计', value: formatNumber(summary.systemTotal) },
            { label: '实盘合计（已盘行）', value: summary.counted > 0 ? formatNumber(summary.countedTotal) : '-' },
            {
              label: '差异合计（实盘-系统）',
              value: summary.diffTotal === null ? '-' : signedNumber(summary.diffTotal),
            },
          ]}
        />
        <Card size="small" style={{ marginTop: 16 }}>
          <Steps
            size="small"
            current={cancelled || stepIndex < 0 ? 0 : stepIndex}
            status={cancelled ? 'error' : undefined}
            items={COUNT_STEPS.map((step) => ({ title: step.title }))}
          />
          {cancelled && (
            <Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
              该盘点单已取消，状态机流程终止
            </Text>
          )}
        </Card>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="盘点范围">
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="范围模式">
                {COUNT_SCOPE_MODE_LABEL[order.scope.mode] ?? order.scope.mode}
              </Descriptions.Item>
              <Descriptions.Item label="范围明细">{scopeText(order.scope, maps)}</Descriptions.Item>
              <Descriptions.Item label="范围冻结" span={2}>
                <Text type="secondary">
                  {order.frozen_at != null
                    ? `已冻结（${formatDateTime(order.frozen_at)}）：范围内库存行 COUNT_FREEZE，实盘期间锁定`
                    : '未冻结：开始盘点时按冻结快照生成盘点明细'}
                </Text>
              </Descriptions.Item>
            </Descriptions>
          </SfDetailSection>
        </div>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="基础信息">
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="盘点单号">{order.count_no}</Descriptions.Item>
              <Descriptions.Item label="仓库">{labelOrId(maps.warehouseMap, order.warehouse_id)}</Descriptions.Item>
              <Descriptions.Item label="创建时间">{formatDateTime(order.created_at)}</Descriptions.Item>
              <Descriptions.Item label="冻结时间">
                {order.frozen_at != null ? formatDateTime(order.frozen_at) : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="审核时间">
                {order.reviewed_at != null ? formatDateTime(order.reviewed_at) : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="完成时间">
                {order.completed_at != null ? formatDateTime(order.completed_at) : '-'}
              </Descriptions.Item>
              {order.cancelled_at != null && (
                <Descriptions.Item label="取消时间">{formatDateTime(order.cancelled_at)}</Descriptions.Item>
              )}
              <Descriptions.Item label="备注" span={order.cancelled_at != null ? 1 : 2}>
                {order.remark || '-'}
              </Descriptions.Item>
            </Descriptions>
          </SfDetailSection>
        </div>
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="盘点明细">
            <SfTable<CountItem>
              storageKey="count-detail-items"
              rowKey="id"
              columns={itemColumns}
              dataSource={itemCounts.paged}
              loading={detailQuery.isFetching}
              pagination={itemCounts.pagination}
              total={items.length}
              onPageChange={itemCounts.onPageChange}
              emptyText={
                order.status === 'DRAFT'
                  ? '盘点明细在「开始盘点」时按冻结快照生成'
                  : '该盘点单暂无明细行'
              }
              scrollX={1180}
              showFullscreen={false}
            />
          </SfDetailSection>
        </div>
        {showDiffSection && (
          <div style={{ marginTop: 16 }}>
            <SfDetailSection title="差异表（完成实盘后由系统对比生成）">
              <SfTable<CountDifference>
                storageKey="count-detail-diffs"
                rowKey="id"
                columns={diffColumns}
                dataSource={diffCounts.paged}
                pagination={diffCounts.pagination}
                total={differences.length}
                onPageChange={diffCounts.onPageChange}
                emptyText="无差异行：实盘与冻结快照一致，或尚未完成实盘"
                scrollX={1180}
                showFullscreen={false}
              />
            </SfDetailSection>
          </div>
        )}
        <div style={{ marginTop: 16 }}>
          <SfDetailSection title="业务流程">
            <Timeline items={timelineItems} />
          </SfDetailSection>
        </div>
      </>
    )
  }

  const flowMeta = flowAction ? FLOW_ACTION_META[flowAction] : null

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title={order?.count_no ?? '盘点详情'}
        subtitle={order ? <CountStatusTag status={order.status} /> : undefined}
        onBack={() => navigate('/counts')}
        extra={
          <>
            {allowedActions
              .filter((action) => canAccess(user, FLOW_ACTION_META[action].permission))
              .map((action) => (
                <Button
                  key={action}
                  type={FLOW_ACTION_META[action].danger ? 'default' : 'primary'}
                  danger={FLOW_ACTION_META[action].danger}
                  onClick={() => openFlow(action)}
                >
                  {FLOW_ACTION_META[action].label}
                </Button>
              ))}
            <Button icon={<ReloadOutlined />} onClick={handleRefresh}>
              刷新
            </Button>
          </>
        }
      />
      {renderBody()}

      {/* 流转确认（业务操作确认而非危险操作提示，保留 Modal 形态；danger 动作按钮/确认键 danger） */}
      <Modal
        title={flowMeta ? `确认「${flowMeta.label}」？` : '确认操作'}
        open={flowAction !== null}
        width={520}
        confirmLoading={flowMutation.isPending}
        okText={flowMeta?.label ?? '确认'}
        cancelText="再想想"
        okButtonProps={flowMeta?.danger ? { danger: true } : undefined}
        onOk={submitFlow}
        onCancel={() => setFlowAction(null)}
      >
        {flowMeta && (
          <>
            <Alert type={flowMeta.danger ? 'warning' : 'info'} showIcon message={flowMeta.confirm} style={{ marginBottom: 16 }} />
            {flowMeta.inputLabel && (
              <Form<FlowFormValues> form={flowForm} layout="vertical">
                <Form.Item name="opinion" label={flowMeta.inputLabel} extra={flowMeta.placeholder}>
                  <Input.TextArea rows={3} maxLength={500} showCount placeholder={flowMeta.placeholder} />
                </Form.Item>
              </Form>
            )}
          </>
        )}
      </Modal>

      {/* 实盘登记（批量幂等 PUT 的单项提交）；差异原因/备注不入登记载荷——后端
          CountRegistration 仅 InventoryRowID/SerialNo/Qty，差异说明随审核意见提交 */}
      <Modal
        title={registering ? `实盘登记：${maps.skuMap.get(String(registering.sku_id))?.code ?? `#${String(registering.sku_id)}`}` : '实盘登记'}
        open={registering !== null}
        width={560}
        confirmLoading={registerMutation.isPending}
        okText="提交实盘"
        onOk={handleRegister}
        onCancel={() => setRegistering(null)}
      >
        {registering && (
          <>
            {registerMutation.isError && (
              <Alert
                type="error"
                showIcon
                message={resolveErrorMessage(registerMutation.error)}
                style={{ marginBottom: 16 }}
              />
            )}
            <Alert
              type="info"
              showIcon
              message="差异原因 / 备注不随实盘登记提交"
              description="登记载荷仅含 库存行 / 序列号 / 数量（internal/stockops/store.go:204-208）；差异说明请在「待复核」阶段随审核意见提交"
              style={{ marginBottom: 16 }}
            />
            <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="SKU">
                {maps.skuMap.get(String(registering.sku_id))?.code ?? `#${String(registering.sku_id)}`}
              </Descriptions.Item>
              <Descriptions.Item label="库位">{labelOrId(maps.binMap, registering.bin_id)}</Descriptions.Item>
              <Descriptions.Item label="序列号">{registering.serial_no || '-'}</Descriptions.Item>
              <Descriptions.Item label="系统数量">
                <span className="sf-num">{formatNumber(registering.qty_system)}</span>
              </Descriptions.Item>
            </Descriptions>
            <Input
              value={scanValue}
              onChange={(e) => setScanValue(e.target.value)}
              onPressEnter={handleScan}
              prefix={<ScanOutlined style={{ color: 'var(--sf-text-muted)' }} />}
              placeholder={
                registeringIsSerial
                  ? '扫描本行序列号确认「在库」'
                  : '扫码录入：扫描该 SKU 条码 / 编码，每扫一件数量 +1'
              }
              allowClear
              style={{ marginBottom: 16 }}
            />
            <Form<RegisterFormValues> form={registerForm} layout="vertical">
              {registeringIsSerial ? (
                <Form.Item
                  name="serialQty"
                  label="实盘结果"
                  rules={[{ required: true, message: '请选择该序列号的实盘结果' }]}
                  extra="序列号明细行逐件登记，数量仅允许 0（缺失）/ 1（在库）（inventory-rules.md §8.2）"
                >
                  <Radio.Group>
                    <Radio value="1">在库（数量 1）</Radio>
                    <Radio value="0">缺失（数量 0）</Radio>
                  </Radio.Group>
                </Form.Item>
              ) : (
                <Form.Item
                  name="qty"
                  label="实盘数量"
                  rules={[
                    { required: true, message: '请输入实盘数量' },
                    { type: 'number', min: 0, message: '实盘数量不能为负数' },
                  ]}
                  extra="登记为 0 必须显式提交（inventory-rules.md §9）；PUT 幂等，重复登记覆盖原值"
                >
                  <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="请输入实际清点数量" />
                </Form.Item>
              )}
            </Form>
            {previewDiff !== null && previewDiff !== 0 && (
              <Alert
                type="warning"
                showIcon
                message={`与系统数量存在差异（${signedNumber(previewDiff)}）`}
                description="差异不可直接修改系统库存，审核通过后生成库存调整单并走审批链路；差异说明随审核意见提交"
              />
            )}
          </>
        )}
      </Modal>
    </div>
  )
}
