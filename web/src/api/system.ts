import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 系统管理域（前端先行契约：docs/api.md:75-76 —— /api/logs 日志、
// /api/system 系统配置/监控/定时任务） ----------
//
// 后端 M1 未交付这两域（internal/router 仅注册 auth/masterdata/warehouse/inventory），
// 页面呈统一 Loading/Empty/Error 三态，禁止 mock。端点路径与字段为前端提案
// （同 api/reports.ts 先例），对齐依据：
//   - 操作/登录日志字段与已冻结的审计表 DDL 全量对齐
//     （db/migrations/000002_create_audit_log_tables.up.sql；database.md §7：审计数据
//     可查询、可追溯、不可篡改——应用层不提供 UPDATE/DELETE 接口，故日志域只读）；
//   - 定时任务对齐 architecture.md §9.2（cron 执行时间 / 启停 / 执行日志：开始、结束、
//     结果、耗时 / 失败重试）与 requirements.md:184 任务清单；
//   - 系统配置对齐 database.md §3（system_configs 表）、permission.md §6（修改强制审计）；
//   - 监控指标对齐 deployment.md §5 / requirements.md:184（CPU、内存、磁盘、数据库连接、
//     API 请求、错误率、运行时间、队列任务）。
// 待后端冻结后回对 JSON tag（snake_case）。

/** 菜单权限码（web/src/config/menu.tsx:157-160 既有编码，前端 fail-closed，后端必须校验） */
export const SYSTEM_LOG_VIEW_PERMISSION = 'system:log:view'
export const SYSTEM_JOB_VIEW_PERMISSION = 'system:job:view'
export const SYSTEM_CONFIG_VIEW_PERMISSION = 'system:config:view'
export const SYSTEM_MONITOR_VIEW_PERMISSION = 'system:monitor:view'

// ---------- 日志（/api/logs：操作日志 / 登录日志，均分页、只读） ----------

/** 操作日志筛选（db/migrations/000002 索引：user+created_at、module+created_at、created_at） */
export interface OperationLogQuery extends PageQuery {
  /** 用户名 / IP 模糊匹配（operation_logs.username / ip） */
  keyword?: string
  /** 模块筛选（operation_logs.module，编码值由后端字典冻结后提供） */
  module?: string
  /** 结果筛选：true=仅成功 / false=仅失败 */
  success?: boolean
}

/** 登录日志筛选（login_logs.username+created_at 索引） */
export interface LoginLogQuery extends PageQuery {
  /** 用户名 / IP 模糊匹配 */
  keyword?: string
  /** 结果筛选：true=仅成功 / false=仅失败 */
  success?: boolean
}

/** 操作日志条目（operation_logs 列全量；快照 jsonb 键结构由后端写入方决定，前端原样展示） */
export interface OperationLogItem {
  id: number | string
  request_id?: string
  user_id?: number | string
  username: string
  ip?: string
  user_agent?: string
  module: string
  object_type?: string
  object_id?: number | string
  action: string
  method?: string
  path?: string
  success: boolean
  error_code?: string
  request_snapshot?: Record<string, unknown>
  before_snapshot?: Record<string, unknown>
  after_snapshot?: Record<string, unknown>
  created_at: string
}

/** 登录日志条目（login_logs 列全量；成功/失败均记录，permission.md §3.3） */
export interface LoginLogItem {
  id: number | string
  user_id?: number | string
  username: string
  ip?: string
  user_agent?: string
  success: boolean
  fail_reason?: string
  created_at: string
}

export const systemLogApi = {
  /** GET /api/logs/operations：关键操作日志分页（审计只读，无写接口） */
  operations: (query: OperationLogQuery) =>
    http.get<PageResult<OperationLogItem>>('/api/logs/operations', { params: query }),
  /** GET /api/logs/logins：登录日志分页 */
  logins: (query: LoginLogQuery) =>
    http.get<PageResult<LoginLogItem>>('/api/logs/logins', { params: query }),
}

// ---------- 系统配置（/api/system/configs：读全部 + 批量保存） ----------

/** 配置控件类型（前端提案；enum 必须携带 options 候选值） */
export type SystemConfigType = 'text' | 'number' | 'boolean' | 'enum'

/** 配置项（system_configs；页面按 group 分区渲染为分组表单） */
export interface SystemConfigItem {
  /** 配置键（唯一，如 inventory.alert.scan-interval，前端提案命名空间） */
  key: string
  /** 当前值（统一字符串存储：boolean 为 "true"/"false"，number 为十进制字符串） */
  value: string
  /** 展示名 */
  name: string
  /** 分组编码（基础/库存/单据/安全等，页面按此分区） */
  group: string
  type?: SystemConfigType
  /** enum 类型候选值 */
  options?: string[]
  remark?: string
  /** 只读项（如版本/许可信息）不可编辑，保存时剔除 */
  readonly?: boolean
}

