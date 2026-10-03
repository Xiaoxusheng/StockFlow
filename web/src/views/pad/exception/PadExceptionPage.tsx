import { useMemo, useState } from 'react'
import { Button, message } from 'antd'
import {
  CameraOutlined,
  CheckOutlined,
  LockOutlined,
  SearchOutlined,
} from '@ant-design/icons'
import type { ExceptionItem, ExceptionQuery, ExceptionStatus, ExceptionType } from '@/api/exception'
import { EXCEPTION_STATUS_TAG, EXCEPTION_TYPE_LABEL, exceptionApi } from '@/api/exception'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'
import { ActionPlaceholder, ExceptionCard } from './ExceptionCards'

/** 九类异常 chip（business-flow.md §11.2；文案取 api/exception.ts EXCEPTION_TYPE_LABEL） */
const TYPE_OPTIONS = (Object.entries(EXCEPTION_TYPE_LABEL) as Array<[ExceptionType, string]>).map(
  ([value, label]) => ({ label, value }),
)

/** 七态生命周期 chip（business-flow.md §11.2；label 取 EXCEPTION_STATUS_TAG） */
const STATUS_OPTIONS = (
  Object.entries(EXCEPTION_STATUS_TAG) as Array<[ExceptionStatus, { label: string }]>
).map(([value, meta]) => ({ label: meta.label, value }))

/** 动作端点未交付的统一占位原因（认领/处理/关闭，M2 后接线；本轮不假装可用） */
const ACTION_DISABLED_REASON =
  '异常认领 / 处理 / 关闭端点尚未交付（business-flow.md §11.2 生命周期，M2 后接线），本轮为占位按钮'
const PHOTO_DISABLED_REASON =
  '拍照取证属 Pad 拍照链路（frontend.md §20.6 异常为拍照场景），本轮为占位入口，待设备/文件域交付后接线'

/**
 * Pad 异常页（/pad/exception，frontend.md §20.2/§20.3/§20.4 + business-flow.md §11.2）：
 * - 横屏三栏：左=异常卡列表（exceptionApi.list）+ 九类异常/生命周期大触摸 chip 筛选；
 *   中=异常详情 PadInfoCard（描述/来源单据/责任人/处理人）；右=处理操作区（认领/处理/关闭
 *   动作占位 + 拍照取证入口占位，端点未交付 disabled 注明，M2 后接线）。
 * - 竖屏堆叠：顶部当前异常卡（选中详情内联列表上方，同 PadTasksPage 模式）→ 详情/列表滚动区
 *   → 底部 PadActionBar [返回][扫码][异常]（查看为主，省略 [暂停][完成]）。
 * - 页面以查看为主；九类异常类型按普通文本、生命周期经 SfStatusTag；无假数据、动作不做假提交。
 */
