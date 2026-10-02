/**
 * 业务状态统一注册表（frontend.md §24：状态颜色全局统一，禁止页面自定义）
 * 状态值与后端对齐后以枚举为准；此处为前端先行定义的唯一映射。
 */

/** 语义色：正常/成功/处理中/待处理/警告/危险/禁用/异常 */
export type StatusSemantic =
  | 'success'
  | 'processing'
  | 'pending'
  | 'warning'
  | 'danger'
  | 'neutral'
  | 'disabled'

export interface StatusMeta {
  label: string
  semantic: StatusSemantic
}

export const STATUS_META: Record<string, StatusMeta> = {
  // 通用单据状态（business-flow.md §13.2）
  draft: { label: '草稿', semantic: 'neutral' },
  pending_review: { label: '待审核', semantic: 'pending' },
  approved: { label: '已审核', semantic: 'success' },
  rejected: { label: '已驳回', semantic: 'danger' },
  pending: { label: '待处理', semantic: 'pending' },
  processing: { label: '处理中', semantic: 'processing' },
  in_progress: { label: '进行中', semantic: 'processing' },
  completed: { label: '已完成', semantic: 'success' },
  closed: { label: '已关闭', semantic: 'neutral' },
  cancelled: { label: '已取消', semantic: 'neutral' },
  voided: { label: '已作废', semantic: 'neutral' },

  // 入库（business-flow.md §2–5）
  pending_receipt: { label: '待收货', semantic: 'pending' },
  receiving: { label: '收货中', semantic: 'processing' },
  received: { label: '已收货', semantic: 'success' },
  pending_inspection: { label: '待质检', semantic: 'pending' },
  inspecting: { label: '质检中', semantic: 'processing' },
  inspected: { label: '已质检', semantic: 'success' },
  pending_putaway: { label: '待上架', semantic: 'pending' },
  putaway_in_progress: { label: '上架中', semantic: 'processing' },
  putaway_completed: { label: '已上架', semantic: 'success' },

  // 出库（business-flow.md §6–8）
  pending_allocate: { label: '待分配', semantic: 'pending' },
  allocated: { label: '已分配', semantic: 'processing' },
  pending_pick: { label: '待拣货', semantic: 'pending' },
  picking: { label: '拣货中', semantic: 'processing' },
  picked: { label: '已拣货', semantic: 'success' },
  pending_check: { label: '待复核', semantic: 'pending' },
  checking: { label: '复核中', semantic: 'processing' },
  checked: { label: '已复核', semantic: 'success' },
  pending_pack: { label: '待打包', semantic: 'pending' },
  packing: { label: '打包中', semantic: 'processing' },
  packed: { label: '已打包', semantic: 'success' },
  pending_shipment: { label: '待发货', semantic: 'pending' },
  shipped: { label: '已发货', semantic: 'processing' },
  in_transit: { label: '运输中', semantic: 'processing' },
  signed: { label: '已签收', semantic: 'success' },
  shipment_exception: { label: '发货异常', semantic: 'danger' },

  // 库存（inventory-rules.md）
  normal: { label: '正常', semantic: 'success' },
  locked: { label: '锁定', semantic: 'warning' },
  frozen: { label: '冻结', semantic: 'danger' },
  low_stock: { label: '低库存', semantic: 'warning' },
  overstock: { label: '超储', semantic: 'warning' },
  near_expiry: { label: '临期', semantic: 'warning' },
  expired: { label: '过期', semantic: 'danger' },
  slow_moving: { label: '积压', semantic: 'warning' },

  // 库位状态（frontend.md §11）
  idle: { label: '空闲', semantic: 'neutral' },
  partially_occupied: { label: '部分占用', semantic: 'processing' },
  full: { label: '满载', semantic: 'success' },
  abnormal: { label: '异常', semantic: 'danger' },

  // 盘点（frontend.md §10.5）
  pending_execute: { label: '待执行', semantic: 'pending' },
  counting: { label: '盘点中', semantic: 'processing' },

  // 质检结果
  qualified: { label: '合格', semantic: 'success' },
  partially_qualified: { label: '部分合格', semantic: 'warning' },
  unqualified: { label: '不合格', semantic: 'danger' },

  // 设备与账号
  enabled: { label: '已启用', semantic: 'success' },
  disabled: { label: '已停用', semantic: 'neutral' },
  online: { label: '在线', semantic: 'success' },
  offline: { label: '离线', semantic: 'neutral' },
}

/** 未知状态兜底：中性灰展示原始文案，保证后端新增状态不阻塞页面 */
export function resolveStatus(key: string | undefined | null): StatusMeta | undefined {
  if (!key) return undefined
  return STATUS_META[key]
}
