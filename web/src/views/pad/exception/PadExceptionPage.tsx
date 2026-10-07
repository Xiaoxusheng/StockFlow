import { useMemo, useRef, useState } from 'react'
import { Button, message } from 'antd'
import {
  CameraOutlined,
  CheckOutlined,
  LockOutlined,
  PlayCircleOutlined,
  RocketOutlined,
  CheckSquareOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ExceptionId, ExceptionItem, ExceptionQuery, ExceptionStatus, ExceptionType } from '@/api/exception'
import {
  EXCEPTION_ASSIGN_PERMISSION,
  EXCEPTION_CLOSE_PERMISSION,
  EXCEPTION_EXECUTE_PERMISSION,
  EXCEPTION_STATUS_TAG,
  EXCEPTION_TYPES,
  exceptionApi,
} from '@/api/exception'
import { resolveErrorMessage } from '@/api/client'
import { buildBinCodeMap, buildSkuMaps, fetchBinOptions, fetchSkuOptions, SKU_OPTIONS_KEY, BIN_OPTIONS_KEY } from '@/api/options'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { EMPTY_TEXT, formatDateTime } from '@/utils/format'
import {
  EXCEPTION_IMAGE_LIMIT,
  ExceptionImageStrip,
  exceptionImagesDisabledReason,
  uploadAndAttachExceptionImages,
} from '@/views/exception/exceptionImages'
import { ExceptionCard, ExceptionStatusTag, PadExceptionAction } from './ExceptionCards'

/** 九类异常 chip（后端中文值域即文案：internal/returns/models.go:48-51） */
const TYPE_OPTIONS: Array<{ label: string; value: ExceptionType }> = EXCEPTION_TYPES.map((value) => ({
  label: value,
  value,
}))

/** 六态生命周期 chip（chk_exceptions_status 值域；label 取 EXCEPTION_STATUS_TAG） */
const STATUS_OPTIONS: Array<{ label: string; value: ExceptionStatus }> = (
  Object.keys(EXCEPTION_STATUS_TAG) as ExceptionStatus[]
).map((value) => ({ label: EXCEPTION_STATUS_TAG[value].label, value }))

/** 拍照取证：capture=environment 直起相机（桌面浏览器忽略该属性降级为选图），
 * 仅收图片；白名单/数量权威校验在后端（internal/returns/service_exception_images.go） */
const PHOTO_INPUT_ACCEPT = 'image/*'

/**
 * Pad 异常页（/pad/exception，frontend.md §20.2/§20.3/§20.4 + business-flow.md §11.2）：
 * - 契约回对共享层 api/exception.ts（ExceptionView snake_case，六态 chk_exceptions_status，
 *   九类中文值域 chk_exceptions_type，internal/returns/service_exception.go:64-88）；
 * - 横屏三栏：左=异常卡列表（exceptionApi.list）+ 九类异常/生命周期大触摸 chip 筛选；
 *   中=异常详情 PadInfoCard（描述/来源/定位/责任人/处理人/处理记录）；右=处理操作区；
 * - 生命周期动作接线 returns 域 8 端点（internal/returns/handler.go:139-146）：
 *   认领=assign（OPEN，分派给当前登录人）、开始处理=start（ASSIGNED）、提交复核=review
 *   （PROCESSING）、解决=resolve（PENDING_REVIEW，后端同事务释放异常冻结）、关闭=close
 *   （RESOLVED）；前置权限码 returns:exception:* 经 canAccess fail-closed 控制
 *   （permission.md §5：前端仅体验优化，后端 RequirePermission 仍强校验）；
 * - 拍照取证真实接线（@/views/exception/exceptionImages 两段式契约：文件中心上传 →
 *   POST /api/exceptions/{id}/images 挂接，权限 returns:exception:execute + 生命周期
 *   OPEN..PENDING_REVIEW 前置、累计 ≤20 预检）；取证图片经认证 Blob 缩略图展示；
 * - 竖屏堆叠：顶部当前异常卡（选中详情内联列表上方）→ 详情/列表滚动区 → 底部 PadActionBar。
 */
