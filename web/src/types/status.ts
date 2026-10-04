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
  // 通用单据状态（business-flow.md §13.2；后端大写枚举经 toStatusKey 归一后命中）
  draft: { label: '草稿', semantic: 'neutral' },
  pending_review: { label: '待审核', semantic: 'pending' },
  pending_approval: { label: '待审核', semantic: 'pending' },
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

  // 调拨单（business-flow.md §10.1 七态经 toStatusKey 归一：draft/pending_approval/approved/
  // transferring/awaiting_receipt/completed/cancelled——internal/stockops/models.go:134-142；
  // approved 复用上方通用键「已审核」，调拨语境「待出库」文案由 api/transfer.ts
  // TRANSFER_STATUS_TAG 以 label 覆盖；原 pending_outbound/pending_inbound 为前端虚构
  // 值域已移除，后端无此态）
  transferring: { label: '调拨中', semantic: 'processing' },
  awaiting_receipt: { label: '待入库', semantic: 'pending' },

  // 库存（inventory-rules.md）
  normal: { label: '正常', semantic: 'success' },
  locked: { label: '锁定', semantic: 'warning' },
  frozen: { label: '冻结', semantic: 'danger' },
  low_stock: { label: '低库存', semantic: 'warning' },
  overstock: { label: '超储', semantic: 'warning' },
  near_expiry: { label: '临期', semantic: 'warning' },
  expired: { label: '过期', semantic: 'danger' },
  slow_moving: { label: '积压', semantic: 'warning' },

  // 库存锁定（db/migrations/000005 inventory_locks CHECK 值域，与 LocksPage/StockDetailPage 映射一致）
  active: { label: '生效中', semantic: 'processing' },
  released: { label: '已释放', semantic: 'neutral' },
  consumed: { label: '已消耗', semantic: 'success' },

  // 序列号（db/migrations/000005 serial_numbers CHECK 值域；locked/frozen 复用上方库存组键）
  in_stock: { label: '在库', semantic: 'success' },
  outbound: { label: '已出库', semantic: 'neutral' },
  returned: { label: '已退货', semantic: 'neutral' },

  // 库存调整单（business-flow.md §13.2：DRAFT→PENDING_APPROVAL→APPROVED→EXECUTED，驳回/作废复用通用键）
  executed: { label: '已执行', semantic: 'success' },

  // 库位占用状态（契约：internal/warehouse/service_map.go:21-26 四值 IDLE/PARTIAL/FULL/LOCKED，
  // 后端大写枚举经 toStatusKey 小写后注册；LOCKED 复用上方库存锁定键 locked；
  // 冻结/异常为后端不产出的假值域（M1 不交付），已从库位占用域移除）
  idle: { label: '空闲', semantic: 'neutral' },
  partial: { label: '部分占用', semantic: 'processing' },
  full: { label: '满载', semantic: 'success' },

  // 盘点（后端五态 DRAFT/COUNTING/PENDING_REVIEW/COMPLETED/CANCELLED——internal/stockops/
  // models.go:143-147，单据态经大小写归一后复用通用键 draft/pending_review/completed/
  // cancelled；此处为明细行态；原 pending_execute 为前端虚构值域已移除）
  counting: { label: '盘点中', semantic: 'processing' },
  counted: { label: '已盘', semantic: 'success' },

  // 异常生命周期（business-flow.md §11.2：发现→创建→分派→处理中→待复核→已解决→已关闭；
  // processing/closed 复用通用键，与 api/exception.ts EXCEPTION_STATUS_TAG 对齐）
  discovered: { label: '发现', semantic: 'warning' },
  created: { label: '已创建', semantic: 'pending' },
  assigned: { label: '已分派', semantic: 'processing' },
  pending_recheck: { label: '待复核', semantic: 'pending' },
  resolved: { label: '已解决', semantic: 'success' },

  // 质检结果
  qualified: { label: '合格', semantic: 'success' },
  partially_qualified: { label: '部分合格', semantic: 'warning' },
  unqualified: { label: '不合格', semantic: 'danger' },

  // 质检处理结果（business-flow.md §4.3 处置值；后端质检单 result 为九类中文值域，
  // 见 api/quality.ts QUALITY_RESULT_TAG_META——下列英文键为旧前端先行值域保留兜底）
  return_supplier: { label: '退供应商', semantic: 'warning' },
  scrap: { label: '报废', semantic: 'danger' },
  rework: { label: '返工', semantic: 'processing' },
  downgrade: { label: '降级', semantic: 'warning' },
  to_defective_warehouse: { label: '转不良品仓', semantic: 'danger' },
  special_release: { label: '特批放行', semantic: 'success' },

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
