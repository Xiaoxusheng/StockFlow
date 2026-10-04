import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 数据中心（后端 M3 已交付：internal/datax/handler.go:92-125 装配四组路由，
// 出参为 datax 视图 snake_case——plan §12.4 条 2：后端为冻结契约） ----------
//
// 导入管线（excel.md §1.2）：模板（现场生成无静态文件）→ 上传解析 → 校验 → 预览 →
// 确认导入 → 任务记录；导出走异步任务（excel.md §3）。

/** 下拉/筛选项通用结构 */
export interface LabelValueOption {
  label: string
  value: string
}

/** 导入类型（internal/datax/registry.go:8-18 九值冻结，excel.md §1.1） */
export type ImportType =
  | 'PRODUCT'
  | 'SKU'
  | 'SUPPLIER'
  | 'CUSTOMER'
  | 'WAREHOUSE'
  | 'LOCATION'
  | 'PURCHASE_ORDER'
  | 'SALES_ORDER'
  | 'INITIAL_INVENTORY'

/** 导入类型选项（registry.go:41-44 冻结顺序；后端模板接口失败时兜底展示用） */
export const IMPORT_TYPE_OPTIONS: Array<{ label: string; value: ImportType }> = [
  { label: '商品导入', value: 'PRODUCT' },
  { label: 'SKU 导入', value: 'SKU' },
  { label: '供应商导入', value: 'SUPPLIER' },
  { label: '客户导入', value: 'CUSTOMER' },
  { label: '仓库导入', value: 'WAREHOUSE' },
  { label: '库位导入', value: 'LOCATION' },
  { label: '采购订单导入', value: 'PURCHASE_ORDER' },
  { label: '销售订单导入', value: 'SALES_ORDER' },
  { label: '初始化库存导入', value: 'INITIAL_INVENTORY' },
]

/** 初始化库存导入为高危操作（excel.md §6.1：必须走二次确认；registry.go:152-154 同一判定） */
export function isHighRiskImport(importType?: string | null): boolean {
  return importType === 'INITIAL_INVENTORY'
}

/**
 * 导入/导出任务状态（internal/datax/model.go:21-29 + 000011 CHECK 同源）：
 * 导入任务走 PARSED→VALIDATED→EXECUTING→SUCCESS/PARTIAL_SUCCESS/FAILED，
 * 导出任务走 QUEUED→PROCESSING→SUCCESS/FAILED（导出单值成功无部分成功）。
 */
export type DataTaskStatus =
  | 'QUEUED'
  | 'PROCESSING'
  | 'PARSED'
  | 'VALIDATED'
  | 'EXECUTING'
  | 'SUCCESS'
  | 'PARTIAL_SUCCESS'
  | 'FAILED'

