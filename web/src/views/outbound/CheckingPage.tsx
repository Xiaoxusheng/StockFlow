import { useMemo, useState } from 'react'
import { Button, Card, Form, Input, Modal, Radio, Select, Typography, message } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import type { ColumnsType } from 'antd/es/table'
import {
  CHECK_CLAIM_PERMISSION,
  CHECK_EXECUTE_PERMISSION,
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
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfTable } from '@/components/table/SfTable'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { resolveErrorMessage } from '@/api/client'
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

  // 筛选与分页同步到 URL：刷新 / 分享链接 / 前进后退均可还原（不再需要 persistKey）
  const list = usePagedList<CheckTask, CheckTaskQuery>({
    queryKey: ['outbound', 'checks'],
    fetch: (q) => outboundTaskApi.checks.list(q),
    urlSync: true,
  })

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
        />
        <SfTable<CheckTask>
          storageKey="outbound-checks"
          rowKey="id"
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

      {/* 复核确认弹窗（PENDING→DONE/EXCEPTION，不要求先领取）：
          通过时序列号任务的扫描值必须与任务序列号一致（service_outbound.go:630-631）；
          异常路径 result 必须为五类之一，登记异常中心 */}
      <Modal
        open={!!checkTarget}
        title={`复核确认 · ${checkTarget?.check_no ?? ''}`}
        okText="提交复核"
        confirmLoading={confirmMutation.isPending}
        onCancel={() => setCheckTarget(null)}
        onOk={() => {
          form
            .validateFields()
            .then((v) => {
              const pass = v.pass === 'pass'
              const serial = typeof v.serial === 'string' && v.serial.trim() ? v.serial.trim() : undefined
              confirmMutation.mutate({
                id: checkTarget!.id,
                payload: pass ? { pass: true, serial } : { pass: false, result: v.result, serial },
              })
            })
            .catch(() => {
              // 表单校验失败：Form.Item 已内联提示
            })
        }}
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
