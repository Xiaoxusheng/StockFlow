import { http } from './client'
import { downloadFile } from './data'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 打印中心（printing.md §1–§6 + frontend.md §13，api.md:65 /api/prints 域） ----------
//
// 前端先行契约：后端打印域未交付，全部端点按本文档字段冻结后回对；
// 后端未交付时页面呈统一错误态/空态，前端不伪造内容、不生成假 PDF（requirements.md §10）。
//
// 端点清单（api.md:65 /api/prints）：
//   GET  /api/prints/templates             模板列表
//   POST /api/prints/templates             新建模板
//   PUT  /api/prints/templates/:id         修改模板
//   POST /api/prints/templates/:id/copy    复制模板
//   PUT  /api/prints/templates/:id/status  启用/停用模板
//   POST /api/prints/tasks                 创建打印任务
//   GET  /api/prints/tasks                 打印任务列表（预览页以 id 精确过滤取单条）
//   GET  /api/prints/history               打印历史列表（printing.md §1.2：后端在任务执行时落记录）
//   GET  /api/prints/:id/pdf               任务 PDF 下载（Blob，带认证头走统一下载）

/** 视图出参 ID（对齐基础资料域：后端 database.ID 序列化为字符串，保留 number 兼容） */
export type PrintId = number | string

// ---------- 业务类型（printing.md §1.1 支持对象中 F12 首批 9 类，frontend.md §13 模板页列） ----------

export type PrintObjectType =
  | 'SKU_LABEL'
  | 'BIN_LABEL'
  | 'CARTON_CODE'
  | 'PALLET_CODE'
  | 'INBOUND_ORDER'
  | 'OUTBOUND_ORDER'
  | 'PICK_ORDER'
  | 'COUNT_ORDER'
  | 'SHIPMENT_ORDER'

export const PRINT_OBJECT_TYPE_OPTIONS: Array<{ label: string; value: PrintObjectType }> = [
  { label: 'SKU标签', value: 'SKU_LABEL' },
  { label: '库位标签', value: 'BIN_LABEL' },
  { label: '箱码', value: 'CARTON_CODE' },
  { label: '托盘', value: 'PALLET_CODE' },
  { label: '入库单', value: 'INBOUND_ORDER' },
  { label: '出库单', value: 'OUTBOUND_ORDER' },
  { label: '拣货单', value: 'PICK_ORDER' },
  { label: '盘点单', value: 'COUNT_ORDER' },
  { label: '发货单', value: 'SHIPMENT_ORDER' },
]

export function resolveObjectTypeLabel(objectType?: string | null): string {
  return PRINT_OBJECT_TYPE_OPTIONS.find((option) => option.value === objectType)?.label ?? objectType ?? '-'
}

/** 标签类（热敏/标签纸，一码一页）；其余为单据类（A4/A5，页眉+明细表+页脚，printing.md §2） */
export function isLabelObjectType(objectType?: string | null): boolean {
  return objectType === 'SKU_LABEL' || objectType === 'BIN_LABEL' || objectType === 'CARTON_CODE' || objectType === 'PALLET_CODE'
}

// ---------- 纸张（printing.md §6：A4、A5、热敏纸、标签纸多种纸张） ----------

export interface PrintPaperSpec {
  key: string
  label: string
  widthMm: number
  heightMm: number
}

export const PRINT_PAPERS: PrintPaperSpec[] = [
  { key: 'A4', label: 'A4 竖版', widthMm: 210, heightMm: 297 },
  { key: 'A5', label: 'A5 竖版', widthMm: 148, heightMm: 210 },
  { key: 'THERMAL_40_30', label: '热敏 40×30', widthMm: 40, heightMm: 30 },
  { key: 'THERMAL_60_40', label: '热敏 60×40', widthMm: 60, heightMm: 40 },
  { key: 'THERMAL_100_50', label: '热敏 100×50', widthMm: 100, heightMm: 50 },
]

export const PRINT_PAPER_OPTIONS: Array<{ label: string; value: string }> = PRINT_PAPERS.map((paper) => ({
  label: `${paper.label}（${paper.widthMm}×${paper.heightMm}mm）`,
  value: paper.key,
}))

