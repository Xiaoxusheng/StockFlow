import { useEffect, useState } from 'react'
import { Button, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  CountId,
  CountItem,
  CountItemQuery,
  CountItemSavePayload,
  CountQuery,
  CountTaskItem,
} from '@/api/count'
import { countApi } from '@/api/count'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { resolveErrorMessage } from '@/api/client'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import { CountItemRow, CountStatusTag, CountTaskCard, countScopeText } from './CountCards'
import {
  EMPTY_REGISTER_FORM,
  RegisterPanel,
  type RegisterFormValue,
} from './RegisterPanel'

/** 录入草稿暂存键（PadActionBar [暂停]：暂停当前录入，重进自动恢复，纯本机会话行为） */
const draftKeyOf = (countId: CountId, itemId: CountId) =>
  `sf.pad.count.draft.${String(countId)}.${String(itemId)}`

const sameId = (a: CountId | null | undefined, b: CountId | null | undefined) =>
  a != null && b != null && String(a) === String(b)

/**
 * Pad 盘点页（/pad/count，frontend.md §10.5 盘点扫码页 + §20.3/§20.4/§20.7）：
 * - 横屏三栏：左=盘点任务卡列表（countApi.list）+ 选中后明细卡（countApi.items）；
 *   中=任务摘要 + 当前库位商品信息（PadInfoCard，系统数量 metric 大字）；
 *   右=实盘登记操作区（数量大录入位 / 差异前端计算展示 / 差异原因备注 / [登记实盘] / 拍照占位 / PadScanStub）。
 * - 竖屏堆叠：顶部当前盘点任务摘要 → 中部当前库位商品 + 实盘录入 + 明细滚动区 → 底部 PadActionBar 五槽。
 * - 登记实盘是本轮唯一可真实调用的写端点（countApi.registerItem，后端盘点域未交付时呈统一错误提示）；
 *   盘点不做普通 CRUD 表格（frontend.md §30.1），不在前端算库存（差异仅为行内展示）。
 */
