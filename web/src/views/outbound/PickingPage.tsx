import { useMemo, useState } from 'react'
import { Button, Card, Form, Input, InputNumber, Modal, Select, Typography, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  PICK_CLAIM_PERMISSION,
  PICK_EXECUTE_PERMISSION,
  outboundTaskApi,
  type PickTask,
  type PickTaskQuery,
  type PickTaskStatus,
} from '@/api/outbound'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import {
  buildBinCodeMap,
  buildSkuMaps,
  buildWarehouseMaps,
  fetchBinOptions,
  fetchSkuOptions,
  fetchWarehouseOptions,
  idKey,
} from '@/api/options'
import { usePagedList } from '@/hooks/usePagedList'
import { useAutoRefresh } from '@/hooks/useAutoRefresh'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfViewBar } from '@/components/table/SfViewBar'
import { SfAutoRefreshSelect } from '@/components/common/SfAutoRefreshSelect'
import { BatchResultDrawer } from '@/components/batch/BatchResultDrawer'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { resolveErrorMessage } from '@/api/client'
import type { BatchResult } from '@/api/printing'
import { TASK_PRIORITY_OPTIONS } from '@/api/task'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 拣货任务状态 → SfStatusTag（models.go:294-301 六值；
 * CLAIMED/EXCEPTION 注册表暂无键，以 label/semantic 兜底） */
