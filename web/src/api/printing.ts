import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 打印中心（后端 M3 已交付：internal/printing/handler.go:127-145，
// 出参为 TemplateView/TaskView/HistoryItem snake_case，internal/printing/service.go:119-250） ----------
//
// 端点清单（handler.go 路由集全量）：
//   GET  /api/prints/templates             模板列表
//   GET  /api/prints/templates/{id}        模板详情
//   POST /api/prints/templates             新建模板
//   PUT  /api/prints/templates/{id}        修改模板
//   POST /api/prints/templates/{id}/copy   复制模板
//   PUT  /api/prints/templates/{id}/status 启用/停用模板
//   GET  /api/prints/tasks                 打印任务列表
//   GET  /api/prints/tasks/{id}            任务详情（携带模板快照与渲染数据包）
//   POST /api/prints/tasks                 创建打印任务
//   POST /api/prints/tasks/{id}/execute    执行确认（{result: SUCCESS|FAILED, message?}）
//   GET  /api/prints/history               打印历史（print_tasks 已确认子集）
//   GET  /api/prints/barcode?text=&symbology=&width=&height=  条码/二维码 PNG（Blob）
// 后端不产出任务 PDF（无 /api/prints/{id}/pdf 端点）——单据打印走浏览器打印层。

/** 视图出参 ID（后端 database.ID 序列化为字符串，保留 number 兼容） */
export type PrintId = number | string

// ---------- 业务类型（printing.md §1.1 支持对象中 F12 首批 9 类，frontend.md §13 模板页列；
// 与 internal/printing objectTypes 冻结清单一致） ----------

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

// ---------- 条码/二维码（printing.md §4.1：Code128 默认推荐，码制与扫码端 scanner.md §5.1 一致；
// 与 internal/printing models.go:92-94 templateSymbologies 值域同源） ----------

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

// ---------- 模板字段绑定（printing.md §2：可配置的数据字段绑定；
// 键与文案和 internal/printing fields.go 预设注册表逐键同源冻结） ----------

/** 模板绑定字段读视图（FieldView = FieldPreset，fields.go:22-25） */
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

/** 模板状态（models.go:101-102 chk_print_templates_status 同源） */
export type PrintTemplateStatus = 'ENABLED' | 'DISABLED'

export interface PrintTemplateQuery extends PageQuery {
  keyword?: string
  objectType?: string
  status?: string
}

/** 模板（TemplateView，service.go:120-134 字段全量；fields 为预设键序的 {key,label} 列表） */
export interface PrintTemplateItem {
  id: PrintId
  name: string
  object_type: PrintObjectType
  /** PRINT_PAPERS 键（A4/A5/热敏规格） */
  paper: string
  /** 标签类条码码制（printing.md §4.1；单据类为单据二维码，无需码制） */
  barcode_symbology?: BarcodeSymbology
  /** 是否附加二维码（库位二维码/单据二维码，printing.md §4.2） */
  qrcode_enabled: boolean
  /** 已绑定字段（预设键序） */
  fields: PrintTemplateField[]
  /** 单据页眉文本（公司名/单据名；缺省渲染层用模板名兜底） */
  header_text: string
  status: PrintTemplateStatus
  remark: string
  created_at: string
  updated_at: string
  created_by: string
}

/** 新建/修改入参（TemplateInput，service_template.go:19-28；fields 为绑定键列表，
 * 后端按预设注册表回填文案——不信前端传入文案，models.go:172-173） */
export interface PrintTemplateSavePayload {
  name: string
  object_type: PrintObjectType
  paper: string
  barcode_symbology?: BarcodeSymbology
  qrcode_enabled?: boolean
  fields?: string[]
  header_text?: string
  remark?: string
}

/** 创建任务时冻结的模板快照（TemplateSnapshot，models.go:224-232；预览按快照渲染，
 * 模板后续修改不影响已创建任务） */
export interface PrintTemplateSnapshot {
  name: string
  object_type: PrintObjectType
  paper: string
  barcode_symbology?: BarcodeSymbology
  qrcode_enabled: boolean
  fields?: Record<string, string>
  header_text?: string
}

// ---------- 打印任务（printing.md §1.2：创建任务 → 预览渲染数据包 → 执行确认 → 历史记录） ----------

/** 任务状态（models.go:104-107 chk_print_tasks_status：render 队列态） */
export type PrintTaskStatus = 'QUEUED' | 'PROCESSING' | 'SUCCESS' | 'FAILED'

