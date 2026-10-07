import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'
import type { SalesId } from './sales'

// ---------- 质检单（后端 M2 已交付：GET /api/quality，internal/purchase/purchase.go:64-68；
// 出参为 QualityOrder GORM 模型 snake_case，internal/purchase/models.go:273-292） ----------
//
// inspection_type / result 为中文值域（db/migrations/000007 chk_quality_orders_inspection_type
// / chk_quality_orders_result），渲染经 SfStatusTag 的 label + semantic 显式指定或兜底。
// 退货质检复用同一套实现（source_type=RETURN，QCCreator 由 returns 域调用）。

/** 检验方式（business-flow.md §4.1：免检、抽检、全检；迁移 chk 同源中文值域） */
export type InspectionMethod = '免检' | '抽检' | '全检'

/** 检验方式文案（值即文案，未知值回退展示原始值） */
export const INSPECTION_METHOD_LABEL: Record<InspectionMethod, string> = {
  免检: '免检',
  抽检: '抽检',
  全检: '全检',
}

/** 质检单状态（internal/purchase/models.go:36-38：PENDING→INSPECTING→COMPLETED，
 * COMPLETED 即处理结果落定并触发库存映射） */
export type QualityOrderStatus = 'PENDING' | 'INSPECTING' | 'COMPLETED'

/** 质检来源（models.go:52-53：INBOUND 入库质检 / RETURN 退货质检） */
export type QualitySourceType = 'INBOUND' | 'RETURN'

/**
 * 质检结果九值（business-flow.md §4.3；迁移 chk_quality_orders_result 同源中文值域——
 * 检验结论与处理结果共用一个值域字段 result）。
 */
export type QualityResult =
  | '合格'
  | '部分合格'
  | '不合格'
  | '退供应商'
  | '报废'
  | '返工'
  | '降级'
  | '转不良品仓'
  | '特批放行'

/**
 * 质检结果 → SfStatusTag 兜底映射（中文值域不与 types/status.ts 小写注册表相交，
 * 全部经 label + semantic 显式指定；未知值不配置 → SfStatusTag 兜底中性灰，不崩溃）。
 */
export const QUALITY_RESULT_TAG_META: Record<QualityResult, { label: string; semantic: StatusSemantic }> = {
  合格: { label: '合格', semantic: 'success' },
  部分合格: { label: '部分合格', semantic: 'warning' },
  不合格: { label: '不合格', semantic: 'danger' },
  退供应商: { label: '退供应商', semantic: 'warning' },
  报废: { label: '报废', semantic: 'danger' },
  返工: { label: '返工', semantic: 'processing' },
  降级: { label: '降级', semantic: 'warning' },
  转不良品仓: { label: '转不良品仓', semantic: 'danger' },
  特批放行: { label: '特批放行', semantic: 'success' },
}

/** 质检单列表筛选（handler.go:381-401：keyword/status/source_type/source_no/warehouse_id） */
export interface QualityInspectionQuery extends PageQuery {
  keyword?: string
  status?: QualityOrderStatus
  source_type?: QualitySourceType
  source_no?: string
  warehouse_id?: SalesId
}

/** 质检单（QualityOrder，models.go:273-292 字段全量；单号 QC- 前缀，business-flow.md §13.1） */
export interface QualityInspectionItem {
  id: SalesId
  /** 质检单号（QC-日期-流水） */
  qc_no: string
  source_type: QualitySourceType
  /** 来源单号（入库单号 / 退货单号） */
  source_no: string
  warehouse_id: number
  inspection_type: InspectionMethod
  status: QualityOrderStatus
  qty_inspected: number
  qty_qualified: number
  qty_defective: number
  /** 九类中文值域之一（§4.3） */
  result: string
  inspector_id: number
  inspector_name: string
  inspected_at: string | null
  /** 现场照片引用（jsonb 字符串数组；文件中心阶段承载） */
  image_refs: string[]
  remark: string
  created_at: string
  updated_at: string
  /** database.ID 序列化为字符串（BaseModel，database/model.go:122-128） */
  created_by: string
  updated_by: string
}

/** 质检明细（QualityItem，models.go:296-310；检验/合格/不合格数量逐行记录） */
export interface QualityInspectionLine {
  id: SalesId
  qc_id: number
  line_no: number
  sku_id: number
  batch_no: string
  qty_inspected: number
  qty_qualified: number
  qty_defective: number
  remark: string
  created_at: string
  updated_at: string
  created_by: string
  updated_by: string
}

/** 质检单详情（GET /api/quality/{id}，service_quality.go:457-460 返回 {order, items}） */
export interface QualityInspectionDetail {
  order: QualityInspectionItem
  items: QualityInspectionLine[]
}

