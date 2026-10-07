import { useMemo, useState } from 'react'
import { Button, Card, message } from 'antd'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ColumnsType } from 'antd/es/table'
import { useNavigate } from 'react-router'
import { DateCell } from '@/components/table/cells'
import { taskApi, type TaskItem, type TaskQuery, type TaskStatus, type TaskType } from '@/api/task'
import type { BatchResult } from '@/api/printing'
import { putawayApi } from '@/api/putaway'
import { outboundTaskApi } from '@/api/outbound'
import { resolveErrorMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { useAutoRefresh } from '@/hooks/useAutoRefresh'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfAutoRefreshSelect } from '@/components/common/SfAutoRefreshSelect'
import { BatchResultDrawer } from '@/components/batch/BatchResultDrawer'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { formatNumber } from '@/utils/format'

/** 任务类型文案（api/task.ts TaskType 三值；未知值回退展示原始值） */
const TYPE_LABEL: Record<string, string | undefined> = {
  putaway: '上架任务',
  picking: '拣货任务',
  checking: '复核任务',
}

/** 类型筛选选项（后端仅映射 putaway/picking/checking 三任务表——packing/moving/counting
 * 传值 400，api.md §9 平台批） */
const TYPE_OPTIONS: Array<{ label: string; value: TaskType }> = [
  { label: '上架任务', value: 'putaway' },
  { label: '拣货任务', value: 'picking' },
  { label: '复核任务', value: 'checking' },
]

/** 状态选项（统一五值；exception 为第五统一值——picking/checking 原态 EXCEPTION 映射） */
const STATUS_OPTIONS: Array<{ label: string; value: TaskStatus }> = [
  { label: '待处理', value: 'pending' },
  { label: '进行中', value: 'in_progress' },
  { label: '已完成', value: 'completed' },
  { label: '已取消', value: 'cancelled' },
  { label: '异常', value: 'exception' },
]

const COLUMNS: ColumnsType<TaskItem> = [
  { title: '任务号', dataIndex: 'task_no', width: 150, fixed: 'left' },
  {
    title: '任务类型',
    dataIndex: 'task_type',
    width: 110,
    render: (v: string) => TYPE_LABEL[v] ?? v,
  },
  // 关联单号列的交互渲染由组件注入（需 navigate/权限上下文），模块层仅保留静态列骨架
  { title: '关联单号', dataIndex: 'source_no', width: 150 },
  { title: '仓库', dataIndex: 'warehouse_name', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '计划数量',
    dataIndex: 'total_qty',
    width: 100,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '已完成',
    dataIndex: 'completed_qty',
    width: 110,
    align: 'right',
    render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 100,
    render: (v: string) => <SfStatusTag status={v} />,
  },
  { title: '负责人', dataIndex: 'assignee_name', width: 100, render: (v?: string) => v ?? '-' },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 170,
    render: (v: string) => <DateCell value={v} />,
  },
  {
    title: '完成时间',
    dataIndex: 'completed_at',
    width: 170,
    render: (v?: string) => <DateCell value={v} />,
  },
]

/** 我的任务（/tasks，menu.tsx 仓储中心子菜单；GET /api/tasks 2026-10-05 平台批已交付，
 * 行列含 raw_status 原表态）。筛选仅后端契约参数 task_type/status/warehouse_code
 * （workbench.go:865-891 仅读三键）——keyword 后端不消费，假筛选项已摘除（api.md §9 平台批）。 */
