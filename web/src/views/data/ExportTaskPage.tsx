import { useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Col,
  DatePicker,
  Form,
  Input,
  InputNumber,
  Modal,
  Progress,
  Row,
  Select,
  Tooltip,
  message,
} from 'antd'
import { ExportOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import type { Dayjs } from 'dayjs'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import {
  DATA_TASK_STATUS_META,
  dataApi,
  downloadFile,
  EXPORT_MODULE_OPTIONS,
  EXPORT_SCOPE_OPTIONS,
  isTaskFinished,
  isTaskInFlight,
  resolveModuleLabel,
  resolveScopeLabel,
  resolveTaskTypeLabel,
  type DataTaskItem,
  type DataTaskQuery,
  type DataTaskStatus,
  type ExportCreatePayload,
  type ExportScope,
} from '@/api/data'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

/** 轮询间隔：任务在途时每 5 秒刷新进度（excel.md §3「处理中 35%」） */
const POLL_INTERVAL_MS = 5000

/** 任务状态筛选选项（excel.md §4） */
const TASK_STATUS_OPTIONS: Array<{ label: string; value: string }> = (
  Object.keys(DATA_TASK_STATUS_META) as DataTaskStatus[]
).map((value) => ({ label: DATA_TASK_STATUS_META[value].label, value }))

/** 任务状态标签：数据中心状态不在 types/status.ts 注册表中，显式指定文案/语义（frontend.md §24） */
function renderTaskStatus(status?: string): ReactNode {
  const meta = status ? DATA_TASK_STATUS_META[status as DataTaskStatus] : undefined
  if (meta) return <SfStatusTag label={meta.label} semantic={meta.semantic} />
  return <SfStatusTag status={status} />
}

function renderDateTime(value?: string): ReactNode {
  return <span style={{ whiteSpace: 'nowrap' }}>{formatDateTime(value)}</span>
}

function renderCount(value?: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/** 新建导出任务表单值（范围 excel.md §2.2） */
interface ExportFormValues {
  module: string
  scope: ExportScope
  idsText?: string
  page?: number
  pageSize?: number
  keyword?: string
  timeRange?: [Dayjs, Dayjs] | null
}

/** 表单值 → 导出任务契约（字段对齐 excel.md §4 任务记录模型，冻结后回对） */
function toExportPayload(values: ExportFormValues): ExportCreatePayload {
  const payload: ExportCreatePayload = { module: values.module, scope: values.scope }
  if (values.scope === 'SELECTED') {
    payload.ids = (values.idsText ?? '')
      .split(/[\n,，;；]+/)
      .map((item) => item.trim())
      .filter(Boolean)
  }
  if (values.scope === 'CURRENT_PAGE') {
    payload.page = values.page
    payload.pageSize = values.pageSize
  }
  if (values.scope === 'BY_FILTER') {
    if (values.keyword) payload.filters = { keyword: values.keyword }
    if (values.timeRange?.[0] && values.timeRange?.[1]) {
      payload.startTime = values.timeRange[0].format('YYYY-MM-DD HH:mm:ss')
      payload.endTime = values.timeRange[1].format('YYYY-MM-DD HH:mm:ss')
    }
  }
  return payload
}

/**
 * Excel 导出任务中心（/data/exports，excel.md §3/§4）：
 * 创建导出任务（范围 excel.md §2.2）→ 任务列表展示进度（处理中 35%）→ 终态后下载产物。
 * 进度轮询经 TanStack Query refetchInterval 驱动，任务全部进入终态后自动停止；
 * 产物下载走真实链接（downloadFile），后端未交付呈可读错误态，不生成假文件。
 */
export default function ExportTaskPage() {
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [params, setParams] = useState<DataTaskQuery>({})
  const [modalOpen, setModalOpen] = useState(false)
  const [downloadingId, setDownloadingId] = useState<string | null>(null)
  const [form] = Form.useForm<ExportFormValues>()
  const scope = Form.useWatch('scope', form)

  const list = usePagedList<DataTaskItem, DataTaskQuery>({
    queryKey: ['data', 'exports'],
    fetch: (query) => dataApi.exports.list(query),
    params,
  })

  const hasInFlight = list.items.some((item) => isTaskInFlight(item.status))

  // 进度轮询：usePagedList 未暴露 refetchInterval，以独立轮询查询驱动列表 refetch；
  // 仅在存在排队/处理中任务时启用，全部进入终态后 refetchInterval 置 false 自动停止。
  useQuery({
    queryKey: ['data', 'exports', 'progress-poll'],
    queryFn: async () => {
      await list.refetch()
      return null
    },
    refetchInterval: hasInFlight ? POLL_INTERVAL_MS : false,
    enabled: hasInFlight,
  })

  const createMutation = useMutation({
    mutationFn: (payload: ExportCreatePayload) => dataApi.exports.create(payload),
    onSuccess: () => {
      setModalOpen(false)
      form.resetFields()
      messageApi.success('导出任务已创建，处理完成后可在列表中下载产物')
      void queryClient.invalidateQueries({ queryKey: ['data', 'exports'] })
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleDownload = async (record: DataTaskItem) => {
    if (!record.fileUrl) return
    setDownloadingId(record.id)
    try {
      await downloadFile(record.fileUrl, record.fileName ?? `导出产物_${record.id}.xlsx`)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloadingId(null)
    }
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => createMutation.mutate(toExportPayload(values)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const columns: ColumnsType<DataTaskItem> = [
    { title: '任务ID', dataIndex: 'id', width: 150, ellipsis: true },
    { title: '任务类型', dataIndex: 'taskType', width: 90, render: (value: string) => resolveTaskTypeLabel(value) },
    {
      title: '业务模块',
      key: 'module',
      width: 120,
      render: (_: unknown, record: DataTaskItem) => record.moduleName ?? resolveModuleLabel(record.module),
    },
    { title: '导出范围', dataIndex: 'scope', width: 140, render: (value?: string) => resolveScopeLabel(value) },
    { title: '文件名', dataIndex: 'fileName', width: 180, ellipsis: true, render: (value?: string) => value ?? '-' },
    { title: '创建人', dataIndex: 'creator', width: 100, render: (value?: string) => value ?? '-' },
    { title: '数量', dataIndex: 'totalRows', width: 90, align: 'right', render: renderCount },
    { title: '成功', dataIndex: 'successRows', width: 90, align: 'right', render: renderCount },
    { title: '失败', dataIndex: 'failedRows', width: 90, align: 'right', render: renderCount },
    {
      title: '进度',
      key: 'progress',
      width: 140,
      render: (_: unknown, record: DataTaskItem) =>
        record.status === 'PROCESSING' && typeof record.progress === 'number' ? (
          <Progress percent={record.progress} size="small" style={{ width: 110 }} />
        ) : (
          '-'
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (value: string, record: DataTaskItem) =>
        record.errorMessage ? (
          <Tooltip title={record.errorMessage}>{renderTaskStatus(value)}</Tooltip>
        ) : (
          renderTaskStatus(value)
        ),
    },
    { title: '开始时间', dataIndex: 'startedAt', width: 160, render: renderDateTime },
    { title: '结束时间', dataIndex: 'finishedAt', width: 160, render: renderDateTime },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 90,
      render: (_: unknown, record: DataTaskItem) =>
        isTaskFinished(record.status) && record.fileUrl ? (
          <Button
            type="link"
            size="small"
            icon={<ExportOutlined />}
            loading={downloadingId === record.id}
            onClick={() => handleDownload(record)}
          >
            下载
          </Button>
        ) : (
          '-'
        ),
    },
  ]

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader
        title="Excel 导出"
        subtitle="导出任务化执行：创建任务 → 后台处理 → 下载产物（excel.md §3）"
        extra={
          <Button type="primary" icon={<ExportOutlined />} onClick={() => setModalOpen(true)}>
            新建导出任务
          </Button>
        }
      />
      <Card size="small">
        <SfSearchForm
          fields={[
            { name: 'module', label: '业务模块', control: 'select', options: EXPORT_MODULE_OPTIONS },
            { name: 'status', label: '状态', control: 'select', options: TASK_STATUS_OPTIONS },
          ]}
          onSearch={(values) => {
            setParams(values as DataTaskQuery)
            list.resetToFirstPage()
          }}
        />
        <SfTable<DataTaskItem>
          storageKey="data-export-tasks"
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
          emptyText="暂无导出任务，点击右上角「新建导出任务」创建"
          scrollX={1690}
        />
      </Card>

      <Modal
        title="新建导出任务"
        open={modalOpen}
        width={560}
        forceRender
        confirmLoading={createMutation.isPending}
        okText="创建任务"
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
      >
        <Form<ExportFormValues> form={form} layout="vertical" initialValues={{ scope: 'ALL', page: 1, pageSize: 20 }}>
          <Form.Item name="module" label="业务模块" rules={[{ required: true, message: '请选择要导出的业务模块' }]}>
            <Select options={EXPORT_MODULE_OPTIONS} placeholder="请选择业务模块" />
          </Form.Item>
          <Form.Item name="scope" label="导出范围" rules={[{ required: true, message: '请选择导出范围' }]} extra="范围定义见 excel.md §2.2">
            <Select options={EXPORT_SCOPE_OPTIONS} />
          </Form.Item>
          {scope === 'SELECTED' && (
            <Form.Item
              name="idsText"
              label="选中记录 ID"
              rules={[{ required: true, message: '请输入至少一条记录 ID' }]}
              extra="每行一个，或用英文逗号分隔"
            >
              <Input.TextArea rows={3} placeholder="如：1001,1002,1003" />
            </Form.Item>
          )}
          {scope === 'CURRENT_PAGE' && (
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item name="page" label="页码" rules={[{ required: true, message: '请输入页码' }]}>
                  <InputNumber min={1} precision={0} style={{ width: '100%' }} />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item name="pageSize" label="每页条数" rules={[{ required: true, message: '请输入每页条数' }]}>
                  <InputNumber min={1} precision={0} style={{ width: '100%' }} />
                </Form.Item>
              </Col>
            </Row>
          )}
          {scope === 'BY_FILTER' && (
            <>
              <Form.Item name="keyword" label="筛选关键词">
                <Input placeholder="随业务模块的筛选条件（后端契约冻结后回对）" />
              </Form.Item>
              <Form.Item name="timeRange" label="时间范围">
                <DatePicker.RangePicker showTime style={{ width: '100%' }} />
              </Form.Item>
            </>
          )}
          {scope === 'ALL' && (
            <Alert
              type="info"
              showIcon
              message="将导出该模块全部数据"
              description="由后端分批流式生成，任务完成后在列表中下载（excel.md §3：大数据量禁止一次性内存生成）。"
            />
          )}
          {createMutation.isError && (
            <Alert type="error" showIcon message={resolveErrorMessage(createMutation.error)} style={{ marginTop: 16 }} />
          )}
        </Form>
      </Modal>
    </div>
  )
}
