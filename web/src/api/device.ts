import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 设备域（前端先行契约，api.md:71 /api/devices + devices.md §6–7） ----------
//
// 后端设备域尚未交付：本文件按 api.md 域划分与 devices.md §6（注册/激活二维码/绑定仓库）
// §7（设备管理后台：列表字段、绑定/解绑/停用）冻结前端契约，后端交付后回对字段名与枚举值。
// 字段命名沿用项目已交付域（api/masterdata.ts）的 snake_case 形态：
//   - 视图出参 ID 为字符串（internal/database/model.go MarshalJSON，保留 number 兼容）；
//   - 提交入参外键一律 number（Go int64 不接受字符串形态）。

/** 视图出参 ID（后端序列化为字符串，保留 number 兼容） */
export type DeviceId = number | string

/** 设备类型（frontend.md §14.1：PC、Pad、PDA、Scanner、Printer） */
export type DeviceType = 'pc' | 'pad' | 'pda' | 'scanner' | 'printer'

/** 设备类型展示文案（与 config/menu.tsx:141-144 设备中心菜单命名一致） */
export const DEVICE_TYPE_LABEL: Record<DeviceType, string> = {
  pc: 'PC 设备',
  pad: 'Pad 设备',
  pda: 'PDA 设备',
  scanner: '扫码设备',
  printer: '打印设备',
}

/** 路由 → 设备类型（config/menu.tsx:141-144 设备中心四路由逐一对齐） */
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

/** 设备启停状态（devices.md §7.1 状态列；types/status.ts 已注册 enabled/disabled） */
export type DeviceStatus = 'ENABLED' | 'DISABLED'

/** 设备激活状态机（devices.md §6.2：登记 → 生成激活二维码 → Scan 扫码激活） */
export type DeviceActivationStatus = 'pending' | 'activated' | 'expired' | 'disabled'

/** 激活状态展示映射（未在 types/status.ts 注册的状态，经 SfStatusTag 的
 * label + semantic 走全局语义色，页面不自造颜色） */
export const ACTIVATION_STATUS_META: Record<DeviceActivationStatus, { label: string; semantic: 'success' | 'processing' | 'warning' | 'neutral' }> = {
  pending: { label: '待激活', semantic: 'processing' },
  activated: { label: '已激活', semantic: 'success' },
  expired: { label: '已过期', semantic: 'warning' },
  disabled: { label: '已停用', semantic: 'neutral' },
}

/** 列表筛选（api.md:71：type/warehouse/online/keyword + 统一分页） */
export interface DeviceQuery extends PageQuery {
  type?: DeviceType
  warehouse_id?: string
  online?: boolean
  keyword?: string
}

/** 设备列表项（frontend.md §14.1 十列：设备名称/设备类型/品牌/型号/仓库/绑定用户/
 * 在线状态/最后在线/App 版本/最后扫码；另含编号与启停状态） */
export interface DeviceItem {
  id: DeviceId
  /** 设备编号（devices.md §6.1 设备注册主键，如 SF-SCAN-001） */
  code: string
  name: string
  type: DeviceType
  brand?: string
  model?: string
  warehouse_id?: DeviceId | null
  warehouse_name?: string
  bound_user_id?: DeviceId | null
  bound_user_name?: string
  /** 在线状态（心跳判定，devices.md §7.2；渲染走 SfDeviceStatus → types/status.ts online/offline） */
  online: boolean
  last_online_at?: string | null
  app_version?: string
  last_scan_at?: string | null
  status: DeviceStatus
  remark?: string
  created_at?: string
  updated_at?: string
}

/** 设备激活码 payload：POST /api/devices 创建成功返回；GET /api/devices/:id/activation
 * 轮询同一结构（devices.md §6.2：二维码内容=服务器地址+设备编号，Scan 扫码后自动获取
 * 服务器地址/设备编号/仓库/初始配置） */
