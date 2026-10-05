import type { OutboundOrderStatus } from '@/api/outbound'
import type { SalesOrderStatus, SalesReturnStatus } from '@/api/sales'
import type { StatusSemantic } from '@/types/status'

/**
 * 销售域状态 → SfStatusTag 映射（域内唯一来源：订单列表/详情/退货列表/退货抽屉/出库列表
 * 共用，禁止各页复制粘贴漂移）。
 *
 * 渲染约定（同采购域 PurchaseListPage、出库域 OutboundPage 范式）：后端大写枚举经
 * toStatusKey 归一后先命中 types/status.ts 注册表（draft/pending_approval/approved/
 * rejected/completed/cancelled/pending_allocate/allocated/picking/picked/checked/packed/
 * closed/receiving/shipped，label/semantic 与本表逐键一致，注册表优先渲染不变）；
 * PARTIAL_SHIPPED/SHIPPED_ALL/IN_QC 三专有键注册表已于 2026-10-05 收口补键
 * （partial_shipped/shipped_all/in_qc，与本表逐键同值，行为零变化），本表保留为
 * 域内唯一来源的类型完备映射；后端返回未知值时映射缺失，
 * SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 *
 * 值域出处（迁移 CHECK 同源）：销售订单 internal/sales/models.go:240-248（8 态）、
 * 出库单 internal/sales/models.go:265-276（10 态）、退货单 internal/returns/models.go:27-36
 * （8 态；SHIPPED 为采购退货出库完成态，销售退货流程不产出）。
 */

/** 销售订单状态（DRAFT→PENDING_APPROVAL→APPROVED→出库→COMPLETED；business-flow.md §6.2） */
export const SALES_ORDER_STATUS_TAG: Record<SalesOrderStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  REJECTED: { label: '已驳回', semantic: 'danger' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'processing' },
  SHIPPED_ALL: { label: '全部发货', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}

/** 出库单状态（分配 → 拣货 → 复核 → 打包 → 发货；/sales/outbounds 数据源为出库域） */
export const OUTBOUND_STATUS_TAG: Record<OutboundOrderStatus, { label: string; semantic: StatusSemantic }> = {
  PENDING_ALLOCATE: { label: '待分配', semantic: 'pending' },
  ALLOCATED: { label: '已分配', semantic: 'processing' },
  PICKING: { label: '拣货中', semantic: 'processing' },
  PICKED: { label: '已拣货', semantic: 'success' },
  CHECKED: { label: '已复核', semantic: 'success' },
  PACKED: { label: '已打包', semantic: 'success' },
  PARTIAL_SHIPPED: { label: '部分发货', semantic: 'processing' },
  SHIPPED_ALL: { label: '全部发货', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
  CLOSED: { label: '已关闭', semantic: 'neutral' },
}

/** 退货单状态（退货申请 → 审核 → 收货 → 质检；business-flow.md §9.1） */
export const SALES_RETURN_STATUS_TAG: Record<SalesReturnStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  RECEIVING: { label: '收货中', semantic: 'processing' },
  IN_QC: { label: '质检中', semantic: 'processing' },
  SHIPPED: { label: '已发货', semantic: 'processing' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}
