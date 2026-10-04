import { useEffect, useMemo, useState } from 'react'
import { Button, Input, InputNumber, Modal, Select, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { resolveErrorMessage } from '@/api/client'
import { inventoryApi, type LedgerItem, type LedgerQuery } from '@/api/inventory'
import {
  MOVE_EXECUTE_PERMISSION,
  transferApi,
  type MoveBinPayload,
  type MoveKeyInput,
} from '@/api/transfer'
import { OPTIONS_FETCH_PAGE_SIZE, buildIdItemMap, buildSkuMaps, fetchBinOptions, fetchSkuOptions } from '@/api/options'
import type { BinItem } from '@/api/warehouse'
import type { SkuItem } from '@/api/masterdata'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  PadScanStub,
  usePadOrientation,
  type PadActionBarAction,
} from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import './stockmove.css'

/**
 * 本页语义（changelog 第三批裁决）：/pad/stockmove 为库位间库存转移作业，
 * /pad/transfer 为仓库间调拨单。
 * 契约回对（审计问题 #7 + Pad 移库 partial 审计）：列表从无注册的 GET /api/inventory/transfers
 * 改读真实移库流水 GET /api/inventory-ledgers?change_type=MOVE（inventoryApi.ledger，
 * 后端 handler.go:300-308 校验 change_type 值域含 MOVE）；[确认移库] 接线
 * POST /api/inventory/moves（transferApi.moveBin，internal/stockops/routes.go:60 +
 * moves.go:25-41：同仓同 SKU 同批次跨库位移动可用库存，源/目标两行各落一条 MOVE 流水）。
 */
const LEDGER_BASE_FILTER = {} as LedgerQuery

/** 移库端五维键（MoveKeyInput，stockops/moves.go:24-30）：关联 ID 提交 number，
 * 选项行 ID（database.ID 字符串）经 Number() 还原；完整定位键经 binApi 选项行
 * warehouse_id/zone_id/shelf_id 解析（api/warehouse.ts BinItem 层级锚点） */
function toMoveKey(bin: BinItem, skuId: number, batchId: number): MoveKeyInput {
  return {
    warehouse_id: Number(bin.warehouse_id),
    zone_id: Number(bin.zone_id),
    shelf_id: Number(bin.shelf_id),
    bin_id: Number(bin.id),
    sku_id: skuId,
    batch_id: batchId,
  }
}

/** §21.4 移库操作序列（docs/business-flow.md §10.1 库位→库位维度）：扫源库位 → 扫商品·录数量 → 扫目标库位 → 确认 */
const MOVE_STEPS: Array<{ key: 1 | 2 | 3 | 4; label: string }> = [
  { key: 1, label: '选源库位' },
  { key: 2, label: '扫商品 · 录数量' },
  { key: 3, label: '选目标库位' },
  { key: 4, label: '确认移库' },
]

type ActiveStep = 1 | 2 | 3

interface MoveDraft {
  sourceBin: string
  targetBin: string
  skuId: string
  batchId: string
  qty: number | null
  sourceNo: string
  remark: string
  activeStep: ActiveStep
}

const DRAFT_KEY = 'sf.pad.stockmove.draft'

