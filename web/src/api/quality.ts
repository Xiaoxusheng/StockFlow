import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 质量中心（docs/api.md §1：/api/quality；业务值域 business-flow.md §4、§13.1） ----------
// 前端先行契约：M1 后端未交付质量域（backend-m1-plan.md §13），字段以 business-flow.md
// §4.1–§4.3 为准；后端冻结接口后回对字段与值域。就绪前页面呈统一错误态
// （requirements.md §10），禁止 mock。

/** 检验方式（business-flow.md §4.1：免检、抽检、全检） */
export type InspectionMethod = 'NONE' | 'SAMPLING' | 'FULL'

/** 检验方式文案（未知值回退展示原始值） */
export const INSPECTION_METHOD_LABEL: Record<InspectionMethod, string> = {
  NONE: '免检',
  SAMPLING: '抽检',
  FULL: '全检',
}

/** 检验结果（key 对齐 types/status.ts 注册表：qualified/partially_qualified/unqualified） */
export type QualityResult = 'qualified' | 'partially_qualified' | 'unqualified'

/** 处理结果九值（business-flow.md §4.3：合格、部分合格、不合格、退供应商、报废、返工、降级、转不良品仓、特批放行） */
export type QualityDisposition =
  | 'qualified'
  | 'partially_qualified'
  | 'unqualified'
  | 'return_supplier'
  | 'scrap'
  | 'rework'
  | 'downgrade'
  | 'to_defective_warehouse'
  | 'special_release'

/**
 * 处理结果 SfStatusTag 兜底映射：仅收录注册表（types/status.ts）没有的六种处置值；
 * qualified/partially_qualified/unqualified 命中注册表，无需 label/semantic。
 * 未知值不配置 → SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
export const DISPOSITION_TAG_FALLBACK: Partial<
  Record<QualityDisposition, { label: string; semantic: StatusSemantic }>
> = {
  return_supplier: { label: '退供应商', semantic: 'warning' },
  scrap: { label: '报废', semantic: 'danger' },
  rework: { label: '返工', semantic: 'processing' },
  downgrade: { label: '降级', semantic: 'warning' },
  to_defective_warehouse: { label: '转不良品仓', semantic: 'danger' },
  special_release: { label: '特批放行', semantic: 'success' },
}

/** 质检单列表筛选（参数名为前端先行定义，待后端契约对齐） */
export interface QualityInspectionQuery extends PageQuery {
  keyword?: string
  inspectionMethod?: InspectionMethod
  result?: QualityResult
  disposition?: QualityDisposition
}

/** 质检单记录（business-flow.md §4.2 字段全量；单号 QC- 前缀，§13.1） */
export interface QualityInspectionItem {
  id: number | string
  /** 质检单号（business-flow.md §13.1：QC-日期-流水） */
  inspectionNo: string
  /** 来源单据类型（如入库单/收货单，随入库域冻结回对） */
  sourceType?: string
  sourceNo?: string
  skuCode: string
  productName: string
  batchNo?: string
  inspectionMethod: InspectionMethod
  /** 检验数量 / 合格数量 / 不合格数量（business-flow.md §4.2） */
  inspectQty: number
  qualifiedQty: number
  unqualifiedQty: number
  result: QualityResult
  disposition?: QualityDisposition
  /** 不合格原因（business-flow.md §4.2） */
  reason?: string
  inspectorName?: string
  inspectedAt?: string
  createdAt: string
}

/** 不合格品列表筛选（处理结果/去向值域对齐 business-flow.md §4.3） */
export interface NonconformingQuery extends PageQuery {
  keyword?: string
  disposition?: QualityDisposition
  destination?: NonconformingDestination
}

/** 去向（business-flow.md §4.3 处置去向：退供应商/报废/返工/降级/不良品仓/特批放行；枚举前端先行，后端冻结后回对） */
export type NonconformingDestination =
  | 'supplier'
  | 'scrap_area'
  | 'rework_area'
  | 'downgrade_bin'
  | 'defective_warehouse'
  | 'released'

/** 去向文案（未知值回退展示原始值） */
export const DESTINATION_LABEL: Record<NonconformingDestination, string> = {
  supplier: '退供应商',
  scrap_area: '报废区',
  rework_area: '返工区',
  downgrade_bin: '降级库位',
  defective_warehouse: '不良品仓',
  released: '放行',
}

/** 不合格品记录：以关联质检单号（QC-）为业务锚点，不另设单号（§13.1 编号规则未定义独立前缀） */
export interface NonconformingItem {
  id: number | string
  /** 关联质检单号（business-flow.md §13.1：QC-日期-流水） */
  qcNo: string
  sourceType?: string
  sourceNo?: string
  skuCode: string
  productName: string
  batchNo?: string
  /** 不合格数量 */
  qty: number
  /** 不合格原因（business-flow.md §4.2） */
  reason?: string
  /** 处理结果（business-flow.md §4.3 六种处置值） */
  disposition?: QualityDisposition
  /** 去向（§4.3；转不良品仓必须产生库存变动与流水） */
  destination?: NonconformingDestination
  handlerName?: string
  handledAt?: string
  createdAt: string
}

/** 质量追溯筛选：SKU / 批次号 / 序列号 / 单据号 四维检索质量记录链 */
export interface QualityTraceQuery extends PageQuery {
  skuCode?: string
  batchNo?: string
  serialNo?: string
  bizNo?: string
}

/** 质量记录类型（值域前端先行，覆盖来料检验 / 库存抽检 / 退货质检 / 处置记录；后端冻结后回对） */
export type QualityRecordType = 'INCOMING' | 'STOCK' | 'RETURN' | 'DISPOSAL'

/** 质量记录类型文案（未知值回退展示原始值） */
export const RECORD_TYPE_LABEL: Record<QualityRecordType, string> = {
  INCOMING: '来料检验',
  STOCK: '库存抽检',
  RETURN: '退货质检',
  DISPOSAL: '处置记录',
}

/** 质量追溯记录链节点 */
export interface QualityTraceItem {
  id: number | string
  /** 记录时间 */
  occurredAt: string
  recordType: QualityRecordType
  /** 关联质检单号（QC-，可为空：处置记录直接挂来源单据） */
  qcNo?: string
  bizType?: string
  bizNo?: string
  skuCode: string
  productName: string
  batchNo?: string
  serialNo?: string
  inspectionMethod?: InspectionMethod
  result?: QualityResult
  disposition?: QualityDisposition
  inspectorName?: string
  remark?: string
}

export const qualityApi = {
  /** 质检单列表（GET /api/quality/inspections，前端先行契约） */
  inspections: (query: QualityInspectionQuery) =>
    http.get<PageResult<QualityInspectionItem>>('/api/quality/inspections', { params: query }),
  /** 不合格品列表（GET /api/quality/nonconforming，前端先行契约） */
  nonconforming: (query: NonconformingQuery) =>
    http.get<PageResult<NonconformingItem>>('/api/quality/nonconforming', { params: query }),
  /** 质量追溯（GET /api/quality/trace，前端先行契约） */
  trace: (query: QualityTraceQuery) =>
    http.get<PageResult<QualityTraceItem>>('/api/quality/trace', { params: query }),
}
