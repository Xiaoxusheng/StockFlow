import type { PurchaseStatus } from '@/api/purchase'
import type { StatusSemantic } from '@/types/status'

/**
 * 采购订单状态 → SfStatusTag 映射（域内唯一来源，与销售域 salesStatusMeta.ts 同范式：
 * 订单列表/详情/退货抽屉/入库表单来源单选择共用，禁止各页复制粘贴漂移）。
 *
 * 值域出处（internal/purchase/models.go:17-23 七态，迁移 CHECK 同源）。
 * 渲染约定（同销售域）：后端大写枚举经 toStatusKey 归一后先命中 types/status.ts
 * 注册表（draft/pending_approval/approved/completed/cancelled，文案/语义与本表
 * 逐键一致，注册表优先渲染不变）；PARTIAL_RECEIVED/RECEIVED_ALL 为采购语境专有键
 * 未注册，经 SfStatusTag 的 label/semantic 兜底；后端返回未知值时映射缺失，
 * SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
export const PO_STATUS_TAG: Record<PurchaseStatus, { label: string; semantic: StatusSemantic }> = {
  DRAFT: { label: '草稿', semantic: 'neutral' },
  PENDING_APPROVAL: { label: '待审核', semantic: 'pending' },
  APPROVED: { label: '已审核', semantic: 'success' },
  PARTIAL_RECEIVED: { label: '部分到货', semantic: 'processing' },
  RECEIVED_ALL: { label: '到货完成', semantic: 'success' },
  COMPLETED: { label: '已完成', semantic: 'success' },
  CANCELLED: { label: '已取消', semantic: 'neutral' },
}
