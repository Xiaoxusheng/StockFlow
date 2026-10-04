import { useEffect, useMemo, useState } from 'react'
import { Button, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  countApi,
  type CountId,
  type CountItem,
  type CountOrder,
  type CountQuery,
} from '@/api/count'
import { masterdataApi, OPTIONS_PAGE_SIZE, type SkuItem } from '@/api/masterdata'
import { binApi, shelfApi, warehouseApi, zoneApi } from '@/api/warehouse'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { PadActionBar, PadInfoCard, PadPageShell, usePadOrientation } from '@/layouts/pad'
import { usePagedList } from '@/hooks/usePagedList'
import { resolveErrorMessage } from '@/api/client'
import { EMPTY_TEXT, formatDateTime, formatNumber } from '@/utils/format'
import {
  CountDifferenceCard,
  CountItemRow,
  CountItemStateTag,
  CountStatusTag,
  CountTaskCard,
  countScopeText,
  type ScopeIdMaps,
} from './CountCards'
import { EMPTY_REGISTER_FORM, RegisterPanel, type RegisterFormValue } from './RegisterPanel'

/** 草稿暂存键（PadActionBar [暂停]：仅数量，键 = 明细行 id；纯本机会话行为，重进自动恢复） */
const draftKeyOf = (countId: CountId) => `sf.pad.count.draft.${String(countId)}`

/** 读取本机草稿（仅接受非负有限数量，损坏内容忽略） */
function readDrafts(countId: CountId | null): Record<string, number> {
  if (!countId) return {}
  try {
    const raw = sessionStorage.getItem(draftKeyOf(countId))
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const drafts: Record<string, number> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === 'number' && Number.isFinite(value) && value >= 0) drafts[key] = value
    }
    return drafts
  } catch {
    return {}
  }
}

const sameId = (a: CountId | null | undefined, b: CountId | null | undefined) =>
  a != null && b != null && String(a) === String(b)

type ItemFilter = 'ALL' | 'PENDING' | 'COUNTED'

interface ItemLabels {
  skuCode: string
  productName: string
  binCode: string
}

/**
 * Pad 盘点页（/pad/count，frontend.md §10.5 盘点扫码页 + §20.2/§20.3/§20.4/§20.7）：
 * - 数据契约（已核实问题 #4 重做）：任务列表 GET /api/counts（snake_case CountOrder），
 *   详情 GET /api/counts/{id} 一次返回 {order,items,differences}（handler.go:170-174），
 *   明细直接取 detail.items——不再调用不存在的 GET /api/counts/{id}/items；
 * - [登记实盘] 批量幂等 PUT /api/counts/{id}/items，items 数组按 (InventoryRowID, SerialNo, Qty)
 *   提交（store.go:204-208 CountRegistration 无差异原因 / 备注字段，差异说明随审核意见提交）；
 * - 状态值域为后端五态 DRAFT/COUNTING/PENDING_REVIEW/COMPLETED/CANCELLED；
 *   明细行 qty_counted=null=待盘（未登记 ≠ 登记 0，inventory-rules.md §9）；
 * - 明细行 sku_id / bin_id 经基础资料 options 端点映射编码显示，映射失败降级 #id；
 *   扫码 / 手输本地比对 SKU 编码 / 库位编码定位明细行（真实扫码属 Scan 端 / F16）；
 * - [暂停] 数量草稿存本机 sessionStorage（仅数量）；盘点不做普通 CRUD 表格（frontend.md §30.1），
 *   不在前端算库存（差异仅为录入预览与差异行展示）。
 */
