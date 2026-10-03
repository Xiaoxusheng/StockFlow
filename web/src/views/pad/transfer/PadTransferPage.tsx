import { useMemo, useState } from 'react'
import { Button, message } from 'antd'
import { usePagedList } from '@/hooks/usePagedList'
import { transferApi, TRANSFER_STATUS_TAG, type TransferItem, type TransferQuery, type TransferStatus, type TransferType } from '@/api/transfer'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import { TransferDocCard } from './TransferDocCard'
import './transfer.css'

/** 类型 chip（business-flow.md §10.1 两种维度；文案与 PC 端 TransferPage 同源） */
const TYPE_OPTIONS: Array<{ label: string; value: TransferType }> = [
  { label: '跨仓调拨', value: 'warehouse' },
  { label: '库位调拨', value: 'bin' },
]

/** 状态 chip（七态顺序即状态机主链顺序；label 取 api/transfer.ts TRANSFER_STATUS_TAG） */
const STATUS_OPTIONS: Array<{ label: string; value: TransferStatus }> = (
  Object.keys(TRANSFER_STATUS_TAG) as TransferStatus[]
).map((value) => ({ value, label: TRANSFER_STATUS_TAG[value].label }))

/** 主链流转顺序（business-flow.md §10.1：草稿→待审核→待出库→调拨中→待入库→已完成；任一环节可已取消） */
const TRANSFER_FLOW: TransferStatus[] = [
  'draft',
  'pending_review',
  'pending_outbound',
  'transferring',
  'pending_inbound',
  'completed',
]

/** 出库发起 / 入库确认动作占位口径：执行端点未交付，交付前不发起真实写请求（requirements.md §10） */
const ACTION_DISABLED_REASON = '调拨出库 / 入库执行端点未交付（GET /api/transfers 亦为前端先行契约），交付后接线'

/**
 * Pad 调拨页（仓库主管查看 / 跟踪视角，devices.md §12 Pad 职能）：
 * - 横屏三栏：左=调拨单卡片列表（transferApi.list 前端先行契约，错误态统一呈现）+ 类型/状态 chip 筛选，
 *   中=单据信息 PadInfoCard，右=七态状态流转只读卡（全部经 SfStatusTag）+ 出库/入库动作占位；
 * - 竖屏堆叠：当前调拨单（信息 + 流转）→ 筛选与列表 → PadActionBar [返回][扫码][异常]。
 * 本页只读跟踪，不发起任何调拨写请求；动作按钮 disabled 占位并注明原因（死按钮门禁）。
 */
