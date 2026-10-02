import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 盘点中心（前端先行契约：docs/api.md §1 仅冻结 /api/counts 前缀，字段待后端盘点域冻结后回对） ----------
// 业务依据：business-flow.md §10.2（范围/类型/状态机/差异必须走库存调整单 + 审批链路）、
// §13.1 单号规则（CK-日期-流水）、frontend.md §10.5（列表字段 / 详情四要素）。

/** ID 序列化为字符串是后端 M1 约定（backend-m1-plan §12），保留 number 兼容数值返回 */
export type CountId = number | string

/** 盘点范围（business-flow.md §10.2：全盘、按仓、按库区、按货架、按库位、按 SKU） */
export type CountScopeType = 'ALL' | 'WAREHOUSE' | 'ZONE' | 'SHELF' | 'BIN' | 'SKU'

/** 盘点类型（business-flow.md §10.2：周期盘点、动碰盘点、抽盘） */
export type CountType = 'CYCLE' | 'MOVEMENT' | 'SPOT'

/** 盘点单七态状态机（frontend.md §10.5：草稿/待执行/盘点中/待复核/待审核/已完成/已取消） */
export type CountStatus =
  | 'DRAFT'
  | 'PENDING_EXECUTE'
  | 'COUNTING'
  | 'PENDING_REVIEW'
  | 'PENDING_APPROVAL'
  | 'COMPLETED'
  | 'CANCELLED'

/** 明细行状态（前端先行：待盘 / 已盘，差异通过实盘数量对比呈现，后端冻结时对齐） */
export type CountItemStatus = 'PENDING' | 'COUNTED'

export const COUNT_SCOPE_TYPE_LABEL: Record<CountScopeType, string> = {
  ALL: '全盘',
  WAREHOUSE: '按仓',
  ZONE: '按库区',
  SHELF: '按货架',
  BIN: '按库位',
  SKU: '按 SKU',
}

export const COUNT_TYPE_LABEL: Record<CountType, string> = {
  CYCLE: '周期盘点',
  MOVEMENT: '动碰盘点',
  SPOT: '抽盘',
}

export interface CountQuery extends PageQuery {
  /** 关键词：盘点单号（CK- 前缀，business-flow.md §13.1） */
  keyword?: string
  warehouseCode?: string
  countType?: CountType
  scopeType?: CountScopeType
  status?: CountStatus
}

export interface CountTaskItem {
  id: CountId
  /** 盘点单号（CK-日期-流水，business-flow.md §13.1） */
  countNo: string
  warehouseCode?: string
  warehouseName: string
  scopeType: CountScopeType
  /** 范围明细（库区 / 货架 / 库位 / SKU 编码，全盘为空） */
  scopeValue?: string
  countType: CountType
  /** 负责人（前端先行字段名，后端冻结时对齐） */
  ownerName?: string
  status: CountStatus
  createdAt?: string
  startedAt?: string
  completedAt?: string
}

/** 盘点汇总（frontend.md §10.5：系统库存 / 实盘库存 / 差异 / 完成率） */
export interface CountSummary {
  /** 明细行数 */
  totalItems: number
  /** 已盘行数 */
  countedItems: number
  /** 系统库存合计 */
  systemQty: number
  /** 实盘库存合计 */
  countedQty: number
  /** 差异数合计（实盘 - 系统） */
  diffQty: number
  /** 完成率 0~100 */
  completionRate: number
}

export interface CountTaskDetail extends CountTaskItem {
  remark?: string
  summary: CountSummary
}

export interface CountItemQuery extends PageQuery {
  status?: CountItemStatus
  keyword?: string
}

export interface CountItem {
  id: CountId
  countId: CountId
  skuCode: string
  productName: string
  binCode?: string
  batchNo?: string
  /** 系统数量（冻结范围时的快照） */
  systemQty: number
  /** 实盘数量（未登记为空；差异在页面按 实盘-系统 计算，前端先行契约不重复下发 diffQty） */
  countedQty?: number
  status: CountItemStatus
  countedByName?: string
  countedAt?: string
}

/** 实盘登记（PUT /api/counts/{id}/items/{itemId}，前端先行契约）：
 * 差异原因 / 备注必填（devices.md §10.3 差异进入原因/备注/照片/提交流程）；
 * 不携带库存修改语义——差异调整由后端经库存调整单 + 审批链路落库（business-flow.md §10.2 硬性规则） */
export interface CountItemSavePayload {
  countedQty: number
  reason: string
  remark: string
}

export const countApi = {
  list: (query: CountQuery) => http.get<PageResult<CountTaskItem>>('/api/counts', { params: query }),
  detail: (id: CountId) => http.get<CountTaskDetail>(`/api/counts/${id}`),
  items: (id: CountId, query: CountItemQuery) =>
    http.get<PageResult<CountItem>>(`/api/counts/${id}/items`, { params: query }),
  /** 实盘登记（前端先行：后端盘点域未交付前呈统一错误提示） */
  registerItem: (id: CountId, itemId: CountId, payload: CountItemSavePayload) =>
    http.put<unknown>(`/api/counts/${id}/items/${itemId}`, payload),
}