export default function PadExceptionPage() {
  const orientation = usePadOrientation()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  // 按钮级权限码经 canAccess fail-closed 过滤（模式同 PadTransferPage；permission.md §5）
  const user = useAuthStore((state) => state.user)
  const canAssign = canAccess(user, EXCEPTION_ASSIGN_PERMISSION)
  const canExecute = canAccess(user, EXCEPTION_EXECUTE_PERMISSION)
  const canClose = canAccess(user, EXCEPTION_CLOSE_PERMISSION)

  const [typeFilter, setTypeFilter] = useState<ExceptionType | 'all'>('all')
  const [statusFilter, setStatusFilter] = useState<ExceptionStatus | 'all'>('all')
  const [selectedId, setSelectedId] = useState<ExceptionId | null>(null)

  const params = useMemo<ExceptionQuery>(
    () => ({
      type: typeFilter === 'all' ? undefined : typeFilter,
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

  // SKU/库位编码映射（基础资料 options 一次取全；失败降级为 ID，不造假数据）
  const skuOptions = useQuery({ queryKey: SKU_OPTIONS_KEY, queryFn: fetchSkuOptions })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const binOptions = useQuery({ queryKey: BIN_OPTIONS_KEY, queryFn: fetchBinOptions })
  const binCodeMap = useMemo(() => buildBinCodeMap(binOptions.data ?? []), [binOptions.data])

  const selected = list.items.find((item) => String(item.id) === String(selectedId)) ?? null

  const invalidateExceptions = () => {
    void queryClient.invalidateQueries({ queryKey: ['pad', 'exceptions'] })
  }

  const notifyError = (error: unknown) => messageApi.error(resolveErrorMessage(error))

  // 认领：POST /api/exceptions/{id}/assign（OPEN→ASSIGNED，分派给当前登录人）
  const assignMutation = useMutation({
    mutationFn: (id: ExceptionId) =>
      exceptionApi.assign(id, {
        assignee_id: Number(user?.id ?? 0),
        assignee_name: user?.real_name || user?.username || '',
      }),
    onSuccess: (view) => {
      messageApi.success(`已认领异常单 ${view.exception_no}（待处理 → 已分派）`)
      invalidateExceptions()
    },
    onError: notifyError,
  })
  // 生命周期迁移动作（start/review/resolve/close 共用形态，返回迁移后 ExceptionView）
  const lifecycleMutation = useMutation({
    mutationFn: ({ id, action }: { id: ExceptionId; action: 'start' | 'review' | 'resolve' | 'close' }) =>
      exceptionApi[action](id),
    onSuccess: (view, { action }) => {
      const fromTo: Record<typeof action, string> = {
        start: '已分派 → 处理中',
        review: '处理中 → 待复核',
        resolve: '待复核 → 已解决（异常冻结已按需释放）',
        close: '已解决 → 已关闭',
      }
      messageApi.success(`异常单 ${view.exception_no}：${fromTo[action]}`)
      invalidateExceptions()
    },
    onError: notifyError,
  })

  // 拍照取证：@/views/exception/exceptionImages 两段式编排——文件中心上传
  // （module=exception、business_no=异常单号）→ POST /api/exceptions/{id}/images 挂接（追加式）
  const cameraInputRef = useRef<HTMLInputElement>(null)
  const attachMutation = useMutation({
    mutationFn: (files: File[]) => {
      if (selected == null) throw new Error('未选择异常单')
      return uploadAndAttachExceptionImages(selected.id, selected.exception_no, files)
    },
    onSuccess: (view, files) => {
      messageApi.success(`已挂接 ${files.length} 张取证图片（共 ${view.image_refs.length}/${EXCEPTION_IMAGE_LIMIT}）`)
      invalidateExceptions()
    },
    onError: notifyError,
  })

  /** 拍照/选图提交前预检：图片类型 + 累计 ≤20（后端权威校验，前端拦截明显超限的提交） */
  const handlePhotoFiles = (fileList: FileList | null) => {
    if (selected == null || fileList == null || fileList.length === 0) return
    const files = Array.from(fileList).filter((file) => file.type.startsWith('image/'))
    if (files.length === 0) {
      messageApi.warning('请选择图片文件（png/jpg/jpeg/gif/webp/bmp）')
      return
    }
    const room = EXCEPTION_IMAGE_LIMIT - selected.image_refs.length
    if (files.length > room) {
      messageApi.warning(
        `累计挂接图片不能超过 ${EXCEPTION_IMAGE_LIMIT} 张（后端限制），还可挂接 ${Math.max(room, 0)} 张`,
      )
      return
    }
    attachMutation.mutate(files)
  }

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
        item.exception_no.toUpperCase() === key || (item.source_no ?? '').toUpperCase() === key,
    )
    if (hit) {
      setSelectedId(hit.id)
      messageApi.success(`已定位异常单：${hit.exception_no}`)
    } else {
      messageApi.warning(`未在当前异常列表中找到「${code}」，请检查单号或手工点选`)
    }
  }

  /** 动作可用性守卫（canAccess fail-closed + 状态机前置态，给出具体原因） */
  const actionDisabledReason = (action: 'assign' | 'start' | 'review' | 'resolve' | 'close') => {
    if (selected == null) return '先从左侧选择一张异常卡'
    const guards = {
      assign: { permission: canAssign, permissionName: EXCEPTION_ASSIGN_PERMISSION, status: 'OPEN' as ExceptionStatus, hint: '仅「待处理」状态可认领' },
      start: { permission: canExecute, permissionName: EXCEPTION_EXECUTE_PERMISSION, status: 'ASSIGNED' as ExceptionStatus, hint: '仅「已分派」状态可开始处理' },
      review: { permission: canExecute, permissionName: EXCEPTION_EXECUTE_PERMISSION, status: 'PROCESSING' as ExceptionStatus, hint: '仅「处理中」状态可提交复核' },
      resolve: { permission: canExecute, permissionName: EXCEPTION_EXECUTE_PERMISSION, status: 'PENDING_REVIEW' as ExceptionStatus, hint: '仅「待复核」状态可解决' },
      close: { permission: canClose, permissionName: EXCEPTION_CLOSE_PERMISSION, status: 'RESOLVED' as ExceptionStatus, hint: '仅「已解决」状态可关闭' },
    }
    const guard = guards[action]
    if (!guard.permission) return `缺少 ${guard.permissionName} 权限`
    if (selected.status !== guard.status) return `${guard.hint}（当前 ${EXCEPTION_STATUS_TAG[selected.status]?.label ?? selected.status}）`
    return undefined
  }

  const handleAction = (action: 'assign' | 'start' | 'review' | 'resolve' | 'close') => {
    if (!selected) return
    if (action === 'assign') {
      assignMutation.mutate(selected.id)
    } else {
      lifecycleMutation.mutate({ id: selected.id, action })
    }
  }

  const skuDisplay = (item: ExceptionItem) =>
    String(item.sku_id) === '0' ? EMPTY_TEXT : (skuMaps.code.get(String(item.sku_id)) ?? `#${String(item.sku_id)}`)
  const binDisplay = (item: ExceptionItem) =>
    String(item.bin_id) === '0' ? EMPTY_TEXT : (binCodeMap.get(String(item.bin_id)) ?? `#${String(item.bin_id)}`)

  const totalPage = Math.max(1, Math.ceil(list.total / list.pagination.pageSize))

  const detailCard = selected ? (
    <PadInfoCard
      title={`异常详情 · ${selected.exception_no}`}
      items={[
        { label: '异常单号', value: selected.exception_no },
        { label: '生命周期', value: <ExceptionStatusTag status={selected.status} /> },
        { label: '异常类型', value: selected.type },
        { label: '来源类型', value: selected.source_type || EMPTY_TEXT },
        { label: '来源单据', value: selected.source_no || EMPTY_TEXT },
        { label: 'SKU', value: skuDisplay(selected) },
        { label: '库位', value: binDisplay(selected) },
        { label: '序列号', value: selected.serial_no || EMPTY_TEXT },
        { label: '责任人', value: selected.owner_name || EMPTY_TEXT },
        { label: '处理人', value: selected.assignee_name || EMPTY_TEXT },
        { label: '登记时间', value: formatDateTime(selected.created_at) },
        ...(selected.assigned_at ? [{ label: '分派时间', value: formatDateTime(selected.assigned_at) }] : []),
        ...(selected.resolved_at ? [{ label: '解决时间', value: formatDateTime(selected.resolved_at) }] : []),
        ...(selected.closed_at ? [{ label: '关闭时间', value: formatDateTime(selected.closed_at) }] : []),
        ...(String(selected.freeze_lock_id) !== '0'
          ? [{ label: '异常冻结', value: `锁 #${String(selected.freeze_lock_id)}（解决/关闭时后端释放）` }]
          : []),
        { label: '描述', value: selected.detail || EMPTY_TEXT },
        { label: '备注', value: selected.remark || EMPTY_TEXT },
        ...(selected.image_refs.length > 0
          ? [
              {
                label: `取证图片（${selected.image_refs.length}）`,
                value: <ExceptionImageStrip refs={selected.image_refs} width={56} height={56} />,
              },
            ]
          : []),
      ]}
      columns={2}
    />
  ) : null

  const handleRecordsNode = selected ? (
    <section className="sf-pad-card" aria-label="处理记录">
      <h3 className="sf-pad-card-title">处理记录（追加式台账，{selected.handle_records.length} 条）</h3>
      {selected.handle_records.length === 0 ? (
        <SfEmpty description="暂无处理记录" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {selected.handle_records.map((record, index) => (
            <div key={`${String(record.at)}-${record.action}-${index}`} className="sf-pad-task-card">
              <div className="sf-pad-task-card__head">
                <span className="sf-pad-task-card__no">{record.action}</span>
                <span>{formatDateTime(record.at)}</span>
              </div>
              <div className="sf-pad-task-card__meta">
                <span>{record.by_name || `#${String(record.by_id)}`}</span>
                {record.qty ? <span>数量 {record.qty}</span> : null}
              </div>
              {record.note ? (
                <div className="sf-pad-task-card__qty">
                  <span style={{ overflowWrap: 'anywhere' }}>{record.note}</span>
                </div>
              ) : null}
            </div>
          ))}
        </div>
      )}
    </section>
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
        <div className="sf-pad-chip-row" role="group" aria-label="生命周期筛选（六态）">
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
          description="异常单列表（GET /api/exceptions）加载失败"
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

  // 右栏处理操作区：生命周期动作真实接线（权限/状态守卫）+ 拍照取证（两段式挂接，守卫同口径）
  const photoReason = selected
    ? exceptionImagesDisabledReason(selected, canExecute)
    : '先从左侧选择一张异常卡'
  const openPhotoPicker = () => {
    if (photoReason) {
      messageApi.warning(photoReason)
      return
    }
    cameraInputRef.current?.click()
  }

  const actionPanel = (
    <section className="sf-pad-card" aria-label="异常处理操作区">
      <h3 className="sf-pad-card-title">处理操作</h3>
      {!selected ? (
        <SfEmpty description="从左侧选择一张异常卡后，在此进行分派 / 处理 / 关闭" />
      ) : (
        <div style={{ display: 'grid', gap: 'var(--sf-space-2)' }}>
          <PadExceptionAction
            label="认领异常（分派给自己）"
            icon={<LockOutlined />}
            disabledReason={actionDisabledReason('assign')}
            loading={assignMutation.isPending}
            onClick={() => handleAction('assign')}
          />
          <PadExceptionAction
            label="开始处理"
            icon={<PlayCircleOutlined />}
            disabledReason={actionDisabledReason('start')}
            loading={lifecycleMutation.isPending}
            onClick={() => handleAction('start')}
          />
          <PadExceptionAction
            label="提交复核"
            icon={<RocketOutlined />}
            disabledReason={actionDisabledReason('review')}
            loading={lifecycleMutation.isPending}
            onClick={() => handleAction('review')}
          />
          <PadExceptionAction
            label="解决"
            icon={<CheckSquareOutlined />}
            disabledReason={actionDisabledReason('resolve')}
            loading={lifecycleMutation.isPending}
            onClick={() => handleAction('resolve')}
          />
          <PadExceptionAction
            label="关闭异常"
            icon={<CheckOutlined />}
            disabledReason={actionDisabledReason('close')}
            loading={lifecycleMutation.isPending}
            onClick={() => handleAction('close')}
          />
          <span
            role="button"
            tabIndex={0}
            aria-label={photoReason ? `拍照取证（不可用：${photoReason}）` : '拍照取证'}
            style={{ display: 'block', cursor: photoReason ? 'not-allowed' : 'pointer' }}
            onClick={openPhotoPicker}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                openPhotoPicker()
              }
            }}
          >
            <Button
              block
              size="large"
              icon={<CameraOutlined />}
              disabled={!!photoReason}
              loading={attachMutation.isPending}
              style={{ height: 'var(--sf-pad-touch-min)' }}
            >
              拍照取证
            </Button>
          </span>
          <input
            ref={cameraInputRef}
            type="file"
            accept={PHOTO_INPUT_ACCEPT}
            capture="environment"
            multiple
            hidden
            onChange={(e) => {
              handlePhotoFiles(e.target.files)
              e.target.value = ''
            }}
          />
          <p className="sf-pad-muted-note" style={{ margin: 0 }}>
            生命周期：待处理 → 已分派 → 处理中 → 待复核 → 已解决 → 已关闭
            （business-flow.md §11.2；异常冻结在解决/关闭时由后端同事务释放，inventory-rules.md §4.2）；
            动作需 returns:exception:* 权限并按状态机前置态启用
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
            <>
              {detailCard}
              {handleRecordsNode}
            </>
          ) : (
            <SfEmpty description="从左侧选择一张异常卡，此处展示异常详情" />
          )
        } actionSlot={actionPanel} />
      ) : (
        // 竖屏：顶部当前异常卡（选中详情内联在列表上方）→ 筛选 + 列表滚动区；动作并入详情下方
        <PadPageShell
          ratios={ratios}
          tasksSlot={
            <>
              {selected && (
                <div className="sf-pad-tasks-detail">
                  {detailCard}
                  {handleRecordsNode}
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
