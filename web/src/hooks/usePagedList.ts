import { useCallback, useMemo, useState } from 'react'
import { keepPreviousData, useQuery, type UseQueryOptions } from '@tanstack/react-query'
import { useSearchParams } from 'react-router'
import type { PageQuery, PageResult } from '@/types/api'

export interface PagedListOptions<T, Q extends PageQuery> {
  /** Query 基础 key，如 ['inventory', 'stock'] */
  queryKey: unknown[]
  fetch: (query: Q) => Promise<PageResult<T>>
  /**
   * 筛选参数（页面受控）；变化时自动回到第 1 页。
   * 开启 `urlSync` 后本字段可省略——筛选改由 URL 承载，见 urlSync 说明。
   */
  params?: Q
  defaultPageSize?: number
  /** 筛选未就绪时置 false 可暂停请求 */
  enabled?: boolean
  /**
   * §26.3 页面状态保留：传入时把 page/pageSize 持久化到 sessionStorage
   * （键 = sf.pagedList.<persistKey>），进入详情再返回时恢复离开前分页；
   * 筛选 params 仍由页面自行初始化（§26.3 为"尽量保留"，hook 只承诺分页）。
   * 开启 `urlSync` 时本字段失效——URL 已完整承载筛选与分页，无需再存一份。
   */
  persistKey?: string
  /**
   * 把筛选条件与分页同步到 URL query（刷新 / 分享链接 / 前进后退均可还原）。
   *
   * 开启后 **params 由 hook 接管**，页面写法相应调整：
   *   1. 删掉 `const [params, setParams] = useState<Q>({})`；
   *   2. `usePagedList` 配置里去掉 `params`、加上 `urlSync: true`；
   *   3. 查询表单 `onSearch={list.applyFilters}`（等价于「应用筛选并回到第 1 页」）；
   *   4. 查询表单 `initialValues={list.params}`，使刷新后输入框仍回显当前条件。
   *
   * 类型说明：URL 里筛选值一律是字符串——SfSearchForm 的 input/select 产出本就是
   * string（SearchField.options.value: string），故往返无类型损失；分页另存
   * `page` / `pageSize` 两个保留键（缺省值不写入，保持 URL 干净）。
   */
  urlSync?: boolean
  /**
   * URL 原始值 → 业务类型的补偿转换。
   *
   * URL query 里一律是字符串，而 Query 类型可能声明 boolean / number 字段
   * （如 `online?: boolean`、`warehouse_id?: number`）。不提供本函数时 hook 直接按 Q
   * 复用字符串值——仅适用于筛选值全为 string 的页面；含类型转换的页面**必须**提供，
   * 否则 `'true'` 会被当作真值字符串传给后端、`'123'` 也不会变成 number。
   *
   * 转换函数应是模块级或 useCallback 包裹的稳定引用（会进 useMemo 依赖）。
   * 页面可复用已有的 onSearch 转换逻辑（如 DeviceListPage 的 toDeviceQuery）。
   */
  decodeParams?: (raw: Record<string, string>) => Q
  /**
   * 自动刷新轮询间隔（任务页自动刷新，计划 §2.11）：直传内部 useQuery 的 refetchInterval，
   * 函数形态由 `hooks/useAutoRefresh` 产出（含 document.hidden 暂停与连续失败退避停轮）。
   * 缺省不轮询——既有页面零行为变化（additive）。
   */
  refetchInterval?: UseQueryOptions<PageResult<T>>['refetchInterval']
}

/** sessionStorage 命名空间前缀，避免与其他页面状态混写 */
const PERSIST_PREFIX = 'sf.pagedList.'

/** URL 保留键：分页参数（筛选字段不占用这两个名字） */
const PAGE_PARAM = 'page'
const PAGE_SIZE_PARAM = 'pageSize'

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

/** URL query → 筛选对象（剔除分页保留键；值原样为字符串） */
function readUrlParams(searchParams: URLSearchParams): Record<string, string> {
  const raw: Record<string, string> = {}
  searchParams.forEach((value, key) => {
    if (key === PAGE_PARAM || key === PAGE_SIZE_PARAM) return
    raw[key] = value
  })
  return raw
}

/** URL query → 分页（非法值回落默认，防止手改 URL 打出越界请求） */
function readUrlPagination(
  searchParams: URLSearchParams,
  defaultPageSize: number,
): PersistedPagination {
  const page = Number(searchParams.get(PAGE_PARAM) ?? '')
  const pageSize = Number(searchParams.get(PAGE_SIZE_PARAM) ?? '')
  return {
    page: Number.isInteger(page) && page >= 1 ? page : 1,
    pageSize: Number.isInteger(pageSize) && pageSize >= 1 ? pageSize : defaultPageSize,
  }
}

