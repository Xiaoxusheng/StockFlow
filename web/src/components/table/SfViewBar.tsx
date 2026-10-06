import { useEffect, useMemo, useState } from 'react'
import { Button, Dropdown, Input, Modal, Select, Space, message } from 'antd'
import { EllipsisOutlined, SaveOutlined } from '@ant-design/icons'
import { resolveErrorMessage } from '@/api/client'
import type { SavedView } from '@/api/savedViews'
import { useSavedViews } from '@/hooks/useSavedViews'
import { SfConfirm } from '@/components/common/SfConfirm'

/**
 * 保存视图工具条（计划 §2.2 B5 裁决，docs/plans/2026-10-06-efficiency-layer-phase1.md；
 * 放 SfToolbar extra 区 / SfTable actions 区）。
 *
 * 视图下拉 = 应用 / 保存当前 / 重命名 / 删除 / 设为默认 / 恢复默认。
 * 双形态 prop `mode`（B5）：
 * - 'url'（urlSync 页，URL 为筛选/分页唯一事实源）：应用视图 = 调 usePagedList 公开 API
 *   （applyFilters / onPageChange）把 filters_json/page_size 展开写入 URL——
 *   **禁止绕过 hook 直改 URL 或页面 state**；两段写入经 effect 对账串联
 *   （applyFilters 落筛选 → 等页面渲染出 applyFilters 结果 → onPageChange 落页大小），
 *   避免同 tick 内 hook 闭包参数互踩；
 * - 'state'（旧形态页）：置 params+pagination（onApplyState 单回调，页面自行 setState）。
 *
 * 边界（B5/B6）：
 * - `view=<id>` 仅作当前视图名标记（本组件内 state 呈现）；usePagedList 无保留键通道
 *   （F2 禁改该文件），故 URL 标记一期不落 URL——用户改动任一筛选即清除标记（effect 对账）；
 * - sort_json 一期不采集（恒不发送，schema 预留——api.md/frontend.md 披露）；
 * - columns_json 应用 = 经 SfTable 受控 props（hiddenColumns/onHiddenColumnsChange）生效，
 *   不写 localStorage 以免覆盖用户列偏好；保存时采集当前 hidden 集合（currentHiddenColumns）。
 */

export interface SfViewBarProps {
  /** 页面键（user_saved_views.page_key，服务端正则 ^[a-z0-9._-]{1,64}$） */
  pageKey: string
  /** 形态声明（B5）：url=urlSync 页经 usePagedList API 写 URL；state=旧形态页置 params */
  mode: 'url' | 'state'
  /** mode='url' 必传：usePagedList 返回值（只取 applyFilters/onPageChange 两个公开 API） */
  paged?: {
    applyFilters: (values: Record<string, unknown>) => void
    onPageChange: (page: number, pageSize: number) => void
  }
  /** mode='url' 必传：当前生效筛选（list.params）——应用视图两段写入的对账基准 + 标记清除监测 */
  appliedFilters?: Record<string, unknown>
  /** mode='state' 必传：旧形态页应用回调（置 params + pagination.pageSize） */
  onApplyState?: (filters: Record<string, string>, pageSize: number) => void
  /** 保存当前视图采集：当前筛选（urlSync 页传 list.formValues，旧形态页传 params） */
  currentFilters: Record<string, unknown>
  /** 保存采集：当前分页大小 */
  currentPageSize: number
  /** 保存采集：当前隐藏列（SfTable 受控列 hiddenColumns；非受控页可不传=列不采集） */
  currentHiddenColumns?: string[]
  /** 列应用出口：视图 columns_json 经 SfTable 受控 props 生效 */
  onHiddenColumnsChange?: (hidden: string[]) => void
}

/** filters_json 序列化（对账键；空值剔除后 stringify 保证同形可比） */
function stableFiltersJson(filters: Record<string, unknown>): string {
  const clean: Record<string, string> = {}
  for (const [key, value] of Object.entries(filters)) {
    if (value === undefined || value === null || value === '') continue
    clean[key] = String(value)
  }
  return JSON.stringify(clean)
}

/** 视图 filters_json → applyFilters 入参（SavedView.filters_json 已是 string 值对象） */
function toApplyValues(view: SavedView): Record<string, unknown> {
  return { ...(view.filters_json ?? {}) }
}