/** 暂停 = 本地暂存作业现场（sessionStorage）；不产生任何业务写请求，恢复时如实提示 */
function readDraft(): MoveDraft | null {
  try {
    const raw = sessionStorage.getItem(DRAFT_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<MoveDraft> | null
    if (parsed == null) return null
    return {
      sourceBin: typeof parsed.sourceBin === 'string' ? parsed.sourceBin : '',
      targetBin: typeof parsed.targetBin === 'string' ? parsed.targetBin : '',
      skuId: typeof parsed.skuId === 'string' ? parsed.skuId : '',
      batchId: typeof parsed.batchId === 'string' ? parsed.batchId : '',
      qty: typeof parsed.qty === 'number' && Number.isFinite(parsed.qty) && parsed.qty > 0 ? parsed.qty : null,
      sourceNo: typeof parsed.sourceNo === 'string' ? parsed.sourceNo : '',
      remark: typeof parsed.remark === 'string' ? parsed.remark : '',
      activeStep: parsed.activeStep === 2 || parsed.activeStep === 3 ? parsed.activeStep : 1,
    }
  } catch {
    return null
  }
}

/** 守卫动作按钮（死按钮门禁口径，frontend.md §9.1）：disabled 外包 span 接管点按把原因
 * 以 Toast 送达；可执行时为真实提交按钮（POST /api/inventory/moves） */
function MoveConfirmAction({
  label,
  disabledReason,
  loading = false,
  onClick,
}: {
  label: string
  disabledReason?: string
  loading?: boolean
  onClick?: () => void
}) {
  const [messageApi, contextHolder] = message.useMessage()
  if (!disabledReason) {
    return (
      <Button block size="large" className="sf-pad-sm-btn" type="primary" icon={<CheckOutlined />} loading={loading} onClick={onClick}>
        {label}
      </Button>
    )
  }
  return (
    <>
      {contextHolder}
      <span className="sf-pad-sm-confirm-wrap" onClick={() => messageApi.warning(disabledReason)}>
        <Button block size="large" className="sf-pad-sm-btn" type="primary" icon={<CheckOutlined />} disabled>
          {label}
        </Button>
      </span>
    </>
  )
}

/**
 * Pad 移库页（frontend.md §20.2 / §21.4 移库操作序列）：
 * - 横屏三栏：左=移库流水卡列表（GET /api/inventory-ledgers?change_type=MOVE，源行负/目标行正成对），
 *   中=步骤指示 + 移库上下文 PadInfoCard，右=三步大按钮操作区 + 确认块；
 * - 竖屏堆叠：步骤指示条 → 当前步骤操作块 + 信息卡 → 移库流水列表 → PadActionBar 五槽。
 * [确认移库] 提交 POST /api/inventory/moves（stockops:move:execute 经 canAccess fail-closed）；
 * [暂停] 为本地暂存作业现场（sessionStorage），不产生业务写请求。
 */
export default function PadStockMovePage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限码经 canAccess fail-closed 过滤（模式同 PC TransferPage；permission.md §5）
  const user = useAuthStore((state) => state.user)
  const canMove = canAccess(user, MOVE_EXECUTE_PERMISSION)

  const [draft] = useState<MoveDraft | null>(readDraft)
  const [sourceBin, setSourceBin] = useState(draft?.sourceBin ?? '')
  const [targetBin, setTargetBin] = useState(draft?.targetBin ?? '')
  const [skuId, setSkuId] = useState<string>(draft?.skuId ?? '')
  const [batchId, setBatchId] = useState<string>(draft?.batchId ?? '')
  const [qty, setQty] = useState<number | null>(draft?.qty ?? null)
  const [sourceNo, setSourceNo] = useState(draft?.sourceNo ?? '')
  const [remark, setRemark] = useState(draft?.remark ?? '')
  const [activeStep, setActiveStep] = useState<ActiveStep>(draft?.activeStep ?? 1)
  const [selectedLedger, setSelectedLedger] = useState<LedgerItem | null>(null)
  const [scanOpen, setScanOpen] = useState(false)

  // 恢复暂存现场的一次性提示
  useEffect(() => {
    if (draft) messageApi.info('已恢复上次本地暂存的移库现场（[暂停] 仅本地暂存，不产生业务数据）')
    // messageApi 为 useMessage 稳定引用；draft 仅初始化读取
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messageApi])

  // 移库流水列表（真实端点 GET /api/inventory-ledgers，固定 change_type=MOVE；禁止 mock）
  const moveList = usePagedList<LedgerItem, LedgerQuery>({
    queryKey: ['pad', 'stockmove', 'move-ledger'],
    fetch: (q) => inventoryApi.ledger({ ...q, change_type: 'MOVE' }),
    params: LEDGER_BASE_FILTER,
    defaultPageSize: 20,
  })
  const totalPage = Math.max(1, Math.ceil(moveList.total / moveList.pagination.pageSize))

  // SKU / 库位映射（基础资料 options 一次取全；失败呈错误态并禁用相关步骤，不造假数据）
  const skuOptions = useQuery({
    queryKey: ['pad', 'stockmove', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const binOptions = useQuery({
    queryKey: ['pad', 'stockmove', 'bin-options'],
    queryFn: fetchBinOptions,
  })
  const skuItems = useMemo(() => skuOptions.data ?? [], [skuOptions.data])
  const binItems = useMemo(() => binOptions.data ?? [], [binOptions.data])
  const skuMaps = useMemo(() => buildSkuMaps(skuItems), [skuItems])
  /** 库位编码 → 整行（扫码 / 手输库位编码解析为 MoveKeyInput 完整定位键） */
  const binByCode = useMemo(() => {
    const map = new Map<string, BinItem>()
    for (const bin of binItems) map.set(bin.code, bin)
    return map
  }, [binItems])
  const binItemMap = useMemo(() => buildIdItemMap<BinItem>(binItems, (bin) => bin.id), [binItems])
  /** SKU 编码 → 整行（扫码 SKU 编码解析，PadInventoryPage 反查同口径） */
  const skuByCode = useMemo(() => {
    const map = new Map<string, SkuItem>()
    for (const sku of skuItems) map.set(sku.code, sku)
    return map
  }, [skuItems])

  // 选中 SKU 的批次台账（启用批次管理时按 SKU 拉 /api/batches；未启用按非批次 batch_id=0 提交）
  const selectedSku = skuItems.find((sku) => String(sku.id) === skuId) ?? null
  const batchManaged = selectedSku?.is_batch_managed === true
  const batches = useQuery({
    queryKey: ['pad', 'stockmove', 'batches', skuId],
    queryFn: () => inventoryApi.batches({ sku_id: skuId, page: 1, pageSize: OPTIONS_FETCH_PAGE_SIZE }),
    enabled: batchManaged && Boolean(skuId),
  })

  // 库位编码 → BinItem（空串不入表）；options 加载失败时解析为 null 并阻断提交（fail-closed）
  const sourceBinItem = sourceBin ? (binByCode.get(sourceBin) ?? null) : null
  const targetBinItem = targetBin ? (binByCode.get(targetBin) ?? null) : null

  const stepDone = { 1: !!sourceBinItem, 2: !!skuId && qty != null && qty > 0, 3: !!targetBinItem } as const

  const resetDraft = () => {
    setSourceBin('')
    setTargetBin('')
    setSkuId('')
    setBatchId('')
    setQty(null)
    setSourceNo('')
    setRemark('')
    setActiveStep(1)
    setSelectedLedger(null)
    try {
      sessionStorage.removeItem(DRAFT_KEY)
    } catch {
      // 存储不可用时忽略（会话内状态已重置）
    }
  }

  /** 解析并记录源 / 目标库位（编码 → BinItem；同仓 + 目标 ≠ 源约束与后端 MoveBin 原语一致，
   * inventory/service.go:706-713，跨仓属调拨域 /pad/transfer） */
  const resolveBinInput = (code: string, kind: 'source' | 'target') => {
    const value = code.trim()
    if (!value) return
    const bin = binByCode.get(value)
    if (!bin) {
      messageApi.error(`未匹配到库位编码「${value}」，请核对后重试`)
      return
    }
    if (kind === 'source') {
      if (targetBinItem && String(bin.id) === String(targetBinItem.id)) {
        messageApi.error('目标库位不能与源库位相同')
        return
      }
      setSourceBin(bin.code)
      setActiveStep(2)
      messageApi.info(`已确认源库位「${bin.code}」（仓 ${String(bin.warehouse_id)}），请选择商品并录入数量`)
      return
    }
    if (sourceBinItem && String(bin.warehouse_id) !== String(sourceBinItem.warehouse_id)) {
      messageApi.error(`目标库位须与源库位同仓（源仓 ${String(sourceBinItem.warehouse_id)}）；跨仓请走调拨单 /pad/transfer`)
      return
    }
    if (sourceBinItem && String(bin.id) === String(sourceBinItem.id)) {
      messageApi.error('目标库位不能与源库位相同')
      return
    }
    setTargetBin(bin.code)
    messageApi.info(`已确认目标库位「${bin.code}」，请填写作业依据号后确认移库`)
  }

  /** 扫码 / 手输按当前步骤录入：第 2 步为 SKU 编码解析，第 1 / 3 步为库位编码解析 */
  const handleCodeSubmit = (code: string) => {
    const value = code.trim()
    if (!value) return
    if (activeStep === 2) {
      const sku = skuByCode.get(value)
      if (!sku) {
        messageApi.error(`未匹配到 SKU 编码「${value}」，请核对后重试`)
        return
      }
      setSkuId(String(sku.id))
      setBatchId('')
      messageApi.info(`已选择商品「${sku.code}」，请录入移库数量`)
      return
    }
    resolveBinInput(value, activeStep === 1 ? 'source' : 'target')
  }

  const handlePause = () => {
    try {
      sessionStorage.setItem(
        DRAFT_KEY,
        JSON.stringify({ sourceBin, targetBin, skuId, batchId, qty, sourceNo, remark, activeStep } satisfies MoveDraft),
      )
      messageApi.info('已本地暂存移库现场，下次进入本页自动恢复（不产生业务数据）')
    } catch {
      messageApi.warning('当前环境本地暂存不可用，现场未保存')
    }
  }

  const handleFinishScan = (code: string) => {
    handleCodeSubmit(code)
    setScanOpen(false)
  }

  // 确认移库守卫（§21.7 具体原因；后端 moves.go:56-64 仍做强校验）
  const confirmDisabledReason = (() => {
    if (!canMove) return `缺少 ${MOVE_EXECUTE_PERMISSION} 权限，无法执行移库`
    if (binOptions.isPending || skuOptions.isPending) return '库位 / 商品基础资料加载中，请稍候'
    if (binOptions.error || skuOptions.error) return '库位 / 商品基础资料加载失败，无法解析移库定位键'
    if (!sourceBinItem) return '先选择源库位（第 1 步）'
    if (!skuId) return '先选择商品 SKU（第 2 步）'
    if (batchManaged && !batchId) return '该 SKU 启用批次管理，请选择批次'
    if (qty == null || qty <= 0) return '先录入大于 0 的移库数量（第 2 步）'
    if (!/^\d+(\.\d{1,4})?$/.test(String(qty))) return '数量须为十进制数字，最多 4 位小数（numeric(18,4)）'
    if (!targetBinItem) return '先选择目标库位（第 3 步）'
    if (!sourceNo.trim()) return '作业依据号必填（inventory-rules §5 来源追溯，落入流水 business_no）'
    return undefined
  })()

  // 确认移库：POST /api/inventory/moves（MoveBin 原语：源行 available/total 同减、目标行同增）
  const moveMutation = useMutation({
    mutationFn: (payload: MoveBinPayload) => transferApi.moveBin(payload),
    onSuccess: (result) => {
      messageApi.success(
        result.replay
          ? `移库已提交（幂等重放，库存未重复变更；流水 ${result.ledger.ledger_no}）`
          : `✓ 移库成功：${sourceBin} → ${targetBin}，已生成移库流水 ${result.ledger.ledger_no}`,
      )
      void queryClient.invalidateQueries({ queryKey: ['pad', 'stockmove'] })
      resetDraft()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleConfirmMove = () => {
    if (!sourceBinItem || !targetBinItem || !selectedSku || qty == null) return
    moveMutation.mutate({
      from: toMoveKey(sourceBinItem, Number(selectedSku.id), batchManaged && batchId ? Number(batchId) : 0),
      to: toMoveKey(targetBinItem, Number(selectedSku.id), batchManaged && batchId ? Number(batchId) : 0),
      // numeric(18,4) 文本（moves.go:38 qty 注释；stock.ParseQty 十进制格式校验）
      qty: String(qty),
      source_no: sourceNo.trim(),
      remark: remark.trim() || undefined,
    })
  }

  const stepIndicator = (
    <div className="sf-pad-sm-steps" role="list" aria-label="移库步骤指示">
      {MOVE_STEPS.map((step) => {
        const done = step.key !== 4 && stepDone[step.key as ActiveStep]
        const active = step.key === activeStep
        return (
          <span
            key={step.key}
            role="listitem"
            className={`sf-pad-sm-step${done ? ' sf-pad-sm-step--done' : ''}${active ? ' sf-pad-sm-step--active' : ''}`}
          >
            {done ? <CheckCircleOutlined /> : null}第 {step.key} 步 · {step.label}
          </span>
        )
      })}
    </div>
  )

  // ---------- 三步操作块（§21.4：选源库位 → 扫商品·录数量 → 选目标库位 → 确认） ----------

  const sourceBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">① 选源库位{stepDone[1] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">
        当前源库位：{sourceBinItem ? `${sourceBinItem.code}（仓 ${String(sourceBinItem.warehouse_id)}）` : sourceBin || EMPTY_TEXT}
      </div>
      {activeStep === 1 ? (
        <PadScanStub
          onSubmit={(code) => resolveBinInput(code, 'source')}
          placeholder="手输源库位编码兜底"
        />
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(1)}>
          {stepDone[1] ? '重新选源库位' : '选源库位'}
        </Button>
      )}
    </div>
  )

  const skuOptionsList = skuItems.map((sku) => ({
    label: sku.product_name ? `${sku.code} ${sku.product_name}` : sku.code,
    value: String(sku.id),
  }))
  const batchOptions = (batches.data?.items ?? []).map((item) => ({
    label: item.batch_no,
    value: String(item.id),
  }))

  const skuQtyBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">② 扫商品 · 录数量{stepDone[2] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">
        当前商品：{selectedSku ? `${selectedSku.code} ${selectedSku.product_name ?? EMPTY_TEXT}` : EMPTY_TEXT}
        {qty != null ? ` · 数量 ${formatNumber(qty)}` : ''}
      </div>
      {activeStep === 2 ? (
        <>
          <Select
            size="large"
            showSearch
            optionFilterProp="label"
            options={skuOptionsList}
            value={skuId || undefined}
            placeholder={skuOptions.isPending ? 'SKU 加载中…' : '选择商品 SKU（可输入编码检索）'}
            loading={skuOptions.isPending}
            disabled={skuOptions.isError}
            onChange={(value: string) => {
              setSkuId(value)
              setBatchId('')
            }}
            style={{ width: '100%' }}
            aria-label="选择商品 SKU"
          />
          {batchManaged && (
            <Select
              size="large"
              showSearch
              optionFilterProp="label"
              options={batchOptions}
              value={batchId || undefined}
              placeholder={batches.isPending ? '批次加载中…' : '选择批次（该 SKU 启用批次管理）'}
              loading={batches.isPending}
              onChange={(value: string) => setBatchId(value)}
              style={{ width: '100%' }}
              aria-label="选择批次"
            />
          )}
          {batchManaged && batches.isError && (
            <p className="sf-pad-muted-note">批次台账加载失败：{resolveErrorMessage(batches.error)}</p>
          )}
          <div className="sf-pad-sm-qty-input">
            <InputNumber
              size="large"
              min={0}
              value={qty}
              placeholder="移库数量（大于 0）"
              onChange={(value) => setQty(typeof value === 'number' ? value : null)}
              aria-label="移库数量"
            />
            <Button
              size="large"
              className="sf-pad-sm-btn"
              type="primary"
              disabled={qty == null || qty <= 0}
              onClick={() => {
                setActiveStep(3)
                messageApi.info('数量已记录，请选择目标库位')
              }}
            >
              确认数量
            </Button>
          </div>
          {skuOptions.error && (
            <p className="sf-pad-muted-note">
              SKU 基础资料加载失败：{resolveErrorMessage(skuOptions.error)}；可点击底部 [扫码] 手输 SKU 编码兜底
            </p>
          )}
        </>
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(2)}>
          {stepDone[2] ? '重新选商品' : '选商品 · 录数量'}
        </Button>
      )}
    </div>
  )

  const targetBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">③ 选目标库位{stepDone[3] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">
        当前目标库位：{targetBinItem ? `${targetBinItem.code}（仓 ${String(targetBinItem.warehouse_id)}）` : targetBin || EMPTY_TEXT}
      </div>
      {activeStep === 3 ? (
        <PadScanStub
          onSubmit={(code) => resolveBinInput(code, 'target')}
          placeholder="手输目标库位编码兜底"
        />
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(3)}>
          {stepDone[3] ? '重新选目标库位' : '选目标库位'}
        </Button>
      )}
    </div>
  )

  const confirmBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">④ 确认移库</h4>
      <Input
        size="large"
        value={sourceNo}
        placeholder="作业依据号（必填，如作业工单 / 异常单号）"
        maxLength={64}
        onChange={(e) => setSourceNo(e.target.value)}
        aria-label="作业依据号"
      />
      <Input.TextArea
        value={remark}
        placeholder="备注（选填）"
        rows={2}
        maxLength={255}
        onChange={(e) => setRemark(e.target.value)}
        aria-label="移库备注"
      />
      <MoveConfirmAction
        label={moveMutation.isPending ? '提交中…' : '确认移库'}
        disabledReason={confirmDisabledReason}
        loading={moveMutation.isPending}
        onClick={handleConfirmMove}
      />
      <p className="sf-pad-muted-note">
        同仓同 SKU 同批次跨库位移动可用库存；提交后源 / 目标库位各生成一条 MOVE 流水，
        作业依据号落入流水 business_no 追溯（inventory-rules §5）
      </p>
    </div>
  )

  // ---------- 左栏：移库流水卡列表（成对 MOVE：源行负 / 目标行正） ----------

  const directionOf = (change: number) => (change > 0 ? '移入' : change < 0 ? '移出' : '无变动')

  const listNode = (
    <div>
      <h4 className="sf-pad-group-title">移库流水（库位 → 库位）</h4>
      {moveList.isPending ? (
        <SfLoading rows={6} />
      ) : moveList.error ? (
        <SfError
          error={moveList.error}
          onRetry={() => void moveList.refetch()}
          description="移库流水（GET /api/inventory-ledgers?change_type=MOVE）加载失败"
        />
      ) : moveList.items.length === 0 ? (
        <SfEmpty description="暂无库位间移库流水，完成首次移库后在此追溯" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {moveList.items.map((item) => {
            const isSelected = selectedLedger != null && String(selectedLedger.id) === String(item.id)
            return (
              <div
                key={String(item.id)}
                role="button"
                tabIndex={0}
                aria-pressed={isSelected}
                className={`sf-pad-sm-record-card${isSelected ? ' sf-pad-sm-record-card--selected' : ''}`}
                onClick={() => setSelectedLedger(item)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    setSelectedLedger(item)
                  }
                }}
              >
                <div className="sf-pad-sm-record-card__head">
                  <span>{item.business_no || EMPTY_TEXT}</span>
                  <span>
                    {directionOf(item.qty_change)} {item.qty_change > 0 ? '+' : ''}
                    {formatNumber(item.qty_change)}
                  </span>
                </div>
                <div className="sf-pad-sm-record-card__route">
                  位 {String(item.bin_id) === '0' ? EMPTY_TEXT : (binItemMap.get(String(item.bin_id))?.code ?? `#${String(item.bin_id)}`)} ·{' '}
                  {skuMaps.code.get(String(item.sku_id)) ?? `#${String(item.sku_id)}`}
                </div>
                <div className="sf-pad-sm-record-card__qty">
                  <span className="sf-pad-metric">
                    {formatNumber(item.qty_before)} → {formatNumber(item.qty_after)}
                  </span>
                  <span className="sf-pad-sm-record-card__qty-unit">{item.ledger_no}</span>
                </div>
                <div className="sf-pad-sm-record-card__foot">
                  <span>{item.operator_name || EMPTY_TEXT}</span>
                  <span>{formatDateTime(item.created_at)}</span>
                </div>
              </div>
            )
          })}
        </div>
      )}
      <div className="sf-pad-tasks-pager">
        <span className="sf-pad-info-label">
          共 {moveList.total} 条 · 第 {moveList.pagination.current}/{totalPage} 页
        </span>
        <div className="sf-pad-pager-btns">
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={moveList.pagination.current <= 1}
            onClick={() => moveList.onPageChange(moveList.pagination.current - 1, moveList.pagination.pageSize)}
          >
            上一页
          </Button>
          <Button
            size="large"
            className="sf-pad-chip"
            disabled={moveList.pagination.current >= totalPage}
            onClick={() => moveList.onPageChange(moveList.pagination.current + 1, moveList.pagination.pageSize)}
          >
            下一页
          </Button>
        </div>
      </div>
    </div>
  )

  // ---------- 中栏：移库上下文信息卡 ----------

  const contextInfoNode = (
    <>
      <PadInfoCard
        title="移库上下文"
        items={[
          { label: '源库位', value: sourceBinItem ? sourceBinItem.code : sourceBin || EMPTY_TEXT },
          { label: '目标库位', value: targetBinItem ? targetBinItem.code : targetBin || EMPTY_TEXT },
          { label: 'SKU', value: selectedSku ? selectedSku.code : EMPTY_TEXT },
          { label: '商品名称', value: selectedSku?.product_name ?? EMPTY_TEXT },
          {
            label: '批次',
            value: batchManaged ? (batchId || EMPTY_TEXT) : '非批次',
          },
          { label: '移库数量', value: qty != null ? formatNumber(qty) : EMPTY_TEXT, emphasis: true },
          { label: '作业依据号', value: sourceNo || EMPTY_TEXT },
          {
            label: '参考流水',
            value: selectedLedger ? (selectedLedger.business_no || selectedLedger.ledger_no) : EMPTY_TEXT,
          },
        ]}
      />
      <p className="sf-pad-muted-note">
        操作序列对齐 frontend.md §21.4（选源库位 → 扫商品·录数量 → 选目标库位 → 确认）；
        库位编码经 binApi 选项行解析完整定位键（warehouse/zone/shelf/bin），失败阻断提交不造假提交
      </p>
    </>
  )

  const contextNode = (
    <>
      {stepIndicator}
      {contextInfoNode}
    </>
  )

  // ---------- 底部操作栏：竖屏五槽（完成接线确认移库），横屏三槽 ----------

  // 每次渲染重建（不走 useMemo）：[完成] 依赖 sourceBin/sku/qty 等易变作业现场，
  // 记忆化会留 stale 闭包——提交动作必须拿到当前渲染的最新上下文
  const actionBarActions: PadActionBarAction[] = [
    { key: 'back', label: '返回', icon: <ArrowLeftOutlined />, onClick: () => navigate(-1) },
    { key: 'scan', label: '扫码', icon: <ScanOutlined />, onClick: () => setScanOpen(true) },
    {
      key: 'exception',
      label: '异常',
      icon: <WarningOutlined />,
      variant: 'danger',
      onClick: () => navigate('/pad/exception'),
    },
    ...(orientation === 'portrait'
      ? [
          { key: 'pause', label: '暂停', icon: <PauseOutlined />, onClick: handlePause },
          {
            key: 'finish',
            label: '完成',
            icon: <CheckOutlined />,
            variant: 'primary' as const,
            loading: moveMutation.isPending,
            // [完成] = 确认移库同一提交动作；不可执行时禁用并送出具体原因（死按钮门禁）
            disabled: !!confirmDisabledReason,
            disabledReason: confirmDisabledReason ?? '提交移库',
            onClick: () => {
              if (confirmDisabledReason) {
                messageApi.warning(confirmDisabledReason)
                return
              }
              handleConfirmMove()
            },
          },
        ]
      : []),
  ]

  const actionBar = <PadActionBar actions={actionBarActions} />

  const scanModal = (
    <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
      <PadScanStub
        onSubmit={handleFinishScan}
        placeholder={
          activeStep === 2
            ? '手输 SKU 编码兜底（解析为商品）'
            : activeStep === 1
              ? '手输源库位编码兜底'
              : '手输目标库位编码兜底'
        }
      />
      <p className="sf-pad-muted-note">
        扫码能力属 Scan 端与 F16（frontend.md §19.2 边界）；本页手输按当前步骤（源库位 / SKU / 目标库位）解析录入。
      </p>
    </Modal>
  )

  return (
    <>
      {messageContext}
      {orientation === 'landscape' ? (
        // 横屏三栏：左移库流水 / 中步骤指示 + 上下文 / 右三步大按钮操作区 + 确认块
        <>
          <PadPageShell
            ratios={[32, 30, 38]}
            tasksSlot={listNode}
            contentSlot={contextNode}
            actionSlot={
              <>
                {sourceBlock}
                {skuQtyBlock}
                {targetBlock}
                {confirmBlock}
              </>
            }
          />
          {actionBar}
        </>
      ) : (
        // 竖屏堆叠：步骤指示 → 当前步骤操作块 + 信息卡 → 移库流水列表（规格：顶部步骤条 → 信息卡 → 底部五槽）
        <>
          <PadPageShell
            ratios={[32, 30, 38]}
            tasksSlot={
              <>
                {stepIndicator}
                <div className="sf-pad-tasks-detail">
                  {sourceBlock}
                  {skuQtyBlock}
                  {targetBlock}
                  {confirmBlock}
                </div>
                {contextInfoNode}
              </>
            }
            contentSlot={listNode}
          />
          {actionBar}
        </>
      )}
      {scanModal}
    </>
  )
}
