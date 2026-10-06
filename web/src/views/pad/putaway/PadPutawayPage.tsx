import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, Input, Modal, Select, Steps, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { resolveErrorMessage } from '@/api/client'
import {
  PUTAWAY_CLAIM_PERMISSION,
  PUTAWAY_EXECUTE_PERMISSION,
  putawayApi,
  type PutawayExecutePayload,
  type PutawayFromState,
  type PutawayTask,
  type PutawayTaskId,
  type PutawayTaskQuery,
  type PutawayTaskStatus,
} from '@/api/putaway'
import {
  buildIdItemMap,
  buildSkuMaps,
  buildUserNameMap,
  fetchBinOptions,
  fetchSkuOptions,
  fetchUserOptions,
} from '@/api/options'
import type { BinItem } from '@/api/warehouse'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import type { StatusSemantic } from '@/types/status'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  usePadOrientation,
} from '@/layouts/pad'
import { ScanInput } from '@/components/scanner/ScanInput'
import { usePagedList } from '@/hooks/usePagedList'
import { useNextTask } from '@/hooks/useNextTask'
import { SfCompleteNextButton } from '@/components/task/SfCompleteNextButton'
import { TASK_PRIORITY_OPTIONS } from '@/api/task'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'

// ---------- 上架任务契约已收敛至共享层 web/src/api/putaway.ts（AGENTS.md 规则 6 API 层统一） ----------

/**
 * 上架任务状态 → SfStatusTag 文案/语义（frontend.md §24：颜色统一经 SfStatusTag）。
 * 后端大写枚举 PENDING/IN_PROGRESS/COMPLETED/CANCELLED 未注册于 types/status.ts
 * （不在本单元文件清单内），与 api/quality.ts / PadReceivePage 同口径以 label + semantic
 * 显式指定，文案对齐注册表既有词条（待处理/上架中/已取消；putaway_in_progress=上架中）。
 */
