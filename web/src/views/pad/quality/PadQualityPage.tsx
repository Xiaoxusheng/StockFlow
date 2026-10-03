import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Button, Input, Modal, Select, message } from 'antd'
import {
  ArrowLeftOutlined,
  CameraOutlined,
  CheckOutlined,
  PauseOutlined,
  ScanOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router'
import type {
  QualityDisposition,
  QualityInspectionItem,
  QualityInspectionQuery,
  QualityResult,
} from '@/api/quality'
import { DISPOSITION_TAG_FALLBACK, INSPECTION_METHOD_LABEL, qualityApi } from '@/api/quality'
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

/** 检验结果 chip（QualityResult 前端先行契约，文案取 types/status.ts 注册表） */
const RESULT_OPTIONS: Array<{ label: string; value: QualityResult }> = (
  ['qualified', 'partially_qualified', 'unqualified'] as const
).map((value) => ({ value, label: STATUS_META[value]?.label ?? value }))

/** 处置结果九值选项（business-flow.md §4.3；文案注册表优先，六种处置值回退 DISPOSITION_TAG_FALLBACK） */
const DISPOSITION_OPTIONS: Array<{ label: string; value: QualityDisposition }> = (
  [
    'qualified',
    'partially_qualified',
    'unqualified',
    'return_supplier',
    'scrap',
    'rework',
    'downgrade',
    'to_defective_warehouse',
    'special_release',
  ] as const
).map((value) => ({
  value,
  label: STATUS_META[value]?.label ?? DISPOSITION_TAG_FALLBACK[value]?.label ?? value,
}))

/** 质检结果提交端点未冻结：操作按钮统一禁用原因（无死按钮门禁） */
const SUBMIT_DISABLED_REASON = '质检结果提交端点后端未冻结：M2 质量域冻结后接线'

/** 质检单任务卡：inspectionNo/skuCode/productName/检验方式/检验·合格数量/result，
 * 复用 sf-pad-task-card 卡片基元与 ≥64px 热区（frontend.md §20.2/§20.9） */
function InspectionTaskCard({
  inspection,
  selected = false,
  onClick,
}: {
  inspection: QualityInspectionItem
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
        <span className="sf-pad-task-card__no">{inspection.inspectionNo}</span>
        <SfStatusTag status={inspection.result} />
      </div>
      <div className="sf-pad-task-card__meta">
        <span>{inspection.skuCode}</span>
        <span>{inspection.productName}</span>
      </div>
      <div className="sf-pad-task-card__qty">
        <span className="sf-pad-metric">{formatNumber(inspection.qualifiedQty)}</span>
        <span className="sf-pad-task-card__qty-unit">/ {formatNumber(inspection.inspectQty)} 合格</span>
      </div>
      <div className="sf-pad-task-card__foot">
        <span>
          {INSPECTION_METHOD_LABEL[inspection.inspectionMethod] ?? inspection.inspectionMethod} · 不合格{' '}
          {formatNumber(inspection.unqualifiedQty)}
        </span>
        <span>{formatDateTime(inspection.createdAt)}</span>
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

/**
 * Pad 质检页（frontend.md §20.3 三栏 / §20.4 竖屏；business-flow.md §4 / §21.4 质检要点）：
 * - 左栏：qualityApi.inspections（前端先行契约）→ InspectionTaskCard 列表 + 结果 chip 筛选；
 * - 中栏：选中质检单 PadInfoCard（SKU / 名称 / 批次 / 检验方式 / 检验·合格·不合格数量）；
 * - 右栏：合格 / 部分合格 / 不合格三个大按钮（提交端点未冻结，disabled 占位注明）+
 *   处置与原因录入占位 + PadScanStub + 不合格拍照入口占位（§20.6）；
 * - 竖屏：顶部当前质检任务卡 → 商品信息 → 任务列表滚动区 → 底部 PadActionBar。
 * result / 处置展示一律经 SfStatusTag（§24），不自定颜色。
 */
export default function PadQualityPage() {
  const orientation = usePadOrientation()
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const [resultFilter, setResultFilter] = useState<QualityResult | 'all'>('all')
  const [selected, setSelected] = useState<QualityInspectionItem | null>(null)
  const [scanOpen, setScanOpen] = useState(false)

  const params = useMemo<QualityInspectionQuery>(
    () => ({ result: resultFilter === 'all' ? undefined : resultFilter }),
    [resultFilter],
  )

  const list = usePagedList<QualityInspectionItem, QualityInspectionQuery>({
    queryKey: ['pad', 'quality-inspections'],
    fetch: (q) => qualityApi.inspections(q),
    params,
    defaultPageSize: 30,
  })

  const handleSelect = (inspection: QualityInspectionItem) => {
    setSelected(inspection)
  }

  const handleFilter = (value: QualityResult | 'all') => {
    setResultFilter(value)
    setSelected(null)
    list.resetToFirstPage()
  }

  const handleScanSubmit = (code: string) => {
    // 扫码链路属 Scan 端 / F16（frontend.md §19.2）：本轮仅本地接收手输编码
    messageApi.info(`已接收手输编码：${code}（质检扫码校验 M2 质量域冻结后接线）`)
    setScanOpen(false)
  }

  const infoNode = selected ? (
    <PadInfoCard
      title={`质检单 · ${selected.inspectionNo}`}
      items={[
        { label: '质检单号', value: selected.inspectionNo },
        { label: '结果', value: <SfStatusTag status={selected.result} /> },
        { label: 'SKU', value: selected.skuCode },
        { label: '商品名称', value: selected.productName },
        { label: '批次号', value: selected.batchNo ?? EMPTY_TEXT },
        {
          label: '检验方式',
          value: INSPECTION_METHOD_LABEL[selected.inspectionMethod] ?? selected.inspectionMethod,
        },
        { label: '来源单号', value: selected.sourceNo ?? EMPTY_TEXT },
        { label: '检验人', value: selected.inspectorName ?? EMPTY_TEXT },
        {
          label: '检验时间',
          value: selected.inspectedAt ? formatDateTime(selected.inspectedAt) : EMPTY_TEXT,
        },
        { label: '检验数量', value: formatNumber(selected.inspectQty), emphasis: true },
        { label: '合格数量', value: formatNumber(selected.qualifiedQty), emphasis: true },
        { label: '不合格数量', value: formatNumber(selected.unqualifiedQty), emphasis: true },
      ]}
    />
  ) : (
    <SfEmpty description="从质检单列表选择一张任务卡，此处展示 SKU / 批次 / 检验方式 / 检验数量" />
  )

  const actionNode = (
    <>
      <section className="sf-pad-card" aria-label="质检判定">
        <h3 className="sf-pad-card-title">质检判定</h3>
        {selected ? (
          <>
            <DisabledAction label="合格" reason={SUBMIT_DISABLED_REASON} />
            <DisabledAction label="部分合格" reason={SUBMIT_DISABLED_REASON} />
            <DisabledAction label="不合格" reason={SUBMIT_DISABLED_REASON} />
            <p className="sf-pad-muted-note">
              检验结果值域：合格 / 部分合格 / 不合格（business-flow.md §4.2），提交端点 M2 质量域冻结后启用
            </p>
          </>
        ) : (
          <SfEmpty description="选择质检单后在此判定合格 / 部分合格 / 不合格" />
        )}
      </section>

      <section className="sf-pad-card" aria-label="处置与原因录入占位">
        <h3 className="sf-pad-card-title">处置与原因</h3>
        <Select
          size="large"
          placeholder="处理结果（退供应商 / 报废 / 返工 / 降级 / 转不良品仓 / 特批放行…）"
          disabled
          options={DISPOSITION_OPTIONS}
          style={{ width: '100%', height: 'var(--sf-pad-touch-min)' }}
        />
        <Input.TextArea
          size="large"
          placeholder="不合格原因 / 备注"
          disabled
          rows={3}
          style={{ minHeight: 'var(--sf-pad-touch-min)' }}
        />
        <p className="sf-pad-muted-note">
          处置与原因录入（business-flow.md §4.3）随质检结果提交端点一并启用，当前为占位
        </p>
      </section>

      <section className="sf-pad-card" aria-label="扫码占位">
        <h3 className="sf-pad-card-title">扫码</h3>
        <PadScanStub
          placeholder="手工输入质检单 / SKU 条码兜底"
          onSubmit={(code) =>
            messageApi.info(`已接收手输编码：${code}（质检扫码校验 M2 质量域冻结后接线）`)
          }
        />
      </section>

      <section className="sf-pad-card" aria-label="不合格拍照">
        <h3 className="sf-pad-card-title">不合格拍照</h3>
        <DisabledAction
          label="拍照留证（不合格）"
          icon={<CameraOutlined />}
          reason="质检拍照上传属文件域与 Scan 端集成（frontend.md §20.6），接线前为占位"
        />
      </section>
    </>
  )

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="检验结果筛选">
          <Button
            size="large"
            className="sf-pad-chip"
            type={resultFilter === 'all' ? 'primary' : 'default'}
            onClick={() => handleFilter('all')}
          >
            全部
          </Button>
          {RESULT_OPTIONS.map((option) => (
            <Button
              key={option.value}
              size="large"
              className="sf-pad-chip"
              type={resultFilter === option.value ? 'primary' : 'default'}
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
          description="质检单列表为前端先行契约（GET /api/quality/inspections），M2 质量域冻结后可用"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有质检单，试试切换结果筛选" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((inspection) => (
            <InspectionTaskCard
              key={inspection.id}
              inspection={inspection}
              selected={selected?.id === inspection.id}
              onClick={() => handleSelect(inspection)}
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
        // 竖屏（§20.4）：顶部当前质检任务卡 → 商品信息 → 任务列表滚动区，底部 PadActionBar
        <PadPageShell
          tasksSlot={
            <>
              {selected && <InspectionTaskCard inspection={selected} selected />}
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
            disabledReason: SUBMIT_DISABLED_REASON,
          },
        ]}
      />
      <Modal title="扫码" open={scanOpen} footer={null} centered onCancel={() => setScanOpen(false)}>
        <PadScanStub onSubmit={handleScanSubmit} placeholder="手工输入质检单 / SKU 条码兜底" />
      </Modal>
    </>
  )
}