export default function PadTransferPage() {
  const orientation = usePadOrientation()
  const [messageApi, messageContext] = message.useMessage()
  const [typeFilter, setTypeFilter] = useState<TransferType | 'all'>('all')
  const [statusFilter, setStatusFilter] = useState<TransferStatus | 'all'>('all')
  const [selected, setSelected] = useState<TransferItem | null>(null)

  const list = usePagedList<TransferItem, TransferQuery>({
    queryKey: ['pad', 'transfer', 'list'],
    fetch: (q) => transferApi.list(q),
    params: useMemo<TransferQuery>(
      () => ({
        transferType: typeFilter === 'all' ? undefined : typeFilter,
        status: statusFilter === 'all' ? undefined : statusFilter,
      }),
      [typeFilter, statusFilter],
    ),
    defaultPageSize: 20,
  })
  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const handleFilter = (apply: () => void) => {
    apply()
    setSelected(null)
    list.resetToFirstPage()
  }

  /** 扫码 / 手输兜底：在当前已加载调拨单中按单号选中（不做不存在的“直达详情”假动作） */
  const handleCodeSubmit = (code: string) => {
    const value = code.trim()
    const hit = list.items.find((item) => item.transferNo === value)
    if (hit) {
      setSelected(hit)
      messageApi.info(`已选中调拨单 ${hit.transferNo}`)
    } else {
      messageApi.warning(`当前列表未加载单号「${value}」；扫码直达单据属 Scan 端通用扫码中心（frontend.md §21.5）`)
    }
  }

  const flowNode = (
    <section className="sf-pad-card" aria-label="状态流转">
      <h3 className="sf-pad-card-title">状态流转（只读跟踪）</h3>
      {selected == null ? (
        <SfEmpty description="从左侧选择一张调拨单卡，此处展示其七态状态机位置" />
      ) : selected.status === 'cancelled' ? (
        <>
          <SfStatusTag status={selected.status} />
          <p className="sf-pad-muted-note">该调拨单已取消（任一环节可取消，business-flow.md §10.1）；主链状态仅作流程参照</p>
        </>
      ) : (
        <div className="sf-pad-tr-flow">
          {TRANSFER_FLOW.map((status, index) => {
            const currentIndex = TRANSFER_FLOW.indexOf(selected.status)
            const rowState = index < currentIndex ? ' sf-pad-tr-flow-row--done' : index === currentIndex ? ' sf-pad-tr-flow-row--current' : ''
            return (
              <div key={status} className={`sf-pad-tr-flow-row${rowState}`}>
                <span className="sf-pad-tr-flow-index">{String(index + 1).padStart(2, '0')}</span>
                <SfStatusTag status={status} />
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
        {/* disabled 占位：外包 span 接管点按，把原因以 Toast 送达（与 PadActionBar 同门禁口径） */}
        <span className="sf-pad-tr-action-wrap" style={{ flex: 1 }} onClick={() => messageApi.warning(ACTION_DISABLED_REASON)}>
          <Button block size="large" className="sf-pad-tr-action-btn" type="primary" disabled>
            发起出库
          </Button>
        </span>
        <span className="sf-pad-tr-action-wrap" style={{ flex: 1 }} onClick={() => messageApi.warning(ACTION_DISABLED_REASON)}>
          <Button block size="large" className="sf-pad-tr-action-btn" disabled>
            确认入库
          </Button>
        </span>
      </div>
      <p className="sf-pad-muted-note">
        调拨出库 / 入库执行动作端点未交付：按钮为 disabled 占位并注明原因，交付后接线（不发起真实写请求）。
      </p>
    </section>
  )

  const detailNode = selected ? (
    <PadInfoCard
      title={`调拨单信息 · ${selected.transferNo}`}
      items={[
        { label: '调拨单号', value: selected.transferNo },
        { label: '类型', value: selected.transferType === 'warehouse' ? '跨仓调拨' : '库位调拨' },
        { label: '状态', value: <SfStatusTag status={selected.status} /> },
        { label: '源仓库', value: selected.sourceWarehouseName || EMPTY_TEXT },
        { label: '目标仓库', value: selected.targetWarehouseName || EMPTY_TEXT },
        { label: '源库位', value: selected.sourceBinCode ?? EMPTY_TEXT },
        { label: '目标库位', value: selected.targetBinCode ?? EMPTY_TEXT },
        { label: 'SKU 编码', value: selected.skuCode },
        { label: '商品名称', value: selected.productName },
        { label: '批次号', value: selected.batchNo ?? EMPTY_TEXT },
        { label: '调拨数量', value: formatNumber(selected.qty), emphasis: true },
        { label: '创建人', value: selected.createdByName ?? EMPTY_TEXT },
        { label: '创建时间', value: formatDateTime(selected.createdAt) },
        ...(selected.completedAt ? [{ label: '完成时间', value: formatDateTime(selected.completedAt) }] : []),
      ]}
    />
  ) : (
    <SfEmpty description="从左侧选择一张调拨单卡，此处展示两仓库 / 商品 / 批次 / 数量等单据信息" />
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
          description="GET /api/transfers 为前端先行契约，后端交付前呈统一错误态，属预期行为"
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有调拨单，试试切换类型或状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((item) => (
            <TransferDocCard
              key={String(item.id)}
              item={item}
              selected={selected != null && String(selected.id) === String(item.id)}
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
        // 横屏三栏：左调拨单列表 + 筛选 / 中单据信息 / 右状态流转只读卡 + 动作占位
        <>
          <PadPageShell ratios={[34, 30, 36]} tasksSlot={listNode} contentSlot={detailNode} actionSlot={<>{flowNode}{actionNode}</>} />
          {actionBar}
        </>
      ) : (
        // 竖屏堆叠：顶部当前调拨单（信息 + 流转 + 动作占位）→ 筛选与列表 → 底部 [返回][扫码][异常]
        <>
          <PadPageShell
            ratios={[34, 30, 36]}
            tasksSlot={
              <>
                {selected ? (
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
