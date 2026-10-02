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

// ---------- 出库作业任务（拣货 → 复核 → 打包 → 发货，business-flow.md §7.2/§8） ----------
// 端点为前端先行骨架：后端 outbound 域未交付，页面呈统一错误态（requirements.md §10）。
// 字段名对齐 business-flow.md §8.2–8.5 作业内容与 database.md §表清单
// （pick_tasks / check_tasks / packing_records / shipments），最终以后端 Go JSON tag 为准，交付时需回对。

/** 拣货任务状态（business-flow.md §8.2；后端枚举冻结前仅用于筛选传参，展示走 SfStatusTag 兜底） */
export type PickingTaskStatus = 'pending_pick' | 'picking' | 'picked'

/** 复核任务状态（business-flow.md §8.3） */
export type CheckingTaskStatus = 'pending_check' | 'checking' | 'checked'

/** 打包记录状态（business-flow.md §8.4） */
export type PackingTaskStatus = 'pending_pack' | 'packing' | 'packed'

/** 发货状态（business-flow.md §8.5：待发货 → 已发货 → 运输中 → 已签收，含异常分支） */
export type ShipmentStatus =
  | 'pending_shipment'
  | 'shipped'
  | 'in_transit'
  | 'signed'
  | 'shipment_exception'

/** 出库作业列表筛选公共参数（参数名为前端先行定义，待后端契约对齐） */
interface OutboundTaskQuery extends PageQuery {
  keyword?: string
  warehouseCode?: string
}

export interface PickingTaskQuery extends OutboundTaskQuery {
  status?: PickingTaskStatus
}

/** 拣货任务（business-flow.md §8.2：拣货单 → SKU → 来源库位 → 数量 → 操作人 → 完成时间） */
export interface PickingTaskItem {
  id: number | string
  /** 拣货任务号 */
  pickTaskNo: string
  /** 关联出库单号 */
  outboundNo: string
  skuCode: string
  skuName?: string
  /** 来源库位 */
  fromBinCode?: string
  /** 批次（拣货支持换批次处理，business-flow.md §8.2） */
  batchNo?: string
  /** 应拣数量 */
  totalQty: number
  /** 已拣数量 */
  pickedQty: number
  status: string
  operatorName?: string
  /** 完成时间 */
  completedAt?: string
  createdAt: string
}

export interface CheckingTaskQuery extends OutboundTaskQuery {
  status?: CheckingTaskStatus
}

/** 复核任务（business-flow.md §8.3：重新确认 SKU / 条码 / 数量 / 批次 / 序列号 / 订单） */
export interface CheckingTaskItem {
  id: number | string
  /** 复核任务号 */
  checkTaskNo: string
  /** 关联出库单号 */
  outboundNo: string
  skuCode: string
  skuName?: string
  batchNo?: string
  /** 复核数量 */
  totalQty: number
  status: string
  /** 复核人 */
  operatorName?: string
  /** 复核完成时间 */
  completedAt?: string
  createdAt: string
}

export interface PackingTaskQuery extends OutboundTaskQuery {
  status?: PackingTaskStatus
}

/** 打包记录（business-flow.md §8.4 记录字段；一个订单允许多个包裹） */
export interface PackingTaskItem {
  id: number | string
  /** 包裹编号 */
  packageNo: string
  /** 关联出库单号 */
  outboundNo: string
  /** 包装材料 */
  packMaterial?: string
  /** 重量 */
  weight?: number
  /** 体积 */
  volume?: number
  /** 快递公司 */
  carrierName?: string
  /** 快递单号 */
  trackingNo?: string
  status: string
  /** 打包人 */
  operatorName?: string
  /** 打包时间 */
  packedAt?: string
  createdAt: string
}

export interface ShipmentQuery extends OutboundTaskQuery {
  status?: ShipmentStatus
}