export function resolvePaper(key?: string | null): PrintPaperSpec | undefined {
  return PRINT_PAPERS.find((paper) => paper.key === key)
}

/** 纸张展示文案：`A4 竖版（210×297mm）`；未知规格回显原始值 */
export function resolvePaperLabel(paper?: string | null): string {
  const spec = resolvePaper(paper)
  return spec ? `${spec.label}（${spec.widthMm}×${spec.heightMm}mm）` : paper ?? '-'
}

// ---------- 条码/二维码（printing.md §4.1：Code128 默认推荐，码制与扫码端 scanner.md §5.1 一致） ----------

export type BarcodeSymbology = 'CODE128' | 'CODE39' | 'EAN13' | 'EAN8' | 'UPC'

export const BARCODE_SYMBOLOGY_OPTIONS: Array<{ label: string; value: BarcodeSymbology }> = [
  { label: 'Code128（默认推荐）', value: 'CODE128' },
  { label: 'Code39', value: 'CODE39' },
  { label: 'EAN-13', value: 'EAN13' },
  { label: 'EAN-8', value: 'EAN8' },
  { label: 'UPC', value: 'UPC' },
]

export function resolveBarcodeSymbologyLabel(symbology?: string | null): string {
  return BARCODE_SYMBOLOGY_OPTIONS.find((option) => option.value === symbology)?.label ?? symbology ?? '-'
}

// ---------- 模板字段绑定（printing.md §2：可配置的数据字段绑定；键为前端先行命名，冻结后回对） ----------

export interface PrintTemplateField {
  key: string
  label: string
}

/** 各业务类型的模板字段预设（表单多选项 + 渲染层字段文案的唯一来源） */
export const PRINT_TEMPLATE_FIELD_PRESETS: Record<PrintObjectType, Array<{ label: string; value: string }>> = {
  SKU_LABEL: [
    { label: 'SKU编码', value: 'sku_code' },
    { label: '商品名称', value: 'product_name' },
    { label: '规格', value: 'spec' },
    { label: '单位', value: 'unit' },
    { label: '数量', value: 'qty' },
  ],
  BIN_LABEL: [
    { label: '仓库', value: 'warehouse_name' },
    { label: '库区', value: 'zone_name' },
    { label: '货架', value: 'shelf_code' },
    { label: '库位编码', value: 'bin_code' },
  ],
  CARTON_CODE: [
    { label: '箱码', value: 'carton_code' },
    { label: '关联单号', value: 'order_no' },
    { label: '箱序', value: 'box_seq' },
    { label: '箱内数量', value: 'total_qty' },
  ],
  PALLET_CODE: [
    { label: '托盘码', value: 'pallet_code' },
    { label: '仓库', value: 'warehouse_name' },
    { label: '托盘总量', value: 'total_qty' },
    { label: '客户', value: 'customer_name' },
  ],
  INBOUND_ORDER: [
    { label: '单据号', value: 'order_no' },
    { label: '供应商', value: 'supplier_name' },
    { label: '仓库', value: 'warehouse_name' },
    { label: '入库时间', value: 'inbound_at' },
    { label: '操作人', value: 'operator' },
  ],
  OUTBOUND_ORDER: [
    { label: '单据号', value: 'order_no' },
    { label: '客户', value: 'customer_name' },
    { label: '仓库', value: 'warehouse_name' },
    { label: '出库时间', value: 'outbound_at' },
    { label: '操作人', value: 'operator' },
  ],
  PICK_ORDER: [
    { label: '单据号', value: 'order_no' },
    { label: '拣货人', value: 'picker_name' },
    { label: '仓库', value: 'warehouse_name' },
    { label: '拣货时间', value: 'pick_at' },
  ],
  COUNT_ORDER: [
    { label: '单据号', value: 'order_no' },
    { label: '仓库', value: 'warehouse_name' },
    { label: '盘点人', value: 'counter_name' },
    { label: '盘点时间', value: 'count_at' },
  ],
  SHIPMENT_ORDER: [
    { label: '单据号', value: 'order_no' },
    { label: '客户', value: 'customer_name' },
    { label: '承运商', value: 'carrier_name' },
    { label: '发货时间', value: 'shipment_at' },
    { label: '发货人', value: 'operator' },
  ],
}

