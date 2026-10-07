import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'
import type { StatusSemantic } from '@/types/status'

// ---------- 设备域（后端 M3 已交付：internal/devices/handler.go:183-197，/api/devices） ----------
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
  /** 后端组装的完整二维码内容，冻结 JSON 三字段 {server_url,device_code,token}
   * （service_device.go:199-210 qrContent）；缺失时只能经 PUT /{id}/activation
   * 重新生成取回（token 查询不可还原），前端不自行拼装替代内容 */
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
export const DEVICE_SCANLOG_LIST_PERMISSION = 'devices:scanlog:list'
export const SCANNER_RESOLVE_PERMISSION = 'scanner:resolve:list'

// ---------- 设备日志（GET /api/devices/{id}/logs，handler.go:194/:503-520；
// device_logs level CHECK 冻结值域 INFO/WARN/ERROR，000013 迁移 chk_device_logs_level） ----------

export type DeviceLogLevel = 'INFO' | 'WARN' | 'ERROR'

/** 日志级别文案与语义色（types/status.ts 注册表未含日志级别，经 SfStatusTag 显式指定） */
export const DEVICE_LOG_LEVEL_META: Record<DeviceLogLevel, { label: string; semantic: StatusSemantic }> = {
  INFO: { label: '信息', semantic: 'processing' },
  WARN: { label: '警告', semantic: 'warning' },
  ERROR: { label: '错误', semantic: 'danger' },
}

/** 设备运行日志行（DeviceLog 裸模型，models.go:185-194 字段全量；append-only 设备端上报） */
export interface DeviceLogItem {
  id: DeviceId
  device_id: number
  level: DeviceLogLevel
  event_type: string
  message: string
  /** jsonb 上下文原样（键结构由上报端约定） */
  context: Record<string, unknown> | null
  occurred_at: string
  created_at: string
}

/** 设备日志筛选（handler.go:513：level + 分页） */
export interface DeviceLogQuery extends PageQuery {
  level?: DeviceLogLevel
}

// ---------- 扫码日志（GET /api/scanner/logs，handler.go:197/:529-581，devices:scanlog:list；
// scan_logs 裸模型 models.go:161-179，仓库范围数据权限后端 fail-closed） ----------

/** 扫码日志行（ScanLog，models.go:161-179 字段全量；device_id 可空=Web HID 场景） */
export interface ScanLogItem {
  id: DeviceId
  device_id: number | null
  device_code: string | null
  user_id: number
  username: string
  ip: string
  warehouse_id: number
  raw_code: string
  symbology: string
  resolve_type: string
  resolve_id: number
  resolve_code: string
  page: string
  business_no: string
  success: boolean
  error_code: string
  created_at: string
}

/** 扫码日志筛选（handler.go:535-565：keyword/device_id/user_id/warehouse_id/success + 分页） */
export interface ScanLogQuery extends PageQuery {
  keyword?: string
  device_id?: DeviceId
  user_id?: number
  warehouse_id?: number
  /** 识别结果成败（true/false） */
  success?: boolean
}

// ---------- 设备配置下发（PUT /api/devices/{id}/config，handler.go:193/:477-498；
// 键白名单与值类型 service_device.go:38-48 configKeyKinds + :570-606 validateDeviceConfig） ----------

/** 配置下发项（devices.md §7.3 冻结九键；bool 开关类 / int 非负整数类 /
 * scan_mode 为 1-64 字符字符串——validateDeviceConfig 同口径） */
export interface DeviceConfigPayload {
  scan_mode?: string
  sound?: boolean
  vibrate?: boolean
  auto_focus?: boolean
  continuous_scan?: boolean
  scan_timeout_seconds?: number
  /** 0=不设默认仓 */
  default_warehouse_id?: number
  task_refresh_seconds?: number
  /** 0=关闭自动锁屏（devices.md §6.5） */
  auto_lock_minutes?: number
}

/** 配置下发响应（handler.go:492：{version}，下发后版本 +1） */
export interface DeviceConfigResult {
  version: number
}

// ---------- 统一扫码解析（POST /api/scanner/resolve，handler.go:209/:731-745；
// 双轨认证：设备令牌或用户 JWT（scanner:resolve:list）——Web HID 走用户链，plan §8.3；
// 只识别不执行业务，scanner.md §5.3） ----------

/** 识别对象类型（service_scan.go resolvePipeline 管线冻结值域） */
export type ScanResolveType = 'doc' | 'sku' | 'bin' | 'serial' | 'batch'

/** 单据前缀（ports.go:79-95 docPrefixOwners 冻结 15 值，{prefix}-{YYYYMMDD}-{seq} 首段） */
export type ScanDocKind =
  | 'IN' | 'PO' | 'QC' | 'RC' | 'PW'
  | 'SO' | 'OUT' | 'PK' | 'CH' | 'BP' | 'SH'
  | 'TR' | 'CK'
  | 'RT' | 'EX'

/** 识别结果解析请求（ResolveInput，service_scan.go:61-65；code 必填 ≤255，
 * symbology/page 为可选上下文落 scan_logs 对应列） */
export interface ScanResolveInput {
  code: string
  symbology?: string
  /** 调用页面标识（扫码审计 page 列，如 PC 路由路径） */
  page?: string
}

/** 单个识别对象（ResolveItem，service_scan.go:36-44；多命中时 items 携带全量列表） */
export interface ScanResolveItem {
  type: ScanResolveType
  id: DeviceId
  code: string
  name: string
  /** type=doc 时的单号前缀 */
  doc_kind?: ScanDocKind
  warehouse_id?: number
  status?: string
}

/** 识别结果（ResolveResult，service_scan.go:47-57；多命中未定位时后端置零 ID——经
 * database.ID MarshalJSON 恒序列化为字符串 "0"（internal/database/model.go:22-24，
 * service_scan.go:295-299），携带 items；消费方禁止用数字 0 等值比较判未定位） */
export interface ScanResolveResult extends ScanResolveItem {
  /** 仅多命中携带（库位跨仓同码 / 批次跨 SKU，plan §8.3 歧义消解） */
  items?: ScanResolveItem[]
  /** 去重窗口标记（scanner.md §6.6：只注记不抑制识别） */
  duplicate: boolean
}

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
  /** PUT /api/devices/{id}/config：配置下发（devices.md §7.3 白名单校验，返回新版本号） */
  config: (id: DeviceId, payload: DeviceConfigPayload) =>
    http.put<DeviceConfigResult>(`/api/devices/${id}/config`, payload),
  /** GET /api/devices/{id}/logs：设备运行日志分页（level 筛选，devices.md §7.1 查看设备日志） */
  logs: (id: DeviceId, query: DeviceLogQuery) =>
    http.get<PageResult<DeviceLogItem>>(`/api/devices/${id}/logs`, { params: query }),
  /** GET /api/scanner/logs：扫码日志分页（devices.md §13.2 扫码审计；仓库范围数据权限） */
  scanLogs: (query: ScanLogQuery) => http.get<PageResult<ScanLogItem>>('/api/scanner/logs', { params: query }),
}

/** 扫码解析 API（POST /api/scanner/resolve 双轨：设备令牌或用户 JWT 链——PC 端
 * Web HID 场景以登录用户 JWT 调用（handler.go:236-239 resolveAuth），需
 * scanner:resolve:list 权限；只识别不执行业务，业务执行走各域业务 API——scanner.md §5.3） */
export const scannerApi = {
  resolve: (payload: ScanResolveInput) => http.post<ScanResolveResult>('/api/scanner/resolve', payload),
}
