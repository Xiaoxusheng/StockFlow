import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Descriptions,
  Flex,
  Modal,
  Select,
  Space,
  Steps,
  Typography,
  Upload,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { StepsProps, UploadFile } from 'antd'
import { DownloadOutlined, InboxOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import {
  DATA_TASK_STATUS_META,
  dataApi,
  downloadImportErrorFile,
  downloadImportTemplate,
  isHighRiskImport,
  resolveModuleLabel,
  type DataTask,
  type DataTaskQuery,
  type DataTaskStatus,
  type ImportConfirmResult,
  type ImportType,
  type ImportUploadResult,
  type ImportValidationError,
  type ImportValidateResult,
} from '@/api/data'
import { DateCell } from '@/components/table/cells'
import { resolveErrorMessage } from '@/api/client'
import { usePagedList } from '@/hooks/usePagedList'
import { SfDetailSection, SfSummaryBar } from '@/components/common/SfDetailSection'
import { SfEmpty } from '@/components/common/SfEmpty'
import { SfError } from '@/components/common/SfError'
import { SfLoading } from '@/components/common/SfLoading'
import { SfPageHeader } from '@/components/common/SfPageHeader'
import { SfSearchForm } from '@/components/table/SfSearchForm'
import { SfStatusTag } from '@/components/common/SfStatusTag'
import { SfTable } from '@/components/table/SfTable'
import { formatDateTime, formatNumber } from '@/utils/format'

const { Text } = Typography

/** 六步向导，与 frontend.md §12 / excel.md §1.2 一一对应（禁止「选文件→导入」一步式） */
const STEP_ITEMS: NonNullable<StepsProps['items']> = [
  { title: '下载模板' },
  { title: '上传文件' },
  { title: '数据校验' },
  { title: '预览' },
  { title: '确认导入' },
  { title: '导入结果' },
]

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
  return <DateCell value={value} />
}

function renderCount(value?: number): ReactNode {
  return <span className="sf-num">{formatNumber(value)}</span>
}

/** 校验/导入失败明细列：行号精确定位 + 原因（excel.md §1.4「第 N 行：原因」） */
const ERROR_COLUMNS: ColumnsType<ImportValidationError> = [
  {
    title: '行号',
    dataIndex: 'row',
    width: 100,
    align: 'right',
    render: (value: number) => <span className="sf-num">第 {formatNumber(value)} 行</span>,
  },
  { title: '字段', dataIndex: 'column', width: 140, render: (value?: string) => value ?? '-' },
  { title: '错误原因', dataIndex: 'message' },
]

/** 导入任务记录列（excel.md §4 任务记录模型；出参 ListTaskItem snake_case，service_import.go:794-815） */
const IMPORT_TASK_COLUMNS: ColumnsType<DataTask> = [
  { title: '任务单号', dataIndex: 'task_no', width: 170, ellipsis: true },
  {
    title: '导入类型',
    key: 'module',
    width: 130,
    render: (_: unknown, record: DataTask) => record.module_name ?? resolveModuleLabel(record.module),
  },
  { title: '文件名', dataIndex: 'file_name', width: 200, ellipsis: true, render: (value?: string) => value ?? '-' },
  { title: '数量', dataIndex: 'total_rows', width: 90, align: 'right', render: renderCount },
  { title: '成功', dataIndex: 'success_rows', width: 90, align: 'right', render: renderCount },
  { title: '失败', dataIndex: 'failed_rows', width: 90, align: 'right', render: renderCount },
  { title: '状态', dataIndex: 'status', width: 100, render: (value: string) => renderTaskStatus(value) },
  { title: '开始时间', dataIndex: 'started_at', width: 160, render: renderDateTime },
  { title: '结束时间', dataIndex: 'finished_at', width: 160, render: renderDateTime },
]