/** 任务状态文案与语义色（types/status.ts 注册表未含数据中心状态，经 SfStatusTag 显式指定，frontend.md §24） */
export const DATA_TASK_STATUS_META: Record<DataTaskStatus, { label: string; semantic: StatusSemantic }> = {
  QUEUED: { label: '排队中', semantic: 'pending' },
  PROCESSING: { label: '处理中', semantic: 'processing' },
  PARSED: { label: '已解析', semantic: 'processing' },
  VALIDATED: { label: '校验通过', semantic: 'processing' },
  EXECUTING: { label: '执行中', semantic: 'processing' },
  SUCCESS: { label: '成功', semantic: 'success' },
  PARTIAL_SUCCESS: { label: '部分成功', semantic: 'warning' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 任务是否在途（进度轮询仅在在途时进行，进入终态自动停止） */
export function isTaskInFlight(status?: string | null): boolean {
  return status === 'QUEUED' || status === 'PROCESSING' || status === 'EXECUTING'
}

/** 任务是否终态（终态后才允许下载产物/错误文件） */
export function isTaskFinished(status?: string | null): boolean {
  return status === 'SUCCESS' || status === 'PARTIAL_SUCCESS' || status === 'FAILED'
}

/** 任务类型（000011 CHECK：IMPORT / EXPORT） */
export type DataTaskType = 'IMPORT' | 'EXPORT'

export function resolveTaskTypeLabel(taskType?: string | null): string {
  if (taskType === 'IMPORT') return '导入'
  if (taskType === 'EXPORT') return '导出'
  return taskType ?? '-'
}

/** 导出范围（excel.md §2.2 五值：当前页/选中/全部/按筛选条件/按时间范围；registry 冻结
 * exportScopes，internal/datax/service_export.go:46-50） */
export type ExportScope = 'CURRENT_PAGE' | 'SELECTED' | 'ALL' | 'BY_FILTER' | 'TIME_RANGE'

export const EXPORT_SCOPE_OPTIONS: LabelValueOption[] = [
  { label: '当前页导出', value: 'CURRENT_PAGE' },
  { label: '选中数据导出', value: 'SELECTED' },
  { label: '全部导出', value: 'ALL' },
  { label: '按筛选条件导出', value: 'BY_FILTER' },
  { label: '按时间范围导出', value: 'TIME_RANGE' },
]

export function resolveScopeLabel(scope?: string | null): string {
  return EXPORT_SCOPE_OPTIONS.find((option) => option.value === scope)?.label ?? scope ?? '-'
}

/** 业务模块选项（导出范围覆盖 excel.md §2.1 全量；值集/顺序与 registry.go:47-52 冻结同源） */
export const EXPORT_MODULE_OPTIONS: LabelValueOption[] = [
  { label: '商品', value: 'PRODUCT' },
  { label: 'SKU', value: 'SKU' },
  { label: '供应商', value: 'SUPPLIER' },
  { label: '客户', value: 'CUSTOMER' },
  { label: '仓库', value: 'WAREHOUSE' },
  { label: '库位', value: 'LOCATION' },
  { label: '采购订单', value: 'PURCHASE_ORDER' },
  { label: '入库单', value: 'PURCHASE_INBOUND' },
  { label: '质检单', value: 'QUALITY' },
  { label: '出库单', value: 'SALES_OUTBOUND' },
  { label: '库存', value: 'INVENTORY' },
  { label: '库存流水', value: 'INVENTORY_LEDGER' },
  { label: '调拨单', value: 'TRANSFER' },
  { label: '盘点单', value: 'COUNT' },
  { label: '异常单', value: 'EXCEPTION' },
  { label: '报表', value: 'REPORT' },
]

const MODULE_LABELS: Record<string, string> = Object.fromEntries(
  [...IMPORT_TYPE_OPTIONS, ...EXPORT_MODULE_OPTIONS].map((option) => [option.value, option.label]),
)

/** 业务模块展示名（后端 moduleName 缺失时按契约值兜底） */
export function resolveModuleLabel(module?: string | null): string {
  if (!module) return '-'
  return MODULE_LABELS[module] ?? module
}

// ---------- 导入（excel.md §1；internal/datax/contract.go + service_import.go） ----------

/** 单元格类型（contract.go:26-31；驱动导入解析与导出数字格式，禁止全字符串导出） */
export type ImportCellType = 'TEXT' | 'NUMBER' | 'MONEY' | 'DATE'

/** 列定义（Column，contract.go:64-74；模板列头 + 上传表头逐列比对依据） */
export interface ImportTemplateColumn {
  key: string
  title: string
  type: ImportCellType
  /** 列宽（字符） */
  width?: number
  /** 必填（excel.md §1.3） */
  required?: boolean
  /** 文件内去重（excel.md §1.3） */
  unique_in_file?: boolean
  /** 模板示例值 */
  example?: string
  /** 填表说明 */
  note?: string
}

/**
 * 导入模板（TemplateSpec，contract.go:78-87）。模板下载 = 后端按列定义用 excelize
 * 现场生成（GET /api/imports/templates/{type} 流式下发），无静态文件、无 downloadUrl
 * 字段——下载统一走 downloadImportTemplate。
 */
export interface ImportTemplate {
  import_type: ImportType
  /** 展示名（如「商品导入」） */
  name: string
  /** 模板下载文件名（如「商品导入模板.xlsx」） */
  file_name: string
  description: string
  /** 数据页名 */
  sheet: string
  columns: ImportTemplateColumn[]
  /** 示例行（与 columns 等长对齐的原始文本） */
  sample_rows: string[][]
  /** 校验说明（excel.md §1.2 模板必须携带校验说明） */
  notes: string[]
}

/** 上传解析结果（ImportUploadResult，service_import.go:93-100 字段全量） */
export interface ImportUploadResult {
  id: string
  /** 导入单号（IMP- 前缀） */
  import_no: string
  import_type: ImportType
  file_name: string
  total_rows: number
  status: DataTaskStatus
}

/** 逐行校验错误（RowError，contract.go:91-95：第 N 行 + 出错列键 + 原因） */
export interface ImportValidationError {
  /** 数据行号（1 起，不含表头） */
  row: number
  /** 出错列键；行级/跨列错误缺省 */
  column?: string
  message: string
}

/** 校验结果（ImportValidateResult，service_import.go:338-347 字段全量；
 * 校验幂等可重复执行，error_rows>0 时生成错误 Excel 并回填下载地址） */
export interface ImportValidateResult {
  id: string
  status: DataTaskStatus
  total_rows: number
  valid_rows: number
  error_rows: number
  errors: ImportValidationError[]
  /** 错误 Excel（原数据 + 错误原因列）下载地址；后端未生成时不返回 */
  error_file_url?: string
  error_file_name?: string
}

/** 预览列定义（ImportPreviewColumn，service_import.go:575-578） */
export interface ImportPreviewColumn {
  key: string
  title: string
}

/** 解析后的结构化数据预览（ImportPreviewResult，service_import.go:580-585；前 100 行） */
export interface ImportPreviewResult {
  id: string
  total_rows: number
  columns: ImportPreviewColumn[]
  rows: Array<Record<string, unknown>>
}

/** 导入结果（ImportConfirmResult，service_import.go:678-686；逐行失败进 errors） */
export interface ImportConfirmResult {
  id: string
  status: DataTaskStatus
  total_rows: number
  success_rows: number
  failed_rows: number
  errors?: ImportValidationError[]
  finished_at?: string
}

// ---------- 任务记录（GET /api/imports、/api/exports，出参 ListTaskItem，service_import.go:794-815） ----------

/** 任务列表筛选（handler.go:237-268：module/status） */
export interface DataTaskQuery extends PageQuery {
  status?: DataTaskStatus
  /** 导入任务=导入类型；导出任务=导出模块 */
  module?: string
}

/** 任务记录（ListTaskItem 字段全量；文件地址/有效期终态后下发） */
export interface DataTask {
  id: string
  /** 任务单号（导入 IMP- / 导出 EXP-） */
  task_no: string
  task_type: DataTaskType
  module: string
  module_name: string
  scope?: ExportScope
  status: DataTaskStatus
  /** 处理进度 0-100（导入由 success/failed 行数推导，plan §13.3） */
  progress?: number
  total_rows?: number
  success_rows?: number
  failed_rows?: number
  creator_id?: number
  file_name?: string
  /** 产物/错误文件下载地址（终态后下发；一律经认证 Blob 下载） */
  file_url?: string
  /** 文件有效期（excel.md §3：过期自动清理） */
  file_expired_at?: string
  started_at?: string
  finished_at?: string
  created_at: string
  error_message?: string
}

// ---------- 导出（excel.md §2/§3；ExportCreateInput，service_export.go:24-32） ----------

/** 创建导出任务入参（scope 五值；TIME_RANGE 必填 time_from/time_to） */
export interface ExportCreatePayload {
  module: string
  scope: ExportScope
  /** scope=SELECTED：选中记录 ID（≤1000，后端校验） */
  ids?: string[]
  /** scope=CURRENT_PAGE：页码/页大小（单页上限 100 行） */
  page?: number
  page_size?: number
  /** scope=BY_FILTER：透传列表筛选条件（各域白名单键，未知键忽略） */
  filters?: Record<string, string>
  /** scope=TIME_RANGE：创建时间区间（YYYY-MM-DD HH:mm:ss） */
  time_from?: string
  time_to?: string
}

// ---------- API ----------

/** 大文件上传、批量校验/导入/导出耗时高于默认 15s，数据中心请求统一放宽超时 */
const DATA_REQUEST_TIMEOUT_MS = 60_000

export const dataApi = {
  imports: {
    /** 导入任务列表（GET /api/imports） */
    list: (query: DataTaskQuery) => http.get<PageResult<DataTask>>('/api/imports', { params: query }),
    /** 模板规格列表（GET /api/imports/templates） */
    templates: () => http.get<ImportTemplate[]>('/api/imports/templates'),
    /** 上传并解析（multipart：import_type + file；Content-Type 与 boundary 由 axios 按 FormData 自动携带） */
    upload: (payload: { importType: ImportType; file: File }) => {
      const form = new FormData()
      form.append('importType', payload.importType)
      form.append('file', payload.file)
      return http.post<ImportUploadResult>('/api/imports', form, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      })
    },
    /** 数据校验（POST /api/imports/{id}/validate；PARSED/VALIDATED 幂等可重复执行） */
    validate: (id: string) =>
      http.post<ImportValidateResult>(`/api/imports/${id}/validate`, undefined, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      }),
    /** 解析后结构化数据预览（GET /api/imports/{id}/preview，前 100 行） */
    preview: (id: string) => http.get<ImportPreviewResult>(`/api/imports/${id}/preview`),
    /** 确认导入（POST /api/imports/{id}/confirm；仅 VALIDATED 且无错误行；
     * 初始化库存导入必须 confirmed=true 二次确认，excel.md §6.1） */
    confirm: (id: string, payload: { confirmed: boolean }) =>
      http.post<ImportConfirmResult>(`/api/imports/${id}/confirm`, payload, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      }),
  },
  exports: {
    /** 导出任务列表（GET /api/exports） */
    list: (query: DataTaskQuery) => http.get<PageResult<DataTask>>('/api/exports', { params: query }),
    /** 创建导出任务（POST /api/exports，异步任务化执行；创建即审计） */
    create: (payload: ExportCreatePayload) =>
      http.post<DataTask>('/api/exports', payload, { timeout: DATA_REQUEST_TIMEOUT_MS }),
  },
  /** 任务详情刷新（GET /api/data-tasks/import/{id}、/api/data-tasks/export/{id}，
   * handler.go:123-124——导入/导出各自权限点，防跨资源权限借用） */
  importTask: (id: string) => http.get<DataTask>(`/api/data-tasks/import/${id}`),
  exportTask: (id: string) => http.get<DataTask>(`/api/data-tasks/export/${id}`),
}

