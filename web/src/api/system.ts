import { http } from './client'
import type { PageQuery, PageResult } from '@/types/api'

// ---------- 系统管理域（后端 M3 已交付：internal/sysops/routes.go:94-120——
// /api/logs 日志、/api/system 配置/任务/监控、/api/notifications 通知） ----------
//
// 出参为 sysops 视图 snake_case（logs.go:16-47 / configs.go:23-33 / jobsapi.go:14-40 /
// monitor.go:186-234；此域 ID 为裸 int64 出参数字，非 database.ID 字符串）。
// 审计数据可查询、可追溯、不可篡改（database.md §7）——日志域只读，无写接口。

/** 菜单权限码（后端冻结三段式，internal/auth/permissions.go system:log:* 等） */
export const SYSTEM_LOG_VIEW_PERMISSION = 'system:log:list'
export const SYSTEM_JOB_VIEW_PERMISSION = 'system:job:list'
export const SYSTEM_CONFIG_VIEW_PERMISSION = 'system:config:list'
export const SYSTEM_MONITOR_VIEW_PERMISSION = 'system:monitor:list'

// ---------- 日志（/api/logs：操作日志 / 登录日志，均分页、只读） ----------

/** 操作日志筛选（routes.go:186-213：keyword/module/action/success/time_from/time_to） */
export interface OperationLogQuery extends PageQuery {
  /** 用户名 / IP 模糊匹配（operation_logs.username / ip） */
  keyword?: string
  /** 模块筛选（operation_logs.module） */
  module?: string
  /** 动作筛选（handler.go:205） */
  action?: string
  /** 结果筛选：true=仅成功 / false=仅失败 */
  success?: boolean
  /** 时间范围：YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss（parseDayParam，routes.go:172-182） */
  time_from?: string
  time_to?: string
}

/** 登录日志筛选（routes.go:228-254：keyword/success/time_from/time_to） */
export interface LoginLogQuery extends PageQuery {
  keyword?: string
  success?: boolean
  time_from?: string
  time_to?: string
}

/** 操作日志条目（OperationLog，internal/sysops/logs.go:16-35 字段全量；快照 jsonb 键
 * 结构由写入方决定，前端原样展示） */
