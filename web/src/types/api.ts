/** API 统一响应信封（docs/api.md §2.2） */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
  request_id?: string
}

/** 统一分页响应（docs/api.md §2.1） */
export interface PageResult<T> {
  page: number
  pageSize: number
  total: number
  items: T[]
}

/** 统一分页请求参数 */
export interface PageQuery {
  page?: number
  pageSize?: number
}
