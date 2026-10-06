import { useMemo, useState } from 'react'
import { Button, Card, Form, Input, Modal, Radio, Select, Typography, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  CHECK_CLAIM_PERMISSION,
  CHECK_EXECUTE_PERMISSION,
  outboundApi,
  outboundTaskApi,
  type CheckResultType,
  type CheckTask,
  type CheckTaskQuery,
  type CheckTaskStatus,
} from '@/api/outbound'
import { DateCell } from '@/components/table/cells'
import { toStatusKey } from '@/api/masterdata'
import { buildSkuMaps, buildWarehouseMaps, fetchSkuOptions, fetchWarehouseOptions, idKey } from '@/api/options'
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
import { TASK_PRIORITY_OPTIONS, type TaskItem } from '@/api/task'
import { SfCompleteNextButton } from '@/components/task/SfCompleteNextButton'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import type { StatusSemantic } from '@/types/status'
import { formatNumber } from '@/utils/format'

const { Link, Text } = Typography

/** 复核任务状态 → SfStatusTag（models.go:315-319 三值，无 CLAIMED——领取为原子指派不迁移状态；
 * DONE/EXCEPTION 注册表暂无键，以 label/semantic 兜底） */
const CHECK_STATUS_TAG: Record<CheckTaskStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING: { label: '待复核', semantic: 'pending' },
  DONE: { label: '已复核', semantic: 'success' },
  EXCEPTION: { label: '复核异常', semantic: 'danger' },
}

function CheckStatusTag({ status }: { status: CheckTaskStatus }) {
  const meta = CHECK_STATUS_TAG[status]
  return <SfStatusTag status={toStatusKey(status)} label={meta?.label} semantic={meta?.semantic} />
}

/** 状态筛选（handler.go listChecks：status 原样透传大写枚举） */
const STATUS_OPTIONS: Array<{ label: string; value: CheckTaskStatus }> = [
  { label: '待复核', value: 'PENDING' },
  { label: '已复核', value: 'DONE' },
  { label: '复核异常', value: 'EXCEPTION' },
]

/** 复核异常五类（CheckConfirmInput result 中文值域，service_outbound.go:648-649） */
const CHECK_RESULT_OPTIONS: Array<{ label: string; value: CheckResultType }> = [
  { label: '错货', value: '错货' },
  { label: '少货', value: '少货' },
  { label: '多货', value: '多货' },
  { label: '批次错误', value: '批次错误' },
  { label: '序列号错误', value: '序列号错误' },
]

/**
 * 复核管理（GET /api/checks，后端 M2 已交付）：列表列回对 CheckTask 裸模型——
 * 复核重新确认 SKU/条码/数量/批次/序列号/订单（business-flow.md §8.3）；
 * result 为空串 = 复核通过，异常为五类中文值域（错货/少货/多货/批次错误/序列号错误）直出。
 * SKU/仓库 ID 经基础资料 options 本地映射，失败降级为 ID；
 * 写端点交互：PENDING 直接复核确认（service_outbound.go ConfirmCheck 不要求先领取，
 * pass 通过 → DONE、fail 五类异常 → EXCEPTION 登记异常中心）；领取为原子指派不迁移状态；
 * EXCEPTION →(reopen)→ PENDING（异常处置后重开，否则出库单永久卡死 PICKED）。
 */