export interface DeviceActivation {
  device_id: DeviceId
  device_code: string
  server_url: string
  /** 后端组装的完整二维码内容；缺失时前端以 server_url + device_code 兜底拼接（devices.md §6.1） */
  qr_content?: string
  warehouse_name?: string
  status: DeviceActivationStatus
  /** 激活码有效期（devices.md 未定长，契约冻结后确认） */
  expires_at?: string | null
  activated_at?: string | null
  activated_by?: string
}

/** 新建设备入参（devices.md §6.1–6.2：设备编号 + 基本信息；绑定仓库即初始配置仓库） */
export interface DeviceCreatePayload {
  code: string
  name: string
  type: DeviceType
  brand?: string
  model?: string
  /** 外键提交 number；null=创建不绑定仓库（devices.md §6.3 绑定仓库可后续绑定） */
  warehouse_id?: number | null
  remark?: string
}

/** 绑定用户入参（devices.md §6.4：共享设备多人使用，但所有业务操作必须记录真实操作者） */
export interface DeviceBindPayload {
  user_id: number
}

/** 使用记录（frontend.md §14.2 使用记录区：谁、什么时候、在设备上做了什么） */
export interface DeviceUsageRecord {
  id: DeviceId
  used_at?: string
  user_name?: string
  action?: string
  remark?: string
}

/** 异常记录（devices.md §7.2 设备健康监控：最近错误） */
export interface DeviceExceptionRecord {
  id: DeviceId
  occurred_at?: string
  type?: string
  message?: string
  resolved?: boolean
}

/** 操作记录（devices.md §7.1：绑定/解绑/停用等后台操作审计，frontend.md §14.2 操作记录区） */
export interface DeviceOperationLog {
  id: DeviceId
  time?: string
  operator?: string
  action?: string
  detail?: string
}

/** 设备详情（frontend.md §14.2：基础信息/在线状态/软件版本/扫码信息/使用记录/异常记录/操作记录） */
export interface DeviceDetail extends DeviceItem {
  /** 操作系统（devices.md §7.1 系统列） */
  os?: string
  ip?: string
  /** 电量百分比 0–100（devices.md §7.2：设备支持时返回） */
  battery_level?: number
  last_sync_at?: string | null
  activation?: DeviceActivation
  scan_total?: number
  scan_today?: number
  usage_records?: DeviceUsageRecord[]
  exception_records?: DeviceExceptionRecord[]
  operation_logs?: DeviceOperationLog[]
}

/** 设备域 API（前端先行契约：后端 /api/devices 域未交付前，页面一律呈统一错误态/空态） */
export const deviceApi = {
  /** GET /api/devices（type/warehouse/online/keyword 筛选，api.md:71） */
  list: (query: DeviceQuery) => http.get<PageResult<DeviceItem>>('/api/devices', { params: query }),
  /** GET /api/devices/:id（frontend.md §14.2 详情结构） */
  get: (id: DeviceId) => http.get<DeviceDetail>(`/api/devices/${id}`),
  /** POST /api/devices：创建成功返回激活二维码 payload（devices.md §6.2） */
  create: (payload: DeviceCreatePayload) => http.post<DeviceActivation>('/api/devices', payload),
  /** GET /api/devices/:id/activation：激活状态查询（前端待激活时轮询） */
  activation: (id: DeviceId) => http.get<DeviceActivation>(`/api/devices/${id}/activation`),
  /** POST /api/devices/:id/bind：绑定用户（devices.md §6.4） */
  bind: (id: DeviceId, payload: DeviceBindPayload) =>
    http.post<unknown>(`/api/devices/${id}/bind`, payload),
  /** POST /api/devices/:id/unbind：解绑用户（devices.md §7.1） */
  unbind: (id: DeviceId) => http.post<unknown>(`/api/devices/${id}/unbind`),
  /** POST /api/devices/:id/disable：停用设备（devices.md §7.1；远程注销不在本期契约内） */
  disable: (id: DeviceId) => http.post<unknown>(`/api/devices/${id}/disable`),
}
