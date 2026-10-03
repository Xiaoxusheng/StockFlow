import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, Modal, Steps, message } from 'antd'
import {
  ArrowLeftOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { InboundDetailItem, InboundItem, InboundQuery, InboundStatus } from '@/api/inbound'
import { inboundApi } from '@/api/inbound'
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

/** 入库类型文案（business-flow.md §3.1；api/inbound.ts InboundType 无导出标签映射，此处页面级兜底，未知值回退原始值） */
const INBOUND_TYPE_LABEL: Record<string, string> = {
  purchase: '采购入库',
  production: '生产入库',
  sales_return: '销售退货入库',
  transfer: '调拨入库',
  other: '其他入库',
}

/** 上架任务源状态 chip（入库单状态；默认筛「待上架」，文案取 types/status.ts 注册表） */
const STATUS_OPTIONS: Array<{ label: string; value: InboundStatus }> = (
  ['pending_putaway', 'putaway_completed'] as const
).map((value) => ({ value, label: STATUS_META[value]?.label ?? value }))

/** 上架执行确认端点未交付：统一禁用原因（无死按钮门禁） */
const CONFIRM_DISABLED_REASON = '上架执行确认端点未交付：上架作业域后端未交付，M2 冻结后接线'

/** 上架任务卡：任务源 = 入库单（inboundApi.list），字段 inboundNo/类型/来源单号/仓库/已收·计划数量/状态，
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9） */
function InboundTaskCard({
  inbound,
  selected = false,
  onClick,
}: {
  inbound: InboundItem
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
        <span className="sf-pad-task-card__no">{inbound.inboundNo}</span>
        <SfStatusTag status={inbound.status} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{INBOUND_TYPE_LABEL[inbound.inboundType] ?? inbound.inboundType}</span>
        {inbound.sourceNo && <span>{inbound.sourceNo}</span>}
        <span>{inbound.warehouseName}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(inbound.receivedQty)}</span>
        <span className="sf-pad-task-card__qty-unit">/ {formatNumber(inbound.totalQty)} 已收</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>{inbound.operatorName ?? EMPTY_TEXT}</span>
        <span>{formatDateTime(inbound.createdAt)}</span>
      </div>
    </div>
  )
}

/** 禁用动作按钮（死按钮门禁口径，frontend.md §9.1）：外包 span 接管点按，Toast 送达禁用原因 */
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

/** 三步序列（frontend.md §21.4 上架：扫商品 → 扫库位 → 系统校验 → 完成） */
const STEP_TITLES = [{ title: '扫商品' }, { title: '扫库位' }, { title: '系统校验 · 完成' }]

/**
 * Pad 上架页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §5 / §21.4 上架要点）：
 * - 左栏：上架任务卡列表——任务源复用既有 inboundApi.list（前端先行契约），默认筛「待上架」态；
 * - 中栏：选中入库单 PadInfoCard（SKU / 应上架数量 / 目标库位），多明细时 chip 切换当前明细；
 * - 右栏：三步序列操作区（步骤指示 Steps + PadScanStub 手输兜底 + 本地比对校验），
 *   [完成上架] 确认按钮 disabled 占位注明「上架作业域后端未交付」；
 * - 竖屏：顶部当前上架任务卡 → 商品信息 / 步骤指示 → 任务列表滚动区 → 底部 PadActionBar。
 * 上架页面不做 CRUD 表格（frontend.md §30.1）；上架执行动作待 M2 冻结后接线。
 */
