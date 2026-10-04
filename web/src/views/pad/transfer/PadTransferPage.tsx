import { useMemo, useState } from 'react'
import { Button, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { resolveErrorMessage } from '@/api/client'
import {
  TRANSFER_EXECUTE_PERMISSION,
  TRANSFER_STATUS_TAG,
  transferApi,
  type TransferOrder,
  type TransferQuery,
  type TransferStatus,
  type TransferType,
} from '@/api/transfer'
import {
  buildIdItemMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
} from '@/api/options'
import type { BinItem } from '@/api/warehouse'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import { TRANSFER_TYPE_LABEL, TransferDocCard, TransferStatusTag } from './TransferDocCard'
import './transfer.css'

/** 类型 chip（business-flow.md §10.1 两种维度；文案与 api/transfer.ts TransferType 同源） */
const TYPE_OPTIONS: Array<{ label: string; value: TransferType }> = (
  Object.keys(TRANSFER_TYPE_LABEL) as TransferType[]
).map((value) => ({ value, label: TRANSFER_TYPE_LABEL[value] }))

/** 状态 chip（七态顺序即状态机主链顺序 + 取消；label 取 api/transfer.ts TRANSFER_STATUS_TAG） */
const STATUS_OPTIONS: Array<{ label: string; value: TransferStatus }> = (
  Object.keys(TRANSFER_STATUS_TAG) as TransferStatus[]
).map((value) => ({ value, label: TRANSFER_STATUS_TAG[value].label }))

/** 主链流转顺序（business-flow.md §10.1：草稿→待审核→待出库→调拨中→待入库→已完成；任一环节可已取消） */
const TRANSFER_FLOW: TransferStatus[] = [
  'DRAFT',
  'PENDING_APPROVAL',
  'APPROVED',
  'TRANSFERRING',
  'AWAITING_RECEIPT',
  'COMPLETED',
]

/** 守卫动作按钮（死按钮门禁口径，frontend.md §9.1）：disabled 外包 span 接管点按把原因
 * 以 Toast 送达；可执行时为真实提交按钮（不再是无端点占位） */
function TransferAction({
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
      <Button
        block
        size="large"
        className="sf-pad-tr-action-btn"
        type="primary"
        loading={loading}
        onClick={onClick}
      >
        {label}
      </Button>
    )
  }
  return (
    <>
      {contextHolder}
      <span style={{ flex: 1, minWidth: 0, display: 'block' }} onClick={() => messageApi.warning(disabledReason)}>
        <Button block size="large" className="sf-pad-tr-action-btn" disabled>
          {label}
        </Button>
      </span>
    </>
  )
}

/**
 * Pad 调拨页（仓库主管查看 / 执行视角，devices.md §12 Pad 职能）：
 * - 契约回对共享层 api/transfer.ts（TransferOrderView snake_case，七态状态机
 *   DRAFT→PENDING_APPROVAL→APPROVED→TRANSFERRING→AWAITING_RECEIPT→COMPLETED / CANCELLED，
 *   internal/stockops/models.go:134-142）；明细走 GET /api/transfers/{id}；
 * - [发起出库] 接线 POST /api/transfers/{id}/outbound（仅 APPROVED，源仓扣减）；
 *   [到货登记] 接线 POST /api/transfers/{id}/arrive（仅 TRANSFERRING，无库存动作，
 *   transfer.go:594-627 —— 到货登记与收货入库是两步迁移，不并入同一按钮流）；
 *   [确认入库] 接线 POST /api/transfers/{id}/receive（仅 AWAITING_RECEIPT，逐行 TransferIn）；
 * - 动作前置权限码 stockops:transfer:execute（permissions.go:221）经 canAccess fail-closed
 *   控制（permission.md §5：前端仅体验优化，后端 RequirePermission 仍强校验）；
 * - 仓库/SKU/库位编码经基础资料 options 本地映射（PadInventoryPage 范本），失败降级为 ID；
 * - 横屏三栏：左=调拨单卡列表 + 类型/状态 chip 筛选，中=单据信息 + 明细，右=七态流转 + 动作；
 *   竖屏堆叠：当前调拨单（信息 + 流转 + 动作）→ 筛选与列表 → PadActionBar [返回][扫码][异常]。
 */
