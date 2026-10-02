import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 数据中心：Excel 导入 / 导出（excel.md §1–§4 + frontend.md §12） ----------
// 前端先行契约：字段对齐 excel.md §4 任务记录模型，后端契约冻结后统一回对；
// 后端未交付时页面呈统一错误态，前端不解析、不伪造、不生成假文件。

/** 下拉/筛选项通用结构 */
export interface LabelValueOption {
  label: string
  value: string
}

/** 导入类型（excel.md §1.1） */
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

/** 导入类型选项（excel.md §1.1；后端模板接口失败时兜底展示用） */
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

/** 初始化库存导入为高危操作（excel.md §6.1：必须走审批或二次确认） */
export function isHighRiskImport(importType?: string | null): boolean {
  return importType === 'INITIAL_INVENTORY'
}

/** 导入/导出任务状态（excel.md §4：排队中/处理中/成功/部分成功/失败） */
export type DataTaskStatus = 'QUEUED' | 'PROCESSING' | 'SUCCESS' | 'PARTIAL_SUCCESS' | 'FAILED'

/** 任务状态文案与语义色（types/status.ts 注册表未含数据中心状态，经 SfStatusTag 显式指定，frontend.md §24） */
export const DATA_TASK_STATUS_META: Record<DataTaskStatus, { label: string; semantic: StatusSemantic }> = {
  QUEUED: { label: '排队中', semantic: 'pending' },
  PROCESSING: { label: '处理中', semantic: 'processing' },
  SUCCESS: { label: '成功', semantic: 'success' },
  PARTIAL_SUCCESS: { label: '部分成功', semantic: 'warning' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 任务是否在途（进度轮询仅在在途时进行，进入终态自动停止） */
export function isTaskInFlight(status?: string | null): boolean {
  return status === 'QUEUED' || status === 'PROCESSING'
}

/** 任务是否终态（终态后才允许下载产物） */
export function isTaskFinished(status?: string | null): boolean {
  return status === 'SUCCESS' || status === 'PARTIAL_SUCCESS' || status === 'FAILED'
}

/** 任务类型（excel.md §4 任务记录模型） */
export type DataTaskType = 'IMPORT' | 'EXPORT'

export function resolveTaskTypeLabel(taskType?: string | null): string {
  if (taskType === 'IMPORT') return '导入'
  if (taskType === 'EXPORT') return '导出'
  return taskType ?? '-'
}

/** 导出范围（excel.md §2.2：当前页/选中/全部/按筛选条件/按时间范围） */
export type ExportScope = 'CURRENT_PAGE' | 'SELECTED' | 'ALL' | 'BY_FILTER'

export const EXPORT_SCOPE_OPTIONS: LabelValueOption[] = [
  { label: '当前页导出', value: 'CURRENT_PAGE' },
  { label: '选中数据导出', value: 'SELECTED' },
  { label: '全部导出', value: 'ALL' },
  { label: '按筛选条件导出', value: 'BY_FILTER' },
]

export function resolveScopeLabel(scope?: string | null): string {
  return EXPORT_SCOPE_OPTIONS.find((option) => option.value === scope)?.label ?? scope ?? '-'
}

/** 业务模块选项（导出范围覆盖 excel.md §2.1 全量列表；模块值为前端先行命名，冻结后回对） */
export const EXPORT_MODULE_OPTIONS: LabelValueOption[] = [
  { label: '商品', value: 'PRODUCT' },
  { label: 'SKU', value: 'SKU' },
  { label: '供应商', value: 'SUPPLIER' },
  { label: '客户', value: 'CUSTOMER' },
  { label: '仓库', value: 'WAREHOUSE' },
  { label: '库位', value: 'LOCATION' },
  { label: '采购订单', value: 'PURCHASE_ORDER' },
  { label: '入库单', value: 'PURCHASE_INBOUND' },
  { label: '出库单', value: 'SALES_OUTBOUND' },
  { label: '库存', value: 'INVENTORY' },
  { label: '库存流水', value: 'INVENTORY_LEDGER' },
  { label: '调拨单', value: 'TRANSFER' },
  { label: '盘点单', value: 'COUNT' },
  { label: '质检单', value: 'QUALITY' },
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

// ---------- 导入（excel.md §1） ----------

/** 导入模板（GET /api/imports/templates，excel.md §1.2 第 1 步：下载模板） */
export interface ImportTemplate {
  importType: ImportType
  name: string
  fileName?: string
  description?: string
  /** 模板下载地址（后端下发，前端不拼接、不生成假文件） */
  downloadUrl: string
}

/** 上传解析结果（POST /api/imports multipart，excel.md §1.2 第 2 步：读取/后端解析） */
export interface ImportUploadResult {
  /** 导入任务 ID（后续校验/预览/确认均以其为凭据） */
  id: string
  importType: ImportType
  fileName?: string
  totalRows?: number
  status: DataTaskStatus
}

/** 逐行校验错误（excel.md §1.4：第 N 行：原因） */
export interface ImportValidationError {
  /** Excel 数据行号（含表头偏移的定义由后端统一） */
  row: number
  /** 出错列名（可缺省） */
  column?: string
  message: string
}

/** 校验结果（POST /api/imports/{id}/validate，excel.md §1.2 第 3 步 / §1.3 校验规则） */
export interface ImportValidateResult {
  totalRows: number
  validRows: number
  errorRows: number
  errors: ImportValidationError[]
  /** 错误 Excel（原数据 + 错误原因列）下载地址；后端未生成时不返回 */
  errorFileUrl?: string
  errorFileName?: string
  status: DataTaskStatus
}

/** 预览列定义（GET /api/imports/{id}/preview，excel.md §1.2 第 4 步） */
export interface ImportPreviewColumn {
  key: string
  title: string
}

/** 解析后的结构化数据预览（前端不做本地解析，仅渲染后端结果） */
export interface ImportPreviewResult {
  id: string
  totalRows: number
  columns: ImportPreviewColumn[]
  rows: Array<Record<string, unknown>>
}

/** 导入结果（POST /api/imports/{id}/confirm，excel.md §1.2 第 6 步：成功数/失败数/错误明细） */
export interface ImportConfirmResult {
  id: string
  status: DataTaskStatus
  totalRows: number
  successRows: number
  failedRows: number
  /** 失败明细（分批事务失败行进入导入结果，excel.md §6.2） */
  errors?: ImportValidationError[]
  finishedAt?: string
}

// ---------- 任务记录（excel.md §4 数据中心任务化模型） ----------

/** 导入/导出任务列表筛选（参数名为前端先行定义，冻结后回对） */
export interface DataTaskQuery extends PageQuery {
  status?: DataTaskStatus
  module?: string
}

/** 任务记录（excel.md §4：任务类型/业务模块/创建人/起止时间/数量/成功失败/文件/状态） */
export interface DataTaskItem {
  id: string
  taskType: DataTaskType
  module: string
  moduleName?: string
  scope?: ExportScope
  fileName?: string
  creator?: string
  totalRows?: number
  successRows?: number
  failedRows?: number
  /** 处理进度 0-100（excel.md §3：处理中 35%）；仅处理中且后端下发时展示 */
  progress?: number
  status: DataTaskStatus
  /** 产物/错误文件下载地址（终态后下发） */
  fileUrl?: string
  /** 文件有效期（excel.md §3：过期自动清理） */
  fileExpiredAt?: string
  startedAt?: string
  finishedAt?: string
  createdAt?: string
  /** 失败原因 */
  errorMessage?: string
}

// ---------- 导出（excel.md §2/§3） ----------

/** 创建导出任务（POST /api/exports；范围定义 excel.md §2.2） */
export interface ExportCreatePayload {
  /** 业务模块（EXPORT_MODULE_OPTIONS 值集） */
  module: string
  scope: ExportScope
  /** scope=SELECTED：选中记录 ID */
  ids?: string[]
  /** scope=CURRENT_PAGE：页码/页大小 */
  page?: number
  pageSize?: number
  /** scope=BY_FILTER：透传列表筛选条件（键值随模块，后端冻结后回对） */
  filters?: Record<string, string>
  /** 按时间范围导出（excel.md §2.2），格式 YYYY-MM-DD HH:mm:ss */
  startTime?: string
  endTime?: string
}

// ---------- API ----------

/** 大文件上传、批量校验/导入/导出耗时高于默认 15s，数据中心请求统一放宽超时 */
const DATA_REQUEST_TIMEOUT_MS = 60_000

export const dataApi = {
  imports: {
    /** 模板列表（excel.md §1.2 第 1 步） */
    templates: () => http.get<ImportTemplate[]>('/api/imports/templates'),
    /** 上传并解析（multipart；Content-Type 与 boundary 由 axios 按 FormData 自动携带） */
    upload: (payload: { importType: ImportType; file: File }) => {
      const form = new FormData()
      form.append('importType', payload.importType)
      form.append('file', payload.file)
      return http.post<ImportUploadResult>('/api/imports', form, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      })
    },
    /** 数据校验（excel.md §1.2 第 3 步） */
    validate: (id: string) =>
      http.post<ImportValidateResult>(`/api/imports/${id}/validate`, undefined, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      }),
    /** 解析后结构化数据预览（excel.md §1.2 第 4 步） */
    preview: (id: string) => http.get<ImportPreviewResult>(`/api/imports/${id}/preview`),
    /** 确认导入（excel.md §1.2 第 5 步；初始化库存需二次确认，excel.md §6.1） */
    confirm: (id: string) =>
      http.post<ImportConfirmResult>(`/api/imports/${id}/confirm`, undefined, {
        timeout: DATA_REQUEST_TIMEOUT_MS,
      }),
    /** 导入任务列表（excel.md §4 数据中心·导入任务） */
    list: (query: DataTaskQuery) => http.get<PageResult<DataTaskItem>>('/api/imports', { params: query }),
  },
  exports: {
    /** 导出任务列表（excel.md §3/§4） */
    list: (query: DataTaskQuery) => http.get<PageResult<DataTaskItem>>('/api/exports', { params: query }),
    /** 创建导出任务（异步任务化执行，excel.md §3） */
    create: (payload: ExportCreatePayload) =>
      http.post<DataTaskItem>('/api/exports', payload, { timeout: DATA_REQUEST_TIMEOUT_MS }),
  },
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