export default function PadExceptionPage() {
  const orientation = usePadOrientation()
  const [messageApi, contextHolder] = message.useMessage()
  const [typeFilter, setTypeFilter] = useState<ExceptionType | 'all'>('all')
  const [statusFilter, setStatusFilter] = useState<ExceptionStatus | 'all'>('all')
  const [selectedId, setSelectedId] = useState<ExceptionItem['id'] | null>(null)

  const params = useMemo<ExceptionQuery>(
    () => ({
      exceptionType: typeFilter === 'all' ? undefined : typeFilter,
      status: statusFilter === 'all' ? undefined : statusFilter,
    }),
    [typeFilter, statusFilter],
  )

  const list = usePagedList<ExceptionItem, ExceptionQuery>({
    queryKey: ['pad', 'exceptions'],
    fetch: (q) => exceptionApi.list(q),
    params,
    defaultPageSize: 30,
  })

  const selected = list.items.find((item) => String(item.id) === String(selectedId)) ?? null

  const handleFilter = (apply: () => void) => {
    apply()
    setSelectedId(null)
    list.resetToFirstPage()
  }

  // 扫码 / 手输兜底：仅本地匹配当前列表（真实扫码属 Scan 端 / F16）
  const handleScanCode = (code: string) => {
    const key = code.trim().toUpperCase()
    const hit = list.items.find(
      (item) =>
        item.exceptionNo.toUpperCase() === key ||
        (item.bizNo ?? '').toUpperCase() === key ||
        (item.skuCode ?? '').toUpperCase() === key,
    )
    if (hit) {
      setSelectedId(hit.id)
      messageApi.success(`已定位异常单：${hit.exceptionNo}`)
    } else {
      messageApi.warning(`未在当前异常列表中找到「${code}」，请检查单号或手工点选`)
    }
  }

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const detailCard = selected ? (
    <PadInfoCard
      title={`异常详情 · ${selected.exceptionNo}`}
      items={[
        { label: '异常单号', value: selected.exceptionNo },
        { label: '生命周期', value: <SfStatusTag status={selected.status} /> },
        { label: '异常类型', value: EXCEPTION_TYPE_LABEL[selected.exceptionType] ?? selected.exceptionType },
        { label: '标题', value: selected.title },
        { label: '仓库', value: selected.warehouseName || EMPTY_TEXT },
        { label: 'SKU', value: selected.skuCode || EMPTY_TEXT },
        { label: '来源单据', value: selected.bizNo || EMPTY_TEXT },
        { label: '责任人', value: selected.ownerName || EMPTY_TEXT },
        { label: '处理人', value: selected.handlerName || EMPTY_TEXT },
        { label: '发现时间', value: formatDateTime(selected.discoveredAt) },
        ...(selected.resolvedAt
          ? [{ label: '解决时间', value: formatDateTime(selected.resolvedAt) }]
          : []),
        { label: '描述', value: selected.description || EMPTY_TEXT },
      ]}
      columns={2}
    />
  ) : null

  const listNode = (
    <div>
      <div className="sf-pad-filter-block">
        <div className="sf-pad-chip-row" role="group" aria-label="异常类型筛选（九类）">
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
        <div className="sf-pad-chip-row" role="group" aria-label="生命周期筛选（七态）">
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
          description="异常域接口（GET /api/exceptions）为前端先行契约，后端异常域交付前呈统一错误态"
          onRetry={() => void list.refetch()}
        />
      ) : list.items.length === 0 ? (
        <SfEmpty description="当前筛选条件下没有异常单，试试切换类型或生命周期" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {list.items.map((item) => (
            <ExceptionCard
              key={String(item.id)}
              item={item}
              selected={String(item.id) === String(selectedId)}
              onClick={() => setSelectedId(item.id)}
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

  // 右栏处理操作区：认领/处理/关闭动作占位 + 拍照取证入口占位（端点未交付，disabled 注明）
  const actionPanel = (
    <section className="sf-pad-card" aria-label="异常处理操作区">
      <h3 className="sf-pad-card-title">处理操作</h3>
      {!selected ? (
        <SfEmpty description="从左侧选择一张异常卡后，在此进行认领 / 处理 / 关闭" />
      ) : (
        <div style={{ display: 'grid', gap: 'var(--sf-space-2)' }}>
          <ActionPlaceholder
            label="认领异常"
            icon={<LockOutlined />}
            reason={ACTION_DISABLED_REASON}
            notify={(text) => messageApi.info(text)}
          />
          <ActionPlaceholder
            label="处理异常"
            icon={<SearchOutlined />}
            reason={ACTION_DISABLED_REASON}
            notify={(text) => messageApi.info(text)}
          />
          <ActionPlaceholder
            label="关闭异常"
            icon={<CheckOutlined />}
            reason={ACTION_DISABLED_REASON}
            notify={(text) => messageApi.info(text)}
          />
          <ActionPlaceholder
            label="拍照取证（占位）"
            icon={<CameraOutlined />}
            reason={PHOTO_DISABLED_REASON}
            notify={(text) => messageApi.info(text)}
          />
          <p className="sf-pad-muted-note" style={{ margin: 0 }}>
            处理动作端点 M2 后接线（business-flow.md §11.2：发现→创建→分派→处理中→待复核→已解决→已关闭）；
            本轮仅提供查看与筛选，不做假提交。
          </p>
        </div>
      )}
    </section>
  )

  const ratios: [number, number, number] = [38, 34, 28]

  return (
    <>
      {contextHolder}
      {orientation === 'landscape' ? (
        <PadPageShell ratios={ratios} tasksSlot={listNode} contentSlot={
          selected ? (
            detailCard
          ) : (
            <SfEmpty description="从左侧选择一张异常卡，此处展示异常详情" />
          )
        } actionSlot={actionPanel} />
      ) : (
        // 竖屏：顶部当前异常卡（选中详情内联在列表上方）→ 筛选 + 列表滚动区；动作占位并入详情下方
        <PadPageShell
          ratios={ratios}
          tasksSlot={
            <>
              {selected && (
                <div className="sf-pad-tasks-detail">
                  {detailCard}
                  {actionPanel}
                </div>
              )}
              {listNode}
            </>
          }
          contentSlot={
            selected ? null : <SfEmpty description="从异常列表点选一张异常卡，此处展示异常详情" />
          }
        />
      )}
      <PadActionBar hideSlots={['pause', 'finish']} onScanSubmit={handleScanCode} />
    </>
  )
}
