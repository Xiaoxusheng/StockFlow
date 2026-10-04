import axios, {
  type AxiosError,
  type AxiosInstance,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios'
import type { ApiResponse } from '@/types/api'
import { useAuthStore } from '@/stores/auth'
import type { LoginResult } from './auth'

/** 业务/API 错误：code 为后端字符串错误码（AUTH_/COMMON_ 前缀等）或 HTTP 状态数字（api.md §2.2） */
export class ApiError extends Error {
  readonly code: number | string
  readonly requestId?: string
  /** 失败信封 details（校验失败必带，api.md §4；bind 失败形态见 extractFieldErrors） */
  readonly details?: unknown

  constructor(code: number | string, message: string, requestId?: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.requestId = requestId
    this.details = details
  }
}

/** 网络层错误（无法连接 / 超时） */
export const NETWORK_ERROR_CODE = -1

/** 后端失败信封：失败时 code 为模块命名空间字符串错误码（internal/response/errors.go 包注释） */
interface ErrorEnvelope {
  code?: number | string
  message?: string
  request_id?: string
  details?: unknown
}

/** 首登强制改密门禁错误码（internal/auth/errors.go ErrPasswordChangeRequired，HTTP 403） */
const PASSWORD_CHANGE_REQUIRED = 'AUTH_PASSWORD_CHANGE_REQUIRED'

/** 登录/刷新请求自身失败不触发静默续期（防递归） */
function isAuthSessionRequest(url: string | undefined): boolean {
  return !!url && (url.includes('/auth/login') || url.includes('/auth/refresh'))
}

// —— 401 静默续期（单飞）——
// 后端 access TTL 2h / refresh TTL 7d，refresh_token 已持久化（stores/auth.ts StoredSession）：
// 401 时先经 POST /api/auth/refresh 换新令牌并重放原请求，刷新失败才清会话跳登录，
// 消除用户每 2 小时强制重登。并发 401 共享同一次刷新（refreshInFlight 单飞）。
let refreshInFlight: Promise<boolean> | null = null

/**
 * 用 refresh_token 换新令牌并落库。后端刷新会轮换 refresh_token
 * （internal/auth/service_auth.go Refresh → store.Rotate），新令牌必须回存。
 * 经统一 http 入口直打 /api/auth/refresh：isAuthSessionRequest 已涵盖该路径，刷新自身
 * 401 不会进入续期分支（无递归）；请求拦截器附带的失效 Bearer 会被公开路由忽略。
 */
async function performRefresh(): Promise<boolean> {
  const refreshToken = useAuthStore.getState().refreshToken
  if (!refreshToken) return false
  try {
    // 经统一 http 入口（拦截器解包信封后返回业务数据 T；仍走同一 client 实例，
    // isAuthSessionRequest 已涵盖该路径，刷新自身 401 不会进入续期分支——无递归）
    const data = await http.post<LoginResult>('/api/auth/refresh', {
      refresh_token: refreshToken,
    })
    if (!data?.access_token) return false
    const cur = useAuthStore.getState()
    // LoginResult 不含 permissions 权限点集：保留既有快照避免置空（置空会触发
    // PcLayout 会话自愈回拉 /me 多一跳）；user/must_change_password 以后端刷新结果为准
    cur.setSession({
      token: data.access_token,
      refreshToken: data.refresh_token,
      user: data.user ?? cur.user,
      permissions: cur.permissions,
      isSuper: cur.isSuper,
      mustChangePassword: data.must_change_password,
    })
    return true
  } catch {
    return false
  }
}

/** 单飞刷新：进行中则复用同一 Promise，结束（含失败）即清空，下次 401 重新刷新 */
function refreshSession(): Promise<boolean> {
  refreshInFlight ??= performRefresh().finally(() => {
    refreshInFlight = null
  })
  return refreshInFlight
}

const client: AxiosInstance = axios.create({
  // 生产环境由同源服务提供 API；开发环境走 Vite 代理
  baseURL: import.meta.env.VITE_API_BASE_URL ?? '',
  timeout: 15_000,
})

client.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

/** 原请求重放标记：重放后再次 401 不再续期，直接走清会话跳登录（防死循环） */
type RetriableConfig = InternalAxiosRequestConfig & { _sfRetried?: boolean }

client.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResponse | undefined
    // 非信封响应（文件流等）原样放行
    if (!body || typeof body !== 'object' || typeof body.code !== 'number') {
      return response
    }
    if (body.code !== 0) {
      throw new ApiError(body.code, body.message || '请求失败', body.request_id, body.details)
    }
    // 信封解包：调用方直接拿到 data
    response.data = body.data
    return response
  },
  async (error: AxiosError<ErrorEnvelope>) => {
    if (error.response) {
      const { status, data } = error.response
      const message = data?.message
      // 失败信封 code 为字符串错误码；非信封（如网关响应）退回 HTTP 状态数字
      const code = typeof data?.code === 'string' ? data.code : status
      if (status === 401 && !isAuthSessionRequest(error.config?.url)) {
        // 先试静默续期并重放原请求（_sfRetried 防重放后再次 401 死循环）；
        // 刷新失败（refresh_token 失效/被踢/网络异常）才清会话回登录页（permission.md §5）
        const config = error.config as RetriableConfig | undefined
        if (config && !config._sfRetried && (await refreshSession())) {
          config._sfRetried = true
          const token = useAuthStore.getState().token
          if (token) config.headers.Authorization = `Bearer ${token}`
          return client.request(config)
        }
        useAuthStore.getState().clearSession()
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.href = `/login?redirect=${redirect}`
        return Promise.reject(new ApiError(code, '登录已过期，请重新登录', data?.request_id))
      }
      if (status === 403 && data?.code === PASSWORD_CHANGE_REQUIRED) {
        // 首登强制改密门禁（internal/auth/middleware.go passwordGateAllowed，
        // 白名单仅放行改密/登出/me）：不走通用「没有权限」提示，置位标志后由
        // PcLayout 引导强制改密 UI
        useAuthStore.getState().setMustChangePassword(true)
        return Promise.reject(
          new ApiError(code, message ?? '必须先修改初始密码', data?.request_id, data?.details),
        )
      }
      const fallback =
        status === 403
          ? '没有权限执行此操作'
          : status === 404
            ? '请求的资源不存在'
            : status >= 500
              ? '服务器开小差了，请稍后重试'
              : '请求失败'
      return Promise.reject(new ApiError(code, message ?? fallback, data?.request_id, data?.details))
    }
    if (error.code === 'ECONNABORTED') {
      return Promise.reject(new ApiError(NETWORK_ERROR_CODE, '请求超时，请检查网络后重试'))
    }
    return Promise.reject(
      new ApiError(NETWORK_ERROR_CODE, '无法连接服务器，请检查网络或后端服务是否可用'),
    )
  },
)

