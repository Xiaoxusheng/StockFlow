import axios, { type AxiosError, type AxiosInstance, type AxiosRequestConfig } from 'axios'
import type { ApiResponse } from '@/types/api'
import { useAuthStore } from '@/stores/auth'

/** 业务/API 错误：code 为后端字符串错误码（AUTH_/COMMON_ 前缀等）或 HTTP 状态数字（api.md §2.2） */
export class ApiError extends Error {
  readonly code: number | string
  readonly requestId?: string

  constructor(code: number | string, message: string, requestId?: string) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.requestId = requestId
  }
}

/** 网络层错误（无法连接 / 超时） */
export const NETWORK_ERROR_CODE = -1

/** 后端失败信封：失败时 code 为模块命名空间字符串错误码（internal/response/errors.go 包注释） */
interface ErrorEnvelope {
  code?: number | string
  message?: string
  request_id?: string
}

/** 首登强制改密门禁错误码（internal/auth/errors.go:21，HTTP 403） */
const PASSWORD_CHANGE_REQUIRED = 'AUTH_PASSWORD_CHANGE_REQUIRED'

function isAuthRequest(url: string | undefined): boolean {
  return !!url && url.includes('/auth/login')
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

client.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResponse | undefined
    // 非信封响应（文件流等）原样放行
    if (!body || typeof body !== 'object' || typeof body.code !== 'number') {
      return response
    }
    if (body.code !== 0) {
      throw new ApiError(body.code, body.message || '请求失败', body.request_id)
    }
    // 信封解包：调用方直接拿到 data
    response.data = body.data
    return response
  },
  (error: AxiosError<ErrorEnvelope>) => {
    if (error.response) {
      const { status, data } = error.response
      const message = data?.message
      // 失败信封 code 为字符串错误码；非信封（如网关响应）退回 HTTP 状态数字
      const code = typeof data?.code === 'string' ? data.code : status
      if (status === 401 && !isAuthRequest(error.config?.url)) {
        // 会话失效：清除登录态并回到登录页（permission.md §5）
        useAuthStore.getState().clearSession()
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.href = `/login?redirect=${redirect}`
        return Promise.reject(new ApiError(code, '登录已过期，请重新登录'))
      }
      if (status === 403 && data?.code === PASSWORD_CHANGE_REQUIRED) {
        // 首登强制改密门禁（internal/auth/middleware.go:74-78，白名单仅放行改密/登出/me）：
        // 不走通用「没有权限」提示，置位标志后由 PcLayout 引导强制改密 UI
        useAuthStore.getState().setMustChangePassword(true)
        return Promise.reject(
          new ApiError(code, message ?? '必须先修改初始密码', data?.request_id),
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
      return Promise.reject(new ApiError(code, message ?? fallback, data?.request_id))
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

/** 从任意错误中提取用户可读信息（frontend.md §9：错误必须可理解） */
export function resolveErrorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message
  if (error instanceof Error) return error.message
  return '发生未知错误'
}
