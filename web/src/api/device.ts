import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 设备域（后端 M3 已交付：internal/devices/handler.go:178-187，/api/devices） ----------
//
// 出参为 DeviceView = Device 裸模型 + online 派生位（internal/devices/service_device.go:167-170
// + models.go:111-137，snake_case；ID 出参 database.ID 序列化为字符串）。
// 后端不返回 warehouse_name / bound_user_name——页面经 options（api/options.ts）
// 以 warehouse_id / bound_user_id 本地映射展示，映射失败降级为 ID，不造假数据。

/** 视图出参 ID（后端序列化为字符串，保留 number 兼容） */
export type DeviceId = number | string

/** 设备类型（models.go:20-24 迁移 CHECK 同源小写值域：pc/pad/pda/scanner/printer） */
export type DeviceType = 'pc' | 'pad' | 'pda' | 'scanner' | 'printer'

/** 设备类型展示文案（与 config/menu.tsx 设备中心菜单命名一致） */
export const DEVICE_TYPE_LABEL: Record<DeviceType, string> = {
  pc: 'PC 设备',
  pad: 'Pad 设备',
  pda: 'PDA 设备',
  scanner: '扫码设备',
  printer: '打印设备',
}

/** 路由 → 设备类型（config/menu.tsx 设备中心四路由逐一对齐） */
export const DEVICE_TYPE_BY_PATH: Record<string, DeviceType> = {
  '/devices/scanners': 'scanner',
  '/devices/pda': 'pda',
  '/devices/pads': 'pad',
  '/devices/printers': 'printer',
}

/** 设备类型 → 列表路由（详情页返回对应列表用；pc 无独立菜单路由） */
export const DEVICE_LIST_PATH: Partial<Record<DeviceType, string>> = {
  scanner: '/devices/scanners',
  pda: '/devices/pda',
  pad: '/devices/pads',
  printer: '/devices/printers',
}

/** 设备启停状态（models.go:36：DISABLED；启用为缺省态，types/status.ts 已注册 enabled/disabled） */
export type DeviceStatus = 'ENABLED' | 'DISABLED'

/**
 * 激活状态双字段（DeviceActivationPayload，service_device.go:183-196）：
 *   - activation_status：后端冻结枚举（PENDING/ACTIVATED，大写）；
 *   - status：前端派生态（pending/activated/expired/disabled，小写，deriveActivationStatus
 *     service_device.go:213-221——expired/disabled 为前端派生值，后端不落库）。
 */
export type DeviceActivationStatus = 'pending' | 'activated' | 'expired' | 'disabled'

/** 激活状态展示映射（经 SfStatusTag 的 label + semantic 走全局语义色，页面不自造颜色） */
export const ACTIVATION_STATUS_META: Record<DeviceActivationStatus, { label: string; semantic: 'success' | 'processing' | 'warning' | 'neutral' }> = {
  pending: { label: '待激活', semantic: 'processing' },
  activated: { label: '已激活', semantic: 'success' },
  expired: { label: '已过期', semantic: 'warning' },
  disabled: { label: '已停用', semantic: 'neutral' },
}

/** 列表筛选（handler.go:261-293：type/status/keyword/warehouse_id/online） */
export interface DeviceQuery extends PageQuery {
  type?: DeviceType
  status?: DeviceStatus
  keyword?: string
  warehouse_id?: DeviceId
  /** 在线状态筛选（true/false） */
  online?: boolean
}

/**
 * 设备列表项（DeviceView = Device + online，service_device.go:167-170 + models.go:111-137
 * 字段全量）。无 warehouse_name / bound_user_name——经 options 映射；
 * activation_token_hash 后端 json:"-" 不出参。
 */
export interface DeviceItem {
  id: DeviceId
  /** 设备编号（devices.md §6.1 设备注册主键，如 SF-SCAN-001） */
  code: string
  name: string
  type: DeviceType
  brand: string
  model: string
  /** 操作系统（详情形态含） */
  os: string
  /** 0=未绑定仓库 */
  warehouse_id: number
  /** 0=未绑定用户 */
  bound_user_id: number
  status: DeviceStatus
  /** 冻结枚举 PENDING/ACTIVATED（service_device.go:183-186 注） */
  activation_status: string
  activation_expires_at: string | null
  activated_at: string | null
  activated_by: number
  app_version: string
  last_online_at: string | null
  last_scan_at: string | null
  /** 电量百分比（设备支持时返回，可空） */
  battery_level: number | null
  ip: string
  token_version: number
  remark: string
  created_at: string
  updated_at: string
  /** 在线派生位（心跳判定，devices.md §7.2；渲染走 SfStatusTag online/offline） */
  online: boolean
}

