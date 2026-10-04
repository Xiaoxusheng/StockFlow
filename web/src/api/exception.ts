import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 异常中心（后端已交付：internal/returns/routes 入口 handler.go:139-146，/api/exceptions） ----------
//
// 出参为 ExceptionView snake_case（internal/returns/service_exception.go:64-88；ID 出参
// database.ID 序列化为字符串，int64 裸列为 JSON number，JSONTime 序列化 "YYYY-MM-DD HH:mm:ss"
// 零值为 null）。业务规则 business-flow.md §11.2：九类异常统一生命周期，处理记录追加式
// （jsonb 数组，不覆盖历史）；异常冻结联动 inventory-rules §4（EXCEPTION_FREEZE→frozen，
// RESOLVED/CLOSED 必须释放）。
//
// 图片（image_refs）：表列 jsonb 常驻，创建时恒空数组（service_exception.go:140），
// 取证图经创建后挂接端点写入——POST /api/exceptions/{id}/images（d314103 交付，
// internal/returns/handler.go:143，file_ids 为文件中心 /api/files 上传产物，
// ExceptionImageInput service_exception_images.go:31-35，权限 returns:exception:execute），
// jsonb 追加 + 处理记录 + 审计。读侧建模、创建入参不带图片（后端 ExceptionCreateInput
// 无 image_refs 字段），不造假数据。

/** 异常单 ID（ExceptionView.id，database.ID → JSON 字符串） */
export type ExceptionId = number | string

/** 异常类型九类（internal/returns/models.go:48-51 + 迁移 000010 chk_exceptions_type 中文值域） */
export type ExceptionType =
  | '收货异常'
  | '质检异常'
  | '上架异常'
  | '库存异常'
  | '拣货异常'
  | '复核异常'
  | '物流异常'
  | '盘点异常'
  | '系统异常'

/** 九类值域（后端值即中文文案；筛选/创建选项与类型收窄共用，顺序对齐 models.go:48-51） */
export const EXCEPTION_TYPES: ExceptionType[] = [
  '收货异常',
  '质检异常',
  '上架异常',
  '库存异常',
  '拣货异常',
  '复核异常',
  '物流异常',
  '盘点异常',
  '系统异常',
]

/**
 * 异常类型是分类不是状态，按普通文本渲染，不走 SfStatusTag；
 * 值域即中文文案，后端未知值原样展示（不另设映射表，避免双源漂移）。
 */

/** 异常生命周期状态（internal/returns/models.go:39-44 + chk_exceptions_status 六态）：
 * OPEN（待处理）→ ASSIGNED（已分派）→ PROCESSING（处理中）→ PENDING_REVIEW（待复核）
 * → RESOLVED（已解决）→ CLOSED（已关闭）；业务口径 business-flow.md §11.2
 * 发现→创建→分派→处理中→待复核→已解决→已关闭（发现/创建归并为 OPEN 起始态） */
export type ExceptionStatus =
  | 'OPEN'
  | 'ASSIGNED'
  | 'PROCESSING'
  | 'PENDING_REVIEW'
  | 'RESOLVED'
  | 'CLOSED'

/**
 * 异常生命周期状态 → SfStatusTag。注册表（types/status.ts）以小写键收录
 * assigned/processing/resolved/closed，OPEN 无对应键、PENDING_REVIEW 注册表文案为
 * 通用的「待审核」——大写原始值不命中注册表（resolveStatus 精确匹配），label/semantic
 * 兜底接管（api/transfer.ts TRANSFER_STATUS_TAG 同款覆盖路径）；后端返回未知值时
 * 同样兜底中性灰 + 原始文案，不崩溃。
 */
export const EXCEPTION_STATUS_TAG: Record<ExceptionStatus, { label: string; semantic: StatusSemantic }> = {
  OPEN: { label: '待处理', semantic: 'warning' },
  ASSIGNED: { label: '已分派', semantic: 'processing' },
  PROCESSING: { label: '处理中', semantic: 'processing' },
  PENDING_REVIEW: { label: '待复核', semantic: 'pending' },
  RESOLVED: { label: '已解决', semantic: 'success' },
  CLOSED: { label: '已关闭', semantic: 'neutral' },
}

/** 异常单列表筛选（handler.go:510-514 实测仅绑 type/status/source_type/source_no，
 * keyword 模糊搜索后端不支持） */
export interface ExceptionQuery extends PageQuery {
  type?: ExceptionType
  status?: ExceptionStatus
  /** 来源单据类型（自由文本，迁移列 varchar(32) 无 CHECK；收货/拣货/复核/盘点等产生环节） */
  source_type?: string
  /** 来源单号（与 source_type 配对定位，idx_exceptions_source） */
  source_no?: string
}

/** 异常处理记录（HandleRecord，models.go:117-125；追加式：assign 分派 / start 开始处理 /
 * review 提交复核 / resolve 解决 / close 关闭 / freeze 异常冻结 / release 解除冻结） */
export interface ExceptionHandleRecord {
  at: string | null
  action: string
  by_id: number
  by_name: string
  note?: string
  /** 冻结/释放记录回指 inventory_locks（0=非冻结类记录） */
  lock_id?: number
  /** 冻结/释放量（numeric(18,4) 文本） */
  qty?: string
}