// ---------- 质检执行入参（QCExecuteInput，internal/purchase/service_quality.go:48-62） ----------

/** 质检结果行（QCExecuteLine：合格 + 不合格 必须 = 该行检验数量） */
export interface QualityExecuteLineInput {
  line_no: number
  qty_qualified: number
  qty_defective: number
  remark?: string
}

export interface QualityExecutePayload {
  lines: QualityExecuteLineInput[]
  /** 九类中文值域之一 */
  result: QualityResult
  /** 现场照片引用（文件中心 FileItem.id 字符串列表；POST /api/files 上传后回填，
   * 随 execute 落质检单 image_refs——QCExecuteInput.ImageRefs，service_quality.go:52） */
  image_refs?: string[]
  remark?: string
}

// ---------- 质检单创建入参（QCCreateInput，internal/purchase/service_quality.go:37-45；
// POST /api/quality 已注册，internal/purchase/purchase.go:65，权限 purchase:quality:create） ----------

/** 创建明细计划行（QCLineInput：SKU + 批次 + 计划检验数量；同一 SKU 不得重复，
 * 数量不得超过该 SKU 未处理余量——后端按入库单收货/已处理量强校验） */
export interface QualityCreateLineInput {
  sku_id: number
  batch_no?: string
  qty_inspected: number
}

/** 创建入参（source_no=入库单号：入库单须 AWAITING_QC / AWAITING_PUTAWAY；M2 本域
 * source_type 固定 INBOUND，RETURN 退货质检由后端 returns 域经 QCCreator 窄接口生成） */
export interface QualityCreatePayload {
  source_no: string
  inspection_type: InspectionMethod
  lines: QualityCreateLineInput[]
  remark?: string
}

// ---------- 不合格品 / 质量追溯（后端 2026-10-04 已交付：GET /api/quality/trace、
// GET /api/quality/nonconforming，internal/purchase/purchase.go:67-68 挂载、
// service_quality_trace.go 实现——原「后端未立项呈统一错误态」披露废止）。
// 已知边界（internal/purchase/service_quality_trace.go 文件头「已知边界」同源，禁止假功能）：
//   - serial_no：质检链无序列号台账，trace 的 serial_no 检索后端显式 400（service_quality_trace.go:107-111），
//     响应亦无 serial_no 字段——前端不提供该检索维度与结果列；
//   - destination：quality_orders 无「处置去向」列，nonconforming 的 destination 检索后端
//     显式 400（service_quality_trace.go:145-149）、记录也不下发——前端不提供该筛选与该列。
// 字段名以后端 Go JSON tag（service_quality_trace.go:43-83）为准对齐。 ----------

/** 不合格品处理结果（六值，business-flow.md §4.3；types/status.ts 质检处置六键同源） */
export type QualityDisposition =
  | 'return_supplier'
  | 'scrap'
  | 'rework'
  | 'downgrade'
  | 'to_defective_warehouse'
  | 'special_release'

/**
 * 处理结果 → SfStatusTag 兜底映射（六处置值为英文键，label/semantic 与
 * types/status.ts 质检处置六键逐键一致；后端返回未知值时兜底中性灰 + 原始文案，不崩溃）。
 */
export const DISPOSITION_TAG_FALLBACK: Record<QualityDisposition, { label: string; semantic: StatusSemantic }> = {
  return_supplier: { label: '退供应商', semantic: 'warning' },
  scrap: { label: '报废', semantic: 'danger' },
  rework: { label: '返工', semantic: 'processing' },
  downgrade: { label: '降级', semantic: 'warning' },
  to_defective_warehouse: { label: '转不良品仓', semantic: 'danger' },
  special_release: { label: '特批放行', semantic: 'success' },
}

/** 不合格品列表筛选（keyword/disposition + 分页；destination 检索后端不支持传值 400，
 * 不提供——service_quality_trace.go:145-149） */
export interface NonconformingQuery extends PageQuery {
  keyword?: string
  disposition?: QualityDisposition
}

/**
 * 不合格品记录（无独立 NCR 单号，以关联质检单号 QC- 为锚点；字段对齐后端
 * NonconformingItem JSON tag，service_quality_trace.go:71-83）：
 * 后端不下发「不合格原因」（质检单 result 即处置承载）与「去向」（未建模列），
 * 前端不声明、列表不渲染（消费不存在字段恒空，禁止）。
 */