export default function MyTasksPage() {
  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const user = useAuthStore((s) => s.user)
  /** 任务页自动刷新（§2.11）：档位 关/10/30/60 秒；页签隐藏暂停；连续失败 ≥2 次退避停轮 */
  const autoRefresh = useAutoRefresh()

  const list = usePagedList<TaskItem, TaskQuery>({
    queryKey: ['task', 'my'],
    fetch: (q) => taskApi.list(q),
    urlSync: true,
    refetchInterval: autoRefresh.refetchInterval,
  })

  // 联动批次一 L9（2026-10-07）：关联单号按任务类型映射列表预筛——source_no 语义经
  // workbench.go:746/760/771 实证（putaway=入库单号、picking/checking=出库单号）。
  // 入库列表 source_no 参数筛的是「来源单号」（PO 号，repository.go:133 注释），单号本身
  // 走 keyword（匹配 inbound_no/source_no）；出库列表 outbound_no 即出库单号本身。
  // 无权限回退纯文本（canAccess 仅体验优化，目标列表仍有最终校验）。
  const columns = useMemo<ColumnsType<TaskItem>>(
    () =>
      COLUMNS.map((column) => {
        if (!('dataIndex' in column) || column.dataIndex !== 'source_no') return column
        return {
          ...column,
          render: (v: string | undefined, record: TaskItem) => {
            if (!v) return '-'
            const target =
              record.task_type === 'putaway'
                ? { path: `/inbound?keyword=${encodeURIComponent(v)}`, permission: 'inbound:view' }
                : { path: `/outbound?outbound_no=${encodeURIComponent(v)}`, permission: 'outbound:view' }
            if (!canAccess(user, target.permission)) return v
            return (
              <Button type="link" size="small" onClick={() => navigate(target.path)}>
                {v}
              </Button>
            )
          },
        }
      }),
    [navigate, user],
  )

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  /** 批量领取（§2.7）：行 id 即各任务表主键（putaway_tasks/pick_tasks/check_tasks），
   * 按行 task_type 分组调用三个批量端点后合并为一份批量结果（逐条三态在抽屉内对账） */
  const [selectedRowKeys, setSelectedRowKeys] = useState<Array<string | number>>([])
  const [batchResult, setBatchResult] = useState<BatchResult | null>(null)
  const [resultOpen, setResultOpen] = useState(false)
  /** 任一类任务的领取权限（无权限不渲染批量入口；具体行权限仍由后端逐条裁决） */
  const canClaimAny =
    canAccess(user, 'purchase:putaway:claim') ||
    canAccess(user, 'sales:pick:claim') ||
    canAccess(user, 'sales:check:claim')

  const batchClaimMutation = useMutation({
    mutationFn: async (keys: Array<string | number>) => {
      const putawayIds: Array<string | number> = []
      const pickIds: Array<string | number> = []
      const checkIds: Array<string | number> = []
      for (const key of keys) {
        const row = list.items.find((item) => String(item.id) === String(key))
        if (!row) continue
        if (row.task_type === 'putaway') putawayIds.push(row.id)
        else if (row.task_type === 'picking') pickIds.push(row.id)
        else if (row.task_type === 'checking') checkIds.push(row.id)
      }
      const parts: BatchResult[] = []
      if (putawayIds.length > 0) parts.push(await putawayApi.batchClaim(putawayIds))
      if (pickIds.length > 0) parts.push(await outboundTaskApi.picks.batchClaim(pickIds))
      if (checkIds.length > 0) parts.push(await outboundTaskApi.checks.batchClaim(checkIds))
      return parts.reduce<BatchResult>(
        (acc, part) => ({
          total: acc.total + part.total,
          success_count: acc.success_count + part.success_count,
          failed_count: acc.failed_count + part.failed_count,
          skipped_count: acc.skipped_count + part.skipped_count,
          results: [...acc.results, ...part.results],
        }),
        { total: 0, success_count: 0, failed_count: 0, skipped_count: 0, results: [] },
      )
    },
    onSuccess: (res) => {
      setBatchResult(res)
      setResultOpen(true)
      setSelectedRowKeys([])
      void queryClient.invalidateQueries({ queryKey: ['task', 'my'] })
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  return (
    <div className="sf-page">
      <SfPageHeader title="我的任务" subtitle="上架 / 拣货 / 复核 作业任务" />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'task_type', label: '任务类型', control: 'select', options: TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            { name: 'warehouse_code', label: '仓库', control: 'input', placeholder: '仓库编码' },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图 + 自动刷新 + 批量领取：与查询/重置同行渲染
             （用户口径：批量按钮与其他按钮同一行，不占独立面板） */
          extraActions={
            <>
              <SfViewBar
                pageKey="task.my"
                mode="url"
                paged={{ applyFilters: list.applyFilters, onPageChange: list.onPageChange }}
                appliedFilters={list.params as unknown as Record<string, unknown>}
                currentFilters={list.formValues}
                currentPageSize={list.pagination.pageSize}
                currentHiddenColumns={hiddenColumns}
                onHiddenColumnsChange={setHiddenColumns}
              />
              <SfAutoRefreshSelect value={autoRefresh.seconds} onChange={autoRefresh.setSeconds} />
              {canClaimAny && selectedRowKeys.length > 0 && (
                <Button
                  type="primary"
                  loading={batchClaimMutation.isPending}
                  onClick={() => batchClaimMutation.mutate(selectedRowKeys)}
                >
                  批量领取（{selectedRowKeys.length}）
                </Button>
              )}
            </>
          }
        />
        <SfTable<TaskItem>
          storageKey="my-tasks"
          rowKey="id"
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          /* 批量领取（§2.7）：选中行按类型分组调用三个批量端点，结果统一落 BatchResultDrawer；
             批量按钮已上移至搜索行 extraActions */
          rowSelection={{
            selectedRowKeys,
            onChange: (keys) => setSelectedRowKeys(keys as Array<string | number>),
          }}
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有任务"
          scrollX={1360}
        />
      </Card>

      {/* 批量结果统一承载面（§2.7）：计数条 + 逐条三态 + 仅重试失败（按类型分组重发同一端点） */}
      <BatchResultDrawer
        open={resultOpen}
        result={batchResult}
        onRetry={(failedIds) => batchClaimMutation.mutate(failedIds)}
        retrying={batchClaimMutation.isPending}
        onClose={() => setResultOpen(false)}
      />
    </div>
  )
}
