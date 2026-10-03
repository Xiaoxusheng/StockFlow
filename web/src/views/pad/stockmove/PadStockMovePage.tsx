import { useEffect, useMemo, useState } from 'react'
import { Button, InputNumber, Modal, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router'
import {
  inventoryApi,
  type InventoryTransferItem,
  type InventoryTransferQuery,
} from '@/api/inventory'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import {
  PadActionBar,
  PadInfoCard,
  PadPageShell,
  PadScanStub,
  usePadOrientation,
  type PadActionBarAction,
} from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import './stockmove.css'

/** 本页语义（changelog 第三批裁决）：/pad/stockmove 为库位间库存转移作业，
 * /pad/transfer 为仓库间调拨单；列表固定按库位维度过滤。 */
const MOVE_TYPE_FILTER: InventoryTransferQuery = { transferType: 'bin' }

/** [确认移库] 死按钮口径：执行端点未冻结（后端 internal/inventory/transfer.go M2 在途），
 * 冻结前不发起真实写请求（requirements.md §10 禁止假功能）。 */
const CONFIRM_DISABLED_REASON =
  '移库提交端点未冻结（后端 M2 在途，internal/inventory/transfer.go），冻结后接线；当前不发起真实写请求'

/** §21.4 移库操作序列（docs/business-flow.md §10.1 库位→库位维度）：三步 + 确认 */
const MOVE_STEPS: Array<{ key: 1 | 2 | 3 | 4; label: string }> = [
  { key: 1, label: '选源库位' },
  { key: 2, label: '录数量' },
  { key: 3, label: '选目标库位' },
  { key: 4, label: '确认移库' },
]

type ActiveStep = 1 | 2 | 3

interface MoveDraft {
  sourceBin: string
  qty: number | null
  targetBin: string
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
      qty: typeof parsed.qty === 'number' && Number.isFinite(parsed.qty) && parsed.qty > 0 ? parsed.qty : null,
      targetBin: typeof parsed.targetBin === 'string' ? parsed.targetBin : '',
      activeStep: parsed.activeStep === 2 || parsed.activeStep === 3 ? parsed.activeStep : 1,
    }
  } catch {
    return null
  }
}

/**
 * Pad 移库页（frontend.md §20.2 / §21.4 移库操作序列）：
 * - 横屏三栏：左=移库记录卡列表（inventoryApi.transfers 前端先行契约，错误态统一呈现），
 *   中=步骤指示 + 源库位/SKU/数量/目标库位 PadInfoCard，右=三步大按钮操作区 + PadScanStub；
 * - 竖屏堆叠：步骤指示条 → 当前步骤操作块 + 信息卡 → 移库记录列表 → PadActionBar 五槽。
 * [确认移库] / [完成] disabled 占位并注明「M2 冻结后接线」，禁用原因点击 Toast 送达（死按钮门禁）；
 * [暂停] 为本地暂存作业现场（sessionStorage），不产生业务写请求。
 */