export interface OperationLogItem {
  id: number
  request_id: string
  user_id: number
  username: string
  ip: string
  user_agent?: string
  module: string
  object_type?: string
  object_id: number
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

/** 登录日志条目（LoginLog，logs.go:38-47 字段全量；成功/失败均记录，permission.md §3.3） */
export interface LoginLogItem {
  id: number
  user_id: number
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
  /** GET /api/logs/operations/{id}：操作日志详情（三快照，routes.go:95） */
  operation: (id: number) => http.get<OperationLogItem>(`/api/logs/operations/${id}`),
  /** GET /api/logs/logins：登录日志分页 */
  logins: (query: LoginLogQuery) =>
    http.get<PageResult<LoginLogItem>>('/api/logs/logins', { params: query }),
}

// ---------- 系统配置（/api/system/configs：读全部 + 批量保存） ----------

/** 配置控件类型（db/migrations/000014 chk_system_configs_type：text/number/boolean/enum） */
export type SystemConfigType = 'text' | 'number' | 'boolean' | 'enum'

/** 配置项（systemConfigItem，internal/sysops/configs.go:23-33 字段全量；页面按 group
 * 分区渲染为分组表单） */
export interface SystemConfigItem {
  /** 配置键（唯一，如 datax.export_batch_size） */
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
  /** 只读项不可编辑，保存时剔除 */
  readonly: boolean
  updated_at: string
}

/** 批量保存入参（saveConfigsPayload，routes.go:267-272；仅提交变更项，逐项审计） */
export interface SystemConfigSavePayload {
  items: Array<{ key: string; value: string }>
}

// ---------- 定时任务（/api/system/jobs：列表 / 启停 / 执行日志） ----------

export type SystemJobId = number

/** 任务筛选（routes.go:298-316：keyword/enabled） */
export interface SystemJobQuery extends PageQuery {
  /** 任务编码 / 名称模糊匹配 */
  keyword?: string
  /** 启停筛选（true/false） */
  enabled?: boolean
}

/** 最近一次执行状态（db/migrations/000014 chk_scheduled_jobs_last_run_status 小写值域） */
export type SystemJobRunStatus = 'success' | 'failed' | 'running' | 'never'

/** 定时任务条目（ScheduledJob，internal/sysops/jobsapi.go:14-25 字段全量；
 * 执行时间 cron 表达式 + 启停热更新 + 最近执行三要素） */
export interface SystemJobItem {
  id: SystemJobId
  /** 任务编码（如 file_cleanup） */
  code: string
  name: string
  /** 执行时间 cron 表达式 */
  cron: string
  enabled: boolean
  last_run_at?: string | null
  last_run_status?: SystemJobRunStatus
  /** 最近一次执行耗时（毫秒） */
  last_run_duration_ms?: number
  next_run_at?: string | null
  remark?: string
}

/** 执行日志条目（ScheduledJobRun，jobsapi.go:32-40 字段全量；每次执行记录
 * 触发方式/开始/结束/结果/耗时，失败原因进 message） */
export interface SystemJobRunLogItem {
  id: number
  /** 任务编码（业务关联键，非外键 ID） */
  job_code: string
  /** 触发方式（000014 chk：SCHEDULED/MANUAL/SKIPPED） */
  trigger: string
  start_at: string
  end_at?: string | null
  success: boolean
  /** 耗时（毫秒） */
  duration_ms: number
  /** 执行结果说明 / 失败原因 */
  message?: string
}

// ---------- 系统监控（GET /api/system/monitor，monitorResponse，monitor.go:186-234） ----------

export interface SystemMonitorDatabase {
  max_open_connections: number
  in_use: number
  idle: number
  wait_count: number
  healthy: boolean
}

/** Redis 连通性（enabled=false 时 healthy 置 true 不误报，status 注明未启用） */
export interface SystemMonitorRedis {
  enabled: boolean
  healthy: boolean
  status: string
}

/** API 请求与错误率（近 1h 窗口） */
export interface SystemMonitorApi {
  request_count_1h: number
  error_count_1h: number
  /** 错误率（0~100） */
  error_rate_percent: number
}

/** 定时任务最近状态汇总 */
export interface SystemMonitorJobs {
  total: number
  enabled: number
  failed_last_run: number
}

/** 队列积压统计（asynq 模式注入 Inspector 时下发，inline 省略） */
export interface SystemMonitorQueueStat {
  queue: string
  pending: number
  active: number
  scheduled: number
  retry: number
}

/** 趋势采样点（api_trend 5 分钟聚桶 × 24h，图表数据源全部来自接口，禁止前端造数） */
export interface SystemMonitorTrendPoint {
  /** 采样时间（api.md §2 统一格式） */
  time: string
  requests: number
  errors: number
}

export interface SystemMonitorMetrics {
  database: SystemMonitorDatabase
  redis: SystemMonitorRedis
  api: SystemMonitorApi
  jobs: SystemMonitorJobs
  /** 队列积压（未注入 Inspector 时缺省） */
  queued_tasks?: SystemMonitorQueueStat[]
  /** 进程运行时长（秒） */
  uptime_seconds: number
  /** 应用版本（构建注入或 dev） */
  version: string
  go_version: string
  commit?: string
  api_trend: SystemMonitorTrendPoint[]
  /** 指标口径注记（进程内采样局限、系统资源指标豁免） */
  remarks: string[]
}

// ---------- API ----------

export const systemApi = {
  logs: systemLogApi,
  configs: {
    /** GET /api/system/configs：读全部配置（量级有限不分页，分组渲染） */
    list: () => http.get<SystemConfigItem[]>('/api/system/configs'),
    /** PUT /api/system/configs：批量保存（仅提交变更项），返回 {saved: 保存条数} */
    save: (payload: SystemConfigSavePayload) => http.put<{ saved: number }>('/api/system/configs', payload),
  },
  jobs: {
    /** GET /api/system/jobs：任务列表（分页，与其他列表域格式统一 api.md §2.1） */
    list: (query: SystemJobQuery) =>
      http.get<PageResult<SystemJobItem>>('/api/system/jobs', { params: query }),
    /** PUT /api/system/jobs/{id}/status：启用/停用（{enabled} 必填布尔，热更新生效） */
    setStatus: (id: SystemJobId, payload: { enabled: boolean }) =>
      http.put<SystemJobItem>(`/api/system/jobs/${id}/status`, payload),
    /** GET /api/system/jobs/{id}/run-logs：执行日志分页（触发方式/开始/结束/结果/耗时） */
    runLogs: (id: SystemJobId, query: PageQuery) =>
      http.get<PageResult<SystemJobRunLogItem>>(`/api/system/jobs/${id}/run-logs`, {
        params: query,
      }),
  },
  monitor: {
    /** GET /api/system/monitor：监控指标 + 趋势（deployment.md §5 指标矩阵） */
    metrics: () => http.get<SystemMonitorMetrics>('/api/system/monitor'),
  },
}