/** 批量保存入参：仅提交发生变更的项（后端逐项审计，permission.md §6 敏感操作） */
export interface SystemConfigSavePayload {
  items: Array<{ key: string; value: string }>
}

// ---------- 定时任务（/api/system/jobs：列表 / 启停 / 执行日志） ----------

export type SystemJobId = number | string

/** 任务筛选 */
export interface SystemJobQuery extends PageQuery {
  /** 任务编码 / 名称模糊匹配 */
  keyword?: string
  /** 启停筛选 */
  enabled?: boolean
}

/** 最近一次执行状态（前端提案值域） */
export type SystemJobRunStatus = 'success' | 'failed' | 'running'

/** 定时任务条目（architecture.md §9.1 任务清单 + §9.2 管理要求） */
export interface SystemJobItem {
  id: SystemJobId
  /** 任务编码（如 inventory-alert-scan，前端提案命名空间） */
  code: string
  name: string
  /** 执行时间 cron 表达式（§9.2：支持配置执行时间） */
  cron: string
  /** 执行时间人读描述（如 "每 5 分钟"，可选） */
  cron_desc?: string
  enabled: boolean
  last_run_at?: string | null
  last_run_status?: SystemJobRunStatus
  /** 最近一次执行耗时（毫秒） */
  last_run_duration_ms?: number
  next_run_at?: string | null
  remark?: string
}

/** 任务执行日志条目（§9.2：每次执行记录开始/结束/结果/耗时；失败含原因） */
export interface SystemJobRunLogItem {
  id: number | string
  job_id: SystemJobId
  start_at: string
  end_at?: string | null
  success: boolean
  /** 耗时（毫秒） */
  duration_ms?: number
  /** 执行结果说明 / 失败原因 */
  message?: string
}

// ---------- 系统监控（/api/system/monitor：指标 + 趋势一次返回） ----------

export interface SystemMonitorCpu {
  /** 使用率（0~100） */
  usagePercent: number
  /** 逻辑核数（可选） */
  cores?: number
}

export interface SystemMonitorMemory {
  usagePercent: number
  usedBytes: number
  totalBytes: number
}

export interface SystemMonitorDisk {
  usagePercent: number
  usedBytes: number
  totalBytes: number
}

/** 数据库连接（deployment.md §5「至少能够发现数据库连接异常」） */
export interface SystemMonitorDatabase {
  openConnections: number
  maxConnections: number
  healthy: boolean
}

/** API 请求与错误率（近 1 小时窗口为前端提案口径） */
export interface SystemMonitorApi {
  requestCount1h?: number
  errorCount1h?: number
  /** 错误率（0~100） */
  errorRatePercent: number
  qps?: number
}

/** 趋势采样点（图表数据源，全部来自接口，禁止前端造数） */
export interface SystemMonitorTrendPoint {
  /** 采样时间（YYYY-MM-DD HH:mm:ss，api.md §2） */
  time: string
  requests: number
  errors: number
}

export interface SystemMonitorMetrics {
  cpu: SystemMonitorCpu
  memory: SystemMonitorMemory
  disk: SystemMonitorDisk
  database: SystemMonitorDatabase
  api: SystemMonitorApi
  /** 进程运行时长（秒，deployment.md §5 运行时间） */
  uptimeSeconds?: number
  /** 队列中任务数（deployment.md §5 队列任务，可选） */
  queuedTasks?: number
  /** API 请求/错误趋势（近 24 小时，供图表渲染） */
  apiTrend: SystemMonitorTrendPoint[]
}

// ---------- API ----------

export const systemApi = {
  logs: systemLogApi,
  configs: {
    /** GET /api/system/configs：读全部配置（量级有限不分页，分组渲染） */
    list: () => http.get<SystemConfigItem[]>('/api/system/configs'),
    /** PUT /api/system/configs：批量保存（仅提交变更项） */
    save: (payload: SystemConfigSavePayload) => http.put<unknown>('/api/system/configs', payload),
  },
  jobs: {
    /** GET /api/system/jobs：任务列表（分页，与其他列表域格式统一 api.md §2.1） */
    list: (query: SystemJobQuery) =>
      http.get<PageResult<SystemJobItem>>('/api/system/jobs', { params: query }),
    /** PUT /api/system/jobs/:id/status：启用/停用（{enabled} 布尔开关，同 masterdata 先例） */
    setStatus: (id: SystemJobId, payload: { enabled: boolean }) =>
      http.put<unknown>(`/api/system/jobs/${id}/status`, payload),
    /** GET /api/system/jobs/:id/run-logs：执行日志分页（开始/结束/结果/耗时） */
    runLogs: (id: SystemJobId, query: PageQuery) =>
      http.get<PageResult<SystemJobRunLogItem>>(`/api/system/jobs/${id}/run-logs`, {
        params: query,
      }),
  },
  monitor: {
    /** GET /api/system/monitor：监控指标 + 趋势（deployment.md §5） */
    metrics: () => http.get<SystemMonitorMetrics>('/api/system/monitor'),
  },
}