/** 任务状态文案与语义色（types/status.ts 注册表未含打印状态，经 SfStatusTag 显式指定，frontend.md §24） */
export const PRINT_TASK_STATUS_META: Record<PrintTaskStatus, { label: string; semantic: StatusSemantic }> = {
  QUEUED: { label: '排队中', semantic: 'pending' },
  PROCESSING: { label: '处理中', semantic: 'processing' },
  SUCCESS: { label: '成功', semantic: 'success' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 执行确认结果（models.go:112-114 chk_print_tasks_result；NULL=尚未确认，回填一次） */
export type PrintExecuteResult = 'SUCCESS' | 'FAILED'

/** 执行确认入参（ExecuteInput，service_task.go:33-37） */
export interface PrintTaskExecutePayload {
  result: PrintExecuteResult
  message?: string
}

/** 打印结果文案与语义色（printing.md §1.2 历史记录的打印结果） */
export const PRINT_RESULT_META: Record<PrintExecuteResult, { label: string; semantic: StatusSemantic }> = {
  SUCCESS: { label: '成功', semantic: 'success' },
  FAILED: { label: '失败', semantic: 'danger' },
}

/** 任务是否在途（终态后才可执行确认） */
export function isPrintTaskFinished(status?: string | null): boolean {
  return status === 'SUCCESS' || status === 'FAILED'
}

/** 任务列表筛选 */
export interface PrintTaskQuery extends PageQuery {
  objectType?: string
  status?: string
}

/** 任务内容行（RowView，service.go:160-166：主码内容 + 字段绑定取值 + 单据明细行） */
export interface PrintContentRow {
  /** 行序 */
  seq: number
  /** 行身份 ID（以主码内容承载，print_task_rows 无业务 ID 列） */
  id: string
  /** 主码内容：SKU 条码 / 库位编码 / 箱码 / 托盘码 / 单据号 */
  code: string
  /** 模板字段绑定取值（键对齐 PRINT_TEMPLATE_FIELD_PRESETS，值由后端按业务数据填充） */
  values?: Record<string, string>
  /** 单据类明细行（键对齐 PRINT_LINE_FIELD_LABELS；仅单据模板返回） */
  lines?: Array<Record<string, string>>
}

/** 打印任务（TaskView，service.go:181-200 字段全量；template/rows 仅详情携带） */
export interface PrintTaskItem {
  id: string
  /** 任务单号（PT- 前缀，docnum 冻结规则，models.go:118） */
  print_no: string
  object_type: PrintObjectType
  template_id: string
  /** 快照派生（任务创建时冻结） */
  template_name?: string
  paper: string
  copies: number
  /** 内容行数（数据量，printing.md §1.2） */
  total_count: number
  status: PrintTaskStatus
  /** 执行确认结果（未确认时省略） */
  result?: PrintExecuteResult
  printed_by?: string
  printed_at?: string
  error_message?: string
  created_by: string
  created_at: string
  /** 模板快照（仅详情返回） */
  template?: PrintTemplateSnapshot
  /** 内容行（仅详情返回） */
  rows?: PrintContentRow[]
}

/** 创建打印任务入参（TaskCreateInput，service_task.go:27-31；dataIds 为打印对象业务 ID，
 * 每条生成一行内容） */
export interface PrintTaskCreatePayload {
  template_id: number
  data_ids: string[]
  copies?: number
}

// ---------- 打印历史（printing.md §1.2：打印人/打印时间/模板/数据量/打印结果；
// = print_tasks 已确认子集，HistoryItem，service.go:238-250） ----------

export interface PrintHistoryQuery extends PageQuery {
  objectType?: string
  result?: string
  keyword?: string
}

export interface PrintHistoryItem {
  id: string
  print_no: string
  object_type: PrintObjectType
  template_id?: string
  template_name?: string
  printed_by: string
  printed_at: string
  total_count: number
  result: PrintExecuteResult
  error_message?: string
}

// ---------- API ----------

/** 模板/任务下拉一次取全的页大小（上限对齐后端 MaxPageSize=100，response.go:47；失败时调用方降级为空，不阻塞表单） */
export const PRINT_OPTIONS_PAGE_SIZE = 100

/** 条码/二维码 PNG 请求（GET /api/prints/barcode，handler.go:366-392：
 * text 必填、symbology 缺省 CODE128、width/height 可选整型；返回 PNG Blob） */
export function fetchBarcodePng(params: {
  text: string
  symbology?: BarcodeSymbology
  width?: number
  height?: number
}): Promise<Blob> {
  return http.get<Blob>('/api/prints/barcode', {
    params,
    responseType: 'blob',
    timeout: 30_000,
  })
}

export const printingApi = {
  templates: {
    list: (query: PrintTemplateQuery) =>
      http.get<PageResult<PrintTemplateItem>>('/api/prints/templates', { params: query }),
    detail: (id: PrintId) => http.get<PrintTemplateItem>(`/api/prints/templates/${id}`),
    create: (payload: PrintTemplateSavePayload) => http.post<PrintTemplateItem>('/api/prints/templates', payload),
    update: (id: PrintId, payload: PrintTemplateSavePayload) =>
      http.put<PrintTemplateItem>(`/api/prints/templates/${id}`, payload),
    /** POST /api/prints/templates/{id}/copy：后端生成副本（printing:template:create），前端刷新列表 */
    copy: (id: PrintId) => http.post<PrintTemplateItem>(`/api/prints/templates/${id}/copy`),
    /** PUT /api/prints/templates/{id}/status：启用/停用（printing.md §2，停用即不可再被任务选用），返回更新后模板 */
    setStatus: (id: PrintId, payload: { status: PrintTemplateStatus }) =>
      http.put<PrintTemplateItem>(`/api/prints/templates/${id}/status`, payload),
  },
  tasks: {
    list: (query: PrintTaskQuery) => http.get<PageResult<PrintTaskItem>>('/api/prints/tasks', { params: query }),
    /** 任务详情（GET /api/prints/tasks/{id}，携带模板快照与渲染数据包，预览页据此渲染） */
    detail: (id: string) => http.get<PrintTaskItem>(`/api/prints/tasks/${id}`),
    create: (payload: PrintTaskCreatePayload) => http.post<PrintTaskItem>('/api/prints/tasks', payload),
    /** 执行确认（POST /api/prints/tasks/{id}/execute，{result, message?}，结果回填一次） */
    execute: (id: string, payload: PrintTaskExecutePayload) =>
      http.post<PrintTaskItem>(`/api/prints/tasks/${id}/execute`, payload),
  },
  history: {
    list: (query: PrintHistoryQuery) => http.get<PageResult<PrintHistoryItem>>('/api/prints/history', { params: query }),
  },
}
