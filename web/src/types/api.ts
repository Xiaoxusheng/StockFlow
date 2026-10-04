/** API 统一响应信封（docs/api.md §2.2）。details 仅失败信封携带（omitempty）：
 * 校验失败必带（api.md §4，internal/response/errors.go envelope.Details），
 * bind 失败形态 {reason, fields}（api/client.ts extractFieldErrors 消费），
 * 域错误可为任意结构化补充（如 AUTH_PASSWORD_WEAK 的 {min_length,max_length}） */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
  request_id?: string
  details?: unknown
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