/** 预览单元格统一转可读文本（仅渲染后端解析结果，不做业务解析） */
function renderPreviewCell(value: unknown): string {
  if (value === null || value === undefined || value === '') return '-'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

/**
 * Excel 导入六步向导（/data/imports，frontend.md §12 + excel.md §1.2）：
 * 下载模板 → 上传文件 → 数据校验 → 预览 → 确认导入 → 导入结果。
 * 每一步均为真实后端请求（api/data.ts 已回对 datax 冻结契约：模板/校验/预览/结果
 * 出参 snake_case，internal/datax/service_import.go），任何步骤失败呈可读错误且可重试；
 * 前端不做本地解析、不伪造校验/导入结果、不生成假文件（requirements.md §10）。
 */
export default function ImportWizardPage() {
  const [messageApi, contextHolder] = message.useMessage()
  const queryClient = useQueryClient()

  const [current, setCurrent] = useState(0)
  const [importType, setImportType] = useState<ImportType>()
  const [file, setFile] = useState<File | null>(null)
  const [uploadResult, setUploadResult] = useState<ImportUploadResult | null>(null)
  const [validateResult, setValidateResult] = useState<ImportValidateResult | null>(null)
  const [confirmResult, setConfirmResult] = useState<ImportConfirmResult | null>(null)
  const [riskModalOpen, setRiskModalOpen] = useState(false)
  const [riskAck, setRiskAck] = useState(false)
  const [downloadingTemplate, setDownloadingTemplate] = useState(false)
  const [downloadingErrorFile, setDownloadingErrorFile] = useState(false)

  // 第 1 步：模板列表由后端统一下发（GET /api/imports/templates）
  const templatesQuery = useQuery({
    queryKey: ['data', 'imports', 'templates'],
    queryFn: dataApi.imports.templates,
  })
  const templates = useMemo(() => templatesQuery.data ?? [], [templatesQuery.data])
  const selectedTemplate = templates.find((template) => template.import_type === importType) ?? null
  // 高危判定优先消费后端清单下发的 high_risk（TemplateItem，service_import.go:43），
  // 模板清单缺位时按 import_type 本地兜底（isHighRiskImport 与 registry.go:152-154 同判）
  const highRisk = selectedTemplate?.high_risk ?? isHighRiskImport(uploadResult?.import_type)

  // 第 4 步：预览为后端解析后的结构化数据（GET /api/imports/{id}/preview），进入该步时加载
  const previewQuery = useQuery({
    queryKey: ['data', 'imports', uploadResult?.id ?? 'none', 'preview'],
    queryFn: () => {
      if (!uploadResult) throw new Error('请先上传并解析文件')
      return dataApi.imports.preview(uploadResult.id)
    },
    enabled: current === 3 && uploadResult !== null,
  })

  // 导入任务记录（excel.md §4 数据中心·导入任务）
  const [taskParams, setTaskParams] = useState<DataTaskQuery>({})
  const tasks = usePagedList<DataTask, DataTaskQuery>({
    queryKey: ['data', 'imports', 'tasks'],
    fetch: (query) => dataApi.imports.list(query),
    params: taskParams,
  })

  const refreshTasks = () => {
    void queryClient.invalidateQueries({ queryKey: ['data', 'imports', 'tasks'] })
  }

  const uploadMutation = useMutation({
    mutationFn: () => {
      if (!importType) throw new Error('请先在第一步选择导入类型')
      if (!file) throw new Error('请先选择要上传的 Excel 文件')
      return dataApi.imports.upload({ importType, file })
    },
    onSuccess: (result) => {
      setUploadResult(result)
      setValidateResult(null)
      setConfirmResult(null)
      refreshTasks()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const validateMutation = useMutation({
    mutationFn: () => {
      if (!uploadResult) throw new Error('请先上传并解析文件')
      return dataApi.imports.validate(uploadResult.id)
    },
    onSuccess: (result) => setValidateResult(result),
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const confirmMutation = useMutation({
    mutationFn: () => {
      if (!uploadResult) throw new Error('导入任务不存在，请重新上传')
      // 高危类型必须 confirmed=true 二次确认（excel.md §6.1，service_import.go ConfirmInput）
      return dataApi.imports.confirm(uploadResult.id, { confirmed: true })
    },
    onSuccess: (result) => {
      setConfirmResult(result)
      setCurrent(5)
      refreshTasks()
    },
    onError: (error) => messageApi.error(resolveErrorMessage(error)),
  })

  const handleSelectType = (value: ImportType) => {
    if (value === importType) return
    // 切换导入类型后，已上传/已校验数据作废，需重新走流程
    setImportType(value)
    setFile(null)
    setUploadResult(null)
    setValidateResult(null)
    setConfirmResult(null)
  }

  const handleBeforeUpload = (uploadFile: File): boolean => {
    // 阻止 antd 自动上传：真实上传由「上传并解析」按钮经 api 层发起（multipart）
    setFile(uploadFile)
    return false
  }

  const handleDownloadTemplate = async () => {
    if (!selectedTemplate) return
    setDownloadingTemplate(true)
    try {
      // GET /api/imports/templates/{type}：后端按列定义用 excelize 现场生成（流式下发），
      // 认证 Blob 下载；前端不生成假文件
      await downloadImportTemplate(selectedTemplate)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloadingTemplate(false)
    }
  }

  const handleDownloadErrorFile = async () => {
    if (!uploadResult || !validateResult) return
    setDownloadingErrorFile(true)
    try {
      // 优先校验结果下发的 error_file_url，缺省回退 GET /api/imports/{id}/error-file
      await downloadImportErrorFile(uploadResult.id, `导入错误明细_${uploadResult.id}.xlsx`, validateResult)
    } catch (error) {
      messageApi.error(resolveErrorMessage(error))
    } finally {
      setDownloadingErrorFile(false)
    }
  }

  const handleConfirmImport = () => {
    if (!uploadResult) return
    // 高危类型（后端 high_risk 判定）必须二次确认（excel.md §6.1）
    if (highRisk) {
      setRiskAck(false)
      setRiskModalOpen(true)
      return
    }
    confirmMutation.mutate()
  }

  const resetWizard = () => {
    setCurrent(0)
    setImportType(undefined)
    setFile(null)
    setUploadResult(null)
    setValidateResult(null)
    setConfirmResult(null)
    setRiskAck(false)
    setRiskModalOpen(false)
  }

  const uploadFileList: UploadFile[] = file
    ? [{ uid: 'sf-import-file', name: file.name, size: file.size }]
    : []

  const previewColumns = useMemo<ColumnsType<Record<string, unknown>>>(() => {
    const columns: ColumnsType<Record<string, unknown>> = [
      {
        title: '序号',
        key: 'seq',
        width: 80,
        align: 'right',
        render: (_: unknown, __: Record<string, unknown>, index: number) => (
          <span className="sf-num">{index + 1}</span>
        ),
      },
    ]
    for (const column of previewQuery.data?.columns ?? []) {
      columns.push({
        title: column.title,
        dataIndex: column.key,
        key: column.key,
        width: 150,
        render: (value: unknown) => renderPreviewCell(value),
      })
    }
    return columns
  }, [previewQuery.data])
  const previewScrollX = Math.max(640, (previewQuery.data?.columns.length ?? 0) * 150 + 100)

  return (
    <div className="sf-page">
      {contextHolder}
      <SfPageHeader title="Excel 导入" subtitle="六步向导：下载模板 → 上传 → 校验 → 预览 → 确认 → 结果" />
      <Card size="small" styles={{ body: { padding: 16 } }}>
        <Steps
          current={current}
          onChange={(step) => {
            // 仅允许回退已完成步骤；导入完成后锁定向导，避免重复执行（excel.md §6.3 幂等）
            if (confirmResult === null && step < current) setCurrent(step)
          }}
          items={STEP_ITEMS.map((item, index) => ({
            ...item,
            disabled: index > current || (confirmResult !== null && index < current),
          }))}
        />

        <div style={{ marginTop: 16 }}>
          {current === 0 &&
            (templatesQuery.isPending ? (
              <SfLoading rows={2} />
            ) : templatesQuery.isError ? (
              <SfError
                error={templatesQuery.error}
                onRetry={() => void templatesQuery.refetch()}
                description="模板列表来自 GET /api/imports/templates，请稍后重试"
              />
            ) : templates.length === 0 ? (
              <SfEmpty description="后端暂未下发导入模板（GET /api/imports/templates 返回为空）" />
            ) : (
              <Flex vertical gap={12} style={{ maxWidth: 640 }}>
                <Flex vertical gap={4}>
                  <Text strong>第一步：下载模板</Text>
                  <Text type="secondary">选择导入类型并下载对应模板，按模板填写后进入下一步（excel.md §1.2）。</Text>
                </Flex>
                <Select
                  style={{ width: 320 }}
                  placeholder="请选择导入类型"
                  value={importType}
                  onChange={handleSelectType}
                  options={templates.map((template) => ({ label: template.name, value: template.import_type }))}
                />
                {selectedTemplate?.description && (
                  <Text type="secondary">{selectedTemplate.description}</Text>
                )}
                {selectedTemplate !== null && (
                  <>
                    <Button
                      icon={<DownloadOutlined />}
                      loading={downloadingTemplate}
                      onClick={handleDownloadTemplate}
                    >
                      下载模板
                    </Button>
                    {selectedTemplate.high_risk && (
                      <Alert
                        type="warning"
                        showIcon
                        message="该导入类型属于高危操作"
                        description="导入将直接写入库存并产生库存流水（业务类型=初始化导入），确认导入环节要求二次确认（excel.md §6.1）。"
                      />
                    )}
                  </>
                )}
              </Flex>
            ))}

          {current === 1 && (
            <Flex vertical gap={12} style={{ maxWidth: 640 }}>
              <Flex vertical gap={4}>
                <Text strong>第二步：上传文件</Text>
                <Text type="secondary">
                  导入类型：{selectedTemplate?.name ?? resolveModuleLabel(importType)}；文件由后端解析读取（excel.md
                  §1.2），前端不做本地解析。
                </Text>
              </Flex>
              <Upload.Dragger
                accept=".xlsx"
                maxCount={1}
                fileList={uploadFileList}
                beforeUpload={handleBeforeUpload}
                onRemove={() => {
                  setFile(null)
                  return true
                }}
                disabled={uploadMutation.isPending}
              >
                <p className="ant-upload-drag-icon">
                  <InboxOutlined />
                </p>
                <p className="ant-upload-text">点击或拖拽文件到此处</p>
                <p className="ant-upload-hint">仅支持按模板填写的 .xlsx 文件（excelize 不支持旧版 .xls，选 .xls 必被后端拒绝）</p>
              </Upload.Dragger>
              {uploadMutation.isError && (
                <SfError
                  error={uploadMutation.error}
                  onRetry={() => uploadMutation.mutate()}
                  description="上传解析由 POST /api/imports（multipart）执行，失败不产生假成功"
                />
              )}
              {uploadResult !== null && (
                <Alert
                  type="success"
                  showIcon
                  message={`文件解析完成：共 ${formatNumber(uploadResult.total_rows)} 行数据，请进入下一步进行数据校验`}
                />
              )}
            </Flex>
          )}

          {current === 2 && (
            <Flex vertical gap={12}>
              <Flex vertical gap={4}>
                <Text strong>第三步：数据校验</Text>
                <Text type="secondary">
                  由后端执行字段与数据校验（必填/类型/格式/存在性/重复/数量金额合法性，excel.md §1.3）。
                </Text>
              </Flex>
              {validateMutation.isError && (
                <SfError
                  error={validateMutation.error}
                  onRetry={() => validateMutation.mutate()}
                  description="校验由 POST /api/imports/{id}/validate 执行，失败可重试"
                />
              )}
              {validateResult !== null && (
                <>
                  {validateResult.error_rows > 0 ? (
                    <Alert
                      type="error"
                      showIcon
                      message={`校验未通过：${formatNumber(validateResult.error_rows)} 行存在错误`}
                      description="请按下方逐行定位修复数据（或下载错误 Excel 修复后重新上传）；校验通过后才能进入预览。"
                    />
                  ) : (
                    <Alert type="success" showIcon message="校验通过，可进入下一步预览解析数据" />
                  )}
                  <SfSummaryBar
                    items={[
                      { label: '总行数', value: formatNumber(validateResult.total_rows) },
                      { label: '校验通过', value: formatNumber(validateResult.valid_rows) },
                      { label: '错误行', value: formatNumber(validateResult.error_rows) },
                    ]}
                  />
                  {validateResult.errors.length > 0 && (
                    <SfTable<ImportValidationError>
                      variant="nested"
                      rowKey={(record) => `${record.row}-${record.column ?? ''}-${record.message}`}
                      columns={ERROR_COLUMNS}
                      dataSource={validateResult.errors}
                      pagination={{ pageSize: 10 }}
                      total={validateResult.errors.length}
                    />
                  )}
                  {validateResult.error_rows > 0 && (
                    <Button
                      icon={<DownloadOutlined />}
                      loading={downloadingErrorFile}
                      onClick={handleDownloadErrorFile}
                    >
                      下载错误 Excel（原数据 + 错误原因列）
                    </Button>
                  )}
                </>
              )}
              {validateResult === null && !validateMutation.isError && (
                <Text type="secondary">点击下方「开始校验」执行后端校验；错误将逐行精确定位展示（excel.md §1.4）。</Text>
              )}
            </Flex>
          )}

          {current === 3 && (
            <Flex vertical gap={12}>
              <Flex vertical gap={4}>
                <Text strong>第四步：预览</Text>
                <Text type="secondary">预览为后端解析后的结构化数据（GET /api/imports/&#123;id&#125;/preview）。</Text>
              </Flex>
              {previewQuery.isPending ? (
                <SfLoading rows={5} />
              ) : previewQuery.isError ? (
                <SfError
                  error={previewQuery.error}
                  onRetry={() => void previewQuery.refetch()}
                  description="预览来自 GET /api/imports/{id}/preview，请稍后重试"
                />
              ) : previewQuery.data ? (
                <>
                  <SfSummaryBar
                    items={[
                      { label: '总行数', value: formatNumber(previewQuery.data.total_rows) },
                      { label: '列数', value: formatNumber(previewQuery.data.columns.length) },
                    ]}
                  />
                  <SfTable<Record<string, unknown>>
                    variant="nested"
                    rowKey={(_record, index) => `row-${index ?? 0}`}
                    columns={previewColumns}
                    dataSource={previewQuery.data.rows}
                    scroll={{ x: previewScrollX }}
                    pagination={{ pageSize: 10 }}
                    total={previewQuery.data.rows.length}
                  />
                </>
              ) : (
                <SfEmpty description="暂无预览数据" />
              )}
            </Flex>
          )}

          {current === 4 && (
            <Flex vertical gap={12}>
              <Flex vertical gap={4}>
                <Text strong>第五步：确认导入</Text>
                <Text type="secondary">
                  确认后由后端执行批量写入（分批事务，单批失败不影响已完成批次，excel.md §6.2）。
                </Text>
              </Flex>
              <Descriptions
                size="small"
                column={2}
                items={[
                  {
                    key: 'type',
                    label: '导入类型',
                    children: selectedTemplate?.name ?? resolveModuleLabel(uploadResult?.import_type),
                  },
                  { key: 'file', label: '文件名', children: uploadResult?.file_name ?? file?.name ?? '-' },
                  {
                    key: 'total',
                    label: '总行数',
                    children: <span className="sf-num">{formatNumber(validateResult?.total_rows)}</span>,
                  },
                  {
                    key: 'valid',
                    label: '校验通过',
                    children: <span className="sf-num">{formatNumber(validateResult?.valid_rows)}</span>,
                  },
                ]}
              />
              {highRisk && (
                <Alert
                  type="warning"
                  showIcon
                  message="该导入类型为高危操作"
                  description="点击「确认导入」后将弹出二次确认，确认无误才会执行导入（excel.md §6.1）。"
                />
              )}
              {confirmMutation.isError && (
                <SfError
                  error={confirmMutation.error}
                  onRetry={handleConfirmImport}
                  description="确认导入由 POST /api/imports/{id}/confirm 执行，失败不产生假成功"
                />
              )}
            </Flex>
          )}

          {current === 5 &&
            (confirmResult === null ? (
              <SfEmpty description="尚未执行导入" />
            ) : (
              <Flex vertical gap={12}>
                <Flex gap={8} align="center" wrap="wrap">
                  <Text strong>导入结果：</Text>
                  {renderTaskStatus(confirmResult.status)}
                  {confirmResult.finished_at && (
                    <Text type="secondary">{formatDateTime(confirmResult.finished_at)}</Text>
                  )}
                </Flex>
                <SfSummaryBar
                  items={[
                    { label: '总行数', value: formatNumber(confirmResult.total_rows) },
                    { label: '成功', value: formatNumber(confirmResult.success_rows) },
                    { label: '失败', value: formatNumber(confirmResult.failed_rows) },
                  ]}
                />
                {confirmResult.status === 'EXECUTING' && (
                  <Alert
                    type="info"
                    showIcon
                    message="导入任务已进入执行中（EXECUTING），处理完成后可在下方任务记录中查看结果（excel.md §4）。"
                  />
                )}
                {confirmResult.errors && confirmResult.errors.length > 0 && (
                  <>
                    <Alert
                      type="warning"
                      showIcon
                      message="部分数据导入失败，失败明细如下"
                      description="批量写入采用分批事务：单批失败不影响已完成批次，失败明细进入导入结果（excel.md §6.2）。"
                    />
                    <SfTable<ImportValidationError>
                      variant="nested"
                      rowKey={(record) => `${record.row}-${record.column ?? ''}-${record.message}`}
                      columns={ERROR_COLUMNS}
                      dataSource={confirmResult.errors}
                      pagination={{ pageSize: 10 }}
                      total={confirmResult.errors.length}
                    />
                  </>
                )}
                <Text type="secondary">
                  导入执行具备幂等守卫，同一任务不会重复执行（excel.md §6.3）。点击「再导入一批」开始新的导入。
                </Text>
              </Flex>
            ))}
        </div>

        <Flex justify="space-between" align="center" wrap="wrap" gap={8} style={{ marginTop: 16 }}>
          <Button disabled={current === 0 || confirmResult !== null} onClick={() => setCurrent(current - 1)}>
            上一步
          </Button>
          <Space wrap>
            {current === 0 && (
              <Button type="primary" disabled={selectedTemplate === null} onClick={() => setCurrent(1)}>
                下一步：上传文件
              </Button>
            )}
            {current === 1 && (
              <>
                <Button
                  loading={uploadMutation.isPending}
                  disabled={file === null || importType === undefined}
                  onClick={() => uploadMutation.mutate()}
                >
                  上传并解析
                </Button>
                <Button type="primary" disabled={uploadResult === null} onClick={() => setCurrent(2)}>
                  下一步：数据校验
                </Button>
              </>
            )}
            {current === 2 && (
              <>
                <Button
                  loading={validateMutation.isPending}
                  disabled={uploadResult === null}
                  onClick={() => validateMutation.mutate()}
                >
                  {validateResult ? '重新校验' : '开始校验'}
                </Button>
                <Button
                  type="primary"
                  disabled={validateResult === null || validateResult.error_rows > 0}
                  onClick={() => setCurrent(3)}
                >
                  下一步：预览
                </Button>
              </>
            )}
            {current === 3 && (
              <Button type="primary" disabled={previewQuery.data === undefined} onClick={() => setCurrent(4)}>
                下一步：确认导入
              </Button>
            )}
            {current === 4 && (
              <Button
                type="primary"
                danger={highRisk}
                loading={confirmMutation.isPending}
                onClick={handleConfirmImport}
              >
                确认导入
              </Button>
            )}
            {current === 5 && (
              <Button type="primary" onClick={resetWizard}>
                再导入一批
              </Button>
            )}
          </Space>
        </Flex>
      </Card>

      {/* 初始化库存导入二次确认（excel.md §6.1：高危操作必须二次确认，导入产生库存流水） */}
      <Modal
        title="初始化库存导入二次确认"
        open={riskModalOpen}
        okText="确认导入"
        okButtonProps={{ danger: true, disabled: !riskAck, loading: confirmMutation.isPending }}
        onOk={() => confirmMutation.mutate()}
        onCancel={() => setRiskModalOpen(false)}
      >
        <Alert
          type="warning"
          showIcon
          message="初始化库存导入属于高危操作"
          description="导入将直接写入库存并生成库存流水（业务类型=初始化导入）；批量写入采用分批事务，单批失败不影响已完成批次，失败明细进入导入结果。"
          style={{ marginBottom: 16 }}
        />
        <Checkbox checked={riskAck} onChange={(e) => setRiskAck(e.target.checked)}>
          我已确认导入文件内容无误，了解该操作对库存数据的直接影响
        </Checkbox>
      </Modal>

      <SfDetailSection
        title="导入任务记录"
        extra={<Text type="secondary">数据中心·导入任务（excel.md §4）</Text>}
      >
        <SfSearchForm
          fields={[{ name: 'status', label: '状态', control: 'select', options: TASK_STATUS_OPTIONS }]}
          onSearch={(values) => {
            setTaskParams(values as DataTaskQuery)
            tasks.resetToFirstPage()
          }}
        />
        <SfTable<DataTask>
          storageKey="data-import-tasks"
          rowKey="id"
          columns={IMPORT_TASK_COLUMNS}
          dataSource={tasks.items}
          loading={tasks.isFetching}
          error={tasks.error}
          onRetry={tasks.refetch}
          onRefresh={tasks.refetch}
          pagination={tasks.pagination}
          total={tasks.total}
          onPageChange={tasks.onPageChange}
          emptyText="暂无导入任务，完成一次导入后在此查看执行记录"
          scrollX={1300}
        />
      </SfDetailSection>
    </div>
  )
}