const PUTAWAY_STATUS_TAG_META: Record<PutawayTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待领取', semantic: 'pending' },
  IN_PROGRESS: { label: '上架中', semantic: 'processing' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

function PutawayStatusTag({ status }: { status: PutawayTaskStatus }) {
  const meta = PUTAWAY_STATUS_TAG_META[status]
  return <SfStatusTag label={meta?.label ?? status} semantic={meta?.semantic ?? 'neutral'} />
}

/** 状态 chip（默认筛「待领取」= 可作业任务源；business-flow.md §5.1 待上架→上架中→已完成） */
const STATUS_OPTIONS: Array<{ label: string; value: PutawayTaskStatus }> = (
  ['PENDING', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED'] as const
).map((value) => ({ value, label: PUTAWAY_STATUS_TAG_META[value].label }))

/** from_state 文案（models.go:60-63 注释：免检直通 / 经检待检） */
const FROM_STATE_LABEL: Record<PutawayFromState, string> = {
  available: '免检直通',
  pending_inspect: '经检待检',
}

/** 三步序列（frontend.md §21.4 上架：扫商品 → 扫库位 → 系统校验 → 完成） */
const STEP_TITLES = [{ title: '扫商品' }, { title: '扫库位' }, { title: '系统校验 · 完成' }]

/** 上架任务卡：任务源 = GET /api/putaway（PENDING 起作业），字段 putawayNo/来源单/库位/数量/状态，
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9） */
function PutawayTaskCard({
  task,
  targetBinCode,
  selected = false,
  onClick,
}: {
  task: PutawayTask
  targetBinCode: string
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
        <span className="sf-pad-task-card__no">{task.putaway_no}</span>
        <PutawayStatusTag status={task.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{task.inbound_no}</span>
        <span>{FROM_STATE_LABEL[task.from_state] ?? task.from_state}</span>
        <span>位 {targetBinCode}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(task.qty)}</span>
        <span className="sf-pad-task-card__qty-unit">
          {task.serial_no ? `序列号 ${task.serial_no}` : '应上架数量'}
        </span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>{task.claimed_at ? `领取 ${formatDateTime(task.claimed_at)}` : formatDateTime(task.created_at)}</span>
        <span>{task.completed_at ? `完成 ${formatDateTime(task.completed_at)}` : EMPTY_TEXT}</span>
      </div>
    </div>
  )
}

/**
 * 守卫动作按钮（死按钮门禁口径，frontend.md §9.1）：disabled 时外包 span 接管点按，
 * 把原因以 Toast 送达；可执行时为真实提交按钮（不再是只弹提示的占位）。
 */
function GuardedAction({
  label,
  icon,
  variant = 'primary',
  disabledReason,
  loading = false,
  onClick,
}: {
  label: string
  icon?: ReactNode
  variant?: 'primary' | 'default'
  disabledReason?: string
  loading?: boolean
  onClick?: () => void
}) {
  const [messageApi, contextHolder] = message.useMessage()
  if (!disabledReason) {
    return (
      <Button
        block
        size="large"
        type={variant}
        icon={icon}
        loading={loading}
        onClick={onClick}
        style={{ minHeight: 'var(--sf-pad-card-min-height)' }}
      >
        {label}
      </Button>
    )
  }
  return (
    <>
      {contextHolder}
      <span style={{ display: 'block' }} onClick={() => messageApi.warning(disabledReason)}>
        <Button
          block
          size="large"
          type={variant}
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
 * Pad 上架页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §5 / §21.4 上架要点）：
 * - 左栏：上架任务卡列表——任务源 GET /api/putaway（PENDING 领取 → IN_PROGRESS 作业 →
 *   COMPLETED 落账，purchase.go:71-75 已交付），默认筛「待领取」；
 * - 中栏：选中任务 PadInfoCard（SKU 编码经基础资料 options 映射 / 应上架数量 / 目标库位
 *   编码经 binApi options 映射，映射失败降级为 ID，不造假数据）；
 * - 右栏：三步序列操作区（步骤指示 Steps + ScanInput 快速模式扫码（HID 承接 + 手输兜底，
 *   frontend.md §22 一期示范；autoAdvance='safe'——三步比对为定位类低风险动作，连扫自动
 *   步进）+ 本地比对校验），
 *   [领取任务] 接线 POST /api/putaway/{id}/claim，[完成上架] 接线
 *   POST /api/putaway/{id}/execute {bin_id?, remark}（IN_PROGRESS→COMPLETED 触发落账）；
 * - 竖屏：顶部当前上架任务卡 → 商品信息 / 步骤指示 → 任务列表滚动区 → 底部 PadActionBar。
 * 上架页面不做 CRUD 表格（frontend.md §30.1）；完整 ScannerManager→Event 总线归 F15/F16。
 */
export default function PadPutawayPage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.user)
  const canClaim = canAccess(user, PUTAWAY_CLAIM_PERMISSION)
  const canExecute = canAccess(user, PUTAWAY_EXECUTE_PERMISSION)

  const [statusFilter, setStatusFilter] = useState<PutawayTaskStatus | 'all'>('PENDING')
  const [selected, setSelected] = useState<PutawayTask | null>(null)
  const [step, setStep] = useState(0)
  const [scannedBinId, setScannedBinId] = useState<number | null>(null)
  const [remark, setRemark] = useState('')
  const [scanOpen, setScanOpen] = useState(false)

  const params = useMemo<PutawayTaskQuery>(
    () => ({ status: statusFilter === 'all' ? undefined : statusFilter }),
    [statusFilter],
  )

  const list = usePagedList<PutawayTask, PutawayTaskQuery>({
    queryKey: ['pad', 'putaway', 'tasks'],
    fetch: (q) => putawayApi.list(q),
    params,
    defaultPageSize: 30,
  })

  // 任务详情：GET /api/putaway/{id}，选中任务后拉取（claim/execute 后随失效刷新）
  const detailQuery = useQuery({
    queryKey: ['pad', 'putaway', 'task', String(selected?.id ?? '')],
    queryFn: () => {
      if (!selected) throw new Error('未选择上架任务')
      return putawayApi.detail(selected.id)
    },
    enabled: selected != null,
  })
  const task = detailQuery.data ?? null

  // SKU / 库位 / 用户映射（基础资料 options 一次取全；失败降级为 ID 显示，不阻塞任务列表）
  const skuOptions = useQuery({
    queryKey: ['pad', 'putaway', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const binOptions = useQuery({
    queryKey: ['pad', 'putaway', 'bin-options'],
    queryFn: fetchBinOptions,
  })
  const userOptions = useQuery({
    queryKey: ['pad', 'putaway', 'user-options'],
    queryFn: fetchUserOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const binItemMap = useMemo(
    () => buildIdItemMap<BinItem>(binOptions.data ?? [], (bin) => bin.id),
    [binOptions.data],
  )
  /** 库位编码 → 整行（三步序列第 2 步 code→id 解析；BinView 携带 warehouse/zone/shelf 锚点） */
  const binByCode = useMemo(() => {
    const map = new Map<string, BinItem>()
    for (const bin of binOptions.data ?? []) map.set(bin.code, bin)
    return map
  }, [binOptions.data])
  const userNameMap = useMemo(() => buildUserNameMap(userOptions.data ?? []), [userOptions.data])

  const expectedSkuCode = task ? (skuMaps.code.get(String(task.sku_id)) ?? EMPTY_TEXT) : EMPTY_TEXT
  const targetBin = task && String(task.target_bin_id) !== '0' ? binItemMap.get(String(task.target_bin_id)) : undefined
  const targetBinCode = targetBin?.code ?? (task && String(task.target_bin_id) !== '0' ? `#${String(task.target_bin_id)}` : EMPTY_TEXT)

  // 切换任务时重置三步序列与已确认库位
  useEffect(() => {
    setStep(0)
    setScannedBinId(null)
    setRemark('')
  }, [selected?.id])

  const handleSelect = (item: PutawayTask) => {
    setSelected(item)
    setStep(0)
    setScannedBinId(null)
    setRemark('')
  }

  const handleFilter = (value: PutawayTaskStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  // 领取任务：POST /api/putaway/{id}/claim（PENDING→IN_PROGRESS 原子抢占，并发冲突由后端裁决）
  const claimMutation = useMutation({
    mutationFn: (id: PutawayTaskId) => putawayApi.claim(id),
    onSuccess: (claimed) => {
      messageApi.success(`已领取任务 ${claimed.putaway_no}，请按三步序列执行上架`)
      void queryClient.invalidateQueries({ queryKey: ['pad', 'putaway'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 完成上架：POST /api/putaway/{id}/execute {bin_id?, remark}（IN_PROGRESS→COMPLETED 触发 Putaway 落账）
  const executeMutation = useMutation({
    mutationFn: ({ id, payload }: { id: PutawayTaskId; payload: PutawayExecutePayload }) =>
      putawayApi.execute(id, payload),
    onSuccess: (completed) => {
      const confirmedBinCode = scannedBinId != null ? (binItemMap.get(String(scannedBinId))?.code ?? targetBinCode) : targetBinCode
      messageApi.success(`✓ 上架完成：${completed.putaway_no} → ${confirmedBinCode}`)
      void queryClient.invalidateQueries({ queryKey: ['pad', 'putaway'] })
      setStep(0)
      setScannedBinId(null)
      setRemark('')
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  /** 扫码手输兜底的本地比对（§21.4 序列；真实扫码属 Scan 端 / F16，上架执行以后端强校验为准，
   * 错误按 §21.7 给出具体原因） */
  const handleScan = (code: string) => {
    if (!task) return
    const value = code.trim()
    if (step === 0) {
      if (String(task.sku_id) === '0') {
        messageApi.error('任务缺少 SKU 定位（sku_id=0），数据异常不可作业')
        return
      }
      if (!skuMaps.code.has(String(task.sku_id))) {
        messageApi.error(`SKU 编码映射缺失（sku_id ${String(task.sku_id)}），基础资料未加载无法本地比对`)
        return
      }
      if (value === expectedSkuCode) {
        setStep(1)
        messageApi.success(`✓ ${expectedSkuCode} 商品匹配，请扫目标库位`)
      } else {
        messageApi.error(`商品不匹配：当前任务 ${expectedSkuCode}，扫描结果 ${value}`)
      }
      return
    }
    if (step === 1) {
      const bin = binByCode.get(value)
      if (!bin) {
        messageApi.error(`未匹配到库位编码 ${value}，请核对后重扫`)
        return
      }
      if (String(task.target_bin_id) !== '0' && String(bin.id) !== String(task.target_bin_id)) {
        messageApi.error(`库位不匹配：目标库位 ${targetBinCode}，扫描结果 ${value}`)
        return
      }
      // 扫码库位=库位改指定（service_putaway.go:239-246）：任务无目标库位时以扫码库位提交
      setScannedBinId(Number(bin.id))
      setStep(2)
      messageApi.success(`✓ 校验通过：${expectedSkuCode} → ${bin.code}`)
      return
    }
    messageApi.info('三步校验已完成：点击 [完成上架] 提交执行')
  }

  const handleScanSubmit = (code: string) => {
    handleScan(code)
    setScanOpen(false)
  }

  const claimDisabledReason = (() => {
    if (!task || task.status !== 'PENDING') return undefined
    if (!canClaim) return `缺少 ${PUTAWAY_CLAIM_PERMISSION} 权限，无法领取任务`
    return undefined
  })()

  const executeDisabledReason = (() => {
    if (!task) return '先从左侧任务列表选择一张上架任务卡'
    if (task.status === 'PENDING') return '任务尚未领取：请先 [领取任务]（PENDING→IN_PROGRESS）'
    if (task.status === 'COMPLETED') return '该任务已完成上架'
    if (task.status === 'CANCELLED') return '该任务已取消（随入库单联动取消）'
    if (!canExecute) return `缺少 ${PUTAWAY_EXECUTE_PERMISSION} 权限，无法确认上架`
    if (step < 2) return '先完成三步校验：扫商品 → 扫库位 → 系统校验'
    return undefined
  })()

  /**
   * 下一条上架任务（§2.4 / api.md §9，验收场景 3）：putaway 分支候选池 =
   * (本人已领取且进行中) ∪ (PENDING 未领取)；排序 本人进行中 > priority > 超时 >
   * created_at（全部由后端 SQL 承担，前端零推算）；current_task_id 恒排除当前任务。
   */
  const nextTask = useNextTask({
    task_type: 'putaway',
    current_task_id: task?.id,
    enabled: !!task,
  })

  /** 优先级设置权限（效率层一期 B3：purchase:putaway:assign——后端同码校验） */
  const canAssign = canAccess(user, 'purchase:putaway:assign')
  /** 行内设置任务优先级（§2.4：/api/tasks/next putaway 分支排序层的数据来源） */
  const priorityMutation = useMutation({
    mutationFn: ({ id, priority }: { id: PutawayTaskId; priority: number }) =>
      putawayApi.setPriority(id, priority),
    onSuccess: (res) => {
      messageApi.success(`优先级已更新为 ${res.priority}`)
      void detailQuery.refetch()
      void list.refetch()
    },
    onError: (e) => messageApi.error(resolveErrorMessage(e)),
  })

  const handleExecute = async (): Promise<unknown> => {
    if (!task || executeDisabledReason) return undefined
    return executeMutation.mutateAsync({
      id: task.id,
      // bin_id 仅在扫码确认库位后携带（缺省=任务目标库位，service_putaway.go:26）
      payload: { bin_id: scannedBinId ?? undefined, remark: remark.trim() || undefined },
    })
  }

  const handleFinishAction = () => {
    // 底部 [完成] 与右栏 [完成上架] 同一动作；不可执行时 PadActionBar 以 disabledReason 提示
    if (executeDisabledReason) {
      messageApi.warning(executeDisabledReason)
      return
    }
    handleExecute()
  }

  const infoNode = !selected ? (
    <SfEmpty description="从上架任务列表选择一张任务卡，此处展示 SKU / 应上架数量 / 目标库位" />
  ) : detailQuery.isPending ? (
    <SfLoading rows={4} />
  ) : detailQuery.error ? (
    <SfError
      error={detailQuery.error}
      description="上架任务详情（GET /api/putaway/{id}）加载失败"
      onRetry={() => void detailQuery.refetch()}
    />
  ) : task ? (
    <>
      <PadInfoCard
        title={`上架任务 · ${task.putaway_no}`}
        items={[
          { label: '上架单号', value: task.putaway_no },
          { label: '状态', value: <PutawayStatusTag status={task.status} /> },
          {
            label: '优先级',
            value: canAssign ? (
              <Select
                size="small"
                value={task.priority ?? 0}
                style={{ width: 72 }}
                options={TASK_PRIORITY_OPTIONS}
                disabled={task.status === 'COMPLETED' || task.status === 'CANCELLED'}
                onChange={(next) => priorityMutation.mutate({ id: task.id, priority: next })}
              />
            ) : (
              String(task.priority ?? 0)
            ),
          },
          { label: '来源入库单', value: task.inbound_no || EMPTY_TEXT },
          { label: '来源收货单', value: task.receipt_no || EMPTY_TEXT },
          { label: 'SKU 编码', value: String(task.sku_id) === '0' ? EMPTY_TEXT : skuMaps.code.get(String(task.sku_id)) ?? `#${String(task.sku_id)}` },
          { label: '商品名称', value: String(task.sku_id) === '0' ? EMPTY_TEXT : skuMaps.name.get(String(task.sku_id)) ?? EMPTY_TEXT },
          {
            label: '批次',
            value: String(task.batch_id) === '0' ? '非批次' : `#${String(task.batch_id)}`,
          },
          { label: '序列号', value: task.serial_no || EMPTY_TEXT },
          { label: '库存状态', value: FROM_STATE_LABEL[task.from_state] ?? task.from_state },
          {
            label: '应上架数量',
            value: formatNumber(task.qty),
            emphasis: true,
          },
          { label: '目标库位', value: targetBinCode },
          { label: '领取人', value: task.claimed_by ? (userNameMap.get(String(task.claimed_by)) ?? `#${String(task.claimed_by)}`) : EMPTY_TEXT },
          { label: '领取时间', value: formatDateTime(task.claimed_at) },
          { label: '完成时间', value: formatDateTime(task.completed_at) },
        ]}
      />
      {task.remark && <p className="sf-pad-muted-note">任务备注：{task.remark}</p>}
      {skuOptions.error && (
        <p className="sf-pad-muted-note">
          SKU 基础资料加载失败：编码/名称以 ID 降级展示，扫码比对不可用，可点击任务卡重试
        </p>
      )}
      {binOptions.error && (
        <p className="sf-pad-muted-note">库位基础资料加载失败：目标库位以 ID 降级展示，库位扫码比对不可用</p>
      )}
    </>
  ) : null

  const stepsNode = (
    <section className="sf-pad-card" aria-label="上架三步序列">
      <h3 className="sf-pad-card-title">上架三步（扫商品 → 扫库位 → 系统校验）</h3>
      {!selected || !task ? (
        <SfEmpty
          description={
            selected ? '上架任务加载后开始三步序列' : '选择任务卡后按 领取任务 → 扫商品 → 扫库位 → 完成上架 执行'
          }
        />
      ) : (
        <>
          {task.status === 'PENDING' && (
            <>
              <p className="sf-pad-muted-note">
                任务待领取：领取为原子抢占（PENDING→IN_PROGRESS），他人已领取时后端返回冲突原因
              </p>
              <GuardedAction
                label={claimMutation.isPending ? '领取中…' : '领取任务'}
                icon={<CheckOutlined />}
                variant="primary"
                disabledReason={claimDisabledReason}
                loading={claimMutation.isPending}
                onClick={() => claimMutation.mutate(task.id)}
              />
            </>
          )}
          {task.status === 'CANCELLED' && (
            <p className="sf-pad-muted-note">该任务已取消（business-flow.md §5.1 随入库单联动取消），不可作业</p>
          )}
          {task.status === 'COMPLETED' && (
            <p className="sf-pad-muted-note">该任务已完成上架（IN_PROGRESS→COMPLETED 已触发库存落账），无需重复作业</p>
          )}
          {task.status === 'IN_PROGRESS' && (
            <>
              <Steps size="small" current={step} items={STEP_TITLES} />
              <ScanInput
                mode="fast"
                autoAdvance="safe"
                hint={
                  step === 0
                    ? `第 1 步 · 扫入 / 输入 SKU（期望 ${expectedSkuCode}）`
                    : step === 1
                      ? String(task.target_bin_id) === '0'
                        ? '第 2 步 · 扫入库位编码（任务无目标库位，扫码即改指定）'
                        : `第 2 步 · 扫入库位（期望 ${targetBinCode}）`
                      : '三步校验已完成'
                }
                onScan={(code) => handleScan(code)}
              />
              {step === 2 && (
                <>
                  <div className="sf-pad-sm-qty-input">
                    <Input
                      size="large"
                      value={remark}
                      placeholder="上架备注（选填）"
                      maxLength={255}
                      onChange={(e) => setRemark(e.target.value)}
                    />
                  </div>
                  <GuardedAction
                    label={executeMutation.isPending ? '提交中…' : '完成上架'}
                    icon={<CheckOutlined />}
                    variant="primary"
                    loading={executeMutation.isPending}
                    onClick={handleExecute}
                  />
                  <SfCompleteNextButton
                    onComplete={handleExecute}
                    nextTask={nextTask.data?.task ?? null}
                    onClaimNext={async (next) => {
                      try {
                        await claimMutation.mutateAsync(next.id)
                      } catch {
                        // 已被本人/他人领取等冲突：视为可进入（后端状态机为最终裁决）
                      }
                    }}
                    onNavigateNext={(next) => {
                      const hit = list.items.find((t) => String(t.id) === String(next.id))
                      if (hit) handleSelect(hit)
                    }}
                    completing={executeMutation.isPending}
                    disabled={!!executeDisabledReason}
                  />
                </>
              )}
              <p className="sf-pad-muted-note">
                三步序列见 frontend.md §21.4；当前校验为前端手输比对（扫码链路属 Scan 端 / F16），
                上架执行以后端强校验为准（仅领取人可执行，IN_PROGRESS→COMPLETED 触发 Putaway 落账）
              </p>
            </>
          )}
        </>
      )}
    </section>
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="上架任务状态筛选">
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
          description="上架任务列表（GET /api/putaway）加载失败"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有上架任务，试试切换状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((item) => (
            <PutawayTaskCard
              key={String(item.id)}
              task={item}
              targetBinCode={
                String(item.target_bin_id) === '0'
                  ? '待指定'
                  : (binItemMap.get(String(item.target_bin_id))?.code ?? `#${String(item.target_bin_id)}`)
              }
              selected={selected != null && String(selected.id) === String(item.id)}
              onClick={() => handleSelect(item)}
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

  const actionBarActions = [
    { key: 'back', label: '返回', icon: <ArrowLeftOutlined />, onClick: () => navigate(-1) },
    { key: 'scan', label: '扫码', icon: <ScanOutlined />, onClick: () => setScanOpen(true) },
    {
      key: 'exception',
      label: '异常',
      icon: <WarningOutlined />,
      variant: 'danger' as const,
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
      variant: 'primary' as const,
      loading: executeMutation.isPending,
      // 不可执行时禁用 + 原因 Toast（死按钮门禁）；可执行时与右栏 [完成上架] 同一提交动作
      disabled: !!executeDisabledReason,
      disabledReason: executeDisabledReason ?? '提交上架确认',
      onClick: handleFinishAction,
    },
  ]

  return (
    <>
      {contextHolder}
      {orientation === 'landscape' ? (
        <PadPageShell tasksSlot={listNode} contentSlot={infoNode} actionSlot={stepsNode} />
      ) : (
        // 竖屏（§20.4）：顶部当前上架任务卡 → 商品信息 / 步骤指示 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {task && (
                <PutawayTaskCard
                  task={task}
                  targetBinCode={targetBinCode}
                  selected
                />
              )}
              {task && <div className="sf-pad-tasks-detail">{infoNode}</div>}
              {stepsNode}
              {listNode}
            </>
          }
          contentSlot={null}
        />
      )}
      <PadActionBar actions={actionBarActions} />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <ScanInput
          mode="fast"
          autoAdvance="safe"
          autoFocus={false}
          hint={
            task
              ? `扫入 / 输入 SKU 或库位（当前期望：${step === 0 ? expectedSkuCode : targetBinCode}）`
              : '扫入 / 输入 SKU 或库位'
          }
          onScan={handleScanSubmit}
        />
      </Modal>
    </>
  )
}
