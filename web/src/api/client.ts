import axios, { type AxiosError, type AxiosInstance, type AxiosRequestConfig } from 'axios'
import type { ApiResponse } from '@/types/api'
import { useAuthStore } from '@/stores/auth'

/** 业务/API 错误：携带后端错误码与 request_id（docs/api.md §2.2） */
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
  (error: AxiosError<Partial<ApiResponse>>) => {
    if (error.response) {
      const { status, data } = error.response
      const message = data?.message
      if (status === 401 && !isAuthRequest(error.config?.url)) {
        // 会话失效：清除登录态并回到登录页（permission.md §5）
        useAuthStore.getState().clearSession()
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.href = `/login?redirect=${redirect}`
        return Promise.reject(new ApiError(status, '登录已过期，请重新登录'))
      }
      const fallback =
        status === 403
          ? '没有权限执行此操作'
          : status === 404
            ? '请求的资源不存在'
            : status >= 500
              ? '服务器开小差了，请稍后重试'
              : '请求失败'
      return Promise.reject(new ApiError(status, message ?? fallback, data?.request_id))
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