export default function CheckingPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  const [form] = Form.useForm()
  /** 复核确认弹窗当前任务（null=关闭） */
  const [checkTarget, setCheckTarget] = useState<CheckTask | null>(null)

  // 按钮级权限（后端 RequirePerm 独立校验，前端只隐藏入口）
  const canClaim = canAccess(user, CHECK_CLAIM_PERMISSION)
  const canExecute = canAccess(user, CHECK_EXECUTE_PERMISSION)
  /** 优先级设置权限（效率层一期 B3：sales:check:assign——后端同码校验） */
  const canAssign = canAccess(user, 'sales:check:assign')

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  /** 任务页自动刷新（§2.11）：档位 关/10/30/60 秒；页签隐藏暂停；连续失败 ≥2 次退避停轮 */
  const autoRefresh = useAutoRefresh()

  const list = usePagedList<CheckTask, CheckTaskQuery>({
    queryKey: ['outbound', 'checks'],
    fetch: (q) => outboundTaskApi.checks.list(q),
    urlSync: true,
    refetchInterval: autoRefresh.refetchInterval,
  })

  /** 保存视图的列应用（受控列 API，§2.2 B6）：undefined=非受控（沿用 localStorage 列偏好） */
  const [hiddenColumns, setHiddenColumns] = useState<string[] | undefined>(undefined)

  /** 批量领取（§2.7）：多选行 → POST /api/checks/batch-claim，逐条结果经 BatchResultDrawer 呈现 */
  const [selectedRowKeys, setSelectedRowKeys] = useState<Array<string | number>>([])
  const [batchResult, setBatchResult] = useState<BatchResult | null>(null)
  const [resultOpen, setResultOpen] = useState(false)

  /** 任务落定后刷新列表（状态机由后端守卫，前端无条件 refetch 对齐） */
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['outbound', 'checks'] })

  const claimMutation = useMutation({
    mutationFn: (id: CheckTask['id']) => outboundTaskApi.checks.claim(id),
    onSuccess: (t) => {
      message.success(`已领取任务 ${t.check_no}`)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  /** 批量领取（逐条审计、不整体回滚；成功/跳过/失败三态在抽屉内对账，仅重试失败） */
  const batchClaimMutation = useMutation({
    mutationFn: (ids: Array<number | string>) => outboundTaskApi.checks.batchClaim(ids),
    onSuccess: (res) => {
      setBatchResult(res)
      setResultOpen(true)
      setSelectedRowKeys([])
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  /** 行内设置任务优先级（§2.4：/api/tasks/next checking 分支排序层的数据来源） */
  const priorityMutation = useMutation({
    mutationFn: ({ id, priority }: { id: CheckTask['id']; priority: number }) =>
      outboundTaskApi.checks.setPriority(id, priority),
    onSuccess: (res) => {
      message.success(`优先级已更新为 ${res.priority}`)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })
  const confirmMutation = useMutation({
    mutationFn: ({
      id,
      payload,
    }: {
      id: CheckTask['id']
      payload: { pass: boolean; result?: CheckResultType; serial?: string }
    }) => outboundTaskApi.checks.confirm(id, payload),
    onSuccess: (t) => {
      message.success(t.status === 'DONE' ? `任务 ${t.check_no} 复核通过` : `任务 ${t.check_no} 已登记复核异常`)
      setCheckTarget(null)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })
  const reopenMutation = useMutation({
    mutationFn: (id: CheckTask['id']) => outboundTaskApi.checks.reopen(id),
    onSuccess: (t) => {
      message.success(`任务 ${t.check_no} 已重开为待复核`)
      invalidate()
    },
    onError: (e) => message.error(resolveErrorMessage(e)),
  })

  const openCheck = (record: CheckTask) => {
    setCheckTarget(record)
    form.resetFields()
    form.setFieldsValue({ pass: 'pass' })
  }

  /** 弹窗提交（Promise 化，供按钮组 onComplete 消费）：表单校验失败 / 后端拒绝均 reject——
      错误提示仍由 confirmMutation.onError 负责（SfCompleteNextButton 契约：组件不重复弹错）。
      fail 路径登记异常后同样视为「完成当前」，可继续推进下一条 */
  const submitCheckConfirm = async (): Promise<unknown> => {
    if (!checkTarget) return undefined
    const v = await form.validateFields()
    const pass = v.pass === 'pass'
    const serial = typeof v.serial === 'string' && v.serial.trim() ? v.serial.trim() : undefined
    return confirmMutation.mutateAsync({
      id: checkTarget.id,
      payload: pass ? { pass: true, serial } : { pass: false, result: v.result, serial },
    })
  }

  /**
   * 下一条任务的领取（§2.4）：checking 候选池=PENDING 且 assignee ∈ (本人, 0)——
   * 未指派任务经 claim 原子指派；已指派本人 → claim 冲突吞掉（幂等，视为可进入，
   * 与 PadPutawayPage 同口径）；被他人指派/无领取权限 → 由 gotoNext 以详情真实状态复核。
   */
  const claimNextCheckTask = async (next: TaskItem) => {
    if (next.raw_status !== 'PENDING') return
    try {
      await outboundTaskApi.checks.claim(next.id)
      message.success(`已领取任务 ${next.task_no}`)
    } catch {
      // 指派冲突：视为可进入，由 gotoNextCheckTask 以详情真实状态裁决
    }
  }

  /**
   * 进入下一条（§2.4「完成后自动下一条」）：GET /api/outbounds/{no} 详情还原完整
   * CheckTask（TaskItem 无 SKU/序列号——复核表单需完整任务对象，禁止拼半截对象），
   * id 精确匹配；复核不要求先领取（service_outbound.go ConfirmCheck），PENDING 即可进入；
   * 其余状态提示并停留列表。source_no=出库单号
   * （workbench.go nextTaskCheckBranch 投影 ck.outbound_no AS source_no）。
   */
  const gotoNextCheckTask = (next: TaskItem) => {
    const no = next.source_no
    if (!no) {
      message.warning(`下一条任务 ${next.task_no} 缺少出库单号，请从列表处理`)
      invalidate()
      return
    }
    void outboundApi
      .get(no)
      .then((detail) => {
        invalidate()
        const hit = (detail.checks ?? []).find((c) => String(c.id) === String(next.id))
        if (!hit) {
          message.warning(`下一条任务 ${next.task_no} 未能从出库单详情还原，请从列表处理`)
          return
        }
        if (hit.status !== 'PENDING') {
          message.info(
            `下一条任务 ${next.task_no} 当前为「${CHECK_STATUS_TAG[hit.status]?.label ?? hit.status}」，请从列表处理`,
          )
          return
        }
        setCheckTarget(hit)
        form.resetFields()
        form.setFieldsValue({ pass: 'pass' })
      })
      .catch((e) => {
        invalidate()
        message.error(`进入下一条任务失败：${resolveErrorMessage(e)}`)
      })
  }

  /** 复核结论联动：fail 时显示异常类型选择；序列号任务显示扫描输入 */
  const passValue = Form.useWatch('pass', form)

  // SKU / 仓库 ID → 编码/名称（options.ts：一次取全基础资料，失败降级为 ID）
  const skuOptions = useQuery({
    queryKey: ['outbound', 'sku-options'],
    queryFn: fetchSkuOptions,
  })
  const warehouseOptions = useQuery({
    queryKey: ['outbound', 'warehouse-options'],
    queryFn: fetchWarehouseOptions,
  })
  const skuMaps = useMemo(() => buildSkuMaps(skuOptions.data ?? []), [skuOptions.data])
  const warehouseNames = useMemo(
    () => buildWarehouseMaps(warehouseOptions.data ?? []).name,
    [warehouseOptions.data],
  )

  const columns: ColumnsType<CheckTask> = [
    { title: '复核任务号', dataIndex: 'check_no', width: 160, fixed: 'left' },
    {
      title: '出库单号',
      dataIndex: 'outbound_no',
      width: 160,
      render: (v: string, record: CheckTask) => (
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
      render: (_: unknown, record: CheckTask) => {
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
    // 序列号 SKU 一行一件（models.go:143-164），非序列号任务为空串
    { title: '序列号', dataIndex: 'serial_no', width: 140, render: (v: string) => v || '-' },
    {
      title: '复核数量',
      dataIndex: 'qty',
      width: 100,
      align: 'right',
      render: (v: number) => <span className="sf-num">{formatNumber(v)}</span>,
    },
    {
      title: '复核结果',
      dataIndex: 'result',
      width: 110,
      render: (v: string) => v || '-',
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (v: CheckTaskStatus) => <CheckStatusTag status={v} />,
    },
    { title: '复核人', dataIndex: 'assignee_name', width: 100, render: (v: string) => v || '-' },
    {
      title: '复核时间',
      dataIndex: 'done_at',
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
    // 优先级列（§2.4 行内设置入口）：持 sales:check:assign 时可编辑（0–9，终态后端 409 拒绝）
    {
      title: '优先级',
      dataIndex: 'priority',
      width: 92,
      align: 'center' as const,
      render: (v: number, record: CheckTask) =>
        canAssign ? (
          <Select
            size="small"
            value={v ?? 0}
            style={{ width: 66 }}
            options={TASK_PRIORITY_OPTIONS}
            disabled={record.status === 'DONE' || record.status === 'EXCEPTION'}
            onChange={(next) => priorityMutation.mutate({ id: record.id, priority: next })}
          />
        ) : (
          <span className="sf-num">{v ?? 0}</span>
        ),
    },
    // 操作列按状态机装配（仅持有对应权限时渲染）：
    // PENDING → 领取（原子指派）+ 复核确认（可直接做）；EXCEPTION → 重开
    ...(canClaim || canExecute
      ? [
          {
            title: '操作',
            key: 'actions',
            fixed: 'right' as const,
            width: 190,
            render: (_: unknown, record: CheckTask) => (
              <span style={{ whiteSpace: 'nowrap' }}>
                {record.status === 'PENDING' && canClaim && (
                  <SfConfirm
                    okText="领取"
                    confirming={claimMutation.isPending}
                    title={`领取任务 ${record.check_no}？领取仅指派复核人，不迁移状态。`}
                    onConfirm={() => claimMutation.mutate(record.id)}
                  >
                    <Button type="link" size="small">
                      领取
                    </Button>
                  </SfConfirm>
                )}
                {record.status === 'PENDING' && canExecute && (
                  <Button type="link" size="small" onClick={() => openCheck(record)}>
                    复核
                  </Button>
                )}
                {record.status === 'EXCEPTION' && canExecute && (
                  <SfConfirm
                    okText="重开"
                    confirming={reopenMutation.isPending}
                    title={`重开任务 ${record.check_no}？异常处置完成后重新复核（EXCEPTION→PENDING）。`}
                    onConfirm={() => reopenMutation.mutate(record.id)}
                  >
                    <Button type="link" size="small">
                      重开
                    </Button>
                  </SfConfirm>
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
        title="复核管理"
        subtitle="复核确认：SKU / 条码 / 数量 / 批次 / 序列号 / 订单"
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
                pageKey="outbound.checking"
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
        <SfTable<CheckTask>
          hiddenColumns={hiddenColumns}
          onHiddenColumnsChange={setHiddenColumns}
          storageKey="outbound-checks"
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
          emptyText="当前筛选条件下没有复核任务"
          scrollX={1850}
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

      {/* 复核确认弹窗（PENDING→DONE/EXCEPTION，不要求先领取）：
          通过时序列号任务的扫描值必须与任务序列号一致（service_outbound.go:630-631）；
          异常路径 result 必须为五类之一，登记异常中心。
          footer 按钮组（§2.4 完成后自动下一条）：完成后调 GET /api/tasks/next 重新查询
          （后端 SQL 排序，前端零推算）→ 领取指派 → 以出库单详情还原完整任务进入弹窗 */}
      <Modal
        open={!!checkTarget}
        title={`复核确认 · ${checkTarget?.check_no ?? ''}`}
        onCancel={() => setCheckTarget(null)}
        footer={
          <SfCompleteNextButton
            onComplete={submitCheckConfirm}
            nextQuery={{ task_type: 'checking', current_task_id: checkTarget?.id }}
            onClaimNext={claimNextCheckTask}
            onNavigateNext={gotoNextCheckTask}
            completing={confirmMutation.isPending}
            backText="关闭"
          />
        }
      >
        {checkTarget && (
          <Form form={form} layout="vertical">
            <Form.Item label="复核结论" name="pass" rules={[{ required: true }]}>
              <Radio.Group
                options={[
                  { label: '通过', value: 'pass' },
                  { label: '异常', value: 'fail' },
                ]}
                optionType="button"
                buttonStyle="solid"
              />
            </Form.Item>
            {passValue === 'fail' && (
              <Form.Item
                label="异常类型"
                name="result"
                rules={[{ required: true, message: '请选择异常类型（五类之一）' }]}
              >
                <Select options={CHECK_RESULT_OPTIONS} placeholder="错货 / 少货 / 多货 / 批次错误 / 序列号错误" />
              </Form.Item>
            )}
            {checkTarget.serial_no && (
              <Form.Item
                label={`序列号扫描（任务序列号：${checkTarget.serial_no}）`}
                name="serial"
                extra={
                  passValue === 'pass'
                    ? '复核通过时扫描值必须与任务序列号一致'
                    : '与任务序列号不一致可作为「序列号错误」的佐证记录'
                }
                rules={[
                  ...(passValue === 'pass'
                    ? [{ required: true, message: '序列号任务复核通过必须扫描序列号' }]
                    : []),
                  {
                    validator: (_, v) => {
                      if (passValue !== 'pass') return Promise.resolve()
                      const s = String(v ?? '').trim()
                      if (!s || s === checkTarget.serial_no) return Promise.resolve()
                      return Promise.reject(new Error('扫描值与任务序列号不一致（不一致请改选「异常」并选序列号错误）'))
                    },
                  },
                ]}
              >
                <Input placeholder="扫描或输入序列号" />
              </Form.Item>
            )}
          </Form>
        )}
      </Modal>
    </div>
  )
}
