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
  image_refs?: string[]
  remark?: string
}

// ---------- 不合格品 / 质量追溯（前端先行契约，docs/changelog.md [2026-10-02] 集成记录冻结：
// GET /api/quality/nonconforming、/trace 两端点后端未交付，后端就绪前页面呈统一错误态
// （SfTable error 兜底），属预期行为，禁止 mock（requirements.md §10）；
// 字段名与枚举值在后端质量域落地时以后端 Go JSON tag 为准回对） ----------

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

/** 不合格品去向（六值，business-flow.md §4.3 处置后物流去向） */
export type NonconformingDestination =
  | 'supplier'
  | 'scrap_area'
  | 'rework_area'
  | 'downgrade_bin'
  | 'defective_warehouse'
  | 'released'

/** 去向文案（值即文案，未知值回退展示原始值） */
export const DESTINATION_LABEL: Record<NonconformingDestination, string> = {
  supplier: '退供应商',
  scrap_area: '报废区',
  rework_area: '返工区',
  downgrade_bin: '降级库位',
  defective_warehouse: '不良品仓',
  released: '放行',
}

/** 不合格品列表筛选（前端先行契约：keyword/disposition/destination + 分页） */
export interface NonconformingQuery extends PageQuery {
  keyword?: string
  disposition?: QualityDisposition
  destination?: NonconformingDestination
}

/** 不合格品记录（无独立 NCR 单号，以关联质检单号 QC- 为锚点，changelog 冻结回对清单） */
export interface NonconformingItem {
  id: SalesId
  /** 关联质检单号（QC-日期-流水） */
  qcNo: string
  /** 来源单号（入库单号 / 退货单号） */
  sourceNo?: string
  skuCode: string
  productName: string
  batchNo?: string
  /** 不合格数量 */
  qty: number
  /** 不合格原因 */
  reason?: string
  disposition?: QualityDisposition
  destination?: NonconformingDestination
  handlerName?: string
  /** 处理时间 */
  handledAt?: string
  /** 记录时间 */
  createdAt: string
}

/** 质量记录类型（质量记录链两段：检验 → 处置，frontend.md §9.1） */
export type QualityRecordType = 'inspection' | 'disposition'

/** 记录类型文案（未知值回退展示原始值） */
export const RECORD_TYPE_LABEL: Record<QualityRecordType, string> = {
  inspection: '检验记录',
  disposition: '处置记录',
}

/** 质量追溯筛选（四维检索：SKU / 批次号 / 序列号 / 单据号 + 分页） */
export interface QualityTraceQuery extends PageQuery {
  skuCode?: string
  batchNo?: string
  serialNo?: string
  bizNo?: string
}

/** 质量追溯记录（检验与处置两级链路逐条记录） */
export interface QualityTraceItem {
  id: SalesId
  /** 记录时间 */
  occurredAt: string
  recordType: QualityRecordType
  /** 关联质检单号（QC-） */
  qcNo?: string
  /** 来源业务（入库 / 退货等） */
  bizType?: string
  /** 来源单据号 */
  bizNo?: string
  skuCode: string
  productName: string
  batchNo?: string
  serialNo?: string
  /** 检验方式三值（§4.1，与质检单 inspection_type 同值域） */
  inspectionMethod?: InspectionMethod
  /** 质检结果（qualified/partially_qualified/unqualified 命中 types/status.ts 质检结果三键，未知值 SfStatusTag 兜底） */
  result?: string
  disposition?: QualityDisposition
  inspectorName?: string
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
  /** 质检单详情（GET /api/quality/{id}，{order, items}） */
  detail: (id: SalesId) => http.get<QualityInspectionDetail>(`/api/quality/${id}`),
  /** 开始质检（POST /api/quality/{id}/start，PENDING→INSPECTING） */
  start: (id: SalesId) => http.post<QualityInspectionItem>(`/api/quality/${id}/start`),
  /** 提交质检结果（POST /api/quality/{id}/execute，INSPECTING→COMPLETED + 库存映射） */
  execute: (id: SalesId, payload: QualityExecutePayload) =>
    http.post<QualityInspectionItem>(`/api/quality/${id}/execute`, payload),
  /** 不合格品列表（GET /api/quality/nonconforming，前端先行契约：后端未交付，统一错误态兜底） */
  nonconforming: (query: NonconformingQuery) =>
    http.get<PageResult<NonconformingItem>>('/api/quality/nonconforming', { params: query }),
  /** 质量追溯（GET /api/quality/trace，前端先行契约：后端未交付，统一错误态兜底） */
  trace: (query: QualityTraceQuery) =>
    http.get<PageResult<QualityTraceItem>>('/api/quality/trace', { params: query }),
}