/** 发货单（business-flow.md §8.5 发货信息字段全量；发货完成是库存正式扣减的触发点） */
export interface ShipmentItem {
  id: number | string
  /** 发货单号 */
  shipmentNo: string
  /** 关联出库单号 */
  outboundNo: string
  /** 物流公司 */
  carrierName?: string
  /** 物流单号 */
  trackingNo?: string
  /** 发货仓 */
  warehouseName?: string
  /** 包裹数量 */
  packageCount: number
  /** 发货人 */
  shipperName?: string
  /** 发货时间 */
  shippedAt?: string
  status: string
  createdAt: string
}

// ---------- 出库单详情（GET /api/outbounds/{id}，前端先行契约：后端 outbound 单据域未交付，冻结后回对字段） ----------

/** 出库单商品明细行 */
export interface OutboundDetailItem {
  id: number | string
  skuCode: string
  skuName?: string
  /** 计量单位 */
  unitName?: string
  /** 需求数量 */
  totalQty: number
  /** 已拣数量 */
  pickedQty?: number
  /** 来源库位 */
  fromBinCode?: string
  /** 批次 */
  batchNo?: string
  status?: string
  remark?: string
}

/** 出库单详情：含明细与流程节点时间（business-flow.md §7.2/§8、§13.4） */
export interface OutboundDetail {
  id: number | string
  outboundNo: string
  outboundType: string
  /** 来源单号（销售订单号等） */
  sourceNo?: string
  customerName?: string
  warehouseCode?: string
  warehouseName: string
  /** 需求数量合计 */
  totalQty: number
  /** 已拣数量合计 */
  pickedQty?: number
  status: string
  /** 创建人 */
  operatorName?: string
  /** 创建时间 */
  createdAt: string
  /** 库存分配时间 */
  allocatedAt?: string
  /** 分配人 */
  allocatedBy?: string
  /** 拣货完成时间 */
  pickedAt?: string
  /** 拣货人 */
  pickedBy?: string
  /** 复核完成时间 */
  checkedAt?: string
  /** 复核人 */
  checkedBy?: string
  /** 打包完成时间 */
  packedAt?: string
  /** 打包人 */
  packedBy?: string
  /** 发货时间（发货完成触发库存正式扣减，business-flow.md §7.2） */
  shippedAt?: string
  /** 发货人 */
  shippedBy?: string
  /** 物流公司（business-flow.md §8.5 发货信息） */
  carrierName?: string
  /** 物流单号 */
  trackingNo?: string
  /** 备注 */
  remark?: string
  /** 商品明细 */
  items: OutboundDetailItem[]
}

export const outboundApi = {
  /** 出库单列表（预置端点，后端未就绪时页面呈现统一错误态） */
  list: (query: OutboundQuery) =>
    http.get<PageResult<OutboundItem>>('/api/outbounds', { params: query }),
  /** 出库单详情（前端先行契约，后端未交付时页面呈现统一错误态） */
  get: (id: OutboundItem['id']) =>
    http.get<OutboundDetail>(`/api/outbounds/${id}`),
  /** 拣货任务列表（前端先行骨架，后端未交付时页面呈现统一错误态） */
  pickingTasks: (query: PickingTaskQuery) =>
    http.get<PageResult<PickingTaskItem>>('/api/outbound/picking-tasks', { params: query }),
  /** 复核任务列表（前端先行骨架，后端未交付时页面呈现统一错误态） */
  checkingTasks: (query: CheckingTaskQuery) =>
    http.get<PageResult<CheckingTaskItem>>('/api/outbound/checking-tasks', { params: query }),
  /** 打包记录列表（前端先行骨架，后端未交付时页面呈现统一错误态） */
  packingTasks: (query: PackingTaskQuery) =>
    http.get<PageResult<PackingTaskItem>>('/api/outbound/packing-tasks', { params: query }),
  /** 发货单列表（前端先行骨架，后端未交付时页面呈现统一错误态） */
  shipments: (query: ShipmentQuery) =>
    http.get<PageResult<ShipmentItem>>('/api/outbound/shipments', { params: query }),
}