export default function PadPutawayPage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const [statusFilter, setStatusFilter] = useState<InboundStatus | 'all'>('pending_putaway')
  const [selected, setSelected] = useState<InboundItem | null>(null)
  const [lineId, setLineId] = useState<InboundDetailItem['id'] | null>(null)
  const [step, setStep] = useState(0)
  const [scanOpen, setScanOpen] = useState(false)

  const params = useMemo<InboundQuery>(
    () => ({ status: statusFilter === 'all' ? undefined : statusFilter }),
    [statusFilter],
  )

  const list = usePagedList<InboundItem, InboundQuery>({
    queryKey: ['pad', 'putaway-inbounds'],
    fetch: (q) => inboundApi.list(q),
    params,
    defaultPageSize: 30,
  })

  // 任务详情：inboundApi.get（前端先行契约），选中任务后拉取
  const detailQuery = useQuery({
    queryKey: ['pad', 'putaway', 'inbound', selected?.id],
    queryFn: () => {
      if (!selected) throw new Error('未选择入库单')
      return inboundApi.get(selected.id)
    },
    enabled: selected != null,
  })

  const items = useMemo(() => detailQuery.data?.items ?? [], [detailQuery.data])
  const line = items.find((item) => item.id === lineId) ?? items[0]

  // 切换任务 / 明细时重置三步序列
  useEffect(() => {
    setStep(0)
  }, [selected?.id, lineId])

  const handleSelect = (inbound: InboundItem) => {
    setSelected(inbound)
    setLineId(null)
    setStep(0)
  }

  const handleFilter = (value: InboundStatus | 'all') => {
    setStatusFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  /** 扫码手输兜底的本地比对（§21.4 序列演示；真实扫码属 Scan 端 / F16，
   * 上架执行以后端强校验为准，错误按 §21.7 给出具体原因） */
  const handleScan = (code: string) => {
    if (!line) return
    const value = code.trim()
    if (step === 0) {
      if (value === line.skuCode) {
        setStep(1)
        messageApi.success(`✓ ${line.skuCode} 商品匹配，请扫目标库位`)
      } else {
        messageApi.error(`商品不匹配：当前明细 ${line.skuCode}，扫描结果 ${value}`)
      }
      return
    }
    if (step === 1) {
      if (!line.binCode) {
        messageApi.warning('该明细无推荐库位：推荐库位算法（business-flow.md §5.3）后端未交付，无法校验')
        return
      }
      if (value === line.binCode) {
        setStep(2)
        messageApi.success(`✓ 校验通过：${line.skuCode} → ${line.binCode}`)
      } else {
        messageApi.error(`库位不匹配：目标库位 ${line.binCode}，扫描结果 ${value}`)
      }
      return
    }
    messageApi.info('三步校验已完成：确认上架待后端端点接线')
  }

  const handleScanSubmit = (code: string) => {
    handleScan(code)
    setScanOpen(false)
  }

  const infoNode = !selected ? (
    <SfEmpty description="从上架任务列表选择一张入库单任务卡，此处展示 SKU / 应上架数量 / 目标库位" />
  ) : detailQuery.isPending ? (
    <SfLoading rows={4} />
  ) : detailQuery.error ? (
    <SfError
      error={detailQuery.error}
      description="入库单详情为前端先行契约（GET /api/inbounds/{id}），M2 入库域冻结后可用"
      onRetry={() => void detailQuery.refetch()}
    />
  ) : (
    <>
      <PadInfoCard
        title={`上架任务 · ${detailQuery.data.inboundNo}`}
        items={[
          { label: '入库单号', value: detailQuery.data.inboundNo },
          {
            label: '入库类型',
            value: INBOUND_TYPE_LABEL[detailQuery.data.inboundType] ?? detailQuery.data.inboundType,
          },
          { label: '来源单号', value: detailQuery.data.sourceNo ?? EMPTY_TEXT },
          { label: '仓库', value: detailQuery.data.warehouseName },
          { label: '状态', value: <SfStatusTag status={detailQuery.data.status} /> },
          { label: 'SKU', value: line?.skuCode ?? EMPTY_TEXT },
          { label: '商品名称', value: line?.skuName ?? EMPTY_TEXT },
          { label: '批次', value: line?.batchNo ?? EMPTY_TEXT },
          {
            label: '应上架数量',
            value: formatNumber(line ? (line.receivedQty ?? line.totalQty) : undefined),
            emphasis: true,
          },
          { label: '目标库位', value: line?.binCode ?? '待推荐（推荐库位后端未交付）' },
        ]}
      />
      {items.length > 1 && (
        <div className="sf-pad-filter-block">
          <div className="sf-pad-chip-row" role="group" aria-label="切换当前明细 SKU">
            {items.map((item) => (
              <Button
                key={item.id}
                size="large"
                className="sf-pad-chip"
                type={line?.id === item.id ? 'primary' : 'default'}
                onClick={() => setLineId(item.id)}
              >
                {item.skuCode}
              </Button>
            ))}
          </div>
        </div>
      )}
      <p className="sf-pad-muted-note">
        应上架数量取明细已收货数量（缺失回退计划数量），字段语义待 M2 入库域冻结后回对；
        目标库位为推荐库位（business-flow.md §5.3），无推荐时待后端指定
      </p>
    </>
  )

  const stepsNode = (
    <section className="sf-pad-card" aria-label="上架三步序列">
      <h3 className="sf-pad-card-title">上架三步（扫商品 → 扫库位 → 系统校验）</h3>
      {!selected || !line ? (
        <SfEmpty
          description={
            selected ? '入库单明细加载后开始三步序列' : '选择入库单后按 扫商品 → 扫库位 → 系统校验 → 完成 执行上架'
          }
        />
      ) : (
        <>
          <Steps size="small" current={step} items={STEP_TITLES} />
          <PadScanStub
            placeholder={
              step === 0
                ? `第 1 步 · 手工输入 SKU（期望 ${line.skuCode}）兜底`
                : step === 1
                  ? `第 2 步 · 手工输入库位（期望 ${line.binCode ?? '待推荐'}）兜底`
                  : '三步校验已完成'
            }
            onSubmit={handleScan}
          />
          <p className="sf-pad-muted-note">
            三步序列见 frontend.md §21.4；当前校验为前端手输比对演示（扫码链路属 Scan 端 / F16），
            上架执行以后端强校验为准，确认按钮待上架作业域交付后启用
          </p>
          <DisabledAction label="完成上架" variant="primary" reason={CONFIRM_DISABLED_REASON} />
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
          description="上架任务源复用入库单列表（GET /api/inbounds，前端先行契约），M2 入库域冻结后可用"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有可作业的入库单，试试切换状态" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((inbound) => (
            <InboundTaskCard
              key={inbound.id}
              inbound={inbound}
              selected={selected?.id === inbound.id}
              onClick={() => handleSelect(inbound)}
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
        <PadPageShell tasksSlot={listNode} contentSlot={infoNode} actionSlot={stepsNode} />
      ) : (
        // 竖屏（§20.4）：顶部当前上架任务卡 → 商品信息 / 步骤指示 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {selected && <InboundTaskCard inbound={selected} selected />}
              {selected && <div className="sf-pad-tasks-detail">{infoNode}</div>}
              {stepsNode}
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
            disabledReason: CONFIRM_DISABLED_REASON,
          },
        ]}
      />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <PadScanStub
          onSubmit={handleScanSubmit}
          placeholder={line ? `手工输入 SKU / 库位（当前期望：${step === 0 ? line.skuCode : (line.binCode ?? '待推荐')}）兜底` : '手工输入 SKU / 库位兜底'}
        />
      </Modal>
    </>
  )
}