/** 异常单（ExceptionView，service_exception.go:64-88 字段全量） */
export interface ExceptionItem {
  id: ExceptionId
  exception_no: string
  type: ExceptionType
  /** 来源单据类型（自由文本） */
  source_type: string
  /** 来源单号（如入库单/质检单号，§13.1） */
  source_no: string
  /** SKU 定位（0=未定位） */
  sku_id: number
  /** 库位定位（0=未定位） */
  bin_id: number
  /** 序列号定位（空串=非序列号问题） */
  serial_no: string
  status: ExceptionStatus
  /** 问题描述（创建入参 detail 原文） */
  detail: string
  /** 处理人（0=未分派） */
  assignee_id: number
  assignee_name: string
  /** 责任人（0=未指定） */
  owner_id: number
  owner_name: string
  /** 处理记录（追加式台账） */
  handle_records: ExceptionHandleRecord[]
  /** 异常图片引用（创建恒空数组，经挂接端点写入） */
  image_refs: string[]
  /** 异常冻结锁（inventory_locks 逻辑引用，0=未冻结） */
  freeze_lock_id: number
  assigned_at: string | null
  resolved_at: string | null
  closed_at: string | null
  remark: string
  created_at: string | null
  updated_at: string | null
}

// ---------- 创建 / 动作入参（ExceptionCreateInput/AssignInput/NoteInput，service_exception.go:33-61） ----------

export interface ExceptionCreatePayload {
  type: ExceptionType
  source_type?: string
  source_no?: string
  /** 可空定位：0=未定位（后端 int64，前端未填不下发） */
  sku_id?: number
  bin_id?: number
  serial_no?: string
  owner_id?: number
  owner_name?: string
  /** 问题描述 */
  detail: string
  remark?: string
  /** 异常冻结开关：定位到具体库存行（sku_id+bin_id+freeze_warehouse_id）才可冻结
   * （inventory-rules §4.1，后端 fail-closed） */
  freeze_enabled?: boolean
  freeze_warehouse_id?: number
  /** 0=非批次 SKU */
  freeze_batch_id?: number
  /** 冻结量（numeric(18,4) 文本，解冻释放取量依据） */
  freeze_qty?: string
}

/** 分派入参（ExceptionAssignInput：assignee_id 必填，OPEN→ASSIGNED） */
export interface ExceptionAssignPayload {
  assignee_id: number
  assignee_name: string
}

/** 处理动作备注（ExceptionNoteInput：开始处理/提交复核/解决/关闭；后端空请求体允许） */
export interface ExceptionNotePayload {
  note?: string
}

/** 挂接图片取证入参（ExceptionImageInput，service_exception_images.go:31-35；
 * file_ids 为文件中心 /api/files 上传返回的文件 ID，database.ID 字符串形态） */
export interface ExceptionImagePayload {
  file_ids: Array<number | string>
}

// ---------- 权限码（internal/auth/permissions.go:272-277 三段式冻结） ----------

export const EXCEPTION_LIST_PERMISSION = 'returns:exception:list'
export const EXCEPTION_READ_PERMISSION = 'returns:exception:read'
export const EXCEPTION_CREATE_PERMISSION = 'returns:exception:create'
export const EXCEPTION_ASSIGN_PERMISSION = 'returns:exception:assign'
export const EXCEPTION_EXECUTE_PERMISSION = 'returns:exception:execute'
export const EXCEPTION_CLOSE_PERMISSION = 'returns:exception:close'

export const exceptionApi = {
  /** 异常单列表（GET /api/exceptions，handler.go:504-521） */
  list: (query: ExceptionQuery) =>
    http.get<PageResult<ExceptionItem>>('/api/exceptions', { params: query }),
  /** 异常单详情（GET /api/exceptions/{id}，handler.go:563-574，返回 ExceptionView 全量） */
  detail: (id: ExceptionId) => http.get<ExceptionItem>(`/api/exceptions/${id}`),
  /** 登记异常（POST /api/exceptions，创建为 OPEN；返回 { exception_no }，handler.go:531-554） */
  create: (payload: ExceptionCreatePayload) =>
    http.post<{ exception_no: string }>('/api/exceptions', payload),
  /** 分派（POST /api/exceptions/{id}/assign，OPEN→ASSIGNED） */
  assign: (id: ExceptionId, payload: ExceptionAssignPayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/assign`, payload),
  /** 开始处理（POST /api/exceptions/{id}/start，ASSIGNED→PROCESSING） */
  start: (id: ExceptionId, payload?: ExceptionNotePayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/start`, payload ?? {}),
  /** 提交复核（POST /api/exceptions/{id}/review，PROCESSING→PENDING_REVIEW） */
  review: (id: ExceptionId, payload?: ExceptionNotePayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/review`, payload ?? {}),
  /** 解决（POST /api/exceptions/{id}/resolve，PENDING_REVIEW→RESOLVED + 按需释放异常冻结） */
  resolve: (id: ExceptionId, payload?: ExceptionNotePayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/resolve`, payload ?? {}),
  /** 关闭（POST /api/exceptions/{id}/close，RESOLVED→CLOSED + 防御性释放冻结） */
  close: (id: ExceptionId, payload?: ExceptionNotePayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/close`, payload ?? {}),
  /** 挂接图片取证（POST /api/exceptions/{id}/images，d314103 交付；jsonb 追加 + 处理记录
   * + 审计，返回更新后 ExceptionView 全量，handler.go:577-603） */
  attachImages: (id: ExceptionId, payload: ExceptionImagePayload) =>
    http.post<ExceptionItem>(`/api/exceptions/${id}/images`, payload),
}