export default function PadTransferPage() {
  const orientation = usePadOrientation()
  const [messageApi, messageContext] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限码经 canAccess fail-closed 过滤（模式同 PC TransferPage；permission.md §5）
  const user = useAuthStore((state) => state.user)
  const canExecute = canAccess(user, TRANSFER_EXECUTE_PERMISSION)

  const [typeFilter, setTypeFilter] = useState<TransferType | 'all'>('all')
  const [statusFilter, setStatusFilter] = useState<TransferStatus | 'all'>('all')
  const [selected, setSelected] = useState<TransferOrder | null>(null)

  const list = usePagedList<TransferOrder, TransferQuery>({
    queryKey: ['pad', 'transfer', 'list'],
    fetch: (q) => transferApi.list(q),
    params: useMemo<TransferQuery>(
      () => ({
        type: typeFilter === 'all' ? undefined : typeFilter,
        status: statusFilter === 'all' ? undefined : statusFilter,
      }),
      [typeFilter, statusFilter],
    ),
    defaultPageSize: 20,
  })
  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  // 单据详情：GET /api/transfers/{id} 返回 {order, items}（handler.go:86-89）；出库/入库动作后
  // 随 ['pad','transfer'] 失效刷新，单据头以详情优先（动作后状态即时可见），未就绪回退列表行
  const detailQuery = useQuery({
    queryKey: ['pad', 'transfer', 'detail', String(selected?.id ?? '')],
    queryFn: () => {
      if (!selected) throw new Error('未选择调拨单')
      return transferApi.detail(selected.id)
    },
    enabled: selected != null,
  })
  const order = detailQuery.data?.order ?? selected
  const lines = detailQuery.data?.items ?? []

  // 仓库/SKU/库位编码映射（基础资料 options 一次取全；失败降级为 ID 显示，不造假数据）
  const warehouseOptions = useQuery({
    queryKey: ['pad', 'transfer', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const warehouseNameMap = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const skuOptions = useQuery({
    queryKey: ['pad', 'transfer', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const binOptions = useQuery({
    queryKey: ['pad', 'transfer', 'bin-options'],
    queryFn: fetchBinOptions,
  })
  const binItemMap = useMemo(
    () => buildIdItemMap<BinItem>(binOptions.data ?? [], (bin) => bin.id),
    [binOptions.data],
  )

  const warehouseNameOf = (id: TransferOrder['from_warehouse_id']) =>
    warehouseNameMap.get(String(id)) ?? `#${String(id)}`
  const binCodeOf = (id: BinItem['id']) =>
    String(id) === '0' ? EMPTY_TEXT : (binItemMap.get(String(id))?.code ?? `#${String(id)}`)

  const handleFilter = (apply: () => void) => {
    apply()
    setSelected(null)
    list.resetToFirstPage()
  }

  /** 扫码 / 手输兜底：在当前已加载调拨单中按单号选中（不做不存在的“直达详情”假动作） */
  const handleCodeSubmit = (code: string) => {
    const value = code.trim()
    const hit = list.items.find((item) => item.transfer_no === value)
    if (hit) {
      setSelected(hit)
      messageApi.info(`已选中调拨单 ${hit.transfer_no}`)
    } else {
      messageApi.warning(`当前列表未加载单号「${value}」；扫码直达单据属 Scan 端通用扫码中心（frontend.md §21.5）`)
    }
  }

  const invalidateTransfer = () => {
    void queryClient.invalidateQueries({ queryKey: ['pad', 'transfer'] })
  }

  // 发起出库：POST /api/transfers/{id}/outbound（APPROVED→TRANSFERRING + 源仓逐行 TransferOut）
  const outboundMutation = useMutation({
    mutationFn: (id: TransferOrder['id']) => transferApi.outbound(id),
    onSuccess: (detail) => {
      messageApi.success(`调拨单 ${detail.order.transfer_no} 已发起出库（待出库 → 调拨中，源仓已扣减）`)
      invalidateTransfer()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  // 到货登记：POST /api/transfers/{id}/arrive（TRANSFERRING→AWAITING_RECEIPT，无库存动作）
  const arriveMutation = useMutation({
    mutationFn: (id: TransferOrder['id']) => transferApi.arrive(id),
    onSuccess: (detail) => {
      messageApi.success(`调拨单 ${detail.order.transfer_no} 已到货登记（调拨中 → 待入库）`)
      invalidateTransfer()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })
  // 确认入库：POST /api/transfers/{id}/receive（AWAITING_RECEIPT→COMPLETED + 逐行 TransferIn）
  const receiveMutation = useMutation({
    mutationFn: (id: TransferOrder['id']) => transferApi.receive(id),
    onSuccess: (detail) => {
      messageApi.success(`调拨单 ${detail.order.transfer_no} 已确认入库（待入库 → 已完成，目标仓已入账）`)
      invalidateTransfer()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  /** 动作可用性守卫（canAccess fail-closed + 状态机前置态，§21.7 给出具体原因） */
  const actionDisabledReason = (action: 'outbound' | 'arrive' | 'receive') => {
    if (order == null) return '先从左侧选择一张调拨单卡'
    if (!canExecute) return `缺少 ${TRANSFER_EXECUTE_PERMISSION} 权限，无法执行调拨动作`
    const requireStatus: Record<typeof action, { status: TransferStatus; hint: string }> = {
      outbound: { status: 'APPROVED', hint: '仅「待出库」状态可发起出库' },
      arrive: { status: 'TRANSFERRING', hint: '仅「调拨中」状态可到货登记' },
      receive: { status: 'AWAITING_RECEIPT', hint: '仅「待入库」状态可确认入库' },
    }
    const { status, hint } = requireStatus[action]
    if (order.status !== status) return `${hint}（当前 ${TRANSFER_STATUS_TAG[order.status]?.label ?? order.status}）`
    return undefined
  }

  const flowNode = (
    <section className="sf-pad-card" aria-label="状态流转">
      <h3 className="sf-pad-card-title">状态流转（只读跟踪）</h3>
      {order == null ? (
        <SfEmpty description="从左侧选择一张调拨单卡，此处展示其七态状态机位置" />
      ) : order.status === 'CANCELLED' ? (
        <>
          <TransferStatusTag status={order.status} />
          <p className="sf-pad-muted-note">该调拨单已取消（任一环节可取消，business-flow.md §10.1）；主链状态仅作流程参照</p>
        </>
      ) : (
        <div className="sf-pad-tr-flow">
          {TRANSFER_FLOW.map((status, index) => {
            const currentIndex = TRANSFER_FLOW.indexOf(order.status)
            const rowState = index < currentIndex ? ' sf-pad-tr-flow-row--done' : index === currentIndex ? ' sf-pad-tr-flow-row--current' : ''
            return (
              <div key={status} className={`sf-pad-tr-flow-row${rowState}`}>
                <span className="sf-pad-tr-flow-index">{String(index + 1).padStart(2, '0')}</span>
                <TransferStatusTag status={status} />
                {index === currentIndex && <span className="sf-pad-tr-flow-current-flag">当前</span>}
              </div>
            )
          })}
        </div>
      )}
      <p className="sf-pad-muted-note">
        状态机：草稿 → 待审核 → 待出库 → 调拨中 → 待入库 → 已完成，任一环节可已取消（business-flow.md §10.1）
      </p>
    </section>
  )

  const actionNode = (
    <section className="sf-pad-card" aria-label="调拨动作">
      <h3 className="sf-pad-card-title">调拨动作</h3>
      <div className="sf-pad-tr-action-wrap">
        <TransferAction
          label="发起出库"
          disabledReason={actionDisabledReason('outbound')}
          loading={outboundMutation.isPending}
          onClick={() => order && outboundMutation.mutate(order.id)}
        />
        <TransferAction
          label="到货登记"
          disabledReason={actionDisabledReason('arrive')}
          loading={arriveMutation.isPending}
          onClick={() => order && arriveMutation.mutate(order.id)}
        />
        <TransferAction
          label="确认入库"
          disabledReason={actionDisabledReason('receive')}
          loading={receiveMutation.isPending}
          onClick={() => order && receiveMutation.mutate(order.id)}
        />
      </div>
      <p className="sf-pad-muted-note">
        出库 / 入库执行需 stockops:transfer:execute 权限并按状态机前置态启用；
        到货登记（调拨中 → 待入库）与收货入库（待入库 → 已完成）为两步迁移（transfer.go:39-41），到货无库存动作
      </p>
    </section>
  )

  const detailNode = order ? (
    <>
      <PadInfoCard
        title={`调拨单信息 · ${order.transfer_no}`}
        items={[
          { label: '调拨单号', value: order.transfer_no },
          { label: '类型', value: TRANSFER_TYPE_LABEL[order.type] ?? order.type },
          { label: '状态', value: <TransferStatusTag status={order.status} /> },
          { label: '源仓库', value: warehouseNameOf(order.from_warehouse_id) },
          { label: '目标仓库', value: warehouseNameOf(order.to_warehouse_id) },
          { label: '审核时间', value: formatDateTime(order.approved_at) },
          { label: '出库时间', value: formatDateTime(order.outbound_at) },
          { label: '入库时间', value: formatDateTime(order.received_at) },
          ...(order.cancelled_at ? [{ label: '取消时间', value: formatDateTime(order.cancelled_at) }] : []),
          { label: '创建时间', value: formatDateTime(order.created_at) },
          { label: '备注', value: order.remark || EMPTY_TEXT },
        ]}
      />
      {skuOptions.error && <p className="sf-pad-muted-note">SKU 基础资料加载失败：明细编码以 ID 降级展示</p>}
      <section className="sf-pad-card" aria-label="调拨明细">
        <h3 className="sf-pad-card-title">调拨明细（{lines.length} 行）</h3>
        {detailQuery.isPending ? (
          <SfLoading rows={4} />
        ) : detailQuery.error ? (
          <SfError
            error={detailQuery.error}
            description="调拨单明细（GET /api/transfers/{id}）加载失败"
            onRetry={() => void detailQuery.refetch()}
          />
        ) : lines.length === 0 ? (
          <SfEmpty description="该调拨单暂无明细行" />
        ) : (
          <div className="sf-pad-tasks-cards">
            {lines.map((line) => (
              <div key={String(line.id)} className="sf-pad-task-card">
                <div className="sf-pad-task-card__head">
                  <span className="sf-pad-task-card__no">
                    行 {line.line_no} · {skuMaps.code.get(String(line.sku_id)) ?? `#${String(line.sku_id)}`}
                  </span>
                  {line.in_transit_qty > 0 && <span>在途 {formatNumber(line.in_transit_qty)}</span>}
                </div>
                <div className="sf-pad-task-card__meta">
                  <span>{String(line.batch_id) === '0' ? '非批次' : `批次 #${String(line.batch_id)}`}</span>
                  <span>
                    {binCodeOf(line.from_bin_id)} → {binCodeOf(line.to_bin_id)}
                  </span>
                </div>
                <div className="sf-pad-task-card__qty">
                  <span className="sf-pad-metric">{formatNumber(line.qty)}</span>
                  <span className="sf-pad-task-card__qty-unit">
                    已出 {formatNumber(line.qty_out)} · 已入 {formatNumber(line.qty_in)}
                  </span>
                </div>
                {line.remark && <div className="sf-pad-task-card__foot"><span>{line.remark}</span></div>}
              </div>
            ))}
          </div>
        )}
        <p className="sf-pad-muted-note">
          在途数量 = 已出库 − 已入库，由后端计算下发（TransferItemView.in_transit_qty），前端不算库存
        </p>
      </section>
    </>
  ) : (
    <SfEmpty description="从左侧选择一张调拨单卡，此处展示两仓库 / 明细行 / 在途进度等单据信息" />
  )

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="调拨类型筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={typeFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleFilter(() => setTypeFilter('all'))}
          >
            全部类型
          </Button>
          {TYPE_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={typeFilter === option.value ? 'primary' : 'default'}
              onClick={() => handleFilter(() => setTypeFilter(option.value))}
            >
              {option.label}
            </Button>
          ))}
        </div>
        <div className="sf-pad-chip-row" role="group" aria-label="调拨状态筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={statusFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleFilter(() => setStatusFilter('all'))}
          >
            全部状态
          </Button>
          {STATUS_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={statusFilter === option.value ? 'primary' : 'default'}
              onClick={() => handleFilter(() => setStatusFilter(option.value))}
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
          onRetry={() => void list.refetch()}
          description="调拨单列表（GET /api/transfers）加载失败"
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有调拨单，试试切换类型或状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((item) => (
            <TransferDocCard
              key={String(item.id)}
              item={item}
              fromWarehouseName={warehouseNameOf(item.from_warehouse_id)}
              toWarehouseName={warehouseNameOf(item.to_warehouse_id)}
              selected={order != null && String(order.id) === String(item.id)}
              onClick={() => setSelected(item)}
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

  const actionBar = <PadActionBar hideSlots={['pause', 'finish']} onScanSubmit={handleCodeSubmit} />

  return (
    <>
      {messageContext}
      {orientation === 'landscape' ? (
        // 横屏三栏：左调拨单列表 + 筛选 / 中单据信息 + 明细 / 右状态流转只读卡 + 执行动作
        <>
          <PadPageShell ratios={[34, 30, 36]} tasksSlot={listNode} contentSlot={detailNode} actionSlot={<>{flowNode}{actionNode}</>} />
          {actionBar}
        </>
      ) : (
        // 竖屏堆叠：顶部当前调拨单（信息 + 明细 + 流转 + 动作）→ 筛选与列表 → 底部 [返回][扫码][异常]
        <>
          <PadPageShell
            ratios={[34, 30, 36]}
            tasksSlot={
              <>
                {order ? (
                  <div className="sf-pad-tasks-detail">
                    {detailNode}
                    {flowNode}
                    {actionNode}
                  </div>
                ) : (
                  <SfEmpty description="在下方列表选择一张调拨单卡，单据信息与状态流转将置顶展示" />
                )}
                {listNode}
              </>
            }
            contentSlot={null}
          />
          {actionBar}
        </>
      )}
    </>
  )
}
