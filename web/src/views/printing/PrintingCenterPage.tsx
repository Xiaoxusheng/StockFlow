import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Radio,
  Select,
  Switch,
  Tabs,
  Tooltip,
  Typography,
  message,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import {
  BARCODE_SYMBOLOGY_OPTIONS,
  isLabelObjectType,
  isPrintTaskFinished,
  PRINT_OBJECT_TYPE_OPTIONS,
  PRINT_OPTIONS_PAGE_SIZE,
  PRINT_PAPER_OPTIONS,
  PRINT_RESULT_META,
  PRINT_TASK_STATUS_META,
  PRINT_TEMPLATE_FIELD_PRESETS,
  printingApi,
  resolveObjectTypeLabel,
  resolvePaperLabel,
  type BarcodeSymbology,
  type PrintExecuteResult,
  type PrintHistoryItem,
  type PrintHistoryQuery,
  type PrintObjectType,
  type PrintTaskCreatePayload,
  type PrintTaskExecutePayload,
  type PrintTaskItem,
  type PrintTaskQuery,
  type PrintTaskStatus,
  type PrintTemplateItem,
  type PrintTemplateQuery,
  type PrintTemplateSavePayload,
  type PrintTemplateStatus,
} from '@/api/printing'
import { DateCell } from '@/components/table/cells'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { useTableRowFeedback } from '@/hooks/useTableRowFeedback'
import { SfConfirm } from '@/components/common/SfConfirm'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { useAuthStore } from '@/stores/auth'
import { canAccess } from '@/types/permission'
import { formatNumber } from '@/utils/format'

const { Text } = Typography

/** 任务状态标签：打印状态不在 types/status.ts 注册表，显式指定文案/语义（frontend.md §24） */
function renderPrintTaskStatus(status?: string): ReactNode {
  const meta = status ? PRINT_TASK_STATUS_META[status as PrintTaskStatus] : undefined
  if (meta) return <SfStatusTag label={meta.label} semantic={meta.semantic} />
  return <SfStatusTag status={status?.toLowerCase()} />
}

/** 打印历史结果标签（printing.md §1.2：打印结果成功/失败） */
function renderPrintResult(result?: string, errorMessage?: string): ReactNode {
  const meta = result ? PRINT_RESULT_META[result as keyof typeof PRINT_RESULT_META] : undefined
  const tag = meta ? (
    <SfStatusTag label={meta.label} semantic={meta.semantic} />
  ) : (
    <SfStatusTag status={result?.toLowerCase()} />
  )
  return errorMessage ? <Tooltip title={errorMessage}>{tag}</Tooltip> : tag
}

function renderDateTime(value?: string): ReactNode {
  return <DateCell value={value} />
}

