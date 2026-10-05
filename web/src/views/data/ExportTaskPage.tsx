import { useMemo, useState } from 'react'
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
import { useSearchParams } from 'react-router'
import {
  DATA_TASK_STATUS_META,
  dataApi,
  downloadExportFile,
  EXPORT_MODULE_OPTIONS,
  EXPORT_SCOPE_OPTIONS,
  isTaskInFlight,
  MAX_EXPORT_CURRENT_PAGE_ROWS,
  MAX_EXPORT_SELECTED_IDS,
  resolveModuleLabel,
  resolveScopeLabel,
  type DataTask,
  type DataTaskQuery,
  type DataTaskStatus,
  type ExportCreatePayload,
  type ExportScope,
} from '@/api/data'
import { resolveErrorMessage } from '@/api/client'
import { OPTIONS_FETCH_PAGE_SIZE } from '@/api/options'
import { shelfApi, zoneApi, type ShelfItem, type ZoneItem } from '@/api/warehouse'
import type { PageResult } from '@/types/api'
import { usePagedList } from '@/hooks/usePagedList'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { SfError } from '@/components/common/SfError'
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

/** 新建导出任务表单值（范围 excel.md §2.2；zone_id/shelf_id 为 INVENTORY 模块 BY_FILTER 键） */
interface ExportFormValues {
  module: string
  scope: ExportScope
  idsText?: string
  page?: number
  pageSize?: number
  keyword?: string
  /** 库区筛选（仅 INVENTORY 行源白名单键，internal/inventory/datax_export.go:135-139） */
  zone_id?: string
  /** 货架筛选（同上，随已选库区联动收敛候选） */
  shelf_id?: string
  timeRange?: [Dayjs, Dayjs] | null
}

