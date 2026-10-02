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
}

/**
 * 列表页标准数据流：TanStack Query + 受控分页。
 * 所有列表页统一走这里，保证 Loading/分页/筛选重置行为一致。
 */
export function usePagedList<T, Q extends PageQuery>(options: PagedListOptions<T, Q>) {
  const { queryKey, fetch, params, defaultPageSize = 20, enabled = true } = options
  const [pagination, setPagination] = useState({ page: 1, pageSize: defaultPageSize })

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
    onPageChange: (page: number, pageSize: number) => setPagination({ page, pageSize }),
    resetToFirstPage: () => setPagination((p) => ({ ...p, page: 1 })),
  }
}