export default function PadCountPage() {
  const orientation = usePadOrientation()
  const queryClient = useQueryClient()
  const [messageApi, contextHolder] = message.useMessage()
  const [selectedTaskId, setSelectedTaskId] = useState<CountId | null>(null)
  const [selectedItemId, setSelectedItemId] = useState<CountId | null>(null)
  const [drafts, setDrafts] = useState<Record<string, number>>({})
  const [itemFilter, setItemFilter] = useState<ItemFilter>('ALL')

  // —— 任务列表（GET /api/counts，handler.go:451-465 warehouse_id/status/count_no 筛选）——
  const taskList = usePagedList<CountOrder, CountQuery>({
    queryKey: ['pad', 'counts'],
    fetch: (q) => countApi.list(q),
    params: {},
    defaultPageSize: 30,
  })

  // —— 详情（GET /api/counts/{id}：{order, items, differences} 一次返回，明细不分页）——
  const detailQuery = useQuery({
    queryKey: ['pad', 'count-detail', String(selectedTaskId ?? '')],
    queryFn: () => {
      if (!selectedTaskId) throw new Error('未选择盘点任务')
      return countApi.detail(selectedTaskId)
    },
    enabled: !!selectedTaskId,
  })
  const order: CountOrder | null = detailQuery.data?.order ?? null
  const items: CountItem[] = detailQuery.data?.items ?? []
  const differences = detailQuery.data?.differences ?? []

  // —— 基础资料 options 映射（一次取全；失败降级 #id 显示，不阻塞盘点主流程）——
  // OPTIONS_PAGE_SIZE=100 与 PadInventoryPage 同口径（后端 MaxPageSize 上限）：超限时编码降级为 #id
  const warehouseOptions = useQuery({
    queryKey: ['pad', 'count', 'warehouse-options'],
    queryFn: () => warehouseApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const zoneOptions = useQuery({
    queryKey: ['pad', 'count', 'zone-options'],
    queryFn: () => zoneApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const shelfOptions = useQuery({
    queryKey: ['pad', 'count', 'shelf-options'],
    queryFn: () => shelfApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const binOptions = useQuery({
    queryKey: ['pad', 'count', 'bin-options'],
    queryFn: () => binApi.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })
  const skuOptions = useQuery({
    queryKey: ['pad', 'count', 'sku-options'],
    queryFn: () => masterdataApi.skus.list({ page: 1, pageSize: OPTIONS_PAGE_SIZE }),
  })

  const warehouseMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const wh of warehouseOptions.data?.items ?? []) map.set(String(wh.id), wh.code)
    return map
  }, [warehouseOptions.data])
  const zoneMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const zone of zoneOptions.data?.items ?? []) map.set(String(zone.id), zone.code)
    return map
  }, [zoneOptions.data])
  const shelfMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const shelf of shelfOptions.data?.items ?? []) map.set(String(shelf.id), shelf.code)
    return map
  }, [shelfOptions.data])
  const binMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const bin of binOptions.data?.items ?? []) map.set(String(bin.id), bin.code)
    return map
  }, [binOptions.data])
  const skuMap = useMemo(() => {
    const map = new Map<string, SkuItem>()
    for (const sku of skuOptions.data?.items ?? []) map.set(String(sku.id), sku)
    return map
  }, [skuOptions.data])
  const scopeMaps: ScopeIdMaps = useMemo(
    () => ({
      zone: zoneMap,
      shelf: shelfMap,
      bin: binMap,
      sku: new Map([...skuMap.values()].map((sku) => [String(sku.id), sku.code])),
    }),
    [zoneMap, shelfMap, binMap, skuMap],
  )

  const labelsOf = (row: CountItem): ItemLabels => {
    const sku = skuMap.get(String(row.sku_id))
    return {
      skuCode: sku?.code ?? `SKU #${String(row.sku_id)}`,
      productName: sku?.product_name ?? EMPTY_TEXT,
      binCode: binMap.get(String(row.bin_id)) ?? `#${String(row.bin_id)}`,
    }
  }

  const selectedItem = items.find((row) => sameId(row.id, selectedItemId)) ?? null
  const selectedLabels = selectedItem ? labelsOf(selectedItem) : null

  // 切换任务：清空明细选中，并从本机恢复该单数量草稿（仅数量）
  useEffect(() => {
    setSelectedItemId(null)
    setDrafts(readDrafts(selectedTaskId))
  }, [selectedTaskId])

  // 草稿随录入落本机（sessionStorage，会话级；[暂停] 为显式确认入口）
  useEffect(() => {
    if (!selectedTaskId) return
    try {
      sessionStorage.setItem(draftKeyOf(selectedTaskId), JSON.stringify(drafts))
    } catch {
      // 存储不可用时保持会话内状态
    }
  }, [drafts, selectedTaskId])

  // 待盘 / 已盘口径：qty_counted=null=待盘（仅行数统计展示，不计算业务结果）
  const pendingCount = items.filter((row) => row.qty_counted == null).length

  // —— 实盘登记：批量幂等 PUT /api/counts/{id}/items（items 数组，单行批量提交）——
  const registerMutation = useMutation({
    mutationFn: (input: { item: CountItem; qty: number }) => {
      if (!order) throw new Error('未选择盘点任务')
      return countApi.register(order.id, {
        items: [
          {
            InventoryRowID: Number(input.item.inventory_row_id),
            SerialNo: input.item.serial_no,
            Qty: input.qty,
          },
        ],
      })
    },
    onSuccess: (_data, input) => {
      const labels = labelsOf(input.item)
      messageApi.success(
        `已登记实盘：${labels.skuCode}（批量幂等 PUT，同「库存行 + 序列号」重复提交为覆盖）`,
      )
      setDrafts((prev) => {
        const next = { ...prev }
        delete next[String(input.item.id)]
        return next
      })
      void queryClient.invalidateQueries({ queryKey: ['pad', 'count-detail'] })
      void queryClient.invalidateQueries({ queryKey: ['pad', 'counts'] })
      // 连续盘点：自动定位下一待盘行；全部已盘则如实提示后续动作在 PC 端
      const idx = items.findIndex((row) => String(row.id) === String(input.item.id))
      const candidates = idx >= 0 ? [...items.slice(idx + 1), ...items.slice(0, idx)] : items
      const nextPending = candidates.find(
        (row) => row.qty_counted == null && String(row.id) !== String(input.item.id),
      )
      if (nextPending) {
        setSelectedItemId(nextPending.id)
        messageApi.info(`已定位下一待盘行：${labelsOf(nextPending).skuCode}`)
      } else {
        messageApi.info('该盘点单全部明细均已登记实盘，可在 PC 端完成实盘并提交复核')
      }
    },
    onError: (error) => {
      // 校验失败 / 状态冲突：统一错误提示，保留录入内容便于重试
      messageApi.error(resolveErrorMessage(error))
    },
  })

  const formValue: RegisterFormValue = selectedItem
    ? { countedQty: drafts[String(selectedItem.id)] ?? null }
    : EMPTY_REGISTER_FORM

  const handleFormChange = (next: RegisterFormValue) => {
    if (!selectedItem) return
    setDrafts((prev) => {
      const draft = { ...prev }
      if (next.countedQty == null) delete draft[String(selectedItem.id)]
      else draft[String(selectedItem.id)] = next.countedQty
      return draft
    })
  }

  const submitRegister = () => {
    if (!order || !selectedItem) {
      messageApi.warning('请先选择盘点任务并点选要登记的明细行')
      return
    }
    if (order.status !== 'COUNTING') {
      messageApi.warning('仅「盘点中」状态可登记实盘（business-flow.md §10.2：冻结快照后实盘）')
      return
    }
    const qty = formValue.countedQty
    if (qty == null || !Number.isFinite(qty) || qty < 0) {
      messageApi.warning('请先录入有效的实盘数量（非负整数；登记 0 也必须显式提交，inventory-rules.md §9）')
      return
    }
    if (selectedItem.serial_no !== '' && qty !== 0 && qty !== 1) {
      messageApi.warning('序列号行数量仅允许 0（缺失）/ 1（在库），inventory-rules.md §8.2')
      return
    }
    registerMutation.mutate({ item: selectedItem, qty })
  }

  // PadActionBar [暂停]：当前单已录数量暂存本机（仅数量，重进自动恢复）
  const handlePause = () => {
    if (!selectedTaskId) {
      messageApi.info('请先选择盘点任务，再暂停当前录入')
      return
    }
    if (Object.keys(drafts).length === 0) {
      messageApi.info('当前盘点单没有已录入的实盘数量，无需暂停')
      return
    }
    try {
      sessionStorage.setItem(draftKeyOf(selectedTaskId), JSON.stringify(drafts))
      messageApi.success('已暂停：已录入数量已暂存本机（仅数量），重新进入自动恢复')
    } catch {
      messageApi.error('本机存储不可用，暂存失败')
    }
  }

  // 扫码 / 手输兜底：本地比对 SKU 编码 / 库位编码映射定位明细行（真实扫码属 Scan 端 / F16）
  const handleScanCode = (code: string) => {
    if (!selectedTaskId) {
      messageApi.info('请先选择盘点任务，再扫码 / 手输定位明细行')
      return
    }
    const key = code.trim().toUpperCase()
    if (!key) return
    const hit = items.find((row) => {
      const skuCode = (skuMap.get(String(row.sku_id))?.code ?? '').toUpperCase()
      const binCode = (binMap.get(String(row.bin_id)) ?? '').toUpperCase()
      return (skuCode !== '' && skuCode === key) || (binCode !== '' && binCode === key)
    })
    if (hit) {
      setSelectedItemId(hit.id)
      const labels = labelsOf(hit)
      messageApi.success(`已定位明细行：${labels.skuCode}（库位 ${labels.binCode}）`)
    } else {
      messageApi.warning(`未在当前盘点明细中匹配到「${code}」（按 SKU 编码 / 库位编码比对），请检查编码或手工点选`)
    }
  }

  const totalTaskPage = Math.max(1, Math.ceil(taskList.total / taskList.pagination.pageSize))

  const taskSummaryCard = order ? (
    <PadInfoCard
      title={`盘点任务 · ${order.count_no}`}
      items={[
        { label: '盘点单号', value: order.count_no },
        { label: '状态', value: <CountStatusTag status={order.status} /> },
        {
          label: '仓库',
          value: warehouseMap.get(String(order.warehouse_id)) ?? `#${String(order.warehouse_id)}`,
        },
        { label: '范围', value: countScopeText(order.scope, scopeMaps) },
        { label: '冻结时间', value: formatDateTime(order.frozen_at) },
        { label: '创建时间', value: formatDateTime(order.created_at) },
        {
          label: '已盘 / 总行数',
          value: `${formatNumber(items.length - pendingCount)} / ${formatNumber(items.length)}`,
          emphasis: true,
        },
        { label: '待盘行数', value: formatNumber(pendingCount) },
        { label: '差异行数', value: formatNumber(differences.length) },
        { label: '备注', value: order.remark || EMPTY_TEXT },
      ]}
    />
  ) : null

  const taskSummaryBlock = !selectedTaskId ? (
    <SfEmpty description="从左侧选择一张盘点任务卡，此处展示任务摘要与实盘进度" />
  ) : detailQuery.isPending ? (
    <SfLoading rows={3} />
  ) : detailQuery.error ? (
    <SfError error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
  ) : (
    taskSummaryCard
  )

  const itemInfoCard = selectedItem && selectedLabels ? (
    <PadInfoCard
      title={`当前库位商品 · ${selectedLabels.skuCode}`}
      items={[
        { label: '商品名称', value: selectedLabels.productName },
        { label: 'SKU 编码', value: selectedLabels.skuCode },
        { label: '库位', value: selectedLabels.binCode },
        {
          label: '序列号',
          value: selectedItem.serial_no !== '' ? selectedItem.serial_no : '非序列号行（按行汇总）',
        },
        { label: '系统数量', value: formatNumber(selectedItem.qty_system), emphasis: true },
        {
          label: '实盘数量',
          value: selectedItem.qty_counted != null ? formatNumber(selectedItem.qty_counted) : '未登记',
          emphasis: true,
        },
        { label: '盘点状态', value: <CountItemStateTag item={selectedItem} /> },
        { label: '实盘时间', value: formatDateTime(selectedItem.counted_at) },
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
        <SfError error={taskList.error} onRetry={() => void taskList.refetch()} />
      ) : taskList.items.length === 0 ? (
        <SfEmpty description="暂无盘点任务" />
      ) : (
        <>
          <h4 className="sf-pad-group-title">盘点任务（{taskList.total}）</h4>
          <div className="sf-pad-tasks-cards">
            {taskList.items.map((task) => (
              <CountTaskCard
                key={String(task.id)}
                order={task}
                warehouseCode={warehouseMap.get(String(task.warehouse_id)) ?? `#${String(task.warehouse_id)}`}
                scopeText={countScopeText(task.scope, scopeMaps)}
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

  const filteredItems =
    itemFilter === 'ALL'
      ? items
      : itemFilter === 'PENDING'
        ? items.filter((row) => row.qty_counted == null)
        : items.filter((row) => row.qty_counted != null)

  const ITEM_FILTERS: Array<{ key: ItemFilter; label: string }> = [
    { key: 'ALL', label: `全部（${items.length}）` },
    { key: 'PENDING', label: `待盘（${pendingCount}）` },
    { key: 'COUNTED', label: `已盘（${items.length - pendingCount}）` },
  ]

  const itemListNode = !selectedTaskId ? null : (
    <div>
      <h4 className="sf-pad-group-title">盘点明细 · {order?.count_no ?? EMPTY_TEXT}</h4>
      {detailQuery.isPending ? (
        <SfLoading rows={4} />
      ) : detailQuery.error ? (
        <SfError
          error={detailQuery.error}
          description="盘点详情接口（GET /api/counts/{id}）不可用；明细随详情一次返回，无独立明细端点"
          onRetry={() => void detailQuery.refetch()}
        />
      ) : (
        <>
          <div className="sf-pad-filter-block">
            <div className="sf-pad-chip-row" role="group" aria-label="明细行筛选（待盘 / 已盘）">
              {ITEM_FILTERS.map((filter) => (
                <Button
                  key={filter.key}
                  size="large"
                  className="sf-pad-chip"
                  type={itemFilter === filter.key ? 'primary' : 'default'}
                  onClick={() => setItemFilter(filter.key)}
                >
                  {filter.label}
                </Button>
              ))}
            </div>
          </div>
          {filteredItems.length === 0 ? (
            <SfEmpty
              description={
                items.length === 0
                  ? '该盘点单暂无明细行（范围为空或冻结快照未生成）'
                  : '当前筛选下没有明细行，切换筛选查看'
              }
            />
          ) : (
            <div className="sf-pad-tasks-cards">
              {filteredItems.map((row) => {
                const labels = labelsOf(row)
                return (
                  <CountItemRow
                    key={String(row.id)}
                    item={row}
                    skuCode={labels.skuCode}
                    binCode={labels.binCode}
                    draftQty={drafts[String(row.id)] ?? null}
                    selected={sameId(row.id, selectedItemId)}
                    onClick={() => setSelectedItemId(row.id)}
                  />
                )
              })}
            </div>
          )}
          {items.length > 0 && (
            <p className="sf-pad-muted-note">
              共 {items.length} 行 · 待盘 {pendingCount} 行（明细随详情一次返回，无独立明细端点；登记后数量以后端返回为准）
            </p>
          )}
        </>
      )}
    </div>
  )

  const differencesNode = !selectedTaskId || detailQuery.isPending || detailQuery.error ? null : (
    <section className="sf-pad-card" aria-label="盘点差异">
      <h3 className="sf-pad-card-title">盘点差异（{differences.length}）</h3>
      {differences.length === 0 ? (
        <SfEmpty description="完成实盘后由系统对比生成差异；差异经审核后走库存调整单（business-flow.md §10.2）" />
      ) : (
        <div className="sf-pad-tasks-cards">
          {differences.map((diff) => (
            <CountDifferenceCard
              key={String(diff.id)}
              diff={diff}
              skuCode={skuMap.get(String(diff.sku_id))?.code ?? `SKU #${String(diff.sku_id)}`}
              binCode={binMap.get(String(diff.bin_id)) ?? `#${String(diff.bin_id)}`}
            />
          ))}
        </div>
      )}
    </section>
  )

  const registerPanel = (
    <RegisterPanel
      order={order}
      item={selectedItem}
      labels={selectedLabels}
      value={formValue}
      onChange={handleFormChange}
      submitting={registerMutation.isPending}
      onSubmit={submitRegister}
      notify={(text) => messageApi.info(text)}
      onScanSubmit={handleScanCode}
    />
  )

  return (
    <>
      {contextHolder}
      {orientation === 'landscape' ? (
        // 横屏三栏：左=任务卡列表 + 明细行卡；中=任务摘要 + 当前库位商品 + 差异清单；
        // 右=实盘登记操作区（数量大录入位 / 差异预览 / [登记实盘] / 拍照占位 / 扫码）
        <PadPageShell
          ratios={[34, 29, 37]}
          tasksSlot={
            <>
              {taskListNode}
              {itemListNode}
            </>
          }
          contentSlot={
            <>
              {taskSummaryBlock}
              {itemInfoCard}
              {differencesNode}
            </>
          }
          actionSlot={registerPanel}
        />
      ) : (
        // 竖屏堆叠：顶部当前盘点任务摘要 → 任务卡列表 → 当前库位商品 + 实盘录入 + 明细 + 差异
        <PadPageShell
          ratios={[34, 29, 37]}
          tasksSlot={
            <>
              {taskSummaryBlock}
              {taskListNode}
            </>
          }
          contentSlot={
            <>
              {itemInfoCard}
              {registerPanel}
              {itemListNode}
              {differencesNode}
            </>
          }
        />
      )}
      <PadActionBar
        onPause={handlePause}
        onFinish={submitRegister}
        onScanSubmit={handleScanCode}
      />
    </>
  )
}