export default function PadCountPage() {
  const orientation = usePadOrientation()
  const queryClient = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const [selectedTaskId, setSelectedTaskId] = useState<CountId | null>(null)
  const [selectedItemId, setSelectedItemId] = useState<CountId | null>(null)
  const [form, setForm] = useState<RegisterFormValue>(EMPTY_REGISTER_FORM)

  const taskList = usePagedList<CountTaskItem, CountQuery>({
    queryKey: ['pad', 'counts'],
    fetch: (q) => countApi.list(q),
    params: {},
    defaultPageSize: 30,
  })

  const selectedTask =
    taskList.items.find((task) => sameId(task.id, selectedTaskId)) ?? null

  const detailQuery = useQuery({
    queryKey: ['pad', 'count-detail', String(selectedTaskId ?? '')],
    queryFn: () => {
      if (!selectedTaskId) throw new Error('未选择盘点任务')
      return countApi.detail(selectedTaskId)
    },
    enabled: !!selectedTaskId,
  })

  const itemList = usePagedList<CountItem, CountItemQuery>({
    queryKey: ['pad', 'count-items', String(selectedTaskId ?? '')],
    fetch: (q) => countApi.items(selectedTaskId ?? '', q),
    params: {},
    enabled: !!selectedTaskId,
    defaultPageSize: 50,
  })

  const selectedItem =
    itemList.items.find((item) => sameId(item.id, selectedItemId)) ?? null

  // 切换任务：清空明细选中与录入（明细数据随 queryKey 切换独立缓存）
  useEffect(() => {
    setSelectedItemId(null)
    setForm(EMPTY_REGISTER_FORM)
  }, [selectedTaskId])

  // 选中明细行变化：恢复该行草稿（无草稿则重置录入）
  const draftKey = selectedTask && selectedItem ? draftKeyOf(selectedTask.id, selectedItem.id) : null
  useEffect(() => {
    if (!draftKey) return
    try {
      const raw = sessionStorage.getItem(draftKey)
      if (raw) {
        const draft = JSON.parse(raw) as Partial<RegisterFormValue>
        setForm({
          countedQty: typeof draft.countedQty === 'number' ? draft.countedQty : null,
          reason: typeof draft.reason === 'string' ? draft.reason : '',
          remark: typeof draft.remark === 'string' ? draft.remark : '',
        })
        return
      }
    } catch {
      // 草稿损坏时忽略，按空表单处理
    }
    setForm(EMPTY_REGISTER_FORM)
  }, [draftKey])

  const registerMutation = useMutation({
    mutationFn: (input: { itemId: CountId; skuCode: string; payload: CountItemSavePayload }) => {
      if (!selectedTask) throw new Error('未选择盘点任务')
      return countApi.registerItem(selectedTask.id, input.itemId, input.payload)
    },
    onSuccess: (_data, input) => {
      messageApi.success(`已登记实盘：${input.skuCode}，差异调整由后端走库存调整单 + 审批（business-flow.md §10.2）`)
      if (draftKey) sessionStorage.removeItem(draftKey)
      setForm(EMPTY_REGISTER_FORM)
      void queryClient.invalidateQueries({ queryKey: ['pad', 'count-items'] })
      void queryClient.invalidateQueries({ queryKey: ['pad', 'count-detail'] })
    },
    onError: (error) => {
      // 后端盘点域未交付 / 校验失败：统一错误提示，保留录入内容便于重试
      messageApi.error(resolveErrorMessage(error))
    },
  })

  const submitRegister = () => {
    if (!selectedTask || !selectedItem) {
      messageApi.warning('请先选择盘点任务并点选要登记的明细行')
      return
    }
    if (form.countedQty == null || !Number.isFinite(form.countedQty) || form.countedQty < 0) {
      messageApi.warning('请先录入有效的实盘数量（非负数）')
      return
    }
    const diff = form.countedQty - selectedItem.systemQty
    if (diff !== 0) {
      // 差异必走原因/备注流程（devices.md §10.3；api/count.ts CountItemSavePayload 契约必填）
      if (!form.reason.trim()) {
        messageApi.warning('存在盘点差异，必须填写差异原因（devices.md §10.3）')
        return
      }
      if (!form.remark.trim()) {
        messageApi.warning('存在盘点差异，必须填写备注说明（devices.md §10.3）')
        return
      }
    }
    registerMutation.mutate({
      itemId: selectedItem.id,
      skuCode: selectedItem.skuCode,
      payload: { countedQty: form.countedQty, reason: form.reason.trim(), remark: form.remark.trim() },
    })
  }

  // PadActionBar [暂停]：当前录入暂存本机（真实会话行为，重进该行自动恢复）
  const handlePause = () => {
    if (!draftKey) {
      messageApi.info('请先点选明细行后再暂停，当前没有可暂存的录入')
      return
    }
    if (form.countedQty == null && !form.reason.trim() && !form.remark.trim()) {
      messageApi.info('当前行没有录入内容，无需暂停')
      return
    }
    try {
      sessionStorage.setItem(draftKey, JSON.stringify(form))
      messageApi.success('已暂停：录入内容已暂存本机，重新点选该行自动恢复')
    } catch {
      messageApi.error('本机存储不可用，暂存失败')
    }
  }

  // 扫码 / 手输兜底：仅本地匹配当前任务明细行（真实扫码属 Scan 端 / F16）
  const handleScanCode = (code: string) => {
    const key = code.trim().toUpperCase()
    const hit = itemList.items.find(
      (item) =>
        item.skuCode.toUpperCase() === key ||
        (item.binCode ?? '').toUpperCase() === key,
    )
    if (hit) {
      setSelectedItemId(hit.id)
      messageApi.success(`已定位明细行：${hit.skuCode}（库位 ${hit.binCode || EMPTY_TEXT}）`)
    } else {
      messageApi.warning(`未在当前盘点任务明细中找到「${code}」，请检查编码或手工点选`)
    }
  }

  const totalItemPage = Math.max(1, Math.ceil(itemList.total / itemList.pagination.pageSize))
  const totalTaskPage = Math.max(1, Math.ceil(taskList.total / taskList.pagination.pageSize))

  const summary = detailQuery.data?.summary ?? null

  const taskSummaryCard = selectedTask ? (
    <PadInfoCard
      title={`盘点任务 · ${selectedTask.countNo}`}
      items={[
        { label: '盘点单号', value: selectedTask.countNo },
        { label: '状态', value: <CountStatusTag status={selectedTask.status} /> },
        { label: '仓库', value: selectedTask.warehouseName || EMPTY_TEXT },
        { label: '范围', value: countScopeText(selectedTask) },
        { label: '负责人', value: selectedTask.ownerName || EMPTY_TEXT },
        {
          label: '完成率',
          value: summary ? `${summary.completionRate}%` : EMPTY_TEXT,
          emphasis: true,
        },
        {
          label: '已盘 / 总行数',
          value: summary ? `${formatNumber(summary.countedItems)} / ${formatNumber(summary.totalItems)}` : EMPTY_TEXT,
        },
        {
          label: '差异合计',
          value: summary ? formatNumber(summary.diffQty) : EMPTY_TEXT,
          emphasis: true,
        },
        { label: '创建时间', value: formatDateTime(selectedTask.createdAt) },
      ]}
    />
  ) : null

  const itemInfoCard = selectedItem ? (
    <PadInfoCard
      title={`当前库位商品 · ${selectedItem.skuCode}`}
      items={[
        { label: '商品名称', value: selectedItem.productName || EMPTY_TEXT },
        { label: 'SKU', value: selectedItem.skuCode },
        { label: '库位', value: selectedItem.binCode || EMPTY_TEXT },
        { label: '批次', value: selectedItem.batchNo || EMPTY_TEXT },
        { label: '系统数量', value: formatNumber(selectedItem.systemQty), emphasis: true },
        {
          label: '实盘数量',
          value: selectedItem.countedQty != null ? formatNumber(selectedItem.countedQty) : '未登记',
          emphasis: true,
        },
      ]}
      columns={2}
    />
  ) : (
    <SfEmpty description="从明细卡点选要盘点的商品行，此处展示当前库位商品信息" />
  )

  const taskListNode = (
    <div>
      {taskList.isPending ? (
        <SfLoading rows={5} />
      ) : taskList.error ? (
        <SfError
          error={taskList.error}
          description="盘点域接口（GET /api/counts）为前端先行契约，后端盘点域交付前呈错误态"
          onRetry={() => void taskList.refetch()}
        />
      ) : taskList.items.length === 0 ? (
        <SfEmpty description="暂无盘点任务" />
      ) : (
        <>
          <h4 className="sf-pad-group-title">盘点任务（{taskList.total}）</h4>
          <div className="sf-pad-tasks-cards">
            {taskList.items.map((task) => (
              <CountTaskCard
                key={String(task.id)}
                task={task}
                selected={sameId(task.id, selectedTaskId)}
                onClick={() => setSelectedTaskId(task.id)}
              />
            ))}
          </div>
          <div className="sf-pad-tasks-pager">
            <span className="sf-pad-info-label">
              共 {taskList.total} 条 · 第 {taskList.pagination.current}/{totalTaskPage} 页
            </span>
            <div className="sf-pad-pager-btns">
              <Button
                size="large"
                className="sf-pad-chip"
                disabled={taskList.pagination.current <= 1}
                onClick={() => taskList.onPageChange(taskList.pagination.current - 1, taskList.pagination.pageSize)}
              >
                上一页
              </Button>
              <Button
                size="large"
                className="sf-pad-chip"
                disabled={taskList.pagination.current >= totalTaskPage}
                onClick={() => taskList.onPageChange(taskList.pagination.current + 1, taskList.pagination.pageSize)}
              >
                下一页
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  )

  const itemListNode = !selectedTask ? null : (
    <div>
      <h4 className="sf-pad-group-title">盘点明细 · {selectedTask.countNo}</h4>
      {itemList.isPending ? (
        <SfLoading rows={4} />
      ) : itemList.error ? (
        <SfError
          error={itemList.error}
          description="盘点明细接口（GET /api/counts/{id}/items）为前端先行契约，后端盘点域交付前呈错误态"
          onRetry={() => void itemList.refetch()}
        />
      ) : itemList.items.length === 0 ? (
        <SfEmpty description="该盘点任务暂无明细行" />
      ) : (
        <>
          <div className="sf-pad-tasks-cards">
            {itemList.items.map((item) => (
              <CountItemRow
                key={String(item.id)}
                item={item}
                selected={sameId(item.id, selectedItemId)}
                onClick={() => setSelectedItemId(item.id)}
              />
            ))}
          </div>
          <div className="sf-pad-tasks-pager">
            <span className="sf-pad-info-label">
              共 {itemList.total} 行 · 第 {itemList.pagination.current}/{totalItemPage} 页
            </span>
            <div className="sf-pad-pager-btns">
              <Button
                size="large"
                className="sf-pad-chip"
                disabled={itemList.pagination.current <= 1}
                onClick={() => itemList.onPageChange(itemList.pagination.current - 1, itemList.pagination.pageSize)}
              >
                上一页
              </Button>
              <Button
                size="large"
                className="sf-pad-chip"
                disabled={itemList.pagination.current >= totalItemPage}
                onClick={() => itemList.onPageChange(itemList.pagination.current + 1, itemList.pagination.pageSize)}
              >
                下一页
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  )

  const registerPanel = (
    <RegisterPanel
      task={selectedTask}
      item={selectedItem}
      value={form}
      onChange={setForm}
      submitting={registerMutation.isPending}
      onSubmit={submitRegister}
      notify={(text) => messageApi.info(text)}
      onScanSubmit={handleScanCode}
    />
  )

  // 横屏中栏：任务摘要（含完成率/差异合计）+ 当前库位商品信息
  const contentSlotLandscape = (
    <>
      {selectedTask ? (
        detailQuery.isPending ? (
          <SfLoading rows={3} />
        ) : detailQuery.error ? (
          <SfError error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
        ) : (
          taskSummaryCard
        )
      ) : (
        <SfEmpty description="从左侧选择一张盘点任务卡，此处展示任务摘要与完成率" />
      )}
      {itemInfoCard}
    </>
  )

  // 竖屏堆叠：顶部当前盘点任务 → 当前库位商品 + 实盘录入 → 明细滚动区（PadPageShell 单列滚动）
  const tasksSlotPortrait = (
    <>
      {selectedTask ? (
        detailQuery.isPending ? (
          <SfLoading rows={3} />
        ) : detailQuery.error ? (
          <SfError error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
        ) : (
          taskSummaryCard
        )
      ) : null}
      {taskListNode}
    </>
  )

  const contentSlotPortrait = (
    <>
      {itemInfoCard}
      {registerPanel}
      {itemListNode}
    </>
  )

  const ratios: [number, number, number] = [34, 29, 37]

  return (
    <>
      {contextHolder}
      {orientation === 'landscape' ? (
        <PadPageShell
          ratios={ratios}
          tasksSlot={
            <>
              {taskListNode}
              {itemListNode}
            </>
          }
          contentSlot={contentSlotLandscape}
          actionSlot={registerPanel}
        />
      ) : (
        <PadPageShell ratios={ratios} tasksSlot={tasksSlotPortrait} contentSlot={contentSlotPortrait} />
      )}
      <PadActionBar
        onPause={handlePause}
        onFinish={submitRegister}
        onScanSubmit={handleScanCode}
      />
    </>
  )
}