/** 单据明细表列预设（渲染层按 lines 实际键排序取用；文案唯一来源） */
export const PRINT_LINE_FIELD_LABELS: Record<string, string> = {
  line_no: '序号',
  bin_code: '库位',
  sku_code: 'SKU编码',
  product_name: '商品名称',
  spec: '规格',
  unit: '单位',
  batch_no: '批次',
  qty: '数量',
  actual_qty: '实发/实盘数',
}

export function resolveLineFieldLabel(key: string): string {
  return PRINT_LINE_FIELD_LABELS[key] ?? key
}

/** 单据明细列展示顺序：预设键优先，其余键按原始顺序殿后 */
export const PRINT_LINE_FIELD_ORDER = Object.keys(PRINT_LINE_FIELD_LABELS)

// ---------- 打印模板（printing.md §2：可新增、编辑、复制、停用） ----------

export type PrintTemplateStatus = 'ENABLED' | 'DISABLED'

export interface PrintTemplateQuery extends PageQuery {
  keyword?: string
  objectType?: string
  status?: string
}

export interface PrintTemplateItem {
  id: PrintId
  name: string
  objectType: PrintObjectType
  /** PRINT_PAPERS 键（A4/A5/热敏规格） */
  paper: string
  status: PrintTemplateStatus
  /** 标签类条码码制（printing.md §4.1；单据类为单据二维码，无需码制） */
  barcodeSymbology?: BarcodeSymbology
  /** 是否附加二维码（库位二维码/单据二维码，printing.md §4.2） */
  qrcodeEnabled?: boolean
  /** 已绑定字段（按预设键过滤后的子集） */
  fields?: PrintTemplateField[]
  /** 单据页眉文本（公司名/单据名；缺省渲染层用模板名兜底） */
  headerText?: string
  remark?: string
  createdAt?: string
  updatedAt?: string
}

/** 新建/修改入参（创建恒 ENABLED，启停走专用接口 /status） */
export interface PrintTemplateSavePayload {
  name: string
  objectType: PrintObjectType
  paper: string
  barcodeSymbology?: BarcodeSymbology
  qrcodeEnabled?: boolean
  fields?: PrintTemplateField[]
  headerText?: string
  remark?: string
}

/** 创建任务时冻结的模板快照（预览按快照渲染，模板后续修改不影响已创建任务） */
export interface PrintTemplateSnapshot {
  name: string
  objectType: PrintObjectType
  paper: string
  barcodeSymbology?: BarcodeSymbology
  qrcodeEnabled?: boolean
  fields?: PrintTemplateField[]
  headerText?: string
}

// ---------- 打印任务（printing.md §1.2：创建任务 → 选择模板 → 预览 → 执行 → 记录日志） ----------

export type PrintTaskStatus = 'QUEUED' | 'PROCESSING' | 'SUCCESS' | 'FAILED'