const PICK_STATUS_TAG: Record<PickTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待领取', semantic: 'pending' },
  CLAIMED: { label: '已领取', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  EXCEPTION: { label: '拣货异常', semantic: 'danger' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

function PickStatusTag({ status }: { status: PickTaskStatus }) {
  const meta = PICK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listPicks：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: PickTaskStatus }> = [
  { label: '待领取', value: 'PENDING' },
  { label: '已领取', value: 'CLAIMED' },
  { label: '拣货中', value: 'PICKING' },
  { label: '已拣货', value: 'PICKED' },
  { label: '拣货异常', value: 'EXCEPTION' },
  { label: '已取消', value: 'CANCELLED' },
]

/** 可选维度 ID 0 值显示占位符（0=非批次/无来源库位，LedgerPage 同口径） */
function renderIdOrDash(value: number): string {
  return String(value) === '0' ? '-' : String(value)
}

/**
 * 拣货管理（GET /api/picks，后端 M2 已交付）：列表列回对 PickTask 裸模型——
 * 任务内容按 business-flow.md §8.2：SKU → 来源库位 → 数量 → 操作人 → 完成时间；
 * SKU/库位/仓库 ID 经基础资料 options 本地映射，映射失败降级为 ID。
 * 领取/确认写端点（PUT /api/picks/{id}/claim|confirm）：
 * PENDING →(claim 原子抢占)→ CLAIMED →(confirm)→ PICKED；CLAIMED →(exception)→ EXCEPTION。
 * 领取无表单走 SfConfirm；确认/异常为受控弹窗表单（后端校验：picked_qty>0 且 ≤ 任务量、
 * 序列号 SKU 逐件 serials、异常 reason 必填——service_outbound.go ConfirmPick/ReportPickException）。
 */
export default function PickingPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const [form] = Form.useForm()
  /** 拣货确认弹窗当前任务（null=关闭） */
  const [confirmTarget, setConfirmTarget] = useState<PickTask | null>(null)
  /** 异常上报弹窗当前任务（null=关闭） */
  const [exceptionTarget, setExceptionTarget] = useState<PickTask | null>(null)

  // 按钮级权限（后端 RequirePerm 独立校验，前端只隐藏入口）
  const canClaim = canAccess(user, PICK_CLAIM_PERMISSION)
  const canExecute = canAccess(user, PICK_EXECUTE_PERMISSION)
  /** 优先级设置权限（效率层一期 B3：sales:pick:assign——后端同码校验） */
  const canAssign = canAccess(user, 'sales:pick:assign')

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  /** 任务页自动刷新（§2.11）：档位 关/10/30/60 秒；页签隐藏暂停；连续失败 ≥2 次退避停轮 */
  const autoRefresh = useAutoRefresh()

  const list = usePagedList<PickTask, PickTaskQuery>({
    queryKey: ['outbound', 'picks'],
    fetch: (q) => outboundTaskApi.picks.list(q),
    urlSync: true,
    refetchInterval: autoRefresh.refetchInterval,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  /** 批量领取（§2.7）：多选行 → POST /api/picks/batch-claim，逐条结果经 BatchResultDrawer 呈现 */
  const [selectedRowKeys, setSelectedRowKeys] = useState<Array<string | number>>([])
  const [batchResult, setBatchResult] = useState<BatchResult | null>(null)
  const [resultOpen, setResultOpen] = useState(false)

  /** 任务落定后刷新列表（状态机由后端守卫，前端无条件 refetch 对齐） */
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['outbound', 'picks'] })

  const claimMutation = useMutation({
    mutationFn: (id: PickTask['id']) => outboundTaskApi.picks.claim(id),
    onSuccess: (t) => {
      message.success(`已领取任务 ${t.pick_no}`)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  /** 批量领取（逐条审计、不整体回滚；成功/跳过/失败三态在抽屉内对账，仅重试失败） */
  const batchClaimMutation = useMutation({
    mutationFn: (ids: Array<number | string>) => outboundTaskApi.picks.batchClaim(ids),
    onSuccess: (res) => {
      setBatchResult(res)
      setResultOpen(true)
      setSelectedRowKeys([])
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  /** 行内设置任务优先级（§2.4：/api/tasks/next 排序层的数据来源，不设置即排序层无意义） */
  const priorityMutation = useMutation({
    mutationFn: ({ id, priority }: { id: PickTask['id']; priority: number }) =>
      outboundTaskApi.picks.setPriority(id, priority),
    onSuccess: (res) => {
      message.success(`优先级已更新为 ${res.priority}`)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })
  const confirmMutation = useMutation({
    mutationFn: ({ id, payload }: { id: PickTask['id']; payload: { picked_qty: number; serials?: string[] } }) =>
      outboundTaskApi.picks.confirm(id, payload),
    onSuccess: (t) => {
      message.success(`任务 ${t.pick_no} 拣货确认完成`)
      setConfirmTarget(null)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })
  const exceptionMutation = useMutation({
    mutationFn: ({ id, reason }: { id: PickTask['id']; reason: string }) =>
      outboundTaskApi.picks.reportException(id, { reason }),
    onSuccess: (t) => {
      message.success(`任务 ${t.pick_no} 已上报异常`)
      setExceptionTarget(null)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  const openConfirm = (record: PickTask) => {
    setConfirmTarget(record)
    form.resetFields()
    form.setFieldsValue({ picked_qty: record.qty })
  }

  // SKU / 仓库 / 库位 ID → 编码/名称（options.ts：一次取全基础资料，失败降级为 ID）
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const binOptions = useQuery({
    queryKey: ['outbound', 'bin-options'],
    queryFn: fetchBinOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )
  const binCodes = useMemo(() => buildBinCodeMap(binOptions.data ?? []), [binOptions.data])

  /** 序列号 SKU 集合（确认时逐件采集 serials，inventory-rules.md §8.2） */
  const serialManagedIds = useMemo(
    () => new Set((skuOptions.data ?? []).filter((s) => s.is_serial_managed).map((s) => idKey(s.id))),
    [skuOptions.data],
  )

  /** 任务落定后刷新列表（状态机由后端守卫，前端无条件 refetch 对齐） */

  const columns: ColumnsType<PickTask> = [
    { title: '拣货任务号', dataIndex: 'pick_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: PickTask) => (
        <Link onClick={() => navigate(`/outbound/${record.outbound_no}`)}>{v}</Link>
      ),
    },
    {
      title: 'SKU 编码',
      dataIndex: 'sku_id',
      width: 130,
      render: (v: number) => skuMaps.code.get(idKey(v)) ?? idKey(v),
    },
    {
      title: '商品名称',
      dataIndex: 'sku_id',
      key: 'sku_name',
      width: 180,
      ellipsis: true,
      render: (_: unknown, record: PickTask) => {
        const name = skuMaps.name.get(idKey(record.sku_id))
        return name ? (
          <Text style={{ maxWidth: 170 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        ) : (
          '-'
        )
      },
    },
    {
      title: '来源库位',
      dataIndex: 'source_bin_id',
      width: 110,
      render: (v: number) => binCodes.get(idKey(v)) ?? renderIdOrDash(v),
    },
    { title: '批次 ID', dataIndex: 'batch_id', width: 90, render: renderIdOrDash },
    {
      title: '应拣数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '已拣数量',
      dataIndex: 'picked_qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: PickTaskStatus) => <PickStatusTag status={v} />,
    },
    { title: '领取人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '拣货时间',
      dataIndex: 'picked_at',
      width: 170,
      render: (v: string | null) => <DateCell value={v} />,
    },
    {
      title: '仓库',
      dataIndex: 'warehouse_id',
      width: 120,
      render: (v: number) => {
        const name = warehouseNames.get(idKey(v)) ?? idKey(v)
        return (
          <Text style={{ maxWidth: 110 }} ellipsis={{ tooltip: name }}>
            {name}
          </Text>
        )
      },
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <DateCell value={v} />,
    },
    // 优先级列（§2.4 行内设置入口）：持 sales:pick:assign 时可编辑（0–9，终态后端 409 拒绝）；
    // 该值是 /api/tasks/next「mine > priority > 超时 > created_at」排序层的数据来源
    {
      title: '优先级',
      dataIndex: 'priority',
      width: 92,
      align: 'center' as const,
      render: (v: number, record: PickTask) =>
        canAssign ? (
          <Select
            size="small"
            value={v ?? 0}
            style={{ width: 66 }}
            options={TASK_PRIORITY_OPTIONS}
            disabled={record.status === 'PICKED' || record.status === 'CANCELLED'}
            onChange={(next) => priorityMutation.mutate({ id: record.id, priority: next })}
          />
        ) : (
          <span className="sf-num">{v ?? 0}</span>
        ),
    },
    // 操作列按状态机装配（仅持有对应权限时渲染）：
    // PENDING → 领取（原子抢占）；CLAIMED → 拣货确认 + 异常上报
    ...(canClaim || canExecute
      ? [
          {
            title: '操作',
            key: 'actions',
            fixed: 'right' as const,
            width: 190,
            render: (_: unknown, record: PickTask) => (
              <span style={{ whiteSpace: 'nowrap' }}>
                {record.status === 'PENDING' && canClaim && (
                  <SfConfirm
                    okText="领取"
                    confirming={claimMutation.isPending}
                    title={`领取任务 ${record.pick_no}？领取后由您执行拣货确认。`}
                    onConfirm={() => claimMutation.mutate(record.id)}
                  >
                    <Button type="link" size="small">
                      领取
                    </Button>
                  </SfConfirm>
                )}
                {record.status === 'CLAIMED' && canExecute && (
                  <>
                    <Button type="link" size="small" onClick={() => openConfirm(record)}>
                      拣货确认
                    </Button>
                    <Button
                      type="link"
                      size="small"
                      danger
                      onClick={() => {
                        setExceptionTarget(record)
                        form.resetFields()
                      }}
                    >
                      异常上报
                    </Button>
                  </>
                )}
              </span>
            ),
          },
        ]
      : []),
  ]

  return (
    <div className="sf-page">
      <SfPageHeader
        title="拣货管理"
        subtitle="拣货任务：SKU → 来源库位 → 数量 → 操作人 → 完成时间"
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'outbound_no', label: '出库单号', control: 'input', placeholder: '出库单号（精确）' },
            { name: 'status', label: '状态', control: 'select', options: STATUS_OPTIONS },
            {
              name: 'warehouse_id',
              label: '仓库',
              control: 'select',
              options: (warehouseOptions.data ?? []).map((w) => ({
                label: `${w.name}（${w.code}）`,
                value: idKey(w.id),
              })),
            },
          ]}
          initialValues={list.params}
          onSearch={list.applyFilters}
          /* 保存视图 + 自动刷新档位：与查询/重置同行渲染（不再占表格工具栏独立一行） */
          extraActions={
            <>
              <SfViewBar
                pageKey="outbound.picking"
                mode="url"
                paged={{ applyFilters: list.applyFilters, onPageChange: list.onPageChange }}
                appliedFilters={list.params as unknown as Record<string, unknown>}
                currentFilters={list.formValues}
                currentPageSize={list.pagination.pageSize}
                currentHiddenColumns={hiddenColumns}
                onHiddenColumnsChange={setHiddenColumns}
              />
              <SfAutoRefreshSelect value={autoRefresh.seconds} onChange={autoRefresh.setSeconds} />
            </>
          }
        />
        <SfTable<PickTask>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="outbound-picks"
          rowKey="id"
          /* 批量领取（§2.7）：选中 PENDING 行后经统一批量端点领取，逐条结果落抽屉 */
          rowSelection={{
            selectedRowKeys,
            onChange: (keys) => setSelectedRowKeys(keys as Array<string | number>),
          }}
          bulkActions={
            canClaim ? (
              <Button
                type="primary"
                size="small"
                loading={batchClaimMutation.isPending}
                onClick={() => batchClaimMutation.mutate(selectedRowKeys)}
              >
                批量领取（{selectedRowKeys.length}）
              </Button>
            ) : undefined
          }
          columns={columns}
          dataSource={list.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="当前筛选条件下没有拣货任务"
          scrollX={1900}
        />
      </Card>

      {/* 批量结果统一承载面（§2.7）：计数条 + 逐条三态 + 仅重试失败（以失败 ids 重发同一端点） */}
      <BatchResultDrawer
        open={resultOpen}
        result={batchResult}
        onRetry={(failedIds) => batchClaimMutation.mutate(failedIds)}
        retrying={batchClaimMutation.isPending}
        onClose={() => setResultOpen(false)}
      />

      {/* 拣货确认弹窗（CLAIMED→PICKED）：实拣数量 ≤ 任务量（后端 ErrPickExceed 守卫）；
          序列号 SKU 逐件采集，行数须与实拣数量一致（service_outbound.go validateSerialsForPick） */}
      <Modal
        open={!!confirmTarget}
        title={`拣货确认 · ${confirmTarget?.pick_no ?? ''}`}
        okText="确认拣货"
        confirmLoading={confirmMutation.isPending}
        onCancel={() => setConfirmTarget(null)}
        onOk={() => {
          form
            .validateFields()
            .then((v) => {
              const lines =
                typeof v.serials === 'string'
                  ? v.serials
                      .split('\n')
                      .map((s: string) => s.trim())
                      .filter(Boolean)
                  : []
              confirmMutation.mutate({
                id: confirmTarget!.id,
                payload: {
                  picked_qty: v.picked_qty,
                  ...(lines.length > 0 ? { serials: lines } : {}),
                },
              })
            })
            .catch(() => {
              // 表单校验失败：Form.Item 已内联提示
            })
        }}
      >
        {confirmTarget && (
          <Form form={form} layout="vertical">
            <Form.Item
              label={`实拣数量（应拣 ${formatNumber(confirmTarget.qty)}）`}
              name="picked_qty"
              rules={[
                { required: true, message: '实拣数量必填' },
                {
                  validator: (_, v) =>
                    v > 0 && v <= confirmTarget.qty
                      ? Promise.resolve()
                      : Promise.reject(new Error(`实拣数量须大于 0 且不超过应拣数量 ${formatNumber(confirmTarget.qty)}`)),
                },
              ]}
            >
              <InputNumber min={0.01} max={confirmTarget.qty} style={{ width: 160 }} />
            </Form.Item>
            {serialManagedIds.has(idKey(confirmTarget.sku_id)) && (
              <Form.Item
                label="序列号采集"
                name="serials"
                extra={`该 SKU 为序列号管理：每行一个序列号，数量须与实拣数量一致`}
                rules={[
                  {
                    required: true,
                    validator: (_, v) => {
                      const lines = String(v ?? '')
                        .split('\n')
                        .map((s: string) => s.trim())
                        .filter(Boolean)
                      if (lines.length === 0) return Promise.reject(new Error('序列号必填（每行一个）'))
                      const qty = Number(form.getFieldValue('picked_qty'))
                      if (qty && lines.length !== qty) {
                        return Promise.reject(new Error(`序列号数量（${lines.length}）须与实拣数量一致（${qty}）`))
                      }
                      return Promise.resolve()
                    },
                  },
                ]}
              >
                <Input.TextArea rows={4} placeholder={'每行一个序列号，例如：\nSN0001\nSN0002'} />
              </Form.Item>
            )}
          </Form>
        )}
      </Modal>

      {/* 拣货异常上报弹窗（CLAIMED→EXCEPTION）：reason 必填，登记异常中心 */}
      <Modal
        open={!!exceptionTarget}
        title={`拣货异常上报 · ${exceptionTarget?.pick_no ?? ''}`}
        okText="上报异常"
        okButtonProps={{ danger: true, loading: exceptionMutation.isPending }}
        onCancel={() => setExceptionTarget(null)}
        onOk={() => {
          form
            .validateFields()
            .then((v) => exceptionMutation.mutate({ id: exceptionTarget!.id, reason: v.reason.trim() }))
            .catch(() => {
              // 表单校验失败：Form.Item 已内联提示
            })
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            label="异常原因"
            name="reason"
            rules={[{ required: true, whitespace: true, message: '异常原因必填' }]}
          >
            <Input.TextArea rows={3} placeholder="如：库位实物缺失 / 数量不足" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
