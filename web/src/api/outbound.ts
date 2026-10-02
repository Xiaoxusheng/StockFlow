import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 出库管理（docs/api.md §1：领域前缀 /api/outbounds，子路径未冻结） ----------

/** 出库类型（business-flow.md §7.1；后端枚举冻结前仅用于筛选传参，展示走页面标签映射兜底） */
export type OutboundType =
  | 'sales' // 销售出库
  | 'production' // 生产领料
  | 'transfer' // 调拨出库
  | 'other' // 其他出库
  | 'loss' // 报损出库

/** 出库单状态主流程（business-flow.md §7.2 分配 → 拣货 → 复核 → 打包 → 发货；后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type OutboundStatus =
  | 'pending_allocate'
  | 'allocated'
  | 'pending_pick'
  | 'picking'
  | 'picked'
  | 'pending_check'
  | 'pending_pack'
  | 'pending_shipment'
  | 'shipped'
  | 'shipment_exception'
  | 'completed'
  | 'closed'
  | 'cancelled'

export interface OutboundQuery extends PageQuery {
  keyword?: string
  outboundType?: OutboundType
  status?: OutboundStatus
  warehouseCode?: string
}

export interface OutboundItem {
  id: number | string
  /** 出库单号（business-flow.md §13.1：OUT-日期-流水） */
  outboundNo: string
  outboundType: string
  /** 来源单号（销售订单号等） */
  sourceNo?: string
  /** 客户（仅销售出库等有去向场景返回） */
  customerName?: string
  warehouseCode?: string
  warehouseName: string
  /** 需求数量合计 */
  totalQty: number
  /** 已拣货数量合计（发货完成时才正式扣减库存，business-flow.md §7.2） */
  pickedQty: number
  status: string
  operatorName?: string
  createdAt: string
}

export const outboundApi = {
  /** 出库单列表（预置端点，后端未就绪时页面呈现统一错误态） */
  list: (query: OutboundQuery) =>
    http.get<PageResult<OutboundItem>>('/api/outbounds', { params: query }),
}