/** 任务状态文案与语义色（types/status.ts 注册表未含打印状态，经 SfStatusTag 显式指定，frontend.md §24） */
export const PRINT_TASK_STATUS_META: Record<PrintTaskStatus, { label: string; semantic: StatusSemantic }> = {
  QUEUED: { label: '排队中', semantic: 'pending' },
  PROCESSING: { label: '处理中', semantic: 'processing' },
  SUCCESS: { label: '成功', semantic: 'success' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 打印结果文案与语义色（printing.md §1.2 历史记录的打印结果） */
export const PRINT_RESULT_META: Record<'SUCCESS' | 'FAILED', { label: string; semantic: StatusSemantic }> = {
  SUCCESS: { label: '成功', semantic: 'success' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 任务是否在途（进入终态后可预览/下载 PDF） */
export function isPrintTaskFinished(status?: string | null): boolean {
  return status === 'SUCCESS' || status === 'FAILED'
}

/** 任务列表筛选；id 供预览页按任务 ID 精确取单条（后端冻结后可回对为详情端点） */
export interface PrintTaskQuery extends PageQuery {
  objectType?: string
  status?: string
  id?: string
}

/** 任务内容行（后端按模板绑定字段装配的真实业务数据快照；预览页据此渲染，前端不造数据） */
export interface PrintContentRow {
  /** 行业务 ID */
  id: string
  /** 主码内容：SKU 条码 / 库位编码 / 箱码 / 托盘码 / 单据号 */
  code: string
  /** 模板字段绑定取值（键对齐 PRINT_TEMPLATE_FIELD_PRESETS，值由后端按业务数据填充） */
  values?: Record<string, string>
  /** 单据类明细行（键对齐 PRINT_LINE_FIELD_LABELS；仅单据模板返回） */
  lines?: Array<Record<string, string>>
}

export interface PrintTaskItem {
  id: string
  objectType: PrintObjectType
  templateId: PrintId
  templateName?: string
  /** 模板快照（列表 omitempty，仅预览/详情场景返回） */
  template?: PrintTemplateSnapshot
  paper: string
  status: PrintTaskStatus
  /** 内容行数（数据量，printing.md §1.2） */
  totalCount: number
  copies?: number
  /** 内容行（列表 omitempty，仅预览/详情场景返回） */
  rows?: PrintContentRow[]
  /** 产物下载地址（后端下发时优先于契约端点 /api/prints/:id/pdf） */
  pdfUrl?: string
  createdBy?: string
  createdAt?: string
  finishedAt?: string
  errorMessage?: string
}

/** 创建打印任务入参：templateId 为 *int64 提交 number；dataIds 为打印对象业务 ID（每条生成一行内容） */
export interface PrintTaskCreatePayload {
  templateId: number
  dataIds: string[]
  copies?: number
}

// ---------- 打印历史（printing.md §1.2：打印人/打印时间/模板/数据量/打印结果；后端落库，前端只读） ----------

export interface PrintHistoryQuery extends PageQuery {
  objectType?: string
  result?: string
  keyword?: string
}

export interface PrintHistoryItem {
  id: string
  taskId?: string
  objectType: PrintObjectType
  templateId?: PrintId
  templateName?: string
  printedBy?: string
  printedAt?: string
  totalCount?: number
  result: 'SUCCESS' | 'FAILED'
  errorMessage?: string
}

// ---------- API ----------

/** 模板/任务下拉一次取全的页大小（量级有限；失败时调用方降级为空，不阻塞表单） */
export const PRINT_OPTIONS_PAGE_SIZE = 200

export const printingApi = {
  templates: {
    list: (query: PrintTemplateQuery) =>
      http.get<PageResult<PrintTemplateItem>>('/api/prints/templates', { params: query }),
    create: (payload: PrintTemplateSavePayload) => http.post<PrintTemplateItem>('/api/prints/templates', payload),
    update: (id: PrintId, payload: PrintTemplateSavePayload) =>
      http.put<unknown>(`/api/prints/templates/${id}`, payload),
    /** POST /api/prints/templates/:id/copy：后端生成副本（名称追加副本标识），前端刷新列表 */
    copy: (id: PrintId) => http.post<PrintTemplateItem>(`/api/prints/templates/${id}/copy`),
    /** PUT /api/prints/templates/:id/status：启用/停用（printing.md §2，停用即不可再被任务选用） */
    setStatus: (id: PrintId, payload: { status: PrintTemplateStatus }) =>
      http.put<{ status: PrintTemplateStatus }>(`/api/prints/templates/${id}/status`, payload),
  },
  tasks: {
    list: (query: PrintTaskQuery) => http.get<PageResult<PrintTaskItem>>('/api/prints/tasks', { params: query }),
    create: (payload: PrintTaskCreatePayload) => http.post<PrintTaskItem>('/api/prints/tasks', payload),
  },
  history: {
    list: (query: PrintHistoryQuery) => http.get<PageResult<PrintHistoryItem>>('/api/prints/history', { params: query }),
  },
}

/**
 * 任务 PDF 下载：优先任务上后端下发的 pdfUrl（文件中心地址），缺省回落契约端点
 * GET /api/prints/:id/pdf；统一走认证 Blob 下载，失败呈可读错误态，前端绝不生成假 PDF。
 */
export async function downloadPrintPdf(task: Pick<PrintTaskItem, 'id' | 'pdfUrl'>): Promise<void> {
  await downloadFile(task.pdfUrl ?? `/api/prints/${task.id}/pdf`, `打印任务_${task.id}.pdf`)
}