export function SfViewBar({
  pageKey,
  mode,
  paged,
  appliedFilters,
  onApplyState,
  currentFilters,
  currentPageSize,
  currentHiddenColumns,
  onHiddenColumnsChange,
}: SfViewBarProps) {
  const { views, isLoading, createAsync, updateAsync, removeAsync } = useSavedViews(pageKey)
  const [messageApi, contextHolder] = message.useMessage()

  /** 当前视图标记（view=<id> 一期不落 URL，见头注释；改动筛选即清除） */
  const [marker, setMarker] = useState<{ id: string; filtersJson: string } | null>(null)
  /** 应用视图的两段写入状态机（mode='url'） */
  const [pendingApply, setPendingApply] = useState<{
    filters: Record<string, unknown>
    filtersJson: string
    pageSize: number
    view: SavedView
    stage: 'filters' | 'pageSize'
  } | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [modalMode, setModalMode] = useState<'save' | 'rename'>('save')
  const [nameValue, setNameValue] = useState('')
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false)

  const currentView = useMemo(
    () => (marker ? views.find((view) => view.id === marker.id) ?? null : null),
    [marker, views],
  )
  const defaultView = useMemo(() => views.find((view) => view.is_default) ?? null, [views])

  // 标记清除：用户改动任一筛选（appliedFilters 偏离视图快照）→ 状态回归「自定义」
  useEffect(() => {
    if (marker && appliedFilters !== undefined && stableFiltersJson(appliedFilters) !== marker.filtersJson) {
      setMarker(null)
    }
  }, [marker, appliedFilters])

  // 两段写入 stage 1：先落筛选（URL 由 usePagedList.applyFilters 承载）
  useEffect(() => {
    if (!pendingApply || pendingApply.stage !== 'filters' || mode !== 'url' || !paged) return
    paged.applyFilters(pendingApply.filters)
    setPendingApply({ ...pendingApply, stage: 'pageSize' })
  }, [pendingApply, mode, paged])

  // 两段写入 stage 2：筛选已生效（渲染对账）→ 落页大小
  useEffect(() => {
    if (!pendingApply || pendingApply.stage !== 'pageSize' || mode !== 'url' || !paged) return
    if (stableFiltersJson(appliedFilters ?? {}) !== pendingApply.filtersJson) return
    paged.onPageChange(1, pendingApply.pageSize)
    setMarker({ id: pendingApply.view.id, filtersJson: pendingApply.filtersJson })
    onHiddenColumnsChange?.(pendingApply.view.columns_json ?? [])
    setPendingApply(null)
  }, [pendingApply, appliedFilters, mode, paged, onHiddenColumnsChange])

  const applyView = (view: SavedView) => {
    const filtersJson = stableFiltersJson(view.filters_json ?? {})
    if (mode === 'url') {
      if (!paged) {
        messageApi.error('urlSync 形态页未接入 usePagedList，无法应用视图')
        return
      }
      setPendingApply({ filters: toApplyValues(view), filtersJson, pageSize: view.page_size, view, stage: 'filters' })
      return
    }
    if (!onApplyState) {
      messageApi.error('旧形态页未提供 onApplyState，无法应用视图')
      return
    }
    onApplyState({ ...(view.filters_json ?? {}) }, view.page_size)
    setMarker({ id: view.id, filtersJson })
    onHiddenColumnsChange?.(view.columns_json ?? [])
  }

  const openSave = () => {
    setModalMode('save')
    setNameValue('')
    setModalOpen(true)
  }

  const openRename = () => {
    if (!currentView) {
      messageApi.warning('请先选择要重命名的视图')
      return
    }
    setModalMode('rename')
    setNameValue(currentView.name)
    setModalOpen(true)
  }

  const submitModal = async () => {
    const name = nameValue.trim()
    if (!name) {
      messageApi.warning('请输入视图名称')
      return
    }
    try {
      if (modalMode === 'save') {
        const created = await createAsync({
          page_key: pageKey,
          name,
          filters_json: Object.fromEntries(
            Object.entries(currentFilters).filter(([, v]) => v !== undefined && v !== null && v !== ''),
          ) as Record<string, string>,
          columns_json: currentHiddenColumns ?? [],
          page_size: currentPageSize,
        })
        setMarker({ id: created.id, filtersJson: stableFiltersJson(created.filters_json ?? {}) })
        messageApi.success(`视图「${name}」已保存`)
      } else if (currentView) {
        await updateAsync({ id: currentView.id, payload: { name } })
        messageApi.success('视图已重命名')
      }
      setModalOpen(false)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    }
  }

  const setDefault = async () => {
    if (!currentView) {
      messageApi.warning('请先选择要设为默认的视图')
      return
    }
    try {
      await updateAsync({ id: currentView.id, payload: { is_default: true } })
      messageApi.success(`已设「${currentView.name}」为默认视图`)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    }
  }

  // 「恢复默认」= 清除默认视图（PUT is_default=false，internal/userpref 单事务清除语义逆操作）
  const clearDefault = async () => {
    if (!defaultView) return
    try {
      await updateAsync({ id: defaultView.id, payload: { is_default: false } })
      messageApi.success('已恢复系统默认')
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    }
  }

  const deleteCurrent = async () => {
    if (!currentView) return
    try {
      await removeAsync(currentView.id)
      setMarker(null)
      messageApi.success('视图已删除')
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setConfirmDeleteOpen(false)
    }
  }

  const menuItems = [
    { key: 'rename', label: '重命名', disabled: !currentView },
    { key: 'delete', label: '删除', disabled: !currentView, danger: true },
    { key: 'set-default', label: '设为默认', disabled: !currentView || Boolean(currentView?.is_default) },
    { key: 'clear-default', label: '恢复默认', disabled: !defaultView },
  ]

  return (
    <Space size={4}>
      {contextHolder}
      <Select<string>
        style={{ minWidth: 132 }}
        loading={isLoading}
        value={currentView?.id}
        placeholder={defaultView ? `${defaultView.name}（默认）` : '默认视图'}
        allowClear
        onClear={() => setMarker(null)}
        onChange={(id) => {
          const view = views.find((item) => item.id === id)
          if (view) applyView(view)
        }}
        options={views.map((view) => ({
          label: view.is_default ? `${view.name}（默认）` : view.name,
          value: view.id,
        }))}
        aria-label="保存视图"
      />
      <Button icon={<SaveOutlined />} onClick={openSave}>
        保存当前
      </Button>
      <Dropdown
        menu={{
          items: menuItems,
          onClick: ({ key }) => {
            if (key === 'rename') openRename()
            if (key === 'delete') setConfirmDeleteOpen(true)
            if (key === 'set-default') void setDefault()
            if (key === 'clear-default') void clearDefault()
          },
        }}
      >
        <Button icon={<EllipsisOutlined />} aria-label="视图操作" />
      </Dropdown>

      <Modal
        title={modalMode === 'save' ? '保存当前视图' : '重命名视图'}
        open={modalOpen}
        width={420}
        okText={modalMode === 'save' ? '保存' : '确定'}
        confirmLoading={false}
        onOk={() => {
          void submitModal()
        }}
        onCancel={() => setModalOpen(false)}
      >
        <Input
          value={nameValue}
          maxLength={64}
          placeholder="视图名称（同页内不可重名）"
          onChange={(e) => setNameValue(e.target.value)}
        />
        {modalMode === 'save' && (
          <div style={{ marginTop: 8, color: 'var(--sf-text-secondary)', fontSize: 12 }}>
            将保存当前筛选、隐藏列与分页大小（排序一期不采集）。
          </div>
        )}
      </Modal>

      <SfConfirm
        title={`确认删除视图「${currentView?.name ?? ''}」？`}
        description="删除后不可恢复；不影响其他视图与当前筛选状态。"
        okText="删除"
        onConfirm={() => {
          void deleteCurrent()
        }}
      >
        <span />
      </SfConfirm>
      {/* SfConfirm 需由触发点包裹——删除确认以独立 Modal 形态承载 */}
      <Modal
        title="删除视图"
        open={confirmDeleteOpen}
        width={380}
        okText="删除"
        okButtonProps={{ danger: true }}
        onOk={() => {
          void deleteCurrent()
        }}
        onCancel={() => setConfirmDeleteOpen(false)}
      >
        确认删除视图「{currentView?.name ?? ''}」？删除后不可恢复。
      </Modal>
    </Space>
  )
}