/**
 * 列表页标准数据流：TanStack Query + 受控分页。
 * 所有列表页统一走这里，保证 Loading/分页/筛选重置行为一致。
 *
 * 状态承载有两种模式（二选一）：
 * - 默认（页面自持）：页面用 `useState<Q>` 管 params 并传入；分页可经 persistKey 存 sessionStorage。
 * - `urlSync`（URL 自持）：筛选与分页都落 URL query，刷新/分享/前进后退可还原，见 PagedListOptions.urlSync。
 */
export function usePagedList<T, Q extends PageQuery>(options: PagedListOptions<T, Q>) {
  const {
    queryKey,
    fetch,
    params: controlledParams,
    defaultPageSize = 20,
    enabled = true,
    persistKey,
    urlSync = false,
    decodeParams,
    refetchInterval,
  } = options

  const [searchParams, setSearchParams] = useSearchParams()

  // —— 分页状态（URL 模式下由 URL 派生，不再用本地 state）——
  const [statePagination, setStatePagination] = useState<PersistedPagination>(() =>
    persistKey && !urlSync
      ? readPersistedPagination(persistKey, defaultPageSize)
      : { page: 1, pageSize: defaultPageSize },
  )

  const urlPagination = useMemo(
    () => (urlSync ? readUrlPagination(searchParams, defaultPageSize) : null),
    [urlSync, searchParams, defaultPageSize],
  )
  const rawUrlParams = useMemo(
    () => (urlSync ? readUrlParams(searchParams) : null),
    [urlSync, searchParams],
  )
  const urlParams = useMemo(() => {
    if (!rawUrlParams) return null
    if (decodeParams) return decodeParams(rawUrlParams)
    // 未提供 decodeParams：视为筛选值全为 string，URL 值与业务值同构直接复用。
    // 经 unknown 中转是因为泛型 Q 可能声明 number/boolean 字段，静态无法证明重叠。
    return rawUrlParams as unknown as Q
  }, [rawUrlParams, decodeParams])

  const pagination = urlPagination ?? statePagination
  const params = urlParams ?? controlledParams ?? ({} as Q)

  /** URL 模式写入：筛选与分页一并落 query（replace 不污染浏览器历史，翻页不留一长串记录） */
  const writeUrl = useCallback(
    (nextParams: Q, nextPagination: PersistedPagination) => {
      const next = new URLSearchParams()
      for (const [key, value] of Object.entries(nextParams as Record<string, unknown>)) {
        // 空值不写入：避免 ?keyword=&status= 这类噪音，也让"重置"真正清空 URL
        if (value === undefined || value === null || value === '') continue
        next.set(key, String(value))
      }
      // 缺省分页不写入，保持 URL 可读
      if (nextPagination.page > 1) next.set(PAGE_PARAM, String(nextPagination.page))
      if (nextPagination.pageSize !== defaultPageSize) {
        next.set(PAGE_SIZE_PARAM, String(nextPagination.pageSize))
      }
      setSearchParams(next, { replace: true })
    },
    [defaultPageSize, setSearchParams],
  )

  const writePagination = useCallback(
    (next: PersistedPagination) => {
      if (urlSync) {
        writeUrl(params, next)
        return
      }
      setStatePagination(next)
      if (persistKey) {
        try {
          sessionStorage.setItem(PERSIST_PREFIX + persistKey, JSON.stringify(next))
        } catch {
          // 存储不可用时降级为会话内状态
        }
      }
    },
    [urlSync, writeUrl, params, persistKey],
  )

  /**
   * 应用筛选并回到第 1 页——供 SfSearchForm 的 onSearch 直传。
   * 回到第 1 页是必须的：沿用旧页码在新筛选下常常越界（第 3 页在新条件下可能只有 1 页）。
   */
  const applyFilters = useCallback(
    (values: Record<string, unknown>) => {
      writeUrl(values as Q, { page: 1, pageSize: pagination.pageSize })
    },
    [writeUrl, pagination.pageSize],
  )

  const query = useQuery({
    queryKey: [...queryKey, pagination, params],
    queryFn: () => fetch({ ...params, ...pagination }),
    enabled,
    placeholderData: keepPreviousData,
    // 自动刷新（§2.11）：缺省 undefined = 不轮询（既有行为不变）
    refetchInterval,
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
    /** 当前生效的筛选（已按 decodeParams 解码，供 fetch 与页面逻辑使用） */
    params,
    /**
     * URL 原始字符串形态的筛选，供 SfSearchForm 的 initialValues 回填。
     * 提供 decodeParams 的页面**必须**用它而不是 params：表单控件的值是字符串
     * （如 online 的 'true'/'false'），拿解码后的 boolean 去回填匹配不上选项。
     * 未提供 decodeParams 时与 params 内容一致。
     */
    formValues: (rawUrlParams ?? {}) as Record<string, string>,
    pagination: tablePagination,
    onPageChange: (page: number, pageSize: number) => writePagination({ page, pageSize }),
    /** 应用筛选并回到第 1 页（SfSearchForm onSearch 直传） */
    applyFilters,
    resetToFirstPage: () => writePagination({ page: 1, pageSize: pagination.pageSize }),
  }
}