/** 统一请求入口：拦截器已解包信封，返回业务数据 T */
export const http = {
  async get<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
    return (await client.get<T>(url, config)).data
  },
  async post<T>(url: string, body?: unknown, config?: AxiosRequestConfig): Promise<T> {
    return (await client.post<T>(url, body, config)).data
  },
  async put<T>(url: string, body?: unknown, config?: AxiosRequestConfig): Promise<T> {
    return (await client.put<T>(url, body, config)).data
  },
  async patch<T>(url: string, body?: unknown, config?: AxiosRequestConfig): Promise<T> {
    return (await client.patch<T>(url, body, config)).data
  },
  async delete<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
    return (await client.delete<T>(url, config)).data
  },
}

/**
 * 提取 bind 校验失败的 fields 错误映射（internal/response/errors.go BindErrorDetails：
 * {reason, fields}）。注意 fields 键为 Go 结构体字段名的小写形式——validator 未注册
 * json tag 名，如 NewPassword → "newpassword"，与前端 snake_case 不一致，
 * 表单层请经 matchFieldErrors 归一匹配。无字段级信息返回 null。
 */
export function extractFieldErrors(error: unknown): Record<string, string> | null {
  if (!(error instanceof ApiError)) return null
  if (typeof error.details !== 'object' || error.details === null) return null
  const fields = (error.details as { fields?: unknown }).fields
  if (typeof fields !== 'object' || fields === null) return null
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(fields as Record<string, unknown>)) {
    if (typeof value === 'string') out[key] = value
  }
  return Object.keys(out).length > 0 ? out : null
}

/**
 * 把 details.fields 映射到指定表单字段名：后端键（Go 字段名小写，如 "newpassword"）
 * 与前端 snake_case（"new_password"）经归一（去下划线 + 小写）后匹配。
 * 命中至少一个字段返回 {表单字段名: 文案}；否则 null（调用方退回整体错误文案，
 * 避免把「请求体格式错误」固定文案挂在无字段的错误上）。
 */
export function matchFieldErrors(
  error: unknown,
  fieldNames: readonly string[],
): Record<string, string> | null {
  const raw = extractFieldErrors(error)
  if (!raw) return null
  const normalize = (s: string) => s.replace(/_/g, '').toLowerCase()
  const byNormalized = new Map(Object.entries(raw).map(([k, v]) => [normalize(k), v]))
  const out: Record<string, string> = {}
  for (const name of fieldNames) {
    const msg = byNormalized.get(normalize(name))
    if (msg) out[name] = msg
  }
  return Object.keys(out).length > 0 ? out : null
}

/** 从任意错误中提取用户可读信息（frontend.md §9：错误必须可理解） */
export function resolveErrorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message
  if (error instanceof Error) return error.message
  return '发生未知错误'
}