/** 选中记录 ID 文本 → ID 数组（与表单预检共用同一拆分口径） */
function splitIdsText(text?: string): string[] {
  return (text ?? '')
    .split(/[\n,，;；]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

// ---------- BY_FILTER 的 zone/shelf 筛选下拉数据源（INVENTORY 模块白名单键） ----------
//
// 后端仅 INVENTORY（实时库存）行源认领 zone_id/shelf_id（internal/inventory/datax_export.go:135-139，
// 键清单与 StockListPage SfExportButton scopeParams 对齐）；其余模块未知键静默忽略——
// 筛选项只在 module=INVENTORY 时渲染，提交时也仅在该模块并入 filters。

/** options 防御分页上限（同 api/options.ts fetchAllPages MAX_PAGES 口径，100×50=5000 行封顶） */
const FILTER_OPTIONS_MAX_PAGES = 50

/** 分页逐页取全（同构 api/options.ts fetchAllPages——该助手未导出，本地同口径实现） */
async function fetchAllFilterOptions<T>(fetchPage: (page: number) => Promise<PageResult<T>>): Promise<T[]> {
  const first = await fetchPage(1)
  const rows = [...first.items]
  const totalPages = Math.min(Math.ceil(first.total / OPTIONS_FETCH_PAGE_SIZE) || 1, FILTER_OPTIONS_MAX_PAGES)
  for (let page = 2; page <= totalPages; page += 1) {
    const next = await fetchPage(page)
    rows.push(...next.items)
  }
  return rows
}

/** 库区 options（GET /api/zones；warehouseId 为后端绑定形态 handler.go:320——URL 带仓库预置时收敛候选） */
function fetchZoneFilterOptions(warehouseId?: string): Promise<ZoneItem[]> {
  return fetchAllFilterOptions((page) =>
    zoneApi.list({ page, pageSize: OPTIONS_FETCH_PAGE_SIZE, ...(warehouseId ? { warehouseId } : {}) }),
  )
}

/** 货架 options（GET /api/shelves；zoneId 联动已选库区，handler.go:455-459） */
function fetchShelfFilterOptions(warehouseId?: string, zoneId?: string): Promise<ShelfItem[]> {
  return fetchAllFilterOptions((page) =>
    shelfApi.list({
      page,
      pageSize: OPTIONS_FETCH_PAGE_SIZE,
      ...(warehouseId ? { warehouseId } : {}),
      ...(zoneId ? { zoneId } : {}),
    }),
  )
}

/** 表单值 → 导出创建契约（ExportCreateInput，service_export.go:24-32：
 * scope=SELECTED→ids、CURRENT_PAGE→page/page_size、BY_FILTER→filters、TIME_RANGE→time_from/time_to） */
function toExportPayload(values: ExportFormValues, presetFilters: Record<string, string>): ExportCreatePayload {
  const payload: ExportCreatePayload = { module: values.module, scope: values.scope }
  if (values.scope === 'SELECTED') {
    payload.ids = splitIdsText(values.idsText)
  }
  if (values.scope === 'CURRENT_PAGE') {
    payload.page = values.page
    payload.page_size = values.pageSize
  }
  if (values.scope === 'BY_FILTER') {
    // URL 范围参数（SfExportButton 携带的列表筛选，如 warehouse_id）并入 filters 透传；
    // 未知键由后端按各域白名单忽略（contract.go ExportFilter.Filters）
    const filters: Record<string, string> = { ...presetFilters }
    if (values.keyword) filters.keyword = values.keyword
    // zone/shelf 仅 INVENTORY 行源白名单键：显式表单值优先于 URL 预置（同键覆盖），未填不下发
    if (values.module === 'INVENTORY') {
      if (values.zone_id) filters.zone_id = values.zone_id
      if (values.shelf_id) filters.shelf_id = values.shelf_id
    }
    if (Object.keys(filters).length > 0) payload.filters = filters
  }
  if (values.scope === 'TIME_RANGE' && values.timeRange?.[0] && values.timeRange?.[1]) {
    payload.time_from = values.timeRange[0].format('YYYY-MM-DD HH:mm:ss')
    payload.time_to = values.timeRange[1].format('YYYY-MM-DD HH:mm:ss')
  }
  return payload
}

/**
 * Excel 导出任务中心（/data/exports，excel.md §3/§4）：
 * 创建导出任务（范围 excel.md §2.2，契约 ExportCreateInput snake_case）→ 任务列表展示
 * 进度（处理中 35%）→ 终态后认证下载产物（GET /api/exports/{id}/file）。
 * 在途进度轮询走任务详情端点 GET /api/data-tasks/export/{id}（handler.go:123-124，
 * 导出独立权限点），任务全部进入终态后自动停止；未装配行源的模块创建任务后端返回
 * 409 DATAX_MODULE_NOT_AVAILABLE，错误信封如实呈现，不生成假文件。
 */
export default function ExportTaskPage() {
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()
  const [searchParams] = useSearchParams()
  // SfExportButton 跳转携带的 URL 上下文：type=业务模块，其余 query 为范围参数（warehouse_id 等）
  const urlModule = searchParams.get('type')
  const presetFilters = useMemo(
    () => Object.fromEntries([...searchParams.entries()].filter(([key]) => key !== 'type')),
    [searchParams],
  )
  const hasPresetFilters = Object.keys(presetFilters).length > 0
  const [params, setParams] = useState<DataTaskQuery>(() => (urlModule ? { module: urlModule } : {}))
  const [modalOpen, setModalOpen] = useState(false)
  const [downloadingId, setDownloadingId] = useState<string | null>(null)
  /** 在途任务的详情快照（GET /api/data-tasks/export/{id} 轮询结果，仅合并到在途行） */
  const [polledDetails, setPolledDetails] = useState<Record<string, DataTask>>({})
  const [form] = Form.useForm<ExportFormValues>()
  const scope = Form.useWatch('scope', form)
  const moduleValue = Form.useWatch('module', form)
  const zoneIdValue = Form.useWatch('zone_id', form)

  // BY_FILTER 的 zone/shelf 筛选候选（仅 INVENTORY 模块白名单键，fetchZoneFilterOptions 注释）：
  // 库区候选随 URL 仓库预置收敛、货架候选随已选库区联动；弹窗打开且命中模块+范围才拉取，
  // options 加载失败降级为 disabled+可留空提交（错误如实呈现，不造假候选）
  const presetWarehouseId =
    typeof presetFilters.warehouse_id === 'string' && presetFilters.warehouse_id !== ''
      ? presetFilters.warehouse_id
      : undefined
  const zoneOptionsQuery = useQuery({
    queryKey: ['data', 'exports', 'filter-options', 'zones', presetWarehouseId ?? ''],
    queryFn: () => fetchZoneFilterOptions(presetWarehouseId),
    enabled: modalOpen && scope === 'BY_FILTER' && moduleValue === 'INVENTORY',
  })
  const shelfOptionsQuery = useQuery({
    queryKey: ['data', 'exports', 'filter-options', 'shelves', presetWarehouseId ?? '', zoneIdValue ?? ''],
    queryFn: () => fetchShelfFilterOptions(presetWarehouseId, zoneIdValue),
    enabled: modalOpen && scope === 'BY_FILTER' && moduleValue === 'INVENTORY',
  })

  const list = usePagedList<DataTask, DataTaskQuery>({
    queryKey: ['data', 'exports'],
    fetch: (query) => dataApi.exports.list(query),
    params,
  })

  const inFlightItems = useMemo(
    () => list.items.filter((item) => isTaskInFlight(item.status)),
    [list.items],
  )
  const inFlightKey = inFlightItems.map((item) => item.id).join(',')

  // 进度轮询：对每个在途任务轮询详情端点（GET /api/data-tasks/export/{id}），
  // 进度/状态合并展示；任一任务进入终态即刷新列表取回产物地址与最终行数，
  // 全部终态后 enabled=false + refetchInterval=false 自动停止。
  useQuery({
    queryKey: ['data', 'exports', 'in-flight', inFlightKey],
    queryFn: async () => {
      const results = await Promise.allSettled(inFlightItems.map((item) => dataApi.exportTask(item.id)))
      const next: Record<string, DataTask> = {}
      let reachedTerminal = false
      results.forEach((result, index) => {
        const source = inFlightItems[index]
        if (result.status !== 'fulfilled' || !source) return
        const detail = result.value
        next[source.id] = detail
        if (!isTaskInFlight(detail.status)) reachedTerminal = true
      })
      setPolledDetails(next)
      if (reachedTerminal) {
        void queryClient.invalidateQueries({ queryKey: ['data', 'exports'] })
      }
      return next
    },
    refetchInterval: inFlightItems.length > 0 ? POLL_INTERVAL_MS : false,
    enabled: inFlightItems.length > 0,
  })

  /** 展示行：在途行合并详情快照（轮询更新进度），终态行以列表为准 */
  const displayItems = useMemo(
    () =>
      list.items.map((item) => (isTaskInFlight(item.status) ? polledDetails[item.id] ?? item : item)),
    [list.items, polledDetails],
  )

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

  const handleDownload = async (record: DataTask) => {
    setDownloadingId(record.id)
    try {
      // 认证下载：优先任务下发 file_url，缺省回退 GET /api/exports/{id}/file
      await downloadExportFile(record)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloadingId(null)
    }
  }

  /** 打开新建弹窗：URL 携带的模块/范围参数预填进表单（SfExportButton 入口） */
  const openCreateModal = () => {
    if (urlModule || hasPresetFilters) {
      form.setFieldsValue({
        ...(urlModule ? { module: urlModule } : {}),
        ...(hasPresetFilters ? { scope: 'BY_FILTER' as ExportScope } : {}),
      })
    }
    setModalOpen(true)
  }

  const handleSubmit = () => {
    form
      .validateFields()
      .then((values) => createMutation.mutate(toExportPayload(values, presetFilters)))
      .catch(() => {
        // 表单校验失败：Form.Item 已内联提示
      })
  }

  const columns: ColumnsType<DataTask> = [
    { title: '任务单号', dataIndex: 'task_no', width: 160, ellipsis: true },
    {
      title: '业务模块',
      key: 'module',
      width: 120,
      render: (_: unknown, record: DataTask) => record.module_name ?? resolveModuleLabel(record.module),
    },
    { title: '导出范围', dataIndex: 'scope', width: 140, render: (value?: string) => resolveScopeLabel(value) },
    { title: '文件名', dataIndex: 'file_name', width: 180, ellipsis: true, render: (value?: string) => value ?? '-' },
    { title: '数量', dataIndex: 'total_rows', width: 90, align: 'right', render: renderCount },
    { title: '成功', dataIndex: 'success_rows', width: 90, align: 'right', render: renderCount },
    { title: '失败', dataIndex: 'failed_rows', width: 90, align: 'right', render: renderCount },
    {
      title: '进度',
      key: 'progress',
      width: 140,
      render: (_: unknown, record: DataTask) =>
        isTaskInFlight(record.status) && typeof record.progress === 'number' ? (
          <Progress percent={record.progress} size="small" style={{ width: 110 }} />
        ) : (
          '-'
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (value: string, record: DataTask) =>
        record.error_message ? (
          <Tooltip title={record.error_message}>{renderTaskStatus(value)}</Tooltip>
        ) : (
          renderTaskStatus(value)
        ),
    },
    { title: '开始时间', dataIndex: 'started_at', width: 160, render: renderDateTime },
    { title: '结束时间', dataIndex: 'finished_at', width: 160, render: renderDateTime },
    {
      title: '操作',
      key: 'actions',
      fixed: 'right',
      width: 90,
      render: (_: unknown, record: DataTask) =>
        // 导出单值成功无部分成功（QUEUED→PROCESSING→SUCCESS/FAILED）；仅成功任务有产物文件
        record.status === 'SUCCESS' ? (
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
          <Button type="primary" icon={<ExportOutlined />} onClick={openCreateModal}>
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
        <SfTable<DataTask>
          storageKey="data-export-tasks"
          rowKey="id"
          columns={columns}
          dataSource={displayItems}
          loading={list.isFetching}
          error={list.error}
          onRetry={list.refetch}
          onRefresh={list.refetch}
          pagination={list.pagination}
          total={list.total}
          onPageChange={list.onPageChange}
          emptyText="暂无导出任务，点击右上角「新建导出任务」创建"
          emptyAction={
            <Button type="primary" icon={<ExportOutlined />} onClick={openCreateModal}>
              新建导出任务
            </Button>
          }
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
              rules={[
                { required: true, message: '请输入至少一条记录 ID' },
                {
                  // 前端预检：拆分后 ≤1000 条（后端 maxSelectedIDs，service_export.go:36），
                  // 超限提交必 400，提前内联拦截
                  validator: (_, value?: string) =>
                    splitIdsText(value).length > MAX_EXPORT_SELECTED_IDS
                      ? Promise.reject(
                          new Error(`选中记录最多 ${MAX_EXPORT_SELECTED_IDS} 条，请缩小范围（后端限制）`),
                        )
                      : Promise.resolve(),
                },
              ]}
              extra={`每行一个，或用英文逗号分隔；最多 ${MAX_EXPORT_SELECTED_IDS} 条`}
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
                <Form.Item
                  name="pageSize"
                  label="每页条数"
                  rules={[{ required: true, message: '请输入每页条数' }]}
                  extra={`单次当前页导出上限 ${MAX_EXPORT_CURRENT_PAGE_ROWS} 行（后端限制）`}
                >
                  {/* 上限对齐后端 maxCurrentPageRows（service_export.go:39），防超限提交 400 */}
                  <InputNumber min={1} max={MAX_EXPORT_CURRENT_PAGE_ROWS} precision={0} style={{ width: '100%' }} />
                </Form.Item>
              </Col>
            </Row>
          )}
          {scope === 'BY_FILTER' && (
            <>
              {hasPresetFilters && (
                <Alert
                  type="info"
                  showIcon
                  style={{ marginBottom: 16 }}
                  message="已从列表带入范围参数"
                  description={Object.entries(presetFilters)
                    .map(([key, value]) => `${key}=${value}`)
                    .join('，')}
                />
              )}
              {moduleValue === 'INVENTORY' && (
                // zone/shelf 仅 INVENTORY 行源认领（internal/inventory/datax_export.go:135-139）；
                // 表单显式值优先于 URL 预置同键（toExportPayload 合并规则）
                <Row gutter={16}>
                  <Col span={12}>
                    <Form.Item
                      name="zone_id"
                      label="库区筛选"
                      extra={zoneOptionsQuery.isError ? '库区列表加载失败，可留空提交' : '按库区过滤库存行（zone_id）'}
                    >
                      <Select
                        allowClear
                        showSearch
                        optionFilterProp="label"
                        placeholder="全部库区"
                        loading={zoneOptionsQuery.isPending}
                        disabled={zoneOptionsQuery.isError}
                        options={(zoneOptionsQuery.data ?? []).map((zone) => ({
                          value: String(zone.id),
                          label: `${zone.code}（${zone.name}）`,
                        }))}
                        onChange={() => {
                          // 库区变更后原货架候选已失效，联动清空（未选不下发）
                          form.setFieldValue('shelf_id', undefined)
                        }}
                      />
                    </Form.Item>
                  </Col>
                  <Col span={12}>
                    <Form.Item
                      name="shelf_id"
                      label="货架筛选"
                      extra={shelfOptionsQuery.isError ? '货架列表加载失败，可留空提交' : '按货架过滤库存行（shelf_id）'}
                    >
                      <Select
                        allowClear
                        showSearch
                        optionFilterProp="label"
                        placeholder={zoneIdValue ? '全部货架' : '全部货架（选库区可联动收敛候选）'}
                        loading={shelfOptionsQuery.isPending}
                        disabled={shelfOptionsQuery.isError}
                        options={(shelfOptionsQuery.data ?? []).map((shelf) => ({
                          value: String(shelf.id),
                          label: shelf.code,
                        }))}
                      />
                    </Form.Item>
                  </Col>
                </Row>
              )}
              <Form.Item name="keyword" label="筛选关键词">
                <Input placeholder="随业务模块的筛选条件（后端按白名单键认领）" />
              </Form.Item>
            </>
          )}
          {scope === 'TIME_RANGE' && (
            <Form.Item
              name="timeRange"
              label="创建时间范围"
              rules={[{ required: true, message: '请选择导出的创建时间范围' }]}
              extra="按记录创建时间区间导出（time_from / time_to）"
            >
              <DatePicker.RangePicker showTime style={{ width: '100%' }} />
            </Form.Item>
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
            // 未装配行源的模块（PRODUCT/SKU/SUPPLIER/CUSTOMER/REPORT）后端返回
            // 409 DATAX_MODULE_NOT_AVAILABLE——错误信封如实呈现，不造假任务
            <div style={{ marginTop: 16 }}>
              <SfError error={createMutation.error} onRetry={handleSubmit} />
            </div>
          )}
        </Form>
      </Modal>
    </div>
  )
}