/**
 * 统一文件下载：走 axios 单例携带认证头，以 Blob 落地后触发浏览器保存。
 * 后端未交付/下载失败时抛出 ApiError，由调用方呈可读错误态；前端绝不生成假文件。
 */
export async function downloadFile(url: string, fileName: string): Promise<void> {
  const blob = await http.get<Blob>(url, {
    responseType: 'blob',
    timeout: DATA_REQUEST_TIMEOUT_MS,
  })
  const objectUrl = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectUrl
  anchor.download = fileName
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(objectUrl)
}

/** 导入模板下载（GET /api/imports/templates/{type}，后端现场生成 xlsx 流式下发） */
export function downloadImportTemplate(template: Pick<ImportTemplate, 'import_type' | 'file_name'>): Promise<void> {
  return downloadFile(`/api/imports/templates/${template.import_type}`, template.file_name)
}

/** 导入错误 Excel 下载（GET /api/imports/{id}/error-file，handler.go:110；error_rows>0 时可用，
 * 优先校验结果下发的 error_file_url） */
export function downloadImportErrorFile(
  id: string,
  fileName: string,
  validateResult?: Pick<ImportValidateResult, 'error_file_url' | 'error_file_name'>,
): Promise<void> {
  const url = validateResult?.error_file_url ?? `/api/imports/${id}/error-file`
  return downloadFile(url, validateResult?.error_file_name ?? fileName)
}

/** 导出产物下载（GET /api/exports/{id}/file，handler.go:117；终态后可用，
 * 优先任务下发的 file_url） */
export function downloadExportFile(task: Pick<DataTask, 'id' | 'file_name' | 'file_url'>): Promise<void> {
  return downloadFile(task.file_url ?? `/api/exports/${task.id}/file`, task.file_name ?? `导出_${task.id}.xlsx`)
}
