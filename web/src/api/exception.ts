import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 异常中心（docs/api.md §1：/api/exceptions；业务规则 business-flow.md §11.2） ----------
// 前端先行契约：M1 后端未交付异常域（backend-m1-plan.md §13），类型与生命周期值域以
// business-flow.md §11.2 为准，后端冻结接口后回对。就绪前页面呈统一错误态
// （requirements.md §10），禁止 mock。

/** 异常类型九类（business-flow.md §11.2：收货/质检/上架/库存/拣货/复核/物流/盘点/系统） */
export type ExceptionType =
  | 'receiving'
  | 'quality'
  | 'putaway'
  | 'inventory'
  | 'picking'
  | 'checking'
  | 'logistics'
  | 'counting'
  | 'system'

/** 异常类型文案（异常类型是分类不是状态，按普通文本渲染，不走 SfStatusTag；未知值回退展示原始值） */
export const EXCEPTION_TYPE_LABEL: Record<ExceptionType, string> = {
  receiving: '收货异常',
  quality: '质检异常',
  putaway: '上架异常',
  inventory: '库存异常',
  picking: '拣货异常',
  checking: '复核异常',
  logistics: '物流异常',
  counting: '盘点异常',
  system: '系统异常',
}

/** 异常生命周期状态（business-flow.md §11.2：发现 → 创建 → 分派 → 处理中 → 待复核 → 已解决 → 已关闭） */
export type ExceptionStatus =
  | 'discovered'
  | 'created'
  | 'assigned'
  | 'processing'
  | 'pending_recheck'
  | 'resolved'
  | 'closed'

/**
 * 生命周期状态 → SfStatusTag：注册表（types/status.ts）已收录
 * processing（处理中）/closed（已关闭），其余 key 走 label/semantic 兜底；
 * 后端返回未知值时映射缺失，SfStatusTag 兜底中性灰 + 原始文案，不崩溃。
 */
export const EXCEPTION_STATUS_TAG: Record<ExceptionStatus, { label: string; semantic: StatusSemantic }> = {
  discovered: { label: '发现', semantic: 'warning' },
  created: { label: '已创建', semantic: 'pending' },
  assigned: { label: '已分派', semantic: 'processing' },
  processing: { label: '处理中', semantic: 'processing' },
  pending_recheck: { label: '待复核', semantic: 'pending' },
  resolved: { label: '已解决', semantic: 'success' },
  closed: { label: '已关闭', semantic: 'neutral' },
}

export interface ExceptionQuery extends PageQuery {
  keyword?: string
  exceptionType?: ExceptionType
  status?: ExceptionStatus
}

/** 异常单（§11.2：支持图片、附件、评论、处理人、责任人、处理记录；列表先呈现主档字段） */
export interface ExceptionItem {
  id: number | string
  /** 异常单号（编号前缀未在 business-flow.md §13.1 列出，规则待后端冻结后回对） */
  exceptionNo: string
  exceptionType: ExceptionType
  title: string
  description?: string
  warehouseName?: string
  skuCode?: string
  /** 来源单据号（如入库单/质检单，§13.1） */
  bizNo?: string
  /** 责任人 */
  ownerName?: string
  /** 处理人 */
  handlerName?: string
  status: ExceptionStatus
  /** 发现时间 */
  discoveredAt: string
  /** 解决时间 */
  resolvedAt?: string
}

export const exceptionApi = {
  /** 异常单列表（GET /api/exceptions，前端先行契约） */
  list: (query: ExceptionQuery) =>
    http.get<PageResult<ExceptionItem>>('/api/exceptions', { params: query }),
}