export default function PadStockMovePage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, messageContext] = message.useMessage()

  const [draft] = useState<MoveDraft | null>(readDraft)
  const [sourceBin, setSourceBin] = useState(draft?.sourceBin ?? '')
  const [qty, setQty] = useState<number | null>(draft?.qty ?? null)
  const [targetBin, setTargetBin] = useState(draft?.targetBin ?? '')
  const [activeStep, setActiveStep] = useState<ActiveStep>(draft?.activeStep ?? 1)
  const [selected, setSelected] = useState<InventoryTransferItem | null>(null)
  const [scanOpen, setScanOpen] = useState(false)

  // 恢复暂存现场的一次性提示
  useEffect(() => {
    if (draft) messageApi.info('已恢复上次本地暂存的移库现场（[暂停] 仅本地暂存，不产生业务数据）')
    // messageApi 为 useMessage 稳定引用；draft 仅初始化读取
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messageApi])

  // 移库记录列表（前端先行契约 /api/inventory/transfers；后端就绪前呈统一错误态，禁止 mock）
  const moveList = usePagedList<InventoryTransferItem, InventoryTransferQuery>({
    queryKey: ['pad', 'stockmove', 'transfers'],
    fetch: (q) => inventoryApi.transfers(q),
    params: MOVE_TYPE_FILTER,
    defaultPageSize: 20,
  })
  const totalPage = Math.max(1, Math.ceil(moveList.total / moveList.pagination.pageSize))

  const stepDone = { 1: !!sourceBin, 2: qty != null && qty > 0, 3: !!targetBin } as const

  /** 扫码 / 手输按当前步骤录入工作流上下文（本轮仅本地记录，不发起库位校验与写请求） */
  const handleCodeSubmit = (code: string) => {
    const value = code.trim()
    if (!value) return
    if (activeStep === 2) {
      messageApi.warning('当前步骤为录数量，请手动输入数量；库位编码请在第 1 / 3 步录入')
      return
    }
    if (activeStep === 1) {
      setSourceBin(value)
      setActiveStep(2)
      messageApi.info(`已记录源库位「${value}」，请录入移库数量`)
    } else {
      setTargetBin(value)
      messageApi.info(`已记录目标库位「${value}」，可点击确认移库（提交待 M2 冻结后接线）`)
    }
  }

  const handlePause = () => {
    try {
      sessionStorage.setItem(DRAFT_KEY, JSON.stringify({ sourceBin, qty, targetBin, activeStep } satisfies MoveDraft))
      messageApi.info('已本地暂存移库现场，下次进入本页自动恢复（不产生业务数据）')
    } catch {
      messageApi.warning('当前环境本地暂存不可用，现场未保存')
    }
  }

  const handleFinishScan = (code: string) => {
    handleCodeSubmit(code)
    setScanOpen(false)
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

  // ---------- 三步操作块（§21.4：选源库位 → 录数量 → 选目标库位 → 确认） ----------

  const sourceBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">① 选源库位{stepDone[1] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">当前源库位：{sourceBin || EMPTY_TEXT}</div>
      {activeStep === 1 ? (
        <PadScanStub
          onSubmit={(code) => {
            setSourceBin(code.trim())
            setActiveStep(2)
            messageApi.info(`已记录源库位「${code.trim()}」，请录入移库数量`)
          }}
          placeholder="手输源库位编码兜底"
        />
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(1)}>
          {stepDone[1] ? '重新选源库位' : '选源库位'}
        </Button>
      )}
    </div>
  )

  const qtyBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">② 录数量{stepDone[2] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">当前移库数量：{qty != null ? formatNumber(qty) : EMPTY_TEXT}</div>
      {activeStep === 2 ? (
        <div className="sf-pad-sm-qty-input">
          <InputNumber
            size="large"
            min={1}
            precision={0}
            value={qty}
            placeholder="移库数量"
            onChange={(value) => setQty(value)}
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
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(2)}>
          {stepDone[2] ? '重新录数量' : '录数量'}
        </Button>
      )}
    </div>
  )

  const targetBlock = (
    <div className="sf-pad-sm-stepblock">
      <h4 className="sf-pad-group-title">③ 选目标库位{stepDone[3] ? '（已完成）' : ''}</h4>
      <div className="sf-pad-sm-stepblock__value">当前目标库位：{targetBin || EMPTY_TEXT}</div>
      {activeStep === 3 ? (
        <PadScanStub
          onSubmit={(code) => {
            setTargetBin(code.trim())
            messageApi.info(`已记录目标库位「${code.trim()}」，可确认移库（提交待 M2 冻结后接线）`)
          }}
          placeholder="手输目标库位编码兜底"
        />
      ) : (
        <Button size="large" className="sf-pad-sm-btn" onClick={() => setActiveStep(3)}>
          {stepDone[3] ? '重新选目标库位' : '选目标库位'}
        </Button>
      )}
    </div>
  )

  // [确认移库] disabled 占位：外包 span 接管点按，把原因以 Toast 送达（与 PadActionBar 同门禁口径）
  const confirmNode = (
    <span
      className="sf-pad-sm-confirm-wrap"
      onClick={() => messageApi.warning(CONFIRM_DISABLED_REASON)}
    >
      <Button block size="large" className="sf-pad-sm-btn" type="primary" disabled icon={<CheckOutlined />}>
        确认移库
      </Button>
    </span>
  )

  // ---------- 左栏：移库记录卡列表 ----------

  const listNode = (
    <div>
      <h4 className="sf-pad-group-title">移库记录（库位 → 库位）</h4>
      {moveList.isPending ? (
        <SfLoading rows={6} />
      ) : moveList.error ? (
        <SfError
          error={moveList.error}
          onRetry={() => void moveList.refetch()}
          description="GET /api/inventory/transfers 为前端先行契约，后端交付前呈统一错误态，属预期行为"
        />
      ) : moveList.items.length === 0 ? (
        <SfEmpty description="暂无库位间移库记录" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {moveList.items.map((item) => (
            <div
              key={String(item.id)}
              role="button"
              tabIndex={0}
              aria-pressed={selected != null && String(selected.id) === String(item.id)}
              className={`sf-pad-sm-record-card${
                selected != null && String(selected.id) === String(item.id)
                  ? ' sf-pad-sm-record-card--selected'
                  : ''
              }`}
              onClick={() => setSelected(item)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  setSelected(item)
                }
              }}
            >
              <div className="sf-pad-sm-record-card__head">
                <span>{item.transferNo}</span>
                <SfStatusTag status={item.status} />
              </div>
              <div>
                {item.skuCode} · {item.productName}
              </div>
              <div className="sf-pad-sm-record-card__route">
                {item.sourceBinCode ?? EMPTY_TEXT} → {item.targetBinCode ?? EMPTY_TEXT}
              </div>
              <div className="sf-pad-sm-record-card__qty">
                <span className="sf-pad-metric">{formatNumber(item.qty)}</span>
                <span className="sf-pad-sm-record-card__qty-unit">移库数量</span>
              </div>
              <div className="sf-pad-sm-record-card__foot">
                <span>{item.createdByName ?? EMPTY_TEXT}</span>
                <span>{formatDateTime(item.createdAt)}</span>
              </div>
            </div>
          ))}
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
          { label: '源库位', value: sourceBin || EMPTY_TEXT },
          { label: '目标库位', value: targetBin || EMPTY_TEXT },
          { label: 'SKU', value: selected?.skuCode ?? EMPTY_TEXT },
          { label: '商品名称', value: selected?.productName ?? EMPTY_TEXT },
          { label: '移库数量', value: qty != null ? formatNumber(qty) : EMPTY_TEXT, emphasis: true },
          { label: '参考记录', value: selected?.transferNo ?? EMPTY_TEXT },
        ]}
      />
      <p className="sf-pad-muted-note">
        操作序列对齐 frontend.md §21.4（扫源库位 → 录数量 → 选目标库位 → 确认）；本轮扫码为占位入口，
        库位编码仅本地记录、不做存在性校验；[确认移库] 待后端 M2 冻结提交端点后接线。
      </p>
    </>
  )

  const contextNode = (
    <>
      {stepIndicator}
      {contextInfoNode}
    </>
  )

  // ---------- 底部操作栏：竖屏五槽（完成 disabled），横屏三槽 ----------

  const actionBarActions = useMemo<PadActionBarAction[]>(
    () => [
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
              disabled: true,
              disabledReason: CONFIRM_DISABLED_REASON,
            },
          ]
        : []),
    ],
    // handlePause/navigate 为稳定引用；orientation 驱动槽位差异
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [orientation, navigate, sourceBin, qty, targetBin, activeStep],
  )

  const actionBar = <PadActionBar actions={actionBarActions} />

  const scanModal = (
    <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
      <PadScanStub onSubmit={handleFinishScan} placeholder="手输库位编码兜底（按当前步骤录入）" />
      <p className="sf-pad-muted-note">
        扫码能力属 Scan 端与 F16（frontend.md §19.2 边界）；本页手输按当前步骤（源库位 / 目标库位）记录。
      </p>
    </Modal>
  )

  return (
    <>
      {messageContext}
      {orientation === 'landscape' ? (
        // 横屏三栏：左移库记录 / 中步骤指示 + 上下文 / 右三步大按钮操作区
        <>
          <PadPageShell
            ratios={[32, 30, 38]}
            tasksSlot={listNode}
            contentSlot={contextNode}
            actionSlot={
              <>
                {sourceBlock}
                {qtyBlock}
                {targetBlock}
                {confirmNode}
              </>
            }
          />
          {actionBar}
        </>
      ) : (
        // 竖屏堆叠：步骤指示 → 当前步骤操作块 + 信息卡 → 移库记录列表（规格：顶部步骤条 → 信息卡 → 底部五槽）
        <>
          <PadPageShell
            ratios={[32, 30, 38]}
            tasksSlot={
              <>
                {stepIndicator}
                <div className="sf-pad-tasks-detail">
                  {sourceBlock}
                  {qtyBlock}
                  {targetBlock}
                  {confirmNode}
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