function renderCount(value?: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

const TEMPLATE_STATUS_OPTIONS = [
  { label: '已启用', value: 'ENABLED' },
  { label: '已停用', value: 'DISABLED' },
]

const TASK_STATUS_OPTIONS: Array<{ label: string; value: string }> = (
  Object.keys(PRINT_TASK_STATUS_META) as PrintTaskStatus[]
).map((value) => ({ label: PRINT_TASK_STATUS_META[value].label, value }))

const RESULT_OPTIONS: Array<{ label: string; value: string }> = (
  Object.keys(PRINT_RESULT_META) as Array<keyof typeof PRINT_RESULT_META>
).map((value) => ({ label: PRINT_RESULT_META[value].label, value }))

// ================= 打印模板（printing.md §2：可新增、编辑、复制、停用） =================

interface TemplateFormValues {
  name: string
  objectType: PrintObjectType
  paper: string
  barcodeSymbology?: BarcodeSymbology
  qrcodeEnabled?: boolean
  /** 已绑定字段键集合（提交时仅传键列表，文案由后端预设注册表回填） */
  fieldKeys?: string[]
  headerText?: string
  remark?: string
}

/** 表单值 → TemplateSaveInput（snake_case；fields 仅键数组，后端按预设注册表回填文案——
 * internal/printing/service_template.go:18-28，禁止前端自造文案）。
 * qrcode_enabled 两类模板均提交：标签类=附加二维码、单据类=单据二维码（printing.md §4.2，
 * 后端对任意快照按 qrcode_enabled 预生成二维码——service_task.go:397-402） */
function toTemplatePayload(values: TemplateFormValues): PrintTemplateSavePayload {
  const labelType = isLabelObjectType(values.objectType)
  return {
    name: values.name.trim(),
    object_type: values.objectType,
    paper: values.paper,
    barcode_symbology: labelType ? (values.barcodeSymbology ?? 'CODE128') : undefined,
    qrcode_enabled: values.qrcodeEnabled ?? false,
    fields: values.fieldKeys ?? [],
    header_text: values.headerText?.trim() || undefined,
    remark: values.remark,
  }
}

function TemplateTab() {
  const [params, setParams] = useState<PrintTemplateQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<PrintTemplateItem | null>(null)
  const [form] = Form.useForm<TemplateFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  // 动效 #7（frontend.md §31）：启停为行级状态变更，成功/失败行淡色反馈（先 API 后反馈）
  const fb = useTableRowFeedback()

  const list = usePagedList<PrintTemplateItem, PrintTemplateQuery>({
    queryKey: ['printing', 'templates'],
    fetch: (query) => printingApi.templates.list(query),
    params,
  })

  const objectType = Form.useWatch('objectType', form)
  const labelType = isLabelObjectType(objectType)
  const fieldOptions = objectType ? (PRINT_TEMPLATE_FIELD_PRESETS[objectType as PrintObjectType] ?? []) : []

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['printing', 'templates'] })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: PrintTemplateSavePayload) =>
      editing ? printingApi.templates.update(editing.id, payload) : printingApi.templates.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      messageApi.success(editing ? '模板已更新' : '模板已创建')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const copyMutation = useMutation({
    mutationFn: (id: PrintTemplateItem['id']) => printingApi.templates.copy(id),
    onSuccess: () => {
      messageApi.success('模板已复制，副本可在列表中查看')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: PrintTemplateItem['id']; status: PrintTemplateStatus }) =>
      printingApi.templates.setStatus(id, { status }),
    onSuccess: (_result, { id }) => {
      fb.trigger(id, 'success')
      messageApi.success('模板状态已更新')
      invalidate()
    },
    onError: (error, { id }) => {
      fb.trigger(id, 'error')
      messageApi.error(resolveErrorMessage(error))
    },
  })

  const openCreate = () => {
    saveMutation.reset()
    setEditing(null)
    setModalOpen(true)
    form.resetFields()
  }

  const openEdit = (record: PrintTemplateItem) => {
    saveMutation.reset()
    setEditing(record)
    setModalOpen(true)
    form.setFieldsValue({
      name: record.name,
      objectType: record.object_type,
      paper: record.paper,
      barcodeSymbology: record.barcode_symbology,
      qrcodeEnabled: record.qrcode_enabled,
      fieldKeys: record.fields?.map((field) => field.key),
      headerText: record.header_text,
      remark: record.remark,
    })
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => saveMutation.mutate(toTemplatePayload(values)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const columns: ColumnsType<PrintTemplateItem> = [
    {
      title: '模板名称',
      dataIndex: 'name',
      width: 200,
      fixed: 'left',
      render: (value: string) => <Text style={{ maxWidth: 200 }} ellipsis={{ tooltip: value }}>{value}</Text>,
    },
    {
      title: '业务类型',
      dataIndex: 'object_type',
      width: 110,
      render: (value: string) => resolveObjectTypeLabel(value),
    },
    {
      title: '纸张',
      dataIndex: 'paper',
      width: 190,
      render: (value: string) => resolvePaperLabel(value),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (value: string) => <SfStatusTag status={value?.toLowerCase()} />,
    },
    { title: '修改时间', dataIndex: 'updated_at', width: 170, render: renderDateTime },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 170,
      render: (_: unknown, record: PrintTemplateItem) => {
        const disabling = record.status === 'ENABLED'
        return (
          <span style={{ whiteSpace: 'nowrap' }}>
            <Button type="link" size="small" onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Button
              type="link"
              size="small"
              loading={copyMutation.isPending}
              onClick={() => copyMutation.mutate(record.id)}
            >
              复制
            </Button>
            <SfConfirm
              title={disabling ? '确认停用该模板？' : '确认启用该模板？'}
              description={
                disabling ? '停用后新建打印任务不可再选择该模板。' : '启用后该模板可重新被打印任务选用。'
              }
              okText={disabling ? '停用' : '启用'}
              confirming={statusMutation.isPending}
              onConfirm={() =>
                statusMutation.mutate({ id: record.id, status: disabling ? 'DISABLED' : 'ENABLED' })
              }
            >
              <Button type="link" size="small" danger={disabling}>
                {disabling ? '停用' : '启用'}
              </Button>
            </SfConfirm>
          </span>
        )
      },
    },
  ]

  return (
    <>
      {contextHolder}
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '模板名称' },
            // 业务类型筛选键走 snake_case 线格式（后端 handler.go:169 c.Query("object_type")）
            { name: 'object_type', label: '业务类型', control: 'select', options: PRINT_OBJECT_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: TEMPLATE_STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as PrintTemplateQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<PrintTemplateItem>
          storageKey="printing-templates"
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
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          actions={
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              新建模板
            </Button>
          }
          emptyText="暂无打印模板，点击「新建模板」创建（printing.md §2：禁止把打印 HTML 写死）"
          emptyAction={
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              新建模板
            </Button>
          }
          scrollX={1000}
        />
      </Card>

      <Modal
        title={editing ? '编辑模板' : '新建模板'}
        open={modalOpen}
        width={640}
        forceRender
        confirmLoading={saveMutation.isPending}
        okText={editing ? '保存' : '创建'}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        {saveMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(saveMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form<TemplateFormValues> form={form} layout="vertical">
          <Form.Item
            name="name"
            label="模板名称"
            rules={[{ required: true, message: '请输入模板名称' }]}
          >
            <Input placeholder="如：SKU 标签-热敏 60×40" maxLength={64} />
          </Form.Item>
          <Form.Item
            name="objectType"
            label="业务类型"
            rules={[{ required: true, message: '请选择业务类型' }]}
            extra="SKU标签 / 库位标签 / 箱码 / 托盘 / 入库单 / 出库单 / 拣货单 / 盘点单 / 发货单"
          >
            <Select
              options={PRINT_OBJECT_TYPE_OPTIONS}
              placeholder="请选择业务类型"
              onChange={() => form.setFieldValue('fieldKeys', [])}
            />
          </Form.Item>
          <Form.Item
            name="paper"
            label="页面尺寸"
            rules={[{ required: true, message: '请选择纸张规格' }]}
            extra="A4 / A5 / 热敏纸规格（printing.md §6）"
          >
            <Select options={PRINT_PAPER_OPTIONS} placeholder="请选择纸张规格" />
          </Form.Item>
          {labelType && (
            <>
              <Form.Item
                name="barcodeSymbology"
                label="条码码制"
                initialValue="CODE128"
                extra="Code128 默认推荐（printing.md §4.1）"
              >
                <Select options={BARCODE_SYMBOLOGY_OPTIONS} allowClear />
              </Form.Item>
              <Form.Item name="qrcodeEnabled" label="附加二维码" valuePropName="checked">
                <Switch />
              </Form.Item>
            </>
          )}
          {!labelType && (
            <>
              <Form.Item name="qrcodeEnabled" label="单据二维码" valuePropName="checked" initialValue={false}>
                <Switch />
              </Form.Item>
              <Form.Item
                name="headerText"
                label="页眉文本"
                extra="单据页眉（公司名/单据名），留空时以模板名称渲染"
              >
                <Input placeholder="如：库流智能仓储管理系统·出库单" maxLength={64} />
              </Form.Item>
            </>
          )}
          <Form.Item
            name="fieldKeys"
            label="字段绑定"
            extra="选择模板要打印的数据字段（printing.md §2 可配置字段绑定）；未绑定字段不渲染"
          >
            <Select mode="multiple" options={fieldOptions} placeholder={objectType ? '请选择字段' : '请先选择业务类型'} />
          </Form.Item>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} placeholder="请输入备注" maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

// ================= 打印任务（printing.md §1.2：创建任务 → 预览 → 执行 → 记录日志） =================

interface TaskFormValues {
  templateId?: string
  dataIdsText?: string
  copies?: number
}

/** 执行确认表单值（printing.md §1.2 执行打印 → 记录打印日志；result 仅可回填一次） */
interface TaskConfirmFormValues {
  result: PrintExecuteResult
  message?: string
}

/** 任务在途轮询间隔（与数据中心任务一致，excel.md §3） */
const POLL_INTERVAL_MS = 5000

function TaskTab() {
  const [params, setParams] = useState<PrintTaskQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<TaskFormValues>()
  const [confirmTask, setConfirmTask] = useState<PrintTaskItem | null>(null)
  const [confirmForm] = Form.useForm<TaskConfirmFormValues>()
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  // 动效 #7（frontend.md §31）：执行确认为行级操作，成功/失败行淡色反馈（先 API 后反馈）
  const fb = useTableRowFeedback()

  const list = usePagedList<PrintTaskItem, PrintTaskQuery>({
    queryKey: ['printing', 'tasks'],
    fetch: (query) => printingApi.tasks.list(query),
    params,
  })

  // 新建任务模板下拉：一次取全，仅启用中模板可选
  const templates = useQuery({
    queryKey: ['printing', 'templates', 'options'],
    queryFn: () => printingApi.templates.list({ page: 1, pageSize: PRINT_OPTIONS_PAGE_SIZE }),
  })
  const templateItems = templates.data?.items ?? []
  const templateOptions = templateItems
    .filter((item) => item.status === 'ENABLED')
    .map((item) => ({
      label: `${item.name}（${resolveObjectTypeLabel(item.object_type)}·${resolvePaperLabel(item.paper)}）`,
      value: String(item.id),
    }))
  const selectedTemplateId = Form.useWatch('templateId', form)
  const selectedTemplate = templateItems.find((item) => String(item.id) === selectedTemplateId)

  const hasInFlight = list.items.some((item) => !isPrintTaskFinished(item.status))
  useQuery({
    queryKey: ['printing', 'tasks', 'progress-poll'],
    queryFn: async () => {
      await list.refetch()
      return null
    },
    refetchInterval: hasInFlight ? POLL_INTERVAL_MS : false,
    enabled: hasInFlight,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['printing', 'tasks'] })
  }

  const createMutation = useMutation({
    mutationFn: (payload: PrintTaskCreatePayload) => printingApi.tasks.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      form.resetFields()
      messageApi.success('打印任务已创建，内容行装配完成后可在「预览」中查看渲染内容')
      invalidate()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  // 执行确认（POST /api/prints/tasks/{id}/execute，{result, message?}，service_task.go:258-296
  // 回填一次守卫：result IS NULL → 命中，重复确认 409）。printing.md §1.2「执行打印 → 记录
  // 打印日志」的落库入口：打印历史 = result 已回填子集（repository.go:208-209）。
  const executeMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: PrintTaskExecutePayload }) =>
      printingApi.tasks.execute(id, payload),
    onSuccess: (task) => {
      // 回填结果如实着色：确认打印失败 → 行 error 淡色底，成功 → success
      fb.trigger(task.id, task.result === 'FAILED' ? 'error' : 'success')
      setConfirmTask(null)
      confirmForm.resetFields()
      messageApi.success(
        task.result === 'FAILED'
          ? `任务 ${task.print_no} 已确认打印失败，已如实记录`
          : `任务 ${task.print_no} 已确认打印成功，记录已写入打印历史`,
      )
      invalidate()
      void queryClient.invalidateQueries({ queryKey: ['printing', 'history'] })
    },
    onError: (error, { id }) => {
      fb.trigger(id, 'error')
      messageApi.error(resolveErrorMessage(error))
    },
  })

  const openConfirm = (task: PrintTaskItem) => {
    confirmForm.resetFields()
    setConfirmTask(task)
  }

  const handleConfirmSubmit = () => {
    if (!confirmTask) return
    confirmForm
      .validateFields()
      .then((values) => {
        executeMutation.mutate({
          id: String(confirmTask.id),
          payload: { result: values.result, message: values.message?.trim() || undefined },
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => {
        const dataIds = (values.dataIdsText ?? '')
          .split(/[\n,，;；]+/)
          .map((item) => item.trim())
          .filter(Boolean)
        createMutation.mutate({
          template_id: Number(values.templateId),
          data_ids: dataIds,
          copies: values.copies ?? 1,
        })
      })
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const columns: ColumnsType<PrintTaskItem> = [
    { title: '任务单号', dataIndex: 'print_no', width: 170, ellipsis: true, fixed: 'left' },
    {
      title: '业务类型',
      dataIndex: 'object_type',
      width: 110,
      render: (value: string) => resolveObjectTypeLabel(value),
    },
    {
      title: '模板',
      dataIndex: 'template_name',
      width: 180,
      ellipsis: true,
      render: (value?: string) => value ?? '-',
    },
    { title: '纸张', dataIndex: 'paper', width: 190, render: (value: string) => resolvePaperLabel(value) },
    { title: '数据量', dataIndex: 'total_count', width: 90, align: 'right', render: renderCount },
    { title: '份数', dataIndex: 'copies', width: 80, align: 'right', render: renderCount },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (value: string, record: PrintTaskItem) =>
        record.error_message ? (
          <Tooltip title={record.error_message}>{renderPrintTaskStatus(value)}</Tooltip>
        ) : (
          renderPrintTaskStatus(value)
        ),
    },
    { title: '创建人', dataIndex: 'created_by', width: 110, render: (value?: string) => value ?? '-' },
    { title: '创建时间', dataIndex: 'created_at', width: 170, render: renderDateTime },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 200,
      render: (_: unknown, record: PrintTaskItem) => (
        <span style={{ whiteSpace: 'nowrap' }}>
          <Button
            type="link"
            size="small"
            onClick={() => navigate(`/data/printing/preview?taskId=${record.id}`)}
          >
            预览
          </Button>
          {/* 执行确认：printing.md §1.2「执行打印 → 记录打印日志」，result 仅可回填一次
             （service_task.go:258-296 重复确认 409），已确认任务置灰展示结果 */}
          {record.result === undefined ? (
            <Button type="link" size="small" onClick={() => openConfirm(record)}>
              执行确认
            </Button>
          ) : (
            <Tooltip title={`打印结果已确认：${PRINT_RESULT_META[record.result]?.label ?? record.result}`}>
              <Button type="link" size="small" disabled>
                执行确认
              </Button>
            </Tooltip>
          )}
          {/* 后端打印域无任务 PDF 下载端点（internal/printing/handler.go 路由收尾于 GET /barcode）：
              如实置灰并提示，不调用不存在的端点、不前端伪造 PDF（printing.md §5） */}
          <Tooltip title="后端暂未提供任务 PDF 下载端点；请在预览页通过「打印」经浏览器打印层输出">
            <Button type="link" size="small" disabled>
              下载 PDF
            </Button>
          </Tooltip>
        </span>
      ),
    },
  ]

  return (
    <>
      {contextHolder}
      <Card size="small">
        <SfSearchForm
          fields={[
            // keyword 按任务单号模糊检索（后端 handler.go:320 print_no ILIKE）
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '任务单号' },
            // 业务类型筛选键走 snake_case 线格式（后端 handler.go:321 c.Query("object_type")）
            { name: 'object_type', label: '业务类型', control: 'select', options: PRINT_OBJECT_TYPE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: TASK_STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as PrintTaskQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<PrintTaskItem>
          storageKey="printing-tasks"
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
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          actions={
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
              新建打印任务
            </Button>
          }
          emptyText="暂无打印任务，点击「新建打印任务」创建（printing.md §1.2 打印统一走任务模型）"
          emptyAction={
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
              新建打印任务
            </Button>
          }
          scrollX={1350}
        />
      </Card>

      <Modal
        title="新建打印任务"
        open={modalOpen}
        width={560}
        forceRender
        confirmLoading={createMutation.isPending}
        okText="创建任务"
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        {createMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(createMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Form<TaskFormValues> form={form} layout="vertical" initialValues={{ copies: 1 }}>
          <Form.Item
            name="templateId"
            label="打印模板"
            rules={[{ required: true, message: '请选择打印模板' }]}
            extra={templates.error ? '模板列表加载失败：' + resolveErrorMessage(templates.error) : undefined}
          >
            <Select options={templateOptions} placeholder="请选择启用中的模板" loading={templates.isFetching} />
          </Form.Item>
          {selectedTemplate && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message={`${resolveObjectTypeLabel(selectedTemplate.object_type)} · ${resolvePaperLabel(selectedTemplate.paper)}`}
              description="打印前请先预览确认（printing.md §3：批量打印前必须显示影响数量并支持预览确认）"
            />
          )}
          <Form.Item
            name="dataIdsText"
            label="打印对象 ID"
            rules={[{ required: true, message: '请输入至少一条业务对象 ID' }]}
            extra="每行一个，或用逗号分隔；每条对象生成一行打印内容，后端按模板绑定字段装配真实数据"
          >
            <Input.TextArea rows={4} placeholder="如：1001&#10;1002&#10;1003" />
          </Form.Item>
          <Form.Item
            name="copies"
            label="打印份数"
            rules={[{ required: true, message: '请输入打印份数' }]}
            extra="数据量以任务内容行数为准（printing.md §1.2）"
          >
            <InputNumber min={1} max={100} precision={0} style={{ width: 160 }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`执行确认 · ${confirmTask?.print_no ?? ''}`}
        open={confirmTask !== null}
        width={480}
        forceRender
        confirmLoading={executeMutation.isPending}
        okText="确认回填"
        onOk={handleConfirmSubmit}
        onCancel={() => setConfirmTask(null)}
      >
        {executeMutation.isError && (
          <Alert
            type="error"
            showIcon
            message={resolveErrorMessage(executeMutation.error)}
            style={{ marginBottom: 16 }}
          />
        )}
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="打印结果仅可回填一次，提交后不可修改"
          description="确认后回填打印人 / 打印时间 / 打印结果并写入打印历史（printing.md §1.2 执行打印 → 记录打印日志）"
        />
        <Form<TaskConfirmFormValues> form={confirmForm} layout="vertical" initialValues={{ result: 'SUCCESS' }}>
          <Form.Item name="result" label="打印结果" rules={[{ required: true, message: '请选择打印结果' }]}>
            <Radio.Group>
              <Radio value="SUCCESS">打印成功</Radio>
              <Radio value="FAILED">打印失败</Radio>
            </Radio.Group>
          </Form.Item>
          <Form.Item name="message" label="备注（可选）">
            <Input.TextArea rows={3} maxLength={255} placeholder="如：批量打印完成 / 条码生成失败等" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

// ================= 打印历史（printing.md §1.2：打印人/打印时间/模板/数据量/打印结果） =================

function HistoryTab() {
  const [params, setParams] = useState<PrintHistoryQuery>({})
  /** 正在重打的历史行 ID（重打为 detail→create 两段请求，行级 loading 防重复触发） */
  const [reprintingId, setReprintingId] = useState<string | null>(null)
  const [messageApi, contextHolder] = message.useMessage()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const user = useAuthStore((s) => s.user)
  // 动效 #7（frontend.md §31）：重打为行级操作，失败行 error 淡色反馈（先 API 后反馈；
  // 成功即跳转预览页，无需行反馈）
  const fb = useTableRowFeedback()
  // 重打入口 fail-closed（约束 7）：无 printing:task:create 权限不显示按钮（frontend.md §13.2）
  const canCreatePrintTask = canAccess(user, 'printing:task:create')

  const list = usePagedList<PrintHistoryItem, PrintHistoryQuery>({
    queryKey: ['printing', 'history'],
    fetch: (query) => printingApi.history.list(query),
    params,
  })

  // 历史筛选模板下拉：一次取全且不限状态——历史行可能引用其后被停用的模板，
  // 仅列启用项会使这部分历史无法按模板筛选（约束 11：历史复用现有体系只加筛选）。
  // 拉取失败时下拉为空如实呈现，不阻塞其余筛选
  const templates = useQuery({
    queryKey: ['printing', 'templates', 'options', 'history'],
    queryFn: () => printingApi.templates.list({ page: 1, pageSize: PRINT_OPTIONS_PAGE_SIZE }),
  })
  const templateOptions = (templates.data?.items ?? []).map((item) => ({
    label: `${item.name}（${resolveObjectTypeLabel(item.object_type)}）`,
    value: String(item.id),
  }))

  // 重打 = 以行快照 data_id 创建新任务，不修改历史（约束 11 / qr-code.md §7.4）。
  // fail-closed（frontend.md §13.2）：任务任一行缺 data_id（含 0 行）即中止，不发创建请求；
  // 全部行有 data_id 方可 tasks.create（all-or-nothing，禁止部分行静默重打）。
  // data_ids 通道纪律（qr-code.md §8）：恒为行快照 data_id = SKU 数字 ID 十进制文本
  const reprintMutation = useMutation({
    mutationFn: async (history: PrintHistoryItem) => {
      const task = await printingApi.tasks.detail(String(history.id))
      const rows = task.rows ?? []
      if (rows.length === 0 || rows.some((row) => !row.data_id)) {
        return { outcome: 'no-data-id' as const }
      }
      if (!history.template_id) {
        return { outcome: 'no-template' as const }
      }
      const created = await printingApi.tasks.create({
        template_id: Number(history.template_id),
        data_ids: rows.map((row) => row.data_id as string),
        copies: 1,
      })
      return { outcome: 'created' as const, task: created }
    },
    onSuccess: (result) => {
      if (result.outcome === 'created') {
        messageApi.success(`重打任务 ${result.task.print_no} 已创建，即将进入预览`)
        void queryClient.invalidateQueries({ queryKey: ['printing', 'tasks'] })
        void queryClient.invalidateQueries({ queryKey: ['printing', 'history'] })
        navigate(`/data/printing/preview?taskId=${result.task.id}`)
        return
      }
      // 提示文案逐字对齐 frontend.md §13.2 冻结口径
      messageApi.warning(
        result.outcome === 'no-data-id'
          ? '该任务创建于身份快照能力之前，无法自动重打，请到商品二维码中心按 SKU 重选打印'
          : '该历史记录缺少模板信息，无法自动重打，请到商品二维码中心按 SKU 重选打印',
      )
    },
    onError: (error, history) => {
      fb.trigger(history.id, 'error')
      messageApi.error(resolveErrorMessage(error))
    },
  })

  const columns: ColumnsType<PrintHistoryItem> = [
    { title: '打印人', dataIndex: 'printed_by', width: 120, render: (value?: string) => value ?? '-' },
    { title: '打印时间', dataIndex: 'printed_at', width: 170, render: renderDateTime },
    {
      title: '模板',
      dataIndex: 'template_name',
      width: 200,
      ellipsis: true,
      render: (value?: string) => value ?? '-',
    },
    {
      title: '业务类型',
      dataIndex: 'object_type',
      width: 110,
      render: (value: string) => resolveObjectTypeLabel(value),
    },
    { title: '数据量', dataIndex: 'total_count', width: 100, align: 'right', render: renderCount },
    {
      title: '结果',
      dataIndex: 'result',
      width: 100,
      render: (value: string, record: PrintHistoryItem) => renderPrintResult(value, record.error_message),
    },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 90,
      render: (_: unknown, record: PrintHistoryItem) => {
        // 仅 SKU 标签行可重打（qr-code.md §7.4 重打口径；其余标签/单据走既有任务模型重建）
        if (record.object_type !== 'SKU_LABEL' || !canCreatePrintTask) return '-'
        return (
          <SfConfirm
            title="确认重打该任务？"
            description="重打 = 以行快照身份创建新打印任务，不修改本条历史记录。"
            okText="重打"
            confirming={reprintingId === String(record.id) && reprintMutation.isPending}
            onConfirm={() => {
              setReprintingId(String(record.id))
              reprintMutation.mutate(record, { onSettled: () => setReprintingId(null) })
            }}
          >
            <Button type="link" size="small" loading={reprintingId === String(record.id) && reprintMutation.isPending}>
              重打
            </Button>
          </SfConfirm>
        )
      },
    },
  ]

  return (
    <>
      {contextHolder}
      <Card size="small">
        <SfSearchForm
          fields={[
            // 后端 keyword 仅按 print_no ILIKE 检索（internal/printing/repository.go:197）——
            // 占位如实标注「打印单号」，不夸大为打印人/模板名称检索
            { name: 'keyword', label: '关键词', control: 'input', placeholder: '打印单号' },
            // 业务类型筛选键走 snake_case 线格式（后端 handler.go:427 c.Query("object_type")）；
            // 「SKU 标签」即其中 SKU_LABEL 选项
            { name: 'object_type', label: '业务类型', control: 'select', options: PRINT_OBJECT_TYPE_OPTIONS },
            // 模板筛选（handler.go:432-438 c.Query("template_id")，正整数字符串）
            { name: 'template_id', label: '打印模板', control: 'select', options: templateOptions },
            { name: 'result', label: '打印结果', control: 'select', options: RESULT_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as PrintHistoryQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<PrintHistoryItem>
          storageKey="printing-history"
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
          feedbackRowKey={fb.rowKey}
          feedbackTone={fb.tone}
          emptyText="暂无打印历史；打印记录由后端在任务执行确认时写入（printing.md §6 打印记录入操作日志）"
          scrollX={980}
        />
      </Card>
    </>
  )
}

/**
 * 打印中心（/data/printing，frontend.md §13：打印模板 / 打印任务 / 打印历史 / 打印预览）。
 * 模板 CRUD/复制/启停、任务创建、历史三页签全部接 /api/prints 契约端点（snake_case 视图，
 * internal/printing/service.go:113-249）；任务 PDF 下载端点后端未交付，下载按钮如实置灰提示。
 * 历史支持模板筛选与 SKU 标签重打（frontend.md §13.2：重打=创建新任务不碰历史，
 * 行快照 data_id 任一缺失即 fail-closed 中止，all-or-nothing）。
 * 预览在独立页面 /data/printing/preview（printing.md §5 禁止点击直接打印当前网页）。
 */
export default function PrintingCenterPage() {
  const [activeKey, setActiveKey] = useState('templates')

  return (
    <div className="sf-page">
      <SfPageHeader
        title="打印中心"
        subtitle="打印模板 / 打印任务 / 打印历史（printing.md §1–§2，frontend.md §13）"
      />
      <Tabs
        activeKey={activeKey}
        onChange={setActiveKey}
        items={[
          { key: 'templates', label: '打印模板', children: <TemplateTab /> },
          { key: 'tasks', label: '打印任务', children: <TaskTab /> },
          { key: 'history', label: '打印历史', children: <HistoryTab /> },
        ]}
      />
    </div>
  )
}