/** 设备激活码载荷（DeviceActivationPayload，service_device.go:183-196 字段全量；
 * qr_content 仅在创建/重新生成时返回——token 仅存哈希，查询不可还原） */
export interface DeviceActivation {
  device_id: DeviceId
  device_code: string
  server_url: string
  /** 后端组装的完整二维码内容；缺失时前端以 server_url + device_code 兜底拼接（devices.md §6.1） */
  qr_content?: string
  /** 0=未绑定仓库 */
  warehouse_id: number
  /** 冻结枚举（PENDING/ACTIVATED） */
  activation_status: string
  /** 派生态（pending/activated/expired/disabled） */
  status: DeviceActivationStatus
  expires_at: string | null
  activated_at: string | null
}

/** 新建设备入参（DeviceCreateInput，service_device.go:252-260；
 * warehouse_id 提交 number，0=暂不绑定（devices.md §6.3 可后续绑定）） */
export interface DeviceCreatePayload {
  code: string
  name: string
  type: DeviceType
  brand?: string
  model?: string
  warehouse_id?: number
  remark?: string
}

/** 绑定用户入参（POST /api/devices/{id}/bind 请求体，handler.go:360：{user_id}） */
export interface DeviceBindPayload {
  user_id: number
}

/** 设备详情（DeviceDetailView，service_device.go:172-178：Device + online + 配置 + 扫码统计） */
export interface DeviceDetail extends DeviceItem {
  /** 设备配置（device_configs.config jsonb 原样，键结构由下发端约定；无配置行为 null） */
  config: Record<string, unknown> | null
  config_version: number
  scan_total: number
  scan_today: number
}

// ---------- 权限码（internal/devices/permissions.go → auth 冻结三段式） ----------

export const DEVICE_LIST_PERMISSION = 'devices:device:list'
export const DEVICE_READ_PERMISSION = 'devices:device:read'
export const DEVICE_CREATE_PERMISSION = 'devices:device:create'
export const DEVICE_UPDATE_PERMISSION = 'devices:device:update'
export const DEVICE_STATUS_PERMISSION = 'devices:device:status'

export const deviceApi = {
  /** GET /api/devices（type/status/keyword/warehouse_id/online 筛选，handler.go:261-293） */
  list: (query: DeviceQuery) => http.get<PageResult<DeviceItem>>('/api/devices', { params: query }),
  /** GET /api/devices/{id}（DeviceDetailView） */
  get: (id: DeviceId) => http.get<DeviceDetail>(`/api/devices/${id}`),
  /** POST /api/devices：创建成功返回激活二维码 payload（devices.md §6.2） */
  create: (payload: DeviceCreatePayload) => http.post<DeviceActivation>('/api/devices', payload),
  /** GET /api/devices/{id}/activation：激活状态查询（前端待激活时轮询） */
  activation: (id: DeviceId) => http.get<DeviceActivation>(`/api/devices/${id}/activation`),
  /** PUT /api/devices/{id}/activation：重新生成激活二维码（qr_content 仅此处返回） */
  regenActivation: (id: DeviceId) => http.put<DeviceActivation>(`/api/devices/${id}/activation`),
  /** POST /api/devices/{id}/bind：绑定用户（devices.md §6.4） */
  bind: (id: DeviceId, payload: DeviceBindPayload) =>
    http.post<unknown>(`/api/devices/${id}/bind`, payload),
  /** POST /api/devices/{id}/unbind：解绑用户（devices.md §7.1） */
  unbind: (id: DeviceId) => http.post<unknown>(`/api/devices/${id}/unbind`),
  /** POST /api/devices/{id}/disable：停用设备（devices.md §7.1；远程注销不在本期契约内） */
  disable: (id: DeviceId) => http.post<unknown>(`/api/devices/${id}/disable`),
}