export interface NonconformingItem {
  id: SalesId
  /** 关联质检单号（QC-日期-流水） */
  qc_no: string
  /** 来源单号（入库单号 / 退货单号） */
  source_no?: string
  sku_code: string
  product_name: string
  batch_no?: string
  /** 不合格数量 */
  qty: number
  /** 处置六值英文键（检验三值不入表——无处置段，disposition 缺省） */
  disposition?: QualityDisposition
  /** 质检员（处置执行人） */
  handler_name?: string
  /** 处理时间 */
  handled_at?: string
  /** 记录时间 */
  created_at: string
}

/** 质量记录类型（质量记录链两段：检验 → 处置，frontend.md §9.1） */
export type QualityRecordType = 'inspection' | 'disposition'

/** 记录类型文案（未知值回退展示原始值） */
export const RECORD_TYPE_LABEL: Record<QualityRecordType, string> = {
  inspection: '检验记录',
  disposition: '处置记录',
}

/** 质量追溯筛选（三维检索：SKU / 批次号 / 单据号 + 分页；serial_no 检索后端不支持
 * 传值 400，不提供——service_quality_trace.go:107-111） */
export interface QualityTraceQuery extends PageQuery {
  sku_code?: string
  batch_no?: string
  biz_no?: string
}

/** 质量追溯记录（检验与处置两级链路逐条记录；字段对齐后端 QualityTraceItem JSON tag，
 * service_quality_trace.go:43-58——无 serial_no 字段，前端不声明） */
export interface QualityTraceItem {
  id: SalesId
  /** 记录时间 */
  occurred_at: string
  record_type: QualityRecordType
  /** 关联质检单号（QC-） */
  qc_no?: string
  /** 来源业务（入库 / 退货等） */
  biz_type?: string
  /** 来源单据号 */
  biz_no?: string
  sku_code: string
  product_name: string
  batch_no?: string
  /** 检验方式三值（§4.1，与质检单 inspection_type 同值域） */
  inspection_method?: InspectionMethod
  /**
   * 质检结果：九类中文值域之一（db/migrations/000007:248 chk_quality_orders_result，
   * 与质检单 result 同域——检验记录的 result 即其质检单处理结果），
   * 渲染经 QUALITY_RESULT_TAG_META 的 label/semantic；未知值 SfStatusTag 兜底中性灰。
   * 旧英文三键假设（qualified/partially_qualified/unqualified）与后端值域不符已移除。
   */
  result?: string
  disposition?: QualityDisposition
  inspector_name?: string
  remark?: string
}

// ---------- 权限码（internal/auth/permissions.go:164-167 三段式冻结） ----------

export const QUALITY_LIST_PERMISSION = 'purchase:quality:list'
export const QUALITY_READ_PERMISSION = 'purchase:quality:read'
export const QUALITY_CREATE_PERMISSION = 'purchase:quality:create'
export const QUALITY_EXECUTE_PERMISSION = 'purchase:quality:execute'

export const qualityApi = {
  /** 质检单列表（GET /api/quality） */
  inspections: (query: QualityInspectionQuery) =>
    http.get<PageResult<QualityInspectionItem>>('/api/quality', { params: query }),
  /** 手动创建质检单（POST /api/quality，purchase.go:65；QCCreateInput → QualityOrder，
   * 创建即 PENDING，待该入库单后续质检任务） */
  create: (payload: QualityCreatePayload) =>
    http.post<QualityInspectionItem>('/api/quality', payload),
  /** 质检单详情（GET /api/quality/{id}，{order, items}） */
  detail: (id: SalesId) => http.get<QualityInspectionDetail>(`/api/quality/${id}`),
  /** 开始质检（POST /api/quality/{id}/start，PENDING→INSPECTING） */
  start: (id: SalesId) => http.post<QualityInspectionItem>(`/api/quality/${id}/start`),
  /** 提交质检结果（POST /api/quality/{id}/execute，INSPECTING→COMPLETED + 库存映射） */
  execute: (id: SalesId, payload: QualityExecutePayload) =>
    http.post<QualityInspectionItem>(`/api/quality/${id}/execute`, payload),
  /** 不合格品列表（GET /api/quality/nonconforming，后端 2026-10-04 已交付；
   * destination 检索不支持——传值 400，前端不提供） */
  nonconforming: (query: NonconformingQuery) =>
    http.get<PageResult<NonconformingItem>>('/api/quality/nonconforming', { params: query }),
  /** 质量追溯（GET /api/quality/trace，后端 2026-10-04 已交付；
   * serial_no 检索不支持——传值 400，前端不提供） */
  trace: (query: QualityTraceQuery) =>
    http.get<PageResult<QualityTraceItem>>('/api/quality/trace', { params: query }),
}
