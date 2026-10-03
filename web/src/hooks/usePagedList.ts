import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { PageQuery, PageResult } from '@/types/api'

export interface PagedListOptions<T, Q extends PageQuery> {
  /** Query 基础 key，如 ['inventory', 'stock'] */
  queryKey: unknown[]
  fetch: (query: Q) => Promise<PageResult<T>>
  /** 筛选参数（页面受控）；变化时自动回到第 1 页 */
  params: Q
  defaultPageSize?: number
  /** 筛选未就绪时置 false 可暂停请求 */
  enabled?: boolean
  /**
   * §26.3 页面状态保留：传入时把 page/pageSize 持久化到 sessionStorage
   * （键 = sf.pagedList.<persistKey>），进入详情再返回时恢复离开前分页；
   * 筛选 params 仍由页面自行初始化（§26.3 为"尽量保留"，hook 只承诺分页）。
   */
  persistKey?: string
}

/** sessionStorage 命名空间前缀，避免与其他页面状态混写 */
const PERSIST_PREFIX = 'sf.pagedList.'

interface PersistedPagination {
  page: number
  pageSize: number
}

function readPersistedPagination(persistKey: string, defaultPageSize: number): PersistedPagination {
  try {
    const raw = sessionStorage.getItem(PERSIST_PREFIX + persistKey)
    if (!raw) return { page: 1, pageSize: defaultPageSize }
    const parsed = JSON.parse(raw) as Partial<PersistedPagination> | null
    const page =
      typeof parsed?.page === 'number' && Number.isInteger(parsed.page) && parsed.page >= 1
        ? parsed.page
        : 1
    const pageSize =
      typeof parsed?.pageSize === 'number' &&
      Number.isInteger(parsed.pageSize) &&
      parsed.pageSize >= 1
        ? parsed.pageSize
        : defaultPageSize
    return { page, pageSize }
  } catch {
    // 存储不可用 / 内容损坏时降级为默认分页
    return { page: 1, pageSize: defaultPageSize }
  }
}

/**
 * 列表页标准数据流：TanStack Query + 受控分页。
 * 所有列表页统一走这里，保证 Loading/分页/筛选重置行为一致。
 */
export function usePagedList<T, Q extends PageQuery>(options: PagedListOptions<T, Q>) {
  const { queryKey, fetch, params, defaultPageSize = 20, enabled = true, persistKey } = options
  const [pagination, setPagination] = useState<PersistedPagination>(() =>
    persistKey ? readPersistedPagination(persistKey, defaultPageSize) : { page: 1, pageSize: defaultPageSize },
  )

  const writePagination = (next: PersistedPagination) => {
    setPagination(next)
    if (persistKey) {
      try {
        sessionStorage.setItem(PERSIST_PREFIX + persistKey, JSON.stringify(next))
      } catch {
        // 存储不可用时降级为会话内状态
      }
    }
  }

  const query = useQuery({
    queryKey: [...queryKey, pagination, params],
    queryFn: () => fetch({ ...params, ...pagination }),
    enabled,
    placeholderData: keepPreviousData,
  })

  const tablePagination = useMemo(
    () => ({
      current: pagination.page,
      pageSize: pagination.pageSize,
    }),
    [pagination],
  )

  return {
    items: query.data?.items ?? [],
    total: query.data?.total ?? 0,
    isPending: query.isPending,
    isFetching: query.isFetching,
    error: query.error,
    refetch: query.refetch,
    pagination: tablePagination,
    onPageChange: (page: number, pageSize: number) => writePagination({ page, pageSize }),
    resetToFirstPage: () => writePagination({ page: 1, pageSize: pagination.pageSize }),
  }
}
