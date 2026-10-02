import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 调拨单（docs/api.md §1：/api/transfers；业务规则 business-flow.md §10.1、§13.1） ----------
// 前端先行契约：M1 后端未交付调拨单域（backend-m1-plan.md §13），字段以 business-flow.md
// §10.1 为准，后端冻结接口后回对。与 /api/inventory/transfers（库存转移，
// api/inventory.ts + views/inventory/TransferPage.tsx）是两套语义：
//   /api/transfers        调拨单据流（仓库中心菜单组 /transfers，7 态状态机走单）
//   /api/inventory/transfers 库存转移作业（库存中心菜单组 /inventory/transfers）
// 就绪前页面呈统一错误态（requirements.md §10），禁止 mock。

/** 调拨维度（business-flow.md §10.1：仓库 → 仓库 / 库位 → 库位；枚举前端先行） */
export type TransferType = 'warehouse' | 'bin'

/** 调拨单状态机（business-flow.md §10.1：草稿 → 待审核 → 待出库 → 调拨中 → 待入库 → 已完成，任一环节可已取消；枚举前端先行） */
export type TransferStatus =
  | 'draft'
  | 'pending_review'
  | 'pending_outbound'
  | 'transferring'
  | 'pending_inbound'
  | 'completed'
  | 'cancelled'

/**
 * 调拨单状态 → SfStatusTag：注册表（types/status.ts）已收录
 * draft/pending_review/completed/cancelled，其余 key 走 label/semantic 兜底；
 * 后端返回未知值时映射缺失，SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
export const TRANSFER_STATUS_TAG: Record<TransferStatus, { label: string; semantic: StatusSemantic }> = {
  draft: { label: '草稿', semantic: 'neutral' },
  pending_review: { label: '待审核', semantic: 'pending' },
  pending_outbound: { label: '待出库', semantic: 'pending' },
  transferring: { label: '调拨中', semantic: 'processing' },
  pending_inbound: { label: '待入库', semantic: 'pending' },
  completed: { label: '已完成', semantic: 'success' },
  cancelled: { label: '已取消', semantic: 'neutral' },
}

export interface TransferQuery extends PageQuery {
  keyword?: string
  transferType?: TransferType
  status?: TransferStatus
}

/** 调拨明细行（单据 × SKU 行级记录；两端均生成库存流水，跨仓在途数量体现为在途库存，§10.1） */
export interface TransferItem {
  id: number | string
  /** 调拨单号（business-flow.md §13.1：TR-日期-流水） */
  transferNo: string
  transferType: TransferType
  sourceWarehouseName: string
  targetWarehouseName: string
  sourceBinCode?: string
  targetBinCode?: string
  skuCode: string
  productName: string
  batchNo?: string
  qty: number
  status: TransferStatus
  createdByName?: string
  createdAt: string
  completedAt?: string
}

export const transferApi = {
  /** 调拨单列表（GET /api/transfers，前端先行契约） */
  list: (query: TransferQuery) => http.get<PageResult<TransferItem>>('/api/transfers', { params: query }),
}
